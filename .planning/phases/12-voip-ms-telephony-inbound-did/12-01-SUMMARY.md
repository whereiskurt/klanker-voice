---
phase: 12-voip-ms-telephony-inbound-did
plan: 01
subsystem: infra
tags: [go, cobra, voipms, rest-api, ssm, runbook]

# Dependency graph
requires:
  - phase: 11-voip-ms-telephony-local-asterisk-edge
    provides: the Asterisk edge + §24 gate + secret-render-at-start pattern this phase's runbook extends to SSM/production
provides:
  - "kv voipms command family (balance, route-did, set-caps, create-subaccount) over the VoIP.ms REST API"
  - "Centralized, testable VoIP.ms REST constants (base URL + method names), all flagged UNVERIFIED pending a live API-doc confirmation"
  - "docs/operators/voipms-provisioning-runbook.md: the full §25.F blank-account provisioning order + SSM secret paths + Toronto POP IP list"
affects: [12-02, 12-03, 12-04, 12-05, 12-06, 12-07, 12-08]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "VoIP.ms REST calls via stdlib net/http only (no new Go dependency), method names centralized in one named-constants block in voipms.go"
    - "voipmsClient with overridable baseURL/httpClient fields — tests inject an httptest.Server, no live network call ever made in CI"
    - "UNVERIFIED comment convention for API surface that could not be confirmed against live docs in this sandboxed session (no web-fetch tool available)"

key-files:
  created:
    - kv/internal/app/cmd/voipms.go
    - kv/internal/app/cmd/voipms_test.go
    - docs/operators/voipms-provisioning-runbook.md
  modified:
    - kv/internal/app/cmd/root.go

key-decisions:
  - "All six VoIP.ms method-name constants marked UNVERIFIED (not silently hardcoded as confirmed) because this executor session had no outbound web-fetch/browsing tool to check them against the live VoIP.ms API docs, contrary to the plan's VERIFY-BEFORE-HARDCODE step. Flagged as a required human follow-up before running any kv voipms subcommand against a real account."
  - "Included create-subaccount (plan's optional sub-command) since the operator runbook's step 5 depends on it to keep the §25.F order fully scriptable where possible."
  - "getVoipmsBalance degrades to raw JSON envelope on an unexpected response shape rather than panicking/erroring, since the balance response shape is itself UNVERIFIED."

patterns-established:
  - "kv REST-backed provider integrations (as opposed to DynamoDB-backed ones) get their own file with a single centralized constants block + an overridable client struct for offline testing — kv voipms is the template for any future kv <provider> command."

requirements-completed: [D-03, D-04, SC-1]

coverage:
  - id: D1
    description: "kv voipms command family (balance/route-did/set-caps/create-subaccount) with credentials read only from VOIPMS_API_USERNAME/VOIPMS_API_PASSWORD env, registered in root.go"
    requirement: "D-03"
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestVoipmsCmdHelpListsSubcommands"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestVoipmsRootRegistersCmd"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestVoipmsCredsFromEnv"
        status: pass
    human_judgment: false
  - id: D2
    description: "VoIP.ms REST base URL + every method name centralized in one constants block in voipms.go; no credential literal assigned; offline request-shape tests for balance/route-did/set-caps/create-subaccount"
    requirement: "D-03"
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestVoipmsMethodNamesCentralized"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestVoipmsRouteDidBuildsRequest"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestVoipmsGetBalanceBuildsRequest"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestVoipmsSetCapsBuildsRequest"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestVoipmsCreateSubaccountBuildsRequest"
        status: pass
    human_judgment: false
  - id: D3
    description: "VoIP.ms method names are confirmed against the live VoIP.ms API documentation before being trusted in production"
    requirement: "D-03"
    verification: []
    human_judgment: true
    rationale: "This executor session had no outbound web-fetch/browsing tool available to check the six method-name constants against the live voip.ms/resources/api reference, as the plan's VERIFY-BEFORE-HARDCODE step requires. Every constant is instead marked UNVERIFIED in code with a comment block explaining why, and the operator runbook's steps 5 and 7 explicitly tell the human to confirm the subaccount/DID-routing changes took effect in the portal rather than trusting a non-error CLI exit code. A human (or a future session with web access) must confirm the six method names before this is used against a real, live VoIP.ms account."
  - id: D4
    description: "docs/operators/voipms-provisioning-runbook.md documents the full §25.F blank-account setup order, the Secrets -> SSM subsection, the Toronto POP IP list, and contains no outbound-dialing step"
    requirement: "D-03,D-04"
    verification:
      - kind: other
        ref: "grep -qi 2fa && grep -qi outbound && grep -q put-parameter docs/operators/voipms-provisioning-runbook.md"
        status: pass
    human_judgment: false

# Metrics
duration: 25min
completed: 2026-07-12
status: complete
---

# Phase 12 Plan 01: VoIP.ms provisioning split (kv voipms + operator runbook) Summary

**`kv voipms` (balance/route-did/set-caps/create-subaccount) automates the API-drivable VoIP.ms steps behind a single centralized, UNVERIFIED-flagged REST constants block; a new operator runbook documents the portal-only §25.F security steps and the SSM secret paths D-04 requires.**

## Performance

- **Duration:** 25 min
- **Started:** 2026-07-12T18:45:00Z
- **Completed:** 2026-07-12T19:10:16Z
- **Tasks:** 2
- **Files modified:** 4

## Accomplishments
- `kv/internal/app/cmd/voipms.go`: `NewVoipmsCmd` builds `kv voipms balance | route-did <did> | set-caps | create-subaccount`, all credentials sourced only from `VOIPMS_API_USERNAME`/`VOIPMS_API_PASSWORD` env (never a flag, never printed)
- All VoIP.ms REST details (base URL + six method-name constants) centralized in one block at the top of `voipms.go`; the base-URL string appears exactly once in the file, mechanically proven by `TestVoipmsMethodNamesCentralized`
- `voipmsClient` is fully testable offline: `baseURL`/`httpClient` fields are overridable, and 17 new tests exercise every sub-command's request shape against an `httptest.Server` — no live network call in CI
- `docs/operators/voipms-provisioning-runbook.md`: the full §25.F order (2FA → international lock → balance/alerts → API+IP whitelist → `kv voipms create-subaccount` → order DID → `kv voipms route-did` → re-lock whitelist), a "Secrets → SSM" table for all six `VOIPMS_*`/telephony SSM parameters under `/kmv/secrets/use1/...`, the Toronto POP IP table with a 6-month re-verification note, and an explicit "registration POP must equal DID POP" statement — no outbound-dialing step anywhere

## Task Commits

Each task was committed atomically:

1. **Task 1: kv voipms command family over the VoIP.ms REST API** - `40c3224` (feat)
2. **Task 2: VoIP.ms operator provisioning runbook (§25.F order)** - `40678bf` (docs)

## Files Created/Modified
- `kv/internal/app/cmd/voipms.go` - `NewVoipmsCmd` + REST client + centralized constants block, all method names marked UNVERIFIED
- `kv/internal/app/cmd/voipms_test.go` - 17 offline tests (request shape, method-name centralization, creds-from-env, root registration)
- `kv/internal/app/cmd/root.go` - registers `NewVoipmsCmd(cfg)`
- `docs/operators/voipms-provisioning-runbook.md` - the §25.F operator runbook + Secrets→SSM table + Toronto POP IP list

## Decisions Made
- Marked every VoIP.ms method-name constant `UNVERIFIED` rather than presenting them as confirmed — this sandboxed executor session had no web-fetch/browsing tool available to check them against the live `voip.ms/resources/api` docs, which the plan's own "VERIFY-BEFORE-HARDCODE" instruction explicitly anticipates as a possible outcome ("if a name cannot be confirmed, leave a clearly-marked UNVERIFIED comment... do not silently guess"). See "Next Phase Readiness" below for the required follow-up.
- Included `create-subaccount` (the plan's optional sub-command) because the runbook's own §25.F step 5 needs it to keep the account-creation step scriptable rather than manual.
- `getVoipmsBalance` degrades to printing the raw JSON envelope if the expected `balance.current_balance` field isn't present, rather than erroring — appropriate given the response shape itself is unverified; an operator running `kv voipms balance` for the first time against a live account will see either the parsed number or the raw JSON, never a confusing crash.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Runbook-verification regex self-match in the centralized-constants test**
- **Found during:** Task 1 (`go test -run TestVoipms`)
- **Issue:** The doc comment above the VoIP.ms constants block originally repeated the literal string "rest.php" in prose (explaining the acceptance criterion), which made `TestVoipmsMethodNamesCentralized`'s `strings.Count(content, "rest.php")` count 2 occurrences instead of the required 1 — a self-inflicted test failure, not a real acceptance-criteria violation.
- **Fix:** Reworded the comment to say "the REST endpoint filename below" instead of literally repeating "rest.php".
- **Files modified:** `kv/internal/app/cmd/voipms.go`
- **Verification:** `go test ./internal/app/cmd/ -run TestVoipms -v` — all 17 tests pass, `rest.php` appears exactly once.
- **Committed in:** `40c3224` (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (1 bug, self-contained within the task before commit)
**Impact on plan:** No scope creep — a same-task correction before the task's own commit.

## Issues Encountered
- No outbound web-fetch/browsing tool was available in this executor environment, so the plan's "VERIFY-BEFORE-HARDCODE" instruction for the six VoIP.ms method-name constants could not be completed as a live-doc confirmation. Resolved per the plan's own documented escape hatch: every constant is marked `UNVERIFIED` in code with an explanatory comment block, and the runbook's steps 5/7 tell the operator to confirm the resulting portal state manually rather than trust a non-error CLI exit code. See "Next Phase Readiness."

## User Setup Required

None yet in this plan — SSM parameters and the live VoIP.ms account itself are provisioned by a human operator FOLLOWING `docs/operators/voipms-provisioning-runbook.md` (not automatically created here). No code in this plan requires local `.env`/config changes to build or test.

## Next Phase Readiness

- `kv voipms` is ready to be run by an operator working through the runbook, but **before it is run against a real, live VoIP.ms account**, a human (or a future session with web access) must confirm the six method-name constants in `kv/internal/app/cmd/voipms.go` (`voipmsMethodCreateSubAccount`, `voipmsMethodSetSubAccount`, `voipmsMethodSetDIDRouting`, `voipmsMethodGetBalance`, `voipmsMethodSetMaxCallDuration` — lowest confidence of the six, RESEARCH.md could not even confirm this method exists — and `voipmsMethodGetServersInfo`) against https://voip.ms/resources/api, and correct any that are wrong.
- The runbook's own verification checklist and steps 5/7's "confirm in the portal" instructions provide a manual safety net even if a method name turns out to be wrong (the `kv voipms` call would either error immediately or the portal check would catch a silent no-op).
- Downstream plans in this phase (SSM wiring, the `/tel` mint path, the Asterisk trunk, the `telephony-edge` deploy, the manual cellular proof) can proceed independently of this method-name confirmation — they don't call `kv voipms` themselves, they consume the runbook's documented SSM secret paths and the Toronto POP IP list this plan established.
- No blockers for Plan 12-02 onward.

---
*Phase: 12-voip-ms-telephony-inbound-did*
*Completed: 2026-07-12*
