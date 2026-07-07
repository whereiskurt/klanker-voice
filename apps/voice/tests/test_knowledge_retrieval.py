"""Unit tests for klanker_voice.knowledge.retrieval (Phase 7 Amendment 3-A/B/C/G,
PIPE-07: local, keyless SQLite FTS5/BM25 retrieval -- no embeddings, no 4th vendor).

Task 1 (07-02-PLAN.md): chunking + FTS5 index build/query + km walking-slice
corpus. Task 2 extends this same file with injection (pack.build_system_blocks)
and deep-turn-gating (router.KnowledgeRouterProcessor) behaviors -- both
keyless, no Anthropic call needed for those either.
"""

from __future__ import annotations

import time
from pathlib import Path

import pytest

from klanker_voice.config import APP_ROOT, load_config, load_knowledge_config
from klanker_voice.knowledge.retrieval import (
    Chunk,
    RetrievalIndex,
    build_topic_index,
    chunk_text,
    fts5_available,
)

REAL_PIPELINE_TOML = APP_ROOT / "pipeline.toml"

pytestmark = pytest.mark.skipif(
    not fts5_available(), reason="local sqlite3 build lacks the FTS5 extension"
)


# ---------------------------------------------------------------------------
# fts5_available() guard
# ---------------------------------------------------------------------------


def test_fts5_available_on_this_box():
    assert fts5_available() is True


# ---------------------------------------------------------------------------
# chunk_text -- heading-aware chunking
# ---------------------------------------------------------------------------


LONG_MARKDOWN = (
    "# Title\n\nIntro paragraph before any subsection.\n\n"
    "## Section A\n\n" + ("word " * 400) + "\n\n"
    "## Section B\n\nA short section.\n"
)


def test_chunk_text_returns_multiple_chunks_within_max_size_with_metadata():
    chunks = chunk_text(LONG_MARKDOWN, source_path="doc.md", max_chars=300, overlap=50)

    assert len(chunks) > 1
    for c in chunks:
        assert len(c.text) <= 300
        assert c.source_path == "doc.md"

    # Section A's long body produced heading-tagged chunks; Section B's short
    # body produced its own chunk -- heading boundaries are respected.
    headings = {c.heading for c in chunks}
    assert "Section A" in headings
    assert "Section B" in headings
    assert any(c.heading == "Section B" and "A short section." in c.text for c in chunks)


def test_chunk_text_content_before_first_heading_has_no_heading():
    chunks = chunk_text(
        "Leading text with no heading yet.\n\n# First Heading\n\nBody.\n",
        source_path="doc.md",
        max_chars=900,
        overlap=100,
    )
    assert chunks[0].heading is None
    assert "Leading text" in chunks[0].text
    assert chunks[1].heading == "First Heading"


# ---------------------------------------------------------------------------
# build_topic_index -- FTS5 + BM25 ranking
# ---------------------------------------------------------------------------


def test_build_topic_index_bm25_ranks_the_matching_chunk_first():
    chunks = [
        Chunk(text="This chunk talks about apples and oranges.", source_path="a.md", heading="A"),
        Chunk(text="This chunk is about the unique term zarquon only.", source_path="b.md", heading="B"),
        Chunk(text="Generic filler text with nothing distinctive here.", source_path="c.md", heading="C"),
    ]
    conn = build_topic_index(chunks)
    rows = conn.execute(
        "SELECT text FROM chunks WHERE chunks MATCH ? ORDER BY bm25(chunks) LIMIT 5",
        ('"zarquon"',),
    ).fetchall()
    assert rows[0][0] == chunks[1].text


# ---------------------------------------------------------------------------
# RetrievalIndex.query -- top-k / budget cap / graceful degrade
# ---------------------------------------------------------------------------


@pytest.fixture
def tiny_index(tmp_path: Path):
    """A minimal on-disk knowledge/index/{topic}/*.jsonl tree + a stub
    KnowledgeConfig-shaped object with just the ``index_dir`` attribute
    RetrievalIndex reads."""
    import json
    from dataclasses import dataclass

    topic_dir = tmp_path / "index" / "widgets"
    topic_dir.mkdir(parents=True)
    records = [
        {"text": "Widgets are small mechanical devices.", "source_path": "w1.md", "heading": "Intro"},
        {"text": "The flux capacitor requires 1.21 jigawatts.", "source_path": "w2.md", "heading": "Power"},
        {"text": "Widgets come in red, blue, and green.", "source_path": "w1.md", "heading": "Colors"},
    ]
    with (topic_dir / "docs.jsonl").open("w", encoding="utf-8") as f:
        for r in records:
            f.write(json.dumps(r) + "\n")

    @dataclass
    class _StubKnowledgeConfig:
        index_dir: Path

    return RetrievalIndex(_StubKnowledgeConfig(index_dir=tmp_path / "index"))


def test_retrieval_index_query_returns_at_most_top_k_and_within_budget(tiny_index):
    results = tiny_index.query("widgets", "tell me about widgets", top_k=2, max_tokens=1500)
    assert 0 < len(results) <= 2
    assert sum(len(c.text.split()) for c in results) <= 1500


def test_retrieval_index_query_no_built_index_returns_empty_list(tiny_index):
    assert tiny_index.query("no-such-topic", "anything at all") == []


def test_retrieval_index_query_matches_distinctive_term(tiny_index):
    results = tiny_index.query("widgets", "what about the flux capacitor jigawatts", top_k=4, max_tokens=1500)
    assert any("jigawatts" in c.text for c in results)


# ---------------------------------------------------------------------------
# Real km walking-slice corpus -- proves retrieval adds DEPTH the curated
# pack lacks (Amendment 3, ROADMAP criterion 2)
# ---------------------------------------------------------------------------


def _real_index() -> RetrievalIndex:
    cfg = load_knowledge_config(REAL_PIPELINE_TOML)
    return RetrievalIndex(cfg)


def test_km_index_surfaces_long_tail_detail_absent_from_curated_pack():
    """action-quotas / freeze-quarantine is real km depth (Phase 121) that
    exists in the raw docs but is NOT distilled into the curated
    knowledge/topics/klanker-maker.md pack -- proving retrieval adds depth."""
    knowledge_cfg = load_knowledge_config(REAL_PIPELINE_TOML)
    index = RetrievalIndex(knowledge_cfg)

    results = index.query(
        "klanker-maker",
        "what happens when a sandbox gets frozen by action quotas onBreach",
        top_k=4,
        max_tokens=1500,
    )
    assert results, "expected the km index to return real chunks for a freeze/action-quota question"
    assert any("action_frozen" in c.text or "freeze" in c.text.lower() for c in results)

    curated_pack_text = knowledge_cfg.packs_dir.joinpath("klanker-maker.md").read_text(
        encoding="utf-8"
    )
    assert "action_frozen" not in curated_pack_text
    assert "onBreach" not in curated_pack_text


def test_km_index_bm25_query_completes_well_under_100ms():
    index = _real_index()
    # First call builds the index (excluded from the timing budget -- session
    # start, not per-turn); the timed call is a warm, already-built query.
    index.query("klanker-maker", "warm up the index", top_k=4, max_tokens=1500)

    start = time.perf_counter()
    index.query("klanker-maker", "how are dollar budgets enforced", top_k=4, max_tokens=1500)
    elapsed = time.perf_counter() - start

    assert elapsed < 0.1


def test_km_index_query_for_missing_topic_returns_empty_list():
    index = _real_index()
    assert index.query("no-such-topic", "anything") == []


def test_km_index_query_respects_top_k(monkeypatch):
    index = _real_index()
    results = index.query("klanker-maker", "how are dollar budgets enforced", top_k=4, max_tokens=1500)
    assert 0 < len(results) <= 4


def test_km_chunk_file_exists_and_is_nonempty():
    cfg = load_config(REAL_PIPELINE_TOML)
    assert cfg is not None  # sanity: real config still loads
    chunk_file = APP_ROOT / "knowledge" / "index" / "klanker-maker" / "docs.jsonl"
    assert chunk_file.is_file()
    assert chunk_file.stat().st_size > 0


# ---------------------------------------------------------------------------
# Task 2: injection (prompt_assembly.build_system_blocks) + deep-turn gating
# (router.KnowledgeRouterProcessor) + end-to-end graceful degrade. Keyless --
# fixture chunks and a tiny on-disk index, no Anthropic call.
# ---------------------------------------------------------------------------


def test_build_system_blocks_injects_retrieved_chunks_into_uncached_post_breakpoint_block():
    from klanker_voice.knowledge.prompt_assembly import build_system_blocks

    cfg = load_config(REAL_PIPELINE_TOML)
    knowledge_cfg = load_knowledge_config(REAL_PIPELINE_TOML)

    base = build_system_blocks(cfg, knowledge_cfg, "klanker-maker")
    with_chunks = build_system_blocks(
        cfg,
        knowledge_cfg,
        "klanker-maker",
        retrieved_chunks=[Chunk(text="DETAIL-XYZ", source_path="p", heading="h")],
    )

    # system[0] (the cached stable prefix) is byte-identical -- the cache
    # prefix is never invalidated by retrieval (Amendment 3-C, Pitfall 3).
    assert base[0]["text"] == with_chunks[0]["text"]
    assert base[1]["text"] == with_chunks[1]["text"]  # curated pack untouched too
    assert len(with_chunks) == 3
    assert "DETAIL-XYZ" in with_chunks[2]["text"]
    assert "cache_control" not in with_chunks[2]


def test_build_system_blocks_empty_or_none_chunks_unchanged_from_plan01_shape():
    from klanker_voice.knowledge.prompt_assembly import build_system_blocks

    cfg = load_config(REAL_PIPELINE_TOML)
    knowledge_cfg = load_knowledge_config(REAL_PIPELINE_TOML)

    base = build_system_blocks(cfg, knowledge_cfg, "klanker-maker")
    assert build_system_blocks(cfg, knowledge_cfg, "klanker-maker", retrieved_chunks=[]) == base
    assert build_system_blocks(cfg, knowledge_cfg, "klanker-maker", retrieved_chunks=None) == base
    assert len(base) == 2


# --- router deep-turn gating -------------------------------------------------

_ROUTER_TOPIC_MAP = {
    "version": 1,
    "confidence_floor": 2,
    "topics": [
        {
            "id": "klanker-maker",
            "spoken_name": "klanker-maker",
            "hook": "Kurt's AI-agent sandbox runtime.",
            "keywords": [{"term": "klanker maker", "weight": 3}, {"term": "klanker", "weight": 2}],
        },
        {
            "id": "defcon-run-34",
            "spoken_name": "DEFCON dot run, thirty-four",
            "hook": "Kurt's DEFCON running community.",
            "keywords": [{"term": "defcon run", "weight": 3}, {"term": "defcon", "weight": 2}],
        },
    ],
}


def _router_pipeline_cfg(tmp_path: Path):
    from klanker_voice.config import (
        FluxConfig,
        LlmConfig,
        PersonaConfig,
        PipelineConfig,
        SttConfig,
        TtsConfig,
        TurnConfig,
    )

    persona = tmp_path / "persona.md"
    persona.write_text("# stub persona\nYou are K.\n", encoding="utf-8")
    return PipelineConfig(
        stt=SttConfig(
            provider="deepgram-nova3",
            model="nova-3-general",
            flux=FluxConfig(eot_threshold=0.7, eager_eot_threshold=0.0),
        ),
        turn=TurnConfig(strategy="smart_turn_v3", vad_stop_secs=0.2, user_speech_timeout=0.6),
        llm=LlmConfig(provider="anthropic", model="claude-haiku-4-5"),
        tts=TtsConfig(provider="elevenlabs", model="eleven_flash_v2_5", voice_id="", speed=1.1),
        persona=PersonaConfig(prompt_path=persona),
    )


def _router_knowledge_cfg(tmp_path: Path, *, retrieval_enabled: bool = True):
    import yaml

    from klanker_voice.config import KnowledgeConfig

    (tmp_path / "topics").mkdir(exist_ok=True)
    (tmp_path / "topics" / "klanker-maker.md").write_text("km deep pack.\n", encoding="utf-8")
    (tmp_path / "topics" / "defcon-run-34.md").write_text("defcon deep pack.\n", encoding="utf-8")
    (tmp_path / "style.md").write_text("Dry, punchy style.\n", encoding="utf-8")
    manifest = tmp_path / "manifest.yaml"
    manifest.write_text(
        yaml.safe_dump(
            {
                "version": 1,
                "tour_priority": ["klanker-maker", "defcon-run-34"],
                "topics": [
                    {"id": "klanker-maker", "spoken_name": "klanker-maker", "pack": "klanker-maker.md"},
                    {
                        "id": "defcon-run-34",
                        "spoken_name": "DEFCON dot run, thirty-four",
                        "pack": "defcon-run-34.md",
                    },
                ],
            }
        ),
        encoding="utf-8",
    )
    topic_map_path = tmp_path / "topic-map.yaml"
    topic_map_path.write_text(yaml.safe_dump(_ROUTER_TOPIC_MAP), encoding="utf-8")
    return KnowledgeConfig(
        manifest_path=manifest,
        topic_map_path=topic_map_path,
        packs_dir=tmp_path / "topics",
        style_path=tmp_path / "style.md",
        cache_floor=10,
        index_dir=tmp_path / "index",
        retrieval_enabled=retrieval_enabled,
        retrieval_top_k=4,
        retrieval_budget=1500,
    )


class _FakeLLM:
    """Stand-in for AnthropicLLMService -- mirrors test_knowledge_router.py's
    fixture (only ``_settings.system_instruction`` needs to exist)."""

    class _Settings:
        system_instruction = None

    def __init__(self):
        self._settings = self._Settings()


async def _never_fallback(utterance, topics, *, model):
    return None


def _write_topic_index(index_dir: Path, topic_id: str, records: list[dict]) -> None:
    import json

    topic_dir = index_dir / topic_id
    topic_dir.mkdir(parents=True, exist_ok=True)
    with (topic_dir / "docs.jsonl").open("w", encoding="utf-8") as f:
        for r in records:
            f.write(json.dumps(r) + "\n")


async def test_deep_turn_calls_retrieval_index_for_the_new_topic_and_injects_chunks(tmp_path):
    from pipecat.frames.frames import TranscriptionFrame

    from klanker_voice.knowledge.router import KnowledgeRouterProcessor

    knowledge_cfg = _router_knowledge_cfg(tmp_path)
    _write_topic_index(
        knowledge_cfg.index_dir,
        "defcon-run-34",
        [{"text": "DEFCON RUN DETAIL: aid stations every 5k.", "source_path": "s.md", "heading": "h"}],
    )
    cfg = _router_pipeline_cfg(tmp_path)
    llm = _FakeLLM()
    retrieval_index = RetrievalIndex(knowledge_cfg)

    router = KnowledgeRouterProcessor(
        cfg=cfg,
        knowledge_cfg=knowledge_cfg,
        llm=llm,
        initial_topic="klanker-maker",
        fallback_classify=_never_fallback,
        retrieval_index=retrieval_index,
    )

    from pipecat.tests.utils import run_test

    frame = TranscriptionFrame(
        text="let's talk about the defcon run community",
        user_id="u1",
        timestamp="2026-07-07T00:00:00Z",
    )
    await run_test(router, frames_to_send=[frame])

    blocks = llm._settings.system_instruction
    assert len(blocks) == 3
    assert "DEFCON RUN DETAIL" in blocks[2]["text"]
    assert "cache_control" not in blocks[2]


async def test_shallow_same_topic_followup_never_queries_retrieval_or_acks(tmp_path):
    from pipecat.frames.frames import TranscriptionFrame, TTSSpeakFrame
    from pipecat.tests.utils import run_test

    from klanker_voice.knowledge.router import KnowledgeRouterProcessor

    knowledge_cfg = _router_knowledge_cfg(tmp_path)
    _write_topic_index(
        knowledge_cfg.index_dir,
        "klanker-maker",
        [{"text": "should never be queried on a same-topic follow-up", "source_path": "s.md", "heading": None}],
    )
    cfg = _router_pipeline_cfg(tmp_path)
    llm = _FakeLLM()

    calls: list[tuple] = []

    class _RecordingIndex(RetrievalIndex):
        def query(self, topic_id, utterance, *, top_k, max_tokens):  # noqa: D401
            calls.append((topic_id, utterance))
            return super().query(topic_id, utterance, top_k=top_k, max_tokens=max_tokens)

    router = KnowledgeRouterProcessor(
        cfg=cfg,
        knowledge_cfg=knowledge_cfg,
        llm=llm,
        initial_topic="klanker-maker",
        fallback_classify=_never_fallback,
        retrieval_index=_RecordingIndex(knowledge_cfg),
    )

    frame = TranscriptionFrame(
        text="tell me more about klanker maker", user_id="u1", timestamp="2026-07-07T00:00:00Z"
    )
    down, _ = await run_test(router, frames_to_send=[frame])

    assert not any(isinstance(f, TTSSpeakFrame) for f in down)  # Pitfall 2: no ack
    assert calls == []  # and therefore no retrieval query either


async def test_retrieval_disabled_never_queries_even_with_index_supplied(tmp_path):
    from pipecat.frames.frames import TranscriptionFrame
    from pipecat.tests.utils import run_test

    from klanker_voice.knowledge.router import KnowledgeRouterProcessor

    knowledge_cfg = _router_knowledge_cfg(tmp_path, retrieval_enabled=False)
    _write_topic_index(
        knowledge_cfg.index_dir,
        "defcon-run-34",
        [{"text": "DEFCON RUN DETAIL: aid stations every 5k.", "source_path": "s.md", "heading": None}],
    )
    cfg = _router_pipeline_cfg(tmp_path)
    llm = _FakeLLM()

    calls: list[tuple] = []

    class _RecordingIndex(RetrievalIndex):
        def query(self, topic_id, utterance, *, top_k, max_tokens):  # noqa: D401
            calls.append((topic_id, utterance))
            return super().query(topic_id, utterance, top_k=top_k, max_tokens=max_tokens)

    router = KnowledgeRouterProcessor(
        cfg=cfg,
        knowledge_cfg=knowledge_cfg,
        llm=llm,
        initial_topic="klanker-maker",
        fallback_classify=_never_fallback,
        retrieval_index=_RecordingIndex(knowledge_cfg),
    )

    frame = TranscriptionFrame(
        text="let's talk about the defcon run community",
        user_id="u1",
        timestamp="2026-07-07T00:00:00Z",
    )
    await run_test(router, frames_to_send=[frame])

    assert calls == []  # retrieval_enabled=False -> never queried
    assert len(llm._settings.system_instruction) == 2  # Plan-01 shape, unchanged


async def test_deep_turn_for_topic_with_no_built_index_degrades_to_curated_pack_only(tmp_path):
    """End-to-end graceful degrade: a genuine topic switch to a topic with NO
    knowledge/index/{topic}/*.jsonl chunk files still produces a valid
    two-block prompt (curated pack only) -- retrieval is additive, never a
    hard dependency."""
    from pipecat.frames.frames import TranscriptionFrame, TTSSpeakFrame
    from pipecat.tests.utils import run_test

    from klanker_voice.knowledge.router import KnowledgeRouterProcessor

    knowledge_cfg = _router_knowledge_cfg(tmp_path)
    # Deliberately do NOT write any chunk file for defcon-run-34.
    cfg = _router_pipeline_cfg(tmp_path)
    llm = _FakeLLM()
    retrieval_index = RetrievalIndex(knowledge_cfg)

    router = KnowledgeRouterProcessor(
        cfg=cfg,
        knowledge_cfg=knowledge_cfg,
        llm=llm,
        initial_topic="klanker-maker",
        fallback_classify=_never_fallback,
        retrieval_index=retrieval_index,
    )

    frame = TranscriptionFrame(
        text="let's talk about the defcon run community",
        user_id="u1",
        timestamp="2026-07-07T00:00:00Z",
    )
    down, _ = await run_test(router, frames_to_send=[frame])

    assert any(isinstance(f, TTSSpeakFrame) for f in down)  # ack still fires on the switch
    blocks = llm._settings.system_instruction
    assert len(blocks) == 2  # no third block -- graceful degrade, curated pack only
    assert blocks[1]["text"] == "defcon deep pack.\n"
