# Phase 3: Auth Service & Access Codes - Research

**Researched:** 2026-07-05
**Domain:** Next.js 16 (App + Pages Router hybrid) identity service — next-auth v5 magic-link, embedded `oidc-provider` 9.6.0 issuer, ElectroDB single-table on DynamoDB, Altcha; plus a Go `kv` CLI and terragrunt DynamoDB wiring
**Confidence:** HIGH (port is grounded in a running, production-proven source tree at `defcon.run.34/apps/run.auth/webapp`; every claim below cites a real file. The one genuinely new mechanism — Resource-Indicator JWT access tokens on 9.6.0 — is grounded against the installed library source and type defs.)

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- **D-01:** Token carries **tier_id + group claims ONLY**. The voice service reads the `tiers` table for actual limits at session start. Thin token; `tiers` is the single source of truth; editing tier numbers does not require re-issuing tokens.
- **D-02:** Token type is a **JWT access token via oidc-provider Resource Indicators**, audienced to the voice resource (e.g. `aud=voice.klankermaker.ai`). PyJWT + PyJWKClient validates issuer + audience offline via JWKS. run.auth currently has `resourceIndicators: { enabled: false }` and puts claims in the ID token (`conformIdTokenClaims: false`); this phase ENABLES Resource Indicators and moves tier/group onto the access token.
- **D-03:** Access-token **TTL comfortably exceeds the longest tier (30 min) + a reconnect window** (~45–60 min target). Token gates session *establishment* only. No short-TTL/refresh plumbing in the browser client.
- **D-04:** `access_codes` is a **new entity in run.auth's existing ElectroDB single-table design**; one table added to the Phase-2 dynamodb unit.
- **D-05:** Redemption binding is **per-login, latest-wins**: the code entered at a given login sets that session's tier; re-entering changes tier; there is no permanent per-user tier stamp.
- **D-06:** A code's `max_redemptions` counts **unique users**, not total login events. Needs a redeemed-by marker.
- **D-07:** Code carries: tier id, group, expiry date, max_redemptions, redemption_count. Expiry + max-redemption enforced at **login-time code resolution**. Unknown/blank code → `no-access` tier. Login always succeeds via magic link regardless of code.
- **D-08:** Port at run.auth's **exact working versions** (next-auth 5.0.0-beta.30, oidc-provider 9.6.0, electrodb 3.5.3, next 16.1.6, @auth/dynamodb-adapter 2.11.1, altcha 2.3.0 / altcha-lib 1.4.1, nodemailer 7, react 19.2.4). Bump to CLAUDE.md pins as a **separate later task**.
- **D-09:** **Copy wholesale, then trim.** Copy the working webapp verbatim, get it green, then delete DEF CON specifics as a reviewable diff.
- **D-10:** Ported app lives at **`apps/auth/webapp/`**. Matches ECR repo `kmv-auth-app`.
- **D-11:** Phase 3 builds **only `tiers` + `access_codes`**. Drop run.auth's DEF CON quota code (`user-quota.ts` / `quota.ts` / `services/quota.ts` / `lib/quota-definitions.ts`); Phase 4 rebuilds against the design-spec quota schema.

### Claude's Discretion
Claim namespace/naming convention (default: namespace the custom tier/group claims), issuer/audience exact URIs, seed-code values beyond the spec examples (demo→2min, kphdemo123→30min), code case-sensitivity/format, the interstitial confirm-click page design, Altcha server-key wiring from `/kmv/secrets/use1/altcha/secret`, secrets env mapping (run.auth `from-aws.tmpl` → SSM `/kmv/secrets/use1/*`), kv command surface shape, ElectroDB access-pattern/index design, snapshot-copy vs git-history for the port.

### Deferred Ideas (OUT OF SCOPE)
- Dependency bump to CLAUDE.md pins (next-auth beta.31 / oidc-provider 9.8.6 / electrodb 3.9.1) — separate task AFTER port is green.
- All quota enforcement — usage table, ticking, blocking, concurrency, kill-switch, spoken wind-down (Phase 4; QUOT-01..05, KV-03/04/05).
- run.auth's DEF CON quota code — dropped.
- Live session inspection (`kv sessions`, KV-06) — deferred.
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| AUTH-01 | Magic-link email (SES) with an interstitial confirm-click page so corporate link-scanners don't consume tokens | run.auth's flow is portable: `src/config/auth.ts` (nodemailer→SES `sendVerificationRequest`, `signupHTML`), `src/app/(authlogin)/login/verify/page.tsx` (OTP-code entry page). **Gap:** the email currently contains a *direct-consumption* GET link; a true interstitial confirm page is net-new — see Pitfall 4. |
| AUTH-02 | JWT access tokens with tier/group claims validated offline via JWKS (oidc-provider Resource Indicators) | Resource-Indicator config change grounded below (installed `oidc-provider@9.6.0` source). Claims attach via `extraTokenClaims`, NOT `findAccount.claims`. JWKS already exposed at `${routePrefix}/jwks` in `src/config/oidc.ts`. |
| AUTH-03 | Any access code (or none) at login; known→tier, unknown/blank→no-access with guidance | Login form already has an Invite Code field (`src/app/(authlogin)/login/page.tsx`) and `/api/login` validates it (`src/app/api/login/route.ts`). Replace static `AUTH_INVITE_CODES` check with `access_codes`-table resolution → tier. |
| AUTH-04 | Operator-defined codes carry expiry and max-redemption limits | `access_codes` + `code_redemptions` entity sketches below; enforced at login-time resolution. |
| AUTH-05 | Altcha captcha on login form | Fully portable: `src/app/(authlogin)/login/page.tsx` (widget), `src/app/api/captcha/challenge/route.ts` (`createChallenge`), `src/app/api/login/route.ts` (`verifySolution` + replay guard). Server key from `/kmv/secrets/use1/altcha/secret` → `ALTCHA_HMAC_KEY`. |
| KV-01 | `kv` create/list/expire access codes with tier mapping | Go cobra CLI (sibling to `km`), aws-sdk-go-v2 DynamoDB writes to the electro table. Key-format compatibility is the top risk — see Don't Hand-Roll + Pitfall 1. |
| KV-02 | `kv` define and list tiers | Same CLI; `tiers` entity. Seed demo→2min, kphdemo123-tier→30min, no-access. |
</phase_requirements>

## Summary

Phase 3 is a **port, not a build**. `defcon.run.34/apps/run.auth/webapp` is a running Next.js 16 identity service with exactly the machinery this phase needs: next-auth v5 magic-link over nodemailer→SES, an embedded `oidc-provider` issuer with a custom ElectroDB DynamoDB adapter, an Altcha-gated login form that **already has an invite-code field**, and an interstitial OTP-verify page. The correct strategy (D-08/D-09) is to copy it verbatim into `apps/auth/webapp/`, get it green against klanker-voice's SSM/DynamoDB wiring, then trim DEF CON specifics (Discord/Strava/GitHub providers, four of the five OIDC clients, leaflet/gpx/strapi/pdf/qr deps, and all quota code) as a reviewable diff.

Three things are genuinely new and carry the phase's risk. **(1) Resource-Indicator JWT access tokens.** run.auth issues *opaque* access tokens and stuffs service claims into the *ID token* via `findAccount().claims()` + `conformIdTokenClaims:false`. This phase must flip `features.resourceIndicators.enabled:true`, supply `getResourceServerInfo`/`defaultResource`/`useGrantedResource` helpers that emit a **JWT** access token audienced to the voice resource, and inject `tier_id`+`group` via **`extraTokenClaims`** (the access-token hook) — not the ID-token claims callback. **(2) The login→token tier hand-off.** The access code is entered at `/api/login` (before the user exists), but the access token is minted much later during the OIDC interaction. The resolved tier must be bridged across those two requests; the recommended mechanism is a short-lived `login_intent` record keyed by email plus an `activeTierId`/`activeGroup` field stamped onto `AuthProfile`, which `extraTokenClaims` reads by `sub`. **(3) Go↔ElectroDB key-format compatibility.** `kv` writes DynamoDB items that the ElectroDB-reading webapp must be able to load; ElectroDB's composed `$service#attr_value` key format (and its casing rules) must be matched exactly, or use explicit ElectroDB index `template`s to make the keys trivially reproducible from Go.

Infra is nearly ready: Phase 2 applied the dynamodb terragrunt unit with `tables = []`; Phase 3 adds the auth tables by editing `infra/terraform/live/site/services/auth/service.hcl` only. Two physical tables are required — a `nextauth`-schema table for `@auth/dynamodb-adapter`, and an `electro`-schema single table for all ElectroDB entities (oidc-adapter, auth-profile, and the new tiers + access_codes). The dynamodb module already ships both `nextauth` and `electro` predefined schemas.

**Primary recommendation:** Snapshot-copy `run.auth/webapp` → `apps/auth/webapp/`, get it green with a trimmed config (single region, one OIDC client `voice`, email-only provider), then layer the three new mechanisms in this order: Resource-Indicator JWT access tokens (extraTokenClaims) → access_codes/tiers/login_intent entities + login-time resolution → `kv` CLI writing ElectroDB-compatible items (via explicit templates).

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Magic-link email delivery | API / Backend (Next route + nodemailer→SES) | CDN/Static (email HTML) | Server owns SES credentials and token generation |
| Altcha challenge + verify | API / Backend | Browser/Client (widget PoW) | HMAC key is server-side; PoW solved client-side, verified server-side |
| Access-code → tier resolution | API / Backend (`/api/login`) | Database (`access_codes`/`code_redemptions`) | Expiry/redemption limits are authoritative server data |
| OIDC issuance (authorize/token/jwks) | API / Backend (`oidc-provider`) | Database (ElectroDB oidc-adapter) | Issuer state (grants, codes, tokens) persists in DynamoDB |
| JWT access-token claim injection | API / Backend (`extraTokenClaims`) | — | Claims must be signed by the issuer, never client-set |
| Offline token validation | **Consumer = voice service (Phase 4)** | — | PyJWT reads JWKS; out of scope here except the claim CONTRACT |
| Access-code / tier CRUD (operator) | CLI (`kv`, Go) → Database | — | Operator tool writes raw DynamoDB items matching ElectroDB keys |
| Table provisioning | Infra (terragrunt dynamodb unit) | — | `service.hcl` declares tables; module renders them |

## Standard Stack

This phase introduces **no new npm packages** — every dependency is copied at its exact pinned version from run.auth's production `package.json`/lockfile (D-08). The `kv` CLI reuses the toolchain already proven in `km`.

### Core (ported verbatim from `run.auth/webapp/package.json`)
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| next | 16.1.6 | App framework (App + Pages Router hybrid) | run.auth's exact version; OIDC routes rely on Pages Router `bodyParser:false` |
| next-auth | 5.0.0-beta.30 | Magic-link email auth (App Router) | Production-proven at DEF CON; pin the exact beta |
| oidc-provider | 9.6.0 | Embedded OIDC issuer | Installed & running; Resource Indicators available (verified against lib source) |
| electrodb | 3.5.3 | DynamoDB single-table modeling | Existing entities use it; new entities join the same table |
| @auth/dynamodb-adapter | 2.11.1 | next-auth persistence (users/sessions/verification tokens) | Requires its OWN table (`nextauth` schema) |
| @aws-sdk/client-dynamodb + lib-dynamodb | ^3.893.0 | DynamoDB access | Already used by client.ts |
| @aws-sdk/client-sesv2 | ^3.893.0 | Magic-link delivery via SES | Used by auth.ts transport |
| nodemailer | ^7.0.13 | Email transport (wraps SES) | next-auth Email provider backend |
| altcha + altcha-lib | 2.3.0 / 1.4.1 | Login captcha (PoW) | Widget + `createChallenge`/`verifySolution` |
| react / react-dom | 19.2.4 | UI | Matches next 16 |
| @heroui/react | ^2.8.8 | Login/verify UI components | Used by login pages (trim unused) |

### Supporting (`kv` CLI, mirrors `km`)
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| Go | 1.26.x (go.mod `go 1.26`) | Toolchain | CLAUDE.md pin; `km` is on 1.25.5 — `kv` can lead |
| spf13/cobra | v1.10.2 | Command tree | `km` uses cobra; keep the two CLIs structurally identical |
| aws-sdk-go-v2 (+ service/dynamodb, feature/dynamodb/attributevalue) | v1.42.x | DynamoDB item CRUD | Already in `km` go.mod (`service/dynamodb v1.57.0`, `feature/dynamodb/attributevalue v1.20.36`) |
| charmbracelet/lipgloss | v1.1.0 (optional) | Pretty `kv list` output | Nice-to-have; stay on v1 |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| ElectroDB entities for tiers/access_codes | Raw DynamoDB items (no ElectroDB) | ElectroDB gives the webapp typed reads; but then `kv` must match its key format — see Pitfall 1. Explicit index `template`s split the difference. |
| `extraTokenClaims` for tier/group | ID-token claims (run.auth's current approach) | ID token is not what the voice service validates at `/api/offer`; D-02 requires the ACCESS token to carry claims |
| `login_intent`-by-email bridge | Encode tier in the magic-link token | The nodemailer provider controls token generation; hijacking it is fragile |
| `kv` writes DynamoDB directly | `kv` calls a webapp admin API | Design commits to direct DynamoDB via aws-sdk-go-v2; an API adds a deploy dependency |

**Installation:** No `npm install` of new packages — `apps/auth/webapp/package.json` is copied from run.auth. For `kv`: `go mod init` + `go get github.com/spf13/cobra@v1.10.2 github.com/aws/aws-sdk-go-v2/service/dynamodb github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue`.

**Version verification:** Installed `oidc-provider` confirmed at **9.6.0** (`node_modules/oidc-provider/package.json`). All other versions read from the live `run.auth/webapp/package.json` (last modified 2026-07-04). No registry lookup was needed because these are copied from a working production lockfile, not discovered.

## Package Legitimacy Audit

Every npm dependency is transitively present in run.auth's committed, production-deployed `package-lock.json` (527 KB, dated 2026-07-04) and is installed and running today. No package here was discovered via WebSearch or training data; the provenance is a running lockfile, so the slopsquat vector does not apply. Go dependencies (`cobra`, `aws-sdk-go-v2`) are already in `klankrmkr/go.mod`.

| Package | Registry | Provenance | Verdict | Disposition |
|---------|----------|-----------|---------|-------------|
| next 16.1.6 | npm | run.auth lockfile (installed) | OK | Approved (ported) |
| next-auth 5.0.0-beta.30 | npm | run.auth lockfile (installed) | OK (beta — pin exact) | Approved (ported) |
| oidc-provider 9.6.0 | npm | installed, version-confirmed on disk | OK | Approved (ported) |
| electrodb 3.5.3 | npm | run.auth lockfile (installed) | OK | Approved (ported) |
| @auth/dynamodb-adapter 2.11.1 | npm | run.auth lockfile (installed) | OK | Approved (ported) |
| altcha 2.3.0 / altcha-lib 1.4.1 | npm | run.auth lockfile (installed) | OK | Approved (ported) |
| spf13/cobra v1.10.2 | Go proxy | in km go.mod (v1.x) | OK | Approved |
| aws-sdk-go-v2 (dynamodb, attributevalue) | Go proxy | in km go.mod (installed) | OK | Approved |

**Packages removed due to [SLOP] verdict:** none.
**Packages flagged as suspicious [SUS]:** none.
**Note for planner:** the legitimacy gate is satisfied by the wholesale-copy-from-production strategy (D-08/D-09). The planner does NOT need `checkpoint:human-verify` install gates for these. The one thing to verify is that the DEF CON *trim* removes packages without breaking the build (see Port Map) — that is a build-green check, not a legitimacy check.

## Architecture Patterns

### System Architecture Diagram

```
                          BROWSER (Phase 5 client — not built here)
                             │  1. OIDC authorize (code+PKCE, resource=voice)
                             ▼
┌────────────────────────────────────────────────────────────────────────────┐
│  auth.klankermaker.ai  (Next.js 16, apps/auth/webapp)                        │
│                                                                              │
│   /login (App Router)                 /api/oidc/* (Pages Router)             │
│   ┌──────────────────────┐            ┌─────────────────────────────────┐   │
│   │ email + ACCESS CODE  │            │ oidc-provider@9.6.0 (Koa cb)     │   │
│   │ + Altcha widget      │            │  authorize → interaction → token │   │
│   └─────────┬────────────┘            │  jwks • discovery • userinfo     │   │
│             │ POST /api/login          └───────┬─────────────────────────┘   │
│             ▼                                   │                            │
│   ┌──────────────────────┐    (a) resolve code │ (c) extraTokenClaims(token)│
│   │ /api/login route     │───► access_codes ───┤     reads AuthProfile.     │
│   │ • verify Altcha+CSRF │    + code_redemptions│     activeTierId → JWT AT  │
│   │ • resolve code→tier  │    + write login_    │                            │
│   │ • signIn(nodemailer) │      intent(email)   │  getResourceServerInfo →   │
│   └─────────┬────────────┘                      │   {aud:voice, format:jwt}  │
│             │ SES magic link (OTP + link)       │                            │
│             ▼                                   ▼                            │
│   /login/verify (OTP entry)          findAccount(sub) → claims() (ID token)  │
│             │  (b) magic-link consumed → auth.ts jwt cb:                     │
│             │      upsertAuthProfile(userId,email)                           │
│             │      + apply login_intent(email) → AuthProfile.activeTierId    │
│             │      + record code_redemption(code,userId) [unique-user count] │
└─────────────┼───────────────────────────────────┬──────────────────────────┘
              ▼                                     ▼
   ┌────────────────────┐              ┌──────────────────────────────────┐
   │ DynamoDB (nextauth │              │ DynamoDB (electro single table)  │
   │ adapter table):    │              │ oidc-adapter • auth-profile •    │
   │ users, sessions,   │              │ tiers • access_codes •           │
   │ verification tokens│              │ code_redemptions • login_intent  │
   └────────────────────┘              └──────────────────┬───────────────┘
                                                          ▲
                                     kv CLI (Go) ─────────┘  writes/reads
                                     access_codes + tiers (ElectroDB-shaped items)

   JWT access token (aud=voice.klankermaker.ai, claims: tier_id, group)
        └────► Phase 4 voice service /api/offer validates via JWKS (offline)
```

### Recommended Project Structure
```
apps/auth/
└── webapp/                      # snapshot copy of run.auth/webapp, then trimmed
    ├── src/
    │   ├── config/
    │   │   ├── index.ts         # TRIM: 5 clients → 1 (voice); single-region
    │   │   ├── oidc.ts          # EDIT: enable resourceIndicators, JWT AT, extraTokenClaims
    │   │   └── auth.ts          # TRIM: providers → Email only; apply login_intent
    │   ├── entities/
    │   │   ├── client.ts        # TRIM: drop quotaClient/QUOTA_TABLE
    │   │   ├── oidc-adapter.ts  # KEEP verbatim
    │   │   ├── auth-profile.ts  # EDIT: add activeTierId + activeGroup fields
    │   │   ├── access-code.ts   # NEW
    │   │   ├── code-redemption.ts  # NEW (unique-user counting)
    │   │   ├── login-intent.ts  # NEW (email→tier bridge, short TTL)
    │   │   └── tier.ts          # NEW
    │   ├── app/
    │   │   ├── (authlogin)/login/…   # KEEP; rebrand; keep invite→access code field
    │   │   ├── api/login/route.ts    # EDIT: code resolution replaces AUTH_INVITE_CODES
    │   │   ├── api/captcha/challenge/route.ts   # KEEP verbatim
    │   │   └── api/health/route.ts   # NEW (service.hcl health check = /api/health)
    │   └── pages/api/oidc/…      # KEEP verbatim (catch-all + .well-known + interaction)
    ├── from-aws.tmpl            # REWRITE → /kmv/secrets/use1/* ARNs
    ├── from-aws-to-env.sh       # KEEP (generic SSM→.env.local)
    └── Dockerfile.webapp        # KEEP (multi-stage node:current-alpine standalone)

kv/                              # NEW Go module, sibling pattern to klankrmkr
├── go.mod                       # go 1.26
├── cmd/kv/main.go               # calls internal/app/cmd.Execute()
└── internal/app/cmd/
    ├── root.go                  # NewRootCmd (mirror km's structure)
    ├── code.go                  # kv code create|list|expire   (KV-01)
    └── tier.go                  # kv tier define|list          (KV-02)
```

### Pattern 1: Resource-Indicator JWT access token (the AUTH-02 core)
**What:** Turn on Resource Indicators and describe the `voice` resource server so the token endpoint mints a signed JWT access token audienced to voice.
**When to use:** In `src/config/oidc.ts`, replacing `resourceIndicators: { enabled: false }`.
**Grounding:** `ResourceServer` type at `node_modules/@types/oidc-provider/index.d.ts:704`; helper contracts at `node_modules/oidc-provider/lib/helpers/defaults.js:238-266, 2094-2186`.
```typescript
// Source: node_modules/oidc-provider/lib/helpers/defaults.js:2094 (getResourceServerInfo example)
// In `configuration.features`, replace `resourceIndicators: { enabled: false }` with:
resourceIndicators: {
  enabled: true,
  // With no explicit `resource` param, resolve to the voice resource so the
  // `voice` client always gets a voice-audienced token. `oneOf` (present on
  // code/refresh exchanges) must be honored when provided.
  defaultResource: async (ctx, client, oneOf) => {
    if (oneOf) return oneOf;
    return config.oidc.voiceResource; // e.g. "https://voice.klankermaker.ai"
  },
  // Use the resource granted at authorization time on the token exchange,
  // so the browser client need not re-send `resource` at /token.
  useGrantedResource: async () => true,
  getResourceServerInfo: async (ctx, resourceIndicator, client) => ({
    scope: "voice",
    audience: config.oidc.voiceAudience,      // "voice.klankermaker.ai" (Claude's discretion)
    accessTokenTTL: config.oidc.ttl.accessToken, // set to ~45-60 min (D-03)
    accessTokenFormat: "jwt",
    jwt: { sign: { alg: "RS256" } },          // asymmetric → PyJWT validates via JWKS
  }),
},
```
Notes:
- **Use an asymmetric alg (RS256/ES256), never HS256.** Offline PyJWT validation (AUTH-02) requires the public key from the JWKS endpoint; a symmetric key can't be published. The provider auto-serves the public key at `${routePrefix}/jwks` (already routed in `oidc.ts`).
- The `voice` client must request `scope` that includes `voice` and (recommended) send `resource=<voiceResource>` on the authorize request; `getResourceServerInfo.scope` gates which scopes are allowed for that resource.

### Pattern 2: tier/group claims via `extraTokenClaims` (NOT `findAccount.claims`)
**What:** run.auth's `findAccount().claims()` (`oidc.ts:347-410`) feeds the **ID token / userinfo**. The **access token** claims come from `extraTokenClaims` (`oidc.ts:480-483`, currently `return {}`).
**Grounding:** `extraTokenClaims(ctx, token)` default at `defaults.js:266`; only added to JWT-format access tokens.
```typescript
// Source: src/config/oidc.ts:480 (replace the empty extraTokenClaims)
extraTokenClaims: async (ctx, token) => {
  // token has: accountId (sub), kind ('AccessToken'|'ClientCredentials'), scopes, resourceServer
  if (token.kind !== "AccessToken" || !token.accountId) return {};
  const profile = await getAuthProfile(token.accountId);
  return {
    // Namespaced (Claude's discretion — default recommendation):
    "https://klankermaker.ai/tier_id": profile?.activeTierId ?? "no-access",
    "https://klankermaker.ai/group":   profile?.activeGroup ?? null,
  };
},
```
The voice service (Phase 4) reads these two claims. **This is the Phase-4 contract STATE.md flags** — lock the claim names here.

### Pattern 3: login-time code resolution + email→token tier bridge
**What:** Bridge the tier from `/api/login` (pre-user) to `extraTokenClaims` (post-auth).
**Flow (recommended):**
1. `/api/login` (`src/app/api/login/route.ts`): after Altcha+CSRF pass, resolve `inviteCode` against `access_codes` (expiry + unique-redemption check). Compute `{ tierId, group }` (unknown/blank → `no-access`). Write a `login_intent` item keyed by **email** with `{ tierId, group, ttl ≈ magic-link expiry }` (latest-wins overwrite → satisfies D-05). Then `signIn("nodemailer", …)` as today.
2. Magic link consumed → `src/config/auth.ts` `jwt` callback nodemailer branch (`auth.ts:292-303`): after `upsertAuthProfile(userId, "email", {email})`, look up `login_intent` by email; stamp `AuthProfile.activeTierId`/`activeGroup`; write a `code_redemption` item keyed `[code, userId]` and, only if that item is new, conditionally increment `access_codes.redemptionCount` (unique-user count, D-06); delete the consumed `login_intent`.
3. OIDC token issuance → `extraTokenClaims` reads `AuthProfile.activeTierId` (Pattern 2).
**Why not stamp AuthProfile directly at `/api/login`:** brand-new users have no `userId` until the magic-link callback creates them via the adapter — so the bridge must key on email. `login_intent` also gives a natural TTL so a never-clicked code doesn't leak tier state.

### Pattern 4: ElectroDB entities with explicit key `template`s (de-risks `kv`)
**What:** Give the new entities human-readable, explicitly-templated keys so the Go `kv` CLI reproduces them without reverse-engineering ElectroDB's default `$service#attr_value` format.
**Grounding:** ElectroDB `template` supported on index pk/sk (`node_modules/electrodb/index.d.ts:3661,3692,3699`).
```typescript
// Source: src/entities/access-code.ts (NEW) — key excerpt
export const AccessCode = new Entity({
  model: { entity: "AccessCode", version: "1", service: "kmv" },
  attributes: {
    code: { type: "string", required: true },   // store normalized (e.g. lowercased) — decide case policy
    tierId: { type: "string", required: true },
    group: { type: "string" },
    expiresAt: { type: "number" },               // epoch ms; enforced at resolution
    maxRedemptions: { type: "number" },          // unique users; undefined = unlimited
    redemptionCount: { type: "number", default: 0 },
    createdAt: { type: "number", default: () => Date.now(), readOnly: true },
  },
  indexes: {
    primary: {
      pk: { field: "pk", composite: ["code"], template: "code#${code}" },
      sk: { field: "sk", composite: [],       template: "code#" },
    },
    // GSI for `kv code list` (electro-schema table has gsi1..gsi3)
    all: {
      index: "gsi1pk-gsi1sk-index",
      pk: { field: "gsi1pk", composite: [],        template: "accesscodes#" },
      sk: { field: "gsi1sk", composite: ["code"],  template: "code#${code}" },
    },
  },
}, { client: electroClient, table: ELECTRO_TABLE });
```
The Go side then writes `pk = "code#" + code`, `sk = "code#"`, plus the same gsi fields — no `$service`/casing guesswork. Add an integration test: `kv code create demo …` then the webapp's `AccessCode.get({code:"demo"})` must return it.

### Anti-Patterns to Avoid
- **Putting tier/group in the ID token** (run.auth's pattern) — the voice service validates the *access* token; ID-token claims won't be present.
- **HS256 JWT access tokens** — breaks offline JWKS validation (AUTH-02).
- **Permanent `tier` on AuthProfile keyed only by user** as the source of tier — violates D-05 latest-wins semantics unless overwritten every login; use the `login_intent` bridge so re-entering a code changes tier deterministically.
- **Letting `kv` invent its own key layout** — any mismatch with ElectroDB makes rows invisible to the webapp. Use explicit `template`s.
- **Counting redemptions on every login event** — D-06 requires unique users; gate the increment on a new `code_redemption[code,userId]` insert.
- **Keeping run.auth's region prefix** (`next.config.ts` `basePath:/${REGION_SHORT}`, `config.region`) unexamined — klanker-voice is single-region (us-east-1). Decide whether to keep `use1` basePath or flatten to root; the OIDC route prefix, issuer URL, and cookie domain must all agree. Simplest: keep the `use1` convention verbatim (least diff) OR flatten consistently. Flag as a port decision.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| OIDC issuer / JWKS / discovery | Custom JWT signer + JWKS endpoint | `oidc-provider` 9.6.0 (ported) | Spec-correct authorize/token/introspection/rotation already working |
| DynamoDB↔ElectroDB key strings in Go | Hand-parse `$service#attr_value` | Explicit ElectroDB `template`s + a round-trip test | ElectroDB's default composed-key format + casing rules are easy to get subtly wrong |
| Magic-link token + email | Custom OTP + SES SDK calls | next-auth Email(nodemailer) provider (ported `auth.ts`) | Token generation, verification-token table, one-time use already handled |
| Captcha | Roll a PoW / reCAPTCHA integration | Altcha (ported) — `createChallenge`/`verifySolution` + replay LRU | Already wired with server-side replay protection (`api/login/route.ts:12-43`) |
| next-auth persistence | Custom user/session tables | `@auth/dynamodb-adapter` (its own `nextauth`-schema table) | Adapter contract is fiddly; the module already ships the schema |
| Table provisioning | `aws dynamodb create-table` by hand | terragrunt dynamodb unit (`electro`/`nextauth` predefined schemas) | Phase-2 unit already renders; add tables via `service.hcl` |

**Key insight:** Almost nothing in this phase should be *written*; it should be *moved and re-wired*. The only net-new code is: 4 ElectroDB entities, the Resource-Indicator config delta, the code-resolution + bridge logic, a `/api/health` route, and the `kv` CLI. Everything else is a copy-and-trim.

## Runtime State Inventory

This is a greenfield port (new app, new tables — no existing klanker-voice auth data to migrate), but it TOUCHES existing Phase-2 infra state and copies from a live source tree, so the relevant categories:

| Category | Items Found | Action Required |
|----------|-------------|------------------|
| Stored data | **None to migrate** — `access_codes`/`tiers`/`nextauth`/`electro` tables do not yet exist in klanker-voice (Phase-2 dynamodb unit applied with `tables=[]`, per 02-05-SUMMARY). Seed data (tiers, demo codes) is created fresh via `kv`, not migrated. | Seed via `kv` after tables exist |
| Live service config | Phase-2 SSM SecureStrings already provisioned: `/kmv/secrets/use1/{jwt/secret, jwt/internal_secret, oidc/cookie_keys, altcha/secret}` + SES SMTP creds at `/kmv/ses/smtp/default/auth.klankermaker.ai/*` (02-05-SUMMARY). These are CONSUMED, not created, by Phase 3. | Map into container env via `from-aws.tmpl` rewrite + task `valueFrom` |
| OS-registered state | None (no schedulers/daemons; ECS-managed) | None |
| Secrets/env vars | run.auth env NAMES to rewrite: `AUTH_JWT_SECRET`, `OIDC_COOKIE_KEYS`, `ALTCHA_HMAC_KEY`, `AUTH_SES_*`, `AUTH_DYNAMODB_*`, `AUTH_ELECTRO_*`. DROP: all `OIDC_*_CLIENT_ID/SECRET` except `voice`; all `AUTH_{GITHUB,STRAVA,DISCORD}_*`; `AUTH_QUOTA_*`; `AUTH_INVITE_CODES` (replaced by access_codes table). | Rewrite `from-aws.tmpl`; update `config/index.ts` |
| Build artifacts / installed packages | Copying run.auth may drag `.next/`, `node_modules/`, `tsconfig.tsbuildinfo`, `.env`/`.env.local`, `VERSION`. These must NOT be copied (stale, machine-specific, secret-bearing). | Snapshot-copy `src/`, config files, Dockerfile, `package.json`; run fresh `npm install`; regenerate `.env.local` from the rewritten template |

**Canonical question — after every file is updated, what runtime state still has the old string?** For a fresh port the answer is: only the Phase-2 SSM paths and the terragrunt state (dynamodb unit) — both handled by config rewrite + `service.hcl` edit. No orphaned DynamoDB keys, because the tables are new.

## Common Pitfalls

### Pitfall 1: `kv` (Go) and ElectroDB disagree on the DynamoDB key string
**What goes wrong:** `kv code create` writes an item the webapp's `AccessCode.get()` can't find (or vice-versa), because ElectroDB composes keys as `$<service>#<attr>_<value>` with its own casing rules (observed in the quota service: `pk = "$quota#userid_<id>"`, `sk = "$userquota_1#quotaid_<id>"`, `services/quota.ts:218-219`), and Go builds a different string.
**Why it happens:** ElectroDB's default key format is an internal convention (service prefix, entity+version segment, per-attribute `attr_value`, default lowercasing) not obvious from the entity definition.
**How to avoid:** Define the new entities with explicit index `template`s (Pattern 4) so keys are trivially reproducible in Go; and write a compatibility test that round-trips `kv`-written items through an ElectroDB read.
**Warning signs:** `kv code list` shows a code but the login form rejects it as unknown; DynamoDB console shows two items for the "same" code with different pk/sk.

### Pitfall 2: JWT access token isn't actually a JWT (or wrong audience)
**What goes wrong:** Voice service gets an opaque token it can't validate offline, or the `aud` doesn't match what PyJWT checks.
**Why it happens:** `getResourceServerInfo` not returning `accessTokenFormat:'jwt'`, or `defaultResource` not resolving so the token is issued without a resource (falls back to opaque), or the client never requests the `voice` resource/scope.
**How to avoid:** Set `accessTokenFormat:'jwt'` + `audience` explicitly; have `defaultResource` return the voice resource; verify by decoding a real token from the `/token` endpoint in staging and confirming `aud` + `tier_id`/`group` claims + an `RS256` header with a `kid` present in `/jwks`.
**Warning signs:** token has 3 base64 segments but no `aud`; or token is a single opaque string; or `/jwks` returns no matching `kid`.

### Pitfall 3: tier resolved at `/api/login` never reaches the token
**What goes wrong:** Every token comes out `no-access` even for valid codes.
**Why it happens:** The code is entered pre-user-creation; if you try to stamp `AuthProfile` by `userId` at `/api/login`, there's no `userId` yet, so nothing is written; `extraTokenClaims` then reads a default.
**How to avoid:** Use the `login_intent`-by-email bridge (Pattern 3); apply it in the `auth.ts` nodemailer `jwt` branch where `userId` and `email` are both known.
**Warning signs:** first-time users always `no-access`; returning users sometimes work (because their AuthProfile already existed).

### Pitfall 4: the magic-link email still auto-consumes on scan (AUTH-01 not actually satisfied)
**What goes wrong:** Corporate link-scanners GET the callback URL and burn the one-time token before the human clicks.
**Why it happens:** run.auth's email (`auth.ts:signupHTML`, ~line 401) contains a direct link to `/api/auth/callback/nodemailer?token=…` — a GET that consumes immediately. The OTP-entry `/login/verify` page mitigates for users who type the code, but the direct link is still scanner-bait.
**How to avoid:** For AUTH-01's "interstitial confirm-click page," point the email link at a **confirm page** (e.g. `/login/confirm?token=…&email=…`) that renders a button which POSTs (or navigates on explicit click) to the callback — so a HEAD/GET prefetch by a scanner does not consume the token. Keep the OTP code path as the fallback. Treat the confirm page as **net-new** (run.auth does not have it).
**Warning signs:** users report "link already used"; SES logs show the callback hit seconds after send from a datacenter IP.

### Pitfall 5: two tables, not one — the nextauth adapter needs its own
**What goes wrong:** `@auth/dynamodb-adapter` and ElectroDB entities collide, or the adapter can't find its GSI.
**Why it happens:** run.auth actually runs THREE tables (`client.ts:67-69`: `run-auth-authjs`, `run-auth-electro`, `run-quota-electro`). The adapter uses a different key convention (`GSI1PK/GSI1SK`, the module's `nextauth` schema) than ElectroDB (`gsi1pk`/`gsi2pk`/`gsi3pk`, the `electro` schema). "Single-table ElectroDB" (D-04) refers only to the electro entities.
**How to avoid:** Provision exactly two tables in `service.hcl`: one `table_type="nextauth"` (adapter) and one `table_type="electro"` (oidc-adapter + auth-profile + tiers + access_codes + code_redemptions + login_intent). Drop the quota table (D-11). Wire `AUTH_DYNAMODB_DBNAME`→nextauth table, `AUTH_ELECTRO_DBNAME`→electro table.
**Warning signs:** `ResourceNotFoundException` on GSI1; adapter writes landing in the electro table.

### Pitfall 6: region-prefix assumptions leak into a single-region deploy
**What goes wrong:** OIDC issuer URL, cookie domain, basePath, and redirect URIs disagree; login loops or "invalid redirect_uri".
**Why it happens:** run.auth is multi-region (`config.region`, `next.config.ts` `basePath:/${REGION_SHORT}`, `[...path].ts` region-prefix redirect rewriting). klanker-voice is single-region.
**How to avoid:** Decide once — keep `use1` basePath verbatim (least diff, everything already consistent) OR flatten to root and update issuer/routePrefix/basePath/cookie/redirect-URI together. Do not partially flatten.
**Warning signs:** `/api/oidc/.well-known/openid-configuration` issuer doesn't match the JWKS URL host+path the client fetches.

## Code Examples

### Access-code resolution replacing the static invite-code gate
```typescript
// Source: src/app/api/login/route.ts — replace lines 6 + 127-132 (AUTH_INVITE_CODES logic)
// after Altcha + CSRF + replay checks pass:
const normalized = String(inviteCode ?? "").trim().toLowerCase(); // decide case policy
let tierId = "no-access", group: string | null = null;
if (normalized) {
  const rec = await AccessCode.get({ code: normalized }).go();
  const c = rec.data;
  const notExpired = !c?.expiresAt || c.expiresAt > Date.now();
  const underCap = !c?.maxRedemptions || (c.redemptionCount ?? 0) < c.maxRedemptions;
  if (c && notExpired && underCap) { tierId = c.tierId; group = c.group ?? null; }
  // NOTE: login always proceeds even if code invalid/expired (D-07) — just no-access.
}
await LoginIntent.upsert({ email, tierId, group, expiresAt: Date.now() + 15 * 60 * 1000 }).go();
// then existing signIn("nodemailer", { email: encodeURI(email), csrfToken, redirect: false })
```

### Applying the bridge + unique-user redemption count at magic-link consumption
```typescript
// Source: src/config/auth.ts — inside jwt() nodemailer branch (~line 292-303), after upsertAuthProfile
const intent = (await LoginIntent.get({ email: token.email as string }).go()).data;
if (intent && userId) {
  await AuthProfile.patch({ userId })
    .set({ activeTierId: intent.tierId, activeGroup: intent.group ?? undefined }).go();
  // unique-user count: only increment if this (code,userId) redemption is new
  try {
    await CodeRedemption.create({ code: intent.tierId /* or store code on intent */, userId }).go();
    await AccessCode.patch({ code: /* code */ }).add({ redemptionCount: 1 }).go();
  } catch (e) { /* create() conditional-fails if already redeemed → skip increment */ }
  await LoginIntent.delete({ email: token.email as string }).go();
}
```
*(Store the resolved `code` on `login_intent` so the redemption record and count target the right code.)*

### `kv` cobra command surface (KV-01/KV-02)
```
kv code create <code> --tier <tierId> [--group <g>] [--expires <RFC3339>] [--max <n>]
kv code list [--json]
kv code expire <code>                 # set expiresAt = now (soft) or delete
kv tier define <tierId> --session-max <sec> --period-max <sec> --max-concurrent <n>
kv tier list [--json]
```
Mirror `km`'s structure: `cmd/kv/main.go` → `internal/app/cmd.Execute()`; `NewRootCmd(cfg)` builds the tree with `code`/`tier` parent commands (see `klankrmkr/internal/app/cmd/root.go:NewRootCmd`). Each writes/reads the electro table with `aws-sdk-go-v2/service/dynamodb` using the explicit `template` key strings from Pattern 4. `tiers` fields per design spec §6: `tier_id, session_max_seconds, period_max_seconds, max_concurrent`.

## State of the Art

| Old Approach (run.auth today) | Current Approach (this phase) | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Opaque access tokens | JWT access tokens via Resource Indicators | oidc-provider ≥8 supports RI; enable on 9.6.0 | Enables offline PyJWT validation (AUTH-02) |
| Claims in ID token (`conformIdTokenClaims:false`) | tier/group in access token via `extraTokenClaims` | this phase | Voice service reads the token it actually validates |
| Static shared invite codes (`AUTH_INVITE_CODES` env) | `access_codes` table with expiry + unique-user caps | this phase | Operator-managed, per-tier, revocable via `kv` |
| Three tables incl. quota | Two tables (nextauth + electro); quota deferred | D-11 | Smaller surface; Phase 4 adds `usage` |

**Deprecated/outdated (for this phase):**
- `src/services/quota.ts`, `src/entities/user-quota.ts`, `src/lib/quota-definitions.ts`, and the whole `src/app/api/{quota,admin/quota,internal/quota}/**` tree — DROP (D-11). Phase 4 rebuilds against the design-spec `usage` schema.
- Discord/GitHub/Strava providers + `src/lib/strava-tokens.ts` + `src/app/(authlogin)/strava/**` + leaflet/gpx/pdf/qr/strapi deps — DROP (email-only login for this project).
- Four of five OIDC clients in `oidc.ts` (runHuman, cmsStrapi, gpxStudio, flashTool, bib) → keep only a new `voice` client (authorization_code + PKCE; redirect URIs on voice.klankermaker.ai).

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | ElectroDB's default composed-key format is `$<service>#<attr>_<value>` with default lowercasing (inferred from `services/quota.ts:218-219`); exact casing/delimiter rules not re-derived from ElectroDB source this session | Pitfall 1 | If Go mirrors the *default* format instead of using explicit `template`s, a casing/delimiter mismatch silently hides rows. Mitigation (explicit `template`s + round-trip test) makes this moot. |
| A2 | `extraTokenClaims` returned claims appear in JWT-format access tokens (and only there / in introspection for opaque) | Pattern 2 | If claims don't propagate to the JWT, tier/group would be missing; verify by decoding a staging token (Pitfall 2 check covers this) |
| A3 | AUTH-01's interstitial confirm page is net-new (run.auth's email link is a direct-consumption GET) | Pitfall 4 | If run.auth already ships a confirm interstitial elsewhere, this is redundant work — verified absent in the file tree scanned, but confirm during port |
| A4 | Namespaced claim URIs (`https://klankermaker.ai/tier_id`) are acceptable; exact names are Claude's discretion but become the Phase-4 contract | Pattern 2 | Phase 4 must read the SAME names; lock them in this phase's output and STATE.md |
| A5 | Keeping the `use1` region basePath verbatim is the lowest-diff port choice | Pitfall 6 | If the team wants clean root-mounted URLs, more files change together; either is fine if consistent |
| A6 | Two physical tables (nextauth + electro) suffice; `usage`/quota table omitted (D-11) | Pitfall 5 | Correct per D-11; Phase 4 adds the third table |

## Open Questions

1. **Exact claim names + audience/issuer URIs (the Phase-4 contract).**
   - What we know: tier_id + group only (D-01); aud ≈ `voice.klankermaker.ai` (D-02); issuer `https://auth.klankermaker.ai[/use1]/api/oidc`.
   - What's unclear: namespaced vs bare claim keys; whether basePath keeps `use1`.
   - Recommendation: Decide during planning and record verbatim in the phase output + STATE.md so Phase 4's PyJWT config matches byte-for-byte.

2. **Does `login_intent` need to store the `code`, or is `tierId` enough for redemption counting?**
   - What we know: unique-user count is per-code (D-06).
   - What's unclear: whether re-using tierId as the redemption key is safe if two codes map to the same tier (it isn't — two codes sharing a tier would share a count).
   - Recommendation: store the resolved `code` on `login_intent` and key `code_redemptions`/the count on `code`, not `tierId`.

3. **Code case-sensitivity/format policy** (Claude's discretion).
   - Recommendation: normalize to lowercase on both write (`kv`) and read (`/api/login`) to avoid `Demo` vs `demo` misses; document it as the policy.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Node.js 22 LTS | Build/run auth webapp | ✓ (build container `node:current-alpine`) | current-alpine | Pin to `node:22-alpine` for reproducibility |
| Go 1.26.x | `kv` CLI build | assumed (km builds on 1.25.5) | — | verify `go version`; `go 1.26` in go.mod may need toolchain fetch |
| DynamoDB (AWS) | tables | ✓ (Phase 2 unit applied, `tables=[]`) | — | local `dynamodb-local` for dev via `AUTH_*_ENDPOINT` |
| SES (production) | magic-link | ✓ (02-05: 50k/day @ 14/s, DKIM Success) | — | — |
| SSM SecureStrings `/kmv/secrets/use1/{jwt,oidc,altcha}` | container env | ✓ (02-05) | — | — |
| ECR `kmv-auth-app` + ECS cluster `app-use1-kmv` | deploy | ✓ (02-05, IMMUTABLE) | — | — |

**Missing dependencies with no fallback:** none — all infra Phase 3 consumes was provisioned in Phase 2.
**Missing dependencies with fallback:** local dev needs `dynamodb-local` (endpoint envs already supported in `client.ts:4-5`) and a fresh `.env.local` via `from-aws-to-env.sh`.

## Validation Architecture

`workflow.nyquist_validation` is `true` in `.planning/config.json` — this section applies.

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Vitest 4.1.9 (webapp, `vitest.config.ts` present in run.auth — port it); Go `testing` + table tests (kv, mirrors km's extensive `_test.go` suite) |
| Config file | `apps/auth/webapp/vitest.config.ts` (ported); `kv` uses standard `go test` |
| Quick run command | `cd apps/auth/webapp && npm test` ; `cd kv && go test ./...` |
| Full suite command | same (fast unit suites) |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| AUTH-02 | JWT access token has `aud=voice`, `tier_id`, `group`, RS256 header | unit | `npm test -- oidc-resource-token` (mock provider or decode fixture) | ❌ Wave 0 |
| AUTH-03 | known code→tier, blank/unknown→no-access, login still succeeds | unit | `npm test -- access-code-resolution` | ❌ Wave 0 |
| AUTH-04 | expired code / over-max → no-access; count increments once per unique user | unit | `npm test -- code-redemption` | ❌ Wave 0 |
| AUTH-05 | Altcha verify + replay rejection | unit | `npm test -- login-altcha` (port run.auth's replay logic test if any) | ❌ Wave 0 |
| AUTH-01 | confirm page does not consume token on GET/prefetch | integration | manual + route test | ❌ Wave 0 (net-new page) |
| KV-01/02 | kv writes items ElectroDB reads back (key compat) | integration | `go test ./... -run KeyCompat` + a Node read assertion | ❌ Wave 0 (critical — Pitfall 1) |

### Sampling Rate
- **Per task commit:** `npm test` (webapp) or `go test ./...` (kv) for the touched module.
- **Per wave merge:** both suites green.
- **Phase gate:** both suites green + a manual staging check that a real `/token` response decodes to a JWT with the expected `aud`/claims before `/gsd-verify-work`.

### Wave 0 Gaps
- [ ] Port `apps/auth/webapp/vitest.config.ts` from run.auth.
- [ ] `access-code-resolution.test.ts`, `code-redemption.test.ts` — covers AUTH-03/04.
- [ ] `oidc-resource-token.test.ts` — decode a minted token, assert format/aud/claims (AUTH-02).
- [ ] `kv/internal/app/cmd/*_test.go` + a Node-side round-trip assertion — KV key compatibility (Pitfall 1).
- [ ] Test fixtures/mocks for DynamoDB (dynamodb-local or aws-sdk mock).

## Security Domain

`security_enforcement` is not set to `false` in config → enabled.

### Applicable ASVS Categories
| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | yes | next-auth magic-link (one-time tokens, `generateVerificationToken`); Altcha PoW on login (`api/login/route.ts`) |
| V3 Session Management | yes | oidc-provider grants/sessions (ElectroDB adapter), sess_auth JWT cookie httpOnly+secure+sameSite (`auth.ts:169-175`); session invalidation via `sessionVersion` (auth-profile) |
| V4 Access Control | yes | tier_id/group claims gate voice access (enforced Phase 4); `no-access` default-deny for unknown codes |
| V5 Input Validation | yes | validate `email`, `inviteCode`, `csrfToken`, `altcha` on `/api/login`; normalize code case; the untrusted-input boundary applies to `access_codes` values written by `kv` |
| V6 Cryptography | yes | RS256/ES256 JWT signing via oidc-provider JWKS — never hand-roll; signing keys from `/kmv/secrets/use1/jwt/*` + `oidc/cookie_keys` |

### Known Threat Patterns for {Next.js OIDC issuer + DynamoDB}
| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Magic-link token consumed by link-scanner | Spoofing/DoS-of-account | Interstitial confirm page (AUTH-01, Pitfall 4); OTP fallback |
| JWT algorithm confusion (alg=none / HS via public key) | Tampering | Fixed asymmetric alg in `getResourceServerInfo.jwt.sign`; PyJWT must pin allowed algs to RS256/ES256 (Phase-4 note) |
| Altcha replay | Spoofing | Existing in-memory replay LRU (`api/login/route.ts:12-43`) — note it's per-instance; multi-task deploy weakens it (acceptable for PoW; document) |
| Access-code brute force / enumeration | Info disclosure | Rate-limit `/api/login`; codes are not secrets but treat unknown→no-access uniformly (no oracle); Altcha adds cost |
| `kv`-written code injecting control chars into keys | Tampering | Validate/normalize code charset in `kv` before write; explicit templates bound the key shape |
| Open redirect via OIDC `redirect_uri` | Tampering | oidc-provider validates against the registered `voice` client's `redirect_uris` allowlist |
| CSRF on login POST | Tampering | `verifyCsrfToken` + next-auth csrf cookie (`api/login/route.ts:46-80`) |

## Project Constraints (from CLAUDE.md)

- **Naming:** "klanker-voice" everywhere; never "voiceai" (copyright). The `kv` CLI is the sibling to `km`. (Enforced.)
- **Auth stack pins (CLAUDE.md table):** the CLAUDE.md long-term targets are next-auth beta.31 / oidc-provider 9.8.6 / electrodb 3.9.1, but **CONTEXT.md D-08 overrides for THIS phase** — port at run.auth's exact versions (beta.30 / 9.6.0 / 3.5.3) and bump later. CLAUDE.md itself endorses "port whatever run.auth uses, upgrade deliberately," so no conflict.
- **PyJWT validation (CLAUDE.md):** the Phase-4 consumer uses PyJWT 2.13 + PyJWKClient with issuer/audience checks — this phase must expose JWKS and mint tokens compatible with that (asymmetric alg).
- **`kv` stack (CLAUDE.md):** Go 1.26.x, cobra v1.10.2, aws-sdk-go-v2 v1.42.x, optional lipgloss v1.1.0 — matches `km` for structural parity.
- **Secrets:** SOPS → SSM SecureString consumed via `valueFrom` — use the Phase-2 `/kmv/secrets/use1/*` paths, do not introduce new secret-delivery mechanisms.
- **Terraform/terragrunt:** match defcon.run.34 conventions; the dynamodb unit is edited via `service.hcl`, no module major bumps.

## Sources

### Primary (HIGH confidence)
- `defcon.run.34/apps/run.auth/webapp/src/config/oidc.ts` — clients, `resourceIndicators:{enabled:false}`, `findAccount`/`claims()`, `extraTokenClaims`, routes incl. jwks, ttl, cookies
- `.../src/config/auth.ts` — next-auth v5 config, nodemailer→SES `sendVerificationRequest`, `signupHTML`, jwt/session callbacks, provider list (Email/GitHub/Strava/Discord)
- `.../src/config/index.ts` — env→config mapping, region/basePath, OIDC issuer/clients/ttl, cookie names
- `.../src/entities/{oidc-adapter,auth-profile,client,user-quota}.ts` — ElectroDB entity + index conventions, three-table client wiring
- `.../src/app/api/login/route.ts` — invite-code gate, Altcha `verifySolution` + replay LRU, CSRF, `signIn("nodemailer")`
- `.../src/app/api/captcha/challenge/route.ts` — Altcha `createChallenge`, `ALTCHA_HMAC_KEY`
- `.../src/app/(authlogin)/login/page.tsx` + `login/verify/page.tsx` — access-code field, Altcha widget, OTP verify page
- `.../src/pages/api/oidc/[...path].ts` + `.well-known/openid-configuration.ts` + `interaction/[uid].ts` — Pages-Router OIDC wiring, grant minting
- `.../src/services/quota.ts` — DROP target; also the source of the observed ElectroDB key format
- `.../from-aws.tmpl`, `from-aws-to-env.sh`, `Dockerfile.webapp`, `next.config.ts`, `package.json` — port mechanics
- `node_modules/oidc-provider/lib/helpers/defaults.js:238-266, 2094-2186` + `@types/oidc-provider/index.d.ts:704-731,1249-1266` — Resource Indicators / ResourceServer / getResourceServerInfo / extraTokenClaims contracts (installed 9.6.0)
- `klanker-voice/infra/terraform/modules/dynamodb/v1.0.0/{variables,main}.tf` — `electro`/`nextauth`/`standard` predefined schemas, table declaration shape
- `klanker-voice/infra/terraform/live/site/services/auth/service.hcl` — `dynamodb.tables=[]`, health check `/api/health`, ECR/task/service stubs
- `klankrmkr/internal/app/cmd/root.go` + `cmd/km/main.go` + `go.mod` — cobra structure and aws-sdk-go-v2 modules to mirror for `kv`
- `.planning/phases/02-infra-skeleton/02-05-SUMMARY.md` — provisioned SSM secrets, SES, empty dynamodb unit, ECR/cluster names
- `docs/superpowers/specs/2026-07-04-klanker-voice-design.md` §5-6 — token flow into `/api/offer`, access codes, tiers schema

### Secondary (MEDIUM confidence)
- `node_modules/electrodb/index.d.ts:3661,3692,3699` — index `template` support (grounds the de-risking recommendation)

### Tertiary (LOW confidence)
- ElectroDB default key casing/delimiter specifics — inferred, not re-derived from source; superseded by the explicit-`template` recommendation (A1).

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — copied from a running production lockfile; oidc-provider version confirmed on disk.
- Architecture (RI JWT token, extraTokenClaims, bridge): HIGH for the API contracts (grounded in installed lib source/types); the login→token bridge design is a HIGH-confidence recommendation but is net-new code the planner should validate with tests.
- Pitfalls: HIGH — each maps to a concrete observed file/behavior.
- kv key compatibility: MEDIUM — the risk is real; the explicit-template mitigation is HIGH-confidence.

**Research date:** 2026-07-05
**Valid until:** 2026-08-04 (stable — pinned versions, ported source; re-check only if run.auth is upgraded or the deferred dependency bump lands)
