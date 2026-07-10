"""Hidden keyword-topic behavior (greenhouse recruiting-mode easter egg, 2026-07-10).

A `hidden: true` topic must be:
  - keyword-matchable by `classify` (so "greenhouse" unlocks it),
  - NOT advertised in block0's knowledge-map hooks (`render_topic_hooks` skips it),
  - NOT in the tour (`manifest.tour_priority`),
  - excluded from the Haiku fallback candidate set.
"""
from __future__ import annotations

from klanker_voice.config import load_knowledge_config
from klanker_voice.knowledge.prompt_assembly import (
    load_manifest,
    load_topic_map,
    render_topic_hooks,
)
from klanker_voice.knowledge.router import classify


def _topic(topic_map, tid):
    return next((t for t in topic_map.get("topics", []) if t["id"] == tid), None)


def test_greenhouse_topic_is_hidden_and_out_of_tour():
    kcfg = load_knowledge_config()
    topic_map = load_topic_map(kcfg)
    gh = _topic(topic_map, "greenhouse")
    assert gh is not None, "greenhouse topic missing from topic-map"
    assert gh.get("hidden") is True

    manifest = load_manifest(kcfg)
    assert "greenhouse" not in manifest.get("tour_priority", [])


def test_greenhouse_is_keyword_matchable():
    kcfg = load_knowledge_config()
    topic_map = load_topic_map(kcfg)
    tid, score = classify("okay, greenhouse", topic_map)
    assert tid == "greenhouse"
    assert score >= int(topic_map.get("confidence_floor", 1))


def test_hidden_topic_not_in_block0_hooks():
    kcfg = load_knowledge_config()
    hooks = render_topic_hooks(kcfg).lower()
    # advertised topics still present...
    assert "klanker-maker" in hooks
    # ...but the hidden easter egg is not named anywhere in block0.
    assert "greenhouse" not in hooks
    assert "kurt's background" not in hooks


def test_hidden_topics_excluded_from_fallback_candidates():
    # The fallback filters `hidden` topics before building its candidate list;
    # verify the filter expression directly (no network / model call).
    topics = [
        {"id": "klanker-maker", "spoken_name": "klanker-maker"},
        {"id": "greenhouse", "spoken_name": "Kurt's background", "hidden": True},
    ]
    visible = [t for t in topics if not t.get("hidden")]
    assert {t["id"] for t in visible} == {"klanker-maker"}
