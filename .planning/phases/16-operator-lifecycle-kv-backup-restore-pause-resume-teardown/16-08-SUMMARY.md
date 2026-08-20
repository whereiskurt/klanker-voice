---
phase: 16-operator-lifecycle-kv-backup-restore-pause-resume-teardown
plan: 08
subsystem: kv-lifecycle
tags: [go, cobra, pause-resume, ecs, alb, gh, git, cost-reporting]
dependency-graph:
  requires: ["16-05", "16-06", "16-07"]
  provides: ["kv pause", "kv resume", "kv pause status", "RunLifecycleFlip", "PrintPauseReport", "pause.go"]
  affects: ["16-09"]
tech-stack:
  added: []
  patterns:
    - "shared *stepLog recorder wrapping several independent test fakes (GitAPI/GHAPI/ECSAPI/TargetHealthAPI) to assert cross-fake call ORDER, not just per-fake call counts"
    - "capture-before-mutate + explicit restore closure for the confirm-or-cancel file-rewrite pattern (D-18)"
key-files:
  created:
    - kv/internal/app/cmd/pause.go
    - kv/internal/app/cmd/pause_test.go
    - docs/ops/pause-resume.md
  modified:
    - kv/internal/app/cmd/root.go
decisions:
  - "RunLifecycleFlip returns only `error` per the plan's literal signature; PauseReport/PrintPauseReport are built and printed internally at the end of a successful run, so no success line can ever be emitted before every prior step (including the D-22 health wait) has returned nil"
  - "Doc comment explaining the kill-switch prohibition (D-24) paraphrases killswitch.go's function names generically instead of naming ReadKillswitchStatus/EngageKillswitch/DisengageKillswitch literally, so the grep -Ec acceptance criterion holds even inside comments (same technique 16-05 used for the hclwrite grep check)"
  - "DrainOptions.Correct is explicitly set to true in kv pause's production wiring (NewPauseCmd) -- the field defaults to false (never mutate) per 16-07's design, but an operator-facing pause must actually apply the D-20 correction, not just report a stuck service"
metrics:
  duration: "~55m"
  completed: 2026-08-20
status: complete
---

# Phase 16 Plan 08: kv pause / kv resume — command assembly and cost-posture report Summary

Assembled the pause stage's three operator-facing commands (`kv pause [--yes] [--reason]`,
`kv resume [--yes]`, `kv pause status`) by wiring 16-05's paused-flag rewriter, 16-06's git
preflight/gh dispatch seams, and 16-07's ECS drain/ALB health gate into the spec §5.3
seven-step flow, with a completion report that states the cost posture, the ElevenLabs Pro
caveat, paused-behaviour facts, and the kill-switch's deliberate isolation. 15 subtests
across `TestRunLifecycleFlip`, `TestRunLifecycleFlipResume`, `TestPauseCmd`, `TestResumeCmd`,
and `TestPrintPauseReport`, all passing on the first GREEN run; `grep -Ec` for any
kill-switch function name in `pause.go` (including comments) is 0.

## What Was Built

**`kv/internal/app/cmd/pause.go`**: `RunLifecycleFlip(ctx, LifecycleDeps, LifecycleFlipOptions) error`
implements the flow in fixed order — `PreflightLifecycle` (D-18) with `deps.GH.AuthStatus`
wired as the auth check; an idempotent `ReadPausedFlagFile` read that no-ops (prints status,
returns nil, touches nothing) when the flag already matches `opts.Want`; a capture-original-
bytes-then-`SetPausedFlagFile` rewrite whose result is restored verbatim (original bytes,
original file mode) on either a declined or non-interactive confirmation, never partially
applied; `deps.Git.CommitPaths`/`Push` scoped to `SiteHCLRelPath` with `PauseCommitMessage`/
`ResumeCommitMessage` plus an optional `--reason` trailing line; `DispatchWorkflow` with the
D-19 `modules=ecs-service` input, `LatestRunID` resolution timestamped from `deps.Now`, and
`WatchRun` streamed to `deps.Out`, aborting before any verification step on a failed
conclusion; and, on success, either `DrainToZero` (pause) or `WaitForServicesRunning` then
`WaitForTargetsHealthy` over `VoiceAndAuthTargetGroups(deps.TargetGroups)` (resume) — the
health wait's error is returned unchanged with no success line ever written before it returns
nil (D-22). `confirmAffirmative` treats an explicit "no", garbage, or an EOF from an
empty/non-interactive reader identically: none is ever an implied yes (D-18).

`NewPauseCmd`/`NewResumeCmd` build the cobra commands (`--yes`/`--reason` on pause, `--yes`
on resume; a `status` subcommand on pause), each resolving `LifecycleDeps` via
`buildLifecycleDeps` — `repoRoot()`, `NewExecGit`/`NewExecGH`, `cfg.ECSClient`/
`cfg.ELBv2Client` wrapped by `NewECSAPI`/`NewTargetHealthAPI`, and `ResolveECSPosture` over
`NewTerragruntOutputReader`. `kv pause status` reads the flag and calls `DescribeServices`
directly — no `RunLifecycleFlip`, so it never commits, pushes, or dispatches. Both commands
are registered in `root.go` alongside the existing command tree.

`PauseReport`/`PrintPauseReport` render the D-25 completion report: for a pause, the
running-vs-paused AWS cost figures, the ElevenLabs Pro ($99/mo) caveat as a manual
vendor-console step, the three paused-behaviour facts (voice page loads/mic tap 503s, auth
host 503s, DIDs provisioned/fast busy), the kill-switch-was-not-touched statement, the
config-is-the-guard mid-pause-deploy statement, and — when `PauseReport.Corrected` is
non-empty — the named corrected services and that the Application Auto Scaling ordering
correction fired; for a resume, the restored cost posture and that services are running and
targets are healthy.

**`docs/ops/pause-resume.md`** (Task 3): the operator runbook — the three commands and flags
as built; the `infra/.envrc` prerequisite (same as backup-restore.md); each D-18 preflight
refusal and how to clear it; the diff-and-confirm step's non-interactive-stdin-is-a-refusal
rule; the `terraform-apply` required-reviewer gate and what the "waiting" status means; the
Application Auto Scaling ordering hazard and what a reported correction means; an in-flight
pause and how to force one immediately (kill-switch first); the §8 cost table with the
ElevenLabs Pro caveat; paused-state behaviour of the voice page/auth host/DIDs; kill-switch
orthogonality; the mid-pause-deploy safety property; and a three-scenario troubleshooting
section (a service that will not drain, a resume whose targets never go healthy, a hand-edited
ambiguous `site.hcl`). Cross-links `docs/ops/backup-restore.md` and the design spec.

## Deviations from Plan

**1. [Rule 2-adjacent, doc-comment adjustment] Kill-switch function names paraphrased in the
doc comment, not named literally.** The plan's action text and the D-24 prohibition itself
require `grep -Ec 'ReadKillswitchStatus|EngageKillswitch|DisengageKillswitch' pause.go` to
return 0. An early draft of the package-level doc comment explaining *why* the prohibition
exists named those three functions to point at killswitch.go — which the grep then correctly
flagged, including inside a comment. Reworded to "the kill-switch mechanism defined in
killswitch.go" (no literal function names), preserving the explanation while satisfying the
literal acceptance criterion. Verified: `grep -Ec` on the final file returns 0.

No other deviations — plan executed exactly as written, including the literal
`LifecycleDeps`/`LifecycleFlipOptions`/`PauseReport` field sets and the `RunLifecycleFlip`/
`NewPauseCmd`/`NewResumeCmd`/`PrintPauseReport` signatures specified in the action text.

## Self-Check: PASSED

- FOUND: kv/internal/app/cmd/pause.go
- FOUND: kv/internal/app/cmd/pause_test.go
- FOUND: docs/ops/pause-resume.md
- FOUND commit 3a0e43a: test(16-08): add failing tests for kv pause/resume orchestration and report
- FOUND commit 57a6512: feat(16-08): implement kv pause/resume orchestration and completion report
- FOUND commit 0db7f98: docs(16-08): add pause/resume operator runbook

## TDD Gate Compliance

Tasks 1+2 share `pause.go`/`pause_test.go` and were driven as one RED/GREEN cycle (both
`tdd="true"`, and Task 2 only extends the same two files Task 1 creates):

- RED: `test(16-08)` commit 3a0e43a — `pause_test.go` added with `pause.go` absent from the
  tree; confirmed `go test` failed to compile with `undefined: LifecycleDeps` /
  `LifecycleFlipOptions` / `RunLifecycleFlip` / `ErrLifecycleCancelled` / etc.
- GREEN: `feat(16-08)` commit 57a6512 — `pause.go` added (plus the two-line `root.go`
  registration); all 15 subtests passed on the first run:
  - `TestRunLifecycleFlip` (7): AlreadyPausedIsANoOp, PreflightRefusalLeavesFileUnchanged,
    DeclinedConfirmationRestoresFile, NonInteractiveStdinWithoutYesRefuses,
    HappyPathRecordsStepsInOrder, FailedCIRunAbortsBeforeDrain,
    DrainErrorReturnsNonZeroNamingService
  - `TestPauseCmd` (1): HasYesReasonFlagsAndStatusSubcommand
  - `TestRunLifecycleFlipResume` (2): ResumeCallsWaitForServicesRunningThenWaitForTargetsHealthy,
    HealthWaitErrorMakesResumeNonZeroNoSuccessLine
  - `TestPrintPauseReport` (5): ContainsCostPostureElevenLabsKillswitch503FastBusy,
    StatesMidPauseDeployLeavesStackPaused, StatesPausedBehaviourFacts,
    StatesKillswitchNotTouched, NamesCorrectedServicesWhenPresent
  - `TestResumeCmd` (1): HasYesFlag
- One doc-comment fixup after GREEN (see Deviations) to satisfy the killswitch grep check;
  re-verified `grep -Ec` = 0 and `go build ./...` still clean — folded into the same GREEN
  commit's working state before it was committed (no separate REFACTOR commit needed).
- REFACTOR: not needed beyond the fixup above — no separate commit.

## Verification

- `go -C kv build ./...`, `go -C kv vet ./...`, `go -C kv test ./... -count=1` all exit 0
  (full suite: `internal/app/cmd`, `internal/app/electro`, `internal/app/studio` — all pass).
- `go -C kv test ./internal/app/cmd/ -run 'TestRunLifecycleFlip|TestPauseCmd' -count=1 -v`:
  8 passing subtests (exceeds the ≥7 threshold).
- `go -C kv test ./internal/app/cmd/ -run 'TestResumeCmd|TestPrintPauseReport|TestRunLifecycleFlipResume' -count=1 -v`:
  8 passing subtests (exceeds the ≥7 threshold).
- `grep -Ec 'ReadKillswitchStatus|EngageKillswitch|DisengageKillswitch' kv/internal/app/cmd/pause.go` = 0.
- `go -C kv run ./cmd/kv pause --help`, `... pause status --help`, and `... resume --help` all
  exit 0 with the documented flags/subcommand.
- `git diff --stat -- .github/workflows/` is empty — no workflow file created or modified.
- `infra/terraform/live/site/site.hcl` still reads `paused = false` — no live flip, commit,
  push, dispatch, or apply was performed at any point in this plan's execution.
- No test file constructs a real git/gh binary invocation, `*ecs.Client`,
  `*elasticloadbalancingv2.Client`, or `awsconfig.LoadDefaultConfig` (grep-verified empty).
- `docs/ops/pause-resume.md` contains all required strings (`kv pause`, `kv resume`,
  `kv pause status`, `terraform-apply`, `Application Auto Scaling`, `ElevenLabs`,
  `kill-switch`, `fast busy`) and a three-scenario troubleshooting section; both cross-links
  (`docs/ops/backup-restore.md`, the design spec) resolve to real files.

## Human-Check Note (deferred to 16-09)

Per the plan's `<verification><human-check>`, the captured pause completion report should be
read by a human to confirm it says what an operator would want to hear at the moment they
pause the stack for a month. The exact rendered text is reproduced here for that review
(from `TestPrintPauseReport`'s fixtures, `PrintPauseReport` output for a pause with
`Corrected: ["voice"]` and a run id):

```
kv pause: complete.

Cost posture: running ~$190/mo -> paused ~$60/mo (Fargate now $0/mo; NAT+ALB+WAF+misc ~$60/mo continues).
ElevenLabs Pro ($99/mo) is unaffected by this command and becomes the largest remaining
line item during a long pause; pausing or downgrading it is a manual step in the
ElevenLabs console -- not something this command can do.

While paused:
  - voice.klankermaker.ai still loads from CloudFront/S3; only the mic tap fails (/api/offer -> 503).
  - auth.klankermaker.ai is ALB-only and returns 503.
  - the DIDs stay provisioned and billed by VoIP.ms; SIP registration drops and callers get a fast busy.

The kill-switch was not touched -- it is a separate, application-layer mechanism
(kv killswitch) and remains in whatever state it was already in.
A CI deploy landing while paused re-applies desired_count=0 from site.hcl and
leaves the stack paused -- the config is the guard, so this is safe by construction.

Application Auto Scaling ordering correction fired for: voice
(kv corrected desired_count back to 0 itself; terraform already recorded 0, so no drift was introduced.)

Apply run: <id> (verified in <duration>)
```

The live confirmation call and the actual operator run of `kv pause`/`kv resume` against the
real stack is 16-09's gate, not this plan's — this plan only wires and unit-tests the
commands (per this plan's `<critical_constraints>`).
