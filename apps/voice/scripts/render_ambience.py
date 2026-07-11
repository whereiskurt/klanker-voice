"""Render per-topic ambient beds via ElevenLabs Sound Effects.

Each bed is a MONO 24 kHz WAV (the SmallWebRTC/ElevenLabs output rate) that
``SoundfileMixer`` mixes UNDER KPH's voice while a topic that declares
``ambience: <name>`` is active (see knowledge/router/topic-map.yaml + router.py).
The mixer does NOT resample, so rate/channels must match exactly.

To beat loop-detection, each bed is STITCHED from several ElevenLabs SFX clips
(22s max each) into one longer loop (~LOOP_CLIPS * 22s).

Run:  make -C apps/voice ambience            # all beds
      .venv/bin/python scripts/render_ambience.py coffee-shop   # one bed
Billed ElevenLabs call. The API key needs the `sound_generation` permission.
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

OUT_DIR = APP_ROOT / "assets" / "ambience"
TARGET_RATE = 24000       # must match the output transport (mixer won't resample)
CLIP_SECONDS = 22.0       # ElevenLabs SFX max per call
LOOP_CLIPS = 3            # stitch this many -> ~66s loop (harder to detect)
PROMPT_INFLUENCE = 0.4

#: name -> generation prompt. The name is the SoundfileMixer sound + the
#: topic-map `ambience:` value.
AMBIENCE: dict[str, str] = {
    "coffee-shop": (
        "Cozy but busy coffee shop ambience: soft indistinct background chatter, "
        "the occasional hiss and steam of an espresso machine, gentle clinks of "
        "ceramic cups and saucers, distant milk frothing, warm room tone. No "
        "music, no clearly intelligible words. Steady, loopable."
    ),
    "conference": (
        "Cavernous convention-center hall from inside a moving crowd: footsteps "
        "and shoe squeaks on hard polished floors, rolling suitcase wheels "
        "trundling past, a boomy far-off PA announcement echoing under a high "
        "ceiling, huge open-hall reverb, a diffuse distant crowd murmur. Airy, "
        "spacious, clearly a giant hall — NOT a cafe: no espresso machine, no "
        "cups, no music, no intelligible words. Steady, loopable."
    ),
}


def _render_clip(api_key: str, prompt: str, out_mp3: Path) -> None:
    resp = requests.post(
        "https://api.elevenlabs.io/v1/sound-generation",
        headers={"xi-api-key": api_key, "Content-Type": "application/json"},
        json={
            "text": prompt,
            "duration_seconds": CLIP_SECONDS,
            "prompt_influence": PROMPT_INFLUENCE,
            "loop": True,
            "output_format": "mp3_44100_128",
        },
        timeout=120,
    )
    if resp.status_code != 200:
        raise SystemExit(f"ElevenLabs SFX failed: HTTP {resp.status_code} {resp.text[:300]}")
    out_mp3.write_bytes(resp.content)


def render(name: str, prompt: str, api_key: str) -> None:
    print(f"generating '{name}' bed ({LOOP_CLIPS} x {CLIP_SECONDS:.0f}s) via ElevenLabs SFX…")
    tmp = Path(tempfile.mkdtemp())
    clips = []
    for i in range(LOOP_CLIPS):
        mp3 = tmp / f"{name}-{i}.mp3"
        _render_clip(api_key, prompt, mp3)
        clips.append(mp3)
    # Concatenate the clips, then convert to mono / 24kHz / 16-bit WAV.
    concat_list = tmp / "list.txt"
    concat_list.write_text("".join(f"file '{c}'\n" for c in clips))
    OUT_DIR.mkdir(parents=True, exist_ok=True)
    out_wav = OUT_DIR / f"{name}.wav"
    subprocess.run(
        ["ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
         "-f", "concat", "-safe", "0", "-i", str(concat_list),
         "-ac", "1", "-ar", str(TARGET_RATE), "-sample_fmt", "s16", str(out_wav)],
        check=True,
    )
    shutil.rmtree(tmp, ignore_errors=True)
    print(f"wrote {out_wav} (mono, {TARGET_RATE} Hz, ~{LOOP_CLIPS * CLIP_SECONDS:.0f}s loop)")


def main() -> int:
    api_key = os.environ.get("ELEVENLABS_API_KEY")
    if not api_key:
        print("ELEVENLABS_API_KEY not set — run `make -C apps/voice env` first.", file=sys.stderr)
        return 1
    if not shutil.which("ffmpeg"):
        print("ffmpeg not found — needed to stitch + convert to mono 24kHz WAV.", file=sys.stderr)
        return 1
    which = sys.argv[1:] or list(AMBIENCE)
    for name in which:
        if name not in AMBIENCE:
            print(f"unknown ambience '{name}' (have: {', '.join(AMBIENCE)})", file=sys.stderr)
            return 1
        render(name, AMBIENCE[name], api_key)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
