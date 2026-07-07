"""Shared fixtures: minimal valid pipeline TOML in tmp_path + mutator helper."""

from __future__ import annotations

from pathlib import Path

import pytest


@pytest.fixture(autouse=True)
def _reset_scale_in_protection_state():
    """Reset the module-global scale-in-protection cache between tests so the
    'only call ECS on a real transition' assertions stay isolated (mirrors the
    per-file _active_session_count resets)."""
    from klanker_voice import session

    session._protection_state = None
    yield
    session._protection_state = None


MINIMAL_TOML = """\
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


@pytest.fixture
def make_config_file(tmp_path: Path):
    """Write a pipeline TOML (default: minimal valid) into tmp_path.

    Returns a factory: ``make_config_file(replace=[(old, new), ...], append=str)``
    -> Path to the written TOML. A stub persona file is created alongside so the
    default persona.prompt_path resolves.
    """

    def _make(
        *,
        replace: list[tuple[str, str]] | None = None,
        append: str = "",
        persona_file: bool = True,
    ) -> Path:
        if persona_file:
            prompts_dir = tmp_path / "prompts"
            prompts_dir.mkdir(exist_ok=True)
            (prompts_dir / "concierge.md").write_text(
                "# stub persona v1\nYou are K.\n", encoding="utf-8"
            )
        text = MINIMAL_TOML
        for old, new in replace or []:
            assert old in text, f"mutator target not found in TOML: {old!r}"
            text = text.replace(old, new)
        if append:
            text += "\n" + append + "\n"
        config_path = tmp_path / "pipeline.toml"
        config_path.write_text(text, encoding="utf-8")
        return config_path

    return _make


@pytest.fixture
def stub_provider_keys(monkeypatch: pytest.MonkeyPatch):
    """Dummy provider API keys so factories construct services without real keys.

    Unit tests never make network calls; construction only needs non-empty values.
    """
    monkeypatch.setenv("DEEPGRAM_API_KEY", "dg-test-dummy")
    monkeypatch.setenv("ANTHROPIC_API_KEY", "an-test-dummy")
    monkeypatch.setenv("ELEVENLABS_API_KEY", "el-test-dummy")
