"""Render the pre-recorded KPH greetings from the CONFIGURED voice (D-04 slick-start).

Reads voice_id from apps/voice/pipeline.toml so the clips always match the live
TTS voice, then renders each greetings.source.json line to MP3 (iOS-safe) and
writes a manifest the client consumes + the drift-guard test checks. Run via
`make -C apps/voice greetings`. Requires ELEVENLABS_API_KEY in the env.
"""
import json
import os
import sys
import tomllib
from pathlib import Path

import httpx

APP_ROOT = Path(__file__).resolve().parents[1]          # apps/voice
PIPELINE_TOML = APP_ROOT / "pipeline.toml"
GREETINGS_DIR = APP_ROOT / "client" / "public" / "greetings"
SOURCE = GREETINGS_DIR / "greetings.source.json"
MANIFEST = GREETINGS_DIR / "greetings.manifest.json"
API_BASE = "https://api.elevenlabs.io/v1"
MODEL_ID = "eleven_flash_v2_5"
OUTPUT_FORMAT = "mp3_44100_128"

def voice_id_from_config() -> str:
    data = tomllib.loads(PIPELINE_TOML.read_text())
    vid = str(data.get("tts", {}).get("voice_id", "")).strip()
    if not vid:
        sys.exit("render_greetings: tts.voice_id is empty in pipeline.toml")
    return vid

def voice_settings_from_config() -> dict:
    """Read the SAME [tts] voice settings the live service uses (07.1), so the
    pre-rendered welcome clip and the live voice share one identical character."""
    tts = tomllib.loads(PIPELINE_TOML.read_text()).get("tts", {})
    return {
        "speed": float(tts.get("speed", 1.0)),
        "stability": float(tts.get("stability", 0.4)),
        "similarity_boost": float(tts.get("similarity_boost", 0.85)),
        "style": float(tts.get("style", 0.1)),
    }

def main() -> None:
    key = os.environ.get("ELEVENLABS_API_KEY")
    if not key:
        sys.exit("render_greetings: ELEVENLABS_API_KEY not set (run `make -C apps/voice env`)")
    voice_id = voice_id_from_config()
    voice_settings = voice_settings_from_config()
    texts = json.loads(SOURCE.read_text())["greetings"]
    GREETINGS_DIR.mkdir(parents=True, exist_ok=True)

    clips = []
    with httpx.Client(headers={"xi-api-key": key}, timeout=60.0) as client:
        for i, text in enumerate(texts, 1):
            fname = f"greeting-{i}.mp3"
            print(f"rendering greeting {i}/{len(texts)} -> {fname}")
            resp = client.post(
                f"{API_BASE}/text-to-speech/{voice_id}",
                params={"output_format": OUTPUT_FORMAT},
                json={"text": text, "model_id": MODEL_ID, "voice_settings": voice_settings},
            )
            resp.raise_for_status()
            (GREETINGS_DIR / fname).write_bytes(resp.content)
            clips.append({"text": text, "file": fname})

    MANIFEST.write_text(json.dumps(
        {"voiceId": voice_id, "model": MODEL_ID, "clips": clips}, indent=2) + "\n")
    print(f"wrote {MANIFEST.relative_to(APP_ROOT)} ({len(clips)} clips, voice {voice_id})")

if __name__ == "__main__":
    main()
