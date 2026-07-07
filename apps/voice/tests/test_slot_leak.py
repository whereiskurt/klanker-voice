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


# --- BUG 1 primary reproduction: a *terminal* connection close must free the
# slot IMMEDIATELY, not after the 12s reconnect grace ---
#
# Refinement (rev :13 live finding): the raw ``SmallWebRTCConnection`` ``closed``
# event is *terminal* — it fires only once aiortc has actually ``pc.close()``d
# the peer (graceful disconnect, ICE ``failed`` self-close, or the connecting
# timeout). A tab-close / RELOAD is terminal: the client returns with a
# brand-new ``session_id``, so routing this close through the D-07 reconnect
# grace waits 12s for a same-session reconnect that can never come. Two reloads
# inside 12s stack two draining slots and wall the third at max_concurrent=2.
# So the ``closed`` fast-path must call ``release()`` synchronously; the 12s
# grace stays reserved for the transport-level ``on_transport_disconnected``
# path (see the transient contrast test below and test_teardown.py).


async def test_abrupt_connection_close_releases_heartbeat_lease_immediately(fake_aws):
    """A terminal ``SmallWebRTCConnection`` ``closed`` (tab-close / RELOAD /
    ICE-close) must release the heartbeat lease SYNCHRONOUSLY on the event —
    NOT after the reconnect grace.

    RED against the rev :13 behavior (``closed`` routed through
    ``on_transport_disconnected`` -> the 12s reconnect grace): with a large
    grace and no sleep, the slot would still be held right after the close.
    GREEN once ``_wire_connection_teardown`` calls ``release()`` directly.
    """
    user_id, session_id = _ids()
    quota.acquire_heartbeat(user_id, session_id, ttl_seconds=45)
    assert quota.count_active_heartbeats(user_id) == 1

    lifecycle = session.SessionLifecycle(
        user_id=user_id,
        session_id=session_id,
        tier=_tier(),
        # A deliberately LONG grace: if the terminal close still routed through
        # the reconnect grace, the slot would remain held for 30s and these
        # no-sleep assertions would fail — that is the rev :13 leak this
        # refinement removes.
        quota_config=_quota_config(reconnect_grace_seconds=30),
    )
    await lifecycle.start()

    conn = _FakeWebRTCConnection()
    server._wire_connection_teardown(conn, lifecycle)

    await conn.fire_closed()
    # NO grace sleep: a terminal close releases synchronously on the event.
    assert session.active_session_count() == 0
    assert quota.count_active_heartbeats(user_id) == 0

    # And the lease row itself is logically expired (released), not lingering
    # at the full 45s TTL the production leak exhibited.
    item = _lease_item(user_id, session_id)
    assert item is not None
    assert int(item["expiresAt"]) <= quota._now_epoch()


async def test_transport_disconnect_still_uses_the_reconnect_grace(fake_aws):
    """Contrast lock: the TRANSIENT path is unchanged. A transport-level
    ``on_transport_disconnected`` (an ICE blip that may recover via
    ``on_transport_reconnected``) must STILL hold the slot through the
    reconnect grace — the refinement makes only the terminal raw-connection
    ``closed`` event immediate, it must not weaken transient-reconnect
    semantics."""
    user_id, session_id = _ids()
    quota.acquire_heartbeat(user_id, session_id, ttl_seconds=45)

    lifecycle = session.SessionLifecycle(
        user_id=user_id,
        session_id=session_id,
        tier=_tier(),
        quota_config=_quota_config(reconnect_grace_seconds=0.05, user_silence_timeout=100),
    )
    await lifecycle.start()

    await lifecycle.on_transport_disconnected()
    # Still within the grace window -> slot deliberately held for reconnect.
    assert session.active_session_count() == 1
    assert quota.count_active_heartbeats(user_id) == 1

    await asyncio.sleep(0.12)  # let the grace elapse
    assert session.active_session_count() == 0
    assert quota.count_active_heartbeats(user_id) == 0


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
