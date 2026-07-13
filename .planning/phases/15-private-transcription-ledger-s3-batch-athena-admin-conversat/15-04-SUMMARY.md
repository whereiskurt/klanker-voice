---
phase: 15-private-transcription-ledger-s3-batch-athena-admin-conversat
plan: 04
subsystem: infra
tags: [terraform, terragrunt, s3, athena, glue, iam, sops, ssm, ecs]

requires:
  - phase: 15-02
    provides: "ledger.LEDGER_FIELDS canonical record shape — the Glue DDL column set + schema-drift test source of truth"
provides:
  - "Private SSE (AES256) S3 bucket kmv-ledger-use1-adba57e4419be01f with PAB all-true, 365-day lifecycle on ledger/, no public policy"
  - "Athena workgroup kmv-ledger-use1 + Glue database kmv_ledger_use1 + partition-projection Glue table `ledger` (no crawler/MSCK)"
  - "Least-privilege task-role IAM: voice s3:PutObject on ledger/* only; auth s3:ListBucket (prefix-conditioned) + s3:GetObject on ledger/* only; no delete anywhere"
  - "SOPS-sourced SSM SecureString /kmv/secrets/use1/ledger/code_hash_salt (32-byte HMAC salt) injected to voice as KMV_LEDGER_SALT via valueFrom"
  - "SSM params /kmv/ledger/use1/bucket_name + bucket_arn; KMV_LEDGER_BUCKET (voice) + LEDGER_BUCKET/ADMIN_EMAILS (auth) env injected via the ecs-task ledger dependency"
  - "apps/voice/tests/test_ledger_schema.py — DDL-vs-LEDGER_FIELDS drift guard (6/6)"
affects: [15-03, 15-05]

tech-stack:
  added: []
  patterns:
    - "New modules/ledger/v1.0.0 mirrors cloudfront-assets' private-SSE-bucket + PAB + ssm.tf file split; unit copied from the secrets unit with an enabled toggle in site.hcl"
    - "Random-suffixed bucket name injected into voice/auth task containers via a dependency \"ledger\" block in the ecs-task unit (mirrors the existing task_role_arn injection) rather than as a literal in pure-data service.hcl"
---

## What shipped

**Tasks 1–2 (code, autonomous — committed by the executor):**
- `modules/ledger/v1.0.0/{main,variables,outputs,ssm}.tf` + `config.hcl` — the ledger module (`dba0334`)
- `live/site/region/us-east-1/ledger/terragrunt.hcl` unit, `site.hcl` toggle, voice/auth `service.hcl` IAM+secret+env, `.secrets.sops.json.template`, `SECRETS.md`, the `ecs-task` ledger-dependency injection, and `apps/voice/tests/test_ledger_schema.py` (`653a501`)
- Verified pre-apply: `terraform validate` green, `terragrunt hcl fmt --check` clean, schema-drift test 6/6, IAM grep confirms least-privilege (no delete, voice write-only, auth read-only), no plaintext salt.

**Task 3 (operator-SSO apply — performed live 2026-07-13 with the `klanker-terraform`/`klanker-application` SSO profiles, account 052251888500):**

1. Added the real `ledger.code_hash_salt` (a fresh `openssl rand -hex 32`) into the encrypted `.secrets.sops.json` via `sops --set` (the executor had only left a `CHANGEME` template, having had no KMS access). Value is encrypted at rest (`ENC[AES256_...]`); never printed.
2. `terragrunt plan` on the ledger unit — **10 add, 0 change, 0 destroy**; reviewed PAB all-true + AES256 + no public bucket policy. Applied → 10 resources created; bucket `kmv-ledger-use1-adba57e4419be01f`.
3. `secrets` unit — **1 add** (the salt SecureString) — applied.
4. `ecs-task` unit — **2 add / 2 change / 2 destroy** (new voice+auth task-def revisions with the ledger IAM/secret/env; the "destroy" is deregistering the old immutable revisions, non-disruptive) — applied.
5. `ecs-service` unit — the un-targeted plan wanted to revert **telephony-edge :9 → :8** (pre-existing state/live drift, unrelated to the ledger), so applied **only** voice + auth via `-target` (**0 add / 2 change / 0 destroy**). Rolling deploy: voice → task-def :44, auth → :11; both `rolloutState=COMPLETED`, old tasks drained.

## Live verification (operator checkpoint steps)

- `aws s3api get-public-access-block` → all four flags `true`; `get-bucket-encryption` → `AES256`.
- `aws ssm get-parameter --with-decryption /kmv/secrets/use1/ledger/code_hash_salt` → `SecureString`, 64-hex (32-byte) value resolves.
- voice task :44 carries env `KMV_LEDGER_BUCKET` + secret `KMV_LEDGER_SALT`; auth task :11 carries env `LEDGER_BUCKET` + `ADMIN_EMAILS=whereiskurt@gmail.com`.
- Live services healthy: `voice.klankermaker.ai/` → 200; auth under its `/use1` basePath → `/use1` 200, `/use1/api/health` 200, `/use1/api/oidc/.well-known/openid-configuration` 200 (issuer correct). ECS deployments COMPLETED with the deployment circuit unbroken.

## Deviation

`region/us-east-1/ecs-task/terragrunt.hcl` gained a `dependency "ledger"` block that injects the random-suffixed bucket name onto the voice (`KMV_LEDGER_BUCKET`) and auth (`LEDGER_BUCKET`) containers, mirroring the file's existing `task_role_arn` injection. This file was not in the plan's declared `files_modified` (Rule 2, documented) — necessary because `service.hcl` is pure-data and the bucket name is only known after apply.

## Issues Encountered / open follow-ups

- **App-code images predate phase 15 (the key operational caveat).** The terraform apply updated task-def *metadata* (env/secret/IAM) but not the container *images*. Running images are `kmv-voice-app:288f4bcc` (PR #29, 2026-07-11) and `kmv-auth-app:244dcdd5` (PR #26, 2026-07-10) — neither contains `ledger.py`, the tap wiring, the token claims, or the `/admin` routes. So the ledger **store is live and wired, but nothing writes to or reads from it yet**: the voice writer (15-02/15-03) and the auth `/admin` view (15-05) only activate once the voice/auth images are rebuilt from phase-15 code and deployed (normally on merge to `main` + CI image build). `/use1/admin` currently 404s because the route is absent from the deployed image, not because of the gate.
- **telephony-edge :9→:8 state drift** (pre-existing, unrelated to this phase): the live telephony-edge service runs task-def :9 while terraform state wants :8 (an out-of-band image push). Left untouched via `-target`. A future `terragrunt apply` of the `ecs-service` unit will try to revert it — reconcile the drift (re-import or re-register :9 in terraform) before the next full service apply.

## User Setup Required

None remaining for the infra. To activate end-to-end capture + admin view: merge phase 15 to `main` so CI builds and deploys new voice/auth images (or build/push the two images and bump the task-def image tags manually), then re-verify a live session appears under `/use1/admin/transcripts`.

## Next Phase Readiness

- The ledger store, IAM, salt, and env wiring are all live and verified — no infra blockers.
- The remaining gap to a working demo is purely an application-image deploy of the already-committed phase-15 code, gated on phase verification + merge.

---
*Phase: 15-private-transcription-ledger-s3-batch-athena-admin-conversat*
*Completed: 2026-07-13*

## Self-Check: PASSED

Module + unit + service wiring committed (`dba0334`, `653a501`); infra applied live and verified (bucket private/SSE, salt SecureString resolves, least-privilege IAM, voice/auth services healthy on the new task-defs). LEDG-02 + LEDG-05 marked complete.
