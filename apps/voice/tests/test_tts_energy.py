"""Phase 07.1: tunable TTS voice settings (energy retune).

stability / similarity_boost / style (+ existing speed) are exposed in
pipeline.toml [tts], threaded to BOTH the live ElevenLabs factory and the
pre-rendered greeting render script so the welcome clip and live voice share
one identical character.
"""

from klanker_voice.config import load_config
from klanker_voice.factories import build_tts


def test_tts_config_parses_voice_settings():
    cfg = load_config()
    for v in (cfg.tts.stability, cfg.tts.similarity_boost, cfg.tts.style, cfg.tts.speed):
        assert isinstance(v, float)
    assert 0.0 <= cfg.tts.stability <= 1.0
    assert 0.0 <= cfg.tts.similarity_boost <= 1.0
    assert 0.0 <= cfg.tts.style <= 1.0
    assert 0.7 <= cfg.tts.speed <= 1.2


def test_factory_threads_voice_settings(stub_provider_keys):
    cfg = load_config()
    tts = build_tts(cfg)
    assert tts._settings.stability == cfg.tts.stability
    assert tts._settings.similarity_boost == cfg.tts.similarity_boost
    assert tts._settings.style == cfg.tts.style
    assert tts._settings.speed == cfg.tts.speed


def test_render_greetings_voice_settings_match_config():
    # The greeting render must speak with the SAME voice settings as the live
    # service, sourced from the same pipeline.toml [tts] table.
    import importlib.util
    from pathlib import Path

    app_root = Path(__file__).resolve().parents[1]
    spec = importlib.util.spec_from_file_location(
        "render_greetings", app_root / "scripts" / "render_greetings.py"
    )
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)

    vs = mod.voice_settings_from_config()
    cfg = load_config()
    assert vs["stability"] == cfg.tts.stability
    assert vs["similarity_boost"] == cfg.tts.similarity_boost
    assert vs["style"] == cfg.tts.style
    assert vs["speed"] == cfg.tts.speed
