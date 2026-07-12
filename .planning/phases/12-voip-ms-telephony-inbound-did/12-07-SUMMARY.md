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
  - "The user pre-authorized moving production infra for this plan but required the apply mechanism be the repo's established GitHub Actions path (terragrunt-plan.yml on the PR, then a human-gated terragrunt-apply.yml workflow_dispatch) instead of a local terraform/terragrunt apply from this session -- so Task 2 (the plan's own checkpoint:human-verify, gate=blocking) was NOT executed locally; this SUMMARY documents Task 1 (IaC authoring + local validation) as done and Task 2 (deploy + live posture confirmation) as pending that CI-gated apply."
  - "The shared ecs-service/v1.0.0 module's `security_group_ids` is a single list applied to every service; discovered during authoring that it INCLUDES webrtc_udp (0.0.0.0/0 on UDP 20000-20100) -- attaching telephony-edge to that shared list would have silently defeated the entire POP-lock even with a correctly-authored telephony-sg. Fixed via a Rule 2 addition: a new `security_group_overrides` map lets telephony-edge use its own SG list; voice/auth are unaffected (empty override = unchanged default behavior)."
  - "readonly_root_filesystem = false for the telephony-edge container (module default is true, correct for voice/auth's stateless apps) -- Asterisk needs writable /etc/asterisk (config rendering), /var/spool/asterisk, /var/log/asterisk, /var/lib/asterisk (astdb) at runtime."
  - "The Dockerfile builds FROM the SAME pinned Asterisk base image (andrius/asterisk:22.10.1_debian-trixie) the Phase-11 local dev harness already proved works, adding a uv-managed Python 3.12 toolchain on top (the base ships only python3.13 via apt; uv downloads its own 3.12 to match apps/voice/.python-version) rather than building a new Asterisk-from-source image or copying binaries across a multi-stage build."
  - "entrypoint.sh explicitly `unset`s VOIPMS_SIP_USERNAME/PASSWORD after Asterisk's config is rendered and before exec'ing the Python controller -- the closest a single-container design can get to D-04's 'never passed into the Python process' requirement, since ECS necessarily injects all container secrets into one shared initial environment."

requirements-completed: []  # Task 1 only (auto); Task 2 (D-01/D-04/SC-1/SC-3's live-deploy proof) is NOT complete -- see coverage below

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
    description: "A deployed, live telephony-edge task is RUNNING with a public IP, registers OUTBOUND to the Toronto POP, has no public ARI listener, and the security group is confirmed POP-locked against the live AWS account (the plan's actual Task 2 / checkpoint:human-verify)"
    requirement: "D-01, D-04, SC-1, SC-3"
    verification: []
    human_judgment: true
    rationale: "Per an explicit mid-plan user directive, live infra changes for this plan MUST go through the repo's GitHub Actions apply path (terragrunt-plan.yml on the PR, then a human-gated terragrunt-apply.yml workflow_dispatch), not a local terraform/terragrunt apply from this executor session. This coverage item is genuinely NOT YET DONE -- it is not a deferred-but-passable checkpoint, it is pending the orchestrator dispatching the apply workflow and someone confirming the live posture per this SUMMARY's 'Deploy Checkpoint' section below."

# Metrics
duration: 35min
completed: 2026-07-12
status: pending_deploy
---

# Phase 12 Plan 07: Telephony-Edge Service Stub + POP-Locked SG + Asterisk Dockerfile Summary

**Task 1 (IaC authoring) is done and locally validated: a telephony-edge ECS service stub with a dedicated least-privilege task role and all 7 SSM-backed secrets, a network-module security group whose ingress is a `dynamic` block over the 8 Toronto VoIP.ms POP CIDRs (never 0.0.0.0/0), a per-service security-group-override fix to the shared ecs-service module (needed because the module-wide default list includes the public webrtc_udp SG), and a single-container Asterisk+controller Dockerfile that was locally built AND run-tested end-to-end. Task 2 (the actual GitHub-Actions-gated deploy + live posture confirmation) is NOT done — see "Deploy Checkpoint" below.**

## Performance

- **Duration:** ~35 min
- **Started:** 2026-07-12T20:20Z (approx, per STATE.md's prior session timestamp)
- **Completed:** 2026-07-12T20:55Z
- **Tasks:** 1 of 2 (Task 1 complete; Task 2 is the plan's own `checkpoint:human-verify` and is explicitly not executed this session per the mid-plan directive below)
- **Files modified:** 13 (4 created, 9 modified)

## Mid-Plan Directive (overrides the plan's own Task 2 mechanics)

The orchestrator/coordinator sent an explicit directive partway through execution:
production infra changes for this plan are pre-authorized, but the apply mechanism
MUST be the repo's established GitHub Actions path — not a local `terraform`/
`terragrunt apply` from this executor session. The flow is: push the phase branch →
PR → `terragrunt-plan.yml` auto-runs (paths: `infra/**`) → plan output reviewed →
the orchestrator dispatches `terragrunt-apply.yml` (`workflow_dispatch`, the
human-gated `terraform-apply` environment). This SUMMARY documents that split:
Task 1 (author + locally validate, done) vs. Task 2 (CI-gated apply + live
confirmation, pending).

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
   `a7acc34` (feat)

Task 2 (the plan's own `checkpoint:human-verify`, `gate="blocking"`) has NOT been
executed — see "Deploy Checkpoint" below.

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

See `key-decisions` in frontmatter for the full list. Summary: (1) Task 2 deferred
to the GitHub-Actions-gated apply path per explicit mid-plan directive; (2) the
shared ecs-service module needed a genuinely-necessary per-service SG override fix
(Rule 2, not optional); (3) `readonly_root_filesystem = false` for this one
container, matching Asterisk's real runtime needs; (4) build FROM the proven
Phase-11 Asterisk base image + a uv-managed Python 3.12, rather than a from-scratch
or multi-stage-copy image; (5) explicit `unset` of the SIP credential before
exec'ing the Python controller.

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

None beyond the mid-plan directive changing Task 2's execution mechanism (documented
above, not a bug).

## User Setup Required

**Before the CI-gated apply can run successfully, the following SSM SecureString
parameters must exist** (per `docs/operators/voipms-provisioning-runbook.md`,
`klanker-application` account, `us-east-1`):

| Parameter | Populated? (per 12-05/12-06 SUMMARYs + this session) |
|---|---|
| `/kmv/secrets/use1/voipms/sip_username` | Unknown — not confirmed this session; runbook step 5 |
| `/kmv/secrets/use1/voipms/sip_password` | Unknown — not confirmed this session; runbook step 5 |
| `/kmv/secrets/use1/asterisk/ari_username` | **Likely NOT populated** — no prior 12-0x SUMMARY records creating this parameter |
| `/kmv/secrets/use1/asterisk/ari_password` | **Likely NOT populated** — same as above |
| `/kmv/secrets/use1/telephony/endpoint_auth_token` | Unknown — runbook lists it, not confirmed created |
| `/kmv/secrets/use1/telephony/access_pin` | **Likely NOT populated** — no prior 12-0x SUMMARY records creating this parameter |
| `/kmv/secrets/use1/telephony/passphrase_words` | **Likely NOT populated** — same as above |

None of the prior 12-01..12-06 execution SUMMARYs record actually running the
`aws ssm put-parameter` commands for any of these seven — they document the
DynamoDB-side seed data (12-05) and the code paths that CONSUME these env vars
(12-04/12-06), not the SSM writes themselves. **This is very likely the single
biggest blocker to a successful `terragrunt apply` of the ECS task definition** — if
any `valueFrom` ARN points at a parameter that doesn't exist, the ECS task will fail
to start with a `ResourceNotFoundException` from the execution role's SSM
`GetParameters` call. An operator must run the `aws ssm put-parameter --type
SecureString` commands from the runbook's "Secrets → SSM" section (both the
original 6-row table and this plan's added 4-row table) before dispatching
`terragrunt-apply.yml`.

## Deploy Checkpoint (Task 2 — NOT executed this session)

**Type:** human-verify / CI-gated apply
**Plan:** 12-07
**Status:** Task 1 (IaC authoring) complete and committed (`a7acc34`); Task 2 (deploy
+ live posture confirmation) pending.

### (a) Terragrunt `apply` dispatch — exact modules

The three Terragrunt units this plan's changes touch, in dependency order (network
must apply before ecs-service, since ecs-service depends on network's new
`telephony_edge_security_group_id` output; ecs-task has no changes from this plan
but must already be applied/current for `ecs-service`'s `task_definitions`
dependency to resolve):

```
region/us-east-1/network
region/us-east-1/ecr
region/us-east-1/ecs-task
region/us-east-1/ecs-service
```

If `terragrunt-apply.yml`'s `modules` input takes a comma-separated list matching
this repo's existing `deploy.yml`/other workflow conventions, dispatch with:

```
modules=region/us-east-1/network,region/us-east-1/ecr,region/us-east-1/ecs-task,region/us-east-1/ecs-service
```

(`ecr` is included because the new `telephony-edge` ECR repository must exist
before an image can be pushed to it — check whether the repo's existing CI
convention applies `ecr` implicitly/always or needs to be listed explicitly.)

### (b) What a clean/expected plan should show

- **`region/us-east-1/network`:** 1 resource ADD (`aws_security_group.telephony_edge`
  with 16 ingress rules — 8 CIDRs × 2 port ranges — plus 1 egress rule) and 1 output
  ADD (`telephony_edge_security_group_id`). **Zero changes** to any existing
  resource (sshhttps/http_only/webrtc_udp/nlb/vpc/subnets/alb) — this is a purely
  additive security group.
- **`region/us-east-1/ecr`:** 1 resource ADD (the `telephony-edge` ECR repository +
  its lifecycle policy). Zero changes to `auth-app`/`voice-app` repos.
- **`region/us-east-1/ecs-task`:** 1 resource ADD (the `telephony-edge` task
  definition, revision 1) + 1 resource ADD (the dedicated `telephony-edge` task
  IAM role, since `task_role_policy_statements` is non-empty). Zero changes to
  `voice`/`auth` task definitions or their existing roles.
- **`region/us-east-1/ecs-service`:** 1 resource ADD (the `telephony-edge` ECS
  service, `desired_count = 1`, `assign_public_ip = true`, network_configuration
  using ONLY the new `telephony_edge` SG — verify the plan does NOT show
  `sshhttps`/`http_only`/`webrtc_udp` attached to this service). Zero changes to
  `voice`/`auth` services (their `network_configuration.security_groups` is
  unchanged — the module change is additive/backward-compatible; confirm the plan
  shows **no diff** on those two services as the key regression check).
- **Total across all four units:** ~5 resource adds, 0 changes, 0 destroys. A plan
  showing any DESTROY or in-place MODIFY on an existing voice/auth/network resource
  means something in this session's understanding of the shared-module wiring was
  wrong — do not apply, investigate first.

### (c) SSM parameters required BEFORE apply (else the ECS task fails to start)

See the "User Setup Required" table above — all 7 `voipms`/`asterisk`/`telephony`
SSM SecureString parameters. **None were confirmed populated by any prior 12-0x
SUMMARY**; this must be checked/completed before (or the ECS service will spin up
in a perpetual `STOPPED`/`ResourceNotFoundException` failure loop, though it costs
nothing since Fargate only bills running tasks).

### (d) Post-apply verification steps

1. `aws ecs describe-services --cluster app-use1-kmv --services telephony-edge-use1
   --query 'services[0].{status:status,runningCount:runningCount,desiredCount:
   desiredCount}'` — expect `runningCount: 1`.
2. `aws ecs list-tasks --cluster app-use1-kmv --service-name telephony-edge-use1`
   then `aws ecs describe-tasks` on the returned task ARN — confirm `lastStatus:
   RUNNING`, note the attached public IP (via the task's `attachments[].details`
   `networkInterfaceId` → `aws ec2 describe-network-interfaces`).
3. `aws logs tail /ecs/telephony-edge --follow` (or the actual log group name
   `enable_logging` produces) — confirm: `render_configs.py` rendered without
   leaving `${VOIPMS_SIP_USERNAME}`/`${VOIPMS_SIP_PASSWORD}` as literal unsubstituted
   text (that would mean the SSM parameters were empty/missing); `Asterisk Ready.`;
   a successful OUTBOUND REGISTER to `toronto.voip.ms` (Asterisk logs a 200 OK on
   registration, not a repeated retry-with-error); the controller log line
   `telephony controller starting: ... require_gate=True`.
4. `aws ec2 describe-security-groups --group-ids <telephony_edge_sg_id>
   --query 'SecurityGroups[0].IpPermissions'` — confirm ingress is exactly the 8
   Toronto POP `/32`s on UDP 5060 and UDP 20000-20100, nothing else, no
   `0.0.0.0/0`.
5. Confirm there is NO target group / load balancer listener referencing the
   telephony-edge service (`aws elbv2 describe-target-groups` should show nothing
   for this service) — proves ARI stayed private-only.
6. Confirm no inbound SIP port had to be opened for registration to succeed — the
   ONLY way traffic reaches Asterisk is the outbound REGISTER + the SG's POP-locked
   ingress for the return leg, never a listener VoIP.ms initiates cold.

## Next Phase Readiness

- Task 1's IaC is authored, locally validated (hclfmt, `terraform validate` on both
  modified modules, and a real `docker build`+`docker run` proving the entrypoint's
  render→start→scrub→exec chain), and committed.
- **Blocking for Task 2 / the CI apply:** the 7 SSM SecureString parameters (see
  User Setup Required) must be confirmed populated first.
- **12-08** (the manual cellular proof, depends on `12-07`) cannot start until Task 2
  completes — a real deployed edge is its prerequisite.
- No code-level blockers; the blockers are entirely: (1) SSM parameter population,
  (2) the CI-gated `terragrunt-apply.yml` dispatch itself.

## Self-Check: PASSED

- FOUND: infra/terraform/live/site/services/telephony-edge/service.hcl
- FOUND: infra/terraform/live/site/region/us-east-1/network/telephony-sg.hcl
- FOUND: apps/voice/asterisk/Dockerfile
- FOUND: apps/voice/asterisk/entrypoint.sh
- FOUND commit: a7acc34
- `terraform validate` re-run clean on both modified modules
- `terragrunt hcl format --check` re-run clean on both new files
- `grep` re-run: zero `0.0.0.0/0` on telephony_edge SG ingress; zero secret literals
  in Dockerfile/entrypoint.sh
- Full apps/voice suite re-run: 417 passed, 53 skipped, 0 failed

---
*Phase: 12-voip-ms-telephony-inbound-did*
*Completed: 2026-07-12 (Task 1 only; Task 2 pending CI-gated apply)*
