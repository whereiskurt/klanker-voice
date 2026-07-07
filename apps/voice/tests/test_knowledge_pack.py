"""Unit tests for klanker_voice.config's [knowledge] table (KnowledgeConfig)
and klanker_voice.knowledge.prompt_assembly / lint (Phase 7, D-01/D-13, T-1-04).

Wave-0 note (Task 1, 07-01-PLAN.md): the ``build_system_blocks``/``count_tokens``/
``advisory_lint`` tests below import from ``klanker_voice.knowledge.prompt_assembly``
and ``klanker_voice.knowledge.lint`` -- neither module exists until Task 2, so this
whole file is RED (ImportError) immediately after Task 1's commit. That RED state is
the Task 1 gate; Task 2 makes it GREEN.
"""

from __future__ import annotations

import os
from pathlib import Path

import pytest

from klanker_voice.config import (
    APP_ROOT,
    ConfigError,
    KnowledgeConfig,
    load_config,
    load_knowledge_config,
)

REAL_PIPELINE_TOML = APP_ROOT / "pipeline.toml"

MINIMAL_KNOWLEDGE_TOML = """
[knowledge]
manifest = "knowledge/manifest.yaml"
topic_map = "knowledge/router/topic-map.yaml"
packs_dir = "knowledge/topics"
style_path = "knowledge/style/kurt-voice.md"
cache_floor = 10
"""


def _write_knowledge_fixture(tmp_path: Path) -> None:
    """Write a tiny, valid knowledge/ tree under tmp_path (isolated fixture)."""
    (tmp_path / "knowledge" / "router").mkdir(parents=True, exist_ok=True)
    (tmp_path / "knowledge" / "topics").mkdir(parents=True, exist_ok=True)
    (tmp_path / "knowledge" / "style").mkdir(parents=True, exist_ok=True)
    # 07-02: retrieval_enabled defaults True, so index_dir must exist (empty is
    # fine -- RetrievalIndex/load_knowledge_config only require the dir, not
    # any topic subdirs; a topic with no chunk files degrades gracefully).
    (tmp_path / "knowledge" / "index").mkdir(parents=True, exist_ok=True)

    (tmp_path / "knowledge" / "manifest.yaml").write_text(
        """
version: 1
tour_priority:
  - test-topic
topics:
  - id: test-topic
    spoken_name: "test topic"
    pack: test-topic.md
    sources: []
""",
        encoding="utf-8",
    )
    (tmp_path / "knowledge" / "router" / "topic-map.yaml").write_text(
        """
version: 1
confidence_floor: 2
topics:
  - id: test-topic
    spoken_name: "test topic"
    hook: "a tiny test topic"
    keywords:
      - term: "test topic"
        weight: 3
""",
        encoding="utf-8",
    )
    (tmp_path / "knowledge" / "topics" / "test-topic.md").write_text(
        "# Test Topic\n\nThis is the deep pack for the test topic.\n", encoding="utf-8"
    )
    (tmp_path / "knowledge" / "style" / "kurt-voice.md").write_text(
        "# Style\n\nDry, punchy, self-deprecating.\n", encoding="utf-8"
    )


@pytest.fixture
def make_knowledge_config_file(make_config_file, tmp_path: Path):
    """Extend the shared ``make_config_file`` fixture with a valid ``[knowledge]``
    table + on-disk knowledge/ tree, isolated to tmp_path."""

    def _make(*, replace=None, append: str = "", omit_file: str | None = None) -> Path:
        _write_knowledge_fixture(tmp_path)
        if omit_file:
            (tmp_path / omit_file).unlink()
        return make_config_file(replace=replace, append=MINIMAL_KNOWLEDGE_TOML + append)

    return _make


# ---------------------------------------------------------------------------
# KnowledgeConfig / load_knowledge_config (Task 1)
# ---------------------------------------------------------------------------


def test_real_checked_in_knowledge_table_round_trips():
    cfg = load_knowledge_config(REAL_PIPELINE_TOML)
    assert isinstance(cfg, KnowledgeConfig)
    assert cfg.manifest_path.is_file()
    assert cfg.manifest_path.name == "manifest.yaml"
    assert cfg.topic_map_path.is_file()
    assert cfg.topic_map_path.name == "topic-map.yaml"
    assert cfg.packs_dir.is_dir()
    assert cfg.style_path.is_file()
    assert cfg.style_path.name == "kurt-voice.md"
    assert cfg.cache_floor == 4096


def test_real_checked_in_manifest_has_km_and_tour_priority():
    import yaml

    cfg = load_knowledge_config(REAL_PIPELINE_TOML)
    manifest = yaml.safe_load(cfg.manifest_path.read_text(encoding="utf-8"))
    assert manifest["tour_priority"]
    assert any(t["id"] == "klanker-maker" for t in manifest["topics"])


def test_real_checked_in_topic_map_has_km_and_confidence_floor():
    import yaml

    cfg = load_knowledge_config(REAL_PIPELINE_TOML)
    topic_map = yaml.safe_load(cfg.topic_map_path.read_text(encoding="utf-8"))
    assert topic_map["confidence_floor"] >= 1
    assert any(t["id"] == "klanker-maker" for t in topic_map["topics"])


def test_minimal_fixture_knowledge_table_parses(make_knowledge_config_file):
    cfg = load_knowledge_config(make_knowledge_config_file())
    assert cfg.manifest_path.is_file()
    assert cfg.cache_floor == 10


def test_missing_knowledge_table_rejected(make_config_file):
    """load_knowledge_config requires [knowledge] -- MINIMAL_TOML omits it
    (mirrors load_quota_config's [quota]-required precedent)."""
    path = make_config_file()
    with pytest.raises(ConfigError, match="knowledge"):
        load_knowledge_config(path)


def test_load_config_ignores_knowledge_table(make_knowledge_config_file):
    """load_config() (PipelineConfig) never requires or reads [knowledge] --
    it's a fully independent loader (existing 168+ tests stay green)."""
    path = make_knowledge_config_file()
    cfg = load_config(path)
    assert cfg is not None


@pytest.mark.parametrize(
    "omit_file,match",
    [
        ("knowledge/manifest.yaml", "manifest"),
        ("knowledge/router/topic-map.yaml", "topic_map"),
        ("knowledge/style/kurt-voice.md", "style_path"),
    ],
)
def test_missing_knowledge_file_rejected(make_knowledge_config_file, omit_file, match):
    path = make_knowledge_config_file(omit_file=omit_file)
    with pytest.raises(ConfigError, match=match):
        load_knowledge_config(path)


def test_knowledge_credential_looking_field_rejected(make_knowledge_config_file):
    path = make_knowledge_config_file(append='\napi_key = "sk-oops"\n')
    with pytest.raises(ConfigError, match="credential"):
        load_knowledge_config(path)


# ---------------------------------------------------------------------------
# build_system_blocks / count_tokens (Task 2) -- RED until the knowledge
# package exists.
# ---------------------------------------------------------------------------


def test_build_system_blocks_returns_two_blocks_cache_control_on_first_only():
    from klanker_voice.knowledge.prompt_assembly import build_system_blocks

    cfg = load_config(REAL_PIPELINE_TOML)
    knowledge_cfg = load_knowledge_config(REAL_PIPELINE_TOML)

    blocks = build_system_blocks(cfg, knowledge_cfg, "klanker-maker")

    assert len(blocks) == 2
    assert blocks[0]["type"] == "text"
    assert blocks[0].get("cache_control") == {"type": "ephemeral"}
    assert blocks[1]["type"] == "text"
    assert "cache_control" not in blocks[1]


def test_build_system_blocks_block0_byte_identical_across_topics():
    """The caching invariant (Pitfall 3): block0 never varies with topic."""
    from klanker_voice.knowledge.prompt_assembly import build_system_blocks

    cfg = load_config(REAL_PIPELINE_TOML)
    knowledge_cfg = load_knowledge_config(REAL_PIPELINE_TOML)

    blocks_a = build_system_blocks(cfg, knowledge_cfg, "klanker-maker")
    blocks_b = build_system_blocks(cfg, knowledge_cfg, "klanker-maker")

    assert blocks_a[0]["text"] == blocks_b[0]["text"]


def test_build_system_blocks_block0_contains_persona_style_and_topic_hooks():
    from klanker_voice.knowledge.prompt_assembly import build_system_blocks

    cfg = load_config(REAL_PIPELINE_TOML)
    knowledge_cfg = load_knowledge_config(REAL_PIPELINE_TOML)

    blocks = build_system_blocks(cfg, knowledge_cfg, "klanker-maker")
    block0_text = blocks[0]["text"]

    assert "KPH" in block0_text  # persona (concierge.md)
    assert "PG-13" in block0_text or "public-mic" in block0_text.lower()  # style guardrail
    assert "klanker-maker" in block0_text  # topic-map hook


def test_build_system_blocks_block1_is_selected_topic_pack():
    from klanker_voice.knowledge.prompt_assembly import build_system_blocks

    cfg = load_config(REAL_PIPELINE_TOML)
    knowledge_cfg = load_knowledge_config(REAL_PIPELINE_TOML)

    blocks = build_system_blocks(cfg, knowledge_cfg, "klanker-maker")
    pack_text = knowledge_cfg.packs_dir.joinpath("klanker-maker.md").read_text(encoding="utf-8")

    assert blocks[1]["text"] == pack_text


def test_build_system_blocks_unknown_topic_raises():
    from klanker_voice.knowledge.prompt_assembly import build_system_blocks

    cfg = load_config(REAL_PIPELINE_TOML)
    knowledge_cfg = load_knowledge_config(REAL_PIPELINE_TOML)

    with pytest.raises(ValueError, match="unknown"):
        build_system_blocks(cfg, knowledge_cfg, "not-a-real-topic")


def _real_anthropic_key() -> str | None:
    """A real (non-dummy) Anthropic key, loaded from apps/voice/.env like
    harness/judge.py does -- count_tokens is a genuine network call."""
    try:
        from dotenv import load_dotenv

        load_dotenv(APP_ROOT / ".env", override=False)
    except ImportError:  # pragma: no cover -- dotenv is always a dep here
        pass
    key = os.environ.get("ANTHROPIC_API_KEY")
    return key if key and key.startswith("sk-ant-") else None


@pytest.mark.skipif(
    not _real_anthropic_key(), reason="ANTHROPIC_API_KEY not set (live network test)"
)
def test_count_tokens_block0_crosses_cache_floor_floor():
    from klanker_voice.knowledge.prompt_assembly import build_system_blocks, count_tokens

    cfg = load_config(REAL_PIPELINE_TOML)
    knowledge_cfg = load_knowledge_config(REAL_PIPELINE_TOML)

    blocks = build_system_blocks(cfg, knowledge_cfg, "klanker-maker")
    tokens = count_tokens(blocks[0]["text"], model=cfg.llm.model)

    assert tokens >= knowledge_cfg.cache_floor


def test_count_tokens_missing_key_raises_actionable_error(monkeypatch):
    from klanker_voice.knowledge.prompt_assembly import count_tokens

    monkeypatch.delenv("ANTHROPIC_API_KEY", raising=False)
    with pytest.raises(RuntimeError, match="ANTHROPIC_API_KEY"):
        count_tokens("hello world")


# ---------------------------------------------------------------------------
# advisory_lint (Task 2) -- flags, never blocks (Amendment 3-E/D-02)
# ---------------------------------------------------------------------------


def test_advisory_lint_flags_account_id_and_role_arn():
    from klanker_voice.knowledge.lint import advisory_lint

    text = (
        "Do not say the account id 052251888500 or the role arn "
        "arn:aws:iam::052251888500:role/kmv-github-delegate aloud."
    )
    findings = advisory_lint(text)
    patterns = {f.pattern for f in findings}
    assert "aws_account_id" in patterns
    assert "role_arn" in patterns


def test_advisory_lint_flags_internal_hostname():
    from klanker_voice.knowledge.lint import advisory_lint

    text = "reach it at bridge.internal.klankermaker.ai or foo.local"
    findings = advisory_lint(text)
    patterns = {f.pattern for f in findings}
    assert "internal_hostname" in patterns


def test_advisory_lint_clean_text_returns_no_findings():
    from klanker_voice.knowledge.lint import advisory_lint

    findings = advisory_lint("klanker-maker is a Go CLI that builds AWS sandboxes.")
    assert findings == []


def test_advisory_lint_never_raises_on_arbitrary_text():
    from klanker_voice.knowledge.lint import advisory_lint

    # Garbage input, binary-ish text, empty string -- advisory_lint is a
    # flag-only lint (Amendment 3-E); it must never raise.
    for text in ["", "\x00\x01\x02", "a" * 10000, "🍆🧌"]:
        findings = advisory_lint(text)
        assert isinstance(findings, list)
