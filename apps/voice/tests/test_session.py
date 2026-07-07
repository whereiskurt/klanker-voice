"""SessionLifecycle: service timer, 15s tick (persist/renew/rollup/auto-trip),
hard-stop, ActiveSessions metric, ECS scale-in protection (QUOT-02, D-02,
D-10, D-13, INFR-06).

Uses a real `asyncio` event loop with tiny (sub-100ms) intervals rather than
mocking `asyncio.sleep` — the timing behavior itself (does the tick fire
repeatedly, does the timer fire at the right offset) is what's under test.
AWS calls (CloudWatch, ECS) are always faked via a recording stub so no test
here ever makes a real network call, matching test_webrtc.py's precedent.
`quota.record_tick` is faked for the lifecycle-mechanics tests (isolating
SessionLifecycle's own logic) and real (dynamodb-local) for the one
auto-trip integration test, which is what actually proves the control item
gets flipped. Every test in this module — including the "stubbed" ones — is
pinned at dynamodb-local via an autouse fixture: `SessionLifecycle.stop()`
always calls the real, unstubbed `quota.release_heartbeat()` regardless of
whether the tick is faked, and this dev environment carries live AWS
credentials — that call must never reach the real `kmv-voice-usage` table.
"""

from __future__ import annotations

import asyncio
import uuid

import boto3
import pytest

from klanker_voice import quota, session
from klanker_voice.config import QuotaConfig

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
    """Records every call made to it; every method just returns None."""

    def __init__(self):
        self.calls: list[tuple[str, dict]] = []

    def __getattr__(self, name):
        def _record(**kwargs):
            self.calls.append((name, kwargs))
            return {}

        return _record


@pytest.fixture(autouse=True)
def local_dynamodb_env(monkeypatch):
    """Every real (unstubbed) quota.* call in this file — most notably
    ``release_heartbeat`` inside ``SessionLifecycle.stop()``, which is never
    stubbed even in the "fake tick" tests — must hit dynamodb-local, never
    real AWS. This dev environment carries live AWS SSO credentials."""
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
    """Fake boto3.client(...) for cloudwatch/ecs; records every call."""
    clients: dict[str, _FakeAwsClient] = {}

    def _client(name, *args, **kwargs):
        clients.setdefault(name, _FakeAwsClient())
        return clients[name]

    monkeypatch.setattr(session.boto3, "client", _client)
    monkeypatch.setattr(session, "_task_metadata_ids", lambda: ("test-cluster", "test-task-123"))
    return clients


def _quota_config(**overrides) -> QuotaConfig:
    base = dict(
        heartbeat_renew_interval=0.02,
        heartbeat_ttl=5.0,
        sub_floor_seconds=1,
        per_task_max_sessions=5,
        auto_trip_ceiling_seconds=100_000,
        auto_trip_ceiling_dollars=100_000.0,
        est_cost_per_second=0.01,
    )
    base.update(overrides)
    return QuotaConfig(**base)


def _tier(*, session_max=5, period_max=600, max_concurrent=2) -> quota.Tier:
    return quota.Tier(
        tier_id="t", session_max_seconds=session_max, period_max_seconds=period_max, max_concurrent=max_concurrent
    )


# --- tick mechanics (stubbed quota.record_tick — isolates SessionLifecycle) ---


async def test_tick_loop_calls_record_tick_repeatedly_with_first_tick_flag(monkeypatch, fake_aws):
    calls: list[dict] = []

    def fake_record_tick(**kwargs):
        calls.append(kwargs)
        return quota.TickResult(daily_exhausted=False, site_paused=False)

    monkeypatch.setattr(quota, "record_tick", fake_record_tick)

    lifecycle = session.SessionLifecycle(
        user_id="u1", session_id="s1", tier=_tier(session_max=5), quota_config=_quota_config()
    )
    await lifecycle.start()
    await asyncio.sleep(0.09)  # >= 4 tick intervals at 0.02s
    await lifecycle.stop()

    assert len(calls) >= 3
    assert calls[0]["user_id"] == "u1"
    assert calls[0]["session_id"] == "s1"
    assert calls[0]["is_first_tick"] is True
    assert calls[1]["is_first_tick"] is False


async def test_bypass_session_skips_tick_and_timer_entirely(monkeypatch, fake_aws):
    calls: list[dict] = []
    monkeypatch.setattr(quota, "record_tick", lambda **kw: calls.append(kw))
    stop_calls = []

    async def on_stop():
        stop_calls.append(True)

    lifecycle = session.SessionLifecycle(
        user_id="service:smoke",
        session_id="s1",
        tier=_tier(session_max=0),  # bypass sessions carry a zeroed placeholder tier
        quota_config=_quota_config(),
        bypass_accounting=True,
        on_stop=on_stop,
    )
    await lifecycle.start()
    await asyncio.sleep(0.05)
    await lifecycle.stop()

    assert calls == []  # no accounting at all
    assert stop_calls == []  # no service timer fired (would hard-stop instantly at session_max=0)


# --- hard-stop at session_max (D-02) ---


async def test_hard_stop_fires_at_session_max(fake_aws, monkeypatch):
    monkeypatch.setattr(quota, "record_tick", lambda **kw: quota.TickResult(False, False))
    stopped = asyncio.Event()
    warned = asyncio.Event()

    async def on_warning():
        warned.set()

    async def on_stop():
        stopped.set()

    lifecycle = session.SessionLifecycle(
        user_id="u1",
        session_id="s1",
        tier=_tier(session_max=0.05),
        quota_config=_quota_config(),
        on_warning=on_warning,
        on_stop=on_stop,
    )
    await lifecycle.start()
    await asyncio.wait_for(stopped.wait(), timeout=2.0)
    await lifecycle.stop()

    # session_max (0.05s) < WARNING_LEAD_SECONDS (30s), so warning fires
    # immediately (clamped to t=0) and stop fires right after — both proven.
    assert warned.is_set()
    assert stopped.is_set()


async def test_daily_exhaustion_mid_session_invokes_wind_down_hook(fake_aws, monkeypatch):
    monkeypatch.setattr(quota, "record_tick", lambda **kw: quota.TickResult(daily_exhausted=True, site_paused=False))
    exhausted = asyncio.Event()

    async def on_daily_exhausted():
        exhausted.set()

    lifecycle = session.SessionLifecycle(
        user_id="u1",
        session_id="s1",
        tier=_tier(session_max=5),
        quota_config=_quota_config(),
        on_daily_exhausted=on_daily_exhausted,
    )
    await lifecycle.start()
    await asyncio.wait_for(exhausted.wait(), timeout=1.0)
    await lifecycle.stop()


async def test_daily_exhaustion_falls_back_to_on_stop_when_no_dedicated_hook(fake_aws, monkeypatch):
    monkeypatch.setattr(quota, "record_tick", lambda **kw: quota.TickResult(daily_exhausted=True, site_paused=False))
    stopped = asyncio.Event()

    async def on_stop():
        stopped.set()

    lifecycle = session.SessionLifecycle(
        user_id="u1", session_id="s1", tier=_tier(session_max=5), quota_config=_quota_config(), on_stop=on_stop
    )
    await lifecycle.start()
    await asyncio.wait_for(stopped.wait(), timeout=1.0)
    await lifecycle.stop()


# --- ActiveSessions metric + scale-in protection (D-13/INFR-06) ---


async def test_scale_in_protection_acquired_on_first_and_released_on_last(fake_aws, monkeypatch):
    monkeypatch.setattr(quota, "record_tick", lambda **kw: quota.TickResult(False, False))
    cfg = _quota_config()

    lifecycle_a = session.SessionLifecycle(user_id="ua", session_id="sa", tier=_tier(), quota_config=cfg)
    lifecycle_b = session.SessionLifecycle(user_id="ub", session_id="sb", tier=_tier(), quota_config=cfg)

    await lifecycle_a.start()
    assert session.active_session_count() == 1
    protection_calls = [c for c in fake_aws["ecs"].calls if c[0] == "update_task_protection"]
    assert protection_calls == [("update_task_protection", {"cluster": "test-cluster", "tasks": ["test-task-123"], "protectionEnabled": True})]

    await lifecycle_b.start()
    assert session.active_session_count() == 2
    # A second concurrent session does NOT re-acquire protection (already held).
    protection_calls = [c for c in fake_aws["ecs"].calls if c[0] == "update_task_protection"]
    assert len(protection_calls) == 1

    await lifecycle_a.stop()
    assert session.active_session_count() == 1
    protection_calls = [c for c in fake_aws["ecs"].calls if c[0] == "update_task_protection"]
    assert len(protection_calls) == 1  # still not released — lifecycle_b is still active

    await lifecycle_b.stop()
    assert session.active_session_count() == 0
    protection_calls = [c for c in fake_aws["ecs"].calls if c[0] == "update_task_protection"]
    assert protection_calls[-1] == (
        "update_task_protection",
        {"cluster": "test-cluster", "tasks": ["test-task-123"], "protectionEnabled": False},
    )


async def test_scale_in_protection_not_stranded_when_released_mid_start(fake_aws, monkeypatch):
    """Black-screen root cause regression: a terminal connection close during
    start()'s awaits must NOT leave ECS scale-in protection enabled with zero
    active sessions. If it does, the stale task can't be drained by a rolling
    deploy, two builds serve at once, and the SPA asset hashes mismatch -> black
    screen. Invariant: protection_enabled <=> active_session_count() > 0.

    Deterministic reproduction: park start() inside its first await (metric
    emit) AFTER it has incremented the count, run release() to completion
    (count -> 0, protection cleared), then let start() finish. The buggy code
    re-set protection True after release() cleared it and bailed on _stopped,
    stranding protection ON.
    """
    import threading

    lc = session.SessionLifecycle(
        user_id="u", session_id="s", tier=_tier(), quota_config=_quota_config(),
        bypass_accounting=True,  # exercises the protection path with no tick/heartbeat deps
    )

    reached_first_await = threading.Event()
    release_completed = threading.Event()
    emit_calls = {"n": 0}

    def blocking_emit():
        emit_calls["n"] += 1
        if emit_calls["n"] == 1:
            reached_first_await.set()      # start() has incremented + parked here
            release_completed.wait(3.0)    # hold the thread until release() has run
        # release()'s own emit (2nd call) returns immediately

    monkeypatch.setattr(lc, "_emit_metric", blocking_emit)

    start_task = asyncio.create_task(lc.start())
    for _ in range(400):
        if reached_first_await.is_set():
            break
        await asyncio.sleep(0.005)
    assert reached_first_await.is_set(), "start() never reached its first await"
    assert session.active_session_count() == 1  # start() incremented before parking

    # Terminal close mid-start: release runs fully, count -> 0.
    await lc.release()
    assert session.active_session_count() == 0

    release_completed.set()  # let start() resume (it will see _stopped and bail)
    await start_task

    # THE INVARIANT: zero active sessions => scale-in protection MUST be OFF.
    protection_calls = [c for c in fake_aws["ecs"].calls if c[0] == "update_task_protection"]
    assert protection_calls, "expected at least one scale-in protection call"
    assert protection_calls[-1][1]["protectionEnabled"] is False, (
        f"scale-in protection stranded ON with 0 active sessions -> deploy-blocking "
        f"stale task -> black screen. Calls: {protection_calls}"
    )


async def test_active_sessions_metric_emitted_on_start_and_stop(fake_aws, monkeypatch):
    monkeypatch.setattr(quota, "record_tick", lambda **kw: quota.TickResult(False, False))
    lifecycle = session.SessionLifecycle(user_id="u1", session_id="s1", tier=_tier(), quota_config=_quota_config())

    await lifecycle.start()
    await lifecycle.stop()

    metric_calls = [c for c in fake_aws["cloudwatch"].calls if c[0] == "put_metric_data"]
    assert len(metric_calls) == 2  # once on start, once on stop
    assert metric_calls[0][1]["Namespace"] == session.METRIC_NAMESPACE
    assert metric_calls[0][1]["MetricData"][0]["MetricName"] == session.METRIC_NAME


async def test_scale_in_protection_skipped_gracefully_outside_ecs(monkeypatch):
    """Local dev/test with no ECS task metadata: no exception, no ECS call."""
    calls = []
    monkeypatch.setattr(session.boto3, "client", lambda name, *a, **k: calls.append(name) or _FakeAwsClient())
    monkeypatch.setattr(session, "_task_metadata_ids", lambda: ("", ""))
    monkeypatch.setattr(quota, "record_tick", lambda **kw: quota.TickResult(False, False))

    lifecycle = session.SessionLifecycle(user_id="u1", session_id="s1", tier=_tier(), quota_config=_quota_config())
    await lifecycle.start()
    await lifecycle.stop()

    assert "ecs" not in calls  # update_task_protection never attempted
    assert "cloudwatch" in calls  # metric emission still attempted


# --- auto-trip integration (real dynamodb-local — proves the control item flips) ---


async def test_auto_trip_flips_control_item_when_ceiling_crossed(fake_aws, monkeypatch):
    day = f"test-day-{uuid.uuid4().hex[:12]}"  # isolate this test's rollup item
    quota._usage_table().put_item(Item={"pk": quota.CONTROL_PK, "sk": quota.CONTROL_SK, "engaged": False})

    user_id = f"test-user-{uuid.uuid4().hex[:12]}"
    session_id = f"test-session-{uuid.uuid4().hex[:12]}"
    quota.acquire_heartbeat(user_id, session_id, ttl_seconds=5)

    lifecycle = session.SessionLifecycle(
        user_id=user_id,
        session_id=session_id,
        tier=_tier(session_max=5, period_max=100_000),
        # A 1-second ceiling: the first real tick's delta trips it.
        quota_config=_quota_config(auto_trip_ceiling_seconds=1),
        day=day,
        # A fixed clock: the first tick sees a 10s delta (_last_tick_at is
        # set 10s in the past below); every subsequent tick sees a 0s delta
        # (clock never advances), so only the first tick's delta matters.
        clock=lambda: 1_000_000.0,
    )

    await lifecycle.start()
    # start() sets _last_tick_at = clock() = 1_000_000.0; back-date it here
    # (synchronously, before the tick task's first sleep elapses) so the
    # first real tick sees a 10s delta instead of 0.
    lifecycle._last_tick_at = 1_000_000.0 - 10
    await asyncio.sleep(0.05)  # >= 2 tick intervals
    await lifecycle.stop()

    assert quota.read_control_item()["engaged"] is True

    # Cleanup: never leave the shared control item engaged for other tests/local dev.
    quota._usage_table().put_item(Item={"pk": quota.CONTROL_PK, "sk": quota.CONTROL_SK, "engaged": False})
