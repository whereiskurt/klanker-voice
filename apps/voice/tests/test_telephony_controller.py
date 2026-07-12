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

from klanker_voice.auth import AuthError, SessionIdentity

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
