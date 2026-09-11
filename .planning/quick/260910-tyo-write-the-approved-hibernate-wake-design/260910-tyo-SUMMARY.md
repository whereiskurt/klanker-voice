---
id: 260910-tyo
title: Write hibernate/wake design spec
status: complete
date: 2026-09-10
---

# Quick Task 260910-tyo — Summary

## What was done

Wrote `docs/superpowers/specs/2026-09-10-hibernate-wake-design.md`, the approved design for
a third lifecycle tier — `kv hibernate` / `kv wake` — sitting between `kv pause` and
`kv destroy`. Documentation only — no code or infra changed.

## Design, in short

**Hibernate** destroys the ECS services (and with them their target groups and ALB listener
rules), the NAT Gateway, and the ALB, while retaining the NAT EIP, the Route53 zone, ACM
certs, DynamoDB, the S3 ledger, the cf-assets bucket, ECR, and the VPC. ~$60/mo → **~$14/mo**,
~20–25 minutes each way, and — unlike `kv destroy` — **no data restore is needed to return**.

**Mechanism** is a second git-tracked boolean, `hibernated`, beside the shipped `paused` flag
in `site.hcl`, with the invariant *hibernated implies paused*. A state enum was rejected
because it would rewrite shipped, tested, live-gated pause code for cosmetics.

**Two strictly sequential CI applies.** Terragrunt applies dependencies first, which is
backwards for removal, so phase 1 (`ecs-service,cloudfront`) removes everything referencing
the ALB and phase 2 (`network`) then deletes the ALB and NAT. Wake runs them in reverse,
which is terragrunt's natural order.

**Three small module changes:** a conditional ALB origin in the CloudFront module, an EIP
lifetime split in `natgw.tf`, and the flag plumbing.

## Findings that reshaped the design mid-brainstorm

All verified against the tree, not assumed:

1. **WAF is already disabled for this site** — `global/cloudfront/terragrunt.hcl:147` sets
   `waf_web_acl_arns = {}`. An earlier estimate credited hibernate with ~$5–12/mo of WAF
   savings; it earns none. The honest floor is ~$14/mo, not the ~$5–9 originally quoted to
   the operator. Corrected before the design was approved.
2. **`terragrunt exclude` skips a unit including its destroy** —
   `ecs-service/terragrunt.hcl:13-16` carries `actions = ["all"]`, so the obvious mechanism
   (`ecs_services.enabled = false`) would leave the services **running and unmanaged**: still
   billing, and still holding the listener rules that block the ALB delete. The module is
   driven by `for_each` over `var.ecs_services`, so an **empty services list with the unit
   still enabled** destroys them in-graph. This was the most dangerous wrong turn available
   and is now called out as such in the spec.
3. **`try()` does not intercept `null`** — `global/cloudfront/terragrunt.hcl:131` reads
   `try(...alb_dns_name, "")`, but the output is declared and returns `null` when the ALB is
   off (`network/v1.0.0/outputs.tf:121-124`), so `null` reaches `domain_name` and fails the
   apply. The CloudFront module change is unavoidable.
4. **The apply workflow cancels its own in-progress runs** — `terragrunt-apply.yml` sets
   `cancel-in-progress: true` on a group keyed by workflow and ref, so both phases share a
   group and an eager phase-2 dispatch would **cancel phase 1 mid-destroy**. The sequencing
   requirement is a correctness constraint, not a style choice.
5. **Empty `modules` means apply-all**, which would apply the `email` unit and steal the
   account's single active SES receipt rule set — the known, unfixed defect documented in
   `docs/operators/ses-active-rule-set-fix.md`. Both commands must refuse an empty list.
6. **S3 object content is not covered by "config is the guard"** — the SPA is published by
   `build-voice.yml:111-127`, not terraform, so a build during hibernation would sync the real
   `index.html` back over the maintenance page silently. `build-voice.yml` needs a
   `hibernated` check.
7. **`auth` is the only service in a private subnet** (`services/auth/service.hcl:230`), so
   the NAT Gateway exists solely for its egress. This also killed the partial "phones-only
   wake" idea: telephony relays SMS through auth and the CTF OTP is served by auth, so a
   phones-only tier would still need NAT and all three tasks, saving only the ALB.

## Operator decisions captured

- **Depth:** hibernate tier, built in one pass (chosen over finishing `kv destroy`, and over
  a staged NAT-first rollout).
- **NAT EIP:** retained at ~$3.60/mo. A stale VoIP.ms allowlist does not fail at wake — it
  fails later and silently, the first time someone sends an SMS or dials the OTP DID.
- **Public face:** keep CloudFront and swap in a static maintenance page, so the URL keeps
  resolving and unfurling rather than showing a mic button that cannot work.

## Cost picture

| | Running | `kv pause` | `kv hibernate` | `kv destroy` |
|---|---|---|---|---|
| AWS | ~$190 | ~$60 | **~$14** | ~$1 |
| ElevenLabs Pro (manual) | $99 | $99 | $99 | $99 |
| Restore needed to return? | no | no | no | **yes** |

## Open items

- The ~$10/mo misc floor (Route53, ~4 KMS CMKs, ECR, S3, CloudWatch Logs) is **inferred from
  resource inventory, not measured** — AWS credentials were expired throughout the session
  (`ExpiredToken`). Confirm with Cost Explorer.
- Implementation not started. No `kv hibernate`/`kv wake` code, no module changes.
- `kv destroy` (16-10, 16-11) remains planned and unexecuted; hibernate does not replace it.
- The ~20–25 min wake estimate assumes a CloudFront config update, not a create. Measure on
  the first real wake.
- Nothing pushed — branch `quick/260910-tyo-hibernate-wake-spec`, and local `main` still
  carries 3 unpushed commits from 260903-mqd.
