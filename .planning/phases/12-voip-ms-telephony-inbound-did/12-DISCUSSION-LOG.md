# Phase 12: VoIP.ms Telephony — Inbound DID - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-12
**Phase:** 12-voip-ms-telephony-inbound-did
**Areas discussed:** Test topology, Mint boundary, VoIP.ms provisioning, Secrets, Phase 12↔14 boundary, Tier composition + seed, Exit proof + CI

---

## Test topology (where Asterisk runs + VoIP.ms connection)

| Option | Description | Selected |
|--------|-------------|----------|
| Local + registration trunk | Keep Phase-11 local docker-compose Asterisk; add VoIP.ms registration-based outbound trunk (no public inbound port, no cloud). Cloud edge stays Phase 14. | |
| Stand up cloud edge early | Pull the Phase-14 telephony-edge deploy forward so the DID hits a cloud Asterisk (public IP, SG locked to POP ranges). | ✓ |
| Temp-exposed local host | Local Asterisk with a temporary public IP/port-forward for the test window. | |

**User's choice:** Stand up cloud edge early.
**Notes:** Reframed as a deliberate pull-forward. Registration-based trunking (§4) is still the mechanism (no public inbound SIP port); the SG-lock-to-POP is defense-in-depth. Forced the Phase 12↔14 boundary follow-up (see below).

---

## Mint boundary (how the Python controller gets a minted token from caller ID)

| Option | Description | Selected |
|--------|-------------|----------|
| Private auth endpoint (mirror /join) | New INTERNAL-only auth-app route (e.g. GET /tel/<e164>): byPhone GSI → resolveAccessCode → mintAnonToken → token. Not internet-exposed like /join. | ✓ |
| Shared mint helper, no endpoint | Extract resolve+mint into a shared module — but controller is Python, mint is TS/jose (cross-language, still needs a boundary). | |
| Asterisk-level caller-ID map | Map caller-ID→code in Asterisk, pass code to controller. Pushes secrets/mapping into Asterisk config. | |

**User's choice:** Private auth endpoint (mirror /join).
**Notes:** The endpoint is a token-minting oracle — must be private-network/edge-locked, unlike public /join, and must preserve the bypass no-oracle failure contract.

---

## VoIP.ms provisioning (automate vs runbook)

| Option | Description | Selected |
|--------|-------------|----------|
| kv voipms automation + runbook | `kv voipms` drives the API-drivable steps (subaccount, DID route, caps); a runbook covers portal-only steps (2FA, balance, API IP whitelist per §25.F). | ✓ |
| Runbook only (manual portal) | Document every step; no new kv code. Fastest, less repeatable. | |
| kv voipms only, skip runbook | Automate via kv, minimal prose. Risky — skips documenting the portal-first security steps. | |

**User's choice:** kv voipms automation + runbook.
**Notes:** Portal-first security steps (2FA, restrictions, whitelist) are exactly the ones not to skip/under-document; API steps benefit from being scriptable.

---

## Secrets (SSM now vs defer to Phase 14)

| Option | Description | Selected |
|--------|-------------|----------|
| Minimal SSM now for SIP/DID | Pull SSM forward just for the new public-edge secrets; full provisioning stays Phase 14. | |
| Keep env, defer all SSM to 14 | Continue Phase-11 local-env pattern; move all SSM to Phase 14. Tension with SC#1. | |
| Full SSM + wiring now | Complete SSM SecureString + valueFrom wiring this phase. | ✓ |

**User's choice:** Full SSM + wiring now.
**Notes:** Consistent with the cloud-edge pull-forward — a deployed container consumes secrets via valueFrom, not env. Satisfies SC#1 ("secrets live only in SSM"). SIP password is rendered into Asterisk config at container start, not passed into Klanker Python.

---

## Phase 12↔14 boundary (what stays in Production Hardening)

| Option | Description | Selected |
|--------|-------------|----------|
| 12 = working secure edge; 14 = ops hardening | Phase 12: deployed, SSM-backed, inbound-only edge + SG-to-POP + cellular proof. Phase 14: alarms/dashboards, fail2ban, TLS/SRTP, load test, failure-routing, runbook. | ✓ |
| Fold 14 into 12 | Do all hardening now, collapse/repurpose Phase 14. | |
| Minimal deploy now; hardening + IaC cleanup in 14 | Just enough deployed edge to test; leave SG-tightening, TLS/SRTP, alarms, fail2ban, load test to 14. | |

**User's choice:** 12 = working secure edge; 14 = ops hardening.
**Notes:** Keeps Phase 14 substantive. SG-lock-to-POP + inbound-only + private ARI come now (table-stakes for a public DID at DEF CON); observability/anti-abuse polish is Phase 14.

---

## Tier composition + seed data

| Option | Description | Selected |
|--------|-------------|----------|
| Baseline mint + gate upgrade + seed kph-tier & Kurt's map | Caller-ID mint → constrained baseline tier; Phase-11 gate is the only path to kph-tier. Seed kph-tier row + Kurt's phone→defcon34 via new `kv code phone`. | ✓ |
| Baseline mint + gate upgrade; seed as separate operator step | Same composition; treat seeding as manual/later, not a Phase-12 deliverable. | |
| Caller-ID grants full mapped tier directly | Caller-ID mints the actual tier (incl. kph-tier) without the gate. REJECTED by §23 (spoofable). | |

**User's choice:** Baseline mint + gate upgrade + seed kph-tier & Kurt's map.
**Notes:** Composes the new caller-ID identity source with the Phase-11 gate's unlock-upgrade. The full-tier-on-caller-ID option is explicitly recorded as rejected per §23's spoofing caveat.

---

## Exit proof + CI artifacts

| Option | Description | Selected |
|--------|-------------|----------|
| Manual cellular proof + auth/kv/config unit tests | Manual documented cell call is the exit proof; CI covers the mint path (byPhone, /tel, normalization, no-oracle), kv commands, and Asterisk registration-config validation. | ✓ |
| Manual proof only | Just the documented call; skip new automated tests. Leaves the security-critical mint path unguarded in CI. | |

**User's choice:** Manual cellular proof + auth/kv/config unit tests.
**Notes:** Mirrors the Phase-11 §19-C manual softphone proof; keeps the full existing suite green.

---

## Claude's Discretion

- Exact `/tel` endpoint path/shape and private-network lock mechanism (ACL vs bearer token vs both).
- `byPhone` GSI index name / key templates and E.164 normalization helper location.
- `kv voipms` sub-command tree and which API calls are wrapped vs left to the runbook.
- Terraform/Terragrunt module shape for the minimal `telephony-edge` deploy (reuse defcon.run.34 conventions).
- Which VoIP.ms POP IP ranges the SG allow-list uses and how they're sourced.
- Whether `kph-tier` seeding is a script vs a captured `kv` invocation.

## Deferred Ideas

- Phase 13: physical payphone (`payphone-ata` subaccount, ATA gain/DTMF/echo tuning, VoIP.ms echo test).
- Phase 14: alarms/dashboards, fail2ban, TLS/SRTP, load/concurrency test, failure-routing polish, ops runbook, IaC cleanup beyond the minimal edge.
- G.722 HD SIP-to-SIP audio (§4) — narrowband PCMU is the Phase-12 codec.
- Pre-rendered PSTN greeting clip (§12) — after canonical greet_now() proven on PSTN.
