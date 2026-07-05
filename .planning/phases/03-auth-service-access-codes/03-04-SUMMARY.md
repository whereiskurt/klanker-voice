---
phase: 03-auth-service-access-codes
plan: 04
subsystem: infra
tags: [go, cobra, aws-sdk-go-v2, dynamodb, electrodb, cli, key-compatibility]

requires:
  - phase: 03-auth-service-access-codes plan 02
    provides: "Four ElectroDB entities (AccessCode, Tier, LoginIntent, CodeRedemption) with EXPLICIT key templates on kmv-auth-electro — the byte-for-byte contract this plan's Go kv CLI reproduces"
provides:
  - "kv/ Go module (go 1.26, cobra v1.10.2, aws-sdk-go-v2 v1.42.x) — the operator CLI, sibling to klanker-maker's km"
  - "kv code create|list|expire and kv tier define|list commands, writing/reading the kmv-auth-electro table directly"
  - "electro/keys.go: pure key-builder functions + full item Marshal() reproducing the 03-02 AccessCode/Tier ElectroDB templates byte-for-byte (pk/sk/gsi1 + __edb_e__/__edb_v__ bookkeeping markers)"
  - "Bidirectional key-compatibility proof (Pitfall 1 closed): kv-written items are readable by webapp-shaped reads and vice versa, verified both by pure string-equality tests (no container) and live round-trip tests against dynamodb-local"
  - "Seeded no-access / demo-tier (2-min) / kphdemo-tier (30-min) tiers and demo / kphdemo123 access codes on the local kmv-auth-electro table"
affects: [phase-4-voice]

tech-stack:
  added: [cobra v1.10.2, aws-sdk-go-v2 v1.42.1, aws-sdk-go-v2/service/dynamodb v1.59.2]
  patterns:
    - "electro package keeps key-string builders (pure functions, no AWS import needed for testing) separate from item Marshal() (AWS attribute-value types) — keys_test.go proves key compatibility with zero infrastructure dependency, while roundtrip_test.go proves the full item shape against a live table"
    - "kv mirrors km's cmd/<bin>/main.go -> internal/app/cmd.Execute() structure and NewRootCmd() shape for cross-CLI consistency, but keeps Config as a plain struct (not km's full config.Load() machinery) since kv has no persisted local config file"
    - "Round-trip tests skip (not fail) when dynamodb-local is unreachable, so `go test ./...` stays green in sandboxes without the container; keys_test.go's pure string-equality assertions are the always-on gate"

key-files:
  created:
    - kv/go.mod
    - kv/go.sum
    - kv/cmd/kv/main.go
    - kv/internal/app/cmd/root.go
    - kv/internal/app/cmd/code.go
    - kv/internal/app/cmd/tier.go
    - kv/internal/app/electro/keys.go
    - kv/internal/app/electro/keys_test.go
    - kv/internal/app/cmd/roundtrip_test.go
  modified: []

key-decisions:
  - "Split electro package into pure key-string builder functions (AccessCodePK/TierPK/etc, no AWS dependency) plus a separate Marshal() that returns aws-sdk-go-v2 dynamodb/types.AttributeValue maps — lets keys_test.go assert exact key-string equality without any DynamoDB/container dependency, while code.go/tier.go use Marshal() directly for PutItem"
  - "ElectroDB bookkeeping markers (__edb_e__, __edb_v__=\"1\") were derived from a live `aws dynamodb scan` of the existing kmv-auth-electro table (populated by 03-02's own tests) rather than reverse-engineered from ElectroDB source — ground truth over inference"
  - "code create/tier define use PutItem (unconditional put/replace), not a conditional-create — matches the operator use case (idempotent re-running a seed script should not error) and doesn't collide with the webapp's own conditional CodeRedemption.create() gate (a different entity)"
  - "code expire is a soft-expire via UpdateItem (SET expiresAt = now with ConditionExpression attribute_exists(pk)) rather than DeleteItem — preserves redemptionCount history and matches AccessCode's optional expiresAt semantics"
  - "Tier ids for the seeded demo tiers: no-access (0/0/0 — cannot start voice sessions), demo-tier (session-max 120s = 2min, period-max 600s, max-concurrent 1), kphdemo-tier (session-max 1800s = 30min, period-max 3600s, max-concurrent 1) — period-max/max-concurrent values were not specified by the design spec beyond the session-max examples, so reasonable per-day/concurrency bounds were chosen"

requirements-completed: [KV-01, KV-02]

coverage:
  - id: D1
    description: "kv Go CLI (go 1.26, cobra v1.10.2, aws-sdk-go-v2) builds and its help tree renders for root, code, and tier"
    requirement: KV-01
    verification:
      - kind: integration
        ref: "go build ./... && go run ./cmd/kv --help / code --help / tier --help (manually run, all render correctly with create/list/expire and define/list subcommands)"
        status: pass
    human_judgment: false
  - id: D2
    description: "electro/keys.go reproduces the 03-02 AccessCode + Tier pk/sk/gsi1 key templates byte-for-byte, including case normalization (kv-normalized 'DEMO' keys identically to webapp-normalized 'demo')"
    requirement: KV-01
    verification:
      - kind: unit
        ref: "kv/internal/app/electro/keys_test.go (TestKeyCompat_AccessCode, TestKeyCompat_CaseCrossCheck, TestKeyCompat_Tier, TestAccessCodeItem_Marshal, TestTierItem_Marshal — all pass, no container required)"
        status: pass
    human_judgment: false
  - id: D3
    description: "Bidirectional round-trip: a kv-written code is found by a webapp-shaped GetItem, and a webapp-shaped PutItem is found by kv code list's own gsi1 query — the phase's top de-risking gate (Pitfall 1)"
    requirement: KV-01
    verification:
      - kind: integration
        ref: "kv/internal/app/cmd/roundtrip_test.go (TestRoundTrip_KVWriteWebappRead, TestRoundTrip_WebappWriteKVRead — both pass against a live dynamodb-local kmv-auth-electro table)"
        status: pass
    human_judgment: false
  - id: D4
    description: "kv code create/list/expire and kv tier define/list perform real DynamoDB PutItem/Query(gsi1pk-gsi1sk-index)/UpdateItem against the electro table; code charset validated before write (T-03-10)"
    requirement: KV-01
    verification:
      - kind: integration
        ref: "Manual smoke test against dynamodb-local: tier define + code create + code list + code expire + code list --json, then aws dynamodb get-item confirming field-for-field match with a live ElectroDB item shape (see Deviations/Issues section for the exact commands and output)"
        status: pass
    human_judgment: false
  - id: D5
    description: "kv seeds the design-spec codes/tiers: demo -> 2-minute tier, kphdemo123 -> 30-minute tier, plus a no-access tier"
    requirement: KV-02
    verification:
      - kind: integration
        ref: "Seed commands run against dynamodb-local's kmv-auth-electro table; kv tier list / kv code list confirm no-access, demo-tier (120s), kphdemo-tier (1800s), demo, and kphdemo123 are present exactly once (see Accomplishments for the exact commands)"
        status: pass
    human_judgment: false
  - id: D6
    description: "kv tier define/list commands work end-to-end against the electro table's gsi1 tiers# partition"
    requirement: KV-02
    verification:
      - kind: integration
        ref: "Manual smoke test (tier define smoke-test-tier, tier list, aws dynamodb get-item confirming tier#/tiers# key shape) — see Accomplishments"
        status: pass
    human_judgment: false

duration: 4min
completed: 2026-07-05
status: complete
---

# Phase 3 Plan 04: kv Go CLI — access codes + tiers, ElectroDB key-compat Summary

**A Go `kv` CLI (cobra, sibling to `km`) with `code create/list/expire` and `tier define/list`, whose `electro/keys.go` reproduces the 03-02 ElectroDB key templates byte-for-byte — proven bidirectionally compatible against a live dynamodb-local table (Pitfall 1 closed) — seeded with the design-spec `demo`/`kphdemo123` codes and their tiers.**

## Performance

- **Duration:** 4 min (task-commit span, 03c996b→6729cfa; excludes upfront research/reading)
- **Started:** 2026-07-05T13:50:56-04:00
- **Completed:** 2026-07-05T13:54:53-04:00
- **Tasks:** 3
- **Files modified:** 9 (all new)

## Accomplishments

- New `kv/` Go module (`go 1.26`, `cobra v1.10.2`, `aws-sdk-go-v2 v1.42.1` + `service/dynamodb v1.59.2` + `feature/dynamodb/attributevalue v1.20.50` + `config`/`credentials`), mirroring klanker-maker's `km` cmd/main.go → internal/app/cmd.Execute() structure
- `kv code create|list|expire` and `kv tier define|list` command trees with the exact flag surface from 03-RESEARCH.md §"kv cobra command surface"
- `electro/keys.go`: pure key-builder functions (`AccessCodePK`/`AccessCodeGSI1SK`/`TierPK`/`TierGSI1SK`/etc.) plus `AccessCodeItem`/`TierItem` structs whose `Marshal()` produces the exact ElectroDB item shape — **verified against a live `aws dynamodb scan` of `kmv-auth-electro`** (the actual bytes ElectroDB writes: `pk`, `sk`, `gsi1pk`, `gsi1sk`, `__edb_e__`, `__edb_v__="1"`, plus entity attributes with optional fields entirely omitted when unset)
- **Pitfall 1 closed, proven two ways:**
  - `keys_test.go` — pure string-equality table tests (no container): AccessCode/Tier pk/sk/gsi1 strings match the 03-02 templates exactly, including a direct case-cross-check (`AccessCodePK("DEMO") == AccessCodePK("demo")`)
  - `roundtrip_test.go` — live against dynamodb-local: `TestRoundTrip_KVWriteWebappRead` (kv creates a code, a raw GetItem built with the webapp's exact key composition finds it) and `TestRoundTrip_WebappWriteKVRead` (a webapp-shaped PutItem is found by kv's own `ListAccessCodes` gsi1 query) — **both pass**
- Manual end-to-end smoke test against dynamodb-local (`kmv-auth-electro`, the same table 03-02's tests provisioned): `tier define` → `code create` → `code list` → `code expire` → `code list --json`, then a raw `aws dynamodb get-item` confirming the written item is field-for-field identical to a live ElectroDB item — cleaned up afterward
- Seeded the design-spec tiers and codes on the local table:
  ```
  kv tier define no-access --session-max 0 --period-max 0 --max-concurrent 0
  kv tier define demo-tier --session-max 120 --period-max 600 --max-concurrent 1
  kv tier define kphdemo-tier --session-max 1800 --period-max 3600 --max-concurrent 1
  kv code create demo --tier demo-tier --group conference
  kv code create kphdemo123 --tier kphdemo-tier --group conference
  ```
  Confirmed present exactly once via `kv tier list` / `kv code list` (see Issues Encountered for a note on pre-existing unrelated test-data pollution in the same table).
- `go build ./...`, `go vet ./...`, `go test ./...` all clean; naming check (`voiceai`/`kmk`) clean across `kv/`

## Task Commits

Each task was committed atomically:

1. **Task 1: Scaffold kv module + root/code/tier command tree** - `03c996b` (feat)
2. **Task 2: ElectroDB key reproduction + DynamoDB CRUD wired to the electro table** - `b6a754a` (feat)
3. **Task 3: Round-trip key-compat test + seed demo/kphdemo123 tiers and codes** - `6729cfa` (test)

_No separate plan-metadata commit — STATE.md/ROADMAP.md are orchestrator-owned in worktree mode (per this plan's explicit instruction)._

## Files Created/Modified

- `kv/go.mod`, `kv/go.sum` — new Go module
- `kv/cmd/kv/main.go` — entrypoint, calls `cmd.Execute()`
- `kv/internal/app/cmd/root.go` — `NewRootCmd()`, `Config` (table/endpoint/region flags), `DynamoClient()`
- `kv/internal/app/cmd/code.go` — `code create|list|expire`, `CreateAccessCode`/`ListAccessCodes`/`ExpireAccessCode`, `validateCodeCharset`
- `kv/internal/app/cmd/tier.go` — `tier define|list`, `DefineTier`/`ListTiers`
- `kv/internal/app/electro/keys.go` — key-string builders + `AccessCodeItem`/`TierItem` + `Marshal()`
- `kv/internal/app/electro/keys_test.go` — pure key-string + item-shape assertions
- `kv/internal/app/cmd/roundtrip_test.go` — live bidirectional compatibility gate against dynamodb-local

## Decisions Made

See frontmatter `key-decisions` for the full list. Highlights:
- electro package split into pure key-string functions (testable with zero AWS dependency) vs. `Marshal()` (AWS attribute-value types) — lets the Pitfall-1 gate run in two tiers: an always-on pure test and a live round-trip test that skips gracefully without a container.
- `__edb_e__`/`__edb_v__` bookkeeping values were read directly off a live `aws dynamodb scan` of `kmv-auth-electro` (populated by 03-02's own test runs) rather than inferred from ElectroDB source — ground truth, not guesswork.
- `code create`/`tier define` use unconditional `PutItem` (create-or-replace), matching an idempotent operator seed-script use case.
- `code expire` is a soft-expire (`UpdateItem SET expiresAt = now`, condition `attribute_exists(pk)`) — preserves `redemptionCount` history rather than deleting the row.

## Deviations from Plan

None — plan executed exactly as written. The only structural choice not explicitly dictated by the plan was splitting Task 1's scaffold into stub `RunE` bodies (returning `"not implemented yet"`) and then wiring the real DynamoDB calls in Task 2, matching the plan's own task breakdown and files_modified lists precisely (rather than writing the full implementation in one pass, which would have blurred the two tasks' atomic-commit boundary).

## Issues Encountered

- **Pre-existing test-data pollution in the shared `kmv-auth-electro` dynamodb-local table:** 03-02's own vitest suite runs appear to have left many timestamp-suffixed test records (`demo-<epoch>-<rand>`, `uniq-same-user-<epoch>-<rand>`, `capped-<epoch>-<rand>`, etc.) in the table across multiple prior sessions — visible in `kv code list`/`kv tier list` output alongside this plan's clean `demo`/`kphdemo123`/`no-access`/`demo-tier`/`kphdemo-tier` seed data. This is out of this plan's scope (a test-isolation gap in 03-02's own test suite against a shared local container, not a kv defect) and was not touched, per the deviation-rules scope boundary. `kv`'s own seed items are each present exactly once and unaffected.
- **Go toolchain:** the ambient `go` was 1.25.5; `go.mod`'s `go 1.26` directive triggered `GOTOOLCHAIN=auto` to transparently fetch a matching 1.26.x toolchain on first build — no manual intervention needed, confirmed working via `go build ./...`.
- **Worktree cwd mix-up (self-corrected, no lasting effect):** an initial `go mod init` was accidentally run against the main checkout path (`/Users/khundeck/working/klanker-voice/kv`) instead of the worktree path — caught before any files were staged or committed there (confirmed via `git status --short kv` showing it untracked in the wrong repo). All subsequent work was redone correctly inside the worktree (`/Users/khundeck/working/klanker-voice/.claude/worktrees/agent-a9d584d397a55b132/kv`); the stray directory in the main checkout was left in place (deletion there is blocked by a safety guard and it is harmless/untracked).

## User Setup Required

None. All seeding was performed against the existing local `dynamodb-local` container's `kmv-auth-electro` table (already provisioned by 03-01/03-02's own setup). Seeding the **real** AWS `kmv-auth-electro` table (once deployed) is an operational follow-up using the exact same `kv` commands documented above, pointed at the live table via `--table`/ambient AWS credentials (no `--endpoint-url` needed).

## Next Phase Readiness

- **KV-01/KV-02 complete:** an operator can create/list/expire access codes and define/list tiers via `kv`, and Pitfall 1 (Go↔ElectroDB key-format compatibility) is closed and proven bidirectionally.
- **Phase 4 (voice service):** the `Tier` entity's `sessionMaxSeconds`/`periodMaxSeconds`/`maxConcurrent` fields (readable via `kv tier list --json` or directly by the voice service's own DynamoDB read) are the thin-token source of truth (D-01) the voice service will read at session start.
- **Operational follow-up (not blocking):** once the real `kmv-auth-electro` table is deployed to AWS (infra not yet applied for this table per 03-02's Known Gaps), run the same seed commands documented above against it (and consider adding `ttl_enabled`/`ttl_attribute_name` to the terragrunt `service.hcl`, per 03-02's own recommendation — that remains unapplied and is not part of this plan's `files_modified` scope).

---
*Phase: 03-auth-service-access-codes*
*Completed: 2026-07-05*

## Self-Check: PASSED

- Created files verified present: `kv/go.mod`, `kv/go.sum`, `kv/cmd/kv/main.go`, `kv/internal/app/cmd/root.go`, `kv/internal/app/cmd/code.go`, `kv/internal/app/cmd/tier.go`, `kv/internal/app/electro/keys.go`, `kv/internal/app/electro/keys_test.go`, `kv/internal/app/cmd/roundtrip_test.go`
- Commits verified present in `git log --oneline`: `03c996b` (scaffold), `b6a754a` (DynamoDB wiring), `6729cfa` (round-trip test + seed)
- `go build ./...`, `go vet ./...`, `go test ./...` all pass; `go run ./cmd/kv --help`/`code --help`/`tier --help` render correctly; naming check clean
