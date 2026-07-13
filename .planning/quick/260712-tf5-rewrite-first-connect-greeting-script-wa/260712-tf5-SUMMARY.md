---
quick_id: 260712-tf5
status: complete
completed: 2026-07-13
commit: e63338b
---

# Summary: Rewrite first-connect greeting (warmer KPH intro)

**Shipped** the new first-connect greeting on branch `quick/260712-greeting-rewrite`
(commit `e63338b`), PR opened for Kurt to review/merge (merge triggers
build-voice.yml deploy; greetings sync with short TTL + `/greetings/*`
invalidation, so the new clip propagates on deploy).

## What changed

- `apps/voice/client/public/greetings/greeting-1.mp3` — the approved 7.2s clip:
  a hand-spliced composite of three ElevenLabs renders (K-take-3 front,
  "Ah — his Klanker!" aha tail, fresh full-energy "How can I help?" ending),
  all rendered at live pipeline.toml voice settings so it matches the live voice.
- `greetings.source.json` — collapsed 3 rotating scripts to the single canonical
  script (with `<break>` tags).
- `greetings.manifest.json` — single clip entry + `note` warning that the mp3 is
  a cherry-picked take (`make greetings` would overwrite it with a random new
  delivery; re-render only on voice/settings change, then re-audition).
- Deleted `greeting-2.mp3`, `greeting-3.mp3`.

## Verification

- `tests/test_greeting_voice_drift.py` — 2 passed (voiceId + text alignment).
- Full suite: 283 passed, 53 skipped.
- Clip approved by Kurt by ear after ~20-candidate audition loop.

## Notes / follow-ups

- Stale `greeting-2/3.mp3` may linger on S3 (sync has no `--delete`) —
  unreferenced, harmless.
- Audition tooling (candidate JSON + renderer reusing pipeline.toml settings)
  lives in the session scratchpad; worth promoting to
  `apps/voice/scripts/` if greeting iteration happens again.
