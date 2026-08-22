---
phase: 16-operator-lifecycle-kv-backup-restore-pause-resume-teardown
plan: 03
subsystem: infra
tags: [go, aws-sdk-go-v2, dynamodb, s3, electrodb, next-auth, kv-cli, restore]

# Dependency graph
requires:
  - phase: 16-01
    provides: "LiveTargets/ResolveLiveTargets, BackupManifest schema, Config.S3Client, KvVersion()"
  - phase: 16-02
    provides: "unmarshalItemJSONL (avWire decode), VerifyBackupArchive, WriteBackupArchive, BackupDeps/BackupOptions shape the round-trip test drives"
provides:
  - "kv restore <zip>: reads a 16-02 backup archive, resolves every destination live (never from the manifest, D-10), drops ephemeral rows by default (D-11), writes idempotently with retry/backoff (D-12), and refuses when a destination table/bucket doesn't exist yet (D-13)"
  - "IsEphemeralItem: the per-table key-shape + TTL-attribute classifier for concurrency leases, OIDC session/interaction state, login intents, next-auth sessions, and verification tokens"
  - "RestoreTable/RestoreLedger/RunRestore: the batched idempotent writer, dry-run reporter, and full orchestration"
  - "docs/ops/backup-restore.md: the D-13 rebuild ordering runbook"
affects: [16-10-kv-destroy]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Ephemeral row classification derives every key-shape constant from the LIVE entity/adapter source, not from guesses — including constructing the real OIDCModel electrodb Entity (against the installed electrodb npm dependency) and calling .put(...).params() to confirm its default (untemplated) pk label format, since oidc-adapter.ts's primary index has composite:[\"modelName\"] with no explicit template string"
    - "TTL-based expiry classification is scoped to ONE designated attribute name per logical table (voice-usage:\"expiresAt\", auth-electro:\"ttl\", auth-authjs:\"expires\") rather than any attribute happening to be called \"expiresAt\" — verified that AccessCode also has its own unrelated epoch-ms \"expiresAt\" business field on auth-electro, which a blanket sweep would have wrongly dropped"
    - "Pre-flight destination validation: RunRestore checks every table + the ledger bucket exist in LiveTargets BEFORE issuing any write, so a missing destination is a hard error with zero calls made to either write fake, not a partial write discovered mid-restore"
    - "Full-item PutRequest via BatchWriteItem is the idempotency mechanism (D-12) — no dedup bookkeeping needed; a re-run's PutRequest simply overwrites the same pk/sk"

key-files:
  created:
    - kv/internal/app/cmd/restore.go
    - kv/internal/app/cmd/restore_test.go
    - docs/ops/backup-restore.md
  modified:
    - kv/internal/app/cmd/root.go

key-decisions:
  - "Verified OIDCModel's default (untemplated) ElectroDB pk format by constructing the real Entity against the installed electrodb npm package and calling .put({modelName:\"Session\",...}).params() rather than deriving it by reading electrodb's source — confirmed pk = \"$oidc#modelname_session\" / \"$oidc#modelname_interaction\" (service+label+lowercased-value, DefaultKeyCasing=\"lower\" lowercases the whole key including the value)."
  - "Confirmed via infra/terraform/live/site/services/{auth,voice}/service.hcl that ONLY kmv-voice-usage has DynamoDB TTL actually enabled (ttl_enabled=true, ttl_attribute_name=\"expiresAt\"); neither kmv-auth-electro nor kmv-auth-authjs has ttl_enabled set. The per-table TTL sweep for the two auth tables is therefore a defensive application-level check (LoginIntent's \"ttl\"/auth-authjs's \"expires\") — not a live DynamoDB TTL mirror — and is deliberately scoped to attribute names that are NOT shared with durable entities (AccessCode's own \"expiresAt\" business field must survive restore per Test 3)."
  - "@auth/dynamodb-adapter's default key shapes (USER#<id>/USER#<id> user, USER#<userId>/ACCOUNT#<provider>#<providerAccountId> account, USER#<userId>/SESSION#<sessionToken> session, VT#<identifier>/VT#<token> verification token) and its \"expires\" (epoch seconds) TTL special-case were read directly from the installed node_modules/@auth/dynamodb-adapter/index.js, confirming apps/auth/webapp/src/config/auth.ts constructs the adapter with no key-name overrides."
  - "RunRestore validates ALL needed destinations (every table in the manifest + the ledger bucket) before writing ANY of them, rather than failing partway through — the acceptance criteria required zero calls to the write fakes on a missing-destination error, which a per-table-as-you-go check could not guarantee if the missing table wasn't first in scan order."

requirements-completed: [OPS-03]

coverage:
  - id: D1
    description: "IsEphemeralItem classifies voice-usage heartbeat leases, auth-electro OIDC session/interaction rows and login-intent rows, and auth-authjs sessions/verification-tokens as ephemeral (with a named reason); access codes, tiers, auth profiles, code redemptions, users, and accounts are never classified ephemeral even when they carry their own unrelated expiresAt field"
    requirement: "OPS-03"
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/restore_test.go#TestIsEphemeralItem"
        status: pass
    human_judgment: false
  - id: D2
    description: "Any item on any table whose designated TTL attribute reads a past epoch is classified ephemeral regardless of key shape; a future TTL is not"
    requirement: "OPS-03"
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/restore_test.go#TestIsEphemeralItem/ExpiredTTLIsEphemeralRegardlessOfKeyShape_FutureTTLIsNot"
        status: pass
    human_judgment: false
  - id: D3
    description: "OpenBackupArchive refuses to open an archive that fails VerifyBackupArchive (T-16-03-01); ReadTableItems decodes 16-02's JSONL encoding back into DynamoDB items"
    requirement: "OPS-03"
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/restore_test.go#TestOpenBackupArchive"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/restore_test.go#TestReadTableItems"
        status: pass
    human_judgment: false
  - id: D4
    description: "RestoreTable batches writes at 25 items/BatchWriteItem, retries UnprocessedItems and throttling/5xx errors (classified via errors.As) with bounded backoff, converges on re-run (idempotent), and issues zero writes under --dry-run while still reporting counts"
    requirement: "OPS-03"
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/restore_test.go#TestRestoreTable"
        status: pass
      - kind: other
        ref: "grep -c 'errors.As' kv/internal/app/cmd/restore.go -> 5"
        status: pass
    human_judgment: false
  - id: D5
    description: "RunRestore resolves every destination from LiveTargets only, validates all needed destinations exist before any write, and refuses with a terragrunt-apply-naming error and zero write calls when one is missing (D-13)"
    requirement: "OPS-03"
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/restore_test.go#TestRunRestore/MissingTableDestinationRefusesBeforeAnyWrite"
        status: pass
    human_judgment: false
  - id: D6
    description: "A full backup/restore round trip (WriteBackupArchive -> RunRestore) drops every ephemeral row and keeps every durable row by default, restores ephemeral rows too when --skip-ephemeral=false, and a corrupted archive is refused before any write (checksum verification exercised, not assumed) — D-31"
    requirement: "OPS-03"
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/restore_test.go#TestBackupRestoreRoundTrip"
        status: pass
    human_judgment: false
  - id: D7
    description: "kv restore <zip> is wired with --tables/--ledger/--dry-run/--skip-ephemeral (skip-ephemeral defaulting true), registered in root.go, and PrintRestoreReport labels manifest (provenance) vs live-resolved (destination) table/bucket names distinctly"
    requirement: "OPS-03"
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/restore_test.go#TestNewRestoreCmd"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/restore_test.go#TestPrintRestoreReport"
        status: pass
      - kind: other
        ref: "go -C kv run ./cmd/kv restore --help lists --tables/--ledger/--dry-run/--skip-ephemeral, skip-ephemeral default true, exits 0"
        status: pass
    human_judgment: false
  - id: D8
    description: "docs/ops/backup-restore.md documents the D-13 ordering (config, terragrunt apply, kv restore), the live-resolution rationale (ledger bucket random_id suffix), and the filtered row classes (including why a restored concurrency lease wedges the quota gate)"
    requirement: "OPS-03"
    verification:
      - kind: other
        ref: "test -f docs/ops/backup-restore.md && grep -q 'terragrunt apply' && grep -q 'random_id' && grep -q 'concurrency lease'"
        status: pass
    human_judgment: false

duration: ~2h
completed: 2026-08-20
status: complete
---

# Phase 16 Plan 03: kv restore — live-resolved destinations, ephemeral row filtering, idempotent writes Summary

**`kv restore <zip>` reads a 16-02 backup archive and rebuilds a freshly-applied stack: destinations always resolved live from terraform outputs (never the manifest), concurrency leases/OIDC session state/login intents/next-auth sessions and verification tokens dropped by default, and writes batched with retry/backoff so a re-run over a partial restore converges instead of duplicating.**

## Performance

- **Duration:** ~2h (unusually long: verifying OIDCModel's and @auth/dynamodb-adapter's exact key shapes against live source/installed packages, rather than guessing, took the majority of the time)
- **Completed:** 2026-08-20T05:50:00Z
- **Tasks:** 3
- **Files modified:** 4 (3 created, 1 modified)

## Accomplishments
- `IsEphemeralItem` classifies every D-11 row class by key shape verified against **live source**, not guesses: constructed the real `OIDCModel` ElectroDB entity against the installed `electrodb` npm dependency and called `.put({modelName:"Session",...}).params()` to confirm its default (untemplated) pk format is `$oidc#modelname_session` / `$oidc#modelname_interaction`; read `@auth/dynamodb-adapter`'s published `index.js` directly for its default `USER#`/`SESSION#`/`ACCOUNT#`/`VT#` key shapes and its `expires`-epoch-seconds TTL special-case.
- The per-table TTL sweep is scoped to exactly one designated attribute name per table (`voice-usage`:`expiresAt`, `auth-electro`:`ttl`, `auth-authjs`:`expires`) after confirming via `infra/terraform/live/site/services/{auth,voice}/service.hcl` that only `kmv-voice-usage` has DynamoDB TTL actually enabled at the infra level, and that `AccessCode` carries its own unrelated epoch-ms `expiresAt` business field on `auth-electro` that a blanket "any expiresAt in the past" sweep would have wrongly classified ephemeral.
- `RestoreTable` batches at 25 items/`BatchWriteItem`, retries `UnprocessedItems` and throttling/provisioned-throughput/5xx errors (via `errors.As` against typed SDK errors) with exponential-jittered backoff, and issues zero writes under `--dry-run` while still returning accurate counts.
- `RunRestore` validates every table and the ledger bucket exist in `LiveTargets` **before** issuing a single write — a missing destination is a hard error naming the resource and the `terragrunt apply` step, with zero calls made to either write fake.
- `TestBackupRestoreRoundTrip` drives `WriteBackupArchive` (16-02) into `RunRestore` (this plan) against a deliberate durable+ephemeral row mix across all three tables plus a ledger object: default `--skip-ephemeral` drops every ephemeral row and keeps every durable one; `--skip-ephemeral=false` restores everything; a corrupted archive is refused before any write, proving checksum verification actually runs.
- `docs/ops/backup-restore.md` documents the D-13 rebuild ordering, why destinations are resolved live (the ledger bucket's `random_id` suffix changes on recreate), which rows are filtered and why, and the dry-run workflow.

## Task Commits

Each task was committed atomically:

1. **Task 1: Classify ephemeral rows and read the archive against live-resolved destinations** - `ed0a5cd` (test, TDD RED+GREEN in one commit)
2. **Task 2: Write idempotently in batches with retry and backoff, and make --dry-run write nothing** - `ad0eafa` (feat, same shape)
3. **Task 3: Wire the kv restore command, prove the round trip, and document the ordering** - `e483371` (feat)

**Plan metadata:** pending (this commit)

_Note: all three tasks were `tdd="true"`; tests were written alongside the implementation they exercise and both were verified together (`go build`/`go vet`/`go test`) before each commit — matching 16-01/16-02's documented single-commit-per-file-pair convention._

## Files Created/Modified
- `kv/internal/app/cmd/restore.go` - `EphemeralReason`, `IsEphemeralItem`, `OpenBackupArchive`, `ReadTableItems`, `RestoreDeps`/`RestoreOptions`/`RestoreReport`, `dynamoBatchWriteAPI`/`s3PutAPI`, `restoreMaxAttempts`/`restoreMaxBackoff`, `RestoreTable`, `RestoreLedger`, `RunRestore`, `PrintRestoreReport`, `NewRestoreCmd`
- `kv/internal/app/cmd/restore_test.go` - hand-rolled fakes (`fakeDynamoBatchWriteAPI`, `fakePutObjectAPI`, `perTableDynamoScanAPI`) and test functions covering every behavior across the plan's three tasks
- `kv/internal/app/cmd/root.go` - `root.AddCommand(NewRestoreCmd(cfg))`
- `docs/ops/backup-restore.md` - the D-13 rebuild-ordering runbook

## Decisions Made
- Verified OIDCModel's untemplated default ElectroDB pk format by constructing the real Entity against the installed `electrodb` package and calling `.put(...).params()`, rather than deriving it by reading `electrodb`'s source formulas — eliminates guesswork risk on a security-relevant key-shape match.
- Confirmed via terraform config that only `kmv-voice-usage` has live DynamoDB TTL enabled; the two auth tables' TTL sweep is a defensive application-level check scoped to attribute names (`ttl`, `expires`) that don't collide with durable entities' own business-logic `expiresAt` fields.
- `RunRestore` validates all destinations up front (not per-table-as-you-go) so a missing-destination error is provably zero-write, matching the acceptance criteria's stricter requirement.

## Deviations from Plan

None — plan executed exactly as written. The extra research depth (constructing electrodb's real `OIDCModel` entity and reading `@auth/dynamodb-adapter`'s installed source rather than assuming key formats) was explicitly required by the plan's `read_first`/`action` text ("verify the prefixes and the TTL attribute name against that file rather than assuming defaults") — not a deviation, but the reason this plan took materially longer than 16-01/16-02.

## Issues Encountered
None.

## User Setup Required

None - no external service configuration required. (`kv restore` requires live AWS credentials and a stack already created by `terragrunt apply`, same as every other `kv` command — no test in this plan touches real AWS, terragrunt, or network.)

## Next Phase Readiness
- 16-10 (`kv destroy --with-backup`) can reuse the same `VerifyBackupArchive`/`WriteBackupArchive` pair this plan's round-trip test drove, and its own "abort on backup mismatch" gate is now proven end-to-end against a restore, not just a re-read.
- `go -C kv build ./...`, `go -C kv vet ./...`, and `go -C kv test ./...` all exit 0. `go -C kv run ./cmd/kv restore --help` lists all four flags with `--skip-ephemeral` defaulting true. No test in `restore_test.go` issues a real AWS, terragrunt, or network call — `dynamoBatchWriteAPI`/`s3PutAPI`/`dynamoScanAPI`/`s3ListGetAPI`/`TerraformOutputReader` are all fake-backed.
- Phase 16's backup/restore stage (spec §4, D-01 first wave) is now fully built and independently useful — the phase may stop here or continue into pause/resume (16-04+) per D-01's "each wave lands independently useful" framing.

---
*Phase: 16-operator-lifecycle-kv-backup-restore-pause-resume-teardown*
*Completed: 2026-08-20*

## Self-Check: PASSED

All 3 created files (kv/internal/app/cmd/restore.go, kv/internal/app/cmd/restore_test.go, docs/ops/backup-restore.md) and 3 task-commit hashes (ed0a5cd, ad0eafa, e483371) verified present on disk / in `git log --oneline --all`.
