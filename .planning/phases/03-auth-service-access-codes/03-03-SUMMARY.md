---
phase: 03-auth-service-access-codes
plan: 03
subsystem: auth
tags: [oidc-provider, resource-indicators, jwt, jwks, rs256, next-auth]

requires:
  - phase: 03-auth-service-access-codes plan 02
    provides: "AuthProfile.activeTierId/activeGroup — the latest-wins tier bridge extraTokenClaims reads via getAuthProfile(token.accountId)"
provides:
  - "oidc-provider Resource Indicators enabled: features.resourceIndicators.{enabled,defaultResource,useGrantedResource,getResourceServerInfo} mint a signed RS256 JWT access token audienced to https://voice.klankermaker.ai"
  - "extraTokenClaims injects EXACTLY the two namespaced claims (https://klankermaker.ai/tier_id, https://klankermaker.ai/group) onto AccessToken-kind tokens only, read from AuthProfile.activeTierId/activeGroup — no other custom claims, ID token untouched"
  - "configuration.jwks sourced from OIDC_JWKS (a JSON JWK Set) — persistent/shared signing key, not auto-generated per process"
  - "config/index.ts: oidc.voiceResource/voiceAudience/claimNames/jwks — the single source config.oidc.ttl.accessToken (3600s) now documented as the pinned Phase-4 value"
  - "from-aws.tmpl: OIDC_JWKS -> SSM ARN /kmv/secrets/use1/oidc/jwks; AUTH_PUBLIC_URL -> https://auth.klankermaker.ai/use1 (pinned base, Pitfall 6)"
  - "Pinned Phase-4 contract (verbatim below) for PyJWT + PyJWKClient to match byte-for-byte"
affects: [phase-4-voice]

tech-stack:
  added: []
  patterns:
    - "Test-mint an AccessToken directly via `new (oidc as any).AccessToken({...})` + a manually-invoked `features.resourceIndicators.getResourceServerInfo` hook, bypassing the HTTP/Koa layer entirely — JWT-format AccessToken.save() never calls adapter.upsert (see node_modules/oidc-provider/lib/models/formats/jwt.js), so the whole AUTH-02 contract is unit-testable with zero DynamoDB dependency for the token-minting mechanics themselves (AuthProfile reads still hit real dynamodb-local, matching Plan 02's convention)"
    - "Deep-import oidc-provider's internal `lib/helpers/weak_cache.js` (`instance(oidc)`) in tests to read the SAME configuration/keystore/jwks the provider was constructed with — oidc-provider ships no package.json `exports` map, so this is a stable (if unofficial) test seam, not a copy that could drift from the real served JWKS"
    - "Fixed-kid injection into the test JWK Set (`privateJwk.kid = publicJwk.kid = \"test-kid-1\"`) so independent offline signature verification (jose.createLocalJWKSet) can match without reimplementing oidc-provider's internal calculateKid() thumbprint algorithm"

key-files:
  created:
    - apps/auth/webapp/src/config/__tests__/oidc-resource-token.test.ts
  modified:
    - apps/auth/webapp/src/config/oidc.ts
    - apps/auth/webapp/src/config/index.ts
    - apps/auth/webapp/from-aws.tmpl

key-decisions:
  - "voiceResource and voiceAudience are the SAME pinned URI (https://voice.klankermaker.ai) — single first-party client, no need for them to differ"
  - "claimNames are namespaced (https://klankermaker.ai/tier_id, https://klankermaker.ai/group) per 03-RESEARCH.md's recommendation (A4) and the pinned_phase4_contract in 03-03-PLAN.md"
  - "configuration.jwks falls back to `undefined` when OIDC_JWKS is unset (local dev without the secret configured) rather than throwing — oidc-provider's own dev-only quick-start-keys fallback (with a console warning) is preserved unchanged for that case"
  - "JWKS secret creation (SSM SecureString /kmv/secrets/use1/oidc/jwks) is gated behind a human-action checkpoint — no live AWS/SSO session was available in this executor session (see Checkpoint section below); all code wiring that depends on it is complete and tested against a fixed local JWK Set instead"

requirements-completed: [AUTH-02]

coverage:
  - id: D1
    description: "resourceIndicators is enabled with defaultResource/useGrantedResource/getResourceServerInfo returning a jwt/RS256 resource server for the voice resource"
    requirement: AUTH-02
    verification:
      - kind: unit
        ref: "src/config/__tests__/oidc-resource-token.test.ts > 'resourceIndicators is enabled with a jwt/RS256 resource server for the voice resource' (pass)"
        status: pass
    human_judgment: false
  - id: D2
    description: "A minted voice-resource access token is a three-segment RS256 JWT with a kid present in the provider's served JWKS, audienced to the pinned voice resource, and independently verifiable offline with the corresponding public key"
    requirement: AUTH-02
    verification:
      - kind: unit
        ref: "src/config/__tests__/oidc-resource-token.test.ts > 'mints a three-segment RS256 JWT with a kid present in the provider's JWKS...' (pass)"
        status: pass
    human_judgment: false
  - id: D3
    description: "extraTokenClaims emits EXACTLY the two namespaced tier_id/group claims (no others) for AccessToken-kind tokens, read from AuthProfile.activeTierId/activeGroup, with a no-access/null default when the profile is unset"
    requirement: AUTH-02
    verification:
      - kind: unit
        ref: "src/config/__tests__/oidc-resource-token.test.ts (both claim-bearing sub-tests, pass); full suite 33/33 pass"
        status: pass
    human_judgment: false
  - id: D4
    description: "configuration.jwks is sourced from OIDC_JWKS (persistent/shared, not auto-generated per process); from-aws.tmpl maps OIDC_JWKS to the SSM ARN and pins AUTH_PUBLIC_URL to the Phase-4 contract base"
    requirement: AUTH-02
    verification:
      - kind: unit
        ref: "src/config/__tests__/oidc-resource-token.test.ts (all 3 sub-tests run against a fixed OIDC_JWKS-injected JWK Set, proving the wiring); grep checks: accessTokenFormat/RS256/jwks/voiceResource present"
        status: pass
    human_judgment: false
  - id: D5
    description: "Manual staging: a real /token response (through the actual HTTP/Koa layer, not the direct AccessToken-construction test seam) decodes to an RS256 JWT with the pinned aud, a kid present in /jwks, and the two claims"
    verification: []
    human_judgment: true
    rationale: "Requires a running app instance + a real OIDC authorize->interaction->token HTTP round-trip (same class of gap as Plan 01/02's own D6 items) — not exercisable inside this sandboxed executor session. The unit suite exercises the EXACT SAME underlying mechanism the /token endpoint invokes (features.resourceIndicators.getResourceServerInfo + AccessToken construction + extraTokenClaims + JWT-format .save()), just without the Koa/HTTP wrapper, so confidence is high; only the wrapping HTTP layer itself is unverified live."
  - id: D6
    description: "JWKS SecureString created at SSM /kmv/secrets/use1/oidc/jwks via SOPS-encrypted secrets file, and site.hcl's oidc secret definition extended with the jwks key so terragrunt provisions the param"
    verification: []
    human_judgment: true
    rationale: "Gated behind a human-action checkpoint (see below) — no live AWS/SSO session was available in this executor session to run `sops encrypt` (KMS-backed) or `terragrunt apply`. All webapp-side code that depends on this secret is complete and tested against a fixed local substitute JWK Set."

duration: 6min
completed: 2026-07-05
status: complete
---

# Phase 3 Plan 03: Resource-Indicator JWT access tokens Summary

**oidc-provider's Resource Indicators are enabled end-to-end: the voice client's `/token` response is now a signed RS256 JWT audienced to `https://voice.klankermaker.ai`, carrying exactly `tier_id`/`group` claims read from `AuthProfile.activeTierId`/`activeGroup` — proven by a unit suite that mints a real token through the same `getResourceServerInfo`/`extraTokenClaims` code paths the live endpoint uses, verifies it offline against the provider's own served JWKS, and confirms the no-access default for accounts with no profile.**

## Performance

- **Duration:** 6 min (task-commit span, e1d3303→75cad7f)
- **Started:** 2026-07-05T13:52:03-04:00
- **Completed:** 2026-07-05T13:58:08-04:00
- **Tasks:** 3
- **Files modified:** 4 (1 new test file, 3 modified)

## Accomplishments

- `features.resourceIndicators` flipped from `{ enabled: false }` to a full config: `defaultResource` (resolves to the voice resource, honoring an explicit `oneOf`), `useGrantedResource` (always `true`), and `getResourceServerInfo` (`scope: "voice"`, the pinned audience, `accessTokenFormat: "jwt"`, `jwt.sign.alg: "RS256"` — asymmetric only, T-03-11)
- `extraTokenClaims` now reads `getAuthProfile(token.accountId)` and emits **exactly** `https://klankermaker.ai/tier_id` and `https://klankermaker.ai/group` for `AccessToken`-kind tokens only — no other custom claims, ID token/userinfo path (`findAccount.claims`) untouched
- `configuration.jwks` sourced from a new `config.oidc.jwks` getter that parses `OIDC_JWKS` as a JSON JWK Set — persistent/shared across the Fargate fleet (T-03-13/T-03-14), not oidc-provider's per-process dev-only auto-generated keys
- `config/index.ts` gained `oidc.voiceResource`, `oidc.voiceAudience` (both `https://voice.klankermaker.ai`), `oidc.claimNames.{tierId,group}`, and the `oidc.jwks` getter — the single source of truth Phase 4's contract is pinned against
- `from-aws.tmpl` gained `OIDC_JWKS` (SSM ARN `/kmv/secrets/use1/oidc/jwks`) and `AUTH_PUBLIC_URL=https://auth.klankermaker.ai/use1` (the pinned base, so issuer/routePrefix/jwks/redirect URIs agree — Pitfall 6 — for the production-mode local staging check this template feeds)
- New `oidc-resource-token.test.ts` mints a voice-resource access token by directly invoking the real `getResourceServerInfo` hook and constructing `oidc.AccessToken` (no HTTP/Koa layer, no DynamoDB write — JWT-format `AccessToken.save()` never calls `adapter.upsert`), then verifies it three ways: RI config shape, a full mint+verify+claims-content round trip against a fixed injected JWK Set, and the no-access/null default for an account with no `AuthProfile` row
- 33/33 vitest passing (30 prior + 3 net-new), `tsc --noEmit` clean (excluding the pre-existing, out-of-scope TS2578 from Plan 01), `next build` green

## Task Commits

Each task was committed atomically:

1. **Task 1: Wave-0 test — decode a minted access token, assert JWT/aud/claims/RS256 (RED)** - `e1d3303` (test)
2. **Task 2: Enable Resource Indicators + RS256 JWT access token + persistent JWKS + voice resource config** - `3e3b6c2` (feat)
3. **Task 3: extraTokenClaims emits tier_id + group from AuthProfile (GREEN)** - `75cad7f` (feat)

_No separate plan-metadata commit — STATE.md/ROADMAP.md are orchestrator-owned in worktree mode (per this plan's explicit instruction)._

## Phase-4 Contract — pinned verbatim (PyJWT in the voice service reads these byte-for-byte)

- **issuer**: `https://auth.klankermaker.ai/use1/api/oidc`
- **jwks_uri**: `https://auth.klankermaker.ai/use1/api/oidc/jwks`
- **audience (aud)**: `https://voice.klankermaker.ai`
- **signing alg**: `RS256` (asymmetric — never HS256)
- **scope gating the resource**: `voice`
- **claim names (namespaced)**: `https://klankermaker.ai/tier_id` (string; `"no-access"` when unset) and `https://klankermaker.ai/group` (string or `null`)
- **access-token TTL**: `3600` seconds (60 min)
- **AUTH_PUBLIC_URL** (container env) must be `https://auth.klankermaker.ai/use1`

Phase 4 should configure `PyJWKClient(issuer, ...)` and validate with `algorithms=["RS256"]`, `audience="https://voice.klankermaker.ai"`, `issuer="https://auth.klankermaker.ai/use1/api/oidc"`, then read `token["https://klankermaker.ai/tier_id"]` / `token["https://klankermaker.ai/group"]`.

## Files Created/Modified

- `apps/auth/webapp/src/config/__tests__/oidc-resource-token.test.ts` — new; mints and verifies a voice-resource access token via the real `getResourceServerInfo`/`extraTokenClaims` code paths, no HTTP layer
- `apps/auth/webapp/src/config/oidc.ts` — `configuration.jwks` from `config.oidc.jwks`; `features.resourceIndicators` enabled with `defaultResource`/`useGrantedResource`/`getResourceServerInfo`; `extraTokenClaims` emits the two namespaced claims for `AccessToken`-kind tokens
- `apps/auth/webapp/src/config/index.ts` — `oidc.voiceResource`/`voiceAudience`/`claimNames`/`jwks`; accessToken TTL comment records the pinned 3600s contract
- `apps/auth/webapp/from-aws.tmpl` — `OIDC_JWKS` → SSM ARN; `AUTH_PUBLIC_URL` → pinned base

## Decisions Made

See frontmatter `key-decisions`. Highlights:
- `voiceResource`/`voiceAudience` are the same URI — no distinction needed with a single first-party client.
- `configuration.jwks` falls back to `undefined` (oidc-provider's own dev-only quick-start keys) when `OIDC_JWKS` is unset, preserving local-dev ergonomics.
- The actual JWKS SecureString creation is gated behind a checkpoint (no live AWS session this session) — see below.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Test JWK Set needed an explicit fixed `kid` for offline verification to match**
- **Found during:** Task 2, first attempt to independently verify a minted token's signature with `jose.createLocalJWKSet`
- **Issue:** `jose.exportJWK()` on a freshly generated keypair produces a JWK with no `kid`. oidc-provider's `initialize_keystore.js` computes its own `kid` via a SHA-256 thumbprint (`calculateKid`) when none is present on the input JWK, so the token's header `kid` didn't match our locally-held public JWK (`kid: undefined`), and `jose.jwtVerify` threw `JWKSNoMatchingKey`.
- **Fix:** Set an explicit `kid` (`"test-kid-1"`) on both the private JWK (fed to `OIDC_JWKS`) and the public JWK (used for local verification) before injecting either — `initialize_keystore.js` honors an already-present `kid` via `key.kid ??= calculateKid(key)`, so this is a legitimate, supported input shape, not a workaround.
- **Files modified:** `apps/auth/webapp/src/config/__tests__/oidc-resource-token.test.ts`
- **Verification:** All 3 sub-tests GREEN afterward.
- **Committed in:** `3e3b6c2` (Task 2 commit)

**2. [Rule 3 - Blocking] Worktree needed fast-forwarding onto waves 1+2 before Plan 03's own base existed**
- **Found during:** Task setup, before Task 1
- **Issue:** This worktree was forked at `c44454b` (before waves 1/2 landed); `apps/auth/webapp` (Plans 01/02's entire deliverable, including `oidc.ts`/`index.ts`/`auth-profile.ts` this plan edits) did not exist in the working tree. `git rev-parse --show-toplevel` also initially resolved to the MAIN checkout path rather than this worktree's own path in several early Read calls (a session-setup mistake caught before any Edit/Write), which would have edited the wrong tree entirely.
- **Fix:** Verified `c44454b` is a strict ancestor of the orchestrator's stated `expected_base` (`f3220a90...`, linear history via `git merge-base`), ran `git merge --ff-only f3220a9086e152b19c51412416dfbc44452524d1` (pure fast-forward, no merge commit), confirmed `git rev-parse --abbrev-ref HEAD` stayed `worktree-agent-a11b62e36e683235a` throughout, and re-read every file via the correct worktree-absolute path before any Edit.
- **Files modified:** none beyond the fast-forward itself (brought in Plans 01/02's 63 files unchanged)
- **Verification:** `git rev-parse HEAD` == `f3220a9086e152b19c51412416dfbc44452524d1` before Task 1 began; branch remained `worktree-agent-a11b62e36e683235a` throughout; all subsequent Read/Edit calls used the worktree-absolute path and succeeded.
- **Committed in:** N/A (fast-forward merge, no new commit content)

**3. [Rule 3 - Blocking] `node_modules` not present after the worktree fast-forward**
- **Found during:** Task setup, before Task 1
- **Issue:** Fresh worktree checkout had no `node_modules` for `apps/auth/webapp`.
- **Fix:** Ran `npm install` (Node 23.6.0 via `nvm`, same accommodation Plans 01/02 made for vitest 4's engine requirement) — reproduced Plan 01's exact D-08 pins already locked in `package-lock.json`, no new dependency versions introduced.
- **Files modified:** none (local toolchain only, not committed)
- **Verification:** `npx vitest run` and `npx next build` both succeeded afterward.
- **Committed in:** N/A (environment setup, not a repo change)

---

**Total deviations:** 3 (1 test bug/Rule 1, 2 blocking/Rule 3 environment setup — all necessary for correctness or for the plan to be executable at all in this worktree)
**Impact on plan:** None affected the shipped implementation's behavior; #1 fixed the test itself (not the implementation under test), #2/#3 were required setup steps with no lasting repo changes beyond the fast-forward merge.

## Issues Encountered

- **Node engine version:** vitest 4.1.9 requires Node `^22.0.0 || ^22.13.0+ || >=24`; ambient Node was 22.1.0. Used the already-installed `nvm` Node 23.6.0 for all `npm`/`npx` invocations, matching Plans 01/02's precedent exactly.

## User Setup Required

**JWKS SecureString — gated behind the checkpoint below.** No new local-dev setup beyond what Plans 01/02 already established (`dynamodb-local` on port 8888).

## Known Gaps

- **`infra/terraform/live/site/site.hcl`'s `secrets.definitions.oidc.keys` does not yet include `"jwks"`** (currently just `["cookie_keys"]`). This is the terragrunt-side declaration that would cause the `secrets` module to provision `/kmv/secrets/use1/oidc/jwks` as an SSM SecureString once a value exists in the SOPS secrets file. This file is **not** in this plan's declared `files_modified` (`apps/auth/webapp/src/config/{oidc.ts,index.ts}` + `from-aws.tmpl` only), and — mirroring Plan 02's own precedent of deliberately not touching `service.hcl` for the DynamoDB TTL attribute — was left untouched here to avoid unplanned infra/terragrunt-apply scope creep in a parallel-executor worktree. **Recommend:** a small infra follow-up (this phase's wrap-up, or Phase 4) adds `keys = ["cookie_keys", "jwks"]` to the `oidc` definitions block and re-applies the `secrets` terragrunt unit, immediately followed by the JWKS generation/encryption steps in the checkpoint below.
- **D5 (live E2E through the real HTTP `/token` endpoint)** is unverified in this sandboxed session — same category of gap as Plan 01/02's own D6 items. The unit suite exercises the exact underlying mechanism (`getResourceServerInfo` + `extraTokenClaims` + JWT-format `AccessToken.save()`); only the Koa/HTTP wrapper itself is unverified live. Recommend a staging pass once the app is deployed with the real `OIDC_JWKS` secret: authorize the voice client, hit `/token`, decode the returned access token, and confirm `aud`/`kid`/claims match this SUMMARY's pinned contract.

## Threat Flags

None beyond the plan's own threat model (T-03-11 through T-03-16, all addressed as designed):
- T-03-11 (alg confusion): mitigated — `getResourceServerInfo` hardcodes `jwt.sign.alg: "RS256"`, no HS256 path exists.
- T-03-12 (audience confusion): mitigated — single voice client + `defaultResource` forces the voice audience.
- T-03-13 (divergent JWKS across the fleet): mitigated in code (`configuration.jwks` from a single `OIDC_JWKS` value) — the SSM-side provisioning that makes this a *shared* value across the fleet is the Known Gap above, not a code gap.
- T-03-14 (signing key leaked via repo/logs): mitigated by design — `OIDC_JWKS` is never hardcoded, only read from env; the actual SecureString/SOPS handling is the pending checkpoint below.
- T-03-15 (over-broad claims): mitigated — `extraTokenClaims` emits exactly the two namespaced claims, nothing else, verified by test (`customClaims` assertion).
- T-03-16 (open redirect): unaffected by this plan (client redirect_uris already registered in Plan 01, untouched here).

## Next Phase Readiness

- **Phase 4 (voice service)**: unblocked for the JWT-validation contract — see the pinned Phase-4 Contract block above for the exact `PyJWKClient`/`PyJWT` configuration. Blocked on the JWKS SecureString existing in SSM (checkpoint below) before a real deployed `/token` response can be validated end-to-end; all webapp-side code is complete and tested regardless.
- **This phase's infra wrap-up (or a follow-up)**: should add `"jwks"` to `site.hcl`'s `oidc` secret definition (Known Gaps above) alongside creating the actual secret value (checkpoint below).

---
*Phase: 03-auth-service-access-codes*
*Completed: 2026-07-05*

## Self-Check: PASSED

- Created files verified present: `apps/auth/webapp/src/config/__tests__/oidc-resource-token.test.ts`, this SUMMARY.md
- Modified files verified present with expected content: `apps/auth/webapp/src/config/oidc.ts` (resourceIndicators enabled, extraTokenClaims logic, configuration.jwks), `apps/auth/webapp/src/config/index.ts` (voiceResource/voiceAudience/claimNames/jwks), `apps/auth/webapp/from-aws.tmpl` (OIDC_JWKS, AUTH_PUBLIC_URL)
- Commits verified present in `git log --oneline`: `e1d3303` (RED), `3e3b6c2` (RI/jwks GREEN-partial), `75cad7f` (extraTokenClaims GREEN)
- `npx vitest run` (full suite): 33/33 pass; `npx tsc --noEmit`: clean (excluding the pre-existing, out-of-scope `confirm-no-consume.test.ts` TS2578 from Plan 01); `npx next build`: green (same 11 app routes + 3 pages routes as Plan 02)
