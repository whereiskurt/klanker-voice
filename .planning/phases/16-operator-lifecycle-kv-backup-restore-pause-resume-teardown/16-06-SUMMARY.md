---
phase: 16-operator-lifecycle-kv-backup-restore-pause-resume-teardown
plan: 06
subsystem: kv-lifecycle
tags: [go, git, github-cli, pause-resume, testability]
dependency-graph:
  requires: ["16-05"]
  provides: ["GitAPI", "GHAPI", "PreflightLifecycle", "lifecycle_git.go", "lifecycle_gh.go"]
  affects: ["16-07", "16-08"]
tech-stack:
  added: []
  patterns:
    - "func-typed injectable seam (commandRunner) defaulting to an exec.CommandContext closure, mirroring studio.go's didRouterFunc pattern"
    - "sentinel errors wrapped with actionable at-the-raise-site context (fmt.Errorf %w + what/required/fix-command)"
key-files:
  created:
    - kv/internal/app/cmd/lifecycle_git.go
    - kv/internal/app/cmd/lifecycle_git_test.go
    - kv/internal/app/cmd/lifecycle_gh.go
    - kv/internal/app/cmd/lifecycle_gh_test.go
  modified: []
decisions:
  - "ResumeCommitMessage count is 'desired_count=1' -- verified against all three services' committed service.hcl baseline (desired_count = 1 each), not guessed"
  - "PreflightLifecycle takes authCheck as a bare function parameter, not a GHAPI value, so lifecycle_git.go has zero import dependency on lifecycle_gh.go (Task 2 stays optional to Task 1's build)"
  - "LatestRunID/WatchRun use an injectable sleep func(time.Duration) field (default time.Sleep) so bounded-backoff and poll-loop tests run instantly with a no-op sleep, never a real timer"
metrics:
  duration: "~50m"
  completed: 2026-08-20
status: complete
---

# Phase 16 Plan 06: git preflight seam + gh workflow dispatch/tracking Summary

Built the two narrow, offline-testable seams `kv pause`/`kv resume` orchestrate through:
`GitAPI`/`PreflightLifecycle` (four distinct D-18 refusal sentinels — dirty tree, wrong
branch, stale origin, gh auth — each proven to commit and push zero times) and
`GHAPI`/`execGH` (dispatches the existing `terragrunt-apply.yml` unchanged, resolves the
dispatched run id with a bounded backoff, and streams it to a terminal conclusion while
treating the `terraform-apply` environment's required-reviewer wait as an ordinary,
non-error state). 12 subtests across both files, zero real `git`/`gh` invocations, zero
token or environment content in `lifecycle_gh.go`.

## What Was Built

**`kv/internal/app/cmd/lifecycle_git.go`** (Task 1): `GitAPI` interface
(`CurrentBranch`/`IsClean`/`SyncedWithOrigin`/`Diff`/`CommitPaths`/`Push`/`HeadSHA`) +
`execGit` (exec-backed, `Dir=root`, following `knowledge.go`'s convention, stdout/stderr
captured separately so no error ever inlines the process environment).
`PreflightLifecycle(ctx, GitAPI, authCheck func(context.Context) error, requiredBranch)`
runs the four D-18 checks — clean tree, branch, origin sync, gh auth — in that fixed order,
returning on the first failure. Each check has a distinct sentinel
(`ErrPreflightDirtyTree`/`ErrPreflightWrongBranch`/`ErrPreflightStaleOrigin`/`ErrPreflightGHAuth`),
wrapped at the raise site with what was found, what was required, and the one command that
fixes it (e.g. `git checkout main`, `gh auth login`). `authCheck` is a bare function
parameter, not a `GHAPI` value, so this file has zero dependency on Task 2's file.
`CommitPaths` stages and commits only the named paths (`git add -- <paths>` /
`git commit -m <msg> -- <paths>`) so an unrelated stray working-tree file can never ride
along. `SyncedWithOrigin` fetches the remote ref and compares the local and remote commit
objects directly (`git rev-parse <branch>` vs `git rev-parse origin/<branch>`), collapsing
behind/ahead/diverged into a single `false`. `LifecycleBranch = "main"`,
`PauseCommitMessage`/`ResumeCommitMessage` hold the exact D-19 strings — the resume count
(`desired_count=1`) was verified against the actual committed baseline in all three
services' `service.hcl` files, not assumed.

**`kv/internal/app/cmd/lifecycle_gh.go`** (Task 2): `GHAPI` interface
(`AuthStatus`/`DispatchWorkflow`/`LatestRunID`/`WatchRun`) + `execGH`, with every gh
invocation routed through an injectable `commandRunner` field (defaulting to an
`exec.CommandContext` closure with `Dir=root`) — mirroring `studio.go`'s
func-typed-injection pattern. `TerragruntApplyWorkflow = "terragrunt-apply.yml"` and
`EcsServiceApplyModules = "ecs-service"` name the exact workflow file and `modules` input
value the design requires (D-19), and the workflow file itself is never touched (D-23,
confirmed by an empty `git diff --stat -- .github/workflows/`). `AuthStatus` maps a failed
`gh auth status` to `ErrPreflightGHAuth` from Task 1, so `execGH.AuthStatus` is exactly the
function a future `pause.go` passes as `PreflightLifecycle`'s `authCheck` parameter.
`DispatchWorkflow` builds `gh workflow run <workflow> --ref <ref> -f k=v ...` with
inputs sorted by key for deterministic argv. `LatestRunID` polls `gh run list` (bounded to
5 attempts, 500ms backoff) filtering by workflow, branch, and a `notBefore` dispatch
timestamp, returning the newest matching run or `ErrLatestRunNotFound` on exhaustion —
mitigating both an unbounded poll (T-16-06-06) and attaching to the wrong run
(T-16-06-04). `WatchRun` polls `gh run view` to a terminal conclusion, writing a line to
its `io.Writer` on every status change; a `"waiting"` status (the `terraform-apply`
environment's required-reviewer gate) is written as "awaiting the terraform-apply reviewer
approval" and is explicitly not an error or a timeout (D-19/D-23, T-16-06-03) — only a
non-`"success"` terminal conclusion returns an error, naming the run id and the
conclusion. Both `LatestRunID`'s backoff and `WatchRun`'s poll cadence go through an
injectable `sleep func(time.Duration)` field so their tests run instantly with a no-op
sleep instead of a real timer.

## Deviations from Plan

None — plan executed exactly as written. One implementation note: Task 1's `<behavior>`
Test 5 describes "records exactly one Commit... followed by exactly one Push" as part of
the *success path* test, even though `PreflightLifecycle` itself only performs checks (per
the plan's own `<action>` text: "running the four checks... and returning on the first
failure"). Resolved by having that subtest call `PreflightLifecycle` (asserting nil), then
directly exercise the fake's `CommitPaths`/`Push` with the exact args a future
`pause.go` will use (`PauseCommitMessage`, `SiteHCLRelPath`, `LifecycleBranch`) — proving
the fake's recording semantics and the intended one-path-commit contract that Task
16-07/16-08's orchestration will rely on, without inventing a new production function this
plan's `<action>`/artifact list never declared.

## Self-Check: PASSED

- FOUND: kv/internal/app/cmd/lifecycle_git.go
- FOUND: kv/internal/app/cmd/lifecycle_git_test.go
- FOUND: kv/internal/app/cmd/lifecycle_gh.go
- FOUND: kv/internal/app/cmd/lifecycle_gh_test.go
- FOUND commit 3bf7b63: test(16-06): add failing table tests for git preflight refusals
- FOUND commit 12a23d4: feat(16-06): implement git preflight seam with four D-18 refusals
- FOUND commit 04d0fa5: test(16-06): add failing table tests for gh workflow dispatch/run tracking
- FOUND commit 6570eb9: feat(16-06): implement gh workflow dispatch, run-id resolution, and streaming

## TDD Gate Compliance

Both tasks (`tdd="true"`) followed RED/GREEN, with the implementation file temporarily
held back (renamed off-tree) so the RED commit's `go test` failure was a genuine compile
failure against the working tree, not just an uncommitted-file artifact:

**Task 1:**
- RED: `test(16-06)` commit 3bf7b63 — `lifecycle_git_test.go` added with
  `lifecycle_git.go` absent from the tree; confirmed `go test` failed with
  `undefined: PreflightLifecycle` / `LifecycleBranch` / the four sentinels.
- GREEN: `feat(16-06)` commit 12a23d4 — `lifecycle_git.go` restored; all 6 subtests
  (`TestPreflightLifecycle`'s 6 t.Run cases + `TestExecGit_Constructor`) pass.
- REFACTOR: not needed — no follow-up commit.

**Task 2:**
- RED: `test(16-06)` commit 04d0fa5 — `lifecycle_gh_test.go` added with
  `lifecycle_gh.go` absent; confirmed `go test` failed with `undefined: execGH` /
  `TerragruntApplyWorkflow` / `ErrLatestRunNotFound` / etc.
- GREEN: `feat(16-06)` commit 6570eb9 — `lifecycle_gh.go` restored; all 6 tests pass
  (`TestExecGH_DispatchWorkflow_BuildsExpectedArgv`,
  `TestExecGH_LatestRunID_ReturnsNewestRunAtOrAfterDispatch`,
  `TestExecGH_LatestRunID_EmptyPayloadReturnsBoundedError`,
  `TestExecGH_WatchRun_FailureConclusionIsError`,
  `TestExecGH_WatchRun_WaitingStateIsNotAnError`,
  `TestExecGH_NoTokenOrEnvironmentInArgv`).
- REFACTOR: not needed — no follow-up commit.

## Verification

- `go -C kv build ./...`, `go -C kv vet ./...`, `go -C kv test ./...` all exit 0
  (full suite: `internal/app/cmd`, `internal/app/electro`, `internal/app/studio` — all pass).
- `grep -Eic 'GITHUB_TOKEN|GH_TOKEN|os.Environ' kv/internal/app/cmd/lifecycle_gh.go` = 0.
- `git diff --stat -- .github/workflows/` is empty — no workflow file created or modified.
- Neither test file contains `exec.Command` or a `t.TempDir()`-backed git repository.
- No real workflow dispatch, `git push`, or `terragrunt apply` was performed at any point.
