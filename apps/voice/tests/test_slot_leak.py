"""BUG 1 (voice-concurrency-slot-leak): the heartbeat lease must be released
on session teardown for EVERY way a session can end — including an abrupt
client vanish (tab-close / ICE-close / retry-storm churn), where the
underlying SmallWebRTCConnection fires its own ``closed`` event but the
higher-level pipeline never cleanly finalizes.

Production evidence: a dropped session's lease lingered at
``expiresAt = last_renewal + heartbeat_ttl`` (full 45s TTL, NOT an immediate
``now-1`` expiry), proving ``SessionLifecycle.release()`` ->
``quota.release_heartbeat()`` never ran on the abrupt path. The concurrency
gate (``count_active_heartbeats``: ``expiresAt > now``) then kept counting the
dead lease for up to 45s, walling reconnects with ``concurrency-limit`` 403.

The clean ``stop()`` path already releases correctly (proven green below).
The gap is that nothing wires the raw connection's ``closed`` teardown signal
to the lifecycle — so the abrupt path never reaches ``release()``.

Same test harness as test_teardown.py / test_session.py: a real
(tiny-interval) asyncio loop, pinned at dynamodb-local via the autouse
fixture so the real (unstubbed) ``quota.release_heartbeat()`` inside
``release()`` hits the local table, never real AWS.
"""

from __future__ import annotations

import asyncio
import uuid

import boto3
import pytest

import server
from klanker_voice import quota, session
from klanker_voice.config import QuotaConfig
from pipecat.utils.base_object import BaseObject

DYNAMODB_LOCAL_ENDPOINT = "http://localhost:8888"


def _dynamodb_local_available() -> bool:
    try:
        boto3.client(
            "dynamodb",
            endpoint_url=DYNAMODB_LOCAL_ENDPOINT,
            region_name="us-east-1",
            aws_access_key_id="local",
            aws_secret_access_key="local",
        ).list_tables()
        return True
    except Exception:
        return False


pytestmark = pytest.mark.skipif(
    not _dynamodb_local_available(), reason="dynamodb-local not reachable on localhost:8888"
)


class _FakeAwsClient:
    def __init__(self):
        self.calls: list[tuple[str, dict]] = []

    def __getattr__(self, name):
        def _record(**kwargs):
            self.calls.append((name, kwargs))
            return {}

        return _record


class _FakeWebRTCConnection(BaseObject):
    """A minimal stand-in for ``SmallWebRTCConnection`` that fires the real
    pipecat ``closed`` event through the genuine ``BaseObject`` dispatch
    machinery — so anything server.py registers via
    ``connection.event_handler("closed")`` runs exactly as it would against a
    live aiortc peer, without needing a browser/SDP negotiation."""

    def __init__(self, pc_id: str = "pc-test"):
        super().__init__()
        self.pc_id = pc_id
        # Real SmallWebRTCConnection registers these; mirror it so the
        # event_handler decorator attaches rather than warning + dropping.
        self._register_event_handler("closed")
        self._register_event_handler("disconnected")

    async def fire_closed(self) -> None:
        """Simulate aiortc tearing the peer connection down (abrupt ICE-close
        / tab-close). Awaits the dispatched handler tasks so the test is
        deterministic."""
        await self._call_event_handler("closed")
        # Async handlers are dispatched as tasks; let them run to completion.
        pending = [task for (_name, task) in list(self._event_tasks)]
        if pending:
            await asyncio.gather(*pending, return_exceptions=True)


@pytest.fixture(autouse=True)
def local_dynamodb_env(monkeypatch):
    monkeypatch.setenv(quota.DYNAMODB_ENDPOINT_ENV_VAR, DYNAMODB_LOCAL_ENDPOINT)
    monkeypatch.setenv("AWS_ACCESS_KEY_ID", "local")
    monkeypatch.setenv("AWS_SECRET_ACCESS_KEY", "local")
    monkeypatch.setenv("AWS_DEFAULT_REGION", "us-east-1")
    monkeypatch.setenv(quota.USAGE_TABLE_ENV_VAR, "kmv-voice-usage")
    monkeypatch.setenv(quota.TIERS_TABLE_ENV_VAR, "kmv-auth-electro")


@pytest.fixture(autouse=True)
def reset_active_count():
    session._active_session_count = 0
    yield
    session._active_session_count = 0


@pytest.fixture
def fake_aws(monkeypatch):
    clients: dict[str, _FakeAwsClient] = {}

    def _client(name, *args, **kwargs):
        clients.setdefault(name, _FakeAwsClient())
        return clients[name]

    monkeypatch.setattr(session.boto3, "client", _client)
    monkeypatch.setattr(session, "_task_metadata_ids", lambda: ("test-cluster", "test-task-123"))
    return clients


@pytest.fixture(autouse=True)
def fake_record_tick(monkeypatch):
    monkeypatch.setattr(quota, "record_tick", lambda **kw: quota.TickResult(False, False))


def _quota_config(**overrides) -> QuotaConfig:
    base = dict(
        heartbeat_renew_interval=0.02,
        heartbeat_ttl=5.0,
        sub_floor_seconds=1,
        per_task_max_sessions=5,
        auto_trip_ceiling_seconds=100_000,
        auto_trip_ceiling_dollars=100_000.0,
        est_cost_per_second=0.01,
        winddown_warning_seconds=100,
        goodbye_grace_seconds=1,
        user_silence_timeout=100,  # keep the silence watchdog well clear
        reconnect_grace_seconds=0.03,
    )
    base.update(overrides)
    return QuotaConfig(**base)


def _tier(*, session_max=100, period_max=600, max_concurrent=2) -> quota.Tier:
    return quota.Tier(
        tier_id="t",
        session_max_seconds=session_max,
        period_max_seconds=period_max,
        max_concurrent=max_concurrent,
    )


def _ids() -> tuple[str, str]:
    return f"test-user-{uuid.uuid4().hex[:12]}", f"test-session-{uuid.uuid4().hex[:12]}"


def _lease_item(user_id: str, session_id: str):
    return quota._usage_table().get_item(
        Key={"pk": quota._heartbeat_pk(user_id), "sk": quota._heartbeat_sk(session_id)}
    ).get("Item")


# --- BUG 1 primary reproduction: abrupt connection close must free the slot ---


async def test_abrupt_connection_close_releases_heartbeat_lease(fake_aws):
    """RED: on an abrupt SmallWebRTCConnection ``closed`` (tab-close /
    ICE-close), the session's heartbeat lease must be released within the
    reconnect grace. Today nothing wires the connection's own teardown to the
    lifecycle, so the lease lingers at full TTL and walls reconnects."""
    user_id, session_id = _ids()
    quota.acquire_heartbeat(user_id, session_id, ttl_seconds=45)
    assert quota.count_active_heartbeats(user_id) == 1

    lifecycle = session.SessionLifecycle(
        user_id=user_id,
        session_id=session_id,
        tier=_tier(),
        quota_config=_quota_config(reconnect_grace_seconds=0.03),
    )
    await lifecycle.start()

    conn = _FakeWebRTCConnection()
    # The fix seam: wire the raw connection's teardown to the lifecycle, at
    # connection-creation time, independent of the transport handler. Until
    # the fix lands this attribute does not exist AT ALL — the absence of any
    # connection-level teardown wiring IS the bug, so we let the abrupt close
    # proceed unwired and assert the observable leak (lease stays live), the
    # exact production symptom. A no-op fix would also fail these assertions.
    wire = getattr(server, "_wire_connection_teardown", None)
    if wire is not None:
        wire(conn, lifecycle)

    await conn.fire_closed()
    await asyncio.sleep(0.1)  # let the reconnect grace elapse

    assert session.active_session_count() == 0
    assert quota.count_active_heartbeats(user_id) == 0

    # And the lease row itself is logically expired (released), not lingering
    # at the full 45s TTL the production leak exhibited.
    item = _lease_item(user_id, session_id)
    assert item is not None
    assert int(item["expiresAt"]) <= quota._now_epoch()


# --- regression lock: the clean stop() path already releases the lease ---


async def test_clean_stop_releases_heartbeat_lease(fake_aws):
    """GREEN today (regression lock at the LEASE level, which no existing
    test asserts): when the pipeline finalizes cleanly and server.py's
    ``finally: lifecycle.stop()`` runs, the heartbeat lease is released."""
    user_id, session_id = _ids()
    quota.acquire_heartbeat(user_id, session_id, ttl_seconds=45)
    assert quota.count_active_heartbeats(user_id) == 1

    lifecycle = session.SessionLifecycle(
        user_id=user_id, session_id=session_id, tier=_tier(), quota_config=_quota_config()
    )
    await lifecycle.start()
    await lifecycle.stop()  # server.py's finally-block name -> release()

    assert session.active_session_count() == 0
    assert quota.count_active_heartbeats(user_id) == 0

    item = _lease_item(user_id, session_id)
    assert item is not None
    assert int(item["expiresAt"]) <= quota._now_epoch()
