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
#: so the ``defcon.run.34`` phrase is normalized before ``defcon.run``, which is
#: normalized before the bare ``defcon`` rule; and ``km CLI`` is normalized
#: before the bare ``km`` and ``CLI`` rules (otherwise "km CLI" would come out
#: as the robotic "klanker maker see elle eye" instead of "klanker maker tool").
_RULES: list[tuple[re.Pattern[str], str]] = [
    (re.compile(r"\bdef\s*con\.run\.34\b", re.IGNORECASE), "deaf con run thirty four"),
    (re.compile(r"\bdef\s*con\.run\.33\b", re.IGNORECASE), "deaf con run thirty three"),
    (re.compile(r"\bdef\s*con\.run\b", re.IGNORECASE), "deaf con run"),
    (re.compile(r"\bmesh\s*tk\b", re.IGNORECASE), "Mesh Tee Kay"),
    (re.compile(r"\btiogo\b", re.IGNORECASE), "tee oh go"),
    # kvmlab before the bare kvm rule so "kvmlab" -> "kay vee em lab", not
    # "kay vee em lab" via two passes leaving a stray "lab".
    (re.compile(r"\bkvmlab\b", re.IGNORECASE), "kay vee em lab"),
    (re.compile(r"\bkvm\b", re.IGNORECASE), "kay vee em"),
    # kv is the klanker-voice CLI (sibling to km). Mirror the km treatment:
    # "kv CLI" -> "klanker voice tool" (before the bare kv/CLI rules), bare
    # "kv" -> "klanker voice". Word-boundary anchored, so kv never fires inside
    # "kvm" (v->m is not a boundary) or "kvmlab".
    (re.compile(r"\bkv\s*CLI\b", re.IGNORECASE), "klanker voice tool"),
    (re.compile(r"\bkv\b", re.IGNORECASE), "klanker voice"),
    # "the klanker maker tool" -- no leading article in the replacement so the
    # sentence's own "the"/"a" composes ("the km CLI" -> "the klanker maker tool").
    (re.compile(r"\b`?km`?\s*CLI\b", re.IGNORECASE), "klanker maker tool"),
    (re.compile(r"\bkm\s*CLI\b", re.IGNORECASE), "klanker maker tool"),
    (re.compile(r"\bdef\s*con\b", re.IGNORECASE), "deaf con"),
    (re.compile(r"\bkm\b", re.IGNORECASE), "klanker maker"),
    (re.compile(r"\bCLI\b", re.IGNORECASE), "command"),
    (re.compile(r"\bGuelph\b", re.IGNORECASE), "Gwelf"),
    (re.compile(r"\bGIAC\b", re.IGNORECASE), "JEE-ack"),
    (re.compile(r"\bGCSA\b", re.IGNORECASE), "G C S A"),
    (re.compile(r"\bCISSP\b", re.IGNORECASE), "sissp"),
    (re.compile(r"\bOWASP\b", re.IGNORECASE), "Oh wasp"),
    (re.compile(r"\bebpf\b", re.IGNORECASE), "e b p f"),
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
