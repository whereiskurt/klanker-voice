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
