"""Unit tests for klanker_voice.config (PIPE-04, D-09, T-1-04)."""

from __future__ import annotations

from pathlib import Path

import pytest

from klanker_voice.config import (
    APP_ROOT,
    CONFIG_PATH_ENV_VAR,
    ConfigError,
    PipelineConfig,
    load_config,
)

REAL_PIPELINE_TOML = APP_ROOT / "pipeline.toml"


def test_real_checked_in_pipeline_toml_round_trips():
    cfg = load_config(REAL_PIPELINE_TOML)
    assert isinstance(cfg, PipelineConfig)
    assert cfg.stt.provider == "deepgram-nova3"
    assert cfg.stt.model == "nova-3-general"
    assert cfg.stt.flux.eot_threshold == 0.7
    assert cfg.stt.flux.eager_eot_threshold == 0.0
    assert cfg.turn.strategy in {"vad_timeout", "smart_turn_v3"}
    assert cfg.llm.provider == "anthropic"
    assert cfg.llm.model == "claude-haiku-4-5"
    assert cfg.tts.provider == "elevenlabs"
    assert cfg.tts.model == "eleven_flash_v2_5"
    assert 0.7 <= cfg.tts.speed <= 1.2
    assert cfg.persona.prompt_path.is_file()
    assert cfg.persona.prompt_path.name == "concierge.md"


def test_minimal_fixture_toml_parses(make_config_file):
    cfg = load_config(make_config_file())
    assert cfg.stt.provider == "deepgram-nova3"
    assert cfg.persona.prompt_path.is_file()


@pytest.mark.parametrize(
    "old,new",
    [
        ('provider = "deepgram-nova3"', 'provider = "whisper-local"'),
        ('provider = "anthropic"', 'provider = "openai"'),
        ('provider = "elevenlabs"', 'provider = "cartesia"'),
        ('strategy = "smart_turn_v3"', 'strategy = "aggregation_timeout"'),
    ],
)
def test_unknown_provider_or_strategy_rejected(make_config_file, old, new):
    path = make_config_file(replace=[(old, new)])
    with pytest.raises(ConfigError, match="unknown"):
        load_config(path)


@pytest.mark.parametrize(
    "old,new,field",
    [
        ("speed = 1.1", "speed = 1.5", "tts.speed"),
        ("speed = 1.1", "speed = 0.5", "tts.speed"),
        ("eot_threshold = 0.7", "eot_threshold = 0.3", "stt.flux.eot_threshold"),
        ("eot_threshold = 0.7", "eot_threshold = 0.95", "stt.flux.eot_threshold"),
        ("vad_stop_secs = 0.2", "vad_stop_secs = 6.0", "turn.vad_stop_secs"),
        ("vad_stop_secs = 0.2", "vad_stop_secs = -0.1", "turn.vad_stop_secs"),
        ("user_speech_timeout = 0.6", "user_speech_timeout = 0.0", "turn.user_speech_timeout"),
    ],
)
def test_out_of_range_knobs_rejected(make_config_file, old, new, field):
    path = make_config_file(replace=[(old, new)])
    with pytest.raises(ConfigError, match=field.rsplit(".", 1)[-1]):
        load_config(path)


@pytest.mark.parametrize(
    "snippet",
    [
        '[tts.extra]\napi_key = "sk-oops"',
        '[llm.opts]\nsecret = "oops"',
        '[stt.opts]\nauth_token = "oops"',
        'password = "oops"',  # top-level
    ],
)
def test_credential_looking_field_rejected(make_config_file, snippet):
    path = make_config_file(append=snippet)
    with pytest.raises(ConfigError, match="credential"):
        load_config(path)


def test_missing_persona_file_rejected(make_config_file):
    path = make_config_file(persona_file=False)
    with pytest.raises(ConfigError, match="persona prompt not found"):
        load_config(path)


def test_missing_config_file_rejected(tmp_path: Path):
    with pytest.raises(ConfigError, match="not found"):
        load_config(tmp_path / "nope.toml")


def test_env_var_override_honored(make_config_file, monkeypatch):
    custom = make_config_file(replace=[('strategy = "smart_turn_v3"', 'strategy = "vad_timeout"')])
    monkeypatch.setenv(CONFIG_PATH_ENV_VAR, str(custom))
    cfg = load_config()  # no explicit path — must pick up the env var
    assert cfg.turn.strategy == "vad_timeout"


def test_explicit_path_beats_env_var(make_config_file, monkeypatch):
    monkeypatch.setenv(CONFIG_PATH_ENV_VAR, "/nonexistent/env-pointed.toml")
    cfg = load_config(make_config_file())
    assert cfg.stt.provider == "deepgram-nova3"
