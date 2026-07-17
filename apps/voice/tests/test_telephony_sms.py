"""Quick task 260716-hg5 (+ relay-via-auth follow-up) — CTF OTP SMS-during-call
("check your phone" punchline). Covers the dst helper, the GSM-7 body, the
spoken-script branch, the relay POST primitive (fake aiohttp — NO real network),
and the fire-early scheduling hook inside ``_gate_announcement`` (reusing the
controller-test fixtures/helpers).

The send no longer calls VoIP.ms directly: telephony-edge POSTs the built SMS to
the auth app's ``/ctf/sms`` relay (which egresses from the stable, whitelisted
NAT EIP). The VoIP.ms sendSMS + pool-fallback logic lives in the auth app and is
tested there (ctf-sms-route.test.ts).

Design doc: docs/superpowers/specs/2026-07-16-ctf-otp-sms-during-call-design.md
"""

from __future__ import annotations

from typing import Any

import pytest

from klanker_voice.telephony.config import AnnouncementEntry
from klanker_voice.telephony.controller import (
    ANNOUNCEMENT_BYE_COPY,
    ANNOUNCEMENT_SMS_PUNCHLINE_COPY,
    _build_announcement_script,
    _dialed_did_from_sip_to,
    _select_sms_send_dids,
    _send_sms_via_relay,
    _sms_dst_from_caller,
)

# Reuse the controller-test helpers/fixtures. `make_config_file`/
# `stub_provider_keys` come from conftest (auto-discovered). `fake_aws` and the
# autouse `stub_call_session_run` fixture live in sibling modules and MUST be
# imported so pytest registers them.
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

RELAY_URL = "https://auth.example/use1/ctf/sms"


# --- Fake aiohttp so _send_sms_via_relay's POST path is exercised offline -----


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
    """Records the single POST, returns a scripted response (or raises)."""

    last_url: str | None = None
    last_json: Any = None
    last_headers: Any = None

    def __init__(self, *, status: int = 200, payload: Any = None, raise_on_post: Exception | None = None):
        self._status = status
        self._payload = payload
        self._raise = raise_on_post

    async def __aenter__(self):
        return self

    async def __aexit__(self, *exc):
        return False

    def post(self, url, json=None, headers=None):
        _FakeSession.last_url = url
        _FakeSession.last_json = json
        _FakeSession.last_headers = headers
        if self._raise is not None:
            raise self._raise
        return _FakeResp(self._status, self._payload)


def _patch_session(monkeypatch, **kwargs):
    import aiohttp

    monkeypatch.setattr(aiohttp, "ClientSession", lambda *a, **kw: _FakeSession(**kwargs))


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


# --- GSM-7 body regression ---------------------------------------------------


def test_sms_body_is_gsm7_ascii_safe():
    """REGRESSION (live-proven): the SMS body MUST be pure 7-bit ASCII. A single
    non-GSM char (em-dash, curly quote, …) forces UCS-2 and the VoIP.ms->
    NA-mobile route SILENTLY DROPS UCS-2 while the send still reports success.
    Check the FORMATTED body (the {code} braces are substituted away)."""
    from klanker_voice.telephony.controller import ANNOUNCEMENT_SMS_BODY_TEMPLATE

    body = ANNOUNCEMENT_SMS_BODY_TEMPLATE.format(code="482913")
    non_ascii = [c for c in body if ord(c) >= 128]
    assert non_ascii == [], f"SMS body has non-GSM-7 (UCS-2-forcing) chars: {non_ascii!r}"
    assert "{" not in body and "}" not in body


# --- _build_announcement_script branch ---------------------------------------


def test_script_ineligible_is_byte_identical_to_legacy():
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
    assert "check your phone" in eligible
    assert "check your phone" not in legacy
    assert "<break" not in eligible and "/>" not in eligible
    assert "123456" not in eligible


# --- _send_sms_via_relay (POST to auth relay, faked transport) ---------------

DIDS = ("6134805878",)


async def test_relay_success_on_200_sent_true(monkeypatch):
    _patch_session(monkeypatch, status=200, payload={"sent": True})
    ok = await _send_sms_via_relay(RELAY_URL, {"Authorization": "Bearer t"}, "5197101515", "body", DIDS)
    assert ok is True
    # POSTs the built SMS envelope to the relay URL
    assert _FakeSession.last_url == RELAY_URL
    assert _FakeSession.last_json == {"to": "5197101515", "message": "body", "dids": ["6134805878"]}
    assert _FakeSession.last_headers == {"Authorization": "Bearer t"}


async def test_relay_false_on_non_200(monkeypatch):
    # the relay's uniform-404 failure response
    _patch_session(monkeypatch, status=404, payload="Not found")
    assert await _send_sms_via_relay(RELAY_URL, {}, "5197101515", "body", DIDS) is False


async def test_relay_false_when_sent_not_true(monkeypatch):
    _patch_session(monkeypatch, status=200, payload={"sent": False})
    assert await _send_sms_via_relay(RELAY_URL, {}, "5197101515", "body", DIDS) is False


async def test_relay_never_raises_on_transport_error(monkeypatch):
    _patch_session(monkeypatch, raise_on_post=RuntimeError("connreset"))
    assert await _send_sms_via_relay(RELAY_URL, {}, "5197101515", "body", DIDS) is False


async def test_relay_false_on_empty_inputs_no_post(monkeypatch):
    _FakeSession.last_url = None
    _patch_session(monkeypatch, status=200, payload={"sent": True})
    assert await _send_sms_via_relay("", {}, "5197101515", "body", DIDS) is False   # no url
    assert await _send_sms_via_relay(RELAY_URL, {}, "", "body", DIDS) is False       # no dst
    assert await _send_sms_via_relay(RELAY_URL, {}, "5197101515", "body", ()) is False  # no dids
    assert _FakeSession.last_url is None  # short-circuited before any POST


# --- _gate_announcement fire-early scheduling hook ---------------------------


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
    monkeypatch.setattr(
        "klanker_voice.telephony.controller.ANNOUNCEMENT_SLOW_DIGIT_SECONDS", 0.001
    )
    goodbye: list[str] = []

    async def _spy_goodbye(worker, copy):
        goodbye.append(copy)

    monkeypatch.setattr("klanker_voice.telephony.controller.speak_goodbye", _spy_goodbye)
    return goodbye


def _sms_entry(**overrides):
    return _announcement_entry(sms_dids=("6134805878",), sms_relay_url=RELAY_URL, **overrides)


async def test_hook_eligible_posts_one_relay_call_and_speaks_punchline(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    goodbye = _stub_common(monkeypatch)
    sent: list[tuple[Any, ...]] = []

    async def _fake_relay(url, headers, to, message, dids):
        sent.append((url, headers, to, message, dids))
        return True

    monkeypatch.setattr(
        "klanker_voice.telephony.controller._send_sms_via_relay", _fake_relay
    )

    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(),
        announcement_codes={ANNOUNCEMENT_CODE: _sms_entry()},
    )

    await controller.on_stasis_start(_stasis_event(caller_number="5197101515"))
    for event in _dial(ANNOUNCEMENT_CODE):
        await controller.on_channel_dtmf_received(event)

    assert len(sent) == 1
    url, headers, to, message, dids = sent[0]
    assert url == RELAY_URL
    assert to == "5197101515"
    assert "483920" in message              # the plain, copy-pasteable OTP
    assert dids == ("6134805878",)
    assert len(goodbye) == 1
    assert ANNOUNCEMENT_SMS_PUNCHLINE_COPY in goodbye[0]
    assert controller.calls == {}


async def test_hook_ineligible_caller_no_relay_legacy_signoff(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    goodbye = _stub_common(monkeypatch)

    async def _fail_relay(*a, **k):
        raise AssertionError("relay must not run for an ineligible caller")

    monkeypatch.setattr("klanker_voice.telephony.controller._send_sms_via_relay", _fail_relay)

    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(),
        announcement_codes={ANNOUNCEMENT_CODE: _sms_entry()},
    )
    await controller.on_stasis_start(_stasis_event(caller_number="1001"))  # extension
    for event in _dial(ANNOUNCEMENT_CODE):
        await controller.on_channel_dtmf_received(event)

    assert len(goodbye) == 1
    assert ANNOUNCEMENT_BYE_COPY in goodbye[0]
    assert ANNOUNCEMENT_SMS_PUNCHLINE_COPY not in goodbye[0]


async def test_hook_no_relay_url_behaves_legacy(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """An entry with a pool but NO relay URL -> no send (relay is the only send
    path) even for a textable caller; legacy sign-off."""
    goodbye = _stub_common(monkeypatch)

    async def _fail_relay(*a, **k):
        raise AssertionError("relay must not run without a relay URL")

    monkeypatch.setattr("klanker_voice.telephony.controller._send_sms_via_relay", _fail_relay)

    entry = _announcement_entry(sms_dids=("6134805878",))  # no sms_relay_url
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


async def test_hook_failing_relay_never_breaks_teardown(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    _stub_common(monkeypatch)

    async def _boom_relay(*a, **k):
        raise RuntimeError("simulated relay explosion")

    monkeypatch.setattr("klanker_voice.telephony.controller._send_sms_via_relay", _boom_relay)

    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(),
        announcement_codes={ANNOUNCEMENT_CODE: _sms_entry()},
    )
    await controller.on_stasis_start(_stasis_event(caller_number="5197101515"))
    for event in _dial(ANNOUNCEMENT_CODE):
        await controller.on_channel_dtmf_received(event)

    assert ari.count("hangup", arg="chan-1") == 1
    assert sessions[0].closed is True
    assert controller.calls == {}


async def test_hook_log_discipline_no_secret_leak(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """The OTP code and the SMS body never appear in any emitted log record
    during an eligible announcement. Captures LOGURU (caplog does not intercept
    it). The caller ANI is logged by the pre-existing on_stasis_start line, so we
    assert on the OTP, not the number."""
    from loguru import logger

    _stub_common(monkeypatch, otp="778899")

    async def _ok_relay(*a, **k):
        return True

    monkeypatch.setattr("klanker_voice.telephony.controller._send_sms_via_relay", _ok_relay)

    captured: list[str] = []
    sink_id = logger.add(lambda m: captured.append(str(m)), level="DEBUG")
    try:
        controller, ari, sessions = _build_controller(
            make_config_file,
            telephony_cfg=_gated_cfg(),
            announcement_codes={ANNOUNCEMENT_CODE: _sms_entry()},
        )
        await controller.on_stasis_start(_stasis_event(caller_number="5197101515"))
        for event in _dial(ANNOUNCEMENT_CODE):
            await controller.on_channel_dtmf_received(event)
    finally:
        logger.remove(sink_id)

    blob = "\n".join(captured)
    assert captured, "expected at least one log record during the announcement"
    assert "778899" not in blob  # OTP never logged by the SMS path


# --- Per-DID reply: _dialed_did_from_sip_to (SIP To: header parser) ----------


@pytest.mark.parametrize(
    "sip_to,expected",
    [
        # VoIP.ms inbound To: header carries the dialed DID in the sip: URI user
        ("<sip:17254043234@toronto.voip.ms>", "7254043234"),
        ('"Las Vegas" <sip:+17254043283@toronto.voip.ms>;tag=abc123', "7254043283"),
        ("<sip:7254043234@toronto.voip.ms>", "7254043234"),        # bare 10-digit user
        ("17254043234", "7254043234"),                              # no URI, just the number
        # the shared sub-account name is NOT a resolvable DID -> unknown ("")
        ("<sip:557010_klanker-pbx@toronto.voip.ms>", ""),
        ("", ""),                                                    # header absent
        (None, ""),                                                  # never raises
        ("<sip:+445551234567@toronto.voip.ms>", ""),                # non-NA -> unknown
    ],
)
def test_dialed_did_from_sip_to(sip_to, expected):
    assert _dialed_did_from_sip_to(sip_to) == expected


# --- Per-DID reply: _select_sms_send_dids (sender selection) -----------------


def _reply_entry(**overrides) -> AnnouncementEntry:
    return _announcement_entry(
        sms_dids=("6134805878",),
        sms_reply_dids=("7254043234", "7254043283"),
        sms_relay_url=RELAY_URL,
        **overrides,
    )


def test_select_send_dids_enrolled_replies_from_dialed_did():
    entry = _reply_entry()
    assert _select_sms_send_dids(entry, "7254043234") == ("7254043234",)
    assert _select_sms_send_dids(entry, "7254043283") == ("7254043283",)


def test_select_send_dids_resolved_but_unenrolled_sends_nothing():
    # a resolved-but-not-enrolled DID (e.g. reserved 613) gets NO text, even
    # though the announcement trigger itself is DID-agnostic.
    assert _select_sms_send_dids(_reply_entry(), "6134805878") == ()


def test_select_send_dids_unresolved_falls_back_to_legacy_pool():
    # To: parse miss (dialed DID unknown) -> the legacy pool, so the feature is
    # never stranded while the header mechanism is being verified live.
    assert _select_sms_send_dids(_reply_entry(), "") == ("6134805878",)


def test_select_send_dids_no_enrollment_is_byte_identical_legacy_pool():
    legacy = _announcement_entry(sms_dids=("6134805878",), sms_relay_url=RELAY_URL)
    # with no sms_reply_dids, the dialed DID is irrelevant -> always the pool.
    assert _select_sms_send_dids(legacy, "7254043234") == ("6134805878",)
    assert _select_sms_send_dids(legacy, "") == ("6134805878",)


# --- Per-DID reply: end-to-end through _gate_announcement --------------------


async def test_hook_per_did_enrolled_texts_from_dialed_did(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """A call to an ENROLLED dialed DID (surfaced via the KLANKER_SIP_TO channel
    var) is texted FROM that same DID -- not from the shared pool."""
    _stub_common(monkeypatch)
    sent: list[tuple[Any, ...]] = []

    async def _fake_relay(url, headers, to, message, dids):
        sent.append((url, headers, to, message, dids))
        return True

    monkeypatch.setattr("klanker_voice.telephony.controller._send_sms_via_relay", _fake_relay)

    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(),
        announcement_codes={ANNOUNCEMENT_CODE: _reply_entry()},
    )
    ari.channel_vars["KLANKER_SIP_TO"] = "<sip:17254043283@toronto.voip.ms>"

    await controller.on_stasis_start(_stasis_event(caller_number="5197101515"))
    for event in _dial(ANNOUNCEMENT_CODE):
        await controller.on_channel_dtmf_received(event)

    assert len(sent) == 1
    assert sent[0][4] == ("7254043283",)  # texted FROM the dialed DID
    assert sent[0][2] == "5197101515"      # to the caller's own ANI


async def test_hook_per_did_unenrolled_did_sends_nothing(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """A call to a resolved-but-UNENROLLED DID (e.g. reserved 613) gets no text
    and the legacy sign-off -- reserving that DID even though the trigger is
    DID-agnostic."""
    goodbye = _stub_common(monkeypatch)

    async def _fail_relay(*a, **k):
        raise AssertionError("relay must not run for an unenrolled dialed DID")

    monkeypatch.setattr("klanker_voice.telephony.controller._send_sms_via_relay", _fail_relay)

    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(),
        announcement_codes={ANNOUNCEMENT_CODE: _reply_entry()},
    )
    ari.channel_vars["KLANKER_SIP_TO"] = "<sip:16134805878@toronto.voip.ms>"

    await controller.on_stasis_start(_stasis_event(caller_number="5197101515"))
    for event in _dial(ANNOUNCEMENT_CODE):
        await controller.on_channel_dtmf_received(event)

    assert len(goodbye) == 1
    assert ANNOUNCEMENT_BYE_COPY in goodbye[0]
    assert ANNOUNCEMENT_SMS_PUNCHLINE_COPY not in goodbye[0]


async def test_hook_per_did_unresolved_falls_back_to_pool(
    make_config_file, stub_provider_keys, fake_aws, monkeypatch
):
    """When the To: header carries no resolvable DID (e.g. the shared
    sub-account name), the send falls back to the legacy pool so the feature is
    not stranded."""
    _stub_common(monkeypatch)
    sent: list[tuple[Any, ...]] = []

    async def _fake_relay(url, headers, to, message, dids):
        sent.append((url, headers, to, message, dids))
        return True

    monkeypatch.setattr("klanker_voice.telephony.controller._send_sms_via_relay", _fake_relay)

    controller, ari, sessions = _build_controller(
        make_config_file,
        telephony_cfg=_gated_cfg(),
        announcement_codes={ANNOUNCEMENT_CODE: _reply_entry()},
    )
    ari.channel_vars["KLANKER_SIP_TO"] = "<sip:557010_klanker-pbx@toronto.voip.ms>"

    await controller.on_stasis_start(_stasis_event(caller_number="5197101515"))
    for event in _dial(ANNOUNCEMENT_CODE):
        await controller.on_channel_dtmf_received(event)

    assert len(sent) == 1
    assert sent[0][4] == ("6134805878",)  # legacy pool fallback
