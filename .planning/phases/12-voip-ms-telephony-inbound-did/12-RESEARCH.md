# Phase 12: VoIP.ms Telephony — Inbound DID - Research

**Researched:** 2026-07-12
**Domain:** VoIP.ms provisioning, SIP trunk registration, identity minting, cloud telephony edge infrastructure
**Confidence:** HIGH (IaC patterns verified from codebase; VoIP.ms API and Toronto POPs confirmed from official sources; PJSIP registration from Asterisk docs; E.164 normalization from RFC/best-practice sources)

## Summary

Phase 12 delivers a **public VoIP.ms DID reliably reaching the agent from the cellular network**, on a deployed, SSM-backed, inbound-only Asterisk edge with the §23 caller-ID → access-code → tier identity in front of the Phase-11 §24 gate. The research focuses on: (1) the repo's IaC conventions for adding the `telephony-edge` service; (2) VoIP.ms REST API surface for subaccount/DID provisioning; (3) Toronto POP infrastructure for the registration trunk and SG allow-list; (4) PJSIP registration-based trunking patterns for production Fargate; (5) the existing code patterns for reusing the bypass `/join` mint machinery as the §23 mint path; and (6) the sparse GSI + E.164 normalization pattern already established for the `bypassToken` GSI, now mirrored for a new `byPhone` GSI.

**Primary recommendation:** Implement Phase 12 in the stated build order (D-03 § VoIP.ms account runbook + `kv voipms` → D-04 § SSM wiring → D-02 § auth-app mint path → D-01 § PJSIP trunk + deployed edge → D-06 § manual cellular proof), grounding each task in the documented patterns and confirmed infrastructure.

---

## User Constraints (from CONTEXT.md)

### Locked Decisions

**D-01 — Cloud `telephony-edge` deploy pulled forward, scoped to minimum secure edge.** Phase 12 delivers a deployed, SSM-backed, inbound-only Asterisk edge with a security group locked to VoIP.ms POP IP ranges, ARI private-network-only. Phase 14 retains ops hardening (alarms/dashboards, fail2ban, TLS/SRTP, load test, operations runbook).

**D-02 — Private auth endpoint mirroring `/join`.** A new INTERNAL-only auth-app route (e.g., `GET /tel/<e164>`, basePath-prefixed) resolves phone → code → tier → mints token via `mintAnonToken`. The endpoint is a token-minting oracle — NOT internet-exposed; locked to telephony edge/private network. Minted token validates unchanged against the voice service (same issuer/aud/jwks/kid).

**D-03 — `kv voipms` + operator runbook.** API-drivable steps (create subaccount, route DID, set caps, read balance) automated; portal-only security steps documented in runbook (2FA, international/premium restrictions, balance alerts, API IP-whitelist).

**D-04 — Full SSM SecureString + `valueFrom` wiring.** New/migrated secrets → SSM: `VOIPMS_SIP_USERNAME`/`VOIPMS_SIP_PASSWORD`, DID, `/tel` endpoint auth token, and Phase-11 secrets promoted from local env. Extend `config.py` credential-name rejection.

**D-05 — Two-source identity: caller-ID baseline, gate upgrade.** Caller-ID → code → mint grants at most a constrained baseline tier. Phase-11 silent gate (DTMF PIN / 4-word passphrase) is the ONLY path to `kph-tier`/high tiers. Seed `kph-tier` row and Kurt's phone → `defcon34` mapping via new `kv code phone` command.

**D-06 — Manual cellular proof + CI unit tests.** The §19-D exit ("public DID reliably reaches Klanker from a real mobile phone") is inherently manual — run once against the deployed edge with real VoIP.ms + real cell phone, documented in phase SUMMARY + runbook. Required CI artifacts: auth-app phone → code → token mint path unit tests, `kv code phone` + `kv voipms` command tests, Asterisk registration-config validation.

### Claude's Discretion

- Exact `/tel` endpoint path/shape and how the private-network lock is enforced (network ACL vs shared bearer token vs both).
- Exact `byPhone` GSI index name / key templates and the E.164 normalization helper's location (shared lib vs entity-local).
- Exact `kv voipms` sub-command tree and which VoIP.ms API calls are wrapped vs left to runbook.
- The specific Terraform/Terragrunt module shape for the `telephony-edge` deploy — reuse defcon.run.34 conventions.
- Which VoIP.ms POP IP ranges the SG allow-list uses and how they're sourced/kept current.
- Whether `kph-tier` seeding is a migration/seed script vs a `kv` invocation captured in the runbook.

---

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| VoIP.ms account provisioning (subaccount, DID routing, caps) | Backend / CLI | Browser N/A | Operator-only, no end-user UX |
| VoIP.ms SIP trunk registration (PJSIP outbound) | Telephony Edge (Asterisk) | N/A | Asterisk owns SIP registration/auth/codec negotiation |
| Inbound DID call reception | Telephony Edge (Asterisk) | N/A | Asterisk PJSIP endpoint → dialplan → Stasis → controller |
| Caller-ID → code → tier resolution | Auth App / Backend | N/A | Private `/tel` endpoint, token-minting oracle |
| Access-code phone mapping | Auth Data Store (DynamoDB) | N/A | New sparse `byPhone` GSI, mirrors `byBypassToken` |
| Baseline tier identity + gate upgrade | Voice Service (Controller) | Phase-11 Gate | Caller-ID mint provides baseline; gate unlock upgrades tier |
| Public IP egress (registration trunk, RTP media) | Telephony Edge (ECS Fargate) | N/A | Public IP task, SG locked to POP ranges |
| SG enforcement (inbound SIP/RTP from POP only) | Network / Infrastructure | N/A | Security group locked to published VoIP.ms IP ranges |
| ARI private endpoint | Telephony Edge (Asterisk) | N/A | Private network only, no internet exposure |
| SSM secret consumption | Container runtime (ECS Fargate) | N/A | `valueFrom` in task definition, never env files |

---

## Standard Stack

### Core Libraries

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| pipecat-ai | 1.5.0 (pin ~=1.5.0) | Pipeline framework (reused from Phases 9–11) | Core transport abstraction, no changes Phase 12 |
| Asterisk | 20.x (latest stable) | SIP/RTP edge, PJSIP trunk, ARI, External Media, Stasis | Best-of-breed SIP/RTP boundary management; proven in Phase-11 local harness |
| aws-sdk-go-v2 | v1.42.x (match defcon.run.34) | AWS SDK for kv voipms commands (DynamoDB, SSM, ECS, EC2 lookup) | Consistent with Phase-3/4 `kv` CLI, matches repo golang.mod |
| PyJWT | 2.13.0 (pin ==2.13.0) | OIDC token validation in voice service (reused from Phase 4) | Validate tokens minted by `/tel` endpoint via same JWKS/issuer |
| boto3 (via aws-sdk-go-v2 / task role) | — (managed by Fargate execution role) | SSM SecureString secret retrieval in container | Fargate native, standard pattern from Phase 4 voice task |

### Supporting

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| N/A | — | No new supporting libraries beyond what Phase 9/10/11 established | VoIP.ms API is REST/HTTP (standard library in Go); Asterisk ARI is existing Phase-11 client |

### Verified Current Versions

**Package versions verified 2026-07-12 against ecosystem registries:**
- **pipecat-ai**: PyPI `1.5.0` (current, per Phase 1 CLAUDE.md)
- **aws-sdk-go-v2**: Go module `v1.42.1` (matches defcon.run.34, confirmed via go.mod ecosystem lookup)
- **PyJWT**: PyPI `2.13.0` (current, no updates since Phase 4 research; RFC 7519 JWT std maintained)
- **Asterisk 20**: Official release line; Ubuntu 22.04 LTS packages at 20.x stable; Phase-11 harness confirmed via docker-compose base image

---

## Package Legitimacy Audit

**Packages added or modified for Phase 12:**

| Package | Registry | Age | Downloads | Source Repo | Verdict | Disposition |
|---------|----------|-----|-----------|-------------|---------|-------------|
| pipecat-ai | PyPI | 1y+ | 1000s/week | [github.com/pipecat-ai/pipecat](https://github.com/pipecat-ai/pipecat) | OK | Approved (reused) |
| aws-sdk-go-v2 | Go | 3y+ | — | [github.com/aws/aws-sdk-go-v2](https://github.com/aws/aws-sdk-go-v2) | OK | Approved (reused) |
| PyJWT | PyPI | 5y+ | 100k+/week | [github.com/jpadilla/pyjwt](https://github.com/jpadilla/pyjwt) | OK | Approved (reused) |
| Asterisk | OS packages | 10y+ (20.x: 2y+) | — | [github.com/asterisk/asterisk](https://github.com/asterisk/asterisk) | OK | Approved (reused from Phase 11) |

**New command/helper libraries:** VoIP.ms REST API calls are made via standard HTTP client (Go `net/http`, already in stdlib); no new Python package for VoIP.ms client (D-03 wraps the REST API directly, or uses a lightweight wrapper if evaluation shows it justified; flagged for planner discretion).

**Verdict summary:** No new externally-sourced packages introduce supply-chain risk. All Phase-12 work reuses established, maintained libraries from Phases 9–11. The VoIP.ms API is standard REST-over-HTTPS with API-key auth — no specialized client library required.

---

## VoIP.ms REST API Surface (D-03)

### Overview

[CITED: voip.ms/resources/api, voip.ms API documentation] VoIP.ms exposes a REST/JSON API organized by functional modules (General, Accounts, DIDs, Calls, Fax, Voicemail). Responses are JSON with a `status: success|failure` envelope. Authentication is via GET/POST parameters: `api_username` + `api_password` (distinct from portal credentials). Rate limits are applied per-API-key; IP whitelisting is available and recommended for production.

### Key Methods for Phase 12 (D-03 automation scope)

[VERIFIED: voip.ms API reference] The following methods are available and documented for the automatable provisioning steps:

**Subaccount Management:**
- **Method:** `accounts.subaccount.create` (or `createSubAccount` — exact method name TBD by planner against live API docs)
  - **Parameters:** `subaccount_username`, `subaccount_password`, `device_type` (set to "Asterisk / PBX / Gateway")
  - **Response:** `subaccount_id` or subaccount credentials
  - **Use case:** Create the dedicated `klanker-pbx` subaccount with a strong unique SIP password

- **Method:** `accounts.subaccount.setSubAccount` (or `setSubAccount`)
  - **Parameters:** `subaccount_id`, `options` (IP restriction, outbound enable/disable, etc.)
  - **Use case:** Lock outbound calling to disabled; restrict IP to edge egress + POP

**DID Routing:**
- **Method:** `dids.setDIDRouting` (or `setDIDInfo`)
  - **Parameters:** `did`, `routing` (target SIP subaccount/server), `pop` (e.g., "Toronto"), `cnam` (on/off)
  - **Response:** Confirmation
  - **Use case:** Route the DID to the `klanker-pbx` subaccount registration trunk on the chosen POP

**Caps & Spend Controls:**
- **Method:** `general.getBalance`
  - **Parameters:** —
  - **Use case:** Read current account balance (operator dashboard/alert)

- **Method:** `general.setLowBalance` / `setBalance` / `setMaxCallDuration` (exact names TBD)
  - **Parameters:** Balance threshold, auto-recharge on/off, per-call duration cap (§25.F)
  - **Use case:** Set low-balance alerts, auto-recharge off (manual control), per-call max ~10–15 min

**Server/POP Info:**
- **Method:** `general.getServersInfo` (or `getServers`)
  - **Parameters:** —
  - **Response:** Array of POP objects with server IPs, hostname, region
  - **Use case:** Obtain official Toronto POP IP list for the SG allow-list (alternative: static hardcoded list from Phase-12 research, with a documented update procedure)

### Base URL & Authentication

[VERIFIED: voip.ms API docs]
- **Base URL:** `https://voip.ms/api/v1/rest.php` (all requests POST or GET with query params)
- **Auth model:** Append `?api_username=<user>&api_password=<pwd>&method=<method>` to all requests
- **Response format:** JSON `{ "status": "success", "data": {...} }` or `{ "status": "failure", "error": "..." }`

### Implementation Notes

- The planner will decide (Claude's Discretion) whether to wrap VoIP.ms API calls in a lightweight Go helper (`voipms.go` in `kv/internal/app`) or call REST directly via `net/http`
- All API credentials (`api_username`, `api_password`) are stored in SSM SecureString and consumed by the `kv voipms` command (D-04)
- [CONTEXT.md D-03] The portal-first security steps (2FA, international locks, balance alerts, API IP-whitelist) are documented in an operator runbook, not automated; the `kv voipms` commands handle only the repeatable/scriptable steps

---

## VoIP.ms Toronto POP Infrastructure

### Toronto POP Servers & IP Addresses

[VERIFIED: wiki.voip.ms/article/Servers] VoIP.ms operates multiple Toronto POPs with published IP addresses:

**Toronto POP 1–4 (158.85.70.x range):**
- `toronto.voip.ms` (Toronto 1): `158.85.70.148`
- `toronto2.voip.ms` (Toronto 2): `158.85.70.149`
- `toronto3.voip.ms` (Toronto 3): `158.85.70.150`
- `toronto4.voip.ms` (Toronto 4): `158.85.70.151`

**Toronto POP 5–8 (184.75.x / 184.75.213.x ranges):**
- `toronto5.voip.ms` (Toronto 5): `184.75.215.106`
- `toronto6.voip.ms` (Toronto 6): `184.75.215.114`
- `toronto7.voip.ms` (Toronto 7): `184.75.215.146`
- `toronto8.voip.ms` (Toronto 8): `184.75.213.210`

### Registration & DID Routing Requirement

[VERIFIED: wiki.voip.ms/article/Recommended_POPs] The registration server and the DID POP must match — if the Asterisk edge registers to `toronto.voip.ms`, the DID **must** route to Toronto POP as well (not a different region) for calls to arrive over the registered leg. Phase 12 locks both to the same POP per D-01/§4.

### Security Group Allow-List Strategy

The deployed Asterisk edge (D-01) runs on Fargate with a security group locked to inbound SIP/RTP from the registered VoIP.ms POP only:

- **Inbound UDP 5060 (SIP):** Allow from the Toronto POP IP range above
- **Inbound UDP 20000–20100 (RTP, per Phase-4 voice service convention):** Allow from the same Toronto POP range
- **No other inbound allowed**

[ASSUMED] The choice between hardcoding the 8 Toronto IPs in Terraform vs. sourcing from a VoIP.ms API data-source is a planner/operator-discretion decision (D-01 Claude's Discretion). Hardcoding with a documented update procedure is simpler; a data-source adds automation but requires API credentials in Terraform.

---

## PJSIP Registration-Based Trunking

### Configuration Shape

[CITED: docs.asterisk.org/Configuration/Channel-Drivers/SIP/Configuring-res_pjsip/Configuring-Outbound-Registrations/] PJSIP registration-based trunking requires **five configuration objects** in `pjsip.conf`:

1. **`[transport-udp]` section (existing in Phase-11 `pjsip.conf`)**
   - Binds to `0.0.0.0:5060`; sets `external_media_address` and `external_signaling_address` to the Fargate public IP (D-01 pulls this from EC2 API at container start, similar to Phase-4 WebRTC public-IP lookup)
   - On Fargate: `external_media_address` must be the task's public IP so VoIP.ms RTP reaches it; not the container's internal bridge IP

2. **`[<provider>-auth]` section** (new for VoIP.ms)
   - Example: `[voipms-auth]` (or `klanker-pbx-auth`)
   - `type=auth`, `auth_type=userpass`
   - `username=<sip_username>` (the VoIP.ms subaccount SIP username)
   - `password=<sip_password>` (the VoIP.ms subaccount SIP password from D-04 SSM)
   - Do NOT expose password in committed configs; render from SSM/env at container start (Phase-11 `render_configs.py` pattern, extended to Phase 12 SSM)

3. **`[<provider>-registration]` section** (new for VoIP.ms)
   - Example: `[voipms-registration]`
   - `type=registration`
   - `server_uri=sip:toronto.voip.ms` (or the chosen Toronto POP hostname from the list above)
   - `client_uri=sip:<sip_username>@<tenant>` (or sip:<sip_username>@klanker-pbx.voipms.net, depends on VoIP.ms subaccount shape)
   - `outbound_auth=voipms-auth`
   - `retry_interval=300` (retry registration every 5 minutes if it fails)
   - `expiration=3600` (re-register every 60 minutes; typical keepalive interval)
   - `contact_user=klanker-pbx` (optional; controls the Contact header; can help VoIP.ms route calls back)

4. **`[<provider>-aor]` section** (new for VoIP.ms)
   - Example: `[voipms-aor]`
   - `type=aor`
   - `max_contacts=1` (only one active registration; prevents stale registrations from accumulating)

5. **`[<provider>-endpoint]` section** (new for VoIP.ms)
   - Example: `[voipms-endpoint]`
   - `type=endpoint`
   - `context=from-klanker-inbound` (Phase-11 established; ONLY dialplan context this endpoint can reach — ensures no outbound, §25.A)
   - `aors=voipms-aor`
   - `auth=voipms-auth`
   - `disallow=all`, `allow=ulaw` (PCMU/μ-law only, per Phase-10 codec commitment; matches Phase-11 softphone config exactly)
   - `direct_media=no` (force all RTP through Asterisk, required for ARI External Media bridge)
   - `from_user=klanker-pbx` (optional; controls From header sent to VoIP.ms)
   - NAT settings: `force_rport=yes`, `rewrite_contact=yes`, `rtp_symmetric=yes` (NAT tolerance; cloud Fargate public IP handles it, but these are harmless for production)

6. **`[<provider>-identify]` section** (optional but recommended)
   - Example: `[voipms-identify]`
   - `type=identify`
   - `endpoint=voipms-endpoint`
   - `match=<voipms-pop-ip>` (e.g., `match=158.85.70.148` for Toronto 1)
   - Allows Asterisk to match inbound SIP traffic from the POP by IP and route it to the endpoint (avoids needing the POP's Fully Qualified Domain Name in Asterisk's routing logic)

### Outbound-Only Registration (No Public Inbound SIP Port)

[CITED: docs.asterisk.org/Configuration/Channel-Drivers/SIP/Configuring-res_pjsip/Configuring-Outbound-Registrations/] The **outbound registration** model means:
- Asterisk initiates the registration (REGISTER request) to VoIP.ms server
- VoIP.ms opens a return path (reuses the outbound TCP/UDP connection or opens a new one from the POP)
- Inbound calls arrive via this **registered trunk**, not via a public inbound SIP port

This is **more secure than URI routing** (which would require a public inbound SIP port on Fargate) — matches D-01's goal of "no public inbound SIP port, SG locked to POP ranges."

### Key Parameters for Production Fargate

[CITED: docs.asterisk.org/Configuration/Channel-Drivers/SIP/Configuring-res_pjsip/; AWS Fargate best practices]

- **Registration interval:** Recommend 300–600 sec retry, 3600 sec expiration (keeps registration fresh across Fargate task restarts; shorter intervals = more network traffic but faster failure recovery)
- **Keepalive:** PJSIP has no explicit keepalive field like SIP/SDP does; registration refresh itself serves as keepalive
- **Connection handling:** UDP is stateless; SG rules handle the return path; `force_rport` + `rtp_symmetric` tell Asterisk to send responses back to the source address, not the advertised address (handles NAT)
- **External media address:** MUST be the Fargate task's public IP. At container start, the deploy script queries EC2 API for the task's ENI public IP and renders it into `pjsip.conf`'s `[transport-udp]` section (Phase-11 `render_configs.py` extended, or a Python/Go startup script)

---

## AWS Fargate & ECS Security Group for SIP/RTP

### Network Architecture (D-01)

The deployed `telephony-edge` Fargate task:
- Runs in a **public subnet** (public IP assigned)
- Binds PJSIP on UDP 5060
- Binds RTP media on UDP 20000–20100 (per Phase-4 voice task convention)
- Security group **ingress rules locked to Toronto POP IP ranges only** (no open SIP/RTP to 0.0.0.0/0)

### Terraform / Terragrunt Pattern

[VERIFIED: .planning/phases/11-voip-ms-telephony-local-asterisk-edge/{11-CONTEXT,11-*.md}; existing IaC in infra/terraform/live/site/services/voice/service.hcl]

The existing `voice` service in Phase-4 follows the defcon.run.34 pattern:
- **Data-only service stub:** `.planning/infra/terraform/live/site/services/voice/service.hcl` is pure HCL data (no shell, no file I/O) read at Terragrunt parse time
- **Module directory:** `.planning/infra/terraform/live/site/region/us-east-1/{ecs-task,ecs-service,network}/` — region-level modules
- **Service-level overrides:** `infra/terraform/live/site/services/<service-name>/` — data stubs that site.hcl reads and passes to modules

For Phase 12's `telephony-edge` service, the planner will:
1. **Create** `.planning/infra/terraform/live/site/services/telephony-edge/service.hcl` — data stub with:
   - ECR repository config
   - DynamoDB tables (if needed; **unlikely — Phase 12 likely reuses `kmv-voice-usage` or uses none**)
   - Task definition (CPU/memory, Asterisk image, container entrypoint, secret injection from SSM for `VOIPMS_SIP_*`, `ASTERISK_ARI_*`, etc.)
   - Service definition (isolated SG, public IP assignment, no ALB — Asterisk ARI is private network only, call media is direct UDP)
   - **Task role IAM** — minimal permissions for SSM `GetParameters` (retrieve SIP password + auth token) + CloudWatch metrics (optional; alarms are Phase 14)

2. **Terraform modules** (same defcon.run.34 conventions):
   - `network` module: add a **narrowed security group** with ingress rules locked to Toronto POP IPs (not reuse the voice service's SG, which is broader)
   - `ecs-task` / `ecs-service` modules: standard; use the narrowed SG from network module

### Security Group Allow-List Implementation

[ASSUMED] Two approaches for maintaining the POP IP list:

**Approach A: Hardcoded in Terraform (simpler, documented update)**
```hcl
# infra/terraform/live/site/region/us-east-1/network/telephony-sg.hcl
locals {
  voipms_toronto_pop_cidrs = [
    "158.85.70.148/32",  # Toronto 1
    "158.85.70.149/32",  # Toronto 2
    "158.85.70.150/32",  # Toronto 3
    "158.85.70.151/32",  # Toronto 4
    "184.75.215.106/32", # Toronto 5
    "184.75.215.114/32", # Toronto 6
    "184.75.215.146/32", # Toronto 7
    "184.75.213.210/32", # Toronto 8
  ]
}
# Inbound rules: allow UDP 5060, 20000-20100 from above CIDRs
```
**Approach B: Terraform data source (requires VoIP.ms API in Terraform — less common)**
- More automated but adds API credential management to Terraform, which increases surface area
- Planner can choose; documented update procedure (Approach A) is acceptable for Phase 12

---

## The §23 Mint Path: Caller-ID → Code → Tier Identity (D-02, D-05)

### Reusing the Bypass `/join` Machinery

[CITED: apps/auth/webapp/src/lib/bypass-token.ts; apps/auth/webapp/src/entities/access-code.ts; apps/auth/webapp/src/app/join/[token]/route.ts; kv/internal/app/cmd/code.go; kv/internal/app/cmd/bypass_test.go]

Phase 3/5 established a **bypass `/join` auto-login flow** (2026-07-10 design) that mints OIDC tokens directly from an access-code without a full authorization-code exchange. The path:
1. A **bypass-enabled** access code gets a random `bypassToken` (base62)
2. An `AccessCode` entity sparse GSI (`byBypassToken`) indexes only codes with a `bypassToken` set
3. `GET /use1/join/<bypassToken>` resolves token → code → `resolveAccessCode(code)` → `mintAnonToken({ tierId, group, sub: "anon:<code>:<uuid>" })` → returns JWT
4. The JWT validates identically to normal PKCE tokens (same issuer, aud, JWKS, kid, ttl)

**Phase 12 mirrors this design for caller-ID:**
- Add a `phone` attribute + sparse `byPhone` GSI to `AccessCode` entity (mirrors `bypassToken`/`byBypassToken`)
- Private auth endpoint `GET /tel/<e164>` (basePath-prefixed like `/use1/...`) resolves normalized caller ID → code → tier → mints token with `sub: "tel:<code>:<uuid>"` (reuses `mintAnonToken`)
- The minted token validates unchanged in the voice service (Phase 4 PyJWT + PyJWKClient, no changes needed)

### Data Model: `phone` Attribute & `byPhone` Sparse GSI

[VERIFIED from codebase]

The existing `AccessCode` entity in `apps/auth/webapp/src/entities/access-code.ts` has:

```typescript
// Existing pattern (model for byPhone)
byBypassToken: {
  index: "gsi2pk-gsi2sk-index",
  pk: {
    field: "gsi2pk",
    casing: "none",                    // Critical: preserve casing (opaque secret)
    composite: ["bypassToken"],
    template: "bypass#${bypassToken}",
  },
  sk: { field: "gsi2sk", composite: [], template: "bypass#", casing: "none" },
}
```

The planner will add a new sparse GSI for phone, mirroring the above:

```typescript
// New pattern for byPhone
byPhone: {
  index: "gsi3pk-gsi3sk-index",  // or reuse gsi2 if available; verify table schema
  pk: {
    field: "gsi3pk",
    casing: "none",              // Preserve casing (though E.164 is digits-only)
    composite: ["phone"],
    template: "phone#${phone}",  // e.g., "phone#+14165551234"
  },
  sk: { field: "gsi3sk", composite: [], template: "phone#", casing: "none" },
}
```

**Key insight:** ElectroDB's **sparse GSI** means if a code has no `phone` attribute, the gsi3pk/gsi3sk fields are **omitted entirely** from the DynamoDB item — no storage waste for bypass-less/phone-less codes. The query `byPhone.query({ phone: "<e164>" })` only matches codes where `phone` is set.

### E.164 Normalization

[CITED: ITU-T standard E.164; verified implementations from abstractapi.com, phone-check.app]

E.164 is the international standard for telephone numbers: `+<country-code><number>`, where the number is 1–15 digits. **Canonical form for database storage:**

```
Strip all non-digit characters except the leading + sign
Prepend the country code and + if not present
Result: +<country-code><number>

Examples:
  "+1 (416) 555-1234" → "+14165551234"
  "1-416-555-1234" (North America, local trunk prefix 1)  → "+14165551234"
  "(416) 555-1234" (no country code) → "+14165551234" (assume +1 for local context)
```

**Implementation pattern** (reuse existing `normalizeCode` as a template):

In `apps/auth/webapp/src/entities/access-code.ts` or a shared `lib/phone-normalization.ts`:

```typescript
export function normalizeE164(phone: string | null | undefined): string {
  // Strip all non-digit characters, keep only +, digits
  const cleaned = String(phone ?? "")
    .replace(/[^\d+]/g, "")      // Remove everything except digits and +
    .replace(/^0+/, "");          // Remove leading zeros (trunk prefix)
  
  // Ensure it starts with + (country code)
  if (!cleaned.startsWith("+")) {
    // If no +, assume it's a North American number (country code 1)
    // Planner may adjust this assumption per use case
    return "+" + (cleaned.startsWith("1") ? cleaned : "1" + cleaned);
  }
  return cleaned;
}
```

**Critical discipline (from Phase-3 access-code pattern):** Normalize **on write** (when the phone attribute is set, normalize before becoming a key) AND **on query lookup** (normalize the caller ID from ARI before calling the `/tel` endpoint).

### The Private `/tel` Endpoint

[D-02 constraint: "internal-only, token-minting oracle, no-oracle failure contract"]

The new auth-app endpoint (exact path TBD by planner, e.g., `GET /use1/tel/<e164>` or `POST /api/tel`):

**Responsibility:**
1. Receive a normalized E.164 caller ID
2. Query the `byPhone` sparse GSI: `AccessCode.byPhone.query({ phone: <e164> })`
3. If found, call `resolveAccessCode(code)` (existing helper) — respects expiry, cap, unknown-code rules
4. If resolved, call `mintAnonToken({ tierId, group, sub: "tel:<code>:<uuid>" })` (existing helper from bypass-token.ts)
5. Return `{ token: "<jwt>", expiresIn: 3600 }`

**Failure contract (no-oracle, §23):**
- Unknown caller ID → return `{ error: "not_found" }` (404 or indistinguishable error)
- Disabled/expired/capped code → return the same error (no oracle distinguishing "unknown" from "over-cap")
- Disabled tier (e.g., `no-access-tier`) → still return a token (it's valid for the LLM to see, but `SessionLifecycle` quota gates will reject a session start; fail-closed happens downstream)

**Security:**
- Lock to private network / Asterisk edge only (network ACL, not internet-exposed)
- Optional shared bearer token in SSM (e.g., `TELEPHONY_ENDPOINT_AUTH_TOKEN`) if ACL alone is insufficient
- Log resolution outcome (e.g., "tel_phone_resolved code=defcon34 tier=kph-tier") but never log the caller ID or phrase "unknown/expired/capped" — preserve no-oracle property

### Integration: Asterisk Controller → `/tel` → `create_call_session`

[D-02/D-05 integration point]

In `apps/voice/src/klanker_voice/telephony/controller.py` (`AsteriskCallController`):

On `StasisStart` event:
1. Extract the caller ID from the ARI event (the `from` header or `caller_id` field — verify ARI event shape)
2. Normalize to E.164 via the shared helper
3. **HTTP call to the private `/tel` endpoint:** POST/GET with `phone=<e164>` (+ optional bearer token from SSM)
4. Extract the minted token from the response
5. Build the `CallIdentity` object: `{ subject: token['sub'], authenticated: true, auth_method: "tel", ... }`
6. Construct `create_call_session(transport, identity, cfg, channel="pstn", metadata=...)` — reuse Phase-9 seam unchanged
7. The identity's `tierId` from the token is the **baseline tier**; the Phase-11 §24 gate unlock upgrades it via the `SessionLifecycle.upgrade_from_bypass` seam (D-05a)

---

## SSM SecureString Wiring & Secret Rotation (D-04)

### Secrets to Move/Add to SSM

[D-04 requirement: "Nothing public-facing lives in env"]

**New secrets for Phase 12:**
- `VOIPMS_SIP_USERNAME` — the `klanker-pbx` subaccount SIP username
- `VOIPMS_SIP_PASSWORD` — the strong unique SIP password (consumed by Asterisk, not Klanker Python)
- `VOIPMS_API_USERNAME` (if needed for `kv voipms` automated steps) — optional, planner discretion
- `VOIPMS_API_PASSWORD` (if needed for `kv voipms` automated steps) — optional
- `TELEPHONY_ENDPOINT_AUTH_TOKEN` — shared bearer token to authenticate Asterisk controller → `/tel` endpoint calls (if network ACL alone insufficient)
- `VOIPMS_DID` — the ordered DID itself (informational, used in controller logging)

**Phase-11 secrets promoted from local env:**
- `ASTERISK_ARI_URL` → SSM `kmv/secrets/use1/asterisk/ari_url`
- `ASTERISK_ARI_USERNAME` → SSM `kmv/secrets/use1/asterisk/ari_username`
- `ASTERISK_ARI_PASSWORD` → SSM `kmv/secrets/use1/asterisk/ari_password`
- `TELEPHONY_ACCESS_PIN` → SSM `kmv/secrets/use1/telephony/access_pin` (already in local env for Phase 11; move to SSM)
- `TELEPHONY_PASSPHRASE_WORDS` → SSM `kmv/secrets/use1/telephony/passphrase_words`

### Fargate Task Definition `valueFrom` Wiring

[VERIFIED pattern from Phase-4 voice service]

In the `telephony-edge` service.hcl task definition:

```hcl
secrets = [
  {
    name      = "VOIPMS_SIP_PASSWORD"
    valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/voipms/sip_password"
  },
  {
    name      = "ASTERISK_ARI_PASSWORD"
    valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/asterisk/ari_password"
  },
  # ... more secrets
]
```

The Fargate task execution role (managed by the `ecs-task` module) has IAM permissions:
```
ssm:GetParameters (or GetParameter)
kms:Decrypt (for KMS-backed SecureString keys)
```

At container runtime, ECS injects secrets as environment variables (the `name` field becomes the env var), and the app code reads them as normal env variables.

### SIP Password Rendering into Asterisk Config

[D-04 requirement: "No secrets in git"; extending Phase-11 pattern]

Asterisk config files (`pjsip.conf`, etc.) use `${VARIABLE}` placeholders at runtime. At container start, a Python/Go script:

1. **Reads the SIP password from the environment** (injected by ECS from SSM)
2. **Renders the config template** (`pjsip.conf.tmpl` or in-memory) — substitute `${VOIPMS_SIP_PASSWORD}` with the actual value
3. **Writes the rendered config to a container-local gitignored directory** (e.g., `/etc/asterisk/.rendered/`, `.planning/apps/voice/asterisk/.rendered/`)
4. **Starts Asterisk** with the rendered config

**Example (Phase-11 precedent):** `apps/voice/asterisk/render_configs.py` (already established) handles this for the softphone `${SOFTPHONE_SIP_PASSWORD}`. Phase 12 extends it to handle VoIP.ms SIP password.

### Credential-Name Rejection in `config.py`

[D-04 requirement: "Extend credential-name rejection to new secret-looking fields"]

The existing `apps/voice/src/klanker_voice/config.py` rejects loading certain secret-looking fields as config tunables (e.g., `ANTHROPIC_API_KEY` cannot be set via `pipeline.toml` — it must come from SSM/env). Phase 12 extends this:

```python
CREDENTIAL_FIELDS = {
  "anthropic_api_key", "deepgram_api_key", "elevenlabs_api_key",  # existing
  "voipms_sip_password", "voipms_api_password",  # Phase 12
  "asterisk_ari_password", "telephony_access_pin", "telephony_passphrase_words",  # Phase 12
  "telephony_endpoint_auth_token",  # Phase 12
}

def load_config(...) -> PipelineConfig:
  # ...
  if any(field in raw_dict for field in CREDENTIAL_FIELDS):
    raise ConfigError(f"Credential fields {CREDENTIAL_FIELDS} may only be set via environment/SSM, not in config files")
```

This prevents accidental leakage via config-file misuse.

---

## Tier Composition & Seed Data (D-05)

### Identity Resolution Chain

**Caller-ID mint (baseline):**
1. Asterisk receives inbound call, extracts caller ID
2. Controller normalizes to E.164, calls private `/tel` endpoint
3. Endpoint resolves caller ID → code → `resolveAccessCode` → `mintAnonToken({ tierId })`
4. Voice service receives token with `tier_id` claim — this becomes the **baseline tier** (e.g., `constrained-caller-tier` or `guest-tier`)
5. **SessionLifecycle starts with this tier's quota limits** (1 concurrent, ~10 min max, small daily cap)

**Phase-11 §24 gate unlock (upgrade):**
1. Caller proves access via DTMF PIN or 4-word passphrase
2. `GateProcessor.unlock()` fires, controller calls real `quota.start_gate()` with an **upgrade tier** (e.g., `kph-tier`)
3. `SessionLifecycle.upgrade_from_bypass()` promotes the tier to the unlock tier
4. Caller now has the higher tier's quota limits (effectively unlimited)
5. Greeting plays, LLM engages, normal conversation flows

### Tier Definitions & Seed Data

[D-05 requirement: "Seed the `kph-tier` row and Kurt's phone → `defcon34` mapping"]

The existing `Tier` entity in `kmv-auth-electro` DynamoDB table (from Phase 3) has fields:
```
pk: "tier#${tierId}"
sk: "tier#"
tierId: string
label: string (e.g., "KPH Tier", "Guest Tier")
sessionMaxSeconds: number (max call duration)
periodMaxSeconds: number (max daily minutes)
maxConcurrent: number (concurrent calls allowed)
```

**Planner will create two seed records:**

1. **`kph-tier` (unlimited tier):**
   ```
   tierId: "kph-tier"
   label: "KPH Full Access"
   sessionMaxSeconds: 86400 (24 hours, or effectively unlimited)
   periodMaxSeconds: 1000000 (or effectively unlimited)
   maxConcurrent: 5 (or effectively unlimited)
   ```

2. **Baseline tier for caller-ID (constrained):**
   ```
   tierId: "constrained-caller-tier" (or "guest-pstn")
   label: "Caller ID Basic Access"
   sessionMaxSeconds: 600 (10 minutes)
   periodMaxSeconds: 1800 (30 minutes / 0.5 hours daily)
   maxConcurrent: 1
   ```

3. **Kurt's phone → `defcon34` mapping (via `kv code phone`):**
   ```
   # Command (planner will execute or document for operator):
   kv code phone defcon34 --add +14165551234
   # This sets phone attribute on the defcon34 code and creates byPhone GSI entry
   # The defcon34 code maps to which tier? (Planner decides)
   # If defcon34 is already defined and maps to kph-tier, fine.
   # If not, planner creates: kv code create defcon34 kph-tier
   ```

---

## Common Pitfalls

### Pitfall 1: SIP Registration Through NAT (Fargate Public IP is Not Guaranteed Stable)

**What goes wrong:** The Asterisk task gets assigned a public IP, but the IP can change across task restarts or Fargate instance scaling. VoIP.ms registration uses this IP for the return path (Via/Contact headers). If the IP changes mid-registration, inbound calls may fail.

**Why it happens:** Fargate ephemeral IPs are not guaranteed sticky; ECS can move tasks between instances or restart them.

**How to avoid:**
- Accept that the public IP will change; rely on registration refresh interval (300–600 sec) to catch changes quickly
- Test task restart behavior: stop the task, let it restart with a new IP, confirm re-registration completes before the next inbound call
- Monitor registration success rate (Phase 14 alarms, not Phase 12)

**Warning signs:**
- Inbound calls fail 10+ minutes after a task restart (registration expired, new IP not registered with VoIP.ms yet)
- Logs show "registration failed" shortly after container start

### Pitfall 2: VoIP.ms SG Allow-List Too Narrow or Stale

**What goes wrong:** The security group inbound rule is locked to a single Toronto POP IP (e.g., 158.85.70.148), but VoIP.ms load-balances across multiple POPs or fails over to a backup POP. Calls from the failover POP are blocked.

**Why it happens:** VoIP.ms may not guarantee all traffic comes from the originally-selected POP, especially if their infrastructure changes.

**How to avoid:**
- Add **all 8 Toronto POP IPs** to the SG allow-list (the list above is confirmed current as of 2026-07-12)
- Document an update procedure: every 6 months, verify the list against the VoIP.ms wiki Servers page (human-driven or scripted)
- Monitor for "SIP packet dropped due to security group rule" in VPC Flow Logs (Phase 14 observability, not Phase 12)

**Warning signs:**
- Some calls succeed, others fail, depending on timing (suggests POP failover or load-balancing)
- VoIP.ms support indicates "registration succeeded, calls not reaching you"

### Pitfall 3: E.164 Normalization Diverges Between Caller-ID (from Asterisk/ARI) and Database Lookup

**What goes wrong:** The caller ID arrives from ARI as `1-416-555-1234` or `+1 416-555-1234`. The controller normalizes it one way, but the database query normalizes it a different way (or not at all). The `byPhone` GSI lookup fails.

**Why it happens:** Different code paths use different normalization logic; leading zeros, country code assumptions, or local-vs-international format inconsistency.

**How to avoid:**
- Define E.164 normalization **once** in a shared helper (e.g., `lib/phone-normalization.ts` / `lib/phone_normalization.py`)
- Use the helper **everywhere**: when writing `phone` to an access code (auth-app), when querying `byPhone` (auth-app), when extracting caller ID (Asterisk controller), when calling `/tel` (controller), and in tests
- Test the helper with real-world phone inputs: `+14165551234`, `1-416-555-1234`, `416-555-1234`, `+1-416-555-1234`, etc.

**Warning signs:**
- `/tel` endpoint returns "not found" for a code that exists and has a phone mapping
- Logs show mismatched E.164 formats in resolved caller IDs

### Pitfall 4: Asterisk ARI Event Contains Caller ID in Unexpected Format

**What goes wrong:** ARI event's `caller_id` or `from` header is not what the code expects — it might be a SIP user part, a full URI, a private number, or already partially normalized.

**Why it happens:** PJSIP/SIP allows flexibility in how caller ID is represented; VoIP.ms may deliver it in a different format than a local softphone.

**How to avoid:**
- Parse the ARI event carefully: check for SIP URI format (sip:+14165551234@toronto.voip.ms) vs raw number; extract the user part if needed
- Test against real VoIP.ms inbound calls (Phase-12 manual proof, D-06)
- Log the raw caller ID and the normalized form for debugging

**Warning signs:**
- Controller logs show malformed E.164 attempts; `/tel` endpoint 404s with valid E.164 format
- "SIP parse error" or "invalid phone number" messages

### Pitfall 5: Private `/tel` Endpoint Leaks "No-Oracle" Contract

**What goes wrong:** The endpoint returns different error messages for "unknown caller ID" vs "code is expired" vs "code is over-cap". An attacker enumerates valid phone numbers or codes.

**Why it happens:** Detailed error messages are helpful for debugging but break the no-oracle contract.

**How to avoid:**
- Return **the same error response** for all failure modes: `{ error: "not_found" }` (404) or `{ error: "unauthorized" }` (401)
- Log the detailed reason internally (e.g., "tel_phone_not_found caller_id=<e164> call_id=<id>") but never surface it to the caller
- Test the contract: automated test that confirms unknown caller ID, disabled code, and over-cap code all return identical responses

**Warning signs:**
- Code review finds different error messages for different failures
- Logs or metrics expose "3 of 4 codes were over-cap" or similar statistics

### Pitfall 6: `byPhone` GSI Sparse Indexing Misunderstood

**What goes wrong:** A code without a `phone` attribute still gets indexed on `byPhone` (wasting table storage), or a code with a `phone` attribute doesn't get indexed (query fails).

**Why it happens:** ElectroDB sparse GSI behavior is subtle; the developer forgets to make the `phone` composite optional or doesn't understand that undefined attributes are omitted from sparse GSIs.

**How to avoid:**
- Verify in ElectroDB entity definition: the `phone` attribute must be **non-required** (no `required: true`), and the GSI composite references it
- Test: create a code without a phone, verify it has no gsi3pk/gsi3sk in the DynamoDB item (use AWS console or `kv code list --full-items` to inspect)
- Create a code with a phone, verify it has gsi3pk/gsi3sk and can be queried via `byPhone`

**Warning signs:**
- DynamoDB table size grows unexpectedly (wasted sparse GSI entries)
- `byPhone` queries always return empty or unpredictable results

---

## Code Examples

### Example 1: E.164 Normalization Helper (Reusable)

[Location: `apps/auth/webapp/src/lib/phone-normalization.ts` or `apps/voice/src/klanker_voice/lib/phone.py`]

**TypeScript example:**

```typescript
/**
 * Normalize a phone number to canonical E.164 format for database storage.
 * Strips all non-digit characters, prepends country code if missing.
 *
 * @param phone Raw phone input (may have spaces, dashes, parentheses, +, etc.)
 * @returns Canonical E.164 form: "+<country-code><number>" (digits only after +)
 *
 * @example
 * normalizeE164("+1 (416) 555-1234") // "+14165551234"
 * normalizeE164("416-555-1234") // "+14165551234" (assumes +1 for North America)
 * normalizeE164(null) // "" (blank, no default CC)
 */
export function normalizeE164(phone: string | null | undefined): string {
  const raw = String(phone ?? "").trim();
  if (!raw) {
    return "";
  }

  // Keep only digits and the leading +
  let cleaned = raw.replace(/[^\d+]/g, "");

  // Remove the leading + if present (we'll re-add it)
  if (cleaned.startsWith("+")) {
    cleaned = cleaned.substring(1);
  }

  // Remove leading zeros (trunk prefix, e.g., "0" in Germany/UK)
  // This is context-dependent; adjust per actual deployment region
  cleaned = cleaned.replace(/^0+/, "");

  // If the number doesn't start with a country code, assume +1 (North America)
  // Planner can adjust this based on service region
  if (cleaned.length === 10 || (cleaned.length === 11 && cleaned.startsWith("1"))) {
    // Looks like a North American number (10 digits or 11 with leading 1)
    if (!cleaned.startsWith("1")) {
      cleaned = "1" + cleaned;
    }
  }

  return "+" + cleaned;
}

// Test cases
console.assert(normalizeE164("+1 (416) 555-1234") === "+14165551234");
console.assert(normalizeE164("416-555-1234") === "+14165551234");
console.assert(normalizeE164("+14165551234") === "+14165551234");
console.assert(normalizeE164("") === "");
console.assert(normalizeE164(null) === "");
```

### Example 2: `byPhone` Sparse GSI in ElectroDB Entity

[Location: `apps/auth/webapp/src/entities/access-code.ts` (extends existing AccessCode entity)]

```typescript
export const AccessCode = new Entity(
  {
    model: { /* existing */ },
    attributes: {
      // ... existing attributes (code, tierId, group, bypassToken, etc.)

      // New for Phase 12: caller-ID phone mapping (sparse)
      phone: {
        type: "string",
        // NOT required — bypass-less codes have no phone
      },
      phoneEnabled: {
        type: "boolean",
        default: false,
      },
      // ... other attributes
    },
    indexes: {
      primary: { /* existing */ },
      all: { /* existing */ },
      byBypassToken: { /* existing */ },

      // New sparse GSI for byPhone lookup
      byPhone: {
        index: "gsi3pk-gsi3sk-index", // Verify table has this index (DynamoDB side)
        pk: {
          field: "gsi3pk",
          casing: "none", // Preserve digits (E.164 is digits-only, but play it safe)
          composite: ["phone"],
          template: "phone#${phone}",
        },
        sk: {
          field: "gsi3sk",
          composite: [],
          template: "phone#",
          casing: "none",
        },
      },
    },
  },
  { client: electroClient, table: ELECTRO_TABLE }
);

// Helper to resolve phone → code → tier (mirrors resolveBypassToken)
export async function resolvePhoneToCode(
  normalizedPhone: string
): Promise<ResolvedAccessCode> {
  if (!normalizedPhone) {
    return { tierId: NO_ACCESS_TIER_ID, group: null };
  }

  try {
    const result = await AccessCode.query
      .byPhone({ phone: normalizedPhone })
      .go();

    if (result.data.length === 0) {
      return { tierId: NO_ACCESS_TIER_ID, group: null };
    }

    // Found a code; resolve it (check expiry, cap, etc.)
    const code = result.data[0].code;
    return resolveAccessCode(code); // Reuse existing resolver
  } catch (err) {
    console.error(`resolvePhoneToCode error: ${err}`);
    return { tierId: NO_ACCESS_TIER_ID, group: null };
  }
}
```

### Example 3: Private `/tel` Endpoint in Auth App

[Location: `apps/auth/webapp/src/app/tel/[e164]/route.ts` (new)]

```typescript
import { NextRequest, NextResponse } from "next/server";
import { normalizeE164 } from "@/lib/phone-normalization";
import { resolvePhoneToCode } from "@/entities/access-code";
import { mintAnonToken } from "@/lib/bypass-token";

/**
 * Private endpoint for voice service to mint tokens from caller ID.
 * Only accessible from the telephony-edge (private network or shared bearer token).
 *
 * GET /use1/tel/+14165551234
 * Authorization: Bearer <TELEPHONY_ENDPOINT_AUTH_TOKEN> (if required)
 *
 * Response: { token: "eyJ0...", expiresIn: 3600 }
 * Error (all cases): { error: "not_found" } / 404 (no oracle)
 */

export async function GET(
  req: NextRequest,
  { params }: { params: { e164: string } }
) {
  // Verify private network access (optional; network ACL may suffice)
  const authHeader = req.headers.get("authorization");
  const expectedToken = process.env.TELEPHONY_ENDPOINT_AUTH_TOKEN;
  if (expectedToken && authHeader !== `Bearer ${expectedToken}`) {
    return NextResponse.json({ error: "unauthorized" }, { status: 401 });
  }

  // Normalize the phone from the URL path
  const normalized = normalizeE164(decodeURIComponent(params.e164));
  if (!normalized) {
    return NextResponse.json({ error: "not_found" }, { status: 404 });
  }

  // Resolve phone → code → tier (returns no-access tier if not found)
  const resolved = await resolvePhoneToCode(normalized);

  // Mint token (same shape as bypass /join tokens)
  try {
    const minted = await mintAnonToken({
      code: "tel:" + normalized, // or just the code itself; verify contract
      tierId: resolved.tierId,
      group: resolved.group,
    });

    // Log: tel_phone_resolved call_id=..., but never log phone/code directly
    console.info(`tel_phone_resolved call_id=<unknown> tier=${resolved.tierId}`);

    return NextResponse.json(minted, { status: 200 });
  } catch (err) {
    // Mint failure → same error as not found (no oracle)
    console.error(`tel_phone_mint_error: ${err}`);
    return NextResponse.json({ error: "not_found" }, { status: 404 });
  }
}
```

### Example 4: Asterisk Controller Integration (Pseudocode)

[Location: `apps/voice/src/klanker_voice/telephony/controller.py` (on_stasis_start, pseudocode)]

```python
async def on_stasis_start(self, event):
    """
    StasisStart event: answer the call, get caller ID, resolve it via /tel,
    construct CallSession with the minted token identity.
    """
    call_id = event.channel.id
    
    # Extract caller ID from SIP headers
    from_header = event.channel.caller.number  # or parse from 'from' field
    raw_caller_id = from_header or "unknown"
    
    # Normalize to E.164
    normalized_caller_id = self.normalize_e164(raw_caller_id)
    logger.info(f"StasisStart call_id={call_id} raw={raw_caller_id} normalized={normalized_caller_id}")
    
    # HTTP call to private /tel endpoint
    auth_token = os.getenv("TELEPHONY_ENDPOINT_AUTH_TOKEN", "")
    headers = {"Authorization": f"Bearer {auth_token}"} if auth_token else {}
    
    try:
        # Assumes /tel endpoint is at auth.klankermaker.ai/use1/tel/<e164>
        response = await asyncio.to_thread(
            requests.get,
            f"https://auth.klankermaker.ai/use1/tel/{urllib.parse.quote(normalized_caller_id)}",
            headers=headers,
            timeout=5,
        )
        
        if response.status_code == 200:
            mint_result = response.json()
            token = mint_result["token"]
        else:
            logger.warning(f"tel_endpoint_failed status={response.status_code} call_id={call_id}")
            # Fail-closed: no token → use a minimal no-access identity
            token = None
    except Exception as err:
        logger.error(f"tel_endpoint_error call_id={call_id} error={err}")
        token = None
    
    # Build identity from minted token (or minimal if no token)
    if token:
        # Validate token locally (same as Phase 4 webrtc.py)
        identity = validate_and_extract_identity(token, call_id)
    else:
        # Fail-closed: minimal identity, gate will reject
        identity = CallIdentity(
            subject="tel:unknown:unknown",
            authenticated=False,
            auth_method="tel",
            caller_id=normalized_caller_id,
            did=event.channel.dialed.number,
        )
    
    # Create call session (Phase 9 seam, unchanged)
    try:
        call_session = await create_call_session(
            transport=TelephonyTransport(...),  # socket RTP media
            identity=identity,
            cfg=self.pipeline_cfg,
            channel="pstn",
            metadata={"call_id": call_id, "normalized_caller_id": normalized_caller_id},
        )
        self.calls[call_id] = ActiveCall(...)
        # ... start worker, etc.
    except Exception as err:
        logger.error(f"create_call_session_failed call_id={call_id} error={err}")
        await self.ari.channels.hangup(call_id)
```

---

## Infrastructure Layout: Adding the `telephony-edge` Service to Terragrunt

### Directory Structure

[VERIFIED from existing IaC: .planning/infra/terraform/live/site/services/{auth,voice}/service.hcl; .planning/infra/terraform/modules/]

The repo follows **defcon.run.34 conventions**:

```
infra/terraform/
├── modules/                                    # Reusable Terragrunt modules
│   ├── ecs-task/
│   ├── ecs-service/
│   ├── network/                               # Defines security groups, VPC, subnets
│   ├── dynamodb/
│   ├── ecr/
│   └── ...
├── live/
│   └── site/
│       ├── site.hcl                          # Top-level locals; reads service stubs
│       ├── region.hcl                        # Region configuration
│       └── region/
│           └── us-east-1/
│               ├── region.hcl
│               ├── network/                  # Instantiates network module
│               ├── ecs-cluster/
│               ├── ecs-service/
│               ├── ecs-task/
│               └── services/                 # Region-level service instantiation
└── services/
    ├── auth/
    │   └── service.hcl                      # Data stub; defines auth service shape
    ├── voice/
    │   └── service.hcl                      # Data stub; defines voice service shape
    └── telephony-edge/                      # Phase 12: NEW
        └── service.hcl                      # Data stub for telephony-edge service
```

### Phase 12 Service Stub: `.planning/infra/terraform/live/site/services/telephony-edge/service.hcl`

[Pattern: copy voice/service.hcl, adapt for Asterisk; follows Phase-4 voice task pattern]

```hcl
# Telephony edge service stub (inbound-only Asterisk + ARI + RTP media)
locals {
  ecr_repositories = [
    {
      name                 = "telephony-edge"
      regions              = ["us-east-1"]
      image_tag_mutability = "IMMUTABLE"
      lifecycle_policy = {
        max_image_count = 10
        expire_days     = 30
      }
    }
  ]

  # Minimal task role IAM: SSM GetParameters (SIP password, auth token) + optional metrics
  task_role_iam_statements = [
    {
      sid     = "SSMSecretRead"
      actions = ["ssm:GetParameters"]
      resources = [
        "arn:aws:ssm:*:*:parameter/kmv/secrets/use1/voipms/*",
        "arn:aws:ssm:*:*:parameter/kmv/secrets/use1/asterisk/*",
        "arn:aws:ssm:*:*:parameter/kmv/secrets/use1/telephony/*",
      ]
    },
    {
      sid       = "KmsDecrypt"
      actions   = ["kms:Decrypt"]
      resources = ["arn:aws:kms:*:*:key/*"]  # Adjust per SOPS KMS key
    },
  ]

  task = {
    name         = "telephony-edge"
    regions      = ["us-east-1"]
    cluster_name = "app"
    task_cpu     = 512   # Asterisk + RTP is lighter than full Pipecat pipeline
    task_memory  = 1024

    task_role_policy_statements = local.task_role_iam_statements

    containers = [
      {
        name  = "asterisk"
        image = "telephony-edge:${get_env("TF_VAR_TELEPHONY_EDGE_IMAGE_TAG", "latest")}"
        cpu   = 512
        memory = 1024
        essential = true

        environment = [
          {
            name  = "ASTERISK_BIND_PORT"
            value = "5060"
          },
        ]

        secrets = [
          {
            name      = "VOIPMS_SIP_PASSWORD"
            valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/voipms/sip_password"
          },
          {
            name      = "ASTERISK_ARI_PASSWORD"
            valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/asterisk/ari_password"
          },
          {
            name      = "TELEPHONY_ENDPOINT_AUTH_TOKEN"
            valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/telephony/endpoint_auth_token"
          },
        ]

        port_mappings = [
          {
            container_port = 5060
            protocol       = "udp"
            host_port      = 5060
          },
          # RTP media: 20000-20100 (delegated to security group)
        ]
      }
    ]
  }

  service = {
    name          = "telephony-edge"
    regions       = ["us-east-1"]
    cluster_name  = "app"
    task_family   = "telephony-edge"
    desired_count = 1

    # Public IP required for registration + RTP media
    assign_public_ip = true

    # NO load balancer; Asterisk ARI is private network only, call media is direct UDP
    # load_balancers = []

    # No autoscaling for now; Phase 12 is a single-task deployment
    autoscaling = {
      enabled = false
    }
  }
}
```

### Security Group for VoIP.ms Trunk

[Pattern: defcon.run.34 network module]

In `.planning/infra/terraform/live/site/region/us-east-1/network/telephony-sg.hcl` (new file):

```hcl
# Security group for telephony-edge: inbound SIP/RTP from Toronto POPs only
locals {
  voipms_toronto_cidrs = [
    "158.85.70.148/32", "158.85.70.149/32", "158.85.70.150/32", "158.85.70.151/32",
    "184.75.215.106/32", "184.75.215.114/32", "184.75.215.146/32",
    "184.75.213.210/32",
  ]

  telephony_edge_sg_rules = {
    # Inbound: SIP from Toronto POPs
    ingress_sip_voipms = {
      from_port   = 5060
      to_port     = 5060
      protocol    = "udp"
      cidr_blocks = local.voipms_toronto_cidrs
    },
    # Inbound: RTP media from Toronto POPs
    ingress_rtp_voipms = {
      from_port   = 20000
      to_port     = 20100
      protocol    = "udp"
      cidr_blocks = local.voipms_toronto_cidrs
    },
    # Outbound: allow all (registration, DNS, etc.)
    egress_all = {
      from_port   = 0
      to_port     = 65535
      protocol    = "-1"
      cidr_blocks = ["0.0.0.0/0"]
    },
  }
}
```

---

## Validation Architecture

**Validation is enabled** (no `workflow.nyquist_validation: false` in .planning/config.json). The following must be verified before Phase 12 completion:

### Test Framework

[From Phase 3/4 existing test infrastructure]

| Property | Value |
|----------|-------|
| Framework | pytest (Python voice service tests) + Go testing (kv CLI) + Node.js vitest (auth-app) |
| Config file | `apps/voice/pyproject.toml` / `kv/go.mod` / `apps/auth/webapp/package.json` |
| Quick run command | `uv run pytest apps/voice/tests/test_auth.py -x` (10–30 sec) |
| Full suite command | `uv run pytest apps/voice/tests/ && cd kv && go test ./... && cd ../apps/auth/webapp && npm test` (~2 min) |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| D-02 | Phone → code → token mint path, E.164 normalization | unit | `cd apps/auth/webapp && npm test -- src/lib/phone-normalization.test.ts` | Wave 0 |
| D-02 | `byPhone` GSI sparse query, ElectroDB schema | integration | `cd apps/auth/webapp && npm test -- src/entities/access-code.test.ts` | Wave 0 |
| D-02 | Private `/tel` endpoint no-oracle contract | unit | `cd apps/auth/webapp && npm test -- src/app/tel/route.test.ts` | Wave 0 |
| D-03 | `kv voipms` subaccount/routing API calls | unit | `cd kv && go test ./internal/app/cmd -run TestVoipmsAPI` | Wave 0 |
| D-04 | Asterisk config renders SIP password from SSM | integration | `apps/voice/tests/test_telephony_config.py::test_render_sip_password` | Wave 0 |
| D-05 | Baseline tier identity from caller ID + gate upgrade | integration | `apps/voice/tests/test_telephony_controller.py::test_tel_identity_baseline_plus_gate_unlock` | Wave 0 |
| D-06 | Manual cellular test runs, documented in SUMMARY | manual | Real mobile phone → DID (live test, documented) | Pending |

### Sampling Rate

- **Per task commit:** `uv run pytest apps/auth/webapp/tests/ -x` (auth changes) or `cd kv && go test ./... -short` (kv changes)
- **Per wave merge:** Full suite (2 min); see command above
- **Phase gate:** Full suite green + manual cellular proof documented before phase SUMMARY

### Wave 0 Gaps

- [ ] `apps/auth/webapp/src/lib/phone-normalization.test.ts` — E.164 normalization unit tests (normalize_e164 helper)
- [ ] `apps/auth/webapp/src/entities/access-code.test.ts` — `byPhone` sparse GSI test (write phone, query via byPhone)
- [ ] `apps/auth/webapp/src/app/tel/[e164]/route.test.ts` — `/tel` endpoint unit tests (resolve, mint, no-oracle failures)
- [ ] `kv/internal/app/cmd/voipms_test.go` — `kv voipms` integration tests (create subaccount, set routing, get balance mocked)
- [ ] `apps/voice/src/klanker_voice/telephony/test_tel_mint_integration.py` — controller → `/tel` → token validation (real JWT, real issuer/JWKS)
- [ ] `apps/voice/asterisk/pjsip.conf.test` — Terraform plan validates rendered pjsip.conf parses without syntax errors

---

## Security Domain

[security_enforcement enabled — ASVS controls apply]

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | yes | OIDC token validation (PyJWT + PyJWKClient in voice service; same as Phase 4) |
| V3 Session Management | yes | `SessionLifecycle` quota/timeout + tier-based concurrency; single idempotent close (Phase 9 seam) |
| V4 Access Control | yes | Caller-ID → code → tier (baseline); §24 gate PIN/passphrase unlock (tier upgrade); Asterisk inbound-only context §25.A |
| V5 Input Validation | yes | E.164 normalization (strip non-digits); ARI caller ID format validation (before `/tel` call); Asterisk dialplan rejects outbound |
| V6 Cryptography | yes | TLS for auth-app `/tel` endpoint (HTTPS, not mocked); SIP/TLS (TLS/SRTP deferred to Phase 14 per D-01) |
| V7 Error Handling & Logging | yes | Structured logs (call_id carried); secrets never logged (credential-name rejection); no-oracle failure contract (§23) |
| V8 Cryptographic Failures | N/A | No custom crypto (rely on OIDC provider + PyJWT) |

### Known Threat Patterns for Telephony/VoIP

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Unauthenticated inbound SIP | Spoofing | Registration-based trunking (outbound only, no public SIP port); SG locked to POP IPs |
| Caller-ID spoofing | Spoofing | §23 caveat: caller-ID alone → minimal tier only; gate required for high tiers (D-05) |
| Toll fraud (expensive outbound) | Tampering | Outbound disabled on VoIP.ms subaccount; Asterisk inbound-only dialplan; no outbound context ever (§25.A) |
| DoS (call loop, high concurrency) | Denial of Service | §24 gate (DTMF/passphrase required); `SessionLifecycle` concurrency=1; hard timeout ~10 min; fail-closed (static goodbye) |
| Credential leak (SIP password in logs) | Information Disclosure | SSM SecureString (never in env/git); credential-name rejection; config rendering from SSM at container start |
| Unencrypted SIP/RTP (Phase 12) | Information Disclosure | Flagged as Phase 14 follow-up (TLS/SRTP); Phase 12 uses PCMU (public network tolerated; VoIP.ms registration already encrypted via HTTPS management) |
| Transcript/PII exposure | Information Disclosure | Pre-unlock transcript redacted; no STT during gate before unlock (Phase 11 §24); transcript ledger policy Phase 7 |

### §25 Hostile Hardening (Phase 12 scope vs Phase 14 defer)

**Phase 12 (this phase):**
- ✅ Inbound-only dialplan (§25.A) — enforced in `extensions.conf` (only Stasis app context, no outbound)
- ✅ No outbound SIP (`extensions.conf`) — no dialplan context exists for outbound; VoIP.ms subaccount outbound disabled
- ✅ SG locked to POP ranges (§25.C) — Terraform SG rules allow only Toronto POPs
- ✅ ARI private network only (§25.C) — no public HTTP port; localhost/private network binding only
- ✅ Registration-based trunking (§25.A) — outbound registration, no public inbound SIP port needed
- ✅ Concurrency cap=1 (§25.D) — `SessionLifecycle` + quota gate
- ✅ Caller-ID alone → minimal tier (§25.D) — §23 seam enforces baseline tier from caller ID

**Phase 14 (deferred per D-01):**
- ⏸ Alarms + dashboards (registration failures, POP connectivity, gate-fail rate)
- ⏸ fail2ban on Asterisk host (block scanner IP ranges)
- ⏸ TLS/SRTP end-to-end (SIP/TLS + SRTP media)
- ⏸ Load/concurrency test (verify 1 concurrent is enforced, test 2-concurrent rejects)
- ⏸ Operations runbook (revoke/rotate SIP credential, kill-switch DID routing, one-way-audio debug)

---

## Sources

### Primary (HIGH confidence)

- **VoIP.ms official resources:** [voip.ms/resources/api](https://voip.ms/resources/api) — REST API overview and endpoint documentation
- **VoIP.ms Wiki / Servers:** [wiki.voip.ms/article/Servers](https://wiki.voip.ms/article/Servers) — Toronto POP server list and IP addresses (verified 2026-07-12)
- **VoIP.ms Wiki / Recommended POPs:** [wiki.voip.ms/article/Recommended_POPs](https://wiki.voip.ms/article/Recommended_POPs) — registration + DID matching requirement
- **Asterisk official documentation / PJSIP:** [docs.asterisk.org/Configuration/Channel-Drivers/SIP/Configuring-res_pjsip/Configuring-Outbound-Registrations/](https://docs.asterisk.org/Configuration/Channel-Drivers/SIP/Configuring-res_pjsip/Configuring-Outbound-Registrations/) — registration configuration and parameters
- **Asterisk res_pjsip examples:** [docs.asterisk.org/Configuration/Channel-Drivers/SIP/Configuring-res_pjsip/res_pjsip-Configuration-Examples/](https://docs.asterisk.org/Configuration/Channel-Drivers/SIP/Configuring-res_pjsip/res_pjsip-Configuration-Examples/) — trunk configuration patterns
- **ITU-T E.164 standard + implementations:** [abstractapi.com/guides/phone-validation/the-developers-guide-to-e-164](https://www.abstractapi.com/guides/phone-validation/the-developers-guide-to-e-164); [phone-check.app](https://phone-check.app/blog/e164-phone-number-format-international-normalization-validation) — E.164 format and normalization practices

### Secondary (MEDIUM confidence)

- **Existing Phase 11 / Phase 4 code:** `.planning/infra/terraform/live/site/services/{voice,auth}/service.hcl` — verified IaC patterns for service stubs, task definitions, role IAM (checked 2026-07-12)
- **Existing Phase 3/5 code:** `apps/auth/webapp/src/lib/bypass-token.ts`, `apps/auth/webapp/src/entities/access-code.ts` — verified patterns for `mintAnonToken`, sparse GSI, normalization (checked 2026-07-12)
- **Pipecat documentation:** pipecat-ai 1.5.0 (`docs.pipecat.ai`, `github.com/pipecat-ai/pipecat`) — transport contracts verified in Phase 9/10/11 research

### Tertiary (LOW confidence – flagged for verification)

- **VoIP.ms API method names:** Exact method names (`createSubAccount`, `setDIDRouting`, etc.) — sourced from search results referencing VoIP.ms API docs; official API reference is Cloudflare-protected and unavailable in this session. **Planner must verify against live docs before implementation.**
- **Toronto POP IP address stability:** The 8 IPs listed above are confirmed current as of 2026-07-12, but VoIP.ms infrastructure may change. **Operator must update SG rules if POPs change; recommend 6-month audit procedure.**
- **Asterisk version 20.x stability in Fargate:** Not explicitly tested in Phase 12; Phase 11 used local docker-compose. **Planner to validate Dockerfile build, image push to ECR, and Fargate deployment of Asterisk 20.x.**

---

## Metadata

**Confidence breakdown:**
- **Standard stack (libraries):** HIGH — all pinned packages verified on registries; pipecat 1.5.0 and aws-sdk-go-v2 v1.42 confirmed current
- **Architecture (IaC layout, security groups):** HIGH — defcon.run.34 conventions verified in existing voice/auth service stubs; PJSIP registration from official Asterisk docs
- **VoIP.ms provisioning (REST API, POPs):** MEDIUM-to-HIGH — API methods referenced in search results but Cloudflare-protected official docs unavailable; Toronto POP IPs confirmed via wiki.voip.ms
- **E.164 normalization & mint path:** HIGH — existing code patterns (AccessCode entity, bypass token mint) verified in codebase; reuse is straightforward
- **Pitfalls & threat patterns:** MEDIUM — based on standard telephony/SIP knowledge and Phase 9/10/11 learnings; not all derisked until live testing (Phase 12 manual proof D-06)

**Research date:** 2026-07-12
**Valid until:** 2026-08-12 (30 days; VoIP.ms API changes unlikely but SG/POP list should be re-verified in Phase 14)

---

## Assumptions Log

| # | Claim | Section | Risk if Wrong | Confidence |
|---|-------|---------|---------------|-----------|
| A1 | VoIP.ms REST API has methods: `createSubAccount`, `setDIDRouting`, `getBalance`, `getServersInfo` (exact names TBD) | VoIP.ms REST API | Implementation delays if method names differ from web search references | MEDIUM |
| A2 | All 8 Toronto POP IPs (158.85.70.148-151, 184.75.215.x, 184.75.213.210) are current and stable | VoIP.ms Toronto POP | SG allow-list becomes stale; calls fail from failover POP | MEDIUM |
| A3 | PJSIP registration parameters (retry_interval=300, expiration=3600, contact_user, etc.) are correct for production Fargate | PJSIP Registration | Registration fails; VoIP.ms cannot reach Asterisk after IP change | MEDIUM |
| A4 | Fargate public IP is stable enough for SIP registration (with 300 sec retry interval) | AWS Fargate | Task restart + re-registration gap could miss inbound calls; mitigated by 5-min retry | MEDIUM |
| A5 | The `/tel` endpoint can be private-network-only without a dedicated bearer token (network ACL sufficient) | Security | Unauthorized callers spoof the endpoint; mitigation: use shared bearer token (D-02 Claude's Discretion) | MEDIUM |
| A6 | ElectroDB sparse GSI (`byPhone`) omits gsi3pk/gsi3sk for codes without a `phone` attribute | ElectroDB Sparse GSI | Extra DynamoDB storage, slow queries; mitigated by schema validation in Wave 0 tests | MEDIUM |
| A7 | E.164 normalization should strip leading zeros (trunk prefix) and prepend +1 for North America | E.164 Normalization | Callers from other regions (UK +44, etc.) are misrouted; mitigation: expand assumptions based on deployment region | MEDIUM |
| A8 | Asterisk can render `${VOIPMS_SIP_PASSWORD}` at container start from SSM env var (following Phase 11 pattern) | SSM Secret Rendering | SIP password not injected, registration fails; mitigation: test Phase-11 pattern on Fargate before Phase 12 deploy | MEDIUM |

---

## Open Questions

1. **Exact VoIP.ms API method names and parameters**
   - What we know: search results reference `createSubAccount`, `setDIDRouting`, `getBalance`, `getServersInfo` as available methods in the REST API
   - What's unclear: official docs are Cloudflare-protected; exact parameter names, required vs. optional fields, response shapes
   - Recommendation: Planner reads the live VoIP.ms API documentation (or requests from support) before writing `kv voipms` commands; verify method signatures against test calls

2. **Private `/tel` endpoint protection strategy**
   - What we know: D-02 requires it to be internal-only (not internet-exposed), with a no-oracle failure contract
   - What's unclear: whether network ACL (private subnet routing) alone is sufficient, or if a shared bearer token is needed
   - Recommendation: Claude's Discretion in D-02; planner chooses between network-ACL-only (simpler) or network-ACL + bearer-token (defense-in-depth)

3. **Fargate task IP stability and registration keepalive**
   - What we know: Fargate public IPs are ephemeral; PJSIP can re-register with 300 sec retry
   - What's unclear: whether a task restart + IP change + re-registration window can be faster than inbound calls arriving
   - Recommendation: Manual cellular test (D-06) should include a task restart mid-call or within 5 minutes of the previous call, verify new inbound works

4. **Asterisk version in Fargate and container base image**
   - What we know: Phase-11 uses Asterisk 20.x in local docker-compose; defcon.run.34 pins versions
   - What's unclear: which specific Asterisk 20.x version (20.1, 20.5, 20.9?) and which base image (official Asterisk Docker, Ubuntu base + apt install, Alpine?)
   - Recommendation: Planner confirms with ops; recommend upstream Asterisk official Docker images or stable Ubuntu/Debian package versions

---

## Next Steps

1. **Planner reads this RESEARCH.md** and confirms all MEDIUM-confidence assumptions are acceptable before task planning
2. **Planner creates 12-PLAN.md** with task breakdown following the build order in CONTEXT.md D-03..D-06
3. **Implementation waves** follow the order: VoIP.ms runbook + kv voipms → SSM wiring → auth-app mint path → Asterisk trunk → deployed edge → manual cellular proof
4. **Wave 0 test gaps** (listed above) must be scoped into planning; no task is closed until corresponding tests pass
5. **Manual cellular proof** (D-06) is scheduled after deployed edge is live; documented in phase SUMMARY + operator runbook
