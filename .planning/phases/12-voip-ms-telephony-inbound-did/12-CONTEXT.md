# Phase 12: VoIP.ms Telephony — Inbound DID - Context

**Gathered:** 2026-07-12
**Status:** Ready for planning
**Source:** Derived from the authoritative telephony spec (`docs/superpowers/specs/2026-07-11-voipms-telephony-integration.md` §4, §11, §19-D, §23, §24, §25) atop the Phase 9/10/11 telephony stack. Six gray areas were decided interactively (D-01..D-06); the spec provides the locked frame (subaccount, DID routing, codec, security controls, mint-path reuse). Grounded against the live code: the bypass `/join` mint machinery (`apps/auth/webapp/src/lib/bypass-token.ts` `mintAnonToken`, `apps/auth/webapp/src/entities/access-code.ts` `resolveAccessCode` + sparse `byBypassToken` gsi2, `apps/auth/webapp/src/app/join/[token]/route.ts`, `kv/internal/app/cmd/code.go` `code bypass`), the Phase 11 Asterisk edge (`apps/voice/asterisk/pjsip.conf` currently only a local `dev-softphone` AOR), the Phase 11 §24 gate + minimal `CallIdentity` seam, and the Phase 9 `create_call_session(...)` runtime.

> **Scope note — two Phase-14 pieces intentionally pulled forward.** The user chose to (a) stand up the cloud `telephony-edge` deploy in this phase rather than test against a locally-exposed Asterisk, and (b) do full SSM secret wiring now. This is deliberate: a **public** DID handed to a DEF CON audience must reach a **reachable, secret-safe, hardened-enough** edge, and testing "reliably reaches Klanker from the cellular network" (the §19-D exit criterion) is only meaningful against the real deployed topology. The Phase 12↔14 boundary is re-drawn explicitly in D-01 so Phase 14 (Production Hardening) stays substantive.

<domain>
## Phase Boundary

**This phase delivers (spec Phase D — VoIP.ms inbound DID):**
A **public VoIP.ms DID reliably reaches the existing Klanker agent from the cellular network**, on a **deployed, SSM-backed, inbound-only Asterisk edge**, with the §23 caller-ID → access-code → tier identity in front of the Phase-11 §24 gate. Concretely, this phase adds:

1. **VoIP.ms provisioning** (§4 / §25.F): a dedicated `klanker-pbx` subaccount (strong unique SIP password, IP-restricted to the edge egress + POP, **outbound disabled**), one ordered DID (Toronto POP, per-minute, CNAM off) routed to `klanker-pbx` via **registration-based trunking**, international/premium destinations locked down, balance low + auto-recharge off + spend/low-balance alerts, portal 2FA, API IP-whitelist. Split between **`kv voipms` automation** (the API-drivable steps) and a **documented operator runbook** (the portal-only steps) — see D-03.
2. **Asterisk VoIP.ms trunk** (extends the Phase-11 `pjsip.conf`): a PJSIP **registration** + trunk/AOR to the chosen VoIP.ms POP so Asterisk registers **outbound** to VoIP.ms (no public inbound SIP port required), and inbound DID calls arrive over the registered leg into the existing narrow inbound-only Stasis app. Same POP for registration and DID routing (§4).
3. **The §23 caller-ID → code → tier mint path** (reuses the bypass `/join` machinery — D-02): a `phone` attribute + sparse `byPhone` GSI on the `AccessCode` entity (mirror of `bypassToken`/`byBypassToken`), a **private, internal-only** auth-app endpoint that resolves a normalized E.164 caller ID → code → tier and mints an OIDC token via `mintAnonToken` (`aud=voice`, namespaced `tier_id`/`group`, `sub = "tel:<code>:<uuid>"`), and a `kv code phone <code> --add <e164>` operator command (mirror of `kv code bypass`). The Asterisk controller passes the **normalized** caller ID and calls the endpoint.
4. **Tier composition + seed data** (D-05): caller-ID mint grants at most a **constrained baseline tier**; the Phase-11 silent gate (DTMF PIN / 4-word passphrase, verified outside the LLM) is the **only** path to `kph-tier`/high tiers. Seed the `kph-tier` `Tier` row (effectively unlimited `sessionMaxSeconds`/`periodMaxSeconds`/`maxConcurrent`) + Kurt's phone → `defcon34` mapping via the new `kv code phone` command.
5. **Cloud `telephony-edge` deploy** (pulled forward from §15/Phase 14, scoped to "working secure edge" — D-01): an isolated deployed Asterisk edge (public egress IP for the registration trunk) with a **security group locked to VoIP.ms POP IP ranges** (never open SIP/RTP to the internet), inbound-only, ARI private-network-only. This is the "just secure enough to safely expose a public DID" slice; the *ops* hardening (alarms/dashboards, fail2ban, TLS/SRTP, load test, failure-routing polish, runbook) stays Phase 14.
6. **Full SSM secret wiring now** (pulled forward — D-04): `VOIPMS_SIP_*`, the DID, the `/tel` endpoint auth token, and the existing `ASTERISK_ARI_*` / `TELEPHONY_ACCESS_PIN` / `TELEPHONY_PASSPHRASE_WORDS` move to SSM SecureString consumed via `valueFrom` — nothing public-facing lives in env. Satisfies SC#1 ("secrets live only in SSM").
7. **Quota enforcement via the existing `SessionLifecycle`** (§11): 1 concurrent PSTN call, short max duration (~10 min), small daily minute cap, outbound disabled — no independent telephone timer.

**Exit criterion (spec §19-D):** a call from a **real mobile phone** to the public DID reliably reaches the agent, completes a multi-turn conversation, and hangs up cleanly from either side. Fail-closed: a scanner/unknown caller who fails the §24 gate burns **no** STT/LLM/TTS quota and gets a static goodbye + hangup; Klanker unavailable → static unavailable message, never a silent open call.

**Explicitly OUT of scope for this phase (stays Phase 14/F — see D-01):**
- Alarms + dashboards (`ActivePstnCalls`, gate-fail rate, ANY-outbound-attempt, balance drop, registration failures), fail2ban on the Asterisk host, TLS/SRTP end-to-end, load/concurrency test, failure-routing polish, and the operations runbook (revoke/rotate credential, kill-switch DID routing, debug one-way audio).
- Any Terraform/IaC *cleanup/refactor* beyond the minimal isolated `telephony-edge` deploy needed to expose the DID securely.

**Explicitly OUT of scope (other phases / never):**
- Physical payphone / ATA on `payphone-ata` subaccount + gain/DTMF/echo tuning (Phase 13/E).
- NO outbound calling anywhere — VoIP.ms subaccount outbound disabled, no outbound dialplan context, ever (§25.A).
- NO change to the Phase-11 §24 gate mechanism, the Phase-10 media/codec/transport, or the Phase-9 runtime seam — Phase 12 wires a new identity SOURCE (caller-ID) and a real trunk in front of them, it does not rebuild them.
- NO new STT/LLM/TTS provider construction — `factories.py` remains the single source (§22).
- NO caller-driven tool use; the LLM never decides authentication (§18/§25.D).
- Call recording OFF by default (§18/§25.D — Canada two-party consent).
</domain>

<decisions>
## Implementation Decisions

Spec §4/§11/§23/§24/§25 provide the locked frame (subaccount shape, DID routing, codec, security controls, mint-path reuse). D-01..D-06 are the gray areas decided in this discussion.

### Phase 12 ↔ Phase 14 boundary (gray area — DECIDED: "12 = working secure edge; 14 = ops hardening")
- **D-01 — The cloud `telephony-edge` deploy is pulled forward, scoped to the minimum secure edge.** Phase 12 delivers a **deployed, SSM-backed, inbound-only** Asterisk edge with a **security group locked to VoIP.ms POP IP ranges** (table-stakes to expose a public DID without inviting DEF CON scanners), ARI private-network-only, plus the cellular exit proof. Phase 14 (Production Hardening) retains: alarms/dashboards, fail2ban, TLS/SRTP, load/concurrency test, failure-routing polish, and the operations runbook. This keeps Phase 14 substantive rather than hollow. **Rationale:** the §19-D exit criterion ("public DID **reliably** reaches Klanker from the cellular network") is untestable against a local softphone-only harness — it requires a real reachable edge. But loose hardening on a public DID during the roadmap gap is unacceptable at DEF CON, so SG-lock-to-POP + inbound-only + private ARI come *now*; the observability/anti-abuse *polish* is Phase 14.
  - **Registration-based trunking** (§4) is the mechanism: Asterisk registers **outbound** to the VoIP.ms POP, so VoIP.ms delivers the DID over the registered leg and **no public inbound SIP port is needed** — the SG still locks SIP/RTP to POP ranges as defense-in-depth. URI routing is the documented alternative, not the default.

### The §23 mint-path integration boundary (gray area — DECIDED: "Private auth endpoint mirroring /join")
- **D-02 — A new INTERNAL-only auth-app endpoint mints the token from a normalized caller ID, mirroring `/join/[token]`.** The Asterisk controller is Python; the mint path (`mintAnonToken`, `jose`, `OIDC_JWKS`) is TS in the auth app. So the controller passes the **normalized E.164 caller ID** to a new auth-app route (e.g. `GET /tel/<e164>`, basePath-prefixed like `/use1/...`) that: resolves phone → code via the new `byPhone` GSI → `resolveAccessCode(code)` → `mintAnonToken({ tierId, group, sub: "tel:<code>:<uuid>" })` → returns the short-lived token. The minted token validates in the voice service **unchanged** (same issuer/aud/jwks/kid as bypass — the whole point of reusing the path).
  - **Data model** (mirror the bypass gsi2 work): add a `phone` attribute + a **sparse** `byPhone` GSI (pk template e.g. `phone#${phone}`, sk `phone#`) to the `AccessCode` entity. **Normalize to canonical E.164 digit form before it becomes a key** (ElectroDB lowercases keys by default; casing is moot for digits but normalization is not — strip `+`/spaces/dashes to a single canonical form on both write and lookup). Bypass-less/phone-less codes stay unindexed (sparse), exactly like `byBypassToken`.
  - **SECURITY — this endpoint is a token-minting oracle and MUST NOT be internet-exposed like `/join`.** Lock it to the telephony edge / private network (network ACL + a shared `/tel` endpoint auth token in SSM, D-04). Preserve the bypass **no-oracle** contract: every failure mode (unknown caller ID, disabled code, over-cap) returns an indistinguishable response — never leak whether a number is mapped.
  - **CAVEAT (§23):** caller ID is spoofable → the mint grants **at most the constrained baseline tier** (D-05). Never bind `kph-tier`/high tiers to caller ID alone.

### VoIP.ms provisioning surface (gray area — DECIDED: "kv voipms automation + operator runbook")
- **D-03 — Split provisioning: `kv voipms` automates the API-drivable steps; a runbook documents the portal-only security steps.** Add a `kv voipms` command family (aws-sdk-style, using the VoIP.ms REST API) for the automatable/repeatable steps: create the `klanker-pbx` subaccount (strong unique SIP password, IP-restricted, outbound disabled), route the DID → `klanker-pbx`, set per-call max duration + caps, read balance. Write an **operator runbook** for the portal-first steps that the API cannot/should-not do (§25.F order): enable **2FA** + strong portal password → lock international/premium restrictions → set **balance low + auto-recharge OFF + alerts** → enable API with a strong `api_password` (≠ portal) + **whitelist the setup IP** → (then the `kv voipms` API steps) → **order ONE DID** (Toronto POP, per-minute, CNAM off) → route DID → **re-lock the API IP whitelist**. **Rationale:** the portal-first steps are exactly the ones you don't want to skip or under-document; the API steps benefit from being repeatable/scriptable. The VoIP.ms `api_password` + `VOIPMS_SIP_*` live in SSM (D-04), never git.

### Secrets handling (gray area — DECIDED: "Full SSM + wiring now")
- **D-04 — Full SSM SecureString + `valueFrom` wiring lands this phase.** Nothing public-facing lives in env. New/migrated secrets → SSM: `VOIPMS_SIP_USERNAME`/`VOIPMS_SIP_PASSWORD` (+ any `VOIPMS_API_*` for `kv voipms`), the DID, the `/tel` endpoint auth token (D-02), and the Phase-11 secrets promoted from local env (`ASTERISK_ARI_URL`/`USERNAME`/`PASSWORD`, `TELEPHONY_ACCESS_PIN`, `TELEPHONY_PASSPHRASE_WORDS`). The SIP password is consumed by **Asterisk** (rendered into its gitignored config at container start, per the Phase-11 secret-render pattern), **not** passed into the Klanker Python process. Extend the `config.py` credential-name rejection to cover the new secret-looking fields. **Rationale:** satisfies SC#1 literally ("secrets live only in SSM") and is a prerequisite for the deployed edge (D-01) — a deployed container consumes secrets via `valueFrom`, not env files. Follows the SOPS → SSM SecureString pattern from CLAUDE.md / defcon.run.34.

### Tier composition + seed data (gray area — DECIDED: "Baseline mint + gate upgrade + seed kph-tier & Kurt's map")
- **D-05 — Two-source identity: caller-ID baseline, gate upgrade.** Caller-ID → code → mint (D-02) grants **at most a constrained baseline tier** (§11 limits: 1 concurrent, ~10 min, small daily cap). The Phase-11 silent §24 gate (DTMF PIN / 4-word passphrase, verified outside the LLM) is the **ONLY** path to `kph-tier`/high tiers — a passing gate is what UPGRADES a caller-ID-mapped capped tier. Unknown caller IDs → default minimal tier or reject; never an open grant. This composes the Phase-11 gate's tier-grant seam (D-05a there) with the new caller-ID identity source: the caller-ID mint provides the *baseline* token/identity; the gate's PIN/phrase → tier grant is what overrides it upward on unlock.
  - **REJECTED alternative (recorded for clarity):** letting caller-ID mint the code's *actual* tier (incl. `kph-tier`) directly — §23's security caveat forbids it (caller ID is spoofable; spoofing Kurt's number would grant unlimited metered access).
  - **Seed data is a Phase-12 deliverable:** create the `kph-tier` `Tier` row (effectively unlimited `sessionMaxSeconds`/`periodMaxSeconds`/`maxConcurrent`) and seed Kurt's phone → `defcon34` mapping via the new `kv code phone <code> --add <e164>` command. (`defcon34` → `kph-tier` per §23.)

### Exit-criterion proof + CI artifacts (gray area — DECIDED: "Manual cellular proof + auth/kv/config unit tests")
- **D-06 — Manual documented cellular call is the exit proof; the code parts get automated CI tests.** The §19-D exit ("public DID reliably reaches Klanker from a real mobile phone, multi-turn, clean hangup either side; fail-closed for scanners") is inherently manual — run once against the deployed edge with real VoIP.ms + a real cell phone, documented in the phase SUMMARY + the provisioning runbook (mirrors the Phase-11 §19-C manual softphone proof). **Required CI artifacts** for the code deliverables: auth-app unit tests for the phone → code → token mint path (`byPhone` sparse-GSI query, E.164 normalization on write+lookup, the `/tel` endpoint, and the no-oracle failure modes), `kv code phone` + `kv voipms` command tests, and Asterisk **registration-config validation** (the trunk/registration renders + parses; no secrets in the committed config). Keep the full existing suite green (Phase 9/10/11 offline + lifecycle + gate tests untouched).

### Claude's Discretion
- Exact `/tel` endpoint path/shape and how the private-network lock is enforced (network ACL vs shared bearer token vs both) — subject to D-02's no-oracle + not-internet-exposed constraints.
- Exact `byPhone` GSI index name / key templates and the E.164 normalization helper's location (shared lib vs entity-local).
- Exact `kv voipms` sub-command tree and which VoIP.ms API calls are wrapped vs left to the runbook (planner discretion within D-03's split).
- The specific Terraform/Terragrunt module shape for the minimal `telephony-edge` deploy — reuse the defcon.run.34 conventions; the researcher confirms the current repo IaC layout.
- Which VoIP.ms POP IP ranges the SG allow-list uses and how they're sourced/kept current (documented value vs data source).
- Whether `kph-tier` seeding is a migration/seed script vs a `kv` invocation captured in the runbook.

### Reviewed Todos (not folded)
- No new todo matches surfaced for this phase beyond the ledger item already reviewed in Phase 11 (out of scope here — Phase 12 introduces no transcript-ledger work; the pre-unlock redaction guarantee lives in the Phase-11 gate).
</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Authoritative spec
- `docs/superpowers/specs/2026-07-11-voipms-telephony-integration.md` —
  - §4 (VoIP.ms configuration: dedicated `klanker-pbx` subaccount, dedicated SIP password, single POP for registration + DID, PCMU codec, security controls, registration-based trunking preferred over URI routing),
  - §11 (identity/auth/quota: `CallIdentity`, MVP `pstn:` subject vs the §23 refinement, quota mapping — 1 concurrent, ~10 min, small daily cap, outbound disabled, reuse `SessionLifecycle`),
  - §15 (infrastructure — network / security groups / deployment isolation / observability; Phase 12 pulls the *deploy + SG-to-POP* forward, leaves observability to Phase 14),
  - §19-D (Phase D definition + exit criterion "public DID reliably reaches Klanker"),
  - §23 (pre-established phone → code → tier identity: reuse `resolveAccessCode` + `mintAnonToken`, `phone` attr + `byPhone` GSI, `kv code phone`, `sub = "tel:<code>:<uuid>"`, `kph-tier` unlimited row, **caller-ID-alone → minimal tier; second factor required for high tiers**),
  - §24 (the silent gate — built in Phase 11; Phase 12 sits caller-ID identity in front of it; unlock is what upgrades to `kph-tier`),
  - §25 (hostile/DEF CON hardening — §25.A inbound-only/no outbound; §25.B portal+API security & IP whitelist; §25.C SIP edge hardening & SG-to-POP-ranges & private ARI; §25.D caller-side threats & spoofable caller ID; §25.F blank-account setup order — the runbook spine).

### Code to read before planning/implementing (verified present)
- `apps/auth/webapp/src/lib/bypass-token.ts` — `mintAnonToken` (RS256, `OIDC_JWKS`, `aud=voice`, namespaced `tier_id`/`group`, `kid` compat) — the mint the `/tel` endpoint reuses (D-02).
- `apps/auth/webapp/src/entities/access-code.ts` — `AccessCode` entity, `resolveAccessCode`, the sparse `byBypassToken` gsi2 with explicit key templates + casing notes — the exact pattern the new `phone`/`byPhone` GSI mirrors (D-02).
- `apps/auth/webapp/src/app/join/[token]/route.ts` — the `/join` route (resolve → mint → return, no-oracle failure handling) — the template for the private `/tel` endpoint (D-02); note the Phase-12 endpoint is **internal-only**, unlike public `/join`.
- `kv/internal/app/cmd/code.go` + `kv/internal/app/cmd/bypass_test.go` + `kv/internal/app/electro/keys.go` — the `kv code bypass` command + electro key writers — the template for `kv code phone` (D-05) and the electro `byPhone` key writes.
- `apps/voice/asterisk/pjsip.conf` / `ari.conf` / `extensions.conf` (+ `README.md`) — Phase-11 configs; `pjsip.conf` currently has only a local `dev-softphone` AOR. Phase 12 adds the VoIP.ms registration/trunk/AOR to the chosen POP (D-01) and the secret-render-at-container-start pattern for the SIP password (D-04).
- `apps/voice/src/klanker_voice/telephony/controller.py` — the Phase-11 `AsteriskCallController`; Phase 12 wires the normalized caller ID → `/tel` mint call → identity used by `create_call_session(...)`.
- `apps/voice/src/klanker_voice/telephony/config.py` + `config.py` — the `[telephony]` loader + credential-name rejection to extend for the new SSM secret fields (D-04).
- `apps/voice/src/klanker_voice/call_runtime.py` / `session.py` — Phase-9 `create_call_session(*, transport, identity, ...)` + `SessionLifecycle` (quota: 1 concurrent, hard timer) reused unchanged (§11 quota).
- The Phase-11 §24 gate module (`GateProcessor` + PIN/passphrase + the D-05a tier-grant seam) — Phase 12 composes caller-ID baseline identity with this gate's unlock-upgrade (D-05); do NOT modify the gate mechanism.

### Infrastructure references
- `.claude/CLAUDE.md` — stack pins, SOPS → SSM SecureString / `valueFrom` pattern, Terraform/Terragrunt "match defcon.run.34 pins", budget/security constraints (public entry wired to metered APIs must be quota-gated), naming ("klanker-voice", never "voiceai").
- The repo's existing Terragrunt layout for services (the `telephony-edge` module follows it — researcher confirms the live path, e.g. `infra/terraform/live/.../services/telephony-edge/`).

### Phase artifacts (direct dependencies)
- `.planning/phases/11-voip-ms-telephony-local-asterisk-edge/11-CONTEXT.md` + `11-0X-SUMMARY.md` — the Asterisk edge, the §24 gate + minimal `CallIdentity` seam (D-05a there), the docker-compose harness, and the secret-render-at-start pattern Phase 12 extends to SSM.
- `.planning/phases/09-.../09-CONTEXT.md` + `.planning/phases/10-.../10-CONTEXT.md` — the runtime seam + telephony media package Phase 12 sits on top of, unchanged.
- The bypass-join design doc `docs/superpowers/specs/2026-07-10-bypass-join-login-design.md` — the shipped feature §23 mirrors (mint path, sparse GSI, `kv` command).
</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- **Bypass `/join` mint machinery** — `mintAnonToken`, `resolveAccessCode`, the sparse `byBypassToken` gsi2 pattern, and `kv code bypass` are the *direct* templates for the §23 caller-ID mint (`/tel` endpoint, `byPhone` GSI, `kv code phone`). Minted tokens validate in the voice service unchanged (same iss/aud/jwks/kid).
- **Phase-11 Asterisk edge + secret-render pattern** — `pjsip.conf`/`ari.conf`/`extensions.conf`, the docker-compose harness, and the "render real secrets into gitignored configs at container start" mechanism (recent commit `afb0c96`) extend to the VoIP.ms trunk + SSM.
- **Phase-11 §24 gate + `CallIdentity` seam** — reused as-is; Phase 12 supplies the caller-ID baseline identity the gate upgrades on unlock.
- **`SessionLifecycle` / `create_call_session(...)`** — quota (1 concurrent, hard timer) + the single idempotent close reused verbatim (§11 — no independent telephone timer).

### Established Patterns
- **Sparse GSI + explicit key templates** (`byBypassToken`) — the `byPhone` GSI copies it exactly, including the ElectroDB casing/normalization discipline (normalize E.164 before it's a key).
- **No-oracle failure contract** (`resolveAccessCode` / `/join`) — the `/tel` endpoint returns indistinguishable failures for unknown/disabled/over-cap so a caller can't probe which numbers are mapped.
- **`kv code <sub>` command shape** (cobra, aws-sdk-go-v2, electro key writers) — `kv code phone` and `kv voipms` mirror it.
- **Secrets never surfaced as tunables** — extend `config.py` credential-name rejection to the new SSM fields (mirrors Phase-11 D-09).
- **Transport/edge isolation** — the telephony edge stays a separate deployed service; the browser `voice`/`auth` paths are untouched.

### Integration Points
- **VoIP.ms REST API** ↔ `kv voipms` (subaccount create, DID route, caps, balance) (D-03).
- **VoIP.ms POP (SIP registration + RTP)** ↔ the deployed Asterisk edge (registration-based trunk, SG locked to POP ranges) (D-01).
- **Asterisk controller** ↔ the private `/tel` auth endpoint (normalized caller ID → minted token → `create_call_session` identity) (D-02).
- **`AccessCode` `byPhone` GSI** ↔ `resolveAccessCode` + the `/tel` endpoint + `kv code phone` (D-02, D-05).
- **SSM SecureString** ↔ the deployed edge (`valueFrom`) + the SIP-password container-render + `kv voipms` API creds (D-04).
- **Phase-11 §24 gate** ↔ the caller-ID baseline identity (gate unlock upgrades tier) (D-05).
</code_context>

<specifics>
## Specific Ideas

Suggested build order (provision the account safely first, then the code that rides it, then the one manual cellular proof):
1. Read the code list above — especially the bypass `/join` trio (mint / entity+GSI / `kv code bypass`) and the Phase-11 Asterisk configs + gate + secret-render pattern.
2. **VoIP.ms account setup runbook (§25.F order) + `kv voipms`** (D-03): portal-first security steps documented; API-drivable steps automated. Order ONE DID (Toronto POP, per-minute, CNAM off). Keep balance low, auto-recharge off, outbound disabled.
3. **SSM wiring** (D-04): all public-facing secrets to SSM SecureString + `valueFrom`; extend credential-name rejection.
4. **Auth app §23 mint path** (D-02): `phone` attr + sparse `byPhone` GSI + E.164 normalization; the private, no-oracle `/tel` endpoint; `kv code phone` command; seed `kph-tier` + Kurt's mapping (D-05). Unit tests (mint path, GSI, normalization, no-oracle).
5. **Asterisk VoIP.ms trunk** (D-01): PJSIP registration/trunk/AOR to the POP (outbound register, no public inbound port); render SIP password from SSM at container start; config-validation test.
6. **Controller wiring** (D-02/D-05): normalized caller ID → `/tel` mint → baseline identity → `create_call_session`; the Phase-11 gate upgrades to `kph-tier` on unlock. Fail-closed for unknown/gate-fail (static goodbye + hangup, no quota burn).
7. **Minimal secure `telephony-edge` deploy** (D-01): isolated deployed edge, SG locked to VoIP.ms POP ranges, ARI private-only, inbound-only.
8. **Manual §19-D cellular proof** (D-06): real cell → public DID → multi-turn → clean hangup either side; scanner/gate-fail → fail-closed. Documented in SUMMARY + runbook.
9. **Stop at the working secure edge** — alarms/dashboards, fail2ban, TLS/SRTP, load test, failure-routing polish, and the ops runbook are Phase 14 (D-01).

Constraint reminders the planner must honor: inbound-only / no outbound context ever (§25.A); caller-ID alone → baseline tier only, gate required for `kph-tier` (§23/§25.D); the `/tel` endpoint is a minting oracle — internal-only, no-oracle failures; caps at both VoIP.ms and `SessionLifecycle`; secrets in SSM only, never git/`pipeline.toml`/logs; the PIN/passphrase + pre-unlock transcript never enter LLM context (Phase-11 gate guarantee, unchanged).
</specifics>

<deferred>
## Deferred Ideas

Later roadmap phases — NOT here:
- **Phase 13 (spec E):** physical payphone on its own `payphone-ata` subaccount; ATA gain/DTMF/echo tuning; VoIP.ms echo test.
- **Phase 14 (spec F) — the ops hardening retained by D-01:** alarms + dashboards (`ActivePstnCalls`, gate-fail rate, ANY-outbound-attempt, balance drop, registration failures), fail2ban on the Asterisk host, TLS/SRTP end-to-end, load/concurrency test, failure-routing polish, the operations runbook (revoke/rotate SIP credential, kill-switch DID routing, debug one-way audio), and any Terraform/IaC cleanup beyond the minimal `telephony-edge` deploy.
- **G.722 HD SIP-to-SIP audio** (§4) — narrowband PCMU is the Phase-12 codec; HD is a later option.
- **Pre-rendered PSTN greeting clip** (§12) — after the canonical `greet_now()` path is proven on PSTN.

### Reviewed Todos (not folded)
None — discussion stayed within phase scope.

</deferred>

---

*Phase: 12-voip-ms-telephony-inbound-did*
*Context gathered: 2026-07-12 — `/gsd-discuss-phase` (6 gray areas decided: D-01 pull cloud edge forward as "working secure edge" / D-02 private /tel mint endpoint / D-03 kv voipms + runbook / D-04 full SSM now / D-05 caller-ID baseline + gate upgrade + seed kph-tier / D-06 manual cellular proof + CI unit tests) atop the telephony spec §4/§11/§19-D/§23/§24/§25, grounded against the bypass /join mint machinery + the Phase 9/10/11 telephony stack*
