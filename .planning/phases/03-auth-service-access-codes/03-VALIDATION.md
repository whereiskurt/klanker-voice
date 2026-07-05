---
phase: 3
slug: auth-service-access-codes
status: ready
nyquist_compliant: true
wave_0_complete: false
created: 2026-07-05
updated: 2026-07-05
---

# Phase 3 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.
> Authored from the `## Validation Architecture` section of `03-RESEARCH.md` (Nyquist gate:
> `workflow.nyquist_validation` is true). Two runtimes: the ported Next.js webapp (Vitest)
> and the new `kv` Go CLI (`go test`).

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Vitest 4.1.9 (webapp — `vitest.config.ts` ported from run.auth); Go `testing` + table tests (kv, mirrors km's `_test.go` suites) |
| **Config file** | `apps/auth/webapp/vitest.config.ts` (ported in Wave 0); `kv` uses standard `go test` (no config) |
| **Quick run command** | `cd apps/auth/webapp && npm test` · `cd kv && go test ./...` |
| **Full suite command** | same (both are fast unit/integration suites; DynamoDB integration runs against dynamodb-local) |
| **Estimated runtime** | webapp ~seconds; kv ~seconds; dynamodb-local round-trip ~10–20s incl. container start |

---

## Sampling Rate

- **After every task commit:** `npm test` (webapp) or `go test ./...` (kv) for the touched module + forbidden-string grep (`! grep -riq "voiceai\|kmk" apps/auth kv`)
- **After every plan wave:** both suites green
- **Before `/gsd-verify-work`:** both suites green AND a manual staging check that a real `/token` response decodes to a JWT with the expected `aud=https://voice.klankermaker.ai`, RS256 header, and `tier_id`/`group` claims
- **Max feedback latency:** seconds for unit suites; ~20s for the dynamodb-local round-trip (acceptable — it is the critical Pitfall-1 key-compat gate)

---

## Per-Task Verification Map

| Req ID | Plan | Wave | Behavior | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|--------|------|------|----------|------------|-----------------|-----------|-------------------|-------------|--------|
| AUTH-01 | 03-01 | 1 | Confirm page does not consume the magic-link token on GET/prefetch | T-03-01 | Scanner GET is inert; token consumed only on explicit confirm click | integration | `npm test -- login-confirm` + manual prefetch check | ❌ W0 (net-new page) | ⬜ pending |
| AUTH-05 | 03-01 | 1 | Altcha verify + replay rejection on login | T-03-01 | Replayed/absent Altcha solution rejected | unit | `npm test -- login-altcha` | ❌ W0 | ⬜ pending |
| AUTH-03 | 03-02 | 2 | Known code→tier; blank/unknown→no-access; login always succeeds | T-03-02 | First-time known-code login yields correct tier claim (not no-access) | unit | `npm test -- access-code-resolution` | ❌ W0 | ⬜ pending |
| AUTH-04 | 03-02 | 2 | Expired / over-max code → no-access; redemption counts once per UNIQUE user | T-03-02 | Redemption race is conditional-write safe; per-user idempotent | unit | `npm test -- code-redemption` | ❌ W0 | ⬜ pending |
| AUTH-02 | 03-03 | 3 | JWT access token has `aud=voice`, `tier_id`, `group`, RS256 header; JWKS stable across fleet | T-03-03 | Offline RS256 validation succeeds against persistent SSM-sourced JWKS | unit | `npm test -- oidc-resource-token` | ❌ W0 | ⬜ pending |
| KV-01 | 03-04 | 3 | kv creates/lists/expires access codes; items ElectroDB reads back | T-03-04 | kv-written key format matches ElectroDB templates exactly | integration | `go test ./... -run KeyCompat` + Node read assertion (round-trip) | ❌ W0 (critical — Pitfall 1) | ⬜ pending |
| KV-02 | 03-04 | 3 | kv defines/lists tiers; items ElectroDB reads back | T-03-04 | Same key-format compat as KV-01 | integration | `go test ./... -run KeyCompat` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] Port `apps/auth/webapp/vitest.config.ts` from run.auth (webapp test harness).
- [ ] `access-code-resolution.test.ts`, `code-redemption.test.ts` — AUTH-03/04.
- [ ] `oidc-resource-token.test.ts` — mint a token, decode it, assert format/`aud`/claims (AUTH-02).
- [ ] `login-altcha.test.ts` — Altcha verify + replay (AUTH-05).
- [ ] `kv/internal/.../keys_test.go` + `roundtrip_test.go` (dynamodb-local) — bidirectional kv↔webapp key compatibility (Pitfall 1, blocking gate).
- [ ] DynamoDB test fixtures/mocks (dynamodb-local or aws-sdk mock).

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Corporate link-scanner does not burn the magic-link token | AUTH-01 | Requires a real scanning MUA / prefetching client to reproduce | Send a magic-link email to a scanned mailbox (or simulate HEAD/GET prefetch on the link); confirm the confirm-click page still lets a human complete login afterward |
| Live `/token` returns a JWT the voice service validates offline | AUTH-02 | End-to-end trust seam; needs the deployed issuer + a real JWKS fetch | On staging, run an authorization-code+PKCE flow for the `voice` client; decode the access token, assert `aud`, `iss`, RS256, `tier_id`, `group`; fetch JWKS and validate the signature offline (PyJWT) |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies (checker confirmed 8a–8d pass against every PLAN.md `<verify>` block)
- [x] Sampling continuity: no 3 consecutive tasks without automated verify (checker-confirmed)
- [x] Wave 0 covers all MISSING references (listed above)
- [x] No watch-mode flags (Vitest run mode, `go test` non-watch)
- [x] Feedback latency < 30s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** approved 2026-07-05
