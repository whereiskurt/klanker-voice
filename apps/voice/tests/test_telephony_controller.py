"""§23 caller-ID mint + fail-closed tests for ``AsteriskCallController``
(Phase 12 Plan 06, D-02/D-04/D-05/SC-2/SC-4).

Hermetic and offline throughout, mirroring ``test_telephony_lifecycle.py``'s
rig exactly (same fakes, same ``_build_controller``/``_gated_cfg``/
``_stasis_event`` helpers, imported directly rather than duplicated):

- ``klanker_voice.telephony.controller._fetch_tel_token`` (the ONE network
  call this plan adds) is monkeypatched per test -- NO real aiohttp request
  is ever issued. This mirrors how ``quota.start_gate``/``greet_now``/
  ``speak_goodbye`` are already stubbed at module level in this suite.
- ``klanker_voice.telephony.controller.validate_access_token`` is
  monkeypatched to avoid a real JWKS round trip -- the offline JWT
  validation path itself is already covered by ``test_auth.py``; these
  tests only prove the controller CALLS it correctly.
"""

from __future__ import annotations

import asyncio
import re
from pathlib import Path
from typing import Any

from pipecat.frames.frames import TTSSpeakFrame
from pipecat.processors.frame_processor import FrameDirection, FrameProcessor

from klanker_voice.auth import AuthError, SessionIdentity
from klanker_voice.telephony.config import AnnouncementEntry
from klanker_voice.telephony.controller import _build_announcement_line

from tests.test_call_runtime import _gate_result, _quota_config, fake_aws, reset_active_count  # noqa: F401
from tests.test_telephony_lifecycle import (  # noqa: F401 -- stub_call_session_run is an autouse fixture
    FakeAriClient,
    FakeRtpMediaSession,
    _build_controller,
    _gated_cfg,
    _stasis_event,
    stub_call_session_run,
)


# --- Task 2: mapped caller -> mint success -> gate-unlock grants THAT tier -


async def test_gated_mint_success_grants_entitled_tier_not_static_unlock_tier(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """A stubbed /tel mint returning a token for a mapped number proves the
    gate-unlock grants the TOKEN-DERIVED tier (not the static
    telephony.unlock_tier_id) via upgrade_from_bypass."""
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

    fetch_calls: list[Any] = []

    async def _fake_fetch_tel_token(url: str, headers: dict[str, str]) -> str | None:
        fetch_calls.append((url, headers))
        return "minted-token"

    monkeypatch.setattr(
        "klanker_voice.telephony.controller._fetch_tel_token", _fake_fetch_tel_token
    )

    def _fake_validate_access_token(token: str) -> SessionIdentity:
        assert token == "minted-token"
        return SessionIdentity(
            sub="tel:defcon34:uuid-1", tier_id="kph-tier-defcon34", group=None, bypass_accounting=False
        )

    monkeypatch.setattr(
        "klanker_voice.telephony.controller.validate_access_token", _fake_validate_access_token
    )
    monkeypatch.setenv("TELEPHONY_ENDPOINT_AUTH_TOKEN", "s3kr1t-bearer")

    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(
            tel_mint_url="https://auth.klankermaker.ai/use1/tel", unlock_tier_id="kph-tier"
        ),
        access_pin="4242",
    )

    await controller.on_stasis_start(_stasis_event(caller_number="4165551234"))

    # E.164-normalized caller ID, URL-encoded (the auth-app /tel route
    # explicitly `decodeURIComponent`s the path segment -- 12-02-SUMMARY.md),
    # composed onto tel_mint_url, Bearer from the configured env var (never
    # a literal).
    assert fetch_calls == [
        (
            "https://auth.klankermaker.ai/use1/tel/%2B14165551234",
            {"Authorization": "Bearer s3kr1t-bearer"},
        )
    ]

    active_call = controller.calls["chan-1"]
    assert active_call.grant_tier_id == "kph-tier-defcon34"

    for digit in "4242":
        await controller.on_channel_dtmf_received(
            {"type": "ChannelDtmfReceived", "channel": {"id": "chan-1"}, "digit": digit}
        )

    assert active_call.gate.unlocked is True
    assert len(identities) == 1
    assert identities[0].tier_id == "kph-tier-defcon34"
    assert identities[0].tier_id != "kph-tier"  # NOT the static telephony.unlock_tier_id
    assert len(greet_calls) == 1


# --- Task 2: unmapped/failed mint -> fail closed, zero quota burn ----------


async def test_gated_mint_failure_fails_closed_no_quota_no_greet(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """A stubbed /tel mint returning None (404 / timeout / error, uniformly)
    proves NO real quota.start_gate grant occurs, a static goodbye is
    emitted, and the single idempotent teardown runs exactly once --
    fail-closed, zero metered quota burn (SC-4)."""
    start_gate_calls: list[Any] = []

    def _spy_start_gate(identity, **kwargs):
        start_gate_calls.append(identity)
        raise AssertionError("quota.start_gate must never be called for a failed/unmapped mint")

    monkeypatch.setattr("klanker_voice.telephony.controller.quota.start_gate", _spy_start_gate)

    greet_calls: list[Any] = []

    async def _spy_greet_now(worker, context):
        greet_calls.append((worker, context))

    monkeypatch.setattr("klanker_voice.telephony.controller.greet_now", _spy_greet_now)

    goodbye_calls: list[Any] = []

    async def _spy_speak_goodbye(worker, copy):
        goodbye_calls.append(copy)

    monkeypatch.setattr("klanker_voice.telephony.controller.speak_goodbye", _spy_speak_goodbye)

    async def _fake_fetch_tel_token(url: str, headers: dict[str, str]) -> str | None:
        return None  # simulates a uniform 404 / timeout / transport error

    monkeypatch.setattr(
        "klanker_voice.telephony.controller._fetch_tel_token", _fake_fetch_tel_token
    )

    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(tel_mint_url="https://auth.klankermaker.ai/use1/tel"),
        quota_cfg=_quota_config(reconnect_grace_seconds=3600.0, goodbye_grace_seconds=0.01),
    )

    await controller.on_stasis_start(_stasis_event(caller_number="9995551234"))

    # The fail-closed path (goodbye + grace + _close_active_call) is async --
    # give it a moment to complete (mirrors test_telephony_lifecycle.py's own
    # gate-window-expiry test).
    await asyncio.sleep(0.1)

    assert start_gate_calls == []
    assert greet_calls == []
    assert len(goodbye_calls) == 1
    assert ari.count("hangup", arg="chan-1") == 1
    assert ari.count("destroy_bridge", arg="bridge-1") == 1
    assert controller.calls == {}


async def test_gated_no_caller_id_fails_closed(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """An empty/missing caller ID (nothing to normalize) never even attempts
    the /tel call and fails closed the same way as any other mint failure."""
    fetch_calls: list[Any] = []

    async def _fail_if_called(url: str, headers: dict[str, str]) -> str | None:
        fetch_calls.append(url)
        return None

    monkeypatch.setattr("klanker_voice.telephony.controller._fetch_tel_token", _fail_if_called)

    goodbye_calls: list[Any] = []

    async def _spy_speak_goodbye(worker, copy):
        goodbye_calls.append(copy)

    monkeypatch.setattr("klanker_voice.telephony.controller.speak_goodbye", _spy_speak_goodbye)

    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(tel_mint_url="https://auth.klankermaker.ai/use1/tel"),
        quota_cfg=_quota_config(reconnect_grace_seconds=3600.0, goodbye_grace_seconds=0.01),
    )

    await controller.on_stasis_start(_stasis_event(caller_number=""))
    await asyncio.sleep(0.1)

    assert fetch_calls == []  # normalized caller id was empty -- no HTTP call at all
    assert len(goodbye_calls) == 1
    assert controller.calls == {}


# --- Regression: mint unconfigured -> legacy static unlock_tier_id --------


async def test_mint_unconfigured_uses_legacy_static_unlock_tier_id(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """When telephony.tel_mint_url is empty (the default -- every existing
    Phase-11 fixture/config), the mint step is never attempted and the gate
    grants the legacy static unlock_tier_id exactly as before this plan."""
    identities: list[Any] = []

    def _recording_start_gate(identity, **kwargs):
        identities.append(identity)
        return _gate_result()

    monkeypatch.setattr(
        "klanker_voice.telephony.controller.quota.start_gate", _recording_start_gate
    )

    async def _spy_greet_now(worker, context):
        return None

    monkeypatch.setattr("klanker_voice.telephony.controller.greet_now", _spy_greet_now)

    fetch_calls: list[Any] = []

    async def _fail_if_called(url: str, headers: dict[str, str]) -> str | None:
        fetch_calls.append(url)
        raise AssertionError("the /tel mint must never be attempted when tel_mint_url is unset")

    monkeypatch.setattr("klanker_voice.telephony.controller._fetch_tel_token", _fail_if_called)

    controller, ari, sessions = _build_controller(
        make_config_file, telephony_cfg=_gated_cfg(unlock_tier_id="kph-tier"), access_pin="4242"
    )

    await controller.on_stasis_start(_stasis_event())
    active_call = controller.calls["chan-1"]
    assert active_call.grant_tier_id == "kph-tier"

    for digit in "4242":
        await controller.on_channel_dtmf_received(
            {"type": "ChannelDtmfReceived", "channel": {"id": "chan-1"}, "digit": digit}
        )

    assert fetch_calls == []
    assert len(identities) == 1
    assert identities[0].tier_id == "kph-tier"


# --- Bearer token discipline (grep proof) -----------------------------------


def test_bearer_token_read_from_configured_env_var_never_a_literal():
    """T-12-06-03: the shared bearer token is read from the env var NAMED by
    telephony.tel_mint_env_var, never a literal string in controller.py."""
    source_path = (
        Path(__file__).resolve().parents[1] / "src" / "klanker_voice" / "telephony" / "controller.py"
    )
    text = source_path.read_text()
    assert "os.environ.get(self._telephony_cfg.tel_mint_env_var" in text
    # No hardcoded "Bearer <literal>" string anywhere (an f-string
    # interpolation like f"Bearer {bearer}" contains braces and never
    # matches this pattern).
    assert not re.search(r'"Bearer [^"{}]+"', text)


# --- Quick task 260715-oq0: CTF phone-OTP announcement DID -----------------

ANNOUNCEMENT_DID = "7254043234"


def _announcement_entry(**overrides) -> AnnouncementEntry:
    base = dict(
        did=ANNOUNCEMENT_DID,
        otp_url="https://auth.klankermaker.ai/use1/ctf/otp",
        otp_env_var="CTF_OTP_AUTH_TOKEN",
        line_template="Hey! O T P. {code}. That's {code}. Bye.",
    )
    base.update(overrides)
    return AnnouncementEntry(**base)


class _FakeTtsSynthSpy(FrameProcessor):
    """A minimal passthrough FrameProcessor standing in for
    ``factories.build_tts`` -- records every ``TTSSpeakFrame.text`` it sees
    (and, when given a shared ``order`` list, appends "tts_synth" to it) so
    tests can assert playback fired without a real ElevenLabs/network call."""

    def __init__(self, calls: list[str], order: list[str] | None = None) -> None:
        super().__init__()
        self._calls = calls
        self._order = order

    async def process_frame(self, frame, direction: FrameDirection) -> None:  # noqa: ANN001
        await super().process_frame(frame, direction)
        if isinstance(frame, TTSSpeakFrame):
            self._calls.append(frame.text)
            if self._order is not None:
                self._order.append("tts_synth")
        await self.push_frame(frame, direction)


def test_build_announcement_line_spaces_digits_and_substitutes_both_occurrences():
    line = _build_announcement_line("A {code}. That's {code}.", "123456")
    assert line == "A 1 2 3 4 5 6. That's 1 2 3 4 5 6."


async def test_announcement_did_dispatches_before_gate_no_quota_no_pipeline(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """A matched announcement DID invokes _run_announcement and returns
    BEFORE the §24 gate -- quota.start_gate is never called, no ActiveCall
    is registered, and no CallSession/pipeline is ever constructed for this
    channel."""
    start_gate_calls: list[Any] = []

    def _spy_start_gate(identity, **kwargs):
        start_gate_calls.append(identity)
        raise AssertionError("quota.start_gate must never be called for an announcement DID")

    monkeypatch.setattr("klanker_voice.telephony.controller.quota.start_gate", _spy_start_gate)

    run_announcement_calls: list[dict[str, Any]] = []

    async def _spy_run_announcement(self, **kwargs):
        run_announcement_calls.append(kwargs)

    monkeypatch.setattr(
        "klanker_voice.telephony.controller.AsteriskCallController._run_announcement",
        _spy_run_announcement,
    )

    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(announcements=(_announcement_entry(),)),
    )

    await controller.on_stasis_start(_stasis_event(exten=ANNOUNCEMENT_DID))

    assert start_gate_calls == []
    assert len(run_announcement_calls) == 1
    call_kwargs = run_announcement_calls[0]
    assert call_kwargs["sip_channel_id"] == "chan-1"
    assert call_kwargs["entry"].did == ANNOUNCEMENT_DID
    assert call_kwargs["bridge_id"] == "bridge-1"
    assert call_kwargs["external_media_channel_id"] == "ext-media-1"
    # No ActiveCall was ever registered for this channel (the whole point of
    # bypassing the gate -- announcement calls are never in self.calls).
    assert controller.calls == {}


async def test_non_announcement_did_still_enters_gated_path(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """A DID that does NOT match any configured announcement entry is
    completely unaffected -- the existing gated flow (§24) runs exactly as
    before, byte-unchanged."""
    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(announcements=(_announcement_entry(),)),
    )

    # _stasis_event()'s default exten ("1000") does not match ANNOUNCEMENT_DID.
    await controller.on_stasis_start(_stasis_event())

    active_call = controller.calls["chan-1"]
    assert active_call.gate is not None
    assert active_call.gate.unlocked is False


async def test_announcement_otp_fetch_failure_tears_down_no_tts(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """OTP fetch returning None (uniform 404/timeout/error) tears the call
    down immediately -- no TTS synthesis, no spoken line."""

    async def _fake_fetch_ctf_otp(url: str, headers: dict[str, str]) -> str | None:
        return None

    monkeypatch.setattr(
        "klanker_voice.telephony.controller._fetch_ctf_otp", _fake_fetch_ctf_otp
    )

    def _fail_if_tts_built(cfg):
        raise AssertionError("build_tts must never be called when the OTP fetch fails")

    monkeypatch.setattr("klanker_voice.telephony.controller.build_tts", _fail_if_tts_built)

    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(announcements=(_announcement_entry(),)),
    )

    await controller.on_stasis_start(_stasis_event(exten=ANNOUNCEMENT_DID))

    assert ari.count("hangup", arg="chan-1") == 1
    assert ari.count("destroy_bridge", arg="bridge-1") == 1
    assert ari.count("hangup", arg="ext-media-1") == 1
    assert sessions[0].closed is True
    assert controller.calls == {}


async def test_announcement_success_speaks_line_then_hangs_up_after_playback(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """On a successful OTP fetch, the built (digit-spaced, twice-substituted)
    line is synthesized over the existing transport, and the SIP channel is
    hung up only AFTER the TTS synth fires -- never before."""
    order: list[str] = []

    async def _fake_fetch_ctf_otp(url: str, headers: dict[str, str]) -> str | None:
        assert url == "https://auth.klankermaker.ai/use1/ctf/otp"
        return "123456"

    monkeypatch.setattr(
        "klanker_voice.telephony.controller._fetch_ctf_otp", _fake_fetch_ctf_otp
    )

    tts_calls: list[str] = []

    def _fake_build_tts(cfg):
        return _FakeTtsSynthSpy(tts_calls, order)

    monkeypatch.setattr("klanker_voice.telephony.controller.build_tts", _fake_build_tts)
    # Keep the test fast -- the bounded grace period is a fixed sleep, not an
    # event, so shrink it rather than actually waiting ~12s.
    monkeypatch.setattr(
        "klanker_voice.telephony.controller.ANNOUNCEMENT_PLAYBACK_GRACE_SECONDS", 0.05
    )

    controller, ari, sessions = _build_controller(
        make_config_file,
        order=order,
        telephony_cfg=_gated_cfg(announcements=(_announcement_entry(),)),
    )

    await controller.on_stasis_start(_stasis_event(exten=ANNOUNCEMENT_DID))

    assert tts_calls == ["Hey! O T P. 1 2 3 4 5 6. That's 1 2 3 4 5 6. Bye."]
    assert ari.count("hangup", arg="chan-1") == 1
    assert ari.count("destroy_bridge", arg="bridge-1") == 1
    assert sessions[0].closed is True
    assert controller.calls == {}

    # Hangup follows playback -- the TTS synth spy fired before the SIP
    # channel hangup was issued.
    assert order.index("tts_synth") < order.index("hangup")
