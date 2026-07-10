"""KnowledgeRouterProcessor: keyword-first topic router + confidence-floor
fallback + ack-on-switch (Amendment 1, RESEARCH Pattern 2, Pitfalls 1/2).

Sits between the STT service and the ``LLMContextAggregatorPair`` in
``pipeline.build_pipeline`` (the RESEARCH-stated insertion point). It
classifies each finalized ``TranscriptionFrame`` against
``knowledge/router/topic-map.yaml``'s weighted keyword lists; below the
confidence floor it falls back to a single same-vendor Haiku classification
call (PIPE-07: never a 4th vendor) rather than guessing (Pitfall 1). On a
genuine DIFFERENT-topic switch it fires the "let's dig into it" ack (a
deterministic ``TTSSpeakFrame``, matching ``pipeline.speak_goodbye``'s
pattern) and swaps block1 on the live LLM service to the new topic's deep
pack -- block0 is never touched (Pitfall 3). Same-topic follow-ups and
shallow one-liners the stable prefix can already answer never trigger the
ack (Pitfall 2) and never swap the pack.

07-02 (Amendment 3-B/G): on that SAME genuine deep-turn switch -- never on a
shallow one-liner or same-topic follow-up -- this processor also queries a
:class:`klanker_voice.knowledge.retrieval.RetrievalIndex` (when one is
supplied and ``knowledge_cfg.retrieval_enabled``) for the newly-switched
topic and passes the returned chunks into
:func:`klanker_voice.knowledge.prompt_assembly.build_system_blocks`'s
``retrieved_chunks``. The query is local (tens of ms) and happens exactly
where the ack is already firing, so its cost is ack-masked -- no new network
hop before the ack (Amendment 3-G). ``retrieval_index=None`` (the default)
reproduces Plan 01's behavior unchanged.

07-05 (D-06, time-aware pacing): on that same deep-turn block1 rebuild, this
processor also calls an optional ``remaining_seconds_fn`` -- a zero-arg
callable a caller (e.g. server.py, from the session's own
:class:`klanker_voice.session.SessionLifecycle.remaining_seconds`) supplies
-- and threads its return value into ``build_system_blocks``'s
``remaining_seconds``. This is a READ, never a new timer: the callable is
invoked once, synchronously, at the moment of the switch.
``remaining_seconds_fn=None`` (the default) reproduces Plan 01/02's behavior
unchanged (no pacing note).
"""

from __future__ import annotations

import os
import re
from typing import Any, Awaitable, Callable

from pipecat.frames.frames import Frame, TranscriptionFrame, TTSSpeakFrame
from pipecat.processors.frame_processor import FrameDirection, FrameProcessor

from klanker_voice.config import KnowledgeConfig, PipelineConfig
from klanker_voice.knowledge.prompt_assembly import (
    apply_system_blocks,
    build_system_blocks,
    load_topic_map,
)
from klanker_voice.knowledge.retrieval import RetrievalIndex

#: Default ack templates (Amendment 1's "OK! Let's dig into it.", naturalized).
#: Rendered with the switched-to topic's spoken_name; one fires ONLY on a
#: genuine deep-pack switch (Pitfall 2), never on a same-topic follow-up. The
#: processor rotates through these round-robin (deterministic, testable) so the
#: "let me think about it" beat that masks the pack-swap + BM25 retrieval feels
#: human instead of a canned repeat. Every variant ends on ``{spoken_name}`` (or
#: leads into a beat) so the retrieval stays masked behind the spoken ack.
DEFAULT_ACK_TEMPLATES: list[str] = [
    "Ooh, {spoken_name} — good one. Let me get into it.",
    "Okay, let's dig into {spoken_name}.",
    "Right — {spoken_name}. Here's the deal.",
    "Love that one. So, {spoken_name}…",
]

FallbackClassifier = Callable[..., Awaitable["str | None"]]


def _normalize(text: str) -> str:
    return re.sub(r"[^a-z0-9 ]", " ", text.lower())


def classify(utterance: str, topic_map: dict) -> tuple[str | None, int]:
    """Weighted keyword-match classify (RESEARCH Pattern 2).

    Sums the weight of every topic-map keyword/alias found (as a substring)
    in the normalized utterance, per topic. Returns the highest-scoring
    topic id, or ``None`` when the best score is below
    ``topic_map["confidence_floor"]`` -- the router must never guess
    (Pitfall 1).

    Returns:
        ``(topic_id, confidence)`` -- ``topic_id`` is ``None`` below the
        floor; ``confidence`` is always the best raw score found (even when
        it's below the floor), so callers can log/inspect it.
    """
    floor = int(topic_map.get("confidence_floor", 1))
    normalized = _normalize(utterance)

    best_id: str | None = None
    best_score = 0
    for topic in topic_map.get("topics", []):
        score = 0
        for kw in topic.get("keywords", []):
            if isinstance(kw, dict):
                term, weight = kw.get("term", ""), int(kw.get("weight", 1))
            else:
                term, weight = str(kw), 1
            if term and _normalize(term) in normalized:
                score += weight
        if score > best_score:
            best_score = score
            best_id = topic["id"]

    if best_score < floor:
        return None, best_score
    return best_id, best_score


def _require_env(name: str) -> str:
    value = os.environ.get(name)
    if not value:
        raise RuntimeError(
            f"{name} is not set. Run `make -C apps/voice env` to write .env from SSM."
        )
    return value


async def default_haiku_fallback_classify(
    utterance: str, topics: list[dict], *, model: str = "claude-haiku-4-5"
) -> str | None:
    """Same-vendor Haiku classification fallback (PIPE-07: never a 4th
    vendor). Fired only when the keyword router is below ``confidence_floor``
    -- masked by the ack line when it does fire (Amendment 1's
    "thinking-partner" note), never a mandatory pre-step (RESEARCH
    Anti-Patterns).

    Returns the matched topic id, or ``None`` if Haiku also declines to
    commit to one (never guess, Pitfall 1).
    """
    from anthropic import AsyncAnthropic

    # Hidden topics (e.g. the `greenhouse` recruiting easter-egg) are reachable
    # ONLY by an explicit keyword match in `classify`, never by fuzzy Haiku
    # intent-classification -- otherwise an unrelated utterance could get routed
    # into a hidden pack. Exclude them from the fallback candidate set.
    topics = [t for t in topics if not t.get("hidden")]
    valid_ids = {t["id"] for t in topics}
    if not valid_ids:
        return None

    client = AsyncAnthropic(api_key=_require_env("ANTHROPIC_API_KEY"))
    topic_list = ", ".join(f"{t['id']} ({t.get('spoken_name', t['id'])})" for t in topics)
    prompt = (
        "You are a topic classifier for a voice assistant. Classify the spoken "
        f"utterance below into exactly one of these topic ids, or reply with the "
        f"single word NONE if it doesn't clearly match any: {topic_list}\n\n"
        f"Utterance: {utterance!r}\n\n"
        "Reply with ONLY the topic id, or NONE. No other words."
    )
    response = await client.messages.create(
        model=model,
        max_tokens=16,
        messages=[{"role": "user", "content": prompt}],
    )
    text = "".join(getattr(block, "text", "") for block in response.content).strip()
    return text if text in valid_ids else None


class KnowledgeRouterProcessor(FrameProcessor):
    """Classifies finalized transcriptions, swaps block1 on a genuine topic
    switch, and fires the dig-in ack -- never on a same-topic follow-up or an
    unresolved low-confidence guess.
    """

    def __init__(
        self,
        *,
        cfg: PipelineConfig,
        knowledge_cfg: KnowledgeConfig,
        llm: Any,
        initial_topic: str,
        fallback_classify: FallbackClassifier | None = None,
        ack_templates: list[str] | None = None,
        retrieval_index: RetrievalIndex | None = None,
        remaining_seconds_fn: Callable[[], "float | None"] | None = None,
    ) -> None:
        super().__init__()
        self._cfg = cfg
        self._knowledge_cfg = knowledge_cfg
        self._llm = llm
        self._topic_map = load_topic_map(knowledge_cfg)
        self._initial_topic = initial_topic  # the "normal" topic a sticky-mode exit returns to
        self._active_topic = initial_topic
        self._fallback_classify = fallback_classify or default_haiku_fallback_classify
        self._ack_templates = ack_templates or DEFAULT_ACK_TEMPLATES
        self._ack_index = 0
        self._retrieval_index = retrieval_index
        self._remaining_seconds_fn = remaining_seconds_fn

    def _spoken_name(self, topic_id: str) -> str:
        for topic in self._topic_map.get("topics", []):
            if topic["id"] == topic_id:
                return topic.get("spoken_name", topic_id)
        return topic_id

    def _topic_ack_templates(self, topic_id: str) -> list[str] | None:
        """A topic may override the generic dig-in ack with its own line(s) via
        a topic-map ``ack`` field (string or list) -- e.g. the greenhouse
        easter egg's playful "Did someone say... Greenhouse?!" opener. Returns
        None (use the round-robin defaults) when the topic sets no override."""
        for topic in self._topic_map.get("topics", []):
            if topic["id"] == topic_id:
                ack = topic.get("ack")
                if isinstance(ack, str) and ack.strip():
                    return [ack]
                if isinstance(ack, list) and ack:
                    return [str(a) for a in ack]
                return None
        return None

    def _topic_field(self, topic_id: str, key: str) -> Any:
        for topic in self._topic_map.get("topics", []):
            if topic["id"] == topic_id:
                return topic.get(key)
        return None

    def _is_sticky(self, topic_id: str | None) -> bool:
        """A ``sticky: true`` topic (e.g. greenhouse recruiting mode) holds the
        floor once active: the router will not switch away on a normal topic
        keyword -- only on an explicit exit phrase (see :meth:`_matches_exit`).
        This keeps KPH in candidate framing for every interview question."""
        return bool(topic_id) and bool(self._topic_field(topic_id, "sticky"))

    def _matches_exit(self, utterance: str, topic_id: str) -> bool:
        """True iff the utterance matches one of the sticky topic's ``exit``
        phrases -- the visitor's explicit "interview's over" release."""
        phrases = self._topic_field(topic_id, "exit") or []
        norm = _normalize(utterance)
        return any(p and _normalize(str(p)) in norm for p in phrases)

    def _exit_ack_templates(self, topic_id: str) -> list[str]:
        """The spoken beat when a sticky topic is released; falls back to the
        round-robin defaults if the topic sets no ``exit_ack``."""
        ack = self._topic_field(topic_id, "exit_ack")
        if isinstance(ack, str) and ack.strip():
            return [ack]
        if isinstance(ack, list) and ack:
            return [str(a) for a in ack]
        return self._ack_templates

    async def process_frame(self, frame: Frame, direction: FrameDirection) -> None:
        await super().process_frame(frame, direction)

        if isinstance(frame, TranscriptionFrame) and frame.text.strip():
            await self._handle_utterance(frame.text)

        await self.push_frame(frame, direction)

    async def _handle_utterance(self, utterance: str) -> None:
        topics = self._topic_map.get("topics", [])

        # Sticky topic held the floor (greenhouse recruiting mode): STAY here for
        # every question -- a topic keyword mid-interview ("tell me about
        # klanker-maker") is answered in candidate framing, never yanked back to
        # the technical pack -- UNTIL the visitor explicitly releases it, which
        # snaps back to the normal (initial) technical topic with the exit beat.
        if self._is_sticky(self._active_topic):
            if self._matches_exit(utterance, self._active_topic):
                await self._commit_switch(
                    self._initial_topic,
                    utterance,
                    ack_templates=self._exit_ack_templates(self._active_topic),
                )
            return

        topic_id, _confidence = classify(utterance, self._topic_map)

        if topic_id is None:
            # Below the confidence floor -- never guess (Pitfall 1). Try the
            # same-vendor Haiku fallback before giving up on a switch.
            topic_id = await self._fallback_classify(
                utterance, topics, model=self._cfg.llm.model
            )

        if topic_id is None or topic_id == self._active_topic:
            # No confident switch: stay on the currently active pack, no ack
            # (Pitfall 1/2) -- a shallow one-liner or same-topic follow-up.
            return

        # A topic may SUPPRESS the spoken ack with an explicit empty `ack: ""`
        # (e.g. greenhouse -- its LLM opener is the sole output, so it also lands
        # in the chat transcript; a TTSSpeakFrame ack never does).
        ack_raw = self._topic_field(topic_id, "ack")
        ack_templates = (
            None if ack_raw == "" else (self._topic_ack_templates(topic_id) or self._ack_templates)
        )
        await self._commit_switch(topic_id, utterance, ack_templates=ack_templates)

    async def _commit_switch(
        self, topic_id: str, utterance: str, *, ack_templates: list[str] | None
    ) -> None:
        """Swap block1 to ``topic_id``'s pack (+ optional BM25 chunks) and fire
        the spoken ack. Shared by a normal deep-turn switch and a sticky-topic
        release (which passes the exit beat as ``ack_templates``)."""
        self._active_topic = topic_id

        retrieved_chunks = None
        if self._retrieval_index is not None and self._knowledge_cfg.retrieval_enabled:
            # Local (tens of ms), topic-scoped (Amendment 3-B) BM25 query --
            # fired on the exact same deep-turn condition as the ack below,
            # so its cost is ack-masked (Amendment 3-G). A topic with no
            # built index returns [] (graceful degrade, never a crash).
            retrieved_chunks = (
                self._retrieval_index.query(
                    topic_id,
                    utterance,
                    top_k=self._knowledge_cfg.retrieval_top_k,
                    max_tokens=self._knowledge_cfg.retrieval_budget,
                )
                or None
            )

        # 07-05 (D-06): a synchronous READ of the caller-supplied session
        # state -- never a new timer/thread. None (the default) reproduces
        # Plan 01/02's block1 text unchanged.
        remaining_seconds = self._remaining_seconds_fn() if self._remaining_seconds_fn else None

        blocks = build_system_blocks(
            self._cfg,
            self._knowledge_cfg,
            topic_id,
            retrieved_chunks=retrieved_chunks,
            remaining_seconds=remaining_seconds,
        )
        apply_system_blocks(self._llm, blocks)

        # ack_templates None/empty -> the topic suppressed the spoken beat (its
        # LLM turn is the sole output). Otherwise a custom override or the
        # round-robin defaults (deterministic, no back-to-back repeat).
        if ack_templates:
            template = ack_templates[self._ack_index % len(ack_templates)]
            self._ack_index += 1
            ack_text = (
                template.format(spoken_name=self._spoken_name(topic_id))
                if "{spoken_name}" in template
                else template
            )
            await self.push_frame(TTSSpeakFrame(text=ack_text, append_to_context=False))
