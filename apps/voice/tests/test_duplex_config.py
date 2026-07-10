"""Unit tests for the optional [duplex] table loader (full-duplex, 2026-07-10).

Uses the shared ``make_config_file`` fixture (tests/conftest.py) so [duplex] is
exercised against a real, minimal pipeline.toml.
"""

from __future__ import annotations

import pytest

from klanker_voice.config import (
    DEFAULT_BACKCHANNEL_WORDS,
    DEFAULT_EMITTER_PHRASES,
    ConfigError,
    load_duplex_config,
)


def test_absent_table_yields_disabled_default(make_config_file):
    # The shipped pipeline.toml / voice1 has no [duplex] table.
    cfg = load_duplex_config(make_config_file())
    assert cfg.enabled is False
    assert cfg.backchannel_emitter is False
    assert cfg.backchannel_words == DEFAULT_BACKCHANNEL_WORDS
    assert cfg.emitter_phrases == DEFAULT_EMITTER_PHRASES


def test_enabled_table_parses(make_config_file):
    path = make_config_file(
        append=(
            "[duplex]\n"
            "enabled = true\n"
            "backchannel_emitter = true\n"
            "max_backchannel_words = 4\n"
            "interruption_hold_ms = 300\n"
            "emitter_min_gap_seconds = 5.0\n"
        )
    )
    cfg = load_duplex_config(path)
    assert cfg.enabled is True
    assert cfg.backchannel_emitter is True
    assert cfg.max_backchannel_words == 4
    assert cfg.interruption_hold_ms == 300
    assert cfg.emitter_min_gap_seconds == 5.0


def test_custom_word_lists_override_defaults(make_config_file):
    path = make_config_file(
        append=(
            "[duplex]\n"
            "enabled = true\n"
            'backchannel_words = ["yeah", "OK", "Right"]\n'
            'emitter_phrases = ["mm-hm.", "sure."]\n'
        )
    )
    cfg = load_duplex_config(path)
    assert cfg.backchannel_words == ("yeah", "ok", "right")  # lowercased
    assert cfg.emitter_phrases == ("mm-hm.", "sure.")


def test_bad_word_list_type_raises(make_config_file):
    path = make_config_file(append="[duplex]\nbackchannel_words = \"yeah\"\n")
    with pytest.raises(ConfigError):
        load_duplex_config(path)


def test_empty_string_entry_raises(make_config_file):
    path = make_config_file(append='[duplex]\nemitter_phrases = ["ok", ""]\n')
    with pytest.raises(ConfigError):
        load_duplex_config(path)


def test_out_of_range_hold_raises(make_config_file):
    path = make_config_file(append="[duplex]\ninterruption_hold_ms = 99999\n")
    with pytest.raises(ConfigError):
        load_duplex_config(path)
