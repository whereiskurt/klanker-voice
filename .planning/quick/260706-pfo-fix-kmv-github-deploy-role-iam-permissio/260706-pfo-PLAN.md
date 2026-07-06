---
phase: quick-260706-pfo
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
  - infra/terraform/live/site/site.hcl
autonomous: true
requirements: []

must_haves:
  truths:
    - "The kmv-github-deploy CI role can run `terragrunt apply` (refresh reads role policies/tags) without AccessDenied on the deploy path."
    - "The kmv-github-deploy CI role can RegisterTaskDefinition-with-tags and tag/untag ECS resources during deploy."
  artifacts:
    - "infra/terraform/live/site/site.hcl — deploy role's ecs-deploy policy carries the three ECS tagging actions."
    - "infra/terraform/live/site/site.hcl — deploy role's iam-pass-role second statement carries the full IAM-read set plus iam:ListRoleTags."
  key_links:
    - "The deploy role's two policies mirror the proven release role's ecs-deploy (~L642) and IAMReadRoles (~L685) equivalents."
---

<objective>
Bring the CI `deploy` GitHub-OIDC role (`kmv-github-deploy`, site.hcl ~L806, `branch_restriction = "main"`) up to the permission level of the proven, working `release` role by expanding two under-provisioned inline policies. This codifies in terraform the exact permission set that was validated live (via ad-hoc inline policies, since reverted) during the Phase-5 deploy debug.

Purpose: The Phase-5 deploy debug proved `kmv-github-deploy` fails `terragrunt apply` (its refresh reads role policies/tags) and fails RegisterTaskDefinition-with-tags without these exact perms. This makes the CI deploy path work without hand-patched IAM.

Output: One edited file — infra/terraform/live/site/site.hcl — with two expanded Action lists. No terragrunt apply, no workflow changes, no change to the `release` role.
</objective>

<execution_context>
@$HOME/.claude/gsd-core/workflows/execute-plan.md
@$HOME/.claude/gsd-core/templates/summary.md
</execution_context>

<context>
@.planning/STATE.md

# The single file this plan edits. Study BOTH roles before editing:
#   - `release` role  ~L490 (the PROVEN reference; do NOT modify it)
#     - its ecs-deploy policy    ~L642-666 (tagging actions at L659-661)
#     - its IAMReadRoles stmt    ~L685-699 (the full read set to mirror)
#   - `deploy` role   ~L806 (branch_restriction = "main"; the target of BOTH edits)
#     - its ecs-deploy policy    ~L847-868 (Action list ends at ecs:ListClusters, L863)
#     - its iam-pass-role policy  ~L870-899 (GetRole stmt ~L886-896, only iam:GetRole)
@infra/terraform/live/site/site.hcl
</context>

<tasks>

<task type="auto">
  <name>Task 1: Expand the deploy role's ecs-deploy and iam-pass-role policies to match the release role</name>
  <files>infra/terraform/live/site/site.hcl</files>
  <action>
Make TWO scoped edits inside the `deploy` role block (the one with `branch_restriction = "main"`, name = "deploy", ~L806). Do NOT touch the `release` role (~L490) — it is the proven reference only.

Edit 1 — deploy role's `ecs-deploy` policy (`Sid = "ECSFullDeploy"`, ~L847-868). Its Action list currently ends at `"ecs:ListClusters"`. Append the three ECS tagging actions so the list mirrors the release role's ecs-deploy policy (which has them at ~L659-661): add `"ecs:TagResource"`, `"ecs:UntagResource"`, and `"ecs:ListTagsForResource"` after `"ecs:ListClusters"`. Add a trailing comma after `"ecs:ListClusters"` so the array stays valid HCL. Leave `Resource = "*"` unchanged.

Edit 2 — deploy role's `iam-pass-role` policy, its SECOND statement (`Sid = "GetRole"`, ~L886-896). Its Action list currently contains ONLY `"iam:GetRole"`. Replace that single-entry list with the full IAM-read set, mirroring the release role's `IAMReadRoles` statement (~L688-693) plus one extra action the Phase-5 live-deploy debug validated as needed for the deploy path. New Action list, in order: `"iam:GetRole"`, `"iam:ListRolePolicies"`, `"iam:GetRolePolicy"`, `"iam:ListAttachedRolePolicies"`, `"iam:ListInstanceProfilesForRole"`, `"iam:ListRoleTags"`. Do NOT change this statement's `Resource` block — it already carries the correct two role-ARN patterns (matching the release role). The extra `iam:ListRoleTags` is a read-only List action not present in the release role; it is deliberately included per the validated deploy path and is harmless over-permission. The `Sid` MAY be renamed from `"GetRole"` to `"IAMReadRoles"` for parity with the release role, but that is cosmetic — either value is acceptable.

Do NOT run `terragrunt apply` or `terragrunt plan`. Do NOT modify any `.github/workflows/*.yml` file. Do NOT modify the `release` role.
  </action>
  <verify>
    <automated>cd infra/terraform/live/site && terragrunt hclfmt --diff site.hcl 2>/dev/null; test -z "$(terragrunt hclfmt --diff site.hcl 2>/dev/null)" || (echo 'FALLBACK: terraform fmt' && terraform fmt -check=false site.hcl >/dev/null && terraform fmt -check site.hcl); awk '/name += +"deploy"/{d=1} d&&/ecs:TagResource/{t++} d&&/ecs:UntagResource/{t++} d&&/ecs:ListTagsForResource/{t++} d&&/iam:ListRolePolicies/{r++} d&&/iam:ListInstanceProfilesForRole/{r++} d&&/iam:ListRoleTags/{r++} END{ if(t>=3 && r>=3){print "PASS: deploy role has 3 ECS tag actions + expanded IAM-read set"; exit 0} else {print "FAIL: t="t" r="r; exit 1} }' site.hcl</automated>
  </verify>
  <done>
Inside the `deploy` role block: the `ecs-deploy` policy's Action list includes `ecs:TagResource`, `ecs:UntagResource`, and `ecs:ListTagsForResource` (mirroring the release role); the `iam-pass-role` policy's second statement Action list includes `iam:GetRole`, `iam:ListRolePolicies`, `iam:GetRolePolicy`, `iam:ListAttachedRolePolicies`, `iam:ListInstanceProfilesForRole`, and `iam:ListRoleTags`, with its `Resource` block unchanged. The file still parses as valid HCL (`terragrunt hclfmt --diff` reports no reformatting needed, or `terraform fmt` confirms formatting). The `release` role is byte-for-byte unchanged and no workflow file was touched.
  </done>
</task>

</tasks>

<verification>
- `terragrunt hclfmt --diff site.hcl` (or `terraform fmt` fallback) confirms the file is syntactically valid HCL and correctly formatted after the edits.
- `git diff infra/terraform/live/site/site.hcl` shows changes ONLY within the `deploy` role block (the two Action lists) — no hunk touches the `release` role and no `.github/workflows/*.yml` file appears in the diff.
- The deploy role's `ecs-deploy` policy Action list now matches the release role's `ecs-deploy` policy (three tagging actions present).
- The deploy role's `iam-pass-role` GetRole/IAMReadRoles statement now carries the full six-action read set.
</verification>

<success_criteria>
- Two Action lists in the `deploy` role expanded exactly as specified; `release` role untouched.
- File is valid, formatted HCL.
- No apply/plan run; no workflow files modified.
</success_criteria>

<output>
Create `.planning/quick/260706-pfo-fix-kmv-github-deploy-role-iam-permissio/260706-pfo-SUMMARY.md` when done.
</output>
