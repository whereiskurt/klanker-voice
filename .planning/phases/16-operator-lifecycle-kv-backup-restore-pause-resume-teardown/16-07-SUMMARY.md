---
phase: 16-operator-lifecycle-kv-backup-restore-pause-resume-teardown
plan: 07
subsystem: kv-lifecycle
tags: [go, ecs, elbv2, pause-resume, autoscaling-hazard, task-protection]
dependency-graph:
  requires: ["16-01", "16-06"]
  provides: ["ECSAPI", "DrainToZero", "WaitForServicesRunning", "CountInFlightSessions", "TargetHealthAPI", "WaitForTargetsHealthy", "VoiceAndAuthTargetGroups"]
  affects: ["16-08"]
tech-stack:
  added: []
  patterns:
    - "call-count-driven stateful fake (not wall-clock scripted) for hazard/correction/grace state machines, so bounce-back and timeout assertions are exact rather than racy"
    - "narrow injectable AWS seam (ECSAPI / TargetHealthAPI) adapting the real *ecs.Client / *elasticloadbalancingv2.Client, mirroring lifecycle_git.go/lifecycle_gh.go's GitAPI/GHAPI convention"
key-files:
  created:
    - kv/internal/app/cmd/lifecycle_ecs.go
    - kv/internal/app/cmd/lifecycle_ecs_test.go
    - kv/internal/app/cmd/lifecycle_alb.go
    - kv/internal/app/cmd/lifecycle_alb_test.go
  modified: []
decisions:
  - "DrainOptions.Correct is a real field (not defaulted true internally): when false, a stuck service still ends the drain in a non-nil error but DrainToZero never calls UpdateDesiredCount itself -- the plan's action text specified this field without prescribing its exact semantics, and 'never mutate unless explicitly told to' was the safer reading for a Claude's-Discretion field on a command that touches live ECS state"
  - "VoiceAndAuthTargetGroups matches target-group keys by exact name OR by a '<service>-' prefix, because the real ecs-service module's target_groups output key shape is '<service>-<region_label>-lb-<idx>' (main.tf's lb_map key construction) while 16-01's own ResolveECSPosture test fixtures use the simplified exact-name form ('voice', 'auth') -- both forms are tested so neither the real terraform shape nor the established fixture convention breaks"
  - "CountInFlightSessions' doc comment cites apps/voice/src/klanker_voice/session.py:475-509 (_set_scale_in_protection/_reconcile_scale_in_protection) as the traced source of how ECS task-scale-in protection is actually driven -- a per-TASK boolean set via the ecs:UpdateTaskProtection control-plane API (boto3), not the ECS agent's local metadata endpoint"
metrics:
  duration: "~65m"
  completed: 2026-08-20
status: complete
---

# Phase 16 Plan 07: ECS drain-to-zero + ALB resume health gate Summary

Closed the two real hazards of `kv pause`/`kv resume` in code: `DrainToZero` observes all
three ECS services until running and desired both read zero (the only success condition,
D-20), detecting and correcting an Application Auto Scaling bounce-back the instant it is
observed rather than waiting out the ten-minute timeout, and reporting `waiting for N
in-flight session(s) to drain` while a live call holds ECS task-scale-in protection (D-21).
`WaitForTargetsHealthy` gates `kv resume`'s success on observed ALB target-group health
rather than a clean `terragrunt apply` (D-22), explicitly refusing to treat an empty target
group as healthy since that is exactly the paused-stack shape resume exists to clear. 19
subtests across both files, zero real ECS/ELBv2 clients, zero AWS calls, all four D-20/D-21/
D-22 hazard paths driven end to end against a deterministic stateful fake.

## What Was Built

**`kv/internal/app/cmd/lifecycle_ecs.go`** (Tasks 1 + 2): `ECSAPI` interface
(`DescribeServices`/`UpdateDesiredCount`/`ListRunningTasks`/`GetTaskProtection`) +
`ecsClientAPI` adapting `*ecs.Client`, mirroring `lifecycle_git.go`/`lifecycle_gh.go`'s
seam convention exactly. `DrainToZero(ctx, api, cluster, services, w, DrainOptions)` polls
every named service each `PollInterval` (default 10s, ten-minute `defaultDrainTimeout`
budget): a service is drained when both `Running` and `Desired` read zero, and the *only*
success condition is every named service reaching that state simultaneously. It fires the
D-20 correction (`UpdateDesiredCount` to zero) the moment a service previously observed at
zero is observed above zero again — the ordering hazard actually occurring, corrected
without waiting for the timeout — and also corrects any service still above zero once the
timeout elapses. A correction is never issued for a service already at zero and this file
never touches autoscaling (grep-verified, 0 hits for `appautoscaling|RegisterScalableTarget|
PutScalingPolicy`). After a correction, `DrainToZero` keeps polling for up to
`GraceAfterCorrection` (default 90s); a service still above zero once that window elapses is
a hard, named error — never a best-effort success. A transient `DescribeServices` error is
retried (bounded by `maxDrainDescribeErrors = 5`); a persistent one returns an error and
never a false success. `WaitForServicesRunning` is resume's observe-only counterpart (no
correction path — resume has no equivalent ordering hazard). `CountInFlightSessions` sums
ECS task-scale-in-protected tasks across services (skipping the protection lookup entirely
for a service with zero running tasks) and is wired into `DrainToZero`'s poll loop: it
prints the D-21 `waiting for N in-flight session(s) to drain` line only when the count is
positive and has changed since the last observation (never repeated verbatim on an unchanged
count), degrades to an `unavailable` line on a protection-lookup error without aborting the
drain, and `DrainReport.InFlightPeak` records the highest count observed. Its doc comment
traces the actual mechanism to `apps/voice/src/klanker_voice/session.py:475-509` — protection
is a per-TASK boolean driven via the `ecs:UpdateTaskProtection` control-plane API through
boto3 directly, not the ECS agent's local endpoint. No code path in this file calls
`StopTask`, `DeregisterTargets`, or `ForceNewDeployment` (grep-verified, 0 hits) — waiting is
the entire drain mechanism.

**`kv/internal/app/cmd/lifecycle_alb.go`** (Task 3): `TargetHealthAPI` interface
(`DescribeTargetHealth`) + `elbv2ClientAPI` adapting `*elasticloadbalancingv2.Client`.
`WaitForTargetsHealthy(ctx, api, targetGroups, w, timeout, interval)` (default ten-minute
timeout, 10s poll) polls every named group until it has at least one registered target and
every registered target reports `healthy` — a group with zero registered targets is
explicitly **not** satisfied (that state is precisely a paused stack, spec §5.5), so
resume can never fall through with a false success. A group stuck unhealthy or empty through
the timeout returns a named error listing every unsatisfied group and its last-observed
states; a transient describe error is retried (bounded, same pattern as `lifecycle_ecs.go`).
`VoiceAndAuthTargetGroups` selects the voice/auth entries out of the full target-group map,
matching a key by exact name or by the real ecs-service module's
`<service>-<region_label>-lb-<idx>` prefix shape, tolerating telephony-edge's absence (no
load balancer) while erroring if neither voice nor auth is present.

## Deviations from Plan

**1. [Design decision, not a deviation] Test-fake architecture chosen to be call-count driven,
not wall-clock scripted.** The plan's behavior list (bounce-back "without waiting out the full
timeout", timeout-triggered correction, stuck-after-grace) implies exact temporal ordering
between polls, a correction, and a later re-observation. A naive scripted-response-array fake
indexed purely by call count would have made the *test's* correctness depend on predicting
exactly which wall-clock-driven call index a correction fires at — fragile under CI
scheduling jitter. Built `fakeDrainECS`/`fakeTargetHealthAPI` as small stateful in-memory
simulators instead (state changes only in response to `UpdateDesiredCount` or an explicit
per-service script such as `bounceOnce`/`naturalZeroAtCall`), so every assertion is exact and
deterministic while `Timeout`/`PollInterval`/`GraceAfterCorrection` are still real (tiny)
`time.Duration` values exercising the genuine poll loop, satisfying the plan's "under 10
seconds, proving the timing knobs are injectable" acceptance criterion without timing races.
Not a deviation from any `<action>`/artifact requirement — `ECSAPI`/`TargetHealthAPI` are
public interfaces per the plan; the fake shape implementing them beneath the test file is
Claude's Discretion territory (test conventions).

No other deviations — plan executed exactly as written, including the literal
`DrainOptions{Timeout, PollInterval, Correct, GraceAfterCorrection}` field set and both
`defaultDrainTimeout = 10 * time.Minute` and `defaultHealthTimeout = 10 * time.Minute`
constants exactly as specified.

## Self-Check: PASSED

- FOUND: kv/internal/app/cmd/lifecycle_ecs.go
- FOUND: kv/internal/app/cmd/lifecycle_ecs_test.go
- FOUND: kv/internal/app/cmd/lifecycle_alb.go
- FOUND: kv/internal/app/cmd/lifecycle_alb_test.go
- FOUND commit a2be102: test(16-07): add failing tests for ECS drain-to-zero and in-flight session reporting
- FOUND commit 04af8ea: feat(16-07): implement ECS drain-to-zero, resume wait, and in-flight session reporting
- FOUND commit cc97dd3: test(16-07): add failing tests for ALB target-health resume gate
- FOUND commit 3eb1005: feat(16-07): implement ALB target-health resume gate

## TDD Gate Compliance

Both tasks (Task 1+2 share `lifecycle_ecs.go`; Task 3 is `lifecycle_alb.go`) followed
RED/GREEN, with the implementation file temporarily moved off-tree so each RED commit's
`go test` failure was a genuine compile failure against the working tree, not just an
uncommitted-file artifact:

**Tasks 1 + 2 (`lifecycle_ecs.go`):**
- RED: `test(16-07)` commit a2be102 — `lifecycle_ecs_test.go` added with
  `lifecycle_ecs.go` absent; confirmed `go test` failed with `undefined: ServicePosture` /
  `DrainToZero` / `DrainOptions` / etc.
- GREEN: `feat(16-07)` commit 04af8ea — `lifecycle_ecs.go` restored; all 15 subtests pass
  on the first run (`TestDrainToZero` x6, `TestWaitForServicesRunning` x1,
  `TestCountInFlightSessions` x2, `TestDrainToZeroInFlight` x6 — exceeding both the ≥7 and
  ≥6 acceptance thresholds).
- REFACTOR: not needed — no follow-up commit.

**Task 3 (`lifecycle_alb.go`):**
- RED: `test(16-07)` commit cc97dd3 — `lifecycle_alb_test.go` added with
  `lifecycle_alb.go` absent; confirmed `go test` failed with `undefined: TargetState` /
  `WaitForTargetsHealthy` / etc.
- GREEN: `feat(16-07)` commit 3eb1005 — `lifecycle_alb.go` restored; all 9 subtests pass
  on the first run (`TestWaitForTargetsHealthy` x6, `TestVoiceAndAuthTargetGroups` x3 —
  exceeding the ≥6 acceptance threshold).
- REFACTOR: not needed — no follow-up commit.

## Verification

- `go -C kv build ./...`, `go -C kv vet ./...`, `go -C kv test ./... -count=1` all exit 0
  (full suite: `internal/app/cmd`, `internal/app/electro`, `internal/app/studio` — all pass).
- `go test ./internal/app/cmd/ -run 'TestDrainToZero' -count=1` completes in ~0.4s;
  `go test ./internal/app/cmd/ -run 'TestWaitForTargetsHealthy' -count=1` completes in
  ~0.3s — both far under the 10-second acceptance bound, proving every timing knob is
  injectable.
- `grep -v '^\s*//' lifecycle_ecs.go | grep -Eic 'appautoscaling|RegisterScalableTarget|PutScalingPolicy'` = 0.
- `grep -v '^\s*//' lifecycle_ecs.go | grep -Eic 'StopTask|DeregisterTargets|ForceNewDeployment'` = 0.
- No test file constructs a real `*ecs.Client`, `*elasticloadbalancingv2.Client`, or
  `awsconfig.LoadDefaultConfig` (grep-verified empty).
- No AWS service was scaled, updated, or mutated at any point during this plan's execution
  — every assertion runs against `fakeDrainECS`/`scriptedProtectionECS`/
  `fakeTargetHealthAPI`, all pure in-memory fakes.
