"""audition.py — D-02 three-voice ElevenLabs audition renderer.

Queries the ElevenLabs voice library, shortlists exactly three premade
candidates against the D-03 brief (fast, punchy, high demo intelligibility,
conversational — never narration-styled), and renders the SAME K-register
script through each candidate at speed 1.1 / eleven_flash_v2_5.

Each candidate is rendered in its OWN HTTP call (RESEARCH Pitfall 8: voice
settings never change mid-session/stream — one voice per render session).

Output: apps/voice/artifacts/audition/{candidate}.mp3 (gitignored) plus a
manifest.json recording name, voice_id, and a one-line rationale per
candidate. The API key is read from apps/voice/.env and is never printed
or persisted anywhere (T-1-11).

Usage::

    cd apps/voice && uv run python scripts/audition.py
"""

from __future__ import annotations

import json
import os
import re
import sys
from datetime import datetime, timezone
from pathlib import Path

import httpx
from dotenv import load_dotenv

APP_ROOT = Path(__file__).resolve().parent.parent
OUT_DIR = APP_ROOT / "artifacts" / "audition"
API_BASE = "https://api.elevenlabs.io/v1"
MODEL_ID = "eleven_flash_v2_5"
SPEED = 1.1  # D-03: slightly above default; ElevenLabs WS/HTTP range 0.7-1.2
OUTPUT_FORMAT = "mp3_44100_128"

# The audition script — identical for every candidate, written in K's actual
# register (prompts/concierge.md persona v2): greeting that names K (D-01/D-04),
# a 1-2 sentence answer with a depth hook (D-05), and a playful-with-teeth
# line (D-07). Plain prose only: K speaks for the ear.
SCRIPT = (
    "Hey — I'm K, the KlankerMaker concierge. "
    "Ask me anything about Kurt, the klanker platform, or defcon dot run. "
    "Klanker is Kurt's agent sandbox: isolated boxes where AI agents do real "
    "work over email and Slack. Want the long version? "
    "And sure, I roast gently if invited — but fair warning: I've read Kurt's "
    "commit messages, so my bar for 'gently' is generous."
)

# D-03 brief scoring vocabulary. use_case and description come from the
# voice library's labels; we want conversational-punchy, not narration-calm.
USE_CASE_BONUS = {
    "conversational": 4,
    "social media": 2,
    "characters": 1,
    "characters_animation": 1,
}
USE_CASE_PENALTY = {
    "narration": -4,
    "narrative_story": -4,
    "informative_educational": -2,
    "news": -1,
    "meditation": -5,
    "asmr": -5,
}
DESC_BONUS = {
    "confident": 2,
    "energetic": 2,
    "upbeat": 2,
    "expressive": 2,
    "crisp": 2,
    "casual": 1,
    "friendly": 1,
    "intense": 1,
    "direct": 1,
    "warm": 1,
}
DESC_PENALTY = {
    "calm": -2,
    "soothing": -3,
    "soft": -2,
    "gentle": -2,
    "meditative": -4,
    "relaxed": -2,
    "sleepy": -4,
    "raspy": -1,  # texture over intelligibility — wrong trade for a demo floor
}


def api_key() -> str:
    load_dotenv(APP_ROOT / ".env", override=True)
    key = os.environ.get("ELEVENLABS_API_KEY", "")
    if not key:
        print(
            "ERROR: ELEVENLABS_API_KEY not set. Run `make -C apps/voice env` "
            "(or populate apps/voice/.env) and retry.",
            file=sys.stderr,
        )
        sys.exit(1)
    return key


def fetch_voices(client: httpx.Client) -> list[dict]:
    resp = client.get(f"{API_BASE}/voices")
    resp.raise_for_status()
    return resp.json().get("voices", [])


def score_voice(voice: dict) -> tuple[int, list[str]]:
    """Score one voice against the D-03 brief; return (score, reasons)."""
    labels = {k: (v or "").strip().lower() for k, v in (voice.get("labels") or {}).items()}
    use_case = labels.get("use_case", "")
    desc = labels.get("description", "") or labels.get("descriptive", "")
    accent = labels.get("accent", "")
    age = labels.get("age", "")

    score = 0
    reasons: list[str] = []

    for k, v in USE_CASE_BONUS.items():
        if k in use_case:
            score += v
            reasons.append(f"use_case '{use_case}' fits a live agent")
            break
    for k, v in USE_CASE_PENALTY.items():
        if k in use_case:
            score += v
            reasons.append(f"use_case '{use_case}' is narration/ambient-styled")
            break

    for k, v in DESC_BONUS.items():
        if k in desc:
            score += v
            reasons.append(f"'{desc}' delivery suits fast-punchy")
            break
    for k, v in DESC_PENALTY.items():
        if k in desc:
            score += v
            reasons.append(f"'{desc}' delivery is too laid-back for the brief")
            break

    # Demo intelligibility on a conference floor: standard broadly-parsed accents
    # rank slightly ahead; young/middle-aged reads punchier than 'old'.
    if accent in ("american", "british", "standard", "us", "uk"):
        score += 1
        reasons.append(f"{accent} accent — high floor-demo intelligibility")
    if age in ("young", "middle aged", "middle-aged", "middle_aged"):
        score += 1

    return score, reasons


def shortlist(voices: list[dict]) -> list[dict]:
    """Pick exactly three premade candidates, preferring gender variety."""
    premade = [v for v in voices if (v.get("category") or "").lower() == "premade"]
    pool = premade if len(premade) >= 3 else voices
    scored = []
    for v in pool:
        s, reasons = score_voice(v)
        scored.append((s, v.get("name", ""), v, reasons))
    scored.sort(key=lambda t: (-t[0], t[1]))

    picks: list[tuple[dict, list[str], int]] = []
    seen_genders: set[str] = set()
    # First pass: greedy by score with gender variety.
    for s, _name, v, reasons in scored:
        gender = ((v.get("labels") or {}).get("gender") or "").lower()
        if gender and gender in seen_genders and len(seen_genders) < 2:
            continue
        picks.append((v, reasons, s))
        seen_genders.add(gender)
        if len(picks) == 3:
            break
    # Second pass: top up regardless of gender if variety filtering ran short.
    if len(picks) < 3:
        chosen_ids = {p[0]["voice_id"] for p in picks}
        for s, _name, v, reasons in scored:
            if v["voice_id"] not in chosen_ids:
                picks.append((v, reasons, s))
            if len(picks) == 3:
                break

    if len(picks) < 3:
        print("ERROR: fewer than three voices available in the library.", file=sys.stderr)
        sys.exit(1)

    candidates = []
    for v, reasons, s in picks:
        labels = v.get("labels") or {}
        rationale = (
            f"{labels.get('gender', '?')}, {labels.get('accent', '?')}, "
            f"use_case={labels.get('use_case', '?')}: "
            + (reasons[0] if reasons else "top scorer against the fast-punchy conversational brief")
        )
        candidates.append(
            {
                "name": v.get("name", "unknown"),
                "voice_id": v["voice_id"],
                "labels": labels,
                "score": s,
                "rationale": rationale,
            }
        )
    return candidates


def slugify(name: str) -> str:
    return re.sub(r"[^a-z0-9]+", "-", name.lower()).strip("-") or "voice"


def render(client: httpx.Client, candidate: dict, out_path: Path) -> None:
    """Render the script for ONE candidate in its own HTTP call (Pitfall 8)."""
    resp = client.post(
        f"{API_BASE}/text-to-speech/{candidate['voice_id']}",
        params={"output_format": OUTPUT_FORMAT},
        json={
            "text": SCRIPT,
            "model_id": MODEL_ID,
            "voice_settings": {"speed": SPEED},
        },
    )
    resp.raise_for_status()
    out_path.write_bytes(resp.content)


def main() -> None:
    key = api_key()
    OUT_DIR.mkdir(parents=True, exist_ok=True)

    with httpx.Client(headers={"xi-api-key": key}, timeout=60.0) as client:
        voices = fetch_voices(client)
        candidates = shortlist(voices)

        for i, cand in enumerate(candidates, 1):
            fname = f"candidate-{i}-{slugify(cand['name'])}.mp3"
            out_path = OUT_DIR / fname
            print(f"rendering candidate {i}/3: {cand['name']} ({cand['voice_id']}) -> {fname}")
            render(client, cand, out_path)
            cand["file"] = str(out_path.relative_to(APP_ROOT))

    manifest = {
        "rendered_at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
        "model_id": MODEL_ID,
        "speed": SPEED,
        "output_format": OUTPUT_FORMAT,
        "script": SCRIPT,
        "candidates": candidates,
    }
    manifest_path = OUT_DIR / "manifest.json"
    manifest_path.write_text(json.dumps(manifest, indent=2) + "\n")

    print(f"\nwrote {manifest_path.relative_to(APP_ROOT)}")
    print("\nListen (same script, three voices):")
    for cand in candidates:
        print(f"  afplay {APP_ROOT / cand['file']}")


if __name__ == "__main__":
    main()
