"""Unit tests for the §24 silent answer-gate (Phase 11 Plan 06, D-05).

Hermetic and offline: no real Asterisk/ARI, no real STT/LLM/TTS, no real
socket. :class:`~klanker_voice.telephony.gate.GateProcessor` is tested in
isolation (pure functions) and via ``pipecat.tests.utils.run_test`` (the
same harness ``test_knowledge_router.py`` uses) for its frame-processor
behavior -- proving the structural redaction boundary (D-05e/R5) by
asserting ZERO frames reach a downstream sink during the locked window.
"""

from __future__ import annotations

import asyncio
import json
import logging

import pytest
from loguru import logger

from pipecat.frames.frames import (
    InterimTranscriptionFrame,
    TranscriptionFrame,
    TTSSpeakFrame,
    UserStartedSpeakingFrame,
    UserStoppedSpeakingFrame,
)
from pipecat.processors.frame_processor import FrameDirection
from pipecat.tests.utils import run_test

from klanker_voice.telephony.call_event import (
    CALL_EVENT_MARKER,
    CALL_EVENT_OUTCOMES,
    build_call_event,
    elapsed_seconds,
    emit_call_event,
)
from klanker_voice.telephony.gate import GateProcessor, accumulate_dtmf, match_passphrase


# --- loguru -> stdlib logging / caplog bridge -----------------------------


@pytest.fixture
def loguru_caplog(caplog):
    """Bridge loguru (this codebase's logger) into stdlib ``logging`` so
    pytest's ``caplog`` can capture records -- loguru does not feed stdlib
    logging/``caplog`` by default."""

    class _PropagateHandler(logging.Handler):
        def emit(self, record: logging.LogRecord) -> None:
            logging.getLogger(record.name).handle(record)

    handler_id = logger.add(_PropagateHandler(), format="{message}", level="DEBUG")
    caplog.set_level(logging.DEBUG)
    yield caplog
    logger.remove(handler_id)


# --- pure functions --------------------------------------------------------


class TestMatchPassphrase:
    def test_all_four_words_present_in_any_order_matches(self):
        secret = {"purple", "falcon", "midnight", "compass"}
        accumulated = {"the", "midnight", "compass", "found", "a", "purple", "falcon"}
        assert match_passphrase(accumulated, secret) is True

    def test_three_of_four_does_not_match(self):
        secret = {"purple", "falcon", "midnight", "compass"}
        accumulated = {"purple", "falcon", "midnight"}
        assert match_passphrase(accumulated, secret) is False

    def test_empty_accumulated_does_not_match(self):
        assert match_passphrase(set(), {"purple", "falcon", "midnight", "compass"}) is False

    def test_empty_secret_words_never_matches(self):
        # gate_mode="dtmf" -- the passphrase factor is disabled entirely.
        assert match_passphrase({"purple", "falcon", "midnight", "compass"}, set()) is False


class TestAccumulateDtmf:
    def test_exact_pin_matches(self):
        buffer = ""
        for digit in "1234":
            buffer, matched = accumulate_dtmf(buffer, digit, "1234")
        assert matched is True
        assert buffer == "1234"

    def test_wrong_sequence_does_not_match(self):
        buffer = ""
        for digit in "9999":
            buffer, matched = accumulate_dtmf(buffer, digit, "1234")
        assert matched is False

    def test_partial_sequence_does_not_match(self):
        buffer = ""
        for digit in "123":
            buffer, matched = accumulate_dtmf(buffer, digit, "1234")
        assert matched is False

    def test_early_exit_after_extra_leading_digits(self):
        # Fat-fingered extra digits before the real PIN still match, because
        # only the trailing len(pin) digits are compared (early-exit).
        buffer = ""
        for digit in "991234":
            buffer, matched = accumulate_dtmf(buffer, digit, "1234")
        assert matched is True
        assert buffer == "1234"

    def test_unset_pin_never_matches(self):
        buffer, matched = accumulate_dtmf("", "1", "")
        assert matched is False
        assert buffer == ""

    def test_empty_digit_never_matches(self):
        buffer, matched = accumulate_dtmf("123", "", "1234")
        assert matched is False
        assert buffer == "123"


# --- GateProcessor: locked-window redaction boundary ------------------------


def _gate(**overrides) -> tuple[GateProcessor, list[str], list]:
    """Build a GateProcessor with recording on_unlock/on_fail_closed
    callbacks. Returns ``(gate, unlock_calls, fail_closed_calls)``."""
    unlock_calls: list[str] = []
    fail_closed_calls: list[str] = []

    async def _on_unlock() -> None:
        unlock_calls.append("unlocked")

    async def _on_fail_closed() -> None:
        fail_closed_calls.append("fail_closed")

    kwargs = dict(
        call_id="chan-1",
        passphrase_words={"purple", "falcon", "midnight", "compass"},
        gate_window_seconds=60.0,
        on_unlock=_on_unlock,
        on_fail_closed=_on_fail_closed,
    )
    kwargs.update(overrides)
    gate = GateProcessor(**kwargs)
    return gate, unlock_calls, fail_closed_calls


async def test_locked_window_swallows_all_gated_frame_types():
    """Redaction boundary (D-05e/R5): while locked, a downstream fake
    receives ZERO frames for TranscriptionFrame/InterimTranscriptionFrame/
    UserStartedSpeakingFrame/UserStoppedSpeakingFrame -- even a
    non-matching transcription never reaches the sink."""
    gate, unlock_calls, fail_closed_calls = _gate()

    frames = [
        UserStartedSpeakingFrame(),
        InterimTranscriptionFrame(text="hello", user_id="", timestamp=""),
        TranscriptionFrame(text="just chatting, nothing special here", user_id="", timestamp=""),
        UserStoppedSpeakingFrame(),
    ]

    down, _ = await run_test(gate, frames_to_send=frames, expected_down_frames=[])

    assert down == []
    assert unlock_calls == []
    assert fail_closed_calls == []


async def test_passphrase_split_across_two_frames_in_any_order_unlocks():
    """All 4 words, split across two TranscriptionFrames, in a scrambled
    order relative to the secret set, unlocks -- and frames sent AFTER
    unlock flow through untouched (pass-through proof)."""
    gate, unlock_calls, _ = _gate()

    frames = [
        TranscriptionFrame(text="I think the compass is purple", user_id="", timestamp=""),
        TranscriptionFrame(text="and the falcon flies at midnight", user_id="", timestamp=""),
        TTSSpeakFrame(text="post-unlock frame", append_to_context=False),
    ]

    down, _ = await run_test(gate, frames_to_send=frames)

    assert unlock_calls == ["unlocked"]
    assert gate.unlocked is True
    # Only the post-unlock frame reached the sink -- both pre-unlock
    # TranscriptionFrames were swallowed, never forwarded.
    assert len(down) == 1
    assert isinstance(down[0], TTSSpeakFrame)


async def test_post_unlock_swallows_the_unlocking_utterance_tail_until_new_turn():
    """After unlock the gate must swallow the TAIL of the utterance that was in
    flight at unlock — the passphrase keeps transcribing for a beat after the
    gate opens. If that tail passes through it becomes the first user turn:
    it leaks the passphrase into the LLM/ledger AND triggers a second self-intro
    on top of greet_now's greeting (the live 'double greeting' bug). Speech
    frames are suppressed until a genuinely NEW user turn (next
    UserStartedSpeakingFrame); a TTS/control frame in between still flows."""
    # Direct process_frame + a captured push_frame: run_test manages its own
    # speaking-state frames and won't forward an injected UserStartedSpeakingFrame
    # to the processor, so drive the gate directly to control the turn boundary.
    gate, unlock_calls, _ = _gate()
    pushed: list = []

    async def _capture(frame, direction=FrameDirection.DOWNSTREAM):
        pushed.append(frame)

    gate.push_frame = _capture  # type: ignore[method-assign]
    D = FrameDirection.DOWNSTREAM

    # the passphrase (all 4 words) unlocks — this frame is swallowed by the match:
    await gate.process_frame(
        TranscriptionFrame(text="purple falcon midnight compass", user_id="", timestamp=""), D
    )
    assert unlock_calls == ["unlocked"]
    # tail of the SAME utterance keeps transcribing AFTER unlock -> swallowed:
    await gate.process_frame(
        TranscriptionFrame(text="purple falcon midnight compass again", user_id="", timestamp=""), D
    )
    await gate.process_frame(UserStoppedSpeakingFrame(), D)
    # greet_now's greeting (a non-speech frame) flows through while suppressing:
    await gate.process_frame(TTSSpeakFrame(text="greeting", append_to_context=False), D)
    # a genuinely NEW user turn ends suppression; its transcription flows:
    await gate.process_frame(UserStartedSpeakingFrame(), D)
    await gate.process_frame(
        TranscriptionFrame(text="tell me about kurt", user_id="", timestamp=""), D
    )

    assert any(isinstance(f, TTSSpeakFrame) for f in pushed)  # greeting flowed
    # ONLY the new turn's transcription flowed — the post-unlock passphrase tail
    # was swallowed (no leak into the LLM/ledger, no re-greet trigger).
    fwd_texts = [f.text for f in pushed if isinstance(f, TranscriptionFrame)]
    assert fwd_texts == ["tell me about kurt"]
    assert all("compass" not in t for t in fwd_texts)


async def test_three_of_four_words_does_not_unlock():
    gate, unlock_calls, _ = _gate()

    frames = [
        TranscriptionFrame(
            text="purple falcon midnight but no fourth word here", user_id="", timestamp=""
        ),
    ]

    down, _ = await run_test(gate, frames_to_send=frames, expected_down_frames=[])

    assert down == []
    assert unlock_calls == []
    assert gate.unlocked is False


async def test_unrelated_speech_never_unlocks():
    gate, unlock_calls, _ = _gate()

    frames = [
        TranscriptionFrame(text="what is the weather like today", user_id="", timestamp=""),
    ]

    await run_test(gate, frames_to_send=frames, expected_down_frames=[])

    assert unlock_calls == []
    assert gate.unlocked is False


# --- GateProcessor: DTMF unlock path (controller-layer) --------------------


async def test_dtmf_unlock_via_direct_unlock_call():
    """D-05b: the controller compares digits to the PIN itself and calls
    ``unlock("dtmf")`` directly -- the PIN never touches the pipeline/frame
    stream at all."""
    gate, unlock_calls, _ = _gate()

    await gate.unlock("dtmf")

    assert unlock_calls == ["unlocked"]
    assert gate.unlocked is True


async def test_unlock_is_idempotent_across_both_factors():
    """D-05b 'either': whichever factor fires first wins; a second unlock
    call (either factor) is a no-op -- the callback fires exactly once."""
    gate, unlock_calls, _ = _gate()

    await gate.unlock("dtmf")
    await gate.unlock("passphrase")
    await gate.unlock("dtmf")

    assert unlock_calls == ["unlocked"]


# --- GateProcessor: fail-closed timer ---------------------------------------


async def test_fail_closed_fires_exactly_once_on_timer_expiry():
    gate, unlock_calls, fail_closed_calls = _gate(gate_window_seconds=0.05)

    gate.start_timer()
    await asyncio.sleep(0.15)

    assert fail_closed_calls == ["fail_closed"]
    assert unlock_calls == []

    # A second, redundant start_timer() call post-resolution is a no-op --
    # no double-fire, no lingering task.
    gate.start_timer()
    await asyncio.sleep(0.15)
    assert fail_closed_calls == ["fail_closed"]


async def test_unlock_before_expiry_cancels_the_timer_no_fail_closed():
    gate, unlock_calls, fail_closed_calls = _gate(gate_window_seconds=0.05)

    gate.start_timer()
    await gate.unlock("dtmf")
    await asyncio.sleep(0.15)

    assert unlock_calls == ["unlocked"]
    assert fail_closed_calls == []


async def test_start_timer_is_idempotent():
    gate, _, fail_closed_calls = _gate(gate_window_seconds=0.05)

    gate.start_timer()
    gate.start_timer()  # second call before resolution: no-op, not a second timer
    await asyncio.sleep(0.15)

    assert fail_closed_calls == ["fail_closed"]


# --- GateProcessor: deadline-based timer + cue defer (quick task 260805-fki) -
#
# The cue-relative fail-closed rebase: defer_for_cue moves the deadline
# forward by a bounded lead, never removes or delays start_timer's own
# unconditional fire. Small windows/leads (0.05-0.2s) keep this fast.


async def test_never_deferred_gate_still_fires_at_plain_gate_window_seconds():
    """No behavior change for a gate that is never deferred: fail-closed
    fires at approximately gate_window_seconds after start_timer(), not at
    gate_window_seconds + max_cue_lead_seconds."""
    gate, _, fail_closed_calls = _gate(gate_window_seconds=0.1, max_cue_lead_seconds=1.0)

    gate.start_timer()
    await asyncio.sleep(0.05)
    assert fail_closed_calls == []  # not yet -- too early proves no clamp-length firing
    await asyncio.sleep(0.15)
    assert fail_closed_calls == ["fail_closed"]


async def test_defer_for_cue_rebases_deadline_by_the_lead():
    """A gate deferred by a cue lead L fires at approximately
    L + gate_window_seconds after the defer, not gate_window_seconds after
    timer start."""
    gate, _, fail_closed_calls = _gate(gate_window_seconds=0.1, max_cue_lead_seconds=1.0)

    gate.start_timer()
    gate.defer_for_cue(0.1)
    await asyncio.sleep(0.15)  # would have fired by ~0.1s if the defer had no effect
    assert fail_closed_calls == []
    await asyncio.sleep(0.15)  # now past window(0.1) + lead(0.1) = 0.2s
    assert fail_closed_calls == ["fail_closed"]


async def test_defer_for_cue_is_one_shot_second_call_does_not_extend():
    """A second defer_for_cue call is a no-op -- the window is not extended
    again."""
    gate, _, fail_closed_calls = _gate(gate_window_seconds=0.1, max_cue_lead_seconds=2.0)

    gate.start_timer()
    gate.defer_for_cue(0.1)
    gate.defer_for_cue(1.0)  # ignored -- one-shot, not additive
    await asyncio.sleep(0.35)  # well past window+first-lead (0.2s), short of window+second-lead

    assert fail_closed_calls == ["fail_closed"]


async def test_defer_for_cue_lead_is_clamped_to_max_cue_lead_seconds():
    """An absurdly large cue lead is clamped -- the gate still fires within
    gate_window_seconds + max_cue_lead_seconds of timer start (the D-05d
    fail-closed bound)."""
    gate, _, fail_closed_calls = _gate(gate_window_seconds=0.05, max_cue_lead_seconds=0.1)

    gate.start_timer()
    gate.defer_for_cue(1000.0)  # absurd lead; must clamp to max_cue_lead_seconds
    await asyncio.sleep(0.1)  # short of window+clamped-lead (0.15s); would still be silent
    assert fail_closed_calls == []
    await asyncio.sleep(0.15)  # now past 0.05 + 0.1 = 0.15s
    assert fail_closed_calls == ["fail_closed"]


async def test_defer_for_cue_before_start_timer_yields_same_deadline_as_after():
    """defer_for_cue before start_timer, or after -- either call order --
    yields the same (approximate) deferred deadline."""
    gate_before, _, fail_closed_before = _gate(gate_window_seconds=0.1, max_cue_lead_seconds=1.0)
    gate_before.defer_for_cue(0.1)
    gate_before.start_timer()

    gate_after, _, fail_closed_after = _gate(gate_window_seconds=0.1, max_cue_lead_seconds=1.0)
    gate_after.start_timer()
    gate_after.defer_for_cue(0.1)

    await asyncio.sleep(0.15)  # short of window(0.1)+lead(0.1)=0.2s for both
    assert fail_closed_before == []
    assert fail_closed_after == []
    await asyncio.sleep(0.15)  # now past 0.2s for both
    assert fail_closed_before == ["fail_closed"]
    assert fail_closed_after == ["fail_closed"]


async def test_defer_for_cue_after_resolved_is_noop_never_resurrects_timer():
    """defer_for_cue after the gate has already resolved (unlock) is a
    no-op and never resurrects a cancelled timer."""
    gate, unlock_calls, fail_closed_calls = _gate(
        gate_window_seconds=0.05, max_cue_lead_seconds=1.0
    )

    gate.start_timer()
    await gate.unlock("dtmf")
    gate.defer_for_cue(0.5)  # no-op: already resolved
    await asyncio.sleep(0.15)

    assert unlock_calls == ["unlocked"]
    assert fail_closed_calls == []  # timer stays cancelled, never resurrected


async def test_negative_or_zero_cue_lead_never_shortens_the_window():
    gate, _, fail_closed_calls = _gate(gate_window_seconds=0.1, max_cue_lead_seconds=1.0)

    gate.start_timer()
    gate.defer_for_cue(-5.0)
    await asyncio.sleep(0.05)
    assert fail_closed_calls == []
    await asyncio.sleep(0.1)
    assert fail_closed_calls == ["fail_closed"]


async def test_unlock_still_cancels_a_deferred_timer():
    gate, unlock_calls, fail_closed_calls = _gate(
        gate_window_seconds=0.05, max_cue_lead_seconds=1.0
    )

    gate.start_timer()
    gate.defer_for_cue(0.5)
    await gate.unlock("dtmf")
    await asyncio.sleep(0.2)

    assert unlock_calls == ["unlocked"]
    assert fail_closed_calls == []


async def test_cancel_for_takeover_still_cancels_a_deferred_timer():
    gate, unlock_calls, fail_closed_calls = _gate(
        gate_window_seconds=0.05, max_cue_lead_seconds=1.0
    )

    gate.start_timer()
    gate.defer_for_cue(0.5)
    gate.cancel_for_takeover("announcement")
    await asyncio.sleep(0.2)

    assert fail_closed_calls == []
    assert unlock_calls == []


# --- GateProcessor: cancel_for_takeover (quick task 260716-1g0, Revision 2) -


async def test_cancel_for_takeover_resolves_without_unlocking():
    """cancel_for_takeover flips _resolved True but leaves _unlocked False
    (the §24 redaction boundary stays CLOSED) -- neither on_unlock nor
    on_fail_closed ever fires."""
    gate, unlock_calls, fail_closed_calls = _gate(gate_window_seconds=0.05)

    gate.cancel_for_takeover("announcement")

    assert gate._resolved is True
    assert gate.unlocked is False
    assert unlock_calls == []
    assert fail_closed_calls == []


async def test_cancel_for_takeover_cancels_the_fail_closed_timer():
    """A subsequent gate-window expiry does NOT fire on_fail_closed once
    cancel_for_takeover has already resolved the gate -- the timer task is
    cancelled, so no racing second goodbye."""
    gate, unlock_calls, fail_closed_calls = _gate(gate_window_seconds=0.05)

    gate.start_timer()
    gate.cancel_for_takeover("announcement")
    await asyncio.sleep(0.15)

    assert fail_closed_calls == []
    assert unlock_calls == []
    assert gate.unlocked is False


async def test_cancel_for_takeover_is_idempotent():
    """A second call (or a call after the gate already resolved via unlock)
    is a no-op."""
    gate, unlock_calls, _ = _gate()

    await gate.unlock("dtmf")
    gate.cancel_for_takeover("announcement")  # no-op: already resolved via unlock
    gate.cancel_for_takeover("announcement")  # no-op: idempotent

    assert unlock_calls == ["unlocked"]
    assert gate.unlocked is True  # unchanged by the later cancel_for_takeover calls


async def test_cancel_for_takeover_keeps_redaction_boundary_closed():
    """Post-takeover, the gate still swallows transcription/speaking-state
    frames -- process_frame's locked-window behavior is unaffected (only
    ``unlock`` flips it to pass-through)."""
    gate, _, _ = _gate()
    gate.cancel_for_takeover("announcement")

    down, _ = await run_test(
        gate,
        frames_to_send=[
            TranscriptionFrame(text="anything at all", user_id="", timestamp=""),
        ],
        expected_down_frames=[],
    )
    assert down == []


async def test_cancel_for_takeover_never_logs_reason_beyond_call_id(loguru_caplog):
    """D-05e: only reason + call_id are logged -- never a transcript, PIN, or
    DTMF code."""
    gate, _, _ = _gate(call_id="chan-77")
    gate.cancel_for_takeover("announcement")

    text = loguru_caplog.text
    assert "chan-77" in text
    assert "announcement" in text


# --- D-05e: never-logged guarantees ------------------------------------------


async def test_unlock_and_fail_closed_never_log_secrets_or_transcript(loguru_caplog):
    """No secret word, PIN, raw utterance, or partial-match count ever
    appears in a log record -- for BOTH the passphrase-unlock path and the
    fail-closed path."""
    secret_words = {"purple", "falcon", "midnight", "compass"}
    utterance = "the midnight compass found a purple falcon"
    gate, _, _ = _gate(passphrase_words=secret_words, gate_window_seconds=60.0)

    await run_test(
        gate,
        frames_to_send=[TranscriptionFrame(text=utterance, user_id="", timestamp="")],
    )

    fail_gate, _, _ = _gate(gate_window_seconds=0.05)
    fail_gate.start_timer()
    await asyncio.sleep(0.15)

    log_text = loguru_caplog.text.lower()
    for word in secret_words:
        assert word not in log_text
    assert utterance.lower() not in log_text
    assert "1234" not in log_text  # a stand-in PIN never logged
    # No per-word partial-match oracle ("3 of 4", "3/4", etc.).
    assert " of 4" not in log_text
    assert "3/4" not in log_text
    # The only thing that IS expected: the structured unlocked{...}/
    # fail-closed marker with method + call_id.
    assert "unlocked{method" in loguru_caplog.text
    assert "gate fail-closed call_id" in loguru_caplog.text


# --- gate_debug_log_heard: opt-in fail-path heard-words logging (260714) -----
#
# Deliberate, operator-accepted relaxation of D-05e for the FAIL path only: with
# the opt-in flag on, a failed (window-expiry) attempt logs the caller's number +
# the tokens STT heard, so an accent/STT mismatch can be debugged. Never on
# success, never the configured secret/PIN, off by default.


async def test_fail_closed_debug_log_off_by_default_emits_no_heard_line(loguru_caplog):
    """Default posture (flag off) is byte-identical to D-05e: even after the
    caller has spoken non-matching words, the fail-closed path emits only the
    plain ``gate fail-closed call_id`` marker -- never a ``gate_fail_heard`` line."""
    gate, _, fail_closed_calls = _gate(gate_window_seconds=0.05)

    await gate.process_frame(
        TranscriptionFrame(text="the weather is nice today", user_id="", timestamp=""),
        FrameDirection.DOWNSTREAM,
    )
    gate.start_timer()
    await asyncio.sleep(0.15)

    assert fail_closed_calls == ["fail_closed"]
    assert "gate_fail_heard" not in loguru_caplog.text
    assert "gate fail-closed call_id" in loguru_caplog.text


async def test_fail_closed_debug_log_on_emits_heard_tokens_and_caller(loguru_caplog):
    """With ``debug_log_heard=True``, a failed attempt emits one
    ``gate_fail_heard`` line carrying the caller_id, call_id, the heard tokens,
    and the token count -- exactly what the operator needs to see WHY an
    accent-mismatched utterance missed the passphrase."""
    gate, _, fail_closed_calls = _gate(
        gate_window_seconds=0.05,
        call_id="chan-99",
        caller_id="+15551234567",
        debug_log_heard=True,
    )

    await gate.process_frame(
        TranscriptionFrame(text="the weather is nice today", user_id="", timestamp=""),
        FrameDirection.DOWNSTREAM,
    )
    gate.start_timer()
    await asyncio.sleep(0.15)

    assert fail_closed_calls == ["fail_closed"]
    text = loguru_caplog.text
    assert "gate_fail_heard" in text
    assert "+15551234567" in text
    assert "chan-99" in text
    # every heard token is present, and the count is reported
    for token in ("the", "weather", "is", "nice", "today"):
        assert token in text
    assert "token_count: 5" in text
    assert "window_expired: true" in text


async def test_debug_log_on_never_emits_on_success_path(loguru_caplog):
    """Even with the flag on, the SUCCESS (unlock) path emits NO
    ``gate_fail_heard`` line -- unlock cancels the timer, and heard-words
    logging is strictly fail-path only (a success ~ the secret)."""
    gate, unlock_calls, fail_closed_calls = _gate(
        gate_window_seconds=0.05, debug_log_heard=True
    )

    frames = [
        TranscriptionFrame(
            text="purple falcon midnight compass", user_id="", timestamp=""
        ),
    ]
    await run_test(gate, frames_to_send=frames)
    await asyncio.sleep(0.15)

    assert unlock_calls == ["unlocked"]
    assert fail_closed_calls == []
    assert "gate_fail_heard" not in loguru_caplog.text


async def test_debug_log_on_never_reconstructs_unspoken_secret_words(loguru_caplog):
    """With the flag on, only what the caller ACTUALLY said is logged. A secret
    word the caller never spoke does not appear -- the operator cannot
    reconstruct the passphrase from a failed attempt's log."""
    gate, _, _ = _gate(
        gate_window_seconds=0.05,
        passphrase_words={"purple", "falcon", "midnight", "compass"},
        caller_id="+15550001111",
        debug_log_heard=True,
    )

    # Caller says only ONE of the four secret words, plus filler.
    await gate.process_frame(
        TranscriptionFrame(text="was it purple something", user_id="", timestamp=""),
        FrameDirection.DOWNSTREAM,
    )
    gate.start_timer()
    await asyncio.sleep(0.15)

    text = loguru_caplog.text.lower()
    assert "gate_fail_heard" in text
    assert "purple" in text  # the caller genuinely said this -> logged
    # the three secret words the caller never uttered are absent
    for unspoken in ("falcon", "midnight", "compass"):
        assert unspoken not in text


# --- Quick task 260717-o2q: concierge_unlock_enabled (per-DID gate policy Part A) ---


async def test_concierge_disabled_passphrase_never_unlocks():
    """concierge_unlock_enabled=False: all 4 passphrase words arriving in a
    TranscriptionFrame never unlocks -- on_unlock never fires, the gate stays
    locked."""
    gate, unlock_calls, _ = _gate(concierge_unlock_enabled=False)

    frames = [
        TranscriptionFrame(text="purple falcon midnight compass", user_id="", timestamp=""),
    ]

    down, _ = await run_test(gate, frames_to_send=frames, expected_down_frames=[])

    assert down == []
    assert unlock_calls == []
    assert gate.unlocked is False


async def test_concierge_disabled_explicit_unlock_passphrase_and_dtmf_are_noops():
    """concierge_unlock_enabled=False: an explicit await gate.unlock("passphrase")
    and await gate.unlock("dtmf") are both no-ops -- unlocked stays False."""
    gate, unlock_calls, _ = _gate(concierge_unlock_enabled=False)

    await gate.unlock("passphrase")
    await gate.unlock("dtmf")

    assert unlock_calls == []
    assert gate.unlocked is False
    assert gate._resolved is False


async def test_concierge_disabled_cancel_for_takeover_still_resolves():
    """concierge_unlock_enabled=False: cancel_for_takeover("announcement")
    STILL resolves the gate -- the 333266 takeover path is untouched by the
    concierge-suppression flag."""
    gate, unlock_calls, fail_closed_calls = _gate(
        concierge_unlock_enabled=False, gate_window_seconds=0.05
    )

    gate.cancel_for_takeover("announcement")

    assert gate._resolved is True
    assert gate.unlocked is False
    assert unlock_calls == []

    # The fail-closed timer can never race the takeover afterward either.
    gate.start_timer()
    await asyncio.sleep(0.15)
    assert fail_closed_calls == []


async def test_concierge_enabled_default_true_byte_identical():
    """The default (concierge_unlock_enabled=True, unspecified) is
    byte-identical to every pre-260717-o2q behavior: passphrase match
    unlocks, and an explicit unlock("dtmf") unlocks too."""
    gate, unlock_calls, _ = _gate()  # default concierge_unlock_enabled=True

    frames = [
        TranscriptionFrame(text="purple falcon midnight compass", user_id="", timestamp=""),
    ]
    await run_test(gate, frames_to_send=frames)
    assert unlock_calls == ["unlocked"]
    assert gate.unlocked is True

    gate2, unlock_calls2, _ = _gate()
    await gate2.unlock("dtmf")
    assert unlock_calls2 == ["unlocked"]
    assert gate2.unlocked is True


# --- GateProcessor: announcement spoken-trigger factor (quick task 260727-pdh) --


def _announcement_gate(**overrides):
    """Build a GateProcessor with an armed announcement-words registry (key
    "uctf" -> {"hack", "the", "planet"}) and a recording on_announcement_words
    callback. Returns (gate, unlock_calls, fail_closed_calls,
    announcement_calls). Mirrors _gate()'s shape/defaults exactly."""
    announcement_calls: list[str] = []

    async def _on_announcement_words(key: str) -> None:
        announcement_calls.append(key)

    overrides.setdefault("announcement_words", {"uctf": ["hack", "the", "planet"]})
    overrides.setdefault("on_announcement_words", _on_announcement_words)
    gate, unlock_calls, fail_closed_calls = _gate(**overrides)
    return gate, unlock_calls, fail_closed_calls, announcement_calls


async def test_announcement_words_accumulate_and_match_when_concierge_disabled():
    """D-06: with concierge_unlock_enabled=False (the OTP-only DID case),
    tokens still accumulate and match the armed announcement registry --
    across TWO separate TranscriptionFrames, proving accumulation now
    happens where it used to be skipped entirely."""
    gate, unlock_calls, _, announcement_calls = _announcement_gate(
        concierge_unlock_enabled=False
    )
    D = FrameDirection.DOWNSTREAM

    await gate.process_frame(TranscriptionFrame(text="hack the", user_id="", timestamp=""), D)
    await gate.process_frame(TranscriptionFrame(text="planet", user_id="", timestamp=""), D)
    await asyncio.sleep(0)  # let the spawned callback task run

    assert announcement_calls == ["uctf"]
    assert gate.unlocked is False
    assert unlock_calls == []


async def test_announcement_words_match_cancels_fail_closed_timer():
    """A successful announcement match resolves the gate via
    cancel_for_takeover BEFORE the callback is spawned, so the fail-closed
    timer can never fire afterward (T-pdh-05)."""
    gate, unlock_calls, fail_closed_calls, announcement_calls = _announcement_gate(
        gate_window_seconds=0.05
    )
    gate.start_timer()

    await gate.process_frame(
        TranscriptionFrame(text="hack the planet", user_id="", timestamp=""),
        FrameDirection.DOWNSTREAM,
    )
    await asyncio.sleep(0.15)

    assert announcement_calls == ["uctf"]
    assert fail_closed_calls == []
    assert unlock_calls == []


async def test_announcement_words_callback_fires_at_most_once():
    """A second matching utterance after the first match produces NO second
    callback invocation -- the gate is already resolved."""
    gate, _, _, announcement_calls = _announcement_gate()
    D = FrameDirection.DOWNSTREAM

    await gate.process_frame(
        TranscriptionFrame(text="hack the planet", user_id="", timestamp=""), D
    )
    await asyncio.sleep(0)
    await gate.process_frame(
        TranscriptionFrame(text="hack the planet again", user_id="", timestamp=""), D
    )
    await asyncio.sleep(0)

    assert announcement_calls == ["uctf"]


async def test_announcement_words_redaction_zero_frames_and_no_secret_in_logs(loguru_caplog):
    """Redaction (D-07/T-pdh-01): a downstream sink receives ZERO frames
    during/after an announcement match, and captured logs contain neither
    the heard words, the matched words, nor the registry key."""
    gate, _, _, announcement_calls = _announcement_gate()

    frames = [
        TranscriptionFrame(text="hack the planet", user_id="", timestamp=""),
    ]
    down, _ = await run_test(gate, frames_to_send=frames, expected_down_frames=[])
    await asyncio.sleep(0)  # let the spawned callback task run

    assert down == []
    assert announcement_calls == ["uctf"]
    log_text = loguru_caplog.text.lower()
    assert "hack" not in log_text
    assert "planet" not in log_text
    assert "uctf" not in log_text


async def test_announcement_words_no_registry_is_byte_identical_default():
    """A GateProcessor with no announcement_words/on_announcement_words kwargs
    (the default) behaves exactly as before this task -- no announcement
    branch ever runs, concierge matching is unaffected."""
    gate, unlock_calls, _ = _gate()  # no announcement kwargs at all

    frames = [
        TranscriptionFrame(text="purple falcon midnight compass", user_id="", timestamp=""),
    ]
    await run_test(gate, frames_to_send=frames)

    assert unlock_calls == ["unlocked"]
    assert gate.unlocked is True


# --- Quick task 260727-v5e: per-call structured telemetry -------------------
#
# call_event.py's pure builder/clock-helper (tests 1-4) and the two D-05e-safe
# GateProcessor read-only views (tests 5-6, D-04 telemetry).


class TestBuildCallEvent:
    def test_single_line_marker_prefix_and_exact_field_set_in_order(self):
        """Test 1: build_call_event emits a single line whose prefix is the
        marker plus one space and whose remainder parses as JSON with
        exactly the nine D-03 keys, in D-03 order."""
        line = build_call_event(
            call_id="chan-1",
            dialed_did="7254043234",
            caller_id="+15550001234",
            otp_only=False,
            outcome="concierge_unlock_dtmf",
            digits_entered=4,
            words_heard=0,
            seconds_to_outcome=12.3,
            duration_seconds=45.6,
        )
        prefix, _, remainder = line.partition(" ")
        assert prefix == CALL_EVENT_MARKER
        payload = json.loads(remainder)
        assert list(payload.keys()) == [
            "call_id",
            "dialed_did",
            "caller_id",
            "otp_only",
            "outcome",
            "digits_entered",
            "words_heard",
            "seconds_to_outcome",
            "duration_seconds",
        ]

    def test_null_seconds_to_outcome_and_one_decimal_rounding(self):
        """Test 2: seconds_to_outcome=None renders as JSON null, and both
        timing floats are rounded to one decimal."""
        line = build_call_event(
            call_id="chan-2",
            dialed_did="",
            caller_id="",
            otp_only=True,
            outcome="early_hangup",
            digits_entered=0,
            words_heard=0,
            seconds_to_outcome=None,
            duration_seconds=3.14159,
        )
        payload = json.loads(line.partition(" ")[2])
        assert payload["seconds_to_outcome"] is None
        assert payload["duration_seconds"] == 3.1

        line2 = build_call_event(
            call_id="chan-3",
            dialed_did="",
            caller_id="",
            otp_only=False,
            outcome="gate_timeout",
            digits_entered=0,
            words_heard=0,
            seconds_to_outcome=7.777,
            duration_seconds=7.777,
        )
        payload2 = json.loads(line2.partition(" ")[2])
        assert payload2["seconds_to_outcome"] == 7.8
        assert payload2["duration_seconds"] == 7.8


class TestElapsedSeconds:
    def test_boundaries(self):
        """Test 3: unset start returns None, unset end returns None, an end
        before the start clamps to 0.0, and a normal span rounds to one
        decimal."""
        assert elapsed_seconds(0.0, 10.0) is None
        assert elapsed_seconds(10.0, 0.0) is None
        assert elapsed_seconds(10.0, 5.0) == 0.0
        assert elapsed_seconds(10.0, 12.345) == 2.3


class TestEmitCallEventNeverRaises:
    def test_never_raises_on_unserializable_value(self, loguru_caplog):
        """Test 4: emit_call_event never raises -- an unserializable value
        (an object json.dumps refuses) returns normally and logs a warning
        rather than propagating."""

        class _Unserializable:
            pass

        # dialed_did is typed str, but the redaction-boundary signature is
        # keyword-only Python, not runtime-enforced -- deliberately smuggle
        # a value json.dumps refuses to prove the never-raise contract.
        emit_call_event(
            call_id="chan-4",
            dialed_did=_Unserializable(),  # type: ignore[arg-type]
            caller_id="",
            otp_only=False,
            outcome="error",
            digits_entered=0,
            words_heard=0,
            seconds_to_outcome=None,
            duration_seconds=0.0,
        )

        assert "call event emit failed" in loguru_caplog.text
        assert CALL_EVENT_MARKER not in loguru_caplog.text


class TestGateProcessorUnlockMethod:
    def test_none_initially_then_dtmf_then_passphrase_each_on_a_fresh_gate(self):
        """Test 5: unlock_method is None initially, becomes "dtmf" /
        "passphrase" after a real unlock, and STAYS None when
        concierge_unlock_enabled=False suppresses the factor."""
        gate, _, _ = _gate()
        assert gate.unlock_method is None

    async def test_becomes_dtmf_after_dtmf_unlock(self):
        gate, unlock_calls, _ = _gate()
        await gate.unlock("dtmf")
        assert unlock_calls == ["unlocked"]
        assert gate.unlock_method == "dtmf"

    async def test_becomes_passphrase_after_passphrase_unlock(self):
        gate, unlock_calls, _ = _gate()
        frames = [
            TranscriptionFrame(text="purple falcon midnight compass", user_id="", timestamp=""),
        ]
        await run_test(gate, frames_to_send=frames)
        assert unlock_calls == ["unlocked"]
        assert gate.unlock_method == "passphrase"

    async def test_stays_none_when_concierge_unlock_disabled(self):
        gate, unlock_calls, _ = _gate(concierge_unlock_enabled=False)
        await gate.unlock("dtmf")
        await gate.unlock("passphrase")
        assert unlock_calls == []
        assert gate.unlock_method is None


class TestGateProcessorTokenCount:
    async def test_counts_distinct_accumulated_tokens_and_is_an_int(self):
        """Test 6: token_count counts distinct accumulated tokens after
        feeding TranscriptionFrames, and the property is an int (never the
        set)."""
        gate, _, _ = _gate(passphrase_words=set())  # never unlocks, keeps accumulating
        assert gate.token_count == 0
        assert isinstance(gate.token_count, int)

        await gate.process_frame(
            TranscriptionFrame(text="hack the planet", user_id="", timestamp=""),
            FrameDirection.DOWNSTREAM,
        )
        assert gate.token_count == 3
        assert isinstance(gate.token_count, int)

        # A repeated token does not inflate the count -- it's a SET.
        await gate.process_frame(
            TranscriptionFrame(text="hack it again", user_id="", timestamp=""),
            FrameDirection.DOWNSTREAM,
        )
        assert gate.token_count == 5  # {hack, the, planet, it, again}
