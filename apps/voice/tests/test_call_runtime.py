"""Focused tests for klanker_voice.call_runtime (Phase A, D-07/D-08).

Proves the D-01/D-02/D-05 contract against a fake, non-WebRTC ``BaseTransport``
— no aiortc, no HTTP, no live media — so these tests demonstrate
``create_call_session`` is genuinely transport-neutral, not just tested
through its one real (WebRTC) caller.

Every ``GateResult`` here carries ``bypass_accounting=True`` so
``SessionLifecycle.release()`` never calls the real (network) ``quota.
release_heartbeat`` — these are unit tests of the runtime's own
construction/close/release wiring, not of quota accounting (already covered
by test_session.py/test_slot_leak.py against dynamodb-local). AWS calls
(CloudWatch, ECS) are always faked via the same recording-stub pattern
test_session.py uses, so metric emission can be counted exactly.
"""

from __future__ import annotations

import asyncio

import pytest

from pipecat.processors.frame_processor import FrameProcessor
from pipecat.transports.base_transport import BaseTransport

from klanker_voice import quota, session
from klanker_voice.call_runtime import CallIdentity, CallSession, create_call_session
from klanker_voice.config import DuplexConfig, QuotaConfig, load_config, load_knowledge_config
from klanker_voice.session import SessionLifecycle


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


class FakeTransport(BaseTransport):
    """A minimal, deliberately NOT-WebRTC ``BaseTransport`` stub — proves
    ``create_call_session`` works against ANY transport, not just
    ``SmallWebRTCTransport`` (D-07(a))."""

    def __init__(self) -> None:
        super().__init__()
        self._register_event_handler("on_client_connected")
        self._register_event_handler("on_client_disconnected")

    def input(self) -> FrameProcessor:
        return FrameProcessor(name="fake-transport-input")

    def output(self) -> FrameProcessor:
        return FrameProcessor(name="fake-transport-output")


def _quota_config(**overrides) -> QuotaConfig:
    base = dict(
        heartbeat_renew_interval=15,
        heartbeat_ttl=45,
        sub_floor_seconds=30,
        per_task_max_sessions=5,
        auto_trip_ceiling_seconds=7200,
        auto_trip_ceiling_dollars=40,
        est_cost_per_second=0.005,
        reconnect_grace_seconds=1.0,  # bounded but plenty of margin for the fast test below
    )
    base.update(overrides)
    return QuotaConfig(**base)


def _gate_result(**overrides) -> quota.GateResult:
    base = dict(
        session_id="call-runtime-test-session",
        tier=quota.Tier(
            tier_id="t", session_max_seconds=600, period_max_seconds=6000, max_concurrent=5
        ),
        session_max_seconds=600,
        remaining_daily_seconds=6000,
        # Never touch real DynamoDB from this unit test — these tests exercise
        # the runtime's own construction/close/release wiring, not quota
        # accounting (already covered by test_session.py/test_slot_leak.py).
        bypass_accounting=True,
    )
    base.update(overrides)
    return quota.GateResult(**base)


async def _build_call_session(make_config_file, *, transport: BaseTransport | None = None) -> CallSession:
    config_path = make_config_file()
    cfg = load_config(config_path)
    # The real repo pipeline.toml's [knowledge] table -- KnowledgeConfig is a
    # required create_call_session parameter, and building a synthetic one
    # would just re-point at the same real manifest/topic-map/packs anyway.
    knowledge_cfg = load_knowledge_config()

    return await create_call_session(
        transport=transport or FakeTransport(),
        identity=CallIdentity(subject="tester", authenticated=True),
        gate_result=_gate_result(),
        cfg=cfg,
        knowledge_cfg=knowledge_cfg,
        duplex_cfg=DuplexConfig(),
        quota_cfg=_quota_config(),
        channel="webrtc",
        metadata={},
    )


async def test_create_call_session_is_transport_neutral(make_config_file, stub_provider_keys, fake_aws):
    """A CallSession is constructed around a fake, non-WebRTC BaseTransport —
    no SmallWebRTCConnection, no HTTP, no aiortc anywhere in this test."""
    transport = FakeTransport()

    call_session = await _build_call_session(make_config_file, transport=transport)

    assert isinstance(call_session, CallSession)
    assert call_session.session_id == "call-runtime-test-session"
    assert isinstance(call_session.lifecycle, SessionLifecycle)
    assert call_session.worker is not None

    # The transport-agnostic event wiring actually fired on our fake
    # transport (proves create_call_session doesn't special-case WebRTC).
    assert transport._event_handlers["on_client_disconnected"].handlers
    assert transport._event_handlers["on_client_connected"].handlers


async def test_close_is_idempotent(make_config_file, stub_provider_keys, fake_aws):
    """Calling close() twice is safe (D-05): the second call is a no-op, and
    the ActiveSessions metric this releases only fires exactly once."""
    call_session = await _build_call_session(make_config_file)

    await call_session.close("first close")
    await call_session.close("second close")

    assert call_session.lifecycle._stopped is True
    metric_calls = [c for c in fake_aws["cloudwatch"].calls if c[0] == "put_metric_data"]
    assert len(metric_calls) == 1  # release()'s _stopped guard collapses the second close()


async def test_release_fires_once_on_worker_termination(make_config_file, stub_provider_keys, fake_aws):
    """The same hook server.py wires to runner.cancel (on_released) fires
    exactly once no matter how many times close() is called."""
    call_session = await _build_call_session(make_config_file)

    release_count = 0

    async def _recording_on_released() -> None:
        nonlocal release_count
        release_count += 1

    call_session.lifecycle.on_released = _recording_on_released

    await call_session.close("worker run ended")
    await call_session.close("worker run ended (racing second call)")

    assert release_count == 1


async def test_transport_termination_triggers_single_release(make_config_file, stub_provider_keys, fake_aws):
    """A transport disconnect (D-06 layer 1, reconnect-grace scheduled) racing
    an explicit close() — the WebRTC analog is server.py's
    ``_wire_connection_teardown`` terminal-close fast-path racing the
    transport's own on_client_disconnected — must still release exactly
    once (spec §6.10 / D-05): the explicit close() wins immediately and
    cancels the pending reconnect-grace task before it can fire a second
    release()."""
    transport = FakeTransport()
    call_session = await _build_call_session(make_config_file, transport=transport)

    # Drive the transport's on_client_disconnected handler(s) directly (no
    # real transport event loop needed) -- this schedules SessionLifecycle's
    # reconnect-grace timer.
    for handler in transport._event_handlers["on_client_disconnected"].handlers:
        await handler(transport, None)

    # A racing terminal close (e.g. server.py's abrupt-connection-close fast
    # path) fires release() immediately, well within the 1s reconnect grace.
    await call_session.close("terminal close")
    await asyncio.sleep(0.05)  # let the (now-cancelled) reconnect-grace task settle

    metric_calls = [c for c in fake_aws["cloudwatch"].calls if c[0] == "put_metric_data"]
    assert len(metric_calls) == 1  # the _stopped guard collapsed both terminal paths into one
