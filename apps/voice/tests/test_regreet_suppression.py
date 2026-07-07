"""Phase 07.1: suppress the double-greeting echo.

When ``greet_first`` is false (prod slick-start: the browser client plays a
pre-recorded greeting on tap), the LLM must NOT re-introduce itself on its first
turn. build_pipeline seeds a one-shot developer nudge into the context saying the
greeting already happened.
"""

import dataclasses

from pipecat.processors.frame_processor import FrameProcessor

from klanker_voice.config import load_config
from klanker_voice.pipeline import NO_REGREET_KICK_MESSAGE, build_pipeline


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


def test_seeds_no_regreet_nudge_when_greet_first_false(stub_provider_keys):
    built = build_pipeline(_cfg(False), _FakeTransport())
    msgs = built.context.get_messages()
    assert NO_REGREET_KICK_MESSAGE in msgs
    # It must clearly instruct against a second self-introduction.
    assert "introduce" in NO_REGREET_KICK_MESSAGE["content"].lower()


def test_does_not_seed_nudge_when_greet_first_true(stub_provider_keys):
    built = build_pipeline(_cfg(True), _FakeTransport())
    msgs = built.context.get_messages()
    assert NO_REGREET_KICK_MESSAGE not in msgs
