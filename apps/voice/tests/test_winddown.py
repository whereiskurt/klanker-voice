"""Spoken wind-down: natural warning (LLM-context inject) + deterministic
goodbye (TTS bypass), the goodbye grace + hard-close, and mid-session
daily/period exhaustion reusing the same wind-down (QUOT-03, D-04/D-05).

Two layers under test:

1. ``pipeline.inject_warning_instruction`` / ``pipeline.speak_goodbye`` —
   pure functions against a stubbed ``PipelineWorker``/``LLMContext``, no
   event loop timing involved.
2. ``session.SessionLifecycle``'s wiring of those two hooks via
   ``on_warning``/``on_stop`` — a real (tiny-interval) event loop, mirroring
   test_session.py's precedent, plus the dynamodb-local pin that module's
   autouse fixture already established (``SessionLifecycle.stop()`` always
   calls the real, unstubbed ``quota.release_heartbeat()``).
"""

from __future__ import annotations

import asyncio

import boto3
import pytest

from klanker_voice import pipeline, quota, session
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
    def __init__(self):
        self.calls: list[tuple[str, dict]] = []

    def __getattr__(self, name):
        def _record(**kwargs):
            self.calls.append((name, kwargs))
            return {}

        return _record


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


def _quota_config(**overrides) -> QuotaConfig:
    base = dict(
        heartbeat_renew_interval=0.02,
        heartbeat_ttl=5.0,
        sub_floor_seconds=1,
        per_task_max_sessions=5,
        auto_trip_ceiling_seconds=100_000,
        auto_trip_ceiling_dollars=100_000.0,
        est_cost_per_second=0.01,
        winddown_warning_seconds=30,
        goodbye_grace_seconds=0.03,
        user_silence_timeout=100,
        reconnect_grace_seconds=100,
        warning_copy="30 seconds left",
        goodbye_copy="goodbye now",
    )
    base.update(overrides)
    return QuotaConfig(**base)


def _tier(*, session_max=5, period_max=600, max_concurrent=2) -> quota.Tier:
    return quota.Tier(
        tier_id="t", session_max_seconds=session_max, period_max_seconds=period_max, max_concurrent=max_concurrent
    )


# --- pipeline.py: inject_warning_instruction / speak_goodbye (pure) ---


class _FakeContext:
    def __init__(self):
        self.messages: list[dict] = []

    def add_message(self, message: dict) -> None:
        self.messages.append(message)


class _FakeWorker:
    def __init__(self):
        self.queued: list[list] = []

    async def queue_frames(self, frames: list) -> None:
        self.queued.append(frames)


async def test_inject_warning_instruction_pushes_high_priority_context_message():
    worker = _FakeWorker()
    context = _FakeContext()

    await pipeline.inject_warning_instruction(worker, context, "time's almost up")

    assert len(context.messages) == 1
    assert context.messages[0]["content"] == "time's almost up"
    # A developer/system-priority role, not a spoken-verbatim user/assistant turn.
    assert context.messages[0]["role"] in ("developer", "system")
    # Queues a run frame so the LLM actually weaves it into its next turn.
    assert len(worker.queued) == 1


async def test_speak_goodbye_routes_text_straight_to_tts_bypassing_llm():
    from pipecat.frames.frames import TTSSpeakFrame

    worker = _FakeWorker()

    await pipeline.speak_goodbye(worker, "take care!")

    assert len(worker.queued) == 1
    (frame,) = worker.queued[0]
    assert isinstance(frame, TTSSpeakFrame)
    assert frame.text == "take care!"
    # No LLM turn: never appended as a context message the next inference sees.
    assert frame.append_to_context is False


# --- session.py: on_warning/on_stop wiring, goodbye grace, hard-close ---


async def test_warning_fires_before_stop_with_correct_lead_time(fake_aws, monkeypatch):
    monkeypatch.setattr(quota, "record_tick", lambda **kw: quota.TickResult(False, False))
    events: list[str] = []

    async def on_warning():
        events.append("warning")

    async def on_stop():
        events.append("stop")

    lifecycle = session.SessionLifecycle(
        user_id="u1",
        session_id="s1",
        tier=_tier(session_max=0.05),
        quota_config=_quota_config(winddown_warning_seconds=30),
        on_warning=on_warning,
        on_stop=on_stop,
    )
    await lifecycle.start()
    await asyncio.sleep(0.2)
    await lifecycle.stop()

    # session_max (0.05s) < winddown_warning_seconds (30s): warning clamps to
    # t=0 and fires immediately, stop follows right after.
    assert events == ["warning", "stop"]


async def test_stop_hook_speaks_goodbye_waits_grace_then_hard_closes(fake_aws, monkeypatch):
    """The real on_stop wiring server.py builds: speak_goodbye -> sleep up to
    goodbye_grace_seconds -> hard-close (here: a fake runner.cancel())."""
    monkeypatch.setattr(quota, "record_tick", lambda **kw: quota.TickResult(False, False))
    worker = _FakeWorker()
    closed = asyncio.Event()
    quota_cfg = _quota_config(goodbye_grace_seconds=0.03)

    async def on_stop():
        await pipeline.speak_goodbye(worker, quota_cfg.goodbye_copy)
        await asyncio.sleep(quota_cfg.goodbye_grace_seconds)
        closed.set()

    lifecycle = session.SessionLifecycle(
        user_id="u1", session_id="s1", tier=_tier(session_max=0.02), quota_config=quota_cfg, on_stop=on_stop
    )
    await lifecycle.start()
    await asyncio.wait_for(closed.wait(), timeout=2.0)
    await lifecycle.stop()

    assert len(worker.queued) == 1  # the goodbye TTSSpeakFrame was queued
    assert closed.is_set()


async def test_daily_exhaustion_reuses_the_same_wind_down_sequence(fake_aws, monkeypatch):
    """D-04: mid-session daily/period exhaustion (a 15s tick result, not the
    service timer) fires the identical on_stop wind-down."""
    monkeypatch.setattr(
        quota, "record_tick", lambda **kw: quota.TickResult(daily_exhausted=True, site_paused=False)
    )
    stopped = asyncio.Event()
    quota_cfg = _quota_config()

    async def on_stop():
        stopped.set()

    lifecycle = session.SessionLifecycle(
        # session_max well beyond the tick interval, so only the exhaustion
        # path (not the service timer) could fire on_stop this fast.
        user_id="u1",
        session_id="s1",
        tier=_tier(session_max=100),
        quota_config=quota_cfg,
        on_stop=on_stop,
    )
    await lifecycle.start()
    await asyncio.wait_for(stopped.wait(), timeout=1.0)
    await lifecycle.stop()


async def test_wind_down_never_fires_twice_even_if_exhaustion_and_timer_race(fake_aws, monkeypatch):
    """Both triggers reaching on_stop (daily exhaustion via the tick, and —
    in a pathological config — the service timer landing at nearly the same
    moment) must still invoke on_stop exactly once (D-04)."""
    monkeypatch.setattr(
        quota, "record_tick", lambda **kw: quota.TickResult(daily_exhausted=True, site_paused=False)
    )
    calls: list[str] = []

    async def on_stop():
        calls.append("stop")

    lifecycle = session.SessionLifecycle(
        # session_max small enough that the service timer's own stop could
        # also land soon after the first (very fast) exhaustion tick.
        user_id="u1",
        session_id="s1",
        tier=_tier(session_max=0.05),
        quota_config=_quota_config(winddown_warning_seconds=30, heartbeat_renew_interval=0.02),
        on_stop=on_stop,
    )
    await lifecycle.start()
    await asyncio.sleep(0.15)
    await lifecycle.stop()

    assert calls == ["stop"]  # never double-fired
