---
phase: quick-260706-pfo
plan: 01
subsystem: infra
tags: [terraform, terragrunt, iam, github-oidc, ecs, deploy]

requires:
  - phase: quick-260706 (Phase 5 deploy debug, ad-hoc)
    provides: the validated-live permission set for kmv-github-deploy (previously applied as reverted ad-hoc inline policies)
provides:
  - Two expanded inline IAM policies on the kmv-github-deploy CI role's terraform-managed IAM group, mirroring the proven release role
affects: [ci-deploy, terraform-live-site]

tech-stack:
  added: []
  patterns:
    - "deploy role IAM inline policies mirror the release role's proven policies (ecs-deploy, iam-pass-role) rather than diverging permission sets per role"

key-files:
  created: []
  modified:
    - infra/terraform/live/site/site.hcl

key-decisions:
  - "Renamed the deploy role's iam-pass-role second statement Sid from GetRole to IAMReadRoles for naming parity with the release role's equivalent statement (cosmetic, no functional change)"
  - "Included iam:ListRoleTags in the deploy role's IAM-read set even though the release role's IAMReadRoles statement doesn't have it -- validated live during the Phase-5 deploy debug as needed for the deploy path (harmless read-only over-permission)"

patterns-established: []

requirements-completed: []

coverage:
  - id: D1
    description: "deploy role's ecs-deploy policy Action list gains the three ECS tagging actions (TagResource/UntagResource/ListTagsForResource), matching the release role"
    verification:
      - kind: other
        ref: "awk verification script confirming all 3 ECS tag actions present within the deploy role block of site.hcl"
        status: pass
    human_judgment: false
  - id: D2
    description: "deploy role's iam-pass-role second statement (Sid renamed GetRole -> IAMReadRoles) expanded from iam:GetRole only to the full 6-action IAM-read set (GetRole/ListRolePolicies/GetRolePolicy/ListAttachedRolePolicies/ListInstanceProfilesForRole/ListRoleTags), Resource block unchanged"
    verification:
      - kind: other
        ref: "awk verification script confirming all 3 new IAM-read actions present within the deploy role block of site.hcl"
        status: pass
    human_judgment: false
  - id: D3
    description: "release role and all .github/workflows/*.yml files remain byte-for-byte unchanged; no terragrunt apply/plan executed"
    verification:
      - kind: other
        ref: "git diff infra/terraform/live/site/site.hcl -- confirms only two hunks, both within the deploy role block (lines ~860, ~887); git status confirms no workflow files touched"
        status: pass
    human_judgment: false

duration: 2min
completed: 2026-07-06
status: complete
---

# Quick Task 260706-pfo: Expand kmv-github-deploy IAM permissions Summary

**Expanded the CI `deploy` GitHub-OIDC role's ecs-deploy and iam-pass-role inline policies in site.hcl to mirror the proven `release` role, codifying in terraform the permission set validated live (via now-reverted ad-hoc inline policies) during the Phase-5 deploy debug.**

## Performance

- **Duration:** ~2 min
- **Started:** 2026-07-06T22:21:54Z
- **Completed:** 2026-07-06T22:23:43Z
- **Tasks:** 1
- **Files modified:** 1

## Accomplishments
- `deploy` role's `ecs-deploy` policy (`ECSFullDeploy` Sid) Action list now includes `ecs:TagResource`, `ecs:UntagResource`, `ecs:ListTagsForResource` — required for `RegisterTaskDefinition`-with-tags and ECS tag/untag calls during CI deploy.
- `deploy` role's `iam-pass-role` policy's second statement (`GetRole` -> renamed `IAMReadRoles`) Action list expanded from just `iam:GetRole` to the full six-action IAM-read set (`GetRole`, `ListRolePolicies`, `GetRolePolicy`, `ListAttachedRolePolicies`, `ListInstanceProfilesForRole`, `ListRoleTags`) — required for `terragrunt apply`'s refresh to read role policies/tags without `AccessDenied`.
- Verified the `release` role (the proven reference, ~L490-701) is byte-for-byte unchanged, and no `.github/workflows/*.yml` file was touched.
- Confirmed `site.hcl` is valid, correctly-formatted HCL (checked via an isolated copy with `terragrunt hcl format --diff`, avoiding side effects on sibling files in the live repo).

## Task Commits

Each task was committed atomically:

1. **Task 1: Expand the deploy role's ecs-deploy and iam-pass-role policies to match the release role** - `0b5cb0d` (fix)

**Plan metadata:** (handled by orchestrator — SUMMARY.md/STATE.md/PLAN.md not committed by this agent per instructions)

## Files Created/Modified
- `infra/terraform/live/site/site.hcl` - Two Action-list edits inside the `deploy` role block only: `ecs-deploy` policy gains 3 ECS tag actions; `iam-pass-role` policy's `GetRole`/`IAMReadRoles` statement expanded to the 6-action IAM-read set (Resource block unchanged).

## Decisions Made
- Renamed the deploy role's `iam-pass-role` second statement `Sid` from `GetRole` to `IAMReadRoles` for parity with the release role's equivalent statement — cosmetic only, per the plan's own explicit "either value is acceptable" guidance.
- Kept `iam:ListRoleTags` in the deploy role's expanded set even though the release role's `IAMReadRoles` statement lacks it — the plan called this out as a deliberate, validated-live addition for the deploy path; harmless read-only over-permission.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Reverted accidental out-of-scope formatting changes caused by my own verification command**
- **Found during:** Task 1 verification step
- **Issue:** Running `terragrunt hcl format --diff site.hcl` in the live repo (intended as a read-only diff check per the plan's `<automated>` verify block) actually mutated two sibling files it does not govern scope over — `infra/terraform/live/site/services/auth/service.hcl` and `infra/terraform/live/site/services/voice/service.hcl` — reformatting pre-existing alignment/whitespace in unrelated statements (`sid`/`actions` alignment, `table_name`/`ttl_enabled` alignment, `name = "voice-app"`). The `--diff` flag did not prevent the write; `terragrunt hcl format` applies formatting as a side effect regardless.
- **Fix:** Reverted both files with a scoped `git checkout -- <file>` (not a blanket reset), then re-ran the same formatting check against an isolated copy of `site.hcl` in the scratchpad directory to confirm `site.hcl` itself needs no reformatting, without risking further side effects on sibling files in the live worktree.
- **Files modified:** `infra/terraform/live/site/services/auth/service.hcl`, `infra/terraform/live/site/services/voice/service.hcl` (reverted to original state; net change: none)
- **Verification:** `git status --short` confirmed only `site.hcl` remained modified after the revert; `git diff` on the two sibling files is empty.
- **Committed in:** N/A (reverted before any commit; never entered the task commit)

---

**Total deviations:** 1 auto-fixed (1 bug — self-caused, contained before commit)
**Impact on plan:** No scope creep in the final commit. The commit (`0b5cb0d`) touches only `infra/terraform/live/site/site.hcl`, exactly as the plan specifies.

## Issues Encountered
- `terragrunt hclfmt --diff` (the exact command form in the plan's `<automated>` verify block) is not a valid terragrunt global-flag invocation on the installed terragrunt version — the correct subcommand is `terragrunt hcl format --diff`. Used the corrected form; also discovered it writes to disk despite `--diff`, so re-verified `site.hcl`'s formatting via an isolated scratchpad copy rather than re-running it in place a second time.

## User Setup Required
None - no external service configuration required. Note: this change is terraform source only — `terragrunt apply` was explicitly NOT run per the plan's constraints; the new deploy-role permissions will not take effect against the live `kmv-github-deploy` IAM role until a future apply.

## Next Phase Readiness
- `site.hcl` now correctly declares the `kmv-github-deploy` role's required ECS-tagging and IAM-read permissions in terraform, closing the drift between the proven ad-hoc live fix and the source of truth.
- Follow-up (not part of this task): a `terragrunt apply` against the `iam-roles`/CI-role unit is needed to actually push these policy changes to the live AWS role before the next CI deploy relies on them.

---
*Phase: quick-260706-pfo*
*Completed: 2026-07-06*

## Self-Check: PASSED

- FOUND: infra/terraform/live/site/site.hcl
- FOUND: .planning/quick/260706-pfo-fix-kmv-github-deploy-role-iam-permissio/260706-pfo-SUMMARY.md
- FOUND: 0b5cb0d (git log --oneline --all)
