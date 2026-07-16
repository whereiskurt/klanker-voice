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

VALID_TELEPHONY_TOML_WITH_TEL_MINT = VALID_TELEPHONY_TOML + """
tel_mint_url = "https://auth.klankermaker.ai/use1/tel"
tel_mint_env_var = "TELEPHONY_ENDPOINT_AUTH_TOKEN"
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


def test_gate_debug_log_heard_defaults_off(make_config_file):
    """The opt-in fail-path heard-words debug flag defaults False -- a
    [telephony] table without it keeps the D-05e posture byte-identical."""
    path = make_config_file(append=VALID_TELEPHONY_TOML)
    cfg = load_telephony_config(path)
    assert cfg.gate_debug_log_heard is False


def test_gate_debug_log_heard_parses_true_when_set(make_config_file):
    """When the operator flips gate_debug_log_heard = true, it parses through."""
    path = make_config_file(
        append=VALID_TELEPHONY_TOML + "gate_debug_log_heard = true\n"
    )
    cfg = load_telephony_config(path)
    assert cfg.gate_debug_log_heard is True


def test_telephony_table_without_tel_mint_defaults_to_unconfigured(make_config_file):
    """Phase 12 Plan 06 (D-02/D-04): a [telephony] table with no tel_mint_*
    fields at all -- e.g. every existing Phase-11 fixture/checked-in TOML --
    parses with the mint integration OFF (empty URL, the default env var
    name), so the legacy static unlock_tier_id grant stays byte-unaffected."""
    path = make_config_file(append=VALID_TELEPHONY_TOML)
    cfg = load_telephony_config(path)
    assert cfg.tel_mint_url == ""
    assert cfg.tel_mint_env_var == "TELEPHONY_ENDPOINT_AUTH_TOKEN"


def test_tel_mint_fields_parse(make_config_file):
    """Phase 12 Plan 06 (D-02/D-04): the /tel endpoint URL + the NAME of the
    env var holding the shared bearer token both load as plain (non-secret)
    config fields -- the token VALUE itself never lives in TOML."""
    path = make_config_file(append=VALID_TELEPHONY_TOML_WITH_TEL_MINT)
    cfg = load_telephony_config(path)
    assert cfg.tel_mint_url == "https://auth.klankermaker.ai/use1/tel"
    assert cfg.tel_mint_env_var == "TELEPHONY_ENDPOINT_AUTH_TOKEN"


@pytest.mark.parametrize(
    "bad_key",
    ["tel_endpoint_auth_token", "tel_mint_bearer_token", "tel_mint_password"],
)
def test_credential_looking_tel_mint_field_rejected(make_config_file, bad_key):
    """D-02/D-04/D-09: even a Phase-12-shaped credential field name (an
    endpoint auth TOKEN value, not just the env-var NAME this plan actually
    adds) is still refused by the same shared credential gate -- proves the
    /tel integration cannot smuggle a real secret into pipeline.toml."""
    snippet = VALID_TELEPHONY_TOML_WITH_TEL_MINT + f'{bad_key} = "oops"\n'
    path = make_config_file(append=snippet)
    with pytest.raises(ConfigError, match="credential"):
        load_telephony_config(path)


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


# --- Quick task 260715-oq0: [[telephony.announcement]] (CTF phone-OTP) -----

VALID_ANNOUNCEMENT_TOML = """
[[telephony.announcement]]
did = "7254043234"
otp_url = "https://auth.klankermaker.ai/use1/ctf/otp"
otp_env_var = "CTF_OTP_AUTH_TOKEN"
line_template = "Hey! Let me get you that O T P. {code}. That's {code}. Buh bye."
"""


def test_absent_announcement_table_yields_empty_tuple_byte_unchanged(make_config_file):
    """Absent [[telephony.announcement]] -> announcements == () and every
    other field stays at its documented default -- byte-identical to the
    pre-260715-oq0 TelephonyConfig shape."""
    path = make_config_file(append=VALID_TELEPHONY_TOML)
    cfg = load_telephony_config(path)
    assert cfg.announcements == ()
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


def test_well_formed_announcement_entry_parses(make_config_file):
    path = make_config_file(append=VALID_TELEPHONY_TOML + VALID_ANNOUNCEMENT_TOML)
    cfg = load_telephony_config(path)
    assert len(cfg.announcements) == 1
    entry = cfg.announcements[0]
    assert entry.did == "7254043234"
    assert entry.otp_url == "https://auth.klankermaker.ai/use1/ctf/otp"
    assert entry.otp_env_var == "CTF_OTP_AUTH_TOKEN"
    assert entry.line_template == "Hey! Let me get you that O T P. {code}. That's {code}. Buh bye."


def test_announcement_entry_without_code_placeholder_rejected(make_config_file):
    bad_toml = VALID_TELEPHONY_TOML + VALID_ANNOUNCEMENT_TOML.replace(
        'line_template = "Hey! Let me get you that O T P. {code}. That\'s {code}. Buh bye."',
        'line_template = "Hey! Let me get you that OTP. Buh bye."',
    )
    path = make_config_file(append=bad_toml)
    with pytest.raises(ConfigError, match="line_template"):
        load_telephony_config(path)


def test_announcement_entry_missing_did_rejected(make_config_file):
    bad_toml = VALID_TELEPHONY_TOML + VALID_ANNOUNCEMENT_TOML.replace(
        'did = "7254043234"', 'did = ""'
    )
    path = make_config_file(append=bad_toml)
    with pytest.raises(ConfigError, match="did"):
        load_telephony_config(path)


def test_announcement_entry_missing_otp_url_rejected(make_config_file):
    bad_toml = VALID_TELEPHONY_TOML + VALID_ANNOUNCEMENT_TOML.replace(
        'otp_url = "https://auth.klankermaker.ai/use1/ctf/otp"\n', ""
    )
    path = make_config_file(append=bad_toml)
    with pytest.raises(ConfigError, match="otp_url"):
        load_telephony_config(path)


def test_announcement_otp_env_var_optional_defaults_empty(make_config_file):
    """otp_env_var is optional -- an entry omitting it still parses (an
    unconfigured bearer means no Authorization header is sent at call time)."""
    no_env_var_toml = VALID_TELEPHONY_TOML + VALID_ANNOUNCEMENT_TOML.replace(
        'otp_env_var = "CTF_OTP_AUTH_TOKEN"\n', ""
    )
    path = make_config_file(append=no_env_var_toml)
    cfg = load_telephony_config(path)
    assert cfg.announcements[0].otp_env_var == ""


def test_real_checked_in_telephony_toml_has_announcement_entry():
    """apps/voice/configs/telephony.toml (the standalone telephony-edge
    harness config, NOT pipeline.toml) carries the concrete 7254043234
    announcement entry."""
    telephony_toml_path = APP_ROOT / "configs" / "telephony.toml"
    cfg = load_telephony_config(telephony_toml_path)
    assert len(cfg.announcements) == 1
    entry = cfg.announcements[0]
    assert entry.did == "7254043234"
    assert entry.otp_env_var == "CTF_OTP_AUTH_TOKEN"
    assert "{code}" in entry.line_template
