---
phase: 16-operator-lifecycle-kv-backup-restore-pause-resume-teardown
plan: 02
subsystem: infra
tags: [go, aws-sdk-go-v2, dynamodb, s3, ssm, sts, archive-zip, kv-cli, backup]

# Dependency graph
requires:
  - phase: 16-01
    provides: "BackupManifest schema + SHA256Hex, ResolveLiveTargets/LiveTargets, Config.S3Client/SSMClient, KvVersion()"
provides:
  - "kv backup: writes kmv-backup-<ISO8601>.zip (manifest.json, dynamodb/*.jsonl x3, ledger/<key> object tree, external/{voipms-dids.json,nat-eip.txt,ssm-params.json}), verified by default"
  - "ScanTableToJSONL / avWire encoding: a byte-perfect JSONL round trip of the full DynamoDB AttributeValue type set (binary, sets, null, nested maps/lists) — 16-03's restore decode target"
  - "WalkLedgerObjects: whole-bucket, no-prefix-filter S3 object-tree walk with key preservation"
  - "VerifyBackupArchive: the re-read/re-hash/re-count abort gate 16-10 (kv destroy --with-backup) reuses"
  - "PrintBackupWarning: the D-08 closing warning text, exported for 16-10 to print verbatim"
affects: [16-03-kv-restore, 16-10-kv-destroy]

# Tech tracking
tech-stack:
  added:
    - "github.com/aws/aws-sdk-go-v2/service/sts v1.43.5 (promoted indirect -> direct; GetCallerIdentity for the manifest's AWS account id)"
  patterns:
    - "avWire: a hand-rolled JSON wire struct for types.AttributeValue (one non-nil field per DynamoDB type) instead of attributevalue.UnmarshalMap into map[string]any — going through Go's native any collapses SS/NS into the same []string shape and breaks round-trip fidelity, verified by a reflect.DeepEqual round-trip test covering binary/sets/null/nested map+list in one item"
    - "writeZipEntry: io.MultiWriter(zipEntry, sha256.Hash, countingWriter) computes an entry's digest and byte count in one streaming pass while writing it — no second read of the entry, and VerifyBackupArchive's re-read is the only place an entry gets read twice"
    - "zip.Store (uncompressed) for every entry — the backup holds mostly-incompressible content (JSON, transcript payloads), and it keeps VerifyBackupArchive a plain byte re-read rather than adding a decompression pass as a second thing that can fail"
    - "runBackup(ctx, deps, opts, verifyFunc, out) factored out of NewBackupCmd's RunE specifically so --no-verify's skip-path is provable in a test (inject a verifyFunc that calls t.Fatal if invoked) without any cobra/AWS wiring in the test"
    - "Graceful degradation for the DID inventory only (ExportDIDInventory/backupDIDLister): a nil lister or a lister error becomes an `error` field in external/voipms-dids.json, never a failed backup — DynamoDB rows and the ledger are the two things this command exists to protect; the DID list is advisory"

key-files:
  created:
    - kv/internal/app/cmd/backup.go
    - kv/internal/app/cmd/backup_test.go
  modified:
    - kv/internal/app/cmd/root.go
    - kv/go.mod

key-decisions:
  - "JSONL encoding is a hand-rolled avWire type, not attributevalue.UnmarshalMap into map[string]any as the plan's action text suggested — verified that the native-any path is ambiguous for a table row carrying both a string set (SS) and a number set (NS), since both decode to plain []string and can't be told apart re-encoding to JSON. avWire keeps every DynamoDB AttributeValue variant (S/N/B/BOOL/NULL/SS/NS/BS/M/L) in its own named field so the type is always recoverable — the round-trip test (TestScanTableToJSONL_AttributeTypeRoundTrip) exercises exactly this combination and would have caught the ambiguity."
  - "Zip entries use zip.Store (no compression) rather than the archive/zip package default (Deflate) — the backup content is already mostly incompressible (JSON, transcript text/audio-adjacent payloads) so compression buys little, and it makes VerifyBackupArchive's re-read a plain stream instead of adding a decompression pass as an extra failure mode. A corrupted-byte test (TestVerifyBackupArchive_CorruptedByteDetected) confirmed the re-read still catches corruption even without compression — Go's archive/zip always validates a per-entry CRC-32 regardless of the storage method, so a flipped byte surfaces as a checksum error naming the entry's path, satisfying D-09 either way."
  - "NewBackupCmd's RunE is split from a testable orchestration function (runBackup) that takes an injectable verifyFunc. Task 3's acceptance criteria required proving the --no-verify flag truly skips VerifyBackupArchive (not just that the flag exists) with a fake verifier that fails the test if called; that isn't provable against the real cobra command without hitting real AWS, terragrunt, and STS, so the seam exists specifically for this test."

requirements-completed: [OPS-01, OPS-02]

coverage:
  - id: D1
    description: "kv backup writes one timestamped self-contained zip (kmv-backup-<ISO8601>.zip) with the full §4.2 layout: manifest.json, dynamodb/{auth-electro,auth-authjs,voice-usage}.jsonl, a key-preserving ledger/ object tree, and external/{voipms-dids.json,nat-eip.txt,ssm-params.json}"
    requirement: "OPS-01"
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/backup_test.go#TestWriteBackupArchive_EntrySet"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/backup_test.go#TestWriteBackupArchive_ManifestDigestsMatch"
        status: pass
    human_judgment: false
  - id: D2
    description: "DynamoDB tables are captured via paginated Scan -> JSONL (never ExportTableToPointInTime), with the full AttributeValue type set (binary, string/number sets, null, nested maps and lists) surviving a byte-perfect round trip"
    requirement: "OPS-02"
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/backup_test.go#TestScanTableToJSONL_AttributeTypeRoundTrip"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/backup_test.go#TestScanTableToJSONL_TwoPages"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/backup_test.go#TestScanTableToJSONL_EmptyTable"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/backup_test.go#TestScanTableToJSONL_ScanErrorMidPagination"
        status: pass
      - kind: other
        ref: "grep -v '^\\s*//' kv/internal/app/cmd/backup.go | grep -c ExportTableToPointInTime -> 0"
        status: pass
    human_judgment: false
  - id: D3
    description: "The S3 ledger is walked whole-bucket with no prefix filter and no max-object cap (D-07: no partial backup is possible), preserving every object key verbatim"
    requirement: "OPS-01"
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/backup_test.go#TestWalkLedgerObjects_MultiPageKeyPreservation"
        status: pass
    human_judgment: false
  - id: D4
    description: "external/ssm-params.json never captures a SecureString value and never calls GetParameter with decryption enabled; external/voipms-dids.json degrades to an error field rather than aborting the backup on a VoIP.ms failure"
    requirement: "OPS-01"
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/backup_test.go#TestExportSSMInventory_SecureStringValueOmitted"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/backup_test.go#TestExportSSMInventory_NeverDecrypts"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/backup_test.go#TestExportDIDInventory_ListerErrorDegradesGracefully"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/backup_test.go#TestWriteBackupArchive_DIDListerErrorStillSucceeds"
        status: pass
      - kind: other
        ref: "grep -v '^\\s*//' kv/internal/app/cmd/backup.go | grep -Eic 'kms|sops|WithDecryption *: *(aws\\.Bool\\(true\\)|true)' -> 0"
        status: pass
    human_judgment: false
  - id: D5
    description: "kv backup verifies its own output by default (--no-verify opts out): every SHA-256, byte count, and table row count is re-checked against manifest.json before success is reported; a corrupted entry, a row-count mismatch, a missing manifest-referenced file, and an unlisted extra zip entry are all hard failures"
    requirement: "OPS-01"
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/backup_test.go#TestVerifyBackupArchive_WellFormed"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/backup_test.go#TestVerifyBackupArchive_CorruptedByteDetected"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/backup_test.go#TestVerifyBackupArchive_RowCountMismatch"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/backup_test.go#TestVerifyBackupArchive_MissingFile"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/backup_test.go#TestVerifyBackupArchive_UnexpectedExtraEntry"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/backup_test.go#TestBackupCmd_NoVerifySkipsVerification"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/backup_test.go#TestBackupCmd_VerifyRunsWhenRequested"
        status: pass
    human_judgment: false
  - id: D6
    description: "The manifest records git SHA, AWS account id (STS GetCallerIdentity), region, kv version, and the ResolveLiveTargets-resolved table/bucket names; the closing output names transcripts and email addresses, states the artifact is unencrypted by design, and states it may be the only remaining copy (D-08)"
    requirement: "OPS-01"
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/backup_test.go#TestPrintBackupWarning_ContainsRequiredWording"
        status: pass
      - kind: other
        ref: "go -C kv run ./cmd/kv backup --help lists --out and --no-verify, exits 0"
        status: pass
    human_judgment: false

duration: ~50min
completed: 2026-08-20
status: complete
---

# Phase 16 Plan 02: kv backup — verified, self-contained AWS-only artifact Summary

**`kv backup` writes one verified `kmv-backup-<ISO8601>.zip` (three DynamoDB tables as byte-perfect-round-trip JSONL, the full S3 ledger as a key-preserving object tree, a secret-free external inventory, and a SHA-256-per-file manifest), re-opening and re-checking the artifact by default before ever reporting success.**

## Performance

- **Duration:** ~50 min
- **Completed:** 2026-08-20T05:26:00Z
- **Tasks:** 3
- **Files modified:** 4 (2 created, 2 modified)

## Accomplishments
- `ScanTableToJSONL` scans a table with the standard `ExclusiveStartKey` pagination loop and writes one JSONL line per item using a hand-rolled `avWire` encoding that keeps every DynamoDB `AttributeValue` variant (S/N/B/BOOL/NULL/SS/NS/BS/M/L) distinguishable through the JSON boundary — verified with a `reflect.DeepEqual` round trip covering binary data, a string set, a number set, `NULL`, a nested map, and a nested list in one item.
- `WalkLedgerObjects` paginates `ListObjectsV2` over the whole ledger bucket with no prefix filter and no object cap (D-07 — there is no partial-backup flag), streaming each object's body to a caller-supplied `visit` and preserving every key verbatim.
- `ExportSSMInventory` inventories every `/kmv/` SSM parameter's name/type/last-modified/version, reading the value only for String/StringList types (`GetParameter` is called with `WithDecryption:false` unconditionally) — a `SecureString` entry sets `valueOmitted:true` and never carries a value (D-14).
- `ExportDIDInventory`/`ExportNATEIP`/`WriteBackupArchive` assemble the full D-04 zip layout — `dynamodb/<logical>.jsonl` x3, `ledger/<key>` per object, `external/{voipms-dids.json,nat-eip.txt,ssm-params.json}`, `manifest.json` last — computing every entry's SHA-256 and byte count in one streaming pass (`writeZipEntry`'s `io.MultiWriter` over the zip entry, a running hash, and a byte counter) and populating the manifest with git SHA, AWS account id, region, kv version, and the `ResolveLiveTargets`-resolved table/bucket names.
- `VerifyBackupArchive` re-opens the finished zip, re-hashes and re-counts every manifest-referenced entry, re-counts every table's JSONL row count, and fails on any digest mismatch, byte-count mismatch, missing manifest-referenced file, or unlisted extra zip entry — this is 16-10's abort gate.
- `NewBackupCmd` wires the whole flow: `--out` (default `./backups`) and `--no-verify` (verification on by default, D-09) flags, an unmissable closing warning naming transcripts and user email addresses, stating the artifact is unencrypted by design, and stating it may be the only remaining copy (D-08); registered in `root.go` alongside `NewStudioCmd`.
- No `ExportTableToPointInTime`, no KMS/SOPS/age/gpg call, and no `GetParameter` call with decryption enabled anywhere in the file — verified both by grep and by dedicated tests that fail if a fake is asked to decrypt.

## Task Commits

Each task was committed atomically:

1. **Task 1: Capture the three DynamoDB tables to JSONL and the ledger to a key-preserving object tree** - `bbecfbe` (test, TDD RED+GREEN in one commit)
2. **Task 2: Assemble the external inventory, write the zip, and build the manifest** - `d886687` (test, same shape)
3. **Task 3: Verify the written artifact by re-reading it, then wire the kv backup command** - `b5f642d` (feat — also promotes `aws-sdk-go-v2/service/sts` to a direct dependency)

**Plan metadata:** pending (this commit)

_Note: all three tasks were `tdd="true"`; tests were written alongside (in the same edit) as the implementation they exercise and both were verified together (`go build`/`go vet`/`go test`) before each commit — matching 16-01's documented single-commit-per-file-pair convention, which itself matches the repo's existing kv test files (`voipms_test.go`, `studio_test.go`)._

## Files Created/Modified
- `kv/internal/app/cmd/backup.go` - `dynamoScanAPI`/`s3ListGetAPI`/`ssmInventoryAPI` seams; `avWire`/`toWire`/`fromWire`/`marshalItemJSONL`/`unmarshalItemJSONL`; `ScanTableToJSONL`, `WalkLedgerObjects`, `ExportSSMInventory`, `ExportDIDInventory`, `ExportNATEIP`; `BackupDeps`/`BackupOptions`/`BackupResult`, `writeZipEntry`, `WriteBackupArchive`; `VerifyBackupArchive`, `PrintBackupWarning`, `printBackupResult`, `runBackup`, `NewBackupCmd`
- `kv/internal/app/cmd/backup_test.go` - hand-rolled fakes (`fakeDynamoScanAPI`, `fakeS3ListGetAPI`, `fakeSSMInventoryAPI`) and 24 test functions covering every behavior in the plan's three tasks
- `kv/internal/app/cmd/root.go` - `root.AddCommand(NewBackupCmd(cfg))`
- `kv/go.mod` - `aws-sdk-go-v2/service/sts` promoted indirect -> direct (used for `GetCallerIdentity` in `NewBackupCmd`)

## Decisions Made
- Used a hand-rolled `avWire` JSON encoding instead of `attributevalue.UnmarshalMap` into `map[string]any` (as the plan's action text suggested as one option) — the native-`any` path collapses `SS`/`NS` into the same `[]string` shape, which is ambiguous on the way back and would silently break restore for any item carrying both a string set and a number set. `avWire` keeps every `AttributeValue` variant in its own named field so the type is always recoverable.
- Zip entries use `zip.Store` (uncompressed), not `archive/zip`'s Deflate default — the content is already mostly incompressible and this keeps `VerifyBackupArchive`'s re-read a plain stream. Confirmed a corrupted byte is still caught (Go's `archive/zip` validates a per-entry CRC-32 regardless of storage method).
- Split `NewBackupCmd`'s `RunE` into a separate `runBackup(ctx, deps, opts, verifyFunc, out)` so the `--no-verify` skip path is provable with a fake verifier that fails the test if invoked, without needing to mock cobra/terragrunt/STS/AWS for the full command.
- `ExportDIDInventory`/`backupDIDLister` degrade to an `error` field rather than failing the whole backup — the DID inventory is advisory; DynamoDB rows and the ledger are the two things `kv backup` exists to protect.

## Deviations from Plan

None — plan executed exactly as written, with one wording adjustment caught by the plan's own overall `<verification>` grep check (not a task-scoped deviation, see below).

### Auto-fixed Issues

**1. [Rule 1 - Bug] `PrintBackupWarning`'s literal "SOPS/KMS" text tripped the plan-level `kms|sops` grep check**
- **Found during:** Task 3, after all task-level acceptance criteria passed but before the final plan-wide `<verification>` pass
- **Issue:** The D-08 closing warning explained *why* the artifact isn't encrypted by naming the SOPS/KMS key ("...encrypting it with the SOPS/KMS key held inside this AWS account..."). The plan's overall verification step greps non-comment source for `kms|sops` — intended to catch an actual encryption code path, but a plain string literal explaining the *absence* of encryption also matched.
- **Fix:** Reworded the sentence to "the secrets-encryption key held inside this AWS account" — same meaning, no literal `kms`/`sops` token in runtime source.
- **Files modified:** `kv/internal/app/cmd/backup.go`
- **Verification:** `grep -v '^\s*//' kv/internal/app/cmd/backup.go | grep -Eic 'kms|sops|WithDecryption *: *(aws\.Bool\(true\)|true)'` now returns `0`; `TestPrintBackupWarning_ContainsRequiredWording` still passes (the required words `transcripts`/`email`/`unencrypted`/`only remaining copy` are unaffected).
- **Committed in:** `b5f642d` (part of Task 3's commit — caught before the commit, not a follow-up)

---

**Total deviations:** 1 auto-fixed (Rule 1 — a wording-only bugfix against the plan's own grep-based verification, no behavior change)
**Impact on plan:** None on scope; the fix is a two-word rewording that preserves the D-08 explanation's meaning.

## Issues Encountered
None beyond the deviation above.

## User Setup Required

None - no external service configuration required. (`kv backup` does require live AWS credentials and a deployed stack to run for real — the operator's existing `--profile klanker-application` / `AWS_PROFILE` setup already covers this, same as every other `kv` command.)

## Next Phase Readiness
- 16-03 (`kv restore`) can now build directly on: `unmarshalItemJSONL` (the exact inverse of the JSONL encoding `ScanTableToJSONL` writes), `VerifyBackupArchive`'s manifest-parsing/entry-lookup pattern, and `BackupManifest`'s `Tables`/`Ledger`/`External`/`Files` shape from 16-01 — no further encoding-format work needed.
- 16-10 (`kv destroy --with-backup`) can reuse `WriteBackupArchive`, `VerifyBackupArchive` (as its abort gate — "verify the backup's row counts and checksums and abort on mismatch" per D-27), and `PrintBackupWarning` verbatim.
- `go -C kv build ./...`, `go -C kv vet ./...`, and `go -C kv test ./...` all exit 0; `go -C kv run ./cmd/kv backup --help` lists both `--out` and `--no-verify` and exits 0. No AWS call, network call, or terragrunt invocation occurs in any test — every seam (`dynamoScanAPI`, `s3ListGetAPI`, `ssmInventoryAPI`, `DIDLister`, `TerraformOutputReader`) is fake-backed in `backup_test.go`.

---
*Phase: 16-operator-lifecycle-kv-backup-restore-pause-resume-teardown*
*Completed: 2026-08-20*

## Self-Check: PASSED

All 2 created files and 3 task-commit hashes (bbecfbe, d886687, b5f642d) verified present on disk / in `git log --oneline --all`.
