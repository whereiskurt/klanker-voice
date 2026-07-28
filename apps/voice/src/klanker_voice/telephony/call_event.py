"""``game_call_event`` -- the D-01/D-02/D-03 per-call telemetry line (quick
task 260727-v5e, per-call structured telemetry).

**Purpose.** Before the three phone-game DIDs go out to a large group, the
operator needs per-call analytics -- volume, code-entry attempts, outcomes --
without hand-diving CloudWatch. This module is the ONE payload builder/
emitter every telephony teardown path (``telephony.controller.
_close_active_call`` / ``_teardown_gate_resources``) calls exactly once
(D-01), producing a single structured, greppable log line:
``game_call_event {"call_id": ..., "dialed_did": ..., ...}``.

**D-05e redaction is structural, not a formatting convention.**
:func:`build_call_event` is PURE and keyword-only, and its parameter list IS
the redaction boundary: it accepts only ints, floats, bools, and
pre-approved label/identifier strings (``call_id``, ``dialed_did``,
``caller_id``, ``outcome``) -- there is no parameter through which a caller's
entered DTMF digits, a heard/matched word, or a code value could ever enter
the payload. ``words_heard`` is sourced from ``telephony.gate.GateProcessor.
token_count`` (an int -- see that property's own docstring for why the
accumulated token SET itself never crosses the processor boundary);
``digits_entered`` is a plain counter the controller increments per ARI
``ChannelDtmfReceived`` event, never the digit buffer itself. This mirrors
(and is deliberately cross-referenced against) ``telephony.gate``'s own
D-05e redaction-discipline docstring -- both modules independently enforce
the same invariant: transcripts/digits/codes are counted, never carried.

**Never-raise emission (T-v5e-03).** :func:`emit_call_event` wraps its whole
body in a broad ``except Exception`` and downgrades any failure (e.g. a
non-serializable value slipping through) to a ``logger.warning`` -- mirroring
the never-raise posture ``telephony.controller._send_sms_sequence`` /
``_safe_ari`` already establish in this codebase. A telemetry bug can never
abort a teardown and leave a PSTN call, bridge, or RTP socket open.
"""

from __future__ import annotations

import json

from loguru import logger

#: The stable marker prefix every emitted line starts with (D-02). MUST
#: match the Go constant ``callEventMarker`` in
#: ``kv/internal/app/cmd/telephony_stats.go`` -- guarded on the Go side by a
#: cross-language drift test that reads this exact assignment out of this
#: module's source, so renaming the marker on one side fails the other
#: side's build.
CALL_EVENT_MARKER = "game_call_event"

#: The eight outcome labels a ``game_call_event`` line can carry: the seven
#: D-04 labels plus the discretionary ``ungated_grant`` (the
#: ``require_gate=False`` test/dev-only escape hatch -- exists purely so
#: that path still satisfies D-01's "one line per call"; production never
#: emits it).
CALL_EVENT_OUTCOMES: frozenset[str] = frozenset(
    {
        "announcement_code",
        "announcement_words",
        "concierge_unlock_dtmf",
        "concierge_unlock_passphrase",
        "gate_timeout",
        "early_hangup",
        "error",
        "ungated_grant",
    }
)


def elapsed_seconds(started_at: float, ended_at: float) -> float | None:
    """The single clock helper both D-03 timing fields (``seconds_to_
    outcome``, ``duration_seconds``) use, so an unset timestamp can never
    produce an epoch-sized duration: returns ``None`` if either bound is
    ``<= 0`` (the "unset" sentinel every caller in this codebase already
    uses for an unstamped ``float`` field), else the non-negative,
    one-decimal-rounded span -- an end before the start clamps to ``0.0``
    rather than going negative."""
    if started_at <= 0 or ended_at <= 0:
        return None
    return round(max(0.0, ended_at - started_at), 1)


def build_call_event(
    *,
    call_id: str,
    dialed_did: str,
    caller_id: str,
    otp_only: bool,
    outcome: str,
    digits_entered: int,
    words_heard: int,
    seconds_to_outcome: float | None,
    duration_seconds: float,
) -> str:
    """PURE D-02/D-03 payload builder -- see module docstring for why this
    exact keyword-only signature IS the D-05 redaction boundary.

    Builds an ordered dict in D-03 field order and serializes with
    ``json.dumps(payload, separators=(",", ":"))`` -- deliberately no
    ``sort_keys``, so the emitted field order matches D-03 and stays
    human-readable. Both timing floats are rounded to one decimal;
    ``seconds_to_outcome=None`` serializes as JSON ``null``. Returns
    ``f"{CALL_EVENT_MARKER} {serialized}"`` -- the marker, a single space,
    then the compact JSON object."""
    payload = {
        "call_id": call_id,
        "dialed_did": dialed_did,
        "caller_id": caller_id,
        "otp_only": otp_only,
        "outcome": outcome,
        "digits_entered": digits_entered,
        "words_heard": words_heard,
        "seconds_to_outcome": (
            round(seconds_to_outcome, 1) if seconds_to_outcome is not None else None
        ),
        "duration_seconds": round(duration_seconds, 1),
    }
    serialized = json.dumps(payload, separators=(",", ":"))
    return f"{CALL_EVENT_MARKER} {serialized}"


def emit_call_event(
    *,
    call_id: str,
    dialed_did: str,
    caller_id: str,
    otp_only: bool,
    outcome: str,
    digits_entered: int,
    words_heard: int,
    seconds_to_outcome: float | None,
    duration_seconds: float,
) -> None:
    """Log one :func:`build_call_event` line via ``logger.info`` -- and MUST
    NEVER RAISE (T-v5e-03): the whole body is wrapped in a broad
    ``try``/``except Exception``, downgrading any failure to a
    ``logger.warning`` rather than propagating, so a telemetry bug can never
    abort the teardown sequence that calls this. The prebuilt string is
    passed as loguru's single positional message with NO format args --
    loguru only brace-formats a message when args are supplied, and this
    payload contains braces (the JSON object)."""
    try:
        line = build_call_event(
            call_id=call_id,
            dialed_did=dialed_did,
            caller_id=caller_id,
            otp_only=otp_only,
            outcome=outcome,
            digits_entered=digits_entered,
            words_heard=words_heard,
            seconds_to_outcome=seconds_to_outcome,
            duration_seconds=duration_seconds,
        )
        logger.info(line)
    except Exception:  # noqa: BLE001 -- telemetry must never break teardown (T-v5e-03)
        logger.warning("call event emit failed; continuing teardown")
