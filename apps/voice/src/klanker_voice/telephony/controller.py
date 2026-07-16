"""``AsteriskCallController`` -- the ARI/Stasis call-control seam (Phase 11,
D-02/D-04/R6).

**Process boundary (D-08, mirrors ``webrtc.py`` isolation).** This module
runs inside a **standalone telephony entrypoint** (a later plan, D-08) --
its own local process, run alongside the docker-compose Asterisk instance.
It is never imported by, and never imports, ``webrtc.py`` or the browser
``server.py``. It reuses ``factories.py`` / ``pipeline.py`` /
``call_runtime.py`` **in-process** (shared *code*), exactly the way
``server.py`` does for the browser path -- but the *process* is separate,
so a telephony bug can never take down the browser voice service and vice
versa.

**Responsibilities (D-02, spec Sec13).** ``AsteriskCallController`` consumes
ARI events and owns the ``calls: dict[str, ActiveCall]`` registry, keyed by
the *original* Asterisk SIP channel ID:

- :meth:`on_stasis_start` -- accepts only the expected inbound
  context/app, normalizes ANI/DID, binds the socket-backed
  :class:`~klanker_voice.telephony.rtp_socket.SocketRtpMediaSession`
  **before** creating Asterisk's External Media channel (R2 bind-first
  ordering -- ``connection_type=client`` means Asterisk always dials out),
  creates the External Media channel + a mixing bridge, attaches both
  channels, evaluates the quota gate, constructs a Klanker ``CallSession``
  via :func:`~klanker_voice.call_runtime.create_call_session`
  (``channel="pstn"``), and runs the worker as a tracked background task
  (Sec12 greeting-readiness ordering -- the pipeline is not started until
  the media path is fully wired).
- :meth:`on_channel_destroyed` / a hard session timeout -- both funnel
  through the single idempotent :meth:`_close_active_call`:
  ``CallSession.close()`` -> ``lifecycle.release()`` exactly once, then
  bridge/external-media-channel/RTP-socket teardown, then the registry
  entry is removed. No leaked resources (ROADMAP criterion 3, R6,
  T-11-05-01).
- A hard session timeout *also* ARI-hangs-up the original SIP channel
  (``lifecycle.on_released`` composed with ``ari.hangup(sip_channel_id)``,
  R6/Sec17/T-11-05-02) -- a wind-down that only cancels the Klanker-side
  pipeline would leave the caller's line silently open, still burning PSTN
  minutes.
- A quota-denied caller (the gate passed, but ``quota.start_gate`` then
  rejects -- e.g. ``ERROR_CONCURRENCY_LIMIT`` at ``max_concurrent_calls=1``)
  never gets a ``CallSession`` constructed; the bridge/external-media
  channel/socket already allocated for the gate are torn down and the SIP
  channel is hung up (R6 "quota-denied leaves no bridge", T-11-05-03).

**§24 gate (Plan 06, D-05).** ``on_stasis_start`` now branches on
``telephony_cfg.require_gate``:

- **Gated (default, production):** the persistent pipeline is built
  immediately (via :func:`~klanker_voice.call_runtime.create_call_session`,
  with a :class:`~klanker_voice.telephony.gate.GateProcessor` threaded into
  it and a zeroed, ``bypass_accounting=True`` placeholder ``GateResult`` --
  see ``_bypass_gate_result``) so STT (+ ARI DTMF) can run during the gate
  with NO real accounting/timer engaged yet (D-05d). The caller stays dark
  -- no greeting, no LLM, no TTS -- until :meth:`GateProcessor.unlock`
  fires, from either the spoken 4-word passphrase (matched inside the
  processor, D-05b) or an ARI ``ChannelDtmfReceived`` PIN match
  (:meth:`on_channel_dtmf_received`, entirely outside the pipeline/LLM).
  On unlock, :meth:`_gate_unlock` calls the REAL ``quota.start_gate`` and,
  on success, promotes the placeholder lifecycle via
  ``SessionLifecycle.upgrade_from_bypass`` and fires
  :func:`~klanker_voice.pipeline.greet_now` (D-05c -- the greeting fires
  HERE, not on answer). A quota rejection at that point, or the gate's own
  ``gate_window_seconds`` expiry with no unlock, both funnel through
  :meth:`_gate_fail_closed` -- a deterministic goodbye (bypasses the LLM,
  mirrors ``pipeline.speak_goodbye``) then hangup then the single
  idempotent :meth:`_close_active_call` teardown (D-05d: never a silent
  open PSTN call).
- **Ungated (``require_gate=False``, test/dev-only escape hatch per
  ``TelephonyConfig``'s own docstring):** the Plan-05 interim behavior is
  preserved byte-for-byte -- ``quota.start_gate`` is called immediately at
  StasisStart with ``telephony_cfg.unlock_tier_id``, and
  ``register_greet_first`` greets on connect. This keeps every Plan-05
  lifecycle test (bridge/teardown plumbing, unrelated to the gate itself)
  green unchanged; the new gated flow gets its own dedicated tests.

The PIN (``TELEPHONY_ACCESS_PIN``) and passphrase words
(``TELEPHONY_PASSPHRASE_WORDS``) are read ONCE, at
:class:`AsteriskCallController` construction (or injected directly for
tests) -- never re-read per call, never logged (D-05e).

**CTF phone-OTP DTMF-code trigger (quick task 260716-1g0, Revision 2 --
design doc docs/superpowers/specs/2026-07-15-ctf-phone-otp-announcement-did-
design.md).** A caller already INSIDE the §24 gate who enters an armed
announcement code (checked AFTER the real PIN, only on a PIN miss) is
dispatched to :meth:`_gate_announcement` from
:meth:`on_channel_dtmf_received` -- fetches the current OTP and speaks it
(digit-spaced, twice), then tears the call down via the single idempotent
:meth:`_close_active_call`. Mirrors :meth:`_gate_fail_closed`'s shape
exactly, but resolves the gate via :meth:`~klanker_voice.telephony.gate.
GateProcessor.cancel_for_takeover` (NOT ``unlock``) so the §24 redaction
boundary (D-05e) stays closed the whole time and no fail-closed timer can
race the announcement's own goodbye. Never calls ``quota.start_gate`` or
``greet_now`` -- no concierge, no metered session. Supersedes Revision 1's
DID-keyed PRE-gate dispatch, which never fired on a live call (VoIP.ms
routes every DID to one sub-account, so the dialed number is invisible at
``on_stasis_start``).

**Pickup cue (quick task 260713-m9n).** Both finish paths register a
fire-once ``on_client_connected`` handler (:meth:`_register_pickup_cue`)
that plays a short ring + pre-rendered KPH "hey" prompt
(:func:`~klanker_voice.telephony.pickup_cue.play_pickup_cue`) the moment the
media path is ready -- including during the §24 gate window, before unlock.
It is additive to (never replaces) the existing ``on_client_connected``
wiring, and is pre-rendered OUTBOUND-only audio (no transcription forward,
no LLM turn, no TTS call), so the D-05d cost invariant is unchanged.

**Logging discipline (§13).** Structured logs always carry the call ID.
This module NEVER logs SIP passwords, ARI auth headers, or full
PIN/passphrase values -- :class:`~klanker_voice.telephony.ari.AriError`
already enforces this one layer down (never embeds credentials), and no
code here introduces a new place credentials could leak.
"""

from __future__ import annotations

import asyncio
import os
import re
import time
import uuid
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field, replace
from typing import Any
from urllib.parse import quote

import aiohttp
from loguru import logger

from klanker_voice import quota
from klanker_voice.auth import AuthError, SessionIdentity, validate_access_token
from klanker_voice.call_runtime import CallIdentity, CallSession, create_call_session
from klanker_voice.config import DuplexConfig, KnowledgeConfig, PipelineConfig, QuotaConfig
from klanker_voice.pipeline import greet_now, speak_goodbye
from klanker_voice.telephony.ari import AriClient, AriError
from klanker_voice.telephony.config import AnnouncementEntry, TelephonyConfig
from klanker_voice.telephony.gate import GateProcessor, accumulate_dtmf
from klanker_voice.telephony.pickup_cue import play_pickup_cue
from klanker_voice.telephony.rtp_socket import SocketRtpMediaSession
from klanker_voice.telephony.transport import TelephonyTransport
from klanker_voice.telephony.types import RtpMediaSession, TelephonyTransportParams

#: Signature of the callable that opens a bound, listening RTP media session.
#: Defaults to :meth:`SocketRtpMediaSession.open`; a test can inject a fake
#: opener so the §16 lifecycle matrix never binds a real UDP socket.
MediaSessionOpener = Callable[[str, int], Awaitable[RtpMediaSession]]

#: The one Stasis app name every Phase-11 Asterisk config (``extensions.conf``
#: / ``ari.conf``) is wired to (11-02-SUMMARY.md).
DEFAULT_APP_NAME = "klanker"

#: The one inbound dialplan context every StasisStart must have come through
#: (``apps/voice/asterisk/extensions.conf``, 11-02-SUMMARY.md, T-11-02-01) --
#: anything else is an unexpected/hostile entry and is rejected, never
#: allocated a bridge (D-02).
DEFAULT_EXPECTED_CONTEXT = "from-klanker-inbound"

#: §24 gate secrets (D-09/D-05e) -- env only, NEVER TOML (config.py's
#: credential-field-name regex already refuses these two shapes if smuggled
#: into pipeline.toml). Read exactly once, at controller construction.
PIN_ENV_VAR = "TELEPHONY_ACCESS_PIN"
PASSPHRASE_WORDS_ENV_VAR = "TELEPHONY_PASSPHRASE_WORDS"

#: Deterministic, LLM-free fail-closed goodbye (D-05d) -- shared by both the
#: gate-window-expiry timeout and a quota rejection discovered right after a
#: successful unlock. Sent straight to TTS (``pipeline.speak_goodbye``),
#: exactly like the existing session wind-down goodbye.
GATE_FAIL_CLOSED_COPY = (
    "Sorry, I wasn't able to verify access on this line. Goodbye."
)

#: Short timeout for the /tel mint HTTP call (D-02, Phase 12 Plan 06): this
#: happens once, on the call-setup critical path, before the caller ever
#: hears anything -- a slow/unresponsive mint endpoint must never hang a
#: caller indefinitely.
TEL_MINT_TIMEOUT_SECONDS = 3.0

#: Short timeout for the CTF phone-OTP /ctf/otp fetch (quick task 260715-oq0,
#: T-OTP-05) -- mirrors TEL_MINT_TIMEOUT_SECONDS: a slow/unresponsive auth
#: endpoint must never hang the announcement-DID call.
CTF_OTP_FETCH_TIMEOUT_SECONDS = 3.0

#: Bounded wait for the OTP announcement line to finish playing before the
#: call is torn down (T-OTP-05). Mirrors the existing goodbye leg's proven
#: pattern (``_gate_fail_closed``: ``speak_goodbye`` ->
#: ``asyncio.sleep(...)`` -> ``_close_active_call``) rather than a
#: frame-level completion event -- a fixed, generous grace period so a
#: stuck TTS synth can NEVER hang the PSTN line. The spoken line is two
#: digit-spaced 6-digit reads plus a short intro/outro (roughly 25 spoken
#: "words"); 12s is comfortably above a natural reading of that length even
#: with ElevenLabs' slower delivery. Longer than the plain
#: ``goodbye_grace_seconds`` (5s) used by ``_gate_fail_closed``'s own
#: shorter fixed copy. NOTE: this is now the BASE grace for the surrounding
#: speech only -- ``_gate_announcement`` adds per-digit pause time on top
#: (``2 * len(code) * ANNOUNCEMENT_DIGIT_PAUSE_SECONDS``) so the slowed,
#: spoken-twice readout is never cut off mid-number.
ANNOUNCEMENT_PLAYBACK_GRACE_SECONDS = 12.0

#: Silence inserted between each spoken digit of the OTP readout so the
#: caller can write the number down (the operator asked for it to "slow WAY
#: down"). Rendered as an ElevenLabs ``<break time="Xs" />`` tag AND a
#: trailing period per digit (belt-and-suspenders: the period paces the
#: read even if a model ignores the break tag). Tunable -- raise for a
#: slower read, lower for a snappier one.
ANNOUNCEMENT_DIGIT_PAUSE_SECONDS = 1.0

#: Bound on the raw trailing ARI DTMF digit buffer (quick task 260716-1g0)
#: used ONLY for announcement-code suffix matching -- separate from
#: ``ActiveCall.dtmf_buffer``, which stays windowed to ``len(pin)`` for the
#: real PIN. Generous enough for fat-fingered noise before the 6-digit
#: announcement code while staying bounded (never grows unbounded across a
#: long gate window).
DTMF_RAW_MAX_DIGITS = 32

_E164_STRIP_RE = re.compile(r"[^\d+]")


def _normalize_e164(raw: Any) -> str:
    """Best-effort E.164 normalization for an ARI caller-ID field (Phase 12
    Plan 06, D-02) -- mirrors ``apps/auth/webapp/src/lib/phone-normalization.
    ts``'s ``normalizeE164`` line-for-line so the SAME canonical form is
    produced on both sides of the ``/tel`` mint call (the auth-app's
    ``byPhone`` GSI lookup was written against this exact shape). Never
    raises on odd input (``None``, non-string, empty) -- returns ``""``."""
    text = "" if raw is None else str(raw).strip()
    if not text:
        return ""
    cleaned = _E164_STRIP_RE.sub("", text)
    if cleaned.startswith("+"):
        cleaned = cleaned[1:]
    cleaned = cleaned.lstrip("0")
    if len(cleaned) == 10 or (len(cleaned) == 11 and cleaned.startswith("1")):
        if not cleaned.startswith("1"):
            cleaned = "1" + cleaned
    if not cleaned:
        return ""
    return "+" + cleaned


async def _fetch_tel_token(url: str, headers: dict[str, str]) -> str | None:
    """Issue the private ``/tel`` mint GET request, returning the minted
    token string on a 200 JSON response with a non-empty ``token`` field, or
    ``None`` for ANY failure -- non-200 (incl. the endpoint's own uniform
    404 no-oracle response), timeout, network error, or a malformed body.
    Never raises (D-02/D-05: every failure mode fails closed identically).

    A module-level function (not a method) so tests can monkeypatch it
    directly to avoid a real network call, mirroring how
    ``quota.start_gate``/``greet_now``/``speak_goodbye`` are already stubbed
    at module level in this test suite."""
    try:
        timeout = aiohttp.ClientTimeout(total=TEL_MINT_TIMEOUT_SECONDS)
        async with aiohttp.ClientSession(timeout=timeout) as session:
            async with session.get(url, headers=headers) as resp:
                if resp.status != 200:
                    return None
                data = await resp.json(content_type=None)
    except Exception:  # noqa: BLE001 -- any transport/parse failure is a mint failure, never fatal
        logger.warning("tel mint: /tel request failed (transport/parse error)")
        return None
    token = data.get("token") if isinstance(data, dict) else None
    return token if isinstance(token, str) and token else None


def _build_announcement_line(template: str, code: str) -> str:
    """Substitute the digit-spaced ``code`` into every ``{code}`` occurrence
    of ``template`` (quick task 260715-oq0). Digit-spacing is a plain
    space-join over the code string is now paced: each digit gets a trailing
    period and a ``<break time="Ns" />`` tag between digits (e.g.
    ``"123456"`` -> ``'1. <break time="1.0s" /> 2. <break ...> ... 6.'``) so
    TTS reads it slowly, one digit at a time, with a real pause the caller
    can write down (``ANNOUNCEMENT_DIGIT_PAUSE_SECONDS``); ``str.replace``
    substitutes EVERY occurrence, matching the design's "speak it twice"
    template shape. A standalone, pure, module-level function so it's
    unit-testable without a call (no controller/ARI/pipeline dependency)."""
    paced = f' <break time="{ANNOUNCEMENT_DIGIT_PAUSE_SECONDS}s" /> '.join(
        f"{digit}." for digit in code
    )
    return template.replace("{code}", paced)


async def _fetch_ctf_otp(url: str, headers: dict[str, str]) -> str | None:
    """Issue the private ``/ctf/otp`` GET request (quick task 260715-oq0),
    returning the current-step OTP code string on a 200 JSON response with a
    non-empty ``code`` field, or ``None`` for ANY failure -- non-200 (incl.
    the endpoint's own uniform 404 no-oracle response), timeout, network
    error, or a malformed body. Never raises (T-OTP-05: every failure mode
    fails closed identically). Never logs the URL or any response body
    content (T-OTP-04).

    Mirrors :func:`_fetch_tel_token` exactly, including being a module-level
    function (not a method) so tests can monkeypatch it directly to avoid a
    real network call."""
    try:
        timeout = aiohttp.ClientTimeout(total=CTF_OTP_FETCH_TIMEOUT_SECONDS)
        async with aiohttp.ClientSession(timeout=timeout) as session:
            async with session.get(url, headers=headers) as resp:
                if resp.status != 200:
                    return None
                data = await resp.json(content_type=None)
    except Exception:  # noqa: BLE001 -- any transport/parse failure is a fetch failure, never fatal
        logger.warning("ctf otp: /ctf/otp request failed (transport/parse error)")
        return None
    code = data.get("code") if isinstance(data, dict) else None
    return code if isinstance(code, str) and code else None


def _bypass_gate_result() -> quota.GateResult:
    """A zeroed, ``bypass_accounting=True`` placeholder (D-05): lets
    :func:`~klanker_voice.call_runtime.create_call_session` build the
    persistent gated pipeline (and a ``SessionLifecycle``) up front, before
    any real access has been proven, with NO real accounting/timer engaged
    (``SessionLifecycle.start()`` skips its tick/timer/watchdog loops
    entirely for a bypass session). ``SessionLifecycle.upgrade_from_bypass``
    (session.py, Rule 2 auto-add) promotes this into a REAL metered session
    once ``quota.start_gate`` actually grants a tier at unlock (D-05a/c)."""
    placeholder_tier = quota.Tier(
        tier_id=quota.NO_ACCESS_TIER_ID, session_max_seconds=0, period_max_seconds=0, max_concurrent=0
    )
    return quota.GateResult(
        session_id=str(uuid.uuid4()),
        tier=placeholder_tier,
        session_max_seconds=0,
        remaining_daily_seconds=0,
        bypass_accounting=True,
    )


@dataclass
class ActiveCall:
    """One live (or gate-pending) PSTN call, registered by
    :meth:`AsteriskCallController.on_stasis_start` and torn down exactly
    once by :meth:`AsteriskCallController._close_active_call` (§13 field
    shape, D-02).

    ``call_session`` is ``None`` only for the brief window between the
    bridge/external-media allocation and a successful ``quota.start_gate``
    call -- if the gate rejects, this ``ActiveCall`` is never registered at
    all (R6 "quota-denied leaves no bridge"), so every entry that actually
    reaches ``self.calls`` has a real ``call_session``.
    """

    sip_channel_id: str
    external_media_channel_id: str
    bridge_id: str
    media_session: RtpMediaSession
    call_session: CallSession
    caller_id: str
    did: str
    created_at: float
    closed: bool = False
    lock: asyncio.Lock = field(default_factory=asyncio.Lock)
    #: Phase 11 Plan 06 (D-05): the §24 gate for this call, or ``None`` when
    #: ``telephony_cfg.require_gate`` is False (the ungated escape hatch --
    #: the call is already fully granted by the time it's registered).
    gate: GateProcessor | None = None
    #: The controller's own accumulated ARI ``ChannelDtmfReceived`` digit
    #: buffer for this call (Landmine 5: ARI delivers one event per digit).
    dtmf_buffer: str = ""
    #: Quick task 260716-1g0 (Revision 2): a SEPARATE raw trailing-digit
    #: buffer (bounded to ``DTMF_RAW_MAX_DIGITS``), used only for
    #: announcement-code suffix matching -- additive, never replaces or
    #: windows ``dtmf_buffer`` (the real PIN path stays byte-for-byte
    #: unchanged).
    dtmf_raw: str = ""
    #: Phase 12 Plan 06 (D-02/D-05): the tier :meth:`AsteriskCallController.
    #: _gate_unlock` grants on a successful gate factor. ``None`` means
    #: "never grant a tier for this call" -- set only when
    #: ``telephony_cfg.tel_mint_url`` IS configured and the caller-ID mint
    #: has already failed (fail-closed already in progress; see
    #: :meth:`AsteriskCallController._finish_stasis_start_gated`). When
    #: ``tel_mint_url`` is unconfigured (the legacy/default case), this is
    #: the static ``telephony_cfg.unlock_tier_id`` -- byte-identical to
    #: Phase 11's behavior.
    grant_tier_id: str | None = None


def _normalize_token(raw: Any) -> str:
    """Best-effort string normalization for caller-supplied ARI event
    fields (ANI/DID) -- never trusted for control, only for identity/logging
    (§13). Never raises on odd input (missing/None/non-string)."""
    if raw is None:
        return ""
    return str(raw).strip()


class AsteriskCallController:
    """Consumes ARI events and owns the ``calls`` registry (D-02, Sec13).

    Constructed once per process by the (later) standalone telephony
    entrypoint (D-08) and wired to an already-``connect()``ed
    :class:`~klanker_voice.telephony.ari.AriClient` via :meth:`register`
    (or by registering :meth:`on_stasis_start` / :meth:`on_channel_destroyed`
    directly with ``ari.on(...)``).
    """

    def __init__(
        self,
        ari: AriClient,
        cfg: PipelineConfig,
        knowledge_cfg: KnowledgeConfig,
        quota_cfg: QuotaConfig,
        telephony_cfg: TelephonyConfig,
        *,
        app_name: str = DEFAULT_APP_NAME,
        expected_context: str = DEFAULT_EXPECTED_CONTEXT,
        rtp_bind_host: str = "0.0.0.0",
        rtp_advertise_host: str = "127.0.0.1",
        media_session_opener: MediaSessionOpener = SocketRtpMediaSession.open,
        access_pin: str | None = None,
        passphrase_words: frozenset[str] | None = None,
        announcement_codes: dict[str, AnnouncementEntry] | None = None,
    ) -> None:
        self._ari = ari
        self._cfg = cfg
        self._knowledge_cfg = knowledge_cfg
        self._quota_cfg = quota_cfg
        self._telephony_cfg = telephony_cfg
        self._app_name = app_name
        self._expected_context = expected_context
        self._rtp_bind_host = rtp_bind_host
        self._rtp_advertise_host = rtp_advertise_host
        self._open_media_session = media_session_opener

        # §24 gate secrets (D-05e/D-09): read ONCE here -- from the explicit
        # kwarg when a caller (tests) injects one, else from env -- never
        # re-read per call, never logged.
        self._pin = access_pin if access_pin is not None else os.environ.get(PIN_ENV_VAR, "")
        if passphrase_words is not None:
            self._passphrase_words = frozenset(w.strip().lower() for w in passphrase_words if w.strip())
        else:
            raw_words = os.environ.get(PASSPHRASE_WORDS_ENV_VAR, "")
            self._passphrase_words = frozenset(w.strip().lower() for w in raw_words.split() if w.strip())

        #: Quick task 260716-1g0 (Revision 2): armed DTMF trigger code ->
        #: AnnouncementEntry, built once from telephony_cfg.announcements.
        #: Resolved from the explicit `announcement_codes` kwarg when a
        #: caller (tests) injects one, else from os.environ[entry.
        #: code_env_var] -- an entry whose env var is unset/empty is
        #: skipped entirely (no trigger armed, gate behaves normally). Empty
        #: dict for every deployment without an [[telephony.announcement]]
        #: table, or where the code env var isn't set -- the default,
        #: byte-unaffected case.
        if announcement_codes is not None:
            self._announcements_by_code: dict[str, AnnouncementEntry] = dict(announcement_codes)
        else:
            self._announcements_by_code = {
                code: entry
                for entry in telephony_cfg.announcements
                if (code := os.environ.get(entry.code_env_var, "").strip())
            }

        #: D-02's registry, keyed by the original Asterisk SIP channel ID.
        self.calls: dict[str, ActiveCall] = {}

        #: Strong references to each call's tracked background
        #: ``call_session.run()`` task (mirrors ``server.py``'s
        #: ``SESSION_TASKS`` pattern -- ``asyncio.create_task`` only holds a
        #: *weak* reference, so without retaining it here a still-running
        #: call's task could be garbage-collected mid-call).
        self._tasks: dict[str, asyncio.Task] = {}

    def register(self) -> None:
        """Wire :meth:`on_stasis_start` / :meth:`on_channel_destroyed` /
        :meth:`on_channel_dtmf_received` onto the ``AriClient``'s event
        dispatch (convenience for the standalone entrypoint, D-08) -- tests
        may instead call the handlers directly."""
        self._ari.on("StasisStart", self.on_stasis_start)
        self._ari.on("ChannelDestroyed", self.on_channel_destroyed)
        self._ari.on("ChannelDtmfReceived", self.on_channel_dtmf_received)

    # --- StasisStart: allocate + construct (Task 1) ------------------------

    async def on_stasis_start(self, event: dict[str, Any]) -> None:
        """Handle one ARI ``StasisStart`` event (D-02/D-04, Sec12/Sec13).

        Accepts only the expected inbound context/app; anything else is
        hung up immediately with no allocation. On the happy path: answer
        -> bind the socket media session FIRST (R2) -> create the External
        Media channel + mixing bridge -> attach both channels -> then
        branch on ``telephony_cfg.require_gate`` (Plan 06, see module
        docstring "§24 gate") into either the gated flow (§24 answer-gate,
        default/production) or the ungated escape hatch (Plan 05's interim
        immediate-grant behavior, test/dev-only).
        """
        channel = event.get("channel", {}) or {}
        sip_channel_id = _normalize_token(channel.get("id"))
        channel_name = channel.get("name", "") or ""
        application = event.get("application", "")
        dialplan = channel.get("dialplan", {}) or {}
        context = dialplan.get("context", "")

        # The externalMedia channel we create below (D-08) re-enters THIS same
        # Stasis app and fires its own StasisStart with context='default'. It is
        # an internal media leg (Asterisk technology "UnicastRTP"), already
        # bridged in this handler -- never a new inbound call. Ignore it; hanging
        # it up (via the guard below) would kill our own audio path. Surfaced by
        # the §19-C live softphone proof: fake-media tests never create a real
        # externalMedia channel that re-enters Stasis, so they can't catch this.
        if channel_name.startswith("UnicastRTP"):
            logger.debug(
                f"on_stasis_start: ignoring external-media leg channel={sip_channel_id!r} "
                f"name={channel_name!r}"
            )
            return

        if application != self._app_name or context != self._expected_context:
            logger.warning(
                f"on_stasis_start: unexpected app={application!r} context={context!r} "
                f"channel={sip_channel_id!r}; hanging up, no allocation"
            )
            if sip_channel_id:
                await self._safe_ari(self._ari.hangup(sip_channel_id), "hangup (unexpected context)")
            return

        caller_id = _normalize_token((channel.get("caller") or {}).get("number"))
        did = _normalize_token(dialplan.get("exten"))

        logger.info(f"on_stasis_start: channel={sip_channel_id} caller={caller_id} did={did}")

        await self._ari.answer(sip_channel_id)

        # R2: Klanker must already be bound and listening BEFORE Asterisk's
        # externalMedia channel is created -- connection_type=client means
        # Asterisk always dials OUT to us; a not-yet-bound port silently
        # drops the first datagrams (UDP has no handshake/retry).
        media = await self._open_media_session(self._rtp_bind_host, 0)
        bound_port = media.bound_port

        bridge_id: str | None = None
        external_media_channel_id: str | None = None
        try:
            external_media_channel_id = await self._ari.create_external_media(
                app=self._app_name,
                external_host=f"{self._rtp_advertise_host}:{bound_port}",
                fmt="ulaw",
            )
            bridge_id = await self._ari.create_bridge("mixing")
            await self._ari.add_channel(bridge_id, sip_channel_id)
            await self._ari.add_channel(bridge_id, external_media_channel_id)
        except Exception:
            logger.exception(
                f"on_stasis_start: failed to establish media/bridge for channel={sip_channel_id}"
            )
            await self._teardown_gate_resources(
                bridge_id, external_media_channel_id, media, sip_channel_id
            )
            return

        transport_params = TelephonyTransportParams(
            clock_rate=self._telephony_cfg.sample_rate,
            packet_time_ms=self._telephony_cfg.packet_ms,
            samples_per_packet=self._telephony_cfg.sample_rate
            * self._telephony_cfg.packet_ms
            // 1000,
        )
        transport = TelephonyTransport(call_id=sip_channel_id, media=media, params=transport_params)
        identity = CallIdentity(
            subject=f"tel:{caller_id or sip_channel_id}", authenticated=True, auth_method="pstn"
        )

        if not self._telephony_cfg.require_gate:
            await self._finish_stasis_start_ungated(
                sip_channel_id=sip_channel_id,
                caller_id=caller_id,
                did=did,
                media=media,
                bridge_id=bridge_id,
                external_media_channel_id=external_media_channel_id,
                transport=transport,
                identity=identity,
            )
            return

        await self._finish_stasis_start_gated(
            sip_channel_id=sip_channel_id,
            caller_id=caller_id,
            did=did,
            media=media,
            bridge_id=bridge_id,
            external_media_channel_id=external_media_channel_id,
            transport=transport,
            identity=identity,
        )

    def _register_pickup_cue(self, transport: TelephonyTransport, call_session: CallSession) -> None:
        """Quick task 260713-m9n: register a fire-once ``on_client_connected``
        handler that plays the ring+hey pickup cue the moment the media path
        is ready. Additive -- ``TelephonyTransport`` fires
        ``on_client_connected`` exactly once (its own internal fire-once
        guard, transport.py), and pipecat's event-handler registry supports
        multiple handlers per event name (``BaseObject.add_event_handler``
        appends to a list) -- ``create_call_session``'s own handler(s)
        (lifecycle reconnect, and the D-05c-gated ``register_greet_first``
        for the ungated flow) are never replaced or suppressed by this one.

        Called from BOTH the gated and ungated finish paths, right after
        ``create_call_session`` and before the worker task is spawned --
        during the gate window (before unlock) on the gated path. The cue is
        pre-rendered, OUTBOUND-only audio: it forwards no transcription
        frame, invokes no LLM turn, and makes no TTS API call, so the D-05d
        "no billed turn until unlock" invariant is unchanged (T-M9N-01).
        Caller speech mid-cue flushes it via the pipeline's existing
        ``InterruptionFrame`` (``telephony.pickup_cue`` module docstring,
        T-M9N-02) -- inbound audio still flows to STT for passphrase
        matching regardless."""

        @transport.event_handler("on_client_connected")
        async def _on_pickup_cue_ready(transport, client):  # noqa: ANN001 -- pipecat handler shape
            await play_pickup_cue(call_session.worker)

    async def _finish_stasis_start_ungated(
        self,
        *,
        sip_channel_id: str,
        caller_id: str,
        did: str,
        media: RtpMediaSession,
        bridge_id: str,
        external_media_channel_id: str,
        transport: TelephonyTransport,
        identity: CallIdentity,
    ) -> None:
        """``telephony_cfg.require_gate=False`` escape hatch: Plan 05's
        interim behavior, preserved byte-for-byte -- grant
        ``telephony_cfg.unlock_tier_id`` immediately via ``quota.start_gate``
        and greet on connect (no §24 gate at all). Test/dev-only per
        ``TelephonyConfig.require_gate``'s own docstring; never expected in
        a production deployment."""
        gate_identity = SessionIdentity(
            sub=f"tel:{caller_id or sip_channel_id}",
            tier_id=self._telephony_cfg.unlock_tier_id,
            group=None,
            bypass_accounting=False,
        )
        try:
            gate_result = quota.start_gate(
                gate_identity,
                active_session_count=len(self.calls),
                per_task_max_sessions=self._telephony_cfg.max_concurrent_calls,
                heartbeat_ttl_seconds=self._quota_cfg.heartbeat_ttl,
                sub_floor_seconds=self._quota_cfg.sub_floor_seconds,
            )
        except quota.QuotaError as exc:
            # R6 "quota-denied leaves no bridge": the gate's own bridge +
            # external-media channel + socket are torn down; NO CallSession
            # is ever constructed for this caller.
            logger.warning(
                f"on_stasis_start: quota denied ({exc.error_type}) channel={sip_channel_id}"
            )
            await self._teardown_gate_resources(
                bridge_id, external_media_channel_id, media, sip_channel_id
            )
            return

        call_session = await create_call_session(
            transport=transport,
            identity=identity,
            gate_result=gate_result,
            cfg=self._cfg,
            knowledge_cfg=self._knowledge_cfg,
            duplex_cfg=DuplexConfig(),
            quota_cfg=self._quota_cfg,
            channel="pstn",
            metadata={"call_id": sip_channel_id, "did": did},
        )

        active_call = ActiveCall(
            sip_channel_id=sip_channel_id,
            external_media_channel_id=external_media_channel_id,
            bridge_id=bridge_id,
            media_session=media,
            call_session=call_session,
            caller_id=caller_id,
            did=did,
            created_at=time.time(),
        )
        self.calls[sip_channel_id] = active_call
        self._register_pickup_cue(transport, call_session)

        # R6: a hard session timeout (SessionLifecycle's own D-02 wall-clock
        # cutoff) must ALSO reach the SIP channel -- runner.cancel() alone
        # only ends the Klanker-side pipeline, leaving the PSTN line open
        # (T-11-05-02). Compose the default on_released (runner.cancel, set
        # by create_call_session) with the ARI hangup, then route through
        # the single idempotent teardown so bridge/external/socket/registry
        # are cleaned up too.
        async def _on_released() -> None:
            await call_session.runner.cancel("session wind-down complete")
            await self._safe_ari(
                self._ari.hangup(sip_channel_id), "hangup sip channel (hard timeout)"
            )
            await self._close_active_call(active_call, "hard timeout release")

        call_session.lifecycle.on_released = _on_released

        task = asyncio.create_task(call_session.run())
        self._tasks[sip_channel_id] = task
        task.add_done_callback(lambda _t, cid=sip_channel_id: self._tasks.pop(cid, None))

    async def _mint_tier_from_caller_id(
        self, normalized_caller_id: str
    ) -> tuple[str | None, str | None]:
        """D-02: call the private ``/tel`` mint endpoint for
        ``normalized_caller_id`` and validate the returned token via the
        SAME offline auth path the browser uses
        (:func:`klanker_voice.auth.validate_access_token`) to obtain the
        caller's entitled tier_id. Returns ``(None, None)`` on ANY failure
        -- no caller ID, HTTP non-200/timeout/network error, a
        missing/invalid token body, or token validation failure -- never
        raises (D-05: every failure mode fails closed identically,
        mirroring the /tel endpoint's own no-oracle contract, T-12-06-04).

        Phase 15 (LEDG-01): widened to ALSO return the validated token's
        ``sub`` (``anon:<code>:<uuid>``, the same shape the browser bypass
        `/join` path mints) alongside ``tier_id`` -- the ONLY place a PSTN
        caller's raw access code is ever recoverable, so
        :func:`~klanker_voice.call_runtime.create_call_session`'s ledger
        tap can hash it (never logged, never stored raw -- T-15-03-03).

        The Bearer token is read from the env var NAMED by
        ``telephony_cfg.tel_mint_env_var`` at call time -- never a literal
        (T-12-06-03)."""
        if not normalized_caller_id:
            return None, None
        bearer = os.environ.get(self._telephony_cfg.tel_mint_env_var, "")
        headers = {"Authorization": f"Bearer {bearer}"} if bearer else {}
        url = (
            f"{self._telephony_cfg.tel_mint_url.rstrip('/')}/"
            f"{quote(normalized_caller_id, safe='')}"
        )
        token = await _fetch_tel_token(url, headers)
        if not token:
            return None, None
        try:
            identity = validate_access_token(token)
        except AuthError:
            logger.warning("tel mint: minted token failed validation")
            return None, None
        return identity.tier_id, identity.sub

    async def _finish_stasis_start_gated(
        self,
        *,
        sip_channel_id: str,
        caller_id: str,
        did: str,
        media: RtpMediaSession,
        bridge_id: str,
        external_media_channel_id: str,
        transport: TelephonyTransport,
        identity: CallIdentity,
    ) -> None:
        """The §24 silent answer-gate (D-05, Plan 06): build the persistent
        pipeline with an inline ``GateProcessor`` NOW (using a zeroed
        bypass ``GateResult`` placeholder, D-05d -- no real accounting/
        timer starts yet), run it as a tracked background task so STT (+
        ARI DTMF) can observe the caller immediately, and defer the REAL
        ``quota.start_gate`` call + greeting to :meth:`_gate_unlock`, which
        fires from ``GateProcessor.unlock`` (passphrase, internal, or DTMF,
        external via :meth:`on_channel_dtmf_received`). Fail-closed
        (gate-window expiry OR a quota rejection right after unlock) both
        route through :meth:`_gate_fail_closed`.

        Phase 12 Plan 06 (D-02/D-05, §23 composed in front of the unchanged
        §24 gate): BEFORE the gate window ever opens, resolve the caller's
        entitled tier via the private ``/tel`` mint (:meth:`
        _mint_tier_from_caller_id`) when ``telephony_cfg.tel_mint_url`` is
        configured. On mint success, ``active_call.grant_tier_id`` becomes
        the caller's OWN entitled tier -- :meth:`_gate_unlock` then grants
        THAT tier, not the static ``unlock_tier_id``. On ANY mint failure
        (unmapped caller ID, no caller ID, /tel non-200/timeout/error, bad
        token), the persistent pipeline is still built (so a TTS-capable
        worker exists to speak the fail-closed goodbye) but the gate window
        is never started -- :meth:`_gate_fail_closed` fires immediately
        instead, so ``quota.start_gate`` is NEVER called and no metered
        STT/LLM/TTS session is ever billed (SC-4). When ``tel_mint_url`` is
        unconfigured (empty, the default), the mint step is skipped
        entirely and ``grant_tier_id`` is the legacy static
        ``unlock_tier_id`` -- byte-identical to Phase 11's behavior.
        """
        normalized_caller_id = _normalize_e164(caller_id)
        mint_configured = bool(self._telephony_cfg.tel_mint_url)
        mint_failed = False
        mint_sub: str | None = None
        if mint_configured:
            entitled_tier_id, mint_sub = await self._mint_tier_from_caller_id(normalized_caller_id)
            mint_failed = entitled_tier_id is None
            grant_tier_id = entitled_tier_id
        else:
            grant_tier_id = self._telephony_cfg.unlock_tier_id

        identity = replace(
            identity,
            tier_id=grant_tier_id,
            caller_id=normalized_caller_id or None,
            did=did or None,
            # Phase 15 (LEDG-01): the mint token's own sub (anon:<code>:<uuid>)
            # is the ONLY place this call's raw access code is recoverable --
            # thread it straight into CallIdentity.code so the ledger tap can
            # hash it (create_call_session prefers identity.code over trying
            # to parse one out of `identity.subject`, which for telephony is
            # `tel:<caller_id>` -- not a code-bearing shape). None when the
            # mint is unconfigured or failed (no code, no code_hash).
            code=mint_sub,
        )

        # A forward-reference container: GateProcessor's callbacks are
        # constructed before the ActiveCall (and its .gate back-reference)
        # exist -- both closures look this up lazily, by which time
        # `active_call_holder["call"]` has always been populated below.
        active_call_holder: dict[str, ActiveCall] = {}

        async def _on_fail_closed() -> None:
            active_call = active_call_holder.get("call")
            if active_call is not None:
                await self._gate_fail_closed(active_call, "gate window expired")

        async def _on_unlock() -> None:
            active_call = active_call_holder.get("call")
            if active_call is not None:
                await self._gate_unlock(active_call, caller_id=caller_id, sip_channel_id=sip_channel_id)

        passphrase_words = (
            self._passphrase_words
            if self._telephony_cfg.gate_mode in ("passphrase", "either")
            else frozenset()
        )
        gate = GateProcessor(
            call_id=sip_channel_id,
            passphrase_words=passphrase_words,
            gate_window_seconds=self._telephony_cfg.gate_window_seconds,
            on_unlock=_on_unlock,
            on_fail_closed=_on_fail_closed,
            # Opt-in fail-path debug logging (D-05e relaxation, default off):
            # the gate logs caller_id + heard tokens on fail-closed only when
            # telephony.gate_debug_log_heard is true.
            caller_id=caller_id,
            debug_log_heard=self._telephony_cfg.gate_debug_log_heard,
        )

        call_session = await create_call_session(
            transport=transport,
            identity=identity,
            gate_result=_bypass_gate_result(),
            cfg=self._cfg,
            knowledge_cfg=self._knowledge_cfg,
            duplex_cfg=DuplexConfig(),
            quota_cfg=self._quota_cfg,
            channel="pstn",
            metadata={"call_id": sip_channel_id, "did": did},
            gate_processor=gate,
        )

        active_call = ActiveCall(
            sip_channel_id=sip_channel_id,
            external_media_channel_id=external_media_channel_id,
            bridge_id=bridge_id,
            media_session=media,
            call_session=call_session,
            caller_id=caller_id,
            did=did,
            created_at=time.time(),
            gate=gate,
            grant_tier_id=grant_tier_id,
        )
        self.calls[sip_channel_id] = active_call
        active_call_holder["call"] = active_call
        self._register_pickup_cue(transport, call_session)

        # R6: a hard session timeout (only reachable once the gate has
        # unlocked and SessionLifecycle.upgrade_from_bypass has started the
        # real service timer) must ALSO reach the SIP channel.
        async def _on_released() -> None:
            await call_session.runner.cancel("session wind-down complete")
            await self._safe_ari(
                self._ari.hangup(sip_channel_id), "hangup sip channel (hard timeout)"
            )
            await self._close_active_call(active_call, "hard timeout release")

        call_session.lifecycle.on_released = _on_released

        task = asyncio.create_task(call_session.run())
        self._tasks[sip_channel_id] = task
        task.add_done_callback(lambda _t, cid=sip_channel_id: self._tasks.pop(cid, None))

        if mint_configured and mint_failed:
            # D-02/D-05/SC-4: an unmapped caller ID or any /tel mint failure
            # fails closed immediately -- the gate window never opens, no
            # DTMF/passphrase factor is ever accepted, and quota.start_gate
            # is never called (zero metered STT/LLM/TTS billing for a caller
            # who never proved entitlement).
            await self._gate_fail_closed(active_call, "caller-id mint failed")
            return

        # Starts the fail-closed timer NOW (idempotent -- GateProcessor
        # itself would also start it on the pipeline's first StartFrame;
        # this just guarantees the window starts even sooner).
        gate.start_timer()

    async def _gate_unlock(self, active_call: ActiveCall, *, caller_id: str, sip_channel_id: str) -> None:
        """D-05a/c: the REAL tier grant, on either unlock factor. On
        success, promotes the placeholder ``SessionLifecycle`` and greets
        (the greeting fires HERE, not on answer). On a quota rejection,
        routes through the same fail-closed teardown as a gate-window
        timeout (R6 quota-denied, extended to the post-unlock case).

        Phase 12 Plan 06 (D-02/D-05): grants ``active_call.grant_tier_id``
        -- the caller's OWN entitled tier when the §23 mint is configured
        and succeeded, or the legacy static ``telephony_cfg.unlock_tier_id``
        when the mint is unconfigured. ``grant_tier_id is None`` means a
        mint failure already triggered fail-closed in
        :meth:`_finish_stasis_start_gated` -- this is a defensive no-op
        guard against the narrow async race where a DTMF/passphrase factor
        still matches during that fail-closed goodbye's grace period; it
        must NEVER fall back to granting any tier in that case (never an
        open grant for an unmapped/failed-mint caller, D-05)."""
        if active_call.grant_tier_id is None:
            logger.warning(
                f"gate unlock ignored: no entitled tier for channel={sip_channel_id!r} "
                "(caller-id mint failed; fail-closed already in progress)"
            )
            return
        gate_identity = SessionIdentity(
            sub=f"tel:{caller_id or sip_channel_id}",
            tier_id=active_call.grant_tier_id,
            group=None,
            bypass_accounting=False,
        )
        try:
            # This call is already registered in self.calls (added right
            # after construction, above) -- exclude it from its own
            # at-capacity count.
            gate_result = quota.start_gate(
                gate_identity,
                active_session_count=max(0, len(self.calls) - 1),
                per_task_max_sessions=self._telephony_cfg.max_concurrent_calls,
                heartbeat_ttl_seconds=self._quota_cfg.heartbeat_ttl,
                sub_floor_seconds=self._quota_cfg.sub_floor_seconds,
            )
        except quota.QuotaError as exc:
            logger.warning(
                f"gate unlock: quota denied ({exc.error_type}) channel={sip_channel_id}"
            )
            await self._gate_fail_closed(active_call, "quota denied after gate unlock")
            return

        await active_call.call_session.lifecycle.upgrade_from_bypass(
            tier=gate_result.tier, session_id=gate_result.session_id, user_id=gate_identity.sub
        )
        # Phase 15 (LEDG-01/T-15-03-01): the ledger writer was constructed
        # DISABLED (create_call_session's `enabled=not gate_result.
        # bypass_accounting`, since this call started on the zeroed §24
        # bypass placeholder) -- flip it live at the SAME real-unlock
        # boundary as the lifecycle promotion above, and correct its
        # session_id/tier_id to the REAL granted values (the bypass
        # placeholder carried a random uuid session id and the no-access
        # placeholder tier). Nothing was ever captured while locked --
        # belt-and-suspenders alongside the GateProcessor's own D-05e
        # transcription-frame withholding.
        writer = active_call.call_session.writer
        if writer is not None:
            writer.session_id = gate_result.session_id
            writer.tier_id = gate_result.tier.tier_id
            writer.enabled = True
        await greet_now(active_call.call_session.worker, active_call.call_session.context)

    async def _gate_fail_closed(self, active_call: ActiveCall, reason: str) -> None:
        """D-05d fail-closed: a deterministic goodbye (bypasses the LLM,
        mirrors ``pipeline.speak_goodbye``'s existing wind-down usage), a
        grace period for it to play, then the single idempotent
        :meth:`_close_active_call` teardown -- never a silent open PSTN
        call, whether the trigger was a gate-window timeout or a quota
        rejection discovered right after unlock.

        No explicit ``hangup(sip_channel_id)`` call here: ``_close_active_
        call`` -> ``call_session.close()`` -> ``lifecycle.release()``
        already cascades into the composed ``on_released`` hook this
        module wires in :meth:`_finish_stasis_start_gated` (mirrors the R6
        hard-timeout path, T-11-05-02) -- that hook hangs up the SIP
        channel exactly once. Hanging up here too would double the call
        (harmless against real ARI, a 404 swallowed by ``_safe_ari`` --
        but the fake test client would double-count it)."""
        await speak_goodbye(active_call.call_session.worker, GATE_FAIL_CLOSED_COPY)
        await asyncio.sleep(self._quota_cfg.goodbye_grace_seconds)
        await self._close_active_call(active_call, reason)

    async def _gate_announcement(self, active_call: ActiveCall, entry: AnnouncementEntry) -> None:
        """CTF phone-OTP DTMF-code trigger (quick task 260716-1g0, Revision
        2 -- design doc docs/superpowers/specs/2026-07-15-ctf-phone-otp-
        announcement-did-design.md): fetch the current OTP from
        ``entry.otp_url``, speak it (digit-spaced, twice) over the SAME
        persistent pipeline/worker the §24 gate already built for this
        call, then tear the call down. MIRRORS :meth:`_gate_fail_closed`'s
        shape exactly -- no ``quota.start_gate``, no ``greet_now``, no
        concierge turn.

        First resolves the gate via ``active_call.gate.cancel_for_takeover``
        (NOT ``unlock``) so the §24 redaction boundary (D-05e) stays CLOSED
        the whole time and the fail-closed timer can never race this
        method's own goodbye -- single teardown, exactly one
        :meth:`_close_active_call` reached either way.

        On any OTP-fetch failure (non-200, network error, malformed body --
        ``_fetch_ctf_otp`` returns ``None`` uniformly) the call tears down
        immediately with NO spoken line. On success, the built line is
        queued via the same ``speak_goodbye`` seam ``_gate_fail_closed``
        uses, given a bounded grace period to finish playing (never a
        frame-level completion event -- a stuck synth can NEVER hang the
        PSTN line, T-OTP-05), then the single idempotent teardown runs.

        Logging discipline (§13/T-OTP-04/D-05e): never logs the DTMF
        trigger code, the OTP code, the otp_url, or the bearer -- only the
        channel id."""
        if active_call.gate is not None:
            active_call.gate.cancel_for_takeover("announcement")

        bearer = os.environ.get(entry.otp_env_var, "") if entry.otp_env_var else ""
        headers = {"Authorization": f"Bearer {bearer}"} if bearer else {}
        code = await _fetch_ctf_otp(entry.otp_url, headers)
        if not code:
            logger.warning(
                f"announcement: OTP fetch failed channel={active_call.sip_channel_id!r}"
            )
            await self._close_active_call(active_call, "announcement otp fetch failed")
            return

        line = _build_announcement_line(entry.line_template, code)
        await speak_goodbye(active_call.call_session.worker, line)
        # Base grace for the surrounding speech + the per-digit pause time
        # (the code is read TWICE), so the slowed readout is never cut off
        # mid-number by the teardown.
        grace = ANNOUNCEMENT_PLAYBACK_GRACE_SECONDS + 2 * len(code) * ANNOUNCEMENT_DIGIT_PAUSE_SECONDS
        await asyncio.sleep(grace)
        logger.info(f"announcement: played channel={active_call.sip_channel_id!r}")
        await self._close_active_call(active_call, "announcement complete")

    # --- ChannelDtmfReceived: the §24 gate's DTMF PIN path (Task 3) --------

    async def on_channel_dtmf_received(self, event: dict[str, Any]) -> None:
        """D-05b: accumulate one ARI-delivered digit against this call's
        buffer and compare to ``TELEPHONY_ACCESS_PIN`` (Landmine 5: ARI
        delivers one event per digit, never the whole PIN at once). On an
        exact match, unlocks the gate directly -- the PIN never touches the
        pipeline/frame stream/LLM at all. A no-op for an unknown channel,
        an ungated call (``active_call.gate is None``), an empty digit, or
        when ``gate_mode`` excludes the DTMF factor.

        Quick task 260716-1g0 (Revision 2): the real PIN keeps STRICT
        priority -- checked first, unchanged, and this method ``return``s
        immediately on a PIN match. ONLY when the PIN did NOT match does it
        test the raw trailing digit buffer (suffix semantics, a fat-fingered
        prefix before the code still matches) against every armed
        announcement code; a match dispatches to :meth:`_gate_announcement`
        instead of unlocking the concierge."""
        if self._telephony_cfg.gate_mode not in ("dtmf", "either"):
            return
        channel_id = _normalize_token((event.get("channel", {}) or {}).get("id"))
        digit = _normalize_token(event.get("digit"))
        active_call = self.calls.get(channel_id)
        if active_call is None or active_call.gate is None or not digit:
            return
        active_call.dtmf_raw = (active_call.dtmf_raw + digit)[-DTMF_RAW_MAX_DIGITS:]
        active_call.dtmf_buffer, matched = accumulate_dtmf(
            active_call.dtmf_buffer, digit, self._pin
        )
        if matched:
            await active_call.gate.unlock("dtmf")
            return
        for code, entry in self._announcements_by_code.items():
            if code and active_call.dtmf_raw.endswith(code):
                await self._gate_announcement(active_call, entry)
                return

    async def _teardown_gate_resources(
        self,
        bridge_id: str | None,
        external_media_channel_id: str | None,
        media_session: RtpMediaSession,
        sip_channel_id: str,
    ) -> None:
        """Tear down the gate-only bridge/external-media channel/socket
        allocated before a quota rejection (or an unexpected allocation
        failure) -- no ``ActiveCall`` was ever registered for these, so this
        is NOT routed through :meth:`_close_active_call` (R6). A played
        goodbye is deferred to Plan 06's real §24 gate (no TTS-capable
        pipeline exists yet at this point in the flow); this plan hangs up
        directly so no PSTN charge is ever left silently open (§17)."""
        if bridge_id is not None:
            await self._safe_ari(self._ari.destroy_bridge(bridge_id), "destroy_bridge (gate)")
        if external_media_channel_id is not None:
            await self._safe_ari(
                self._ari.hangup(external_media_channel_id), "hangup external_media (gate)"
            )
        await media_session.close()
        await self._safe_ari(self._ari.hangup(sip_channel_id), "hangup sip channel (gate)")

    # --- ChannelDestroyed + the single idempotent teardown (Task 2) --------

    async def on_channel_destroyed(self, event: dict[str, Any]) -> None:
        """Handle one ARI ``ChannelDestroyed`` event: look up the
        ``ActiveCall`` by the original SIP channel ID and route through the
        one idempotent teardown. An unknown channel id is logged and
        ignored -- never fatal (mirrors :class:`~klanker_voice.telephony.
        ari.AriClient`'s own "never crash the dispatch loop" posture)."""
        channel_id = _normalize_token((event.get("channel", {}) or {}).get("id"))
        active_call = self.calls.get(channel_id)
        if active_call is None:
            logger.warning(f"on_channel_destroyed: unknown channel={channel_id!r}")
            return
        await self._close_active_call(active_call, "ari channel destroyed")

    async def _close_active_call(self, active_call: ActiveCall, reason: str) -> None:
        """The single idempotent teardown every close trigger funnels
        through (D-02/R6, T-11-05-01): ``ChannelDestroyed``, a hard-timeout
        release, and any future caller. Mirrors ``SessionLifecycle._stopped``
        one layer up -- a synchronous check-and-set under ``active_call.lock``
        so racing callers (simultaneous hangup + timeout) still tear down
        exactly once."""
        async with active_call.lock:
            if active_call.closed:
                return
            active_call.closed = True

        logger.info(f"_close_active_call: channel={active_call.sip_channel_id} reason={reason!r}")

        await active_call.call_session.close(reason)
        await self._safe_ari(self._ari.destroy_bridge(active_call.bridge_id), "destroy_bridge")
        await self._safe_ari(
            self._ari.hangup(active_call.external_media_channel_id), "hangup external_media"
        )
        await active_call.media_session.close()
        self.calls.pop(active_call.sip_channel_id, None)
        self._tasks.pop(active_call.sip_channel_id, None)

    async def _safe_ari(self, coro: Any, description: str) -> None:
        """Await ``coro`` (an in-flight ARI REST call), swallowing
        :class:`~klanker_voice.telephony.ari.AriError` so one already-gone
        Asterisk-side resource (e.g. Asterisk itself already tore the bridge
        down when the last channel left it) never aborts the rest of a
        teardown sequence (no leaked resources, T-11-05-01)."""
        try:
            await coro
        except AriError as exc:
            logger.warning(f"{description} failed (status={exc.status}); continuing teardown")


# --- Standalone runnable path (D-08, Plan 07) -------------------------------
#
# D-08 phrases the entrypoint as `python -m klanker_voice.telephony.controller`
# literally; the actual load-config/construct-AriClient/register/connect/run
# sequence lives in `klanker_voice.telephony.__main__.main` (that module is
# ALSO independently runnable via `python -m klanker_voice.telephony`, see
# its own docstring for why both exist). Importing that module here happens
# lazily, inside the guard, so `import klanker_voice.telephony.controller`
# (e.g. from tests) never triggers it and there is no import-time circular
# dependency between the two modules.
if __name__ == "__main__":  # pragma: no cover - exercised via `python -m klanker_voice.telephony.controller`
    import asyncio

    from klanker_voice.telephony.__main__ import main as _main

    asyncio.run(_main())
