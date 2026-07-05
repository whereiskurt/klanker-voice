# Phase 02 User Setup

**Status: Complete** (all manual steps performed and confirmed 2026-07-05)

## Service: AWS management account (481723467561)

**Why manual:** No in-scope profile has IAM write (or even read) permissions in the
management account — `klanker-management` is the HostedZoneAdmin SSO role only
(`iam:GetRole` probe returned AccessDenied). Creating the CI cross-account delegate
role required the user's admin access.

### Dashboard configuration — DONE (user-confirmed, 2026-07-05)

| Item | Value |
|------|-------|
| Role name | `kmv-github-delegate` (exact — the generated CI management provider assumes this ARN) |
| Role ARN | `arn:aws:iam::481723467561:role/kmv-github-delegate` |
| Trust policy | Verbatim contents of `.planning/phases/02-infra-skeleton/02-DELEGATE-TRUST.json` — principals are the four `kmv-github-*` app-account roles, `Condition: StringEquals sts:ExternalId = "kmv"` (confused-deputy guard, T-2-18) |
| Permissions | Inline policy: `route53:ChangeResourceRecordSets`, `route53:ListResourceRecordSets`, `route53:GetHostedZone` on `arn:aws:route53:::hostedzone/Z036807010CWM2JH60RKQ` + `route53:ListHostedZones` on `*` (zone-scoped, Route53-only) |
| Location | AWS Console → 481723467561 → IAM → Roles → Create role (custom trust policy) |

### Verification

- Claude-side verification is structurally impossible (no IAM read in 481723467561);
  status rests on the user's explicit "created" confirmation with the exact trust and
  permissions JSON provided.
- Live end-to-end proof lands with the first CI run that plans a management-provider
  unit (site / certs / email / dmarc) — the workflow's `assume_role` on
  `arn:aws:iam::481723467561:role/kmv-github-delegate` with `external_id = "kmv"`
  succeeds instead of AccessDenied (Plan 07 / first PR plan).

## Environment variables

None — no new local env vars required by this setup. CI reads repo variables
(set by Plan 06 Task 1); local shells read `infra/.envrc`.
