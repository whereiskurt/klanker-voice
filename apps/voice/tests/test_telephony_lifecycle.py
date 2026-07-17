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

    def __init__(
        self, order: list[str] | None = None, channel_vars: dict[str, str] | None = None
    ) -> None:
        self.calls: list[tuple[str, tuple, dict]] = []
        self._order = order
        self._ext_counter = 0
        self._bridge_counter = 0
        #: variable name -> value, served by get_channel_var for every channel
        #: (tests are single-call). Empty ⇒ get_channel_var returns "" (the
        #: real ARI behavior for an unset var), so the dialed DID is unknown
        #: and per-DID SMS falls back to the legacy pool -- byte-identical to
        #: the pre-per-DID tests.
        self.channel_vars: dict[str, str] = dict(channel_vars or {})

    def _record(self, name: str, args: tuple = (), kwargs: dict | None = None) -> None:
        self.calls.append((name, args, kwargs or {}))
        if self._order is not None:
            self._order.append(name)

    async def answer(self, channel_id: str) -> None:
        self._record("answer", (channel_id,))

    async def get_channel_var(self, channel_id: str, variable: str) -> str:
        self._record("get_channel_var", (channel_id, variable))
        return self.channel_vars.get(variable, "")

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
    # Phase 11 Plan 06 (D-05): this file's existing tests exercise the
    # bridge/teardown PLUMBING (ARI allocate/close, idempotent teardown,
    # hard-timeout hangup, quota-denied-leaves-no-bridge) -- not the §24
    # gate itself. `require_gate=False` preserves Plan 05's byte-for-byte
    # immediate-grant behavior for those tests unchanged; the gated flow
    # (require_gate=True, the production default) gets its own dedicated
    # tests below.
    base = dict(
        enabled=True, max_concurrent_calls=1, unlock_tier_id="kph-tier", require_gate=False
    )
    base.update(overrides)
    return TelephonyConfig(**base)


def _build_controller(
    make_config_file,
    *,
    order: list[str] | None = None,
    telephony_cfg: TelephonyConfig | None = None,
    quota_cfg: quota.QuotaConfig | None = None,
    access_pin: str | None = None,
    passphrase_words: frozenset[str] | None = None,
    announcement_codes: dict[str, Any] | None = None,
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
        quota_cfg or _quota_config(reconnect_grace_seconds=3600.0),
        telephony_cfg or _telephony_cfg(),
        media_session_opener=opener,
        # Hermetic: explicit "" / empty frozenset rather than the
        # controller's own env-var fallback, so these tests never
        # accidentally read a real TELEPHONY_ACCESS_PIN/_PASSPHRASE_WORDS
        # from the environment.
        access_pin=access_pin if access_pin is not None else "",
        passphrase_words=passphrase_words if passphrase_words is not None else frozenset(),
        # Quick task 260716-1g0: explicit injection seam (mirrors access_pin/
        # passphrase_words) -- None means "resolve from telephony_cfg.
        # announcements + os.environ" (the controller's own default), an
        # explicit dict hermetically arms trigger codes without touching
        # the real environment.
        announcement_codes=announcement_codes,
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


async def test_session_max_hard_stop_hangs_up_even_if_heartbeat_release_fails(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """telephony-cap-no-hangup: the §24 session cap must HANG UP at session_max,
    not merely warn. This drives the REAL ``SessionLifecycle._service_timer``
    to a tiny session_max through the whole live wind-down chain
    (``_fire_wind_down`` -> ``_on_stop`` -> ``runner.cancel`` ->
    ``CallSession.run`` finally -> ``lifecycle.release()`` -> composed
    ``on_released`` -> ARI hangup) -- with the best-effort
    ``quota.release_heartbeat`` RAISING (models the live telephony-edge failure
    mode: a scoped task-role IAM gap on the usage table, or a transient
    DynamoDB error). Before the fix, that raise aborted ``release()`` before
    ``on_released``, so the PSTN line was never hung up and the pipeline ran on
    -- exactly the reported "warns at 2:30 but never hangs up at 3:00" bug.

    Overrides the module-level ``stub_call_session_run`` with a FAITHFUL
    ``CallSession.run`` bracket (start -> block until the runner is cancelled ->
    ``finally: stop()``), so the real ``on_stop -> release -> on_released`` link
    is exercised offline without a live provider round trip.
    """

    async def _faithful_run(self: CallSession) -> None:
        await self.lifecycle.start()
        try:
            await self.runner._shutdown_event.wait()
        finally:
            await self.lifecycle.stop()

    monkeypatch.setattr(CallSession, "run", _faithful_run)

    # Neutralize TTS/greet frame emission (no live pipeline is running here).
    async def _noop(*_a, **_k):
        return None

    monkeypatch.setattr("klanker_voice.call_runtime.speak_goodbye", _noop)
    monkeypatch.setattr("klanker_voice.telephony.controller.greet_now", _noop)
    monkeypatch.setattr(
        "klanker_voice.session.quota.record_tick",
        lambda **_k: quota.TickResult(False, False),
    )

    # THE failure mode under test: the best-effort heartbeat release raises.
    def _boom(*_a, **_k):
        raise RuntimeError("dynamodb release_heartbeat failed (best-effort)")

    monkeypatch.setattr("klanker_voice.session.quota.release_heartbeat", _boom)

    tiny_tier = quota.Tier(
        tier_id="pstn-public-tier",
        session_max_seconds=0.12,
        period_max_seconds=600,
        max_concurrent=5,
    )
    monkeypatch.setattr(
        "klanker_voice.telephony.controller.quota.start_gate",
        lambda identity, **kwargs: _gate_result(
            tier=tiny_tier, session_max_seconds=0.12, bypass_accounting=False
        ),
    )

    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(),
        quota_cfg=_quota_config(
            winddown_warning_seconds=0.05,
            goodbye_grace_seconds=0.01,
            user_silence_timeout=300.0,
            reconnect_grace_seconds=300.0,
            heartbeat_renew_interval=300.0,
        ),
        access_pin="4242",
    )

    await controller.on_stasis_start(_stasis_event())
    active_call = controller.calls["chan-1"]

    # Unlock (DTMF) -> upgrade_from_bypass starts the REAL service timer.
    for digit in "4242":
        await controller.on_channel_dtmf_received(
            {"type": "ChannelDtmfReceived", "channel": {"id": "chan-1"}, "digit": digit}
        )
    assert active_call.gate.unlocked is True

    # Let the real timer fire the warning (~0.05s) then the hard stop (~0.12s),
    # and the whole wind-down chain settle.
    await asyncio.sleep(1.0)

    assert ari.count("hangup", arg="chan-1") == 1, (
        f"session-max hard stop never hung up the SIP channel (best-effort "
        f"heartbeat-release failure stranded the teardown). ARI calls: {ari.names()}"
    )
    assert ari.count("destroy_bridge", arg="bridge-1") == 1
    assert controller.calls == {}


async def test_session_max_hard_stop_hangs_up_even_if_goodbye_leg_raises(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """telephony-cap-no-hangup (CORRECTED root cause, faithful repro): the §24
    session cap must HANG UP at session_max even if the best-effort spoken
    wind-down leg fails. Unlike the heartbeat-release variant above, this drives
    the ACTUAL reproduced strand: the REAL ``SessionLifecycle._service_timer``
    fires the warning, then at session_max ``_fire_wind_down`` -> ``_on_stop``,
    whose ``speak_goodbye`` RAISES (a worker in a bad state at the cap, a
    transport hiccup — the live call's leaf trigger).

    Before the fix, that raise propagated out of ``_on_stop`` ->
    ``_fire_wind_down`` (``_wind_down_fired`` already True) -> ``_service_timer``
    (catches only ``CancelledError``): the timer task died BEFORE
    ``runner.cancel``, so ``CallSession.run`` never unblocked, ``release`` /
    ``on_released`` never ran, and the SIP line stayed open past session_max —
    exactly "warns at 2:30 but never hangs up at 3:00", with the whole
    hard-stop path silent in CloudWatch. ``_on_stop`` now runs ``runner.cancel``
    in a ``finally`` so the hard close ALWAYS fires; ``_fire_wind_down`` swallows
    the goodbye exception so the timer task ends cleanly. The warning still fires
    (this asserts the soft-warning path is NOT regressed)."""

    async def _faithful_run(self: CallSession) -> None:
        await self.lifecycle.start()
        try:
            await self.runner._shutdown_event.wait()
        finally:
            await self.lifecycle.stop()

    monkeypatch.setattr(CallSession, "run", _faithful_run)

    warnings_fired: list[str] = []

    async def _spy_warning(worker, context, copy):
        warnings_fired.append(copy)

    # THE failure mode under test: the spoken goodbye leg raises at session_max.
    async def _boom_goodbye(worker, copy):
        raise RuntimeError("speak_goodbye queue_frames failed at session_max")

    monkeypatch.setattr("klanker_voice.call_runtime.inject_warning_instruction", _spy_warning)
    monkeypatch.setattr("klanker_voice.call_runtime.speak_goodbye", _boom_goodbye)

    async def _noop(*_a, **_k):
        return None

    monkeypatch.setattr("klanker_voice.telephony.controller.greet_now", _noop)
    monkeypatch.setattr(
        "klanker_voice.session.quota.record_tick",
        lambda **_k: quota.TickResult(False, False),
    )

    tiny_tier = quota.Tier(
        tier_id="pstn-public-tier",
        session_max_seconds=0.12,
        period_max_seconds=600,
        max_concurrent=5,
    )
    monkeypatch.setattr(
        "klanker_voice.telephony.controller.quota.start_gate",
        lambda identity, **kwargs: _gate_result(
            tier=tiny_tier, session_max_seconds=0.12, bypass_accounting=False
        ),
    )

    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(),
        quota_cfg=_quota_config(
            winddown_warning_seconds=0.05,
            goodbye_grace_seconds=0.01,
            user_silence_timeout=300.0,
            reconnect_grace_seconds=300.0,
            heartbeat_renew_interval=300.0,
        ),
        access_pin="4242",
    )

    await controller.on_stasis_start(_stasis_event())
    active_call = controller.calls["chan-1"]

    # Let the spawned CallSession.run task reach lifecycle.start() (bypass=True,
    # timers skipped) BEFORE unlocking — mirrors prod, where the caller DTMFs
    # seconds after answer, long after start() has run. (Unlocking in the same
    # microsecond would race start() past upgrade_from_bypass and spawn a
    # duplicate timer — a test-only artifact, never the prod ordering.)
    await asyncio.sleep(0.02)

    for digit in "4242":
        await controller.on_channel_dtmf_received(
            {"type": "ChannelDtmfReceived", "channel": {"id": "chan-1"}, "digit": digit}
        )
    assert active_call.gate.unlocked is True

    await asyncio.sleep(1.0)

    # The soft warning still fires exactly once (not regressed) ...
    assert len(warnings_fired) == 1, f"soft winddown warning regressed: {warnings_fired}"
    # ... AND the hard stop still hangs up the SIP channel despite the goodbye raise.
    assert ari.count("hangup", arg="chan-1") == 1, (
        f"session-max hard stop never hung up the SIP channel — the failing "
        f"spoken-goodbye leg stranded the teardown. ARI calls: {ari.names()}"
    )
    assert ari.count("destroy_bridge", arg="bridge-1") == 1
    assert controller.calls == {}
    assert active_call.call_session.lifecycle._stopped is True


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


# --- Phase 11 Plan 06: the §24 silent answer-gate (D-05, Task 3) -----------
#
# `require_gate=True` (the production default) -- unlike every test above,
# which pins `require_gate=False` via `_telephony_cfg()`'s own default.
# `stub_call_session_run` (autouse, module-level) still replaces
# `CallSession.run` with a version that only brackets `lifecycle.start()`
# (never `runner.run()`), so the gate's own `GateProcessor.process_frame`
# is driven DIRECTLY in these tests (never through a live pipeline) --
# `_finish_stasis_start_gated` already calls `gate.start_timer()` itself
# regardless of whether the real pipeline ever runs a StartFrame through it.


def _gated_cfg(**overrides) -> TelephonyConfig:
    base = dict(gate_window_seconds=60.0, gate_mode="either")
    base.update(overrides)
    return _telephony_cfg(require_gate=True, **base)


async def test_gated_stasis_start_stays_locked_no_quota_no_greet(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """D-05d: on answer, the gated flow builds a CallSession immediately
    (bypass_accounting=True placeholder) but calls NEITHER
    ``quota.start_gate`` NOR ``greet_now`` until unlock."""
    start_gate_calls: list[Any] = []

    def _spy_start_gate(identity, **kwargs):
        start_gate_calls.append(identity)
        raise AssertionError("quota.start_gate must not be called before unlock")

    monkeypatch.setattr("klanker_voice.telephony.controller.quota.start_gate", _spy_start_gate)
    greet_calls: list[Any] = []

    async def _spy_greet_now(worker, context):
        greet_calls.append((worker, context))

    monkeypatch.setattr("klanker_voice.telephony.controller.greet_now", _spy_greet_now)

    controller, ari, sessions = _build_controller(
        make_config_file, telephony_cfg=_gated_cfg()
    )

    await controller.on_stasis_start(_stasis_event())

    active_call = controller.calls["chan-1"]
    assert active_call.gate is not None
    assert active_call.gate.unlocked is False
    assert active_call.call_session.lifecycle.bypass_accounting is True
    assert start_gate_calls == []
    assert greet_calls == []


async def test_gated_passphrase_unlock_grants_tier_and_greets(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """Task 3 acceptance: locked -> passphrase unlock -> quota.start_gate
    called with SessionIdentity(tier_id=unlock_tier_id) -> greet_now
    fired."""
    from pipecat.frames.frames import TranscriptionFrame
    from pipecat.processors.frame_processor import FrameDirection

    identities: list[Any] = []

    def _recording_start_gate(identity, **kwargs):
        identities.append(identity)
        return _gate_result()

    monkeypatch.setattr(
        "klanker_voice.telephony.controller.quota.start_gate", _recording_start_gate
    )
    greet_calls: list[Any] = []

    async def _spy_greet_now(worker, context):
        greet_calls.append((worker, context))

    monkeypatch.setattr("klanker_voice.telephony.controller.greet_now", _spy_greet_now)

    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(unlock_tier_id="kph-tier"),
        passphrase_words=frozenset({"purple", "falcon", "midnight", "compass"}),
    )

    await controller.on_stasis_start(_stasis_event(caller_number="1001"))
    active_call = controller.calls["chan-1"]
    gate = active_call.gate

    await gate.process_frame(
        TranscriptionFrame(text="the midnight compass", user_id="", timestamp=""),
        FrameDirection.DOWNSTREAM,
    )
    await gate.process_frame(
        TranscriptionFrame(text="found a purple falcon", user_id="", timestamp=""),
        FrameDirection.DOWNSTREAM,
    )

    assert gate.unlocked is True
    assert len(identities) == 1
    assert identities[0].tier_id == "kph-tier"
    assert identities[0].sub == "tel:1001"
    assert identities[0].bypass_accounting is False
    assert len(greet_calls) == 1
    assert active_call.call_session.lifecycle.bypass_accounting is False


async def test_gated_dtmf_unlock_never_touches_pipeline(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """D-05b: DTMF unlock flips the gate from the controller layer -- the
    PIN never reaches the pipeline/frame-stream/LLM (this test never once
    calls ``gate.process_frame``)."""
    _patch_start_gate(monkeypatch)
    greet_calls: list[Any] = []

    async def _spy_greet_now(worker, context):
        greet_calls.append((worker, context))

    monkeypatch.setattr("klanker_voice.telephony.controller.greet_now", _spy_greet_now)

    controller, ari, sessions = _build_controller(
        make_config_file, telephony_cfg=_gated_cfg(), access_pin="4242"
    )

    await controller.on_stasis_start(_stasis_event())
    active_call = controller.calls["chan-1"]
    gate = active_call.gate

    for digit in "999999":  # noise before the real PIN -- early-exit tail match
        await controller.on_channel_dtmf_received(
            {"type": "ChannelDtmfReceived", "channel": {"id": "chan-1"}, "digit": digit}
        )
    assert gate.unlocked is False  # not yet -- only noise so far

    for digit in "4242":
        await controller.on_channel_dtmf_received(
            {"type": "ChannelDtmfReceived", "channel": {"id": "chan-1"}, "digit": digit}
        )

    assert gate.unlocked is True
    assert len(greet_calls) == 1


async def test_gate_mode_dtmf_only_disables_passphrase_factor(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    from pipecat.frames.frames import TranscriptionFrame
    from pipecat.processors.frame_processor import FrameDirection

    _patch_start_gate(monkeypatch)
    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(gate_mode="dtmf"),
        passphrase_words=frozenset({"purple", "falcon", "midnight", "compass"}),
    )

    await controller.on_stasis_start(_stasis_event())
    active_call = controller.calls["chan-1"]
    gate = active_call.gate

    await gate.process_frame(
        TranscriptionFrame(
            text="the midnight compass found a purple falcon", user_id="", timestamp=""
        ),
        FrameDirection.DOWNSTREAM,
    )

    assert gate.unlocked is False  # gate_mode="dtmf" -- the passphrase never unlocks


async def test_gate_fail_closed_on_window_expiry_no_greet(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """Task 3 acceptance: fail-closed path -> speak_goodbye + hangup, no
    greet, no LLM turn."""
    greet_calls: list[Any] = []
    goodbye_calls: list[Any] = []

    async def _spy_greet_now(worker, context):
        greet_calls.append((worker, context))

    async def _spy_speak_goodbye(worker, copy):
        goodbye_calls.append(copy)

    monkeypatch.setattr("klanker_voice.telephony.controller.greet_now", _spy_greet_now)
    monkeypatch.setattr("klanker_voice.telephony.controller.speak_goodbye", _spy_speak_goodbye)

    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(gate_window_seconds=0.05),
        quota_cfg=_quota_config(reconnect_grace_seconds=3600.0, goodbye_grace_seconds=0.01),
    )

    await controller.on_stasis_start(_stasis_event())
    assert "chan-1" in controller.calls

    await asyncio.sleep(0.3)

    assert len(goodbye_calls) == 1
    assert greet_calls == []
    assert ari.count("hangup", arg="chan-1") == 1
    assert ari.count("destroy_bridge", arg="bridge-1") == 1
    assert controller.calls == {}


async def test_gate_fail_closed_on_quota_denied_after_unlock(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """R6 quota-denied, extended to the post-unlock case: the gate unlocks
    (DTMF) but quota.start_gate rejects -- fail-closed goodbye + hangup,
    never a greet."""
    greet_calls: list[Any] = []
    goodbye_calls: list[Any] = []

    async def _spy_greet_now(worker, context):
        greet_calls.append((worker, context))

    async def _spy_speak_goodbye(worker, copy):
        goodbye_calls.append(copy)

    monkeypatch.setattr("klanker_voice.telephony.controller.greet_now", _spy_greet_now)
    monkeypatch.setattr("klanker_voice.telephony.controller.speak_goodbye", _spy_speak_goodbye)
    _patch_start_gate(
        monkeypatch, error=quota.QuotaError(quota.ERROR_CONCURRENCY_LIMIT, "at capacity")
    )

    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(),
        quota_cfg=_quota_config(reconnect_grace_seconds=3600.0, goodbye_grace_seconds=0.01),
        access_pin="4242",
    )

    await controller.on_stasis_start(_stasis_event())
    active_call = controller.calls["chan-1"]

    for digit in "4242":
        await controller.on_channel_dtmf_received(
            {"type": "ChannelDtmfReceived", "channel": {"id": "chan-1"}, "digit": digit}
        )

    assert active_call.gate.unlocked is True  # the FACTOR matched...
    assert greet_calls == []  # ...but quota denied it, so no greeting ever fires
    assert len(goodbye_calls) == 1
    assert ari.count("hangup", arg="chan-1") == 1
    assert controller.calls == {}


# --- Phase 15 Plan 03 (LEDG-01): PSTN ledger capture -----------------------


async def test_mint_tier_from_caller_id_returns_tier_and_sub_tuple(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """_mint_tier_from_caller_id now returns (tier_id, sub) on success --
    the mint token's sub is the ONLY place a PSTN caller's raw access code
    is recoverable (LEDG-01). Every failure path still returns the
    None-equivalent tuple, never raises."""
    from klanker_voice.auth import SessionIdentity

    async def _fake_fetch_tel_token(url, headers):
        return "minted-token"

    def _fake_validate(token):
        assert token == "minted-token"
        return SessionIdentity(
            sub="anon:kphdemo123:uuid-1", tier_id="kph-tier", group=None, bypass_accounting=False
        )

    monkeypatch.setattr(
        "klanker_voice.telephony.controller._fetch_tel_token", _fake_fetch_tel_token
    )
    monkeypatch.setattr(
        "klanker_voice.telephony.controller.validate_access_token", _fake_validate
    )

    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(tel_mint_url="https://auth.klankermaker.ai/use1/tel"),
    )

    tier_id, sub = await controller._mint_tier_from_caller_id("+14165551234")
    assert tier_id == "kph-tier"
    assert sub == "anon:kphdemo123:uuid-1"

    # Failure paths -- no caller id -- never raise, return the
    # None-equivalent tuple, and never call the network.
    assert await controller._mint_tier_from_caller_id("") == (None, None)


async def test_mint_tier_from_caller_id_failure_paths_return_none_tuple(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """A failed /tel fetch and a token that fails offline validation both
    fail closed identically -- (None, None), never raises."""
    from klanker_voice.auth import AuthError

    async def _fake_fetch_tel_token_none(url, headers):
        return None  # uniform 404/timeout/transport-error shape

    monkeypatch.setattr(
        "klanker_voice.telephony.controller._fetch_tel_token", _fake_fetch_tel_token_none
    )
    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(tel_mint_url="https://auth.klankermaker.ai/use1/tel"),
    )
    assert await controller._mint_tier_from_caller_id("+14165551234") == (None, None)

    async def _fake_fetch_tel_token_ok(url, headers):
        return "minted-token"

    def _fake_validate_raises(token):
        raise AuthError("bad token")

    monkeypatch.setattr(
        "klanker_voice.telephony.controller._fetch_tel_token", _fake_fetch_tel_token_ok
    )
    monkeypatch.setattr(
        "klanker_voice.telephony.controller.validate_access_token", _fake_validate_raises
    )
    assert await controller._mint_tier_from_caller_id("+14165551234") == (None, None)


async def test_gated_mint_unlock_ledger_record_has_caller_id_did_and_code_hash(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """LEDG-01: a telephony CallIdentity carries caller_id + did and email
    stays None; the session's writer starts DISABLED while the §24 gate is
    locked (nothing captured), and is enabled at unlock with caller_id/did
    already populated and a non-null code_hash derived from the mint sub."""
    monkeypatch.setenv("KMV_LEDGER_SALT", "test-salt")
    from klanker_voice.auth import SessionIdentity

    monkeypatch.setattr(
        "klanker_voice.telephony.controller.quota.start_gate", lambda identity, **kwargs: _gate_result()
    )

    async def _fake_fetch_tel_token(url, headers):
        return "minted-token"

    def _fake_validate(token):
        return SessionIdentity(
            sub="anon:kphdemo123:uuid-1", tier_id="kph-tier", group=None, bypass_accounting=False
        )

    monkeypatch.setattr(
        "klanker_voice.telephony.controller._fetch_tel_token", _fake_fetch_tel_token
    )
    monkeypatch.setattr(
        "klanker_voice.telephony.controller.validate_access_token", _fake_validate
    )

    async def _spy_greet_now(worker, context):
        return None

    monkeypatch.setattr("klanker_voice.telephony.controller.greet_now", _spy_greet_now)

    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(
            tel_mint_url="https://auth.klankermaker.ai/use1/tel", gate_mode="dtmf"
        ),
        access_pin="4242",
    )

    await controller.on_stasis_start(_stasis_event(caller_number="4165551234", exten="1000"))
    active_call = controller.calls["chan-1"]
    writer = active_call.call_session.writer
    assert writer is not None

    # Still locked: the writer is disabled -- nothing is captured (T-15-03-01).
    assert writer.enabled is False
    await writer.append(role="user", text="should never appear")
    assert writer._buffer == []

    for digit in "4242":
        await controller.on_channel_dtmf_received(
            {"type": "ChannelDtmfReceived", "channel": {"id": "chan-1"}, "digit": digit}
        )

    assert active_call.gate.unlocked is True
    assert writer.enabled is True
    assert writer.caller_id == "+14165551234"
    assert writer.did == "1000"
    assert writer.email is None

    await writer.append(role="assistant", text="hi, you're through")
    record = writer._buffer[-1]
    assert record["caller_id"] == "+14165551234"
    assert record["did"] == "1000"
    assert record["email"] is None
    assert record["code_hash"] is not None


async def test_gated_writer_disabled_when_mint_unconfigured(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """When telephony.tel_mint_url is empty (legacy static-tier grant), the
    writer still starts disabled while locked and enables at unlock, but
    carries no code (no mint sub -- code_hash stays null)."""
    _patch_start_gate(monkeypatch)

    async def _spy_greet_now(worker, context):
        return None

    monkeypatch.setattr("klanker_voice.telephony.controller.greet_now", _spy_greet_now)

    controller, ari, sessions = _build_controller(
        make_config_file, telephony_cfg=_gated_cfg(unlock_tier_id="kph-tier"), access_pin="4242"
    )

    await controller.on_stasis_start(_stasis_event(caller_number="1001"))
    active_call = controller.calls["chan-1"]
    writer = active_call.call_session.writer
    assert writer.enabled is False

    for digit in "4242":
        await controller.on_channel_dtmf_received(
            {"type": "ChannelDtmfReceived", "channel": {"id": "chan-1"}, "digit": digit}
        )

    assert writer.enabled is True
    await writer.append(role="user", text="hello")
    assert writer._buffer[-1]["code_hash"] is None
