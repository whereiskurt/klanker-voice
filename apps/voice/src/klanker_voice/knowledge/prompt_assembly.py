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

``retrieved_chunks`` (07-02, local BM25 retrieval) and ``remaining_seconds``
(07-05, time-aware pacing) are accepted-but-unused parameters here -- both
future plans inject into block1 ONLY, never block0 (Amendment 3-C, D-13).

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

CacheControlBlock = dict[str, Any]


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


def build_system_blocks(
    cfg: PipelineConfig,
    knowledge_cfg: KnowledgeConfig,
    topic: str,
    *,
    retrieved_chunks: list[str] | None = None,
    remaining_seconds: float | None = None,
) -> list[CacheControlBlock]:
    """Build the two-block Anthropic ``system`` array for ``topic``.

    Args:
        cfg: The pipeline config (persona path, llm model).
        knowledge_cfg: The ``[knowledge]`` config (manifest/topic-map/pack/
            style paths).
        topic: The manifest topic id to load into block1.
        retrieved_chunks: Present-but-unused (07-02 fills retrieval injection
            into block1 ONLY -- never block0).
        remaining_seconds: Present-but-unused (07-05 fills time-aware pacing,
            also into block1 ONLY).

    Returns:
        ``[block0, block1]`` -- block0 carries ``cache_control: ephemeral``,
        block1 does not.
    """
    del retrieved_chunks, remaining_seconds  # 07-02 / 07-05 seams; unused here

    stable_prefix = build_stable_prefix_text(cfg, knowledge_cfg)
    pack_text = load_topic_pack_text(knowledge_cfg, topic)

    return [
        {
            "type": "text",
            "text": stable_prefix,
            "cache_control": {"type": "ephemeral"},
        },
        {
            "type": "text",
            "text": pack_text,
        },
    ]


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
