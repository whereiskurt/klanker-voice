# Phase 4: Voice Service Deployed & Quota Enforcement - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-05
**Phase:** 4-voice-service-deployed-quota-enforcement
**Areas discussed:** Concurrency & usage race-safety, Session lifecycle: wind-down + teardown, Kill-switch + operator loop, Deploy/autoscale & ICE smoke test

> Session note: resumed from an interrupted checkpoint (area 1 completed in a prior
> session). Areas 2–4 discussed this session; the user accepted the recommended option
> on every question.

---

## Concurrency & usage race-safety
*(Completed in prior session — decisions carried from checkpoint.)*

**Decisions:** heartbeat-lease concurrency slots (self-healing, ~45s TTL); service-timer-authoritative stop clock (precise per-session cutoff) with the 15s tick owning durable accounting + heartbeat renewal; `/api/offer` start gate that rejects no-access, acquires the slot atomically, and blocks sub-floor (<~30s remaining) starts with a typed error.

---

## Session lifecycle: wind-down + teardown

| Question | Options | Selected |
|----------|---------|----------|
| Warning + goodbye delivery | Hybrid (LLM-woven warning, deterministic TTS goodbye) ✓ / Fully LLM-driven / Fully scripted TTS | Hybrid |
| Hard stop strictness | Finish-utterance + ~5s grace ✓ / Hard cut at 0 | Finish + grace |
| Idle teardown layers | ICE disconnect ✓ / user-silence VAD ✓ / bot-stall·pipeline-error ✓ | All three |
| Transport drop | Short reconnect grace ~10–15s ✓ / Immediate teardown | Reconnect grace |

**User's choice:** all recommended.
**Notes:** reconnect grace explicitly motivated by hostile conference networks + mobile handover; three idle layers sit atop the area-1 wall-clock outer bound.

---

## Kill-switch + operator loop

| Question | Options | Selected |
|----------|---------|----------|
| Kill-switch store | DynamoDB control item ✓ / SSM parameter / config-table field | DynamoDB control item |
| Manual vs auto-trip | Manual + auto-trip on daily budget ✓ / Manual-only | Manual + auto-trip |
| Site-wide aggregation | Global daily rollup item ✓ / Sum per-user on demand | Global rollup item |
| Reject seam | Distinct typed errors ✓ / Generic rejection | Distinct typed errors |

**User's choice:** all recommended.
**Notes:** auto-trip framed as the real budget circuit breaker for the public mic; typed errors are the clean seam to Phase-5 friendly pages.

---

## Deploy/autoscale & ICE smoke test

| Question | Options | Selected |
|----------|---------|----------|
| srflx candidate path | Self-advertise metadata IP + STUN backup ✓ / Public STUN only / Private subnet + NAT + TURN | Metadata IP + STUN backup |
| Autoscale + scale-in | Session-count metric + task scale-in protection ✓ / CPU + ECS draining | Session-count + protection |
| Per-task capacity | Per-task soft cap + retryable reject ✓ / Assume headroom, no cap | Soft cap + reject |
| Smoke test | Full offer→ICE→RTP, quota-bypass ✓ / Lightweight reachability | Full offer→ICE→RTP |

**User's choice:** all recommended.
**Notes:** TURN explicitly declined (deferred per CLAUDE.md); smoke test must prove RTP actually flows, not just port reachability.

---

## Claude's Discretion
- DynamoDB `usage`-table key/index design and item shapes (heartbeat, per-user usage, global rollup, control item)
- Concrete thresholds (renew interval/TTL, sub-floor, silence timeout, grace windows, per-task max_sessions, auto-trip ceiling)
- Warning/goodbye copy + LLM-instruction injection mechanism
- STUN server choice; task-metadata read mechanics
- `kv usage`/`killswitch`/`smoke` command surface + output; smoke-test service credential
- CloudWatch custom-metric publication + target-tracking tuning

## Deferred Ideas
- TURN fallback for UDP-blocked networks (revisit only if mandatory)
- `kv sessions` live session inspection (KV-06)
- Finer per-provider cost attribution in the rollup
