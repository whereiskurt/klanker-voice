# Phase 16: Operator Lifecycle — kv backup/restore, pause/resume, teardown - Context

**Gathered:** 2026-08-19
**Status:** Ready for planning
**Source:** PRD Express Path (`docs/superpowers/specs/2026-08-12-pause-backup-teardown-design.md`)

The spec was brainstormed and operator-approved on 2026-08-12, and every infrastructure
claim in it was verified against the live terraform modules at authoring time (file:line
citations are in the spec's §2.1). Treat the spec as locked: the decisions below are
transcribed from it, not re-derived.

<domain>
## Phase Boundary

**In scope** — three new `kv` command groups, built in this order:

1. `kv backup` / `kv restore` — a verified, self-contained artifact of everything that
   exists only in AWS.
2. `kv pause` / `kv resume` — scale every ECS service to zero and back, ~$190 → ~$60/mo,
   ~5 minutes each way.
3. `kv destroy --with-backup` — tear the AWS footprint down without silently eating data.

**Out of scope** — pausing or downgrading the ElevenLabs Pro subscription (vendor console,
manual; but the largest remaining line item during a long pause, so the commands must say
so); releasing DIDs (VoIP.ms-side, irreversible, manual); a maintenance page for
`voice.klankermaker.ai` or `auth.klankermaker.ai` while paused; backup encryption; any
ledger size threshold, incremental mode, or streaming backup design.

**Branch:** all work lands on `feat/pause-backup-teardown`, which already carries the spec
commits. The branch stays isolated from `main` until the phase is done — this is deliberate
(single operator, not production-critical, one large branch by operator decision
2026-08-19).

</domain>

<decisions>
## Implementation Decisions

Every decision below is transcribed from the approved spec. Cite the D-ID in the relevant
plan's `must_haves` so nothing is silently dropped in translation.

### Build order and framing

- **D-01**: Build in spec order — backup/restore, then pause/resume, then destroy. Each
  wave lands independently useful and execution may stop after any wave.
- **D-02**: Backup is designed for the **destroy** case from day one, not the pause case.
  A backup built only to survive a scale-to-zero would capture a subset and quietly fail
  on the day it actually matters.

### `kv backup` / `kv restore` (OPS-01, OPS-02, OPS-03)

- **D-03**: Interface is `kv backup [--out ./backups] [--no-verify]` and
  `kv restore <zip> [--tables] [--ledger] [--dry-run] [--skip-ephemeral]`.
- **D-04**: One self-contained timestamped zip (`kmv-backup-<ISO8601>.zip`) with the
  spec §4.2 layout: `manifest.json`, `dynamodb/{kmv-auth-electro,kmv-auth-authjs,kmv-voice-usage}.jsonl`,
  `ledger/` (key-preserving object tree), `external/{voipms-dids.json,nat-eip.txt,ssm-params.json}`.
- **D-05**: `manifest.json` records git SHA at backup time, AWS account id, region, `kv`
  version, resolved table and bucket names, per-table row counts, ledger object count and
  byte total, and a SHA-256 per file.
- **D-06**: DynamoDB is captured with a plain `Scan` → JSONL, **not**
  `ExportTableToPointInTime` — native export needs PITR and writes its output to an S3
  bucket *inside the account being destroyed*, which is backwards here. JSONL is portable,
  greppable, diffable, and restorable with no cloud dependency.
- **D-07**: The ledger is **always** included — no partial backups are possible. `kv backup`
  prints size and elapsed time so a slow backup is never a mystery, and warns above a size
  threshold. (Operator directive 2026-08-19: assume the ledger is reasonably small; no
  incremental or streaming design in scope.)
- **D-08**: The zip is written **unencrypted**, with a loud closing warning naming what it
  contains (transcripts, user email addresses) and stating it may be the only remaining
  copy. Explicitly do **not** encrypt with the SOPS/KMS key — that key lives in the account
  being destroyed, which would make the backup unopenable at exactly the moment it is
  needed. Any future encryption must use a key held outside the account (age or GPG).
- **D-09**: Verification is **on by default** (`--no-verify` opts out): after writing,
  re-open the zip and check every row count and SHA-256 against the manifest. "Backup
  succeeded" must mean the artifact was read back.
- **D-10**: Restore resolves table and bucket names from **current terraform outputs at
  restore time**, never from the manifest — post-destroy buckets carry a new `random_id`
  suffix. The manifest's copies exist to audit *where the backup came from*; restore must
  never read them as destinations.
- **D-11**: Restore filters ephemeral rows by default (`--skip-ephemeral`, on by default):
  concurrency leases, OIDC session state, and expired-TTL items. Restoring a stale
  concurrency lease would wedge the quota gate — a bug this project has already debugged
  once.
- **D-12**: Restore is idempotent and resumable — batched `PutItem` with retry and backoff,
  safe to re-run over a partially-completed restore. `--dry-run` reports per-table write
  counts and ledger object counts without writing.
- **D-13**: Restore assumes target tables and buckets already exist; it does not create
  infrastructure. Documented ordering is config (git) → `terragrunt apply` → `kv restore`.
- **D-14**: `backups/` is added to `.gitignore` — the artifact holds personal data and must
  never be committed. No secret values are read or written by any of these commands;
  secrets stay in SOPS and SSM.

### `kv pause` / `kv resume` (OPS-04, OPS-05)

- **D-15**: The pause mechanism is a single git-tracked boolean `paused` in
  `infra/terraform/live/site/site.hcl`, all-or-nothing across voice, auth, and
  telephony-edge. Per-service granularity was offered and declined. A raw AWS API call is
  not acceptable: `ecs-service` has no `lifecycle { ignore_changes = [desired_count] }`, so
  terraform owns the count and would revert it on the next CI deploy.
- **D-16**: Pause must override **both** `desired_count = 0` **and**
  `autoscaling.min_capacity = 0`. Either alone fails — Application Auto Scaling enforces
  `min_capacity = 1` and bounces the service back. The module accepts 0
  (`min_capacity = optional(number, 1)` carries no positive-value validation).
- **D-17**: `ecs_tasks` stays enabled while paused — task definitions cost nothing and
  staying registered means resume re-registers nothing.
- **D-18**: `kv pause` preflight refuses rather than committing onto a surprise: clean
  working tree, on `main`, synced with origin, valid `gh` auth. It is idempotent — already
  paused prints status and exits 0 — and it shows the diff and confirms before committing
  (`--yes` skips the prompt).
- **D-19**: The command commits to `main` (`ops(infra): pause ECS services (desired_count=0)`),
  pushes, then dispatches `gh workflow run terragrunt-apply.yml --ref main -f modules=ecs-service`,
  resolves the run id, and streams to completion. The `terraform-apply` environment's
  required-reviewer rule still gates the apply.
- **D-20**: Verification closes the §5.2 apply-ordering hazard deterministically. Because
  `aws_appautoscaling_target` declares `depends_on = [aws_ecs_service.service]`, a pause
  apply sets `desired_count → 0` first and lowers `min_capacity → 0` second; in that window
  Application Auto Scaling can scale back out to MinCapacity, leaving a service pinned at 1
  task that terraform believes is at 0. `kv` polls `ecs describe-services` for all three
  services until running and desired are both 0 (~10 min timeout) and, if a service has not
  reached zero, issues `update-service --desired-count 0` itself. Terraform already records
  0, so the correction introduces no drift. "Usually it won't fire" is not an acceptable
  property for an operator tool.
- **D-21**: Voice tasks hold ECS task-scale-in protection while a session is live, so a
  pause issued during a call waits. `kv` must print
  `waiting for N in-flight session(s) to drain` rather than appear hung.
- **D-22**: `kv resume` mirrors pause and additionally waits for the voice and auth ALB
  target groups to report healthy. A clean apply that never reaches healthy is exactly the
  failure worth catching, so resume does not report success until targets are in service.
- **D-23**: No CI workflow changes — the config is the guard. Because the pause lives in
  `site.hcl`, a mid-pause deploy re-applies `desired_count = 0` and the stack stays paused.
  A build still runs and pushes an image to ECR; it simply deploys to a sleeping service.
- **D-24**: `kv pause` does **not** flip the kill-switch. They are orthogonal — the
  kill-switch gates new voice sessions at the application layer, pause removes the compute —
  and coupling them would produce a surprise on resume.
- **D-25**: Pause reports the resulting cost posture and the manual follow-ups, explicitly
  naming ElevenLabs Pro ($99/mo) as the largest remaining line item during a long pause.
  Paused behaviour is documented, not fixed: `voice.klankermaker.ai` still loads from
  CloudFront/S3 (only the mic tap 503s), `auth.klankermaker.ai` is ALB-only and 503s, and
  DIDs stay provisioned but give callers fast busy once SIP registration drops.

### `kv destroy --with-backup` (OPS-06)

- **D-26**: Interface is `kv destroy [--with-backup] [--no-backup] [--dry-run]`.
  `--with-backup` is the default; `--no-backup` requires typing the site label to confirm.
- **D-27**: Sequence is: full backup (ledger included) → verify the zip's row counts and
  checksums and **abort on mismatch** → drain to zero reusing the `kv pause` path → empty
  the ledger bucket **explicitly** (it has no `force_destroy`, unlike cf-assets and the ALB
  log bucket, so this is a required deliberate step rather than a surprise mid-destroy
  failure) → `terragrunt destroy` in dependency order (ecs-service → ecs-task → ecs-cluster
  → ledger → network → certs/site) → report.
- **D-28**: The final report must state the backup path, its size, and that it may be the
  only copy; that the NAT EIP is gone and VoIP.ms API allowlisting will need the new one on
  rebuild; that bucket names will differ on recreate; and that the DIDs are still
  provisioned and still billing unless released manually.
- **D-29**: DID release stays manual and outside the tool. It is irreversible and
  vendor-side, and must never be one flag away from a typo — `725-404-8283` ("U-CTF") and
  the toll-free `855-916-INFO` cannot be recovered once released. `kv destroy` reports the
  inventory and stops there.

### Cross-cutting

- **D-30**: New commands follow existing `kv` structure — one file per command group under
  `kv/internal/app/cmd/`, registered in `root.go` alongside `NewKillswitchCmd` etc. AWS
  clients, `gh`, and `git` go behind narrow interfaces so orchestration is testable without
  touching the cloud, matching how `knowledge.go` and `studio/sop_git.go` already isolate
  their shell-outs.
- **D-31**: Testing must cover: table tests for the `site.hcl` rewrite (idempotence,
  comment preservation, already-paused, malformed input); a backup/restore round-trip
  against local DynamoDB and a MinIO/fake S3, asserting ephemeral-row filtering and
  checksum verification; `--dry-run` coverage on both destructive paths; and
  preflight-refusal tests (dirty tree, wrong branch, stale origin).
- **D-32**: No command may destroy something irreversible without an explicit, deliberate
  action.

### Claude's Discretion

- Go package layout beneath the command files, interface names, and struct shapes.
- Concrete zip/JSONL encoding libraries and the local-fake choice for tests
  (DynamoDB Local vs a hand-rolled fake; MinIO vs an S3 stub) — the spec names them as
  examples, not requirements.
- The exact backup size-warning threshold and the wording of warnings and reports, as long
  as they say what D-08 / D-25 / D-28 require.
- Polling intervals and backoff curves, within the stated ~10 min verification timeout.
- Whether pause/resume share an internal orchestration helper with destroy's drain step
  (the spec requires reuse of the *path*, not a specific factoring).

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### The design contract
- `docs/superpowers/specs/2026-08-12-pause-backup-teardown-design.md` — the approved spec.
  §2 risk inventory, §2.1 verified findings, §4 backup/restore, §5 pause/resume,
  §6 destroy, §7 cross-cutting, §9 decisions log, §10 open items.

### Infrastructure the commands manipulate or must not break
- `infra/terraform/live/site/site.hcl` — where the `paused` flag lives; `ecs_services`
  is built here.
- `infra/terraform/modules/ecs-service/v1.0.0/main.tf` — `desired_count` has no
  `ignore_changes` (:246); `aws_appautoscaling_target` sets `min_capacity = 1` (:313) and
  declares `depends_on = [aws_ecs_service.service]` (:315) — the ordering hazard.
- `infra/terraform/modules/ledger/v1.0.0/main.tf` — the ledger bucket has no
  `force_destroy` (:13); a `terraform destroy` will fail on a non-empty ledger.
- `infra/terraform/modules/site/v1.0.0/route35.tf` — the Route53 parent zone is a `data`
  source (:1), so a destroy/recreate fixes NS delegation automatically and never involves
  the registrar.
- `.github/workflows/terragrunt-apply.yml` — the workflow `kv pause` dispatches; the
  `terraform-apply` environment's required-reviewer rule gates the apply.

### `kv` CLI patterns to match
- `kv/internal/app/cmd/root.go` — command registration.
- `kv/internal/app/cmd/killswitch.go` — the closest analog: an operator command with a
  confirmation posture.
- `kv/internal/app/cmd/knowledge.go` and `kv/internal/app/cmd/studio/sop_git.go` — how
  existing code isolates shell-outs behind narrow interfaces for testability.
- `kv/internal/app/cmd/voipms.go` — the VoIP.ms API client the DID inventory export reuses
  (and its cred-leak invariant: nothing may log params or URLs).

</canonical_refs>

<specifics>
## Specific Ideas

- Cost outcome the phase is judged against: ~$190/mo running → ~$60/mo paused → ~$1/mo
  destroyed, with a ~5-minute round trip each way. The ~$60 paused floor is only correct
  because `vpc_endpoints.enabled = false` and `vpc_flow_logs.enabled = false`
  (`network.hcl:41,48`); six interface endpoints would have added ~$44/mo.
- `client/public/greetings/greeting-1.mp3` (the hand-spliced take from PR #31),
  `telephony/kph-hey.wav`, and the ambience beds are already committed to git. They do not
  need backing up and a restore does not risk them.
- The three DynamoDB tables are `kmv-auth-electro` (access codes, tiers, OIDC adapter,
  auth profiles), `kmv-auth-authjs` (users, accounts, sessions, verification tokens), and
  `kmv-voice-usage` (daily usage, rollup, kill-switch control, leases).
- The NAT EIP is the address allowlisted at VoIP.ms. Pause preserves it — that is the main
  reason pause is scale-to-zero rather than a deeper teardown.

</specifics>

<deferred>
## Deferred Ideas

- Backup encryption with an out-of-account key (age or GPG) — explicitly future work; the
  wrong answer today is a key inside the account being destroyed.
- A maintenance page for `auth.klankermaker.ai` (503 while paused) — belongs in the
  CloudFront/S3 layer, independent of this mechanism.
- A VoIP.ms-side failover recording so paused DIDs give callers something better than fast
  busy — vendor-console change, operator's call.
- Automating the ElevenLabs Pro subscription pause — vendor console only.
- `kv sessions` / live session inspection (already deferred as KV-06).

</deferred>

---

*Phase: 16-operator-lifecycle-kv-backup-restore-pause-resume-teardown*
*Context gathered: 2026-08-19 via PRD Express Path from the approved design spec*
