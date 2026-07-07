"""build_pipeline(cfg, transport) + greet-first wiring (RESEARCH Patterns 1 & 6).

Canonical 1.5.0 shape: VAD lives on the user aggregator (not the transport),
services are built via Settings objects, and barge-in context truncation is
built into the frame path — no custom truncation bookkeeping here (Pattern 5).
"""

from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass

from pipecat.frames.frames import LLMRunFrame, TTSSpeakFrame
from pipecat.pipeline.pipeline import Pipeline
from pipecat.pipeline.worker import PipelineParams, PipelineWorker
from pipecat.processors.aggregators.llm_context import LLMContext
from pipecat.processors.aggregators.llm_response_universal import LLMContextAggregatorPair
from pipecat.processors.frameworks.rtvi import RTVIProcessor
from pipecat.transports.base_transport import BaseTransport

from klanker_voice.config import KnowledgeConfig, PipelineConfig, load_knowledge_config
from klanker_voice.factories import (
    build_llm,
    build_stt,
    build_tts,
    build_user_aggregator_params,
)
from klanker_voice.knowledge.prompt_assembly import (
    apply_system_blocks,
    build_system_blocks,
    load_manifest,
)
from klanker_voice.knowledge.retrieval import RetrievalIndex
from klanker_voice.knowledge.router import KnowledgeRouterProcessor


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
    cfg: PipelineConfig,
    transport: BaseTransport,
    *,
    rtvi: RTVIProcessor | None = None,
    knowledge_cfg: KnowledgeConfig | None = None,
    remaining_seconds_fn: Callable[[], float | None] | None = None,
) -> BuiltPipeline:
    """Assemble the canonical cascade pipeline from config.

    ``rtvi`` (05-01, CLNT-03/04/06): when provided, the RTVIProcessor is
    placed immediately after ``transport.input()`` — the standard RTVI
    placement so it observes every upstream/downstream frame in the cascade.
    Optional so pipelines built without a live browser client (harness,
    console) are unaffected.

    Phase 7 (D-13, Amendment 1): the persona system message is replaced by
    the two-block cached system prompt (``build_system_blocks`` — persona +
    Kurt STYLE layer + topic-map hooks in the cached block0, the manifest's
    lead ``tour_priority`` topic's deep pack in block1). ``LLMContext`` no
    longer carries an initial system message at all — its content now lives
    on the LLM service's ``Settings.system_instruction`` (see
    ``knowledge.prompt_assembly.apply_system_blocks`` for why: pipecat's own
    ``LLMContext`` system-message path can't carry ``cache_control``
    block-level markers through to the Anthropic API). The greet-first kick
    (``GREET_KICK_MESSAGE``, a "developer"-role message) becomes the
    context's first message instead — unaffected, since
    ``AnthropicLLMAdapter._extract_initial_system`` only ever inspects a
    "system"-role ``messages[0]``.

    ``knowledge_cfg`` defaults to ``load_knowledge_config()`` (the checked-in
    ``pipeline.toml``'s ``[knowledge]`` table) when not supplied, mirroring
    how ``load_quota_config()`` is called independently elsewhere
    (server.py/bot.py/console.py) — those callers need no change.

    ``KnowledgeRouterProcessor`` (Amendment 1, RESEARCH Pattern 2) is placed
    between ``stt`` and ``user_aggregator`` — the RESEARCH-stated insertion
    point, before the transcription reaches ``LLMContextAggregatorPair``. It
    classifies each finalized transcription, swaps block1 on a genuine topic
    switch, and fires the "let's dig into it" ack — never touching block0.

    07-02 (Amendment 3-B/G): one ``RetrievalIndex`` is constructed here, once
    per session, and reused across every turn (never rebuilt per turn — the
    per-topic FTS5 connection it lazily builds on first query stays cached
    for the life of the session). It's handed to ``KnowledgeRouterProcessor``,
    which queries it on the same deep-turn condition that fires the ack, so
    the local BM25 query's cost is ack-masked.
    """
    stt = build_stt(cfg)
    llm = build_llm(cfg)
    tts = build_tts(cfg)

    knowledge_cfg = knowledge_cfg or load_knowledge_config()
    manifest = load_manifest(knowledge_cfg)
    initial_topic = manifest["tour_priority"][0]
    apply_system_blocks(llm, build_system_blocks(cfg, knowledge_cfg, initial_topic))

    context = LLMContext()
    if not cfg.persona.greet_first:
        # 07.1: the client already played a spoken greeting on tap — tell the
        # LLM not to re-introduce itself (see NO_REGREET_KICK_MESSAGE).
        context.add_message(dict(NO_REGREET_KICK_MESSAGE))
    aggregator_pair = LLMContextAggregatorPair(
        context,
        user_params=build_user_aggregator_params(cfg),
    )
    user_aggregator, assistant_aggregator = aggregator_pair.user(), aggregator_pair.assistant()

    retrieval_index = RetrievalIndex(knowledge_cfg) if knowledge_cfg.retrieval_enabled else None

    router = KnowledgeRouterProcessor(
        cfg=cfg,
        knowledge_cfg=knowledge_cfg,
        llm=llm,
        initial_topic=initial_topic,
        retrieval_index=retrieval_index,
        remaining_seconds_fn=remaining_seconds_fn,
    )

    processors = [transport.input()]
    if rtvi is not None:
        processors.append(rtvi)
    processors.extend(
        [
            stt,
            router,
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

#: 07.1 double-greeting fix. When greet_first is false (prod slick-start: the
#: browser client plays a pre-recorded greeting clip on tap), the LLM tends to
#: re-introduce itself on its first turn anyway — the visitor hears "Hey, I'm
#: KPH…" twice. Seeding this developer message into the context makes the
#: no-second-greeting instruction immediate and turn-local, which the persona
#: system prompt alone did not reliably enforce.
NO_REGREET_KICK_MESSAGE = {
    "role": "developer",
    "content": (
        "The visitor has ALREADY heard your spoken greeting — a short pre-recorded "
        "clip played the instant they connected. Do NOT greet them, say hi, or "
        "introduce yourself again; a second greeting is a jarring echo. When they "
        "speak, answer their first message directly and get straight to the substance."
    ),
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
