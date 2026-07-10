"""260710-ixf: the double-greeting echo is now suppressed by the persona
system prompt ALONE, not a context-seeded developer message.

Root cause (reproduced live, 260710-ixf): the old no-regreet kick constant
was seeded as a ``role="developer"`` context message when ``greet_first`` is
false. pipecat's Anthropic adapter converts developer/system context messages
to USER-role turns, so on a content-free first turn (e.g. "alright"/"ok") the
model read the instruction ALOUD instead of following it silently. The fix:
remove the context seed entirely and rely on the persona's own "Opening move"
section (prompts/concierge.md), which never enters the context as a turn --
it lives on the LLM service's ``Settings.system_instruction``
(``apply_system_blocks``), the one path pipecat's adapter can't leak into a
spoken turn. These tests assert the negative: no such developer/no-regreet
message is ever seeded into the context, for greet_first true OR false.
"""

import dataclasses

from pipecat.processors.frame_processor import FrameProcessor

from klanker_voice.config import load_config
from klanker_voice.pipeline import build_pipeline


class _FakeTransport:
    def input(self) -> FrameProcessor:
        return FrameProcessor(name="fake-transport-input")

    def output(self) -> FrameProcessor:
        return FrameProcessor(name="fake-transport-output")

    def event_handler(self, _name: str):
        def _decorator(fn):
            return fn

        return _decorator


def _cfg(greet_first: bool):
    cfg = load_config()
    return dataclasses.replace(
        cfg, persona=dataclasses.replace(cfg.persona, greet_first=greet_first)
    )


def _has_no_regreet_developer_message(msgs: list) -> bool:
    """True if any context message is a developer-role no-regreet-style
    instruction (greeting/introduce/pre-recorded content) — the leak this
    plan removes."""
    for msg in msgs:
        if msg.get("role") != "developer":
            continue
        content = str(msg.get("content", "")).lower()
        if "greet" in content or "introduce" in content or "pre-record" in content:
            return True
    return False


def test_no_regreet_context_seed_when_greet_first_false(stub_provider_keys):
    """greet_first=false is the prod slick-start shape (client plays a
    pre-rendered greeting on tap) — this used to be exactly when the leaky
    seed was added. Now the context carries NO developer message at all,
    since nothing seeds one at build time."""
    built = build_pipeline(_cfg(False), _FakeTransport())
    msgs = built.context.get_messages()
    assert not _has_no_regreet_developer_message(msgs)
    assert not any(msg.get("role") == "developer" for msg in msgs)


def test_no_regreet_context_seed_when_greet_first_true(stub_provider_keys):
    built = build_pipeline(_cfg(True), _FakeTransport())
    msgs = built.context.get_messages()
    assert not _has_no_regreet_developer_message(msgs)
