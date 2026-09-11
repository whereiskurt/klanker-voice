# kv hibernate / kv wake runbook (2026-09-10 spec)

`kv hibernate` is the third infrastructure lifecycle tier, below `kv pause`. Where
`kv pause` scales every ECS service to zero and leaves ~$60/mo of NAT Gateway and ALB
running against no traffic, `kv hibernate` destroys that plumbing outright and lands at
**~$14/mo** — see [Cost outcome](#cost-outcome) for the full breakdown. Unlike `kv destroy`,
nothing durable is removed: every DynamoDB table, the S3 transcript ledger, the cf-assets
bucket, DNS, and ACM certs all survive in place, so `kv wake` needs **no backup and no
restore** to bring the stack back.

## The three tiers, side by side

| | `kv pause` | `kv hibernate` | `kv destroy` |
|---|---|---|---|
| AWS bill | ~$60/mo | ~$14/mo | ~$1/mo |
| Round trip | ~5 min each way | ~20–25 min each way | hours + manual restore |
| Data restore needed to return? | no | **no** | **yes** |
| Right for | a weeks-scale gap between demos | an indefinite hold | permanently done |

Pick `kv hibernate` over `kv pause` for anything longer than a few weeks — the extra ~$46/mo
`kv pause` leaves running (NAT Gateway + ALB) is pure waste against zero traffic. Pick
`kv pause` over `kv hibernate` when you expect to come back inside days and want the fastest
possible round trip. Pick `kv destroy` only when the project is actually done — see
[docs/ops/pause-resume.md](./pause-resume.md) and the pause/backup/teardown design spec for
that tier.

## ✅ The plan gate — RUN AND PASSED 2026-09-10, against the live stack

**Status: satisfied.** This gate was originally deferred (AWS credentials were expired at
implementation time), but it was then run via CI's `terragrunt-plan` workflow, which uses the
GitHub OIDC role rather than an operator's local session. Evidence, on PR #97:

| Check | Unit | Result |
|---|---|---|
| `hibernated = false` is a no-op | `network`, `cloudfront`, `ecs-service` | **No changes** on all three |
| `hibernated = true` destroys the right things | `network` | 0 add, 0 change, **9 destroy** — ALB, listener, NAT Gateway, private route, ALB log bucket |
| **The NAT EIP survives** | `network` | `aws_eip.nat[0]` (`eipalloc-0a098e4128849c46f`) refreshed, **absent from the destroy list** |
| Services are destroyed, not orphaned | `ecs-service` | 0 add, 0 change, **9 destroy** — all three services, both listener rules, both target groups, autoscaling. The unit **planned**; it was not `exclude`d |
| CloudFront is not recreated | `global/cloudfront` | `will be **updated in-place**` — 0 add, 1 change, 0 destroy |

It also earned its keep immediately: the first run **failed**, catching an
`Inconsistent conditional result types` error in `site.hcl` that broke evaluation of *every*
terragrunt unit, at both flag values. `terragrunt hcl format --check` had passed it — formatting
is not evaluation. Fixed in `2402962`.

**Re-run this gate after any change to the `hibernated` plumbing or the network/cloudfront
modules.** The cheap way is to push the change to a PR touching `infra/**`: the
`terragrunt-plan` workflow triggers automatically. For the `hibernated = true` half, push a
throwaway branch with the flag flipped and `gh workflow run terragrunt-plan.yml --ref <branch>`
— a plan mutates nothing — then delete the branch.

The original manual procedure, for when you want to run it locally instead:

From `infra/terraform/live/site`, after `aws sso login --profile klanker-terraform` and
sourcing `infra/.envrc` (see [Prerequisite](#prerequisite-source-infraenvrc-before-running-either-command)
below):

1. With `hibernated = false` (the current, live value): `terragrunt plan` against
   `region/us-east-1/network` **must report `No changes`**. Anything else means the
   `alb_origin_enabled` / `retain_eip` plumbing altered infrastructure that is currently
   running — stop and investigate before touching `hibernated` at all.
2. Flip `hibernated = true` locally (do not commit) and re-plan each of the three units:
   - `network` must plan to destroy the ALB, the NAT Gateway, and the private route — but
     **must not** plan to destroy `aws_eip.nat[0]`. If the EIP shows as destroyed, the
     `retain_eip` wiring (design spec §5.2) is broken and hibernate will break the VoIP.ms
     allowlist on every cycle.
   - `ecs-service` must plan to destroy all three services (voice, auth, telephony-edge)
     along with their target groups and listener rules, and the plan output must **not**
     report the unit as excluded from the run graph. An excluded unit is the single most
     dangerous failure mode in this design (design spec §2.1) — it would leave the services
     running and unmanaged, still billing, and blocking the ALB delete.
   - `cloudfront` must plan to **update in place** (dropping the ALB origin and its two
     ordered behaviors) — never to destroy and recreate the distribution. A recreate means a
     new distribution ID and every cached DNS/CDN association breaks.
3. **Restore the flag to `false`** in your local working tree afterwards and confirm with
   `git diff --exit-code` before doing anything else. This step is local-only plan checking;
   nothing here should ever be committed or applied outside the real `kv hibernate` flow.

Only once all three plans read as expected is the command ready for its first live run.

## The three commands, as built

```
kv hibernate [--yes] [--reason "on hold"] [--dry-run]
kv wake      [--yes] [--dry-run]
kv pause status          # grows a "hibernated flag" row
```

- **`kv hibernate`** — flips the git-tracked `hibernated` boolean in
  `infra/terraform/live/site/site.hcl` to `true`, commits and pushes to `main`, drains any
  in-flight voice sessions to zero, dispatches two strictly sequential
  `terragrunt-apply.yml` runs — **swapping in the maintenance page between them, as soon as
  phase 1 succeeds** — then verifies the ALB and NAT Gateway are gone and the EIP is still
  allocated, and reports the resulting cost posture. `--yes` skips the diff-and-confirm
  prompt. `--reason` records an operator note as a trailing line in the commit body, same as
  `kv pause`.
- **`kv wake`** — mirrors `kv hibernate` in reverse: flips the flag back to `false`, runs the
  same two apply phases in reverse order, waits for every service to come up and for the
  voice and auth ALB target groups to report **healthy** — the same bar `kv resume` already
  sets — and only **then** restores the real SPA shell over the maintenance page. It also
  reports what happened to the NAT EIP (see
  [The NAT EIP outcome](#the-nat-eip-outcome-why-kv-wake-reports-it)).

  The page swap sits where it does, at both ends, to hold one invariant: **the maintenance
  page is up whenever the stack is not serving.** Phase 1 of a hibernate drops CloudFront's
  `/api/*` and `/health` behaviours, so from that moment the real SPA would be a mic button
  against a stack that cannot answer — and if phase 2 then failed, it would stay that way.
  At the other end, restoring the SPA before the health gate would put a working-looking
  page in front of services that may never come up.

  Neither direction issues a CloudFront invalidation, and neither needs one: `index.html` is
  written `no-cache, no-store, must-revalidate`, and CloudFront honours an origin's
  `Cache-Control`, so it revalidates on the next request rather than serving the stale
  shell.
- **`kv pause status`** — unchanged command, one new row: it now also prints the
  `hibernated` flag next to `paused`, and a one-line note when hibernated is true
  (`hibernated implies paused: the service list is empty, and the NAT Gateway and ALB are
  destroyed`). Still read-only — never commits, pushes, or dispatches anything.

## Prerequisite: source `infra/.envrc` before running either command

Same requirement as `kv pause`/`kv resume` (see
[docs/ops/pause-resume.md](./pause-resume.md#prerequisite-source-infraenvrc-before-running-any-of-these-commands)):
`kv hibernate`/`kv wake` resolve the live ECS cluster, service names, target-group ARNs, the
NAT EIP, and the cf-assets S3 bucket name via `terragrunt output` and SSM, which needs the
backend env vars from `infra/.envrc` sourced into your shell first:

```bash
set -a && . infra/.envrc && set +a
```

Without it, `terragrunt output` fails with an opaque backend error that does not obviously
point at the missing env.

## Preflight — what each refusal means and how to clear it

`kv hibernate`/`kv wake` reuse `kv pause`'s preflight verbatim (D-18: refuse rather than
commit onto a surprise). Every check runs in this fixed order; the command stops at the
first failure and the working tree is never touched by the flag rewrite until every check
passes:

| Refusal | What it means | How to clear it |
|---|---|---|
| working tree is not clean | You have pending changes (tracked or untracked) in the repo | `git status`, then commit or stash them |
| not on the required branch | You're not on `main` | `git checkout main` |
| local branch is out of sync with origin | Your local `main` doesn't match `origin/main` (behind, ahead, or diverged) | `git fetch origin && git status` to see which, then reconcile |
| `gh` is not authenticated | The GitHub CLI has no valid session | `gh auth login` |

An already-hibernated `kv hibernate` (or already-awake `kv wake`) is **not** a refusal — and
it is **not a no-op either**. The flag is committed *before* the applies run, so finding it
already set proves only that some earlier invocation got as far as the commit, not that the
applies, the page swap or the verification ever happened. The command says so —
`flag already hibernated=true (committed by an earlier run) -- re-running the remaining
steps` — skips only the flag rewrite, confirm, commit and push, and then re-runs everything
after them. That is what makes [Recovery from a failed phase 1](#recovery-from-a-failed-phase-1)
below actually recover something. The repeated steps are all safe to repeat: terragrunt
applies are idempotent, the page swap refuses to re-copy over an existing `index.spa.html`,
and the verification is read-only.

## The diff-and-confirm step

Without `--yes`, both commands show the one-line diff to `site.hcl` and wait for an
affirmative answer (`y`/`yes`) before committing. Anything else — an explicit "no", garbage
input, or a non-interactive stdin with nothing to read — is treated as a refusal, never as an
implied yes, and the flag file is restored to its original bytes before the command exits.
`--yes` is the only sanctioned way to run either command unattended.

## The two-phase apply — the single most surprising thing about this command

`kv hibernate` does not dispatch one apply. It dispatches **two**, back to back, and it
**waits for the first to finish before it even considers dispatching the second**:

| | `modules` input | Effect |
|---|---|---|
| **Phase 1** | `ecs-service,cloudfront` | The three ECS services, their target groups and listener rules are destroyed. CloudFront's ALB origin and its `/api/*` and `/health` behaviors are dropped. Nothing references the ALB any more. |
| **Phase 2** | `network` | The ALB and NAT Gateway are destroyed. The NAT EIP is retained, unattached. |

`kv wake` runs the same two units in **reverse** — `network` first, then
`ecs-service,cloudfront` — which happens to be terragrunt's natural dependency order, so
wake carries no equivalent hazard and the wait between its two phases is comparatively
uneventful.

**This means a full `kv hibernate` needs TWO separate required-reviewer approvals in the
GitHub Actions UI**, not one. The command dispatches phase 1, streams its status to
terminal success, *then* dispatches phase 2 and streams that too. Between the two, the
command sits visibly waiting — this is not a hang. If you only approve the first run and
walk away, `kv hibernate` will sit there indefinitely waiting for the second approval to
appear, same as it would sit waiting for the first. Be ready to approve both before you
start.

**Why two phases at all, and why not dispatch both up front:** terragrunt applies
dependencies first — `network` before `ecs-service` — which is the correct order for
*creation* and exactly backwards for *removal*. A single apply would try to delete the ALB
while `aws_lb_listener_rule` resources still bind to its listener, and fail partway,
leaving the stack in a half-torn state with the services already gone. Splitting into two
sequential applies fixes the ordering — but `terragrunt-apply.yml`'s `concurrency: {
group: "${{ github.workflow }}-${{ github.ref }}", cancel-in-progress: true }` means both
phases land in the **same concurrency group**, because both dispatch against `main`.
Dispatching phase 2 before phase 1 has reached a terminal state would **cancel phase 1
mid-destroy**, not queue behind it. That is why `kv hibernate` watches phase 1 all the way
to success before it dispatches phase 2, under every flag including `--yes` — there is no
"fast path" that skips the wait.

## Recovery from a failed phase 1

If phase 1 fails, is cancelled, or times out, `kv hibernate` aborts the whole operation and
**never dispatches phase 2**. Nothing later ran. To see what actually applied:

```bash
kv pause status
```

This shows the live `hibernated`/`paused` flags next to each service's real desired/running
counts, so you can tell whether phase 1 partially landed (some services gone, some not) or
never started. While hibernated there are no services at all, and it says so
(`no ECS services defined (hibernated)`) rather than printing an empty table.

Re-running `kv hibernate` is safe, and it is the recovery: the `hibernated` flag is already
committed to `true` from the first attempt, so the command skips the rewrite/commit/push and
— once you resolve whatever caused phase 1 to fail — re-attempts the drain, both phases, the
page swap and the verification. There is no special "resume" mode and none is needed; the
command's own idempotence covers this case.

The same applies to a failed **phase 2**: the services are already gone and the maintenance
page is already up (it goes up between the phases), but the ALB and NAT Gateway are still
running and still billing ~$48/mo. Re-running `kv hibernate` re-dispatches both phases —
phase 1 is a no-op apply against a stack that already matches — and does not report success
until the verification confirms the ALB and NAT Gateway are actually gone and the EIP is
still allocated.

> **What a re-run does NOT re-check: orphaned ECS services.** The verification looks for
> orphans by name, and it takes those names from the `ecs-service` unit's terraform outputs,
> which are resolved *before* the applies. On a first run the stack is still up, so it has all
> three names and the check is real. On a **re-run**, phase 1 has already emptied that unit, so
> the name list comes back empty and the orphan check passes trivially — it has nothing to look
> for. The ALB, NAT Gateway and EIP checks are unaffected and stay real in both cases.
>
> In practice this only bites if phase 1 failed *partway*, leaving some services behind. Confirm
> that yourself with `kv pause status`, which reads the live cluster rather than terraform's
> view, before trusting a re-run's clean verification. A fix that resolves the cluster's services
> directly is a tracked follow-up.

## What breaks while hibernated

- **`voice.klankermaker.ai`** serves a committed maintenance page in place of the real SPA
  and still unfurls correctly (it carries the same OG/Twitter tags as `index.html`) — a
  shared link still shows a proper preview card, it just leads to a page saying the demo is
  on hold rather than a mic button that cannot work.
- **`auth.klankermaker.ai`** is ALB-only. With the ALB destroyed, it **does not resolve to a
  healthy origin at all** — there is no maintenance page for it, unlike voice (design spec
  §12: giving auth the same treatment would mean putting it behind CloudFront, a larger
  change than this feature carries).
- **The DIDs stay provisioned and billed** by VoIP.ms, exactly as under `kv pause`.
  telephony-edge's outbound SIP registration drops while its ECS service is destroyed, so
  the sub-account reads as offline and callers get a **fast busy**.

## A mid-hibernation deploy will not clobber the maintenance page

`build-voice.yml` reads the `hibernated` flag from the checked-out `site.hcl` and **skips
the `index.html` upload and the CloudFront invalidation** when it is true — the hashed-asset
sync may still proceed harmlessly, but the maintenance page stays in place. This matters
because, unlike the `paused` flag, S3 object content is not itself config: without this
guard, a CI build landing mid-hibernation would silently sync the real `index.html` back
over the maintenance page, restoring a mic button that cannot work with no failure anywhere
to notice. See design spec §7.1 for why "config is the guard" (the property that makes a
mid-pause deploy safe, see [pause-resume.md](./pause-resume.md#a-mid-pause-deploy-is-safe--the-config-is-the-guard))
does not extend to S3 content on its own.

## Cost outcome

| | Running | `kv pause` | `kv hibernate` | `kv destroy` |
|---|---|---|---|---|
| Fargate (3.5 vCPU / 7 GB) | ~$126 | $0 | $0 | $0 |
| NAT Gateway | ~$32 | ~$32 | $0 | $0 |
| ALB | ~$16 | ~$16 | $0 | $0 |
| Retained NAT EIP | — | — | ~$3.60 | $0 |
| Misc floor (Route53, ~4× KMS CMK, ECR, S3, CloudWatch Logs) | ~$10 | ~$10 | ~$10 | ~$1 |
| **AWS total** | **~$190/mo** | **~$60/mo** | **~$14/mo** | **~$1/mo** |
| ElevenLabs Creator (manual, all states) | $22/mo | $22/mo | $22/mo | $22/mo |
| Round trip | — | ~5 min each way | ~20–25 min each way | hours + manual restore |
| Data restore needed to return? | no | no | **no** | **yes** |

**The ~$10/mo misc floor is estimated from resource inventory (Route53 hosted zone, ~4
customer-managed KMS CMKs at $1/mo each, ECR storage, S3, CloudWatch Logs retention), not
measured** — AWS credentials were expired at spec time. Confirm it against Cost Explorer
after the first real hibernation; if the total lands materially above ~$14/mo, the CMKs and
CloudWatch Logs retention are the first places to look.

**The ElevenLabs subscription (Creator, $22/mo since 2026-09-10) is not affected by any of
these three commands.** It was deliberately downgraded from Pro ($99/mo) to Creator to keep
the cloned voice — it is not something to cancel — but at ~1.5× the hibernated AWS bill it
becomes the dominant line item during a long hibernation, and `kv hibernate`'s completion
report names it on every run precisely so it doesn't become an invisible recurring charge
against a stack that's doing nothing.

## The NAT EIP outcome — why `kv wake` reports it

The NAT Gateway is destroyed on hibernate, but its Elastic IP is deliberately **retained,
unattached**, at ~$3.60/mo. This is not an oversight — it is the one piece of "waste" this
design keeps on purpose. The retained address is what the VoIP.ms API allowlist is keyed
to, and that allowlist gates the SMS relay-through-auth path and the CTF OTP endpoint. If
the EIP were released and a fresh one allocated on wake, a forgotten allowlist update would
not fail loudly at wake — it would fail **later, silently**, the first time someone sends an
SMS or dials the OTP DID.

Because of this, `kv wake` always names exactly one of four EIP outcomes in its completion
report:

- **unchanged** — the retained address survived and matches what was allocated going into
  wake. The VoIP.ms allowlist is still valid; no action needed.
- **changed** — the address after wake differs from the one that was retained.
- **newly allocated** — no EIP was retained going into this wake, so a new one was
  allocated.
- **lost** — no EIP is allocated at all after wake.

**A changed or newly allocated address does not make `kv wake` fail** — the command still
reports success, because the ALB, NAT Gateway, and ECS services all came back correctly.
But it fails *silently* on the very next SMS relay or CTF OTP call unless you act on it. If
`kv wake`'s report names a changed or new EIP, **update the VoIP.ms allowlist immediately**,
before assuming the phone side is fully back.

## `kv wake` needs no backup and no restore

This is the property that distinguishes `kv hibernate`/`kv wake` from `kv destroy`. Nothing
in the hibernated state requires a snapshot to return: every DynamoDB table (access codes,
tiers, usage, auth), the full S3 transcript ledger, the cf-assets bucket (its `random_id`
suffix never regenerates), ECR images, VPC/subnets/security groups, KMS CMKs, DNS records,
and ACM certs all survive hibernation untouched. `kv wake` is a pure infrastructure
rebuild against state that was never disturbed.

A `kv backup` before hibernating is still good hygiene and worth doing — see
[docs/ops/backup-restore.md](./backup-restore.md) — but it is **not a precondition** the way
it is before `kv destroy`. Skipping it does not put anything at risk that `kv hibernate`
itself touches.

## Kill-switch is a separate mechanism and is deliberately untouched

Same rule as `kv pause`/`kv resume` (see
[pause-resume.md](./pause-resume.md#kill-switch-is-a-separate-mechanism-and-is-deliberately-untouched)):
`kv hibernate`/`kv wake` never read, write, or reference the kill-switch (`kv killswitch`).
They operate at different layers — the kill-switch gates new voice sessions at the
application layer (an instant DynamoDB flip, no AWS mutation), while hibernate/wake remove
or rebuild compute and network infrastructure entirely. Every completion report states
explicitly that the kill-switch was not touched.

## Releasing a DID stays manual, and irreversible

Nothing about `kv hibernate` touches VoIP.ms DID ownership. The DIDs stay provisioned and
billed throughout every lifecycle state — running, paused, hibernated, even destroyed until
someone explicitly cancels them. Releasing a number is a manual, irreversible,
VoIP.ms-side action (`kv voipms cancel-did <did> --yes`) that stays outside this tool by
design — see the pause/backup/teardown design spec §6.4.

## Troubleshooting

### The command appears to hang between the two phases

This is very likely not a hang — see [The two-phase apply](#the-two-phase-apply--the-single-most-surprising-thing-about-this-command)
above. Check the GitHub Actions UI for a `terragrunt-apply` run sitting on the
`terraform-apply` environment's required-reviewer gate. `kv` prints the wait as an ordinary
status line, not an error or a timeout — the same behaviour `kv pause` already has. Approve
the run and the command continues.

### Phase 1 failed partway through

See [Recovery from a failed phase 1](#recovery-from-a-failed-phase-1) above: check
`kv pause status`, resolve whatever caused the failure (check the ECS console and the run's
logs in the GitHub Actions UI), and re-run `kv hibernate` — it's safe, because the flag is
already committed and the command is idempotent.

### `kv wake` never reports the target groups as healthy

Same failure mode `kv resume` already has (see
[pause-resume.md's equivalent section](./pause-resume.md#a-resume-whose-target-groups-never-go-healthy)):
check the ECS console for the voice/auth services — are tasks actually running? If
`desired > 0` but `running = 0`, the task is failing to start (bad image tag, missing
secret, crash loop). If tasks are running but the target group still isn't healthy, check
the ALB target group's health-check settings and the task's own health-check endpoint. Once
fixed, re-run `kv wake`; it's safe to re-run.

### The SMS relay or CTF OTP endpoint stopped working after a wake

Check the NAT EIP outcome in the most recent `kv wake` report (see
[The NAT EIP outcome](#the-nat-eip-outcome-why-kv-wake-reports-it) above). If it reported a
changed or newly allocated address, the VoIP.ms API allowlist is stale — update it in the
VoIP.ms portal to the address `kv wake` printed.

## See also

- [docs/ops/pause-resume.md](./pause-resume.md) — the sibling runbook for `kv pause`/
  `kv resume`, the lighter-weight tier this command sits below.
- [docs/ops/backup-restore.md](./backup-restore.md) — `kv backup`/`kv restore`, recommended
  but not required before a hibernation.
- [docs/superpowers/specs/2026-09-10-hibernate-wake-design.md](../superpowers/specs/2026-09-10-hibernate-wake-design.md) —
  the approved design spec this runbook is drawn from, including the full decisions log
  (§13) and the findings that shaped the design (§2.1).
- [docs/superpowers/specs/2026-08-12-pause-backup-teardown-design.md](../superpowers/specs/2026-08-12-pause-backup-teardown-design.md) —
  the design spec for `kv pause`/`kv backup`/`kv destroy`.
