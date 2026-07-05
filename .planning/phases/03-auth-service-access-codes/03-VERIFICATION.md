---
phase: 03-auth-service-access-codes
verified: 2026-07-05T19:45:00Z
status: gaps_found
score: 4/5 must-haves verified
behavior_unverified: 0
overrides_applied: 0
gaps:
  - truth: "User may enter any access code (or none) at login: known codes map to tiers, unknown/blank yields a no-access tier with guidance"
    status: partial
    reason: "The resolution logic (unknown/blank/expired/over-cap -> no-access, login always succeeds) is fully implemented and proven by test. The 'with guidance' clause of the success criterion (and its source design decision, 03-CONTEXT.md D-07: 'UI explains how to get a code') is NOT implemented anywhere. The only post-login page (src/app/(authlogin)/page.tsx) shows sign-in status and a Sign Out button only — no tier/access-code messaging of any kind. This was a silent scope drop: the executor's own code comment on that file explicitly notes quota-tier status display was 'dropped for klanker-voice,' but no plan's must_haves captured the D-07 guidance requirement as a truth to verify, so it was never tested for and never flagged as a deviation in any of the four SUMMARY.md files."
    artifacts:
      - path: "apps/auth/webapp/src/app/(authlogin)/page.tsx"
        issue: "No tier-status or no-access guidance text; component only renders avatar, email, and Sign Out"
    missing:
      - "User-facing copy (in the auth webapp home page, or an explicit, documented decision to defer it to Phase 5's voice client) telling a no-access user how to obtain an access code"
deferred: []
---

# Phase 3: Auth Service & Access Codes Verification Report

**Phase Goal:** A user can sign in via magic link with an access code and receive a tier-claimed JWT that downstream services validate offline via JWKS; operators manage codes and tiers via `kv`.
**Verified:** 2026-07-05T19:45:00Z
**Status:** gaps_found
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | User can sign in via magic-link email, with an interstitial confirm-click page so corporate link-scanners don't consume tokens | VERIFIED | `apps/auth/webapp/src/app/(authlogin)/login/confirm/page.tsx` is a zero-client-JS async Server Component rendering a plain `<form method="GET">`; token only reaches `/api/auth/callback/nodemailer` on explicit submit. `config/auth.ts` signupHTML links to `/login/confirm`, not the raw callback. `confirm-no-consume.test.ts` + `login-altcha.test.ts` pass (verified live, ran full vitest suite: 33/33 pass). Two live DynamoDB tables (`kmv-auth-authjs`, `kmv-auth-electro`) confirmed `ACTIVE` in us-east-1 via `aws dynamodb describe-table`. |
| 2 | Auth issues JWT access tokens with tier/group claims that a relying service validates offline via the JWKS endpoint | VERIFIED | `config/oidc.ts` `resourceIndicators.enabled: true`, `getResourceServerInfo` returns `accessTokenFormat: "jwt"` + `jwt.sign.alg: "RS256"` + pinned audience. `extraTokenClaims` emits exactly the two namespaced claims (`https://klankermaker.ai/tier_id`, `.../group`) for `AccessToken`-kind tokens, read from `AuthProfile.activeTierId/activeGroup`. `configuration.jwks` sourced from `OIDC_JWKS` env. Live SSM SecureString `/kmv/secrets/use1/oidc/jwks` confirmed present (RS256, 1 key, `kid: kmv-oidc-m-zCTIi5`, matches the commit message). `oidc-resource-token.test.ts` (mints+verifies a real token against the injected JWK Set) part of the 33/33 passing suite. |
| 3 | User may enter any access code (or none) at login: known codes map to tiers, unknown/blank yields a no-access tier with guidance | PARTIAL (see gap) | `resolveAccessCode()` (`entities/access-code.ts`) implements the full matrix (unknown/blank/expired/over-cap -> `no-access`, no oracle distinguishing them, T-03-07); `/api/login`'s `AUTH_INVITE_CODES` gate is removed and login always proceeds. Tested by `access-code-resolution.test.ts` + `login-access-code.test.ts`. **However**, the "with guidance" clause (and its source decision, D-07: "UI explains how to get a code") has no implementation anywhere in the app — see gap below. |
| 4 | Operator-defined codes carry expiry and max-redemption limits, and the login form is protected by Altcha captcha | VERIFIED | `AccessCode` entity has `expiresAt` (epoch ms) and `maxRedemptions`/`redemptionCount` (unique-user counted via conditional `CodeRedemption.create()`). `login/page.tsx` wires the `<altcha-widget>` client-side, gates submit on `altchaVerified`; `/api/login` calls `verifySolution()` + an in-memory replay guard (`markChallengeUsed`). Both proven by test. |
| 5 | Operator can create, list, and expire access codes and define/list tiers via `kv` | VERIFIED (capability); gap on live data | `kv code create/list/expire` and `kv tier define/list` all build, `--help` renders correctly, and are wired to real DynamoDB PutItem/Query/UpdateItem (confirmed by running `go build`, `go vet`, and the full `go test ./...` suite locally: all pass). Bidirectional key-compat round-trip (`TestRoundTrip_KVWriteWebappRead`, `TestRoundTrip_WebappWriteKVRead`) passes against a live dynamodb-local table — Pitfall 1 is genuinely closed. **However**, querying the real AWS `kmv-auth-electro` table's `accesscodes#`/`tiers#` GSI1 partitions returned zero items — the design-spec seed data (`demo`, `kphdemo123`, and the three tiers) documented in 03-04-SUMMARY.md was seeded only against the local `dynamodb-local` container, never against the live table. This is a data/operational gap, not a code defect — `kv`'s default `--table` is already `kmv-auth-electro` and the same documented commands would populate the live table once run with real AWS credentials. |

**Score:** 4/5 truths fully verified, 1 partial (functional core correct, a documented sub-clause missing)

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `apps/auth/webapp/` | Trimmed, buildable Next.js 16 port | VERIFIED | Builds green; `npx tsc --noEmit` clean; single Email provider, single `voice` OIDC client confirmed via `grep -c client_id` = 1 |
| `apps/auth/webapp/src/app/(authlogin)/login/confirm/page.tsx` | Interstitial confirm page | VERIFIED | 90 lines, zero client JS, form-based consume-on-click |
| `apps/auth/webapp/src/app/api/health/route.ts` | Dependency-free health check | VERIFIED | Returns `{status:"ok"}`, no DB/SES calls |
| `apps/auth/webapp/src/entities/{access-code,tier,login-intent,code-redemption}.ts` | Four ElectroDB entities, explicit key templates | VERIFIED | All four exist with explicit `pk`/`sk`/`gsi1` templates matching the Plan-02 contract |
| `apps/auth/webapp/src/config/login-intent-bridge.ts` | Login->token tier bridge | VERIFIED | `applyLoginIntentBridge()` consumes intent, stamps `activeTierId`/`activeGroup`, gates redemption count on conditional create |
| `apps/auth/webapp/src/config/oidc.ts` | Resource Indicators + RS256 JWT + JWKS | VERIFIED | `resourceIndicators.enabled: true`, `jwt.sign.alg: "RS256"`, `configuration.jwks` from `OIDC_JWKS` |
| `kv/` Go module | code/tier CRUD, key-compat | VERIFIED | Builds, vets, and full test suite passes; help tree renders correctly for root/code/tier |
| `kv/internal/app/electro/keys.go` | Byte-for-byte key reproduction | VERIFIED | `TestKeyCompat_*` (pure) + `TestRoundTrip_*` (live dynamodb-local) all pass |
| Two live DynamoDB tables (us-east-1) | `kmv-auth-authjs`, `kmv-auth-electro` | VERIFIED | Both `TableStatus: ACTIVE`, expected GSIs present (`GSI1` / `gsi1pk-gsi1sk-index`, `gsi2...`, `gsi3...`) |
| SSM SecureString `/kmv/secrets/use1/oidc/jwks` | Persistent shared RS256 JWK Set | VERIFIED | Present, `SecureString`, decodes to a valid 1-key RS256 JWK Set with a stable `kid` |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| signupHTML email link | `/login/confirm` | href rewrite | WIRED | `grep -q 'login/confirm' config/auth.ts` confirmed |
| `/login/confirm` submit | `/api/auth/callback/nodemailer` | plain GET form action | WIRED | Verified by reading the page source directly |
| `/api/login` | `LoginIntent` table | `resolveAccessCode()` + `LoginIntent.upsert()` before `signIn()` | WIRED | `AUTH_INVITE_CODES` absent from route.ts; `LoginIntent`/`resolveAccessCode` imports present |
| `auth.ts` jwt callback (nodemailer branch) | `applyLoginIntentBridge()` | awaited call after `upsertAuthProfile` | WIRED | Confirmed via grep + full test suite pass (`login-intent-bridge.test.ts`) |
| `extraTokenClaims` | `AuthProfile.activeTierId/activeGroup` | `getAuthProfile(token.accountId)` | WIRED | Read directly from source; matches Plan 02's fields exactly |
| `OIDC_JWKS` (SSM) | `configuration.jwks` | env parse | WIRED | Live SSM value confirmed valid RS256 JWK Set |
| `kv` writes | webapp `AccessCode.get`/`list` | shared key templates | WIRED | Proven bidirectionally by live round-trip test against dynamodb-local |

### Behavioral Spot-Checks / Test Execution (run directly by the verifier, not taken from SUMMARY claims)

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Go module builds/vets clean | `cd kv && go build ./... && go vet ./...` | BUILD-OK / VET-OK | PASS |
| Key-compat pure tests | `go test ./... -run 'KeyCompat' -v` | All subtests PASS (AccessCode + Tier, case-cross-check) | PASS |
| Key-compat live round-trip | `go test ./... -run 'RoundTrip' -v` | `TestRoundTrip_KVWriteWebappRead` PASS, `TestRoundTrip_WebappWriteKVRead` PASS (against live dynamodb-local) | PASS |
| `kv` help tree | `go run ./cmd/kv --help`, `code --help`, `tier --help` | Renders create/list/expire, define/list correctly | PASS |
| Full webapp vitest suite | `npx vitest run` (Node 23.6.0 via nvm, matching the executors' own toolchain accommodation) | 9 test files, 33/33 tests PASS | PASS |
| Naming constraint | `grep -rIiE 'voiceai|\bkmk\b'` across `apps/auth/webapp` and `kv/` | No matches | PASS |
| Live DynamoDB tables | `aws dynamodb describe-table` (both tables, profile `klanker-terraform`) | Both `ACTIVE` | PASS |
| Live JWKS secret | `aws ssm get-parameter --with-decryption` (profile `klanker-application`) | Valid RS256 JWK Set, 1 key, stable kid | PASS |
| Live seed data | `aws dynamodb query` (gsi1, `accesscodes#`/`tiers#` partitions) | Zero items returned | GAP (see above) |
| `kmv-auth-electro` native TTL | `aws dynamodb describe-time-to-live` | `TimeToLiveStatus: DISABLED` | Documented known gap (03-02), not a plan must-have — belt-and-suspenders design means correctness does not depend on it |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| AUTH-01 | 03-01 | Magic-link sign-in with scanner-safe interstitial | SATISFIED | Confirm page verified above; live E2E (real SES send/click) not exercised in any sandboxed executor session — flagged by all four SUMMARYs as a deferred Phase-4-deploy verification item, consistent with the team's guidance not to fail on this |
| AUTH-02 | 03-03 | JWT access tokens, tier/group claims, offline JWKS validation | SATISFIED | Fully verified in code + live SSM secret; the deployed `/token` HTTP round-trip is the same deferred class of gap as AUTH-01 |
| AUTH-03 | 03-02 | Any/blank code accepted; known->tier, unknown/blank->no-access | PARTIAL | Resolution logic satisfied; "with guidance" sub-clause not implemented — see gap |
| AUTH-04 | 03-02 | Expiry + max-redemption limits, unique-user counting | SATISFIED | `AccessCode.expiresAt`/`maxRedemptions`, conditional `CodeRedemption.create()`, tested |
| AUTH-05 | 03-01 | Altcha captcha protection | SATISFIED | Widget wired client-side, `verifySolution` + replay guard server-side, tested |
| KV-01 | 03-04 | Create/list/expire codes via `kv` | SATISFIED (capability); live-table seed data missing (operational gap, not a code gap) |
| KV-02 | 03-04 | Define/list tiers via `kv` | SATISFIED (capability); same live-table seed gap as KV-01 |

No orphaned requirements found — REQUIREMENTS.md maps exactly AUTH-01..05 and KV-01/02 to Phase 3, all seven are claimed across the four plans.

### Anti-Patterns Found

None blocking. No `TBD`/`FIXME`/`XXX` markers found in the phase's modified files. No stub return values (`return null`/`{}`/`[]` in production code paths feeding real data) found in the entities, bridge, oidc config, or `kv` command implementations reviewed. The one placeholder-shaped comment found (`apps/auth/webapp/src/app/(authlogin)/login/page.tsx:26-27`, a stale reference to a since-replaced `AUTH_INVITE_CODES` gate) is a leftover doc comment, not a functional stub — the code below it was in fact replaced by Plan 02's `resolveAccessCode`/`LoginIntent` wiring (confirmed by grep: `AUTH_INVITE_CODES` does not appear in `route.ts`).

### Human Verification Required

1. **Real end-to-end magic-link round-trip** — submit email + Altcha at a deployed `/login`, receive the real SES email, click through `/login/confirm`, and confirm an authenticated session results. All four plans' own SUMMARYs flag this as unexercised in their sandboxed executor sessions (no live NextAuth server + real inbox available). Per the verification brief, this is treated as a Phase-4 (deploy) verification item, not a Phase-3 blocker.
   **Expected:** SES email arrives, confirm-page click yields `sess_auth` cookie.
   **Why human:** Requires a deployed app instance and a real inbox; not exercisable in a sandboxed session.

2. **Real `/token` HTTP round-trip** — authorize the voice client against a deployed auth service, hit `/token`, and decode the returned access token to confirm it matches the pinned Phase-4 contract (`aud`, `kid`-in-`/jwks`, the two claims) through the actual Koa/HTTP layer rather than the direct-construction test seam used in `oidc-resource-token.test.ts`.
   **Expected:** RS256 JWT, `aud: https://voice.klankermaker.ai`, `tier_id`/`group` claims present.
   **Why human:** Requires a deployed app instance; same deferred-to-Phase-4 category as item 1.

### Gaps Summary

One real gap and one documentation nit found, neither of which invalidates the phase's core engineering achievement (the token/tier/kv contract is genuinely solid and independently re-verified by this pass — full test suites re-run, live infra re-checked, not taken on SUMMARY claims):

1. **No-access user guidance UI is entirely missing** (success criterion #3's "with guidance" clause; design decision D-07). The access-code resolution logic is correct and well-tested, but nothing in the app tells a signed-in no-access user how to obtain a code — the only post-login page is a bare sign-in/sign-out status card. This was silently dropped during the Plan-01 trim (the code comment there explicitly notes quota-tier status display was removed) and no subsequent plan's `must_haves` re-captured the D-07 guidance requirement, so it was never tested for or flagged as a deviation. Recommend either (a) a small addition to the auth webapp's home page showing `activeTierId` and, when `no-access`, guidance text, or (b) an explicit, documented decision to defer this to Phase 5's voice client UX (with the ROADMAP/CONTEXT updated to reflect that choice) — either resolves this cleanly; it is not resolved today.

2. **Live-table seed data absent** (KV-01/KV-02 plan must-have "kv seeds the design-spec codes and tiers"). `kv`'s code/tier CRUD is fully functional and proven correct against both dynamodb-local and (by direct inspection) is wired to accept real AWS credentials with `kmv-auth-electro` as its default table — but the actual AWS table currently holds zero access-code/tier records. This is a one-command operational follow-up (the exact seed commands are already documented in 03-04-SUMMARY.md), not a code defect, and does not block Phase 4's engineering work. It does need to happen before any real user can redeem `demo`/`kphdemo123` in a live/conference setting.

Neither gap touches the AUTH-02 JWT/JWKS contract (the phase's stated hardest dependency for Phase 4), which is fully verified end-to-end in code and live infra.

---

_Verified: 2026-07-05T19:45:00Z_
_Verifier: Claude (gsd-verifier)_
