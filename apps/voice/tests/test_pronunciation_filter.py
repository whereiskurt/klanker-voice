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
    assert await _f("run km now") == "run klanker maker now"


async def test_km_possessive_kept():
    assert await _f("km's config") == "klanker maker's config"


async def test_km_not_matched_inside_larger_tokens():
    # kmv (sibling CLI) and 10km must NOT be rewritten by the km rule.
    assert await _f("kmv sandbox") == "kmv sandbox"
    assert await _f("a 10km run") == "a 10km run"


async def test_km_cli_expands_to_klanker_maker_tool():
    # "km CLI" must fire the combined rule BEFORE the bare km/CLI rules -- the
    # sentence's own article composes with the article-less replacement.
    assert await _f("the km CLI is great") == "the klanker maker tool is great"
    assert await _f("kmCLI") == "klanker maker tool"


async def test_defcon_run_34_says_thirty_four():
    # The .34 suffix must be spoken, not mangled into "DEFCON-er-one".
    assert await _f("deployed on defcon.run.34") == "deployed on deaf con run thirty four"


async def test_tiogo_spelled_out():
    assert await _f("tell me about tiogo") == "tell me about tee oh go"


async def test_kvmlab_and_kvm_spelled_out():
    # kvmlab must fire before the bare kvm rule (no stray "lab").
    assert await _f("the kvmlab design") == "the kay vee em lab design"
    assert await _f("a kvm host") == "a kay vee em host"


async def test_kv_and_kv_cli_spelled_out_without_touching_kvm():
    # kv mirrors km: bare kv -> "klanker voice", "kv CLI" -> "klanker voice
    # tool" (article composes), and kv must NOT fire inside kvm / kvmlab.
    assert await _f("run kv now") == "run klanker voice now"
    assert await _f("the kv CLI is slick") == "the klanker voice tool is slick"
    assert await _f("the kvm and kvmlab") == "the kay vee em and kay vee em lab"


async def test_cli_spoken_as_command():
    # Standalone CLI reads more naturally as "command" than the robotic
    # "see elle eye" (260710 pronunciation pass).
    assert await _f("the CLI tool") == "the command tool"


async def test_security_acronyms_pronounced():
    # 260710 pronunciation pass: certs/acronyms KPH says in greenhouse interview
    # mode that TTS otherwise mangles.
    assert await _f("I hold a CISSP") == "I hold a sissp"
    assert await _f("GCSA from SANS") == "G C S A from SANS"
    assert await _f("a GIAC cert") == "a JEE-ack cert"
    assert await _f("OWASP ASVS") == "Oh wasp ASVS"
    assert await _f("kernel-level eBPF sandboxing") == "kernel-level e b p f sandboxing"


async def test_guelph_spelled_out():
    assert await _f("visit Guelph Ontario") == "visit Gwelf Ontario"


async def test_multiple_terms_in_one_utterance():
    assert await _f("km and meshtk") == "klanker maker and Mesh Tee Kay"


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
