# Phase 3: Auth Service & Access Codes - Context

**Gathered:** 2026-07-05
**Status:** Ready for planning

<domain>
## Phase Boundary

Port `run.auth` (defcon.run.34) into klanker-voice as the magic-link + OIDC identity
service at auth.klankermaker.ai, add access-code capture → tier/group claims in a JWT the
voice service validates offline, and give `kv` the code + tier CRUD commands.

**In scope:** AUTH-01..05 (magic-link w/ interstitial, JWT access tokens with tier/group
claims via JWKS, access-code capture, code expiry/max-redemption, Altcha), KV-01 (code
CRUD), KV-02 (tier define/list). The `tiers` and `access_codes` tables/entities. UI hint: yes.

**NOT in scope (Phase 4):** quota ENFORCEMENT — the `usage` table, conditional-write
ticking, session blocking, concurrency markers, site-wide kill-switch, spoken wind-down
(QUOT-01..05); KV-03/04/05 (usage view, kill-switch flip, smoke test). The voice service
that consumes the token is Phase 4.

**This phase produces the CONTRACT Phase 4 blocks on** (STATE.md flags this): the exact
JWT claim shape + token type the voice service reads at session start.

</domain>

<decisions>
## Implementation Decisions

### Token claims contract (the Phase-4 dependency)
- **D-01:** Token carries **tier_id + group claims ONLY**. The voice service reads the
  `tiers` table for the actual limits (session/daily/concurrency) at session start — it
  hits DynamoDB for usage anyway in Phase 4. Thin token; `tiers` table is the single
  source of truth; editing a tier's numbers does not require re-issuing tokens.
- **D-02:** Token type is a **JWT access token via oidc-provider Resource Indicators**,
  audienced to the voice resource (e.g. `aud=voice.klankermaker.ai`). The voice service
  (PyJWT + PyJWKClient) validates issuer + audience offline via JWKS — matches AUTH-02
  verbatim. NOTE: run.auth currently has `resourceIndicators: { enabled: false }` and puts
  claims in the ID token (`conformIdTokenClaims: false`); this phase ENABLES Resource
  Indicators and moves tier/group onto the access token.
- **D-03:** Access-token **TTL comfortably exceeds the longest tier (30 min) + a reconnect
  window** (~45–60 min target). The token gates session *establishment* only; the media
  channel runs independently once connected, and the quota system (not token expiry) ends
  sessions. No short-TTL/refresh plumbing in the browser client.

### Access-code model (new — run.auth has no access_codes)
- **D-04:** `access_codes` is a **new entity in run.auth's existing ElectroDB single-table
  design** (consistent with the oidc-adapter / auth-profile / user-quota entities; one
  table added to the Phase-2 dynamodb unit).
- **D-05:** Redemption binding is **per-login, latest-wins**: the code entered at a given
  login sets that session's tier; re-entering changes tier; there is no permanent per-user
  tier stamp. Matches "any value or none accepted at each login"; lets a user upgrade by
  entering a better code.
- **D-06:** A code's `max_redemptions` counts **unique users**, not total login events —
  a shared conference code ("good for 200 people") is not burned by reconnects. Needs a
  redeemed-by marker; also makes the kv redemption count meaningful (people, not clicks).
- **D-07:** Code carries: tier id, group, expiry date, max_redemptions, redemption_count
  (per design spec). Expiry + max-redemption enforced at **login-time code resolution**.
  Unknown/blank code → `no-access` tier (authenticated but cannot start voice sessions;
  UI explains how to get a code). Login always succeeds via magic link regardless of code.

### Port strategy & versions
- **D-08:** Port at run.auth's **exact working versions** (next-auth 5.0.0-beta.30,
  oidc-provider 9.6.0, electrodb 3.5.3, next 16.1.6, @auth/dynamodb-adapter 2.11.1, altcha
  2.3.0 / altcha-lib 1.4.1, nodemailer 7, react 19.2.4). Bump to the CLAUDE.md pins
  (beta.31 / 9.8.6 / 3.9.1) as a **separate later task** — matches CLAUDE.md's own guidance;
  keeps the port and the upgrade as distinct, debuggable diffs.
- **D-09:** **Copy wholesale, then trim.** Copy the working webapp verbatim, get it green
  in klanker-voice, then delete DEF CON specifics (Discord OAuth provider, the run.* OIDC
  clients — keep only `voice`, run-branded UI) as a reviewable diff on a known-good baseline.

### App location & Phase-3/Phase-4 quota boundary
- **D-10:** Ported app lives at **`apps/auth/webapp/`** — `apps/auth/` parallels
  `apps/voice/`, and keeping run.auth's own `webapp/` subdir means its Dockerfile and path
  assumptions port unchanged. Matches ECR repo `kmv-auth-app`.
- **D-11:** Phase 3 builds **only `tiers` + `access_codes`** (tables, entities, kv CRUD).
  The `usage` table, ticking, session-blocking, concurrency, and kill-switch are Phase 4
  (the voice service writes usage). **Drop run.auth's DEF CON quota code** (user-quota.ts /
  quota.ts) — Phase 4 rebuilds against the design-spec quota schema, not DEF CON's semantics.

### Claude's Discretion
- Claim namespace/naming convention (default: namespace the custom tier/group claims),
  issuer/audience exact URIs, seed-code values beyond the spec examples (demo→2min,
  kphdemo123→30min), code case-sensitivity/format, the interstitial confirm-click page
  design, Altcha server-key wiring from `/kmv/secrets/use1/altcha/secret`, secrets env
  mapping (run.auth `from-aws.tmpl` → SSM `/kmv/secrets/use1/*`), kv command surface shape,
  ElectroDB access-pattern/index design, snapshot-copy vs git-history for the port.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Source to port (READ FIRST — this is the app being ported)
- `/Users/khundeck/working/defcon.run.34/apps/run.auth/webapp/` — the working app. Key files:
  - `webapp/package.json` — the exact versions to match (D-08)
  - `webapp/src/config/oidc.ts` — oidc-provider config, `findAccount`/`claims()` callback, the `resourceIndicators: {enabled:false}` to flip (D-02), the 5 run.* clients to trim to `voice` (D-09)
  - `webapp/src/entities/` — oidc-adapter.ts, auth-profile.ts, client.ts, user-quota.ts (the ElectroDB single-table entities; add access_codes here per D-04; drop user-quota per D-11)
  - `webapp/src/services/quota.ts` — DEF CON quota service (DROP; Phase 4 rebuilds)
  - `webapp/src/pages/api/oidc/*` — OIDC issuer routes (Pages Router); `webapp/Dockerfile.webapp`, `from-aws.tmpl`/`from-aws-to-env.sh` — secrets env mapping to rewrite for `/kmv/secrets/*`

### Design & requirements
- `docs/superpowers/specs/2026-07-04-klanker-voice-design.md` §6 (Auth service & quota model: port scope, access codes, token claims, quota tables), §5 (token flow into /api/offer)
- `.planning/REQUIREMENTS.md` — AUTH-01..05, KV-01, KV-02 (this phase's contract); QUOT-*, KV-03/04/05 (Phase 4, out of scope here)
- `.claude/CLAUDE.md` — auth-service tech-stack table (version pins + "port whatever run.auth uses, upgrade deliberately" guidance) and the `kv` CLI stack (cobra/aws-sdk-go-v2)

### Phase-2 infra this phase consumes (already provisioned)
- `.planning/phases/02-infra-skeleton/02-05-SUMMARY.md` — SES identity auth.klankermaker.ai VERIFIED w/ DKIM; SSM SecureStrings `/kmv/secrets/use1/{jwt/{secret,internal_secret},oidc/cookie_keys,altcha/secret}`; DynamoDB unit applied with ZERO tables (Phase 3 adds auth tables by editing its service.hcl); ECR repo `kmv-auth-app`; auth. zone `Z0555375BRDXI4K3061A`
- `.planning/phases/02-infra-skeleton/02-06-SUMMARY.md`, `02-07-SUMMARY.md` — CI/OIDC deploy path (github-oidc roles, terragrunt + build/deploy workflows) that Phase 3's auth container deploy rides on

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- Entire `run.auth/webapp` app — magic-link (next-auth v5 + nodemailer→SES), embedded oidc-provider issuer, ElectroDB single-table adapter, Altcha login captcha: all ported (D-08/D-09)
- Phase-2 DynamoDB unit renders with `tables = {}` — Phase 3 adds `tiers` + `access_codes` by editing the dynamodb service.hcl only (no new module)
- `apps/voice/` monorepo pattern (uv/python) — `apps/auth/` mirrors the layout for the Node app

### Established Patterns
- Secrets: SOPS→SSM SecureString consumed by containers via `valueFrom`; run.auth's `from-aws.tmpl` env-injection pattern maps to `/kmv/secrets/use1/*`
- Single-table ElectroDB with typed entities (oidc-adapter, auth-profile, client) — access_codes joins as another entity
- Deploy: kmv-auth-app ECR + github-oidc terragrunt/build/deploy workflows already exist (Phase 2) — auth container is the first real push through them

### Integration Points
- **The token is the Phase-4 seam:** voice service's `/api/offer` presents this JWT access token; PyJWT validates issuer+audience offline via the JWKS endpoint and reads tier_id+group
- oidc-provider `findAccount`→`claims()` is where tier/group claims get injected (resolved from the login-time code → tier)
- `voice` registered as an OIDC client (authorization code + PKCE) — the browser client (Phase 5) redirects here

</code_context>

<specifics>
## Specific Ideas

- Seed codes from the design spec: `demo` → 2-minute tier; `kphdemo123` → 30-minute tier;
  unknown/blank → `no-access`.
- run.auth is proven in production at DEF CON — the port's north star is "get the known-good
  app green in klanker-voice, then trim," not a rewrite.
- The interstitial confirm-click page (AUTH-01) exists so corporate link-scanners don't
  consume magic-link tokens — verify run.auth already has this or it's net-new.

</specifics>

<deferred>
## Deferred Ideas

- **Dependency bump** to CLAUDE.md pins (next-auth beta.31 / oidc-provider 9.8.6 / electrodb
  3.9.1) — explicitly a separate task AFTER the port is green (D-08).
- **All quota enforcement** — usage table, ticking, blocking, concurrency, kill-switch,
  spoken wind-down (Phase 4; QUOT-01..05, KV-03/04/05).
- **run.auth's DEF CON quota code** (user-quota.ts, quota.ts) — dropped; Phase 4 rebuilds
  against the design-spec schema.
- **Live session inspection** (`kv sessions`, KV-06) — deferred until a multi-user event.

</deferred>

---

*Phase: 3-Auth Service & Access Codes*
*Context gathered: 2026-07-05*
