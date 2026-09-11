# klanker-voice: Hibernate and Wake

**Status:** Approved (brainstormed and validated with operator, 2026-09-10)
**Scope:** Two new `kv` commands — `hibernate` / `wake` — plus three small module changes
**Supersedes:** nothing
**Related:** `docs/superpowers/specs/2026-08-12-pause-backup-teardown-design.md` (pause/backup/destroy),
`docs/superpowers/specs/2026-07-04-klanker-voice-design.md` (authoritative design)

---

## 1. Problem

`kv pause` scales every ECS service to zero and takes the AWS bill from ~$190/mo to ~$60/mo.
It is the right tool for a weeks-scale gap between demos. It is the wrong tool for an
indefinite hold, because the ~$60 it leaves behind is almost entirely **infrastructure that
exists only to serve traffic that is not coming**: a NAT Gateway (~$32/mo) and an
Application Load Balancer (~$16/mo), both running at full price against zero tasks.

klanker-voice is going on hold. The operator's requirement is to tear down everything
except the VoIP.ms DIDs and the configuration, accepting a slower return, while keeping the
project rebuildable without a data restore.

That sits between the two states that already exist:

- **`kv pause`** — ~$60/mo, ~5 min round trip, everything intact.
- **`kv destroy`** — ~$1/mo, hours to return, DNS gone, certs gone, bucket names regenerate,
  NAT EIP gone, and a full `kv restore` from a backup zip required.

**Hibernate** is the third tier: destroy the compute *and* the expensive network plumbing,
keep every piece of durable state and identity, and come back in ~20–25 minutes with
nothing to restore.

### Goals

- `kv hibernate` / `kv wake` round-trip the stack in ~20–25 minutes each way, ~$60 → ~$14/mo.
- No data is backed up, moved, or restored — every durable store survives in place.
- The NAT EIP survives, so the VoIP.ms API allowlist stays valid.
- `voice.klankermaker.ai` keeps resolving and keeps unfurling, showing an honest
  maintenance page rather than a mic button that cannot work.
- No command can orphan a resource — anything that stops being managed must first be destroyed.

### Non-goals

- Changing the ElevenLabs subscription (manual, vendor console). **Resolved 2026-09-10
  (operator action):** downgraded from Pro ($99/mo) to Creator ($22/mo) to retain the
  cloned voice. It is no longer the dominant line item, but it still survives hibernate,
  pause and destroy alike, so `kv hibernate` names it on completion rather than letting it
  become an invisible recurring charge against a stack that is doing nothing.
- Releasing DIDs (manual, VoIP.ms-side, irreversible — unchanged from the pause/destroy spec §6.4).
- Replacing `kv pause` or `kv destroy`. All three tiers coexist.
- A partial "phones-only" wake — see §11.

---

## 2. What survives, what goes

| Resource | `kv pause` | `kv hibernate` | Why |
|---|---|---|---|
| ECS services, target groups, listener rules | desired_count=0 | **destroyed** | Must go before the ALB can be deleted (§4) |
| ECS task definitions | kept | **kept** | Free; keeping them registered makes wake a pure scale-up |
| NAT Gateway | running (~$32) | **destroyed** | Pure waste at zero tasks |
| NAT Elastic IP | attached | **retained, unattached (~$3.60)** | Preserves the VoIP.ms allowlist (§6, D-04) |
| ALB + listeners | running (~$16) | **destroyed** | Pure waste at zero tasks |
| ALB access-log bucket | kept | **kept** (via `alb.retain_logs`) | The live plan showed it destroyed with the ALB — 5 resources, unrecoverable. Retained deliberately; `logs_force_destroy` governs whether a non-empty bucket *may* be destroyed, not whether the destroy is attempted |
| CloudFront distribution | kept | **kept, ALB origin dropped** | Serves the maintenance page (§7) |
| Route53 sub-zone + records | kept | **kept** | The whole point of not destroying — URL keeps resolving |
| ACM certs | kept | **kept** | Avoids re-validation on wake |
| DynamoDB tables + rows | kept | **kept** | No backup/restore needed |
| S3 ledger (transcripts) | kept | **kept** | No backup/restore needed |
| S3 cf-assets bucket | kept | **kept** | Bucket name's `random_id` suffix never regenerates |
| ECR images | kept | **kept** | Wake needs no rebuild |
| VPC, subnets, IGW, SGs | kept | **kept** | Free; keeps wake simple |
| KMS CMKs (SOPS + 3 SSM) | kept | **kept** | Required to read secrets at wake |
| The DIDs | provisioned | **provisioned, fast busy** | VoIP.ms-side, untouched |

Nothing in the "hibernate" column requires a backup to restore. `kv backup` before
hibernating is still good hygiene and the runbook should recommend it, but unlike
`kv destroy` it is not a precondition.

### 2.1 Findings that shaped the design

Established by direct inspection on 2026-09-10.

- **WAF is already off for this site.** `global/cloudfront/terragrunt.hcl:147` sets
  `waf_web_acl_arns = {}` with the comment "WAF disabled for this site"; the ruleset in
  `global/waf/waf.hcl` is an unwired data stub. There is no WAF line item to cut — it is
  $0 today. Earlier estimates that credited hibernate with ~$5–12/mo of WAF savings were wrong.
- **`try()` does not catch `null`.** `global/cloudfront/terragrunt.hcl:131` reads
  `try(dependency.use1_network.outputs.alb_dns_name, "")`. The output is *declared* and
  returns `null` when the ALB is disabled (`modules/network/v1.0.0/outputs.tf:121-124`), and
  `try` only intercepts errors, not null values. So `null` flows into the origin block's
  `domain_name` and fails the apply. The CloudFront module change (§5.1) is genuinely required
  and cannot be avoided with config alone.
- **`exclude` skips; it does not destroy.** `ecs-service/terragrunt.hcl:13-16` carries
  `exclude { if = !local.site_vars.locals.ecs_services.enabled, actions = ["all"] }`. Setting
  `ecs_services.enabled = false` removes the unit from the run graph *including its destroy*,
  which would leave the services, target groups, and listener rules **running and unmanaged** —
  still billing, and still blocking the ALB delete. This is the single most dangerous wrong
  turn available in this design, and §3 exists to avoid it.
- **The module is driven entirely by `for_each` over `var.ecs_services`**
  (`modules/ecs-service/v1.0.0/main.tf:6,103,131,180,247`). An **empty services list** with the
  unit still enabled collapses every derived map to empty and destroys the services, target
  groups, and listener rules in-graph. That is the correct mechanism.
- **`ecs-service` owns the ALB wiring.** `aws_lb_target_group` (`main.tf:130`) and
  `aws_lb_listener_rule` (`main.tf:179`, binding `var.alb_listener_arn` at `main.tf:185`) live
  in the service unit, not the network unit. They must be gone before the ALB is deleted (§4).
- **NAT and ALB already have clean `enabled` toggles.** `natgw.tf` gates `aws_eip.nat`,
  `aws_nat_gateway.nat`, and `aws_route.private_nat_gateway` on `var.nat_gateway.enabled`;
  `alb.tf:1` gates `aws_lb.lb_public` on `var.alb.enabled`. Both outputs already return `null`
  when disabled. No module change is needed to turn them off — only to retain the EIP (§5.2).
- **`auth` is the only service in a private subnet** (`services/auth/service.hcl:230`,
  `assign_public_ip = false`). `voice` (`:207`) and `telephony-edge` (`:382`) both run in public
  subnets with public IPs. The NAT Gateway exists solely for auth's egress.
- **The apply workflow's concurrency group cancels in progress.**
  `.github/workflows/terragrunt-apply.yml` declares
  `concurrency: { group: "${{ github.workflow }}-${{ github.ref }}", cancel-in-progress: true }`.
  Two dispatches against `main` share a group. This makes the two-phase sequence in §4 a
  correctness requirement, not a stylistic one.
- **`modules` empty means apply-everything.** The same workflow defaults `modules` to `''`
  described as "empty = all". Applying the `email` unit steals the account's single active SES
  receipt rule set and silently kills klanker-maker's inbound mail — a known, unfixed defect
  (`docs/operators/ses-active-rule-set-fix.md`, 2026-09-03). See §8.1.
- **The SPA is published to S3 by CI, not by terraform.** `build-voice.yml:111-127` syncs
  `dist/` to the cf-assets bucket and uploads `index.html` separately, then invalidates
  (`:138`). S3 object content is therefore *not* covered by the "config is the guard" property
  that makes the pause flag safe. See §7.1.

---

## 3. Mechanism: a second flag, strictly additive

`paused` stays exactly as shipped (`site.hcl:164`). A new `hibernated` boolean sits beside
it, and the service list keys off **both**:

```hcl
# Operator pause switch (kv pause / kv resume — avoid editing by hand).
paused = false

# Operator hibernate switch (kv hibernate / kv wake — avoid editing by hand).
# true => everything `paused` does, PLUS: the ECS services, their target groups
# and listener rules are destroyed outright, and the NAT Gateway and ALB are
# torn down. The NAT *EIP* is retained (unattached) so the VoIP.ms allowlist
# stays valid. VPC, Route53, ACM, DynamoDB, the S3 ledger, the cf-assets
# bucket and ECR all stay put, so wake needs no restore.
hibernated = false

ecs_services = {
  # NOTE: stays true under hibernation. Setting this false would `exclude` the
  # unit from the run graph including its destroy, orphaning the services —
  # still running, still billing, still blocking the ALB delete.
  enabled = true

  # Under hibernation the list goes empty: the module's for_each maps collapse
  # and terraform destroys the services, target groups and listener rules.
  services = local.hibernated ? [] : [
    for s in [voice, auth, telephony_edge] :
    local.paused ? merge(s, {
      desired_count = 0
      autoscaling   = merge(s.autoscaling, { min_capacity = 0 })
    }) : s
  ]
}
```

`network.hcl` reads the same flag:

```hcl
nat_gateway = {
  enabled    = !local.site_vars.locals.hibernated
  retain_eip = true          # see §5.2 / D-04
}

alb = {
  enabled = !local.site_vars.locals.hibernated
  # ... remaining attributes unchanged
}
```

And the CloudFront unit passes `alb_origin_enabled = !local.site_vars.locals.hibernated`.

### 3.1 Why a second boolean rather than a state enum

A `lifecycle_state = "running" | "paused" | "hibernated"` enum is tidier on paper. It is
rejected because `kv pause` is already shipped, live-gated, documented, and covered by
passing tests — an enum rewrites that working code and every one of its fixtures for a
cosmetic gain.

Two booleans with the invariant **hibernated implies paused** keep every shipped code path
byte-identical and make the deeper state purely additive. The invariant is enforced in one
place (the HCL expression above, where an empty `services` list makes `paused` moot) and
asserted in `kv pause status`, which grows one row.

`kv hibernate` sets `hibernated = true` and leaves `paused` alone. `kv wake` clears
`hibernated` and leaves `paused` alone. An operator who hibernates from a paused state and
then wakes lands back in the paused state they started from, which is the least surprising
behaviour available.

---

## 4. The ordering constraint: two applies, strictly sequential

Terragrunt applies dependencies **first**: `network` before `ecs-service`. That is the
correct order for creation and exactly backwards for removal. A single apply would attempt
to delete the ALB while `aws_lb_listener_rule` resources still bind to its listener, and
fail partway — leaving the stack in a half-torn state with the services already gone.

So hibernate is two dispatches of `terragrunt-apply.yml`:

| | `modules` input | Effect |
|---|---|---|
| **Phase 1** | `ecs-service,cloudfront` | Services, target groups and listener rules destroyed. CloudFront's ALB origin and its `/api/*` and `/health` behaviors dropped. Nothing references the ALB any more. |
| **Phase 2** | `network` | ALB and NAT Gateway destroyed. EIP retained, unattached. |

Wake runs the same two units in **reverse** — `network`, then `ecs-service,cloudfront` —
which is terragrunt's natural dependency order, so wake carries no equivalent hazard.

### 4.1 The concurrency trap

`terragrunt-apply.yml` sets `cancel-in-progress: true` on a concurrency group keyed by
workflow and ref. Both phases dispatch against `main`, so they land in the **same group**:
dispatching phase 2 while phase 1 is still running **cancels phase 1 mid-apply**.

Therefore:

- `kv hibernate` MUST watch phase 1 to a terminal state before dispatching phase 2.
- It MUST abort the entire operation if phase 1 ends in any state other than success,
  and report which phase failed and what state the stack is in.
- It MUST NOT dispatch both phases optimistically under any flag, including `--yes`.

This is the same class of defect as the already-recorded `deploy-ecs-main` group cancelling
displaced pending deploys. The difference is that here the cancelled run is holding a
half-completed destroy.

### 4.2 Drain before phase 1

Voice tasks hold ECS task-scale-in protection while a session is live. Phase 1 destroys
services outright rather than scaling them, so `kv hibernate` reuses the shipped
`DrainToZero` path from `kv pause` **before** dispatching phase 1, and prints
`waiting for N in-flight session(s) to drain` rather than appearing hung.

---

## 5. Module changes (three, all small)

### 5.1 `modules/cloudfront/v1.0.0` — conditional ALB origin

Add `variable "alb_origin_enabled" { type = bool, default = true }`.

Convert three blocks in `main.tf` to `dynamic`, each gated on that variable:

- the ALB `origin` block (`main.tf:111-112`, whose `domain_name` reads `alb_dns_name`),
- the `/api/*` ordered cache behavior (`main.tf:141`),
- the `/health` ordered cache behavior (`main.tf:154`).

The S3 origin and the default behavior are untouched, so the distribution keeps serving the
bucket at the root. Defaulting to `true` means every existing call site keeps its current
behavior with no change.

This is the only change that touches the path every public URL flows through, so it carries
the most test weight (§10).

### 5.2 `modules/network/v1.0.0/natgw.tf` — retain the EIP

Split the EIP's lifetime from the gateway's:

```hcl
resource "aws_eip" "nat" {
  count = var.nat_gateway.enabled || var.nat_gateway.retain_eip ? 1 : 0
  # ... unchanged
}
```

`aws_nat_gateway.nat` and `aws_route.private_nat_gateway` stay gated on
`var.nat_gateway.enabled` alone. Add `retain_eip = optional(bool, false)` to the
`nat_gateway` variable's object type, and widen the `nat_eip_public_ip` output
(`outputs.tf:51-54`) to emit on either condition so `kv` can read the retained address back.

Default `false` preserves today's behavior everywhere; `network.hcl` opts in.

### 5.3 `site.hcl` / `network.hcl` — the flag plumbing

As shown in §3. No module change; configuration only.

---

## 6. The NAT EIP

Retained while hibernated at ~$3.60/mo (AWS bills unattached Elastic IPs hourly).

The alternative — releasing it and re-allowlisting on wake — was considered and declined.
The VoIP.ms API allowlist gates the SMS relay-through-auth path and the CTF OTP endpoint.
A forgotten allowlist entry does not fail loudly at wake; it fails later, silently, the
first time someone sends an SMS or dials the OTP DID. $43/year to remove an entire class of
silent post-wake failure is the right trade for a system whose whole purpose is to work
when a stranger calls it.

`kv wake` still reports the NAT EIP it reattached, so a mismatch is visible rather than assumed.

---

## 7. The maintenance page

A committed `apps/voice/client/public/maintenance.html`: short, styled to match the app, and
carrying the **same OG/Twitter tags as the real `index.html`** so shared links keep unfurling
correctly (the unfurl requires a self-hosted same-origin PNG — see the existing URL-unfurl work).

- **Hibernate**, after phase 1 succeeds: copy the live `index.html` to `index.spa.html` in the
  cf-assets bucket, upload `maintenance.html` as `index.html`, create an invalidation.
- **Wake**, after the final apply: copy `index.spa.html` back to `index.html`, invalidate,
  delete the saved copy.

The real SPA is never destroyed, only shadowed, and no build artifact is needed at either end.

### 7.1 The trap: config is *not* the guard here

The pause flag is safe because it lives in `site.hcl`, so a mid-pause deploy re-applies
`desired_count = 0` and the stack stays paused — correct by construction (pause spec §5.6).

**That property does not extend to S3 object content.** A `build-voice` run during hibernation
would sync the real `index.html` back over the maintenance page and silently restore a mic
button that cannot work, with no failure anywhere.

Fix: `build-voice.yml` reads `hibernated` from the checked-out `site.hcl` and **skips the
`index.html` upload and the invalidation** when it is true. The hashed-asset sync may proceed
harmlessly. One conditional, and the correct-by-construction property is restored.

---

## 8. Command flow

```
kv hibernate [--yes] [--reason "on hold"] [--dry-run]
kv wake      [--yes] [--dry-run]
kv pause status          # grows a hibernated row
```

`kv hibernate` reuses the shipped `kv pause` skeleton verbatim — preflight (clean tree, on
`main`, synced with origin, valid `gh` auth), idempotent read, diff-and-confirm, commit and
push, dispatch, watch to completion — and adds:

1. **Preflight**, as pause. Refuse rather than commit onto a surprise.
2. **Idempotent read** — already hibernated? print status, exit 0.
3. **Rewrite + confirm** — flip `hibernated`, show the diff, confirm (`--yes` skips).
4. **Commit + push to `main`** — `ops(infra): hibernate (destroy NAT, ALB, ECS services)`.
5. **Drain** to zero and wait (§4.2).
6. **Phase 1 dispatch** — `modules=ecs-service,cloudfront`; stream to terminal state; **abort
   on anything but success**.
7. **Phase 2 dispatch** — `modules=network`; stream to terminal state.
8. **Swap the maintenance page** (§7).
9. **Verify** — assert the ALB and NAT Gateway are gone, the EIP is still allocated, and no
   ECS service remains in the cluster. A verify that finds an orphaned service is a failure,
   not a warning.
10. **Report** — resulting cost posture, the retained EIP address, and the manual
    follow-ups (§8.3).

`kv wake` mirrors it in reverse order (network → ecs-service,cloudfront → restore page) and
does not report success until the voice and auth ALB target groups report **healthy** —
the same bar `kv resume` already sets. A clean apply that never reaches healthy is exactly
the failure worth catching.

### 8.1 Both phases carry an explicit module list

Non-negotiable, and the reason is §2.1's last bullet: the `modules` input treats empty as
*all*, and applying the `email` unit steals the account's single active SES receipt rule set,
silently killing klanker-maker's inbound mail.

`kv hibernate` and `kv wake` MUST refuse to dispatch with an empty module list rather than
ever fall back to apply-all. The `email` unit appears in neither phase of neither command.

### 8.2 `--dry-run`

Reports the planned phase order, the exact `modules` string for each dispatch, the S3 keys
that would be swapped, and the current state of everything the verify step will check.
Issues no mutating call: no dispatch, no S3 write, no invalidation.

### 8.3 Stays manual, and the completion output says so

- **The ElevenLabs subscription.** Downgraded to Creator ($22/mo) on 2026-09-10 to keep the
  cloned voice. It survives hibernate, pause and destroy alike — still roughly 1.5× the
  hibernated AWS bill — so the completion output names it rather than letting it run on
  unnoticed against an idle stack.
- **The DIDs stay provisioned and billing** at VoIP.ms. Unchanged and deliberate.
- **The kill-switch is untouched.** Orthogonal to hibernation, exactly as with pause
  (pause spec §5.7). Coupling them would produce a surprise at wake.

---

## 9. Cost outcome

| | Running | `kv pause` | `kv hibernate` | `kv destroy` |
|---|---|---|---|---|
| Fargate (3.5 vCPU / 7 GB) | ~$126 | $0 | $0 | $0 |
| NAT Gateway | ~$32 | ~$32 | $0 | $0 |
| ALB | ~$16 | ~$16 | $0 | $0 |
| Retained NAT EIP | — | — | ~$3.60 | $0 |
| WAF | $0 | $0 | $0 | $0 |
| Misc floor (Route53, 4× KMS CMK, ECR, S3, CloudWatch Logs) | ~$10 | ~$10 | ~$10 | ~$1 |
| **AWS total** | **~$190** | **~$60** | **~$14** | **~$1** |
| ElevenLabs Creator (manual, all states) | $22 | $22 | $22 | $22 |
| Round trip | — | ~5 min | ~20–25 min | hours + restore |
| Data restore needed to return? | no | no | **no** | **yes** |
| DIDs | live | provisioned, fast busy | provisioned, fast busy | released manually, gone |

These are estimates derived from resource inventory and public AWS pricing, **not** from
Cost Explorer — AWS credentials were expired at spec time (§12). The ~$10 misc floor is the
least certain figure and is the one that does not go away without a real `kv destroy`.

---

## 10. Testing

- **Flag rewrite** — table tests for `site.hcl` and `network.hcl`: idempotence, comment
  preservation, already-hibernated, malformed input, and the `paused` × `hibernated` matrix
  (notably: hibernating from paused, then waking, returns to paused).
- **Two-phase sequencing** — a fake-CI test asserting phase 2 is never dispatched until phase 1
  reaches terminal success, and that a failed, cancelled, or timed-out phase 1 aborts the
  operation without dispatching phase 2.
- **Empty module list refusal** — assert both commands refuse to dispatch with an empty
  `modules` string, and that `email` appears in no dispatch.
- **CloudFront module** — `terraform plan` fixtures with `alb_origin_enabled` true and false,
  asserting the S3 origin and default behavior are identical in both and that exactly the ALB
  origin and the two behaviors differ.
- **EIP retention** — plan fixture asserting `aws_eip.nat` survives with
  `enabled = false, retain_eip = true` while the gateway and route are destroyed.
- **Orphan guard** — a test asserting that the mechanism destroys rather than excludes: with
  `hibernated = true`, `ecs_services.enabled` must still be `true` and the services list empty.
- **S3 page swap** — round-trip against a fake S3, including the case where `index.spa.html`
  already exists from an interrupted prior run.
- **`--dry-run`** — coverage on both commands asserting zero mutating calls.
- **Preflight refusals** — dirty tree, wrong branch, stale origin, no `gh` auth.

---

## 11. Rejected: a partial "phones-only" wake

The operator raised the possibility that a return might only need "a phone number or two".
Inspection shows a phones-only tier would not pay for itself:

`telephony-edge` has no ALB attachment at all (`services/telephony-edge/service.hcl:384`), so
it looks like it could wake alone. But it relays SMS **through auth** and the CTF OTP is
served by auth, and `auth` sits in a private subnet (`:230`) — so waking phones requires auth,
which requires the NAT Gateway. It also requires the voice pipeline, since the agent *is* the
product. A phones-only wake would therefore skip only the ALB (~$16/mo) while carrying NAT and
all three tasks.

Not worth a separate tier or its test surface. The flags in §3 are shaped so one could be
expressed later — `hibernated` gates a list, not a hardcoded set — without restructuring.

---

## 12. Open items

- **§7's CloudFront invalidation is deliberately NOT implemented** (ruling R14 at
  implementation time; recorded here so the spec stops requiring something nothing does).
  `index.html` is written `no-cache, no-store, must-revalidate` by both `build-voice.yml`
  and `kv`'s own `PutObject`, and CloudFront honours an origin's `Cache-Control` — so it
  revalidates on the next request rather than serving the stale shell, and the swap is
  effectively immediate in both directions. An invalidation would buy nothing except a new
  `aws-sdk-go-v2/service/cloudfront` dependency, which the same implementation declined for
  `service/ec2` on exactly those grounds. Reconsider only if `index.html`'s cache headers
  ever change. (The invalidation `build-voice.yml` *does* issue is unrelated and stays — it
  is the CI publish path, and it is skipped while hibernated along with the `index.html`
  upload, per §7.1.)
- **§7 and §8 disagreed on when the page swap happens** (ruling R15); §7 ("after phase 1 succeeds") was
  adopted and §8's step list is superseded. The invariant is *the maintenance page is up
  whenever the stack is not serving*: hibernate swaps between the phases, because phase 1
  drops CloudFront's `/api/*` behaviour and a failed phase 2 would otherwise leave a
  live-looking mic button up; wake restores the SPA only after the target-group health gate.
- **The ~$10/mo misc floor is unverified.** AWS credentials were expired at spec time, so the
  breakdown (Route53 hosted zone, ~4 customer-managed KMS CMKs at $1/mo each from
  `infra/.envrc`, ECR storage, S3, CloudWatch Logs) is inferred from resource inventory rather
  than measured. Confirm with Cost Explorer before or shortly after the first hibernation; if
  it lands materially above ~$14/mo total, the CMKs and CloudWatch Logs retention are the first
  places to look.
- **CloudFront propagation time at wake is estimated.** The ~20–25 min figure assumes a
  distribution *config update* (adding an origin back), not a create. Measure on the first wake
  and correct the runbook.
- **A VoIP.ms failover recording for hibernated DIDs.** Callers currently get fast busy. A
  vendor-console change, operator's call — carried forward unchanged from the pause spec.
- **`auth.klankermaker.ai` has no maintenance page.** It is ALB-only, so during hibernation it
  fails to resolve to a healthy origin at all. Giving it the same treatment as `voice` would
  mean putting it behind CloudFront, which is a larger change than this spec should carry.

---

## 13. Decisions log

| ID | Decision | Choice | Rationale |
|---|---|---|---|
| D-01 | Tier depth | Destroy NAT + ALB + ECS services; keep all durable state | The ~$48/mo of the paused bill that serves no traffic, without giving up DNS, certs, data or bucket names |
| D-02 | Flag shape | Second boolean `hibernated`, additive to `paused` | An enum rewrites shipped, tested, live-gated pause code for cosmetics |
| D-03 | Service removal | Empty `services` list, unit stays `enabled` | `exclude` skips the destroy and would orphan running services (§2.1) |
| D-04 | NAT EIP | Retain, unattached, ~$3.60/mo | A stale VoIP.ms allowlist fails silently later, not loudly at wake (§6) |
| D-05 | Public face | Keep CloudFront; swap in a maintenance page | URL keeps resolving and unfurling; nobody taps a dead mic button |
| D-06 | Apply shape | Two strictly sequential dispatches | Terragrunt's dependency order is backwards for removal (§4) |
| D-07 | Phase 2 gating | Never dispatch until phase 1 is terminally successful | Shared concurrency group with `cancel-in-progress` would kill a half-done destroy (§4.1) |
| D-08 | Module lists | Always explicit; empty is refused | Empty means apply-all, and the `email` unit kills klanker-maker's inbound mail (§8.1) |
| D-09 | Build guard | `build-voice.yml` skips `index.html` while hibernated | S3 content is not config, so "config is the guard" does not hold (§7.1) |
| D-10 | Backup | Recommended, not required | Nothing durable is destroyed; unlike `kv destroy` this is not a precondition |
| D-11 | Kill-switch | Untouched | Orthogonal; coupling produces a surprise at wake (pause spec §5.7) |
| D-12 | Partial wake | Rejected | Phones need auth, auth needs NAT; saves only the ALB (§11) |
| D-13 | ElevenLabs | Manual, but named in completion output | Survives every state and is outside AWS entirely. Downgraded Pro → Creator ($99 → $22/mo) on 2026-09-10 to keep the cloned voice; still ~1.5× the hibernated AWS bill |
