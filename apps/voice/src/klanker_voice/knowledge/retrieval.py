"""Local, keyless BM25/FTS5 retrieval (Amendment 3-A/B/C, PIPE-07).

Engine: stdlib ``sqlite3`` FTS5 with its built-in BM25 ranking. No embeddings,
no vector search, no 4th vendor, no network call anywhere in this module --
retrieval is a pure in-process disk-read + local SQL query.

Corpus flow: offline, ``knowledge/index/{topic}/*.jsonl`` chunk files are
committed (D-09 diff-reviewable text, one JSON object per line: ``text``,
``source_path``, ``heading``). At runtime :class:`RetrievalIndex` reads those
files and builds a per-topic FTS5 table lazily (first query per topic), then
reuses the built connection for the life of the process/session -- the
per-turn cost is exactly one BM25 ``MATCH`` query (tens of ms, Amendment 3-G).

A topic with no chunk files -- or no built index at all -- degrades to an
empty query result. Retrieval is additive depth, never a hard dependency
(Pitfall: missing topics must never crash a turn).
"""

from __future__ import annotations

import json
import re
import sqlite3
from dataclasses import dataclass
from pathlib import Path
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from klanker_voice.config import KnowledgeConfig


@dataclass(frozen=True)
class Chunk:
    """One retrievable unit: a heading-scoped window of source text.

    ``heading`` is ``None`` for text that precedes any markdown heading
    (e.g. a document's leading paragraph).
    """

    text: str
    source_path: str
    heading: str | None = None


_HEADING_RE = re.compile(r"^(#{1,6})\s+(.*)$")


def _split_by_heading(text: str) -> list[tuple[str | None, str]]:
    """Split ``text`` into ``(heading, section_body)`` pairs at every
    markdown heading line (``#`` .. ``######``). Content before the first
    heading gets ``heading=None``. The heading line itself stays in its own
    section's body so a chunk reads naturally in isolation."""
    heading: str | None = None
    buf: list[str] = []
    sections: list[tuple[str | None, str]] = []
    for line in text.splitlines():
        match = _HEADING_RE.match(line)
        if match:
            if buf:
                sections.append((heading, "\n".join(buf)))
            heading = match.group(2).strip()
            buf = [line]
        else:
            buf.append(line)
    if buf:
        sections.append((heading, "\n".join(buf)))
    return sections


def chunk_text(
    text: str,
    *,
    source_path: str,
    max_chars: int = 900,
    overlap: int = 150,
) -> list[Chunk]:
    """Heading-aware chunking into overlapping, voice-answer-sized windows.

    Each markdown section (bounded by heading lines) becomes one or more
    chunks of at most ``max_chars`` characters; long sections are split into
    overlapping windows (``overlap`` chars of context carried into the next
    window) so a detail near a chunk boundary isn't orphaned. Chunks stay a
    few hundred tokens each so a top-4 result set fits comfortably inside the
    ~1.5k-token injection budget (Amendment 3-C).
    """
    chunks: list[Chunk] = []
    for heading, section in _split_by_heading(text):
        body = section.strip()
        if not body:
            continue
        if len(body) <= max_chars:
            chunks.append(Chunk(text=body, source_path=source_path, heading=heading))
            continue
        start = 0
        n = len(body)
        while start < n:
            end = min(start + max_chars, n)
            chunks.append(Chunk(text=body[start:end], source_path=source_path, heading=heading))
            if end >= n:
                break
            start = max(end - overlap, start + 1)
    return chunks


def fts5_available() -> bool:
    """Cheap availability probe (T-07-08 robustness): a throwaway
    ``CREATE VIRTUAL TABLE ... USING fts5`` so callers/tests can skip cleanly
    on a sqlite3 build without the FTS5 extension, instead of crashing deep
    inside a query."""
    try:
        conn = sqlite3.connect(":memory:")
        try:
            conn.execute("CREATE VIRTUAL TABLE probe USING fts5(x)")
        finally:
            conn.close()
        return True
    except sqlite3.OperationalError:
        return False


def build_topic_index(chunks: list[Chunk], *, db_path: str = ":memory:") -> sqlite3.Connection:
    """Build an FTS5 virtual table from ``chunks``. Ranking is FTS5's own
    built-in BM25 (``ORDER BY bm25(chunks)`` at query time) -- no embeddings,
    no external ranking model (Amendment 3-A, PIPE-07)."""
    conn = sqlite3.connect(db_path)
    conn.execute(
        "CREATE VIRTUAL TABLE chunks USING fts5(text, source_path UNINDEXED, heading UNINDEXED)"
    )
    conn.executemany(
        "INSERT INTO chunks (text, source_path, heading) VALUES (?, ?, ?)",
        [(c.text, c.source_path, c.heading or "") for c in chunks],
    )
    conn.commit()
    return conn


_WORD_RE = re.compile(r"[A-Za-z0-9]+")


def _sanitize_fts5_query(utterance: str, *, max_terms: int = 24) -> str:
    """Sanitize raw spoken text into a safe FTS5 MATCH query (T-07-08:
    Denial-of-service/robustness threat). Only alnum "words" survive (spoken
    punctuation, hyphens, and FTS5 operator characters -- ``"``, ``:``, ``-``,
    ``(``/``)``, ``*`` -- are stripped entirely), each individually quoted and
    OR-joined so a stray operator-shaped word (``NOT``, ``AND``, ``NEAR``)
    can never be parsed as an FTS5 query operator. Empty input (or input with
    no alnum words) returns ``""`` -- callers treat that as "no query,
    return []", never a crash."""
    words = _WORD_RE.findall(utterance)[:max_terms]
    if not words:
        return ""
    return " OR ".join(f'"{w}"' for w in words)


def _approx_tokens(text: str) -> int:
    """Cheap, local token-count approximation (word count) for the injection
    budget cap. An exact count needs a network call (see
    ``prompt_assembly.count_tokens``) -- too slow to run per BM25 query, and
    unnecessary for a soft budget cap."""
    return max(1, len(text.split()))


def _trim_to_budget(chunks: list[Chunk], max_tokens: int) -> list[Chunk]:
    """Keep chunks in rank order until the next one would exceed
    ``max_tokens`` (approximate). Always keeps at least the first chunk."""
    kept: list[Chunk] = []
    total = 0
    for chunk in chunks:
        cost = _approx_tokens(chunk.text)
        if kept and total + cost > max_tokens:
            break
        kept.append(chunk)
        total += cost
    return kept


class RetrievalIndex:
    """Per-topic FTS5/BM25 index built from committed
    ``knowledge/index/{topic}/*.jsonl`` chunk files.

    Built lazily per topic (first query triggers the build) and cached for
    the life of this instance -- callers (``pipeline.build_pipeline``)
    construct exactly one ``RetrievalIndex`` per session/process and reuse
    it, never rebuilding per turn (Amendment 3-G).
    """

    def __init__(self, knowledge_cfg: "KnowledgeConfig") -> None:
        self._index_dir = Path(knowledge_cfg.index_dir)
        self._connections: dict[str, sqlite3.Connection | None] = {}

    def _load_topic_chunks(self, topic_id: str) -> list[Chunk]:
        topic_dir = self._index_dir / topic_id
        if not topic_dir.is_dir():
            return []
        chunks: list[Chunk] = []
        for jsonl_path in sorted(topic_dir.glob("*.jsonl")):
            with jsonl_path.open(encoding="utf-8") as f:
                for line in f:
                    line = line.strip()
                    if not line:
                        continue
                    obj = json.loads(line)
                    chunks.append(
                        Chunk(
                            text=obj["text"],
                            source_path=obj.get("source_path", ""),
                            heading=obj.get("heading"),
                        )
                    )
        return chunks

    def _get_connection(self, topic_id: str) -> sqlite3.Connection | None:
        if topic_id not in self._connections:
            chunks = self._load_topic_chunks(topic_id)
            self._connections[topic_id] = build_topic_index(chunks) if chunks else None
        return self._connections[topic_id]

    def query(
        self,
        topic_id: str,
        utterance: str,
        *,
        top_k: int = 4,
        max_tokens: int = 1500,
    ) -> list[Chunk]:
        """Return at most ``top_k`` chunks for ``topic_id``, ranked by BM25,
        trimmed to ``max_tokens``. A topic with no built index -- or a query
        that sanitizes to nothing -- returns ``[]`` (graceful degrade, never
        an exception)."""
        conn = self._get_connection(topic_id)
        if conn is None:
            return []
        match_query = _sanitize_fts5_query(utterance)
        if not match_query:
            return []
        try:
            rows = conn.execute(
                "SELECT text, source_path, heading FROM chunks "
                "WHERE chunks MATCH ? ORDER BY bm25(chunks) LIMIT ?",
                (match_query, top_k),
            ).fetchall()
        except sqlite3.OperationalError:
            # Sanitization should prevent this in practice; never crash the
            # turn on a query-syntax edge case (T-07-08 graceful degrade).
            return []
        chunks = [Chunk(text=r[0], source_path=r[1], heading=(r[2] or None)) for r in rows]
        return _trim_to_budget(chunks, max_tokens)
