"""The §24 silent answer-gate (Phase 11 Plan 06, D-05).

**Task 1 architecture spike — confirmed design (Open Questions 1 & 5).**

Verified directly against this repo's installed pipecat 1.5.0 source and this
phase's own ``telephony/transport.py``: ``TelephonyInputTransport``/
``TelephonyOutputTransport`` route ``stop(EndFrame)``, ``cancel(CancelFrame)``,
and ``cleanup()`` all through one idempotent ``_teardown()`` that calls
``self._media.close()`` — closing the live RTP UDP socket. Ending a "gate-only"
``PipelineWorker`` to hand off to a second "full" ``PipelineWorker`` over the
SAME ``TelephonyTransport``/``RtpMediaSession`` would therefore tear down the
call's live media the moment the gate pipeline ends. This rules out
Open Question 1's "two sequential ``build_pipeline()`` calls, same transport"
alternative entirely — it is not merely suboptimal, it is broken by the
already-shipped, verbatim-reused Phase 10 transport contract.

**Confirmed design: one persistent ``Pipeline``/``PipelineWorker``/
``CallSession`` for the whole call, with this module's :class:`GateProcessor`
inline** — occupying the exact same architectural slot pattern as
``knowledge.router.KnowledgeRouterProcessor`` (inserted between ``stt`` and
the duplex/router stage in ``pipeline.build_pipeline``, see
``klanker_voice.pipeline`` module docstring). While gated
(``self._unlocked is False``), :meth:`GateProcessor.process_frame` never
calls ``push_frame`` for ``TranscriptionFrame``/``InterimTranscriptionFrame``/
``UserStartedSpeakingFrame``/``UserStoppedSpeakingFrame`` — this IS the
redaction boundary D-05e requires (the pre-unlock transcript never reaches
the duplex controller/router/user-aggregator/LLM/transcript-ledger/logs,
because it is never forwarded past this processor at all, not "dropped
later"). Every other frame (``StartFrame``, audio, control/system frames)
flows through untouched in both states, so the pipeline's own machinery
(metrics, barge-in, teardown) is unaffected by the lock.

``build_llm``/``build_tts`` (the Anthropic/ElevenLabs SDK client objects) ARE
constructed at pipeline-build time, before the gate passes — confirmed
against ``factories.py``: constructing an SDK client object is a cheap,
no-network-call operation, not a billed API call. The *actual* expense — a
conversational turn — genuinely never happens until :func:`~klanker_voice.
pipeline.greet_now` fires at unlock, because nothing upstream of
``GateProcessor`` ever reaches ``llm``/``tts`` while locked. This is the
documented reading of D-05d ("the LLM/TTS never *engage*") this phase adopts,
per 11-RESEARCH.md R5's own explicit flag of this exact tradeoff.

**Open Question 5 (fail-closed timer sequencing) — confirmed.** The
:class:`GateProcessor`'s ``gate_window_seconds`` timer is a plain,
self-contained ``asyncio.sleep``-based task (mirroring
``klanker_voice.session.SessionLifecycle._service_timer``'s existing
pattern) scoped to the processor itself, NOT to ``SessionLifecycle`` — a real
``SessionLifecycle`` does not exist yet while the gate is locked (Plan 06's
controller wiring constructs the ``CallSession``/``SessionLifecycle`` up
front, as a zeroed ``bypass_accounting=True`` placeholder, precisely so no
real accounting/timer starts until unlock — see
``telephony.controller._finish_stasis_start_gated`` /
``session.SessionLifecycle.upgrade_from_bypass``). The gate timer therefore
genuinely runs and can fire BEFORE any real ``SessionLifecycle`` accounting
begins, consistent with D-05d's "the expensive turn loop is built only after
a pass".

**Redaction discipline (D-05e).** :meth:`GateProcessor.unlock` logs only
``unlocked{method, call_id}``; the fail-closed path logs only
``call_id``. Neither the transcript, the matched words, the PIN, nor a
partial-match ("N of 4") count is ever logged. The 4 passphrase words never
reach any LLM request, persona/system prompt, or transcript ledger — they
live only in this processor's in-memory accumulated-token set for the
duration of the (short) gate window.

**Distinct from the ``greenhouse`` router keyword (D-05f).** This module is a
standalone security/access layer, wired into ``pipeline.build_pipeline`` via
an additive ``gate_processor`` parameter, entirely separate from
``knowledge.router.KnowledgeRouterProcessor``'s persona-unlock keyword
matching. They never share code or state.
"""

from __future__ import annotations

import asyncio
import re
from collections.abc import Awaitable, Callable
from typing import Iterable

from loguru import logger

from pipecat.frames.frames import (
    Frame,
    InterimTranscriptionFrame,
    StartFrame,
    TranscriptionFrame,
    UserStartedSpeakingFrame,
    UserStoppedSpeakingFrame,
)
from pipecat.processors.frame_processor import FrameDirection, FrameProcessor

#: ``unlock(method)`` callback: awaited exactly once, after the processor has
#: already flipped to unlocked (never before -- the caller can safely assume
#: the gate is open when this fires).
UnlockCallback = Callable[[], Awaitable[None]]

#: ``on_fail_closed()`` callback: awaited exactly once, on gate-window
#: expiry with no unlock.
FailClosedCallback = Callable[[], Awaitable[None]]

_TOKEN_RE = re.compile(r"[a-z0-9']+")


def _tokenize(text: str) -> set[str]:
    """Lower-case + tokenize an utterance for order-independent matching."""
    return set(_TOKEN_RE.findall(text.lower()))


def match_passphrase(accumulated: set[str], secret_words: set[str]) -> bool:
    """Order-independent set-membership match (D-05b).

    True only when every word in ``secret_words`` (already lower-cased) is
    present in ``accumulated`` (the caller's lower-cased, tokenized speech
    accumulated across one or more ``TranscriptionFrame``s). An empty
    ``secret_words`` set (e.g. ``gate_mode="dtmf"``, passphrase factor
    disabled) never matches -- returns False, never raises.
    """
    if not secret_words:
        return False
    return secret_words.issubset(accumulated)


def accumulate_dtmf(buffer: str, digit: str, pin: str) -> tuple[str, bool]:
    """Pure DTMF-accumulator helper (Landmine 5): append one digit to
    ``buffer``, keep only the trailing ``len(pin)`` characters (early-exit
    matching -- a caller who fat-fingers extra digits before/after the real
    PIN still matches once the PIN is the most recent ``len(pin)`` digits
    entered), and report whether the result equals ``pin`` exactly.

    An empty/unset ``pin`` or an empty ``digit`` never matches (returns the
    buffer unchanged, ``False``) -- never raises on odd input.
    """
    if not pin or not digit:
        return buffer, False
    new_buffer = (buffer + digit)[-len(pin) :]
    return new_buffer, new_buffer == pin


class GateProcessor(FrameProcessor):
    """The §24 silent answer-gate (D-05), inline in the persistent pipeline.

    Sits immediately after ``stt``, before the duplex/router stage (see
    ``klanker_voice.pipeline.build_pipeline``'s ``gate_processor`` param) --
    the same architectural slot pattern as ``KnowledgeRouterProcessor``.

    While locked (``self.unlocked is False``): swallows (never
    ``push_frame``s) ``TranscriptionFrame``/``InterimTranscriptionFrame``/
    ``UserStartedSpeakingFrame``/``UserStoppedSpeakingFrame`` -- the
    structural redaction boundary (D-05e/R5). Every finalized
    ``TranscriptionFrame`` is tokenized and accumulated into a running
    lower-cased token set; when :func:`match_passphrase` succeeds, the
    processor unlocks itself (``method="passphrase"``). The DTMF PIN path
    never touches this processor's frame stream at all (D-05b: ARI surfaces
    DTMF as a controller-layer event, never a pipecat frame here) -- the
    controller calls :meth:`unlock` directly (``method="dtmf"``) instead.

    A ``gate_window_seconds`` fail-closed timer starts on the first
    ``StartFrame`` this processor observes (i.e. pipeline start) and, on
    expiry with no unlock, awaits the injected ``on_fail_closed`` callback
    exactly once.

    Once unlocked, every frame (including the pre-existing swallow types)
    flows through untouched -- ``process_frame`` becomes a pure pass-through.
    """

    def __init__(
        self,
        *,
        call_id: str,
        passphrase_words: Iterable[str],
        gate_window_seconds: float,
        on_unlock: UnlockCallback,
        on_fail_closed: FailClosedCallback,
        name: str | None = None,
    ) -> None:
        if name is not None:
            super().__init__(name=name)
        else:
            super().__init__()
        self._call_id = call_id
        self._secret_words: set[str] = {
            w.strip().lower() for w in passphrase_words if w and w.strip()
        }
        self._gate_window_seconds = gate_window_seconds
        self._on_unlock = on_unlock
        self._on_fail_closed = on_fail_closed

        self._unlocked = False
        #: True once EITHER unlock or fail-closed has fired -- guards both
        #: paths so exactly one of them ever runs, and the timer never fires
        #: after an unlock (or vice versa).
        self._resolved = False
        self._accumulated_tokens: set[str] = set()
        self._timer_task: asyncio.Task | None = None

    @property
    def unlocked(self) -> bool:
        return self._unlocked

    def start_timer(self) -> None:
        """Start the fail-closed timer. Idempotent (a second call while a
        timer is already running, or after the gate has already resolved,
        is a no-op) -- callers may call this defensively from more than one
        place (e.g. both on the first ``StartFrame`` and explicitly from the
        controller right after construction)."""
        if self._timer_task is None and not self._resolved:
            self._timer_task = asyncio.create_task(self._run_timer())

    async def _run_timer(self) -> None:
        try:
            await asyncio.sleep(self._gate_window_seconds)
        except asyncio.CancelledError:
            raise
        await self._fire_fail_closed()

    async def _fire_fail_closed(self) -> None:
        if self._resolved:
            return
        self._resolved = True
        logger.info(f"gate fail-closed call_id={self._call_id!r}")
        await self._on_fail_closed()

    async def unlock(self, method: str) -> None:
        """Flip the gate to unlocked (idempotent: a second unlock call, from
        either factor firing after the other already did -- D-05b's
        ``gate_mode="either"`` -- or after fail-closed already fired, is a
        no-op). Callable both internally (passphrase match, from
        :meth:`process_frame`) and externally (the controller's DTMF path,
        D-05b: PIN comparison never touches the pipeline)."""
        if self._resolved:
            return
        self._resolved = True
        self._unlocked = True
        if self._timer_task is not None:
            self._timer_task.cancel()
        # D-05e: log ONLY the method + call_id -- never the transcript, the
        # matched words, the PIN, or a partial-match count.
        logger.info(f"unlocked{{method: {method!r}, call_id: {self._call_id!r}}}")
        await self._on_unlock()

    async def process_frame(self, frame: Frame, direction: FrameDirection) -> None:
        await super().process_frame(frame, direction)

        if isinstance(frame, StartFrame) and not self._resolved:
            self.start_timer()

        if self._unlocked:
            await self.push_frame(frame, direction)
            return

        if isinstance(frame, TranscriptionFrame):
            if frame.text and frame.text.strip():
                self._accumulated_tokens |= _tokenize(frame.text)
                if match_passphrase(self._accumulated_tokens, self._secret_words):
                    await self.unlock("passphrase")
            # D-05e/R5: never forward a pre-unlock transcription frame --
            # the structural redaction boundary. This is true whether or
            # not this frame happened to complete the match.
            return

        if isinstance(
            frame,
            (InterimTranscriptionFrame, UserStartedSpeakingFrame, UserStoppedSpeakingFrame),
        ):
            # Swallow speaking-state frames too while locked -- never
            # forward downstream (never gives the caller a partial-match
            # oracle via bot-turn-taking behavior either).
            return

        # Everything else (StartFrame, EndFrame, audio, control/system
        # frames, ...) flows through untouched -- only transcription/
        # speaking-state frames are gated.
        await self.push_frame(frame, direction)
