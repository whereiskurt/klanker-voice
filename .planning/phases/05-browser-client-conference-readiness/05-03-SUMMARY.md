---
phase: 05-browser-client-conference-readiness
plan: 03
subsystem: auth
tags: [oidc, pkce, react, vite, oidc-provider]

# Dependency graph
requires:
  - phase: 03-auth-service-access-codes
    provides: "OIDC issuer (auth.klankermaker.ai), RS256 JWT access-token contract (issuer/aud/scope/tier_id+group claims), 'voice' OIDC client registration"
  - phase: 04-voice-service-deployed-quota-enforcement
    provides: "apps/voice/src/klanker_voice/auth.py offline JWT validation the client's token must satisfy"
  - phase: 05-browser-client-conference-readiness (plan 02)
    provides: "apps/voice/client/ SPA scaffold, App.tsx onTapToTalk seam, OrbCanvas, design tokens"
provides:
  - "PKCE (RFC 7636) utilities + public OIDC client (buildAuthorizeUrl/exchangeCode) for the voice SPA"
  - "In-memory-only access token store (tokenStore.getToken()) — the Bearer source 05-04 sends to /api/offer"
  - "OIDC authorization-code+PKCE sign-in gate in front of the mic (CLNT-08): tap -> redirect -> callback -> token"
  - "No-access exclusive/invite-only gate (D-13 verbatim copy) for authenticated no-access-tier users"
  - "Corrected 'voice' OIDC client registration: public PKCE client (no secret), SPA /callback redirect_uri"
  - "Docker client-build stage now bakes public VITE_OIDC_* config into the production bundle"
affects: ["05-04 (live connect/mic wiring consumes tokenStore.getToken())", "05-05..07 (session lifecycle/reconnect UX)"]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "PKCE via built-in Web Crypto only (crypto.getRandomValues + crypto.subtle.digest) — no jwt/oidc npm dependency"
    - "Config objects passed as function parameters (not module-level singletons) in pure auth helpers, so they stay unit-testable without real env vars; only useAuth.ts calls the lazy env-backed getOidcConfig()"
    - "Plain pathname routing (no router lib) — relies on server.py's 404 SPA fallback (05-01) to make /callback a valid deep link"

key-files:
  created:
    - apps/voice/client/src/auth/pkce.ts
    - apps/voice/client/src/auth/pkce.test.ts
    - apps/voice/client/src/auth/oidcClient.ts
    - apps/voice/client/src/auth/oidcClient.test.ts
    - apps/voice/client/src/auth/tokenStore.ts
    - apps/voice/client/src/auth/useAuth.ts
    - apps/voice/client/src/config/oidc.ts
    - apps/voice/client/.env.example
    - apps/voice/client/src/screens/Callback.tsx
    - apps/voice/client/src/screens/callback.css
    - apps/voice/client/src/screens/NoAccessGate.tsx
    - apps/voice/client/src/screens/noAccessGate.css
    - .planning/phases/05-browser-client-conference-readiness/deferred-items.md
  modified:
    - apps/voice/client/src/App.tsx
    - apps/voice/client/src/vite-env.d.ts
    - apps/auth/webapp/src/config/oidc.ts
    - apps/voice/Dockerfile

key-decisions:
  - "Corrected the 'voice' OIDC client from a stale confidential-client shape (client_secret_post + Auth.js callback URIs, pre-dating the D-01/D-02 SPA pivot) to a public PKCE client (token_endpoint_auth_method: none) with the SPA's own /callback redirect_uri"
  - "Baked public VITE_OIDC_* values into the Dockerfile client-build stage as ARG defaults (no secret exists) so the deployed image doesn't depend on a CI --build-arg change"
  - "getOidcConfig() is lazy/memoized (not an eager module-level const) so pure helpers (buildAuthorizeUrl/exchangeCode) stay unit-testable without real env vars at import time"

requirements-completed: [CLNT-08]

coverage:
  - id: D1
    description: "PKCE utilities (generateCodeVerifier/generateState/codeChallenge) verified against the RFC 7636 Appendix B known-answer vector"
    requirement: "CLNT-08"
    verification:
      - kind: unit
        ref: "apps/voice/client/src/auth/pkce.test.ts"
        status: pass
    human_judgment: false
  - id: D2
    description: "buildAuthorizeUrl (S256 challenge, scope voice, resource/audience, random state) and exchangeCode (no client secret) against the issuer's authorize/token endpoints"
    requirement: "CLNT-08"
    verification:
      - kind: unit
        ref: "apps/voice/client/src/auth/oidcClient.test.ts"
        status: pass
    human_judgment: false
  - id: D3
    description: "In-memory token store + Callback route + useAuth wiring; no persistent (localStorage/cookie) token storage in src/auth"
    requirement: "CLNT-08"
    verification:
      - kind: unit
        ref: "grep -Rn localStorage|document.cookie apps/voice/client/src/auth (no match)"
        status: pass
      - kind: other
        ref: "cd apps/voice/client && npx tsc --noEmit && npm run build"
        status: pass
    human_judgment: false
  - id: D4
    description: "No-access exclusive/invite-only gate (D-13 verbatim heading + body), routed from App.tsx on tier_id === no-access"
    requirement: "CLNT-08"
    verification:
      - kind: other
        ref: "grep -q \"You're on the list\" / \"Kurt needs to give you access\" apps/voice/client/src/screens/NoAccessGate.tsx; tsc --noEmit; npm run build"
        status: pass
    human_judgment: false
  - id: D5
    description: "Corrected 'voice' OIDC client to a public PKCE client with the SPA /callback redirect_uri; verified no regression in the auth webapp's existing OIDC/grant test suite"
    verification:
      - kind: unit
        ref: "apps/auth/webapp: npx vitest run (9 files, 33/33 pass)"
        status: pass
      - kind: other
        ref: "apps/auth/webapp: npx tsc --noEmit (1 pre-existing unrelated failure, logged to deferred-items.md, not caused by this change)"
        status: pass
    human_judgment: false
  - id: D6
    description: "Dockerfile client-build stage bakes public VITE_OIDC_* into the production bundle"
    verification:
      - kind: other
        ref: "local vite build with VITE_OIDC_* as process.env (matching Docker ENV) — confirmed values inlined into the built JS bundle"
        status: pass
    human_judgment: false
  - id: D7
    description: "Live PKCE sign-in round-trip against deployed auth.klankermaker.ai + deployed voice client, in-memory-only token in devtools, no-access gate with a real no-access account, refresh re-auth"
    verification: []
    human_judgment: true
    rationale: "Requires a deployed voice client + the redeployed auth service (this plan's OIDC client correction) — cannot be exercised locally. Per orchestrator guidance, deferred to the orchestrator's post-05-04 deployed-AWS validation pass rather than self-approved here."

duration: 45min
completed: 2026-07-06
status: complete
---

# Phase 5 Plan 03: OIDC PKCE Sign-In Gate + No-Access Gate Summary

**Authorization-code+PKCE sign-in gate in front of the mic (in-memory-only token, RFC 7636-verified) plus the D-13 exclusive no-access gate — all code/unit-testable work complete; the live deployed sign-in round-trip is deferred to the orchestrator per explicit instruction.**

## Performance

- **Duration:** ~45 min
- **Tasks:** 3 of 3 code tasks complete + 2 required deviations; the plan's 4th task (a live-verify checkpoint) is deferred, not self-approved
- **Files modified:** 17 (13 created, 4 modified)

## Accomplishments

- PKCE (RFC 7636) utilities built entirely on the Web Crypto API (no jwt/oidc npm dependency), the S256 challenge verified against the RFC's own Appendix B known-answer vector
- `buildAuthorizeUrl`/`exchangeCode` implement the authorization-code+PKCE round-trip against the Phase-3 issuer: response_type=code, S256 challenge, scope `voice`, resource/audience pinned to `https://voice.klankermaker.ai` (matches auth.py's validation exactly), and token exchange sends NO client secret
- In-memory-only token store (`tokenStore.ts`) — no persistent browser storage anywhere in `src/auth` (grep-verified); a page refresh drops the token and the next tap re-auths
- `Callback.tsx` validates the redirect's `state` against the value stashed before the redirect, exchanges the code, and routes onward with a "Signing you in…" status (Copywriting Contract copy)
- `NoAccessGate.tsx` renders the D-13 heading/body verbatim over the still-alive orb stage, with a "How to get a code" expander (the panel's single accent CTA) and a plain "Sign out"
- **Found and corrected a real blocker**: the pre-existing `voice` OIDC client registration (`apps/auth/webapp/src/config/oidc.ts`) was shaped for a stale, pre-D-01/D-02 design (a hypothetical Next.js/next-auth confidential relying party) — it would have rejected this plan's actual SPA redirect_uri and required a client secret the public PKCE client doesn't have. Corrected to a public client with the SPA's own `/callback` redirect_uri; the full auth webapp test suite still passes 33/33.
- **Found and fixed a second real blocker**: `build-voice.yml` builds the Docker image with no `--build-arg`s, so the production bundle would have shipped with `VITE_OIDC_*` all `undefined` and sign-in would fail immediately. Baked the public (non-secret) values in as Dockerfile ARG defaults.

## Task Commits

Each task was committed atomically:

1. **Task 1: PKCE + OIDC client utilities** — `6815748` (feat)
2. **Task 2: in-memory token store, callback route, useAuth** — `2c9a3a3` (feat)
3. **Task 3: no-access exclusive/invite-only gate** — `e41b2eb` (feat)
4. **Deviation: correct the 'voice' OIDC client to a public PKCE client** — `b6103c6` (fix)
5. **Deviation: bake public VITE_OIDC_* into the Docker client-build stage** — `6f2f507` (fix)

**Plan metadata:** (this commit, docs: complete plan)

## Files Created/Modified

- `apps/voice/client/src/auth/pkce.ts` — RFC 7636 PKCE utilities (verifier/state/S256 challenge)
- `apps/voice/client/src/auth/pkce.test.ts` — RFC 7636 Appendix B known-answer vector test
- `apps/voice/client/src/auth/oidcClient.ts` — buildAuthorizeUrl / exchangeCode
- `apps/voice/client/src/auth/oidcClient.test.ts` — authorize URL shape + token exchange (no secret) tests
- `apps/voice/client/src/auth/tokenStore.ts` — module-scope in-memory token + tier/group claim decode
- `apps/voice/client/src/auth/useAuth.ts` — beginSignIn/signOut/refresh + sessionStorage PKCE stash
- `apps/voice/client/src/config/oidc.ts` — public OIDC config, lazy `getOidcConfig()`
- `apps/voice/client/.env.example` — public VITE_OIDC_* contract, documents no secret exists
- `apps/voice/client/src/screens/Callback.tsx` + `callback.css` — the /callback route
- `apps/voice/client/src/screens/NoAccessGate.tsx` + `noAccessGate.css` — D-13 gate
- `apps/voice/client/src/App.tsx` — onTapToTalk -> beginSignIn; /callback + no-access routing
- `apps/voice/client/src/vite-env.d.ts` — typed `ImportMetaEnv` for VITE_OIDC_*
- `apps/auth/webapp/src/config/oidc.ts` — 'voice' client corrected to public PKCE client
- `apps/voice/Dockerfile` — client-build stage ARGs for public OIDC config
- `.planning/phases/05-browser-client-conference-readiness/deferred-items.md` — pre-existing unrelated tsc finding, logged not fixed

## Decisions Made

- Public OIDC config is read via a lazy, memoized `getOidcConfig()` rather than an eager module-level const, so `oidcClient.ts`'s pure functions (which take an `OidcConfig` parameter) stay importable/unit-testable without real `VITE_OIDC_*` env vars set.
- Treated the 'voice' OIDC client correction and the Dockerfile ARG fix as in-scope auto-fixes (Rule 2/3) rather than checkpoint-worthy architectural changes, per the orchestrator's explicit pre-authorization for OIDC-client-registration edits in this plan's `user_setup` block. Both are called out below and flagged for the orchestrator's deploy plan.
- No-access gate's "How to get a code" is the panel's single reserved-accent CTA; "Sign out" is a plain secondary action — matches the UI-SPEC's "single primary CTA per panel" rule and D-04's "single tap, no modal" sign-out contract.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Critical] Corrected the 'voice' OIDC client to a public PKCE client**
- **Found during:** Task 1 (reading the Phase-3 `voice` client registration while wiring buildAuthorizeUrl/exchangeCode)
- **Issue:** `apps/auth/webapp/src/config/oidc.ts` registered `voice` with `token_endpoint_auth_method: "client_secret_post"` and Auth.js-style redirect URIs (`.../api/auth/callback/voice.{domain}`) — a confidential-client shape left over from before D-01/D-02 pivoted the voice app to a bespoke static SPA. This plan's actual redirect_uri (`/callback`) wasn't registered, and the SPA has no secret to present at the token endpoint — the sign-in round-trip would fail closed.
- **Fix:** `token_endpoint_auth_method: "none"` (public client); `redirect_uris` -> the SPA's own `/callback` (+ `localhost:5173/callback` for dev); `grant_types` trimmed to `authorization_code` only (D-04: re-auth on refresh, not OAuth refresh tokens). `pkce: { required: () => true }` was already global, so PKCE enforcement itself was already correct.
- **Files modified:** `apps/auth/webapp/src/config/oidc.ts`
- **Verification:** Full auth webapp test suite green (9 files, 33/33); confirmed no test exercises `redirect_uris`/`token_endpoint_auth_method` directly (only `oidc-resource-token.test.ts`, which mints tokens via the internal `AccessToken` model directly, bypassing client metadata entirely).
- **Committed in:** `b6103c6`
- **Deploy implication:** `apps/auth` (build-auth.yml) MUST be redeployed alongside the voice service for sign-in to work against the deployed `auth.klankermaker.ai` — the live client registration is currently the pre-Phase-5 confidential-client shape.

**2. [Rule 3 - Blocking] Baked public VITE_OIDC_* config into the Docker client-build stage**
- **Found during:** Task 1 (checking how the Dockerfile's `RUN npm run build` would actually get the public OIDC config)
- **Issue:** `build-voice.yml` runs `docker build -t "$IMAGE" apps/voice` with zero `--build-arg`s, and the Dockerfile had no `ARG`/`ENV` for `VITE_OIDC_*`. Vite inlines `import.meta.env.VITE_*` at build time from `process.env` — without this, every value would be `undefined` in the production bundle, and `getOidcConfig()` throws on the first real page load. Sign-in would be completely broken in the deployed image with no CI change.
- **Fix:** Added `ARG`/`ENV` for all four `VITE_OIDC_*` vars in the Dockerfile's `client-build` stage, defaulted to the real production values (all public — no secret exists for this PKCE client), overridable via `--build-arg` for a non-production target.
- **Files modified:** `apps/voice/Dockerfile`
- **Verification:** Ran `vite build` locally with the same vars set as `process.env` (matching how Docker `ENV` works, not a `.env` file) and confirmed the values are inlined into the built JS bundle.
- **Committed in:** `6f2f507`
- **Deploy implication:** None beyond the normal voice image rebuild — no CI/build-voice.yml change needed since the values default inside the Dockerfile itself.

---

**Total deviations:** 2 auto-fixed (1 missing-critical OIDC client correction, 1 blocking Docker build-arg fix)
**Impact on plan:** Both were required for the plan's own stated deliverable (a working PKCE sign-in round-trip) to function at all once deployed — no unrelated scope creep. Both are flagged above as **Deploy implications** per the orchestrator's explicit instruction.

## Issues Encountered

- Local vitest runs failed on Node v22.1.0 (this shell's default) with an unrelated pre-existing `@exodus/bytes` ESM/CJS interop error inside jsdom's `html-encoding-sniffer` dependency — confirmed this also breaks the previously-passing `orbState.test.ts` (05-02), so it is an environment issue, not caused by this plan. Matches 05-02-SUMMARY.md's existing note ("Local build needs node>=22.12"). Worked around locally via `nvm use v23.6.0` (the same version 05-02 used) for all verification in this plan; no code change needed.
- Found one pre-existing, unrelated `tsc --noEmit` failure in the auth webapp (`confirm-no-consume.test.ts:19`, an unused `@ts-expect-error`) while verifying the OIDC client correction didn't regress anything. Confirmed via `git log` that this file predates this plan's session and was not touched by it. Logged to `deferred-items.md` per the scope-boundary rule rather than fixed.

## User Setup Required

**External services require manual configuration** — see this plan's `user_setup` frontmatter (already partially resolved as code, not a dashboard, since `apps/auth/webapp/src/config/oidc.ts` is config-as-code):

- The 'voice' OIDC client's redirect URI/auth method correction is now code-complete (`b6103c6`) but requires **redeploying `apps/auth`** to take effect against `https://auth.klankermaker.ai`.
- `VITE_OIDC_ISSUER` / `VITE_OIDC_CLIENT_ID` / `VITE_OIDC_AUDIENCE` / `VITE_OIDC_REDIRECT_URI` are now baked into the Docker image by default (`6f2f507`) — no manual env configuration needed for the standard production deploy.

## Next Phase Readiness

- `tokenStore.getToken()` is ready as the single Bearer source for 05-04's `/api/offer` wiring.
- **Blocking for full verification:** this plan's checkpoint (live PKCE sign-in round-trip + no-access gate against the deployed stack) is NOT yet exercised — see `## CHECKPOINT REACHED` in the executor's return message. Per orchestrator guidance, this is intentionally deferred and folded into the post-05-04 deployed-AWS validation pass, not self-approved here.
- Both auth-service and voice-image deploys must land together for the corrected OIDC client to actually work end-to-end on `voice.klankermaker.ai`.

---
*Phase: 5-Browser Client & Conference Readiness*
*Completed: 2026-07-06*

## Self-Check: PASSED

All 18 files listed above verified present on disk; all 5 task/deviation commit hashes
(`6815748`, `2c9a3a3`, `e41b2eb`, `b6103c6`, `6f2f507`) verified present in `git log --oneline --all`.
