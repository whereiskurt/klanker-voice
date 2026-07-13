---
phase: 15-private-transcription-ledger-s3-batch-athena-admin-conversat
plan: 01
subsystem: auth
tags: [oidc-provider, electrodb, jwt, dynamodb, vitest]

requires: []
provides:
  - "Namespaced `https://klankermaker.ai/email` + `https://klankermaker.ai/code` access-token claims (D-01 thin token extended)"
  - "AuthProfile.activeCode (latest-wins) stamped by the login-intent bridge on every login"
affects: [15-02, 15-03]

tech-stack:
  added: []
  patterns:
    - "Namespaced claim registered once in config.oidc.claimNames, consumed by extraTokenClaims, mirrored byte-for-byte in the voice service (auth.py constants, Plan 15-02)"
    - "Additive-optional bridge parameter: setActiveTier(userId, tierId, group, code?) — new capability threaded through an existing call site with zero breakage to prior callers"

key-files:
  created:
    - apps/auth/webapp/src/config/__tests__/token-claims.test.ts
    - apps/auth/webapp/src/entities/__tests__/auth-profile-active-code.test.ts
  modified:
    - apps/auth/webapp/src/config/index.ts
    - apps/auth/webapp/src/config/oidc.ts
    - apps/auth/webapp/src/entities/auth-profile.ts
    - apps/auth/webapp/src/config/login-intent-bridge.ts
    - apps/auth/webapp/src/config/__tests__/oidc-resource-token.test.ts
    - apps/auth/webapp/src/config/__tests__/login-intent-bridge.test.ts

key-decisions:
  - "extraTokenClaims resolves email/code from the SAME already-fetched AuthProfile — no second DynamoDB read added"
  - "setActiveTier's new `code` param is additive/optional (4th arg) so every pre-existing call site stays untouched except the one deliberate bridge call site"
  - "Updated the pre-existing oidc-resource-token.test.ts 'D-01 thin token, only tier_id+group' assertion to include email+code — that invariant is intentionally extended by this plan (Rule 1, in-scope fix)"

patterns-established:
  - "Claim-name single source of truth: config.oidc.claimNames — any future namespaced claim goes here first, then extraTokenClaims, then the consuming service's constants"

requirements-completed: [LEDG-01]

coverage:
  - id: D1
    description: "Access token carries namespaced https://klankermaker.ai/email and https://klankermaker.ai/code claims, resolved from AuthProfile, null-safe"
    requirement: LEDG-01
    verification:
      - kind: unit
        ref: "apps/auth/webapp/src/config/__tests__/token-claims.test.ts"
        status: pass
      - kind: unit
        ref: "apps/auth/webapp/src/config/__tests__/oidc-resource-token.test.ts#mints a three-segment RS256 JWT with a kid present in the provider's JWKS, audienced to the voice resource"
        status: pass
    human_judgment: false
  - id: D2
    description: "AuthProfile persists activeCode, stamped by the login-intent bridge on every login, latest-wins (D-05)"
    requirement: LEDG-01
    verification:
      - kind: unit
        ref: "apps/auth/webapp/src/entities/__tests__/auth-profile-active-code.test.ts"
        status: pass
      - kind: unit
        ref: "apps/auth/webapp/src/config/__tests__/login-intent-bridge.test.ts#stamps activeCode from intent.code (LEDG-01) so the token claim resolves to a real value"
        status: pass
    human_judgment: false
  - id: D3
    description: "Byte-for-byte claim-name parity with the voice service's auth.py EMAIL_CLAIM/CODE_CLAIM constants (Plan 15-02, not yet written)"
    verification: []
    human_judgment: true
    rationale: "Plan 15-02 (the voice-service side of this contract) has not been executed yet — cross-service byte-parity can only be confirmed once both sides exist. config.oidc.claimNames.email/.code are pinned here as the source of truth for that plan to match against."

duration: 25min
completed: 2026-07-13
status: complete
---

# Phase 15 Plan 01: Namespaced Email/Code Claims + AuthProfile activeCode Summary

**The OIDC access token now carries `https://klankermaker.ai/email` and `https://klankermaker.ai/code` namespaced claims, and AuthProfile persists the redeemed access code (latest-wins) via the existing login-intent bridge — the identity contract the voice-service transcription ledger (Plan 15-02) will read.**

## Performance

- **Duration:** 25 min
- **Started:** 2026-07-13T00:17:00-04:00
- **Completed:** 2026-07-13T00:39:14-04:00
- **Tasks:** 2 completed
- **Files modified:** 6 (4 source, 2 pre-existing tests extended) + 2 new test files

## Accomplishments

- Access token issued by auth.klankermaker.ai now carries `https://klankermaker.ai/email` and `https://klankermaker.ai/code`, resolved from AuthProfile in the same `extraTokenClaims` fetch that already resolves `tier_id`/`group` — zero new DynamoDB reads.
- `AuthProfile.activeCode` (new ElectroDB attribute) is stamped by `applyLoginIntentBridge` on every login, alongside `activeTierId`/`activeGroup`, using the exact same latest-wins (D-05) semantics.
- Both claims are null-safe: an unset profile field, or a missing profile entirely, resolves to `null`, never `undefined` and never a thrown error.
- Fixed a stale pre-existing test assertion (`oidc-resource-token.test.ts`) that encoded the now-superseded "D-01 thin token, only tier_id+group" invariant.

## Task Commits

Each task was committed atomically:

1. **Task 1: Register email + code claim names and emit them in extraTokenClaims** - `6cc3c45` (feat)
2. **Task 2: Persist activeCode on AuthProfile via the existing login-intent bridge** - `537bf1b` (feat)

_Both tasks were `tdd="true"`; tests were written and run green together with the implementation in the same commit (no separate RED commit — this plan's TDD flow authored test+implementation as one atomic, already-passing unit per task, matching the plan's own "Test N: ..." behavior-spec-as-acceptance-criteria structure rather than a literal RED→GREEN commit pair)._

## Files Created/Modified

- `apps/auth/webapp/src/config/index.ts` - `claimNames.email` / `claimNames.code` added to the registry (single source of truth)
- `apps/auth/webapp/src/config/oidc.ts` - `extraTokenClaims` emits the two new claims from the already-fetched profile
- `apps/auth/webapp/src/config/__tests__/token-claims.test.ts` (NEW) - 4 tests: claim-name contract, resolved values, null-safety, early-return preserved
- `apps/auth/webapp/src/entities/auth-profile.ts` - `activeCode` attribute; `setActiveTier` gains an additive optional 4th `code` param
- `apps/auth/webapp/src/config/login-intent-bridge.ts` - passes `intent.code` into the same `setActiveTier` call
- `apps/auth/webapp/src/entities/__tests__/auth-profile-active-code.test.ts` (NEW) - 3 tests: stamp, no-spurious-overwrite, latest-wins
- `apps/auth/webapp/src/config/__tests__/login-intent-bridge.test.ts` - +1 test proving the bridge threads `intent.code` through to `activeCode`
- `apps/auth/webapp/src/config/__tests__/oidc-resource-token.test.ts` - updated the "no custom claims beyond tier_id/group" assertion to include email/code

## Decisions Made

- Resolved email/code from the SAME `getAuthProfile(token.accountId)` call `extraTokenClaims` already makes — explicitly verified via a grep-gated acceptance criterion (`getAuthProfile(` count inside the `extraTokenClaims` block is exactly 1).
- `setActiveTier`'s new `code` parameter is optional and additive (4th positional arg) rather than a breaking signature change — every other call site (there is only the one, in `login-intent-bridge.ts`) needed no edits.
- Kept the `?? undefined` posture for `activeCode` identical to the existing `activeGroup` field (empty string is NOT coerced to unset — only `null`/`undefined` are), for consistency with the established D-05 pattern rather than inventing new normalization rules.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Stale test assertion broken by this plan's own intentional change**
- **Found during:** Task 1 (full-suite verification)
- **Issue:** `oidc-resource-token.test.ts` (Plan 03-03) asserted the access token carries "no custom claims beyond the two namespaced ones (D-01 thin token)" — an invariant this plan deliberately extends to four claims.
- **Fix:** Updated the assertion to include `claimNames.email` / `claimNames.code`, with a comment noting the extension and pointing to `token-claims.test.ts` for dedicated coverage.
- **Files modified:** `apps/auth/webapp/src/config/__tests__/oidc-resource-token.test.ts`
- **Verification:** Full suite green (67/67) after the fix.
- **Committed in:** `6cc3c45` (part of Task 1's commit)

---

**Total deviations:** 1 auto-fixed (Rule 1)
**Impact on plan:** Necessary fix — the assertion encoded an invariant this plan's own objective explicitly supersedes. No scope creep; no other file touched beyond what the plan declared.

## Issues Encountered

- Local test environment needed setup not covered by the plan's own scope: `npm install` under the ambient Node v22.1.0 hit a native-binding failure in `vitest`'s `rolldown` dependency (matches the project's known "client tests need Node ≥22.12" gotcha, now also true for the auth webapp's vitest 4.x); resolved by running under `nvm use 23` (v23.6.0, already installed locally). Separately, `dynamodb-local` (port 8888, shared across worktrees) had no `kmv-auth-electro`/`kmv-auth-authjs` tables provisioned in this session — created them by hand via `aws dynamodb create-table` (pk/sk + `gsi1pk-gsi1sk-index`/`gsi2pk-gsi2sk-index`/`gsi3pk-gsi3sk-index` on `kmv-auth-electro`; pk/sk + `GSI1` on `kmv-auth-authjs`), matching the schema all existing entities already declare (`access-code.ts`, `auth-profile.ts`, `tier.ts`, `oidc-adapter.ts`). Neither is a plan defect — both are one-time local-environment setup, same class as the Plan 03-02 precedent documented in its own SUMMARY ("Created kmv-auth-electro on the running local container via aws dynamodb create-table... mirroring how Plan 01's own user_setup already assumes this container exists").

## User Setup Required

None - no external service configuration required. (Local dynamodb-local table provisioning was a one-time dev-environment step, not a user-facing setup requirement — see Issues Encountered.)

## Next Phase Readiness

- Plan 15-02 (voice-service side) can now pin `EMAIL_CLAIM = "https://klankermaker.ai/email"` and `CODE_CLAIM = "https://klankermaker.ai/code"` against a live, tested contract — `config.oidc.claimNames.email`/`.code` in `apps/auth/webapp/src/config/index.ts` are the source of truth to match byte-for-byte.
- No blockers. The manual cross-service byte-parity check called out in the plan's `<verification>` section (comparing `config/index.ts` claim names against `auth.py`'s constants) is deferred to Plan 15-02, since that file doesn't exist yet — tracked as coverage item D3 (`human_judgment: true`).

---
*Phase: 15-private-transcription-ledger-s3-batch-athena-admin-conversat*
*Completed: 2026-07-13*
