"""§16 lifecycle unit-test matrix for ``AsteriskCallController`` (Phase 11
Plan 05, D-02/D-04/R6/T-11-05-*).

Hermetic and offline throughout: NO real Asterisk, NO real ARI HTTP/WebSocket,
NO real UDP socket, NO real Deepgram/Anthropic/ElevenLabs network call, NO
real AWS call (CloudWatch/ECS/DynamoDB) -- everything below is a fake:

- ``FakeAriClient`` -- records every REST call it receives (method name +
  args, in call order); returns predictable, incrementing channel/bridge
  ids. No aiohttp, no real ARI HTTP server anywhere in this file.
- ``FakeRtpMediaSession`` -- satisfies ``telephony.types.RtpMediaSession``
  with no socket at all; injected via ``AsteriskCallController``'s
  ``media_session_opener`` seam (never the real ``SocketRtpMediaSession``).
- ``quota.start_gate`` is monkeypatched per test (never touches DynamoDB) --
  reuses ``test_call_runtime.py``'s ``_gate_result``/``_quota_config``
  builders for a realistic-shaped ``GateResult``/``QuotaConfig``.
- ``fake_aws``/``reset_active_count`` -- imported from ``test_call_runtime.py``
  verbatim (same recording-stub pattern already established by
  ``test_telephony_transport.py``): ``SessionLifecycle.start()``/``release()``
  always call CloudWatch/ECS regardless of ``bypass_accounting``.
- ``stub_call_session_run`` (autouse) -- replaces ``CallSession.run`` with a
  version that only brackets ``lifecycle.start()`` (never ``runner.run()``,
  which would start the real pipeline and attempt a live STT/LLM/TTS
  connection). The controller's ``on_stasis_start`` still spawns a real
  tracked background task calling this stub, proving the "run the worker as
  a background task" wiring without a live provider round trip -- matches
  this plan's own "do not require real Deepgram/Anthropic/ElevenLabs or a
  live socket" instruction. The teardown path under test
  (``call_session.close()`` -> ``lifecycle.release()``) is unaffected by
  this stub -- these tests drive it directly, never relying on
  ``lifecycle.stop()``'s own ``finally`` block (which only fires once
  ``runner.run()`` returns).
"""

from __future__ import annotations

import asyncio
from typing import Any

import pytest

from klanker_voice import quota
from klanker_voice.call_runtime import CallSession
from klanker_voice.config import load_config, load_knowledge_config
from klanker_voice.telephony.config import TelephonyConfig
from klanker_voice.telephony.controller import ActiveCall, AsteriskCallController

# Reuse the test_call_runtime.py offline-pipeline test rig verbatim (D-10
# pattern already established by test_telephony_transport.py) -- fake AWS +
# a controllable GateResult/QuotaConfig builder + the active-session-count
# reset.
from tests.test_call_runtime import (  # noqa: F401 -- fake_aws/reset_active_count are fixtures
    _gate_result,
    _quota_config,
    fake_aws,
    reset_active_count,
)


# --- fakes ---------------------------------------------------------------


class FakeRtpMediaSession:
    """Satisfies ``telephony.types.RtpMediaSession`` with no socket at all."""

    def __init__(self, bound_port: int) -> None:
        self._bound_port = bound_port
        self.closed = False
        self.packets_written: list[bytes] = []

    @property
    def bound_port(self) -> int:
        return self._bound_port

    async def read_packet(self) -> bytes | None:
        return None

    async def write_packet(self, packet: bytes) -> None:
        self.packets_written.append(packet)

    async def close(self) -> None:
        self.closed = True


def _make_media_opener(order: list[str] | None = None):
    """Returns ``(opener, sessions)``: ``opener`` is an async
    ``(bind_host, bind_port) -> FakeRtpMediaSession`` callable suitable for
    ``AsteriskCallController(..., media_session_opener=opener)``; ``sessions``
    accumulates every session it ever created, in creation order."""
    sessions: list[FakeRtpMediaSession] = []

    async def _open(bind_host: str, bind_port: int) -> FakeRtpMediaSession:
        if order is not None:
            order.append("media_open")
        media = FakeRtpMediaSession(bound_port=40000 + len(sessions))
        sessions.append(media)
        return media

    return _open, sessions


class FakeAriClient:
    """Records every REST call (method name + positional args, in call
    order); returns predictable incrementing ids. No HTTP, no aiohttp, no
    real ARI server anywhere in this file."""

    def __init__(self, order: list[str] | None = None) -> None:
        self.calls: list[tuple[str, tuple, dict]] = []
        self._order = order
        self._ext_counter = 0
        self._bridge_counter = 0

    def _record(self, name: str, args: tuple = (), kwargs: dict | None = None) -> None:
        self.calls.append((name, args, kwargs or {}))
        if self._order is not None:
            self._order.append(name)

    async def answer(self, channel_id: str) -> None:
        self._record("answer", (channel_id,))

    async def create_external_media(
        self,
        app: str | None = None,
        external_host: str = "",
        fmt: str = "ulaw",
        channel_id: str | None = None,
    ) -> str:
        self._record(
            "create_external_media", (), {"app": app, "external_host": external_host, "fmt": fmt}
        )
        self._ext_counter += 1
        return f"ext-media-{self._ext_counter}"

    async def create_bridge(self, bridge_type: str = "mixing") -> str:
        self._record("create_bridge", (bridge_type,))
        self._bridge_counter += 1
        return f"bridge-{self._bridge_counter}"

    async def add_channel(self, bridge_id: str, channel_id: str) -> None:
        self._record("add_channel", (bridge_id, channel_id))

    async def hangup(self, channel_id: str) -> None:
        self._record("hangup", (channel_id,))

    async def destroy_bridge(self, bridge_id: str) -> None:
        self._record("destroy_bridge", (bridge_id,))

    def names(self) -> list[str]:
        return [c[0] for c in self.calls]

    def count(self, name: str, *, arg: str | None = None) -> int:
        return sum(1 for c in self.calls if c[0] == name and (arg is None or arg in c[1]))


def _stasis_event(
    *,
    channel_id: str = "chan-1",
    caller_number: str = "1001",
    exten: str = "1000",
    context: str = "from-klanker-inbound",
    application: str = "klanker",
) -> dict[str, Any]:
    return {
        "type": "StasisStart",
        "application": application,
        "args": [],
        "channel": {
            "id": channel_id,
            "name": f"PJSIP/dev-softphone-{channel_id}",
            "state": "Up",
            "caller": {"name": "", "number": caller_number},
            "dialplan": {"context": context, "exten": exten, "priority": 4},
        },
    }


def _channel_destroyed_event(channel_id: str = "chan-1") -> dict[str, Any]:
    return {"type": "ChannelDestroyed", "channel": {"id": channel_id}}


@pytest.fixture(autouse=True)
def stub_call_session_run(monkeypatch):
    """Replace ``CallSession.run`` with a version that only brackets
    ``lifecycle.start()`` -- never ``runner.run()`` (the real pipeline,
    which would attempt a live Deepgram/Anthropic/ElevenLabs connection).
    See module docstring."""

    async def _fake_run(self: CallSession) -> None:
        await self.lifecycle.start()

    monkeypatch.setattr(CallSession, "run", _fake_run)


def _telephony_cfg(**overrides) -> TelephonyConfig:
    base = dict(enabled=True, max_concurrent_calls=1, unlock_tier_id="kph-tier")
    base.update(overrides)
    return TelephonyConfig(**base)


def _build_controller(
    make_config_file,
    *,
    order: list[str] | None = None,
    telephony_cfg: TelephonyConfig | None = None,
) -> tuple[AsteriskCallController, FakeAriClient, list[FakeRtpMediaSession]]:
    cfg = load_config(make_config_file())
    knowledge_cfg = load_knowledge_config()
    ari = FakeAriClient(order=order)
    opener, sessions = _make_media_opener(order=order)
    controller = AsteriskCallController(
        ari,
        cfg,
        knowledge_cfg,
        # A generous reconnect_grace: these tests fake CallSession.run so the
        # real transport/pipeline (and therefore any on_client_disconnected
        # firing) never actually starts -- this is defensive documentation,
        # not a requirement for correctness here.
        _quota_config(reconnect_grace_seconds=3600.0),
        telephony_cfg or _telephony_cfg(),
        media_session_opener=opener,
    )
    return controller, ari, sessions


def _patch_start_gate(
    monkeypatch, *, result: quota.GateResult | None = None, error: Exception | None = None
) -> None:
    def _fake_start_gate(identity, **kwargs):
        if error is not None:
            raise error
        return result or _gate_result()

    monkeypatch.setattr("klanker_voice.telephony.controller.quota.start_gate", _fake_start_gate)


# --- (a)/(f) StasisStart allocation (Task 1) ------------------------------


async def test_stasis_start_allocates_and_registers(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """Task 1 acceptance: answers, creates external-media + bridge, adds
    both channels, constructs a CallSession, registers an ActiveCall keyed
    by the SIP channel id with all §13 fields populated -- and the socket
    session is opened BEFORE create_external_media (R2 bind-first)."""
    order: list[str] = []
    controller, ari, sessions = _build_controller(make_config_file, order=order)
    _patch_start_gate(monkeypatch)

    await controller.on_stasis_start(_stasis_event())

    assert ari.names()[0] == "answer"
    assert ari.count("create_external_media") == 1
    assert ari.count("create_bridge") == 1
    assert ari.count("add_channel") == 2

    assert len(controller.calls) == 1
    active_call = controller.calls["chan-1"]
    assert isinstance(active_call, ActiveCall)
    assert active_call.sip_channel_id == "chan-1"
    assert active_call.external_media_channel_id == "ext-media-1"
    assert active_call.bridge_id == "bridge-1"
    assert active_call.media_session is sessions[0]
    assert active_call.call_session is not None
    assert active_call.caller_id == "1001"
    assert active_call.did == "1000"
    assert active_call.closed is False

    # R2 bind-first ordering: the socket media session must be opened
    # before Asterisk's externalMedia channel is created.
    assert order.index("media_open") < order.index("create_external_media")


async def test_unexpected_context_no_allocation(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """A StasisStart from an unexpected context is ignored/hung up -- no
    bridge, no external-media channel, no media session, no registration."""
    order: list[str] = []
    controller, ari, sessions = _build_controller(make_config_file, order=order)
    _patch_start_gate(monkeypatch)

    await controller.on_stasis_start(_stasis_event(context="some-other-context"))

    assert controller.calls == {}
    assert sessions == []  # media session never opened
    assert ari.count("create_bridge") == 0
    assert ari.count("create_external_media") == 0
    assert ari.count("hangup", arg="chan-1") == 1


# --- (b)/(c) Single idempotent teardown (Task 2) --------------------------


async def test_channel_destroyed_closes_exactly_once(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """ChannelDestroyed -> call_session.close() exactly once (lifecycle
    _stopped True), bridge destroyed, external channel hung up, media
    session closed, registry empty."""
    controller, ari, sessions = _build_controller(make_config_file)
    _patch_start_gate(monkeypatch)
    await controller.on_stasis_start(_stasis_event())
    active_call = controller.calls["chan-1"]

    await controller.on_channel_destroyed(_channel_destroyed_event("chan-1"))

    assert active_call.call_session.lifecycle._stopped is True
    assert ari.count("destroy_bridge", arg="bridge-1") == 1
    assert ari.count("hangup", arg="ext-media-1") == 1
    assert sessions[0].closed is True
    assert controller.calls == {}

    # A second ChannelDestroyed for the same (now-unknown, already-removed)
    # channel id is a no-op, not a re-teardown.
    await controller.on_channel_destroyed(_channel_destroyed_event("chan-1"))
    assert ari.count("destroy_bridge", arg="bridge-1") == 1
    assert ari.count("hangup", arg="ext-media-1") == 1


async def test_simultaneous_close_calls_release_exactly_once(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """Simulated simultaneous hangup + timeout: two racing calls to
    ``_close_active_call`` for the SAME ``ActiveCall`` tear down exactly
    once (the ``ActiveCall.closed`` guard, T-11-05-01)."""
    controller, ari, sessions = _build_controller(make_config_file)
    _patch_start_gate(monkeypatch)
    await controller.on_stasis_start(_stasis_event())
    active_call = controller.calls["chan-1"]

    await asyncio.gather(
        controller._close_active_call(active_call, "hangup"),
        controller._close_active_call(active_call, "timeout"),
    )

    assert active_call.closed is True
    assert ari.count("destroy_bridge", arg="bridge-1") == 1
    assert ari.count("hangup", arg="ext-media-1") == 1
    assert sessions[0].closed is True


async def test_hard_timeout_hangs_up_sip_channel(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """R6/T-11-05-02: a hard-timeout release (``SessionLifecycle.release()``
    firing the composed ``on_released``) ARI-hangs-up the ORIGINAL SIP
    channel, not just the external-media channel/bridge -- never a silent
    open PSTN call."""
    controller, ari, sessions = _build_controller(make_config_file)
    _patch_start_gate(monkeypatch)
    await controller.on_stasis_start(_stasis_event())
    active_call = controller.calls["chan-1"]

    # Simulates the real D-02 wall-clock-cutoff release path (SessionLifecycle
    # ._service_timer -> _fire_wind_down -> ... -> release()) without waiting
    # on a real timer.
    await active_call.call_session.lifecycle.release()

    assert ari.count("hangup", arg="chan-1") == 1
    assert ari.count("hangup", arg="ext-media-1") == 1
    assert ari.count("destroy_bridge", arg="bridge-1") == 1
    assert active_call.closed is True
    assert controller.calls == {}


# --- (e) Quota-denied leaves no bridge (Task 2) ---------------------------


async def test_quota_denied_leaves_no_bridge(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """R6/T-11-05-03: a quota.start_gate rejection (e.g. concurrency-limit)
    leaves NO CallSession and no bridge/external-media channel/socket/
    registry entry -- the gate's own bridge is torn down, and the SIP
    channel is hung up."""
    controller, ari, sessions = _build_controller(make_config_file)
    _patch_start_gate(
        monkeypatch, error=quota.QuotaError(quota.ERROR_CONCURRENCY_LIMIT, "at capacity")
    )

    await controller.on_stasis_start(_stasis_event())

    assert controller.calls == {}
    assert ari.count("create_bridge") == 1
    assert ari.count("destroy_bridge", arg="bridge-1") == 1
    assert ari.count("create_external_media") == 1
    assert ari.count("hangup", arg="ext-media-1") == 1
    assert ari.count("hangup", arg="chan-1") == 1
    assert sessions[0].closed is True
