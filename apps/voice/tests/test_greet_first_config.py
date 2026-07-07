"""Unit tests for PersonaConfig.greet_first (PIPE-02, B-06).

`_MINIMAL` mirrors tests/conftest.py's MINIMAL_TOML fixture body (minimal valid
[stt]/[turn]/[llm]/[tts]/[persona] tables). Its `persona.prompt_path` is the
relative `"prompts/concierge.md"` — same as the shared fixture — so the
`_persona_stub` autouse fixture below creates a real file at that path inside
each test's `tmp_path`, matching `make_config_file`'s own `persona_file=True`
behavior (tests/conftest.py) without needing a fixture argument here.
"""

from __future__ import annotations

from pathlib import Path

import pytest

from klanker_voice.config import load_config

_MINIMAL = """\
[stt]
provider = "deepgram-nova3"
model = "nova-3-general"

[stt.flux]
eot_threshold = 0.7
eager_eot_threshold = 0.0

[turn]
strategy = "smart_turn_v3"
vad_stop_secs = 0.2
user_speech_timeout = 0.6

[llm]
provider = "anthropic"
model = "claude-haiku-4-5"

[tts]
provider = "elevenlabs"
model = "eleven_flash_v2_5"
voice_id = ""
speed = 1.1

[persona]
prompt_path = "prompts/concierge.md"
"""


@pytest.fixture(autouse=True)
def _persona_stub(tmp_path: Path) -> None:
    """Real file at the relative persona.prompt_path _MINIMAL points at."""
    prompts_dir = tmp_path / "prompts"
    prompts_dir.mkdir(exist_ok=True)
    (prompts_dir / "concierge.md").write_text("# stub persona\nYou are K.\n", encoding="utf-8")


def test_greet_first_defaults_true_when_absent(tmp_path: Path):
    toml = tmp_path / "p.toml"
    toml.write_text(_MINIMAL)  # a persona table WITHOUT greet_first
    assert load_config(toml).persona.greet_first is True


def test_greet_first_reads_false(tmp_path: Path):
    toml = tmp_path / "p.toml"
    toml.write_text(_MINIMAL.replace('prompt_path = "prompts/concierge.md"',
                                     'prompt_path = "prompts/concierge.md"\ngreet_first = false'))
    assert load_config(toml).persona.greet_first is False
