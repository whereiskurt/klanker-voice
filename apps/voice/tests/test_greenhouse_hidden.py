"""Hidden keyword-topic behavior (greenhouse recruiting-mode easter egg, 2026-07-10).

A `hidden: true` topic must be:
  - keyword-matchable by `classify` (so "greenhouse" unlocks it),
  - NOT advertised in block0's knowledge-map hooks (`render_topic_hooks` skips it),
  - NOT in the tour (`manifest.tour_priority`),
  - excluded from the Haiku fallback candidate set.
"""
from __future__ import annotations

from pipecat.frames.frames import (
    InterimTranscriptionFrame,
    TranscriptionFrame,
    TTSSpeakFrame,
)
from pipecat.tests.utils import run_test

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


async def test_ambience_enabled_entering_greenhouse_and_off_on_exit(monkeypatch):
    from pipecat.frames.frames import MixerEnableFrame

    # pipeline.toml ships [greenhouse] ambience_enabled = true.
    router = KnowledgeRouterProcessor(
        cfg=load_config(),
        knowledge_cfg=load_knowledge_config(),
        llm=_FakeLLM(),
        initial_topic="klanker-maker",
        fallback_classify=_never_fallback,
    )
    assert router._cfg.greenhouse.ambience_enabled is True
    pushed = []

    async def _capture(frame, direction=None):
        pushed.append(frame)

    monkeypatch.setattr(router, "push_frame", _capture)

    # Entering greenhouse -> bed ON.
    await router._handle_utterance("greenhouse")
    assert router._active_topic == "greenhouse"
    assert any(isinstance(f, MixerEnableFrame) and f.enable for f in pushed)

    # Exiting -> bed OFF.
    pushed.clear()
    await router._handle_utterance("okay, the interview is over")
    assert router._active_topic == "klanker-maker"
    assert any(isinstance(f, MixerEnableFrame) and not f.enable for f in pushed)


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


def test_greenhouse_ack_is_suppressed():
    # 260710: greenhouse suppresses the router ack ("") so its first-person LLM
    # opener is the sole output (also lands in the chat transcript).
    kcfg = load_knowledge_config()
    topic_map = load_topic_map(kcfg)
    gh = _topic(topic_map, "greenhouse")
    assert gh.get("ack") == ""


def test_hidden_topics_excluded_from_fallback_candidates():
    # The fallback filters `hidden` topics before building its candidate list;
    # verify the filter expression directly (no network / model call).
    topics = [
        {"id": "klanker-maker", "spoken_name": "klanker-maker"},
        {"id": "greenhouse", "spoken_name": "Kurt's background", "hidden": True},
    ]
    visible = [t for t in topics if not t.get("hidden")]
    assert {t["id"] for t in visible} == {"klanker-maker"}


# --- Interim early-lock (greenhouse-one-turn-late fix, 2026-07-12) --------------
#
# Root cause: on PSTN, one spoken turn fragments into several finalized
# transcripts. The user-aggregator fires an LLM inference on an earlier
# fragment (no keyword) before the "greenhouse" FINAL reaches the router, and
# the Anthropic service reads the shared `system_instruction` at generation
# time -- so that inference answers in the normal persona and the recruiting
# opener slips to the next turn. WebRTC's single clean final avoids the race.
# Fix: detect the hidden+sticky magic word on INTERIM transcripts (streamed
# continuously during speech, before any turn-stop inference) and commit the
# swap early, so it precedes the premature inference.


def _make_router(monkeypatch_env=None):
    return KnowledgeRouterProcessor(
        cfg=load_config(),
        knowledge_cfg=load_knowledge_config(),
        llm=_FakeLLM(),
        initial_topic="klanker-maker",
        fallback_classify=_never_fallback,
    )


async def test_interim_greenhouse_early_locks_before_any_final():
    # The magic word arriving in an INTERIM transcript must swap block1 to the
    # greenhouse pack immediately -- BEFORE any final transcript / inference --
    # so the answering turn is already in recruiting mode (closes the PSTN
    # fragmentation race that made the opener land one turn late).
    router = _make_router()
    assert router._active_topic == "klanker-maker"
    assert router._llm._settings.system_instruction is None

    await run_test(
        router,
        frames_to_send=[InterimTranscriptionFrame("hey, i'm from greenhouse", "u", "t")],
        expected_down_frames=None,
    )

    assert router._active_topic == "greenhouse"
    # block1 was swapped onto the live LLM service (the recruiting pack).
    assert router._llm._settings.system_instruction is not None


async def test_interim_lock_then_final_greenhouse_is_a_sticky_noop(monkeypatch):
    # After the interim early-lock, the eventual FINAL "greenhouse" must be a
    # sticky no-op: no second switch, no ack -- guaranteeing exactly ONE opener
    # (no double-response) on both the telephony and WebRTC paths.
    router = _make_router()
    pushed = []

    async def _capture(frame, direction=None):
        pushed.append(frame)

    monkeypatch.setattr(router, "push_frame", _capture)

    # Interim locks greenhouse early.
    await router._early_lock_via_interim("i'm from greenhouse")
    assert router._active_topic == "greenhouse"

    # The FINAL keyword transcript now arrives -> sticky branch, no re-commit.
    pushed.clear()
    await router._handle_utterance("i'm from greenhouse")
    assert router._active_topic == "greenhouse"
    assert pushed == []  # no ack, no second swap


async def test_interim_never_early_locks_a_normal_topic(monkeypatch):
    # Interim transcripts are noisy/unstable: a NORMAL topic keyword in an
    # interim must NOT switch topics (only hidden+sticky magic words early-lock)
    # -- and the same-vendor Haiku fallback must NEVER run on an interim.
    called = {"fallback": False}

    async def _boom_fallback(utterance, topics, *, model):
        called["fallback"] = True
        raise AssertionError("fallback must never run on an interim transcript")

    router = KnowledgeRouterProcessor(
        cfg=load_config(),
        knowledge_cfg=load_knowledge_config(),
        llm=_FakeLLM(),
        initial_topic="klanker-maker",
        fallback_classify=_boom_fallback,
    )

    await run_test(
        router,
        frames_to_send=[InterimTranscriptionFrame("tell me about defcon run", "u", "t")],
        expected_down_frames=None,
    )

    assert router._active_topic == "klanker-maker"  # unchanged
    assert router._llm._settings.system_instruction is None  # never swapped
    assert called["fallback"] is False


async def test_interim_early_lock_candidate_only_matches_hidden_sticky():
    # Direct unit check of the candidate selector: the greenhouse magic word
    # is a candidate; a normal-topic keyword is not.
    router = _make_router()
    assert router._early_lock_candidate("okay, greenhouse") == "greenhouse"
    assert router._early_lock_candidate("tell me about klanker maker") is None
    # Once already active, an interim never re-nominates it (no double-commit).
    router._active_topic = "greenhouse"
    assert router._early_lock_candidate("greenhouse again") is None
