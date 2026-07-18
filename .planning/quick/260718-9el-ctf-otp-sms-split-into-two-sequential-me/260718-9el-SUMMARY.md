---
phase: 260718-9el
plan: 01
subsystem: telephony
tags: [sms, ctf-otp, voip.ms, asyncio, controller.py]

requires:
  - phase: quick/260716-hg5
    provides: "_send_sms_via_relay + the sms_eligible gating branch in _gate_announcement"
provides:
  - "ANNOUNCEMENT_SMS_SECOND_BODY static placeholder constant"
  - "_send_sms_sequence never-raise ordered-sequence helper"
  - "sms_eligible branch now sends TWO ordered SMS on one fire-and-forget task"
affects: [telephony-ctf-otp, sms-relay]

tech-stack:
  added: []
  patterns:
    - "Sequential fire-and-forget SMS sends via a single asyncio.create_task, looping never-raise per-message"

key-files:
  created: []
  modified:
    - apps/voice/src/klanker_voice/telephony/controller.py
    - apps/voice/tests/test_telephony_sms.py

key-decisions:
  - "ANNOUNCEMENT_SMS_BODY_TEMPLATE shortened to 'Here: https://q.defcon.run/c?v={code}' (from 'CTF flag redemption: ...') to read cleanly as message 1 of 2"
  - "_send_sms_sequence wraps each per-body _send_sms_via_relay call in its own try/except so a defensive guard exists even though the callee already never raises"
  - "ANNOUNCEMENT_SMS_SECOND_BODY documented explicitly as a placeholder for a future chained CTF clue"

requirements-completed: [CTF-SMS-SPLIT]

coverage:
  - id: D1
    description: "sms-eligible CTF announcement sends the OTP URL as SMS #1, then the static 'Hack the planet!' as SMS #2, in order, on one fire-and-forget task"
    requirement: CTF-SMS-SPLIT
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_sms.py#test_hook_eligible_posts_two_relay_calls_and_speaks_punchline"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_sms.py#test_hook_per_did_enrolled_texts_from_dialed_did"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_sms.py#test_hook_per_did_unresolved_falls_back_to_pool"
        status: pass
    human_judgment: false
  - id: D2
    description: "_send_sms_sequence never short-circuits on a failed/raising first send and never raises itself"
    requirement: CTF-SMS-SPLIT
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_sms.py#test_send_sms_sequence_first_failure_does_not_block_second"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_sms.py#test_send_sms_sequence_first_false_return_does_not_block_second"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_sms.py#test_hook_failing_relay_never_breaks_teardown"
        status: pass
    human_judgment: false
  - id: D3
    description: "Both SMS bodies are pure 7-bit GSM-7 ASCII"
    requirement: CTF-SMS-SPLIT
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_sms.py#test_sms_body_is_gsm7_ascii_safe"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_sms.py#test_sms_second_body_is_gsm7_ascii_safe"
        status: pass
    human_judgment: false
  - id: D4
    description: "Live delivery of two real SMS to a real phone over VoIP.ms (both messages actually arrive as separate texts, in order)"
    verification: []
    human_judgment: true
    rationale: "No live-network/billed SMS send was exercised in this session (offline unit tests only, per plan scope); a real phone-call/SMS check is a human-judgment live-verification item, same pattern as every prior CTF-SMS quick task in this codebase."

duration: 20min
completed: 2026-07-18
status: complete
---

# Quick Task 260718-9el: CTF OTP SMS Split Into Two Sequential Messages Summary

**Split the single CTF OTP SMS into two ordered fire-and-forget sends (URL first, static "Hack the planet!" second) riding one asyncio task, with a never-raise sequence helper that never lets a failed first send block the second.**

## Performance

- **Duration:** ~20 min
- **Completed:** 2026-07-18
- **Tasks:** 2/2 completed
- **Files modified:** 2

## Accomplishments
- `ANNOUNCEMENT_SMS_BODY_TEMPLATE` now formats to `"Here: https://q.defcon.run/c?v={code}"` (message 1)
- New `ANNOUNCEMENT_SMS_SECOND_BODY = "Hack the planet!"` constant (message 2), documented as a future chained-clue placeholder
- New `_send_sms_sequence(url, headers, dst, bodies, dids)` helper: awaits `_send_sms_via_relay` once per body in order, never short-circuits on a `False`/raising first send, never raises itself
- `_gate_announcement`'s `sms_eligible` branch now parks exactly one `_send_sms_sequence` task on `active_call.sms_task` (URL body then static body) instead of a single `_send_sms_via_relay` call — gating logic (`send_dids and relay_url and dst`), the spoken punchline, and the never-log discipline are all unchanged
- Test suite updated: new GSM-7 regression for the second body, three new `_send_sms_sequence` unit tests (order, first-exception-doesn't-block-second, first-False-doesn't-block-second), and the three capture-list hook tests (eligible, per-DID enrolled, per-DID unresolved-fallback) updated to assert two ordered sends sharing dst/dids/url

## Task Commits

1. **Task 1: Add second-body constant + _send_sms_sequence helper, wire the sms_eligible branch** - `fd4ee1d` (feat)
2. **Task 2: Update SMS tests to expect two ordered sends and cover _send_sms_sequence** - `69466cd` (test)

**Plan metadata:** (docs commit handled by orchestrator, not included here)

## Files Created/Modified
- `apps/voice/src/klanker_voice/telephony/controller.py` - shortened URL body template, new second-body constant, new `_send_sms_sequence` helper, updated `sms_eligible` branch + explanatory comment
- `apps/voice/tests/test_telephony_sms.py` - new/updated imports, new GSM-7 regression for second body, 3 new `_send_sms_sequence` unit tests, 3 updated two-send hook-integration assertions

## Decisions Made
- Wrapped each `_send_sms_via_relay` call inside `_send_sms_sequence`'s loop in its own `try/except Exception: pass`, even though `_send_sms_via_relay` already never raises — a defensive belt-and-suspenders guard per the plan's explicit instruction ("wrap defensively so even an unexpected raise cannot escape the parked task").
- Left the inline `from klanker_voice.telephony.controller import ANNOUNCEMENT_SMS_BODY_TEMPLATE` in the old GSM-7 test removed in favor of the top-level import now that it's imported at module scope (minor cleanup, not a deviation).

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

Code-complete and fully unit-tested offline (233/233 telephony tests green, including the full `test_telephony_*.py` regression). Not yet exercised: a real live phone call through the deployed CTF DID confirming two separate texts actually arrive on a real handset in the right order (same class of live-verification deferral as every prior CTF-SMS quick task in this codebase, e.g. `ctf-otp-sms-during-call-idea` memory note). No blockers for merging; live SMS behavior should be spot-checked on next live CTF dial-in.

## Self-Check: PASSED

- FOUND: apps/voice/src/klanker_voice/telephony/controller.py
- FOUND: apps/voice/tests/test_telephony_sms.py
- FOUND: commit fd4ee1d
- FOUND: commit 69466cd

---
*Phase: 260718-9el*
*Completed: 2026-07-18*
