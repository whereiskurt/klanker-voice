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
