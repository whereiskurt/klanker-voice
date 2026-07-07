"""Two-block cached system prompt assembly (RESEARCH Pattern 1, D-13).

``build_system_blocks`` returns the Anthropic ``system`` array as exactly two
text blocks:

- **block0 (STABLE, cached):** persona + Kurt STYLE layer + every topic's
  one-line hook from the topic map, wrapped with ``cache_control: ephemeral``.
  Built the same way for every topic and every turn -- byte-identical across
  topic switches (Pitfall 3: never rebuild it per turn, never interpolate
  session-specific values into it).
- **block1 (SWAPPABLE, uncached):** the selected topic's deep pack. Swapped
  by :class:`klanker_voice.knowledge.router.KnowledgeRouterProcessor` on a
  genuine topic switch; never touches block0.

``retrieved_chunks`` (07-02, local BM25 retrieval): when non-empty, a THIRD
post-breakpoint block is appended -- the topic-scoped top-k chunks
``klanker_voice.knowledge.router.KnowledgeRouterProcessor`` fetched from
:class:`klanker_voice.knowledge.retrieval.RetrievalIndex` on a genuine deep
turn. It never touches block0 or block1's text (Amendment 3-C, Pitfall 3);
empty/None yields exactly the Plan-01 two-block shape.

``remaining_seconds`` (07-05, time-aware pacing, D-06): when supplied, a
short pacing note is prepended to block1 ONLY (never block0) -- tight
highlights + a closing pointer when little session time is left, room for
depth when there's more. ``None`` (the default, and every pre-07-05 caller)
reproduces the exact block1 text of the Plan-01/02 shape unchanged.

Wiring note (a genuine pipecat gap, not a shortcut): see
:func:`apply_system_blocks` for why this two-block ``system`` array is set
directly on the LLM service's ``Settings.system_instruction`` rather than as
an ``LLMContext`` system-role message -- ``AnthropicLLMAdapter`` flattens a
list-content system message into a single joined string before it ever
reaches the API, discarding any ``cache_control`` marker.
"""

from __future__ import annotations

import os
from pathlib import Path
from typing import Any

import yaml

from klanker_voice.config import KnowledgeConfig, PipelineConfig
from klanker_voice.knowledge.retrieval import Chunk

CacheControlBlock = dict[str, Any]

#: D-06 time-aware pacing (07-05) threshold: at or below this many seconds
#: remaining, KPH should tighten to highlights + a closing pointer rather
#: than opening up depth. Deliberately a single binary threshold (not a
#: graduated scale) -- simple enough for the LLM to act on consistently
#: inside a short pacing note.
PACING_TIGHT_THRESHOLD_SECONDS = 90.0


def _read(path: Path) -> str:
    return path.read_text(encoding="utf-8")


def load_manifest(knowledge_cfg: KnowledgeConfig) -> dict:
    """Parse ``knowledge/manifest.yaml`` (D-01)."""
    return yaml.safe_load(_read(knowledge_cfg.manifest_path)) or {}


def load_topic_map(knowledge_cfg: KnowledgeConfig) -> dict:
    """Parse ``knowledge/router/topic-map.yaml`` (RESEARCH Pattern 2)."""
    return yaml.safe_load(_read(knowledge_cfg.topic_map_path)) or {}


def render_topic_hooks(knowledge_cfg: KnowledgeConfig) -> str:
    """Render every topic's one-line spoken hook for the stable prefix.

    KPH can always name what it knows about -- even before any deep pack
    has loaded -- because every topic's hook lives in block0 (Amendment 1's
    "knowledge map" concept).
    """
    topic_map = load_topic_map(knowledge_cfg)
    lines = ["## Knowledge map -- topics KPH can dig into"]
    for topic in topic_map.get("topics", []):
        lines.append(f"- **{topic['spoken_name']}** ({topic['id']}): {topic['hook']}")
    return "\n".join(lines)


def load_persona_text(cfg: PipelineConfig) -> str:
    """Read the versioned persona markdown from config.

    Deliberately duplicates ``pipeline.load_persona``'s one-liner rather than
    importing it: ``pipeline.py`` imports this module (to wire
    ``build_system_blocks`` into ``build_pipeline``), so importing back would
    be circular.
    """
    return cfg.persona.prompt_path.read_text(encoding="utf-8")


def build_stable_prefix_text(cfg: PipelineConfig, knowledge_cfg: KnowledgeConfig) -> str:
    """Persona + Kurt STYLE layer + topic-map hooks, concatenated (block0's text).

    Built fresh each call, but the RESULT is byte-identical for a given
    ``cfg``/``knowledge_cfg`` pair regardless of which topic is selected --
    callers (pipeline.py, the router) should still build it ONCE per session
    and reuse it rather than calling this on every turn (Pitfall 3).
    """
    persona_text = load_persona_text(cfg).strip()
    style_text = _read(knowledge_cfg.style_path).strip()
    hooks_text = render_topic_hooks(knowledge_cfg).strip()
    return "\n\n".join([persona_text, style_text, hooks_text]) + "\n"


def load_topic_pack_text(knowledge_cfg: KnowledgeConfig, topic: str) -> str:
    """Read the deep pack for ``topic`` (block1's text).

    Raises:
        ValueError: ``topic`` is not a manifest entry.
    """
    manifest = load_manifest(knowledge_cfg)
    entry = next((t for t in manifest.get("topics", []) if t["id"] == topic), None)
    if entry is None:
        known = sorted(t["id"] for t in manifest.get("topics", []))
        raise ValueError(f"unknown knowledge topic {topic!r}; not in manifest (known: {known})")
    pack_path = knowledge_cfg.packs_dir / entry["pack"]
    return _read(pack_path)


def render_pacing_note(remaining_seconds: float | None) -> str:
    """Render a short, spoken-friendly pacing note for block1 (07-05, D-06).

    ``None`` (no session-time signal -- e.g. a bypass/smoke session, or any
    caller that predates this parameter) renders an empty string: no pacing
    note at all, block1 unchanged from the Plan-01/02 shape.
    """
    if remaining_seconds is None:
        return ""
    if remaining_seconds <= PACING_TIGHT_THRESHOLD_SECONDS:
        return (
            "## Pacing (time check -- only a little session time left)\n"
            "Keep it tight: land the single strongest highlight, skip "
            "tangents, and close with a pointer (the repo, or come find "
            "Kurt) instead of opening up depth.\n\n"
        )
    return (
        "## Pacing (time check -- plenty of session time left)\n"
        "It's fine to go deeper if the visitor wants it -- offer the long "
        "version, not just the highlight.\n\n"
    )


def render_retrieved_chunks(chunks: list[Chunk]) -> str:
    """Render retrieved chunks with light source attribution KPH can cite
    from -- narration framing, not a raw code/doc dump (Amendment 5's
    retrieval-quality caveat: KPH should narrate, not read code aloud)."""
    lines = ["## Retrieved detail (top matches from the full corpus -- ad-hoc depth)"]
    for chunk in chunks:
        heading_suffix = f" -- {chunk.heading}" if chunk.heading else ""
        lines.append(f"\n### From {chunk.source_path}{heading_suffix}\n{chunk.text.strip()}")
    return "\n".join(lines) + "\n"


def build_system_blocks(
    cfg: PipelineConfig,
    knowledge_cfg: KnowledgeConfig,
    topic: str,
    *,
    retrieved_chunks: list[Chunk] | None = None,
    remaining_seconds: float | None = None,
) -> list[CacheControlBlock]:
    """Build the Anthropic ``system`` array for ``topic``.

    Args:
        cfg: The pipeline config (persona path, llm model).
        knowledge_cfg: The ``[knowledge]`` config (manifest/topic-map/pack/
            style paths).
        topic: The manifest topic id to load into block1.
        retrieved_chunks: 07-02 local BM25 retrieval (Amendment 3-B/C) -- when
            non-empty, a THIRD uncached, post-breakpoint block is appended
            with these chunks, alongside (never replacing) the curated
            block1 pack. Empty/None -> the Plan-01 two-block shape,
            unchanged.
        remaining_seconds: 07-05 time-aware pacing (D-06) -- when not
            ``None``, a short pacing note is prepended to block1 (never
            block0/block2). ``None`` (the default) -> block1 is exactly the
            topic pack text, unchanged from the Plan-01/02 shape.

    Returns:
        ``[block0, block1]`` when no chunks are retrieved (block0 carries
        ``cache_control: ephemeral``, block1 does not); ``[block0, block1,
        block2]`` when chunks are present -- block2 also carries no
        ``cache_control`` (it is per-turn dynamic, Pitfall 3). block1 itself
        never carries ``cache_control`` either way, pacing note or not.
    """
    stable_prefix = build_stable_prefix_text(cfg, knowledge_cfg)
    pack_text = load_topic_pack_text(knowledge_cfg, topic)
    pacing_note = render_pacing_note(remaining_seconds)

    blocks: list[CacheControlBlock] = [
        {
            "type": "text",
            "text": stable_prefix,
            "cache_control": {"type": "ephemeral"},
        },
        {
            "type": "text",
            "text": pacing_note + pack_text,
        },
    ]

    if retrieved_chunks:
        blocks.append(
            {
                "type": "text",
                "text": render_retrieved_chunks(retrieved_chunks),
            }
        )

    return blocks


def apply_system_blocks(llm: Any, blocks: list[CacheControlBlock]) -> None:
    """Set the two-block ``system`` array directly on the live LLM service.

    This deliberately bypasses pipecat's ``LLMContext`` "system"-role message
    convention. ``AnthropicLLMAdapter._extract_initial_system`` (pipecat
    1.5.0) joins a list-content system message's text parts into a single
    string before it reaches the API -- silently discarding any
    ``cache_control`` marker on the blocks. Setting
    ``Settings.system_instruction`` directly is the one path that survives
    intact: ``AnthropicLLMAdapter._resolve_system_instruction`` returns it
    VERBATIM (no type coercion) whenever it's truthy, so our two-block
    ``cache_control`` structure reaches ``client.beta.messages.create(system=
    ...)`` unchanged.

    Must NOT go through ``LLMService.append_system_instruction`` or an
    ``LLMUpdateSettingsFrame`` with ``system_instruction`` set -- both funnel
    through ``LLMService._compose_system_instruction``, which assumes
    ``system_instruction`` is a plain string and silently discards a list
    (``base_si if isinstance(base_si, str) else None``). Assigning the
    private ``_settings`` attribute directly is the only way to carry a
    block-list system prompt through this version of pipecat.
    """
    llm._settings.system_instruction = blocks


def _require_env(name: str) -> str:
    value = os.environ.get(name)
    if not value:
        raise RuntimeError(
            f"{name} is not set. Run `make -C apps/voice env` to write .env from SSM."
        )
    return value


def count_tokens(text: str, *, model: str = "claude-haiku-4-5") -> int:
    """Count tokens for a system-prefix string via the Anthropic API.

    Same-vendor (ANTHROPIC_API_KEY), used to verify the D-13 ``cache_floor``
    is actually crossed by block0 -- this is a genuine network call.
    """
    from anthropic import Anthropic

    client = Anthropic(api_key=_require_env("ANTHROPIC_API_KEY"))
    result = client.messages.count_tokens(
        model=model,
        system=[{"type": "text", "text": text}],
        messages=[{"role": "user", "content": "."}],
    )
    return result.input_tokens
