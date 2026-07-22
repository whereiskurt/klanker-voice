---
phase: quick-260721-te5
plan: 01
subsystem: cli
tags: [voipms, kv, cobra, go, provisioning, did-lifecycle]

# Dependency graph
requires:
  - phase: quick-260721-t7z
    provides: the live-proven Vegas 725-404-8283 DID order (routing/pop/dialtime/cnam/billing_type values reused as order-did's defaults)
provides:
  - kv voipms search-dids/order-did/cancel-did subcommands closing the DID-lifecycle gap in the kv CLI
affects: [voipms-provisioning-runbook, telephony-did-lifecycle]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "New VoIP.ms REST methods added as centralized voipmsMethod* constants with a live-verified comment, mirroring the existing set"
    - "DID search/order/cancel helpers reuse vc.do() so the *url.Error credential-redaction unwrap and voipmsStatusError safe-error shape cover them automatically"
    - "Destructive cobra subcommands (cancel-did) gate behind an explicit --yes flag checked before any credential resolution or network call"

key-files:
  created: []
  modified:
    - kv/internal/app/cmd/voipms.go
    - kv/internal/app/cmd/voipms_test.go
    - docs/operators/voipms-provisioning-runbook.md

key-decisions:
  - "orderVoipmsDID takes the DID as a separate positional arg and an orderDIDOptions struct for routing/pop/dialtime/cnam/billing_type, matching how the cobra layer collects flags"
  - "search-dids with no --ratecenter lists rate centers (getRateCentersUSA) instead of erroring, so an operator can discover a valid --ratecenter value from the same subcommand"
  - "cancel-did checks --yes before resolving credentials or touching the network, so a missing --yes never triggers even a credential-resolution side effect"

patterns-established:
  - "New VoIP.ms REST-method additions: constant + comment in the centralized block, package-level helper reusing vc.do()/voipmsStringField, table-driven httptest-server test asserting method+params, cobra subcommand registered in NewVoipmsCmd"

requirements-completed: [TE5-01]

coverage:
  - id: D1
    description: "kv voipms search-dids --state NV --ratecenter \"LAS VEGAS\" prints available DIDs with pricing"
    requirement: TE5-01
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestSearchVoipmsDIDsUSA_ArrayShape"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestSearchVoipmsDIDsUSA_SingleObjectShape"
        status: pass
    human_judgment: false
  - id: D2
    description: "kv voipms search-dids --state NV (no ratecenter) lists that state's rate centers"
    requirement: TE5-01
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestGetVoipmsRateCentersUSA_BuildsRequest"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestSearchVoipmsDIDsUSA_NoRatecenterOmitsParam"
        status: pass
    human_judgment: false
  - id: D3
    description: "kv voipms order-did <did> orders the DID with today's live-proven defaults and prints success"
    requirement: TE5-01
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestOrderVoipmsDID_BuildsRequest/success"
        status: pass
    human_judgment: false
  - id: D4
    description: "kv voipms cancel-did <did> refuses without --yes and releases the DID only when --yes is given"
    requirement: TE5-01
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestVoipmsCancelDidRefusesWithoutYes"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestCancelVoipmsDID_BuildsRequest"
        status: pass
    human_judgment: false
  - id: D5
    description: "A non-success VoIP.ms status (e.g. did_limit_reached) surfaces as a clear error with no credential leak"
    requirement: TE5-01
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestOrderVoipmsDID_BuildsRequest/did_limit_reached"
        status: pass
    human_judgment: false
  - id: D6
    description: "No live VoIP.ms API calls made during execution; kv build/vet/test all pass"
    verification:
      - kind: unit
        ref: "make -C kv vet"
        status: pass
      - kind: unit
        ref: "make -C kv test"
        status: pass
      - kind: unit
        ref: "make -C kv build"
        status: pass
    human_judgment: false

# Metrics
duration: 20min
completed: 2026-07-21
status: complete
---

# Quick Task 260721-te5: kv voipms search-dids/order-did/cancel-did Summary

**Adds `kv voipms search-dids`, `order-did`, and `cancel-did` — the three VoIP.ms REST methods (getDIDsUSA/getRateCentersUSA, orderDID, cancelDID) that were still curl-only — closing the last DID-lifecycle gap in the `kv` CLI, with `cancel-did` hard-gated behind `--yes`.**

## Performance

- **Duration:** ~20 min
- **Completed:** 2026-07-21
- **Tasks:** 3/3
- **Files modified:** 3 (voipms.go, voipms_test.go, voipms-provisioning-runbook.md)

## Accomplishments
- `search-dids` wraps `getDIDsUSA`/`getRateCentersUSA`: with `--ratecenter` it lists available DIDs+pricing; without it, lists the state's rate centers so an operator can pick one
- `order-did <did>` wraps `orderDID` with the exact live-proven defaults from today's Vegas DID order (`routing=account:557010_klanker-pbx pop=45 dialtime=60 cnam=0 billing_type=1`)
- `cancel-did <did>` wraps `cancelDID`, refusing to run without `--yes` (checked before credential resolution or any network call) and printing a clear irreversibility warning in its help text
- All new HTTP goes through the existing `vc.do()` seam, so the `*url.Error` credential-redaction unwrap and `*voipmsStatusError` safe-error shape apply automatically — no new leak surface
- Runbook's "Order ONE DID" section now documents the CLI alternative to the portal search/order steps

## Task Commits

Each task was committed atomically:

1. **Task 1: Add search/order/cancel client helpers + method constants** - `fc90763` (feat)
2. **Task 2: Table-driven tests for the new helpers and subcommands** - `c42e0a5` (test)
3. **Task 3: Minimal runbook note for the new CLI path** - `5c9e3a8` (docs)

_Metadata commit (SUMMARY.md/STATE.md) is created by the orchestrator, not this executor, per this task's constraints._

## Files Created/Modified
- `kv/internal/app/cmd/voipms.go` - Added `voipmsMethodGetDIDsUSA`/`GetRateCentersUSA`/`OrderDID`/`CancelDID` constants, `AvailableDIDRecord`/`RateCenterRecord` structs, `searchVoipmsDIDsUSA`/`getVoipmsRateCentersUSA`/`orderVoipmsDID`/`cancelVoipmsDID` helpers, and the `search-dids`/`order-did`/`cancel-did` cobra subcommands
- `kv/internal/app/cmd/voipms_test.go` - httptest-server table-driven tests for the new helpers (array/single-object shapes, param serialization, blank-input guards, failure-status + no-credential-leak assertion) and subcommand registration/`--yes` guard tests
- `docs/operators/voipms-provisioning-runbook.md` - 8-line note in "Order ONE DID" pointing at the new CLI path

## Decisions Made
- `orderVoipmsDID(ctx, vc, did, opts)` takes `did` as its own argument rather than folding it into the options struct, since the CLI arg (`args[0]`) and the flag-backed options (routing/pop/dialtime/cnam/billing-type) come from different cobra sources
- `search-dids` never errors on a missing `--ratecenter` — it degrades to listing rate centers, matching the plan's `must_haves` truth that `--state` alone should be useful
- `cancel-did`'s `--yes` check runs before `cfg.resolveVoipmsCreds`, so a missing `--yes` produces a fast, side-effect-free refusal (no SSM/env credential resolution attempted)

## Deviations from Plan

None - plan executed exactly as written. All three `must_haves.truths` are covered by unit tests against the `httptest`-server fake seam; no live VoIP.ms API calls were made at any point.

## Issues Encountered
None.

## User Setup Required
None - no external service configuration required. The three new subcommands reuse the existing `VOIPMS_API_USERNAME`/`VOIPMS_API_PASSWORD` env-first/SSM-fallback credential resolution already wired for every other `kv voipms` subcommand.

## Next Phase Readiness
- `kv voipms search-dids`/`order-did`/`cancel-did` are ready for operator use against the live VoIP.ms account (not exercised live in this session, per the task's "no live VoIP.ms API calls" constraint)
- No blockers. The DID-lifecycle gap referenced in the plan's objective (raw curl for the Vegas 725-404-8283 DID order) is now closed for future DID provisioning/decommissioning

---
*Quick task: 260721-te5*
*Completed: 2026-07-21*

## Self-Check: PASSED

- FOUND: kv/internal/app/cmd/voipms.go
- FOUND: kv/internal/app/cmd/voipms_test.go
- FOUND: docs/operators/voipms-provisioning-runbook.md
- FOUND: .planning/quick/260721-te5-add-kv-voipms-search-dids-order-did-canc/260721-te5-SUMMARY.md
- FOUND commit: fc90763 (feat: client helpers + method constants)
- FOUND commit: c42e0a5 (test: table-driven tests)
- FOUND commit: 5c9e3a8 (docs: runbook note)
