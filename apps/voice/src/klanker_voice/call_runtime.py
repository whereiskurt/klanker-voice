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

from klanker_voice import quota
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

    Deliberately thin: only ``subject`` is used by this phase (threaded into
    :class:`~klanker_voice.session.SessionLifecycle` as ``user_id``).
    Phase 12 (spec §11/§23) adds real phone -> code -> tier resolution for
    telephony callers; do NOT anticipate that here.
    """

    subject: str
    authenticated: bool = False
    auth_method: str = "webrtc-oidc"


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

    async def run(self) -> None:
        """Start the lifecycle, run the pipeline to completion, then always
        stop the lifecycle on exit (success, error, or cancellation) —
        mirrors the old ``_start_and_run_tracked_session`` bracketing
        (QUOT-02/INFR-06): ``lifecycle.start()`` before, ``lifecycle.stop()``
        in a ``finally`` so the heartbeat lease + active-session slot are
        always released."""
        await self.lifecycle.start()
        try:
            await self.runner.run()
        finally:
            await self.lifecycle.stop()

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
    )
