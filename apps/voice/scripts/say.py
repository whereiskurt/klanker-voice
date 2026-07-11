"""Speak an arbitrary text block through the CONFIGURED KPH voice — a
deterministic voice-output test loop (Option 1, 2026-07-11).

It reads the SAME ``[tts]`` settings the live service uses (voice_id, speed,
stability, style, similarity) from ``pipeline.toml`` and applies the SAME
``pronunciation.py`` rules the pipeline applies before TTS — so what you hear is
a faithful preview of the live voice. Paste the same block, tweak a knob in
``pipeline.toml`` (or ``configs/voice2.toml``), re-run, and compare by ear.

Usage:
  .venv/bin/python scripts/say.py "your text here"
  echo "your text" | .venv/bin/python scripts/say.py          # paste via stdin
  make -C apps/voice say TEXT="your text"

Flags:
  --raw            skip the pronunciation rules (hear the exact input)
  --config PATH    read [tts] from PATH instead of pipeline.toml
  --save PATH      keep the rendered mp3 at PATH (default: play then discard)
  --no-play        render only, do not afplay

Requires ELEVENLABS_API_KEY (run `make -C apps/voice env` first).
"""
from __future__ import annotations

import os
import subprocess
import sys
import tempfile
import tomllib
from pathlib import Path

import httpx
from dotenv import load_dotenv

APP_ROOT = Path(__file__).resolve().parents[1]  # apps/voice
sys.path.insert(0, str(APP_ROOT / "src"))
from klanker_voice.pronunciation import normalize_for_speech  # noqa: E402

API_BASE = "https://api.elevenlabs.io/v1"
MODEL_ID = "eleven_flash_v2_5"
OUTPUT_FORMAT = "mp3_44100_128"


def _tts_config(config_path: Path) -> tuple[str, dict]:
    tts = tomllib.loads(config_path.read_text()).get("tts", {})
    vid = str(tts.get("voice_id", "")).strip()
    if not vid:
        sys.exit(f"say: tts.voice_id is empty in {config_path.name}")
    settings = {
        "speed": float(tts.get("speed", 1.0)),
        "stability": float(tts.get("stability", 0.4)),
        "similarity_boost": float(tts.get("similarity_boost", 0.85)),
        "style": float(tts.get("style", 0.1)),
    }
    return vid, settings


def main() -> None:
    args = sys.argv[1:]
    raw = "--raw" in args
    no_play = "--no-play" in args
    config_path = APP_ROOT / "pipeline.toml"
    save_path: Path | None = None
    positional: list[str] = []
    i = 0
    while i < len(args):
        a = args[i]
        if a in ("--raw", "--no-play"):
            pass
        elif a == "--config":
            i += 1
            config_path = Path(args[i]) if i < len(args) else config_path
            if not config_path.is_absolute():
                config_path = APP_ROOT / config_path
        elif a == "--save":
            i += 1
            save_path = Path(args[i]) if i < len(args) else None
        else:
            positional.append(a)
        i += 1

    text = " ".join(positional).strip() if positional else sys.stdin.read().strip()
    if not text:
        sys.exit('say: no text (pass a string or pipe via stdin)')

    load_dotenv(APP_ROOT / ".env", override=True)
    key = os.environ.get("ELEVENLABS_API_KEY")
    if not key:
        sys.exit("say: ELEVENLABS_API_KEY not set (run `make -C apps/voice env`)")

    voice_id, voice_settings = _tts_config(config_path)
    spoken = text if raw else normalize_for_speech(text)

    print(f"config : {config_path.name}  voice {voice_id}  {voice_settings}")
    if spoken != text:
        print(f"spoken : {spoken}")
    print(f"rendering {len(spoken)} chars…")

    with httpx.Client(headers={"xi-api-key": key}, timeout=60.0) as client:
        resp = client.post(
            f"{API_BASE}/text-to-speech/{voice_id}",
            params={"output_format": OUTPUT_FORMAT},
            json={"text": spoken, "model_id": MODEL_ID, "voice_settings": voice_settings},
        )
        resp.raise_for_status()
        audio = resp.content

    out = save_path or Path(tempfile.gettempdir()) / "kph-say.mp3"
    out.write_bytes(audio)
    print(f"wrote  : {out} ({len(audio)} bytes)")

    if not no_play:
        player = "afplay" if sys.platform == "darwin" else "ffplay"
        try:
            subprocess.run([player, "-nodisp", "-autoexit", str(out)] if player == "ffplay"
                           else [player, str(out)], check=True)
        except FileNotFoundError:
            print(f"({player} not found — open {out} to listen)")


if __name__ == "__main__":
    main()
