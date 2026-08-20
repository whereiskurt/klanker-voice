---
phase: 16-operator-lifecycle-kv-backup-restore-pause-resume-teardown
plan: 01
subsystem: infra
tags: [go, aws-sdk-go-v2, terraform, terragrunt, dynamodb, s3, ecs, elasticloadbalancingv2, kv-cli]

# Dependency graph
requires: []
provides:
  - "(*Config).S3Client / (*Config).ECSClient / (*Config).ELBv2Client on kv's Config, matching DynamoClient's loadAWS delegation"
  - "KvVersion() build-info helper"
  - "BackupManifest schema (D-05 fields) with SHA256Hex, Marshal/ParseBackupManifest/FileRef"
  - "TerraformOutputReader seam + ResolveLiveTargets/ResolveLedgerBucket/ResolveTableNames/ResolveECSPosture (D-10)"
  - "backups/ .gitignore entry (D-14), landed before any backup artifact can be written"
affects: [16-02-kv-backup, 16-03-kv-restore, 16-destroy, 16-pause-resume]

# Tech tracking
tech-stack:
  added:
    - "github.com/aws/aws-sdk-go-v2/service/s3 v1.107.2"
    - "github.com/aws/aws-sdk-go-v2/service/ecs v1.90.2"
    - "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2 v1.58.7"
  patterns:
    - "Config client accessors delegate to the existing loadAWS with no new credential logic (S3/ECS/ELBv2 mirror DynamoClient, minus the EndpointURL override which stays DynamoClient-only)"
    - "TerraformOutputReader narrow interface (D-10/D-30): production impl shells out to `terragrunt output -json` via exec.CommandContext; tests inject a fake backed by canned per-unit JSON, no cloud/terragrunt/network reachable from any test"
    - "Live-resolved destination names (LiveTargets) are structurally separated from audit-only manifest fields (BackupManifest.Tables[].TableName / Ledger.BucketName) — no code path connects one to the other"

key-files:
  created:
    - kv/internal/app/cmd/lifecycle_manifest.go
    - kv/internal/app/cmd/lifecycle_manifest_test.go
    - kv/internal/app/cmd/lifecycle_targets.go
    - kv/internal/app/cmd/lifecycle_targets_test.go
  modified:
    - kv/go.mod
    - kv/go.sum
    - kv/internal/app/cmd/root.go
    - .gitignore

key-decisions:
  - "Table logical keys (auth-electro, auth-authjs, voice-usage) are derived at resolve time by stripping the kmv- prefix from the dynamodb unit's physical table name — verified against infra/terraform/modules/dynamodb/v1.0.0/main.tf: the tables output map's key IS the physical table name, so this derivation survives a table-key rename without a hardcoded lookup table."
  - "ResolveECSPosture requires exactly one ECS cluster in the ecs-cluster unit's clusters output and errors otherwise, since infra/terraform/live/site/site.hcl currently declares exactly one (\"app\") and LiveTargets.ClusterName is a single string, not a map."
  - "terragruntOutputReader captures stdout/stderr separately (bytes.Buffer, not CombinedOutput) so OutputJSON never mixes terragrunt's log noise into the JSON it returns, and its error path includes only the unit directory + trimmed stderr — never the process environment (T-16-01-01)."

requirements-completed: [OPS-02, OPS-03]

coverage:
  - id: D1
    description: "Config gains S3Client/ECSClient/ELBv2Client accessors (delegating to loadAWS, no EndpointURL) and a KvVersion() build-info helper; three aws-sdk-go-v2 service modules added to go.mod"
    requirement: "OPS-02"
    verification:
      - kind: unit
        ref: "go -C kv build ./... && go -C kv vet ./..."
        status: pass
    human_judgment: false
  - id: D2
    description: "BackupManifest schema carries every D-05 field (git SHA, AWS account id, region, kv version, resolved table/bucket names, row/object/byte counts, per-file SHA-256), round-trips losslessly, rejects unknown versions and malformed bytes"
    requirement: "OPS-02"
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/lifecycle_manifest_test.go#TestBackupManifest_RoundTrip"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/lifecycle_manifest_test.go#TestSHA256Hex"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/lifecycle_manifest_test.go#TestBackupManifest_ParseUnknownVersion"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/lifecycle_manifest_test.go#TestBackupManifest_FileRef"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/lifecycle_manifest_test.go#TestBackupManifest_ParseMalformed"
        status: pass
    human_judgment: false
  - id: D3
    description: "ResolveLiveTargets resolves the three table names, ledger bucket, ECS cluster, three service names, target-group ARNs, and NAT EIP from canned terraform-output JSON via one injectable TerraformOutputReader interface — no code path lets a BackupManifest field become a destination (D-10)"
    requirement: "OPS-03"
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/lifecycle_targets_test.go#TestResolveLiveTargets/AllFiveUnits"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/lifecycle_targets_test.go#TestResolveLiveTargets/MissingOutputKey"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/lifecycle_targets_test.go#TestResolveLiveTargets/ReaderError"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/lifecycle_targets_test.go#TestResolveLiveTargets/LedgerOnlyEntryPoint"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/lifecycle_targets_test.go#TestResolveLiveTargets/DistinctBucketOnChange"
        status: pass
    human_judgment: false
  - id: D4
    description: "backups/ added to .gitignore before any artifact-writing code exists (D-14)"
    requirement: "OPS-02"
    verification:
      - kind: unit
        ref: "grep -qx 'backups/' .gitignore"
        status: pass
    human_judgment: false

duration: 35min
completed: 2026-08-20
status: complete
---

# Phase 16 Plan 01: Stage A Foundation (AWS clients, backup manifest schema, live-target resolution) Summary

**Three new aws-sdk-go-v2 Config client accessors, a BackupManifest schema with SHA-256 verification helpers, and a TerraformOutputReader seam that resolves every Phase-16 restore/destroy destination live from terraform outputs (D-10) — no command surface yet, just the foundation 16-02/16-03 build on.**

## Performance

- **Duration:** ~35 min
- **Completed:** 2026-08-20T03:41:46Z
- **Tasks:** 3
- **Files modified:** 8 (4 created, 4 modified)

## Accomplishments
- `kv`'s `Config` gained `S3Client`, `ECSClient`, and `ELBv2Client` accessors (all delegating to the existing `loadAWS`, mirroring `DynamoClient`/`SSMClient`'s shape exactly, with no new credential logic and no `EndpointURL` override) plus a `KvVersion()` build-info helper.
- `lifecycle_manifest.go` defines the D-05 backup-artifact schema (`BackupManifest`, `BackupTableRef`, `BackupLedgerRef`, `BackupFileRef`), a streaming `SHA256Hex`, and marshal/parse/lookup helpers — with a doc comment recording the D-10 audit-only rule directly on the type.
- `lifecycle_targets.go` defines the `TerraformOutputReader` seam, its production `terragrunt output -json` implementation, and `ResolveLiveTargets`/`ResolveLedgerBucket`/`ResolveTableNames`/`ResolveECSPosture` — every later Phase-16 command's only legal source of a restore/destroy destination name.
- `backups/` landed in `.gitignore` in Task 1, before any code capable of writing a backup artifact exists (D-14).
- Verified the dynamodb terraform module's `tables` output map is keyed by the *physical* table name (not a separate logical id) by reading `infra/terraform/modules/dynamodb/v1.0.0/main.tf`'s `local.tables` — this let `ResolveTableNames` derive the stable logical key (`auth-electro`/`auth-authjs`/`voice-usage`) by stripping the `kmv-` prefix instead of hardcoding a lookup table.

## Task Commits

Each task was committed atomically:

1. **Task 1: Add the AWS SDK service modules, the three Config client accessors, and the backups ignore entry** - `70f771b` (feat)
2. **Task 2: Define the backup manifest schema and its SHA-256 helpers** - `1af3ffe` (test, TDD RED+GREEN in one commit — tests written first, then the implementation, verified together before commit)
3. **Task 3: Resolve live targets from terraform outputs behind an injectable reader (D-10)** - `56d5601` (test, same TDD shape as Task 2)

**Plan metadata:** pending (this commit)

_Note: Tasks 2 and 3 were `tdd="true"`; tests were written and confirmed failing (compile-time absence of the types/functions under test) before the corresponding implementation file was written, then both were verified together before a single commit — the repo's existing kv test files (e.g. `voipms_test.go`, `studio_test.go`) follow the same single-commit-per-file-pair convention, so no separate RED-only commit was created._

## Files Created/Modified
- `kv/internal/app/cmd/lifecycle_manifest.go` - `BackupManifest`/`BackupTableRef`/`BackupLedgerRef`/`BackupFileRef`, `SHA256Hex`, `Marshal`/`ParseBackupManifest`/`FileRef`
- `kv/internal/app/cmd/lifecycle_manifest_test.go` - round-trip, digest, unknown-version, FileRef found/not-found, malformed-JSON tests
- `kv/internal/app/cmd/lifecycle_targets.go` - `TerraformOutputReader`, `terragruntOutputReader`, `LiveTargets`, `ResolveLiveTargets`/`ResolveLedgerBucket`/`ResolveTableNames`/`ResolveECSPosture`
- `kv/internal/app/cmd/lifecycle_targets_test.go` - fake-reader tests over canned per-unit output-JSON fixtures for all five terraform units
- `kv/internal/app/cmd/root.go` - added `S3Client`/`ECSClient`/`ELBv2Client` accessors and `KvVersion()`
- `kv/go.mod` / `kv/go.sum` - added `service/s3`, `service/ecs`, `service/elasticloadbalancingv2`; core `aws-sdk-go-v2` transitively bumped 1.43.0→1.43.6 by `go mod tidy` (upgrade only, nothing downgraded)
- `.gitignore` - `backups/` entry (D-14), placed adjacent to the existing `kv/bin/` kv-CLI section

## Decisions Made
- Logical table keys are derived from the physical table name (`kmv-` prefix strip) rather than hardcoded, since the dynamodb module's `tables` output map is keyed by physical name — this survives a future table-key rename in the terraform module without a kv-side lookup table to keep in sync.
- `ResolveECSPosture` requires exactly one entry in the `clusters` output and errors on any other count, matching `LiveTargets.ClusterName` being a single string (the live site currently declares exactly one cluster, `"app"`).
- `terragruntOutputReader` captures stdout and stderr into separate buffers (not `CombinedOutput`) so a failure's error message is deliberately narrow: unit directory + trimmed stderr only, never environment variables or credential material (T-16-01-01, verified by the task's own acceptance criteria).

## Deviations from Plan

None - plan executed exactly as written. `go get` + `go mod tidy` initially removed the three newly-added service modules from `go.mod` because nothing in the code imported them yet (module graph pruning); re-running `go get` after writing the `root.go` accessors and then `go mod tidy` again resolved this — not a deviation from the plan's intent, just the correct two-step sequence for adding a dependency before it has a call site.

## Issues Encountered
- A digest transcription typo in the first draft of `TestSHA256Hex`'s "hello world" fixture (`...efcde` vs the correct `...efcde9`) caused an initial test failure; verified the correct digest via `shasum -a 256` and fixed the fixture before committing. No implementation code was wrong — this was a test-fixture-only error caught by the very test it was writing.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness
- 16-02 (`kv backup`) and 16-03 (`kv restore`) can now build directly on: the three AWS client accessors, the `BackupManifest` schema + SHA-256 helpers, and `ResolveLiveTargets` (and its narrower entry points) — no further foundation work needed.
- No command surface exists yet (by design — this plan's `<objective>` explicitly excludes it); `kv backup`/`kv restore` subcommands are 16-02/16-03's job.
- `go -C kv build ./...`, `go -C kv vet ./...`, and `go -C kv test ./...` all exit 0 with the existing kv test suite (electro, studio packages) unaffected.

---
*Phase: 16-operator-lifecycle-kv-backup-restore-pause-resume-teardown*
*Completed: 2026-08-20*
