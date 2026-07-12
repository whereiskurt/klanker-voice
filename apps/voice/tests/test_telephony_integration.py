"""Deterministic fake-media integration test (Phase 11 Plan 07, D-07).

The CI-required artifact for D-07's "SIP client -> Asterisk -> fake Klanker
media transport" split: no real Asterisk, no real SIPp subprocess, no real
Deepgram/Anthropic/ElevenLabs -- drives ``AsteriskCallController`` directly
at the controller layer through one coherent, end-to-end call scenario per
test (StasisStart -> §24 gate unlock (passphrase or DTMF) -> real
``quota.start_gate`` grant + greet -> ChannelDestroyed -> the single
idempotent teardown), reusing ``FakeAriClient``/``FakeRtpMediaSession`` and
the ``_build_controller``/``_stasis_event``/``_channel_destroyed_event``
helpers verbatim from ``test_telephony_lifecycle.py`` (same rig, same
hermeticity guarantees -- see that module's own docstring).

This file is NOT a re-proof of every granular assertion already covered by
``test_telephony_lifecycle.py`` (bridge/teardown plumbing, exactly-once
locking, hard-timeout hangup) -- it proves the WHOLE call lifecycle as one
connected scenario, matching this plan's own acceptance criteria:

- passphrase-unlock -> ``CallSession`` construction + greet path
- DTMF-unlock -> the PIN never touches the pipeline/frame stream
- fail-closed (gate-window expiry) -> goodbye + hangup, no greet, no real
  ``quota.start_gate`` call
- ``ChannelDestroyed`` -> exactly-once teardown, empty registry

Synthetic ``TranscriptionFrame``s stand in for the STT output a real SIPp
pcap replay (``asterisk/sipp/gate-pass.xml``) -> Asterisk RTP -> Deepgram
round trip would eventually produce -- 11-RESEARCH.md R4 documents this
substitution explicitly as the CI-automatable boundary: the literal
greeting-not-clipped audio-quality judgment and a live Deepgram
transcription of a live spoken passphrase both stay OUT of CI (D-07),
covered instead by the documented manual §19-C softphone proof
(``asterisk/README.md``).

The one Asterisk/SIPp-dependent case in this file
(``test_docker_compose_sipp_profile_is_valid``) only shells out to
``docker compose config`` (no pull, no build, no daemon required) and is
skipped whenever the ``docker`` binary isn't on ``PATH`` -- it never blocks
CI green, and never requires ``docker compose up``/a live Asterisk
container (that full local run stays manual, see
``asterisk/sipp/fixtures/README.md``).
"""

from __future__ import annotations

import asyncio
import shutil
import subprocess
from pathlib import Path

import pytest

from klanker_voice.telephony.config import TelephonyConfig

from tests.test_call_runtime import _gate_result, _quota_config, fake_aws, reset_active_count  # noqa: F401
from tests.test_telephony_lifecycle import (
    _build_controller,
    _channel_destroyed_event,
    _stasis_event,
    stub_call_session_run,  # noqa: F401 - autouse fixture (patches CallSession.run)
)

ASTERISK_DIR = Path(__file__).resolve().parent.parent / "asterisk"


def _gated_cfg(**overrides) -> TelephonyConfig:
    base = dict(
        enabled=True,
        max_concurrent_calls=1,
        unlock_tier_id="kph-tier",
        require_gate=True,
        gate_mode="either",
        gate_window_seconds=60.0,
    )
    base.update(overrides)
    return TelephonyConfig(**base)


def _stub_release_heartbeat(monkeypatch) -> list:
    """Stub ``quota.release_heartbeat`` (patched where ``session.py``
    resolves it) so a genuinely-unlocked (``bypass_accounting=False``)
    ``SessionLifecycle.release()`` never makes a real DynamoDB call.
    Unlike every ``test_telephony_lifecycle.py`` gated test (which never
    combines a real unlock with a close in the same test), this file's
    happy-path/DTMF scenarios do both -- ``fake_aws`` alone only covers
    boto3 CloudWatch/ECS, not this direct ``quota.release_heartbeat`` call.
    Returns the list of recorded ``(user_id, session_id)`` calls."""
    calls: list = []

    def _fake_release_heartbeat(user_id: str, session_id: str) -> None:
        calls.append((user_id, session_id))

    monkeypatch.setattr(
        "klanker_voice.session.quota.release_heartbeat", _fake_release_heartbeat
    )
    return calls


# --- Deterministic fake-media tier (always runs, no keys, no docker) ------


async def test_passphrase_unlock_greets_then_channel_destroyed_tears_down_once(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """The full D-07 happy-path scenario: SIP INVITE (StasisStart) -> gated
    silence -> the caller's passphrase (synthetic ``TranscriptionFrame``s,
    standing in for the SIPp pcap -> Asterisk -> Deepgram round trip) ->
    real ``quota.start_gate`` grant -> ``greet_now`` -> BYE
    (ChannelDestroyed) -> the single idempotent teardown, empty registry."""
    from pipecat.frames.frames import TranscriptionFrame
    from pipecat.processors.frame_processor import FrameDirection

    identities: list = []

    def _recording_start_gate(identity, **kwargs):
        identities.append(identity)
        return _gate_result()

    monkeypatch.setattr(
        "klanker_voice.telephony.controller.quota.start_gate", _recording_start_gate
    )
    greet_calls: list = []

    async def _spy_greet_now(worker, context):
        greet_calls.append((worker, context))

    monkeypatch.setattr("klanker_voice.telephony.controller.greet_now", _spy_greet_now)
    heartbeat_releases = _stub_release_heartbeat(monkeypatch)

    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(),
        passphrase_words=frozenset({"purple", "falcon", "midnight", "compass"}),
    )

    # SIP INVITE -> Asterisk Answer() -> Stasis(klanker) -> StasisStart.
    await controller.on_stasis_start(_stasis_event(caller_number="1001"))
    active_call = controller.calls["chan-1"]
    assert active_call.gate.unlocked is False  # silent, per §24 -- no greeting yet
    assert greet_calls == []
    assert identities == []

    # The caller speaks the 4-word passphrase (stands in for the SIPp
    # pcap-replayed audio -> Asterisk RTP -> Deepgram STT round trip -- the
    # one piece D-07 documents as not CI-automatable without live keys).
    await active_call.gate.process_frame(
        TranscriptionFrame(text="purple falcon midnight compass", user_id="", timestamp=""),
        FrameDirection.DOWNSTREAM,
    )

    assert active_call.gate.unlocked is True
    assert len(identities) == 1
    assert identities[0].tier_id == "kph-tier"
    assert identities[0].bypass_accounting is False
    assert len(greet_calls) == 1  # the greeting fires on unlock, not on answer
    assert active_call.call_session.lifecycle.bypass_accounting is False

    # BYE -> ChannelDestroyed -> the single idempotent teardown.
    await controller.on_channel_destroyed(_channel_destroyed_event("chan-1"))

    assert controller.calls == {}
    assert ari.count("destroy_bridge", arg="bridge-1") == 1
    assert ari.count("hangup", arg="ext-media-1") == 1
    assert sessions[0].closed is True
    assert len(heartbeat_releases) == 1  # the real (unlocked) lease was released exactly once

    # A second ChannelDestroyed (e.g. a duplicate/late ARI event) is a
    # harmless no-op, never a re-teardown (T-11-05-01).
    await controller.on_channel_destroyed(_channel_destroyed_event("chan-1"))
    assert ari.count("destroy_bridge", arg="bridge-1") == 1


async def test_dtmf_unlock_never_touches_pipeline_then_teardown(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """D-05b: the DTMF PIN path unlocks entirely at the controller/ARI
    layer (this test never once calls ``gate.process_frame``) -- greet
    still fires, and the call still tears down exactly once on
    ChannelDestroyed."""

    def _fake_start_gate(identity, **kwargs):
        return _gate_result()

    monkeypatch.setattr("klanker_voice.telephony.controller.quota.start_gate", _fake_start_gate)
    greet_calls: list = []

    async def _spy_greet_now(worker, context):
        greet_calls.append((worker, context))

    monkeypatch.setattr("klanker_voice.telephony.controller.greet_now", _spy_greet_now)
    _stub_release_heartbeat(monkeypatch)

    controller, ari, sessions = _build_controller(
        make_config_file, telephony_cfg=_gated_cfg(), access_pin="4242"
    )

    await controller.on_stasis_start(_stasis_event())
    active_call = controller.calls["chan-1"]

    for digit in "999999" + "4242":  # noise before the real PIN -- early-exit tail match
        await controller.on_channel_dtmf_received(
            {"type": "ChannelDtmfReceived", "channel": {"id": "chan-1"}, "digit": digit}
        )

    assert active_call.gate.unlocked is True
    assert len(greet_calls) == 1

    await controller.on_channel_destroyed(_channel_destroyed_event("chan-1"))
    assert controller.calls == {}
    assert sessions[0].closed is True


async def test_fail_closed_on_gate_window_expiry_no_callsession_ever_granted(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """D-05d fail-closed: the caller never unlocks within
    ``gate_window_seconds`` -> deterministic goodbye (bypasses the LLM) +
    hangup + the single teardown -- no greet, no real ``quota.start_gate``
    call, no leaked bridge/registry entry."""
    greet_calls: list = []
    goodbye_calls: list = []

    async def _spy_greet_now(worker, context):
        greet_calls.append((worker, context))

    async def _spy_speak_goodbye(worker, copy):
        goodbye_calls.append(copy)

    monkeypatch.setattr("klanker_voice.telephony.controller.greet_now", _spy_greet_now)
    monkeypatch.setattr("klanker_voice.telephony.controller.speak_goodbye", _spy_speak_goodbye)

    def _unexpected_start_gate(identity, **kwargs):
        raise AssertionError("quota.start_gate must never be called on a fail-closed timeout")

    monkeypatch.setattr(
        "klanker_voice.telephony.controller.quota.start_gate", _unexpected_start_gate
    )

    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(gate_window_seconds=0.05),
        quota_cfg=_quota_config(reconnect_grace_seconds=3600.0, goodbye_grace_seconds=0.01),
    )

    await controller.on_stasis_start(_stasis_event())
    assert "chan-1" in controller.calls

    await asyncio.sleep(0.3)

    assert greet_calls == []
    assert len(goodbye_calls) == 1
    assert ari.count("hangup", arg="chan-1") == 1
    assert ari.count("destroy_bridge", arg="bridge-1") == 1
    assert controller.calls == {}
    assert sessions[0].closed is True


# --- Asterisk/SIPp-dependent tier (skipped when docker is unavailable) ----


@pytest.mark.skipif(
    shutil.which("docker") is None,
    reason="docker binary unavailable -- skip the Asterisk/SIPp compose-file check",
)
def test_docker_compose_sipp_profile_is_valid():
    """The one Asterisk/SIPp-dependent case in this file (D-07 acceptance:
    "guarded to skip when docker is unavailable, CI-safe"). Only renders
    ``docker-compose.yml``'s ``integration`` profile via ``docker compose
    config`` -- a pure YAML-render/validate step that needs the ``docker``
    CLI but NOT a running daemon, so it stays green even in a sandbox with
    no Docker daemon started (this asserts the ``sipp`` service parses and
    references ``asterisk/sipp/gate-pass.xml`` correctly; it never pulls,
    builds, or starts a container -- that full local run is manual/opt-in,
    see ``asterisk/sipp/fixtures/README.md``)."""
    result = subprocess.run(
        ["docker", "compose", "--profile", "integration", "config"],
        cwd=ASTERISK_DIR,
        capture_output=True,
        text=True,
        timeout=30,
    )
    assert result.returncode == 0, f"docker compose config failed:\n{result.stderr}"
    assert "sipp" in result.stdout
    assert "gate-pass.xml" in result.stdout
