---
phase: 03-auth-service-access-codes
plan: 01
subsystem: auth
tags: [next-auth, oidc-provider, electrodb, dynamodb, altcha, ses, terragrunt, magic-link]

requires:
  - phase: 02-infra-skeleton plan 05
    provides: "SES identity auth.klankermaker.ai VERIFIED w/ DKIM; SSM SecureStrings /kmv/secrets/use1/{jwt/{secret,internal_secret},oidc/cookie_keys,altcha/secret}; DynamoDB unit applied with ZERO tables; ECR repo kmv-auth-app"
provides:
  - "apps/auth/webapp: trimmed, buildable port of run.auth (Next.js 16.1.6, next-auth 5.0.0-beta.30, oidc-provider 9.6.0, electrodb 3.5.3, @auth/dynamodb-adapter 2.11.1 — exact D-08 versions, verified via clean install)"
  - "Net-new /login/confirm interstitial page: magic-link email now points at a non-consuming confirm page instead of the direct callback URL (AUTH-01)"
  - "Two live DynamoDB tables in us-east-1: kmv-auth-authjs (nextauth schema) + kmv-auth-electro (electro schema)"
  - "/api/health route matching service.hcl's ALB health check"
  - "from-aws.tmpl rewritten to /kmv/secrets/use1/* + /kmv/ses/smtp/default/auth.klankermaker.ai/* ARNs"
affects: [03-02, 03-03, 03-04, phase-4-voice]

tech-stack:
  added: []
  patterns:
    - "Snapshot-copy-then-trim port strategy (rsync with excludes, then reviewable delete/edit diff on a known-good baseline)"
    - "Interstitial confirm-click page as a zero-JS async Server Component — safety property comes from absence of client code, not a guard check"
    - "vitest.config.ts @auth path alias + vi.mock('next-auth') to avoid next-auth's extensionless next/server ESM import breaking under Vitest's Node-ESM resolver"

key-files:
  created:
    - apps/auth/webapp/ (full Next.js app tree)
    - apps/auth/webapp/src/app/(authlogin)/login/confirm/page.tsx
    - apps/auth/webapp/src/app/api/health/route.ts
    - apps/auth/webapp/from-aws.tmpl
    - apps/auth/webapp/src/app/api/login/__tests__/login-altcha.test.ts
    - apps/auth/webapp/src/app/(authlogin)/login/confirm/__tests__/confirm-no-consume.test.ts
  modified:
    - infra/terraform/live/site/services/auth/service.hcl (dynamodb.tables: [] -> two tables)
    - apps/auth/webapp/src/config/auth.ts (Email-only provider, signupHTML -> /login/confirm)
    - apps/auth/webapp/src/config/oidc.ts (single voice client)
    - apps/auth/webapp/src/config/index.ts (siteDomain klankermaker.ai, single voice client)
    - apps/auth/webapp/src/entities/client.ts (dropped quota client/table)
    - apps/auth/webapp/src/entities/auth-profile.ts (dropped Discord/GitHub/Strava fields)

key-decisions:
  - "Ported at run.auth's exact working versions (D-08) — verified twice: first npm install silently resolved caret ranges to newer registry versions (next 16.2.10, next-auth beta.31, oidc-provider 9.8.6, electrodb 3.9.1, @auth/dynamodb-adapter 2.11.2), caught via version-diff check, fixed by pinning exact versions (no caret) for the five D-08-named packages, reinstalled clean"
  - "Confirm page implemented as a zero-client-JS async Server Component rendering a plain GET <form>; safety against link-scanners comes from the total absence of any auto-triggering code (no useEffect, no onLoad), not a runtime guard"
  - "Table names: kmv-auth-authjs (nextauth schema) + kmv-auth-electro (electro schema) — matches entities/client.ts fallback defaults and from-aws.tmpl DBNAME wiring"
  - "voice OIDC client_id/secret (OIDC_VOICE_CLIENT_ID/SECRET) are plain local-dev placeholder values in from-aws.tmpl — Phase 2 did not provision an SSM entry for them (only jwt/oidc-cookie/altcha secrets); a later plan wires the real values"
  - "Dropped unused dead code carried over from run.auth verbatim-copy: components/primitives.ts, components/text-effects/, components/PathnameBinder.tsx (zero remaining imports after the OAuth-provider/nav trim)"

requirements-completed: [AUTH-01, AUTH-05]

coverage:
  - id: D1
    description: "apps/auth/webapp builds cleanly at run.auth's exact pinned versions (next 16.1.6, next-auth 5.0.0-beta.30, oidc-provider 9.6.0, electrodb 3.5.3, @auth/dynamodb-adapter 2.11.1) with only the Email provider, one OIDC client (voice), and no quota/DEF CON code"
    requirement: AUTH-05
    verification:
      - kind: unit
        ref: "npm run build (Next.js 16.1.6, 0 errors, 11 routes incl. /login/confirm)"
        status: pass
      - kind: unit
        ref: "node -p version checks: next@16.1.6, next-auth@5.0.0-beta.30, oidc-provider@9.6.0, electrodb@3.5.3, @auth/dynamodb-adapter@2.11.1"
        status: pass
    human_judgment: false
  - id: D2
    description: "Altcha verify + replay guard: missing/invalid Altcha payload rejected 400/403; replayed payload rejected 403 on second submission"
    requirement: AUTH-05
    verification:
      - kind: unit
        ref: "src/app/api/login/__tests__/login-altcha.test.ts (3/3 pass)"
        status: pass
    human_judgment: false
  - id: D3
    description: "Interstitial /login/confirm page: a bare GET/prefetch with only token+email query params does not consume the token (no fetch to the nodemailer callback during render); explicit click is the only path to the callback"
    requirement: AUTH-01
    verification:
      - kind: unit
        ref: "src/app/(authlogin)/login/confirm/__tests__/confirm-no-consume.test.ts (1/1 pass)"
        status: pass
      - kind: manual_procedural
        ref: "npm run build && npm start; GET /use1/login/confirm?token=abc123&email=user@example.com -> HTTP 200, <form method=GET action=/use1/api/auth/callback/nodemailer>, hidden callbackUrl=https://voice.klankermaker.ai/"
        status: pass
    human_judgment: false
  - id: D4
    description: "Two physical DynamoDB tables (nextauth + electro schema) exist live in us-east-1"
    requirement: AUTH-01
    verification:
      - kind: integration
        ref: "terragrunt apply (dynamodb unit): 22 adds, 0 changed, 0 destroyed"
        status: pass
      - kind: integration
        ref: "aws dynamodb describe-table kmv-auth-authjs / kmv-auth-electro -> both TableStatus ACTIVE with expected GSIs"
        status: pass
    human_judgment: false
  - id: D5
    description: "The string 'voiceai' and the forbidden short token do not appear anywhere under apps/auth/webapp (source tree, excluding third-party node_modules)"
    verification:
      - kind: unit
        ref: "grep -rIiE 'voiceai|\\bkmk\\b' --exclude-dir=node_modules apps/auth/webapp -> no matches"
        status: pass
    human_judgment: false
  - id: D6
    description: "End-to-end live check: submitting email+Altcha at /login sends a real SES email; clicking the confirm-page link yields an authenticated sess_auth session"
    verification: []
    human_judgment: true
    rationale: "Requires a running dynamodb-local container (the plan's own user_setup) plus a live SES send/receive round-trip against a real inbox — not exercisable inside this sandboxed executor session. All the pieces this depends on are independently verified (D1-D5); this is the one item that needs a human to actually click through a real email."

metrics:
  duration: "~35 min (task commits 12:58-13:22 UTC-4; excludes upfront research/reading time)"
  started: "2026-07-05T16:58:37Z"
  completed: "2026-07-05T17:21:39Z"
  tasks: 3
  files: 47
status: complete
---

# Phase 3 Plan 01: Port run.auth webapp + interstitial confirm page + two DynamoDB tables Summary

**Ported and trimmed run.auth to `apps/auth/webapp` at its exact production versions (Next 16.1.6 / next-auth beta.30 / oidc-provider 9.6.0), replaced the token-consuming magic-link email link with a zero-JS interstitial `/login/confirm` page, and provisioned the two live DynamoDB tables (`kmv-auth-authjs`, `kmv-auth-electro`) the app needs.**

## Performance

- **Duration:** ~35 min (task-commit span); research/reading preceded this
- **Started:** 2026-07-05T16:58:37Z
- **Completed:** 2026-07-05T17:21:39Z
- **Tasks:** 3
- **Files modified:** 47 (excluding package-lock.json/node_modules)

## Accomplishments

- `apps/auth/webapp` builds green at run.auth's exact pinned dependency versions — single Email provider, single `voice` OIDC client, DEF CON quota/OAuth code fully removed
- Net-new `/login/confirm` interstitial (AUTH-01): the magic-link email now links to a page that itself performs zero network calls on render; only an explicit button click reaches `/api/auth/callback/nodemailer`
- Altcha verify + in-memory replay guard ported and unit-tested (AUTH-05): missing/invalid payload -> 400/403; replayed payload -> 403 on the second submission
- Two live DynamoDB tables provisioned via terragrunt: `kmv-auth-authjs` (nextauth schema, GSI1) and `kmv-auth-electro` (electro schema, gsi1/gsi2/gsi3) — both `ACTIVE` in us-east-1
- `/api/health` added, matching `service.hcl`'s ALB health-check path
- `from-aws.tmpl` rewritten to `/kmv/secrets/use1/{jwt,oidc,altcha}` + `/kmv/ses/smtp/default/auth.klankermaker.ai/*` ARNs, with dynamodb-local endpoints for local dev

## Task Commits

1. **Task 1: Wave-0 tests — Altcha verify/replay + confirm-page-no-consume (RED)** - `f8291f0` (test)
2. **Task 2: Snapshot-copy, trim to single-region Email-only voice-client app, provision two tables** - `bf99689` (feat)
3. **Task 3: Interstitial confirm-click page + point magic-link email at it (GREEN)** - `8114349` (feat)

_No separate plan-metadata commit was requested — STATE.md/ROADMAP.md are orchestrator-owned in worktree mode._

## Table Names & Client Shape (recorded per plan's `<output>`)

- **nextauth-schema table:** `kmv-auth-authjs` (GSI1PK/GSI1SK)
- **electro-schema table:** `kmv-auth-electro` (gsi1pk/sk, gsi2pk/sk, gsi3pk/sk)
- **voice OIDC client:** `client_id` = `OIDC_VOICE_CLIENT_ID` env; `redirect_uris` = `https://voice.klankermaker.ai/api/auth/callback/voice.klankermaker.ai` (+ `/use1` basePath variant + dev localhost variants); `grant_types` = `authorization_code`, `refresh_token`; `scope` = `openid profile email services`; `token_endpoint_auth_method` = `client_secret_post`

## Files Created/Modified

- `apps/auth/webapp/` — full ported Next.js app (see frontmatter `key-files` for the trim highlights)
- `apps/auth/webapp/src/app/(authlogin)/login/confirm/page.tsx` — net-new interstitial confirm page
- `apps/auth/webapp/src/app/api/health/route.ts` — net-new health check
- `apps/auth/webapp/from-aws.tmpl` — rewritten secrets/SES env mapping
- `infra/terraform/live/site/services/auth/service.hcl` — `dynamodb.tables` declares the two tables
- `apps/auth/webapp/src/app/api/login/__tests__/login-altcha.test.ts`, `.../login/confirm/__tests__/confirm-no-consume.test.ts` — Wave-0 tests
- `apps/auth/webapp/vitest.config.ts` — `@auth` alias added for the login route's `signIn` import

## Decisions Made

- **Exact-version pin caught and fixed mid-plan:** a plain `npm install` against `^`-ranged D-08 packages silently resolved to the CLAUDE.md "current" pins (next 16.2.10, next-auth beta.31, oidc-provider 9.8.6, electrodb 3.9.1, @auth/dynamodb-adapter 2.11.2) instead of run.auth's exact working versions. Caught by explicitly diffing installed versions against D-08; fixed by removing the caret from those five packages in `package.json` and reinstalling clean. This is exactly the D-08/D-09 boundary the plan warned about — the dependency bump is explicitly a **separate later task**.
- **Table names, voice client redirect URIs, and the local-dev voice client secret are Claude's discretion per CONTEXT.md** — recorded above for downstream plans.
- **Confirm page has zero client JavaScript** — its security property (not auto-consuming on GET/prefetch) is structural (no code exists that could fire on load), not a runtime check, which is the strongest form of this guarantee.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] npm install resolved caret-ranged D-08 packages to newer registry versions**
- **Found during:** Task 2, first `npm install` + `npm run build`
- **Issue:** `package.json` carried `^`-prefixed versions for `next`/`next-auth`/`oidc-provider`/`electrodb`/`@auth/dynamodb-adapter`; with no lockfile pinning these to run.auth's exact versions, npm resolved to the latest matching semver range (next 16.2.10, next-auth beta.31, oidc-provider 9.8.6, electrodb 3.9.1, @auth/dynamodb-adapter 2.11.2) — silently violating D-08.
- **Fix:** Removed the caret from those five entries (exact pins matching run.auth's `package.json`); deleted `node_modules`/`package-lock.json` and reinstalled; verified each resolved version via `node -p "require('./node_modules/<pkg>/package.json').version"`.
- **Files modified:** `apps/auth/webapp/package.json`, `apps/auth/webapp/package-lock.json`
- **Verification:** All five packages now resolve to run.auth's exact pinned versions; `npm run build` still green.
- **Committed in:** `bf99689` (Task 2 commit)

**2. [Rule 3 - Blocking] Vitest can't resolve next-auth's extensionless `next/server` import**
- **Found during:** Task 2, first `vitest run` of `login-altcha.test.ts`
- **Issue:** `next-auth`'s ESM build does `import ... from "next/server"` (no extension); `next`'s `package.json` has no `exports` map, so Next's own bundler (webpack/turbopack) resolves this via extension inference, but Vitest's stricter Node-ESM module loader for externalized `node_modules` deps does not, throwing `Cannot find package 'next/server'` (then cascading to `@auth/core`).
- **Fix:** Rather than fight Vite's SSR-external/inline machinery (tried and reverted `resolve.alias` + `server.deps.inline`, which cascaded through nested deps), mocked `next-auth` directly in the test (`vi.mock("next-auth", () => ({ AuthError: class extends Error {} }))`) since the route only needs the `AuthError` class for its `instanceof` check — the real `next-auth` package/its internals are never needed by this unit test.
- **Files modified:** `apps/auth/webapp/src/app/api/login/__tests__/login-altcha.test.ts`, `apps/auth/webapp/vitest.config.ts` (kept the `@auth` alias, needed to resolve the `signIn` import from `config/auth.ts`)
- **Verification:** `npx vitest run` — all 4 target tests + the ported `load-existing-grant` suite pass (8/8 total)
- **Committed in:** `bf99689` (Task 2), test fix; alias also present from `f8291f0` (Task 1)

**3. [Rule 1 - Bug] Task-1 scaffold's minimal `package.json`/`vitest.config.ts` needed reconciling once Task 2's full port landed**
- **Found during:** Task 2 start
- **Issue:** Task 1 created a placeholder `package.json` (vitest-only) to make the RED tests runnable before the wholesale copy existed; Task 2's `rsync` copy did not overwrite `package-lock.json` (excluded) or the already-present `vitest.config.ts`/`package.json` paths automatically in a way that reconciled cleanly.
- **Fix:** Task 2 explicitly overwrote `package.json` with the full (trimmed) dependency set and removed the stale placeholder lockfile before a fresh `npm install`.
- **Files modified:** `apps/auth/webapp/package.json`, `apps/auth/webapp/package-lock.json`
- **Verification:** `npm install` + `npm run build` green with the full dependency set.
- **Committed in:** `bf99689` (Task 2 commit)

**4. [Rule 1/2 - Cleanup] Dropped dead code with zero remaining imports after the OAuth/nav trim**
- **Found during:** Task 2, dependency-usage grep pass
- **Issue:** `components/primitives.ts`, `components/text-effects/`, and `components/PathnameBinder.tsx` were carried over by the wholesale copy but had no importers anywhere in the app even before the trim (`PathnameBinder` was never wired into `layout.tsx`; the other two were unused decorative leftovers).
- **Fix:** Deleted all three; confirmed `npm run build` unaffected.
- **Files modified:** deleted `apps/auth/webapp/src/components/{primitives.ts,text-effects/,PathnameBinder.tsx}`
- **Verification:** `grep -rln "components/primitives\|components/text-effects\|PathnameBinder" src` returns only the files' own definitions; build green.
- **Committed in:** `bf99689` (Task 2 commit)

**5. [Rule 2 - UX correctness] Login form's Access Code field made optional (was `required`)**
- **Found during:** Task 2, rebranding `login/page.tsx`
- **Issue:** run.auth's invite-code field was `required` in the HTML form, but CONTEXT.md D-07 (this project's design) explicitly states "any value or none accepted at each login" and "login always succeeds via magic link regardless of code" — the ported form's hard requirement contradicted the documented access-code semantics (access-code *resolution* itself is Plan 03-02's job; this only fixes the form-level validation gate that would otherwise block email-only sign-in).
- **Fix:** Removed the `required` attribute and the client-side "Enter the invite code" validation branch; relabeled the field "Access Code (optional)" with placeholder `demo` (matching the design spec's seed-code example) instead of the DEF CON placeholder `hacktheplanet`.
- **Files modified:** `apps/auth/webapp/src/app/(authlogin)/login/page.tsx`
- **Verification:** `npm run build` green; form still submits `inviteCode` (possibly empty string) to `/api/login`, which the ported route already tolerates (`AUTH_INVITE_CODES` unset -> no restriction).
- **Committed in:** `bf99689` (Task 2 commit)

---

**Total deviations:** 5 auto-fixed (2 blocking/Rule 3, 1 bug/Rule 1, 1 cleanup/Rule 1-2, 1 UX-correctness/Rule 2)
**Impact on plan:** All auto-fixes were necessary to reach a genuinely green build+test state at the D-08-mandated exact versions, or to remove dead/contradictory code the wholesale copy carried over. No scope creep — access-code *resolution* logic itself is untouched (correctly deferred to Plan 03-02).

## Issues Encountered

- **Terragrunt AWS auth:** SSO was live on `klanker-terraform`/`klanker-application` at the start of Task 2 (`aws sts get-caller-identity --profile klanker-terraform` succeeded) — no credential blocker encountered, so the terragrunt apply proceeded without a checkpoint.
- **Node engine mismatch for vitest 4.1.9/rolldown:** the ambient Node (22.1.0) didn't satisfy vitest/rolldown's native-binding requirement (`>=22.12.0`); switched to an available `nvm` Node 23.6.0 for all `npm`/`npx`/`vitest` invocations in this app. This is a local-executor environment note, not a repo change — no `.nvmrc` or engine pin was added since it's out of this plan's stated scope (D-08 governs the *app's* dependency pins, not the executor's toolchain).

## User Setup Required

None new beyond what the plan's own `user_setup` block already flags: a `dynamodb-local` container on port 8000 for local dev login testing (`AUTH_DYNAMODB_ENDPOINT`/`AUTH_ELECTRO_ENDPOINT`, already wired into `from-aws.tmpl`). Not spun up in this sandboxed executor session (see coverage `D6`).

## Known Stubs

- **`OIDC_VOICE_CLIENT_ID`/`OIDC_VOICE_SECRET`** in `from-aws.tmpl` are local-dev placeholder literals (`voice-local-dev` / `voice-local-dev-secret`), not SSM-backed — Phase 2 did not provision an SSM entry for the voice client credentials (only `jwt`/`oidc/cookie_keys`/`altcha`). A later plan (deploy-focused) must decide whether these are treated as secrets (new SSM path) or plain config (service.hcl container `environment`, non-secret since `client_secret_post` with a first-party single-page-app-adjacent client is a common non-critical-secret pattern for this project's scale) before the auth container is actually deployed.

## Threat Flags

None beyond the plan's own threat model — no new network endpoints, auth paths, or schema changes at trust boundaries were introduced outside what T-03-01 through T-03-SC already anticipated. `/api/health` is deliberately dependency-free (no DynamoDB/SES calls) so it carries no new attack surface.

## Next Phase Readiness

- **Plan 03-02 (access-codes/tiers):** unblocked — `apps/auth/webapp` builds green, the `electro` table exists live, and `entities/client.ts`/`entities/auth-profile.ts` are clean of DEF CON quota fields for the new `activeTierId`/`activeGroup` bridge to land on.
- **Plan 03-03 (Resource-Indicator JWT access tokens):** unblocked — `config/oidc.ts` has exactly one client (`voice`) and `resourceIndicators`/`extraTokenClaims` are left as explicit no-op stubs with comments pointing at this plan for the flip.
- **Plan 03-04 (`kv` CLI):** unblocked — the `electro` table (`kmv-auth-electro`) is live for `kv` to target once entities exist.
- **Manual live E2E** (submit email+Altcha -> real SES send -> click confirm page -> authenticated session) is the one item **not** exercised in this run (see coverage `D6`) — recommend a quick staging pass before considering AUTH-01/AUTH-05 fully verified end-to-end.

---
*Phase: 03-auth-service-access-codes*
*Completed: 2026-07-05*
