---
phase: 12-voip-ms-telephony-inbound-did
plan: 07
subsystem: infra
tags: [terraform, terragrunt, ecs, fargate, asterisk, docker, security-group]

# Dependency graph
requires:
  - phase: 12-voip-ms-telephony-inbound-did (12-04)
    provides: "apps/voice/asterisk/pjsip.conf's VoIP.ms registration trunk (voipms-auth/registration/aor/endpoint/identify) this Dockerfile's rendered config activates"
  - phase: 12-voip-ms-telephony-inbound-did (12-05)
    provides: "the hard task-role constraint (dedicated role, SSM grants under /kmv/secrets/use1/* only, never /kmv/operators/*) this plan's task_role_iam_statements implements"
  - phase: 12-voip-ms-telephony-inbound-did (12-06)
    provides: "the controller's /tel mint call + ARI/gate secret consumption this Dockerfile's entrypoint/service.hcl secrets[] wires SSM values into"
provides:
  - "infra/terraform/live/site/services/telephony-edge/service.hcl -- the deployable ECS task+service data stub (ECR repo, dedicated least-privilege task role, all 7 secrets via valueFrom, public IP, no load balancer)"
  - "infra/terraform/live/site/region/us-east-1/network/telephony-sg.hcl -- the 8 Toronto VoIP.ms POP CIDRs + 6-month re-verification runbook"
  - "network/v1.0.0's dedicated telephony_edge security group + standalone telephony_edge_security_group_id output (POP-locked, never 0.0.0.0/0 on ingress)"
  - "ecs-service/v1.0.0's security_group_overrides map -- lets one ECS service in the shared module use its own SG list instead of the module-wide default"
  - "apps/voice/asterisk/Dockerfile + entrypoint.sh -- a single self-contained, locally build- and run-tested Fargate image (Asterisk + the telephony controller, config rendered from env, SIP credential scrubbed before the Python process starts)"
  - "docs/operators/voipms-provisioning-runbook.md's 4 previously-undocumented SSM secret rows (ASTERISK_ARI_*, TELEPHONY_ACCESS_PIN/PASSPHRASE_WORDS)"
affects: [12-08]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Per-service security group override in a shared Terragrunt ECS module: security_group_overrides map (keyed by service name) takes precedence over the module-wide security_group_ids default -- additive, backward-compatible, needed because the shared default list includes webrtc_udp (0.0.0.0/0 on UDP 20000-20100)"
    - "Single-container Asterisk+Python Fargate image: entrypoint.sh renders configs, backgrounds Asterisk, scrubs the SIP credential from its own env, then execs the Python controller in the foreground as PID 1's replacement"
    - "dynamic \"ingress\" blocks over an empty-by-default CIDR list variable = zero ingress rules (closed), not an accidentally-open rule with an empty cidr_blocks list"

key-files:
  created:
    - infra/terraform/live/site/services/telephony-edge/service.hcl
    - infra/terraform/live/site/region/us-east-1/network/telephony-sg.hcl
    - apps/voice/asterisk/Dockerfile
    - apps/voice/asterisk/entrypoint.sh
  modified:
    - infra/terraform/live/site/site.hcl
    - infra/terraform/live/site/region/us-east-1/network/network.hcl
    - infra/terraform/live/site/region/us-east-1/ecs-service/terragrunt.hcl
    - infra/terraform/modules/network/v1.0.0/variables.tf
    - infra/terraform/modules/network/v1.0.0/securitygroups.tf
    - infra/terraform/modules/network/v1.0.0/outputs.tf
    - infra/terraform/modules/ecs-service/v1.0.0/variables.tf
    - infra/terraform/modules/ecs-service/v1.0.0/main.tf
    - docs/operators/voipms-provisioning-runbook.md

key-decisions:
  - "Apply path actually used: the org SCP `DenyInfraAndStorage` (management acct policy p-cvd490xt) explicitly denies ec2:CreateSecurityGroup/iam:CreateRole etc. to ALL principals except SSO operator roles -- so CI could apply only `ecr`; `network` and `ecs-task`/`ecs-service` were applied LOCALLY by the orchestrator with the operator SSO profile (klanker profile-prefix convention, terragrunt 0.97.1). Plans matched CI output exactly at every step (network 1 add; ecs-task 6 adds/0 destroys; ecs-service 1 add)."
  - "The shared ecs-service/v1.0.0 module's `security_group_ids` is a single list applied to every service; discovered during authoring that it INCLUDES webrtc_udp (0.0.0.0/0 on UDP 20000-20100) -- attaching telephony-edge to that shared list would have silently defeated the entire POP-lock even with a correctly-authored telephony-sg. Fixed via a Rule 2 addition: a new `security_group_overrides` map lets telephony-edge use its own SG list; voice/auth are unaffected (empty override = unchanged default behavior)."
  - "readonly_root_filesystem = false for the telephony-edge container (module default is true, correct for voice/auth's stateless apps) -- Asterisk needs writable /etc/asterisk (config rendering), /var/spool/asterisk, /var/log/asterisk, /var/lib/asterisk (astdb) at runtime."
  - "The Dockerfile builds FROM the SAME pinned Asterisk base image (andrius/asterisk:22.10.1_debian-trixie) the Phase-11 local dev harness already proved works, adding a uv-managed Python 3.12 toolchain on top (the base ships only python3.13 via apt; uv downloads its own 3.12 to match apps/voice/.python-version) rather than building a new Asterisk-from-source image or copying binaries across a multi-stage build."
  - "entrypoint.sh explicitly `unset`s VOIPMS_SIP_USERNAME/PASSWORD after Asterisk's config is rendered and before exec'ing the Python controller -- the closest a single-container design can get to D-04's 'never passed into the Python process' requirement, since ECS necessarily injects all container secrets into one shared initial environment."

requirements-completed: [D-01, D-04, SC-1, SC-3]

coverage:
  - id: D1
    description: "telephony-edge service.hcl declares secrets[] valueFrom for all seven secrets, a dedicated least-privilege task role (SSM read + KMS decrypt scoped to /kmv/secrets/use1/{voipms,asterisk,telephony}/* only, never /kmv/operators/*), assign_public_ip=true, and NO load_balancers block"
    requirement: "D-04"
    verification:
      - kind: other
        ref: "grep for '/kmv/operators/' in service.hcl (zero hits); grep for load_balancers in service.hcl (zero hits); manual read of task_role_iam_statements (2 statements, both scoped)"
        status: pass
    human_judgment: false
  - id: D2
    description: "telephony-sg.hcl + network/v1.0.0's dedicated telephony_edge security group: ingress locked to the 8 Toronto POP CIDRs via dynamic blocks (empty list = zero ingress rules), egress open, NEVER 0.0.0.0/0 on any ingress rule; deliberately excluded from the shared security_group_ids output/security_group_overrides wires it in for telephony-edge only"
    requirement: "D-01"
    verification:
      - kind: other
        ref: "awk-scoped grep of the telephony_edge SG resource body for 0.0.0.0/0 (found only on the egress block, zero on ingress); terraform validate clean on network/v1.0.0"
        status: pass
    human_judgment: false
  - id: D3
    description: "apps/voice/asterisk/Dockerfile builds successfully, and at runtime: Asterisk reaches 'Ready', the rendered pjsip.conf carries the real SIP credential, the credential is confirmed ABSENT from the entrypoint's own environment after the unset (and therefore never reaches the exec'd Python process), and the controller starts successfully reading configs/telephony.toml (require_gate=True gate_mode='either') against the rendered ARI config"
    requirement: "D-04"
    verification:
      - kind: other
        ref: "local `docker build` (clean) + `docker run` with real env: Asterisk log shows 'Asterisk Ready.'; controller log shows 'telephony controller starting: ... require_gate=True gate_mode=either'; isolated shell test confirms VOIPMS_SIP_USERNAME/PASSWORD=[UNSET] after the entrypoint's unset while grep finds the real values in the rendered /etc/asterisk/pjsip.conf"
        status: pass
    human_judgment: false
  - id: D4
    description: "A deployed, live telephony-edge task is RUNNING with a public IP, no public ARI listener, all 7 SSM valueFrom secrets resolved, and the security group confirmed POP-locked against the live AWS account (the plan's Task 2 / checkpoint:human-verify). Outbound REGISTER attempted; the 403 it received is the expected pre-provisioning state (the klanker-pbx VoIP.ms subaccount does not exist yet) -- not an edge defect."
    requirement: "D-01, D-04, SC-1, SC-3"
    verification:
      - kind: manual_procedural
        ref: "Operator-verified live (2026-07-12): SG sg-012efce55bc8169f1 = exactly 8 Toronto POP /32s on udp/5060 + udp/20000-20100, zero 0.0.0.0/0; service telephony-edge-use1 on cluster app-use1-kmv RUNNING 1/1 with ONLY the POP-locked SG attached; dedicated telephony-edge-use1-kmv-task-role/-execution-role created least-privilege (12-05 constraint honored); all 7 SSM valueFrom secrets resolved on first task start; logs show public media address discovered, no ${VAR} literals in rendered configs, 'Asterisk Ready.', SIP cred scrubbed pre-controller, controller started (require_gate=True, gate_mode=either)"
        status: pass
    human_judgment: true
    rationale: "Live-account posture verification is inherently an operator judgment call (reading live SG rules, IAM policies, ECS state, CloudWatch logs) -- performed and confirmed by the orchestrator/operator on 2026-07-12 via the local SSO apply path."

# Metrics
duration: 35min (Task 1) + orchestrator deploy session (Task 2)
completed: 2026-07-12
status: complete
---

# Phase 12 Plan 07: Telephony-Edge Service Stub + POP-Locked SG + Asterisk Dockerfile Summary

**DEPLOYED AND LIVE-VERIFIED: the telephony-edge Fargate service is RUNNING (1/1) on `app-use1-kmv` with ONLY the POP-locked security group (`sg-012efce55bc8169f1`: exactly the 8 Toronto VoIP.ms POP /32s on udp/5060 + udp/20000-20100, zero 0.0.0.0/0), a dedicated least-privilege task role, all 7 SSM valueFrom secrets resolved, ARI private-only, configs rendered from env with the SIP credential scrubbed before the Python controller starts. The outbound REGISTER's fatal 403 is the expected pre-provisioning state — the `klanker-pbx` VoIP.ms subaccount does not exist yet.**

## Performance

- **Duration:** ~35 min (Task 1, executor session) + the orchestrator's deploy/landing session (Task 2)
- **Started:** 2026-07-12T20:20Z (approx, per STATE.md's prior session timestamp)
- **Completed:** 2026-07-12 (Task 2 live-verified by the orchestrator/operator)
- **Tasks:** 2 of 2 (Task 1 by this executor; Task 2 deployed + verified by the orchestrator)
- **Files modified:** 13 (4 created, 9 modified) in Task 1, plus 5 orchestrator landing commits (see Task Commits)

## Apply Path Actually Used (SCP discovery)

The original mid-plan directive routed the apply through the repo's GitHub Actions
path. During landing, the orchestrator discovered the org SCP **`DenyInfraAndStorage`**
(management account policy `p-cvd490xt`) explicitly denies `ec2:CreateSecurityGroup`,
`iam:CreateRole`, and related infra mutations to ALL principals **except SSO operator
roles** — so CI could apply only the `ecr` unit. The `network`, `ecs-task`, and
`ecs-service` units were applied **locally by the orchestrator with the operator SSO
profile** (klanker profile-prefix convention, terragrunt 0.97.1). The local plans
matched the CI plan output exactly at every step: `network` 1 add; `ecs-task` 6
adds / 0 destroys; `ecs-service` 1 add.

## Accomplishments (Task 1 — done)

- **`infra/terraform/live/site/services/telephony-edge/service.hcl`** (new): the ECS
  task+service data stub, mirroring voice/auth's shape. ECR repo (`telephony-edge`);
  a dedicated least-privilege task role (`TelephonySecretRead` — `ssm:GetParameters`/
  `ssm:GetParameter` scoped to exactly `/kmv/secrets/use1/{voipms,asterisk,telephony}/*`;
  `TelephonyKmsDecrypt` — `kms:Decrypt` conditioned on `kms:ViaService=ssm.us-east-1
  .amazonaws.com` — honoring 12-05's hard constraint: never the shared cluster role,
  never `/kmv/operators/*`); all seven secrets wired via `secrets[].valueFrom`
  (`VOIPMS_SIP_USERNAME/PASSWORD`, `ASTERISK_ARI_USERNAME/PASSWORD`,
  `TELEPHONY_ENDPOINT_AUTH_TOKEN`, `TELEPHONY_ACCESS_PIN`, `TELEPHONY_PASSPHRASE_WORDS`);
  `assign_public_ip = true` (the registration trunk needs egress); NO `load_balancers`
  block (ARI is private-only); `readonly_root_filesystem = false` (Asterisk needs
  writable `/etc/asterisk`, `/var/spool/asterisk`, `/var/log/asterisk`,
  `/var/lib/asterisk`).
- **`infra/terraform/live/site/region/us-east-1/network/telephony-sg.hcl`** (new): the
  eight Toronto VoIP.ms POP `/32` CIDRs as their own data-only locals stub (mirroring
  `service.hcl`'s pattern), with an in-file 6-month re-verification runbook keeping the
  CIDR list, the operator doc's own table, and `pjsip.conf`'s `[voipms-identify]`
  section in sync.
- **`network/v1.0.0` extended** (`variables.tf`/`securitygroups.tf`/`outputs.tf`): a
  new `telephony_edge_pop_cidrs` variable (default `[]`, backward-compatible for every
  other site/region) and a dedicated `aws_security_group.telephony_edge` resource whose
  ingress is built from TWO `dynamic "ingress"` blocks (SIP 5060/udp, RTP
  20000-20100/udp) iterating that CIDR list — an empty list produces literally zero
  ingress rules (closed), not an accidentally-permissive one. A standalone
  `telephony_edge_security_group_id` output was added; **deliberately NOT folded into**
  the existing `security_groups`/`security_group_ids` outputs, since those are attached
  to every ECS service by default and already include `webrtc_udp` (0.0.0.0/0 on UDP
  20000-20100).
- **Rule 2 fix — `ecs-service/v1.0.0` per-service SG override (genuinely blocking, not
  optional polish):** while wiring the new SG in, discovered the shared `ecs-service`
  module's `network_configuration.security_groups` was hardcoded to the single
  module-wide `var.security_group_ids` list for EVERY service. Since that shared list
  includes `webrtc_udp` (0.0.0.0/0 on UDP 20000-20100, needed for the voice service's
  public WebRTC media), simply adding telephony-edge into `ecs_services.services`
  without a code change would have attached the SAME public 0.0.0.0/0 SG to the
  telephony-edge task's ENI — completely defeating the POP-lock this plan exists to
  build, regardless of how carefully `telephony-sg.hcl` itself was authored. Fixed by
  adding a `security_group_overrides` map (keyed by service name) to `variables.tf`,
  and `lookup(var.security_group_overrides, each.key, var.security_group_ids)` in
  `main.tf`'s `network_configuration` block. Additive/backward-compatible: voice and
  auth are absent from the map, so they keep the exact same shared-list behavior they
  had before this plan.
- **`infra/terraform/live/site/region/us-east-1/ecs-service/terragrunt.hcl`**: wired
  `security_group_overrides = { "telephony-edge" = [dependency.network.outputs
  .telephony_edge_security_group_id] }`, plus the corresponding `mock_outputs` entry
  for offline plan/validate.
- **`infra/terraform/live/site/site.hcl`**: registered `telephony_edge =
  read_terragrunt_config("./services/telephony-edge/service.hcl")` in `service_conf`,
  and appended it into the `ecr.repositories`, `ecs_tasks.tasks`, and
  `ecs_services.services` concat lists (mirroring the existing voice/auth pattern
  exactly). `dynamodb.tables` was left untouched — telephony-edge has no tables.
- **`infra/terraform/live/site/region/us-east-1/network/network.hcl`**: reads
  `telephony-sg.hcl` and merges `telephony_edge_pop_cidrs` into the module inputs
  alongside the existing `vpc`/`nat_gateway`/`alb`/`nlb` keys.
- **`apps/voice/asterisk/Dockerfile` + `entrypoint.sh`** (new): a single
  self-contained Fargate image. `FROM andrius/asterisk:22.10.1_debian-trixie` (the
  SAME pinned base/version the Phase-11 local dev harness already proved ships
  `res_ari`/`res_stasis`) + the same aiortc/audio system libs `apps/voice/Dockerfile`
  installs + a uv-managed Python 3.12 toolchain (the base image's only apt-available
  python3 is 3.13; `apps/voice/.python-version` pins 3.12, and that file is excluded
  from the Docker build context by `.dockerignore`, so the version is pinned
  explicitly via `uv python install 3.12` + `uv sync --python 3.12` instead of relying
  on the `.python-version` convention). `entrypoint.sh`: renders `ari.conf`/
  `pjsip.conf` from env via the UNMODIFIED `render_configs.py`, starts Asterisk in the
  background (`asterisk -f -T -U asterisk -p`, matching the base image's own default
  CMD flags), waits 3s, `unset`s `VOIPMS_SIP_USERNAME`/`VOIPMS_SIP_PASSWORD`, then
  `exec`s `python -m klanker_voice.telephony` as the foreground process (a controller
  crash exits the container and lets ECS restart the task; an Asterisk-only crash is
  a known, explicitly documented Phase-14 ops-hardening gap, not fixed here).
- **`docs/operators/voipms-provisioning-runbook.md`**: added the four SSM parameter
  rows (`ASTERISK_ARI_USERNAME`, `ASTERISK_ARI_PASSWORD`, `TELEPHONY_ACCESS_PIN`,
  `TELEPHONY_PASSPHRASE_WORDS`) that 11-06/12-06 already require as `valueFrom`
  secrets but that the original 12-01 runbook never listed (it only documented 6 of
  what are now 11 total secrets this service's `secrets[]` list references).

## Local Validation Performed (no live AWS mutation)

- `terragrunt hcl format --check` clean on both new files (auto-fixed one formatting
  issue in `service.hcl` before commit) — the plan's own pre-existing repo-wide
  `hclfmt` debt (`auth/service.hcl`, `voice/service.hcl`, `modules/ecs-service/
  config.hcl`) was confirmed untouched by this plan and left alone (out of scope,
  Rule 1-3 scope boundary).
- `terraform init -backend=false && terraform validate` — clean on BOTH modified
  modules (`network/v1.0.0`, `ecs-service/v1.0.0`).
- `terragrunt run -- validate` against the live network unit fails ONLY at the
  SOPS/KMS decrypt step (`site.hcl` unconditionally decrypts `.secrets.sops.json` for
  every unit) — confirmed this is an AWS-credential/backend gate, not a config
  error; this sandbox has no live AWS credentials (`aws sts get-caller-identity`
  returns `InvalidClientTokenId`), so a full `terragrunt plan` could not be run
  locally. This will be exercised for real by `terragrunt-plan.yml` on the PR.
- **`docker build` + `docker run`, real end-to-end**: the image built successfully;
  running it (no secrets) showed `render_configs.py` correctly leaving unset
  `${VAR}` placeholders literal; Asterisk reached `Asterisk Ready.`; with fake
  ARI/gate env vars set, the controller logged `telephony controller starting: ...
  require_gate=True gate_mode='either'` against the real rendered config. A separate
  isolated shell test proved the D-04 scrub: after the entrypoint's `unset`,
  `VOIPMS_SIP_USERNAME`/`PASSWORD` are `[UNSET]` in the shell's own environment, while
  `grep` finds the real supplied values in the rendered `/etc/asterisk/pjsip.conf` —
  the credential reaches Asterisk's config but not the Python process's environment.
- `grep`-based literal-secret checks: zero secret literals in the Dockerfile/
  entrypoint.sh; zero `0.0.0.0/0` on any `telephony_edge` SG *ingress* rule (only on
  its egress, which is intentional/expected).
- Full `apps/voice` test suite: **417 passed, 53 skipped, 0 failed** (matches the
  12-06 baseline — this plan touched no Python source files, only infra/Dockerfile/
  docs).

## Task Commits

1. **Task 1: telephony-edge service stub + POP-locked security group + Dockerfile** —
   `a7acc34` (feat, this executor)
2. **Task 2 (orchestrator landing fixes, committed during the deploy):**
   - `dbe50c9` (fix): shallow-merge network mock outputs so plan works pre-apply
     (new-output bootstrap plan failure)
   - `58cf783` (fix): deep-map-merge ecs-task mocks — telephony-edge task-def key
     missing pre-apply (same bootstrap class)
   - `2c2263d` (fix): pinned image-tag defaults to deployed/built SHAs
     (voice→288f4bc live, auth→244dcdd live, telephony→built SHA) — bare applies
     are now prod-safe; the old defaults pointed voice at a NONEXISTENT ECR image
   - `3819143` (fix): entrypoint discovers the Fargate public media address at boot
     (checkip.amazonaws.com via python3) — the `${TELEPHONY_MEDIA_ADDRESS}` literal
     was surviving rendering and would have black-holed RTP; also sets a random
     per-boot softphone password so the dev-harness endpoint is never open with a
     placeholder credential
   - `59e8131` (fix): pinned telephony image tag to the entrypoint-fixed build

## Files Created/Modified

- `infra/terraform/live/site/services/telephony-edge/service.hcl` — new: ECS task+
  service data stub
- `infra/terraform/live/site/region/us-east-1/network/telephony-sg.hcl` — new: the 8
  Toronto POP CIDRs
- `infra/terraform/live/site/site.hcl` — registered telephony_edge in service_conf/
  ecr/ecs_tasks/ecs_services
- `infra/terraform/live/site/region/us-east-1/network/network.hcl` — merges
  telephony_edge_pop_cidrs into the network module's inputs
- `infra/terraform/live/site/region/us-east-1/ecs-service/terragrunt.hcl` —
  security_group_overrides wiring + mock_outputs
- `infra/terraform/modules/network/v1.0.0/variables.tf` — new
  telephony_edge_pop_cidrs variable
- `infra/terraform/modules/network/v1.0.0/securitygroups.tf` — new
  aws_security_group.telephony_edge resource
- `infra/terraform/modules/network/v1.0.0/outputs.tf` — new
  telephony_edge_security_group_id output (standalone, not in the default list)
- `infra/terraform/modules/ecs-service/v1.0.0/variables.tf` — new
  security_group_overrides map variable
- `infra/terraform/modules/ecs-service/v1.0.0/main.tf` — network_configuration now
  looks up the per-service override
- `apps/voice/asterisk/Dockerfile` — new: single-container Asterisk+controller image
- `apps/voice/asterisk/entrypoint.sh` — new: render → start Asterisk → scrub SIP
  creds → exec controller
- `docs/operators/voipms-provisioning-runbook.md` — added 4 missing SSM secret rows

## Decisions Made

See `key-decisions` in frontmatter for the full list. Summary: (1) the apply landed
via a mixed path — CI for `ecr`, local operator SSO for the SG/IAM/service units
the org SCP denies to CI; (2) the shared ecs-service module needed a
genuinely-necessary per-service SG override fix (Rule 2, not optional); (3)
`readonly_root_filesystem = false` for this one container, matching Asterisk's real
runtime needs; (4) build FROM the proven Phase-11 Asterisk base image + a uv-managed
Python 3.12, rather than a from-scratch or multi-stage-copy image; (5) explicit
`unset` of the SIP credential before exec'ing the Python controller; (6) VoIP.ms
subaccount will rely on registration auth + strong password instead of
`ip_restriction`, since the Fargate public IP is dynamic per task (static egress is
Phase 14).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Critical] Shared ecs-service module had no per-service
security-group override, which would have silently defeated the POP-lock**
- **Found during:** Task 1, while wiring the new telephony_edge SG into the region
  ecs-service unit
- **Issue:** `ecs-service/v1.0.0`'s `network_configuration.security_groups` was
  hardcoded to the single module-wide `var.security_group_ids` list, applied
  identically to every service. That list includes `webrtc_udp` (0.0.0.0/0 on UDP
  20000-20100). Adding telephony-edge to `ecs_services.services` without a code
  change would have attached that public SG to telephony-edge's ENI in addition to
  the new POP-locked one — a real, silent security gap the plan's own acceptance
  criteria ("never 0.0.0.0/0 on any ingress rule") would have failed at deploy time
  regardless of how correct `telephony-sg.hcl` itself was.
- **Fix:** Added a `security_group_overrides` map variable (keyed by service name)
  to `ecs-service/v1.0.0`; `network_configuration.security_groups` now does
  `lookup(var.security_group_overrides, each.key, var.security_group_ids)`. Wired
  `{ "telephony-edge" = [telephony_edge_security_group_id] }` in the region unit.
  Voice/auth are absent from the map and keep their pre-existing behavior exactly.
- **Files modified:** `infra/terraform/modules/ecs-service/v1.0.0/variables.tf`,
  `infra/terraform/modules/ecs-service/v1.0.0/main.tf`,
  `infra/terraform/live/site/region/us-east-1/ecs-service/terragrunt.hcl`
- **Verification:** `terraform validate` clean on the modified module; manual trace
  of `each.key` (confirmed `local.services_map`'s key IS the service name, matching
  the override map's key); grep confirms no `0.0.0.0/0` on the telephony_edge SG's
  ingress rules.
- **Committed in:** `a7acc34` (Task 1 commit)

**2. [Rule 2 - Missing Critical] Operator runbook was missing 4 of 11 required SSM
secret rows**
- **Found during:** Task 1, while authoring `service.hcl`'s `secrets[]` list against
  the plan's own D-04 requirement (VOIPMS_SIP_*, ASTERISK_ARI_*, TELEPHONY_ACCESS_PIN
  /PASSPHRASE_WORDS, the /tel token)
- **Issue:** `docs/operators/voipms-provisioning-runbook.md` (written in 12-01,
  before 11-06's §24 gate and 12-06's ARI/mint wiring existed) only documented 6 SSM
  parameters. `ASTERISK_ARI_USERNAME`, `ASTERISK_ARI_PASSWORD`, `TELEPHONY_ACCESS_PIN`,
  and `TELEPHONY_PASSPHRASE_WORDS` were never added to its "Secrets → SSM" table even
  though the deployed edge's task definition now references all of them as
  `valueFrom` secrets — an operator following the runbook as written would miss 4 of
  11 required parameters and the deploy would fail at container start (missing env).
- **Fix:** Added a clearly-labeled "Added by 12-07" sub-table with the 4 missing
  rows, sources, and consumption notes (mirroring the existing table's style).
- **Files modified:** `docs/operators/voipms-provisioning-runbook.md`
- **Verification:** Manual cross-check against `service.hcl`'s `secrets[]` list (now
  11 SSM parameters total between the two tables, matching exactly).
- **Committed in:** `a7acc34` (Task 1 commit)

---

**Total deviations:** 2 auto-fixed (both Rule 2 — missing critical functionality)
**Impact on plan:** Both fixes are necessary-for-correctness, not scope creep. #1 was
a genuine security gap that would have silently defeated this plan's core purpose;
#2 was a genuine operational gap that would have blocked a successful deploy.

## Issues Encountered

- **SCP blocked the CI apply path:** the org SCP `DenyInfraAndStorage` (p-cvd490xt)
  denies SG/IAM creation to all non-SSO-operator principals, so the originally
  directed GitHub-Actions apply could only land `ecr`; the orchestrator applied the
  remaining units locally with the operator SSO profile (plans matched CI exactly).
- **Bootstrap plan failures on new outputs** (fixed in `dbe50c9`/`58cf783`): the
  region units' `mock_outputs` didn't cover the new `telephony_edge_security_group_id`
  output / telephony-edge task-def key pre-apply — fixed with shallow (network dep)
  and `deep_map_only` (ecs-task dep) merge strategies.
- **Stale image-tag defaults were a live footgun** (fixed in `2c2263d`): the voice
  service's hardcoded fallback tag pointed at a NONEXISTENT ECR image, so any bare
  local apply would have broken the live voice service — all three services' defaults
  now pin the actually-deployed/built SHAs.
- **`${TELEPHONY_MEDIA_ADDRESS}` survived rendering** (fixed in `3819143`): on
  Fargate nothing set that env var, so the literal placeholder reached the rendered
  pjsip.conf and would have black-holed RTP — the entrypoint now discovers the
  task's public IP at boot (checkip.amazonaws.com) when the var is unset, and also
  locks the dev-harness softphone endpoint with a random per-boot password.

## User Setup Required

None remaining for this plan — all 7 SSM SecureString parameters
(`/kmv/secrets/use1/{voipms,asterisk,telephony}/*`) were populated before the apply
and **all 7 `valueFrom` secrets resolved on the very first task start** (confirmed
live). The next operator action belongs to 12-08's provisioning path: create the
`klanker-pbx` VoIP.ms subaccount (currently blocked on the operator's API IP
whitelist — see Open Issues).

## Deploy Checkpoint — EXECUTED AND VERIFIED (Task 2 complete, 2026-07-12)

**Type:** human-verify — performed by the orchestrator/operator via the local SSO
apply path (see "Apply Path Actually Used" above).

### Applies performed

| Unit | Applied via | Result |
|---|---|---|
| `region/us-east-1/ecr` | CI (`terragrunt-apply.yml`) | telephony-edge repository created |
| `region/us-east-1/network` | Local operator SSO (SCP blocks CI) | 1 add — `aws_security_group.telephony_edge` |
| `region/us-east-1/ecs-task` | Local operator SSO | 6 adds / 0 destroys — task def + dedicated task/execution roles |
| `region/us-east-1/ecs-service` | Local operator SSO | 1 add — the telephony-edge service |

Local plans matched the CI plan output exactly at every step. Zero destroys, zero
modifications to any existing voice/auth/network resource — the
`security_group_overrides` module change was confirmed backward-compatible in the
real plan diff (no diff on the voice/auth services).

### Live verification results

- **SG `sg-012efce55bc8169f1`:** exactly the 8 Toronto POP `/32`s on udp/5060 +
  udp/20000-20100, **zero 0.0.0.0/0** on ingress ✓
- **Service `telephony-edge-use1` on cluster `app-use1-kmv`:** RUNNING, 1/1, with
  ONLY the POP-locked SG attached (no sshhttps/http_only/webrtc_udp) ✓
- **Dedicated roles `telephony-edge-use1-kmv-task-role` /
  `telephony-edge-use1-kmv-execution-role`** created; task role least-privilege ✓
  — the 12-05 hard constraint (never the shared cluster role; SSM grants only under
  `/kmv/secrets/use1/*`, never `/kmv/operators/*`) honored in the live account
- **All 7 SSM `valueFrom` secrets resolved on first task start** ✓
- **Logs:** public media address discovered at boot (dynamic per task, via the
  `3819143` entrypoint fix), configs rendered with **no `${VAR}` literals**,
  `Asterisk Ready.`, SIP credential scrubbed before the controller starts (D-04),
  controller started (ARI at localhost:8088, `require_gate=True`,
  `gate_mode=either`) ✓
- **No ALB/target group for the edge** — ARI private-only ✓
- **Outbound REGISTER to `toronto.voip.ms` → fatal 403, registration stopped:
  EXPECTED** — the `klanker-pbx` VoIP.ms subaccount doesn't exist yet (its
  provisioning is blocked on the operator's API IP whitelist). **Note for 12-08:
  after the subaccount is created, a task restart (or an Asterisk registration
  reload) is required** — Asterisk stopped retrying on the fatal 403 and will not
  re-attempt on its own.

## Open Issues (recorded for 12-08 / the runbook)

- **Dynamic Fargate public IP vs. VoIP.ms `ip_restriction`:** the task's public IP
  changes per deployment (observed `100.26.98.23` → `35.172.240.35` across two
  deploys). Locking the VoIP.ms subaccount's `ip_restriction` to the edge IP is
  therefore impractical without a static egress (NAT Gateway + EIP — a Phase-14
  item). The subaccount will rely on **registration auth + a strong SIP password**
  instead of IP restriction. The runbook's "IP-restricted subaccount" step must be
  read with this caveat.
- **Registration retry after fatal 403:** Asterisk treats the 403 as fatal and
  stops the outbound registration. Once the subaccount exists, restart the ECS task
  (or reload registrations) before expecting the trunk to come up.

## Next Phase Readiness

- The telephony-edge is DEPLOYED, RUNNING, and posture-verified — 12-08 (the manual
  cellular proof) is unblocked on the infra side.
- Remaining prerequisites for 12-08 are VoIP.ms-side provisioning only: create the
  `klanker-pbx` subaccount (blocked on the operator's API IP whitelist), order/route
  the DID, then restart the edge task so registration retries.
- The `security_group_overrides` pattern and the SCP discovery (CI cannot mutate
  SG/IAM; operator SSO required) are recorded for future infra plans.

## Self-Check: PASSED

- FOUND: infra/terraform/live/site/services/telephony-edge/service.hcl
- FOUND: infra/terraform/live/site/region/us-east-1/network/telephony-sg.hcl
- FOUND: apps/voice/asterisk/Dockerfile
- FOUND: apps/voice/asterisk/entrypoint.sh
- FOUND commits: a7acc34 (Task 1), dbe50c9 / 58cf783 / 2c2263d / 3819143 / 59e8131
  (Task 2 orchestrator landing fixes)
- `terraform validate` re-run clean on both modified modules (Task 1 session)
- `terragrunt hcl format --check` re-run clean on both new files (Task 1 session)
- `grep` re-run: zero `0.0.0.0/0` on telephony_edge SG ingress; zero secret literals
  in Dockerfile/entrypoint.sh
- Full apps/voice suite re-run: 417 passed, 53 skipped, 0 failed (Task 1 session)
- Task 2 live posture verified by the operator (SG sg-012efce55bc8169f1 POP-locked,
  service RUNNING 1/1, dedicated roles, 7/7 secrets resolved, ARI private-only)
- Secrecy contract re-checked: no secret values, no admin phone digits anywhere in
  this SUMMARY or the STATE/ROADMAP updates

---
*Phase: 12-voip-ms-telephony-inbound-did*
*Completed: 2026-07-12 (both tasks; deployed and live-verified)*
