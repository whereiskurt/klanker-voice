# Phase 4: Voice Service Deployed & Quota Enforcement - Context

**Gathered:** 2026-07-05
**Status:** Ready for planning

<domain>
## Phase Boundary

Deploy the Pipecat voice service to public-IP Fargate tasks and prove real browser↔task
UDP media (the first deployed ICE/RTP test), then wrap every session in race-safe quota
enforcement, graceful spoken wind-down + abandoned-session teardown, a site-wide
budget kill-switch, autoscaling with scale-in protection, and the `kv` operator loop.

**In scope:** INFR-03 (deployed WebRTC: public-IP tasks, wide UDP SG range,
STUN/metadata srflx candidates, ICE smoke test), INFR-06 (autoscale 1→4 w/ scale-in
protection), QUOT-01..05 (start-gate blocking, conditional-write usage ticks + hard-stop,
spoken warning/goodbye incl. daily-exhaustion, kill-switch, layered idle teardown),
KV-03 (usage view), KV-04 (kill-switch flip), KV-05 (deployed smoke test). The `usage`
table + control/rollup items, the quota logic in the voice service, and the deploy/
autoscale infra delta.

**NOT in scope:** the browser client UX and the friendly rejection pages themselves
(Phase 5, CLNT-*); latency v2 tuning (Phase 6); KPH knowledge retrieval (Phase 7);
`kv sessions` live inspection (KV-06, deferred); TURN fallback (deferred per CLAUDE.md).

**Consumes the Phase-3 contract:** the JWT access token (tier_id + group claims,
audienced to the voice resource) is presented at `/api/offer`; the voice service reads
the `tiers` table for actual limits (D-01/D-02/D-03 from Phase 3).

</domain>

<decisions>
## Implementation Decisions

### Concurrency & usage race-safety (QUOT-01, QUOT-02)
- **D-01: Concurrency slot = heartbeat lease.** Each active session writes a heartbeat
  item renewed every 15s tick (expires ~45s TTL); a user's concurrency = count of
  non-expired heartbeats. A crashed task stops renewing → the slot self-expires in
  seconds (self-healing, no reaper process). The slot is acquired atomically via a
  conditional write at `/api/offer`.
- **D-02: Stop clock = service-timer authoritative.** At `/api/offer` the task knows
  `session_start` + tier `session_max` and schedules an exact in-memory stop (warning at
  −30s, goodbye at 0) — precise, no 15s slop on the per-session cap. The 15s tick's job is
  durability + accounting: it conditionally persists `seconds_used` to DynamoDB (daily/
  period totals) and renews the concurrency heartbeat. Daily/period exhaustion detected at
  a tick triggers the same wind-down.
- **D-03: Start gate at `/api/offer`.** No-access tier (`session_max=0`) always rejects;
  the concurrency slot is acquired atomically (conditional write). **Block sub-floor
  starts:** reject if remaining daily time < a config floor (~30s) with a typed
  "daily limit reached" error — avoid frustrating micro-sessions.

### Session lifecycle: wind-down + teardown (QUOT-03, QUOT-05)
- **D-04: Wind-down delivery = hybrid.** At −30s, inject a high-priority instruction into
  the LLM context so the concierge weaves the warning in naturally/in-voice. At 0, a
  deterministic pre-scripted goodbye line goes straight to TTS (bypassing the LLM),
  then a hard media close — natural warning, guaranteed stop. The **same** wind-down
  fires on mid-session daily/period exhaustion (satisfies QUOT-03's daily-exhaustion
  clause).
- **D-05: Hard stop = finish-utterance + small grace.** Let the goodbye TTS finish,
  capped at a ~5s grace window, then force-close the pipeline and release the slot.
  Natural; the extra seconds are negligible on cost/quota.
- **D-06: Idle teardown = three layers** atop the D-02 wall-clock outer bound:
  (1) ICE/transport disconnect, (2) user-silence VAD timeout (~45–60s with no user
  speech), (3) bot-speaking stall / unrecoverable STT·LLM·TTS pipeline error. Any layer
  tears down the session and releases the slot.
- **D-07: Transport drop = short reconnect grace (~10–15s)** before teardown, so a brief
  blip / mobile handover on hostile conference networks can reconnect into the same
  session. The heartbeat lease (D-01, ~45s TTL) still self-heals the slot if the task
  itself dies; the Phase-3 token TTL (D-03 there, ~45–60 min) permits the reconnect.

### Kill-switch & operator loop (QUOT-04, KV-03, KV-04)
- **D-08: Kill-switch state = DynamoDB control item** in the same table the task already
  hits for usage. `/api/offer` reads it on every start gate; `kv` flips it via a
  conditional write — no new dependency, near-instant propagation.
- **D-09: Kill-switch = manual + auto-trip.** Operator can flip it any time via `kv`
  (KV-04) AND the system auto-engages it when site-wide daily usage crosses a configured
  ceiling (seconds or est. $); operator manually resets. This is the automatic circuit
  breaker that bounds the ~$120–165/mo cap on a public mic (design spec §6 "site-wide
  daily budget kill-switch").
- **D-10: Site-wide aggregation = global daily rollup item.** The same conditional-write
  usage tick adds its delta to a site-wide "today" counter (total seconds, session count,
  est. cost). O(1) read for both the KV-03 operator view and the D-09 auto-trip check —
  no scan.
- **D-11: Reject seam = distinct typed errors** at `/api/offer`: `site-paused`
  (kill-switch), `daily-limit`, `concurrency-limit`, `no-access`. The Phase-5 client maps
  each to its own friendly page (satisfies QUOT-04's friendly-page requirement); a
  site-wide "we're paused" page reads very differently from "you've used your time today."

### Deploy, autoscale & ICE smoke test (INFR-03, INFR-06, KV-05)
- **D-12: srflx path = self-advertise metadata IP + STUN backup.** The task reads its own
  public IP from the ECS task-metadata endpoint and injects it as a candidate directly
  (deterministic, no third-party on the connect path), AND also gathers a public STUN
  srflx candidate as belt-and-suspenders. Public subnet, `assign_public_ip=ENABLED`, wide
  UDP SG ingress (e.g. 20000–20100). No TURN.
- **D-13: Autoscale = session-count metric + task scale-in protection.** Scale 1→4 on a
  custom "active sessions" CloudWatch metric with target tracking (sessions are
  long-lived; CPU-per-session is fairly fixed). Each task calls the ECS
  task-scale-in-protection API to shield itself while it holds ≥1 active session and
  clears protection when idle — autoscaling never kills a task mid-conversation.
- **D-14: Per-task soft cap + retryable reject.** Each task has a configured
  `max_sessions` (design spec §5 sizes ~5 concurrent/task); beyond it, `/api/offer`
  returns a typed **retryable** "at capacity, try again" error while autoscaling adds a
  task. Self-protecting; the D-13 metric keeps headroom so caps are rarely hit.
- **D-15: `kv` smoke test = full offer→ICE→RTP, quota-bypass path.** `kv` sends a real
  synthetic WebRTC offer to the live `/api/offer`, negotiates ICE to `connected`,
  confirms RTP frames actually flow to the public-IP task, then tears down — exercising
  the true INFR-03 path end to end. Runs via a dedicated smoke/service credential that
  bypasses quota accounting and the D-10 budget rollup, so it never burns a user's
  allotment or trips the D-09 auto-kill-switch.

### Claude's Discretion
- Exact DynamoDB `usage`-table key/index design and item shapes (heartbeat lease item,
  daily per-user usage item, global rollup item, control/kill-switch item) — the design
  spec §6 seeds `usage` keyed `user_id × yyyy-mm-dd` / `seconds_used`; extend from there.
- Concrete thresholds: heartbeat renew interval (15s) & TTL (~45s), sub-floor seconds,
  user-silence timeout, reconnect-grace length, goodbye grace cap, per-task `max_sessions`,
  the auto-trip site-wide ceiling and est.-cost formula.
- Warning/goodbye copy and exact LLM-instruction injection mechanism (Pipecat frame /
  context push) and how the deterministic goodbye bypasses the LLM.
- STUN server choice for the backup srflx candidate; task-metadata read mechanics.
- `kv usage` / `kv killswitch` / `kv smoke` command surface, flags, and output formatting
  (lipgloss tables per CLAUDE.md); smoke-test service-credential mechanism.
- CloudWatch custom-metric publication mechanism and target-tracking policy tuning.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Design & requirements
- `docs/superpowers/specs/2026-07-04-klanker-voice-design.md` §5 (pipeline, SmallWebRTC
  transport, `/api/offer` over HTTPS through the ALB, deploy topology, 1vCPU/2GB ~5
  sessions/task, autoscale 1→4, UDP-block fallback risk) — the deploy + transport contract
- `docs/superpowers/specs/2026-07-04-klanker-voice-design.md` §6 (auth & quota model:
  `usage` table keyed `user_id × yyyy-mm-dd`/`seconds_used`, 15s tick, site-wide daily
  budget kill-switch, typed rejection at `/api/offer`, spoken wind-down + daily-exhaustion)
- `docs/superpowers/specs/2026-07-04-klanker-voice-design.md` §7 (cost table — the budget
  the kill-switch protects) and §8 (risks: WebRTC/UDP failure paths; TURN fallback listed
  future/out-of-scope) and §9 (testing: quota check/increment/concurrency, UDP-blocked path)
- `.planning/REQUIREMENTS.md` — INFR-03, INFR-06, QUOT-01..05, KV-03, KV-04, KV-05
- `.claude/CLAUDE.md` — infra note (Fargate public IPs + bounded UDP SG 20000–20100 as the
  only infra delta; SmallWebRTC committed, TURN "revisit only if mandatory"), PyJWT
  token-validation stack, and the `kv` CLI stack (cobra + aws-sdk-go-v2 ecs/dynamodb/ssm)

### Phase-3 token contract this phase consumes (the seam Phase 4 blocks on)
- `.planning/phases/03-auth-service-access-codes/03-CONTEXT.md` — D-01 (thin token:
  tier_id + group only; voice reads `tiers` table for limits), D-02 (JWT access token via
  Resource Indicators, `aud=voice.*`, PyJWT+PyJWKClient offline validation), D-03 (token
  TTL exceeds longest tier + reconnect window). **Drop DEF CON quota code; rebuild against
  the design-spec schema** (D-11 there) — this phase does that rebuild.

### Phase-2 infra this phase extends
- `.planning/phases/02-infra-skeleton/02-*-SUMMARY.md` — the `network` / `ecs-service`
  terragrunt modules, ECR repos, the DynamoDB unit (add the `usage` table here by editing
  its service.hcl, per the Phase-2/3 pattern), and the github-oidc + build/deploy CI path
  the voice container rides on. The WebRTC infra delta (public IP + UDP SG range +
  session-count autoscaling/scale-in protection) is new here.

### Existing voice-service code to extend
- `apps/voice/bot.py` — the current single-file Pipecat server (SmallWebRTC + `/api/offer`);
  Phase 4 adds the start-gate quota check, service-timer wind-down, teardown layers, and
  session-count metric emission here
- `apps/voice/pipeline.toml` + `apps/voice/configs/*.toml` — config-driven pipeline; new
  quota/deploy knobs (D-15 discretion list) follow this config pattern
- `apps/voice/tests/test_smoke.py` — existing smoke-test seed to grow into the KV-05 path

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `apps/voice/bot.py`: the working local Pipecat pipeline + SmallWebRTC `/api/offer` from
  Phase 1 — the deploy target and the place quota/lifecycle logic hooks in.
- Config-driven pipeline (`pipeline.toml`, `configs/*.toml`, `console.py` factories) —
  thresholds and quota knobs slot into the same config system rather than hard-coding.
- Phase-2 `ecs-service`/`network` terragrunt modules + DynamoDB unit — the `usage` table
  and the public-IP/UDP-SG delta are edits to existing units, not new modules (matches the
  Phase-3 "add a table by editing service.hcl" pattern).
- Phase-3 JWT + `kv` cobra scaffold — `kv usage/killswitch/smoke` join the existing
  command tree; PyJWKClient validation added to `bot.py`.

### Established Patterns
- **Race-safety = DynamoDB conditional writes** (heartbeat lease, usage tick, kill-switch
  flip, slot acquisition) — one table, typed items, atomic conditional updates.
- **Thin-token / table-of-truth:** voice reads `tiers` for limits at session start
  (Phase-3 D-01), so editing a tier's numbers needs no re-issued tokens.
- Secrets via SOPS→SSM SecureString consumed by the container `valueFrom` (Phase 2).
- Deploy via github-oidc terragrunt build/deploy workflows (Phase 2) — voice container is
  the first push through them alongside the WebRTC infra delta.

### Integration Points
- **`/api/offer` is the enforcement seam:** validates the JWT (issuer/aud via JWKS),
  reads `tiers`, runs the D-03 start gate (no-access / concurrency / daily-floor /
  kill-switch), acquires the heartbeat slot, and returns ICE candidates — or a D-11 typed
  rejection.
- **Service timer ↔ DynamoDB tick:** in-memory timer owns precise per-session cutoff
  (D-02); the 15s tick owns durable accounting + heartbeat renewal + daily/rollup updates.
- **Kill-switch control item + global rollup item** are read on the offer hot path and
  written by the tick — the operator (`kv`) and the auto-trip share this state.
- **Session-count CloudWatch metric** published by tasks drives autoscaling (D-13); the
  scale-in-protection API call is made from the same session-lifecycle code that manages
  the heartbeat.

</code_context>

<specifics>
## Specific Ideas

- The whole area was framed around a **public mic wired to metered APIs** — every decision
  favored self-healing/atomic mechanisms (heartbeat lease over a reaper, service timer over
  tick-slop, distinct typed errors over a generic reject) so a crash or race can't leak
  spend or strand a slot.
- Auto-trip kill-switch is treated as the real budget guardrail, not a nicety — it's the
  automatic circuit breaker behind the ~$120–165/mo conference cap.
- Smoke test must prove **media actually flows** (RTP frames), not just port reachability —
  it's the first deployed UDP test and the thing most likely to break in the wild.

</specifics>

<deferred>
## Deferred Ideas

- **TURN fallback for UDP-blocked networks** (hotel/corp Wi-Fi) — design spec §8 lists it
  future; CLAUDE.md says revisit only if it becomes mandatory. Phase 4 ships public-IP
  direct WebRTC with a clear typed error on UDP failure; the Phase-5 client surfaces it.
- **`kv sessions` live session inspection** (KV-06) — deferred until a multi-user event
  (already deferred in Phase 3).
- **Est.-cost precision / per-provider cost attribution** in the rollup — Phase 4 uses a
  simple seconds→$ estimate for the kill-switch; finer cost accounting is out of scope.

</deferred>

---

*Phase: 4-voice-service-deployed-quota-enforcement*
*Context gathered: 2026-07-05*
