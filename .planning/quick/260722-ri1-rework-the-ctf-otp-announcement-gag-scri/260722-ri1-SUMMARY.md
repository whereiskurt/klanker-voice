---
phase: quick-260722-ri1
plan: 01
subsystem: telephony
tags: [ctf, otp, announcement-gag, elevenlabs, pipecat, telephony, controller.py]

requires:
  - phase: quick-260716-2px/260716-3xx
    provides: the prior markup-free panic-readout gag (comma+accelerating-space passes) this rework replaces
  - phase: quick-260716-hg5
    provides: the SMS-during-call relay flow (send_dids/sms_relay_url) whose eligibility flag this gag branches on
provides:
  - "_build_announcement_script v6 gag: comma-paced {code} opener, space-paced {code_fast} re-read, deterministic A/B/C/D digit-jumble derangement, six-group pre-reveal pause, honest SMS/non-SMS closings"
affects: [ctf-otp-announcement-did, telephony-edge]

tech-stack:
  added: []
  patterns:
    - "Deterministic per-call jumble via random.Random(code) (sha512-seeded, not Python hash-randomized) instead of module-global random state"

key-files:
  created: []
  modified:
    - apps/voice/src/klanker_voice/telephony/controller.py
    - apps/voice/configs/telephony.toml
    - apps/voice/tests/test_telephony_controller.py
    - apps/voice/tests/test_telephony_sms.py

key-decisions:
  - "sms_eligible defaults to False (legacy-compatible) — the plan's own smoke-check snippet omitted the third True argument on its first call while asserting the SMS-eligible punchline; this is a bug in the plan's verify script, not the implementation (see Deviations)."
  - "ANNOUNCEMENT_GAG_TAIL_SECONDS set to 16.0s (recomputed for ~26 total jumble digits + six-group pause + reveal; grace only bounds teardown, so generous is safe)."

patterns-established:
  - "Both sms-eligible and non-sms closings now get ANNOUNCEMENT_PUNCHLINE_PAUSE prepended (previously only the sms-eligible path paused)."

requirements-completed:
  - QUICK-260722-ri1-otp-gag-v6

coverage:
  - id: D1
    description: "Caller hears the v6 gag: new opener, comma-paced then space-paced code read, deterministic A/B/C/D wheels-come-off jumble, six-group pre-reveal pause, then the punchline."
    requirement: "QUICK-260722-ri1-otp-gag-v6"
    verification:
      - kind: unit
        ref: "tests/test_telephony_sms.py#test_v6_gag_is_deterministic_for_a_fixed_code"
        status: pass
      - kind: unit
        ref: "tests/test_telephony_controller.py#test_build_announcement_script_slow_read_twice_then_panic_gag"
        status: pass
    human_judgment: false
  - id: D2
    description: "Same OTP code always produces identical spoken script (deterministic jumble, no call-path randomness)."
    requirement: "QUICK-260722-ri1-otp-gag-v6"
    verification:
      - kind: unit
        ref: "tests/test_telephony_sms.py#test_v6_gag_is_deterministic_for_a_fixed_code"
        status: pass
    human_judgment: false
  - id: D3
    description: "Every jumble digit is drawn from the code's own digit multiset (never a foreign digit)."
    requirement: "QUICK-260722-ri1-otp-gag-v6"
    verification:
      - kind: unit
        ref: "tests/test_telephony_sms.py#test_v6_gag_every_digit_is_from_the_codes_own_multiset"
        status: pass
    human_judgment: false
  - id: D4
    description: "A caller who could NOT be texted never hears 'SMS on the way' (no false promise)."
    requirement: "QUICK-260722-ri1-otp-gag-v6"
    verification:
      - kind: unit
        ref: "tests/test_telephony_sms.py#test_v6_gag_non_sms_fallback_never_says_sms"
        status: pass
      - kind: unit
        ref: "tests/test_telephony_sms.py#test_script_ineligible_is_byte_identical_to_legacy"
        status: pass
    human_judgment: false
  - id: D5
    description: "No angle-bracket markup ever appears in the assembled script."
    requirement: "QUICK-260722-ri1-otp-gag-v6"
    verification:
      - kind: unit
        ref: "tests/test_telephony_sms.py#test_v6_gag_has_no_markup_regression_guard"
        status: pass
    human_judgment: false
  - id: D6
    description: "Full apps/voice pytest suite passes (telephony subsystem in scope)."
    verification:
      - kind: unit
        ref: "uv run pytest -q (apps/voice)"
        status: pass
    human_judgment: false

duration: 25min
completed: 2026-07-22
status: complete
---

# Quick Task 260722-ri1: Rework CTF OTP Announcement Gag Script Summary

**Reworked the CTF OTP announcement gag from a 6-fast-pass "accelerating rattle" to the operator-approved v6 audition take: comma-paced then space-paced code read, a deterministic seeded digit-derangement jumble, a long six-group pre-reveal pause, and an honest SMS-vs-fallback punchline.**

## Performance

- **Duration:** ~25 min
- **Tasks:** 2/2 completed
- **Files modified:** 4

## Accomplishments
- `controller.py`: new `ANNOUNCEMENT_FAST_DIGIT_SEP` + `_pace_digits_fast` for the `{code_fast}` re-read; `_pace_digits_slow` re-paced to comma separation for `{code}`.
- `_build_accel_tail` rewritten to the v6 shape: Segment A (first three digits, comma-paced), Segment B (shuffled last three, space-paced, guaranteed-differ with rotate-by-one fix-up, then "... wait. "), Segment C (shuffled all six, space-paced, guaranteed-differ), Segment D (14-digit space-paced runaway drawn only from the code's own digits) — all seeded off `random.Random(code)` for per-code determinism.
- `_build_announcement_script` now substitutes both `{code}` and `{code_fast}`, builds a pause-prefixed closing for BOTH branches, and never lets the non-SMS fallback say "SMS".
- `ANNOUNCEMENT_SMS_PUNCHLINE_COPY` reworded to "Just kidding. SMS on the way. Hack the planet."; `ANNOUNCEMENT_PUNCHLINE_PAUSE` extended to six ellipsis groups.
- Removed the dead `ANNOUNCEMENT_ACCEL_SEPS` / `ANNOUNCEMENT_NO_COPY` constants.
- `telephony.toml` `line_template` updated to the v6 opener with `{code}`/`{code_fast}` placeholders.
- Test suites updated/extended: rewrote the old panic-gag structural test, renamed the SMS-punchline test to match "SMS on the way", fixed the pre-reveal-pause assertion to cover both closings, and added determinism / digit-multiset / segment-differs / no-markup regression tests.

## Task Commits

1. **Task 1: Rework the announcement constants and assembly functions (controller.py + telephony.toml)** - `599a88b` (feat)
2. **Task 2: Update and extend the announcement tests, full suite green** - `ddb91f9` (test)

_Note: docs/state commit (SUMMARY.md/STATE.md/ROADMAP.md) is made separately by the orchestrator, not by this executor._

## Files Created/Modified
- `apps/voice/src/klanker_voice/telephony/controller.py` - v6 gag constants + `_pace_digits_slow`/`_pace_digits_fast`/`_build_accel_tail`/`_build_announcement_script`
- `apps/voice/configs/telephony.toml` - `line_template` updated to the v6 opener
- `apps/voice/tests/test_telephony_controller.py` - rewrote the structural gag test for the v6 shape
- `apps/voice/tests/test_telephony_sms.py` - renamed/updated SMS-branch tests; added 5 new v6-structure tests

## Decisions Made
- Kept `sms_eligible: bool = False` as the function default (matches the plan's own `<action>` spec and the pre-existing `test_script_ineligible_is_byte_identical_to_legacy` test, which calls the function with no third argument and asserts the legacy bye copy). See Deviations for the smoke-check discrepancy this surfaced.
- `ANNOUNCEMENT_GAG_TAIL_SECONDS` recomputed to 16.0s (was 18.0s for the old 6-pass gag) — the v6 tail is ~26 total jumble digits plus the six-group pause plus the reveal; grace only bounds teardown (never audible), so a generous value was chosen over a tightly-shaved one.
- `ANNOUNCEMENT_SLOW_DIGIT_SECONDS` reduced 0.9 → 0.4 since the comma pace reads far faster than the old "write it down" pace it replaces.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug in plan's own verify snippet] Task 1's literal smoke-check script contradicts the plan's own function-default spec**
- **Found during:** Task 1 verification
- **Issue:** The plan's `<verify><automated>` smoke-check calls `s=b(t,'830429')` (no third argument, so `sms_eligible` defaults to `False` per the plan's own `<action>` text: `_build_announcement_script(template, code, sms_eligible=False)`), then asserts `s.rstrip().endswith('SMS on the way. Hack the planet.')` — but the default-`False` closing is `"Just kidding. " + ANNOUNCEMENT_BYE_COPY"`, which never contains "SMS". Running the snippet verbatim fails with `AssertionError: punchline wrong`. Changing the default to `True` would break `test_script_ineligible_is_byte_identical_to_legacy` (explicitly required to stay unchanged by the plan) and would violate truth "A caller who could NOT be texted never hears 'SMS on the way'" for any caller path that doesn't explicitly opt in.
- **Fix:** Implemented `_build_announcement_script` exactly per the plan's `<action>`/`<behavior>` spec (default `sms_eligible=False`) and Task 2's test suite (which is the authoritative, executable verification for this plan). Re-ran the plan's smoke-check with the first call's missing `True` argument supplied (`b(t,'830429', True)`) to confirm the SMS-eligible branch matches the reference utterance structure exactly (opener, `8, 3, 0, ` Segment A, `wait.`, digit-multiset-only jumble, `SMS on the way. Hack the planet.` reveal) — it passes cleanly. The `False`-default fallback path was separately confirmed to never contain "SMS" (`nf` assertion in the same snippet, and `test_v6_gag_non_sms_fallback_never_says_sms`).
- **Files modified:** None beyond the planned Task 1 files — this was a verification-script-only issue, not an implementation issue.
- **Verification:** `uv run python -c "..."` (corrected snippet) prints `OK`; `test_script_ineligible_is_byte_identical_to_legacy` and all 5 new v6-structure tests pass.
- **Committed in:** 599a88b (Task 1 commit) — implementation was correct as written; no code change resulted from this deviation, only a corrected ad-hoc verification run.

---

**Total deviations:** 1 (documentation/verify-script discrepancy only, no implementation or test change needed)
**Impact on plan:** None on scope or correctness — the shipped code satisfies every `must_haves.truths` line item and the plan's own test-authoring instructions (Task 2) exactly as written.

## Issues Encountered
None beyond the smoke-check discrepancy documented above.

## User Setup Required
None - no external service configuration required. No live API calls were made (per task constraints); all verification is offline/pure-function.

## Next Phase Readiness
- The v6 gag is code-complete and fully covered by the offline test suite; no deploy performed by this executor (orchestrator handles remote steps).
- A live confirm call on the CTF announcement DID (already tracked as an open item in the per-DID gate policy memory) will be the first place to hear the v6 script live once deployed.
- SMS bodies, send flow, OTP fetch, and gate/DTMF mechanics were untouched, per the plan's explicit boundary — no regression risk introduced outside the announcement-script assembly region.

---
*Phase: quick-260722-ri1*
*Completed: 2026-07-22*

## Self-Check: PASSED

All created/modified files found on disk; commits 599a88b and ddb91f9 both present in `git log --all`.
