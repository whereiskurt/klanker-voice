"""``[telephony]`` config loader (Phase 11, D-09 -- deferred from Phase 10).

Every downstream telephony plan (ARI controller, §24 answer-gate, standalone
entrypoint) reads a validated, frozen :class:`TelephonyConfig`. This module is
transport/media/gate *behavior* only -- it NEVER carries a provider
credential, an ARI password, a PIN, or a passphrase (spec §22.3). Those
secrets are sourced exclusively from env/SSM: ``ASTERISK_ARI_URL``,
``ASTERISK_ARI_USERNAME``, ``ASTERISK_ARI_PASSWORD``,
``TELEPHONY_ACCESS_PIN``, ``TELEPHONY_PASSPHRASE_WORDS``.

:func:`load_telephony_config` reuses :func:`klanker_voice.config._resolve_config_path`
and :func:`klanker_voice.config._load_toml_data` -- the same file, the same
shared ``_reject_credential_fields`` gate (D-09) that :func:`klanker_voice.config.load_config`
and :func:`klanker_voice.config.load_quota_config` already run through, so a
credential-looking field anywhere in ``pipeline.toml`` (not just inside
``[telephony]``) is refused before any table-specific parsing happens.
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from pathlib import Path

from klanker_voice.config import ConfigError, _load_toml_data, _resolve_config_path

#: The only allowed values for the §24 answer-gate mode (D-05b).
ALLOWED_GATE_MODES = frozenset({"dtmf", "passphrase", "either"})


@dataclass(frozen=True)
class AnnouncementEntry:
    """One ``[[telephony.announcement]]`` table (quick task 260716-1g0,
    Revision 2 of the CTF phone-OTP announcement -- design doc
    docs/superpowers/specs/2026-07-15-ctf-phone-otp-announcement-did-design.md,
    "Revision 2 (2026-07-16) -- DTMF-code trigger, not DID" section).

    Revision 1 (DID-keyed, dispatched BEFORE the §24 gate) never fired on a
    live call: VoIP.ms routes every DID to one sub-account, so the dialed
    number is never visible at ``on_stasis_start`` (``did`` there is always
    ``557010_klanker-pbx``). Revision 2 instead triggers the announcement by
    a DTMF access code entered INSIDE the existing §24 answer-gate
    (``AsteriskCallController._gate_announcement``, DID-agnostic) -- no
    mint, no quota, no concierge, but the gate's redaction/fail-closed
    machinery stays engaged the whole time.

    Attributes:
        code_env_var: The NAME of the environment variable holding the DTMF
            trigger code that arms this announcement (a short numeric
            keypad sequence, value in SSM -- never in TOML, operator-
            rotatable like
            ``TELEPHONY_ACCESS_PIN``). REQUIRED at load time -- an entry
            without one is meaningless (nothing could ever trigger it).
            Splits to ``code``/``env``/``var``, none of which are credential
            tokens, so it passes this module's shared
            ``_reject_credential_fields`` gate cleanly (mirrors the working
            ``otp_env_var``/``tel_mint_env_var`` precedent).
        did: OPTIONAL/informational only (no longer a matcher -- Revision 2's
            root-cause fix: the dialed DID is not visible at the edge, so it
            can never gate dispatch). Defaults to ``""``.
        otp_url: The auth app's internal-only ``/ctf/otp`` issuer URL. A
            NON-secret plain URL, like ``tel_mint_url``.
        otp_env_var: The NAME of the environment variable holding the
            optional shared bearer token for the ``/ctf/otp`` call -- the
            token VALUE is read from env/SSM by the controller at call time,
            never stored here (mirrors ``tel_mint_env_var``). Defaults to
            ``""`` (unconfigured -- no Authorization header is sent).

            CRITICAL naming deviation from the design doc's proposed
            ``otp_auth_env_var``: this module's shared
            ``_reject_credential_fields`` gate refuses ANY TOML key
            containing ``_auth_`` (``_CREDENTIAL_FIELD_RE`` in
            ``klanker_voice.config``), so ``otp_auth_env_var`` would be
            rejected before parsing even though it only ever holds an env-var
            NAME, never a secret value. ``otp_env_var`` is clean of every
            credential token and exactly mirrors the working
            ``tel_mint_env_var`` precedent.
        line_template: Spoken text with a ``{code}`` placeholder, substituted
            (digit-spaced) for every occurrence -- the template speaks the
            code twice for clarity. Validated at load time to contain at
            least one ``{code}`` occurrence.
        sms_dids: OPTIONAL ordered pool of SMS-capable VoIP.ms sending DIDs
            (quick task 260716-hg5 -- design doc docs/superpowers/specs/
            2026-07-16-ctf-otp-sms-during-call-design.md). Empty tuple (the
            default) ⇒ the SMS-during-call punchline is OFF and this entry
            behaves byte-for-byte as it did before. When non-empty, the
            controller texts the caller a written copy of the OTP, trying each
            DID IN ORDER until one send succeeds (runtime auto-fallback for a
            DID that is not SMS-enabled). A DID is a PUBLIC phone number, not a
            credential -- so the digits live safely in TOML; the ``sms_dids``
            key contains no credential token and passes
            ``_reject_credential_fields``. The VoIP.ms API username/password
            are NEVER here -- the controller reads them at call time from the
            environment (``VOIPMS_API_USERNAME``/``VOIPMS_API_PASSWORD``, task-
            def secrets -> SSM), mirroring how the OTP bearer is read from
            ``otp_env_var``. Each entry is normalized to digits only; empties
            are dropped.
    """

    otp_url: str
    otp_env_var: str = ""
    line_template: str = ""
    code_env_var: str = ""
    did: str = ""
    sms_dids: tuple[str, ...] = ()
    #: Per-DID reply enrollment (quick task 260716-hg5 follow-up -- design doc
    #: docs/superpowers/specs/2026-07-16-ctf-per-did-sms-reply-design.md). The
    #: set of DIALED DIDs that are allowed to reply-via-SMS FROM THEMSELVES: a
    #: call to an enrolled DID is texted from THAT SAME number, not from the
    #: shared ``sms_dids`` pool. Empty (the default) ⇒ pure legacy pool
    #: behavior, byte-identical to before. When non-empty, the controller
    #: reads the actual dialed DID from the SIP ``To:`` header (surfaced by
    #: the dialplan into ``KLANKER_SIP_TO``) and:
    #:   * dialed DID resolved AND enrolled here → text FROM the dialed DID;
    #:   * dialed DID resolved but NOT enrolled → NO text (this is how a DID
    #:     like 613 is RESERVED/unburned even though the announcement itself
    #:     is DID-agnostic);
    #:   * dialed DID unresolved (``To:`` parse miss) → fall back to the
    #:     legacy ``sms_dids`` pool so the feature is never stranded while the
    #:     header mechanism is being verified.
    #: Each entry is normalized to digits only (same rule as ``sms_dids``); a
    #: DID is a public phone number, never a credential.
    sms_reply_dids: tuple[str, ...] = ()
    #: The auth app's internal ``/ctf/sms`` relay URL (quick task 260716-hg5
    #: follow-up). telephony-edge POSTs the built SMS here instead of calling
    #: VoIP.ms directly -- the auth app egresses from the STABLE, VoIP.ms-
    #: whitelisted NAT EIP, whereas this task's Fargate egress IP is ephemeral
    #: and cannot be whitelisted. A NON-secret plain URL, like ``otp_url``.
    #: Empty ⇒ SMS is not sent even if ``sms_dids`` is set (the relay is the
    #: only send path). The bearer reuses ``otp_env_var``.
    sms_relay_url: str = ""


@dataclass(frozen=True)
class TelephonyConfig:
    """The ``[telephony]`` table (Phase 11, D-09): media + §24 gate knobs only.

    Behavior-only surface -- no provider credential, ARI password, PIN, or
    passphrase field ever lives here (§22.3). A config file with no
    ``[telephony]`` table at all parses to these defaults (``enabled=False``),
    so the WebRTC-only path is byte-unaffected until telephony is opted in.

    Attributes:
        enabled: Master switch. False -> no telephony entrypoint/controller
            behavior is expected to run against this config.
        provider: Upstream SIP trunk provider (spec §14). Only ``"voipms"`` is
            in scope for this milestone; kept as a string, not an enum, so a
            future provider doesn't require a schema change.
        edge: Local call-control edge. ``"asterisk-ari"`` is the only
            supported value this phase (spec §7/§13).
        codec: RTP codec (spec §9). ``"pcmu"`` (μ-law) is Phase 10/11's only
            supported codec.
        sample_rate: RTP clock rate in Hz -- 8000 for PCMU.
        packet_ms: RTP packetization interval in milliseconds.
        max_concurrent_calls: Soft cap on simultaneous ARI calls this
            controller process will accept.
        answer_timeout_seconds: How long the controller waits for the
            External Media channel + bridge to become ready before treating
            the call as failed.
        hangup_on_pipeline_error: If True, an unhandled pipeline error tears
            the call down (hangup) rather than leaving a silent open line.
        require_gate: Master switch for the §24 silent answer-gate. True in
            every real deployment; a False value is a test/dev-only escape
            hatch, never expected in TOML shipped to production.
        gate_mode: Which §24 unlock factor(s) are accepted -- ``"dtmf"``
            (PIN only), ``"passphrase"`` (spoken 4-word phrase only), or
            ``"either"`` (default, D-05b: both factors, either unlocks).
        gate_window_seconds: How long the caller has to unlock before the
            fail-closed goodbye + hangup (D-05d).
        unlock_tier_id: The FALLBACK tier granted on a successful gate unlock
            when the §23 caller-ID mint is unconfigured (``tel_mint_url``
            empty) -- the Phase-11 minimal identity-seam grant (D-05a).
            When ``tel_mint_url`` IS configured, the tier actually granted at
            unlock is the caller's OWN entitled tier, resolved by the Phase
            12 caller-ID mint (D-02/D-05) -- this field is then only the
            legacy/dev fallback, never consulted for a caller whose mint
            call has run.
        tel_mint_url: Base URL of the private, internal-only §23 caller-ID
            mint endpoint (D-02, e.g. ``"https://auth.klankermaker.ai/use1/
            tel"`` -- the controller composes ``f"{tel_mint_url}/{e164}"``).
            A NON-secret plain URL, never a credential. Empty (the default)
            means the caller-ID mint integration is not configured for this
            deployment -- every gated call then falls back to the legacy
            Phase-11 ``unlock_tier_id`` grant unchanged (Phase 12 is
            additive/opt-in at the config layer).
        tel_mint_env_var: The NAME of the environment variable holding the
            shared bearer token for the ``/tel`` mint call (D-04) -- the
            token VALUE itself is read from env/SSM by the controller at
            call time, never stored here or anywhere in TOML (mirrors how
            ``ASTERISK_ARI_PASSWORD``/``TELEPHONY_ACCESS_PIN`` are handled --
            see module docstring). Defaults to
            ``"TELEPHONY_ENDPOINT_AUTH_TOKEN"``, the same env var name the
            auth-app's ``/tel`` route reads (12-02-SUMMARY.md).
    """

    enabled: bool = False
    provider: str = "voipms"
    edge: str = "asterisk-ari"
    codec: str = "pcmu"
    sample_rate: int = 8000
    packet_ms: int = 20
    max_concurrent_calls: int = 1
    answer_timeout_seconds: int = 15
    hangup_on_pipeline_error: bool = True
    # --- §24 silent answer-gate (D-05) ---
    require_gate: bool = True
    gate_mode: str = "either"
    gate_window_seconds: int = 10
    unlock_tier_id: str = "kph-tier"
    #: Opt-in (default False): on a fail-closed (gate-window expiry, no unlock),
    #: log the caller_id + the heard STT tokens for accent/STT debugging. A
    #: deliberate, operator-accepted relaxation of D-05e for the FAIL path only
    #: -- never logs the passphrase/PIN, never runs on the success path. Off =
    #: byte-identical D-05e posture.
    gate_debug_log_heard: bool = False
    # --- §23 caller-ID mint (Phase 12, D-02/D-04/D-05) ---
    tel_mint_url: str = ""
    tel_mint_env_var: str = "TELEPHONY_ENDPOINT_AUTH_TOKEN"
    # --- CTF phone-OTP announcement DID(s) (quick task 260715-oq0) ---
    announcements: tuple[AnnouncementEntry, ...] = ()
    # --- Per-DID sub-account -> dialed-DID map (quick task 260716-hg5 follow-up,
    # design doc docs/superpowers/specs/2026-07-16-ctf-per-did-sms-reply-design.md).
    # Maps a per-DID VoIP.ms sub-account SIP username (the ARI dialplan.exten on
    # a call delivered over that sub-account's registered leg, e.g.
    # "557010_vegas3234") to the bare-digits dialed DID ("7254043234"). This is
    # how per-DID SMS reply resolves the ACTUAL dialed number once each DID has
    # its OWN sub-account: the To: header carries only the sub-account name, so
    # the controller maps exten -> DID here (To:-header parse stays as a
    # fallback). Empty (the default) -> no map, byte-identical to before (every
    # call resolves via the To: header only). Keys are matched verbatim against
    # dialplan.exten; values are normalized to digits-only. Sub-account names and
    # DIDs are public (never a credential), so they live safely in TOML.
    subaccount_did_map: dict[str, str] = field(default_factory=dict)


def load_telephony_config(path: Path | str | None = None) -> TelephonyConfig:
    """Parse and validate the ``[telephony]`` table into a ``TelephonyConfig``.

    Same file/path resolution as :func:`klanker_voice.config.load_config`
    (explicit ``path`` arg -> ``KLANKER_PIPELINE_CONFIG`` env var ->
    ``apps/voice/pipeline.toml``). Unlike ``[quota]``/``[knowledge]``,
    ``[telephony]`` is OPTIONAL -- a config file without the table returns
    ``TelephonyConfig()`` (``enabled=False``), so the shipped WebRTC-only
    ``pipeline.toml``/every pre-Phase-11 fixture parses unchanged.

    Never reads a secret here (ARI password, PIN, passphrase words) -- those
    come from env in the controller/entrypoint, never this loader.
    """
    resolved_path = _resolve_config_path(path)
    data = _load_toml_data(resolved_path)  # reuses the shared credential gate (D-09)

    table = data.get("telephony")
    if table is None:
        return TelephonyConfig()
    if not isinstance(table, dict):
        raise ConfigError("pipeline.toml [telephony] must be a table")

    gate_mode = str(table.get("gate_mode", "either"))
    if gate_mode not in ALLOWED_GATE_MODES:
        raise ConfigError(
            f"telephony.gate_mode {gate_mode!r} must be one of {sorted(ALLOWED_GATE_MODES)}"
        )

    announcements = _parse_announcements(table.get("announcement"))
    subaccount_did_map = _parse_subaccount_dids(table.get("subaccount_dids"))

    return TelephonyConfig(
        enabled=bool(table.get("enabled", False)),
        provider=str(table.get("provider", "voipms")),
        edge=str(table.get("edge", "asterisk-ari")),
        codec=str(table.get("codec", "pcmu")),
        sample_rate=int(table.get("sample_rate", 8000)),
        packet_ms=int(table.get("packet_ms", 20)),
        max_concurrent_calls=int(table.get("max_concurrent_calls", 1)),
        answer_timeout_seconds=int(table.get("answer_timeout_seconds", 15)),
        hangup_on_pipeline_error=bool(table.get("hangup_on_pipeline_error", True)),
        require_gate=bool(table.get("require_gate", True)),
        gate_mode=gate_mode,
        gate_window_seconds=int(table.get("gate_window_seconds", 10)),
        unlock_tier_id=str(table.get("unlock_tier_id", "kph-tier")),
        gate_debug_log_heard=bool(table.get("gate_debug_log_heard", False)),
        tel_mint_url=str(table.get("tel_mint_url", "")),
        tel_mint_env_var=str(table.get("tel_mint_env_var", "TELEPHONY_ENDPOINT_AUTH_TOKEN")),
        announcements=announcements,
        subaccount_did_map=subaccount_did_map,
    )


def _parse_announcements(raw: object) -> tuple[AnnouncementEntry, ...]:
    """Parse ``[[telephony.announcement]]`` (a TOML array-of-tables -> a list
    of dicts) into a frozen tuple of :class:`AnnouncementEntry`. Absent
    (``None``) -> an empty tuple, byte-identical to every pre-260715-oq0
    config. Reuses this module's existing ``ConfigError`` style; the shared
    credential gate (``_load_toml_data`` -> ``_reject_credential_fields``)
    has already run over the WHOLE file before this function is ever called,
    so a credential-looking key anywhere inside an announcement table is
    refused before parsing reaches here."""
    if raw is None:
        return ()
    if not isinstance(raw, list):
        raise ConfigError("telephony.announcement must be an array of tables ([[telephony.announcement]])")

    entries: list[AnnouncementEntry] = []
    for i, item in enumerate(raw):
        if not isinstance(item, dict):
            raise ConfigError(f"telephony.announcement[{i}] must be a table")

        # Revision 2 (260716-1g0): `did` is no longer a matcher -- it's
        # optional/informational only (the dialed DID is never visible at
        # the edge). No validation, no error on absence.
        did = str(item.get("did", "")).strip()

        otp_url = item.get("otp_url")
        if not otp_url or not isinstance(otp_url, str):
            raise ConfigError(f"telephony.announcement[{i}].otp_url must be a non-empty string")

        line_template = item.get("line_template")
        if not line_template or not isinstance(line_template, str):
            raise ConfigError(
                f"telephony.announcement[{i}].line_template must be a non-empty string"
            )
        if "{code}" not in line_template:
            raise ConfigError(
                f"telephony.announcement[{i}].line_template must contain a {{code}} placeholder"
            )

        otp_env_var = str(item.get("otp_env_var", ""))

        code_env_var = str(item.get("code_env_var", "")).strip()
        if not code_env_var:
            raise ConfigError(
                f"telephony.announcement[{i}].code_env_var must be a non-empty string"
            )

        sms_dids = _parse_sms_dids(item.get("sms_dids"), i)
        sms_reply_dids = _parse_sms_dids(item.get("sms_reply_dids"), i, field="sms_reply_dids")
        sms_relay_url = str(item.get("sms_relay_url", "")).strip()

        entries.append(
            AnnouncementEntry(
                did=did,
                otp_url=otp_url,
                otp_env_var=otp_env_var,
                line_template=line_template,
                code_env_var=code_env_var,
                sms_dids=sms_dids,
                sms_reply_dids=sms_reply_dids,
                sms_relay_url=sms_relay_url,
            )
        )

    return tuple(entries)


def _parse_sms_dids(raw: object, i: int, field: str = "sms_dids") -> tuple[str, ...]:
    """Normalize a ``[[telephony.announcement]]`` DID-array value (quick task
    260716-hg5) into an ordered tuple of digits-only DIDs. Absent /
    ``None`` ⇒ ``()``. A non-list is a hard config error. Each element is
    coerced to a string and stripped of every non-digit character (so
    ``"613-480-5878"``, ``"+16134805878"``, and ``"6134805878"`` all normalize
    identically); empty results are dropped. ORDER is preserved -- for
    ``sms_dids`` it is the runtime auto-fallback order. No credential ever
    appears here: a DID is a public phone number, and the VoIP.ms API creds are
    read from the environment by the controller, never from TOML. ``field``
    names the parsed key for the error message (reused for both ``sms_dids``
    and ``sms_reply_dids``)."""
    if raw is None:
        return ()
    if not isinstance(raw, (list, tuple)):
        raise ConfigError(
            f"telephony.announcement[{i}].{field} must be an array of DID strings"
        )
    dids: list[str] = []
    for entry in raw:
        digits = re.sub(r"\D", "", str(entry))
        if digits:
            dids.append(digits)
    return tuple(dids)


def _parse_subaccount_dids(raw: object) -> dict[str, str]:
    """Parse the ``[telephony.subaccount_dids]`` table (quick task 260716-hg5
    follow-up) into a ``{sub-account-username: bare-digit-DID}`` dict. Absent /
    ``None`` ⇒ ``{}`` (no per-DID sub-account map -- every call resolves the
    dialed DID via the SIP ``To:`` header only, byte-identical to before).

    A non-table is a hard config error. Each KEY (a VoIP.ms sub-account SIP
    username, matched verbatim against ``dialplan.exten``) is stripped of
    surrounding whitespace; each VALUE is normalized to digits only (so
    ``"725-404-3234"``/``"+17254043234"``/``"7254043234"`` all resolve to the
    same DID). A key or value that normalizes to empty is dropped. The shared
    credential-field gate (run over the whole file before this) has already
    refused any credential-looking key -- sub-account names and DIDs are public,
    never secrets."""
    if raw is None:
        return {}
    if not isinstance(raw, dict):
        raise ConfigError(
            "telephony.subaccount_dids must be a table ([telephony.subaccount_dids])"
        )
    out: dict[str, str] = {}
    for key, value in raw.items():
        subaccount = str(key).strip()
        did = re.sub(r"\D", "", str(value))
        if subaccount and did:
            out[subaccount] = did
    return out
