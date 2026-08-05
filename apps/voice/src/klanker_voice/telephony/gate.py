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

**Announcement spoken-trigger factor (quick task 260727-pdh, D-06/D-07).**
The gate now accumulates tokens whenever EITHER the concierge passphrase
factor is enabled OR an announcement-words registry is armed — including on
an OTP-only DID, where accumulation used to be skipped entirely (quick task
260717-o2q). This still forwards NOTHING and logs NOTHING: every
``TranscriptionFrame`` remains swallowed (never ``push_frame``d) whether or
not it completes an announcement match, and the only new log line on this
path is ``cancel_for_takeover``'s existing ``reason`` + ``call_id`` line —
never the heard tokens, the matched words, or the registry key. The matched
entry's opaque key is handed to the injected ``on_announcement_words``
callback (never the accumulated words themselves), which is spawned via
``asyncio.create_task`` — never awaited inline, so a slow OTP
fetch/readout/grace-sleep downstream can never stall this processor's frame
queue.

**Opt-in fail-path debug logging (260714, relaxes D-05e for the FAIL path
only).** When ``telephony.gate_debug_log_heard=true`` (default False), the
fail-closed path additionally logs one ``gate_fail_heard{call_id, caller_id,
heard_tokens, token_count, window_expired}`` line -- the caller's number plus
the tokens STT actually heard -- so an accent/STT mismatch on the passphrase
can be debugged. Operator-accepted and safe: a failed attempt's heard words are
by definition NOT the passphrase, so no secret leaks; it never runs on the
success/unlock path; it never logs ``self._secret_words`` or any PIN (the DTMF
PIN never reaches this processor); it stays in the telephony-edge CloudWatch
log, never the ledger/LLM/router. With the flag off the redaction posture above
is byte-identical.

**Distinct from the ``greenhouse`` router keyword (D-05f).** This module is a
standalone security/access layer, wired into ``pipeline.build_pipeline`` via
an additive ``gate_processor`` parameter, entirely separate from
``knowledge.router.KnowledgeRouterProcessor``'s persona-unlock keyword
matching. They never share code or state.
"""

from __future__ import annotations

import asyncio
import re
from collections.abc import Awaitable, Callable, Mapping
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

#: ``on_announcement_words(key)`` callback (quick task 260727-pdh, the
#: spoken-trigger seam from this processor to the controller's announcement
#: dispatch): a callable taking ONE opaque, non-secret registry key
#: (never the matched words, never the raw transcript) and returning an
#: awaitable. Spawned via ``asyncio.create_task`` from inside
#: ``process_frame`` -- never awaited inline (see :meth:`GateProcessor.
#: process_frame`'s "spawn, do not await" note) -- and fires AT MOST ONCE
#: per call, since a match resolves the gate synchronously first.
AnnouncementWordsCallback = Callable[[str], Awaitable[None]]

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
        caller_id: str | None = None,
        debug_log_heard: bool = False,
        concierge_unlock_enabled: bool = True,
        announcement_words: Mapping[str, Iterable[str]] | None = None,
        on_announcement_words: AnnouncementWordsCallback | None = None,
        max_cue_lead_seconds: float = 8.0,
        name: str | None = None,
    ) -> None:
        if name is not None:
            super().__init__(name=name)
        else:
            super().__init__()
        self._call_id = call_id
        self._caller_id = caller_id
        #: Opt-in (``telephony.gate_debug_log_heard``): when True, the
        #: fail-closed path logs the caller_id + the heard tokens for accent/STT
        #: debugging. Default False keeps the D-05e posture byte-identical.
        self._debug_log_heard = debug_log_heard
        #: Quick task 260717-o2q (per-DID gate policy Part A): when False,
        #: the concierge passphrase (spoken) AND concierge DTMF PIN factors
        #: are BOTH suppressed -- only ``cancel_for_takeover`` (the 333266
        #: announcement takeover) and the fail-closed timer can still resolve
        #: the gate. Default True is byte-identical to every pre-260717-o2q
        #: behavior.
        self._concierge_unlock_enabled = concierge_unlock_enabled
        self._secret_words: set[str] = {
            w.strip().lower() for w in passphrase_words if w and w.strip()
        }
        self._gate_window_seconds = gate_window_seconds
        self._on_unlock = on_unlock
        self._on_fail_closed = on_fail_closed

        self._unlocked = False
        #: Quick task 260727-v5e (D-04 telemetry): which unlock factor
        #: resolved the gate -- "dtmf" or "passphrase", or ``None`` before
        #: resolution or when ``concierge_unlock_enabled=False`` suppressed
        #: the attempt. Read-only via the :attr:`unlock_method` property;
        #: never set by ``cancel_for_takeover`` (that path never unlocks).
        self._unlock_method: str | None = None
        #: True once EITHER unlock or fail-closed has fired -- guards both
        #: paths so exactly one of them ever runs, and the timer never fires
        #: after an unlock (or vice versa).
        self._resolved = False
        #: Set on unlock: swallow the TAIL of the utterance that was in flight
        #: when the gate opened (the passphrase keeps transcribing for a beat
        #: after unlock) until a genuinely NEW user turn begins. Without this,
        #: that trailing transcription passes through as the first user turn --
        #: leaking the passphrase into the LLM/ledger AND triggering a second
        #: self-intro on top of greet_now's greeting (the "double greeting").
        self._suppress_speech_until_new_turn = False
        self._accumulated_tokens: set[str] = set()
        self._timer_task: asyncio.Task | None = None
        #: Quick task 260805-fki: the HARD upper bound (D-05d safety cap) on
        #: how far a cue lead may push the fail-closed deadline -- see
        #: :meth:`defer_for_cue`.
        self._max_cue_lead_seconds = max_cue_lead_seconds
        #: Monotonic (``loop.time()``) absolute fail-closed fire time, the
        #: SINGLE source of truth for when ``_run_timer`` fires. ``None``
        #: until :meth:`start_timer` runs. ``start_timer`` and
        #: :meth:`defer_for_cue` are its only writers; ``_run_timer`` only
        #: reads it.
        self._deadline: float | None = None
        #: Monotonic stamp of when the fail-closed timer actually started --
        #: the fixed anchor :meth:`defer_for_cue`'s fail-closed cap is
        #: computed against, distinct from the (possibly rebased)
        #: ``_deadline`` itself.
        self._timer_started_at: float | None = None
        #: A cue lead that arrived via :meth:`defer_for_cue` BEFORE
        #: :meth:`start_timer` ran -- applied when the timer finally starts.
        #: 0.0 (no-op) once consumed or when no defer has happened yet.
        self._pending_cue_lead: float = 0.0
        #: One-shot guard: a cue can only ever rebase the deadline once per
        #: call, so a duplicate ``defer_for_cue`` call (or a cue re-fired by
        #: some future retry path) can never accumulate multiple leads.
        self._cue_deferred: bool = False

        # Quick task 260727-pdh: the announcement spoken-trigger registry --
        # opaque entry key -> normalized (strip+lower, empties dropped) word
        # set, insertion order preserved (dict preserves it since Python
        # 3.7). Mirrors self._secret_words' own normalization. An entry
        # whose normalized set ends up empty is dropped entirely -- it can
        # never match match_passphrase's `if not secret_words: return False`
        # guard anyway, so keeping it around would only cost a wasted
        # iteration per frame.
        self._announcement_words: dict[str, frozenset[str]] = {}
        if announcement_words:
            for key, words in announcement_words.items():
                normalized = frozenset(w.strip().lower() for w in words if w and w.strip())
                if normalized:
                    self._announcement_words[key] = normalized
        self._on_announcement_words = on_announcement_words
        #: Strong reference to the spawned announcement-callback task (mirrors
        #: ``ActiveCall.sms_task`` in the controller) -- ``asyncio.create_task``
        #: only holds a WEAK reference, so without this the fire-and-forget
        #: callback could be garbage-collected mid-flight.
        self._announcement_task: asyncio.Task | None = None

    @property
    def unlocked(self) -> bool:
        return self._unlocked

    @property
    def unlock_method(self) -> str | None:
        """Quick task 260727-v5e (D-04 telemetry): which factor unlocked the
        gate -- ``"dtmf"``/``"passphrase"``, or ``None`` before resolution
        (or when ``concierge_unlock_enabled=False`` suppressed the
        attempt). A D-05e-safe read-only view -- never the transcript, the
        PIN, or the passphrase words themselves."""
        return self._unlock_method

    @property
    def token_count(self) -> int:
        """Quick task 260727-v5e (D-04/D-05 telemetry): the COUNT of
        distinct accumulated tokens -- the ``words_heard`` telemetry field's
        source. Deliberately returns an ``int``, NEVER the token set itself
        -- the accumulated-token SET must never cross the processor
        boundary (D-05)."""
        return len(self._accumulated_tokens)

    def start_timer(self) -> None:
        """Start the fail-closed timer. Idempotent (a second call while a
        timer is already running, or after the gate has already resolved,
        is a no-op) -- callers may call this defensively from more than one
        place (e.g. both on the first ``StartFrame`` and explicitly from the
        controller right after construction).

        Quick task 260805-fki: stamps ``_timer_started_at`` and computes the
        initial ``_deadline`` (``_timer_started_at + gate_window_seconds +
        _pending_cue_lead``) BEFORE creating the timer task -- so a
        :meth:`defer_for_cue` call that arrived before the timer started is
        honoured from the very first tick, and ``_run_timer`` never has to
        compute its own start point.

        D-05d invariant: this call is UNCONDITIONAL at both of this gate's
        real call sites (the controller's ``_finish_stasis_start_gated``,
        right after construction, and this processor's own first
        ``StartFrame``) -- :meth:`defer_for_cue` can only ever move the
        deadline this establishes FORWARD by a bounded amount, never remove
        or delay its start. A cue that never plays, errors, hangs, or is
        barge-in-flushed therefore still fails closed at
        ``_timer_started_at + gate_window_seconds`` at the absolute latest
        for the undeferred case, or ``_timer_started_at + gate_window_seconds
        + max_cue_lead_seconds`` at the absolute latest on ANY path,
        deferred or not."""
        if self._timer_task is None and not self._resolved:
            loop = asyncio.get_running_loop()
            self._timer_started_at = loop.time()
            self._deadline = (
                self._timer_started_at + self._gate_window_seconds + self._pending_cue_lead
            )
            self._timer_task = asyncio.create_task(self._run_timer())

    def defer_for_cue(self, cue_seconds: float) -> None:
        """Rebase the fail-closed deadline to (approximately) ``cue_seconds``
        past NOW, so the caller's dialing window starts when the ring+hey
        pickup cue finishes rather than at pipeline start (quick task
        260805-fki). One-shot: a second call is always a no-op, so a cue
        cannot be re-deferred by some future retry path into accumulating
        multiple leads.

        No-op (and never raises) when the gate has already resolved
        (unlock, fail-closed, or ``cancel_for_takeover``) -- this can never
        resurrect an already-cancelled timer. ``cue_seconds`` is clamped to
        ``[0.0, max_cue_lead_seconds]`` first: a non-positive lead is a
        pure no-op (it can never SHORTEN the window), and an absurdly large
        lead is capped at ``max_cue_lead_seconds`` -- the D-05d safety cap.

        If :meth:`start_timer` has not run yet, the clamped lead is stashed
        in ``_pending_cue_lead`` for ``start_timer`` to apply. Otherwise the
        deadline is rebased in place:
        ``_deadline = min(max(_deadline, now + lead + gate_window_seconds),
        _timer_started_at + gate_window_seconds + max_cue_lead_seconds)`` --
        the inner ``max`` guarantees the window can only ever be extended,
        never shortened by a defer call that (for whatever reason) computes
        a smaller lead than one already applied; the outer ``min`` is the
        fail-closed cap, so ``_run_timer`` is bounded no matter what value
        is passed in here.

        This is the D-05d proof for this change: the fail-closed timer is
        started unconditionally by :meth:`start_timer` regardless of this
        method ever being called, and this method can only ever move that
        timer's deadline FORWARD by a bounded amount -- so a cue that never
        plays, errors, is interrupted, or never signals still fails closed
        at ``_timer_started_at + gate_window_seconds``, and the absolute
        worst case on ANY path is
        ``_timer_started_at + gate_window_seconds + max_cue_lead_seconds``.
        Synchronous; never raises."""
        if self._resolved or self._cue_deferred:
            return
        self._cue_deferred = True
        lead = max(0.0, min(cue_seconds, self._max_cue_lead_seconds))
        if lead <= 0.0:
            return
        if self._timer_started_at is None:
            self._pending_cue_lead = lead
            return
        loop = asyncio.get_running_loop()
        candidate_deadline = loop.time() + lead + self._gate_window_seconds
        cap = self._timer_started_at + self._gate_window_seconds + self._max_cue_lead_seconds
        assert self._deadline is not None  # timer started -> always set together
        self._deadline = min(max(self._deadline, candidate_deadline), cap)

    async def _run_timer(self) -> None:
        """Sleep until ``_deadline`` (re-checking after every wake so a
        mid-flight :meth:`defer_for_cue` extension actually takes effect),
        then fire fail-closed exactly once. Cancellation propagates exactly
        as before (:meth:`unlock` / :meth:`cancel_for_takeover` cancel this
        task)."""
        try:
            while True:
                loop = asyncio.get_running_loop()
                assert self._deadline is not None  # only started from start_timer
                remaining = self._deadline - loop.time()
                if remaining <= 0:
                    break
                await asyncio.sleep(remaining)
        except asyncio.CancelledError:
            raise
        await self._fire_fail_closed()

    async def _fire_fail_closed(self) -> None:
        if self._resolved:
            return
        self._resolved = True
        logger.info(f"gate fail-closed call_id={self._call_id!r}")
        # Opt-in D-05e relaxation, FAIL PATH ONLY (telephony.gate_debug_log_heard):
        # log the caller_id + the tokens STT actually heard so an accent/STT
        # mismatch on the passphrase can be debugged. Safe because a failed
        # attempt's heard words are BY DEFINITION not the passphrase, so this
        # leaks no secret. NEVER logs self._secret_words / any PIN; NEVER runs on
        # the success path (unlock). Operator-CloudWatch only. Default off keeps
        # the redaction posture byte-identical.
        if self._debug_log_heard:
            heard = sorted(self._accumulated_tokens)
            logger.info(
                f"gate_fail_heard{{call_id: {self._call_id!r}, "
                f"caller_id: {self._caller_id!r}, heard_tokens: {heard!r}, "
                f"token_count: {len(heard)}, window_expired: true}}"
            )
        await self._on_fail_closed()

    async def unlock(self, method: str) -> None:
        """Flip the gate to unlocked (idempotent: a second unlock call, from
        either factor firing after the other already did -- D-05b's
        ``gate_mode="either"`` -- or after fail-closed already fired, is a
        no-op). Callable both internally (passphrase match, from
        :meth:`process_frame`) and externally (the controller's DTMF path,
        D-05b: PIN comparison never touches the pipeline).

        Quick task 260717-o2q: when ``concierge_unlock_enabled`` is False,
        ``method in ("passphrase", "dtmf")`` is a no-op -- neither factor
        can ever open the gate. Any other ``method`` value is unaffected.
        This never touches ``cancel_for_takeover``, which resolves the gate
        via a wholly separate code path and stays enabled regardless."""
        if not self._concierge_unlock_enabled and method in ("passphrase", "dtmf"):
            return
        if self._resolved:
            return
        self._resolved = True
        self._unlocked = True
        # Quick task 260727-v5e (D-04 telemetry): stamp the unlock method
        # ONLY here -- after both the concierge-suppression and
        # already-resolved guards above have passed -- so a suppressed or
        # already-resolved unlock attempt never records a method.
        self._unlock_method = method
        self._suppress_speech_until_new_turn = True
        if self._timer_task is not None:
            self._timer_task.cancel()
        # D-05e: log ONLY the method + call_id -- never the transcript, the
        # matched words, the PIN, or a partial-match count.
        logger.info(f"unlocked{{method: {method!r}, call_id: {self._call_id!r}}}")
        await self._on_unlock()

    def cancel_for_takeover(self, reason: str) -> None:
        """Resolve the gate WITHOUT unlocking (quick task 260716-1g0,
        Revision 2 CTF phone-OTP DTMF trigger) -- lets a caller-layer
        handler (the controller's ``_gate_announcement``) take over an
        already-answered, still-gated call and speak a line of its own,
        while keeping the §24 redaction boundary CLOSED the whole time
        (``self._unlocked`` is never set here, so ``process_frame`` keeps
        swallowing ``TranscriptionFrame``/``InterimTranscriptionFrame``/
        ``UserStartedSpeakingFrame``/``UserStoppedSpeakingFrame`` exactly as
        it did before this call).

        Idempotent (a second call, or a call after ``unlock``/fail-closed
        already resolved the gate, is a no-op) -- gives the caller the same
        "gate already resolved before teardown" invariant
        :meth:`_fire_fail_closed` relies on, so no fail-closed timer can
        race a second goodbye. Cancels the fail-closed timer task if one is
        running. Logs ONLY ``reason`` + ``call_id`` -- never the
        transcript, the matched words, the PIN, or any DTMF code (D-05e)."""
        if self._resolved:
            return
        self._resolved = True
        if self._timer_task is not None:
            self._timer_task.cancel()
        logger.info(f"gate cancelled for takeover reason={reason!r} call_id={self._call_id!r}")

    async def process_frame(self, frame: Frame, direction: FrameDirection) -> None:
        await super().process_frame(frame, direction)

        if isinstance(frame, StartFrame) and not self._resolved:
            self.start_timer()

        if self._unlocked:
            # Post-unlock tail suppression (double-greeting / passphrase-leak
            # fix): swallow the trailing speech frames of the utterance that was
            # in flight at unlock, until a genuinely NEW user turn starts. A
            # ``UserStartedSpeakingFrame`` ends the suppression (and itself flows
            # through, along with everything after). Non-speech frames (audio,
            # the greeting's TTS, control/system) always flow -- only the
            # unlocking utterance's speech tail is eaten.
            if self._suppress_speech_until_new_turn:
                if isinstance(frame, UserStartedSpeakingFrame):
                    self._suppress_speech_until_new_turn = False
                elif isinstance(
                    frame,
                    (
                        TranscriptionFrame,
                        InterimTranscriptionFrame,
                        UserStoppedSpeakingFrame,
                    ),
                ):
                    return
            await self.push_frame(frame, direction)
            return

        if isinstance(frame, TranscriptionFrame):
            has_text = bool(frame.text and frame.text.strip())
            # Quick task 260727-pdh (D-06): tokens now accumulate when
            # EITHER the concierge passphrase factor is enabled OR the
            # announcement-words registry is armed -- previously
            # accumulation was gated behind concierge_unlock_enabled alone
            # (quick task 260717-o2q), so an OTP-only DID never even
            # tokenized speech. An armed announcement registry needs that
            # same token set to match against, even when the concierge
            # factor itself can never unlock.
            if has_text and (self._concierge_unlock_enabled or self._announcement_words):
                self._accumulated_tokens |= _tokenize(frame.text)

            # 1. Concierge match attempt -- unchanged semantics, unchanged
            # priority (stays first). Quick task 260717-o2q: skipped
            # entirely when the concierge factor is suppressed.
            if self._concierge_unlock_enabled and has_text:
                if match_passphrase(self._accumulated_tokens, self._secret_words):
                    await self.unlock("passphrase")

            # 2. Announcement-words match attempt (quick task 260727-pdh,
            # D-04/D-05/D-06/D-07/T-pdh-01/T-pdh-05): only when a registry
            # is armed, a callback is injected, and the gate has NOT
            # already resolved (a concierge unlock above, a prior
            # announcement match, or fail-closed) -- this is what makes the
            # callback fire AT MOST ONCE. Iterate the registry IN ORDER and
            # reuse match_passphrase (D-05 -- no second matcher). On the
            # first match: resolve the gate SYNCHRONOUSLY via
            # cancel_for_takeover BEFORE spawning the callback (closes the
            # fail-closed-timer race -- T-pdh-05), then spawn (never await
            # inline -- awaiting here would block this processor's frame
            # queue for the whole OTP fetch/readout/grace sleep, stalling
            # every frame including teardown control frames).
            if (
                self._announcement_words
                and self._on_announcement_words is not None
                and not self._resolved
            ):
                for key, words in self._announcement_words.items():
                    if match_passphrase(self._accumulated_tokens, words):
                        self.cancel_for_takeover("announcement")
                        self._announcement_task = asyncio.create_task(
                            self._on_announcement_words(key)
                        )
                        break

            # D-05e/R5: never forward a pre-unlock transcription frame --
            # the structural redaction boundary. This is true whether or
            # not this frame happened to complete either match.
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
