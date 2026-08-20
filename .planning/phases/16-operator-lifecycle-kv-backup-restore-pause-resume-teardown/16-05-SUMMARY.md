---
phase: 16-operator-lifecycle-kv-backup-restore-pause-resume-teardown
plan: 05
subsystem: infra-config + kv-lifecycle
tags: [terraform, hcl, go, pause-resume, ecs]
dependency-graph:
  requires: ["16-04"]
  provides: ["paused-flag-mechanism", "lifecycle_pauseflag.go"]
  affects: ["16-06", "16-07", "16-08"]
tech-stack:
  added: []
  patterns:
    - "line-oriented byte-surgical HCL rewrite (no parser/formatter round trip)"
    - "comment- and string-literal-aware single-line scan for a top-level assignment"
key-files:
  created:
    - kv/internal/app/cmd/lifecycle_pauseflag.go
    - kv/internal/app/cmd/lifecycle_pauseflag_test.go
    - kv/internal/app/cmd/testdata/site-hcl/unpaused.hcl
    - kv/internal/app/cmd/testdata/site-hcl/paused.hcl
    - kv/internal/app/cmd/testdata/site-hcl/missing-flag.hcl
    - kv/internal/app/cmd/testdata/site-hcl/duplicate-flag.hcl
  modified:
    - infra/terraform/live/site/site.hcl
decisions:
  - "Comment- and string-aware single-line scan (codePortion) instead of a full HCL parse, per D-31/§5.3 -- keeps the diff to exactly one boolean literal"
  - "locatePausedFlag returns line index; ReadPausedFlag/SetPausedFlag/ReadPausedFlagFile/SetPausedFlagFile all delegate to it so match/error logic exists in exactly one place"
metrics:
  duration: "~45m"
  completed: 2026-08-20
status: complete
---

# Phase 16 Plan 05: Pause flag mechanism (site.hcl boolean + byte-surgical Go rewriter) Summary

Added the single git-tracked `paused` boolean to `infra/terraform/live/site/site.hcl` that
drives both `desired_count = 0` and `autoscaling.min_capacity = 0` across all three ECS
services via one `for`-expression, and built `lifecycle_pauseflag.go` — a comment- and
string-literal-aware line scanner that reads/flips that boolean with a provably one-line
diff, backed by 7 D-31 table-test cases (10 subtests) proving byte-identical output,
idempotent no-ops, round-trip stability, and named errors on missing/ambiguous input.

## What Was Built

**`infra/terraform/live/site/site.hcl`** (Task 1): a `local.paused` boolean (default
`false`) immediately above `ecs_services`, with a comment naming `kv pause`/`kv resume` as
its sole owners and listing everything that stays put (VPC, NAT+EIP, ALB, WAF, CloudFront,
Route53, ACM, DynamoDB, S3 ledger, ECR). `ecs_services.services` is now a `for`-expression
over the three service locals that, when `local.paused` is true, `merge()`s each service
with `desired_count = 0` and a nested `merge()` on `autoscaling` setting `min_capacity = 0`
— both overrides in the same conditional, with a comment citing the Application Auto Scaling
hazard (D-16) and the exact module lines (`main.tf:252`, `:313`, `:323`) it comes from.
`ecs_tasks` is byte-for-byte untouched (D-17); no `.github/workflows/` file was touched
(D-23).

**`kv/internal/app/cmd/lifecycle_pauseflag.go`** (Task 2): `ReadPausedFlag`/`SetPausedFlag`
operate on raw `[]byte`, splitting on `"\n"` (invertible via `bytes.Join`, so untouched
lines survive byte-identical regardless of trailing-newline or CRLF quirks) and scanning
each line's *code portion* — `codePortion()` walks the line tracking double-quoted string
state (with backslash-escaping) so a `#`/`//` inside a string is never mistaken for a
comment start, and a comment's own mention of "paused" is excluded before the assignment
regex ever sees it. `locatePausedFlag` counts every line whose code portion matches
`^\s*paused\s*=\s*(true|false)\s*$`: zero matches is `ErrPausedFlagNotFound`, more than one
is `ErrPausedFlagAmbiguous` (both sentinel errors, `errors.Is`-compatible), exactly one is
the flag. `SetPausedFlag` replaces only the matched boolean literal's byte range within that
one line and returns the input slice unmodified with `changed=false` when the file already
holds the requested value (D-18). `ReadPausedFlagFile`/`SetPausedFlagFile` join a `repoRoot`
with the new `SiteHCLRelPath` constant, delegate to the byte-level functions, and (on
`SetPausedFlagFile`) write back only when `changed`, preserving the original file mode via
`os.Stat` before the read.

**Fixtures** (`kv/internal/app/cmd/testdata/site-hcl/`): `unpaused.hcl`/`paused.hcl` are
realistic excerpts of the post-Task-1 `site.hcl` (comment block, `ecs_tasks`, the
`ecs_services` for-expression) differing in exactly the `paused` literal.
`missing-flag.hcl` removes the assignment but keeps two decoys — a comment mentioning
"paused" and a string value (`release_notes = "... paused ..."`) containing the word — both
of which the codePortion/regex scan must not match. `duplicate-flag.hcl` carries two
top-level `paused` assignments.

## Deviations from Plan

None — plan executed exactly as written. One implementation note: the plan's action text
used "hclwrite" as the specific library name being deliberately avoided; since the
acceptance criterion `grep -c 'hclwrite' lifecycle_pauseflag.go` requires that exact string
to be absent, the doc comment explaining *why* a parser/formatter round trip is the wrong
choice was phrased generically ("a whole-file HCL formatter/writer") instead of naming the
library, so the file both explains the rationale and satisfies the literal grep check.

## Verification tool note (§5.1 acceptance criterion)

`terragrunt` and `hclfmt` are both installed locally. Running `terragrunt hcl format --diff`
(the current-CLI spelling of the plan's `hclfmt --diff`) recursively over the whole
`infra/terraform/live/site` tree turned out to **write** two unrelated files in place
(`services/auth/service.hcl`, `services/voice/service.hcl` — pre-existing formatting drift,
not part of this plan's scope); those changes were immediately reverted via
`git checkout --`. The safe, non-mutating check used instead was
`terragrunt hcl format --check --file site.hcl`, which exited 0 (no reformat needed) and,
combined with `git status --short`, confirmed no other file was touched. This is the same
guarantee the plan's `hclfmt --diff` check was after, obtained via `--check` instead to
avoid the accidental-write behavior of `--diff` on this terragrunt version (0.99.1).

## Self-Check: PASSED

- FOUND: infra/terraform/live/site/site.hcl (modified, `paused = false` present)
- FOUND: kv/internal/app/cmd/lifecycle_pauseflag.go
- FOUND: kv/internal/app/cmd/lifecycle_pauseflag_test.go
- FOUND: kv/internal/app/cmd/testdata/site-hcl/unpaused.hcl
- FOUND: kv/internal/app/cmd/testdata/site-hcl/paused.hcl
- FOUND: kv/internal/app/cmd/testdata/site-hcl/missing-flag.hcl
- FOUND: kv/internal/app/cmd/testdata/site-hcl/duplicate-flag.hcl
- FOUND commit e67b590: feat(16-05): add paused flag driving both ecs_services overrides
- FOUND commit 9f57d59: test(16-05): add failing table tests for the paused-flag reader/writer
- FOUND commit d9205f2: feat(16-05): implement byte-surgical paused-flag reader and writer

## TDD Gate Compliance

Task 2 (`tdd="true"`) followed RED/GREEN:
- RED: `test(16-05)` commit 9f57d59 — tests + fixtures added, confirmed build-failure
  (`undefined: ReadPausedFlag` etc.) before any implementation existed.
- GREEN: `feat(16-05)` commit d9205f2 — `lifecycle_pauseflag.go` added, all 10 subtests pass
  (7+ required by D-31), `grep -c hclwrite` = 0.
- REFACTOR: not needed — no follow-up commit.
