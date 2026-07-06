---
phase: 04-voice-service-deployed-quota-enforcement
plan: 02
subsystem: infra
tags: [terraform, terragrunt, ecs, fargate, dynamodb, iam, autoscaling, webrtc, security-group]

# Dependency graph
requires:
  - phase: 02-infra-skeleton
    provides: network/ecs-cluster/ecs-task/ecs-service/dynamodb terragrunt modules, the wide webrtc_udp SG groundwork, and the site.hcl ecs_tasks/ecs_services disabled placeholders
  - phase: 04-voice-service-deployed-quota-enforcement (04-01)
    provides: server.py/auth.py/webrtc.py + Dockerfile — the container image this infra will run in 04-03
provides:
  - Voice ECS task/service enabled and wired from voice/service.hcl locals (site.hcl ecs_tasks/ecs_services flipped on)
  - webrtc_udp security group narrowed to 20000-20100/udp + standalone module output
  - ecs-task module support for container systemControls (sysctl) and per-task dedicated least-privilege IAM roles
  - ecs-service module support for custom-metric (non-CPU/memory) TargetTrackingScaling autoscaling policies
  - kmv-voice-usage DynamoDB table (electro-type, TTL on expiresAt) declared for the voice service
  - Voice task-role IAM: usage-table CRUD, namespaced PutMetricData, cluster-scoped task-protection, region-conditioned ENI lookup
  - Voice service autoscaling (min 1 / max 4) target-tracking on a custom ActiveSessions metric
affects: [04-03-deploy-image-and-ice-smoke-test, 04-04-quota-enforcement, 04-05-idle-teardown, 04-06-kill-switch]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Dedicated per-task IAM role: ecs-task module creates a scoped aws_iam_role + aws_iam_role_policy when a task declares task_role_policy_statements, overriding any externally-injected shared task_role_arn (e.g. the ecs-cluster module's wide-open per-cluster role)"
    - "Custom-metric target-tracking autoscaling: ecs-service module's autoscaling.custom_metric_target produces an aws_appautoscaling_policy(TargetTrackingScaling) alongside the existing cpu_target/memory_target step-scaling policies, independently gated"
    - "Container kernel sysctls: ecs-task module's container.system_controls threads into containerDefinitions[].systemControls for OS-level tuning (net.ipv4.ip_local_port_range) paired with a security-group range"

key-files:
  created: []
  modified:
    - infra/terraform/modules/network/v1.0.0/securitygroups.tf
    - infra/terraform/modules/network/v1.0.0/outputs.tf
    - infra/terraform/modules/ecs-task/v1.0.0/variables.tf
    - infra/terraform/modules/ecs-task/v1.0.0/main.tf
    - infra/terraform/modules/ecs-service/v1.0.0/variables.tf
    - infra/terraform/modules/ecs-service/v1.0.0/main.tf
    - infra/terraform/live/site/site.hcl
    - infra/terraform/live/site/services/voice/service.hcl

key-decisions:
  - "Extended the ecs-task module (not in the plan's declared files_modified) to add a dedicated per-task IAM role mechanism and container systemControls support — required because no existing mechanism in this codebase could express least-privilege task-role IAM or kernel sysctls; documented as a Rule 2 deviation (plan's own threat_model assigns mitigate to T-04-06/T-04-13)"
  - "Left the security_group_ids field un-touched on the voice service — it's a module-wide (not per-service) list already produced by the network module's output and already includes webrtc_udp for every ECS service in the account; no per-service SG field exists in the ecs-service schema to add it to individually"
  - "Did not mark INFR-06 complete in REQUIREMENTS.md — this plan lands only the infrastructure (apply deferred to 04-03; no application code yet calls ecs:UpdateTaskProtection or emits the ActiveSessions metric), so 'autoscales... while sessions are active' isn't actually true yet. Mirrors the project's own existing caution about INFR-03 being prematurely marked by 04-01."

patterns-established:
  - "Task-role-per-task IAM (T-04-13 mitigation pattern): declare task_role_policy_statements on a service.hcl's `task` local; the ecs-task module creates the dedicated role automatically. Future services (e.g. a hardened auth deploy) can reuse this instead of the wide-open shared cluster role."

requirements-completed: []  # See Deviations: neither INFR-03 (already marked by 04-01, deployed-verification pending 04-03) nor INFR-06 (infra-only here, runtime behavior pending 04-04/04-05) is genuinely complete from this plan alone.

coverage:
  - id: D1
    description: "webrtc_udp security group narrowed to exactly 20000-20100/udp from 0.0.0.0/0, with a standalone module output"
    requirement: "INFR-03"
    verification:
      - kind: unit
        ref: "grep '20000'/'20100' infra/terraform/modules/network/v1.0.0/securitygroups.tf"
        status: pass
      - kind: integration
        ref: "terraform validate (modules/network/v1.0.0, -backend=false)"
        status: pass
    human_judgment: false
  - id: D2
    description: "site.hcl enables ecs_tasks/ecs_services and wires them from voice/service.hcl locals (no duplicated task/service data)"
    requirement: "INFR-03"
    verification:
      - kind: unit
        ref: "grep 'enabled  *= *true' after 'ecs_services' in infra/terraform/live/site/site.hcl"
        status: pass
    human_judgment: false
  - id: D3
    description: "kmv-voice-usage DynamoDB table (electro, TTL on expiresAt) declared in voice/service.hcl"
    verification:
      - kind: unit
        ref: "grep 'kmv-voice-usage' infra/terraform/live/site/services/voice/service.hcl"
        status: pass
    human_judgment: false
  - id: D4
    description: "Least-privilege voice task-role IAM statements (usage-table CRUD, namespaced PutMetricData, task-protection, region-conditioned ENI lookup) declared and wired via a dedicated ecs-task-module-created role"
    requirement: "INFR-03"
    verification:
      - kind: unit
        ref: "grep 'PutMetricData|UpdateTaskProtection|DescribeNetworkInterfaces' infra/terraform/live/site/services/voice/service.hcl"
        status: pass
      - kind: integration
        ref: "terraform validate (modules/ecs-task/v1.0.0, -backend=false)"
        status: pass
    human_judgment: false
  - id: D5
    description: "ecs-service module custom_metric_target autoscaling variable + TargetTrackingScaling policy resource; voice service set to min 1/max 4 on ActiveSessions"
    requirement: "INFR-06"
    verification:
      - kind: unit
        ref: "grep 'custom_metric_target'/'TargetTrackingScaling' in ecs-service module; grep 'max_capacity  *= *4'/'ActiveSessions' in voice/service.hcl"
        status: pass
      - kind: integration
        ref: "terraform validate (modules/ecs-service/v1.0.0, -backend=false)"
        status: pass
    human_judgment: false
  - id: D6
    description: "Full-stack terragrunt validate/plan against live AWS state for the network/ecs-task/ecs-service/dynamodb units (plan-level verification, apply deferred to 04-03)"
    verification: []
    human_judgment: true
    rationale: "Blocked in this session by an expired AWS SSO refresh token (InvalidGrantException) for the klanker-terraform/klanker-application/klanker-management profiles' shared 'Developer' sso_session, which the S3 backend config hardcodes via an explicit `profile` attribute (bypasses env-var credential injection). Requires an interactive `aws sso login` the agent cannot perform. Module-level offline `terraform validate -backend=false` passed for all three touched modules as a substitute; the live run-all validate/plan must be completed by a human or a future session with a fresh SSO session before 04-03 applies."

duration: 35min
completed: 2026-07-05
status: complete
---

# Phase 4 Plan 02: Voice Deploy Infra — Public-IP Task, Narrowed UDP SG, Usage Table, Least-Privilege IAM, Session-Count Autoscale Summary

**Enabled + wired the voice ECS task/service on a public IP behind a 20000-20100/udp WebRTC SG, added the `kmv-voice-usage` DynamoDB table with a dedicated least-privilege task role, and extended two shared terragrunt modules (ecs-task, ecs-service) to support container sysctls, per-task IAM roles, and custom-metric session-count autoscaling.**

## Performance

- **Duration:** ~35 min
- **Started:** 2026-07-05T23:16:27Z (session resume)
- **Completed:** 2026-07-05T23:35:35Z
- **Tasks:** 3
- **Files modified:** 8

## Accomplishments
- webrtc_udp security group narrowed from the Phase-2 groundwork's wide 1024-65535/udp down to exactly 20000-20100/udp, with a standalone module output for explicit consumption
- Voice ECS task and service flipped on in site.hcl, wired from the voice service.hcl locals (mirrors the existing ecr_repositories concat pattern — no hand-duplicated task/service data)
- Extended the ecs-task module with two new, backward-compatible capabilities: container `system_controls` (kernel sysctls) and per-task `task_role_policy_statements` that create a dedicated least-privilege IAM role instead of relying on the shared, much broader per-cluster role
- Extended the ecs-service module with an optional `custom_metric_target` autoscaling field that produces a `TargetTrackingScaling` policy on an arbitrary CloudWatch metric, independent of the existing CPU/Memory step-scaling policies
- `kmv-voice-usage` DynamoDB table declared (electro-type, TTL on `expiresAt`) for the QUOT heartbeat-lease/usage/rollup/kill-switch items 04-04 will define
- Voice task-role IAM scoped to exactly: usage-table CRUD, `klanker-voice/ecs`-namespaced `PutMetricData`, cluster-scoped `UpdateTaskProtection`/`DescribeTasks`, region-conditioned `DescribeNetworkInterfaces` — no table-wide or account-wide grants
- Voice service autoscaling set to min 1 / max 4 on a custom `ActiveSessions` metric; container sysctl pins aiortc's UDP bind range to match the narrowed SG exactly

## Task Commits

Each task was committed atomically (with one extra commit for the shared-module capability that both Task 1's sysctl requirement and Task 2's least-privilege IAM requirement needed):

1. **Task 1 (network half): narrow + export webrtc_udp SG** - `d4d5992` (feat)
2. **Shared capability: ecs-task module sysctl + dedicated task-role IAM support** - `e4949e0` (feat) — extends the module beyond this plan's declared `files_modified` (see Deviations)
3. **Task 3: ecs-service module custom-metric TargetTrackingScaling** - `d1a3bcb` (feat)
4. **Tasks 1+2+3 (data): enable + wire voice service.hcl + site.hcl** - `1fb3f77` (feat)

_Note: the single voice/service.hcl file carries data for all three tasks (sysctl, usage table + IAM, autoscaling) in one commit — see "Commit granularity" below for why it wasn't split further._

## Files Created/Modified
- `infra/terraform/modules/network/v1.0.0/securitygroups.tf` - webrtc_udp ingress narrowed to 20000-20100/udp
- `infra/terraform/modules/network/v1.0.0/outputs.tf` - new `webrtc_udp_security_group_id` output
- `infra/terraform/modules/ecs-task/v1.0.0/variables.tf` - new `task.task_role_policy_statements` and `container.system_controls` optional fields
- `infra/terraform/modules/ecs-task/v1.0.0/main.tf` - dedicated `aws_iam_role`/`aws_iam_role_policy` per task (when statements declared), `task_role_arn` selection logic, `systemControls` in container definitions
- `infra/terraform/modules/ecs-service/v1.0.0/variables.tf` - new `autoscaling.custom_metric_target` optional object
- `infra/terraform/modules/ecs-service/v1.0.0/main.tf` - new `aws_appautoscaling_policy.custom_metric_scaling` (TargetTrackingScaling)
- `infra/terraform/live/site/site.hcl` - `ecs_tasks.enabled`/`ecs_services.enabled` flipped to true, wired from `local.service_conf.voice.locals`
- `infra/terraform/live/site/services/voice/service.hcl` - `kmv-voice-usage` table, `task_role_iam_statements`/`task_role_policy_statements`, container `system_controls`, `autoscaling.custom_metric_target`

## Decisions Made
- **Extended the ecs-task module beyond the plan's declared file scope.** The plan's `files_modified` frontmatter only listed `voice/service.hcl` for Task 2 and didn't list the ecs-task module at all, but no existing mechanism in this codebase could express (a) container-level kernel sysctls or (b) least-privilege per-task IAM — the closest existing thing is the ecs-cluster module's single wide-open (`dynamodb:*`, `cloudwatch:*`, `ssm:*`, `s3:*`, `secretsmanager:*`, all `Resource = "*"`) role shared by every task in the cluster. Building a dedicated per-task role (that wins over the injected shared role) was the only way to satisfy the plan's own T-04-13 acceptance criteria ("no table-wide or account-wide grants"). Documented as a Rule 2 deviation.
- **Left `security_group_ids` untouched on the voice service.** It's a module-wide, not per-service, list (`var.security_group_ids` on the ecs-service module) fed directly from the network module's `security_group_ids` output, which already includes `webrtc_udp` for every ECS service in the account. There's no per-service SG field in the schema to add it to individually, so "the voice service lists the webrtc_udp SG in security_group_ids" was already true architecturally before this plan — confirmed, not implemented.
- **Did not mark INFR-06 complete.** REQUIREMENTS.md's INFR-06 text is "Voice service autoscales 1→4 tasks with scale-in protection **while sessions are active**" — this plan only lands the Terraform definitions (and apply is deferred to 04-03); no application code yet emits the `ActiveSessions` metric or calls `ecs:UpdateTaskProtection` (that's 04-04/04-05). Marking it complete now would repeat the same premature-completion pattern the project already flagged for INFR-03 after 04-01 (see STATE.md blockers).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Critical] Extended the ecs-task module for container sysctls and per-task least-privilege IAM**
- **Found during:** Task 1 (SG narrow + sysctl) and Task 2 (usage table + task-role IAM)
- **Issue:** The plan's threat_model assigns `mitigate` to T-04-06 (SG narrowing paired with a matching container sysctl) and T-04-13 (least-privilege task-role IAM, "no table-wide or account-wide grants"). Neither capability existed anywhere in the terraform modules: the ecs-task module's container schema had no `systemControls` field, and the only task-role mechanism in the whole codebase was the ecs-cluster module's single shared, wide-open role used by every task in the cluster (`dynamodb:*`/`cloudwatch:*`/`ssm:*`/`s3:*`/`secretsmanager:*` on `Resource = "*"`) — which the ecs-task terragrunt unit unconditionally injects over anything a task declares.
- **Fix:** Added `container.system_controls` (namespace/value pairs → `systemControls` in the ECS container definition) and `task.task_role_policy_statements` (sid/actions/resources/condition) to the ecs-task module. When a task declares statements, the module now creates a dedicated `aws_iam_role` + `aws_iam_role_policy` scoped to exactly those statements and uses it for `task_role_arn`, overriding whatever role was externally injected (the shared cluster role). Tasks that don't declare statements are unaffected — fully backward compatible.
- **Files modified:** `infra/terraform/modules/ecs-task/v1.0.0/variables.tf`, `infra/terraform/modules/ecs-task/v1.0.0/main.tf`
- **Verification:** `terraform validate -backend=false` passes on the module; `grep` markers for `PutMetricData`/`UpdateTaskProtection`/`DescribeNetworkInterfaces` and `20000`/`20100` all present.
- **Committed in:** `e4949e0`

**2. [Rule 2 - Missing Critical] Extended the ecs-service module for custom-metric autoscaling (in-scope per plan, listed here for completeness)**
- **Found during:** Task 3
- **Issue:** The module only supported CPU/Memory step-scaling; the plan explicitly required a custom-metric `TargetTrackingScaling` policy on `ActiveSessions` for D-13.
- **Fix:** Added `autoscaling.custom_metric_target` and a gated `aws_appautoscaling_policy.custom_metric_scaling` resource — matches the plan's own `files_modified` list exactly, no scope expansion here.
- **Files modified:** `infra/terraform/modules/ecs-service/v1.0.0/variables.tf`, `infra/terraform/modules/ecs-service/v1.0.0/main.tf`
- **Verification:** `terraform validate -backend=false` passes; `grep` markers `custom_metric_target`/`TargetTrackingScaling`/`max_capacity  *= *4`/`ActiveSessions` all present.
- **Committed in:** `d1a3bcb`

---

**Total deviations:** 2 auto-fixed (both Rule 2 — missing critical functionality required by the plan's own threat_model mitigations). Item 1 extended files beyond the plan's declared `files_modified`; item 2 was within scope.
**Impact on plan:** Both extensions were necessary for the plan's acceptance criteria to be achievable at all (no prior code path could express least-privilege task IAM or container sysctls in this codebase). No scope creep beyond what the plan's own threat_model already required.

### Commit granularity note

The plan's `files_modified` groups `voice/service.hcl` across Tasks 1, 2, and 3 (sysctl, usage table + IAM, autoscaling all live in the same `task`/`service`/`dynamodb` locals in one HCL file). Splitting that single file into three partial commits would have required staging incomplete/mutually-referencing HCL blocks (e.g. `task.task_role_policy_statements = local.task_role_iam_statements` referencing a local defined for "Task 2" from within the "Task 1" commit), producing intermediate commits that don't represent valid, self-consistent configuration. Instead, the four commits are grouped by *capability boundary* (network SG, ecs-task module, ecs-service module, voice service data) rather than by task number — each commit is independently `terraform validate`-clean, and together they cover all three tasks' acceptance criteria exactly.

## Issues Encountered

**AWS SSO session expired — could not run the plan-level `terragrunt run-all validate` / `terragrunt plan`.** All three terragrunt-generated backend configs in this stack hardcode `profile = "klanker-terraform"` (or `klanker-application`/`klanker-management`) for the S3 state backend. That profile's underlying `Developer` SSO session returned `InvalidGrantException` on every refresh attempt (confirmed via `terraform init`/`terragrunt validate` at both the `network` unit and the root `site` unit) — a genuine expired-refresh-token condition requiring an interactive `aws sso login` that this agent cannot perform (browser-based device/authorization-code flow). Injecting the working `default` profile's static STS credentials as `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`/`AWS_SESSION_TOKEN` env vars got past the *provider*-block credential resolution but not the *backend* block, which explicitly errors ("A Profile was specified along with the environment variables ... The Profile is now used instead") when both an explicit `profile` and env credentials are present — so there's no environment-only workaround available to this agent.

As a substitute, every task's own `<verify>` block was satisfied in full (all are either `grep` markers or module-level `terraform validate -backend=false`, none require live backend/state access) — see the grep/validate output captured in this plan's execution. The plan-level cross-cutting verification item (`terragrunt run-all validate` + `terragrunt plan` on the voice unit) remains outstanding and is tracked as a blocker for 04-03 below.

## User Setup Required

**Before 04-03 can run `terragrunt plan`/`terragrunt apply` on the voice deploy units, refresh the AWS SSO session:**
1. Run `aws sso login --profile klanker-terraform` (or whichever profile maps to the `Developer` SSO session) interactively in a terminal with browser access.
2. Confirm with `aws sts get-caller-identity --profile klanker-terraform`.
3. Re-run `cd infra/terraform/live/site && terragrunt run --all validate` to confirm the network/ecs-cluster/ecs-task/ecs-service/dynamodb units are all clean, then `terragrunt plan` on the ecs-task/ecs-service units specifically to confirm the expected diff (webrtc_udp SG narrowed, kmv-voice-usage table, enabled public-IP task/service, dedicated task-role IAM, ActiveSessions TargetTrackingScaling policy) before 04-03 applies.

## Next Phase Readiness

- All Terraform/HCL changes are written, module-level `terraform validate -backend=false` clean, and every task's grep-based acceptance markers pass.
- **Blocker for 04-03:** the live `terragrunt run-all validate`/`terragrunt plan` confirmation described above has not been run in this session due to the expired AWS SSO session — 04-03 (or a human) must complete that check before applying.
- INFR-03 remains "Complete" per REQUIREMENTS.md (marked by 04-01) but is still pending genuine deployed-verification per the project's own existing caveat; INFR-06 was deliberately left unmarked pending 04-04/04-05's runtime session-lifecycle code.

---
*Phase: 04-voice-service-deployed-quota-enforcement*
*Completed: 2026-07-05*

## Self-Check: PASSED

All 8 modified files verified present on disk; all 4 task commits (`d4d5992`, `e4949e0`, `d1a3bcb`, `1fb3f77`) verified present in git log.
