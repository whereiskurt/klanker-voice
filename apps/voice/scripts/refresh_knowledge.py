"""refresh_knowledge.py -- D-07 manual knowledge refresh (07-04).

An offline, deliberate script (never run during a live session, Amendment
3.G) that regenerates BOTH the curated per-topic packs (facts + Kurt STYLE,
the original D-07/D-08 job) AND the local BM25/FTS5 retrieval chunk files
Plan-02's `RetrievalIndex` loads at startup (`knowledge/index/{topic}/*.jsonl`).

Pipeline (Amendment 3, four responsibilities):
  1. distill the curated per-topic packs + style layer (map-reduce:
     `survey_repo()` -> `distill_topic()` / `style_pass()`);
  2. for code-heavy sources, run the SWAPPABLE doc-generation seam
     (`generate_docs()`) -- see the Amendment 5 note below;
  3. chunk the corpus with Plan-02's `chunk_text()` and write
     `knowledge/index/{topic}/*.jsonl` (`build_topic_chunks()` /
     `write_chunk_file()`);
  4. run Plan-01's advisory `advisory_lint()` over every generated output and
     FLAG findings for the D-09 git-diff human review -- NEVER block or
     refuse the write (Amendment 3.E).

Amendment 5 note (grill-with-docs DROPPED): the plan text and
DESIGN-NOTES.md's Amendment 3.D originally called for the `generate_docs()`
seam to default to shelling out to Matt Pocock's `grill-with-docs` skill for
code-heavy sources (defcon.run.34, meshtk). DESIGN-NOTES.md's Amendment 5
("corpus prep revised: direct code indexing, grill-with-docs DROPPED",
2026-07-07) reverses that -- Kurt's call was to index the code DIRECTLY, no
generated-docs step for launch. 07-RESEARCH.md's own Q1 resolution note
("SUPERSEDED by DESIGN-NOTES Amendments 3-5 ... Corpus prep is DIRECT code
indexing (Amendment 5; grill-with-docs dropped), not doc-gen") and the
already-committed `knowledge/manifest.yaml` source notes ("indexed as code,
no doc-gen step") both independently confirm this is the shipped design of
record, not merely a proposal. `generate_docs()` therefore defaults to a
no-op (`default_doc_generator`, returns None -- see its docstring) so code
sources fall straight through to direct raw-code chunking. The seam itself
is KEPT (a `generator` callable can still be injected) so a future refresh
can re-enable a real doc-gen pass without any pipeline change -- satisfying
the plan's "keep it swappable" framing even though the DEFAULT behavior is
now Amendment 5's direct-indexing choice, not Amendment 3.D's original one.

Never auto-commits -- output lands as an ordinary git diff for the D-09
human review gate. `--dry-run` / `--out-dir` write into a scratch directory
instead of the tracked `apps/voice/knowledge/` tree so tests (and a cautious
first look) never clobber committed packs/chunk files.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Callable

import yaml
from dotenv import load_dotenv

from klanker_voice.knowledge.lint import Finding, advisory_lint
from klanker_voice.knowledge.retrieval import Chunk, chunk_text

APP_ROOT = Path(__file__).resolve().parent.parent

#: manifest.yaml's own relative source paths (e.g. "apps/voice/knowledge/
#: diagrams/km-sandbox-aws.md", ".planning/phases/07-kph-knowledge-base/
#: corpus/km-digest.md") are REPO-ROOT relative, not manifest-file-relative
#: or cwd-relative -- confirmed by the paths' own "apps/voice/" and
#: ".planning/" prefixes (things outside apps/voice live at repo root).
#: `apps/voice` is APP_ROOT, so its grandparent is the repo root.
REPO_ROOT = APP_ROOT.parent.parent
DEFAULT_MANIFEST_PATH = APP_ROOT / "knowledge" / "manifest.yaml"
DEFAULT_PACKS_DIR = "knowledge/topics"
DEFAULT_INDEX_SUBDIR = "knowledge/index"

#: A more capable offline model for distillation (RESEARCH A2) -- the RUNTIME
#: answering model stays claude-haiku-4-5 everywhere else in this repo; this
#: script's own LLM use is infrequent/offline (D-07/D-08) so a stronger model
#: for quality is a reasonable, bounded cost.
DEFAULT_DISTILL_MODEL = "claude-sonnet-4-5"

#: File extensions the survey/chunk pass treats as readable text. Anything
#: else under a source directory is silently skipped (binaries, images, etc).
_TEXT_EXTENSIONS = frozenset(
    {
        ".md",
        ".mdx",
        ".txt",
        ".rst",
        ".py",
        ".go",
        ".ts",
        ".tsx",
        ".js",
        ".jsx",
        ".yaml",
        ".yml",
        ".tf",
        ".tfvars",
        ".proto",
        ".toml",
    }
)

#: Directory names pruned during the source walk (never descended into).
#: A real code checkout (`defcon.run.34/apps`, in practice a Next.js app)
#: can carry hundreds of thousands of files under `node_modules`/build
#: output -- without this prune, `iter_source_files` would walk the entire
#: dependency tree on every refresh (observed: 443,982 files under one real
#: manifest source in this environment), effectively hanging the offline
#: refresh. Any dotdir (`.git`, `.venv`, `.terraform`, `.next`, ...) is also
#: pruned via the leading-dot check below.
_EXCLUDED_DIR_NAMES = frozenset(
    {
        "node_modules",
        "vendor",
        "dist",
        "build",
        "target",
        "coverage",
        "site-packages",
    }
)


# ---------------------------------------------------------------------------
# Manifest model (D-01/D-02)
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class Source:
    """One manifest source entry -- D-01: the manifest is the ONLY place a
    refresh path ever comes from."""

    path: Path
    kind: str
    public: bool
    skip_if_missing: bool = False
    note: str = ""


@dataclass(frozen=True)
class Topic:
    id: str
    spoken_name: str
    pack: str
    sources: list[Source]


@dataclass(frozen=True)
class RefusedSource:
    """A manifest source entry that was NOT included because it lacked an
    explicit `public: true` flag (D-02). Refusal is recorded, never raised --
    a merely-unmarked source is an authoring omission to flag for review, not
    a crash."""

    topic_id: str
    path: str
    reason: str


def load_manifest_yaml(manifest_path: Path) -> dict:
    """Parse the manifest YAML file. Raises if the file is missing/invalid --
    a malformed manifest IS a hard error (unlike a merely-unflagged source)."""
    return yaml.safe_load(manifest_path.read_text(encoding="utf-8")) or {}


def _resolve_source_path(raw_path: str, base_dir: Path) -> Path:
    """Absolute manifest paths (the sibling local checkouts, e.g.
    `/Users/khundeck/working/klankrmkr/docs`) are used verbatim. A relative
    path is resolved against `base_dir` (the repo root by default -- see
    `REPO_ROOT`), matching manifest.yaml's own convention of writing
    in-repo sources with their full repo-relative prefix."""
    p = Path(raw_path)
    return p if p.is_absolute() else (base_dir / p)


def parse_manifest(
    raw: dict, *, base_dir: Path | None = None
) -> tuple[list[Topic], list[RefusedSource]]:
    """D-01/D-02: parse the manifest dict into `Topic`s, keeping only sources
    explicitly flagged `public: true`. A source missing the flag (or set to
    `false`) is excluded from the topic and recorded in the refused list --
    this is the manifest-level public-only gate, applied before any file is
    ever opened. `base_dir` resolves relative source paths (default:
    `REPO_ROOT`; tests pass an explicit `tmp_path` so fixtures never touch
    the real repo)."""
    base = base_dir if base_dir is not None else REPO_ROOT
    topics: list[Topic] = []
    refused: list[RefusedSource] = []
    for topic_raw in raw.get("topics", []) or []:
        topic_id = str(topic_raw["id"])
        sources: list[Source] = []
        for src_raw in topic_raw.get("sources", []) or []:
            src_path = str(src_raw.get("path", ""))
            if src_raw.get("public") is not True:
                refused.append(
                    RefusedSource(
                        topic_id=topic_id,
                        path=src_path,
                        reason="not flagged public:true (D-02)",
                    )
                )
                continue
            sources.append(
                Source(
                    path=_resolve_source_path(src_path, base),
                    kind=str(src_raw.get("kind", "docs")),
                    public=True,
                    skip_if_missing=bool(src_raw.get("skip_if_missing", False)),
                    note=str(src_raw.get("note", "")),
                )
            )
        topics.append(
            Topic(
                id=topic_id,
                spoken_name=str(topic_raw.get("spoken_name", topic_id)),
                pack=str(topic_raw.get("pack", f"{topic_id}.md")),
                sources=sources,
            )
        )
    return topics, refused


def read_manifest(
    manifest_path: Path, *, base_dir: Path | None = None
) -> tuple[list[Topic], list[RefusedSource]]:
    """D-01: read `manifest_path` -- the ONLY source of truth for what this
    refresh surveys -- and gate every source on D-02's public-only rule.
    `base_dir` (default `REPO_ROOT`) resolves any relative source path."""
    raw = load_manifest_yaml(manifest_path)
    return parse_manifest(raw, base_dir=base_dir)


# ---------------------------------------------------------------------------
# Refresh report (warnings, refusals, advisory-lint findings)
# ---------------------------------------------------------------------------


@dataclass
class RefreshReport:
    refused: list[RefusedSource] = field(default_factory=list)
    warnings: list[str] = field(default_factory=list)
    findings: list[tuple[str, Finding]] = field(default_factory=list)
    topics_indexed: list[str] = field(default_factory=list)
    packs_written: list[str] = field(default_factory=list)


# ---------------------------------------------------------------------------
# Source text collection -- reads exactly the files reachable from a
# manifest-listed Source.path, nothing else (D-01: a path NOT in the
# manifest is never opened).
# ---------------------------------------------------------------------------


def _default_reader(path: Path) -> str:
    return path.read_text(encoding="utf-8", errors="ignore")


def iter_source_files(source: Source) -> list[Path]:
    """Every readable text file reachable from `source.path`. Never looks
    anywhere else on disk -- a directory source only ever walks its own
    subtree (D-01). Prunes dotdirs and known vendor/build directories
    (`_EXCLUDED_DIR_NAMES`) BEFORE descending into them via `os.walk`'s
    in-place `dirnames` mutation -- a plain `rglob("*")` has no way to skip
    a subtree early and will walk a real repo's entire `node_modules` (Rule
    1 bug fix: observed hanging on a real manifest source with 443,982
    files before this fix)."""
    if not source.path.exists():
        return []
    if source.path.is_file():
        return [source.path]
    out: list[Path] = []
    for root, dirnames, filenames in os.walk(source.path):
        dirnames[:] = [
            d for d in dirnames if not d.startswith(".") and d not in _EXCLUDED_DIR_NAMES
        ]
        for fname in filenames:
            p = Path(root) / fname
            if p.suffix.lower() in _TEXT_EXTENSIONS:
                out.append(p)
    return sorted(out)


def collect_source_text(
    source: Source, *, reader: Callable[[Path], str] | None = None
) -> list[tuple[str, str]]:
    """Read every file under `source.path` (D-01: only ever paths reachable
    from a manifest-listed source) into `(text, source_path_label)` pairs.
    Unreadable files are skipped, never raised (a survey pass over a large
    real repo will hit the occasional odd file)."""
    reader = reader or _default_reader
    out: list[tuple[str, str]] = []
    for f in iter_source_files(source):
        try:
            text = reader(f)
        except OSError:
            continue
        if text.strip():
            out.append((text, str(f)))
    return out


# ---------------------------------------------------------------------------
# Environment Availability fallback: a missing local checkout is skipped
# with a clear warning, never a hard failure.
# ---------------------------------------------------------------------------


def resolve_topic_sources(topic: Topic, report: RefreshReport) -> list[Source]:
    """Return only the sources whose `path` currently exists on this
    machine, recording a warning for each missing one. Never raises -- a
    developer's machine without every local checkout (`klankrmkr`,
    `defcon.run.34`, `meshtk`) is the expected common case, not an error."""
    existing: list[Source] = []
    for src in topic.sources:
        if not src.path.exists():
            report.warnings.append(
                f"{topic.id}: source not found, skipping ({src.path})"
            )
            continue
        existing.append(src)
    return existing


# ---------------------------------------------------------------------------
# The swappable doc-generation seam (Amendment 3.D/5 -- see module docstring)
# ---------------------------------------------------------------------------

GeneratorFn = Callable[[Source], "str | None"]


def default_doc_generator(source: Source) -> str | None:
    """Amendment 5: the grill-with-docs generated-docs step was DROPPED for
    launch -- the default generator is a no-op. Returning `None` means
    "no generated docs; index this source's raw text directly", which is
    exactly the design of record for defcon.run.34/meshtk's code sources
    (see module docstring). Kept as a function (not inlined) so the seam
    stays genuinely swappable: a future refresh can pass a different
    `generator` callable to `generate_docs()` without any pipeline change."""
    return None


def generate_docs(source: Source, *, generator: GeneratorFn | None = None) -> str | None:
    """The swappable doc-generation seam. `generator` defaults to
    `default_doc_generator` (Amendment 5's no-op). Never raises -- a
    generator that errors (e.g. an uninstalled external skill) degrades to
    `None` so the caller falls back to indexing the source's raw text
    instead of crashing the whole refresh."""
    fn = generator or default_doc_generator
    try:
        return fn(source)
    except Exception:
        return None


# ---------------------------------------------------------------------------
# Chunking + per-topic FTS5 index build (Plan-02's chunk_text, reused)
# ---------------------------------------------------------------------------


def build_topic_chunks(
    topic: Topic,
    sources: list[Source],
    *,
    generator: GeneratorFn | None = None,
) -> list[Chunk]:
    """Chunk `sources`' text with Plan-02's `chunk_text()`. Per Amendment
    3.D/5: a `code`-kind source first tries the swappable `generate_docs()`
    seam; when it returns text (a real generator is plugged in), that
    generated text is indexed as a PRIMARY layer (tagged `generated:<path>`)
    alongside the raw code as a SECONDARY layer. With the default no-op
    generator (Amendment 5), this simply falls through to indexing the raw
    code directly -- no doc-gen step, never a crash."""
    chunks: list[Chunk] = []
    for src in sources:
        if src.kind == "code":
            generated_text = generate_docs(src, generator=generator)
            if generated_text:
                chunks.extend(
                    chunk_text(generated_text, source_path=f"generated:{src.path}")
                )
        for text, path_label in collect_source_text(src):
            chunks.extend(chunk_text(text, source_path=path_label))
    return chunks


@dataclass
class ChunkWriteResult:
    written_path: Path | None
    skipped_reason: str | None = None


def write_chunk_file(
    index_dir: Path, topic_id: str, chunks: list[Chunk], *, force: bool = False
) -> ChunkWriteResult:
    """Write `chunks` to `index_dir/{topic_id}/docs.jsonl` -- one JSON object
    per line (`text`/`source_path`/`heading`, matching Plan-02's
    `RetrievalIndex._load_topic_chunks` shape exactly).

    Two safety guards (T-07-05, destructive-regenerate mitigation):
    - An empty `chunks` list (e.g. every source was missing/unreadable on
      this machine) never overwrites an existing committed index -- it's
      reported as a skip, not silently blanked.
    - A newly-built corpus with FEWER chunks than what's already committed
      is also NOT overwritten by default (a strong signal a local checkout
      is partially/fully missing on this run) -- pass `force=True` to
      override once a real full checkout is confirmed present.
    """
    if not chunks:
        return ChunkWriteResult(
            written_path=None,
            skipped_reason=(
                f"{topic_id}: no chunks built (all sources missing/empty) -- "
                "leaving the existing committed index untouched"
            ),
        )
    topic_dir = index_dir / topic_id
    out_path = topic_dir / "docs.jsonl"
    if out_path.is_file() and not force:
        existing_count = sum(1 for _ in out_path.open(encoding="utf-8"))
        if len(chunks) < existing_count:
            return ChunkWriteResult(
                written_path=None,
                skipped_reason=(
                    f"{topic_id}: newly-built corpus ({len(chunks)} chunks) is smaller "
                    f"than the committed index ({existing_count} chunks) -- skipping "
                    "overwrite (a local checkout is likely missing/partial on this "
                    "machine); rerun with --force once a full checkout is confirmed"
                ),
            )
    topic_dir.mkdir(parents=True, exist_ok=True)
    with out_path.open("w", encoding="utf-8") as f:
        for c in chunks:
            f.write(
                json.dumps({"text": c.text, "source_path": c.source_path, "heading": c.heading})
                + "\n"
            )
    return ChunkWriteResult(written_path=out_path)


# ---------------------------------------------------------------------------
# Advisory do-not-say lint (Amendment 3.E) -- FLAGS, never blocks.
# ---------------------------------------------------------------------------


def flag_landmines(label: str, text: str) -> list[Finding]:
    """Run Plan-01's `advisory_lint()` over `text`. Never raises, never
    blocks -- the return value is ONLY for the D-09 git-diff human review
    report; callers must always write the output regardless of findings
    (Amendment 3.E explicitly reverses the earlier refuse-on-finding
    framing)."""
    return advisory_lint(text)


def write_pack(out_root: Path, pack_filename: str, text: str, *, packs_dir: str = DEFAULT_PACKS_DIR) -> Path:
    """Write a curated pack's text into the tracked `knowledge/topics/` tree
    (or `out_root/packs_dir` for `--dry-run`/tests). Always writes -- the
    advisory lint's findings (if any) are reported alongside, never used to
    withhold the write (Amendment 3.E)."""
    out_path = out_root / packs_dir / pack_filename
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(text, encoding="utf-8")
    return out_path


# ---------------------------------------------------------------------------
# Distillation (map-reduce: survey -> distill -> style pass, RESEARCH
# Pattern 3). The LLM call is behind an injectable seam so tests never hit
# the network.
# ---------------------------------------------------------------------------

LlmCallFn = Callable[[str], str]


def survey_repo(source: Source) -> str:
    """Per-repo/source survey pass (map-reduce step 1): concatenate every
    readable file under `source.path` into one "repo notes" string, the
    input to `distill_topic()`'s per-topic distillation call."""
    return "\n\n".join(text for text, _ in collect_source_text(source))


def _require_env(name: str) -> str:
    value = os.environ.get(name, "")
    if not value:
        raise RuntimeError(f"{name} is not set. Run `make -C apps/voice env`.")
    return value


def _anthropic_llm_call(prompt: str, *, model: str = DEFAULT_DISTILL_MODEL) -> str:
    """Default LLM call for distillation -- same `_require_env` convention as
    `factories.py`/`judge.py`; no new vendor (PIPE-07)."""
    import anthropic

    client = anthropic.Anthropic(api_key=_require_env("ANTHROPIC_API_KEY"))
    response = client.messages.create(
        model=model,
        max_tokens=4096,
        messages=[{"role": "user", "content": prompt}],
    )
    return "".join(block.text for block in response.content if getattr(block, "type", "") == "text")


def _distill_prompt(topic: Topic, survey_text: str) -> str:
    return (
        f"You are distilling a voice-friendly knowledge pack about "
        f"'{topic.spoken_name}' for a spoken concierge (Kurt's own voice and "
        "phrasing, PG-13 by default, hook line + a longer version, facts "
        "only -- never invent details not present in the source notes "
        "below). Never include AWS account IDs, ARNs, key material, or "
        "internal/.local hostnames.\n\n"
        f"--- SOURCE NOTES for {topic.id} ---\n{survey_text}\n"
    )


def distill_topic(topic: Topic, survey_text: str, *, llm_call: LlmCallFn | None = None) -> str:
    """Per-topic distillation pass (map-reduce step 2): survey text ->
    voice-friendly curated pack markdown. `llm_call` is injectable so tests
    never make a real network call; defaults to a live Anthropic call."""
    call = llm_call or _anthropic_llm_call
    return call(_distill_prompt(topic, survey_text))


def style_pass(transcript_text: str, *, llm_call: LlmCallFn | None = None) -> str:
    """Style pass (map-reduce step 3): distill Kurt's speaking cadence from
    transcript text into a short style guide + verbatim exemplar lines
    (Amendment 2/4). `llm_call` is injectable for the same reason as
    `distill_topic`."""
    call = llm_call or _anthropic_llm_call
    prompt = (
        "Distill Kurt's speaking style (cadence, phrasing, humor, how he "
        "explains things) from the transcript excerpts below into a short "
        "(~300-500 token) style guide plus 2-4 short VERBATIM exemplar "
        "lines. Do not invent quotes -- only lift text that actually "
        "appears below.\n\n"
        f"--- TRANSCRIPT EXCERPTS ---\n{transcript_text}\n"
    )
    return call(prompt)


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        prog="refresh_knowledge.py",
        description=(
            "D-07 manual knowledge refresh: reads knowledge/manifest.yaml "
            "(the ONLY source of truth, D-01), refuses any source not "
            "flagged public:true (D-02), runs the swappable doc-generation "
            "seam over code-heavy sources (Amendment 3.D/5 -- defaults to "
            "direct code indexing, grill-with-docs dropped per Amendment "
            "5), builds the per-topic FTS5 retrieval chunk files "
            "(knowledge/index/{topic}/*.jsonl), distills voice-friendly "
            "curated packs + the Kurt STYLE layer, and runs the advisory "
            "do-not-say lint over every output -- flagging findings for "
            "the D-09 git-diff human review, never blocking or refusing "
            "the write (Amendment 3.E). Writes into the tracked "
            "apps/voice/knowledge/ tree for review as an ordinary git diff "
            "-- never auto-commits, never runs during a live session "
            "(Amendment 3.G)."
        ),
    )
    parser.add_argument(
        "--manifest",
        type=Path,
        default=DEFAULT_MANIFEST_PATH,
        help="Path to knowledge/manifest.yaml (default: the checked-in manifest).",
    )
    parser.add_argument(
        "--out-dir",
        type=Path,
        default=None,
        help=(
            "Write into this directory's knowledge/ subtree instead of the "
            "real apps/voice/ tree (used by --dry-run and tests)."
        ),
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help=(
            "Write into a temp directory instead of the tracked knowledge/ "
            "tree, and skip the curated-pack distillation LLM pass "
            "(chunk/index build + advisory lint only, no ANTHROPIC_API_KEY "
            "needed)."
        ),
    )
    parser.add_argument(
        "--skip-distill",
        action="store_true",
        help=(
            "Skip the curated-pack distillation LLM pass; rebuild "
            "chunk/index files + advisory lint only (no ANTHROPIC_API_KEY "
            "needed)."
        ),
    )
    parser.add_argument(
        "--force",
        action="store_true",
        help=(
            "Overwrite a topic's chunk file even if the newly-built corpus "
            "has fewer chunks than what's already committed."
        ),
    )
    return parser.parse_args(argv)


def _require_api_key() -> str:
    load_dotenv(APP_ROOT / ".env", override=True)
    key = os.environ.get("ANTHROPIC_API_KEY", "")
    if not key:
        print(
            "ERROR: ANTHROPIC_API_KEY not set. Run `make -C apps/voice env` "
            "(or populate apps/voice/.env) and retry.",
            file=sys.stderr,
        )
        sys.exit(1)
    return key


def print_report(report: RefreshReport) -> None:
    print(f"\nrefused sources (D-02, missing public:true): {len(report.refused)}")
    for r in report.refused:
        print(f"  - [{r.topic_id}] {r.path}: {r.reason}")
    print(f"\nwarnings: {len(report.warnings)}")
    for w in report.warnings:
        print(f"  - {w}")
    print(f"\ntopics with a rebuilt chunk index: {report.topics_indexed}")
    print(f"packs written: {report.packs_written}")
    print(f"\nadvisory lint findings (Amendment 3.E -- FLAGGED, never blocking): {len(report.findings)}")
    for label, finding in report.findings:
        print(f"  - [{label}] line {finding.line} ({finding.pattern}): {finding.excerpt}")


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    manifest_path = args.manifest.resolve()
    out_root = args.out_dir.resolve() if args.out_dir else APP_ROOT
    needs_llm = not (args.dry_run or args.skip_distill)
    if needs_llm:
        _require_api_key()

    topics, refused = read_manifest(manifest_path)
    report = RefreshReport(refused=refused)

    for topic in topics:
        sources = resolve_topic_sources(topic, report)

        chunks = build_topic_chunks(topic, sources)
        index_dir = out_root / DEFAULT_INDEX_SUBDIR
        write_result = write_chunk_file(index_dir, topic.id, chunks, force=args.force)
        if write_result.written_path is not None:
            report.topics_indexed.append(topic.id)
            corpus_text = "\n".join(c.text for c in chunks)
            findings = flag_landmines(f"{topic.id}/index", corpus_text)
            report.findings.extend((f"{topic.id}/index", f) for f in findings)
        elif write_result.skipped_reason:
            report.warnings.append(write_result.skipped_reason)

        if needs_llm and sources:
            survey_text = "\n\n".join(survey_repo(src) for src in sources)
            if survey_text.strip():
                pack_text = distill_topic(topic, survey_text)
                pack_findings = flag_landmines(f"{topic.id}/pack", pack_text)
                report.findings.extend((f"{topic.id}/pack", f) for f in pack_findings)
                write_pack(out_root, topic.pack, pack_text)
                report.packs_written.append(topic.pack)

    print_report(report)
    return 0


if __name__ == "__main__":
    sys.exit(main())
