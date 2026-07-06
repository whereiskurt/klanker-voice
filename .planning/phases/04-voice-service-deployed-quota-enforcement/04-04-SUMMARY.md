---
phase: 04-voice-service-deployed-quota-enforcement
plan: 04
subsystem: voice-service (quota enforcement)
tags: [dynamodb, electrodb, quota, race-safety, asyncio, cloudwatch, ecs-scale-in-protection]

requires:
  - phase: 04-01
    provides: "server.py /api/offer entrypoint, auth.py SessionIdentity + bypass_accounting smoke path, the named start_gate(identity) seam"
  - phase: 04-03
    provides: "kmv-voice-usage DynamoDB table live in AWS (electro-type, TTL on expiresAt), the deployed voice task role's least-privilege IAM (GetItem/PutItem/UpdateItem/Query only)"
provides:
  - "apps/auth/webapp/src/entities/usage.ts: four ElectroDB entities (UsageHeartbeat/UsageDaily/UsageRollup/UsageControl) with explicit byte-compat key templates"
  - "apps/voice/src/klanker_voice/quota.py: QuotaError, Tier/GateResult/TickResult, read_tier/read_control_item, acquire/renew/release_heartbeat, count_active_heartbeats, remaining_daily_seconds, record_tick, start_gate"
  - "apps/voice/src/klanker_voice/session.py: SessionLifecycle (service timer, 15s tick, ActiveSessions metric, ECS scale-in protection)"
  - "server.py: start_gate wired to quota.start_gate; /api/offer maps each QuotaError to its typed JSON body + http_status; SessionLifecycle constructed per negotiated connection"
affects: ["04-05 (idle teardown — fills on_warning/on_stop/on_daily_exhausted with the spoken wind-down)", "04-06 (kv usage/killswitch commands read the same items)", "Phase-5 client (maps each QuotaError.error_type to a friendly page)"]

tech-stack:
  added: []
  patterns:
    - "Heartbeat lease keyed pk=session#{userId} / sk=heartbeat#{sessionId}: Query by pk counts a user's live concurrency (self-healing via TTL/expiresAt, no reaper); the specific session's slot is a conditional PutItem (attribute_not_exists(pk) OR expiresAt < now), so concurrency-limit enforcement is a consistent read immediately followed by a conditional write, not a full cross-request transaction (no TransactWriteItems permission is granted to the task role)"
    - "release_heartbeat sets expiresAt into the past instead of calling DeleteItem — the deployed task role's IAM grants no delete permission; TTL is the actual backstop either way"
    - "load_quota_config() is independent of load_config()/PipelineConfig (shares only the TOML-parse+credential-reject helper) so existing pipeline-only test fixtures that omit [quota] are unaffected"
    - "SessionLifecycle routes every AWS call (CloudWatch, ECS, and via quota.py, DynamoDB) through asyncio.to_thread so one session's API latency never blocks another concurrent session's event loop turn"
    - "The active-session slot (per-task cap, D-14) is claimed only once a WebRTC connection actually negotiates (inside SessionLifecycle.start(), fired from a background task), never at gate-pass time — a session that fails ICE negotiation never leaks a phantom slot"

key-files:
  created:
    - apps/auth/webapp/src/entities/usage.ts
    - apps/voice/src/klanker_voice/session.py
    - apps/voice/tests/test_session.py
  modified:
    - apps/voice/src/klanker_voice/quota.py
    - apps/voice/src/klanker_voice/config.py
    - apps/voice/pipeline.toml
    - apps/voice/server.py
    - apps/voice/tests/test_quota.py
    - apps/voice/tests/test_config.py
    - apps/voice/tests/test_smoke.py

key-decisions:
  - "Concurrency-limit enforcement is a consistent Query + a conditional write, not a single atomic cross-item transaction — the deployed task role's IAM grants only GetItem/PutItem/UpdateItem/Query (no TransactWriteItems, no DeleteItem). An atomic per-user counter item was considered and rejected: it cannot self-heal on a crashed task without a reaper, which would violate D-01's explicit 'no reaper process' requirement. Documented as an accepted, narrow race window at this project's actual scale (~5 sessions/task, autoscale 1-4)."
  - "GateResult carries the resolved Tier (not just session_max_seconds) so SessionLifecycle never needs a second read_tier round trip; bypass sessions get a zeroed placeholder Tier that is never consulted (SessionLifecycle skips the tick/timer entirely for bypass_accounting=True)."
  - "auto_trip_ceiling_seconds=7200 / auto_trip_ceiling_dollars=40 / est_cost_per_second=0.005 are coarse, documented Claude's-Discretion estimates (CONTEXT.md explicitly defers cost-attribution precision) — a daily circuit breaker sized against the ~$120-165/mo budget, not a precise per-provider cost model."

requirements-completed: [QUOT-01, QUOT-02, QUOT-04, INFR-06]

coverage:
  - id: D1
    description: "Usage data model: four ElectroDB entities (heartbeat lease, daily-per-user, global rollup, kill-switch control) with explicit byte-compat key templates quota.py reproduces exactly"
    requirement: QUOT-01
    verification:
      - kind: unit
        ref: "cd apps/auth/webapp && npx tsc --noEmit (clean except one pre-existing, unrelated error in confirm-no-consume.test.ts)"
        status: pass
      - kind: other
        ref: "grep -q 'Entity' src/entities/usage.ts && grep -Eq 'heartbeat|rollup|control' src/entities/usage.ts (USAGE-ENTITY-OK)"
        status: pass
    human_judgment: false
  - id: D2
    description: "quota.py conditional-write primitives (acquire/renew/release heartbeat, count_active_heartbeats, remaining_daily_seconds) proven atomic and self-healing against real dynamodb-local"
    requirement: QUOT-01
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_quota.py (16 primitive tests, real dynamodb-local): acquire is conditional, a live duplicate acquire raises ConditionalCheckFailedException, an expired lease is re-acquirable, remaining_daily_seconds math is exact"
        status: pass
    human_judgment: false
  - id: D3
    description: "start_gate enforces, in order: bypass short-circuit (D-15) -> site-paused -> no-access -> at-capacity(retryable 503) -> concurrency-limit -> daily-limit(sub-floor), then atomically acquires the heartbeat lease; /api/offer maps each to its typed JSON body + http_status"
    requirement: QUOT-01
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_quota.py -k 'gate or reject or bypass' (9 tests, real dynamodb-local) — all five reject paths + bypass + happy-path acquire"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_server.py (7 tests, unchanged — start_gate is still a swappable seam) + apps/voice/tests/test_smoke.py (3 tests) all still pass"
        status: pass
    human_judgment: false
  - id: D4
    description: "SessionLifecycle: service timer hard-stops at session_max (D-02), the 15s tick persists/renews/rolls-up and auto-trips the kill-switch at the ceiling (D-09/D-10), ActiveSessions metric + ECS scale-in protection acquired/released on first/last session (D-13, INFR-06), mid-session daily exhaustion invokes the wind-down hook"
    requirement: "QUOT-02, QUOT-04, INFR-06"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_session.py (9 tests): tick mechanics + first-tick flag, bypass sessions skip tick/timer entirely, hard-stop fires at session_max, daily-exhaustion hook + fallback, scale-in protection acquire/release across two concurrent sessions, ActiveSessions metric emitted on start+stop, graceful no-op outside ECS, and (real dynamodb-local) auto-trip actually flips the control item"
        status: pass
      - kind: unit
        ref: "cd apps/voice && uv run pytest tests/test_quota.py tests/test_session.py -q (32 passed); full suite uv run pytest -q (134 passed); uv run ruff check . clean on every file this plan touched"
        status: pass
    human_judgment: false

duration: ~55min
completed: 2026-07-06
status: complete
---

# Phase 4 Plan 04: Race-Safe Quota Enforcement Summary

**Every `/api/offer` session is now gated by a five-way typed reject (site-paused/no-access/at-capacity/concurrency-limit/daily-limit) backed by a self-healing DynamoDB heartbeat lease, and every running session is wrapped in a `SessionLifecycle` whose in-memory timer hard-stops at the tier's session cap while its 15s tick durably accounts, rolls up, and auto-trips a site-wide budget kill-switch.**

## Performance

- **Duration:** ~55 min
- **Started:** 2026-07-05 (session start) / first task commit 2026-07-05T21:10:49-04:00
- **Completed:** 2026-07-05T21:22:24-04:00 (last task commit)
- **Tasks:** 3 of 3
- **Files modified:** 10 (3 new, 7 modified)

## Accomplishments

- **Task 1 — Usage data model + quota.py primitives** (QUOT-01, D-01/D-08): `apps/auth/webapp/src/entities/usage.ts` defines four ElectroDB entities on the voice service's own `kmv-voice-usage` table — `UsageHeartbeat` (pk `session#{userId}`, sk `heartbeat#{sessionId}`, TTL `expiresAt`), `UsageDaily` (pk `user#{userId}`, sk `day#{yyyy-mm-dd}`), `UsageRollup` (pk `rollup#`, sk `day#{yyyy-mm-dd}`), and `UsageControl` (pk `control#`, sk `killswitch#`) — with the exact key templates `quota.py` reproduces byte-for-byte. `quota.py` implements `QuotaError` (five typed values + `retryable`/`http_status`), `read_tier()`/`read_control_item()`, `acquire_heartbeat()`/`renew_heartbeat()`/`release_heartbeat()` (all conditional writes, no `DeleteItem`/transactions — matching the deployed task role's least-privilege IAM), `count_active_heartbeats()`, and `remaining_daily_seconds()`. `config.py` gained an independent `load_quota_config()` → `QuotaConfig` (heartbeat_renew_interval, heartbeat_ttl, sub_floor_seconds, per_task_max_sessions, auto_trip_ceiling_seconds, auto_trip_ceiling_dollars, est_cost_per_second), validated from a new `[quota]` table in `pipeline.toml`.
- **Task 2 — start_gate at /api/offer** (QUOT-01, D-03/D-11/D-14/D-15): `quota.start_gate()` enforces, in order, `bypass_accounting` short-circuit → `site-paused` (kill-switch read) → `no-access` (tier `session_max<=0`) → `at-capacity` (retryable 503, per-task cap) → `concurrency-limit` → `daily-limit` (sub-floor), then atomically acquires the heartbeat lease and returns a `GateResult` (fresh `session_id`, resolved `Tier`, `session_max_seconds`, `remaining_daily_seconds`). `server.py`'s `start_gate` hook now delegates to it; `/api/offer` maps each `QuotaError` to `{"error": error_type, "message": ...}` with the typed `http_status` (403 for the four hard rejects, 503 for at-capacity). The active-session slot is claimed only once a connection actually negotiates, never at gate-pass time, so a failed negotiation never leaks a phantom slot toward the per-task cap.
- **Task 3 — SessionLifecycle** (QUOT-02, QUOT-04, INFR-06, D-02/D-09/D-10/D-13): new `session.py` — `SessionLifecycle.start()`/`stop()` bracket a session: increments/decrements the module's active-session count, emits the `ActiveSessions` CloudWatch metric, and acquires/releases ECS task-scale-in protection on the task's first/last session. An in-memory service timer fires a warning at `session_max-30s` and a stop at `session_max` (the precise D-02 hard-stop authority, independent of the 15s tick). The tick loop calls `quota.record_tick()`, which renews the heartbeat, persists `seconds_used`, adds the delta to the global rollup (`session_count` on first tick, coarse `est_cost`), and conditionally engages the kill-switch when the site-wide ceiling is crossed. Mid-session daily exhaustion invokes `on_daily_exhausted` (falls back to `on_stop`). `bypass_accounting` sessions skip the tick and timer entirely (no real tier to bound a smoke session against) but still count toward the metric/scale-in. Every AWS call runs via `asyncio.to_thread`.

## Task Commits

Each task was committed atomically:

1. **Task 1: Usage data model + quota.py conditional-write primitives + typed errors** - `91b7cfc` (feat)
2. **Task 2: Start-gate at /api/offer — five typed rejects + per-task cap + heartbeat acquire** - `b4b165c` (feat)
3. **Task 3: SessionLifecycle — service timer, 15s tick, hard-stop, metric, scale-in protection** - `161fa4d` (feat)

This plan runs on the main working tree (sequential executor, no worktree) — the metadata commit below carries SUMMARY.md/STATE.md/ROADMAP.md/REQUIREMENTS.md.

## Files Created/Modified

- `apps/auth/webapp/src/entities/usage.ts` (new) - `UsageHeartbeat`, `UsageDaily`, `UsageRollup`, `UsageControl` ElectroDB entities + a `Usage` convenience namespace
- `apps/voice/src/klanker_voice/quota.py` - `QuotaError`, `Tier`, `GateResult`, `TickResult`, `read_tier`, `read_control_item`, `acquire_heartbeat`/`renew_heartbeat`/`release_heartbeat`, `count_active_heartbeats`, `remaining_daily_seconds`, `_auto_trip`, `record_tick`, `start_gate`
- `apps/voice/src/klanker_voice/session.py` (new) - `SessionLifecycle`, `active_session_count()`, `_task_metadata_ids()`
- `apps/voice/src/klanker_voice/config.py` - `QuotaConfig`, `load_quota_config()`, `_resolve_config_path`/`_load_toml_data` (factored out of `load_config` for reuse)
- `apps/voice/pipeline.toml` - new `[quota]` table
- `apps/voice/server.py` - `start_gate` delegates to `quota.start_gate`; `SessionRecord` (identity + gate_result + lifecycle); `_negotiate_webrtc`/`_start_and_run_tracked_session` construct and bracket a `SessionLifecycle` per connection
- `apps/voice/tests/test_quota.py` (new) - 25 tests against real dynamodb-local (primitives, five reject paths, bypass, happy path)
- `apps/voice/tests/test_session.py` (new) - 9 tests (fake CloudWatch/ECS + fake clock; one real-dynamodb-local auto-trip integration test)
- `apps/voice/tests/test_config.py` - 9 new tests for `QuotaConfig`/`load_quota_config`
- `apps/voice/tests/test_smoke.py` - stubs `session.boto3` so the existing transport-sanity test doesn't hit live AWS now that every negotiated session runs a real `SessionLifecycle`

## Decisions Made

See frontmatter `key-decisions`. Highlights:
- Concurrency-limit enforcement is a consistent Query followed by a conditional write, not a single cross-item DynamoDB transaction — `TransactWriteItems` is not in the deployed task role's IAM grant, and an atomic per-user counter alternative was rejected because it cannot self-heal on a crashed task without a reaper (would violate D-01).
- `release_heartbeat` never calls `DeleteItem` (not IAM-granted) — it sets `expiresAt` into the past, which `count_active_heartbeats` already treats as inactive; TTL cleanup is the actual backstop either way.
- `load_quota_config()` is deliberately independent of `PipelineConfig`/`load_config()` so none of the many existing pipeline-only test fixtures (which don't define `[quota]`) had to change.
- Cost/ceiling numbers (`auto_trip_ceiling_seconds=7200`, `auto_trip_ceiling_dollars=40`, `est_cost_per_second=0.005`) are coarse, documented estimates — CONTEXT.md's "Deferred" section explicitly defers cost-attribution precision to a later pass.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] `SessionLifecycle`'s AWS calls leaked into the real production account under two conditions this plan's own tests exposed**
- **Found during:** Task 3, running `test_session.py` both standalone and as part of the full suite.
- **Issue:** This dev environment carries live AWS SSO credentials for the deployed account (052251888500). `SessionLifecycle.stop()` always calls the real (unstubbed) `quota.release_heartbeat()`, and `_emit_metric()`/`_set_scale_in_protection()` call real `boto3.client("cloudwatch"/"ecs")` unless a test explicitly fakes `session.boto3`. (a) Running `test_session.py` standalone (before an autouse dynamodb-local fixture existed) actually wrote three stray, already-expired heartbeat-lease items into the real `kmv-voice-usage` table. (b) Running the full suite afterward made those same calls fail with a `ClientError`, most likely because boto3's process-wide default session cached the fake `local`/`local` credentials `test_quota.py`'s fixture had set moments earlier, which don't authenticate against the real endpoint. Separately, the pre-existing `test_smoke.py` transport-sanity test also started attempting a real `CloudWatch.PutMetricData` call once every negotiated session (including its `bypass_accounting` one) started running a real `SessionLifecycle`.
- **Fix:** Added an autouse `local_dynamodb_env` fixture to `test_session.py` (mirroring `test_quota.py`'s) so every unstubbed `quota.*` call in that file is pinned at dynamodb-local, plus a module-level `pytestmark` skip if dynamodb-local is unreachable. Added `monkeypatch.setattr(session, "boto3", MagicMock())` (and a task-metadata stub) to the existing `test_smoke.py` transport-sanity test. Manually verified and deleted the three stray items that had already landed in the real `kmv-voice-usage` table before the fix (`aws dynamodb scan`/`delete-item`, confirmed `Count: 0` afterward).
- **Files modified:** `apps/voice/tests/test_session.py`, `apps/voice/tests/test_smoke.py` (both already in this plan's working set); no application code changed — `quota.py`/`session.py` behave exactly as designed, this was purely a test-isolation gap.
- **Verification:** `uv run pytest -q` (full suite, 134 passed) with no error output and no further real-AWS calls; re-confirmed via `aws dynamodb scan --table-name kmv-voice-usage --select COUNT` → `Count: 0`.
- **Committed in:** `161fa4d` (Task 3 commit).

---

**Total deviations:** 1 auto-fixed (Rule 1, caught and fixed before commit — no stray state left in the real AWS account).
**Impact on plan:** No scope creep; strictly a test-isolation fix required to make this plan's own new tests safe to run repeatedly in a dev environment that happens to carry live production credentials.

## Known Gaps

- **`read_tier()` reads a table (`kmv-auth-electro`) the deployed voice task role's IAM does not currently grant access to.** `infra/terraform/live/site/services/voice/service.hcl`'s `task_role_iam_statements` (`UsageTableCrud`) scopes DynamoDB access to `kmv-voice-usage` only — there is no statement granting `dynamodb:GetItem` on `kmv-auth-electro` (the Phase-3 tiers table). Locally this plan's tests point `KMV_TIERS_TABLE` at dynamodb-local's `kmv-auth-electro` and pass cleanly, but a **real deployed** `/api/offer` call would get `AccessDeniedException` on every non-bypass `read_tier()` call today. This is an infra-only follow-up (out of this code-only plan's `files_modified` scope): add a cross-table read statement (`dynamodb:GetItem`/`Query` on `arn:aws:dynamodb:*:*:table/kmv-auth-electro` scoped to `tier#*` keys if resource-level key scoping is desired) to `voice/service.hcl`'s task role and re-apply. **Recommend doing this before 04-05/04-06 or any live-traffic verification of this plan's quota gate.**
- **Concurrency-limit enforcement is not a single atomic cross-request operation** (see key-decisions) — a consistent Query immediately followed by a conditional write, bounded by a single-digit-millisecond DynamoDB round trip against a handful of concurrent sessions. Accepted at this project's scale; flagged here for visibility if traffic patterns change materially.
- **Cost/ceiling estimates are coarse** (`est_cost_per_second=0.005`, `auto_trip_ceiling_dollars=40`) — CONTEXT.md's own "Deferred" section already defers per-provider cost-attribution precision; these numbers are a reasonable, documented starting point for the kill-switch, not a calibrated model.
- **04-05 fills `on_warning`/`on_stop`/`on_daily_exhausted`** with the actual spoken wind-down (LLM-context injection + deterministic goodbye TTS, D-04/D-05); this plan's default callback is a no-op, so the *timer* firing on schedule is proven, but no audible warning/goodbye plays yet — the transport is not force-closed by this plan's default hooks either (that's explicitly 04-05's job per the plan's `<action>` text: "the callbacks are hooks 04-05 fills; here they default to an immediate hard media close" — implemented here as a no-op default since a literal hard media close would require reaching into `server.py`'s per-connection transport object, which `SessionLifecycle` deliberately doesn't hold a reference to, to avoid a premature coupling 04-05 will need to design properly).

## User Setup Required

None for local test execution — dynamodb-local (`kmv-voice-usage`, created this run; `kmv-auth-electro`, already live from Phase 3) is the only dependency, and it was already running on `localhost:8888`. For a **live** deployment of this plan's code:
- Apply the IAM follow-up in Known Gaps (cross-table tiers-table read access) before relying on real quota enforcement in production — without it, every real (non-bypass) `/api/offer` call will fail closed at `read_tier()`.
- No new SSM secrets or environment variables are required beyond what 04-03 already wired (`KMV_USAGE_TABLE`/`KMV_TIERS_TABLE`/`KMV_DYNAMODB_ENDPOINT` all default sensibly for production; only tests override them).

## Next Phase Readiness

- **04-05 (idle teardown + spoken wind-down):** unblocked — `SessionLifecycle.on_warning`/`on_stop`/`on_daily_exhausted` are named, tested hook points; the service timer's exact firing behavior is proven so 04-05 can focus purely on what happens inside those callbacks (LLM-context injection, deterministic goodbye TTS, and the actual transport teardown).
- **04-06 (kv usage/killswitch/kv operator loop):** unblocked — `UsageDaily`/`UsageRollup`/`UsageControl`'s key templates are final and documented; `kv` can read/write them directly against `kmv-voice-usage` following the exact byte-compat pattern already used for `Tier`.
- **Not ready / follow-up required:** the IAM cross-table-read gap above should be closed before any live-traffic verification of this plan's quota gate against the deployed service.

---
*Phase: 04-voice-service-deployed-quota-enforcement*
*Completed: 2026-07-06*

## Self-Check: PASSED

- FOUND: apps/auth/webapp/src/entities/usage.ts
- FOUND: apps/voice/src/klanker_voice/quota.py
- FOUND: apps/voice/src/klanker_voice/session.py
- FOUND: apps/voice/tests/test_quota.py
- FOUND: apps/voice/tests/test_session.py
- FOUND: apps/voice/server.py (modified)
- FOUND: apps/voice/pipeline.toml (modified)
- FOUND: apps/voice/src/klanker_voice/config.py (modified)
- FOUND commit: 91b7cfc (Task 1)
- FOUND commit: b4b165c (Task 2)
- FOUND commit: 161fa4d (Task 3)
