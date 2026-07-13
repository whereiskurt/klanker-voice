"""Render the pre-rendered KPH "hey" telephony pickup-cue clip (quick task
260713-m9n) from the CONFIGURED voice.

Reads voice_id + the [tts] voice_settings from apps/voice/pipeline.toml so the
clip always matches the live TTS voice, then renders the single
pickup-cue.source.json line to a 24kHz mono WAV (the pipeline's own output
rate -- no MP3 decode needed at playback) and writes a manifest the
drift-guard test checks. Run via `make -C apps/voice pickup-cue`. Requires
ELEVENLABS_API_KEY in the env.

CRITICAL ISOLATION (memory: never casually regenerate greetings): this
script is standalone -- it does NOT import scripts/render_greetings.py and
NEVER writes into the browser client's public greetings asset directory. It
targets assets/telephony/ only, so the hand-spliced browser greeting clip is
never touched.
"""
import json
import os
import sys
import tomllib
import wave
from pathlib import Path

import httpx
from dotenv import load_dotenv

APP_ROOT = Path(__file__).resolve().parents[1]  # apps/voice
PIPELINE_TOML = APP_ROOT / "pipeline.toml"
TELEPHONY_DIR = APP_ROOT / "assets" / "telephony"
SOURCE = TELEPHONY_DIR / "pickup-cue.source.json"
MANIFEST = TELEPHONY_DIR / "pickup-cue.manifest.json"
WAV_FILE = TELEPHONY_DIR / "kph-hey.wav"
API_BASE = "https://api.elevenlabs.io/v1"
MODEL_ID = "eleven_flash_v2_5"
OUTPUT_FORMAT = "pcm_24000"
SAMPLE_RATE = 24000


def voice_id_from_config() -> str:
    data = tomllib.loads(PIPELINE_TOML.read_text())
    vid = str(data.get("tts", {}).get("voice_id", "")).strip()
    if not vid:
        sys.exit("render_pickup_cue: tts.voice_id is empty in pipeline.toml")
    return vid


def voice_settings_from_config() -> dict:
    """Read the SAME [tts] voice settings the live service uses, so the
    pickup cue and the live voice share one identical character (mirrors
    render_greetings.py's own voice_settings_from_config)."""
    tts = tomllib.loads(PIPELINE_TOML.read_text()).get("tts", {})
    return {
        "speed": float(tts.get("speed", 1.0)),
        "stability": float(tts.get("stability", 0.4)),
        "similarity_boost": float(tts.get("similarity_boost", 0.85)),
        "style": float(tts.get("style", 0.1)),
    }


def main() -> None:
    # Load apps/voice/.env the same way render_greetings.py does, so
    # `make env` then `make pickup-cue` works standalone.
    load_dotenv(APP_ROOT / ".env", override=True)
    key = os.environ.get("ELEVENLABS_API_KEY")
    if not key:
        sys.exit("render_pickup_cue: ELEVENLABS_API_KEY not set (run `make -C apps/voice env`)")
    voice_id = voice_id_from_config()
    voice_settings = voice_settings_from_config()
    text = json.loads(SOURCE.read_text())["hey"]
    TELEPHONY_DIR.mkdir(parents=True, exist_ok=True)

    print("rendering pickup-cue hey clip -> kph-hey.wav")
    with httpx.Client(headers={"xi-api-key": key}, timeout=60.0) as client:
        resp = client.post(
            f"{API_BASE}/text-to-speech/{voice_id}",
            params={"output_format": OUTPUT_FORMAT},
            json={"text": text, "model_id": MODEL_ID, "voice_settings": voice_settings},
        )
        resp.raise_for_status()
        pcm = resp.content

    with wave.open(str(WAV_FILE), "wb") as wf:
        wf.setnchannels(1)
        wf.setsampwidth(2)  # 16-bit
        wf.setframerate(SAMPLE_RATE)
        wf.writeframes(pcm)

    MANIFEST.write_text(json.dumps(
        {
            "voiceId": voice_id,
            "model": MODEL_ID,
            "sampleRate": SAMPLE_RATE,
            "clip": {"text": text, "file": "kph-hey.wav"},
        },
        indent=2,
    ) + "\n")
    print(f"wrote {WAV_FILE.relative_to(APP_ROOT)} + {MANIFEST.relative_to(APP_ROOT)} (voice {voice_id})")


if __name__ == "__main__":
    main()
