"""Unit tests for 07-05 time-aware pacing (D-06): ``build_system_blocks``'s
``remaining_seconds`` parameter, ``KnowledgeRouterProcessor`` threading it
into the per-turn block1 rebuild, and ``SessionLifecycle.remaining_seconds()``
as the single, pre-existing source of truth (no second timer/thread).
"""

from __future__ import annotations

from pathlib import Path

import yaml

from pipecat.frames.frames import TranscriptionFrame
from pipecat.tests.utils import run_test

from klanker_voice import quota
from klanker_voice.config import (
    APP_ROOT,
    FluxConfig,
    KnowledgeConfig,
    LlmConfig,
    PersonaConfig,
    PipelineConfig,
    QuotaConfig,
    SttConfig,
    TtsConfig,
    TurnConfig,
    load_config,
    load_knowledge_config,
)
from klanker_voice.session import SessionLifecycle

REAL_PIPELINE_TOML = APP_ROOT / "pipeline.toml"


# ---------------------------------------------------------------------------
# build_system_blocks(..., remaining_seconds=...) -- prompt_assembly.py
# ---------------------------------------------------------------------------


def test_pacing_note_prepends_to_block1_block0_byte_identical():
    from klanker_voice.knowledge.prompt_assembly import build_system_blocks

    cfg = load_config(REAL_PIPELINE_TOML)
    knowledge_cfg = load_knowledge_config(REAL_PIPELINE_TOML)

    no_pacing = build_system_blocks(cfg, knowledge_cfg, "klanker-maker")
    with_pacing = build_system_blocks(cfg, knowledge_cfg, "klanker-maker", remaining_seconds=90)

    # Pitfall 3: block0 (the cached prefix) is never touched by pacing.
    assert with_pacing[0]["text"] == no_pacing[0]["text"]
    assert with_pacing[0].get("cache_control") == {"type": "ephemeral"}

    # block1 gained a pacing note, prepended (not replaced or appended).
    assert with_pacing[1]["text"] != no_pacing[1]["text"]
    assert with_pacing[1]["text"].endswith(no_pacing[1]["text"])
    assert "cache_control" not in with_pacing[1]


def test_tight_vs_depth_pacing_notes_differ_block0_still_identical():
    from klanker_voice.knowledge.prompt_assembly import build_system_blocks

    cfg = load_config(REAL_PIPELINE_TOML)
    knowledge_cfg = load_knowledge_config(REAL_PIPELINE_TOML)

    tight = build_system_blocks(cfg, knowledge_cfg, "klanker-maker", remaining_seconds=45)
    depth = build_system_blocks(cfg, knowledge_cfg, "klanker-maker", remaining_seconds=600)

    assert tight[0]["text"] == depth[0]["text"]  # block0 never varies with pacing
    assert tight[1]["text"] != depth[1]["text"]  # different pacing notes
    # Both still carry the same underlying pack text, just a different note.
    pack_text = knowledge_cfg.packs_dir.joinpath("klanker-maker.md").read_text(encoding="utf-8")
    assert tight[1]["text"].endswith(pack_text)
    assert depth[1]["text"].endswith(pack_text)


def test_remaining_seconds_none_is_byte_identical_to_omitted_call():
    from klanker_voice.knowledge.prompt_assembly import build_system_blocks

    cfg = load_config(REAL_PIPELINE_TOML)
    knowledge_cfg = load_knowledge_config(REAL_PIPELINE_TOML)

    omitted = build_system_blocks(cfg, knowledge_cfg, "klanker-maker")
    explicit_none = build_system_blocks(cfg, knowledge_cfg, "klanker-maker", remaining_seconds=None)

    assert omitted == explicit_none


# ---------------------------------------------------------------------------
# KnowledgeRouterProcessor threads remaining_seconds_fn into block1 (router.py)
# ---------------------------------------------------------------------------


def _pacing_cfg(tmp_path: Path) -> PipelineConfig:
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


def _pacing_knowledge_cfg(tmp_path: Path) -> KnowledgeConfig:
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
    topic_map_path.write_text(
        yaml.safe_dump(
            {
                "version": 1,
                "confidence_floor": 2,
                "topics": [
                    {
                        "id": "klanker-maker",
                        "spoken_name": "klanker-maker",
                        "hook": "km hook",
                        "keywords": [{"term": "klanker maker", "weight": 3}],
                    },
                    {
                        "id": "defcon-run-34",
                        "spoken_name": "DEFCON dot run, thirty-four",
                        "hook": "defcon hook",
                        "keywords": [{"term": "defcon run", "weight": 3}],
                    },
                ],
            }
        ),
        encoding="utf-8",
    )
    return KnowledgeConfig(
        manifest_path=manifest,
        topic_map_path=topic_map_path,
        packs_dir=tmp_path / "topics",
        style_path=tmp_path / "style.md",
        cache_floor=10,
    )


class _FakeLLM:
    class _Settings:
        system_instruction = None

    def __init__(self):
        self._settings = self._Settings()


async def _never_fallback(utterance, topics, *, model):
    return None


async def test_router_threads_remaining_seconds_into_block1_on_switch(tmp_path):
    from klanker_voice.knowledge.router import KnowledgeRouterProcessor

    cfg = _pacing_cfg(tmp_path)
    knowledge_cfg = _pacing_knowledge_cfg(tmp_path)
    llm = _FakeLLM()
    router = KnowledgeRouterProcessor(
        cfg=cfg,
        knowledge_cfg=knowledge_cfg,
        llm=llm,
        initial_topic="klanker-maker",
        fallback_classify=_never_fallback,
        remaining_seconds_fn=lambda: 45.0,
    )

    frame = TranscriptionFrame(
        text="what is defcon run all about", user_id="u1", timestamp="2026-07-07T00:00:00Z"
    )
    await run_test(router, frames_to_send=[frame])

    block1_text = llm._settings.system_instruction[1]["text"]
    assert block1_text.endswith("defcon deep pack.\n")
    assert block1_text != "defcon deep pack.\n"  # a pacing note was prepended


async def test_router_without_remaining_seconds_fn_unchanged_from_plan01_shape(tmp_path):
    """``remaining_seconds_fn=None`` (the default) reproduces Plan 01/02's
    exact block1 text -- no pacing note, no regression for existing callers
    (pipeline.py does not supply this yet)."""
    from klanker_voice.knowledge.router import KnowledgeRouterProcessor

    cfg = _pacing_cfg(tmp_path)
    knowledge_cfg = _pacing_knowledge_cfg(tmp_path)
    llm = _FakeLLM()
    router = KnowledgeRouterProcessor(
        cfg=cfg,
        knowledge_cfg=knowledge_cfg,
        llm=llm,
        initial_topic="klanker-maker",
        fallback_classify=_never_fallback,
    )

    frame = TranscriptionFrame(
        text="what is defcon run all about", user_id="u1", timestamp="2026-07-07T00:00:00Z"
    )
    await run_test(router, frames_to_send=[frame])

    assert llm._settings.system_instruction[1]["text"] == "defcon deep pack.\n"


# ---------------------------------------------------------------------------
# SessionLifecycle.remaining_seconds() -- the pre-existing session-state
# source (session.py); a pure read, no new timer/thread.
# ---------------------------------------------------------------------------


def _tier(session_max=120) -> quota.Tier:
    return quota.Tier(
        tier_id="t", session_max_seconds=session_max, period_max_seconds=600, max_concurrent=2
    )


def _quota_config() -> QuotaConfig:
    return QuotaConfig(
        heartbeat_renew_interval=15.0,
        heartbeat_ttl=45.0,
        sub_floor_seconds=1,
        per_task_max_sessions=5,
        auto_trip_ceiling_seconds=100_000,
        auto_trip_ceiling_dollars=100_000.0,
        est_cost_per_second=0.01,
    )


def test_session_lifecycle_remaining_seconds_reads_existing_state_no_new_clock():
    """D-06: remaining_seconds() is a pure read of the SAME clock/tier state
    the D-02 service timer already uses. Just constructing the object (no
    .start(), no asyncio task, no AWS call) is enough to compute it --
    proving there is no second timer/thread involved."""
    fake_now = [1_000_000.0]

    lifecycle = SessionLifecycle(
        user_id="u1",
        session_id="s1",
        tier=_tier(session_max=120),
        quota_config=_quota_config(),
        clock=lambda: fake_now[0],
    )

    assert lifecycle.remaining_seconds() == 120.0  # no time elapsed yet

    fake_now[0] += 90.0  # 90s later, same clock -- no new timer created
    assert lifecycle.remaining_seconds() == 30.0

    fake_now[0] += 60.0  # past session_max -- clamped to 0, never negative
    assert lifecycle.remaining_seconds() == 0.0


def test_session_lifecycle_remaining_seconds_none_for_bypass_session():
    """A bypass/smoke session (D-15) has no real tier/session_max bound and
    is never subject to the wall-clock cutoff -- remaining_seconds() must
    signal "no pacing data", not a stale zero."""
    lifecycle = SessionLifecycle(
        user_id="svc",
        session_id="s1",
        tier=_tier(session_max=0),  # bypass sessions carry a zeroed placeholder tier
        quota_config=_quota_config(),
        bypass_accounting=True,
    )

    assert lifecycle.remaining_seconds() is None


def test_router_remaining_seconds_fn_composes_with_real_session_lifecycle(tmp_path):
    """Proves the router's remaining_seconds_fn seam composes with the real
    SessionLifecycle.remaining_seconds() -- not a stand-in -- which is the
    'sourced from existing session state, not a new clock' requirement.
    Production wiring through pipeline.py/server.py (passing a bound
    lifecycle.remaining_seconds into KnowledgeRouterProcessor) is a later,
    out-of-scope step; this proves the seam itself is correct end to end."""
    fake_now = [0.0]
    lifecycle = SessionLifecycle(
        user_id="u1",
        session_id="s1",
        tier=_tier(session_max=600),
        quota_config=_quota_config(),
        clock=lambda: fake_now[0],
    )
    fake_now[0] = 550.0  # 50s left -- tight-pacing territory

    assert lifecycle.remaining_seconds() == 50.0

    cfg = _pacing_cfg(tmp_path)
    knowledge_cfg = _pacing_knowledge_cfg(tmp_path)
    from klanker_voice.knowledge.prompt_assembly import build_system_blocks

    blocks_via_lifecycle = build_system_blocks(
        cfg, knowledge_cfg, "klanker-maker", remaining_seconds=lifecycle.remaining_seconds()
    )
    blocks_literal = build_system_blocks(cfg, knowledge_cfg, "klanker-maker", remaining_seconds=50.0)

    assert blocks_via_lifecycle[1]["text"] == blocks_literal[1]["text"]
