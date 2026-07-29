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


# --- Quick task 260716-1g0: [[telephony.announcement]] (CTF phone-OTP,
# Revision 2 -- DTMF-code trigger, not DID) ----------------------------------

VALID_ANNOUNCEMENT_TOML = """
[[telephony.announcement]]
code_env_var = "CTF_ANNOUNCEMENT_CODE_TEST"
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
    assert entry.code_env_var == "CTF_ANNOUNCEMENT_CODE_TEST"
    assert entry.otp_url == "https://auth.klankermaker.ai/use1/ctf/otp"
    assert entry.otp_env_var == "CTF_OTP_AUTH_TOKEN"
    assert entry.line_template == "Hey! Let me get you that O T P. {code}. That's {code}. Buh bye."
    assert entry.did == ""


def test_announcement_entry_without_code_placeholder_rejected(make_config_file):
    bad_toml = VALID_TELEPHONY_TOML + VALID_ANNOUNCEMENT_TOML.replace(
        'line_template = "Hey! Let me get you that O T P. {code}. That\'s {code}. Buh bye."',
        'line_template = "Hey! Let me get you that OTP. Buh bye."',
    )
    path = make_config_file(append=bad_toml)
    with pytest.raises(ConfigError, match="line_template"):
        load_telephony_config(path)


def test_announcement_entry_missing_code_env_var_rejected(make_config_file):
    bad_toml = VALID_TELEPHONY_TOML + VALID_ANNOUNCEMENT_TOML.replace(
        'code_env_var = "CTF_ANNOUNCEMENT_CODE_TEST"', 'code_env_var = ""'
    )
    path = make_config_file(append=bad_toml)
    with pytest.raises(ConfigError, match="code_env_var"):
        load_telephony_config(path)


def test_announcement_did_optional_defaults_empty(make_config_file):
    """Revision 2 (260716-1g0): `did` is no longer a matcher -- an entry
    with no `did` line at all parses cleanly, defaulting to ""."""
    path = make_config_file(append=VALID_TELEPHONY_TOML + VALID_ANNOUNCEMENT_TOML)
    cfg = load_telephony_config(path)
    assert cfg.announcements[0].did == ""


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
    harness config, NOT pipeline.toml) is code-keyed (Revision 2,
    260716-1g0) -- the trigger value itself lives only in SSM. Quick task
    260727-pdh: per-DID game entries now (four as of 260728-tfn), the first
    (3234) renamed off the retired bare CTF_ANNOUNCEMENT_CODE name."""
    telephony_toml_path = APP_ROOT / "configs" / "telephony.toml"
    cfg = load_telephony_config(telephony_toml_path)
    assert len(cfg.announcements) == 5
    entry = cfg.announcements[0]
    assert entry.code_env_var == "CTF_ANNOUNCEMENT_CODE_3234"
    assert entry.otp_env_var == "CTF_OTP_AUTH_TOKEN"
    assert "{code}" in entry.line_template


def test_credential_looking_key_inside_announcement_table_rejected(make_config_file):
    """The shared credential gate refuses a credential-shaped key ANYWHERE
    in the file, including inside an [[telephony.announcement]] table."""
    bad_toml = (
        VALID_TELEPHONY_TOML
        + VALID_ANNOUNCEMENT_TOML.rstrip()
        + '\ncode_secret = "oops"\n'
    )
    path = make_config_file(append=bad_toml)
    with pytest.raises(ConfigError, match="credential"):
        load_telephony_config(path)


# --- Quick task 260716-hg5: [[telephony.announcement]].sms_dids -------------


def test_announcement_sms_dids_absent_defaults_empty(make_config_file):
    """No `sms_dids` line -> `()` (SMS-during-call OFF), byte-identical to the
    pre-260716-hg5 announcement shape."""
    path = make_config_file(append=VALID_TELEPHONY_TOML + VALID_ANNOUNCEMENT_TOML)
    cfg = load_telephony_config(path)
    assert cfg.announcements[0].sms_dids == ()


def test_announcement_sms_dids_parses_and_normalizes(make_config_file):
    """`sms_dids` parses to an ORDERED tuple of digits-only DIDs: mixed
    formats normalize identically, junk/empties drop, order is preserved
    (it is the runtime auto-fallback order)."""
    sms_toml = VALID_ANNOUNCEMENT_TOML.rstrip() + (
        '\nsms_dids = ["613-480-5878", "+17254043234", "  ", "986-276-3234"]\n'
    )
    path = make_config_file(append=VALID_TELEPHONY_TOML + sms_toml)
    cfg = load_telephony_config(path)
    assert cfg.announcements[0].sms_dids == ("6134805878", "17254043234", "9862763234")


def test_announcement_sms_dids_non_list_rejected(make_config_file):
    """A scalar `sms_dids` (not an array) is a hard config error."""
    bad_toml = VALID_ANNOUNCEMENT_TOML.rstrip() + '\nsms_dids = "6134805878"\n'
    path = make_config_file(append=VALID_TELEPHONY_TOML + bad_toml)
    with pytest.raises(ConfigError, match="sms_dids"):
        load_telephony_config(path)


def test_announcement_sms_relay_url_absent_defaults_empty(make_config_file):
    """No `sms_relay_url` -> "" (the relay is the only send path, so SMS is off)."""
    path = make_config_file(append=VALID_TELEPHONY_TOML + VALID_ANNOUNCEMENT_TOML)
    cfg = load_telephony_config(path)
    assert cfg.announcements[0].sms_relay_url == ""


def test_shipped_telephony_toml_arms_sms_did_and_relay(make_config_file):
    """The shipped configs/telephony.toml's 3234 game entry has an EMPTY
    sms_dids fallback pool (quick 260717-buf reserves 613 -- unresolved
    dialed DID sends no text), the auth /ctf/sms relay URL, and per-DID
    reply enrollment NARROWED (quick task 260727-pdh, D-02 amended) to its
    OWN single DID -- 3283/8283 are now served by their own entries."""
    telephony_toml_path = APP_ROOT / "configs" / "telephony.toml"
    cfg = load_telephony_config(telephony_toml_path)
    assert cfg.announcements[0].sms_dids == ()
    assert cfg.announcements[0].sms_relay_url == "https://auth.klankermaker.ai/use1/ctf/sms"
    assert cfg.announcements[0].sms_reply_dids == ("7254043234",)


# --- Quick task 260716-hg5 follow-up: [[telephony.announcement]].sms_reply_dids


def test_announcement_sms_reply_dids_absent_defaults_empty(make_config_file):
    """No `sms_reply_dids` line -> `()` (per-DID reply OFF; pure legacy pool
    behavior), byte-identical to the pre-per-DID announcement shape."""
    path = make_config_file(append=VALID_TELEPHONY_TOML + VALID_ANNOUNCEMENT_TOML)
    cfg = load_telephony_config(path)
    assert cfg.announcements[0].sms_reply_dids == ()


def test_announcement_sms_reply_dids_parses_and_normalizes(make_config_file):
    """`sms_reply_dids` normalizes to digits-only DIDs (same rule as sms_dids),
    order preserved, junk/empties dropped."""
    reply_toml = VALID_ANNOUNCEMENT_TOML.rstrip() + (
        '\nsms_reply_dids = ["725-404-3234", "+17254043283", "  "]\n'
    )
    path = make_config_file(append=VALID_TELEPHONY_TOML + reply_toml)
    cfg = load_telephony_config(path)
    assert cfg.announcements[0].sms_reply_dids == ("7254043234", "17254043283")


def test_announcement_sms_reply_dids_non_list_rejected(make_config_file):
    """A scalar `sms_reply_dids` (not an array) is a hard config error whose
    message names the offending field."""
    bad_toml = VALID_ANNOUNCEMENT_TOML.rstrip() + '\nsms_reply_dids = "7254043234"\n'
    path = make_config_file(append=VALID_TELEPHONY_TOML + bad_toml)
    with pytest.raises(ConfigError, match="sms_reply_dids"):
        load_telephony_config(path)


# --- Quick task 260717-buf: [telephony.cid_prefix_dids] (Approach C) ---


def test_cid_prefix_dids_absent_defaults_empty(make_config_file):
    """No [telephony.cid_prefix_dids] table -> {} (CID-name-prefix resolution OFF,
    byte-identical to before)."""
    path = make_config_file(append=VALID_TELEPHONY_TOML)
    assert load_telephony_config(path).cid_prefix_did_map == {}


def test_cid_prefix_dids_parses_and_normalizes(make_config_file):
    """Keys (CID-name-prefix tags) are kept verbatim; values (DIDs) normalize to
    digits-only."""
    toml = VALID_TELEPHONY_TOML + (
        "\n[telephony.cid_prefix_dids]\n"
        '"KVD3234" = "725-404-3234"\n'
        '"KVD3283" = "+17254043283"\n'
    )
    cfg = load_telephony_config(make_config_file(append=toml))
    assert cfg.cid_prefix_did_map == {
        "KVD3234": "7254043234",
        "KVD3283": "17254043283",
    }


def test_cid_prefix_dids_non_table_rejected(make_config_file):
    """A scalar `cid_prefix_dids` (not a table) is a hard config error."""
    bad_toml = VALID_TELEPHONY_TOML + '\ncid_prefix_dids = "not-a-table"\n'
    with pytest.raises(ConfigError, match="cid_prefix_dids"):
        load_telephony_config(make_config_file(append=bad_toml))


def test_shipped_telephony_toml_maps_both_vegas_cid_prefixes(make_config_file):
    """The shipped configs/telephony.toml maps all three Las Vegas CID-name-prefix
    tags to their DIDs (Approach C per-DID reply resolution), plus the KVD1800
    toll-free tag (quick 260728-tfn -- 855-916-INFO, ordered 2026-07-28).
    The 1800 row is asserted by SHAPE only (a non-empty digit string), never
    by its literal digits, so a future number change stays a pure TOML edit
    with no test churn."""
    cfg = load_telephony_config(APP_ROOT / "configs" / "telephony.toml")
    assert {
        k: v for k, v in cfg.cid_prefix_did_map.items() if k != "KVD1800"
    } == {
        "KVD3234": "7254043234",
        "KVD3283": "7254043283",
        "KVD8283": "7254048283",
    }
    tollfree = cfg.cid_prefix_did_map.get("KVD1800", "")
    assert tollfree.isdigit() and len(tollfree) >= 10


# --- Quick task 260717-o2q: [telephony].otp_only_dids (per-DID gate policy Part A) ---


def test_otp_only_dids_absent_defaults_empty(make_config_file):
    """No `otp_only_dids` line -> `()` (every DID is concierge, byte-identical
    to before this field existed)."""
    path = make_config_file(append=VALID_TELEPHONY_TOML)
    assert load_telephony_config(path).otp_only_dids == ()


def test_otp_only_dids_parses_and_normalizes(make_config_file):
    """`otp_only_dids` normalizes to digits-only DIDs (same rule as
    sms_dids/cid_prefix_dids values), order preserved."""
    toml = VALID_TELEPHONY_TOML + (
        '\notp_only_dids = ["725-404-3234", "+17254043283", "7254043234"]\n'
    )
    cfg = load_telephony_config(make_config_file(append=toml))
    assert cfg.otp_only_dids == ("7254043234", "17254043283", "7254043234")


def test_otp_only_dids_non_list_rejected(make_config_file):
    """A scalar `otp_only_dids` (not an array) is a hard config error whose
    message names the offending field."""
    bad_toml = VALID_TELEPHONY_TOML + '\notp_only_dids = "7254043234"\n'
    with pytest.raises(ConfigError, match="otp_only_dids"):
        load_telephony_config(make_config_file(append=bad_toml))


def test_shipped_telephony_toml_seeds_all_three_vegas_otp_only_dids(make_config_file):
    """The shipped configs/telephony.toml seeds otp_only_dids with exactly the
    three Las Vegas DIDs (per-DID gate policy Part A, extended to 8283 by
    quick task 260727-pdh) plus the toll-free DID (260728-tfn) -- the latter
    asserted by consistency with the KVD1800 cid-prefix row, never by its
    placeholder digits."""
    cfg = load_telephony_config(APP_ROOT / "configs" / "telephony.toml")
    assert cfg.otp_only_dids[:3] == ("7254043234", "7254043283", "7254048283")
    assert len(cfg.otp_only_dids) == 4
    assert cfg.otp_only_dids[3] == cfg.cid_prefix_did_map["KVD1800"]


# --- Quick task 260727-ohq: [[telephony.announcement]].dids (per-DID scoping) --
#
# An entry with a non-empty `dids` list only dispatches when the call's
# resolved dialed_did is in that list; absent/empty stays GLOBAL. Parsed via
# the same `_parse_sms_dids` normalizer as sms_dids/sms_reply_dids.


def test_announcement_dids_absent_defaults_empty(make_config_file):
    """No `dids` line -> `()` -- the entry is GLOBAL, byte-identical to the
    pre-260727-ohq announcement shape."""
    path = make_config_file(append=VALID_TELEPHONY_TOML + VALID_ANNOUNCEMENT_TOML)
    cfg = load_telephony_config(path)
    assert cfg.announcements[0].dids == ()


def test_announcement_dids_empty_array_defaults_empty(make_config_file):
    """`dids = []` -> `()` -- explicit empty is treated identically to
    absent (also GLOBAL)."""
    empty_toml = VALID_ANNOUNCEMENT_TOML.rstrip() + "\ndids = []\n"
    path = make_config_file(append=VALID_TELEPHONY_TOML + empty_toml)
    cfg = load_telephony_config(path)
    assert cfg.announcements[0].dids == ()


def test_announcement_dids_parses_and_normalizes(make_config_file):
    """`dids` normalizes to digits-only DIDs (same rule as sms_dids), order
    preserved, blank/junk elements dropped."""
    dids_toml = VALID_ANNOUNCEMENT_TOML.rstrip() + (
        '\ndids = ["725-404-8283", "+17254043234", "  "]\n'
    )
    path = make_config_file(append=VALID_TELEPHONY_TOML + dids_toml)
    cfg = load_telephony_config(path)
    assert cfg.announcements[0].dids == ("7254048283", "17254043234")


def test_announcement_dids_non_list_rejected(make_config_file):
    """A scalar `dids` (not an array) is a hard config error naming the
    field."""
    bad_toml = VALID_ANNOUNCEMENT_TOML.rstrip() + '\ndids = "7254048283"\n'
    path = make_config_file(append=VALID_TELEPHONY_TOML + bad_toml)
    with pytest.raises(ConfigError, match="dids"):
        load_telephony_config(path)


def test_shipped_telephony_toml_announcement_dids_now_per_game_scoped(make_config_file):
    """D-04/D-02 (amended, quick task 260727-pdh; fourth entry 260728-tfn):
    the shipped configs/telephony.toml now ships FOUR per-DID game entries,
    each scoped to its own single DID -- no entry stays GLOBAL any more.
    The 1800 entry's DID is asserted by consistency with the KVD1800
    cid-prefix row (number-swap-safe); the RICK playback entry (quick
    260729-rck) shares that same DID."""
    cfg = load_telephony_config(APP_ROOT / "configs" / "telephony.toml")
    assert len(cfg.announcements) == 5
    assert cfg.announcements[0].dids == ("7254043234",)
    assert cfg.announcements[1].dids == ("7254043283",)
    assert cfg.announcements[2].dids == ("7254048283",)
    assert cfg.announcements[3].dids == (cfg.cid_prefix_did_map["KVD1800"],)
    assert cfg.announcements[4].dids == (cfg.cid_prefix_did_map["KVD1800"],)


def test_shipped_telephony_toml_three_games_share_template_distinct_code_names(
    make_config_file,
):
    """All four shipped OTP game entries carry ONE identical line_template
    containing a {code} placeholder (the RICK playback entry has none by
    design), and all five code_env_var names are distinct (the cheapest
    available proxy for the distinct-code-value constraint, since the
    values themselves live only in SSM)."""
    cfg = load_telephony_config(APP_ROOT / "configs" / "telephony.toml")
    assert len(cfg.announcements) == 5
    otp_entries = [e for e in cfg.announcements if not e.audio_dir]
    assert len(otp_entries) == 4
    templates = {e.line_template for e in otp_entries}
    assert len(templates) == 1
    assert "{code}" in next(iter(templates))
    code_env_vars = [e.code_env_var for e in cfg.announcements]
    assert len(set(code_env_vars)) == len(code_env_vars) == 5


def test_shipped_telephony_toml_3283_game_entry(make_config_file):
    """The 3283 game entry: its own DID, its own distinct code_env_var NAME,
    its own single-DID sms_reply_dids, and NO words_env_var (numeric-only,
    unchanged behavior)."""
    cfg = load_telephony_config(APP_ROOT / "configs" / "telephony.toml")
    entry = cfg.announcements[1]
    assert entry.dids == ("7254043283",)
    assert entry.code_env_var == "CTF_ANNOUNCEMENT_CODE_3283"
    assert entry.sms_reply_dids == ("7254043283",)
    assert entry.words_env_var == ""


def test_shipped_telephony_toml_8283_game_entry(make_config_file):
    """The 8283 game entry: its own DID, both env var NAMES (code + words),
    and its own single-DID sms_reply_dids -- the either-factor split launch
    state (numeric live, spoken inert behind the __unset__ sentinel) is
    proven at the controller layer, not here."""
    cfg = load_telephony_config(APP_ROOT / "configs" / "telephony.toml")
    entry = cfg.announcements[2]
    assert entry.dids == ("7254048283",)
    assert entry.code_env_var == "CTF_ANNOUNCEMENT_CODE_UCTF"
    assert entry.words_env_var == "CTF_ANNOUNCEMENT_WORDS_UCTF"
    assert entry.sms_reply_dids == ("7254048283",)


# --- Quick task 260729-gfr: [telephony].gate_fail_audio ---------------------


def test_gate_fail_audio_absent_defaults_empty(make_config_file):
    """No `gate_fail_audio` line -> "" (spoken goodbye, byte-identical to
    pre-gfr behavior)."""
    path = make_config_file(append=VALID_TELEPHONY_TOML)
    assert load_telephony_config(path).gate_fail_audio == ""


def test_gate_fail_audio_parses_and_strips(make_config_file):
    toml = VALID_TELEPHONY_TOML + '\ngate_fail_audio = "  assets/telephony/rick/fail.wav  "\n'
    cfg = load_telephony_config(make_config_file(append=toml))
    assert cfg.gate_fail_audio == "assets/telephony/rick/fail.wav"


def test_shipped_telephony_toml_gate_fail_audio_is_short_rickroll(make_config_file):
    """The shipped config points the gate-fail clip at the short rickroll
    (S3-synced at boot; a missing file degrades to the spoken goodbye)."""
    cfg = load_telephony_config(APP_ROOT / "configs" / "telephony.toml")
    assert cfg.gate_fail_audio == "assets/telephony/rick/rick-roll-sound-effect.wav"


# --- Quick task 260729-rck: [[telephony.announcement]].audio_dir ------------
#
# A playback game: on code match the controller plays a continuous shuffle
# of the .wav clips in audio_dir instead of the OTP script. Mutually
# exclusive with otp_url/line_template; max_play_seconds caps the loop.

AUDIO_ANNOUNCEMENT_TOML = """
[[telephony.announcement]]
code_env_var = "CTF_ANNOUNCEMENT_CODE_AUDIO_TEST"
audio_dir = "assets/telephony/rick"
"""


def test_announcement_audio_dir_parses_without_otp_fields(make_config_file):
    """An audio entry needs NO otp_url/line_template -- both default to ""
    and max_play_seconds defaults to 180."""
    path = make_config_file(append=VALID_TELEPHONY_TOML + AUDIO_ANNOUNCEMENT_TOML)
    cfg = load_telephony_config(path)
    entry = cfg.announcements[0]
    assert entry.audio_dir == "assets/telephony/rick"
    assert entry.otp_url == ""
    assert entry.line_template == ""
    assert entry.max_play_seconds == 180.0


def test_announcement_audio_dir_mutually_exclusive_with_otp_url(make_config_file):
    """audio_dir + otp_url in one entry is a hard config error -- an entry
    is either a playback game or an OTP game, never both."""
    bad_toml = AUDIO_ANNOUNCEMENT_TOML.rstrip() + '\notp_url = "https://example.test/otp"\n'
    path = make_config_file(append=VALID_TELEPHONY_TOML + bad_toml)
    with pytest.raises(ConfigError, match="mutually exclusive"):
        load_telephony_config(path)


def test_announcement_audio_dir_mutually_exclusive_with_line_template(make_config_file):
    bad_toml = AUDIO_ANNOUNCEMENT_TOML.rstrip() + '\nline_template = "here is {code}"\n'
    path = make_config_file(append=VALID_TELEPHONY_TOML + bad_toml)
    with pytest.raises(ConfigError, match="mutually exclusive"):
        load_telephony_config(path)


def test_announcement_max_play_seconds_parses_and_bounds(make_config_file):
    """max_play_seconds accepts a positive number <= 3600; zero, negative,
    over-bound, and non-numeric values are hard config errors."""
    ok_toml = AUDIO_ANNOUNCEMENT_TOML.rstrip() + "\nmax_play_seconds = 60\n"
    cfg = load_telephony_config(make_config_file(append=VALID_TELEPHONY_TOML + ok_toml))
    assert cfg.announcements[0].max_play_seconds == 60.0

    for bad_value in ("0", "-5", "3601", '"long"', "true"):
        bad_toml = AUDIO_ANNOUNCEMENT_TOML.rstrip() + f"\nmax_play_seconds = {bad_value}\n"
        with pytest.raises(ConfigError, match="max_play_seconds"):
            load_telephony_config(make_config_file(append=VALID_TELEPHONY_TOML + bad_toml))


def test_announcement_max_play_seconds_requires_audio_dir(make_config_file):
    """max_play_seconds on an OTP entry (no audio_dir) is a hard config
    error naming the constraint."""
    bad_toml = VALID_ANNOUNCEMENT_TOML.rstrip() + "\nmax_play_seconds = 60\n"
    path = make_config_file(append=VALID_TELEPHONY_TOML + bad_toml)
    with pytest.raises(ConfigError, match="only valid with audio_dir"):
        load_telephony_config(path)


def test_announcement_otp_entry_unaffected_by_audio_fields(make_config_file):
    """A plain OTP entry (no audio fields) parses byte-identically to the
    pre-rck shape: audio_dir "" and the max_play_seconds default."""
    path = make_config_file(append=VALID_TELEPHONY_TOML + VALID_ANNOUNCEMENT_TOML)
    cfg = load_telephony_config(path)
    assert cfg.announcements[0].audio_dir == ""
    assert cfg.announcements[0].max_play_seconds == 180.0


def test_shipped_telephony_toml_1800_game_entry(make_config_file):
    """The 1800 toll-free game entry (quick 260728-tfn, 855-916-INFO): its
    own DID (asserted via consistency with the KVD1800 cid-prefix row, so a
    future number change never causes test churn), its own distinct
    code_env_var NAME, numeric-only (no words_env_var), and the claim SMS
    sent from the VEGAS ordered-fallback pool (operator decision
    2026-07-29): an unverified toll-free number cannot send SMS on US
    carriers, so sms_reply_dids stays empty (pool-mode) and sms_dids
    carries the three 725 numbers, tried in order."""
    cfg = load_telephony_config(APP_ROOT / "configs" / "telephony.toml")
    entry = cfg.announcements[3]
    assert entry.dids == (cfg.cid_prefix_did_map["KVD1800"],)
    assert entry.code_env_var == "CTF_ANNOUNCEMENT_CODE_1800"
    assert entry.words_env_var == ""
    assert entry.sms_dids == ("7254043234", "7254043283", "7254048283")
    assert entry.sms_reply_dids == ()
    assert entry.sms_relay_url == "https://auth.klankermaker.ai/use1/ctf/sms"


def test_shipped_telephony_toml_rick_game_entry(make_config_file):
    """The RICK playback entry (quick 260729-rck): same DID as the 1800
    OTP game (two codes, one line), its own distinct code_env_var NAME, a
    playback shape (audio_dir set, no otp_url/line_template/SMS), and the
    180s toll-free spend cap."""
    cfg = load_telephony_config(APP_ROOT / "configs" / "telephony.toml")
    entry = cfg.announcements[4]
    assert entry.dids == (cfg.cid_prefix_did_map["KVD1800"],)
    assert entry.code_env_var == "CTF_ANNOUNCEMENT_CODE_RICK"
    assert entry.audio_dir == "assets/telephony/rick"
    assert entry.max_play_seconds == 180.0
    assert entry.otp_url == ""
    assert entry.line_template == ""
    assert entry.words_env_var == ""
    assert entry.sms_reply_dids == ()
    assert entry.sms_relay_url == ""


# --- Quick task 260727-pdh: [[telephony.announcement]].words_env_var --------
#
# An OPTIONAL, NAME-only spoken-trigger env var, mirroring code_env_var but
# with NO non-empty validation (an entry may legitimately have no spoken
# trigger at all). Deliberately NOT named `passphrase_env_var` -- the shared
# D-09 credential-field gate refuses any key containing a `passphrase` token
# (same precedent as the existing otp_env_var rename).


def test_announcement_words_env_var_absent_defaults_empty(make_config_file):
    """No `words_env_var` line -> "" -- the entry has no spoken trigger,
    byte-identical to the pre-260727-pdh announcement shape."""
    path = make_config_file(append=VALID_TELEPHONY_TOML + VALID_ANNOUNCEMENT_TOML)
    cfg = load_telephony_config(path)
    assert cfg.announcements[0].words_env_var == ""


def test_announcement_words_env_var_parses_and_strips(make_config_file):
    """`words_env_var` parses to the exact NAME string, stripped."""
    words_toml = VALID_ANNOUNCEMENT_TOML.rstrip() + (
        '\nwords_env_var = "  CTF_ANNOUNCEMENT_WORDS_UCTF  "\n'
    )
    path = make_config_file(append=VALID_TELEPHONY_TOML + words_toml)
    cfg = load_telephony_config(path)
    assert cfg.announcements[0].words_env_var == "CTF_ANNOUNCEMENT_WORDS_UCTF"


def test_announcement_passphrase_env_var_key_rejected(make_config_file):
    """The D-09 credential-field gate refuses `passphrase_env_var` outright
    -- the constraint that forced the rename to `words_env_var` (mirrors the
    existing `otp_auth_env_var` -> `otp_env_var` precedent)."""
    bad_toml = VALID_ANNOUNCEMENT_TOML.rstrip() + (
        '\npassphrase_env_var = "CTF_ANNOUNCEMENT_WORDS_UCTF"\n'
    )
    path = make_config_file(append=VALID_TELEPHONY_TOML + bad_toml)
    with pytest.raises(ConfigError, match="credential"):
        load_telephony_config(path)


# --- Quick task 260727-qfq: [[telephony.announcement]].sms_claim_url_template
#
# An OPTIONAL, PUBLIC claim-URL template that replaces ONLY the URL portion
# of the mid-call SMS's first message (D-07). Must contain `{code}` when
# present (mirrors the line_template rule); absent/empty -> the controller's
# built-in default claim URL, byte-identical to every pre-qfq entry.


def test_sms_claim_url_template_absent_defaults_empty(make_config_file):
    """No `sms_claim_url_template` line -> "" (backward compatible --
    byte-identical to the pre-260727-qfq announcement shape)."""
    path = make_config_file(append=VALID_TELEPHONY_TOML + VALID_ANNOUNCEMENT_TOML)
    cfg = load_telephony_config(path)
    assert cfg.announcements[0].sms_claim_url_template == ""


def test_sms_claim_url_template_parses_and_strips(make_config_file):
    """A present value parses to the exact string, stripped."""
    claim_toml = VALID_ANNOUNCEMENT_TOML.rstrip() + (
        '\nsms_claim_url_template = "  https://q.defcon.run/c?c=didhtp3234&v={code}  "\n'
    )
    path = make_config_file(append=VALID_TELEPHONY_TOML + claim_toml)
    cfg = load_telephony_config(path)
    assert (
        cfg.announcements[0].sms_claim_url_template
        == "https://q.defcon.run/c?c=didhtp3234&v={code}"
    )


def test_sms_claim_url_template_without_code_placeholder_rejected(make_config_file):
    """A template missing `{code}` is a hard config error naming the field
    (mirrors the `line_template` rule)."""
    bad_toml = VALID_ANNOUNCEMENT_TOML.rstrip() + (
        '\nsms_claim_url_template = "https://q.defcon.run/c?c=didhtp3234"\n'
    )
    path = make_config_file(append=VALID_TELEPHONY_TOML + bad_toml)
    with pytest.raises(ConfigError, match="sms_claim_url_template"):
        load_telephony_config(path)


def test_sms_claim_url_template_key_clears_credential_gate(make_config_file):
    """The D-09 credential-field gate ACCEPTS `sms_claim_url_template` --
    its tokens (sms/claim/url/template) contain none of the refused
    substrings (api_key|key|keys|secret|secrets|token|tokens|password|...),
    unlike 260727-pdh's `passphrase_env_var` collision. Pins the constraint
    2 finding from this task's planning pass rather than leaving it to a
    future reader's inspection."""
    claim_toml = VALID_ANNOUNCEMENT_TOML.rstrip() + (
        '\nsms_claim_url_template = "https://q.defcon.run/c?c=didhtp3234&v={code}"\n'
    )
    path = make_config_file(append=VALID_TELEPHONY_TOML + claim_toml)
    cfg = load_telephony_config(path)  # must NOT raise ConfigError
    assert cfg.announcements[0].sms_claim_url_template != ""


def test_shipped_telephony_toml_per_game_otp_urls_and_claim_templates(make_config_file):
    """D-06/D-07: each of the four shipped OTP entries' `otp_url` carries
    its own game query, and each `sms_claim_url_template` carries its own
    didhtp slug -- all eight values distinct, all four templates contain
    `{code}`, and every value is pure 7-bit ASCII (the shared GSM-7 rule).
    The RICK playback entry (audio_dir set) is excluded by design -- it
    has no issuer and no claim URL."""
    cfg = load_telephony_config(APP_ROOT / "configs" / "telephony.toml")
    assert len(cfg.announcements) == 5
    otp_entries = [e for e in cfg.announcements if not e.audio_dir]
    assert len(otp_entries) == 4

    otp_urls = [e.otp_url for e in otp_entries]
    assert otp_urls == [
        "https://auth.klankermaker.ai/use1/ctf/otp?g=3234",
        "https://auth.klankermaker.ai/use1/ctf/otp?g=3283",
        "https://auth.klankermaker.ai/use1/ctf/otp?g=8283",
        "https://auth.klankermaker.ai/use1/ctf/otp?g=1800",
    ]
    assert len(set(otp_urls)) == 4

    claim_templates = [e.sms_claim_url_template for e in otp_entries]
    # Per-game q.defcon.run slugs (c3234/c3283/c8283/c1800): the resolver's
    # shared /c slug hardcodes c=didhtp1 in its destination and preserveQuery
    # cannot override it, so each game gets its own Qr row (live 2026-07-27;
    # the c1800 row is a forward contract, DC34-side, not yet created).
    assert claim_templates == [
        "https://q.defcon.run/c3234?v={code}",
        "https://q.defcon.run/c3283?v={code}",
        "https://q.defcon.run/c8283?v={code}",
        "https://q.defcon.run/c1800?v={code}",
    ]
    assert len(set(claim_templates)) == 4
    for template in claim_templates:
        assert "{code}" in template
        assert all(ord(c) < 128 for c in template)
