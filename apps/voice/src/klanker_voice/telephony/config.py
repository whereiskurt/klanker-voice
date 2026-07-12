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

from dataclasses import dataclass
from pathlib import Path

from klanker_voice.config import ConfigError, _load_toml_data, _resolve_config_path

#: The only allowed values for the §24 answer-gate mode (D-05b).
ALLOWED_GATE_MODES = frozenset({"dtmf", "passphrase", "either"})


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
        unlock_tier_id: The tier granted on a successful gate unlock (open
            question #2, additive to D-09's own key list) -- the minimal
            identity-seam grant (D-05a), not the §11/§23 caller-ID/access-code
            resolver (deferred to Phase 12).
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
    )
