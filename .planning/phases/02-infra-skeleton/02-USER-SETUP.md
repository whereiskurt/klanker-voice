# Phase 02 User Setup

**Status: Complete** (role live and proven by the green INFR-07 CI run, 2026-07-05)

> **CORRECTION (2026-07-05, during 02-07):** the user-confirmed creation below turned
> out not to exist — `aws iam get-role` via the admin `sudo-management` SSO profile
> returned NoSuchEntity. The "no in-scope profile has IAM access" rationale was also
> wrong: `sudo-management` has AdministratorAccess in 481723467561. The 02-07 executor
> created the role itself from the exact 02-DELEGATE-TRUST.json spec, plus one action
> missing from the policy below: **`route53:ListTagsForResource`** (the terraform
> `aws_route53_zone` data source reads zone tags on every plan; without it all
> mgmt-provider units fail AccessDenied). The table below is the as-built record.

## Service: AWS management account (481723467561)

**Why manual (original rationale, now known false):** No in-scope profile has IAM write
(or even read) permissions in the management account — `klanker-management` is the
HostedZoneAdmin SSO role only (`iam:GetRole` probe returned AccessDenied). Creating the
CI cross-account delegate role required the user's admin access.

### Dashboard configuration — DONE (created by 02-07 executor via `sudo-management`, 2026-07-05)

| Item | Value |
|------|-------|
| Role name | `kmv-github-delegate` (exact — the generated CI management provider assumes this ARN) |
| Role ARN | `arn:aws:iam::481723467561:role/kmv-github-delegate` |
| Trust policy | Verbatim contents of `.planning/phases/02-infra-skeleton/02-DELEGATE-TRUST.json` — principals are the four `kmv-github-*` app-account roles, `Condition: StringEquals sts:ExternalId = "kmv"` (confused-deputy guard, T-2-18) |
| Permissions | Inline policy: `route53:ChangeResourceRecordSets`, `route53:ListResourceRecordSets`, `route53:GetHostedZone`, `route53:ListTagsForResource` on `arn:aws:route53:::hostedzone/Z036807010CWM2JH60RKQ` + `route53:ListHostedZones` on `*` (zone-scoped, Route53-only) |
| Location | AWS Console → 481723467561 → IAM → Roles → Create role (custom trust policy) |

### Verification

- **VERIFIED LIVE (2026-07-05):** INFR-07 proof run
  https://github.com/whereiskurt/klanker-voice/actions/runs/28726188204 — GitHub OIDC →
  `kmv-github-readonly` → cross-account `sts:AssumeRole` into `kmv-github-delegate`
  (external_id `kmv`) succeeded; all mgmt-provider units (site/dmarc/certs/email)
  planned "No changes", `Succeeded 10` units, zero static keys.

## Environment variables

None — no new local env vars required by this setup. CI reads repo variables
(set by Plan 06 Task 1); local shells read `infra/.envrc`.
