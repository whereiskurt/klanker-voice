"""DuplexController: backchannel-aware barge-in + bot listening cues.

The full-duplex concept (2026-07-10, ``voice2`` variant). The shipped cascade
is half-duplex-with-barge-in: the instant the visitor makes any sound while the
concierge is talking, the pipeline fires an interruption and the concierge
yields the floor. That's wrong for the natural "mm-hm", "yeah", "right" a
listener drops in to say *keep going* — those cut the concierge off mid-word.

This processor sits in the same slot as :class:`~klanker_voice.knowledge.
router.KnowledgeRouterProcessor` (between STT and the user aggregator) and adds
two behaviors, both config-gated (:class:`~klanker_voice.config.DuplexConfig`):

1. **Backchannel-vs-interruption on barge-in (hold-and-release).** The worker
   queues an ``InterruptionFrame`` *downstream* through the whole pipeline the
   moment the visitor starts speaking over the bot (see
   ``pipeline/worker.py``). Because that frame reaches this processor *before*
   it reaches the aggregator/LLM/TTS, we can withhold it: we HOLD it, wait up
   to ``interruption_hold_ms`` for the first transcript, then decide. A
   backchannel -> DROP the interruption (bot keeps talking) and swallow the
   backchannel's final transcript so it never becomes a user turn. Anything
   else -> RELEASE the interruption (a real barge-in, unchanged behavior). If
   no transcript arrives in the window, we release (fail safe: a real
   interruption must never be swallowed).

2. **Bot backchannel emitter (optional).** When ``backchannel_emitter`` is on,
   the concierge drops its own short "mm-hm" (straight to TTS via
   ``TTSSpeakFrame``, never into the LLM context — the ``speak_goodbye``
   pattern) when the visitor pauses, rate-limited by ``emitter_min_gap_seconds``
   and never while the bot is already speaking.

Withholding the barge-in trades a little latency on *genuine* interruptions
(up to ``interruption_hold_ms``, or first-partial time, whichever is shorter)
for not being cut off by a "yeah". That knob, the lexicon, and the emitter
cadence are live-tuning surfaces — the 07-08 spec flagged this whole class of
change as needing on-device audible verification.
"""

from __future__ import annotations

import asyncio
import re
import time
from typing import Callable

from pipecat.frames.frames import (
    BotStartedSpeakingFrame,
    BotStoppedSpeakingFrame,
    Frame,
    InterimTranscriptionFrame,
    InterruptionFrame,
    TranscriptionFrame,
    TTSSpeakFrame,
    UserStoppedSpeakingFrame,
    VADUserStoppedSpeakingFrame,
)
from pipecat.processors.frame_processor import FrameDirection, FrameProcessor

from klanker_voice.config import DuplexConfig

#: Words carved out of a transcript for classification. Keeps ``-`` so
#: "uh-huh" / "mm-hm" stay single tokens; drops trailing punctuation so
#: "yeah." matches "yeah".
_WORD_RE = re.compile(r"[a-z0-9]+(?:-[a-z0-9]+)*")

BACKCHANNEL = "backchannel"
INTERRUPTION = "interruption"


def classify_user_speech(
    text: str, *, backchannel_words: set[str], max_words: int
) -> str:
    """Classify a user utterance as :data:`BACKCHANNEL` or :data:`INTERRUPTION`.

    Backchannel iff the utterance is non-empty, at most ``max_words`` words,
    and *every* word is in ``backchannel_words``. Everything else — longer
    utterances, anything containing a non-cue word ("wait", "no", a real
    question) — is an interruption. Empty/whitespace -> interruption (never
    suppress on nothing).
    """
    words = _WORD_RE.findall(text.lower())
    if not words or len(words) > max_words:
        return INTERRUPTION
    if all(w in backchannel_words for w in words):
        return BACKCHANNEL
    return INTERRUPTION


class DuplexController(FrameProcessor):
    """Backchannel-aware barge-in gate + optional bot listening cues.

    Stateless across sessions; one instance per pipeline. ``monotonic`` is
    injectable so the emitter's rate-limit is deterministic under test.
    """

    def __init__(
        self,
        cfg: DuplexConfig,
        *,
        monotonic: Callable[[], float] = time.monotonic,
    ) -> None:
        super().__init__()
        self._cfg = cfg
        self._monotonic = monotonic
        self._words = {w.lower() for w in cfg.backchannel_words}

        self._bot_speaking = False
        # Held barge-in awaiting a backchannel-vs-real decision.
        self._held: InterruptionFrame | None = None
        self._held_dir: FrameDirection = FrameDirection.DOWNSTREAM
        self._held_task: asyncio.Task | None = None
        self._decided = False
        # True between a backchannel decision and the user turn ending: the
        # backchannel's final transcript must not reach the LLM aggregator.
        self._suppressing = False
        # Emitter round-robin + rate-limit.
        self._emit_index = 0
        self._last_emit: float | None = None

    async def process_frame(self, frame: Frame, direction: FrameDirection) -> None:
        await super().process_frame(frame, direction)

        if isinstance(frame, BotStartedSpeakingFrame):
            self._bot_speaking = True
            await self.push_frame(frame, direction)
            return

        if isinstance(frame, BotStoppedSpeakingFrame):
            self._bot_speaking = False
            # Bot finished on its own: a still-pending held barge-in is moot
            # (nothing left to interrupt) — drop it, don't emit a stray one.
            await self._drop_held()
            self._suppressing = False
            await self.push_frame(frame, direction)
            return

        if isinstance(frame, InterruptionFrame):
            if (
                self._cfg.enabled
                and self._bot_speaking
                and self._held is None
                and self._cfg.interruption_hold_ms > 0
            ):
                await self._hold_interruption(frame, direction)
                return  # withheld until decided/released — do NOT push yet
            await self.push_frame(frame, direction)
            return

        if isinstance(frame, (InterimTranscriptionFrame, TranscriptionFrame)):
            await self._maybe_decide(frame.text)
            if isinstance(frame, TranscriptionFrame) and self._suppressing:
                # A backchannel's finalized transcript: swallow it (no user
                # turn) and end the suppression window — the cue is over.
                self._suppressing = False
                return
            await self.push_frame(frame, direction)
            return

        if isinstance(frame, (UserStoppedSpeakingFrame, VADUserStoppedSpeakingFrame)):
            await self.push_frame(frame, direction)
            await self._maybe_emit()
            self._suppressing = False
            return

        await self.push_frame(frame, direction)

    # -- held-interruption machinery ---------------------------------------

    async def _hold_interruption(
        self, frame: InterruptionFrame, direction: FrameDirection
    ) -> None:
        self._held = frame
        self._held_dir = direction
        self._decided = False
        self._held_task = self.create_task(self._hold_timeout())

    async def _hold_timeout(self) -> None:
        """Fail-safe: no transcript decided us in time -> it's a real
        interruption, release it. Never let a barge-in be swallowed by
        silence."""
        try:
            await asyncio.sleep(self._cfg.interruption_hold_ms / 1000.0)
        except asyncio.CancelledError:
            return
        if self._held is not None and not self._decided:
            self._decided = True
            await self._release_held()

    async def _maybe_decide(self, text: str) -> None:
        if self._held is None or self._decided or not text.strip():
            return
        self._decided = True
        kind = classify_user_speech(
            text, backchannel_words=self._words, max_words=self._cfg.max_backchannel_words
        )
        if kind == BACKCHANNEL:
            await self._drop_held()      # keep talking over the "mm-hm"
            self._suppressing = True      # and don't turn it into a user message
        else:
            await self._release_held()    # genuine barge-in — yield the floor now

    async def _release_held(self) -> None:
        frame, direction = self._held, self._held_dir
        await self._cancel_hold_task()
        self._held = None
        if frame is not None:
            await self.push_frame(frame, direction)

    async def _drop_held(self) -> None:
        await self._cancel_hold_task()
        self._held = None

    async def _cancel_hold_task(self) -> None:
        task = self._held_task
        self._held_task = None
        if task is not None and task is not asyncio.current_task():
            await self.cancel_task(task)

    # -- bot backchannel emitter -------------------------------------------

    async def _maybe_emit(self) -> None:
        if not (self._cfg.enabled and self._cfg.backchannel_emitter):
            return
        if self._bot_speaking:
            return  # never talk over our own turn
        now = self._monotonic()
        if (
            self._last_emit is not None
            and (now - self._last_emit) < self._cfg.emitter_min_gap_seconds
        ):
            return
        phrases = self._cfg.emitter_phrases
        if not phrases:
            return
        phrase = phrases[self._emit_index % len(phrases)]
        self._emit_index += 1
        self._last_emit = now
        # Straight to TTS, never into the LLM context (speak_goodbye pattern).
        await self.push_frame(
            TTSSpeakFrame(text=phrase, append_to_context=False), FrameDirection.DOWNSTREAM
        )
