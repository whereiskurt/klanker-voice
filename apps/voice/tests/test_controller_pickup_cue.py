"""Pickup-cue wiring into ``AsteriskCallController`` (quick task 260713-m9n,
Task 3).

Hermetic and offline throughout, reusing ``test_telephony_lifecycle.py``'s
rig verbatim (same fakes, ``_build_controller``/``_gated_cfg``/
``_stasis_event`` helpers) -- mirrors how the existing controller tests are
already structured (test_telephony_controller.py's own docstring).

``play_pickup_cue`` is monkeypatched at module level (the same pattern
already used for ``quota.start_gate``/``greet_now``/``speak_goodbye`` in this
suite) so no real ringback tone / hey-clip load is exercised here -- that is
covered by ``test_pickup_cue_player.py``.

``TelephonyTransport`` is constructed locally inside ``on_stasis_start`` and
never exposed on ``ActiveCall``/``CallSession`` -- these tests capture the
real instance via a thin recording subclass swapped in for
``klanker_voice.telephony.controller.TelephonyTransport``, then fire
``on_client_connected`` directly (pipecat's ``BaseObject._call_event_handler``
schedules each registered handler as its own ``asyncio.create_task`` -- a
short sleep lets it run before assertions, mirroring this suite's own
fail-closed-path sleep pattern)."""

from __future__ import annotations

import asyncio
from typing import Any

from klanker_voice.telephony.transport import TelephonyTransport as RealTelephonyTransport

from tests.test_call_runtime import _gate_result, _quota_config, fake_aws, reset_active_count  # noqa: F401
from tests.test_telephony_lifecycle import (  # noqa: F401 -- stub_call_session_run is an autouse fixture
    FakeAriClient,
    FakeRtpMediaSession,
    _build_controller,
    _gated_cfg,
    _stasis_event,
    _telephony_cfg,
    stub_call_session_run,
)


def _capture_transports(monkeypatch) -> list[RealTelephonyTransport]:
    """Swap ``controller.TelephonyTransport`` for a recording subclass so a
    test can reach the real instance ``on_stasis_start`` constructs
    internally, without touching any real socket/RTP behavior."""
    captured: list[RealTelephonyTransport] = []

    class _CapturingTransport(RealTelephonyTransport):
        def __init__(self, *args: Any, **kwargs: Any) -> None:
            super().__init__(*args, **kwargs)
            captured.append(self)

    monkeypatch.setattr(
        "klanker_voice.telephony.controller.TelephonyTransport", _CapturingTransport
    )
    return captured


async def _fire_connected(transport: RealTelephonyTransport) -> None:
    await transport._call_event_handler("on_client_connected", None)
    await asyncio.sleep(0.05)  # let the scheduled handler task(s) run


# --- gated flow --------------------------------------------------------


async def test_gated_flow_plays_pickup_cue_on_client_connected(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """The cue plays during the gate window (pre-unlock) -- and does NOT
    trigger any real quota/greet side effects (D-05d preserved)."""
    cue_calls: list[Any] = []

    async def _spy_pickup_cue(worker):
        cue_calls.append(worker)

    monkeypatch.setattr("klanker_voice.telephony.controller.play_pickup_cue", _spy_pickup_cue)

    def _spy_start_gate(identity, **kwargs):
        raise AssertionError("quota.start_gate must not fire before unlock")

    monkeypatch.setattr("klanker_voice.telephony.controller.quota.start_gate", _spy_start_gate)

    greet_calls: list[Any] = []

    async def _spy_greet_now(worker, context):
        greet_calls.append((worker, context))

    monkeypatch.setattr("klanker_voice.telephony.controller.greet_now", _spy_greet_now)

    captured = _capture_transports(monkeypatch)
    controller, ari, sessions = _build_controller(make_config_file, telephony_cfg=_gated_cfg())

    await controller.on_stasis_start(_stasis_event())
    active_call = controller.calls["chan-1"]

    await _fire_connected(captured[0])

    assert cue_calls == [active_call.call_session.worker]
    assert greet_calls == []  # the cue never triggers a greet on its own


async def test_gated_flow_cue_plus_dtmf_unlock_still_greets(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """The cue does not replace or suppress the real unlock -> greet_now
    path -- a DTMF unlock after the cue has played still greets exactly as
    before this plan."""
    cue_calls: list[Any] = []

    async def _spy_pickup_cue(worker):
        cue_calls.append(worker)

    monkeypatch.setattr("klanker_voice.telephony.controller.play_pickup_cue", _spy_pickup_cue)

    def _recording_start_gate(identity, **kwargs):
        return _gate_result()

    monkeypatch.setattr(
        "klanker_voice.telephony.controller.quota.start_gate", _recording_start_gate
    )

    greet_calls: list[Any] = []

    async def _spy_greet_now(worker, context):
        greet_calls.append((worker, context))

    monkeypatch.setattr("klanker_voice.telephony.controller.greet_now", _spy_greet_now)

    captured = _capture_transports(monkeypatch)
    controller, ari, sessions = _build_controller(
        make_config_file, telephony_cfg=_gated_cfg(), access_pin="4242"
    )

    await controller.on_stasis_start(_stasis_event())
    active_call = controller.calls["chan-1"]

    await _fire_connected(captured[0])
    assert cue_calls == [active_call.call_session.worker]
    assert greet_calls == []  # not unlocked yet

    for digit in "4242":
        await controller.on_channel_dtmf_received(
            {"type": "ChannelDtmfReceived", "channel": {"id": "chan-1"}, "digit": digit}
        )

    assert active_call.gate.unlocked is True
    assert len(greet_calls) == 1


# --- ungated (test/dev escape hatch) flow -------------------------------


async def test_ungated_flow_plays_pickup_cue_on_client_connected(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """The cue also plays on the ungated (require_gate=False) path -- and
    the existing immediate quota-grant flow is unaffected."""
    cue_calls: list[Any] = []

    async def _spy_pickup_cue(worker):
        cue_calls.append(worker)

    monkeypatch.setattr("klanker_voice.telephony.controller.play_pickup_cue", _spy_pickup_cue)
    monkeypatch.setattr(
        "klanker_voice.telephony.controller.quota.start_gate",
        lambda identity, **kwargs: _gate_result(),
    )

    captured = _capture_transports(monkeypatch)
    controller, ari, sessions = _build_controller(
        make_config_file, telephony_cfg=_telephony_cfg()
    )

    await controller.on_stasis_start(_stasis_event())
    active_call = controller.calls["chan-1"]

    await _fire_connected(captured[0])

    assert cue_calls == [active_call.call_session.worker]
    assert active_call.call_session is not None  # the immediate grant still happened
