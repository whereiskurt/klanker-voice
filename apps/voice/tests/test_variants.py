"""Unit tests for klanker_voice.variants (full-duplex, 2026-07-10)."""

from __future__ import annotations

from klanker_voice import variants
from klanker_voice.config import APP_ROOT


def test_default_variant_resolves_to_none_config():
    # voice1 == the shipped default pipeline.toml (loaders' own resolution).
    assert variants.variant_config_path("voice1") is None


def test_voice2_resolves_under_app_root():
    path = variants.variant_config_path("voice2")
    assert path is not None
    assert path == (APP_ROOT / "configs/voice2.toml").resolve()
    assert path.is_file()  # the config is actually checked in


def test_unknown_variant_falls_back_to_default():
    assert variants.normalize_variant("voice999") == variants.DEFAULT_VARIANT
    assert variants.normalize_variant(None) == variants.DEFAULT_VARIANT
    assert variants.normalize_variant("") == variants.DEFAULT_VARIANT
    # Unknown -> default config (None), never a filesystem path.
    assert variants.variant_config_path("voice999") is None


def test_path_traversal_attempt_is_not_honored():
    # A hostile value is only ever a registry key — it can't escape to a path.
    hostile = "../../../../etc/passwd"
    assert variants.normalize_variant(hostile) == variants.DEFAULT_VARIANT
    assert variants.variant_config_path(hostile) is None


def test_whitespace_is_trimmed():
    assert variants.normalize_variant("  voice2  ") == "voice2"


def test_known_variants_listed():
    assert set(variants.known_variants()) == {"voice1", "voice2"}
