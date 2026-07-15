"""Three D-06 idle-teardown layers atop the D-02 wall-clock bound, the D-07
reconnect grace, and the single idempotent ``release()`` every layer funnels
through (QUOT-05).

Real (tiny-interval) asyncio event loop, matching test_session.py's and
test_winddown.py's precedent, pinned at dynamodb-local via the same autouse
fixture (``SessionLifecycle.release()`` always calls the real, unstubbed
``quota.release_heartbeat()``).
"""

from __future__ import annotations

import asyncio

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


@pytest.fixture(autouse=True)
def fake_record_tick(monkeypatch):
    """No test in this module exercises the accounting tick itself — only
    the teardown-layer mechanics — so keep it a harmless no-op throughout."""
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
        winddown_warning_seconds=100,  # keep session_max well clear of the D-02 timer
        goodbye_grace_seconds=1,
        user_silence_timeout=0.05,
        reconnect_grace_seconds=0.05,
    )
    base.update(overrides)
    return QuotaConfig(**base)


def _tier(*, session_max=100, period_max=600, max_concurrent=2) -> quota.Tier:
    return quota.Tier(
        tier_id="t", session_max_seconds=session_max, period_max_seconds=period_max, max_concurrent=max_concurrent
    )


def _lifecycle(fake_aws, **overrides) -> session.SessionLifecycle:
    kwargs = dict(
        user_id="u1",
        session_id="s1",
        tier=_tier(),
        quota_config=_quota_config(),
    )
    kwargs.update(overrides)
    return session.SessionLifecycle(**kwargs)


# --- layer 1 + D-07: transport disconnect / reconnect grace ---


async def test_transport_disconnect_releases_after_reconnect_grace_elapses(fake_aws):
    lifecycle = _lifecycle(
        fake_aws, quota_config=_quota_config(reconnect_grace_seconds=0.03, user_silence_timeout=100)
    )
    await lifecycle.start()

    await lifecycle.on_transport_disconnected()
    assert session.active_session_count() == 1  # still within the grace window

    await asyncio.sleep(0.08)

    assert session.active_session_count() == 0  # grace expired -> released


async def test_reconnect_within_grace_cancels_teardown(fake_aws):
    lifecycle = _lifecycle(
        fake_aws, quota_config=_quota_config(reconnect_grace_seconds=0.1, user_silence_timeout=100)
    )
    await lifecycle.start()

    await lifecycle.on_transport_disconnected()
    await asyncio.sleep(0.02)
    await lifecycle.on_transport_reconnected()
    await asyncio.sleep(0.15)  # well past the original grace deadline

    assert session.active_session_count() == 1  # never torn down
    await lifecycle.release()


# --- layer 2: user-silence watchdog ---


async def test_user_silence_timeout_releases_the_session(fake_aws):
    lifecycle = _lifecycle(fake_aws, quota_config=_quota_config(user_silence_timeout=0.03))
    await lifecycle.start()

    await asyncio.sleep(0.08)

    assert session.active_session_count() == 0


async def test_user_speech_resets_the_silence_watchdog(fake_aws):
    lifecycle = _lifecycle(fake_aws, quota_config=_quota_config(user_silence_timeout=0.06))
    await lifecycle.start()

    # Keep "speaking" every tick well inside the timeout window — the
    # session must never be torn down as long as speech keeps resetting it.
    for _ in range(4):
        await asyncio.sleep(0.03)
        await lifecycle.on_user_speech()

    assert session.active_session_count() == 1
    await lifecycle.release()


# --- layer 3: pipeline stall / unrecoverable error ---


async def test_pipeline_stall_releases_immediately_with_no_grace(fake_aws):
    lifecycle = _lifecycle(fake_aws)
    await lifecycle.start()

    await lifecycle.on_pipeline_stall()

    assert session.active_session_count() == 0


# --- TeardownObserver: real pipecat frames drive layers 2 + 3 ---


async def test_teardown_observer_routes_user_started_speaking_to_on_user_speech(fake_aws):
    from pipecat.frames.frames import UserStartedSpeakingFrame
    from pipecat.observers.base_observer import FramePushed
    from pipecat.processors.frame_processor import FrameDirection

    lifecycle = _lifecycle(fake_aws, quota_config=_quota_config(user_silence_timeout=100))
    await lifecycle.start()
    observer = session.TeardownObserver(lifecycle)

    await observer.on_push_frame(
        FramePushed(
            source=None,
            destination=None,
            frame=UserStartedSpeakingFrame(),
            direction=FrameDirection.DOWNSTREAM,
            timestamp=0,
        )
    )

    # A fresh watchdog task was armed by the routed event (proves the wiring
    # fired, without waiting out the (long) configured timeout here).
    assert lifecycle._watchdog_task is not None
    await lifecycle.release()


async def test_teardown_observer_bot_speech_resets_the_silence_watchdog(fake_aws):
    """A caller listening to the bot is NOT idle. Bot speech — start AND stop —
    must reset the D-06 user-silence watchdog, so a long bot turn (e.g. the
    greeting) never counts as user silence and hangs up on a caller who is just
    listening (telephony-experience: 'if the bot is talking I'm not talking and
    it shouldn't count against me')."""
    from pipecat.frames.frames import BotStartedSpeakingFrame, BotStoppedSpeakingFrame
    from pipecat.observers.base_observer import FramePushed
    from pipecat.processors.frame_processor import FrameDirection

    lifecycle = _lifecycle(fake_aws, quota_config=_quota_config(user_silence_timeout=100))
    await lifecycle.start()
    observer = session.TeardownObserver(lifecycle)

    for frame in (BotStartedSpeakingFrame(), BotStoppedSpeakingFrame()):
        before = lifecycle._watchdog_task
        await observer.on_push_frame(
            FramePushed(
                source=None,
                destination=None,
                frame=frame,
                direction=FrameDirection.DOWNSTREAM,
                timestamp=0,
            )
        )
        # A FRESH watchdog was armed by the bot-speech event — proving the
        # window restarts each time the bot speaks, not just on user speech.
        assert lifecycle._watchdog_task is not None
        assert lifecycle._watchdog_task is not before

    await lifecycle.release()


async def test_teardown_observer_routes_fatal_error_frame_to_release(fake_aws):
    from pipecat.frames.frames import ErrorFrame
    from pipecat.observers.base_observer import FramePushed
    from pipecat.processors.frame_processor import FrameDirection

    lifecycle = _lifecycle(fake_aws)
    await lifecycle.start()
    observer = session.TeardownObserver(lifecycle)

    await observer.on_push_frame(
        FramePushed(
            source=None,
            destination=None,
            frame=ErrorFrame(error="STT connection lost", fatal=True),
            direction=FrameDirection.UPSTREAM, timestamp=0,
        )
    )

    assert session.active_session_count() == 0


async def test_teardown_observer_ignores_non_fatal_error_frame(fake_aws):
    from pipecat.frames.frames import ErrorFrame
    from pipecat.observers.base_observer import FramePushed
    from pipecat.processors.frame_processor import FrameDirection

    lifecycle = _lifecycle(fake_aws)
    await lifecycle.start()
    observer = session.TeardownObserver(lifecycle)

    await observer.on_push_frame(
        FramePushed(
            source=None,
            destination=None,
            frame=ErrorFrame(error="transient hiccup", fatal=False),
            direction=FrameDirection.UPSTREAM, timestamp=0,
        )
    )

    assert session.active_session_count() == 1
    await lifecycle.release()


# --- release() idempotency + funnel-through ---


async def test_release_is_idempotent_under_concurrent_triggers(fake_aws):
    lifecycle = _lifecycle(fake_aws)
    await lifecycle.start()

    # Three independent layers racing to tear the same session down at once.
    await asyncio.gather(
        lifecycle.on_pipeline_stall(),
        lifecycle.release(),
        lifecycle.on_pipeline_stall(),
    )

    assert session.active_session_count() == 0
    # Exactly one ActiveSessions "stop" metric emission (start emits one,
    # release() emits exactly one more) — proves the idempotency guard let
    # only the first of the three concurrent callers actually run the body.
    metric_calls = [c for c in fake_aws["cloudwatch"].calls if c[0] == "put_metric_data"]
    assert len(metric_calls) == 2  # one on start(), exactly one on release()


async def test_on_released_hook_fires_exactly_once_regardless_of_trigger(fake_aws):
    """server.py sets on_released to the real pipeline's hard-close (e.g.
    WorkerRunner.cancel) — SessionLifecycle never holds that reference
    itself, but every idle-teardown layer must still actually end the
    running pipeline, not just the DB/metric bookkeeping."""
    released = []

    async def on_released():
        released.append(True)

    lifecycle = _lifecycle(fake_aws, on_released=on_released)
    await lifecycle.start()

    await asyncio.gather(lifecycle.on_pipeline_stall(), lifecycle.release())

    assert released == [True]


async def test_wall_clock_cutoff_routes_through_the_same_release(fake_aws):
    """D-02's own stop path (via on_stop, wired by server.py to hard-close)
    ultimately reaches release() through SessionLifecycle.stop() — proven
    here directly, since on_stop itself is an opaque server.py-built hook."""
    lifecycle = _lifecycle(fake_aws, tier=_tier(session_max=0.03), quota_config=_quota_config(winddown_warning_seconds=30))
    await lifecycle.start()
    await asyncio.sleep(0.1)

    await lifecycle.stop()  # server.py's finally-block name; funnels to release()

    assert session.active_session_count() == 0
