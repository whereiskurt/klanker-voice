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

#: Default ack template (Amendment 1's "OK! Let's dig into it."). Rendered
#: with the switched-to topic's spoken_name; fires ONLY on a genuine
#: deep-pack switch (Pitfall 2), never on a same-topic follow-up.
DEFAULT_ACK_TEMPLATE = "Okay! Let's dig into {spoken_name}."

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
        ack_template: str = DEFAULT_ACK_TEMPLATE,
    ) -> None:
        super().__init__()
        self._cfg = cfg
        self._knowledge_cfg = knowledge_cfg
        self._llm = llm
        self._topic_map = load_topic_map(knowledge_cfg)
        self._active_topic = initial_topic
        self._fallback_classify = fallback_classify or default_haiku_fallback_classify
        self._ack_template = ack_template

    def _spoken_name(self, topic_id: str) -> str:
        for topic in self._topic_map.get("topics", []):
            if topic["id"] == topic_id:
                return topic.get("spoken_name", topic_id)
        return topic_id

    async def process_frame(self, frame: Frame, direction: FrameDirection) -> None:
        await super().process_frame(frame, direction)

        if isinstance(frame, TranscriptionFrame) and frame.text.strip():
            await self._handle_utterance(frame.text)

        await self.push_frame(frame, direction)

    async def _handle_utterance(self, utterance: str) -> None:
        topics = self._topic_map.get("topics", [])
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

        self._active_topic = topic_id
        blocks = build_system_blocks(self._cfg, self._knowledge_cfg, topic_id)
        apply_system_blocks(self._llm, blocks)

        ack_text = self._ack_template.format(spoken_name=self._spoken_name(topic_id))
        await self.push_frame(TTSSpeakFrame(text=ack_text, append_to_context=False))
