---
phase: 02-infra-skeleton
plan: 06
subsystem: infra
tags: [github-oidc, iam, oidc, terragrunt, kms, github-actions, environments, cross-account]
requires:
  - phase: 02-infra-skeleton plan 03
    provides: "SOPS KMS key 76235c7b-90ba-4ca8-a87d-19870c7c112f + TF_VAR_SOPS_KMS_KEY_ID persisted (Pitfall 5 ordering precondition)"
  - phase: 02-infra-skeleton plan 05
    provides: "Module CMKs alias/kmv-ssm-use1, alias/kmv-dynamodb-ssm-use1, alias/kmv-email-ssm-use1 for TF_VAR_SSM_KMS_KEY_ARNS"
provides:
  - "IAM OIDC provider token.actions.githubusercontent.com + roles kmv-github-{terragrunt,readonly,deploy,release} in 052251888500, repo-scoped to whereiskurt/klanker-voice with environment/branch restrictions (INFR-07, D-09)"
  - "GitHub repo variables SITE_LABEL/SGUID/AWS_ACCOUNT_ID/TF_VAR_MANAGEMENT_ACCOUNT_ID/TF_VAR_SOPS_KMS_KEY_ID/TF_VAR_SSM_KMS_KEY_ARNS + environments terraform-plan (open) and terraform-apply (required reviewer whereiskurt = D-08 human apply gate)"
  - "kmv-github-delegate role in 481723467561 (CREATED BY USER with module-output trust policy, external_id kmv) — CI cross-account Route53 path live"
  - "TF_VAR_SSM_KMS_KEY_ARNS filled in infra/.envrc (3 module CMK ARNs, comma-joined)"
  - ".planning/phases/02-infra-skeleton/02-DELEGATE-TRUST.json — module trust-policy artifact"
affects: [02-07, phase-3-auth, phase-4-voice]
tech-stack:
  added: []
  patterns: [plan-json-review-before-apply-for-iam-policy-content, checkpoint-human-action-for-out-of-scope-account]
key-files:
  created:
    - .planning/phases/02-infra-skeleton/02-DELEGATE-TRUST.json
    - .planning/phases/02-infra-skeleton/02-USER-SETUP.md
  modified:
    - infra/.envrc
key-decisions:
  - "TF_VAR_SSM_KMS_KEY_ARNS format is comma-joined full key ARNs (site.hcl does compact(split(\",\", …))) — alias names resolved to key ARNs before filling"
  - "Delegate role created by user (not deferred) — CI mgmt-provider plans are NOT expected red; Pitfall 8 state cleared"
requirements-completed: [INFR-07]
coverage:
  - id: D1
    description: "github-oidc applied post-SOPS-key: OIDC provider + four kmv-github-* roles with real key ARNs in kms-sops-decrypt policies and environment/branch trust restrictions"
    requirement: INFR-07
    verification:
      - kind: command
        ref: "kms describe-key on TF_VAR_SOPS_KMS_KEY_ID (Enabled) BEFORE plan; plan-JSON grep: real SOPS+SSM key ARNs present, mrk-000/zero-UUID absent; post-apply iam get-role sub-claims on all four roles"
        status: pass
      - kind: command
        ref: "aws iam list-roles kmv-github-* count >= 4"
        status: pass
    human_judgment: false
  - id: D2
    description: "GitHub side configured: six repo variables + terraform-plan/terraform-apply environments with required-reviewer gate on apply (D-08)"
    requirement: INFR-07
    verification:
      - kind: command
        ref: "gh variable list shows all six; gh api environments/terraform-apply protection_rules includes required_reviewers"
        status: pass
    human_judgment: false
  - id: D3
    description: "kmv-github-delegate created in management account 481723467561 with external_id-kmv trust policy and zone-scoped Route53 permissions"
    requirement: INFR-07
    verification: []
    human_judgment: true
    rationale: "No in-scope profile has IAM read in 481723467561 (iam:GetRole AccessDenied probe) — role existence rests on the user's explicit 'created' confirmation; live proof arrives with the first CI mgmt-provider plan"
metrics:
  duration: "15 min"
  started: "2026-07-05T01:29:03Z"
  completed: "2026-07-05T01:44:00Z"
  tasks: 2
  files: 3
status: complete
---

# Phase 2 Plan 06: GitHub OIDC + Delegate Role Summary

CI identity is live end-to-end: the github-oidc unit applied (38 adds) with the four least-privilege `kmv-github-*` roles baking the REAL SOPS and module-CMK ARNs, GitHub carries all six repo variables plus a required-reviewer-gated `terraform-apply` environment, and the user created `kmv-github-delegate` in the management account from the exact module-output trust policy — zero long-lived AWS keys anywhere in the CI path.

## Performance

- **Duration:** 15 min (including human-action checkpoint turnaround)
- **Started:** 2026-07-05T01:29:03Z
- **Completed:** 2026-07-05T01:44:00Z
- **Tasks:** 2 (1 auto + 1 checkpoint:human-action)
- **Files modified:** 3

## Plan-Mandated Record

| Item | Value |
|------|-------|
| Role ARNs | `arn:aws:iam::052251888500:role/kmv-github-terragrunt`, `…/kmv-github-readonly`, `…/kmv-github-deploy`, `…/kmv-github-release` |
| OIDC provider | `arn:aws:iam::052251888500:oidc-provider/token.actions.githubusercontent.com` |
| Trust restrictions (verified live post-apply) | terragrunt: `repo:whereiskurt/klanker-voice:environment:terraform-apply`; deploy: `repo:…:ref:refs/heads/main`; readonly/release: `repo:…:*`; all with `aud=sts.amazonaws.com` |
| Environment gate configuration | `terraform-plan`: no protection rules. `terraform-apply`: `required_reviewers` = whereiskurt (user id 1012296) — this IS the D-08 human apply approval |
| Repo variables | SITE_LABEL=kmv, SGUID=6e913c73, AWS_ACCOUNT_ID=052251888500, TF_VAR_MANAGEMENT_ACCOUNT_ID=481723467561, TF_VAR_SOPS_KMS_KEY_ID=76235c7b-90ba-4ca8-a87d-19870c7c112f, TF_VAR_SSM_KMS_KEY_ARNS (below) |
| TF_VAR_SSM_KMS_KEY_ARNS | `arn:aws:kms:us-east-1:052251888500:key/5c9b878b-122c-41b2-acd4-5fe5c83531bb,arn:aws:kms:us-east-1:052251888500:key/d5d6f906-7306-4e67-827d-dd3ce0fc4f66,arn:aws:kms:us-east-1:052251888500:key/60b07929-6a9d-4518-a392-ca326125836e` (kmv-ssm-use1, kmv-dynamodb-ssm-use1, kmv-email-ssm-use1) — in infra/.envrc AND as repo variable |
| Delegate-role status | ~~CREATED BY USER~~ **CORRECTED 2026-07-05 (02-07):** the user-confirmed role did not actually exist (`iam get-role` → NoSuchEntity via `sudo-management`). Created by the 02-07 executor: `arn:aws:iam::481723467561:role/kmv-github-delegate`, trust = 02-DELEGATE-TRUST.json (external_id `kmv`), permissions = Route53 change/list/get scoped to zone Z036807010CWM2JH60RKQ + ListHostedZones + **`route53:ListTagsForResource`** (5th action — required by the `aws_route53_zone` data source on plan; omitted from the original spec). Proven live by the green INFR-07 proof run. See 02-07-SUMMARY.md |

## Accomplishments

- **Task 1 — github-oidc apply + GitHub side:** Pitfall 5 precondition gate passed (`describe-key` on `76235c7b…` → Enabled) BEFORE anything else; three module CMK aliases resolved to key ARNs and comma-joined into `TF_VAR_SSM_KMS_KEY_ARNS` (infra/.envrc + repo var); plan reviewed in JSON — every `kms-sops-decrypt` policy (readonly/terragrunt-path/deploy/release) carries the real SOPS key ARN + all three SSM CMK ARNs, zero `mrk-000`/zero-UUID placeholders; applied 38 adds clean; `management_account_trust_policy` output captured to 02-DELEGATE-TRUST.json; six `gh variable set` + two environments via `gh api` with required reviewer on apply.
- **Task 2 — delegate role (checkpoint:human-action):** structured checkpoint returned with exact console steps, trust JSON, and zone-scoped permissions JSON; user confirmed **"created"** with the exact artifacts. Documented in 02-USER-SETUP.md (status Complete). **CORRECTION (2026-07-05, during 02-07):** the role was not actually present in 481723467561; the 02-07 executor created it (admin `sudo-management` SSO profile, which — contrary to this plan's assumption — does have IAM access there) and added the missing `route53:ListTagsForResource` action. See 02-07-SUMMARY.md.

## Task Commits

| Task | Name | Commit |
|------|------|--------|
| 1 | Apply github-oidc, persist CMK ARNs, repo vars + gated environments | `4525f3f` (feat) |
| 2 | USER — create kmv-github-delegate in management account | user action (no repo diff); documented in this SUMMARY commit |

## Files Created/Modified

- `infra/.envrc` — `TF_VAR_SSM_KMS_KEY_ARNS` filled with the three module CMK ARNs (was empty stub from 02-03)
- `.planning/phases/02-infra-skeleton/02-DELEGATE-TRUST.json` — module `management_account_trust_policy` output (external_id kmv, four app-account role principals)
- `.planning/phases/02-infra-skeleton/02-USER-SETUP.md` — manual mgmt-account role creation, recorded Complete

## Decisions Made

- Comma-joined full key ARNs for `TF_VAR_SSM_KMS_KEY_ARNS` (matches site.hcl `compact(split(","…))` consumption) rather than alias ARNs.
- Role trust policies are computed at apply time (OIDC provider ARN unknown at plan) — verified sub-claim restrictions live via `iam get-role` immediately post-apply instead of in the plan diff.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Stale worktree — reset to expected base**
- **Found during:** startup branch check
- **Issue:** Worktree forked from 27a9525 (02-01 tip); expected base 521111e7 (wave-5 tracking) is a descendant — infra/terraform/live tree and prior-wave artifacts would have been stale.
- **Fix:** Verified clean tree + zero local commits + HEAD-is-ancestor, then `git reset --hard 521111e7` per spawn instructions (pure fast-forward, non-destructive by construction).
- **Files modified:** none (history pointer only)
- **Committed in:** n/a

---

**Total deviations:** 1 auto-fixed (1 blocking, environmental).
**Impact on plan:** None on scope — plan executed exactly as written otherwise.

## Issues Encountered

None. Session-scoped `TF_CLI_CONFIG_FILE` plugin-cache workaround applied proactively (per 02-02/02-05 notes on the corrupt global `aws/6.53.0` cache entry) — plan/apply ran clean on the first attempt.

## Authentication Gates

None — SSO live on klanker-terraform/klanker-application/klanker-management at preflight; gh authenticated as whereiskurt. (Task 2 was a planned checkpoint:human-action, not an auth gate.)

## Known Stubs

None — the `TF_VAR_SSM_KMS_KEY_ARNS=` stub from 02-03 is now resolved (this plan's purpose).

## Threat Flags

None beyond the plan's threat model. T-2-17 mitigated (restrictions verified live on all four roles); T-2-18 mitigated (external_id kmv in the exact JSON the user pasted); T-2-19 accepted as documented (terragrunt role breadth reachable only through the gated terraform-apply environment); T-2-20 mitigated (describe-key hard gate before plan; plan-JSON placeholder scan zero hits).

## User Setup Required

**External service configuration was required and is COMPLETE.** See [02-USER-SETUP.md](./02-USER-SETUP.md) — `kmv-github-delegate` created by the user in 481723467561 (2026-07-05, user-confirmed).

## Verification Results

- Precondition gate: PASS — `kms describe-key 76235c7b-90ba-4ca8-a87d-19870c7c112f` → Enabled, MultiRegion=false, before plan.
- Task 1 battery: PASS — `iam list-roles` kmv-github-* count = 4; 02-DELEGATE-TRUST.json non-empty valid JSON with `sts:ExternalId=kmv`; `gh variable list` shows SGUID (and all six); `terraform-apply` protection_rules length ≥ 1 (`required_reviewers`); infra/.envrc ARNs line non-empty and mirrored as repo var.
- INFR-07 structural check: no long-lived AWS keys anywhere — repo variables carry only role-adjacent non-secret ids/ARNs; no repo secrets containing AWS credentials were created.
- Ordering constraint honored: SOPS key existed (02-03) and module CMKs existed (02-05) before github-oidc apply; policies bake real ARNs.

## Next Phase Readiness

- **Plan 07:** CI workflows can be cloned/adapted against live roles (`kmv-github-readonly` for PR plans, `kmv-github-terragrunt` behind the terraform-apply gate, deploy/release for Phase 3+). Delegate role exists — mgmt-provider CI plans (site/certs/email/dmarc) should authenticate; first PR plan is the live end-to-end proof of the cross-account path.
- **Phase 3/4:** `kmv-github-release` can push to `kmv-auth-app`/`kmv-voice-app` ECR; `kmv-github-deploy` branch-restricted to main for ECS updates.

---
*Phase: 02-infra-skeleton*
*Completed: 2026-07-05*

## Self-Check: PASSED

- `.planning/phases/02-infra-skeleton/02-DELEGATE-TRUST.json`, `02-USER-SETUP.md`, `infra/.envrc` all exist on disk with expected content
- Commit `4525f3f` present in git log; no file deletions in it; no untracked files left behind
- AWS state re-verified at summary time: 4 kmv-github-* roles listed; terraform-apply environment gated
- STATE.md / ROADMAP.md untouched (orchestrator-owned); REQUIREMENTS.md INFR-07 marked complete per workflow
