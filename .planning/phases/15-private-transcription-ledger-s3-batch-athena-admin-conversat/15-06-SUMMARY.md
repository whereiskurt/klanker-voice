---
phase: 15-private-transcription-ledger-s3-batch-athena-admin-conversat
plan: 06
subsystem: ui
tags: [react, vitest, a11y-copy, privacy-notice]

# Dependency graph
requires: []
provides:
  - "A visible 'sessions may be recorded' small-print notice on the pre-connect ReadyToStart screen"
affects: [15-verification, privacy-posture]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Small-print notice sibling of .ready-cta-sub, same muted-color/small-size CSS treatment"

key-files:
  created: []
  modified:
    - apps/voice/client/src/screens/ReadyToStart.tsx
    - apps/voice/client/src/screens/readyToStart.css
    - apps/voice/client/src/screens/ReadyToStart.test.tsx

key-decisions:
  - "Notice copy: 'Sessions may be recorded for quality and demo purposes.' — short, one line, contains 'recorded' per acceptance criteria"
  - "Rendered as a sibling <p> of .ready-cta-sub inside .ready-cta-wrap, new .ready-recording-notice class mirrors .ready-cta-sub's muted small-print styling exactly (same --sz-label/--text-secondary tokens)"

patterns-established:
  - "Pre-connect privacy notices live in ReadyToStart's .ready-cta-wrap as small-print siblings of the CTA sub-line"

requirements-completed: [LEDG-04]

coverage:
  - id: D1
    description: "A visible 'sessions may be recorded' notice renders on ReadyToStart before the mic-start gesture"
    requirement: "LEDG-04"
    verification:
      - kind: unit
        ref: "apps/voice/client/src/screens/ReadyToStart.test.tsx#shows the recording notice alongside the existing CTA"
        status: pass
    human_judgment: false
  - id: D2
    description: "Existing CTA and screen are unregressed (full client suite green)"
    verification:
      - kind: unit
        ref: "apps/voice/client/src/screens/ReadyToStart.test.tsx#fires onStart when the CTA is tapped"
        status: pass
      - kind: unit
        ref: "npm test (full client suite, 32 files / 162 tests)"
        status: pass
    human_judgment: false

duration: 8min
completed: 2026-07-13
status: complete
---

# Phase 15 Plan 06: Pre-Connect Recording Notice Summary

**Small-print "sessions may be recorded" notice added to ReadyToStart, establishing the informed-continuation posture the LOCKED privacy ruling (LEDG-04) requires before the mic-start gesture.**

## Performance

- **Duration:** 8 min
- **Started:** 2026-07-13T04:54:00Z
- **Completed:** 2026-07-13T04:54:53Z
- **Tasks:** 1 (TDD: RED then GREEN)
- **Files modified:** 3

## Accomplishments
- `ReadyToStart.tsx` now renders a `.ready-recording-notice` small-print line ("Sessions may be recorded for quality and demo purposes.") as a sibling of the existing `.ready-cta-sub` line, inside `.ready-cta-wrap` — visible before the "Let's start talking" CTA tap that begins a recorded session.
- `readyToStart.css` gained a matching `.ready-recording-notice` rule mirroring `.ready-cta-sub`'s muted small-print styling (`--sz-label` / `--text-secondary`), clearing the same contrast floor already verified for that line.
- `ReadyToStart.test.tsx` extended with a new assertion proving the notice text and the existing CTA/sub-line both render — no regression to the pre-existing screen.

## Task Commits

Each task was committed atomically (TDD RED then GREEN):

1. **Task 1 — RED: failing test for the recording notice** - `9033a55` (test)
2. **Task 1 — GREEN: add the recording notice** - `b13bb72` (feat)

**Plan metadata:** (this commit)

## Files Created/Modified
- `apps/voice/client/src/screens/ReadyToStart.tsx` - new `.ready-recording-notice` `<p>` sibling of `.ready-cta-sub`
- `apps/voice/client/src/screens/readyToStart.css` - matching `.ready-recording-notice` small-print rule
- `apps/voice/client/src/screens/ReadyToStart.test.tsx` - new assertion for the notice + existing-CTA no-regression check

## Decisions Made
- Copy kept to one short line containing "recorded" per the plan's testable-acceptance-criteria requirement, phrased as "quality and demo purposes" to stay accurate without overclaiming legal terms.
- Styling mirrors `.ready-cta-sub` exactly (same tokens) rather than introducing new visual weight — the notice is informational small print, not a modal/blocking consent gate, matching the LOCKED ruling's "pair with a visible recording notice" (not a click-through gate) requirement.

## Deviations from Plan

None — plan executed exactly as written. `node_modules` was not yet installed in this worktree for `apps/voice/client`; ran `npm ci` under `nvm use 23` (node v23.6.0, satisfies the plan's own documented "node ≥22.12" gotcha) before running tests — this is environment setup, not a plan deviation.

## Issues Encountered
None.

## User Setup Required
None - no external service configuration required.

## Known Stubs
None.

## Threat Flags
None - no new network endpoints, auth paths, file access patterns, or trust-boundary schema changes; a text + CSS change only, consistent with the plan's own threat register (T-15-06-SC: no new package).

## Next Phase Readiness
- LEDG-04 (client recording notice) is now shipped and requirement-complete.
- T-15-06-02 (PSTN callers get no visual notice) remains an accepted, documented gap per the plan's own threat register — out of scope for this plan.
- Visual legibility on real mobile/desktop devices remains a phase-gate item (not a checkpoint in this plan), consistent with every other 15-0x plan's deferred live-verification pattern.

---
*Phase: 15-private-transcription-ledger-s3-batch-athena-admin-conversat*
*Completed: 2026-07-13*

## Self-Check: PASSED

- FOUND: apps/voice/client/src/screens/ReadyToStart.tsx
- FOUND: apps/voice/client/src/screens/readyToStart.css
- FOUND: apps/voice/client/src/screens/ReadyToStart.test.tsx
- FOUND: commit 9033a55 (test: RED)
- FOUND: commit b13bb72 (feat: GREEN)
