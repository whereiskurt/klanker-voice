# Phase 2: Infra Skeleton - Research

**Researched:** 2026-07-04
**Domain:** Terragrunt/Terraform AWS infrastructure clone (defcon.run.34 → klanker-voice site "kmv"), cross-account DNS/ACM, SES, SOPS→SSM secrets, GitHub OIDC CI
**Confidence:** HIGH (primary source is the local dc34 tree itself — every mechanism verified by reading the actual .hcl/.tf files)

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

#### Naming
- **D-01:** Site label is **`kmv`** (klanker-maker-voice). **Never use "kmk" anywhere.** State prefix `tf-kmv`, SSM namespace `/kmv/...`, bootstrap params `/kmv/bootstrap/*`.

#### Module sourcing
- **D-02:** **Copy defcon.run.34 modules verbatim** into `infra/terraform/modules/` (config.hcl + v1.0.0 pattern preserved), changing only naming/config inputs. Prune dead functionality opportunistically in later phases — not during import.
- **D-03:** **Keep the multi-region machinery** (skip_regions, region.hcl derivation, providers/regional.hcl aliases) as-is; us-east-1 is the only active region via config. One simplification allowed: **single-region SOPS KMS key** instead of dc34's multi-region CMK.
- **D-04:** Modules in scope: network, certs, ecs-cluster, ecr, dynamodb, secrets, email, github-oidc, ecs-task, ecs-service, site. Explicitly NOT copied: cloudtrail, waffaw, mqtt, ec2spot, s3-uploads*, cloudfront*, bib/gpx modules.

#### State backend & environment
- **D-05:** State backend created by a checked-in **idempotent `scripts/bootstrap-state.sh`** (aws cli, `klanker-terraform` profile): versioned+encrypted S3 bucket and DynamoDB lock table with `tf-kmv` prefix; prints the `TG_BUCKET_USE1`/`TG_TABLE_USE1` exports. Run once, repeatable.
- **D-06:** Non-secret env (bucket/table names, `TF_VAR_APPLICATION_ACCOUNT_ID=052251888500`, `TF_VAR_MANAGEMENT_ACCOUNT_ID=481723467561`, `TF_VAR_profile_prefix=klanker-`) lives in a **checked-in `infra/.envrc`** (direnv). CI mirrors the same values in workflow env. No secrets in .envrc.

#### GitHub repo & CI
- **D-07:** Repo is **public: github.com/whereiskurt/klanker-voice**. SOPS keeps secrets safe; quotas assume a public URL anyway.
- **D-08:** CI is **path-filtered push-to-main**: `apps/voice/**` → build/deploy voice, `apps/auth/**` → build/deploy auth, `infra/**` → terragrunt **plan only** with manual apply approval (GitHub environment gate). Infra applies are always human-gated.
- **D-09:** GitHub OIDC roles cloned from the dc34 github-oidc module (readonly/deploy/release pattern), scoped to whereiskurt/klanker-voice.

#### SES & email identity
- **D-10:** Magic-link sender is **`sign-in@auth.klankermaker.ai`** — DKIM/SPF scoped under klankermaker.ai, sender domain matches the site the user just visited.
- **D-11:** DMARC for klankermaker.ai: **`p=quarantine`** with SPF+DKIM alignment.
- **D-12:** **SES production access already exists** — the klanker-application account is out of the SES sandbox with an increased quota. INFR-04 reduces to: SES domain identity + DKIM + DMARC records via the email module (cross-account DNS into the management zone). No prod-access request, no external review clock.

### Claude's Discretion
- VPC/subnet sizing, cert SAN layout, ECR retention policies — follow dc34 defaults unless something conflicts with kmv needs.
- Exact GitHub Actions workflow structure (as long as path filtering + infra plan-gate hold).
- Where the WebRTC SG/public-IP knobs land in the network/ecs-service module inputs (Phase 4 verifies; Phase 2 should not block on the aiortc port-range question — wide UDP range per research).

### Deferred Ideas (OUT OF SCOPE)
None — discussion stayed within phase scope.
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| INFR-01 | Terragrunt skeleton (site "kmv") provisions network, certs, ecs-cluster, ecr, dynamodb, secrets, email, github-oidc, ecs-task, ecs-service from the defcon.run.34 layout | Complete clone inventory (§Clone Inventory), site.hcl delta table, terragrunt DAG + apply order — all verified against the dc34 source tree |
| INFR-02 | voice.klankermaker.ai and auth.klankermaker.ai resolve with valid TLS via cross-account DNS (management zone) | §Cross-Account DNS & Certs — site module creates subdomain zones in app account + NS-delegates from mgmt zone; certs module validates ACM per-subdomain in app-account zones and site-cert in mgmt zone |
| INFR-04 | SES production access and DKIM for klankermaker.ai (already out of sandbox per D-12) | §SES / Email — email module already creates identity + DKIM CNAMEs + SPF MAIL FROM + DMARC p=quarantine per SES identity; org-domain DMARC option analyzed |
| INFR-05 | Provider API keys flow SOPS → SSM SecureString → container secrets | §SOPS Setup — site.hcl `secret_values` sops decrypt → secrets module → `/kmv/secrets/use1/<name>/<key>` SecureString; bootstrap-param migration checklist |
| INFR-07 | GitHub Actions deploys via OIDC roles (no long-lived AWS keys) | §GitHub OIDC + §GitHub Actions — role naming `kmv-github-{readonly,terragrunt,deploy,release}`, environment gates, workflow files to clone/adapt |
</phase_requirements>

## Summary

This phase is a **clone-and-rename operation against a proven local source tree**, not greenfield IaC. Everything needed exists at `/Users/khundeck/working/defcon.run.34/infra/terraform/` and has been verified file-by-file in this research: the providers layer (env-driven S3 backend + profile-or-OIDC provider generation), the site.hcl aggregation pattern (service.hcl data files feed dynamodb/ecr/ecs-task/ecs-service lists), 11 in-scope versioned modules, the SOPS→SSM secrets flow (`env.sops.sh` is a complete working reference), and four GitHub Actions workflows implementing exactly the OIDC plan/apply/build/deploy split D-08 wants.

The genuinely new work is small and enumerated: (1) a `scripts/bootstrap-state.sh` (dc34 has no equivalent — its bucket/table naming contract is `tf-<label>-use1-<SGUID>` from `env.sh` and the workflows), (2) a single-region variant of the SOPS KMS key (drop the replicate-key loop from env.sops.sh), (3) kmv-specific `site.hcl` values and two service.hcl stubs, (4) an org-domain DMARC record for klankermaker.ai (the module's DMARC is per-SES-identity; the apex needs one additive record — the dc34 `bib-secrets` inline-unit pattern is the precedent for adding it without touching copied modules), and (5) the WebRTC SG groundwork (a UDP-ingress security group; `assign_public_ip` already exists per-service in the ecs-service module).

Three traps found in source that will break a naive clone: `TF_VAR_profile_prefix` must be `klanker` **not** `klanker-` (providers append `-application` themselves — CONTEXT.md D-06's literal value would produce `klanker--application`); the root `live/site/terragrunt.hcl` anchors repo-root on `find_in_parent_folders("AGENTS.md")` (klanker-voice must have an AGENTS.md at repo root or the anchor must change); and the root terragrunt.hcl unconditionally reads `global/waf/waf.hcl` even though kmv drops WAF (copy the data file or trim the read).

**Primary recommendation:** Clone in dependency order — bootstrap state → copy providers/modules/live tree with the delta table below → SOPS key + secrets migration → apply site → certs → network → parallel(ecr, dynamodb, secrets, email, ecs-cluster) → github-oidc → CI workflows. Leave `ecs_tasks.enabled = false` / `ecs_services.enabled = false` in site.hcl until Phases 3/4 flip them (the exclude-block machinery handles this cleanly).

## Project Constraints (from CLAUDE.md)

- **Tech stack (infra):** Terraform + Terragrunt "match defcon.run.34 pins" — CI pins verified in dc34 workflows: terraform **1.14.3**, terragrunt **v0.97.1**, sops **v3.11.0** [VERIFIED: dc34 .github/workflows/terragrunt-plan.yml].
- **Naming:** "klanker-voice" everywhere; never "voiceai" (copyright). Additionally per D-01: never "kmk" — deliverable grep for both must return zero.
- **Only new infra vs dc34:** Fargate tasks with public IPs + SG ingress for a bounded UDP range (WebRTC media).
- **Secrets:** SOPS → SSM SecureString, containers consume via `valueFrom` — proven dc34 pattern, reproduce as-is.
- **GSD workflow enforcement:** work flows through GSD commands; planner output executes via /gsd-execute-phase.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Remote state (S3+DynamoDB lock) | Database/Storage (app acct 052251888500) | — | providers/*.hcl `remote_state` blocks read `TG_BUCKET_USE1`/`TG_TABLE_USE1`; created out-of-band by bootstrap script |
| Hosted zones auth./voice.klankermaker.ai | API/Backend (app acct Route53) | CDN/edge (mgmt acct NS delegation) | site module creates subdomain zones in app account, writes NS records into mgmt zone Z036807010CWM2JH60RKQ |
| ACM certs + validation records | API/Backend (app acct ACM) | mgmt acct Route53 (site-cert validation only) | certs module: subdomain certs validate in app-account zones; primary-zone cert validates in mgmt zone |
| SES identity/DKIM/SPF/DMARC | API/Backend (app acct SES us-east-1) | DNS records in app-acct subdomain zone (auth.*) and mgmt zone (apex DMARC) | email module ses-domain submodule writes records via provider alias mapping |
| Secrets (API keys) | Database/Storage (SSM SecureString + KMS) | Local dev (SOPS file in git) | site.hcl decrypts `.secrets.sops.json` at plan time; secrets module writes `/kmv/secrets/use1/...` |
| CI identity | API/Backend (IAM OIDC provider + roles, global) | GitHub (environments/vars) | github-oidc module in `live/site/global/`; roles `kmv-github-*` |
| VPC/ALB/SGs, ECS cluster, ECR | API/Backend (regional, us-east-1) | — | network / ecs-cluster / ecr modules, one folder per region under `live/site/region/us-east-1/` |
| WebRTC UDP ingress + public IP | API/Backend (network SG + ecs-service input) | Browser (Phase 4/5) | `assign_public_ip` is an existing per-service input; UDP SG is the one additive resource |

## Standard Stack

### Core

| Tool | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| Terraform | 1.14.3 | IaC engine | dc34 CI pin [VERIFIED: dc34 terragrunt-plan.yml line 96]; local machine has 1.8.2 — **must upgrade** (state written by 1.14 is not readable by 1.8; CI/local must match) |
| Terragrunt | 0.97.1 (CI pin; local 0.99.1 works) | DRY orchestration, backend/provider generation | dc34 pin; `exclude {}` blocks require ≥0.96 [VERIFIED: dc34 skip.hcl comment] |
| sops | 3.11.0 | Secrets encryption (KMS-backed) | dc34 pin; local machine already has 3.11.0 [VERIFIED: local probe] |
| AWS provider | `>= 4.0` | All AWS resources | Generated by providers/*.hcl `required_providers` block [VERIFIED: dc34 providers/global.hcl] |
| random provider | `~> 3.6` | Suffixes | Same generated block [VERIFIED] |
| aws-cli v2 | 2.32.25 (local) | bootstrap-state.sh, SOPS KMS setup, SSM migration | Already installed [VERIFIED: local probe] |
| direnv | latest | Loads infra/.envrc (D-06) | **MISSING locally** — `brew install direnv` + shell hook required |

### Supporting

| Item | Purpose | When to Use |
|---------|---------|-------------|
| GitHub CLI (gh 2.86.0, installed) | `gh variable set` for repo vars (SITE_LABEL, SGUID, AWS_ACCOUNT_ID, TF_VAR_*) | CI setup step |
| AWS SSO session "Developer" | All three klanker profiles resolve through it | Every local terragrunt run (`aws sso login --sso-session=Developer`) |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| SOPS+KMS | age encryption | dc34's SECRETS.md mentions it; rejected — KMS keeps CI decrypt IAM-native (readonly role gets kms:Decrypt) |
| bootstrap-state.sh | Terragrunt auto-provision of backend | Terragrunt can create the bucket/table itself, but D-05 locks the explicit idempotent script (auditable, profile-pinned) |
| direnv .envrc | source env.sh (dc34 style) | D-06 locks .envrc; dc34's env.sh remains the reference for WHAT to export |

**Installation:**
```bash
brew install direnv tfenv   # or: brew install terraform@<...>
tfenv install 1.14.3 && tfenv use 1.14.3
# terragrunt 0.99.1 and sops 3.11.0 already present
```

## Package Legitimacy Audit

This phase installs **no language-registry packages** (no npm/PyPI/crates installs). All tooling is Homebrew/GitHub-release binaries (terraform, terragrunt, sops, direnv) whose exact versions are pinned by the dc34 source workflows being cloned — provenance is the dc34 repo itself, not a registry lookup. Terraform providers (hashicorp/aws, hashicorp/random) are fetched from the HashiCorp registry by terraform init and are constrained by the generated `required_providers` block copied verbatim from dc34 [VERIFIED: dc34 providers/global.hcl]. The seam's npm/pypi/crates check does not apply; no SLOP/SUS candidates exist.

**Packages removed due to [SLOP] verdict:** none
**Packages flagged as suspicious [SUS]:** none

## Architecture Patterns

### System Architecture Diagram (terragrunt dependency DAG)

```
                       [scripts/bootstrap-state.sh]  (one-time, klanker-terraform profile)
                                  │ creates tf-kmv-use1-<SGUID> S3 bucket + DDB lock table
                                  ▼
   infra/.envrc ──exports──▶ TG_BUCKET_USE1 / TG_TABLE_USE1 / TF_VAR_* / SGUID
                                  │
                                  ▼
 live/site/  (root unit = SITE module, global state key)
 ┌─────────────────────────────────────────────────────────────────────────┐
 │ site: creates auth.klankermaker.ai + voice.klankermaker.ai hosted zones │
 │       in APP acct; NS-delegation records in MGMT zone (global-mgmt)     │
 └───────────────┬─────────────────────────────────────────────────────────┘
                 │ zone_map output
                 ▼
 live/site/region/us-east-1/
   certs ──cert_map──▶ network ──vpc/subnets/SGs/ALB──▶ ┌──────────────┐
     │  (ACM: subdomain certs validated in app zones;   │ ecs-cluster  │
     │   site cert validated in mgmt zone)              │ ecr          │
     │                                                  │ dynamodb     │
     │              .secrets.sops.json ──sops──▶        │ secrets      │──▶ SSM /kmv/secrets/use1/*
     │                                                  │ email        │──▶ SES identity+DKIM+DMARC
     │                                                  └──────┬───────┘
     │                                                         ▼
     └────────────────────────────────▶ ecs-task ──▶ ecs-service   (DISABLED until Phase 3/4)
 live/site/global/
   github-oidc ──▶ IAM OIDC provider + kmv-github-{terragrunt,readonly,deploy,release,application} roles
                    └─ outputs trust policy for kmv-github-delegate (created MANUALLY in mgmt acct)
 .github/workflows/
   terragrunt-plan (readonly role, env terraform-plan) / terragrunt-apply (terragrunt role, env terraform-apply)
   build+deploy voice|auth (release/deploy roles, path-filtered)
```

### Recommended Project Structure

```
klanker-voice/
├── AGENTS.md                      # REQUIRED: root terragrunt.hcl anchors repo_root on this file
├── infra/
│   ├── .envrc                     # direnv: TG_*, TF_VAR_*, SGUID (non-secret, checked in)
│   └── terraform/
│       ├── providers/{global.hcl,regional.hcl}
│       ├── modules/<name>/{config.hcl,v1.0.0/}     # 11 modules, verbatim copies
│       └── live/site/
│           ├── terragrunt.hcl  site.hcl  SECRETS.md
│           ├── .secrets.sops.json[.template]
│           ├── global/{github-oidc/,waf/waf.hcl}   # waf.hcl kept as data (root reads it)
│           ├── services/{auth,voice}/service.hcl
│           └── region/{skip.hcl,us-east-1/...}
├── scripts/bootstrap-state.sh
├── .sops.yaml                     # repo root (dc34 precedent)
└── .github/workflows/
```

### Pattern 1: Env-driven backend + CI/local provider switching (copy verbatim)

**What:** `providers/global.hcl` and `providers/regional.hcl` generate provider.tf and backend config. Locally, profiles `${prefix}-application/-management/-terraform` are written into providers; when `CI=true`, profile lines are omitted (creds from OIDC) and the management provider gains `assume_role { role_arn = arn:aws:iam::<MGMT>:role/<label>-github-delegate, external_id = <label> }`.
**When to use:** Unchanged — this is the core machinery D-03 preserves.
**Key values consumed:** `TG_BUCKET_USE1`/`TG_TABLE_USE1` (bucket/table names), `TF_VAR_MANAGEMENT_ACCOUNT_ID`, `TF_VAR_profile_prefix`. [VERIFIED: dc34 providers/global.hcl, providers/regional.hcl]

```hcl
# Source: dc34 providers/global.hcl (verbatim mechanism)
application_profile = local.profile_prefix != "" ? "${local.profile_prefix}-application" : "application"
# ⚠ prefix must be "klanker" (NO trailing dash) to yield "klanker-application"
management_delegate_role = "${local.site_config.locals.site.label}-github-delegate"   # → "kmv-github-delegate"
```

### Pattern 2: service.hcl data files aggregated by site.hcl

**What:** Each service is a pure-data `services/<name>/service.hcl` exposing locals: `ecr_repositories`, `task` (containers with `{{SITE_DOMAIN}}`/`{{SITE_LABEL}}`/`{{REGION_LABEL}}`/`{{REGION}}` placeholders substituted by the ecs-task module), `service` (ALB listener host_headers, autoscaling), `dynamodb.tables`. site.hcl `read_terragrunt_config()`s each one and concats their lists into the `dynamodb`/`ecr`/`ecs_tasks`/`ecs_services` feature blocks. [VERIFIED: dc34 site.hcl + services/run.auth/service.hcl]
**When to use:** Create `services/auth/service.hcl` and `services/voice/service.hcl` **stubs now** with `ecr_repositories` populated (so ECR repos exist before Phase 3 CI pushes) and full task/service locals as best-guess placeholders; keep `ecs_tasks.enabled = false` and `ecs_services.enabled = false` at site level until Phases 3/4. The ecs-task/ecs-service terragrunt units exclude themselves when the flag is false [VERIFIED: dc34 region/us-east-1/ecs-service/terragrunt.hcl exclude block].
**Gotcha:** dc34's run.auth service.hcl calls `file("VERSION.nginx")` — stubs that use `trimspace(file(...))` need the VERSION files to exist or must hardcode a tag until Phase 3; site.hcl reads every service.hcl unconditionally, so a missing file() target breaks ALL plans.

### Pattern 3: Inline live-tree unit for one-off resources (dc34 `bib-secrets` precedent)

**What:** dc34 has `region/us-east-1/bib-secrets/{main.tf,terragrunt.hcl}` — a tiny Terraform unit living directly in the live tree, no versioned module. [VERIFIED: dc34 tree listing]
**When to use:** The apex-domain DMARC record for klankermaker.ai (D-11) — one `aws_route53_record` with the global-management provider — without violating D-02 (modules stay verbatim).

### Pattern 4: Region skip machinery (keep as-is)

**What:** `region/skip.hcl` reads `site.skip_regions` and emits `exclude { if = should_skip, actions = ["all"] }`; every regional unit includes it. kmv sets `skip_regions = ["ap-southeast-1", "ca-central-1"]` and simply **does not copy** those region folders — us-east-1 only. [VERIFIED: dc34 skip.hcl]

### Anti-Patterns to Avoid
- **Setting `TF_VAR_profile_prefix="klanker-"`:** providers compute `"${prefix}-application"` → `klanker--application` (double dash) → every provider fails auth. Use `klanker`. The design spec and CONTEXT.md both carry the trailing-dash typo; profiles in ~/.aws/config are `klanker-application` etc. [VERIFIED: providers/global.hcl line 25 + ~/.aws/config]
- **Dropping AGENTS.md:** root terragrunt.hcl computes `repo_root = dirname(find_in_parent_folders("AGENTS.md"))`. No AGENTS.md at klanker-voice root → root unit fails to parse. Either add AGENTS.md (dc34 has one) or change the anchor filename during copy.
- **Deleting global/waf/waf.hcl because WAF is out of scope:** root terragrunt.hcl unconditionally `read_terragrunt_config(".../global/waf/waf.hcl")`. Keep the file as data (waf.enabled=false in site.hcl means no WAF resources) or trim the root's waf merge — keeping the file is the smaller diff.
- **Renaming while copying:** do a pure copy, then a scripted find/replace pass for the delta table below, then `grep -ri 'dc34\|defcon' infra/` must return only intentional comments (ideally zero).
- **Applying github-oidc before the SOPS key exists:** the readonly/deploy/release roles interpolate `TF_VAR_SOPS_KMS_KEY_ID` into kms-sops-decrypt policies; with the placeholder default the policy references `mrk-000...` and each later apply drifts IAM. Create the KMS key first, persist the ID, then apply github-oidc. [VERIFIED: dc34 site.hcl kms-sops-decrypt + env.sops.sh Step 7 comments]

## Clone Inventory (exact file list)

Source root: `/Users/khundeck/working/defcon.run.34/infra/terraform/`. Destination: `infra/terraform/` in klanker-voice. Never copy `.terragrunt-cache/`, `.terraform.lock.hcl` (regenerate), `.DS_Store`.

### Providers (copy verbatim, zero edits)
| File | Notes |
|------|-------|
| `providers/global.hcl` | Backend key `use1/<path>/tf.global.tfstate`; regional aliases use1/cac1/apse1 kept per D-03 |
| `providers/regional.hcl` | Backend key `<label>/<path>/terraform.tfstate`; reads region.hcl |

### Live tree
| File | Copy mode | kmv edits |
|------|-----------|-----------|
| `live/site/terragrunt.hcl` | verbatim | none needed IF AGENTS.md exists at repo root and global/waf/waf.hcl is kept |
| `live/site/site.hcl` | **rewrite** | full delta table below |
| `live/site/SECRETS.md` | adapt | note: dc34's doc references stale filenames (`.secrets.enc.json`); actual mechanism in site.hcl is `.secrets.sops.json` — fix while copying |
| `live/site/.secrets.sops.json.template` | **rewrite** | kmv secret shape (below) |
| `live/site/global/github-oidc/terragrunt.hcl` | verbatim | none (reads site.hcl) |
| `live/site/global/waf/waf.hcl` | verbatim (data only) | none |
| `live/site/global/cloudfront/`, `global/cloudtrail/` | **DO NOT COPY** | D-04 |
| `live/site/region/skip.hcl` | verbatim | none |
| `live/site/region/us-east-1/region.hcl` | verbatim | none (derives use1 from folder name) |
| `live/site/region/us-east-1/{certs,network,ecs-cluster,ecr,dynamodb,secrets,email,ecs-task,ecs-service}/terragrunt.hcl` | verbatim | ecs-service/terragrunt.hcl: remove the mqtt `nlb_default_certificate_arn` line or leave (try() returns "" when mqtt cert absent — safe as-is); mock_outputs may keep dc34 names (mocks only) |
| `live/site/region/us-east-1/network/network.hcl` | adapt | dc34 defaults fine (10.0.0.0/16, 2 AZ, ALB on); set `nlb.enabled = false` (no MQTT); ALB idle timeout note below |
| `live/site/region/us-east-1/dynamodb/dynamodb.hcl`, `ecr/ecr.hcl`, `ecs-cluster/ecs-cluster.hcl`, `ecs-task/ecs-task.hcl`, `secrets/secrets.hcl` | verbatim | thin locals wrappers, no site-specific values [VERIFIED: secrets.hcl read] |
| `live/site/region/us-east-1/email/email.hcl` | **rewrite** | drop bib receive_rules; keep fwd_rules minimal; keep forwarder_lambda_source_path + copy `email/lambdas/email-forwarder/index.py` |
| `live/site/region/us-east-1/{mqtt,waffaw,s3-uploads*,ec2spot,cloudfront,status-site,bib-reconcile,bib-secrets,email-s3-replication}/` | **DO NOT COPY** | D-04 |
| `live/site/services/{auth,voice}/service.hcl` | **new** (model on run.auth) | see Pattern 2 |
| `live/site/region/us-east-1/dmarc/{main.tf,terragrunt.hcl}` | **new** (bib-secrets pattern) | apex `_dmarc.klankermaker.ai` TXT in mgmt zone (see SES section) |

### Modules (all: `config.hcl` + `v1.0.0/` — copy verbatim, ZERO edits per D-02)
| Module | v1.0.0 contents | Role |
|--------|-----------------|------|
| `site` | route35.tf (subdomain zones in app acct + NS records in mgmt zone), waf.tf, outputs (zone_map) | Root unit; cross-account DNS |
| `certs` | acm.tf, outputs, variables | Per-subdomain certs (`auth.` + `*.auth.`, `voice.` + `*.voice.`) validated in app zones; site cert (`klankermaker.ai` + `*`, `use1`, `*.use1` SANs) validated in mgmt zone |
| `network` | vpc.tf, alb.tf, nlb.tf, natgw.tf, securitygroups.tf, vpc_endpoints.tf, vpc_flowlogs.tf, data.tf, outputs | VPC/subnets/ALB/SGs; SG names embed `${region.label}.${dns.zonename}` — renames flow from config |
| `ecs-cluster` | main/outputs/variables | Fargate cluster "app" + Cloud Map namespace `app-use1-kmv.local` |
| `ecr` | main/outputs/variables | Repos from concat of service ecr_repositories |
| `dynamodb` | main.tf, iam.tf, kms.tf, ssm.tf | Tables + per-table IAM users + creds published at `/kmv/dynamodb/use1/<table>/{table_name,access_key_id,secret_access_key,...}` [VERIFIED: ssm.tf naming] |
| `secrets` | ssm.tf, kms.tf, locals.tf, secretsmanager.tf | SecureStrings `/kmv/secrets/use1/<name>/<key>` (ssm_prefix template `/{{SITE_LABEL}}/secrets/{{REGION_LABEL}}`), own regional KMS CMK [VERIFIED] |
| `email` | ses.tf, ses-domain/ (submodule), s3.tf, receive.tf, forwarding.tf, iam.tf, kms.tf, ssm.tf | See SES section |
| `github-oidc` | main/outputs/variables | Roles named `${label}-github-${role.name}`; outputs `management_account_trust_policy` [VERIFIED: main.tf line 54, outputs.tf] |
| `ecs-task` | main/outputs/variables | Task defs; `{{...}}` placeholder substitution for env/secrets/healthcheck commands; task-role SSM read policy [VERIFIED: main.tf 205-253] |
| `ecs-service` | main/outputs/variables | Per-service `assign_public_ip` selects public subnets [VERIFIED: main.tf line 59]; SGs come from the module-level `security_group_ids` list (all services share the network module's SG output — see WebRTC section) |

Modules **not** copied (D-04): cloudtrail, waffaw, mqtt, ec2spot, s3-uploads, s3-uploads-processor, s3-uploads-replication, cloudfront, cloudfront-assets, bib-reconcile-lambda, email-s3-replication, nlb-dns, status-site, strava-sync-scheduler.

### Repo-root support files
| File | Copy mode | Notes |
|------|-----------|-------|
| `.sops.yaml` | rewrite | single-region: `kms: "arn:aws:kms:us-east-1:052251888500:alias/sops"` (dc34 lists 3 regional ARNs) [VERIFIED: dc34 .sops.yaml] |
| `env.sops.sh` | adapt → `scripts/setup-sops.sh` (or fold into bootstrap) | drop `REPLICA_REGIONS` loop + `--multi-region` flag for single-region key (D-03 allows); keep alias check, .sops.yaml writer, template-encrypt, TF_VAR persistence, `gh variable set` printout |
| `env.sh` | reference only → `infra/.envrc` | see State Bootstrap section |
| `AGENTS.md` | must exist at repo root | terragrunt anchor |

## site.hcl Delta Table (dc34 → kmv)

| Key | dc34 value | kmv value |
|-----|-----------|-----------|
| `site.label` | `"dc34"` | `"kmv"` |
| `site.github_repo_name` | `"defcon.run.34"` | `"klanker-voice"` (repo NAME only — org comes from `TF_VAR_GITHUB_ORG=whereiskurt`) [VERIFIED: dc34 site.hcl github_org = get_env("TF_VAR_GITHUB_ORG")] |
| `site.tf_state_prefix` | `"tf-dc34"` | `"tf-kmv"` |
| `site.random_suffix` | `get_env("SGUID", "80a6b349")` | `get_env("SGUID", "<new 8-hex chosen at bootstrap>")` — default must match the bootstrap-created bucket suffix |
| `site.skip_regions` | `["ap-southeast-1","ca-central-1"]` | same (keeps multi-region machinery inert) |
| `dns.zonename` | `"defcon.run"` | `"klankermaker.ai"` |
| `dns.subdomains` | 8 subdomains | `["auth", "voice"]` |
| `urls.subdomains` / `local_ports` | 5 services | `{ auth = "auth", voice = "voice" }`; local_ports `{ auth = 3002, voice = 7860 }` (voice port = pipecat dev default; discretionary) |
| `service_conf` | 7 read_terragrunt_config entries | 2: `auth = read_terragrunt_config("./services/auth/service.hcl")`, `voice = .../voice/service.hcl` |
| `email` block | 3 zonenames, replica_regions, receive-heavy | keep `enabled=true`, `primary_region="us-east-1"`, `zonenames = ["auth.klankermaker.ai"]`, `smtp_prefix="s"`, `make_site_domain` — **see SES section decision**, `make_regional_domains=false`, `make_domains=true`, `replica_regions=[{use1}]` only, `smtp_iam_users=["auth.klankermaker.ai"]`, minimal fwd_rules |
| `waf` | `enabled=false` | same |
| `cloudfront` | `enabled=true`, big block | `enabled = false` (block must remain present — root terragrunt merges it; keep minimal) — **verify**: nothing in-scope reads cloudfront.* except root/site module which tolerates disabled |
| `ec2spots` | `enabled=false` | drop to `enabled=false, instances=[]` |
| `ecs_clusters` | cluster "app", 3 regions | cluster "app", `regions=["us-east-1"]` |
| `dynamodb.tables` | concat of 3 services | `concat(local.service_conf.auth.locals.dynamodb.tables, local.service_conf.voice.locals.dynamodb.tables)` (voice may expose `tables = []` in Phase 2) |
| `ecr.repositories` | concat of 7 + waffaw | concat auth + voice |
| `ecs_tasks` | `enabled=true`, 8 tasks | **`enabled=false`**, `tasks=[]` (or concat of stubs) until Phase 3 |
| `ecs_services` | `enabled=true`, 8 services | **`enabled=false`**, `services=[]` until Phase 3 |
| `user_uploads` / `upload_processors` / `waffaw` / `cloudtrail` | present | delete blocks (their modules aren't copied; nothing else references them — root terragrunt only merges site_vars into the SITE module inputs, and site module variables must be checked: if site/v1.0.0/variables.tf declares them optional, deletion is safe — **plan-time check**) |
| `secrets.definitions` | 13 defs (discord, strava, strapi...) | kmv v1: `deepgram = {keys=["api_key"]}`, `anthropic = {keys=["api_key"]}`, `elevenlabs = {keys=["api_key"]}`, `jwt = {keys=["secret","internal_secret"]}`, `oidc = {keys=["cookie_keys"]}`, `altcha = {keys=["secret"]}` (auth needs jwt/oidc/altcha in Phase 3 — creating paths now avoids a secrets re-apply) |
| `secrets.ssm_prefix` | `/{{SITE_LABEL}}/secrets/{{REGION_LABEL}}` | same → `/kmv/secrets/use1` |
| `secrets.replica_regions` | cac1+apse1 | `[]` |
| `github_oidc` | full block | keep structure; `ec2_runner_instance_profile.enabled = false` (no self-hosted runners); prune role list to terragrunt/readonly/deploy/release (+application optional); **fix multi-region KMS ARN lists** in the three `kms-sops-decrypt` inline policies → single us-east-1 ARN; state-lock/S3 resources already keyed off `tf_state_prefix` so they rename automatically |

**Grep gates after rewrite:** `grep -ri "kmk" infra/ → 0`; `grep -ri "voiceai" infra/ → 0`; `grep -ri "dc34\|defcon" infra/ → 0` (mock_outputs excepted if kept).

## SOPS Setup (single-region variant)

Verified dc34 flow [VERIFIED: dc34 site.hcl lines 11-19, env.sops.sh, .sops.yaml]:

1. **site.hcl decrypt-on-plan:** `secret_values = jsondecode(run_cmd("--terragrunt-quiet", "sops", "--decrypt", "<dir>/.secrets.sops.json"))` with plaintext `.secrets.json` fallback. Copy verbatim.
2. **KMS key (kmv, single-region — D-03 simplification):**
   ```bash
   aws kms create-key --profile klanker-terraform --region us-east-1 \
     --description "SOPS secrets encryption (kmv)"          # NO --multi-region
   aws kms create-alias --alias-name alias/sops --target-key-id <KeyId> \
     --profile klanker-terraform --region us-east-1
   ```
   Key ID will be a plain UUID (not `mrk-`) — the site.hcl kms-sops-decrypt policies interpolate it into a `key/<id>` ARN, format-agnostic.
3. **`.sops.yaml` (repo root):**
   ```yaml
   creation_rules:
     - path_regex: \.secrets(\.sops)?\.json$
       kms: "arn:aws:kms:us-east-1:052251888500:alias/sops"
   ```
4. **`.secrets.sops.json.template` shape (kmv):**
   ```json
   {
     "deepgram":   { "api_key": "CHANGEME" },
     "anthropic":  { "api_key": "CHANGEME" },
     "elevenlabs": { "api_key": "CHANGEME" },
     "jwt":        { "secret": "CHANGEME", "internal_secret": "CHANGEME" },
     "oidc":       { "cookie_keys": "CHANGEME" },
     "altcha":     { "secret": "CHANGEME" }
   }
   ```
5. **Bootstrap-param migration (exact flow):**
   ```bash
   cp infra/terraform/live/site/.secrets.sops.json.template /tmp/.secrets.json
   for k in deepgram anthropic elevenlabs; do
     aws ssm get-parameter --profile klanker-application --region us-east-1 \
       --name "/kmv/bootstrap/${k}_api_key" --with-decryption --query Parameter.Value --output text
   done   # paste values into /tmp/.secrets.json
   sops encrypt /tmp/.secrets.json > infra/terraform/live/site/.secrets.sops.json && rm /tmp/.secrets.json
   # apply the secrets unit → verify /kmv/secrets/use1/{deepgram,anthropic,elevenlabs}/api_key exist
   # ONLY THEN:
   aws ssm delete-parameter --name /kmv/bootstrap/deepgram_api_key ... (each)
   ```
   Note: elevenlabs bootstrap param is "in progress" per CONTEXT — migration step must tolerate a missing param (leave CHANGEME, fill later via `sops edit`).
6. **Persist + CI:** upsert `TF_VAR_SOPS_KMS_KEY_ID=<KeyId>` into `infra/.envrc`; `gh variable set TF_VAR_SOPS_KMS_KEY_ID`. `TF_VAR_SSM_KMS_KEY_ARNS` starts empty; after secrets/dynamodb/email modules apply (each creates its own regional CMK per env.sops.sh comments), re-discover aliases (`alias/kmv-*ssm*`) and set the var — otherwise the CI readonly role gets AccessDenied on kms:Decrypt during PR plans [VERIFIED: dc34 site.hcl kms-sops-decrypt comment block].

## State Bootstrap

**Naming contract** [VERIFIED: dc34 env.sh lines 41-48 + terragrunt-plan.yml env block]: bucket and table share one name: `tf-${SITE_LABEL}-${region_label}-${SGUID}` → **`tf-kmv-use1-<sguid>`** where SGUID is a fixed 8-hex string (dc34: first 8 of a uuid). providers/*.hcl read `TG_BUCKET_USE1` / `TG_TABLE_USE1` env vars verbatim; CI reconstructs them from repo vars `SITE_LABEL` + `SGUID`.

**`scripts/bootstrap-state.sh` checklist (klanker-terraform profile, us-east-1, idempotent):**
1. Pick/accept SGUID (arg or generate once; echo prominently — it becomes site.hcl default + repo var).
2. `aws s3api create-bucket tf-kmv-use1-$SGUID` (us-east-1: no LocationConstraint) — skip if exists.
3. `put-bucket-versioning Enabled`; `put-bucket-encryption` (SSE-S3 or aws:kms); `put-public-access-block` (all four true).
4. `aws dynamodb create-table tf-kmv-use1-$SGUID --attribute-definitions AttributeName=LockID,AttributeType=S --key-schema AttributeName=LockID,KeyType=HASH --billing-mode PAY_PER_REQUEST` — skip if exists.
5. Print: `export TG_BUCKET_USE1=... TG_TABLE_USE1=... SGUID=...` and matching `gh variable set` commands.

**`infra/.envrc` contents (checked in, non-secret; direnv):**
```bash
export SITE_LABEL=kmv
export SGUID=<fixed-8-hex>
export TG_BUCKET_USE1="tf-${SITE_LABEL}-use1-${SGUID}"
export TG_TABLE_USE1="tf-${SITE_LABEL}-use1-${SGUID}"
export AWS_REGION=us-east-1
export TF_VAR_APPLICATION_ACCOUNT_ID=052251888500
export TF_VAR_MANAGEMENT_ACCOUNT_ID=481723467561
export TF_VAR_profile_prefix=klanker          # NOT "klanker-" — providers append the dash
export TF_VAR_GITHUB_ORG=whereiskurt
export TF_VAR_SOPS_KMS_KEY_ID=<filled after setup-sops>
export TF_VAR_SSM_KMS_KEY_ARNS=               # filled after module CMKs exist
# optional: export TF_VAR_FWD_EMAIL_TO_ADDRESS=whereiskurt@gmail.com
```
Do **not** copy dc34 env.sh's `aws sso login` / `export-credentials` lines into .envrc (direnv runs on every cd; login is interactive). Document them in README/scripts instead. Note dc34's env.sh exports terraform-profile creds into the ambient env for terragrunt's backend bootstrapping; with the bucket/table pre-created by the script and `profile = terraform_profile` in remote_state config, this should be unnecessary — verify on first `terragrunt init` [ASSUMED].

## Cross-Account DNS & Certs

Mechanism verified in source:
- **site module** (`route35.tf`): `data.aws_route53_zone.mgmt` looks up `klankermaker.ai` **by name** via `aws.global-management`; creates `auth.klankermaker.ai` + `voice.klankermaker.ai` hosted zones in the app account (052251888500); writes NS delegation records into the mgmt zone. [VERIFIED]
- **certs module** (`acm.tf`): subdomain certs (`auth.` + SAN `*.auth.`, `voice.` + `*.voice.`) validate via records in the app-account subdomain zones (aws.application). The primary-zone cert (`klankermaker.ai` + SANs `*.`, `use1.`, `*.use1.`) validates via records written to the **mgmt zone** (aws.global-management) — `make_site_cert = true` in the region certs terragrunt.hcl. [VERIFIED]
- **email module ses-domain**: writes TXT/CNAME/MX records through whatever is bound to its `aws.global-management` alias — for subdomain identities dc34 binds it to the **application** provider (records land in the app-account subdomain zone); only the apex identity binds real global-management. [VERIFIED: dc34 email/v1.0.0/ses.tf provider maps]

**Management-permission audit (in-scope modules only):** the only resources on management providers are Route53 `data` lookup + `aws_route53_record` writes (site NS records, site-cert ACM validation, apex SES records if enabled). Grep across certs/network/ecs-cluster/ecr/dynamodb/secrets/email/github-oidc/ecs-task/ecs-service/site confirmed **no IAM/ACM/other management-account resources**. [VERIFIED: grep for `aws.global-management|aws.management`]

**Profile fit:** local applies use profile `klanker-management` directly (SSO role **HostedZoneAdmin** on 481723467561 — confirmed in ~/.aws/config). Required actions: `route53:ListHostedZones`/`GetHostedZone` (data lookup by name) + `ChangeResourceRecordSets`/`ListResourceRecordSets` on the zone. A typical HostedZoneAdmin permission set covers these — but it is a custom SSO permission set; **verify with one `aws route53 list-hosted-zones --profile klanker-management` before the first apply** [ASSUMED: exact policy contents unseen].

**CI cross-account:** when `CI=true`, the generated management provider does `assume_role` on `arn:aws:iam::481723467561:role/kmv-github-delegate` with `external_id = "kmv"`. That role **does not exist yet** and cannot be created by any in-scope profile (klanker-management is HostedZoneAdmin, not IAM-admin). The github-oidc module outputs `management_account_trust_policy` JSON for it. [VERIFIED: providers/*.hcl + github-oidc outputs.tf]
→ **User action required:** create `kmv-github-delegate` in 481723467561 (trust = module output; permissions = Route53 change/list on zone Z036807010CWM2JH60RKQ) using whatever admin access exists for that account. Until then: CI plans of units that touch the management provider will fail — infra applies stay local-only (acceptable; D-08 gates applies anyway, but document it).

## SES / Email (INFR-04)

What the ses-domain submodule creates **per identity** [VERIFIED: email/v1.0.0/ses-domain/{ses.tf,route53.tf}]:
- `aws_ses_domain_identity` + verification TXT record
- Easy-DKIM: 3 CNAMEs `<token>._domainkey.<domain>` → `<token>.dkim.amazonses.com`
- MAIL FROM domain `s.<domain>` with MX (`feedback-smtp.us-east-1.amazonses.com`) + SPF TXT (`v=spf1 include:amazonses.com ~all`)
- DMARC TXT `_dmarc.<domain>`: `v=DMARC1; p=quarantine; rua=mailto:dmarc-reports@<domain>; ruf=...; sp=none; aspf=r; adkim=r;` — **already p=quarantine, matching D-11**
- Receive MX (`inbound-smtp.us-east-1.amazonaws.com`) — hardcoded `enable_receive_mx = true` in all ses.tf call sites
- Plus (top-level email module): receipt rule set `kmv-email`, received-emails S3 bucket, forwarding Lambda (fwd_rules), SMTP IAM users, SSM params (e.g. `/kmv/ses/from_address` pattern used by dc34 service secrets)

**kmv configuration for `sign-in@auth.klankermaker.ai` (D-10):** `email.zonenames = ["auth.klankermaker.ai"]` with `make_domains = true` → SES identity for `auth.klankermaker.ai`; DNS records land in the **app-account** `auth.klankermaker.ai` zone (ses_root binds global-management→global-application). Any local part (sign-in@) is covered by the domain identity. DKIM signs with `d=auth.klankermaker.ai`; SPF aligns via MAIL FROM `s.auth.klankermaker.ai`. `make_regional_domains = false` (no `use1.auth...` identities needed).

**Org-domain DMARC (D-11) decision point:** the module only writes DMARC per identity (`_dmarc.auth.klankermaker.ai`). D-11 wants `_dmarc.klankermaker.ai` p=quarantine. Two routes:
1. `make_site_domain = true` → full SES identity for the apex incl. DMARC in the mgmt zone — **but also creates an apex receive MX** (`inbound-smtp....`), taking over inbound mail for klankermaker.ai, plus apex MAIL FROM records. If any existing mail service uses the apex, this breaks it. [VERIFIED: enable_receive_mx=true hardcoded]
2. **Recommended:** `make_site_domain = false` + a tiny inline unit (`region/us-east-1/dmarc/`, bib-secrets pattern) writing one TXT `_dmarc.klankermaker.ai` = `v=DMARC1; p=quarantine; rua=mailto:dmarc-reports@auth.klankermaker.ai; aspf=r; adkim=r;` via the global-management provider. With relaxed alignment, DKIM d=auth.klankermaker.ai aligns with the org domain, so subdomain sends pass org DMARC.
Confirm the apex-mail question with the user before choosing route 1 (see Runtime State Inventory / Open Questions).

## GitHub OIDC (INFR-07)

Verified role/pattern facts [VERIFIED: github-oidc module main.tf + site.hcl github_oidc block + workflows]:
- IAM roles named `kmv-github-<name>`; dc34 defines terragrunt, application, readonly, prowler, e2e, release, deploy. **kmv keeps: terragrunt (infra apply), readonly (PR/infra plan), deploy (ECS update), release (ECR push + build)**; application optional; drop prowler/e2e for v1.
- Trust: OIDC provider `token.actions.githubusercontent.com`; repo scoping from `github_org` (`TF_VAR_GITHUB_ORG` env → `whereiskurt`) + `github_repo` (site.hcl `github_repo_name` → `klanker-voice`). Restrictions: `environment_restriction = "terraform-apply"` on terragrunt role; `branch_restriction = "main"` on deploy/application.
- kmv edits inside the (site.hcl-resident, so editable) github_oidc block: single-region KMS ARN lists in the three `kms-sops-decrypt` inline policies (drop cac1/apse1 entries); `ec2_runner_instance_profile.enabled = false`; state/lock resource ARNs auto-rename via `tf_state_prefix`; strip the run-* / bib-specific PassRole patterns in the release role's `iam-ecs-roles` policy to kmv task/execution role patterns (naming from ecs-task module: `ecs-task-role-*-kmv-*`, `ecs-execution-role-*-kmv-*` — confirm exact patterns against ecs-task/v1.0.0/main.tf during implementation).
- Cross-account: `cross_account_arns` on terragrunt + readonly roles point at `arn:aws:iam::481723467561:role/kmv-github-delegate` (see DNS section — manual mgmt-account step).
- GitHub side: environments `terraform-plan` and `terraform-apply` (apply environment gets **required reviewers** = the human gate D-08 demands); repo variables `SITE_LABEL=kmv`, `SGUID`, `AWS_ACCOUNT_ID=052251888500`, `TF_VAR_MANAGEMENT_ACCOUNT_ID=481723467561`, `TF_VAR_SOPS_KMS_KEY_ID`, `TF_VAR_SSM_KMS_KEY_ARNS`.

## GitHub Actions (workflows to clone/adapt)

Source: `/Users/khundeck/working/defcon.run.34/.github/workflows/` [VERIFIED: read headers + grep of role/environment/paths]:

| dc34 file | Role assumed | Environment | kmv adaptation |
|-----------|--------------|-------------|----------------|
| `terragrunt-plan.yml` (321 lines) | `dc34-github-readonly` | `terraform-plan` | → `kmv-github-readonly`; change PR path trigger from `infra/**/VERSION.*` to `infra/**` (D-08: any infra change plans); single-region env block (drop CAC1/APSE1 exports); keep tool cache pins (tg 0.97.1 / tf 1.14.3 / sops 3.11.0) |
| `terragrunt-apply.yml` (286 lines) | `dc34-github-terragrunt` | `terraform-apply` (env gate = manual approval) | → `kmv-github-terragrunt`; workflow_dispatch + workflow_call retained; this IS the human-gated apply |
| `buildpub.yml` (879 lines) | `dc34-github-release` | — | Heavy (version bump, PR merge, EC2 runners, multi-region). **Recommend NOT cloning wholesale**: write two lean `build-voice.yml` / `build-auth.yml` (push-to-main, `paths: apps/voice/**` etc.) that OIDC-assume `kmv-github-release`, docker build/push to ECR, then trigger deploy — Claude's-discretion area per CONTEXT |
| `deploy.yml` (209 lines) | `dc34-github-deploy` | — | Terragrunt-driven ECS update of ecs-task/ecs-service units; clone/trim to single region. Deploy workflows are inert until Phase 3 enables services — ship them anyway so Phase 3 only flips site.hcl flags |
| `checkov-scan.yml`, `gitleaks-scan.yml` | — | — | Optional but cheap; gitleaks especially sensible for a public repo (D-07) |
| `rollback.yml`, `e2e-tests.yml`, `ec2-runner.yml`, `prowler-scan.yml`, `npm-audit.yml` | — | — | Skip for v1 |

Common workflow env (from terragrunt-plan.yml, reproduce): `CI=true` implicit; `TG_BUCKET_USE1: tf-${{ vars.SITE_LABEL }}-use1-${{ vars.SGUID }}` (same for table); `TF_VAR_APPLICATION_ACCOUNT_ID: ${{ vars.AWS_ACCOUNT_ID }}`; `TF_VAR_GITHUB_ORG: ${{ github.repository_owner }}`; `permissions: id-token: write, contents: read`.

## WebRTC Groundwork (structure now, verify in Phase 4)

- **Public IP:** already supported — `ecs_services[*].assign_public_ip = true` places the task in public subnets and enables the public-IP flag [VERIFIED: ecs-service variables.tf line 37, main.tf line 59]. Phase 2 needs no change; the voice service.hcl stub sets it.
- **UDP SG:** the network module's `security_group_ids` output is `[sshhttps, http_only, (+nlb)]` — no UDP anywhere [VERIFIED: securitygroups.tf + outputs.tf]. **All services in a region share this SG list** (module-level input, not per-service). Options for where the knob lands (Claude's discretion per CONTEXT):
  - *Recommended:* add one `aws_security_group "webrtc_udp"` (ingress UDP wide range, e.g. 1024–65535 per PITFALLS research — must match what aiortc actually binds, and aiortc binds OS-ephemeral ports) to network/securitygroups.tf + append to the output. This is a functional addition to a copied module — technically beyond "naming-only", but it is the design-spec-sanctioned "only infra delta" and CONTEXT explicitly delegates its placement to Claude. Alternative that keeps modules pristine: define the SG in an inline live-tree unit and pass it via a new ecs-service input — more moving parts, worse.
  - Since Phase 2 has `ecs_services.enabled=false`, the shared-SG blast radius question (auth tasks also getting the UDP SG) can be deferred; note it for Phase 3/4 (per-service SGs would need an ecs-service module extension).
- **Fargate sysctl (open question resolved):** ECS **does support** `systemControls` with namespaced `net.*` sysctls — explicitly including `net.ipv4.ip_local_port_range` (e.g. `"1024 65000"`) — on Fargate **platform version 1.4.0+ (Linux)**; network-namespace sysctls apply to all containers in the task. [VERIFIED: AWS docs task_definition_parameters + AWS containers blog "Announcing additional Linux controls for Amazon ECS tasks on AWS Fargate", via WebSearch 2026-07-04 — MEDIUM per confidence seam]. So the bounded-port option (sysctl `net.ipv4.ip_local_port_range = "20000 20100"` + matching narrow SG) is viable for Phase 4. Caveats: (a) dc34's ecs-task module has **no systemControls support** — Phase 4 would add the field to container defs (grep confirmed absent); (b) terraform-provider-aws issue #40034 reported systemControls rejected for Fargate in provider 5.74.0 — check provider version when implementing [CITED: github.com/hashicorp/terraform-provider-aws/issues/40034]. **Phase 2 does not block on this** — ship the wide-UDP SG; note sysctl as Phase 4's tightening path.
- **ALB idle timeout:** PITFALLS.md flags raising it ≥ max session length (e.g. 2400s) for any ALB-traversing control channel — check whether network module's alb.tf exposes idle_timeout; if it's an easy input, set it now; otherwise Phase 4.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Backend/provider generation, CI profile switching | New terragrunt root config | dc34 providers/*.hcl verbatim | Handles CI assume-role, profile prefixes, per-region backends — battle-tested at DEF CON |
| Cross-account DNS delegation + ACM validation | Manual Route53/ACM clicking or new TF | dc34 site + certs modules | Provider-alias plumbing (app vs mgmt) is the hard part and is already correct |
| SES DKIM/SPF/DMARC record set | Hand-written route53 records | email module ses-domain submodule | Creates the full verified-identity record set incl. DMARC p=quarantine |
| Secrets fan-out to SSM | Per-secret aws_ssm_parameter resources | secrets module definitions map | One definitions block → all SecureStrings, KMS CMK included |
| GitHub OIDC trust policies | Hand-rolled IAM trust JSON | github-oidc module | Environment/branch restrictions, managed-policy splitting (10KB limit workaround), cross-account delegate output |
| SOPS key + file lifecycle | Ad-hoc kms/sops commands | env.sops.sh adapted single-region | Idempotence checks, .sops.yaml writer, template re-encrypt, TF_VAR persistence, CI var printout all already written |

**Key insight:** every "how do I wire X" question in this phase has a working answer in the dc34 tree; the planner's job is sequencing and renaming, not design.

## Runtime State Inventory

(This is a clone-into-new-site phase, but it touches live AWS state.)

| Category | Items Found | Action Required |
|----------|-------------|------------------|
| Stored data | `/kmv/bootstrap/{deepgram,anthropic,elevenlabs}_api_key` SSM params in 052251888500 (deepgram exists; elevenlabs in progress per CONTEXT) | Data migration: values → .secrets.sops.json → secrets module → delete bootstrap params (§SOPS step 5); tolerate missing elevenlabs |
| Live service config | klankermaker.ai zone Z036807010CWM2JH60RKQ in 481723467561 — unknown existing records (apex MX? existing A/TXT?) | Read-only audit before first apply: `aws route53 list-resource-record-sets --hosted-zone-id Z036807010CWM2JH60RKQ --profile klanker-management`; decides the apex-DMARC route and detects NS-delegation collisions |
| Live service config | SES account state in 052251888500: out of sandbox with raised quota (D-12) | None — verify once with `aws sesv2 get-account --profile klanker-application` and record quota |
| OS-registered state | None — no schedulers/daemons involved | None |
| Secrets/env vars | AWS SSO profiles klanker-{application,management,terraform} exist in ~/.aws/config (verified); no long-lived keys | None — providers reference them by constructed name |
| Build artifacts | None yet (no ECR repos, no state bucket) — bucket/table `tf-kmv-use1-*` must not pre-exist with different SGUID | bootstrap-state.sh existence check handles it |

## Common Pitfalls

### Pitfall 1: `TF_VAR_profile_prefix` trailing dash
**What goes wrong:** D-06/design-spec write `klanker-`; providers compute `${prefix}-application` → `klanker--application`; every AWS call fails with profile-not-found.
**How to avoid:** `.envrc` sets `TF_VAR_profile_prefix=klanker`. Add a plan-time sanity note in README.
**Warning sign:** `failed to get shared config profile, klanker--application`.

### Pitfall 2: Missing AGENTS.md root anchor
**What goes wrong:** root terragrunt.hcl `find_in_parent_folders("AGENTS.md")` errors, killing every run from the site root.
**How to avoid:** create AGENTS.md at klanker-voice root during the copy task (any content), or change the anchor to a file that exists.

### Pitfall 3: SGUID drift between bucket, site.hcl default, and CI vars
**What goes wrong:** `site.random_suffix` default, the bootstrap-created bucket suffix, and GitHub `vars.SGUID` are three copies of the same value; mismatch → state bucket "not found" or a second empty bucket.
**How to avoid:** bootstrap-state.sh is the single source; it prints the value and the plan should thread it into site.hcl default + .envrc + `gh variable set SGUID` in one task.

### Pitfall 4: Local terraform 1.8.2 vs CI 1.14.3
**What goes wrong:** state written by 1.14 cannot be read by 1.8; first CI plan after a local apply (or vice versa) errors on state version.
**How to avoid:** upgrade local terraform to 1.14.3 before the first apply (Environment table).

### Pitfall 5: github-oidc applied before SOPS key → IAM policy drift loop
**What goes wrong:** kms-sops-decrypt policies bake `TF_VAR_SOPS_KMS_KEY_ID`; placeholder default (`mrk-000…`) gets applied, then every environment that has/lacks the var flip-flops the policy.
**How to avoid:** ordering — SOPS key + .envrc/gh-var persistence BEFORE github-oidc apply. [VERIFIED: dc34 env.sops.sh Step 7 commentary describes exactly this failure]

### Pitfall 6: `make_site_domain = true` hijacks apex mail
**What goes wrong:** ses-domain hardcodes receive MX; enabling the apex identity writes `klankermaker.ai MX 10 inbound-smtp...` into the mgmt zone, rerouting any existing apex mail to an S3 inbox.
**How to avoid:** zone audit first; prefer the standalone `_dmarc` record unit (§SES route 2).

### Pitfall 7: service.hcl `file()` reads break all plans
**What goes wrong:** site.hcl reads every service.hcl at parse time; a stub that does `file("VERSION.app")` without the file kills plans for unrelated units.
**How to avoid:** stubs either include VERSION files or hardcode an image tag string until Phase 3.

### Pitfall 8: CI management-provider failure before delegate role exists
**What goes wrong:** PR plans touching site/certs/email units assume `kmv-github-delegate` in the mgmt account; role doesn't exist yet → AccessDenied in CI while local plans work.
**How to avoid:** document the manual mgmt-account step (trust policy = github-oidc module output); until done, expect red CI on those units or scope initial CI plans to app-account-only units.

### Pitfall 9: Stale SECRETS.md conventions
**What goes wrong:** dc34's SECRETS.md documents `.secrets.enc.json`; the live mechanism is `.secrets.sops.json` (site.hcl). Copy-pasting doc commands produces a file terragrunt never reads.
**How to avoid:** rewrite SECRETS.md against the actual site.hcl filenames while copying.

## Code Examples

### kmv voice service.hcl stub skeleton (data-only; Phase 4 fills real containers)
```hcl
# Source pattern: dc34 live/site/services/run.auth/service.hcl
locals {
  ecr_repositories = [
    { name = "voice-app", regions = ["us-east-1"], image_tag_mutability = "IMMUTABLE",
      lifecycle_policy = { max_image_count = 10, expire_days = 30 } }
  ]
  dynamodb = { tables = [] }          # Phase 4 adds tiers/usage tables
  task     = { /* placeholder; unused while ecs_tasks.enabled = false */ }
  service  = {
    name = "voice", regions = ["us-east-1"], cluster_name = "app",
    task_family = "voice", desired_count = 1,
    assign_public_ip = true,           # WebRTC: task in public subnets w/ public IP
    load_balancers = [{ type = "alb", container_name = "voice-app", container_port = 7860,
      target_group_protocol = "HTTP", health_check_path = "/health",
      listener = { port = 443, protocol = "HTTPS", host_headers = ["voice.{{SITE_DOMAIN}}"] } }]
  }
}
```

### Apex DMARC inline unit (bib-secrets pattern)
```hcl
# region/us-east-1/dmarc/terragrunt.hcl — includes providers/regional.hcl + skip.hcl; source = "."
# region/us-east-1/dmarc/main.tf
data "aws_route53_zone" "mgmt" { name = var.dns.zonename  provider = aws.global-management }
resource "aws_route53_record" "apex_dmarc" {
  provider = aws.global-management
  zone_id  = data.aws_route53_zone.mgmt.zone_id
  name     = "_dmarc.${var.dns.zonename}"
  type     = "TXT"
  ttl      = 600
  records  = ["v=DMARC1; p=quarantine; rua=mailto:dmarc-reports@auth.${var.dns.zonename}; aspf=r; adkim=r;"]
}
```

### Bootstrap idempotence core
```bash
aws s3api head-bucket --bucket "$BUCKET" --profile klanker-terraform 2>/dev/null \
  || aws s3api create-bucket --bucket "$BUCKET" --profile klanker-terraform --region us-east-1
aws dynamodb describe-table --table-name "$TABLE" --profile klanker-terraform >/dev/null 2>&1 \
  || aws dynamodb create-table --table-name "$TABLE" \
       --attribute-definitions AttributeName=LockID,AttributeType=S \
       --key-schema AttributeName=LockID,KeyType=HASH --billing-mode PAY_PER_REQUEST \
       --profile klanker-terraform --region us-east-1
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Terragrunt `skip = true` inputs | `exclude { if / actions }` blocks | Terragrunt 0.96 | dc34 already uses exclude; requires tg ≥0.96 (have 0.99.1) |
| Fargate: no sysctl support | `systemControls` net.* sysctls on PV 1.4.0+ | AWS launch (2023, GA'd broadly since) | Bounded UDP port range is achievable in Phase 4 (sysctl + narrow SG) — wide SG is still the zero-code v1 |
| SES sandbox gauntlet for new senders | Account already out of sandbox (D-12) | pre-phase | INFR-04 is DNS-records-only; no review clock |

**Deprecated/outdated:** dc34 SECRETS.md filename conventions (see Pitfall 9); dc34 buildpub.yml's EC2-runner machinery (skip for kmv v1).

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | HostedZoneAdmin SSO permission set on 481723467561 permits ListHostedZones + ChangeResourceRecordSets on the klankermaker.ai zone | Cross-Account DNS | First site apply fails; mitigation = one read-only CLI probe before applying |
| A2 | With the state bucket/table pre-created, terragrunt needs no ambient terraform-profile creds (dc34's env.sh export-credentials line is only for terragrunt's auto-provisioning) | State Bootstrap | First `terragrunt init` fails on backend auth; fallback = replicate dc34's export-credentials line in a helper script |
| A3 | Deleting the user_uploads/upload_processors/waffaw/cloudtrail blocks from site.hcl is safe (site module tolerates their absence; no in-scope unit reads them) | site.hcl delta | Plan-time error on site unit; trivially fixed by leaving `enabled=false` stub blocks instead |
| A4 | klankermaker.ai apex currently has no MX/mail service that route-1 SES receive MX would break | SES | Only relevant if make_site_domain=true is chosen; zone audit resolves before apply |
| A5 | terraform-provider-aws current versions support systemControls on Fargate (issue #40034 was version-specific to 5.74.0) | WebRTC groundwork | Phase 4 concern only; wide-SG path unaffected |
| A6 | Voice local dev port 7860 (pipecat runner default) for urls.local_ports | site.hcl delta | Cosmetic; trivially changed |

## Open Questions (RESOLVED)

*Dispositions recorded at planning time (2026-07-04) — each question is embodied in the Phase 2 plan set.*

1. **Apex DMARC route (make_site_domain vs standalone record)** — RESOLVED: route 2 (standalone `_dmarc` inline unit).
   - Disposition: Plan 01 Task 1 performs the read-only zone audit (apex-MX check recorded in 02-ZONE-AUDIT.md); Plan 02 Task 2 locks `make_site_domain = false` in site.hcl and authors the `region/us-east-1/dmarc/` inline unit; Plan 05 Task 3 applies it with an automated empty-apex-MX gate.
2. **Who creates `kmv-github-delegate` in 481723467561** — RESOLVED: the user, via a blocking checkpoint.
   - Disposition: Plan 06 Task 1 writes the module's trust-policy output to 02-DELEGATE-TRUST.json; Plan 06 Task 2 is a `checkpoint:human-action` with exact console steps; Plan 07 Task 3 tolerates the documented partial-red CI state (Pitfall 8) if the user defers.
3. **Elevenlabs bootstrap param timing** — RESOLVED: tolerated absence.
   - Disposition: Plan 03 Task 2 continues on ParameterNotFound (placeholder retained, `sops edit` follow-up documented in SUMMARY); Plan 05 Task 2 preserves any unmigrated bootstrap param (ONLY-THEN deletion rule).
4. **ALB idle timeout knob** — RESOLVED: conditional check during copy.
   - Disposition: Plan 02 Task 2 step 4 checks whether network module alb.tf exposes an idle-timeout input — sets 2400 in network.hcl if yes, records "defer to Phase 4" in 02-02-SUMMARY.md if no. Module itself is not modified either way (D-02).

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| terraform | all applies | ✓ (wrong version) | 1.8.2 local vs 1.14.3 CI pin | tfenv install 1.14.3 — treat as required setup task |
| terragrunt | all applies | ✓ | 0.99.1 (≥0.96 needed; CI pins 0.97.1) | ok as-is |
| sops | secrets flow | ✓ | 3.11.0 (matches CI pin) | — |
| aws-cli v2 | bootstrap, SOPS KMS, SSM migration | ✓ | 2.32.25 | — |
| direnv | infra/.envrc (D-06) | ✗ | — | `brew install direnv` + hook; interim: `source infra/.envrc` manually |
| gh | repo vars/environments setup | ✓ | 2.86.0 | AWS/GitHub web console |
| AWS SSO profiles klanker-{application,management,terraform} | all AWS access | ✓ | verified in ~/.aws/config (052251888500 admin ×2, 481723467561 HostedZoneAdmin) | — |
| dc34 source tree | clone source | ✓ | /Users/khundeck/working/defcon.run.34 present, all referenced files read | — |

**Missing dependencies with no fallback:** none.
**Missing dependencies with fallback:** direnv (manual source), terraform version (tfenv).

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | terragrunt/terraform native validation (no unit-test framework — IaC phase) |
| Config file | none — commands run per-unit in `infra/terraform/live/site` |
| Quick run command | `terragrunt hcl fmt --check && terragrunt validate` (per changed unit) |
| Full suite command | `terragrunt run --all validate` from `live/site` (with .envrc loaded); `terragrunt run --all plan` as the deep gate |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| INFR-01 | All in-scope units plan clean under site kmv | integration | `cd infra/terraform/live/site && terragrunt run --all plan` (excluded units skip) | ❌ Wave 0 (tree doesn't exist yet) |
| INFR-01 | No forbidden strings | smoke | `! grep -ri "kmk\|voiceai" infra/ && ! grep -ri "dc34\|defcon" infra/ --include='*.hcl'` | ❌ Wave 0 |
| INFR-02 | DNS + TLS live | smoke (post-apply) | `dig +short NS auth.klankermaker.ai` non-empty; `aws acm list-certificates --profile klanker-application --query "CertificateSummaryList[?Status=='ISSUED']"` contains auth./voice. certs; after ALB listener exists: `curl -sv https://auth.klankermaker.ai 2>&1 \| grep 'SSL certificate verify ok'` (in Phase 2, cert ISSUED status is the gate — no service answers yet) | manual-only until apply (justified: requires live AWS) |
| INFR-04 | SES identity verified + DKIM + DMARC | smoke (post-apply) | `aws ses get-identity-verification-attributes --identities auth.klankermaker.ai --profile klanker-application`; `dig +short TXT _dmarc.klankermaker.ai` contains `p=quarantine` | manual-only until apply |
| INFR-05 | Secrets landed in SSM | smoke (post-apply) | `aws ssm get-parameter --name /kmv/secrets/use1/deepgram/api_key --with-decryption --profile klanker-application` succeeds; bootstrap params deleted | manual-only until apply |
| INFR-07 | OIDC roles usable from Actions | e2e | trigger `terragrunt-plan.yml` via `gh workflow run` → job assumes kmv-github-readonly and completes plan | manual-only until CI merged |

### Sampling Rate
- **Per task commit:** `terragrunt hcl fmt --check` + `terragrunt validate` on touched units + forbidden-string grep
- **Per wave merge:** `terragrunt run --all validate`; `terragrunt run --all plan` once state/backends exist
- **Phase gate:** full plan clean + the post-apply smoke commands above green before `/gsd-verify-work`

### Wave 0 Gaps
- [ ] `scripts/bootstrap-state.sh` — prerequisite for any plan (backend must exist)
- [ ] `infra/.envrc` — prerequisite for terragrunt env resolution
- [ ] forbidden-string grep check (can be a one-liner in CI or a script) — covers D-01/naming constraint
*(No test-framework install needed — validation is CLI-native.)*

## Security Domain

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no (no app code this phase) | CI auth = GitHub OIDC federation, no long-lived keys (INFR-07) |
| V3 Session Management | no | — |
| V4 Access Control | yes (IAM) | Cloned least-privilege role split (readonly plan / terragrunt apply / deploy / release); environment + branch restrictions on role assumption; delegate role scoped to Route53 with external_id |
| V5 Input Validation | no | — |
| V6 Cryptography | yes | KMS CMKs (SOPS key + per-module SSM CMKs) — never hand-roll; SSE on state bucket; SecureString SSM; ACM-managed TLS |
| V10 Config/Secrets | yes | SOPS-encrypted secrets in a public repo (D-07) — verify `.secrets.json` (plaintext) is gitignored BEFORE first commit; gitleaks workflow recommended |

### Known Threat Patterns for public-repo IaC + OIDC CI

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Plaintext secret committed to public repo | Information Disclosure | .gitignore `.secrets.json` + `env.local.sh`; gitleaks-scan.yml (dc34 has one — clone it); SOPS-only workflow |
| Fork PR assumes OIDC role | Elevation of Privilege | readonly role for PRs only; apply role gated on `terraform-apply` environment with required reviewers; branch_restriction=main on deploy |
| Cross-account role confused-deputy | Spoofing | delegate role trust requires `external_id = "kmv"` (already in providers assume_role block) [VERIFIED] |
| Overbroad terragrunt CI role (kms:\*, iam:\*) | EoP | Inherited from dc34 (known tradeoff); apply role only reachable through human-gated environment — document, don't fix this phase (D-02 prune-later) |
| State bucket public exposure | Information Disclosure | put-public-access-block all-true in bootstrap script; state contains secret values (SSM params in state!) — bucket encryption + no public access is mandatory |
| Wide UDP SG on shared SG list | Tampering/DoS | Phase 2 ships SG structure with services disabled; Phase 4 verifies scope; sysctl+narrow-range is the tightening path |

## Sources

### Primary (HIGH confidence — read directly this session)
- `/Users/khundeck/working/defcon.run.34/infra/terraform/providers/{global,regional}.hcl` — backend/provider generation, CI switching, delegate-role assume
- `.../live/site/{terragrunt.hcl,site.hcl,SECRETS.md,.secrets.sops.json.template}` — aggregation, sops decrypt hook, github_oidc block, secrets definitions
- `.../live/site/region/{skip.hcl,us-east-1/*}` — region derivation, unit wiring, email.hcl, network.hcl
- `.../modules/{site,certs,email,secrets,ecs-service,ecs-task,network,github-oidc,dynamodb,ecr,ecs-cluster}` — all mechanisms cited above
- `defcon.run.34/{.sops.yaml,env.sh,env.sops.sh}` — SOPS + env contract
- `defcon.run.34/.github/workflows/{terragrunt-plan,terragrunt-apply,buildpub,deploy}.yml` — OIDC roles, environments, tool pins, env vars
- `~/.aws/config` — klanker profile/SSO role verification; local tool probes
- klanker-voice `.planning/{REQUIREMENTS.md,research/ARCHITECTURE.md,research/PITFALLS.md}`, `docs/superpowers/specs/2026-07-04-klanker-voice-design.md`

### Secondary (MEDIUM confidence)
- AWS docs: ECS task definition parameters (systemControls / Fargate PV 1.4.0+) + AWS containers blog "Announcing additional Linux controls for Amazon ECS tasks on AWS Fargate" — via WebSearch, classify-confidence seam → MEDIUM (cached, key c6e6b54b…)

### Tertiary (LOW confidence)
- terraform-provider-aws issue #40034 (systemControls-on-Fargate provider bug, version-specific) — noted for Phase 4 verification only

## Metadata

**Confidence breakdown:**
- Clone inventory / mechanisms: HIGH — every claim read from the dc34 source files this session
- site.hcl delta: HIGH for values, MEDIUM for the block-deletion safety (A3) — one plan run resolves
- Cross-account permissions fit: MEDIUM — HostedZoneAdmin policy contents unseen (A1)
- WebRTC/Fargate sysctl: MEDIUM — official docs via web, not exercised
- Pitfalls: HIGH — each traced to a specific source line

**Research date:** 2026-07-04
**Valid until:** 2026-08-04 (stable domain; dc34 tree is a frozen local source)
