<!-- generated-by: gsd-doc-writer -->
# Deployment Guide

klanker-voice deploys three independently-buildable Fargate services — **voice**, **auth**, and
**telephony-edge** — plus a CloudFront/S3 static-asset front for the browser client, onto a
Terragrunt-managed AWS site (`infra/terraform/live/site`, site label `kmv`). This guide covers
what actually deploys the platform today: the CI build/deploy pipelines, the Terragrunt layout,
image contents, secret provisioning, and rollback.

For the full secrets pipeline (SOPS → SSM → container env) and every configuration knob, see
[docs/guides/configuration.md](configuration.md). For the system's overall shape, see
[docs/architecture/overview.md](../architecture/overview.md).

## Deployment targets

| Target | Config | What it deploys |
|---|---|---|
| ECS Fargate (3 services) | `infra/terraform/live/site/services/{voice,auth,telephony-edge}/service.hcl` | The three deployable containers — see [Services](#services) below |
| CloudFront + S3 (full-front) | `infra/terraform/live/site/global/cloudfront/`, `infra/terraform/live/site/region/us-east-1/cloudfront/` | `voice.klankermaker.ai` — serves the built SPA from a retained S3 bucket and routes `/api/*`, `/health` to the ALB origin |
| ALB | `infra/terraform/modules/network/v1.0.0` (`alb.enabled = true` in `network.hcl`) | HTTPS termination + host-header routing to the voice and auth ECS services |
| DynamoDB | `infra/terraform/live/site/region/us-east-1/dynamodb/` | `kmv-voice-usage`, `kmv-auth-authjs`, `kmv-auth-electro` |
| SSM SecureString secrets | `infra/terraform/live/site/region/us-east-1/secrets/` | Provider API keys + app secrets, see [Secrets](#secrets) |
| Route53 + ACM | `infra/terraform/modules/certs`, site-level `dns` locals in `site.hcl` | `auth.klankermaker.ai`, `voice.klankermaker.ai` records + TLS certs |
| Telephony edge (self-hosted Asterisk) | `apps/voice/asterisk/`, `infra/terraform/live/site/services/telephony-edge/service.hcl` | Inbound-only PSTN edge — see [Telephony edge](#telephony-edge) |

There is no separate Docker Compose or Kubernetes deployment path — the only supported target is
this single-region (`us-east-1`) AWS Fargate site. `apps/voice/asterisk/docker-compose.yml` exists
for local Asterisk development only (see [docs/dataflows/telephony-voipms.md](../dataflows/telephony-voipms.md)),
not for production.

## Services

### voice

- Source: `apps/voice/` (Python 3.12, FastAPI `server.py`, Pipecat 1.5.0).
- Public entry: `voice.klankermaker.ai` via the CloudFront distribution (SPA from S3 + `/api/*`,
  `/health` proxied to the ALB).
- ECS task: 1024 CPU / 2048 MiB, `assign_public_ip = true` (media flows UDP direct
  browser↔task — see `docs/dataflows/browser-webrtc.md`), session-count autoscaling
  1→4 on a custom `ActiveSessions` CloudWatch metric.
- `system_controls` pins the container's ephemeral UDP bind range to `20000-20100` to match the
  `webrtc_udp` security group exactly (`infra/terraform/modules/network/v1.0.0/securitygroups.tf`).

### auth

- Source: `apps/auth/webapp/` (Next.js 16 standalone build, embedded `oidc-provider`, next-auth v5
  magic-link).
- Public entry: `auth.klankermaker.ai` via the ALB (no CloudFront in front of auth).
- ECS task: 512 CPU / 1024 MiB, no public IP, no autoscaling (`min_capacity = 1, max_capacity = 2`,
  `enabled = false`).

### telephony-edge

- Source: `apps/voice/asterisk/` (Asterisk + ARI) plus the `klanker_voice.telephony` Python
  controller process, built into one container from `apps/voice/asterisk/Dockerfile`.
- No load balancer — ARI is bound loopback-only (`http://127.0.0.1:8088`) inside the container and
  is never exposed on the task ENI. The task has a public IP solely so it can register outbound to
  the VoIP.ms SIP trunk and receive return SIP/RTP traffic.
- ECS task: 2048 CPU / 4096 MiB (bumped from an original 0.5 vCPU sizing after a live call showed
  audio garble under CPU starvation — Asterisk + µ-law transcode + 8k↔24k resampling + the full
  STT/LLM/TTS cascade cannot share half a core without underruns).
- `readonly_root_filesystem = false` — Asterisk needs a writable root FS for its runtime config
  rewrite and its own logs/spool/astdb.
- Inbound SIP/RTP ingress is locked to the eight Toronto VoIP.ms POP CIDRs
  (`infra/terraform/live/site/region/us-east-1/network/telephony-sg.hcl`), **never** `0.0.0.0/0`.
  This SG is deliberately excluded from the shared `security_group_ids` list voice/auth use (which
  includes `webrtc_udp`, open on UDP 20000-20100) — attaching it here would defeat the POP lock.
- Single-task, no autoscaling — concurrency is enforced at the application layer (one call at a
  time via the answer-gate / quota system), not by running more tasks.
- <!-- VERIFY: whether a dedicated build-telephony-edge.yml CI workflow exists — as of this
  writing no such workflow is present under .github/workflows/, so telephony-edge images are built
  and pushed manually and deployed by setting TF_VAR_TELEPHONY_EDGE_IMAGE_TAG for a manual
  terragrunt apply of the ecs-task/ecs-service units (see Manual apply below). -->

## CI build and deploy pipeline

CI is entirely GitHub Actions with keyless OIDC-to-AWS auth (the `github-oidc` Terraform module
provisions four roles: `kmv-github-readonly`, `kmv-github-terragrunt`, `kmv-github-release`,
`kmv-github-deploy` — no long-lived AWS keys exist in CI). Per `infra/CI.md`:

- **`terragrunt-plan.yml`** — pull requests and pushes to `main` that touch `infra/**` run a
  read-only `terragrunt run plan --all` using the `kmv-github-readonly` role. No approval gate.
- **`terragrunt-apply.yml`** (`infra/terraform-apply` environment) — **infra applies never run
  automatically.** This workflow is `workflow_dispatch`/`workflow_call` only, and the
  `terraform-apply` GitHub environment carries a required-reviewer rule, so every apply is
  human-approved before the `kmv-github-terragrunt` role is ever assumed. Accepts an optional
  comma-separated `modules` input to scope the apply to specific units; empty applies everything
  (`terragrunt run apply --all`).
- **App image build + deploy** — separate per service, triggered by a path filter on push to
  `main`:
  - `apps/voice/**` changes → **`build-voice.yml`** builds `apps/voice/Dockerfile` (guarded — a
    no-op with a job-summary note if the Dockerfile is absent), pushes to ECR as
    `kmv-voice-app:${{ github.sha }}` via `kmv-github-release`, then syncs the extracted client
    `dist/` to the CloudFront-assets S3 bucket and invalidates `/`, `/index.html`, `/greetings/*`
    on the voice CloudFront distribution (both S3 sync and invalidation skip cleanly with a
    job-summary note if the CloudFront SSM parameters don't exist yet, so this can merge before
    the CloudFront cutover is applied).
  - `apps/auth/**` changes → **`build-auth.yml`** builds `apps/auth/webapp/Dockerfile.webapp`
    (same guard pattern), pushes to ECR as `kmv-auth-app:${{ github.sha }}`.
  - Both build workflows then call **`deploy.yml`** (`workflow_call`) with `service: voice` or
    `service: auth` via the `kmv-github-deploy` role.
- There is no equivalent `build-telephony-edge.yml` in `.github/workflows/` today — see the VERIFY
  note under [telephony-edge](#telephony-edge) above.

### What `deploy.yml` actually does

`deploy.yml` is a `terragrunt apply` limited to exactly two units, `ecs-task` and `ecs-service`
under `infra/terraform/live/site/region/us-east-1/`:

1. Assumes `kmv-github-deploy` (branch-restricted to `main`).
2. **Resolves per-service image tags** — because the `ecs-task`/`ecs-service` Terragrunt units are
   shared across both `voice` and `auth`, deploying one service must not repoint the other
   service's task to a nonexistent image. The workflow queries ECS for each service's
   currently-running image tag (`aws ecs describe-services` → `describe-task-definition`) and sets
   `TF_VAR_VOICE_IMAGE_TAG` / `TF_VAR_AUTH_IMAGE_TAG` so only the service actually being deployed
   gets the freshly-built `${{ github.sha }}` tag; the other keeps its current tag (a no-op apply
   for it). First-deploy fallbacks are `voice:0.1.0` / `auth:0.0.0` (the `service.hcl` `get_env`
   defaults).
3. Runs `terragrunt apply --no-color --non-interactive -auto-approve` for `ecs-task` then
   `ecs-service`, in that order, per module — this triggers a new ECS task definition revision and
   an `ecs:UpdateService` rolling deployment.
4. Writes a pass/fail job summary with the commit SHA, triggering actor, and timestamp.

This workflow can also be run directly via `workflow_dispatch` with a `service` choice
(`voice`/`auth`/`all`) for a manual redeploy without a new image build (e.g. to force a task
definition refresh after an out-of-band secret rotation).

### Manual `terragrunt apply`

Tool pins (kept aligned between CI and local): **terragrunt 0.97.1**, **terraform 1.14.3**,
**sops 3.11.0** (see `infra/.envrc` for the non-secret env contract CI mirrors). From
`infra/terraform/live/site`:

```bash
# Plan/apply everything
cd region/us-east-1/<module> && terragrunt apply

# Or, from the site root, everything at once
terragrunt run apply --all
```

The **telephony-edge** image ships this way today: build and push the image manually
(`docker build`, `docker push` to the `telephony-edge` ECR repository), then set
`TF_VAR_TELEPHONY_EDGE_IMAGE_TAG=<sha>` before applying the `ecs-task`/`ecs-service` units for that
service, mirroring the tag-pinning pattern `deploy.yml` automates for voice/auth.

## Container images

| Service | Dockerfile | Build context | Base image(s) | Key system deps |
|---|---|---|---|---|
| voice | `apps/voice/Dockerfile` | `apps/voice` | `node:22-slim` (client build stage) → `python:3.12-slim` (runtime) | `libopus0`, a resolved-at-build-time `libvpx<N>` package (name tracks the Debian ABI, not hardcoded), `ffmpeg`, `ca-certificates`; `uv` (pinned via `ghcr.io/astral-sh/uv:0.11.26`) for dependency install; NLTK `punkt_tab` tokenizer data baked in at build time (needed by ElevenLabs' sentence-streaming TTS) |
| auth | `apps/auth/webapp/Dockerfile.webapp` | `apps/auth/webapp` | `node:current-alpine` (builder) → `node:current-alpine` (runner) | `curl` (health checks), `build-base`/`g++`/`python3` in the builder stage only; Next.js `standalone` output, runs as the non-root `node` user |
| telephony-edge | `apps/voice/asterisk/Dockerfile` | `apps/voice/asterisk` | <!-- VERIFY: base image and full system dependency list — not read as part of this doc pass; inspect apps/voice/asterisk/Dockerfile directly for Asterisk package sourcing and version --> | Asterisk (ARI/Stasis), the Python `klanker_voice.telephony` controller sharing the voice service's dependency tree |

The voice image is deliberately **not** built on `dailyco/pipecat-base` — that base image targets
Pipecat Cloud's own `bot.py` + process-per-session runtime contract, which conflicts with this
project's self-hosted Fargate + FastAPI/uvicorn (`server.py`) entrypoint (documented in this
repo's `.claude/CLAUDE.md`).

The voice image's client build stage bakes public (non-secret) OIDC config directly into the
static bundle via build args (`VITE_OIDC_ISSUER`, `VITE_OIDC_CLIENT_ID`, `VITE_OIDC_AUDIENCE`,
`VITE_OIDC_REDIRECT_URI`) — these default to the production values since the voice client is a
public PKCE client with no secret to protect; override the build args for a non-production build
against a different auth issuer. Both the voice and auth Dockerfiles also bake in a build/version
stamp (`VITE_APP_VERSION`/`APP_VERSION` + a build timestamp) so the running UI/API can report
exactly which commit is live.

## Secrets: SOPS → SSM → container env

Full detail (per-key names, the `SECRETS.md` schema reference, and per-service secret-name tables)
lives in [docs/guides/configuration.md § Secrets: SOPS → SSM → env](configuration.md#secrets-sops--ssm--env).
In short:

1. Secrets are authored locally in `infra/terraform/live/site/.secrets.sops.json` (SOPS-encrypted,
   safe to commit) or a gitignored plaintext `.secrets.json` fallback.
2. `site.hcl` decrypts on the fly at every plan/apply and feeds the `secrets` Terraform module,
   which writes one SSM `SecureString` parameter per secret/key pair under
   `/kmv/secrets/use1/<name>/<key>`.
3. Each ECS task definition's `containers[].secrets` maps a container environment variable to that
   parameter's `valueFrom` ARN; the execution role is granted `ssm:GetParameters` + `kms:Decrypt`
   scoped only to the prefixes it needs. Nothing plaintext touches the image, the task definition
   source, or CI logs.

### One-time state and SOPS bootstrap

Two idempotent scripts stand up the prerequisites a fresh site needs before any Terragrunt apply
can run:

- **`scripts/bootstrap-state.sh [SGUID]`** — creates (or verifies) the versioned + encrypted S3
  Terraform state bucket and the DynamoDB lock table, both named `tf-kmv-use1-<SGUID>`, in the
  state account under the `klanker-terraform` AWS CLI profile. `SGUID` is the single source of
  truth for the state suffix and must match across the bucket/table name, `infra/.envrc`,
  `site.hcl`'s `random_suffix` default, and the GitHub repo variable `SGUID` — the script prints
  the exact `gh variable set` commands to keep those in sync. Safe to re-run.
- **`scripts/setup-sops.sh`** — creates (or reuses, if `alias/sops` already resolves) a
  single-region KMS key for SOPS encryption, writes `.sops.yaml` at the repo root with that key's
  ARN, persists `TF_VAR_SOPS_KMS_KEY_ID` into `infra/.envrc` for local shells, and sets the
  matching `TF_VAR_SOPS_KMS_KEY_ID` GitHub repository variable (CI does not source
  `infra/.envrc`, so this step is required for CI's `kms-sops-decrypt` IAM policies to interpolate
  the correct key). Both scripts run under the `klanker-terraform` AWS profile, region
  `us-east-1`.

## IAM / GitHub OIDC roles

Provisioned by `infra/terraform/live/site/global/github-oidc/` (module:
`infra/terraform/modules/github-oidc`), scoped to this repository via GitHub's OIDC identity
provider — **no static AWS credentials are stored in GitHub**. Four roles, each with a distinct
blast radius:

| Role | Used by | Scope |
|---|---|---|
| `kmv-github-readonly` | `terragrunt-plan.yml` (PRs + pushes to `main`) | AWS managed `ReadOnlyAccess` + SOPS/SSM KMS decrypt (for reading SecureString values at plan time) + Terraform state read/lock. No branch restriction. |
| `kmv-github-terragrunt` | `terragrunt-apply.yml` (`terraform-apply` environment, human-reviewer-gated) | Broad infra CRUD (EC2, ECS, ECR, ELB, Lambda, autoscaling, S3, CloudWatch/Logs, SSM, SNS, CloudFront, Route53, ACM, WAF, service discovery, DynamoDB, IAM, KMS) plus cross-account Route53 delegation. This is the only role that can create/destroy infrastructure, and it only runs after a human approves the environment gate. |
| `kmv-github-release` | `build-voice.yml` / `build-auth.yml` | ECR push (scoped to `kmv-*` repos), S3 asset sync (scoped to `kmv-*` buckets), CloudFront invalidation, SSM read, Terraform state read/lock, ECS register/update, `iam:PassRole` scoped to `*-kmv-task-role`/`*-kmv-execution-role`, plus read-only ELB/service-discovery/autoscaling/CloudWatch for the ECS deploy step. Branch-unrestricted (build workflows can run from any branch), 2-hour max session for long builds. |
| `kmv-github-deploy` | `deploy.yml` | The same ECS update surface as `release` (register/deregister task defs, update service, `iam:PassRole` to the task/execution roles) plus SSM/Terraform-state read, without the ECR push or S3-asset grants `release` has. **Branch-restricted to `main`.** |

Terraform/Terragrunt state itself lives in S3 + DynamoDB (bucket/table `tf-kmv-use1-<SGUID>`),
never local state files.

## Network

Provisioned by `infra/terraform/modules/network` (`infra/terraform/live/site/region/us-east-1/network/`):

- VPC `10.0.0.0/16`, 2 AZs, public subnets `10.0.1.0/24`/`10.0.2.0/24`, private subnets
  `10.0.10.0/24`/`10.0.20.0/24`, NAT gateway enabled.
- ALB (TLS policy `ELBSecurityPolicy-TLS13-1-2-2021-06`) — the shared HTTPS entry for voice's
  `/api/*`/`/health` (behind CloudFront) and auth's full public surface.
- A dedicated **`webrtc_udp`** security group open on UDP `20000-20100` (`0.0.0.0/0`) for direct
  browser↔task WebRTC media — the voice ECS task runs with a public IP and self-advertises it as
  a host ICE candidate rather than routing media through a TURN/SFU vendor.
- A dedicated **`telephony_edge`** security group (Toronto VoIP.ms POP CIDRs only, defined in
  `telephony-sg.hcl`) — deliberately **not** part of the shared security-group list voice/auth
  attach, so it never inherits the open `webrtc_udp` range. See
  [Telephony edge](#telephony-edge) above and the operator runbook embedded as comments in
  `telephony-sg.hcl` for the 6-month POP re-verification procedure (VoIP.ms occasionally rotates
  the Toronto POP IP set; the SG and Asterisk's own `pjsip.conf` `[voipms-identify]` `match=` list
  must be updated together in the same commit).

## CloudFront / static assets

`voice.klankermaker.ai` is a single full-front CloudFront distribution (module:
`infra/terraform/modules/cloudfront`, global unit at
`infra/terraform/live/site/global/cloudfront/`, us-east-1-pinned because CloudFront + its ACM cert
are global/us-east-1 resources) serving the built Vite SPA from a retained private S3 bucket as
the default behavior, and routing `/api/*` and `/health` to the ALB origin. This design exists
specifically to fix a multi-build asset-skew "black screen" failure mode — see
`docs/superpowers/specs/2026-07-07-cloudfront-static-assets-design.md` for the incident and the
design rationale. Only the primary `us-east-1` region is wired as a live origin today;
`ca-central-1`/`ap-southeast-1` are pre-wired-but-inert mock asset buckets in
`site.skip_regions`, so lighting up a second region means removing it from `skip_regions` and
adding it to `cloudfront.domains`/`cloudfront.regions` — no restructuring required.

`build-voice.yml`'s asset-publish step (see [CI build and deploy pipeline](#ci-build-and-deploy-pipeline)
above) is what actually keeps S3 in sync with each deploy: content-hashed `assets/` sync
immutable/long-cache, `index.html` uploaded no-cache so a deploy is visible immediately, and
`greetings/*` synced with a short (5-minute) cache plus an explicit CloudFront invalidation — the
pre-rendered greeting clips use stable filenames, so without both the short TTL and the
invalidation a re-rendered clip would never reach returning visitors.

## Rollback

There is no dedicated rollback workflow in `.github/workflows/` today — rollback is done by
re-deploying a known-good image tag:

- **voice / auth** — re-run `deploy.yml` via `workflow_dispatch` after setting
  `TF_VAR_VOICE_IMAGE_TAG` / `TF_VAR_AUTH_IMAGE_TAG` to a previously-built, known-good `git sha`
  still present in the ECR repository (both repos are `IMMUTABLE` tag-mutability with a 10-image /
  30-day lifecycle policy, so a recent prior tag is normally still pullable), or `terragrunt
  apply` the `ecs-task`/`ecs-service` units manually with that `TF_VAR_*` set.
- **telephony-edge** — the established precedent: `TF_VAR_TELEPHONY_EDGE_IMAGE_TAG` is pinned to
  an exact known-good image SHA in `service.hcl` and only bumped deliberately after live-call
  verification (the current pin was chosen specifically to fix an RTP-pacing audio garble
  regression on live PSTN calls — see `docs/dataflows/telephony-voipms.md` and the Phase-12 debug
  history). Rolling back means reverting that pinned tag string and re-applying the
  `ecs-task`/`ecs-service` units for this service.
- All three ECS services are single-region, and `ecs:UpdateService` performs a standard ECS
  rolling deployment — a bad task definition revision that fails health checks will not fully
  replace healthy running tasks, but manual rollback (re-applying the previous task definition
  revision or image tag) is still the documented recovery path, not an automated canary/blue-green
  mechanism.

## Operational references

- [docs/operators/voipms-provisioning-runbook.md](../operators/voipms-provisioning-runbook.md) —
  step-by-step recipe for provisioning a VoIP.ms subaccount and DID for the telephony edge.
- [docs/operators/phase12-seed-data.md](../operators/phase12-seed-data.md) — operator-only seed
  data and parameters (e.g. the `/kmv/operators/*` SSM prefix, which no service task role is ever
  granted access to).
- [docs/dataflows/telephony-voipms.md](../dataflows/telephony-voipms.md) — the PSTN → VoIP.ms →
  Asterisk → RTP call path in detail.
- [docs/architecture/overview.md](../architecture/overview.md) — full system diagram and
  cross-service data flow.
- [docs/guides/configuration.md](configuration.md) — every environment variable, `pipeline.toml`
  field, and the full secrets-flow reference.

<!-- VERIFY: exact AWS account ID and full SSM/ARN values shown throughout the referenced
terraform live config are committed in this repository and not fabricated here, but confirm they
still match the live AWS account before relying on them for an out-of-band operation (e.g. console
lookups, aws-cli commands run outside CI). -->
