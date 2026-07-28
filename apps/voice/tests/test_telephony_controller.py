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
import json
import re
from pathlib import Path
from typing import Any

from pipecat.frames.frames import TranscriptionFrame
from pipecat.processors.frame_processor import FrameDirection

from klanker_voice.auth import AuthError, SessionIdentity
from klanker_voice.config import load_config, load_knowledge_config
from klanker_voice.telephony.call_event import CALL_EVENT_MARKER, CALL_EVENT_OUTCOMES
from klanker_voice.telephony.config import AnnouncementEntry
from klanker_voice.telephony.controller import (
    ANNOUNCEMENT_WORDS_UNSET_SENTINEL,
    AsteriskCallController,
    _build_announcement_script,
)

from tests.test_call_runtime import _gate_result, _quota_config, fake_aws, reset_active_count  # noqa: F401
from tests.test_telephony_gate import loguru_caplog  # noqa: F401 -- shared loguru->caplog bridge fixture
from tests.test_telephony_lifecycle import (  # noqa: F401 -- stub_call_session_run is an autouse fixture
    FakeAriClient,
    FakeRtpMediaSession,
    _build_controller,
    _gated_cfg,
    _make_media_opener,
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


# --- Quick task 260716-1g0: CTF phone-OTP DTMF-code trigger (Revision 2) ---
#
# Supersedes Revision 1's pre-gate DID dispatch (which never fired on a live
# call -- VoIP.ms routes every DID to one sub-account). The announcement is
# now triggered by a DTMF code entered INSIDE the existing §24 gate.

ANNOUNCEMENT_CODE = "990011"


def _announcement_entry(**overrides) -> AnnouncementEntry:
    base = dict(
        # An arbitrary fixture value -- renamed off the retired bare
        # CTF_ANNOUNCEMENT_CODE (quick task 260727-pdh, D-02 amended) so a
        # grep for that retired name stays at zero; no live behavior
        # depends on this literal string.
        code_env_var="CTF_ANNOUNCEMENT_CODE_3234",
        otp_url="https://auth.klankermaker.ai/use1/ctf/otp",
        otp_env_var="CTF_OTP_AUTH_TOKEN",
        line_template="Hey! O T P. {code}. That's {code}. Bye.",
    )
    base.update(overrides)
    return AnnouncementEntry(**base)


def _dial(digits: str):
    """Build a ChannelDtmfReceived event dict for one digit."""

    def _mk(digit: str) -> dict[str, Any]:
        return {"type": "ChannelDtmfReceived", "channel": {"id": "chan-1"}, "digit": digit}

    return [_mk(d) for d in digits]


def test_build_announcement_script_slow_read_twice_then_panic_gag():
    from klanker_voice.telephony.controller import (
        ANNOUNCEMENT_BYE_COPY,
        ANNOUNCEMENT_DIDYOUGET_COPY,
        _pace_digits_slow,
    )

    line = _build_announcement_script("A {code}. That's {code}.", "123456")

    # NO markup tags anywhere -- the streaming ElevenLabs path reads angle-tag
    # markup ALOUD (the "borked" readout). Pacing is plain punctuation only.
    assert "<" not in line
    assert ">" not in line

    # comma-paced read still appears TWICE -- both {code} occurrences substituted
    # (the template has no {code_fast} placeholder here, so both are the slow form)
    assert line.count(_pace_digits_slow("123456")) == 2

    # gag tail present: "Did you get that? ..." then the reveal punchline
    assert ANNOUNCEMENT_DIDYOUGET_COPY in line
    assert ANNOUNCEMENT_BYE_COPY in line
    assert "wait." in line

    # the full code never appears as a bare concatenated number, at any speed
    assert "123456" not in line

    # the reveal/sign-off lands at the very end of the string
    assert line.rstrip().endswith(ANNOUNCEMENT_BYE_COPY)


async def test_announcement_code_dispatches_no_quota_no_greet(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """A DTMF sequence ending in the armed announcement code invokes
    _gate_announcement and NEVER calls quota.start_gate or greet_now."""

    def _spy_start_gate(identity, **kwargs):
        raise AssertionError("quota.start_gate must never be called for an announcement code")

    monkeypatch.setattr("klanker_voice.telephony.controller.quota.start_gate", _spy_start_gate)

    async def _spy_greet_now(worker, context):
        raise AssertionError("greet_now must never be called for an announcement code")

    monkeypatch.setattr("klanker_voice.telephony.controller.greet_now", _spy_greet_now)

    gate_announcement_calls: list[Any] = []

    async def _spy_gate_announcement(self, active_call, entry):
        gate_announcement_calls.append((active_call, entry))

    monkeypatch.setattr(
        "klanker_voice.telephony.controller.AsteriskCallController._gate_announcement",
        _spy_gate_announcement,
    )

    entry = _announcement_entry()
    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(),
        access_pin="4242",
        announcement_codes={ANNOUNCEMENT_CODE: entry},
    )

    await controller.on_stasis_start(_stasis_event())
    for event in _dial(ANNOUNCEMENT_CODE):
        await controller.on_channel_dtmf_received(event)

    assert len(gate_announcement_calls) == 1
    assert gate_announcement_calls[0][1] is entry


async def test_pin_still_unlocks_concierge(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """With an announcement code armed, the real PIN still unlocks the
    concierge exactly as before -- PIN keeps strict priority."""

    def _recording_start_gate(identity, **kwargs):
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
        telephony_cfg=_gated_cfg(),
        access_pin="4242",
        announcement_codes={ANNOUNCEMENT_CODE: _announcement_entry()},
    )

    await controller.on_stasis_start(_stasis_event())
    active_call = controller.calls["chan-1"]
    for event in _dial("4242"):
        await controller.on_channel_dtmf_received(event)

    assert active_call.gate.unlocked is True
    assert len(greet_calls) == 1


async def test_fat_fingered_prefix_before_code_still_matches(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """Suffix semantics: noise before the code still matches once the code
    is the most recent digits entered."""
    gate_announcement_calls: list[Any] = []

    async def _spy_gate_announcement(self, active_call, entry):
        gate_announcement_calls.append((active_call, entry))

    monkeypatch.setattr(
        "klanker_voice.telephony.controller.AsteriskCallController._gate_announcement",
        _spy_gate_announcement,
    )

    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(),
        access_pin="4242",
        announcement_codes={ANNOUNCEMENT_CODE: _announcement_entry()},
    )

    await controller.on_stasis_start(_stasis_event())
    for event in _dial("12" + ANNOUNCEMENT_CODE):
        await controller.on_channel_dtmf_received(event)

    assert len(gate_announcement_calls) == 1


async def test_announcement_otp_fetch_none_tears_down_no_speak(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """OTP fetch returning None (uniform 404/timeout/error) tears the call
    down via exactly one _close_active_call -- no spoken line."""

    async def _fake_fetch_ctf_otp(url: str, headers: dict[str, str]) -> str | None:
        return None

    monkeypatch.setattr(
        "klanker_voice.telephony.controller._fetch_ctf_otp", _fake_fetch_ctf_otp
    )

    async def _fail_if_speak_goodbye_called(worker, copy):
        raise AssertionError("speak_goodbye must never be called when the OTP fetch fails")

    monkeypatch.setattr(
        "klanker_voice.telephony.controller.speak_goodbye", _fail_if_speak_goodbye_called
    )

    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(),
        announcement_codes={ANNOUNCEMENT_CODE: _announcement_entry()},
    )

    await controller.on_stasis_start(_stasis_event())
    for event in _dial(ANNOUNCEMENT_CODE):
        await controller.on_channel_dtmf_received(event)

    assert ari.count("hangup", arg="chan-1") == 1
    assert ari.count("destroy_bridge", arg="bridge-1") == 1
    assert sessions[0].closed is True
    assert controller.calls == {}


async def test_announcement_success_speaks_digitspaced_line_then_closes(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """On a successful OTP fetch, speak_goodbye is called once with the
    digit-spaced twice-substituted line, then a single _close_active_call
    teardown -- no quota.start_gate/greet_now."""

    async def _fake_fetch_ctf_otp(url: str, headers: dict[str, str]) -> str | None:
        assert url == "https://auth.klankermaker.ai/use1/ctf/otp"
        return "123456"

    monkeypatch.setattr(
        "klanker_voice.telephony.controller._fetch_ctf_otp", _fake_fetch_ctf_otp
    )

    goodbye_calls: list[str] = []

    async def _spy_speak_goodbye(worker, copy):
        goodbye_calls.append(copy)

    monkeypatch.setattr("klanker_voice.telephony.controller.speak_goodbye", _spy_speak_goodbye)

    def _spy_start_gate(identity, **kwargs):
        raise AssertionError("quota.start_gate must never be called for an announcement code")

    monkeypatch.setattr("klanker_voice.telephony.controller.quota.start_gate", _spy_start_gate)

    async def _spy_greet_now(worker, context):
        raise AssertionError("greet_now must never be called for an announcement code")

    monkeypatch.setattr("klanker_voice.telephony.controller.greet_now", _spy_greet_now)

    # Keep the test fast -- the bounded grace period is a fixed sleep, not an
    # event, so shrink it rather than actually waiting ~12s (+ the panic-gag
    # tail budget, quick task 260716-2px).
    monkeypatch.setattr(
        "klanker_voice.telephony.controller.ANNOUNCEMENT_PLAYBACK_GRACE_SECONDS", 0.05
    )
    monkeypatch.setattr(
        "klanker_voice.telephony.controller.ANNOUNCEMENT_GAG_TAIL_SECONDS", 0.05
    )

    entry = _announcement_entry()
    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(),
        announcement_codes={ANNOUNCEMENT_CODE: entry},
    )

    await controller.on_stasis_start(_stasis_event())
    for event in _dial(ANNOUNCEMENT_CODE):
        await controller.on_channel_dtmf_received(event)

    assert goodbye_calls == [_build_announcement_script(entry.line_template, "123456")]
    assert ari.count("hangup", arg="chan-1") == 1
    assert ari.count("destroy_bridge", arg="bridge-1") == 1
    assert sessions[0].closed is True
    assert controller.calls == {}


async def test_gate_mode_passphrase_ignores_announcement_code(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """gate_mode="passphrase" excludes DTMF entirely -- the announcement
    code is never even inspected."""
    gate_announcement_calls: list[Any] = []

    async def _spy_gate_announcement(self, active_call, entry):
        gate_announcement_calls.append((active_call, entry))

    monkeypatch.setattr(
        "klanker_voice.telephony.controller.AsteriskCallController._gate_announcement",
        _spy_gate_announcement,
    )

    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(gate_mode="passphrase"),
        announcement_codes={ANNOUNCEMENT_CODE: _announcement_entry()},
    )

    await controller.on_stasis_start(_stasis_event())
    for event in _dial(ANNOUNCEMENT_CODE):
        await controller.on_channel_dtmf_received(event)

    assert gate_announcement_calls == []


async def test_scoped_announcement_dispatches_on_matching_did(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """A scoped entry (dids=("7254048283",)) fires exactly once when the
    call's resolved dialed_did matches (quick task 260727-ohq, D-02)."""
    gate_announcement_calls: list[Any] = []

    async def _spy_gate_announcement(self, active_call, entry):
        gate_announcement_calls.append((active_call, entry))

    monkeypatch.setattr(
        "klanker_voice.telephony.controller.AsteriskCallController._gate_announcement",
        _spy_gate_announcement,
    )

    entry = _announcement_entry(dids=("7254048283",))
    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(cid_prefix_did_map={"KVD8283": "7254048283"}),
        access_pin="4242",
        announcement_codes={ANNOUNCEMENT_CODE: entry},
    )
    ari.channel_vars["KLANKER_SIP_CIDNAME"] = "KVD8283"

    await controller.on_stasis_start(_stasis_event())
    active_call = controller.calls["chan-1"]
    assert active_call.dialed_did == "7254048283"

    for event in _dial(ANNOUNCEMENT_CODE):
        await controller.on_channel_dtmf_received(event)

    assert len(gate_announcement_calls) == 1
    assert gate_announcement_calls[0][1] is entry


async def test_scoped_announcement_does_not_dispatch_on_other_did(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """A scoped entry does NOT fire when the call resolves to a DIFFERENT
    dialed DID -- zero dispatches, gate still locked afterwards (fail
    closed; the code must not leak onto another DID)."""
    gate_announcement_calls: list[Any] = []

    async def _spy_gate_announcement(self, active_call, entry):
        gate_announcement_calls.append((active_call, entry))

    monkeypatch.setattr(
        "klanker_voice.telephony.controller.AsteriskCallController._gate_announcement",
        _spy_gate_announcement,
    )

    entry = _announcement_entry(dids=("7254048283",))
    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(
            cid_prefix_did_map={"KVD3234": "7254043234", "KVD8283": "7254048283"}
        ),
        access_pin="4242",
        announcement_codes={ANNOUNCEMENT_CODE: entry},
    )
    ari.channel_vars["KLANKER_SIP_CIDNAME"] = "KVD3234"

    await controller.on_stasis_start(_stasis_event())
    active_call = controller.calls["chan-1"]
    assert active_call.dialed_did == "7254043234"

    for event in _dial(ANNOUNCEMENT_CODE):
        await controller.on_channel_dtmf_received(event)

    assert gate_announcement_calls == []
    assert active_call.gate.unlocked is False


async def test_scoped_announcement_does_not_dispatch_on_unresolved_did(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """A scoped entry does NOT fire when the dialed DID is unresolved (no
    channel vars set) -- fail closed."""
    gate_announcement_calls: list[Any] = []

    async def _spy_gate_announcement(self, active_call, entry):
        gate_announcement_calls.append((active_call, entry))

    monkeypatch.setattr(
        "klanker_voice.telephony.controller.AsteriskCallController._gate_announcement",
        _spy_gate_announcement,
    )

    entry = _announcement_entry(dids=("7254048283",))
    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(),
        access_pin="4242",
        announcement_codes={ANNOUNCEMENT_CODE: entry},
    )
    # No KLANKER_SIP_CIDNAME/KLANKER_SIP_TO channel var set -- dialed_did resolves to "".

    await controller.on_stasis_start(_stasis_event())
    active_call = controller.calls["chan-1"]
    assert active_call.dialed_did == ""

    for event in _dial(ANNOUNCEMENT_CODE):
        await controller.on_channel_dtmf_received(event)

    assert gate_announcement_calls == []
    assert active_call.gate.unlocked is False


async def test_global_announcement_dispatches_regardless_of_did_resolution(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """A GLOBAL entry (dids=()) dispatches both when the dialed DID is
    resolved and when it is unresolved -- today's behavior preserved
    exactly (quick task 260727-ohq must not regress the shipped live entry)."""
    gate_announcement_calls: list[Any] = []

    async def _spy_gate_announcement(self, active_call, entry):
        gate_announcement_calls.append((active_call, entry))

    monkeypatch.setattr(
        "klanker_voice.telephony.controller.AsteriskCallController._gate_announcement",
        _spy_gate_announcement,
    )

    entry = _announcement_entry()  # dids defaults to () -- global
    assert entry.dids == ()

    # Case 1: dialed DID resolved.
    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(cid_prefix_did_map={"KVD3234": "7254043234"}),
        access_pin="4242",
        announcement_codes={ANNOUNCEMENT_CODE: entry},
    )
    ari.channel_vars["KLANKER_SIP_CIDNAME"] = "KVD3234"
    await controller.on_stasis_start(_stasis_event())
    assert controller.calls["chan-1"].dialed_did == "7254043234"
    for event in _dial(ANNOUNCEMENT_CODE):
        await controller.on_channel_dtmf_received(event)
    assert len(gate_announcement_calls) == 1

    # Case 2: dialed DID unresolved -- fresh controller/call.
    controller2, ari2, sessions2 = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(),
        access_pin="4242",
        announcement_codes={ANNOUNCEMENT_CODE: entry},
    )
    await controller2.on_stasis_start(_stasis_event())
    assert controller2.calls["chan-1"].dialed_did == ""
    for event in _dial(ANNOUNCEMENT_CODE):
        await controller2.on_channel_dtmf_received(event)
    assert len(gate_announcement_calls) == 2


async def test_scoped_code_no_dispatch_but_global_code_still_dispatches(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """Two entries armed under different codes -- one global, one scoped to
    a DID this call did NOT dial: dialing the scoped code dispatches
    nothing, dialing the global code still dispatches the global entry (the
    loop skips a non-matching entry rather than aborting)."""
    gate_announcement_calls: list[Any] = []

    async def _spy_gate_announcement(self, active_call, entry):
        gate_announcement_calls.append((active_call, entry))

    monkeypatch.setattr(
        "klanker_voice.telephony.controller.AsteriskCallController._gate_announcement",
        _spy_gate_announcement,
    )

    SCOPED_CODE = "990011"
    GLOBAL_CODE = "112233"
    scoped_entry = _announcement_entry(
        code_env_var="CTF_SCOPED_CODE", dids=("7254048283",)
    )
    global_entry = _announcement_entry(code_env_var="CTF_GLOBAL_CODE")

    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(cid_prefix_did_map={"KVD3234": "7254043234"}),
        access_pin="4242",
        announcement_codes={SCOPED_CODE: scoped_entry, GLOBAL_CODE: global_entry},
    )
    ari.channel_vars["KLANKER_SIP_CIDNAME"] = "KVD3234"

    await controller.on_stasis_start(_stasis_event())
    active_call = controller.calls["chan-1"]
    assert active_call.dialed_did == "7254043234"

    for event in _dial(SCOPED_CODE):
        await controller.on_channel_dtmf_received(event)
    assert gate_announcement_calls == []

    for event in _dial(GLOBAL_CODE):
        await controller.on_channel_dtmf_received(event)
    assert len(gate_announcement_calls) == 1
    assert gate_announcement_calls[0][1] is global_entry


def test_announcement_code_unset_env_var_arms_no_trigger(
    make_config_file, monkeypatch
):
    """An entry whose code_env_var is unset in the environment arms NO
    trigger -- the controller's resolved announcement-code map is empty."""
    monkeypatch.delenv("CTF_ANNOUNCEMENT_CODE_3234", raising=False)

    cfg = load_config(make_config_file())
    knowledge_cfg = load_knowledge_config()
    ari = FakeAriClient()
    opener, _sessions = _make_media_opener()
    controller = AsteriskCallController(
        ari,
        cfg,
        knowledge_cfg,
        _quota_config(reconnect_grace_seconds=3600.0),
        _gated_cfg(announcements=(_announcement_entry(),)),
        media_session_opener=opener,
        access_pin="",
        passphrase_words=frozenset(),
        # No announcement_codes injection -- resolves from os.environ, which
        # does NOT have CTF_ANNOUNCEMENT_CODE set.
    )
    assert controller._announcements_by_code == {}


# --- Quick task 260727-pdh: either-factor spoken-trigger seam --------------


def _build_controller_words(
    make_config_file,
    *,
    telephony_cfg,
    announcement_words: dict[str, str] | None = None,
    announcement_codes: dict[str, AnnouncementEntry] | None = None,
    access_pin: str = "",
) -> tuple[AsteriskCallController, "FakeAriClient"]:
    """Construct an AsteriskCallController directly (bypassing
    tests/test_telephony_lifecycle.py's _build_controller, which has no
    announcement_words injection seam) so these tests can hermetically arm
    the spoken-trigger registry without touching the real environment."""
    cfg = load_config(make_config_file())
    knowledge_cfg = load_knowledge_config()
    ari = FakeAriClient()
    opener, _sessions = _make_media_opener()
    controller = AsteriskCallController(
        ari,
        cfg,
        knowledge_cfg,
        _quota_config(reconnect_grace_seconds=3600.0),
        telephony_cfg,
        media_session_opener=opener,
        access_pin=access_pin,
        passphrase_words=frozenset(),
        announcement_codes=announcement_codes,
        announcement_words=announcement_words,
    )
    return controller, ari


async def test_words_registry_skips_four_disabled_states_numeric_trigger_unaffected(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """All four spoken-factor-disabled states (D-03) skip gracefully -- no
    registry row -- while the SAME entry's numeric code trigger stays fully
    armed. This is the 8283 launch state and must be asserted, not assumed."""
    gate_announcement_calls: list[Any] = []

    async def _spy_gate_announcement(self, active_call, entry):
        gate_announcement_calls.append((active_call, entry))

    monkeypatch.setattr(
        "klanker_voice.telephony.controller.AsteriskCallController._gate_announcement",
        _spy_gate_announcement,
    )

    no_field_entry = _announcement_entry(code_env_var="CTF_CODE_NO_FIELD")  # no words_env_var
    absent_entry = _announcement_entry(
        code_env_var="CTF_CODE_ABSENT", words_env_var="WORDS_ABSENT_FROM_SOURCE"
    )
    empty_entry = _announcement_entry(
        code_env_var="CTF_CODE_EMPTY", words_env_var="WORDS_EMPTY"
    )
    sentinel_entry = _announcement_entry(
        code_env_var="CTF_CODE_SENTINEL", words_env_var="WORDS_SENTINEL"
    )

    telephony_cfg = _gated_cfg(
        announcements=(no_field_entry, absent_entry, empty_entry, sentinel_entry)
    )
    numeric_code = "990011"
    controller, ari = _build_controller_words(
        make_config_file,
        telephony_cfg=telephony_cfg,
        announcement_words={
            # WORDS_ABSENT_FROM_SOURCE deliberately not present at all.
            "WORDS_EMPTY": "   ",
            "WORDS_SENTINEL": ANNOUNCEMENT_WORDS_UNSET_SENTINEL,
        },
        # Hermetic numeric-trigger injection (mirrors every other test in
        # this file) -- proves the DTMF path is a wholly separate
        # resolution, unaffected by every one of the four disabled words
        # states above.
        announcement_codes={numeric_code: no_field_entry},
        access_pin="4242",
    )

    # No registry row for ANY of the four entries -- every disabled state
    # skipped gracefully, none raised.
    assert controller._announcement_words_by_code_env_var == {}

    await controller.on_stasis_start(_stasis_event())
    for event in _dial(numeric_code):
        await controller.on_channel_dtmf_received(event)

    assert len(gate_announcement_calls) == 1
    assert gate_announcement_calls[0][1] is no_field_entry


def test_words_registry_sentinel_substring_not_treated_as_sentinel(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """A words value that merely CONTAINS the sentinel token (not an exact
    whole-value match) is NOT disabled -- the entry arms with those real
    words, sentinel token included as an ordinary word among others."""
    entry = _announcement_entry(
        code_env_var="CTF_CODE_UCTF", words_env_var="WORDS_UCTF"
    )
    controller, ari = _build_controller_words(
        make_config_file,
        telephony_cfg=_gated_cfg(announcements=(entry,)),
        announcement_words={"WORDS_UCTF": f"{ANNOUNCEMENT_WORDS_UNSET_SENTINEL} hack the planet"},
        access_pin="4242",
    )

    assert "CTF_CODE_UCTF" in controller._announcement_words_by_code_env_var
    arming_entry, words = controller._announcement_words_by_code_env_var["CTF_CODE_UCTF"]
    assert arming_entry is entry
    assert words == {ANNOUNCEMENT_WORDS_UNSET_SENTINEL, "hack", "the", "planet"}


async def test_words_registry_well_formed_arms_and_dispatches_via_gate_announcement(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """A well-formed words registry arms the gate's announcement_words seam;
    speaking the words through the pipeline dispatches to the exact SAME
    _gate_announcement method the DTMF factor already calls, with the
    matching entry."""
    gate_announcement_calls: list[Any] = []

    async def _spy_gate_announcement(self, active_call, entry):
        gate_announcement_calls.append((active_call, entry))

    monkeypatch.setattr(
        "klanker_voice.telephony.controller.AsteriskCallController._gate_announcement",
        _spy_gate_announcement,
    )

    entry = _announcement_entry(
        code_env_var="CTF_ANNOUNCEMENT_CODE_UCTF", words_env_var="CTF_ANNOUNCEMENT_WORDS_UCTF"
    )
    controller, ari = _build_controller_words(
        make_config_file,
        telephony_cfg=_gated_cfg(announcements=(entry,)),
        announcement_words={"CTF_ANNOUNCEMENT_WORDS_UCTF": "hack the planet"},
        access_pin="4242",
    )

    await controller.on_stasis_start(_stasis_event())
    active_call = controller.calls["chan-1"]
    assert active_call.gate is not None

    await active_call.gate.process_frame(
        TranscriptionFrame(text="hack the planet", user_id="", timestamp=""),
        FrameDirection.DOWNSTREAM,
    )
    await asyncio.sleep(0)  # let the spawned callback task run

    assert len(gate_announcement_calls) == 1
    assert gate_announcement_calls[0][1] is entry
    assert active_call.gate.unlocked is False  # cancel_for_takeover, never unlock


async def test_words_registry_did_filtered_scoped_entry_armed_only_on_own_did(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """A scoped words entry's registry row is present in the per-call phrase
    map only when the call's resolved dialed_did is one of its own -- absent
    for another DID and for an unresolved DID (fail closed, D-04/T-pdh-03)."""
    gate_announcement_calls: list[Any] = []

    async def _spy_gate_announcement(self, active_call, entry):
        gate_announcement_calls.append((active_call, entry))

    monkeypatch.setattr(
        "klanker_voice.telephony.controller.AsteriskCallController._gate_announcement",
        _spy_gate_announcement,
    )

    scoped_entry = _announcement_entry(
        code_env_var="CTF_ANNOUNCEMENT_CODE_UCTF",
        words_env_var="CTF_ANNOUNCEMENT_WORDS_UCTF",
        dids=("7254048283",),
    )
    telephony_cfg = _gated_cfg(
        announcements=(scoped_entry,),
        cid_prefix_did_map={"KVD3234": "7254043234", "KVD8283": "7254048283"},
    )

    # Case 1: dialed a DIFFERENT DID -- the scoped entry must not be armed.
    controller, ari = _build_controller_words(
        make_config_file,
        telephony_cfg=telephony_cfg,
        announcement_words={"CTF_ANNOUNCEMENT_WORDS_UCTF": "hack the planet"},
        access_pin="4242",
    )
    ari.channel_vars["KLANKER_SIP_CIDNAME"] = "KVD3234"
    await controller.on_stasis_start(_stasis_event())
    active_call = controller.calls["chan-1"]
    assert active_call.dialed_did == "7254043234"

    await active_call.gate.process_frame(
        TranscriptionFrame(text="hack the planet", user_id="", timestamp=""),
        FrameDirection.DOWNSTREAM,
    )
    await asyncio.sleep(0)
    assert gate_announcement_calls == []

    # Case 2: dialed DID unresolved -- fresh controller/call, fail closed.
    controller2, ari2 = _build_controller_words(
        make_config_file,
        telephony_cfg=telephony_cfg,
        announcement_words={"CTF_ANNOUNCEMENT_WORDS_UCTF": "hack the planet"},
        access_pin="4242",
    )
    await controller2.on_stasis_start(_stasis_event())
    active_call2 = controller2.calls["chan-1"]
    assert active_call2.dialed_did == ""

    await active_call2.gate.process_frame(
        TranscriptionFrame(text="hack the planet", user_id="", timestamp=""),
        FrameDirection.DOWNSTREAM,
    )
    await asyncio.sleep(0)
    assert gate_announcement_calls == []

    # Case 3: dialed the OWNING DID -- the scoped entry IS armed and dispatches.
    controller3, ari3 = _build_controller_words(
        make_config_file,
        telephony_cfg=telephony_cfg,
        announcement_words={"CTF_ANNOUNCEMENT_WORDS_UCTF": "hack the planet"},
        access_pin="4242",
    )
    ari3.channel_vars["KLANKER_SIP_CIDNAME"] = "KVD8283"
    await controller3.on_stasis_start(_stasis_event())
    active_call3 = controller3.calls["chan-1"]
    assert active_call3.dialed_did == "7254048283"

    await active_call3.gate.process_frame(
        TranscriptionFrame(text="hack the planet", user_id="", timestamp=""),
        FrameDirection.DOWNSTREAM,
    )
    await asyncio.sleep(0)
    assert len(gate_announcement_calls) == 1
    assert gate_announcement_calls[0][1] is scoped_entry


async def test_words_registry_global_entry_armed_regardless_of_did_resolution(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """A GLOBAL words entry (dids=()) is armed and dispatches for every call,
    resolved or not -- mirrors the DTMF path's own global-entry behavior."""
    gate_announcement_calls: list[Any] = []

    async def _spy_gate_announcement(self, active_call, entry):
        gate_announcement_calls.append((active_call, entry))

    monkeypatch.setattr(
        "klanker_voice.telephony.controller.AsteriskCallController._gate_announcement",
        _spy_gate_announcement,
    )

    global_entry = _announcement_entry(
        code_env_var="CTF_ANNOUNCEMENT_CODE_UCTF", words_env_var="CTF_ANNOUNCEMENT_WORDS_UCTF"
    )
    assert global_entry.dids == ()
    telephony_cfg = _gated_cfg(announcements=(global_entry,))

    controller, ari = _build_controller_words(
        make_config_file,
        telephony_cfg=telephony_cfg,
        announcement_words={"CTF_ANNOUNCEMENT_WORDS_UCTF": "hack the planet"},
        access_pin="4242",
    )
    await controller.on_stasis_start(_stasis_event())  # dialed_did unresolved
    active_call = controller.calls["chan-1"]
    assert active_call.dialed_did == ""

    await active_call.gate.process_frame(
        TranscriptionFrame(text="hack the planet", user_id="", timestamp=""),
        FrameDirection.DOWNSTREAM,
    )
    await asyncio.sleep(0)

    assert len(gate_announcement_calls) == 1
    assert gate_announcement_calls[0][1] is global_entry

    assert controller._announcements_by_code == {}


# --- Quick task 260727-v5e: game outcomes + the D-05 redaction gate --------


def _extract_call_events(text: str) -> list[dict[str, Any]]:
    """Pull every ``game_call_event`` marker line out of ``loguru_caplog.
    text`` and parse its JSON payload -- shared helper for the tests below."""
    events: list[dict[str, Any]] = []
    marker = CALL_EVENT_MARKER + " "
    for line in text.splitlines():
        idx = line.find(marker)
        if idx == -1:
            continue
        events.append(json.loads(line[idx + len(marker) :]))
    return events


async def test_dtmf_code_game_trigger_emits_exactly_one_announcement_code_event(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch, loguru_caplog
):
    """Test 7: a DTMF-code game trigger produces exactly one marker line
    with outcome="announcement_code"."""

    async def _fake_fetch_ctf_otp(url: str, headers: dict[str, str]) -> str | None:
        return "123456"

    monkeypatch.setattr(
        "klanker_voice.telephony.controller._fetch_ctf_otp", _fake_fetch_ctf_otp
    )

    async def _spy_speak_goodbye(worker, copy):
        return None

    monkeypatch.setattr("klanker_voice.telephony.controller.speak_goodbye", _spy_speak_goodbye)
    monkeypatch.setattr(
        "klanker_voice.telephony.controller.ANNOUNCEMENT_PLAYBACK_GRACE_SECONDS", 0.01
    )
    monkeypatch.setattr(
        "klanker_voice.telephony.controller.ANNOUNCEMENT_GAG_TAIL_SECONDS", 0.01
    )
    monkeypatch.setattr(
        "klanker_voice.telephony.controller.ANNOUNCEMENT_SLOW_DIGIT_SECONDS", 0.0
    )

    entry = _announcement_entry()
    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(),
        announcement_codes={ANNOUNCEMENT_CODE: entry},
    )

    await controller.on_stasis_start(_stasis_event())
    for event in _dial(ANNOUNCEMENT_CODE):
        await controller.on_channel_dtmf_received(event)

    events = _extract_call_events(loguru_caplog.text)
    assert len(events) == 1
    assert events[0]["outcome"] == "announcement_code"


async def test_spoken_words_game_trigger_emits_exactly_one_announcement_words_event(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch, loguru_caplog
):
    """Test 8: a spoken-words game trigger produces exactly one marker line
    with outcome="announcement_words"."""

    async def _fake_fetch_ctf_otp(url: str, headers: dict[str, str]) -> str | None:
        return "123456"

    monkeypatch.setattr(
        "klanker_voice.telephony.controller._fetch_ctf_otp", _fake_fetch_ctf_otp
    )

    async def _spy_speak_goodbye(worker, copy):
        return None

    monkeypatch.setattr("klanker_voice.telephony.controller.speak_goodbye", _spy_speak_goodbye)
    monkeypatch.setattr(
        "klanker_voice.telephony.controller.ANNOUNCEMENT_PLAYBACK_GRACE_SECONDS", 0.01
    )
    monkeypatch.setattr(
        "klanker_voice.telephony.controller.ANNOUNCEMENT_GAG_TAIL_SECONDS", 0.01
    )
    monkeypatch.setattr(
        "klanker_voice.telephony.controller.ANNOUNCEMENT_SLOW_DIGIT_SECONDS", 0.0
    )

    entry = _announcement_entry(
        code_env_var="CTF_ANNOUNCEMENT_CODE_UCTF", words_env_var="CTF_ANNOUNCEMENT_WORDS_UCTF"
    )
    controller, ari = _build_controller_words(
        make_config_file,
        telephony_cfg=_gated_cfg(announcements=(entry,)),
        announcement_words={"CTF_ANNOUNCEMENT_WORDS_UCTF": "hack the planet"},
    )

    await controller.on_stasis_start(_stasis_event())
    active_call = controller.calls["chan-1"]

    await active_call.gate.process_frame(
        TranscriptionFrame(text="hack the planet", user_id="", timestamp=""),
        FrameDirection.DOWNSTREAM,
    )
    await asyncio.sleep(0.3)  # let the spawned announcement task run to completion

    events = _extract_call_events(loguru_caplog.text)
    assert len(events) == 1
    assert events[0]["outcome"] == "announcement_words"


async def test_wrong_code_gate_timeout_redacts_digits_words_and_code(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch, loguru_caplog
):
    """Test 9 (D-05 redaction proof): a gated game call where the caller
    dials a WRONG code, speaks a distinctive nonsense word, and the gate
    then fails closed (window expiry). The emitted event carries the COUNT
    (digits_entered == 6) but neither the entered digits, the armed game
    code, nor the heard word ever appear in the payload -- only counts, an
    outcome label, and timings."""
    caller_id = "+15550000123"
    wrong_digits = "874321"
    armed_code = "333266"

    async def _spy_speak_goodbye(worker, copy):
        return None

    monkeypatch.setattr("klanker_voice.telephony.controller.speak_goodbye", _spy_speak_goodbye)

    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(gate_window_seconds=0.05),
        quota_cfg=_quota_config(reconnect_grace_seconds=3600.0, goodbye_grace_seconds=0.01),
        access_pin="112233",  # distinct from wrong_digits and armed_code
        announcement_codes={armed_code: _announcement_entry()},
    )

    await controller.on_stasis_start(_stasis_event(caller_number=caller_id))
    active_call = controller.calls["chan-1"]

    for event in _dial(wrong_digits):
        await controller.on_channel_dtmf_received(event)

    await active_call.gate.process_frame(
        TranscriptionFrame(text="zorblattflibber", user_id="", timestamp=""),
        FrameDirection.DOWNSTREAM,
    )

    await asyncio.sleep(0.3)  # gate window expiry -> fail-closed -> teardown

    events = _extract_call_events(loguru_caplog.text)
    assert len(events) == 1
    event = events[0]

    assert event["digits_entered"] == 6
    assert set(event.keys()) == {
        "call_id",
        "dialed_did",
        "caller_id",
        "otp_only",
        "outcome",
        "digits_entered",
        "words_heard",
        "seconds_to_outcome",
        "duration_seconds",
    }
    assert event["outcome"] in CALL_EVENT_OUTCOMES
    assert event["words_heard"] > 0

    payload_text = json.dumps(event)
    assert wrong_digits not in payload_text
    assert armed_code not in payload_text
    assert "zorblattflibber" not in payload_text
