"""build_pipeline(cfg, transport) + greet-first wiring (RESEARCH Patterns 1 & 6).

Canonical 1.5.0 shape: VAD lives on the user aggregator (not the transport),
services are built via Settings objects, and barge-in context truncation is
built into the frame path — no custom truncation bookkeeping here (Pattern 5).
"""

from __future__ import annotations

from dataclasses import dataclass

from pipecat.frames.frames import LLMRunFrame, TTSSpeakFrame
from pipecat.pipeline.pipeline import Pipeline
from pipecat.pipeline.worker import PipelineParams, PipelineWorker
from pipecat.processors.aggregators.llm_context import LLMContext
from pipecat.processors.aggregators.llm_response_universal import LLMContextAggregatorPair
from pipecat.processors.frameworks.rtvi import RTVIProcessor
from pipecat.transports.base_transport import BaseTransport

from klanker_voice.config import PipelineConfig
from klanker_voice.factories import (
    build_llm,
    build_stt,
    build_tts,
    build_user_aggregator_params,
)


@dataclass
class BuiltPipeline:
    """Pipeline plus the handles later plans need.

    The harness plan (01-03) attaches observers to the PipelineWorker, and
    barge-in truncation checks need the context handle. ``rtvi`` (05-01,
    CLNT-03/04/06) is the RTVIProcessor placed in the pipeline, if any — the
    caller passes it to ``RTVIObserver(...)`` when building the worker.
    """

    pipeline: Pipeline
    context: LLMContext
    user_aggregator: object
    assistant_aggregator: object
    rtvi: RTVIProcessor | None = None


def load_persona(cfg: PipelineConfig) -> str:
    """Read the versioned persona markdown (PIPE-06) from config."""
    return cfg.persona.prompt_path.read_text(encoding="utf-8")


def build_pipeline(
    cfg: PipelineConfig, transport: BaseTransport, *, rtvi: RTVIProcessor | None = None
) -> BuiltPipeline:
    """Assemble the canonical cascade pipeline from config.

    Persona markdown from ``cfg.persona.prompt_path`` seeds the LLMContext
    system message; the greeting instruction lives inside that prompt so copy
    iterates without code changes (Pattern 6).

    ``rtvi`` (05-01, CLNT-03/04/06): when provided, the RTVIProcessor is
    placed immediately after ``transport.input()`` — the standard RTVI
    placement so it observes every upstream/downstream frame in the cascade.
    Optional so pipelines built without a live browser client (harness,
    console) are unaffected.
    """
    stt = build_stt(cfg)
    llm = build_llm(cfg)
    tts = build_tts(cfg)

    context = LLMContext(messages=[{"role": "system", "content": load_persona(cfg)}])
    aggregator_pair = LLMContextAggregatorPair(
        context,
        user_params=build_user_aggregator_params(cfg),
    )
    user_aggregator, assistant_aggregator = aggregator_pair.user(), aggregator_pair.assistant()

    processors = [transport.input()]
    if rtvi is not None:
        processors.append(rtvi)
    processors.extend(
        [
            stt,
            user_aggregator,
            llm,
            tts,
            transport.output(),
            assistant_aggregator,
        ]
    )

    pipeline = Pipeline(processors)
    return BuiltPipeline(
        pipeline=pipeline,
        context=context,
        user_aggregator=user_aggregator,
        assistant_aggregator=assistant_aggregator,
        rtvi=rtvi,
    )


def build_worker(pipeline: Pipeline, *, observers: list | None = None) -> PipelineWorker:
    """Wrap a pipeline in a PipelineWorker with metrics on from day one.

    enable_metrics/enable_usage_metrics are on so the plan-03 latency observer
    drops in without rework.
    """
    return PipelineWorker(
        pipeline,
        params=PipelineParams(enable_metrics=True, enable_usage_metrics=True),
        observers=observers or [],
    )


#: The canonical 1.5.0 greet kick (cli/templates/server/_macros/
#: event_handlers.jinja2). Without this developer message the LLM run sees a
#: system-only (or assistant-terminated) context and produces a "briefing
#: acknowledgment" — or nothing at all on re-connects — instead of greeting
#: (found live in plan 01-03).
GREET_KICK_MESSAGE = {
    "role": "developer",
    "content": "Start by concisely introducing yourself.",
}


async def greet_now(worker: PipelineWorker, context: LLMContext) -> None:
    """Kick the context and queue an LLMRunFrame so K greets immediately (D-04)."""
    context.add_message(dict(GREET_KICK_MESSAGE))
    await worker.queue_frames([LLMRunFrame()])


def register_greet_first(
    transport: BaseTransport, worker: PipelineWorker, context: LLMContext
) -> None:
    """Greet the moment the connection lands (D-04, Pattern 6).

    Verified against the installed 1.5.0 CLI template
    (``cli/templates/server/_macros/event_handlers.jinja2``): the standard
    handler appends a developer kick message to the context and queues
    ``LLMRunFrame`` on ``on_client_connected``. The eval transport
    pre-suppresses greetings in text-mode scenarios before this event fires,
    so the wiring is harness-compatible.

    Note: ``LocalAudioTransport`` (terminal mode) never fires
    ``on_client_connected`` — console.py calls :func:`greet_now` directly
    after startup instead.
    """

    @transport.event_handler("on_client_connected")
    async def _on_client_connected(transport, client):  # noqa: ANN001 — pipecat handler shape
        await greet_now(worker, context)


async def inject_warning_instruction(worker: PipelineWorker, context: LLMContext, copy: str) -> None:
    """D-04 spoken wind-down (natural warning): push a high-priority
    developer instruction into the LLM context and queue an ``LLMRunFrame``
    so the concierge weaves the time-remaining warning into its very next
    turn, in its own voice — the warning text is never spoken verbatim by
    code (mirrors the :func:`greet_now` kick pattern)."""
    context.add_message({"role": "developer", "content": copy})
    await worker.queue_frames([LLMRunFrame()])


async def speak_goodbye(worker: PipelineWorker, copy: str) -> None:
    """D-04/D-05 spoken wind-down (deterministic goodbye): send ``copy``
    straight to the TTS stage via ``TTSSpeakFrame`` — the TTS service
    recognizes this frame directly regardless of pipeline position, so the
    goodbye bypasses the LLM entirely (guaranteed stop, no prompt-injection
    surface through it — T-04-19). ``append_to_context=False``: a
    hard-close follows immediately after, so there is no future turn that
    would need this line in context."""
    await worker.queue_frames([TTSSpeakFrame(text=copy, append_to_context=False)])
