"""Wave-0 tests for scripts/refresh_knowledge.py (07-04, D-01/D-02/D-07,
Amendment 3.D/3.E/5).

These tests exercise ONLY the refresh module's own pure helpers -- manifest
reading/gating, public-source refusal, skip-on-missing-source, chunk-file
writing, and advisory-flag-not-block wiring. `scrub.lint()`/`chunk_text()`
equivalents are NOT retested here: `klanker_voice.knowledge.lint.advisory_lint`
(Plan 01) and `klanker_voice.knowledge.retrieval.chunk_text` (Plan 02) are
already unit-tested elsewhere and are simply imported + reused. No Anthropic
call, no doc-gen skill invocation anywhere in this file (keyless, offline).
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "scripts"))

import refresh_knowledge as rk  # noqa: E402  (sys.path shim above)

from klanker_voice.knowledge.lint import advisory_lint  # noqa: E402  (Plan 01, already GREEN)
from klanker_voice.knowledge.retrieval import chunk_text  # noqa: E402  (Plan 02, already GREEN)


def test_plan01_lint_and_plan02_chunk_text_already_resolve():
    """Sanity: the reused Plan-01/02 functions import cleanly and are callable
    -- proves this plan does NOT reimplement them."""
    assert callable(advisory_lint)
    assert callable(chunk_text)


# --------------------------------------------------------------------------
# D-01: manifest is the ONLY source of truth -- a path not listed is never
# opened.
# --------------------------------------------------------------------------


def _write_manifest(tmp_path: Path, topics_yaml: str) -> Path:
    manifest_path = tmp_path / "manifest.yaml"
    manifest_path.write_text(
        "version: 1\ntour_priority: []\ntopics:\n" + topics_yaml, encoding="utf-8"
    )
    return manifest_path


def test_read_manifest_only_includes_public_sources(tmp_path):
    manifest_path = _write_manifest(
        tmp_path,
        """\
  - id: demo
    spoken_name: "demo"
    pack: demo.md
    sources:
      - path: a.md
        kind: docs
        public: true
""",
    )
    topics, refused = rk.read_manifest(manifest_path)
    assert [t.id for t in topics] == ["demo"]
    assert len(topics[0].sources) == 1
    assert topics[0].sources[0].path == Path("a.md")
    assert refused == []


def test_manifest_public_false_is_refused(tmp_path):
    manifest_path = _write_manifest(
        tmp_path,
        """\
  - id: demo
    spoken_name: "demo"
    pack: demo.md
    sources:
      - path: a.md
        kind: docs
        public: false
""",
    )
    topics, refused = rk.read_manifest(manifest_path)
    assert topics[0].sources == []
    assert len(refused) == 1
    assert refused[0].topic_id == "demo"
    assert refused[0].path == "a.md"


def test_manifest_missing_public_flag_is_refused(tmp_path):
    manifest_path = _write_manifest(
        tmp_path,
        """\
  - id: demo
    spoken_name: "demo"
    pack: demo.md
    sources:
      - path: a.md
        kind: docs
""",
    )
    topics, refused = rk.read_manifest(manifest_path)
    assert topics[0].sources == []
    assert len(refused) == 1


def test_collect_source_text_never_opens_a_path_outside_the_manifest_source(tmp_path):
    """D-01: the refresh planner reads only manifest entries -- a repo path
    NOT in the manifest is never opened."""
    corpus_dir = tmp_path / "corpus"
    included_dir = corpus_dir / "included"
    included_dir.mkdir(parents=True)
    (included_dir / "in-scope.md").write_text("in-scope content", encoding="utf-8")

    # A sibling directory NOT referenced by any manifest source.
    excluded_dir = corpus_dir / "excluded"
    excluded_dir.mkdir(parents=True)
    (excluded_dir / "out-of-scope.md").write_text("out-of-scope content", encoding="utf-8")

    source = rk.Source(path=included_dir, kind="docs", public=True, skip_if_missing=False)

    opened: list[Path] = []

    def spy_reader(path: Path) -> str:
        opened.append(path)
        return path.read_text(encoding="utf-8")

    results = rk.collect_source_text(source, reader=spy_reader)

    assert len(results) == 1
    text, label = results[0]
    assert text == "in-scope content"
    assert "included" in label
    assert all("excluded" not in str(p) for p in opened)
    assert all("out-of-scope" not in str(p) for p in opened)


# --------------------------------------------------------------------------
# Environment Availability fallback: a missing local checkout is skipped
# with a warning, the run continues (never raises).
# --------------------------------------------------------------------------


def test_resolve_topic_sources_skips_missing_path_with_warning(tmp_path):
    existing = tmp_path / "exists.md"
    existing.write_text("hello", encoding="utf-8")
    missing = tmp_path / "does-not-exist"

    topic = rk.Topic(
        id="demo",
        spoken_name="demo",
        pack="demo.md",
        sources=[
            rk.Source(path=existing, kind="docs", public=True, skip_if_missing=False),
            rk.Source(path=missing, kind="code", public=True, skip_if_missing=True),
        ],
    )
    report = rk.RefreshReport()

    resolved = rk.resolve_topic_sources(topic, report)

    assert resolved == [topic.sources[0]]
    assert any("does-not-exist" in w for w in report.warnings)
    # Never raises -- the run continues past a missing source.


def test_resolve_topic_sources_all_missing_returns_empty_not_an_exception(tmp_path):
    missing = tmp_path / "nope"
    topic = rk.Topic(
        id="demo",
        spoken_name="demo",
        pack="demo.md",
        sources=[rk.Source(path=missing, kind="docs", public=True, skip_if_missing=True)],
    )
    report = rk.RefreshReport()

    resolved = rk.resolve_topic_sources(topic, report)

    assert resolved == []
    assert report.warnings  # a warning was recorded, no exception raised


# --------------------------------------------------------------------------
# Chunk-writer: per-topic knowledge/index/{topic}/*.jsonl files whose lines
# each parse as JSON with text/source_path/heading keys (Plan-02 chunk_text).
# --------------------------------------------------------------------------


def test_build_topic_chunks_uses_plan02_chunk_text(tmp_path):
    src_file = tmp_path / "doc.md"
    src_file.write_text("# Heading One\n\nSome body text about klanker.\n", encoding="utf-8")
    source = rk.Source(path=src_file, kind="docs", public=True, skip_if_missing=False)
    topic = rk.Topic(id="demo", spoken_name="demo", pack="demo.md", sources=[source])

    chunks = rk.build_topic_chunks(topic, [source])

    assert len(chunks) >= 1
    assert chunks[0].text.strip()
    assert chunks[0].source_path == str(src_file)
    assert chunks[0].heading == "Heading One"


def test_write_chunk_file_produces_parseable_jsonl_with_required_keys(tmp_path):
    index_dir = tmp_path / "index"
    chunks = [chunk_text("# H\n\nbody text", source_path="demo/doc.md")[0]]

    result = rk.write_chunk_file(index_dir, "demo", chunks)

    assert result.written_path is not None
    assert result.written_path.is_file()
    lines = result.written_path.read_text(encoding="utf-8").splitlines()
    assert len(lines) == 1
    obj = json.loads(lines[0])
    assert set(["text", "source_path", "heading"]).issubset(obj.keys())
    assert obj["source_path"] == "demo/doc.md"


def test_write_chunk_file_with_no_chunks_leaves_existing_index_untouched(tmp_path):
    index_dir = tmp_path / "index"
    topic_dir = index_dir / "demo"
    topic_dir.mkdir(parents=True)
    existing_path = topic_dir / "docs.jsonl"
    existing_path.write_text('{"text": "old", "source_path": "old.md", "heading": null}\n', encoding="utf-8")

    result = rk.write_chunk_file(index_dir, "demo", [])

    assert result.written_path is None
    assert existing_path.read_text(encoding="utf-8") == (
        '{"text": "old", "source_path": "old.md", "heading": null}\n'
    )


def test_chunk_writer_output_loads_through_plan02_retrieval_index(tmp_path):
    """The chunk-writer output loads through Plan-02 retrieval: a dry-run temp
    chunk dir builds a RetrievalIndex that returns results for a known term."""
    from klanker_voice.knowledge.retrieval import RetrievalIndex

    index_dir = tmp_path / "index"
    doc = tmp_path / "doc.md"
    doc.write_text(
        "# Freeze Quarantine\n\nThe sandbox enters action_frozen when onBreach fires.\n",
        encoding="utf-8",
    )
    source = rk.Source(path=doc, kind="docs", public=True, skip_if_missing=False)
    topic = rk.Topic(id="demo-topic", spoken_name="demo", pack="demo.md", sources=[source])
    chunks = rk.build_topic_chunks(topic, [source])
    result = rk.write_chunk_file(index_dir, topic.id, chunks)
    assert result.written_path is not None

    class _FakeKnowledgeConfig:
        pass

    cfg = _FakeKnowledgeConfig()
    cfg.index_dir = index_dir
    index = RetrievalIndex(cfg)

    found = index.query(topic.id, "what happens on action frozen onBreach", top_k=4, max_tokens=500)
    assert found
    assert any("action_frozen" in c.text for c in found)


# --------------------------------------------------------------------------
# Amendment 3.E: the advisory do-not-say lint FLAGS but never blocks/refuses
# writing.
# --------------------------------------------------------------------------


def test_advisory_flag_never_blocks_the_write(tmp_path):
    """A generated output whose text makes the imported scrub.lint() (real
    name: advisory_lint) return findings is STILL WRITTEN, and the findings
    are surfaced in the refresh report -- flags, never blocks (Amendment
    3.E). Built at runtime via concatenation so the landmine shape never
    appears as a literal token in this test's own source (no collision with
    a later negative-grep gate)."""
    digits = "".join(str(d) for d in [4, 8, 1, 7, 2, 3, 4, 6, 7, 5, 6, 1])
    landmine_text = "Internal note: account id " + digits + " appears here.\n"

    out_path = tmp_path / "generated-pack.md"
    findings = rk.flag_landmines("demo/pack", landmine_text)

    rk.write_pack(tmp_path, "generated-pack.md", landmine_text, packs_dir=".")

    assert out_path.is_file()
    assert out_path.read_text(encoding="utf-8") == landmine_text
    assert len(findings) >= 1
    assert findings[0].pattern == "aws_account_id"


def test_flag_landmines_on_clean_text_returns_no_findings():
    findings = rk.flag_landmines("demo/pack", "Nothing sensitive here, just facts about klanker.")
    assert findings == []


# --------------------------------------------------------------------------
# The swappable doc-generation seam (Amendment 3.D/5): defaults to a no-op
# (direct code indexing, grill-with-docs DROPPED per Amendment 5), but stays
# swappable via an injectable generator callable.
# --------------------------------------------------------------------------


def test_generate_docs_default_generator_is_a_noop_amendment_5():
    source = rk.Source(path=Path("/tmp/some-code"), kind="code", public=True, skip_if_missing=True)
    assert rk.generate_docs(source) is None


def test_generate_docs_swappable_seam_accepts_a_custom_generator():
    source = rk.Source(path=Path("/tmp/some-code"), kind="code", public=True, skip_if_missing=True)
    result = rk.generate_docs(source, generator=lambda s: f"generated docs for {s.path}")
    assert result == "generated docs for /tmp/some-code"


def test_generate_docs_never_raises_even_if_generator_fails():
    source = rk.Source(path=Path("/tmp/some-code"), kind="code", public=True, skip_if_missing=True)

    def _boom(_source):
        raise RuntimeError("skill not installed")

    assert rk.generate_docs(source, generator=_boom) is None


def test_build_topic_chunks_for_code_source_falls_back_to_raw_code_when_no_generator(tmp_path):
    """Amendment 5: a code source with the default (no-op) generator indexes
    the raw code directly -- never crashes, never requires grill-with-docs."""
    code_file = tmp_path / "main.go"
    code_file.write_text("# Package Main\n\npackage main\nfunc main() {}\n", encoding="utf-8")
    source = rk.Source(path=code_file, kind="code", public=True, skip_if_missing=True)
    topic = rk.Topic(id="demo-code", spoken_name="demo", pack="demo.md", sources=[source])

    chunks = rk.build_topic_chunks(topic, [source])

    assert len(chunks) >= 1
    assert any("package main" in c.text for c in chunks)


# --------------------------------------------------------------------------
# CLI shape (argument parsing only -- no network/API calls).
# --------------------------------------------------------------------------


def test_parse_args_defaults_and_dry_run_flag():
    args = rk.parse_args(["--dry-run"])
    assert args.dry_run is True
    assert args.manifest == rk.DEFAULT_MANIFEST_PATH


def test_parse_args_skip_distill_flag():
    args = rk.parse_args(["--skip-distill"])
    assert args.skip_distill is True
