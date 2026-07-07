"""Unit tests for klanker_voice.knowledge.router (Phase 7 Amendment 1,
RESEARCH Pattern 2, Pitfalls 1/2).

Wave-0 note (Task 1, 07-01-PLAN.md): these import from
``klanker_voice.knowledge.router``, which does not exist until Task 3 -- this
whole file is RED (ImportError) immediately after Task 1's commit.
"""

from __future__ import annotations

from pathlib import Path

import pytest
import yaml

from pipecat.frames.frames import TranscriptionFrame, TTSSpeakFrame
from pipecat.processors.frame_processor import FrameDirection
from pipecat.tests.utils import run_test

from klanker_voice.config import (
    FluxConfig,
    KnowledgeConfig,
    LlmConfig,
    PersonaConfig,
    PipelineConfig,
    SttConfig,
    TtsConfig,
    TurnConfig,
)

TOPIC_MAP = {
    "version": 1,
    "confidence_floor": 2,
    "topics": [
        {
            "id": "klanker-maker",
            "spoken_name": "klanker-maker",
            "hook": "Kurt's AI-agent sandbox runtime.",
            "keywords": [
                {"term": "klanker maker", "weight": 3},
                {"term": "klanker", "weight": 2},
                {"term": "km", "weight": 1},
                {"term": "sandbox", "weight": 1},
            ],
        },
        {
            "id": "defcon-run-34",
            "spoken_name": "DEFCON dot run, thirty-four",
            "hook": "Kurt's DEFCON running community.",
            "keywords": [
                {"term": "defcon run", "weight": 3},
                {"term": "defcon", "weight": 2},
                {"term": "run", "weight": 1},
            ],
        },
    ],
}


def _cfg(tmp_path: Path) -> PipelineConfig:
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


def _knowledge_cfg(tmp_path: Path) -> KnowledgeConfig:
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
    topic_map_path.write_text(yaml.safe_dump(TOPIC_MAP), encoding="utf-8")
    return KnowledgeConfig(
        manifest_path=manifest,
        topic_map_path=topic_map_path,
        packs_dir=tmp_path / "topics",
        style_path=tmp_path / "style.md",
        cache_min_tokens=10,
    )


# ---------------------------------------------------------------------------
# classify() -- pure keyword-weighted scoring (Task 1 RED, Task 3 GREEN)
# ---------------------------------------------------------------------------


def test_classify_directed_km_utterance_returns_km_above_floor():
    from klanker_voice.knowledge.router import classify

    topic_id, confidence = classify("tell me about klanker maker", TOPIC_MAP)
    assert topic_id == "klanker-maker"
    assert confidence >= TOPIC_MAP["confidence_floor"]


def test_classify_ambiguous_utterance_returns_none_low_confidence():
    from klanker_voice.knowledge.router import classify

    topic_id, confidence = classify("what's the weather like today", TOPIC_MAP)
    assert topic_id is None
    assert confidence < TOPIC_MAP["confidence_floor"]


def test_classify_below_confidence_floor_declines_rather_than_guessing():
    from klanker_voice.knowledge.router import classify

    # "km" alone is weight 1, below the floor of 2 -- must not guess.
    topic_id, confidence = classify("do you know km", TOPIC_MAP)
    assert topic_id is None
    assert confidence == 1


def test_classify_picks_highest_scoring_topic_on_overlap():
    from klanker_voice.knowledge.router import classify

    topic_id, _ = classify("let's talk about the defcon run community", TOPIC_MAP)
    assert topic_id == "defcon-run-34"


# ---------------------------------------------------------------------------
# KnowledgeRouterProcessor (Task 3)
# ---------------------------------------------------------------------------


class _FakeLLM:
    """Stand-in for AnthropicLLMService -- only ``_settings.system_instruction``
    needs to exist for apply_system_blocks to mutate."""

    class _Settings:
        system_instruction = None

    def __init__(self):
        self._settings = self._Settings()


async def _never_fallback(utterance, topics, *, model):
    return None


class TestKnowledgeRouterProcessor:
    async def test_ack_fires_on_first_topic_switch(self, tmp_path):
        from klanker_voice.knowledge.router import KnowledgeRouterProcessor

        cfg = _cfg(tmp_path)
        knowledge_cfg = _knowledge_cfg(tmp_path)
        llm = _FakeLLM()
        router = KnowledgeRouterProcessor(
            cfg=cfg,
            knowledge_cfg=knowledge_cfg,
            llm=llm,
            initial_topic="klanker-maker",
            fallback_classify=_never_fallback,
        )

        frame = TranscriptionFrame(
            text="let's talk about the defcon run community",
            user_id="u1",
            timestamp="2026-07-07T00:00:00Z",
        )
        down, _ = await run_test(router, frames_to_send=[frame])

        assert any(isinstance(f, TTSSpeakFrame) for f in down)
        assert any(isinstance(f, TranscriptionFrame) for f in down)
        assert llm._settings.system_instruction[1]["text"] == "defcon deep pack.\n"

    async def test_no_ack_on_same_topic_followup(self, tmp_path):
        from klanker_voice.knowledge.router import KnowledgeRouterProcessor

        cfg = _cfg(tmp_path)
        knowledge_cfg = _knowledge_cfg(tmp_path)
        llm = _FakeLLM()
        router = KnowledgeRouterProcessor(
            cfg=cfg,
            knowledge_cfg=knowledge_cfg,
            llm=llm,
            initial_topic="klanker-maker",
            fallback_classify=_never_fallback,
        )

        frame = TranscriptionFrame(
            text="tell me more about klanker maker",
            user_id="u1",
            timestamp="2026-07-07T00:00:00Z",
        )
        down, _ = await run_test(router, frames_to_send=[frame])

        assert not any(isinstance(f, TTSSpeakFrame) for f in down)
        assert any(isinstance(f, TranscriptionFrame) for f in down)

    async def test_no_ack_on_shallow_one_liner_below_confidence_floor(self, tmp_path):
        """Pitfall 2: a question the stable prefix can already answer must
        never trigger the ack -- the router declines rather than guessing."""
        from klanker_voice.knowledge.router import KnowledgeRouterProcessor

        cfg = _cfg(tmp_path)
        knowledge_cfg = _knowledge_cfg(tmp_path)
        llm = _FakeLLM()
        router = KnowledgeRouterProcessor(
            cfg=cfg,
            knowledge_cfg=knowledge_cfg,
            llm=llm,
            initial_topic="klanker-maker",
            fallback_classify=_never_fallback,
        )

        frame = TranscriptionFrame(
            text="what's your name", user_id="u1", timestamp="2026-07-07T00:00:00Z"
        )
        down, _ = await run_test(router, frames_to_send=[frame])

        assert not any(isinstance(f, TTSSpeakFrame) for f in down)

    async def test_low_confidence_uses_fallback_classify_then_switches(self, tmp_path):
        from klanker_voice.knowledge.router import KnowledgeRouterProcessor

        cfg = _cfg(tmp_path)
        knowledge_cfg = _knowledge_cfg(tmp_path)
        llm = _FakeLLM()

        async def _fallback(utterance, topics, *, model):
            return "defcon-run-34"

        router = KnowledgeRouterProcessor(
            cfg=cfg,
            knowledge_cfg=knowledge_cfg,
            llm=llm,
            initial_topic="klanker-maker",
            fallback_classify=_fallback,
        )

        frame = TranscriptionFrame(
            text="ambiguous question with no keywords",
            user_id="u1",
            timestamp="2026-07-07T00:00:00Z",
        )
        down, _ = await run_test(router, frames_to_send=[frame])

        assert any(isinstance(f, TTSSpeakFrame) for f in down)
        assert llm._settings.system_instruction[1]["text"] == "defcon deep pack.\n"

    async def test_low_confidence_fallback_also_declines_no_switch_no_ack(self, tmp_path):
        from klanker_voice.knowledge.router import KnowledgeRouterProcessor

        cfg = _cfg(tmp_path)
        knowledge_cfg = _knowledge_cfg(tmp_path)
        llm = _FakeLLM()
        router = KnowledgeRouterProcessor(
            cfg=cfg,
            knowledge_cfg=knowledge_cfg,
            llm=llm,
            initial_topic="klanker-maker",
            fallback_classify=_never_fallback,
        )

        frame = TranscriptionFrame(
            text="ambiguous question with no keywords",
            user_id="u1",
            timestamp="2026-07-07T00:00:00Z",
        )
        down, _ = await run_test(router, frames_to_send=[frame])

        assert not any(isinstance(f, TTSSpeakFrame) for f in down)
        assert llm._settings.system_instruction is None  # never touched -- stayed on initial

    async def test_non_transcription_frames_pass_through_unchanged(self, tmp_path):
        from pipecat.frames.frames import UserStartedSpeakingFrame

        from klanker_voice.knowledge.router import KnowledgeRouterProcessor

        cfg = _cfg(tmp_path)
        knowledge_cfg = _knowledge_cfg(tmp_path)
        llm = _FakeLLM()
        router = KnowledgeRouterProcessor(
            cfg=cfg,
            knowledge_cfg=knowledge_cfg,
            llm=llm,
            initial_topic="klanker-maker",
            fallback_classify=_never_fallback,
        )

        down, _ = await run_test(router, frames_to_send=[UserStartedSpeakingFrame()])

        assert any(isinstance(f, UserStartedSpeakingFrame) for f in down)
        assert not any(isinstance(f, TTSSpeakFrame) for f in down)
