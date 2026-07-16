---
status: complete
task: 260716-2px
title: CTF OTP panic-readout gag ("Did you get that? ... No?")
one_liner: Panic-readout gag appended to the CTF phone-OTP announcement — slow x2 read, then "Did you get that? ... No?", 3 accelerating digit re-reads, and an abrupt "BYYYYYEEEE!", all as one speak_goodbye utterance with teardown grace widened to cover it.
tags: [telephony, ctf-otp, quick-task]
dependency-graph:
  requires: []
  provides:
    - _build_announcement_script (controller.py)
    - ANNOUNCEMENT_ACCEL_PAUSES / ANNOUNCEMENT_DIDYOUGET_COPY / ANNOUNCEMENT_NO_COPY / ANNOUNCEMENT_BYE_COPY / ANNOUNCEMENT_GAG_TAIL_SECONDS
  affects:
    - apps/voice/src/klanker_voice/telephony/controller.py (_gate_announcement)
tech-stack:
  added: []
  patterns:
    - "Pure module-level string builders (_pace_digits_slow, _build_accel_tail, _build_announcement_script) kept unit-testable without a call, matching the existing 260715-oq0/260716-1g0 pattern."
key-files:
  created: []
  modified:
    - apps/voice/src/klanker_voice/telephony/controller.py
    - apps/voice/configs/telephony.toml
    - apps/voice/tests/test_telephony_controller.py
decisions:
  - "Renamed _build_announcement_line -> _build_announcement_script (not additive) since the plan's single-utterance invariant means the old name no longer describes the full string it returns."
  - "_build_accel_tail returns the tail WITHOUT a leading space; _build_announcement_script supplies the one joining space per the plan's exact assembly formula (template.replace(...) + \" \" + _build_accel_tail(code)), avoiding a double space."
  - "Also monkeypatched the new ANNOUNCEMENT_GAG_TAIL_SECONDS constant to 0.05s in test_announcement_success_speaks_digitspaced_line_then_closes (alongside the pre-existing ANNOUNCEMENT_PLAYBACK_GRACE_SECONDS patch) to keep the real asyncio.sleep bounded — not specified verbatim by the plan's action text but consistent with that test's existing intent (\"Keep the test fast\") and does not touch any assertion."
metrics:
  duration: "~35 minutes"
  completed: 2026-07-16
---

# Quick Task 260716-2px: CTF OTP Panic-Readout Gag Summary

## What changed

`apps/voice/src/klanker_voice/telephony/controller.py`:

- **Renamed** `_build_announcement_line(template, code)` -> `_build_announcement_script(template, code)`, factored into three pure, module-level helpers:
  - `_pace_digits_slow(code)` — the EXISTING slow digit-spaced join (`"1. <break time=\"1.0s\" /> 2. ..."`), unchanged behavior, just extracted.
  - `_build_accel_tail(code)` — new gag tail: `"Did you get that? <break time=\"0.5s\" /> ... No? "` + 3 accelerating digit passes (break times `0.3s` -> `0.15s` -> `0.0s`/plain-space, digits always space-or-break separated, never concatenated) + `" BYYYYYEEEE!"` with **no** break tag immediately before the bye copy (abrupt cut).
  - `_build_announcement_script` = `template.replace("{code}", _pace_digits_slow(code)) + " " + _build_accel_tail(code)`.
- Added 5 new module constants near `ANNOUNCEMENT_DIGIT_PAUSE_SECONDS`: `ANNOUNCEMENT_ACCEL_PAUSES = (0.3, 0.15, 0.0)`, `ANNOUNCEMENT_DIDYOUGET_COPY`, `ANNOUNCEMENT_NO_COPY`, `ANNOUNCEMENT_BYE_COPY`, `ANNOUNCEMENT_GAG_TAIL_SECONDS = 8.0`.
- `_gate_announcement`: swapped the call from `_build_announcement_line` to `_build_announcement_script`; widened the teardown grace formula to `ANNOUNCEMENT_PLAYBACK_GRACE_SECONDS + 2*len(code)*ANNOUNCEMENT_DIGIT_PAUSE_SECONDS + ANNOUNCEMENT_GAG_TAIL_SECONDS`. Everything else in `_gate_announcement` (the §24 `cancel_for_takeover` call, `_fetch_ctf_otp`, the `speak_goodbye` seam, the single `_close_active_call` teardown, logging discipline) is byte-identical.

`apps/voice/configs/telephony.toml`:

- `line_template` changed from `"Hey! Let me get you that O T P. {code}. That's {code}. Buh bye."` to `"Hey! Let me get you that O T P. {code}. That's {code}."` — the trailing "Buh bye." is dropped since the gag tail now supplies the goodbye (`BYYYYYEEEE!`).

`apps/voice/tests/test_telephony_controller.py`:

- Import fixed: `_build_announcement_line` -> `_build_announcement_script`.
- Builder test renamed `test_build_announcement_line_paces_digits_and_substitutes_both_occurrences` -> `test_build_announcement_script_slow_read_twice_then_panic_gag`, asserting: the slow paced read appears twice, the gag-tail copy constants are present, both accelerating break times (`0.3s`, `0.15s`) appear, the fastest pass is single-space digit-separated (`"1 2 3 4 5 6"`), `"123456"` never appears as a bare substring anywhere in the line, and no `<break .../>` immediately precedes `ANNOUNCEMENT_BYE_COPY`.
- `test_announcement_success_speaks_digitspaced_line_then_closes` now derives its expectation from `_build_announcement_script(entry.line_template, "123456")` (stores `entry = _announcement_entry()` as a local instead of the previous hardcoded literal) rather than hand-writing the built string.

## Test results

All three required suites green:

```
apps/voice/tests/test_telephony_controller.py + test_telephony_lifecycle.py: 31 passed, 2 warnings in 18.99s
apps/voice/tests/test_telephony_config.py: 28 passed, 1 warning in 0.94s
All three together: 59 passed, 2 warnings in 18.70s
```

## Deviations from Plan

None — plan's Task 1 `<action>` followed exactly (helper names, constant names/values, grace formula, toml template change, test rename/import fix).

One minor addition beyond the plan's literal action text: also monkeypatched the new `ANNOUNCEMENT_GAG_TAIL_SECONDS` constant (to `0.05`) in `test_announcement_success_speaks_digitspaced_line_then_closes`, alongside the pre-existing `ANNOUNCEMENT_PLAYBACK_GRACE_SECONDS` patch, so the real `asyncio.sleep` in that test stays bounded. This does not touch any assertion the plan asked to keep unchanged — it only keeps the test's already-stated "keep the test fast" intent true after the new constant was added to the grace formula. (Note: `ANNOUNCEMENT_DIGIT_PAUSE_SECONDS` itself was already unpatched in this test before this task, so it still sleeps ~12s from the digit-pause term — pre-existing behavior, untouched.)

## Auth gates

None encountered.

## Known Stubs

None.

## Threat Flags

None — no new network endpoints, auth paths, file access, or schema changes. Same `speak_goodbye`/TTS seam, same `_fetch_ctf_otp`/§24 gate boundary, same single teardown path. Logging discipline unchanged (never logs the DTMF trigger code, the OTP code, `otp_url`, or the bearer).

## Deferred follow-up

Deploy is explicitly out of scope for this quick task per the plan's success criteria — the orchestrator merges this change and redeploys `telephony-edge`.

## Self-Check: PASSED

- FOUND: apps/voice/src/klanker_voice/telephony/controller.py (modified, contains `_build_announcement_script`, `_build_accel_tail`, `_pace_digits_slow`, new constants)
- FOUND: apps/voice/configs/telephony.toml (modified, `line_template` no longer has "Buh bye.")
- FOUND: apps/voice/tests/test_telephony_controller.py (modified, renamed test present, import fixed)
- FOUND: commit 2eb9095 (`git log --oneline -1` confirms)
