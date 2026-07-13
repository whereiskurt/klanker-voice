"""Transport-neutral shared call runtime (Phase A, spec §6 / D-01/D-02).

``create_call_session`` is the ONE seam every live voice session — WebRTC
today, telephony from Phase 10 on — constructs, runs, and idempotently closes
through. It owns: ``gate_result`` -> :class:`~klanker_voice.session.SessionLifecycle`
-> :func:`~klanker_voice.pipeline.build_pipeline` (already transport-neutral,
accepting an arbitrary :class:`~pipecat.transports.base_transport.BaseTransport`)
-> observers -> warning/goodbye callbacks -> greeting -> a single idempotent
close path (:meth:`CallSession.close`).

Three couplings resisted a perfectly-clean extraction (D-08 architecture
note — see 09-01-SUMMARY.md for the full writeup):

1. The quota ``start_gate`` stays at the HTTP layer (``server.py``'s
   ``offer()``), ahead of any transport/pipeline construction (T-09-01) — its
   ``GateResult`` is threaded into this function as a parameter rather than
   invoked from inside the runtime.
2. The ambience mixer + the transport's ``TransportParams`` are built by the
   transport-specific caller, since they must be attached at transport
   construction time (D-03) — before this function ever sees the transport.
3. The pipeline is now built at connection-callback time (a local,
   network-free construction) rather than inside the old fire-and-forget
   tracked-session task. The genuinely slow step — ``lifecycle.start()``'s
   CloudWatch/ECS/DynamoDB calls — stays deferred inside :meth:`CallSession.run`,
   so the BUG-1 teardown-before-slow-start guarantee still holds.

This module imports ONLY transport-neutral pipecat base types plus
``klanker_voice`` modules — never a transport-specific signaling/connection
class, and never constructs a speech provider directly
(:mod:`klanker_voice.factories`, via ``build_pipeline``, remains the single
source, spec §22.2).
"""

from __future__ import annotations

import asyncio
from dataclasses import dataclass
from typing import Literal

from loguru import logger

from pipecat.pipeline.worker import PipelineWorker
from pipecat.processors.aggregators.llm_context import LLMContext
from pipecat.processors.frame_processor import FrameProcessor
from pipecat.transports.base_transport import BaseTransport
from pipecat.workers.runner import WorkerRunner

from klanker_voice import ledger, quota
from klanker_voice.config import DuplexConfig, KnowledgeConfig, PipelineConfig, QuotaConfig
from klanker_voice.observers import LatencyReportObserver
from klanker_voice.pipeline import (
    build_pipeline,
    build_worker,
    inject_warning_instruction,
    register_greet_first,
    speak_goodbye,
)
from klanker_voice.rtvi import build_rtvi_observer_params, build_rtvi_processor
from klanker_voice.session import SessionLifecycle, TeardownObserver
from pipecat.processors.frameworks.rtvi import RTVIObserver


@dataclass(frozen=True)
class CallIdentity:
    """Minimal, transport-neutral caller identity.

    ``subject`` is the only field used by every existing caller (threaded
    into :class:`~klanker_voice.session.SessionLifecycle` as ``user_id``).

    Phase 12 (spec §11/§23, D-02/D-05) adds three additive, defaulted fields
    for telephony callers: ``tier_id`` (the caller's entitled tier, resolved
    by the telephony controller's caller-ID -> ``/tel`` mint step --
    :func:`klanker_voice.telephony.controller.AsteriskCallController.
    _mint_tier_from_caller_id`), ``caller_id`` (the normalized E.164 ANI),
    and ``did`` (the dialed number). All three stay ``None`` for the WebRTC
    path (and for any telephony call where the caller-ID mint is
    unconfigured -- the legacy Phase-11 static-tier grant) -- the additive
    defaults keep every existing construction call site byte-unchanged.

    Phase 15 (LEDG-01) adds two more additive, defaulted fields for the
    transcription ledger: ``email`` (the webrtc magic-link token's email
    claim -- ``None`` for anonymous/bypass and every PSTN call) and
    ``code`` (the raw access code, when directly known -- e.g. the
    telephony controller's §23 mint-token ``sub``; webrtc callers instead
    let :func:`create_call_session` derive it from ``subject`` via
    :func:`~klanker_voice.ledger.parse_code_from_sub`, so ``code`` usually
    stays ``None`` here for that path).
    """

    subject: str
    authenticated: bool = False
    auth_method: str = "webrtc-oidc"
    tier_id: str | None = None
    caller_id: str | None = None
    did: str | None = None
    email: str | None = None
    code: str | None = None


@dataclass
class CallSession:
    """One live voice session, owned end-to-end by this module.

    Returned by :func:`create_call_session`, already fully wired (pipeline,
    worker, observers, callbacks, greeting) but NOT yet started — the caller
    awaits :meth:`run` to actually start the lifecycle and run the pipeline.
    """

    session_id: str
    worker: PipelineWorker
    lifecycle: SessionLifecycle
    runner: WorkerRunner
    #: Phase 11 (D-05c): the built pipeline's ``LLMContext``, exposed so a
    #: caller building around the §24 gate (the telephony controller) can
    #: call :func:`~klanker_voice.pipeline.greet_now` itself, at the real
    #: unlock boundary, instead of via ``register_greet_first`` (which this
    #: module skips whenever a ``gate_processor`` is supplied — the greeting
    #: fires on unlock, not on answer).
    context: LLMContext
    #: Phase 15 (LEDG-01/LEDG-02): the per-session transcription ledger
    #: writer, always constructed by :func:`create_call_session` (never
    #: ``None`` in practice today, but typed nullable for flexibility).
    #: Constructed disabled (``enabled=False``) for bypass/smoke sessions
    #: (T-15-03-02) so it buffers and flushes nothing.
    writer: ledger.LedgerWriter | None

    async def run(self) -> None:
        """Start the lifecycle, run the pipeline to completion, then always
        stop the lifecycle on exit (success, error, or cancellation) —
        mirrors the old ``_start_and_run_tracked_session`` bracketing
        (QUOT-02/INFR-06): ``lifecycle.start()`` before, ``lifecycle.stop()``
        in a ``finally`` so the heartbeat lease + active-session slot are
        always released. The ledger writer's final flush (LEDG-02, Pitfall
        3) rides the SAME ``finally`` bracket, after ``lifecycle.stop()`` —
        it runs on success, error, AND cancellation."""
        await self.lifecycle.start()
        try:
            await self.runner.run()
        finally:
            await self.lifecycle.stop()
            if self.writer is not None:
                await self.writer.close()

    async def close(self, reason: str) -> None:
        """The single idempotent close path (D-05): delegates to
        ``lifecycle.release()``, whose own ``_stopped`` guard makes repeated
        or racing calls a no-op."""
        logger.info(f"Closing session {self.session_id}: {reason}")
        await self.lifecycle.release()


async def create_call_session(
    *,
    transport: BaseTransport,
    identity: CallIdentity,
    gate_result: quota.GateResult,
    cfg: PipelineConfig,
    knowledge_cfg: KnowledgeConfig,
    duplex_cfg: DuplexConfig,
    quota_cfg: QuotaConfig,
    channel: Literal["webrtc", "pstn"],
    metadata: dict[str, str],
    gate_processor: FrameProcessor | None = None,
) -> CallSession:
    """Construct (but do not start) a :class:`CallSession` around an
    arbitrary ``transport`` (D-01/D-02).

    ``channel`` and ``metadata`` are accepted and available for logging/future
    branching (``"webrtc"`` this phase; ``"pstn"`` reserved for Phase 10+) —
    this function does NOT branch on ``channel`` yet.

    Does NOT call ``lifecycle.start()`` or ``runner.run()`` — that is
    :meth:`CallSession.run`'s job, so the slow, AWS-bound ``lifecycle.start()``
    stays deferred into the caller's spawned task (preserving the BUG-1
    teardown-before-slow-start ordering).

    Phase 11 (D-05): ``gate_processor``, when supplied, is threaded straight
    into :func:`~klanker_voice.pipeline.build_pipeline` (additive, ``None``
    by default — every WebRTC caller passes nothing, so that path is
    byte-unchanged) AND suppresses ``register_greet_first`` — the §24 gate
    fires the greeting itself at the real unlock boundary (D-05c), not on
    answer/connect. ``gate_result`` is still required in this case: the
    telephony controller passes a zeroed, ``bypass_accounting=True``
    placeholder so the ``SessionLifecycle`` this call constructs starts NO
    real accounting/timers while the gate is locked (see
    ``klanker_voice.session.SessionLifecycle.upgrade_from_bypass``, called
    by the controller once ``quota.start_gate`` actually grants a tier at
    unlock).
    """
    logger.info(
        f"create_call_session: channel={channel} session_id={gate_result.session_id} "
        f"metadata={metadata}"
    )

    lifecycle = SessionLifecycle(
        user_id=identity.subject,
        session_id=gate_result.session_id,
        tier=gate_result.tier,
        quota_config=quota_cfg,
        bypass_accounting=gate_result.bypass_accounting,
    )

    rtvi = build_rtvi_processor()
    built = build_pipeline(
        cfg,
        transport,
        rtvi=rtvi,
        knowledge_cfg=knowledge_cfg,
        duplex_cfg=duplex_cfg,
        remaining_seconds_fn=lifecycle.remaining_seconds,
        gate_processor=gate_processor,
    )
    worker = build_worker(
        built.pipeline,
        observers=[
            LatencyReportObserver(cfg),
            RTVIObserver(rtvi, params=build_rtvi_observer_params()),
            TeardownObserver(lifecycle),
        ],
    )

    # Phase 15 (LEDG-01/LEDG-02, T-15-03-01/T-15-03-02): the ONE tap seam --
    # every entry path (webrtc voice1/voice2, PSTN) constructs its session
    # here. `enabled=not gate_result.bypass_accounting` keeps bypass/smoke
    # sessions AND a still-locked §24 telephony placeholder from ledgering
    # anything (the telephony controller flips this on at unlock, alongside
    # `SessionLifecycle.upgrade_from_bypass`). `code` prefers an already-known
    # raw code (telephony's §23 mint-token sub, threaded via
    # `CallIdentity.code`) and otherwise tries to parse one out of the
    # webrtc/bypass `subject` (`anon:<code>:<uuid>`) -- both paths degrade to
    # `None` (no code, no code_hash) for opaque Auth.js/magic-link subjects.
    writer = ledger.LedgerWriter(
        session_id=gate_result.session_id,
        email=identity.email,
        code=identity.code or ledger.parse_code_from_sub(identity.subject),
        caller_id=identity.caller_id,
        did=identity.did,
        tier_id=gate_result.tier.tier_id,
        channel=channel,
        enabled=not gate_result.bypass_accounting,
    )

    @built.user_aggregator.event_handler("on_user_turn_message_added")
    async def _ledger_user(_agg, message):  # noqa: ANN001 — pipecat handler shape
        # The finalized user STT turn, exactly as written to the LLM
        # context (post-duplex-suppression, post-§24-gate) -- `content` is
        # always populated per pipecat's own contract.
        await writer.append(role="user", text=message.content)

    @built.assistant_aggregator.event_handler("on_assistant_turn_stopped")
    async def _ledger_assistant(_agg, message):  # noqa: ANN001 — pipecat handler shape
        # The assistant aggregator sits AFTER transport.output() (pipeline.py)
        # -- `content` is the actually-spoken, barge-in-truncated reply, and
        # `interrupted` is True when a barge-in cut it off. Skip an empty
        # content (e.g. a fully-interrupted-before-any-tokens turn) -- there
        # is nothing to ledger.
        if message.content:
            await writer.append(
                role="assistant", text=message.content, interrupted=message.interrupted
            )

    # D-05c: with a §24 gate present, the greeting fires on UNLOCK (the
    # caller invokes pipeline.greet_now itself via CallSession.context), not
    # on answer/connect -- register_greet_first would greet a still-locked
    # caller.
    if cfg.persona.greet_first and gate_processor is None:
        register_greet_first(transport, worker, built.context)

    runner = WorkerRunner(handle_sigint=False)
    await runner.add_workers(worker)

    async def _on_warning() -> None:
        # D-04 natural warning: a high-priority LLM-context instruction, not
        # spoken verbatim by code.
        await inject_warning_instruction(worker, built.context, quota_cfg.warning_copy)

    async def _on_stop() -> None:
        # D-04/D-05: the deterministic goodbye bypasses the LLM, gets up to
        # goodbye_grace_seconds to finish, then a hard close.
        await speak_goodbye(worker, quota_cfg.goodbye_copy)
        await asyncio.sleep(quota_cfg.goodbye_grace_seconds)
        await runner.cancel("session wind-down complete")

    lifecycle.on_warning = _on_warning
    lifecycle.on_stop = _on_stop
    # D-06 layer 3 fallback / belt-and-suspenders: once release() fires,
    # cancel the runner (end the running pipeline) so an abandoned session
    # never keeps burning STT/LLM/TTS spend after its slot has been freed.
    lifecycle.on_released = runner.cancel

    @transport.event_handler("on_client_disconnected")
    async def _on_client_disconnected(transport, client):  # noqa: ANN001 — pipecat handler shape
        await lifecycle.on_transport_disconnected()

    @transport.event_handler("on_client_connected")
    async def _on_client_reconnected(transport, client):  # noqa: ANN001 — pipecat handler shape
        await lifecycle.on_transport_reconnected()

    return CallSession(
        session_id=gate_result.session_id,
        worker=worker,
        lifecycle=lifecycle,
        runner=runner,
        context=built.context,
        writer=writer,
    )
