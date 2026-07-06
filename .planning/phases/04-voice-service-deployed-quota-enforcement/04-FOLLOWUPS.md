# Phase 4 — Post-ship follow-ups

Phase 4 shipped (merged to main via PR #2). Voice service is live at voice.klankermaker.ai
(image 0.1.0, task def rev 2), deployed via a local admin `terragrunt apply`. These are the
tracked follow-ups — none block Phase 4 functionality; they harden the deploy/ops path.

## 1. [HIGH] CI deploy role (`kmv-github-deploy`) lacks IAM perms for the per-task roles

**Symptom:** `deploy.yml` (workflow_call from build-voice.yml, and workflow_dispatch) fails at
`terragrunt apply` on the ecs-task unit:
```
AccessDenied: kmv-github-deploy is not authorized to perform iam:ListRolePolicies
  on role voice-use1-kmv-execution-role  (and voice-use1-kmv-task-role)
```
**Cause:** the CI deploy role was scoped for the Phase-2 baseline (one shared cluster task
role). 04-02's Rule-2 deviation added dedicated per-task IAM roles (execution + task role with
inline policies); `kmv-github-deploy` has no IAM management rights over them. Local deploys
worked only because they used `klanker-terraform` (AdministratorAccess).
**Fix (deliberate, least-privilege — do NOT just grant iam:\*):** in `site.hcl`'s `github_oidc`
`deploy` role, add an inline statement granting the IAM role-management actions terraform needs
on the task/execution role name patterns:
- Actions: `iam:GetRole`, `iam:ListRolePolicies`, `iam:GetRolePolicy`, `iam:ListAttachedRolePolicies`,
  `iam:CreateRole`, `iam:DeleteRole`, `iam:PutRolePolicy`, `iam:DeleteRolePolicy`,
  `iam:AttachRolePolicy`, `iam:DetachRolePolicy`, `iam:TagRole`, `iam:UntagRole`,
  `iam:UpdateAssumeRolePolicy`, `iam:PassRole`
- Resources: `arn:aws:iam::*:role/*-${site.label}-task-role`, `arn:aws:iam::*:role/*-${site.label}-execution-role`
  (mirror the PassRole pattern already present on the release role).
- Also confirm the deploy role has `ecs:*` (register task def + update service), `application-autoscaling:*`,
  `elasticloadbalancing:*` (target group/listener rule), `logs:*`, and `kms:Decrypt` on the SSM/SOPS CMKs.
Apply the `global/github-oidc` unit with admin creds, then re-run `deploy.yml` (workflow_dispatch,
service=voice) to confirm green. The build half of CI is already validated.

## 2. [MED] DNS records not terraform-managed

`voice.klankermaker.ai` (and `auth.klankermaker.ai`) A-alias → ALB records were created via
CLI (route53 change-resource-record-sets), not terraform — the site module only does NS
delegation. Add a terraform resource (per-service subdomain → ALB alias, EvaluateTargetHealth=false)
so DNS is reproducible. Lives in the management account (use the klanker-management profile).

## 3. [LOW] Deployed voice-to-voice latency re-measurement

The ~1402ms local baseline hasn't been re-measured against the deployed us-east-1 path. Rides
the Phase-5 real-device pass (INFR / latency-v2 tie-in).

## Done / validated
- Image tag is now a `TF_VAR` (`TF_VAR_VOICE_IMAGE_TAG`, default 0.1.0); `deploy.yml` passes
  `github.sha` — so once #1 is fixed, CI deploys the immutable built image cleanly.
- CI build path validated end-to-end (native amd64 build + ECR push via `kmv-github-release` OIDC).
