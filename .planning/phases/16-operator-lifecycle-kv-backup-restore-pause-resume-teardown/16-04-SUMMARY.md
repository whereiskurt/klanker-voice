---
phase: 16-operator-lifecycle-kv-backup-restore-pause-resume-teardown
plan: 04
subsystem: infra
tags: [go, aws-sdk-go-v2, dynamodb, s3, terragrunt, kv-cli, operator-gate]

# Dependency graph
requires:
  - phase: 16-02
    provides: "kv backup: verified, self-contained kmv-backup-<ISO8601>.zip"
  - phase: 16-03
    provides: "kv restore <zip>: live-resolved destinations, ephemeral-row filtering, idempotent batched writes, --dry-run"
provides:
  - "Live operator proof: kv backup and kv restore --dry-run both round-tripped against the real AWS account (052251888500), not just in-memory fakes"
  - "Measured ledger size (28 objects, 3,603,201 bytes) confirming the always-include-the-ledger design assumption (D-07) still holds at current scale"
  - "Proven corrupt-archive refusal against a real artifact holding real personal data, not only a test fixture"
  - "The ROADMAP's Stage-A gate ('round-tripped once against real data') is explicitly RELEASED — Stage C (16-10 kv destroy) is unblocked"
affects: [16-10-kv-destroy]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Operator runbooks that resolve live destinations via `terragrunt output` require infra/.envrc sourced first (S3 backend bucket/table vars) — now documented in docs/ops/backup-restore.md"

key-files:
  created: []
  modified:
    - docs/ops/backup-restore.md

key-decisions:
  - "No source-code changes made in this plan — it is a live-verification checkpoint only, per the plan's own D-32 prohibition (no destructive command; --dry-run only)."
  - "Documented the infra/.envrc prerequisite in docs/ops/backup-restore.md (small, in-scope doc fix) rather than deferring it, since it was the one real friction point an operator would hit verbatim."

requirements-completed: [OPS-01, OPS-02, OPS-03]

coverage:
  - id: D1
    description: "kv backup run once against the live stack; verification pass (on by default) re-read the artifact clean with no digest or row-count mismatch"
    requirement: "OPS-01"
    verification:
      - kind: manual_procedural
        ref: "kv/bin/kv backup --out ./backups against AWS account 052251888500 — artifact backups/kmv-backup-20260820T144605Z.zip, 6.2 MiB, 24.405s, verified clean"
        status: pass
    human_judgment: true
    rationale: "Requires real AWS credentials and produces a real artifact holding personal data; the plan explicitly prohibits self-approval (blocking checkpoint, operator resume signal required)."
  - id: D2
    description: "Measured ledger size (object count + byte total) confirmed independently against the artifact's manifest, falsifying or confirming the D-07 no-incremental-mode assumption"
    requirement: "OPS-01"
    verification:
      - kind: manual_procedural
        ref: "aws s3 ls --summarize --recursive s3://kmv-ledger-use1-adba57e4419be01f -> 28 objects / 3603201 bytes, matches manifest.json ledger.objectCount/byteTotal exactly"
        status: pass
    human_judgment: true
    rationale: "Independent live AWS measurement cross-checked by the operator against the artifact; not derivable from unit tests against fakes."
  - id: D3
    description: "kv restore --dry-run against the live stack resolves destinations from live terraform outputs (distinct from manifest provenance) and reports per-table write/skip counts with zero writes issued"
    requirement: "OPS-03"
    verification:
      - kind: manual_procedural
        ref: "kv/bin/kv restore ./backups/kmv-backup-20260820T144605Z.zip --dry-run -> exit 0, DRY RUN banner, PROVENANCE vs DESTINATIONS blocks, per-table write/skip counts; DynamoDB ItemCount unchanged before/after (8/769/57)"
        status: pass
    human_judgment: true
    rationale: "Requires live AWS DynamoDB item-count comparison before/after to prove no write occurred; not provable from fakes alone (this plan's stated purpose)."
  - id: D4
    description: "Deliberate byte-corruption of a real backup archive is refused before any write, naming the failing entry, proving the checksum-verification refusal path fires on real data"
    requirement: "OPS-02"
    verification:
      - kind: manual_procedural
        ref: "kv/bin/kv restore /tmp/corrupt.zip --dry-run -> exit 1, 'sha256 hash: zip: checksum error' naming dynamodb/auth-electro.jsonl"
        status: pass
    human_judgment: true
    rationale: "Live corruption test against a real artifact, operator-executed and operator-confirmed per the plan's checkpoint gate."
  - id: D5
    description: "Secret-leak audit: external/ssm-params.json in the real live artifact carries zero non-empty SecureString values"
    verification:
      - kind: manual_procedural
        ref: "unzip -p external/ssm-params.json — 74 params, 39 SecureString, all 39 valueOmitted:true, 0 with a non-empty value"
        status: pass
    human_judgment: false
  - id: D6
    description: "ROADMAP's Stage-A gate ('round-tripped once against real data') is explicitly released, unblocking Stage C (16-10 kv destroy)"
    verification:
      - kind: manual_procedural
        ref: "Both checkpoint tasks approved by operator; see Gate Verdict section below"
        status: pass
    human_judgment: true
    rationale: "The gate release itself is an explicit human judgment call recorded in this SUMMARY per the plan's <done> criterion."

duration: 24min (measured backup elapsed time; total operator-gate session longer)
completed: 2026-08-20
status: complete
---

# Phase 16 Plan 04: Live operator gate — kv backup + kv restore --dry-run against real AWS Summary

**`kv backup` and `kv restore --dry-run` both round-tripped against the real AWS account (052251888500) for the first time — verified backup artifact (6.2 MiB, 3 tables + 28-object/3.4 MiB ledger + secret-free external inventory), dry-run destination resolution matched live terraform outputs with zero writes, and the corrupt-archive refusal fired on a real byte-flipped artifact — releasing the ROADMAP's Stage-A gate for Stage C (kv destroy).**

## Performance

- **Duration:** kv backup elapsed 24.405s (measured); full operator-gate session (both tasks, cross-checks, corruption test) longer
- **Completed:** 2026-08-20T14:46:05Z (backup artifact timestamp)
- **Tasks:** 2 (both `checkpoint:human-verify`, both approved)
- **Files modified:** 1 (docs/ops/backup-restore.md — runbook gap fix)

## Accomplishments

**Task 1 — live backup and verification (PASS):**
- Ran `kv/bin/kv backup --out ./backups` against AWS account `052251888500` (assumed role `AWSReservedSSO_AdministratorAccess_.../whereiskurt@gmail.com`). Produced `backups/kmv-backup-20260820T144605Z.zip`, 6.2 MiB (6,490,166 bytes), 24.405s elapsed.
- Per-table row counts: `auth-authjs` 8, `auth-electro` 769, `voice-usage` 57. Ledger: 28 objects, 3.4 MiB.
- Verification (on by default) re-read the artifact and confirmed every SHA-256 and row count against `manifest.json` clean. Closing warning block present and correct (transcripts, user email addresses, unencrypted-by-design, "may be the only remaining copy"). No size warning fired (artifact is small).
- Independent pre-measurement (`aws s3 ls --summarize --recursive s3://kmv-ledger-use1-adba57e4419be01f`) reported 28 objects / 3,603,201 bytes — **matches the manifest's `ledger.objectCount`/`ledger.byteTotal` exactly**. `aws dynamodb describe-table ... ItemCount` pre-run also matched all three per-table row counts exactly (8/769/57).
- Archive contents (35 files): `manifest.json` + 3 × `dynamodb/*.jsonl` + 28 × `ledger/*` + 3 × `external/*` (`voipms-dids.json` 738 B, `nat-eip.txt` 14 B, `ssm-params.json` 16,560 B).
- `manifest.json` fields verified present and correct: `version` 1, `createdAt` 2026-08-20T14:46:05.630809Z, `gitSha` 76f29cb7f7805fb6ddbda83c5c0caa73b5ba19c9, `kvVersion` "dev", `awsAccountId` 052251888500, `region` us-east-1, per-table `{logical, tableName, path, rowCount}`, ledger `{bucketName, prefix, objectCount, byteTotal}`, `external[]`/`files[]` each with bytes + sha256.
- **Secret-leak audit** (beyond the plan's stated criteria): `external/ssm-params.json` holds 74 parameters, 39 of type `SecureString`; all 39 carry `valueOmitted: true` and **zero** have a non-empty `value` field. This independently confirms the design's "non-secret SSM params only" claim.
- `git status --porcelain` shows nothing under `backups/` (only pre-existing untracked `.vscode/` and `kv/kv`); both `backups/` and `kv/bin/` confirmed gitignored.

**Task 2 — live restore dry-run + corruption refusal (PASS):**
- `kv/bin/kv restore ./backups/kmv-backup-20260820T144605Z.zip --dry-run` exited 0 with a `DRY RUN` banner. PROVENANCE (manifest, audit-only, D-10) and DESTINATIONS (live-resolved from terraform outputs, D-10) blocks were distinctly labelled; they resolved to identical names since the stack has not been rebuilt (expected — the divergence case is what D-10 exists for and is not exercised by this gate).
- Per-table dry-run results:
  - `auth-authjs`: written=6, skipped=2 (next-auth verification token) — 6+2=8 ✓
  - `auth-electro`: written=655, skipped=4 (login intent), skipped=110 (oidc session/interaction state) — 655+4+110=769 ✓
  - `voice-usage`: written=57, no skips (no expired-TTL rows present at this time) — 57 ✓
- Ledger: 28 objects, 3.4 MiB — matches manifest.
- DynamoDB item counts after the dry-run: `kmv-auth-authjs` 8, `kmv-auth-electro` 769, `kmv-voice-usage` 57 — **unchanged**. Dry-run wrote nothing.
- **Corruption refusal test**: copied the archive to `/tmp/corrupt.zip`, flipped one byte at offset 1,000,000 (0x75 → 0x8a, inside the `auth-electro` payload). `kv/bin/kv restore /tmp/corrupt.zip --dry-run` exited 1, naming the failing entry explicitly:
  ```
  Error: open backup archive /tmp/corrupt.zip: refused (failed verification):
   backup archive /tmp/corrupt.zip: hash entry dynamodb/auth-electro.jsonl:
   sha256 hash: zip: checksum error
  ```

**Runbook gap fixed in-scope:** `docs/ops/backup-restore.md` now documents that `infra/.envrc` must be sourced (`set -a && . infra/.envrc && set +a`) before running `kv backup`/`kv restore`, since live target resolution goes through `terragrunt output`, whose S3 backend config lives in that file. Without it, the commands fail with a terraform backend error that doesn't obviously point at the missing env — the operator hit this verbatim during this gate.

## Gate Verdict

**PASS — Stage A has round-tripped once against real data. The ROADMAP's Stage-A gate is explicitly RELEASED. Stage C (16-10 `kv destroy`) is unblocked.**

## Task Commits

This plan is a live operator-verification checkpoint, not code work — the only code-adjacent change is the runbook doc fix, committed separately from this SUMMARY:

1. **Runbook gap fix (infra/.envrc prerequisite)** - `1d08a0f` (docs)

**Plan metadata:** pending (this commit)

## Files Created/Modified
- `docs/ops/backup-restore.md` - added a "Prerequisite: source infra/.envrc before running either command" section documenting the terragrunt backend env-var requirement discovered during this gate

## Decisions Made
- No source-code changes made in this plan by design — it is purely a live-verification checkpoint (D-32 prohibits any destructive command; `kv restore` is invoked only with `--dry-run`).
- The `infra/.envrc` runbook gap was fixed in-scope as a small, low-risk doc edit rather than deferred, since it's exactly the kind of friction an operator following this same runbook would hit verbatim next time.
- The `kvVersion: "dev"` limitation (local `go build` carries no version ldflags, so the manifest can't identify which kv build produced an artifact in a real disaster-recovery scenario) is recorded as a known limitation / possible follow-up, not fixed here — out of scope for a verification-only checkpoint.

## Deviations from Plan

None — plan executed exactly as written. Both checkpoint tasks were operator-gated and approved with real measured values; no auto-fix rules were invoked since no source code was touched.

## Issues Encountered

- **Environment gap (documented, not a plan deviation):** `terragrunt output` failed initially with a `bucket = "" ... cannot be empty` backend error until `infra/.envrc` was sourced (`set -a && . infra/.envrc && set +a`). Root-caused to the terragrunt S3 backend config depending on `TG_BUCKET_USE1`/`TG_TABLE_USE1` env vars from that file, not on anything `kv` itself reads. Documented in `docs/ops/backup-restore.md` (see above).
- **`kvVersion` observation:** recorded as `"dev"` in the manifest because the local `go build` carries no version ldflags. In a real disaster-recovery scenario the manifest would not identify which `kv` build produced the artifact. Known limitation, not fixed in this plan.
- **Operator follow-up still outstanding:** `/tmp/corrupt.zip` (6.4 MB — a byte-flipped copy of a real backup containing real transcripts and user email addresses) could **not** be deleted during this session — a shell hook in the execution environment blocks `rm`. This file remains on the operator's `/tmp` and should be deleted manually as a follow-up.

## User Setup Required

None beyond what's now documented — the `infra/.envrc` sourcing step above is the one prerequisite an operator needs, and it's now captured in the runbook.

## Next Phase Readiness

- Stage A (`kv backup`/`kv restore`) is proven end-to-end against real AWS data, not just in-memory fakes. The ROADMAP's explicit gate before trusting the destroy stage is satisfied.
- 16-10 (`kv destroy --with-backup`) can proceed — its abort-on-backup-mismatch gate now rests on a verification path proven against a real, deliberately-corrupted artifact, not only unit fixtures.
- Plans 05-09 (pause/resume) and 16-10 (destroy --with-backup) remain in Phase 16.
- **Outstanding operator follow-up (not blocking):** delete `/tmp/corrupt.zip` manually — the execution environment's `rm`-blocking hook prevented automated cleanup during this session.

---
*Phase: 16-operator-lifecycle-kv-backup-restore-pause-resume-teardown*
*Completed: 2026-08-20*

## Self-Check: PASSED

Modified file `docs/ops/backup-restore.md` confirmed present on disk with the new prerequisite section. Commit `1d08a0f` confirmed present in `git log --oneline`.
