# Bypass "/join" auto-login — design

**Status:** approved (interview 2026-07-10). **Scope:** a per-access-code URL that
auto-logs a visitor in as an *anonymous* user in that code's tier, skipping the
magic-link email step. For conference demos: hand out one slick URL, visitor is
instantly on the mic.

## Decisions (interview-validated)

1. **Quota/identity: unique per visit.** Each visit mints its own anonymous
   identity `sub = anon:<code>:<uuid>`, so everyone gets a fair fresh session and
   its own quota bucket. Bounded by the tier's `session_max_seconds` +
   `max_concurrent` + global kill-switch. (Quota is keyed on `sub`, so a shared
   sub would collide on `max_concurrent` immediately — unique sub is required.)
2. **Opt-in per code.** New `bypassEnabled` flag on the `AccessCode` entity. Only
   flagged codes can be joined by URL; every other code still requires email.
3. **Separate unguessable token.** The URL uses a `bypassToken` (12-char base62,
   ~71 bits), NOT the human code (`kphdemo123` stays predictable + email-only).
   Regenerating the token instantly revokes the old URL.
4. **Auth-hosted redirect.** `/join/<token>` lives on the auth app; it mints the
   token and 302-redirects to the voice app carrying the JWT in the URL
   **fragment** (kept out of server logs / Referer). No CORS.
5. **Token TTL: 1 hour** (matches the existing OIDC access-token TTL). The URL
   itself keeps working (re-mints each visit) until `bypassEnabled` is turned off.

## Why the voice service barely changes

The voice service already: validates ANY RS256 JWT via JWKS (issuer/audience/exp),
reads only `sub` + `tier_id` claims, and keys all quota on `sub`. So a bypass token
that is a **real, correctly-signed OIDC token** with `sub=anon:*` and the code's
`tier_id` flows through the existing gate untouched — no new bypass path on the
metered service (unlike the `KMV_SMOKE_SERVICE_TOKEN` shortcut, which we do NOT
extend). `bypass_accounting=False`, so the tier's real quota applies.

## Token contract (MUST match exactly or voice rejects)

Signed with the OIDC signing key from `OIDC_JWKS` (SSM `/kmv/secrets/use1/oidc/jwks`),
using that key's `kid` (so voice's `PyJWKClient` resolves it):

| claim | value |
|-------|-------|
| `iss` | `${AUTH_PUBLIC_URL}${routePrefix}` = `https://auth.klankermaker.ai/use1/api/oidc` |
| `aud` | `config.oidc.voiceAudience` = `https://voice.klankermaker.ai` |
| `sub` | `anon:<code>:<uuid>` (unique per visit) |
| `exp` | now + 3600s |
| `iat`/`nbf` | now |
| `https://klankermaker.ai/tier_id` | the code's `tierId` |
| `https://klankermaker.ai/group` | the code's `group` (or null) |
| `alg` (header) | `RS256`; `kid` from the JWKS |

## Components

### Auth app (`apps/auth/webapp`)
- **Entity** (`entities/access-code.ts`): add `bypassEnabled: boolean` (default
  false) + `bypassToken: string?`. Add a GSI (`byBypassToken`) keyed
  `pk="bypass#${bypassToken}"` for O(1) lookup. Blank token → not indexed.
- **`resolveBypassToken(token)`**: GSI lookup → returns `{code, tierId, group}` or
  null. Uniform null on missing/disabled/expired — no enumeration oracle.
- **`mintAnonToken({code, tierId, group})`** (`lib/bypass-token.ts`, uses `jose`):
  loads the private JWK from `OIDC_JWKS`, `SignJWT` with the contract above.
- **Route** `app/[region]/join/[token]/route.ts` (or app-router equivalent): GET →
  `resolveBypassToken` → 404 uniform if absent/disabled → `mintAnonToken` →
  302 `Location: https://voice.klankermaker.ai/callback#access_token=<jwt>&token_type=bearer&expires_in=3600&anon=1`.
- Add `jose` as a direct dependency.

### Voice client (`apps/voice/client`)
- **Fragment ingestion**: on `/callback`, if `location.hash` carries
  `access_token`, `setToken(accessToken)`, mark returning, `replaceState("/")`,
  become authenticated — bypassing the PKCE `exchangeCode` path. Clear the hash so
  the token doesn't linger in the URL.

### `kv` CLI (`kv/internal/app/cmd/code.go`)
- **`kv codes bypass <code>`**: sets `bypassEnabled=true`, generates+stores a
  `bypassToken` (12-char base62), prints the ready URL
  `https://voice.klankermaker.ai/join/<token>`. `--disable` clears the flag/token
  (revokes). `--rotate` mints a fresh token (revokes the old URL).

## "Anonymous" in transcripts

There is no server-side transcript ledger today (future S3/Athena work); the voice
service never receives email (only `sub`). So "anonymous" is fully expressed by the
`anon:` sub prefix. When the ledger lands: `anon:*` subs render as Anonymous;
real subs resolve to email via the auth DB. Nothing to sanitize now.

## Security notes

- The `/join` endpoint can ONLY mint `sub=anon:*` at the **looked-up code's** tier
  — no caller-supplied tier, no escalation surface.
- `bypassToken` is a bearer credential in a URL: treat like a password; rotate to
  revoke; opt-in per code limits blast radius.
- Minted tokens are short-lived (1h) and not tracked by oidc-provider's grant store
  — acceptable for ephemeral anonymous sessions; `bypassEnabled=false` is the
  revocation control for the URL itself.
- Rate/spend bound is unchanged: tier `session_max`/`max_concurrent`/daily +
  global kill-switch all still apply (`bypass_accounting=False`).

## Out of scope (V1)

Admin-panel UI for bypass (kv CLI covers generation); transcript ledger itself;
adding email to normal tokens (the ledger will join sub→email offline).
