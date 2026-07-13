"""Fails CI if the (eventually) rendered telephony pickup-cue clip was made
from a different voice than the one pipeline.toml currently ships -- i.e.
someone swapped the TTS voice without re-running
`make -C apps/voice pickup-cue`. Mirrors test_greeting_voice_drift.py (B-04)
for the telephony pickup cue (quick task 260713-m9n). Pure string comparison
-- no ElevenLabs key needed, no network, and does NOT require kph-hey.wav to
actually exist yet (the manifest is metadata; the .wav render is a separate
human step -- see render_pickup_cue.py / pickup_cue.load_hey_clip)."""
import json
import re
import tomllib
from pathlib import Path

APP_ROOT = Path(__file__).resolve().parents[1]
TELEPHONY_DIR = APP_ROOT / "assets" / "telephony"
MANIFEST = TELEPHONY_DIR / "pickup-cue.manifest.json"
SOURCE = TELEPHONY_DIR / "pickup-cue.source.json"
PIPELINE_TOML = APP_ROOT / "pipeline.toml"
RENDER_SCRIPT = APP_ROOT / "scripts" / "render_pickup_cue.py"


def test_pickup_cue_manifest_matches_configured_voice():
    manifest = json.loads(MANIFEST.read_text())
    configured = str(tomllib.loads(PIPELINE_TOML.read_text())["tts"]["voice_id"]).strip()
    assert manifest["voiceId"] == configured, (
        f"pickup-cue manifest was rendered from {manifest['voiceId']!r} but pipeline.toml "
        f"ships {configured!r} -- re-run `make -C apps/voice pickup-cue` and commit the clip."
    )


def test_pickup_cue_manifest_matches_source_text():
    manifest = json.loads(MANIFEST.read_text())
    source_text = json.loads(SOURCE.read_text())["hey"]
    assert manifest["clip"]["text"] == source_text


def test_pickup_cue_manifest_sample_rate_is_pipeline_output_rate():
    manifest = json.loads(MANIFEST.read_text())
    # Matches TelephonyOutputTransport.PIPELINE_OUTPUT_SAMPLE_RATE (transport.py)
    # -- so pickup_cue.load_hey_clip's returned sample_rate needs no resample
    # of its own before the TelephonyOutputTransport boundary resample.
    assert manifest["sampleRate"] == 24000


def test_render_script_never_touches_the_browser_greeting_assets():
    """Isolation guard (memory: never casually regenerate greetings) -- the
    telephony render script must never IMPORT render_greetings.py or write a
    path under client/public/greetings/ (a docstring mention of either, in
    prose explaining the isolation, is fine and expected)."""
    text = RENDER_SCRIPT.read_text()
    assert not re.search(r"^\s*(import|from)\s+.*render_greetings", text, re.MULTILINE)
    assert not re.search(r'["\'].*client["\'/]+public["\'/]+greetings', text)
