"""TTS pronunciation normalizer (Phase 07.1).

ElevenLabs mispronounces several klanker project proper nouns. This filter
rewrites ONLY the spoken text -- it is attached to the ElevenLabs service via
``text_filters=[...]``, which pipecat applies *after* text aggregation and
*before* synthesis (``pipecat.services.tts_service``). The on-screen captions
are unaffected: the RTVI agent caption is built from ``LLMTextFrame`` (raw LLM
output, upstream of TTS), so viewers still read ``DEF CON`` / ``meshtk`` / ``km``
while the voice says the respelled form.

The map is ordered longest/most-specific first (``defcon.run`` before ``defcon``)
and every rule is word-boundary anchored so ``km`` never fires inside ``kmv`` /
``10km`` while still catching the possessive ``km's``. Extend ``_RULES`` to add
a term (e.g. ``kv`` -> ``kay vee``).
"""

from __future__ import annotations

import re
from collections.abc import Mapping
from typing import Any

from pipecat.utils.text.base_text_filter import BaseTextFilter

#: Ordered (pattern, spoken) rules. Order matters: earlier rules win on overlap,
#: so the ``defcon.run`` phrase is normalized before the bare ``defcon`` rule.
_RULES: list[tuple[re.Pattern[str], str]] = [
    (re.compile(r"\bdef\s*con\.run\b", re.IGNORECASE), "deaf con run"),
    (re.compile(r"\bmesh\s*tk\b", re.IGNORECASE), "Mesh Tee Kay"),
    (re.compile(r"\bdef\s*con\b", re.IGNORECASE), "deaf con"),
    (re.compile(r"\bkm\b", re.IGNORECASE), "kay em"),
    (re.compile(r"\bCLI\b", re.IGNORECASE), "see elle eye"),
    (re.compile(r"\bGuelph\b", re.IGNORECASE), "Gwelf"),
]


def normalize_for_speech(text: str) -> str:
    """Apply every pronunciation rule in order and return the spoken form."""
    for pattern, spoken in _RULES:
        text = pattern.sub(spoken, text)
    return text


class PronunciationTextFilter(BaseTextFilter):
    """A pipecat ``BaseTextFilter`` that respells klanker proper nouns for TTS."""

    async def filter(self, text: str) -> str:
        return normalize_for_speech(text)

    async def update_settings(self, settings: Mapping[str, Any]) -> None:
        # No runtime-tunable settings; the rule map is static (edit _RULES).
        return None

    async def handle_interruption(self) -> None:
        return None

    async def reset_interruption(self) -> None:
        return None
