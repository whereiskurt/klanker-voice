"""Unit tests for klanker_voice.duplex (full-duplex concept, 2026-07-10).

Two layers: the pure `classify_user_speech` decision, and the
`DuplexController` frame behavior driven through pipecat's `run_test` harness
(the same idiom as tests/test_knowledge_router.py).
"""

from __future__ import annotations

import pytest

from pipecat.frames.frames import (
    BotStartedSpeakingFrame,
    BotStoppedSpeakingFrame,
    InterimTranscriptionFrame,
    InterruptionFrame,
    TranscriptionFrame,
    TTSSpeakFrame,
    UserStoppedSpeakingFrame,
)
from pipecat.tests.utils import run_test

from klanker_voice.config import (
    DEFAULT_BACKCHANNEL_WORDS,
    DEFAULT_EMITTER_PHRASES,
    DuplexConfig,
)
from klanker_voice.duplex import (
    BACKCHANNEL,
    INTERRUPTION,
    DuplexController,
    classify_user_speech,
)

_WORDS = set(DEFAULT_BACKCHANNEL_WORDS)


def _interim(text: str) -> InterimTranscriptionFrame:
    return InterimTranscriptionFrame(text=text, user_id="u1", timestamp="2026-07-10T00:00:00Z")


def _final(text: str) -> TranscriptionFrame:
    return TranscriptionFrame(text=text, user_id="u1", timestamp="2026-07-10T00:00:00Z")


class TestClassifier:
    @pytest.mark.parametrize("text", ["yeah", "mm-hm", "uh-huh", "okay", "right", "got it", "makes sense", "yeah okay"])
    def test_backchannels(self, text):
        assert classify_user_speech(text, backchannel_words=_WORDS, max_words=3) == BACKCHANNEL

    @pytest.mark.parametrize(
        "text",
        [
            "wait",                        # a real interjection, NOT a cue
            "no stop",
            "actually can you explain",    # too long + real content
            "what about defcon run",
            "",                            # nothing -> never suppress
            "   ",
            "hold on hold on hold on go",  # over max_words
        ],
    )
    def test_interruptions(self, text):
        assert classify_user_speech(text, backchannel_words=_WORDS, max_words=3) == INTERRUPTION

    def test_max_words_boundary(self):
        # All cue words but one over the limit -> interruption.
        assert (
            classify_user_speech("yeah yeah yeah yeah", backchannel_words=_WORDS, max_words=3)
            == INTERRUPTION
        )


def _duplex_cfg(**over) -> DuplexConfig:
    base = dict(enabled=True, backchannel_emitter=False, interruption_hold_ms=5000)
    base.update(over)
    return DuplexConfig(**base)


@pytest.mark.asyncio
class TestDuplexController:
    async def test_backchannel_during_bot_speech_is_suppressed(self):
        """A 'yeah' over the bot: the barge-in is dropped (bot keeps talking)
        and the cue's final transcript never reaches the aggregator."""
        ctrl = DuplexController(_duplex_cfg())
        down, _ = await run_test(
            ctrl,
            frames_to_send=[
                BotStartedSpeakingFrame(),
                InterruptionFrame(),
                _interim("yeah"),
                _final("yeah"),
                UserStoppedSpeakingFrame(),
            ],
        )
        assert not any(isinstance(f, InterruptionFrame) for f in down)  # barge-in suppressed
        assert not any(isinstance(f, TranscriptionFrame) for f in down)  # cue swallowed
        assert any(isinstance(f, InterimTranscriptionFrame) for f in down)  # interims still flow

    async def test_real_interruption_during_bot_speech_is_released(self):
        ctrl = DuplexController(_duplex_cfg())
        down, _ = await run_test(
            ctrl,
            frames_to_send=[
                BotStartedSpeakingFrame(),
                InterruptionFrame(),
                _interim("wait can you explain that again"),
                _final("wait can you explain that again"),
            ],
        )
        assert any(isinstance(f, InterruptionFrame) for f in down)  # genuine barge-in released
        assert any(isinstance(f, TranscriptionFrame) for f in down)  # and the turn flows through

    async def test_held_interruption_not_forwarded_before_a_decision(self):
        """With a long hold and no transcript, the barge-in stays held for the
        life of the test — proving it's withheld, not passed straight through."""
        ctrl = DuplexController(_duplex_cfg(interruption_hold_ms=5000))
        down, _ = await run_test(
            ctrl,
            frames_to_send=[BotStartedSpeakingFrame(), InterruptionFrame()],
        )
        assert not any(isinstance(f, InterruptionFrame) for f in down)

    async def test_interruption_when_bot_silent_passes_through(self):
        ctrl = DuplexController(_duplex_cfg())
        down, _ = await run_test(ctrl, frames_to_send=[InterruptionFrame()])
        assert any(isinstance(f, InterruptionFrame) for f in down)

    async def test_bot_stop_drops_a_pending_hold(self):
        """Bot finishes on its own: the still-held barge-in is moot and must
        not be emitted as a stray interruption after the turn ended."""
        ctrl = DuplexController(_duplex_cfg())
        down, _ = await run_test(
            ctrl,
            frames_to_send=[
                BotStartedSpeakingFrame(),
                InterruptionFrame(),
                BotStoppedSpeakingFrame(),
            ],
        )
        assert not any(isinstance(f, InterruptionFrame) for f in down)
        assert any(isinstance(f, BotStoppedSpeakingFrame) for f in down)

    async def test_disabled_controller_is_transparent_to_barge_in(self):
        ctrl = DuplexController(_duplex_cfg(enabled=False))
        down, _ = await run_test(
            ctrl,
            frames_to_send=[BotStartedSpeakingFrame(), InterruptionFrame(), _final("yeah")],
        )
        # Nothing suppressed: barge-in AND transcript both flow.
        assert any(isinstance(f, InterruptionFrame) for f in down)
        assert any(isinstance(f, TranscriptionFrame) for f in down)


@pytest.mark.asyncio
class TestBackchannelEmitter:
    async def test_emits_mid_long_turn(self):
        # 260710 redesign: the first partial starts the turn (t=100); a later
        # partial at t=109 (>8s continuous talk) fires one subtle "mhmm".
        clock = iter([100.0, 100.0, 109.0])
        ctrl = DuplexController(
            _duplex_cfg(backchannel_emitter=True), monotonic=lambda: next(clock)
        )
        down, _ = await run_test(
            ctrl, frames_to_send=[_interim("still going"), _interim("and going")]
        )
        spoken = [f for f in down if isinstance(f, TTSSpeakFrame)]
        assert len(spoken) == 1
        assert spoken[0].text == DEFAULT_EMITTER_PHRASES[0]

    async def test_no_emit_on_short_turn(self):
        # Only ~2s of talk then more partials -> below the 8s threshold, silent.
        clock = iter([100.0, 100.0, 102.0])
        ctrl = DuplexController(
            _duplex_cfg(backchannel_emitter=True), monotonic=lambda: next(clock)
        )
        down, _ = await run_test(
            ctrl, frames_to_send=[_interim("quick"), _interim("thought")]
        )
        assert not any(isinstance(f, TTSSpeakFrame) for f in down)

    async def test_rate_limited_within_long_turn(self):
        # Eligible at t=109 (emits); again at t=112 but < 6s gap -> only one.
        times = iter([100.0, 100.0, 109.0, 112.0])
        ctrl = DuplexController(
            _duplex_cfg(backchannel_emitter=True, emitter_min_gap_seconds=6.0),
            monotonic=lambda: next(times),
        )
        down, _ = await run_test(
            ctrl, frames_to_send=[_interim("a"), _interim("b"), _interim("c")]
        )
        assert len([f for f in down if isinstance(f, TTSSpeakFrame)]) == 1

    async def test_emitter_off_by_default(self):
        ctrl = DuplexController(_duplex_cfg(backchannel_emitter=False))
        down, _ = await run_test(ctrl, frames_to_send=[UserStoppedSpeakingFrame()])
        assert not any(isinstance(f, TTSSpeakFrame) for f in down)
