---
id: 260910-tyo
title: Write hibernate/wake design spec
status: complete
mode: quick
date: 2026-09-10
---

# Quick Task 260910-tyo: Write hibernate/wake design spec

## Goal

Capture the operator-approved design for a third lifecycle tier — `kv hibernate` /
`kv wake` — sitting between `kv pause` (~$60/mo) and `kv destroy` (~$1/mo). Destroys the
ECS services, NAT Gateway and ALB while retaining the NAT EIP, DNS, certs, DynamoDB, the
S3 ledger, the cf-assets bucket and ECR, so a return needs no data restore. Document only
— no code, no infra changes.

## Context

Operator reported costs getting out of control and asked to tear everything down except
the VoIP.ms DIDs and configs, with klanker-voice on hold indefinitely while attention
moves to defcon.run.34.

`kv pause` (Phase 16, shipped) already scales services to zero but leaves ~$60/mo of NAT
Gateway and ALB running against zero tasks. `kv destroy` is specced (2026-08-12 spec §6)
and planned (16-10, 16-11) but never built, and costs DNS, certs, bucket names, the NAT
EIP, and a full restore to return from. Hibernate is the tier the operator actually
wanted; they chose it over finishing destroy, and chose it in one pass over a staged
NAT-first rollout.

Design was validated against the live repo, not assumed. Findings that reshaped it
mid-brainstorm:

- WAF is already disabled for this site (`waf_web_acl_arns = {}`), so an earlier estimate
  crediting hibernate with ~$5–12/mo of WAF savings was wrong; the honest floor is ~$14/mo,
  not ~$5–9. Corrected with the operator before the design was approved.
- `terragrunt exclude` skips a unit *including its destroy*, so the obvious mechanism
  (`ecs_services.enabled = false`) would orphan running, billing services that also block
  the ALB delete. The correct mechanism is an empty `services` list with the unit still
  enabled.
- `try()` does not intercept `null`, so the CloudFront unit's
  `try(...alb_dns_name, "")` does not protect against a disabled ALB — a module change is
  unavoidable.
- `terragrunt-apply.yml`'s concurrency group has `cancel-in-progress: true`, so a
  two-phase apply must be strictly sequential or phase 2 cancels phase 1 mid-destroy.
- The `modules` input treats empty as apply-all, which would apply the `email` unit and
  steal the account's active SES receipt rule set — a known, unfixed defect.

Two operator decisions captured during brainstorming: retain the NAT EIP (~$3.60/mo) to
keep the VoIP.ms allowlist valid, and serve a static maintenance page from the retained
CloudFront distribution rather than leaving a dead mic button or dropping DNS.

## Tasks

### T-01: Write the design spec

- **files:** `docs/superpowers/specs/2026-09-10-hibernate-wake-design.md`
- **action:** Author the spec covering the problem, the survives/goes inventory, the
  repo findings that shaped it, the two-flag mechanism, the two-phase apply ordering and
  its concurrency trap, the three module changes, the EIP decision, the maintenance page
  and its build-guard trap, the command flow, cost outcome, testing, the rejected
  partial-wake tier, open items, and a decisions log.
- **verify:** File exists; both commands specified; every repo finding cited with a
  file:line reference that resolves; both operator decisions recorded in the decisions
  log; no TBD or placeholder text.

## Out of Scope

- Any code, module, or infra change — this task writes a document only.
- Building `kv destroy` (16-10/16-11 remain planned and unexecuted).
- Cancelling ElevenLabs, releasing DIDs, or running `kv pause` — operator actions.
