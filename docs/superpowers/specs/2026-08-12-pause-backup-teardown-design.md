# klanker-voice: Pause, Backup, and Teardown

**Status:** Approved (brainstormed and validated with operator, 2026-08-12)
**Scope:** Three new `kv` command groups — `backup`/`restore`, `pause`/`resume`, `destroy`
**Supersedes:** nothing
**Related:** `docs/superpowers/specs/2026-07-04-klanker-voice-design.md` (authoritative design)

---

## 1. Problem

The stack runs ~$190/mo of AWS plus a $99/mo ElevenLabs Pro subscription, continuously,
whether or not anyone is talking to it. The project is conference-shaped: it needs to be
hot for DEF CON and demos, and idle for long stretches between. There is currently no way
to stand it down and bring it back.

Two distinct needs:

- **Pause** — weeks-scale, repeatable, low-friction. Stop paying for compute; keep the
  domains resolving, the phone numbers provisioned, and all data intact.
- **Teardown** — eventual, possibly permanent. Destroy the AWS footprint entirely, with
  a data export good enough to rebuild from, accepting that the DIDs are released and
  gone.

Both depend on a backup that is real rather than reassuring, so backup is specified and
built first.

### Goals

- `kv pause` / `kv resume` round-trips the stack in ~5 minutes each way, ~$190 → ~$60/mo.
- `kv backup` produces a single self-contained artifact capturing everything that exists
  only in AWS.
- `kv restore` can repopulate a freshly-applied stack from that artifact.
- `kv destroy --with-backup` tears the account down without silently eating data.
- No command can destroy something irreversible without an explicit, deliberate action.

### Non-goals

- Pausing or downgrading the ElevenLabs subscription (manual, vendor console — but it is
  the single largest line item during a long pause, so the commands should say so).
- Releasing DIDs (manual, VoIP.ms-side, irreversible — see §6.4).
- A maintenance page for `voice.klankermaker.ai` during a pause (see §5.5).

---

## 2. Risk inventory

This table is the foundation of the whole design: it is what decides what `kv backup`
must capture. Established by direct inspection of the repo and the terraform modules on
2026-08-12.

| Category | Lives in | On pause | On destroy |
|---|---|---|---|
| Infra config (`site.hcl`, service `.hcl`, module code) | **git** | untouched | safe |
| Studio operator config (`apps/voice/configs/studio/dids.yaml`, `rule-order.yaml`) | **git** | untouched | safe |
| Secrets (`.secrets.sops.json`, SOPS/KMS-encrypted) | **git** | untouched | safe (but see §4.5) |
| Audio assets — `client/public/greetings/greeting-1.mp3`, `telephony/kph-hey.wav`, ambience beds | **git** | untouched | safe |
| Container images | ECR, built from git | untouched | rebuild via CI |
| Route53 sub-zone + NS delegation in parent | terraform | untouched | **auto-recreated; registrar never involved** |
| ACM certs | terraform | untouched | auto re-validated via DNS |
| ALB, CloudFront distribution | terraform | untouched | recreated (CloudFront ~20 min each way) |
| **DynamoDB rows** — access codes, tiers, users, usage, kill-switch | **AWS only** | untouched | **backup or lose** |
| **S3 ledger** — Phase 15 transcripts | **AWS only** | untouched | **backup or lose** |
| NAT EIP | AWS only | **preserved** | new IP → re-allowlist at VoIP.ms |
| Bucket names (`random_id` suffix) | AWS only | unchanged | **new names on recreate** |
| **The DIDs** | VoIP.ms | provisioned, fast busy | **released manually — gone forever** |

### 2.1 Findings that shaped the design

- **`greeting-1.mp3` is committed.** The hand-spliced take (PR #31) is in git, not only in
  S3. It does not need backing up, and a restore does not risk it.
- **The Route53 parent zone is a `data` source.** `modules/site/v1.0.0/route35.tf:1` reads
  the management-account zone; the sub-zone and its NS delegation record are both
  terraform-managed. A destroy/recreate fixes delegation automatically — **no registrar
  step**, which was the single scariest unknown going in.
- **The ledger bucket has no `force_destroy`** (`modules/ledger/v1.0.0/main.tf:13`), unlike
  cf-assets (`force_destroy = var.force_destroy`) and the ALB log bucket
  (`logs_force_destroy = true`). `terraform destroy` will *fail* on a non-empty ledger.
  This is a good accident — transcripts cannot be silently eaten — and `kv destroy` must
  handle it deliberately rather than trip over it.
- **`vpc_endpoints.enabled = false`** and **`vpc_flow_logs.enabled = false`**
  (`network.hcl:41,48`). Six interface endpoints would have been ~$44/mo; they are off.
  The ~$60/mo paused floor is therefore correct.
- **`ecs-service` has no `lifecycle { ignore_changes = [desired_count] }`**
  (`modules/ecs-service/v1.0.0/main.tf:246`). Terraform owns the count, which makes a
  git-tracked flag the only durable pause mechanism (§5.1).
- **`aws_appautoscaling_target` sets `min_capacity = 1`** for voice and auth
  (`main.tf:313`); telephony-edge has `autoscaling.enabled = false`. Application Auto
  Scaling *enforces* MinCapacity, so `desired_count = 0` alone bounces back (§5.2).

---

## 3. Build order

Each lands independently useful:

1. **`kv backup` / `kv restore`** — foundation; prerequisite for #3; run before every pause.
2. **`kv pause` / `kv resume`** — the immediate need.
3. **`kv destroy --with-backup`** — teardown.

Backup is designed for the **destroy** case from day one, not the pause case. A backup
built only to survive a scale-to-zero would capture a subset and quietly fail on the day
it actually matters.

---

## 4. `kv backup` / `kv restore`

### 4.1 Interface

```
kv backup  [--out ./backups] [--no-verify]
kv restore <zip> [--tables] [--ledger] [--dry-run] [--skip-ephemeral]
```

### 4.2 Artifact layout

One self-contained timestamped zip:

```
kmv-backup-2026-08-12T1430Z.zip
├── manifest.json
├── dynamodb/
│   ├── kmv-auth-electro.jsonl     # access codes, tiers, OIDC adapter, auth profiles
│   ├── kmv-auth-authjs.jsonl      # users, accounts, sessions, verification tokens
│   └── kmv-voice-usage.jsonl      # daily usage, rollup, kill-switch control, leases
├── ledger/                        # full transcript object tree, key-preserving
└── external/
    ├── voipms-dids.json           # DID inventory, routing, sub-account mapping
    ├── nat-eip.txt                # the VoIP.ms-allowlisted egress IP
    └── ssm-params.json            # non-secret runtime parameters
```

`manifest.json` records: git SHA at backup time, AWS account id, region, `kv` version,
resolved table and bucket names, per-table row counts, ledger object count and byte total,
and a SHA-256 per file.

### 4.3 The ledger is always included

Per operator decision. `kv backup` prints size and elapsed time so a slow backup is never
a mystery, and warns above a size threshold. The ledger is append-only with a lifecycle
configuration, so growth is bounded.

### 4.4 Scan, not native export

Plain DynamoDB `Scan` → JSONL rather than `ExportTableToPointInTime`. Native export
requires PITR and writes its output **to an S3 bucket inside the account being
destroyed**, which is backwards for this use case. At these table sizes a scan is faster,
and the output is portable, greppable, diffable, and restorable with no cloud dependency.

### 4.5 Encryption: deliberately none

The zip is written **unencrypted**, with a loud closing warning naming what it contains
(transcripts, user email addresses) and stating that it may be the only remaining copy.

Explicitly **do not** encrypt with the SOPS/KMS key: that key lives in the account being
destroyed, which would make the backup unopenable at exactly the moment it is needed. If
encryption is added later it must use a key held outside the account (age or GPG).

### 4.6 Verification

`--verify` is on by default: after writing, re-open the zip and check every row count and
SHA-256 against the manifest. "Backup succeeded" must mean the artifact was read back.

### 4.7 Restore hazards

Three failure modes, designed for explicitly:

1. **Resolve targets live.** Table and bucket names come from current terraform outputs at
   restore time, never from the manifest — post-destroy buckets carry a new `random_id`
   suffix. The manifest's copies exist to audit *where the backup came from*, and restore
   must never read them as destinations.
2. **Filter ephemeral rows by default** (`--skip-ephemeral`, on by default). Concurrency
   leases, OIDC session state, and expired-TTL items must not be restored. Restoring a
   stale concurrency lease would wedge the quota gate — a bug this project has already
   debugged once.
3. **Idempotent and resumable.** Batched `PutItem` with retry and backoff; safe to re-run
   over a partially-completed restore.

`--dry-run` reports per-table write counts and ledger object counts without writing.

### 4.8 Restore ordering

Config (git) → `terragrunt apply` → `kv restore`. Restore assumes the target tables and
buckets already exist; it does not create infrastructure.

---

## 5. `kv pause` / `kv resume`

### 5.1 Mechanism: one flag in `site.hcl`

```hcl
# Operator pause switch (kv pause / kv resume — avoid editing by hand).
# true => every ECS service runs zero tasks. Nothing else changes: VPC,
# NAT+EIP, ALB, WAF, CloudFront, Route53, ACM, DynamoDB, the S3 ledger and
# ECR all stay put — which is what keeps the VoIP.ms-allowlisted NAT EIP
# alive and makes resume a pure scale-up.
paused = false

ecs_services = {
  enabled = true
  services = [
    for s in [voice, auth, telephony_edge] :
    local.paused ? merge(s, {
      desired_count = 0
      autoscaling   = merge(s.autoscaling, { min_capacity = 0 })
    }) : s
  ]
}
```

`ecs_tasks` stays enabled — task definitions cost nothing and staying registered means
resume re-registers nothing.

**Both overrides are required.** `desired_count = 0` alone is undone by Application Auto
Scaling enforcing `min_capacity = 1`; `min_capacity = 0` alone changes nothing. The module
accepts 0 — `min_capacity = optional(number, 1)` carries no positive-value validation.

### 5.2 The ordering hazard

`aws_appautoscaling_target` declares `depends_on = [aws_ecs_service.service]`
(`main.tf:315`). On a **pause** apply terraform therefore sets `desired_count → 0` first
and lowers `min_capacity → 0` second. In that window the scalable target still has
`min = 1`, and Application Auto Scaling scales out to MinCapacity whenever current
capacity is below it. If it fires there, the result is a service pinned at 1 task that
terraform believes is at 0 — a silent failed pause.

The window is seconds and Application Auto Scaling evaluates on roughly a minute's
cadence, so it usually will not fire. "Usually" is not an acceptable property for an
operator tool, so **the verification step closes it deterministically**: if the service
has not reached zero, `kv` issues `update-service --desired-count 0` itself. Terraform
already records 0, so the correction introduces no drift.

Resume has no equivalent hazard — the same ordering works in its favour.

### 5.3 Command flow

```
kv pause  [--yes] [--reason "off-season"]
kv resume [--yes]
kv pause status
```

`kv pause`:

1. **Preflight** — clean working tree, on `main`, synced with origin, valid `gh` auth.
   Refuse otherwise rather than commit onto a surprise.
2. **Idempotent read** — already paused? print status, exit 0.
3. **Rewrite + confirm** — flip the boolean, show the diff, confirm (`--yes` skips).
4. **Commit + push to `main`** — `ops(infra): pause ECS services (desired_count=0)`.
5. **Dispatch** — `gh workflow run terragrunt-apply.yml --ref main -f modules=ecs-service`,
   resolve the run id, stream to completion. The `terraform-apply` environment's
   required-reviewer rule still gates the apply.
6. **Verify** — poll `ecs describe-services` for all three services until running and
   desired are both 0, applying the §5.2 correction if needed. ~10 min timeout.
7. **Report** — resulting cost posture and the manual follow-ups (§5.6).

`kv resume` mirrors it and additionally waits for ALB target groups to report healthy for
voice and auth. A clean apply that never reaches healthy is exactly the failure worth
catching, so resume does not report success until targets are in service.

### 5.4 Graceful drain

Voice tasks hold ECS task-scale-in protection while a session is live, so a pause issued
during a call waits for that protection to expire. `kv` must print
`waiting for N in-flight session(s) to drain` rather than appear hung.

### 5.5 Behaviour while paused

- `voice.klankermaker.ai` **still loads** — it serves from CloudFront/S3 after the
  front-end cutover, so the page and its OG unfurl work. Only the mic tap fails
  (`/api/offer` → ALB → empty target group → 503).
- `auth.klankermaker.ai` is ALB-only and returns 503.
- The DIDs stay provisioned and billed by VoIP.ms, but telephony-edge's outbound SIP
  registration drops, so the sub-account reads as offline and callers get fast busy.
  A VoIP.ms-side failover recording would be a nicer experience; it is a vendor-console
  change, out of scope here.

A maintenance page is deliberately **not** in scope. If wanted later it belongs in the
CloudFront/S3 layer, independent of this mechanism.

### 5.6 CI interaction — config is the guard

No extra workflow changes. Because the pause lives in `site.hcl`, a mid-pause deploy
re-applies `desired_count = 0` and the stack stays paused. Correct by construction. A
build still runs and pushes an image to ECR; it simply deploys to a sleeping service.

### 5.7 Kill-switch is untouched

`kv pause` does **not** flip the kill-switch. They are orthogonal — the kill-switch gates
new voice sessions at the application layer, pause removes the compute — and coupling them
would produce a surprise on resume. `kv killswitch` remains the instant, zero-risk way to
stop metered API burn without touching AWS.

---

## 6. `kv destroy --with-backup`

### 6.1 Interface

```
kv destroy [--with-backup] [--no-backup] [--dry-run]
```

`--with-backup` is the default. `--no-backup` requires typing the site label to confirm.

### 6.2 Sequence

1. **Backup** (mandatory unless explicitly waived) — full `kv backup`, ledger included.
2. **Verify the zip** — row counts and checksums, per §4.6. Abort on mismatch.
3. **Drain** — scale services to zero and wait, reusing the `kv pause` path.
4. **Empty the ledger bucket explicitly** — the bucket has no `force_destroy`, so this is
   a required, deliberate step rather than a surprise mid-destroy failure.
5. **`terragrunt destroy` in dependency order** — ecs-service → ecs-task → ecs-cluster →
   ledger → network → certs/site.
6. **Report** — what survived, what is now unrecoverable, and the manual follow-ups.

### 6.3 What the final report must state

- The backup path, its size, and that it may be the only copy.
- That the NAT EIP is gone and VoIP.ms API allowlisting will need the new one on rebuild.
- That bucket names will differ on recreate.
- That the DIDs are **still provisioned and still billing** unless released manually.

### 6.4 DID release stays manual

Releasing DIDs is irreversible, VoIP.ms-side, and must never be one flag away from a typo.
`725-404-8283` ("U-CTF") and the toll-free `855-916-INFO` cannot be recovered once
released — someone else can buy them the next day. `kv destroy` reports the inventory and
stops there.

---

## 7. Cross-cutting

### 7.1 CLI conventions

New commands follow existing `kv` structure: one file per command group under
`kv/internal/app/cmd/`, registered in `root.go` alongside `NewKillswitchCmd` etc. AWS
clients, `gh`, and `git` go behind narrow interfaces so orchestration is testable without
touching the cloud — matching how `knowledge.go` and `studio/sop_git.go` already isolate
their shell-outs.

### 7.2 Testing

- Table tests for the `site.hcl` rewrite: idempotence, comment preservation, already-paused,
  malformed input.
- Backup/restore round-trip against local DynamoDB and a MinIO/fake S3, asserting
  ephemeral-row filtering and checksum verification.
- `--dry-run` coverage on both destructive paths.
- Preflight-refusal tests: dirty tree, wrong branch, stale origin.

### 7.3 Security and privacy

- The backup zip contains personal data (transcripts, user emails). It is unencrypted by
  design (§4.5) and the command says so loudly. It must never be committed to the repo —
  add `backups/` to `.gitignore`.
- No secret values are read or written by these commands; secrets remain in SOPS and SSM.

---

## 8. Cost outcome

| | Running | Paused | Destroyed |
|---|---|---|---|
| Fargate (3.5 vCPU / 7 GB) | ~$126 | $0 | $0 |
| NAT + ALB + WAF + misc | ~$60 | ~$60 | $0 |
| **AWS total** | **~$190/mo** | **~$60/mo** | ~$1/mo |
| ElevenLabs Pro (manual) | $99/mo | $99/mo | $99/mo |
| Round trip | — | ~5 min each way | hours + manual steps |
| DIDs | live | provisioned, fast busy | released manually, gone |

During a long pause, the ElevenLabs subscription becomes the largest remaining line item.
`kv pause` should say so in its completion output.

---

## 9. Decisions log

| Decision | Choice | Rationale |
|---|---|---|
| Pause depth | Scale-to-zero | Preserves the NAT EIP and the VoIP.ms allowlist; ~$130/mo saved for a 5-minute round trip |
| Pause granularity | Single boolean, all-or-nothing | Simplest config; per-service was offered and declined |
| Pause mechanism | Git-tracked flag in `site.hcl` | Terraform owns `desired_count`; a raw API call is transient |
| Automation level | Commit + push + dispatch CI | Hands-off; still human-gated by the apply environment's reviewer rule |
| Branch flow | Commit to `main`, then dispatch | Keeps `main` == live infra truth; sole operator, two-line diff |
| Verification | Watch run, then verify task count | Also closes the §5.2 autoscaling ordering hazard |
| CI guard | Config is the guard | Correct by construction; no extra workflow surface |
| Ledger in backup | Always included | One artifact, no partial backups possible |
| Backup encryption | Plain zip + loud warning | A key inside the destroyed account is worse than no key |
| Spec structure | One spec, all three commands | Operator wanted the full picture on paper now |
| DID release | Manual, outside the tool | Irreversible and vendor-side |

---

## 10. Open items

- Ledger size is unmeasured (no valid AWS credentials at spec time). `kv backup` reports
  it; check ahead of time with
  `aws s3 ls --summarize --recursive s3://kmv-ledger-use1-*`.
  **Resolved 2026-08-19 (operator directive):** proceed on the assumption that the ledger
  is reasonably small. §4.3's always-include-the-ledger decision stands as written, with
  no size threshold, incremental mode, or streaming design in scope. `kv backup` still
  reports size and elapsed time and warns above a threshold, so the day the assumption
  stops holding is visible rather than silent.
- Whether to add a paused-state maintenance page for `auth.klankermaker.ai` (503 today).
  Deferred — separate concern from this mechanism.
- Whether a VoIP.ms failover recording should cover paused DIDs. Vendor-console change,
  operator's call.
