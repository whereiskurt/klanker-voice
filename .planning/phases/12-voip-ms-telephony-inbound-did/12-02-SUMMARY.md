---
phase: 12-voip-ms-telephony-inbound-did
plan: 02
subsystem: auth
tags: [electrodb, dynamodb, nextjs, oidc, e164, sparse-gsi, no-oracle]

# Dependency graph
requires:
  - phase: 03-auth-service-access-codes
    provides: AccessCode entity, resolveAccessCode no-oracle pattern
  - phase: 05.1-bypass-join-login (bypass /join design)
    provides: mintAnonToken, byBypassToken sparse GSI, /join/[token]/route.ts no-oracle template
provides:
  - normalizeE164() shared E.164 normalization helper (single source for write + lookup)
  - AccessCode.phone / phoneEnabled attributes + sparse byPhone GSI (gsi3pk-gsi3sk-index)
  - resolvePhoneToCode() (null-on-any-miss no-oracle resolver mirroring resolveBypassToken)
  - GET /tel/<e164> — private, internal-only §23 caller-ID mint route
affects: [12-06-telephony-controller-wiring, 12-05-kv-code-phone-seed-data]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Sparse GSI mint-path mirror: byPhone (gsi3) copies byBypassToken (gsi2) key template, casing, and no-oracle resolver shape exactly"
    - "Single normalization source (normalizeE164) applied via entity `set` transform on write and explicit call on lookup"
    - "Uniform notFound() 404 helper for every token-minting-oracle failure mode, including bearer mismatch"

key-files:
  created:
    - apps/auth/webapp/src/lib/phone-normalization.ts
    - apps/auth/webapp/src/lib/__tests__/phone-normalization.test.ts
    - apps/auth/webapp/src/entities/__tests__/phone-resolution.test.ts
    - "apps/auth/webapp/src/app/tel/[e164]/route.ts"
    - apps/auth/webapp/src/app/tel/__tests__/tel-route.test.ts
  modified:
    - apps/auth/webapp/src/entities/access-code.ts

key-decisions:
  - "phoneEnabled boolean (default false) gates resolvePhoneToCode, mirroring bypassEnabled — a phone attribute alone does not activate the mint path"
  - "The /tel route's shared-bearer check (TELEPHONY_ENDPOINT_AUTH_TOKEN) returns the identical notFound() as every other failure, never a distinct 401/403"
  - "Only one log line in the route (tier-only, success path) — no raw caller ID or mapped/not-mapped distinction anywhere in the module"

patterns-established:
  - "Pitfall-3 discipline: normalizeE164 is a pure function with zero entity/db imports, called from the entity's `set` transform on write and directly on lookup — the exact same canonicalization on both sides of the byPhone GSI"

requirements-completed: [D-02, SC-2]

coverage:
  - id: D1
    description: "normalizeE164() produces canonical E.164 for spaced/dashed/parenthesized/bare-10-digit/idempotent/empty inputs"
    requirement: "D-02"
    verification:
      - kind: unit
        ref: "apps/auth/webapp/src/lib/__tests__/phone-normalization.test.ts"
        status: pass
    human_judgment: false
  - id: D2
    description: "AccessCode.phone (non-required, canonicalized on write) + sparse byPhone GSI on gsi3pk-gsi3sk-index + resolvePhoneToCode's null-on-any-miss contract, including write-time-normalization and sparse-indexing proofs"
    requirement: "D-02"
    verification:
      - kind: unit
        ref: "apps/auth/webapp/src/entities/__tests__/phone-resolution.test.ts"
        status: pass
    human_judgment: false
  - id: D3
    description: "GET /tel/<e164> mints a voice-valid token for a mapped caller ID and returns an identical 404 for unmapped/disabled/expired/bad-bearer, with cache-control no-store and tier-only logging"
    requirement: "SC-2"
    verification:
      - kind: unit
        ref: "apps/auth/webapp/src/app/tel/__tests__/tel-route.test.ts"
        status: pass
    human_judgment: true
    rationale: "The route's real deploy-time network isolation (internal-only, no internet exposure) is a 12-07 infrastructure concern verified at deploy time, not by this plan's unit tests; the source-grep test here proves the code-level no-oracle discipline only."

# Metrics
duration: 30min
completed: 2026-07-12
status: complete
---

# Phase 12 Plan 02: §23 Caller-ID Mint Path Summary

**A shared `normalizeE164` helper, an `AccessCode.phone` + sparse `byPhone` GSI + `resolvePhoneToCode` resolver, and a private `GET /tel/<e164>` route that mints bypass-`/join`-compatible OIDC tokens from a normalized caller ID — every piece mirrors the shipped bypass `/join` machinery byte-for-byte.**

## Performance

- **Duration:** ~30 min
- **Started:** 2026-07-12T19:12Z (session start)
- **Completed:** 2026-07-12T19:20:41Z
- **Tasks:** 3
- **Files modified:** 6 (5 created, 1 modified)

## Accomplishments

- `normalizeE164(phone)` — a pure, dependency-free helper that canonicalizes spaced/dashed/parenthesized/bare-10-digit North American numbers to `+1<10digits>`, idempotent on already-canonical input, `""` for empty/null/undefined. TDD-proven RED (import failure) before GREEN.
- `AccessCode` entity gains a non-required `phone` attribute (canonicalized via a `set` transform so every write is stored in normalized form) + `phoneEnabled` boolean (default `false`, mirrors `bypassEnabled`), plus a sparse `byPhone` GSI reusing the table's existing `gsi3pk-gsi3sk-index` (no new index created — the electro table already declares gsi1..gsi3). `resolvePhoneToCode` mirrors `resolveBypassToken`'s null-on-any-miss contract exactly: empty input, unmapped number, phone-disabled code, and expired code all resolve to `null`.
- `GET /tel/<e164>` — a private, internal-only route (deploy-time network lock is a 12-07 concern; this plan adds an optional `TELEPHONY_ENDPOINT_AUTH_TOKEN` shared-bearer check as defense-in-depth) that normalizes the caller ID, resolves it via `resolvePhoneToCode`, and mints a token via the UNCHANGED `mintAnonToken` — the minted token validates in the voice service with the same issuer/aud/jwks/kid as a bypass `/join` token. Every failure mode (empty/unmapped/disabled/expired number, mint error, bad bearer) returns a byte-identical 404 through one `notFound()` helper copied from `/join`.

## Task Commits

Each task was committed atomically:

1. **Task 1: Shared E.164 normalization helper** - `90f145a` (feat, TDD RED→GREEN)
2. **Task 2: phone attribute + sparse byPhone GSI + resolvePhoneToCode** - `cef4a58` (feat)
3. **Task 3: Private internal-only /tel mint route (no-oracle)** - `fac9483` (feat)

**Plan metadata:** (this commit)

## Files Created/Modified

- `apps/auth/webapp/src/lib/phone-normalization.ts` — the single E.164 normalization source (write + lookup)
- `apps/auth/webapp/src/lib/__tests__/phone-normalization.test.ts` — 5 tests, TDD RED proven before GREEN
- `apps/auth/webapp/src/entities/access-code.ts` — `phone`/`phoneEnabled` attributes, `byPhone` sparse GSI, `resolvePhoneToCode` + `ResolvedPhoneCode`
- `apps/auth/webapp/src/entities/__tests__/phone-resolution.test.ts` — 7 tests against real dynamodb-local, incl. write-time-normalization and sparse-GSI proofs
- `apps/auth/webapp/src/app/tel/[e164]/route.ts` — the private mint route
- `apps/auth/webapp/src/app/tel/__tests__/tel-route.test.ts` — 4 tests, incl. identical-404 assertions across every failure mode and a source-grep for the no-oracle logging discipline

## Decisions Made

- `phoneEnabled` (default `false`) is a required gate in `resolvePhoneToCode`, mirroring `bypassEnabled` — writing a `phone` attribute alone does not activate the mint path, matching the plan's explicit mirror-`resolveBypassToken` instruction.
- The `/tel` route's shared-bearer check returns the SAME `notFound()` as every other miss (never a distinct 401/403), so a bearer-mismatch is not itself a distinguishable oracle signal — this was implicit in the plan's acceptance criteria ("that same 404, not a distinct 401 shape") and applied literally.
- Logging is scoped to a single tier-only line on the success path; no log call fires on any failure path, so the log stream itself carries no unmapped/mapped distinction and never touches the raw caller ID.

## Deviations from Plan

None — plan executed exactly as written.

**Test-environment note (not a deviation, documented for the record):** `apps/auth/webapp`'s dynamodb-local-backed test suite (10 of 13 test files, including this plan's `phone-resolution.test.ts` and `tel-route.test.ts`) requires a live `dynamodb-local` on `localhost:8888` with the `kmv-auth-electro` table (gsi1/gsi2/gsi3). That container was not running at session start (baseline: 4/10 files passing, 21/43 tests). A `dynamodb-local` docker container was started and the electro table created with all three GSIs (matching the terraform `electro` table schema) to genuinely exercise the new byPhone GSI and prove the full auth suite green (`npm test`: 59/59). The container was stopped and removed at the end of this session — it is ephemeral test infrastructure, not a permanent addition, and this is unrelated to the plan's own file scope (the pre-existing `access-code-resolution.test.ts` already depended on this same backend before this plan started).

**Node version note:** this repo's `apps/auth/webapp` requires Node ≥22.12 (vitest 4.1.9 / std-env ESM floor); the ambient default `node` (v22.1.0) fails to even load `vitest.config.ts`. Tests were run under `nvm use 22.12.0`, matching the precedent already documented in STATE.md for this same package (Phase 5.2's client suite hit an identical floor).

## Issues Encountered

- **Unrelated concurrent worktree activity (informational only, not touched):** during this session, `kv/internal/app/cmd/voipms.go`, `kv/internal/app/cmd/voipms_test.go`, and `docs/operators/voipms-provisioning-runbook.md` accumulated uncommitted modifications in this shared worktree that were not made by this plan's execution (plan 12-02's declared file scope is entirely within `apps/auth/webapp`). Consistent with the phase's Plan 01 SUMMARY flag ("every VoIP.ms method name marked UNVERIFIED, human follow-up needed"), the diff appears to resolve that exact follow-up. These files were left untouched and unstaged — every commit in this plan staged only its own declared files individually (never `git add -A`/`git add .`), so none of that concurrent work was swept into this plan's commits. Flagged here so the orchestrator/next agent is aware these files carry uncommitted changes from outside this plan's scope.

## User Setup Required

None - no external service configuration required. (The `TELEPHONY_ENDPOINT_AUTH_TOKEN` SSM wiring is a 12-07/deploy concern, not this plan's.)

## Next Phase Readiness

- The §23 mint path (normalize → resolve → mint) is complete and unit-tested; it is ready for the 12-06 telephony controller to call over HTTP with a normalized caller ID.
- `kph-tier` seeding and Kurt's phone → `defcon34` mapping (D-05, via `kv code phone`) remain for a later plan (12-05 per the phase's suggested build order) — this plan intentionally ships only the entity/route mechanism, not seed data.
- No blockers for the next plan in this phase.

## Self-Check: PASSED

- All 6 key files verified present on disk (`[ -f ]`).
- All 3 task commits (`90f145a`, `cef4a58`, `fac9483`) verified in `git log`.
- All task-level `<acceptance_criteria>` re-run and passing (phone non-required, byPhone index name/templates, resolvePhoneToCode null-on-every-miss, write-time normalization proof, mintAnonToken/resolvePhoneToCode reuse, cache-control no-store, tier-only logging).
- Plan-level `<verification>` re-run: `cd apps/auth/webapp && npm test` → 13 files / 59 tests passed (0 failed).

---
*Phase: 12-voip-ms-telephony-inbound-did*
*Completed: 2026-07-12*
