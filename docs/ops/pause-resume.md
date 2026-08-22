# kv pause / kv resume runbook (Phase 16, §5)

`kv pause` scales every ECS service (voice, auth, telephony-edge) to zero tasks;
`kv resume` scales them back up. The round trip is ~5 minutes each way and takes the
AWS bill from **~$190/mo running** to **~$60/mo paused** — see [Cost outcome](#cost-outcome)
below for the full breakdown and the one caveat that matters most.

Both commands are built for the operator to run unattended: they commit, push, dispatch
CI, and wait — but they refuse rather than proceed at any point where a surprise would be
possible (D-18), and neither ever touches the kill-switch (see
[Kill-switch is a separate mechanism](#kill-switch-is-a-separate-mechanism-and-is-deliberately-untouched)).

## The three commands, as built

```
kv pause  [--yes] [--reason "off-season"]
kv resume [--yes]
kv pause status
```

- **`kv pause`** — flips the git-tracked `paused` boolean in
  `infra/terraform/live/site/site.hcl` to `true`, commits and pushes to `main`, dispatches
  `terragrunt-apply.yml`, watches it to completion, and verifies every service reaches
  `desired=running=0` — correcting the Application Auto Scaling ordering hazard itself if it
  fires (see [The Application Auto Scaling ordering hazard](#the-application-auto-scaling-ordering-hazard)).
  `--yes` skips the diff-and-confirm prompt. `--reason` records an operator note as a
  trailing line in the commit body (e.g. `--reason "off-season, back in November"`).
- **`kv resume`** — mirrors `kv pause`, flipping the boolean back to `false`, and
  additionally waits for the voice and auth ALB target groups to report **healthy** before
  printing success. `--yes` skips the same prompt.
- **`kv pause status`** — a read-only report: the current value of the `paused` flag next
  to each service's live desired/running counts from `ecs describe-services`. Never commits,
  pushes, or dispatches anything. This is the command to run when you're not sure whether a
  prior pause or resume actually finished (see
  [A pause left half-applied](#a-pause-or-resume-left-half-applied)).

## Prerequisite: source `infra/.envrc` before running any of these commands

Same requirement as `kv backup`/`kv restore` (see
[docs/ops/backup-restore.md](./backup-restore.md#prerequisite-source-infraenvrc-before-running-either-command)):
`kv pause`/`kv resume`/`kv pause status` all resolve the live ECS cluster, service names,
and target-group ARNs via `terragrunt output`, which needs the backend env vars from
`infra/.envrc` sourced into your shell first:

```
set -a && . infra/.envrc && set +a
```

Without it, `terragrunt output` fails with an opaque backend error that does not obviously
point at the missing env.

## Preflight — what each refusal means and how to clear it

`kv pause`/`kv resume` refuse before committing onto a surprise (D-18). Every check runs in
this fixed order; the command stops at the first failure and the working tree is never
touched by the flag rewrite until every check passes:

| Refusal | What it means | How to clear it |
|---|---|---|
| working tree is not clean | You have pending changes (tracked or untracked) in the repo | `git status`, then commit or stash them |
| not on the required branch | You're not on `main` | `git checkout main` |
| local branch is out of sync with origin | Your local `main` doesn't match `origin/main` (behind, ahead, or diverged) | `git fetch origin && git status` to see which, then reconcile |
| `gh` is not authenticated | The GitHub CLI has no valid session | `gh auth login` |

An already-paused `kv pause` (or already-running `kv resume`) is **not** a refusal — it's an
idempotent no-op: the command prints the current state and exits 0 without touching git, CI,
or ECS at all.

## The diff-and-confirm step

Without `--yes`, both commands show the one-line diff to `site.hcl` and wait for an
affirmative answer (`y`/`yes`) before committing. Anything else — an explicit "no", garbage
input, or a non-interactive stdin with nothing to read — is treated as a refusal, never as an
implied yes, and the flag file is restored to its original bytes before the command exits.
This is deliberate: `--yes` is the only sanctioned way to run either command unattended
(e.g. from a script or a scheduled job).

## The apply is gated by the `terraform-apply` environment's required-reviewer rule

After dispatch, `kv` streams the run's status. GitHub Actions reports a run status of
`waiting` while it sits on the `terraform-apply` environment's required-reviewer gate — `kv`
prints this as an ordinary waiting state (`run <id> is awaiting the terraform-apply reviewer
approval`), never as an error or a timeout. The command will sit there, visibly waiting,
until someone with reviewer access approves the run in the GitHub Actions UI. No workflow
file is modified by this project (D-23) — the existing gate is dispatched and watched exactly
as it already exists.

## The Application Auto Scaling ordering hazard

`aws_appautoscaling_target` depends on the ECS service resource, so a pause apply sets
`desired_count → 0` first and lowers `min_capacity → 0` second. In that few-second window,
Application Auto Scaling can scale the service back out to `MinCapacity` — leaving a service
pinned at 1 task while terraform believes it's at 0. This usually won't fire (Application
Auto Scaling evaluates on roughly a minute's cadence), but "usually" is not an acceptable
property for an operator tool.

`kv pause` closes this deterministically: it polls `ecs describe-services` for every service
until running and desired both read 0, and the moment it observes a service bounce back above
zero after previously reaching zero, it issues the correction itself
(`update-service --desired-count 0`) — without waiting out the full ~10-minute timeout.
**When the completion report says a correction fired**, it means exactly this happened: the
hazard occurred, `kv` caught it and fixed it, and terraform's own state already recorded 0 —
so the correction introduced no drift. It's worth knowing about, not worth panicking about.

## A pause issued during a live call

Voice tasks hold ECS task-scale-in protection while a session is live. If `kv pause` is run
while a call is in progress, the drain step waits for that protection to clear rather than
force-killing the call — you'll see `waiting for N in-flight session(s) to drain` printed
(and updated as the count changes) instead of the command appearing hung. There is no
override; this is intentional. If you need to pause immediately regardless of live calls,
engage the kill-switch first (`kv killswitch on`) to stop new sessions, wait for the existing
ones to finish naturally, then run `kv pause`.

## Cost outcome

| | Running | Paused | Destroyed |
|---|---|---|---|
| Fargate (3.5 vCPU / 7 GB) | ~$126 | $0 | $0 |
| NAT + ALB + WAF + misc | ~$60 | ~$60 | $0 |
| **AWS total** | **~$190/mo** | **~$60/mo** | ~$1/mo |
| ElevenLabs Pro (manual) | $99/mo | $99/mo | $99/mo |
| Round trip | — | ~5 min each way | hours + manual steps |
| DIDs | live | provisioned, fast busy | released manually, gone |

**ElevenLabs Pro ($99/mo) is not affected by `kv pause` at all** — it becomes the largest
remaining line item during a long pause. Pausing or downgrading it is a manual step in the
ElevenLabs console; `kv` cannot do it and says so in every completion report.

## Behaviour while paused

- `voice.klankermaker.ai` **still loads** — it serves from CloudFront/S3, so the page and its
  OG unfurl work normally. Only the mic tap fails (`/api/offer` → ALB → empty target group →
  503).
- `auth.klankermaker.ai` is ALB-only and returns 503.
- The DIDs stay **provisioned and billed** by VoIP.ms, but telephony-edge's outbound SIP
  registration drops while paused, so the sub-account reads as offline and callers get a
  **fast busy**.

There is no maintenance page for either host while paused — that's out of scope for this
mechanism; see the design spec if you want to add one later.

## Kill-switch is a separate mechanism and is deliberately untouched

`kv pause`/`kv resume` never read, write, or reference the kill-switch (`kv killswitch`).
They operate at different layers: the kill-switch gates new voice sessions at the
application layer (an instant DynamoDB flip, no AWS mutation), while pause/resume remove or
restore compute entirely. Coupling them would produce a surprise on resume — an operator who
paused *and* engaged the kill-switch would reasonably expect resume to leave the kill-switch
alone, not silently re-enable sessions. Every completion report states explicitly that the
kill-switch was not touched. If you want an instant, zero-AWS-risk way to stop metered API
burn, that's what `kv killswitch on` is for — see the killswitch section of the operator
manual, not this command.

## A mid-pause deploy is safe — the config is the guard

No CI workflow changes were made for this feature (D-23). Because the pause lives in
`site.hcl` (a config file, not a runtime flag), a deploy that lands while paused re-applies
`desired_count = 0` and the stack stays paused — correct by construction. A build still runs
and pushes a fresh image to ECR; it simply deploys to a service that isn't running any tasks.
No special handling is needed on your end.

## Troubleshooting

### A service will not drain

`kv pause` will not report success until every service's running and desired counts both
read 0. If one service is stuck above zero:

1. Check `kv pause status` — if the flag is already `true` but a service still shows
   `running > 0`, the apply likely completed but that service hasn't drained yet (or is
   genuinely wedged).
2. If it's `voice`, check for an in-flight session first — a live call holds task-scale-in
   protection and `kv pause` will (correctly) wait for it, printing `waiting for N in-flight
   session(s) to drain`. This is expected, not stuck.
3. If there is no live call and the service is still stuck after the ~10-minute verification
   window, `kv pause` reports a named error listing the service and its counts. Check the ECS
   console for that service's events (a stuck deployment, an unhealthy task that won't stop,
   etc.) and resolve it there before re-running `kv pause`.

### A resume whose target groups never go healthy

`kv resume` waits for the voice and auth ALB target groups to have at least one registered
target and for every registered target to report `healthy`. An empty target group is
explicitly **not** treated as healthy — that's precisely the paused-stack shape resume exists
to clear. If the wait times out:

1. Check the ECS console for the voice/auth services — are tasks actually running? If
   `desired > 0` but `running = 0`, the task is failing to start (check its logs — a bad
   image tag, a missing secret, a crash loop).
2. If tasks are running but the target group still isn't healthy, check the ALB target
   group's health-check settings and the task's health-check endpoint directly — a running
   task that fails its own health check will never register as healthy.
3. Re-run `kv resume` once the underlying issue is fixed; it's safe to re-run — the flag is
   already `false`, so it will report the current live posture as a no-op and you can use
   `kv pause status` to inspect the same desired/running counts while you debug.

### `site.hcl` has been hand-edited into an ambiguous state

The `paused` flag is read by a comment- and string-literal-aware line scanner, not a full HCL
parser (by design — see the design spec §5.1/D-31). If someone hand-edits `site.hcl` and
introduces a second top-level `paused = true|false` assignment, or removes the assignment
entirely, `kv pause`/`kv resume`/`kv pause status` will refuse with a named error
(`paused flag ambiguous: multiple assignments found` or `paused flag not found`) rather than
guess which one is authoritative. Fix the file by hand so exactly one top-level `paused = ...`
assignment exists (restoring the comment block above `ecs_services` in
`infra/terraform/live/site/site.hcl` is the simplest way to get back to a known-good shape),
commit it normally, and re-run the command.

## See also

- [docs/ops/backup-restore.md](./backup-restore.md) — the sibling runbook for `kv backup`/
  `kv restore`, including the same `infra/.envrc` prerequisite.
- [docs/superpowers/specs/2026-08-12-pause-backup-teardown-design.md](../superpowers/specs/2026-08-12-pause-backup-teardown-design.md) —
  the approved design spec (§5 covers pause/resume in full, §8 has the cost table this
  runbook's numbers are drawn from).
