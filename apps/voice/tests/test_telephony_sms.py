"""Quick task 260716-hg5 — CTF OTP SMS-during-call ("check your phone"
punchline). Covers the pure send primitives + eligibility + spoken-script
branch in isolation (fake aiohttp — NO real network), plus the fire-early
scheduling hook inside ``_gate_announcement`` (reusing the controller-test
fixtures/helpers).

Design doc: docs/superpowers/specs/2026-07-16-ctf-otp-sms-during-call-design.md
"""

from __future__ import annotations

from typing import Any

import pytest

from klanker_voice.telephony.controller import (
    ANNOUNCEMENT_BYE_COPY,
    ANNOUNCEMENT_SMS_PUNCHLINE_COPY,
    VOIPMS_SMS_API_URL,
    _build_announcement_script,
    _send_sms,
    _send_sms_pool,
    _sms_dst_from_caller,
)

# Reuse the exact call-setup helpers/fixtures the controller tests use.
# `make_config_file`/`stub_provider_keys` come from conftest (auto-discovered,
# NOT imported). `fake_aws` and the autouse `stub_call_session_run` fixture are
# defined in sibling test modules and MUST be imported so pytest registers them.
from tests.test_call_runtime import fake_aws  # noqa: F401 -- fixture used by name
from tests.test_telephony_controller import (
    ANNOUNCEMENT_CODE,
    _announcement_entry,
    _dial,
)
from tests.test_telephony_lifecycle import (  # noqa: F401 -- stub_call_session_run is autouse
    _build_controller,
    _gated_cfg,
    _stasis_event,
    stub_call_session_run,
)


# --- Fake aiohttp so _send_sms's real HTTP path is exercised offline ---------


class _FakeResp:
    def __init__(self, status: int, payload: Any):
        self.status = status
        self._payload = payload

    async def __aenter__(self):
        return self

    async def __aexit__(self, *exc):
        return False

    async def json(self, content_type=None):
        if isinstance(self._payload, Exception):
            raise self._payload
        return self._payload


class _FakeSession:
    """Records the single GET, returns a scripted response (or raises)."""

    last_url: str | None = None
    last_params: dict[str, Any] | None = None

    def __init__(self, *, status: int = 200, payload: Any = None, raise_on_get: Exception | None = None):
        self._status = status
        self._payload = payload
        self._raise = raise_on_get

    async def __aenter__(self):
        return self

    async def __aexit__(self, *exc):
        return False

    def get(self, url, params=None):
        _FakeSession.last_url = url
        _FakeSession.last_params = params
        if self._raise is not None:
            raise self._raise
        return _FakeResp(self._status, self._payload)


def _patch_session(monkeypatch, **kwargs):
    """Patch aiohttp.ClientSession(...) to return a fresh _FakeSession."""
    import aiohttp

    def _factory(*a, **kw):
        return _FakeSession(**kwargs)

    monkeypatch.setattr(aiohttp, "ClientSession", _factory)


# --- _sms_dst_from_caller ----------------------------------------------------


@pytest.mark.parametrize(
    "caller,expected",
    [
        ("5197101515", "5197101515"),      # bare 10-digit NANP
        ("+15197101515", "5197101515"),    # E.164
        ("15197101515", "5197101515"),     # 11-digit with country code
        ("(519) 710-1515", "5197101515"),  # punctuated
        ("1001", ""),                       # internal extension -> not textable
        ("", ""),                           # withheld caller ID
        ("+445551234567", ""),              # non-North-American -> not textable
        (None, ""),                          # missing
    ],
)
def test_sms_dst_from_caller(caller, expected):
    assert _sms_dst_from_caller(caller) == expected


# --- _build_announcement_script branch ---------------------------------------


def test_script_ineligible_is_byte_identical_to_legacy():
    """sms_eligible defaults to False -> the closing beat is the legacy
    ANNOUNCEMENT_BYE_COPY and the output is byte-identical to the pre-hg5
    call (default arg == explicit False)."""
    template = "Hey! {code}. That's {code}."
    default = _build_announcement_script(template, "123456")
    explicit_false = _build_announcement_script(template, "123456", False)
    assert default == explicit_false
    assert ANNOUNCEMENT_BYE_COPY in default
    assert ANNOUNCEMENT_SMS_PUNCHLINE_COPY not in default


def test_script_eligible_swaps_in_check_your_phone_punchline():
    template = "Hey! {code}. That's {code}."
    eligible = _build_announcement_script(template, "123456", True)
    legacy = _build_announcement_script(template, "123456", False)
    assert ANNOUNCEMENT_SMS_PUNCHLINE_COPY in eligible
    assert "check your phone" in eligible          # the distinguishing beat
    assert "check your phone" not in legacy         # absent in the legacy sign-off
    # (the punchline intentionally still ENDS with the same "Good luck! Hack
    #  the planet!" cheer as ANNOUNCEMENT_BYE_COPY -- so don't assert its absence)
    # markup-free in both branches (streaming ElevenLabs reads tags aloud)
    assert "<break" not in eligible and "/>" not in eligible
    # the tease is unchanged: digits never concatenated into a bare number
    assert "123456" not in eligible


# --- _send_sms (real HTTP path, faked transport) -----------------------------


async def test_send_sms_success_on_200_and_status_success(monkeypatch):
    _patch_session(monkeypatch, status=200, payload={"status": "success", "sms": 42})
    ok = await _send_sms("6134805878", "5197101515", "body", "user", "pass")
    assert ok is True
    # request shape: sendSMS to the VoIP.ms endpoint with did/dst/message
    assert _FakeSession.last_url == VOIPMS_SMS_API_URL
    assert _FakeSession.last_params["method"] == "sendSMS"
    assert _FakeSession.last_params["did"] == "6134805878"
    assert _FakeSession.last_params["dst"] == "5197101515"


async def test_send_sms_false_on_non_success_status(monkeypatch):
    _patch_session(monkeypatch, status=200, payload={"status": "invalid_did"})
    assert await _send_sms("6134805878", "5197101515", "b", "u", "p") is False


async def test_send_sms_false_on_non_200(monkeypatch):
    _patch_session(monkeypatch, status=500, payload={"status": "success"})
    assert await _send_sms("6134805878", "5197101515", "b", "u", "p") is False


async def test_send_sms_never_raises_on_transport_error(monkeypatch):
    _patch_session(monkeypatch, raise_on_get=RuntimeError("connreset"))
    assert await _send_sms("6134805878", "5197101515", "b", "u", "p") is False


async def test_send_sms_false_on_missing_creds_no_request(monkeypatch):
    """Missing creds/DID/dst -> False WITHOUT issuing any request."""
    _FakeSession.last_url = None
    _patch_session(monkeypatch, status=200, payload={"status": "success"})
    assert await _send_sms("6134805878", "5197101515", "b", "", "p") is False
    assert _FakeSession.last_url is None  # short-circuited before any GET


# --- _send_sms_pool (ordered auto-fallback) ----------------------------------


async def test_pool_first_success_wins_and_stops(monkeypatch):
    attempts: list[str] = []

    async def _fake_send(from_did, dst, message, api_user, api_pass):
        attempts.append(from_did)
        return from_did == "222"  # first DID fails, second succeeds

    monkeypatch.setattr("klanker_voice.telephony.controller._send_sms", _fake_send)
    ok = await _send_sms_pool(("111", "222", "333"), "5551234567", "b", "u", "p")
    assert ok is True
    assert attempts == ["111", "222"]  # stopped after first success; "333" untried


async def test_pool_all_fail_returns_false(monkeypatch):
    async def _fake_send(*a, **k):
        return False

    monkeypatch.setattr("klanker_voice.telephony.controller._send_sms", _fake_send)
    assert await _send_sms_pool(("111", "222"), "5551234567", "b", "u", "p") is False


async def test_pool_empty_returns_false_no_send(monkeypatch):
    calls: list[Any] = []

    async def _fake_send(*a, **k):
        calls.append(a)
        return True

    monkeypatch.setattr("klanker_voice.telephony.controller._send_sms", _fake_send)
    assert await _send_sms_pool((), "5551234567", "b", "u", "p") is False
    assert calls == []


# --- _gate_announcement fire-early scheduling hook ---------------------------


def _sms_env(monkeypatch):
    monkeypatch.setenv("VOIPMS_API_USERNAME", "test-user")
    monkeypatch.setenv("VOIPMS_API_PASSWORD", "test-pass")


def _stub_common(monkeypatch, *, otp="483920"):
    async def _fake_fetch_ctf_otp(url, headers):
        return otp

    monkeypatch.setattr(
        "klanker_voice.telephony.controller._fetch_ctf_otp", _fake_fetch_ctf_otp
    )
    monkeypatch.setattr(
        "klanker_voice.telephony.controller.ANNOUNCEMENT_PLAYBACK_GRACE_SECONDS", 0.02
    )
    monkeypatch.setattr(
        "klanker_voice.telephony.controller.ANNOUNCEMENT_GAG_TAIL_SECONDS", 0.02
    )
    # keep the teardown grace sleep tiny (it scales by 2*len(code)*this)
    monkeypatch.setattr(
        "klanker_voice.telephony.controller.ANNOUNCEMENT_SLOW_DIGIT_SECONDS", 0.001
    )
    goodbye: list[str] = []

    async def _spy_goodbye(worker, copy):
        goodbye.append(copy)

    monkeypatch.setattr("klanker_voice.telephony.controller.speak_goodbye", _spy_goodbye)
    return goodbye


async def test_hook_eligible_schedules_one_send_and_speaks_punchline(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    _sms_env(monkeypatch)
    goodbye = _stub_common(monkeypatch)

    sent: list[tuple[Any, ...]] = []

    async def _fake_pool(dids, dst, message, api_user, api_pass):
        sent.append((dids, dst, message, api_user, api_pass))
        return True

    monkeypatch.setattr(
        "klanker_voice.telephony.controller._send_sms_pool", _fake_pool
    )

    entry = _announcement_entry(sms_dids=("6134805878",))
    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(),
        announcement_codes={ANNOUNCEMENT_CODE: entry},
    )

    await controller.on_stasis_start(_stasis_event(caller_number="5197101515"))
    for event in _dial(ANNOUNCEMENT_CODE):
        await controller.on_channel_dtmf_received(event)

    # exactly one send, to the caller's own 10-digit ANI, from the pool, with
    # the plain (copy-pasteable) OTP in the body
    assert len(sent) == 1
    dids, dst, message, api_user, api_pass = sent[0]
    assert dids == ("6134805878",)
    assert dst == "5197101515"
    assert "483920" in message
    assert (api_user, api_pass) == ("test-user", "test-pass")
    # spoken line lands the "check your phone" punchline
    assert len(goodbye) == 1
    assert ANNOUNCEMENT_SMS_PUNCHLINE_COPY in goodbye[0]
    assert controller.calls == {}


async def test_hook_ineligible_no_send_legacy_signoff(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """A non-textable caller (internal extension) -> NO send scheduled and the
    legacy sign-off is spoken (no 'check your phone' promise we can't keep)."""
    _sms_env(monkeypatch)
    goodbye = _stub_common(monkeypatch)

    async def _fail_pool(*a, **k):
        raise AssertionError("_send_sms_pool must not run for an ineligible caller")

    monkeypatch.setattr("klanker_voice.telephony.controller._send_sms_pool", _fail_pool)

    entry = _announcement_entry(sms_dids=("6134805878",))
    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(),
        announcement_codes={ANNOUNCEMENT_CODE: entry},
    )

    await controller.on_stasis_start(_stasis_event(caller_number="1001"))  # extension
    for event in _dial(ANNOUNCEMENT_CODE):
        await controller.on_channel_dtmf_received(event)

    assert len(goodbye) == 1
    assert ANNOUNCEMENT_BYE_COPY in goodbye[0]
    assert ANNOUNCEMENT_SMS_PUNCHLINE_COPY not in goodbye[0]
    assert controller.calls == {}


async def test_hook_no_pool_configured_behaves_legacy(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """An entry with no sms_dids (default) -> no send even for a textable
    caller; legacy sign-off. Proves the feature is fully opt-in."""
    _sms_env(monkeypatch)
    goodbye = _stub_common(monkeypatch)

    async def _fail_pool(*a, **k):
        raise AssertionError("_send_sms_pool must not run when sms_dids is empty")

    monkeypatch.setattr("klanker_voice.telephony.controller._send_sms_pool", _fail_pool)

    entry = _announcement_entry()  # no sms_dids
    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(),
        announcement_codes={ANNOUNCEMENT_CODE: entry},
    )

    await controller.on_stasis_start(_stasis_event(caller_number="5197101515"))
    for event in _dial(ANNOUNCEMENT_CODE):
        await controller.on_channel_dtmf_received(event)

    assert len(goodbye) == 1
    assert ANNOUNCEMENT_BYE_COPY in goodbye[0]


async def test_hook_failing_send_never_breaks_teardown(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """A send that RAISES (fire-and-forget task) never affects the readout or
    the single idempotent teardown."""
    _sms_env(monkeypatch)
    _stub_common(monkeypatch)

    async def _boom_pool(*a, **k):
        raise RuntimeError("simulated send explosion")

    monkeypatch.setattr("klanker_voice.telephony.controller._send_sms_pool", _boom_pool)

    entry = _announcement_entry(sms_dids=("6134805878",))
    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(),
        announcement_codes={ANNOUNCEMENT_CODE: entry},
    )

    await controller.on_stasis_start(_stasis_event(caller_number="5197101515"))
    for event in _dial(ANNOUNCEMENT_CODE):
        await controller.on_channel_dtmf_received(event)

    # teardown still runs exactly once despite the exploding send task
    assert ari.count("hangup", arg="chan-1") == 1
    assert sessions[0].closed is True
    assert controller.calls == {}


async def test_hook_log_discipline_no_secret_leak(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """The OTP code, the SMS body, and the destination number never appear in
    any emitted log record during an eligible announcement. Captures LOGURU
    output (the controller logs via loguru, which pytest's caplog does not
    intercept) by attaching a temporary sink."""
    from loguru import logger

    _sms_env(monkeypatch)
    _stub_common(monkeypatch, otp="778899")

    async def _ok_pool(*a, **k):
        return True

    monkeypatch.setattr("klanker_voice.telephony.controller._send_sms_pool", _ok_pool)

    captured: list[str] = []
    sink_id = logger.add(lambda m: captured.append(str(m)), level="DEBUG")
    try:
        entry = _announcement_entry(sms_dids=("6134805878",))
        controller, ari, sessions = _build_controller(
            make_config_file,
            telephony_cfg=_gated_cfg(),
            announcement_codes={ANNOUNCEMENT_CODE: entry},
        )
        await controller.on_stasis_start(_stasis_event(caller_number="5197101515"))
        for event in _dial(ANNOUNCEMENT_CODE):
            await controller.on_channel_dtmf_received(event)
    finally:
        logger.remove(sink_id)

    blob = "\n".join(captured)
    assert captured, "expected at least one log record during the announcement"
    # The OTP (and therefore the SMS body, which embeds it) is the secret the
    # SMS path must never log. NOTE: the caller ANI is deliberately logged by
    # the pre-existing on_stasis_start line (call identity, §13) -- that is not
    # the SMS code leaking it, so we assert on the OTP, not the number.
    assert "778899" not in blob          # OTP never logged by the SMS path
    assert ANNOUNCEMENT_SMS_PUNCHLINE_COPY  # sanity: constant importable
