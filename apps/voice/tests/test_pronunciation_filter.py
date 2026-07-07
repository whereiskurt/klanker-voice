"""Phase 07.1: TTS pronunciation normalizer (BaseTextFilter).

Rewrites ONLY the spoken text (applied via ElevenLabs ``text_filters`` after
aggregation, before synthesis). Captions are unaffected because the RTVI agent
caption is built from ``LLMTextFrame`` upstream of TTS -- so these tests only
assert the substitution behavior of the filter itself.
"""

import pytest

from klanker_voice.pronunciation import PronunciationTextFilter


async def _f(text: str) -> str:
    return await PronunciationTextFilter().filter(text)


async def test_meshtk_is_spelled_out():
    assert await _f("meshtk is a proxy") == "Mesh Tee Kay is a proxy"


async def test_defcon_run_before_bare_defcon():
    assert await _f("check defcon.run today") == "check deaf con run today"


async def test_bare_defcon_and_def_con_space():
    assert await _f("the defcon talk") == "the deaf con talk"
    assert await _f("DEF CON is fun") == "deaf con is fun"


async def test_km_standalone():
    assert await _f("run km now") == "run kay em now"


async def test_km_possessive_kept():
    assert await _f("km's config") == "kay em's config"


async def test_km_not_matched_inside_larger_tokens():
    # kmv (sibling CLI) and 10km must NOT be rewritten by the km rule.
    assert await _f("kmv sandbox") == "kmv sandbox"
    assert await _f("a 10km run") == "a 10km run"


async def test_cli_spelled_out():
    assert await _f("the CLI tool") == "the see elle eye tool"


async def test_guelph_spelled_out():
    assert await _f("visit Guelph Ontario") == "visit Gwelf Ontario"


async def test_multiple_terms_in_one_utterance():
    assert await _f("km and meshtk") == "kay em and Mesh Tee Kay"


async def test_passthrough_when_no_terms():
    assert await _f("just a normal sentence") == "just a normal sentence"
    assert await _f("") == ""


async def test_is_base_text_filter_with_noop_lifecycle():
    from pipecat.utils.text.base_text_filter import BaseTextFilter

    f = PronunciationTextFilter()
    assert isinstance(f, BaseTextFilter)
    # Lifecycle hooks must be safe no-ops (never raise).
    await f.update_settings({})
    await f.handle_interruption()
    await f.reset_interruption()
