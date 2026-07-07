"""Fails CI if the rendered greetings were made from a different voice than
the one pipeline.toml currently ships — i.e. someone swapped the TTS voice
without re-running `make -C apps/voice greetings`. Closes the stale-voice gap
without needing an ElevenLabs key in CI (pure string comparison)."""
import json
import tomllib
from pathlib import Path

APP_ROOT = Path(__file__).resolve().parents[1]
MANIFEST = APP_ROOT / "client" / "public" / "greetings" / "greetings.manifest.json"
PIPELINE_TOML = APP_ROOT / "pipeline.toml"

def test_greeting_clips_match_configured_voice():
    manifest = json.loads(MANIFEST.read_text())
    configured = str(tomllib.loads(PIPELINE_TOML.read_text())["tts"]["voice_id"]).strip()
    assert manifest["voiceId"] == configured, (
        f"greeting clips were rendered from {manifest['voiceId']!r} but pipeline.toml "
        f"ships {configured!r} — re-run `make -C apps/voice greetings` and commit the clips."
    )

def test_manifest_lists_all_source_greetings():
    manifest = json.loads(MANIFEST.read_text())
    source = json.loads((MANIFEST.parent / "greetings.source.json").read_text())["greetings"]
    assert [c["text"] for c in manifest["clips"]] == source
