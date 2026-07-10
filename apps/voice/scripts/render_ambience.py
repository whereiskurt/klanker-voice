"""Render the greenhouse "coffee shop" ambient bed via ElevenLabs Sound Effects.

Writes a MONO 24 kHz WAV (the SmallWebRTC/ElevenLabs output rate) that
`SoundfileMixer` can mix UNDER KPH's voice while recruiting mode is active.
The mixer does NOT resample, so the rate/channels must match exactly (mono,
24000 Hz) or the bed silently won't play.

Run:  make -C apps/voice ambience   (or) .venv/bin/python scripts/render_ambience.py

Billed ElevenLabs call. Idempotent-ish: overwrites the output WAV each run.
"""
from __future__ import annotations

import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

import requests
from dotenv import load_dotenv

APP_ROOT = Path(__file__).resolve().parent.parent
load_dotenv(APP_ROOT / ".env")

OUT_WAV = APP_ROOT / "assets" / "ambience" / "coffee-shop.wav"
TARGET_RATE = 24000  # must match the output transport (SoundfileMixer won't resample)

PROMPT = (
    "Cozy but busy coffee shop ambience: soft indistinct background chatter, the "
    "occasional hiss and steam of an espresso machine, gentle clinks of ceramic "
    "cups and saucers, distant milk frothing, warm room tone. No music, no "
    "clearly intelligible words. Steady, loopable."
)
DURATION_SECONDS = 22.0  # ElevenLabs SFX max
PROMPT_INFLUENCE = 0.4


def main() -> int:
    api_key = os.environ.get("ELEVENLABS_API_KEY")
    if not api_key:
        print("ELEVENLABS_API_KEY not set — run `make -C apps/voice env` first.", file=sys.stderr)
        return 1
    if not shutil.which("ffmpeg"):
        print("ffmpeg not found — needed to convert to mono 24 kHz WAV.", file=sys.stderr)
        return 1

    print(f"generating coffee-shop ambience ({DURATION_SECONDS}s) via ElevenLabs SFX…")
    resp = requests.post(
        "https://api.elevenlabs.io/v1/sound-generation",
        headers={"xi-api-key": api_key, "Content-Type": "application/json"},
        json={
            "text": PROMPT,
            "duration_seconds": DURATION_SECONDS,
            "prompt_influence": PROMPT_INFLUENCE,
            "loop": True,  # seamless loop for a continuous bed
            "output_format": "mp3_44100_128",
        },
        timeout=120,
    )
    if resp.status_code != 200:
        print(f"ElevenLabs SFX failed: HTTP {resp.status_code} {resp.text[:300]}", file=sys.stderr)
        return 1

    OUT_WAV.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(suffix=".mp3", delete=False) as tmp:
        tmp.write(resp.content)
        tmp_mp3 = tmp.name

    # Convert to exactly mono / 24 kHz / 16-bit PCM WAV (mixer requirement).
    subprocess.run(
        ["ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
         "-i", tmp_mp3, "-ac", "1", "-ar", str(TARGET_RATE), "-sample_fmt", "s16",
         str(OUT_WAV)],
        check=True,
    )
    os.unlink(tmp_mp3)
    print(f"wrote {OUT_WAV} (mono, {TARGET_RATE} Hz)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
