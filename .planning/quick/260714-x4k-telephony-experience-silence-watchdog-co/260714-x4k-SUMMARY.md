---
phase: quick-260714-x4k
plan: 01
status: complete
date: 2026-07-14
commits:
  - fe55a70  # fix: silence watchdog no longer counts bot-talk + telephony timeout 50->120
  - 8b92612  # tune: dial back ack enthusiasm
---

# Quick 260714-x4k — Telephony experience: silence watchdog + ack enthusiasm

Live-feedback tuning after a real PSTN call (operator KPH): "the call hung up ON ME at ~58s…
if the bot is talking for many seconds I'm not talking and it shouldn't count against me" and
"the enthusiasm was a bit much on the ack masking."

## 1. Silence watchdog counted bot-speaking time as user idle (the "hung up on me" bug)
- **Root cause:** `TeardownObserver.on_push_frame` (session.py) reset the D-06 user-silence
  watchdog ONLY on `UserStartedSpeakingFrame`. So a long bot turn (the ~15s double greeting)
  burned the caller's 50s silence budget while they were just listening → hangup at ~58s. It
  was a DIRECT `release()` from the silence watchdog (confirmed live: `release: tearing down`
  with NO `wind-down firing` log, firing at exactly unlock+50s), not the 3-min cap.
- **Fix (fe55a70):** the observer now also resets the watchdog on **bot** speech
  (`BotStartedSpeakingFrame` / `BotStoppedSpeakingFrame`), so it measures TRUE mutual silence —
  the window restarts when the bot finishes, giving the caller the full timeout to reply.
  Shared code, so web benefits too. TDD:
  `test_teardown_observer_bot_speech_resets_the_silence_watchdog` (RED→GREEN).
- Also bumped telephony `user_silence_timeout` 50 → **120** so a caller pausing to think isn't
  dropped; the public 3-min session cap bounds it regardless.

## 2. Ack enthusiasm too much
- **Fix (8b92612):** naturalized `DEFAULT_ACK_TEMPLATES` (knowledge/router.py) — the topic-
  transition latency-mask beat. Dropped the gushy openers ("Ooh… good one", "Love that one",
  "Here's the deal") for calm, measured transitions that still end on the topic name so BM25
  retrieval stays masked. Router tests reference the constant (not literals), so unaffected.

## Verification
- `test_teardown.py` 12 passed (incl. new bot-speech reset test); teardown + router +
  telephony-config + telephony-lifecycle sweep **63 passed**.
- RED confirmed first (watchdog task unchanged on bot frame before the fix).

## Deploy
`apps/voice/**` → build-telephony-edge.yml → deploy.yml. Human deploy after merge.

## 3. Double greeting + passphrase leak on unlock (added to this task)
- **Root cause (confirmed live):** the passphrase keeps transcribing for a beat AFTER the gate
  opens (caller still speaking at unlock — spoke 03:28:01→04, unlock 03:28:02). The
  GateProcessor went full pass-through immediately, so that trailing transcription passed
  through as the first user turn. Prod LLM context proved it:
  `{'text': 'Start by concisely introducing yourself.'}, {'text': 'Hack the planet.'}` — the
  passphrase leaked into the LLM (and ledger) AND triggered a SECOND self-intro on top of
  greet_now's greeting.
- **Fix (this task):** after unlock, GateProcessor swallows speech frames (Transcription/
  Interim/UserStopped) until a genuinely NEW user turn (next UserStartedSpeakingFrame);
  non-speech frames (the greeting TTS, audio, control) still flow. Bonus: keeps the passphrase
  out of the LLM/ledger — better D-05e hygiene. TDD:
  `test_post_unlock_swallows_the_unlocking_utterance_tail_until_new_turn` (RED→GREEN verified
  via temporary neuter). Telephony sweep 64 passed.

## Still open (flagged, NOT in this task)
- **3-min cap still unconfirmed live** — the last call ended on silence before reaching 180s;
  needs a talk-through call held past 3:00 (the PR #49 fix + instrumentation are deployed).
