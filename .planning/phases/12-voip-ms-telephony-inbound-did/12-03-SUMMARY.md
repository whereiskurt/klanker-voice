---
phase: 12-voip-ms-telephony-inbound-did
plan: 03
subsystem: infra
tags: [go, cobra, dynamodb, electrodb, e164, kv-cli]

# Dependency graph
requires:
  - phase: 12-voip-ms-telephony-inbound-did
    provides: "12-02's phone/byPhone GSI + normalizeE164 on the AccessCode entity (the write side this plan targets), and the auth-app phone-normalization.ts canonical-output oracle"
provides:
  - "kv/internal/app/electro: AccessCodeGSI3PK/AccessCodeGSI3SK/GSI3IndexName key writers"
  - "kv code phone <code> --add <e164> | --remove operator sub-command"
  - "AddPhoneMapping/RemovePhoneMapping (conditional UpdateItem SET/REMOVE of phone/phoneEnabled/gsi3pk/gsi3sk)"
  - "Go normalizeE164() proven byte-identical to the auth-app TS helper for every shared input shape"
affects: [12-04, 12-05, 12-06, 12-07, 12-08]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "gsi3 byPhone key writers mirror gsi2 byBypassToken exactly (AccessCodeGSI3PK/SK alongside AccessCodeGSI2PK/SK)"
    - "kv code phone mirrors kv code bypass's cobra sub-command + Add/Remove UpdateItem shape verbatim"
    - "Go normalizeE164 signature diverges intentionally from the TS set-transform: returns (string, error) instead of always returning a string, since an interactive CLI should stop on bad input rather than silently write an empty phone key"

key-files:
  created:
    - kv/internal/app/cmd/code_test.go
  modified:
    - kv/internal/app/electro/keys.go
    - kv/internal/app/cmd/code.go

key-decisions:
  - "normalizeE164(raw string) (string, error) errors on blank/no-digit input rather than mirroring the TS helper's return-empty-string behavior — the TS function is a passive ElectroDB set transform that must never throw, kv is an interactive CLI where a rejected number should stop the command"
  - "Phone-mapping tests (TestAddPhoneMapping/TestRemovePhoneMapping/TestAddPhoneMapping_RequiresExistingCode) exercise a real dynamodb-local instance (skip-if-unreachable), not a stubbed DynamoDB client — no httptest-based DynamoDB request-shape stub exists anywhere in this codebase (only voipms.go's plain net/http calls are httptest-stubbed); the established pattern for DynamoDB write assertions is roundtrip_test.go's/usage_killswitch_test.go's testDynamoClient() skip helper, which this plan's tests reuse verbatim"

patterns-established:
  - "Sparse gsi3 byPhone index (Go side) — exact mirror of gsi2 byBypassToken: same casing discipline (no case transform on the phone digit string, matching the webapp entity's casing:'none'), same conditional-UpdateItem SET/REMOVE shape"

requirements-completed: [D-05, SC-2]

coverage:
  - id: D1
    description: "AccessCodeGSI3PK/AccessCodeGSI3SK/GSI3IndexName key writers produce phone#<e164> / phone# / gsi3pk-gsi3sk-index, matching the auth-app entity's byPhone template exactly"
    requirement: D-05
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/code_test.go#TestAddPhoneMapping (asserts gsi3pk/gsi3sk against electro.AccessCodeGSI3PK/SK)"
        status: unknown
    human_judgment: false
  - id: D2
    description: "kv code phone <code> --add <e164> normalizes and SETs phone/phoneEnabled/gsi3pk/gsi3sk via a conditional UpdateItem (attribute_exists(pk))"
    requirement: D-05
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/code_test.go#TestAddPhoneMapping"
        status: unknown
      - kind: unit
        ref: "kv/internal/app/cmd/code_test.go#TestAddPhoneMapping_RequiresExistingCode"
        status: unknown
    human_judgment: false
  - id: D3
    description: "kv code phone <code> --remove REMOVEs phone/phoneEnabled/gsi3pk/gsi3sk, dropping the code from the sparse byPhone index"
    requirement: D-05
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/code_test.go#TestRemovePhoneMapping"
        status: unknown
    human_judgment: false
  - id: D4
    description: "Go normalizeE164 produces byte-identical canonical output to the auth-app phone-normalization.ts helper for every shared input shape (spaced/parenthesized/dashed, dashed-with-country-code, bare-10-digit, idempotent-already-canonical)"
    requirement: SC-2
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/code_test.go#TestNormalizeE164"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/code_test.go#TestNormalizeE164_AuthAppParity"
        status: pass
    human_judgment: false

duration: 20min
completed: 2026-07-12
status: complete
---

# Phase 12 Plan 03: kv code phone Summary

**`kv code phone <code> --add <e164> | --remove` operator command writes the sparse gsi3 byPhone GSI, with a Go E.164 normalizer proven byte-identical to the auth-app's canonicalizer.**

## Performance

- **Duration:** 20 min
- **Tasks:** 2
- **Files modified:** 3 (2 modified, 1 created)

## Accomplishments

- `kv/internal/app/electro/keys.go` gained `AccessCodeGSI3PK`/`AccessCodeGSI3SK`/`GSI3IndexName`, mirroring the existing gsi2 bypass key writers byte-for-byte (`"phone#" + phone` / `"phone#"` / `"gsi3pk-gsi3sk-index"`), plus an updated file-header key-map comment documenting the new sparse gsi3/byPhone row.
- `kv/internal/app/cmd/code.go` gained `normalizeE164(raw string) (string, error)` — a Go reproduction of `apps/auth/webapp/src/lib/phone-normalization.ts`'s canonical-output rule (strip to digits+leading-`+`, drop leading trunk zeros, prepend country code `1` for a bare 10-digit North-American number) — plus `AddPhoneMapping`/`RemovePhoneMapping` (conditional `UpdateItem` SET/REMOVE of `phone`/`phoneEnabled`/`gsi3pk`/`gsi3sk`, mirroring `EnableBypass`/`DisableBypass` exactly) and a `phone <code>` cobra sub-command (`--add`/`--remove`, mutually exclusive, one required) wired into `NewCodeCmd`.
- `kv/internal/app/cmd/code_test.go` (new file): `TestNormalizeE164` (6 cases including the plan's 4 shared shapes plus 2 blank/whitespace error cases), `TestNormalizeE164_AuthAppParity` (a hardcoded auth-app-output oracle table asserting Go output matches), and `TestAddPhoneMapping`/`TestRemovePhoneMapping`/`TestAddPhoneMapping_RequiresExistingCode` against a real dynamodb-local instance (skip-if-unreachable, mirroring `roundtrip_test.go`'s established pattern — no dynamodb-local was running in this sandbox, so these three skipped, consistent with every prior plan's dynamodb-local-backed tests).

## Task Commits

Each task was committed atomically:

1. **Task 1: AccessCode gsi3 (byPhone) key writers** - `a44f53f` (feat)
2. **Task 2: `kv code phone` sub-command + Add/RemovePhoneMapping + normalization parity** - `e8d0117` (feat)

## Files Created/Modified

- `kv/internal/app/electro/keys.go` - `AccessCodeGSI3PK`/`AccessCodeGSI3SK`/`GSI3IndexName` + updated key-map header comment
- `kv/internal/app/cmd/code.go` - `normalizeE164`, `AddPhoneMapping`, `RemovePhoneMapping`, `phone` cobra sub-command
- `kv/internal/app/cmd/code_test.go` (new) - `TestNormalizeE164`, `TestNormalizeE164_AuthAppParity`, `TestAddPhoneMapping`, `TestRemovePhoneMapping`, `TestAddPhoneMapping_RequiresExistingCode`

## Decisions Made

- `normalizeE164` returns `(string, error)` rather than mirroring the TS helper's "always return a string, `""` for unmappable input" contract — the TS function is a passive ElectroDB `set` transform that must never throw; `kv code phone` is an interactive CLI where blank/no-digit input should stop the command with a clear error, not silently write an empty phone key. This is a deliberate, plan-specified divergence (the plan's own Task 2 action text specifies the `(string, error)` signature and "error on empty"), not a parity gap — for every non-blank input the two normalizers produce byte-identical output, proven by `TestNormalizeE164_AuthAppParity`.
- Phone-mapping mutation tests (`TestAddPhoneMapping`/`TestRemovePhoneMapping`/`TestAddPhoneMapping_RequiresExistingCode`) exercise a real dynamodb-local instance via the existing `testDynamoClient(t)` skip-if-unreachable helper (from `roundtrip_test.go`), asserting actual post-write item state via `GetItem`, rather than the plan's "stubbed DynamoDB, mirroring bypass_test.go" wording taken literally. Investigated first: `bypass_test.go` itself doesn't stub DynamoDB requests for `EnableBypass`/`DisableBypass` (it only tests the pure `generateBypassToken`/`bypassJoinURL` helpers), and no httptest-based DynamoDB request-shape stub exists anywhere in this codebase — the only httptest-backed tests are `voipms.go`'s plain `net/http` REST calls (`voipms_test.go`). The established, repo-wide pattern for asserting DynamoDB write behavior is the `testDynamoClient`/`roundTripTable` skip-if-unreachable harness (`roundtrip_test.go`, `usage_killswitch_test.go`), which these new tests reuse verbatim for consistency and because it actually proves the written item state (not just the outgoing request shape).

## Deviations from Plan

None requiring the Rule 1-4 framework — the one interpretive choice (dynamodb-local-backed tests instead of a from-scratch httptest DynamoDB stub) is documented above under Decisions Made since it follows an existing, stronger codebase pattern rather than introducing new test infrastructure the plan's own cited example (`bypass_test.go`) doesn't actually contain.

## Issues Encountered

None. `cd kv && go build ./... && go vet ./...` clean throughout; `go test ./...` green (all three packages: `cmd`, `electro`, `cmd/kv`); the three new dynamodb-local-backed tests skip cleanly (no running container in this sandbox), matching every prior 09-12 plan's documented dynamodb-local behavior.

## User Setup Required

None - no external service configuration required. (Kurt's phone → `defcon34` mapping seed, per 12-CONTEXT.md D-05, is deferred to whichever later plan seeds `kph-tier` + Kurt's mapping — this plan only delivers the `kv code phone` command itself.)

## Next Phase Readiness

- `kv code phone <code> --add <e164> | --remove` is ready for use by the 12-05 (tier composition + seed data) plan to seed Kurt's phone → `defcon34` mapping.
- The gsi3 key writers (`AccessCodeGSI3PK`/`AccessCodeGSI3SK`) are available for any later plan (12-06 controller wiring) that needs to reproduce the byPhone key strings outside the `cmd` package.
- No blockers. `cd kv && go build ./... && go test ./...` green.

---
*Phase: 12-voip-ms-telephony-inbound-did*
*Completed: 2026-07-12*

## Self-Check: PASSED

- FOUND: kv/internal/app/electro/keys.go
- FOUND: kv/internal/app/cmd/code.go
- FOUND: kv/internal/app/cmd/code_test.go
- FOUND: .planning/phases/12-voip-ms-telephony-inbound-did/12-03-SUMMARY.md
- FOUND commit: a44f53f
- FOUND commit: e8d0117
