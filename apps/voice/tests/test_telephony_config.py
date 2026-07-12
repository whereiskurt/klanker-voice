"""Unit tests for klanker_voice.telephony.config (Phase 11, D-09, T-11-01-01/02)."""

from __future__ import annotations

from pathlib import Path

import pytest

from klanker_voice.config import APP_ROOT, ConfigError
from klanker_voice.telephony.config import TelephonyConfig, load_telephony_config

REAL_PIPELINE_TOML = APP_ROOT / "pipeline.toml"

VALID_TELEPHONY_TOML = """
[telephony]
enabled = true
provider = "voipms"
edge = "asterisk-ari"
codec = "pcmu"
sample_rate = 8000
packet_ms = 20
max_concurrent_calls = 1
answer_timeout_seconds = 15
hangup_on_pipeline_error = true
require_gate = true
gate_mode = "either"
gate_window_seconds = 10
unlock_tier_id = "kph-tier"
"""


def test_real_checked_in_pipeline_toml_telephony_table_round_trips():
    """The shipped pipeline.toml [telephony] table stays enabled=false --
    the WebRTC-only default config load must be behavior-unaffected."""
    cfg = load_telephony_config(REAL_PIPELINE_TOML)
    assert isinstance(cfg, TelephonyConfig)
    assert cfg.enabled is False
    assert cfg.provider == "voipms"
    assert cfg.edge == "asterisk-ari"
    assert cfg.codec == "pcmu"
    assert cfg.gate_mode == "either"


def test_missing_telephony_table_defaults_to_disabled(make_config_file):
    """A config file with no [telephony] table at all (MINIMAL_TOML) must NOT
    raise -- it returns the documented defaults (enabled=False)."""
    cfg = load_telephony_config(make_config_file())
    assert cfg == TelephonyConfig()
    assert cfg.enabled is False


def test_valid_telephony_table_parses(make_config_file):
    path = make_config_file(append=VALID_TELEPHONY_TOML)
    cfg = load_telephony_config(path)
    assert cfg == TelephonyConfig(
        enabled=True,
        provider="voipms",
        edge="asterisk-ari",
        codec="pcmu",
        sample_rate=8000,
        packet_ms=20,
        max_concurrent_calls=1,
        answer_timeout_seconds=15,
        hangup_on_pipeline_error=True,
        require_gate=True,
        gate_mode="either",
        gate_window_seconds=10,
        unlock_tier_id="kph-tier",
    )


def test_invalid_gate_mode_rejected(make_config_file):
    path = make_config_file(
        append=VALID_TELEPHONY_TOML.replace('gate_mode = "either"', 'gate_mode = "open"')
    )
    with pytest.raises(ConfigError, match="gate_mode"):
        load_telephony_config(path)


@pytest.mark.parametrize("gate_mode", ["dtmf", "passphrase", "either"])
def test_each_allowed_gate_mode_accepted(make_config_file, gate_mode):
    path = make_config_file(
        append=VALID_TELEPHONY_TOML.replace('gate_mode = "either"', f'gate_mode = "{gate_mode}"')
    )
    cfg = load_telephony_config(path)
    assert cfg.gate_mode == gate_mode


@pytest.mark.parametrize(
    "bad_key",
    ["access_pin", "passphrase_words", "words", "pass_word"],
)
def test_credential_looking_telephony_field_rejected(make_config_file, bad_key):
    """D-09: a §24-secret-shaped field name must never be accepted as a TOML
    tunable inside [telephony] -- refused before parse, before gate_mode is
    even validated."""
    snippet = VALID_TELEPHONY_TOML + f'{bad_key} = "oops"\n'
    path = make_config_file(append=snippet)
    with pytest.raises(ConfigError, match="credential"):
        load_telephony_config(path)


def test_telephony_table_must_be_a_table(tmp_path: Path):
    # A bare top-level "telephony = 1" scalar (not a [telephony] table) --
    # written directly (not via make_config_file) so it stays top-level
    # rather than nesting under whatever table the fixture's MINIMAL_TOML
    # last opened.
    path = tmp_path / "pipeline.toml"
    path.write_text("telephony = 1\n", encoding="utf-8")
    with pytest.raises(ConfigError, match="\\[telephony\\] must be a table"):
        load_telephony_config(path)
