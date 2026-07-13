---
phase: quick-260713-k0k
plan: 01
subsystem: infra
tags: [go, cobra, voipms, telephony, cli]

requires:
  - phase: quick-260713-dfu
    provides: "kv telephony list operator command (DID mappings + gate config + SSM-backed secrets)"
provides:
  - "ListInboundDIDs — a VoIP.ms getDIDsInfo REST helper (defensive array/single-object shape parsing)"
  - "An Inbound DIDs section at the top of `kv telephony list`, showing the actual numbers the public dials"
affects: [telephony, voipms, kv-cli]

tech-stack:
  added: []
  patterns:
    - "readInboundDIDs mirrors readTelephonySecrets' total-degradation philosophy: no-creds/API-failure never returns a top-level error"
    - "shortVoipmsErrorNote derives a short safe status note, never interpolating the raw request URL or api_password"

key-files:
  created: []
  modified:
    - kv/internal/app/cmd/voipms.go
    - kv/internal/app/cmd/voipms_test.go
    - kv/internal/app/cmd/telephony.go
    - kv/internal/app/cmd/telephony_test.go

key-decisions:
  - "Inbound DIDs render by default (no new flag) — the primary question this command answers is 'what do people call?'; the graceful-degradation path already covers the no-network case cheaply"
  - "Two number tables relabeled for clarity: 'Inbound DIDs (numbers the public calls):' first, then 'Caller-ID mint mappings (auto-identity by caller ID):' — purely additive, no rewrite of the mint-mapping/secrets/gate-config logic"

patterns-established:
  - "getDIDsInfo response defensive parsing: VoIP.ms collapses a single-DID 'dids' array to a bare object — type-switch on []any vs map[string]any, default to a non-nil empty slice"

requirements-completed: [QK-260713-k0k]

coverage:
  - id: D1
    description: "ListInboundDIDs VoIP.ms getDIDsInfo helper parses both array and single-object dids response shapes, sends method=getDIDsInfo, degrades to an empty slice on odd/missing shapes"
    requirement: "QK-260713-k0k"
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestListInboundDIDs_ArrayShape"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestListInboundDIDs_SingleObjectShape"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestListInboundDIDs_RequestShape"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestListInboundDIDs_MissingDidsKeyIsEmptyNotPanic"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestListInboundDIDs_OddlyTypedDidsKeyIsEmptyNotPanic"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestListInboundDIDs_FailureStatusIsError"
        status: pass
    human_judgment: false
  - id: D2
    description: "kv telephony list renders an Inbound DIDs section first (above the caller-ID mint mappings), degrading gracefully to a safe note when creds are absent or the VoIP.ms API fails, and never leaking credentials or the raw request URL"
    requirement: "QK-260713-k0k"
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/telephony_test.go#TestReadInboundDIDs_CredsAbsentNotesEnvVars"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/telephony_test.go#TestReadInboundDIDs_ListerErrorYieldsSafeShortNote"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/telephony_test.go#TestReadInboundDIDs_ListerSuccessCarriesRecords"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/telephony_test.go#TestPrintTelephony_InboundDIDsRendersBeforeMintMappings"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/telephony_test.go#TestPrintTelephony_InboundDIDsStatusNoteWhenAbsent"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/telephony_test.go#TestPrintTelephony_JSONIncludesInboundDIDs"
        status: pass
    human_judgment: false

duration: 5min
completed: 2026-07-13
status: complete
---

# Quick Task 260713-k0k: kv telephony list Inbound DIDs section Summary

**`kv telephony list` now leads with a live-sourced "Inbound DIDs" section (VoIP.ms `getDIDsInfo`) showing the actual numbers the public dials, above the existing caller-ID mint mappings, with total graceful degradation on missing creds or API failure.**

## Performance

- **Duration:** 5min
- **Started:** 2026-07-13T18:29:00Z
- **Completed:** 2026-07-13T18:33:59Z
- **Tasks:** 2 completed (TDD, RED+GREEN each)
- **Files modified:** 4

## Accomplishments

- Added a typed, defensive `ListInboundDIDs` VoIP.ms REST helper (`voipmsMethodGetDIDsInfo`, centralized in the existing method-name const block) that parses both the array and the VoIP.ms single-DID-object quirk shapes of the `getDIDsInfo` response, reusing `voipmsClient.do()`/`voipmsCredsFromEnv`/`newVoipmsClient` unchanged.
- `kv telephony list` now renders an "Inbound DIDs (numbers the public calls):" section FIRST, above a relabeled "Caller-ID mint mappings (auto-identity by caller ID):" table — the two number tables (DIDs the public dials vs. caller-ID auto-identity mappings) are now clearly distinguished.
- Graceful degradation mirrors the existing `readTelephonySecrets` philosophy exactly: no VoIP.ms creds -> a clear "not configured" note; a VoIP.ms API failure -> a short, safe status note that never leaks `api_password` or the raw request URL. Neither case aborts the command — DynamoDB/SSM/gate-config sections always render.
- `--json` includes the inbound DID inventory (`inboundDids.records`) while the existing secret-value omission behavior is unchanged.

## Task Commits

Each task was committed atomically (TDD RED then GREEN):

1. **Task 1: Add the VoIP.ms getDIDsInfo helper** — `6ecfb6d` (test, RED) then `ed51aec` (feat, GREEN)
2. **Task 2: Render the Inbound DIDs section first in telephony list** — `b60b33d` (test, RED) then `9bfd96c` (feat, GREEN)

**Plan metadata:** committed separately by the orchestrator (docs commit not made by this executor per plan constraints)

## Files Created/Modified

- `kv/internal/app/cmd/voipms.go` - added `voipmsMethodGetDIDsInfo` const, `InboundDIDRecord` struct, `ListInboundDIDs`, `didRecordFromMap`
- `kv/internal/app/cmd/voipms_test.go` - 6 new tests for `ListInboundDIDs` (array shape, single-object shape, request shape, missing/oddly-typed `dids` key, failure status)
- `kv/internal/app/cmd/telephony.go` - added `InboundDIDReport`, `readInboundDIDs`, `shortVoipmsErrorNote`; `TelephonyListReport` gains `InboundDIDs` (first field); wired into `NewTelephonyCmd`'s list `RunE`; `printTelephony` renders the new section first and relabels the mint-mapping header
- `kv/internal/app/cmd/telephony_test.go` - 6 new tests for `readInboundDIDs` degradation (creds-absent, lister-error safe note, lister-success) and `printTelephony` (section ordering, status-note fallback, `--json` inclusion)

## Decisions Made

- No new CLI flag was added for the Inbound DIDs section — it renders by default, matching the plan's explicit "no offline escape hatch needed" call (the degradation path already covers the no-network case cheaply, and the primary question this command answers is "what do people call?").
- `shortVoipmsErrorNote` deliberately returns a fixed, generic message rather than any derived-from-error text, since `voipmsClient.do()`'s wrapped errors could in principle still carry method-name context; keeping it a static string is the simplest way to guarantee zero leakage risk (belt-and-suspenders on top of `do()`'s existing `*url.Error` unwrap).

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required. (Existing `VOIPMS_API_USERNAME`/`VOIPMS_API_PASSWORD` env vars, already documented in `docs/operators/voipms-provisioning-runbook.md`, are reused unchanged.)

## Next Phase Readiness

`kv telephony list` is now a complete single-pane operator view: what numbers the public dials (Inbound DIDs, live from VoIP.ms), who gets auto-identified by caller ID (mint mappings), the §24 gate secrets, and the gate config. No blockers. The VoIP.ms `getDIDsInfo` method name carries the same "verified against the live API method registry 2026-07-12" provenance as the other `voipmsMethod*` constants in this file (see the block's header comment) — no new unverified-method risk introduced.

---
*Phase: quick-260713-k0k*
*Completed: 2026-07-13*

## Self-Check: PASSED

All created/modified files and all 4 task commits (6ecfb6d, ed51aec, b60b33d, 9bfd96c) verified present on disk / in git log.
