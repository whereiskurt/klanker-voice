"""build_pipeline(cfg, transport) + greet-first wiring (RESEARCH Patterns 1 & 6).

Canonical 1.5.0 shape: VAD lives on the user aggregator (not the transport),
services are built via Settings objects, and barge-in context truncation is
built into the frame path — no custom truncation bookkeeping here (Pattern 5).
"""

from __future__ import annotations

from dataclasses import dataclass

from pipecat.frames.frames import LLMRunFrame
from pipecat.pipeline.pipeline import Pipeline
from pipecat.pipeline.worker import PipelineParams, PipelineWorker
from pipecat.processors.aggregators.llm_context import LLMContext
from pipecat.processors.aggregators.llm_response_universal import LLMContextAggregatorPair
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
    barge-in truncation checks need the context handle.
    """

    pipeline: Pipeline
    context: LLMContext
    user_aggregator: object
    assistant_aggregator: object


def load_persona(cfg: PipelineConfig) -> str:
    """Read the versioned persona markdown (PIPE-06) from config."""
    return cfg.persona.prompt_path.read_text(encoding="utf-8")


def build_pipeline(cfg: PipelineConfig, transport: BaseTransport) -> BuiltPipeline:
    """Assemble the canonical cascade pipeline from config.

    Persona markdown from ``cfg.persona.prompt_path`` seeds the LLMContext
    system message; the greeting instruction lives inside that prompt so copy
    iterates without code changes (Pattern 6).
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

    pipeline = Pipeline(
        [
            transport.input(),
            stt,
            user_aggregator,
            llm,
            tts,
            transport.output(),
            assistant_aggregator,
        ]
    )
    return BuiltPipeline(
        pipeline=pipeline,
        context=context,
        user_aggregator=user_aggregator,
        assistant_aggregator=assistant_aggregator,
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
