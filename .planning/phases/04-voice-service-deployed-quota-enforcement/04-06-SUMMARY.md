---
phase: 04-voice-service-deployed-quota-enforcement
plan: 06
subsystem: kv (operator CLI)
tags: [go, cobra, dynamodb, kill-switch, usage, operator-cli]

requires:
  - phase: 04-04
    provides: "apps/auth/webapp/src/entities/usage.ts's FINAL Usage entity key templates (UsageHeartbeat/UsageDaily/UsageRollup/UsageControl) and apps/voice/src/klanker_voice/quota.py's own byte-compat key constants — the exact pk/sk strings this plan's kv commands must reproduce"
  - phase: 03-auth-service-access-codes
    provides: "kv/internal/app/electro's key-reproduction discipline (AccessCode/Tier) and cmd/{tier,code}.go's Cobra command + conditional-UpdateItem patterns this plan mirrors"
provides:
  - "kv/internal/app/electro/usage_keys.go: pure functions reproducing UsageHeartbeat/UsageDaily/UsageRollup/UsageControl key templates byte-for-byte"
  - "kv/internal/app/cmd/usage.go: NewUsageCmd — `kv usage today [--user-id] [--json]` / `kv usage history <user-id> [--days N] [--json]`"
  - "kv/internal/app/cmd/killswitch.go: NewKillswitchCmd — `kv killswitch status|on [--reason]|off`, conditional idempotent control-item flip"
  - "root.go: --usage-table persistent flag (KMV_USAGE_TABLE env, default kmv-voice-usage), NewUsageCmd + NewKillswitchCmd registered"
affects: ["Task 3 of this same plan (checkpoint:human-verify, deferred to the orchestrator — requires the deployed voice service + live AWS)"]

tech-stack:
  added: []
  patterns:
    - "kv's Usage* key functions in usage_keys.go declare no GSI (unlike AccessCode/Tier) — every usage/killswitch access pattern (GetItem on rollup/daily/control, Query on a user's day-prefixed partition) is a direct primary-index read, no table scan (KV-03, D-10)"
    - "quota.py's actual DynamoDB writes never set ElectroDB's __edb_e__/__edb_v__ bookkeeping markers on UsageDaily/UsageRollup/UsageControl items (only pk/sk + data attributes) — a pre-existing gap from 04-04, not introduced here. kv's own killswitch on/off writes DO include the markers (forward-compat with a future webapp ElectroDB reader), which required ExpressionAttributeNames aliasing since a bare '__edb_e__' token isn't valid DynamoDB expression grammar (leading double underscore)"
    - "Idempotent conditional flips return (flipped bool, err error) rather than surfacing ConditionalCheckFailedException as a caller-visible error — a redundant `on`/`off` is a normal, harmless outcome (task's own acceptance criteria), not a failure mode"
    - "ListUsageHistory derives each returned record's `day` from its DynamoDB sort key (day#${day}) rather than an item attribute, because quota.py's record_tick() never writes a 'day' attribute on the daily item either — the day only ever lives in the sk"

key-files:
  created:
    - kv/internal/app/electro/usage_keys.go
    - kv/internal/app/cmd/usage.go
    - kv/internal/app/cmd/killswitch.go
    - kv/internal/app/cmd/usage_killswitch_test.go
  modified:
    - kv/internal/app/cmd/root.go

key-decisions:
  - "Split Task 1/Task 2 into two atomic, independently-buildable commits (usage.go+usage_keys.go first, killswitch.go second) by temporarily removing the NewKillswitchCmd registration line for the Task 1 commit and re-adding it for Task 2 — each commit's `go build ./... && go test ./...` passes standalone, matching this project's established atomic-commit discipline (see 04-02-SUMMARY.md's 'commit granularity note' precedent) even though killswitch.go's key-template dependency (usage_keys.go) is entirely satisfied by Task 1."
  - "killswitch `off`'s conditional UpdateItem requires attribute_exists(pk) AND engaged=:true — it never creates a fresh control item (unlike `on`, whose condition allows attribute_not_exists(pk)) — because a missing item is already semantically disengaged (matches quota.py's read_control_item default); writing an item just to record a no-op `off` would be pointless churn."
  - "kv killswitch on/off writes include the ElectroDB __edb_e__/__edb_v__ bookkeeping markers (aliased via ExpressionAttributeNames, since the leading double-underscore isn't a valid bare expression token) even though quota.py's own writes to the same item never set them — a deliberate asymmetry: the plan's Task 1 action text explicitly calls for the markers 'so kv-written items are valid records,' and adding them is forward-compatible/harmless (GetItem-based readers like quota.py's read_control_item and this plan's own ReadKillswitchStatus simply ignore unrecognized attributes)."

requirements-completed: []  # KV-03/KV-04/QUOT-04/INFR-06 pending Task 3 (checkpoint:human-verify against the deployed service) — not marked complete here, matching 04-05's precedent of leaving requirements unmarked until the live checkpoint is approved.

coverage:
  - id: D1
    description: "usage_keys.go reproduces the UsageHeartbeat/UsageDaily/UsageRollup/UsageControl key templates from usage.ts exactly, matching quota.py's own key-building constants"
    requirement: "KV-03, KV-04"
    verification:
      - kind: unit
        ref: "cd kv && go test ./... -run 'Usage|Key' (TestUsageKeyCompat_Heartbeat/Daily/Rollup/Control, TestUsageDayString) — pure string-equality assertions, no external dependency"
        status: pass
    human_judgment: false
  - id: D2
    description: "kv usage today/history reads the O(1) global rollup, a user's daily item, and a user's recent-day history, all via GetItem/single-partition Query — no table scan"
    requirement: "KV-03"
    verification:
      - kind: unit
        ref: "cd kv && go test ./... -run Usage (real dynamodb-local against kmv-voice-usage): fresh-day zero-value read, rollup round-trip, daily round-trip, 3-day history capped/ordered correctly"
        status: pass
      - kind: other
        ref: "go run ./cmd/kv usage --help renders today/history; manual run against dynamodb-local (--endpoint-url http://localhost:8888) confirmed text + --json output shapes"
        status: pass
    human_judgment: false
  - id: D3
    description: "kv killswitch status/on/off operate on the control#/killswitch# item via conditional UpdateItem; on/off are idempotent; off clears the auto-trip reason (D-09)"
    requirement: "KV-04, QUOT-04"
    verification:
      - kind: unit
        ref: "cd kv && go test ./... -run Killswitch (real dynamodb-local): on->engaged+reason, off->disengaged+reason-cleared, redundant on/off are no-ops (flipped=false, no error)"
        status: pass
      - kind: other
        ref: "go run ./cmd/kv killswitch --help renders status/on/off; NewKillswitchCmd registered in root.go"
        status: pass
    human_judgment: false
  - id: D4
    description: "Deployed operator loop end-to-end (kill-switch gates/releases live sessions, kv usage reads real rollup data) and INFR-06 autoscale 1->4 + scale-in protection verified on the running service"
    requirement: "KV-03, KV-04, INFR-06"
    verification: []
    human_judgment: true
    rationale: "Requires a live deployed voice service and real AWS (ActiveSessions autoscaling policy, ECS task-protection state, an actual session hitting a paused/resumed service) — explicitly deferred to the orchestrator's Task 3 checkpoint:human-verify, which this sequential executor does not run (per its instructions: never mutate the real AWS account, stop before the live-verify checkpoint)."

duration: ~25min
completed: 2026-07-06
status: in-progress
---

# Phase 4 Plan 06: kv usage + killswitch — Operator Loop (Auto Tasks) Summary

**`kv usage today/history` reads the site-wide O(1) daily rollup and per-user daily usage via single-partition DynamoDB reads (no scan), and `kv killswitch status/on/off` conditionally flips the same control item `/api/offer`'s start gate reads on every session — both built against key templates proven byte-compatible with 04-04's `usage.ts`/`quota.py`; the live deployed-service verification (Task 3) is deferred to the orchestrator.**

## Performance

- **Duration:** ~25 min
- **Started:** 2026-07-06 (session start)
- **Completed:** auto tasks only — Task 3 (checkpoint) pending
- **Tasks:** 2 of 3 (Task 3 is `checkpoint:human-verify`, deferred to the orchestrator)
- **Files modified:** 5 (4 new, 1 modified)

## Accomplishments

- **Task 1 — Usage key reproduction + `kv usage` today/history** (KV-03, D-10): `kv/internal/app/electro/usage_keys.go` reproduces the four 04-04 Usage entities' key templates as pure functions — `UsageHeartbeatPK/SK` (`session#${userId}`/`heartbeat#${sessionId}`), `UsageDailyPK/SK` (`user#${userId}`/`day#${day}`), `UsageRollupPK/SK` (`rollup#`/`day#${day}`), `UsageControlPK/SK` (`control#`/`killswitch#`) — verified equal to `usage.ts`'s templates and to `quota.py`'s own `_heartbeat_pk`/`_daily_pk`/`ROLLUP_PK`/`CONTROL_PK` constants by table tests. `kv/internal/app/cmd/usage.go` implements `NewUsageCmd`: `kv usage today [--user-id <id>] [--json]` reads the global rollup (or one user's day) via a single `GetItem`, and `kv usage history <user-id> [--days N] [--json]` Queries the user's day-prefixed partition (`ScanIndexForward=false`, `Limit=N`) — both single-partition reads, no table scan. A fresh day/user with no traffic yet reads as a zero-value record rather than an error. A new `--usage-table` persistent flag (`KMV_USAGE_TABLE` env, default `kmv-voice-usage`) targets the voice service's own table, distinct from `--table` (`kmv-auth-electro`).
- **Task 2 — `kv killswitch` status/on/off** (KV-04, QUOT-04, D-08/D-09): `kv/internal/app/cmd/killswitch.go` implements `NewKillswitchCmd`: `status` reads the control item and prints engaged/reason/ceilings; `on [--reason <text>]` conditionally sets `engaged=true` (allows creating the item fresh on a brand-new table); `off` conditionally sets `engaged=false` **and clears the reason** (D-09's explicit-operator-reset requirement, so an auto-trip's cause stays attributable until an operator deliberately resets it). Both directions are idempotent: a redundant flip returns `(flipped=false, nil)` rather than surfacing DynamoDB's `ConditionalCheckFailedException` as a command error. Both `NewUsageCmd`/`NewKillswitchCmd` are registered in `root.go`.

## Task Commits

Each task was committed atomically:

1. **Task 1: Usage key reproduction + `kv usage today`/`history`** - `baaafc3` (feat)
2. **Task 2: `kv killswitch` status/on/off — conditional control-item flip** - `4aab425` (feat)

This plan runs on the main working tree (sequential executor, no worktree) — the metadata commit below carries SUMMARY.md/STATE.md/ROADMAP.md/REQUIREMENTS.md, once written.

**Task 3 (checkpoint:human-verify) is intentionally NOT executed by this run.** Per this plan's own instructions, the sequential executor must never mutate the real AWS account, and Task 3 requires driving the live deployed voice service (kill-switch gate/release against a real session, `kv usage` against real traffic, and confirming the `ActiveSessions` autoscaling policy + ECS scale-in protection via AWS) — that live verification belongs to the orchestrator.

## Files Created/Modified

- `kv/internal/app/electro/usage_keys.go` (new) - `UsageHeartbeatPK/SK`, `UsageDailyPK/SK`, `UsageRollupPK/SK`, `UsageControlPK/SK`, `UsageDayString`
- `kv/internal/app/cmd/usage.go` (new) - `UsageDailyRecord`, `UsageRollupRecord`, `ReadUsageRollup`, `ReadUsageDaily`, `ListUsageHistory`, `NewUsageCmd`, `printUsageRollup`/`printUsageDaily`/`printUsageHistory`
- `kv/internal/app/cmd/killswitch.go` (new) - `KillswitchStatus`, `ReadKillswitchStatus`, `EngageKillswitch`, `DisengageKillswitch`, `isConditionalCheckFailed`, `NewKillswitchCmd`, `printKillswitchStatus`
- `kv/internal/app/cmd/root.go` - `defaultUsageTable`/`usageTableFromEnv`, `Config.UsageTable`, `--usage-table` persistent flag, `NewUsageCmd`/`NewKillswitchCmd` registered
- `kv/internal/app/cmd/usage_killswitch_test.go` (new) - 4 pure key-compat table tests + `TestUsageDayString`, plus dynamodb-local integration tests: 4 for usage (fresh-day zero-value, rollup round-trip, daily round-trip, 3-day history), 4 for killswitch (on→engaged, off→disengaged+reason-cleared, redundant-on no-op, redundant-off no-op)

## Decisions Made

See frontmatter `key-decisions`. Highlights:
- Task 1/Task 2 were committed as two independently-buildable, independently-`go test`-passing commits — Task 1's commit temporarily excluded the `NewKillswitchCmd` registration (added back in Task 2's commit) so neither commit leaves the tree in a broken intermediate state, matching this project's established atomic-commit discipline.
- `kv killswitch off` never creates a fresh control item (unlike `on`) — a missing item already reads as disengaged (matches `quota.py`'s own default), so writing one just to record a no-op would be pointless churn.
- `kv`'s own killswitch writes include the ElectroDB `__edb_e__`/`__edb_v__` bookkeeping markers (via `ExpressionAttributeNames` aliases, since a bare leading-double-underscore token isn't valid DynamoDB expression grammar) even though `quota.py`'s own writes to the same item never set them — deliberate forward-compat per the plan's own Task 1 action text, harmless to every current reader.
- `ListUsageHistory` derives each record's `day` from the DynamoDB sort key rather than an item attribute, because `quota.py`'s `record_tick()` never writes a `day` attribute either (only pk/sk encode it) — this was caught and fixed during Task 1's own test run (see Deviations).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] `ListUsageHistory` returned records with an empty `Day` field**
- **Found during:** Task 1, running `TestUsage_History` against real dynamodb-local.
- **Issue:** `attributevalue.UnmarshalListOfMaps` populates `UsageDailyRecord.Day` from a `"day"` item attribute — but `quota.py`'s `record_tick()` never writes that attribute on the daily item (only `pk`/`sk` encode the day, as `day#${day}`), so every returned record's `Day` field was silently empty.
- **Fix:** `ListUsageHistory` now derives `Day` for each result by stripping the `"day#"` prefix off that item's raw `sk` attribute, matching how `ReadUsageDaily`/`ReadUsageRollup` already thread the `day` parameter through explicitly.
- **Files modified:** `kv/internal/app/cmd/usage.go` (already in this plan's working set).
- **Verification:** `TestUsage_History` passes, asserting exact day ordering/values.
- **Committed in:** `baaafc3` (Task 1 commit).

**2. [Rule 1 - Bug] `EngageKillswitch`/`DisengageKillswitch` UpdateExpression syntax errors on the `__edb_e__`/`__edb_v__` attribute names**
- **Found during:** Task 2, running the killswitch tests against real dynamodb-local.
- **Issue:** Embedding the literal attribute names `__edb_e__`/`__edb_v__` directly into the `UpdateExpression` string produced `ValidationException: Invalid UpdateExpression: Syntax error` — a leading double underscore isn't a valid bare token in DynamoDB's expression grammar.
- **Fix:** Aliased both attribute names via `ExpressionAttributeNames` (`#edbe`/`#edbv`) instead of inlining them. A related follow-on bug in the same area — `DisengageKillswitch`'s `ConditionExpression` referenced `:true` without defining it in `ExpressionAttributeValues` — was caught by the same test run and fixed alongside.
- **Files modified:** `kv/internal/app/cmd/killswitch.go` (already in this plan's working set).
- **Verification:** `go test ./... -run Killswitch` — all 4 tests pass.
- **Committed in:** `4aab425` (Task 2 commit).

---

**Total deviations:** 2 auto-fixed (both Rule 1, caught by this plan's own tests before commit — no stray state left, no scope creep).
**Impact on plan:** Both are implementation bugs in code this plan itself wrote, fixed within the same task before its commit — not a gap discovered in prior-plan code.

## Known Stubs

None — every command is wired to real DynamoDB reads/writes against the actual `kmv-voice-usage` key templates; nothing renders placeholder or mock data.

## Threat Flags

None beyond what the plan's own `<threat_model>` already covers (T-04-11 key-mismatch, T-04-20 IAM scope, T-04-21 repudiation, T-04-SC dependency) — no new network endpoints, auth paths, or schema changes were introduced beyond what the plan specified.

## Issues Encountered

None beyond the two auto-fixed bugs documented above, both caught by this plan's own tests.

## User Setup Required

None for local test execution — dynamodb-local (`kmv-voice-usage`, already live from 04-02/04-04) is the only dependency, already running on `localhost:8888`. No new SSM secrets or environment variables are required; `kv` already inherits the ambient AWS credential chain (`--region`/`--endpoint-url` overrides only needed for local dev).

**Verified no real-AWS side effects:** every test and manual command run in this session used `--endpoint-url http://localhost:8888` (or the tests' own hardcoded dynamodb-local endpoint); `aws dynamodb scan --table-name kmv-voice-usage --select COUNT` against the real account confirms `Count: 0` after this session.

## Next Phase Readiness

- **Task 3 of this plan (checkpoint:human-verify) is the next step** — the orchestrator must drive the kill-switch gate/release loop against the deployed voice service, confirm `kv usage today` reflects real session traffic, and verify (via AWS) that the `ActiveSessions` target-tracking autoscaling policy (min 1/max 4) and ECS scale-in protection actually behave as designed on the running service. **KV-03/KV-04/QUOT-04/INFR-06 are not marked complete in REQUIREMENTS.md/ROADMAP.md until that checkpoint is approved.**
- Both `kv usage` and `kv killswitch` are fully built, unit-tested against real DynamoDB semantics (conditional writes, idempotency, sort-key-derived fields), and ready for the orchestrator's live verification — no further code changes are anticipated unless the live run surfaces something the dynamodb-local tests couldn't (e.g., IAM permission gaps, matching 04-04's own documented `read_tier()` cross-table IAM gap).

---
*Phase: 04-voice-service-deployed-quota-enforcement*
*Completed (auto tasks only — Task 3 pending): 2026-07-06*

## Self-Check: PASSED

- FOUND: kv/internal/app/electro/usage_keys.go
- FOUND: kv/internal/app/cmd/usage.go
- FOUND: kv/internal/app/cmd/killswitch.go
- FOUND: kv/internal/app/cmd/root.go (modified)
- FOUND: kv/internal/app/cmd/usage_killswitch_test.go
- FOUND commit: baaafc3 (Task 1)
- FOUND commit: 4aab425 (Task 2)
