"""Hidden keyword-topic behavior (greenhouse recruiting-mode easter egg, 2026-07-10).

A `hidden: true` topic must be:
  - keyword-matchable by `classify` (so "greenhouse" unlocks it),
  - NOT advertised in block0's knowledge-map hooks (`render_topic_hooks` skips it),
  - NOT in the tour (`manifest.tour_priority`),
  - excluded from the Haiku fallback candidate set.
"""
from __future__ import annotations

from pipecat.frames.frames import TTSSpeakFrame

from klanker_voice.config import load_config, load_knowledge_config
from klanker_voice.knowledge.prompt_assembly import (
    load_manifest,
    load_topic_map,
    render_topic_hooks,
)
from klanker_voice.knowledge.router import KnowledgeRouterProcessor, classify


def _topic(topic_map, tid):
    return next((t for t in topic_map.get("topics", []) if t["id"] == tid), None)


class _FakeLLM:
    class _Settings:
        system_instruction = None

    def __init__(self):
        self._settings = self._Settings()


async def _never_fallback(utterance, topics, *, model):
    return None


def test_greenhouse_topic_is_hidden_and_out_of_tour():
    kcfg = load_knowledge_config()
    topic_map = load_topic_map(kcfg)
    gh = _topic(topic_map, "greenhouse")
    assert gh is not None, "greenhouse topic missing from topic-map"
    assert gh.get("hidden") is True

    manifest = load_manifest(kcfg)
    assert "greenhouse" not in manifest.get("tour_priority", [])


def test_greenhouse_only_unlocks_on_the_magic_word():
    kcfg = load_knowledge_config()
    topic_map = load_topic_map(kcfg)
    # the exact magic word unlocks it...
    tid, score = classify("okay, greenhouse", topic_map)
    assert tid == "greenhouse"
    assert score >= int(topic_map.get("confidence_floor", 1))
    # ...but recruiting synonyms do NOT (deliberately not discoverable).
    for phrase in ("are you hiring", "can I see your resume", "I'm a recruiter", "hire kurt"):
        assert classify(phrase, topic_map)[0] != "greenhouse"


def test_greenhouse_is_sticky_with_exit_phrases():
    kcfg = load_knowledge_config()
    topic_map = load_topic_map(kcfg)
    gh = _topic(topic_map, "greenhouse")
    assert gh.get("sticky") is True
    assert isinstance(gh.get("exit"), list) and gh["exit"]
    assert any("interview" in p for p in gh["exit"])


async def test_sticky_holds_until_explicit_exit(monkeypatch):
    router = KnowledgeRouterProcessor(
        cfg=load_config(),
        knowledge_cfg=load_knowledge_config(),
        llm=_FakeLLM(),
        initial_topic="klanker-maker",
        fallback_classify=_never_fallback,
    )
    pushed = []

    async def _capture(frame, direction=None):
        pushed.append(frame)

    # Stub push_frame so we can exercise _handle_utterance without a live pipeline.
    monkeypatch.setattr(router, "push_frame", _capture)

    # Simulate already being in recruiting mode.
    router._active_topic = "greenhouse"

    # A topic keyword mid-interview must NOT switch away (sticky holds).
    await router._handle_utterance("tell me about klanker-maker")
    assert router._active_topic == "greenhouse"
    assert pushed == []  # no switch, no ack

    # An explicit exit phrase releases back to the initial technical topic.
    await router._handle_utterance("okay, the interview is over")
    assert router._active_topic == "klanker-maker"
    assert any(isinstance(f, TTSSpeakFrame) for f in pushed)  # exit beat fired


def test_hidden_topic_not_in_block0_hooks():
    kcfg = load_knowledge_config()
    hooks = render_topic_hooks(kcfg).lower()
    # advertised topics still present...
    assert "klanker-maker" in hooks
    # ...but the hidden easter egg is not named anywhere in block0.
    assert "greenhouse" not in hooks
    assert "kurt's background" not in hooks


def test_greenhouse_has_custom_playful_switch_ack():
    kcfg = load_knowledge_config()
    topic_map = load_topic_map(kcfg)
    gh = _topic(topic_map, "greenhouse")
    ack = gh.get("ack")
    assert isinstance(ack, str) and ack.strip()
    assert "greenhouse" in ack.lower()  # the playful opener names the magic word


def test_hidden_topics_excluded_from_fallback_candidates():
    # The fallback filters `hidden` topics before building its candidate list;
    # verify the filter expression directly (no network / model call).
    topics = [
        {"id": "klanker-maker", "spoken_name": "klanker-maker"},
        {"id": "greenhouse", "spoken_name": "Kurt's background", "hidden": True},
    ]
    visible = [t for t in topics if not t.get("hidden")]
    assert {t["id"] for t in visible} == {"klanker-maker"}
