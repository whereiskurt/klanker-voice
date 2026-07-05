# Phase 3: Auth Service & Access Codes - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-05
**Phase:** 3-Auth Service & Access Codes
**Areas discussed:** Token claims contract, Access-code model, Port strategy & versions, App location & quota boundary

---

## Token claims contract

| Option | Description | Selected |
|--------|-------------|----------|
| Tier id + group only | Token carries tier_id + group; voice reads tiers table for limits | ✓ |
| Denormalized limits in token | session/daily/concurrency numbers in the token | |
| Both — id + snapshot | tier_id AND limits snapshot | |

| Option | Description | Selected |
|--------|-------------|----------|
| JWT access token via Resource Indicators | Enable RI; aud=voice resource; PyJWT validates offline | ✓ |
| Use the ID token as bearer | Accept ID token (aud=client) | |
| You decide | Planner picks after checking oidc-provider 9.x | |

| Option | Description | Selected |
|--------|-------------|----------|
| TTL > longest tier + reconnect | ~45–60 min; token gates establishment only | ✓ |
| Short TTL + refresh | ~10 min access + refresh rotation | |
| You decide | Planner picks a concrete TTL | |

**User's choices:** Tier id + group only / JWT access token via Resource Indicators / TTL > longest tier + reconnect

---

## Access-code model

| Option | Description | Selected |
|--------|-------------|----------|
| ElectroDB single-table | access_codes as another entity in run.auth's single table | ✓ |
| Separate dedicated table | Its own DynamoDB table | |
| You decide | Planner picks after reading the ElectroDB service | |

| Option | Description | Selected |
|--------|-------------|----------|
| Per-login, latest wins | Code at this login sets this session's tier | ✓ |
| Sticky first redemption | First code binds permanently to profile | |
| You decide | Planner models it | |

| Option | Description | Selected |
|--------|-------------|----------|
| Unique users | max_redemptions caps distinct redeeming users | ✓ |
| Total redemption events | Every login increments | |
| You decide | Planner models it | |

**User's choices:** ElectroDB single-table / Per-login latest-wins / Unique users

---

## Port strategy & versions

| Option | Description | Selected |
|--------|-------------|----------|
| Port as-is, upgrade later | Match run.auth's exact versions; bump as a separate task | ✓ |
| Bump during the port | Upgrade to CLAUDE.md pins in the port pass | |

| Option | Description | Selected |
|--------|-------------|----------|
| Copy wholesale, then trim | Copy verbatim → green → delete DEF CON specifics | ✓ |
| Selective port (spine only) | Copy only named pieces from the start | |

**User's choices:** Port as-is / Copy wholesale then trim

---

## App location & quota boundary

| Option | Description | Selected |
|--------|-------------|----------|
| apps/auth/webapp/ | Mirror apps/voice/; keep webapp/ subdir so paths port unchanged | ✓ |
| apps/auth/ flattened | Collapse webapp/ up | |
| You decide | Planner picks after Dockerfile path check | |

| Option | Description | Selected |
|--------|-------------|----------|
| P3 = tiers + codes only | usage/enforcement/kill-switch all Phase 4; drop DEF CON quota code | ✓ |
| P3 creates all tables, P4 enforces | tiers+codes+usage schema in P3 | |
| Port run.auth quota wholesale | Bring user-quota.ts + quota.ts as-is | |

**User's choices:** apps/auth/webapp/ / P3 = tiers + codes only

---

## Claude's Discretion

Claim namespace/naming, issuer/audience URIs, seed-code values beyond spec examples, code
case-sensitivity/format, interstitial page design, Altcha server-key wiring, secrets env
mapping (from-aws.tmpl→SSM), kv command surface, ElectroDB index design, copy method.

## Deferred Ideas

- Dependency bump to CLAUDE.md pins (separate post-port task)
- All quota enforcement (Phase 4)
- run.auth's DEF CON quota code (dropped; P4 rebuilds)
- kv sessions / KV-06 live inspection (until a multi-user event)
