---
phase: quick-260826-i2c
plan: 01
status: complete
subsystem: docs
tags: [operator-docs, kv-cli, pause-resume, backup-restore, phase-16]

requires:
  - phase: quick-260818-opm
    provides: "docs/operators/ — the eight-page operator manual and its verify-operator-docs.sh gate"
  - phase: "16 (pause/backup/teardown)"
    provides: "kv pause / kv pause status / kv resume / kv backup / kv restore, and their docs/ops/*.md runbooks"
provides:
  - "docs/operators/kv-cli-reference.md — two new command groups (kv pause/resume, kv backup/restore), five level-3 entries, matching Command map rows"
  - "docs/operators/README.md — a Start here entry, two new pages-table rows, and a Turning it off step for kv pause"
  - "scripts/verify-operator-docs.sh — DOCS array now covers 10 pages including both docs/ops/*.md runbooks"
affects: [docs, operator-tooling]

tech-stack:
  added: []
  patterns:
    - "docs/ops/ runbooks stay outside docs/operators/ but are reachable from it and covered by the same verifier, rather than being duplicated or moved"

key-files:
  modified:
    - docs/operators/kv-cli-reference.md
    - docs/operators/README.md
    - scripts/verify-operator-docs.sh

key-decisions:
  - "Verified every documented flag against kv/bin/kv --help directly (not the stale ~/go/bin/kv on PATH) rather than trusting the plan's verified-facts table blindly"
  - "Did not touch scripts/sync-wiki.py — the new docs/ops/ links are dead on the public wiki; mitigated by naming the docs/ops/ repo path in prose per-group, and the gap is reported here as an open operator decision rather than silently fixed"
  - "kv destroy was not documented anywhere — it does not exist in root.go, and the plan explicitly prohibited inventing it regardless of task framing"

requirements-completed: [DOCS-OPS-01]

coverage:
  - id: D1
    description: "kv pause, kv pause status, kv resume, kv backup, kv restore each have a CLI-reference entry with real flags/defaults, matching the file's existing conventions, linking to their runbook"
    requirement: DOCS-OPS-01
    verification:
      - kind: other
        ref: "docs/operators/kv-cli-reference.md heading-count + flag-existence check against kv/bin/kv --help (Task 1 automated verify, commit b8ec8f9)"
        status: pass
    human_judgment: false
  - id: D2
    description: "docs/operators/README.md gains a Start here entry point, two pages-table rows, and a Turning it off step, all pointing at the two Phase 16 runbooks"
    requirement: DOCS-OPS-01
    verification:
      - kind: other
        ref: "docs/operators/README.md content/format grep check (Task 2 automated verify, commit 55e48ef)"
        status: pass
    human_judgment: false
  - id: D3
    description: "scripts/verify-operator-docs.sh link-checks docs/ops/pause-resume.md and docs/ops/backup-restore.md, and the gate passes clean under the local build"
    requirement: DOCS-OPS-01
    verification:
      - kind: other
        ref: "KV=kv/bin/kv bash scripts/verify-operator-docs.sh — checked 10 pages, all-passed banner, zero FAIL: occurrences (commit ddd83ff)"
        status: pass
    human_judgment: false

# Metrics
duration: ~15min
completed: 2026-08-26
---

# Quick Task 260826-i2c: Wire Phase 16 lifecycle commands into the operator manual

**Documented `kv pause`, `kv pause status`, `kv resume`, `kv backup`, and `kv restore` in the operator manual — previously findable only in `docs/ops/`, which the manual never linked to.**

## Performance

- **Duration:** ~15 min
- **Completed:** 2026-08-26
- **Tasks:** 3/3
- **Files modified:** 3 (`docs/operators/kv-cli-reference.md`, `docs/operators/README.md`, `scripts/verify-operator-docs.sh`)

## Accomplishments

- Added two new command groups to the CLI reference, placed deliberately between `kv killswitch` and `kv telephony` — five level-3 entries (`kv pause`, `kv pause status`, `kv resume`, `kv backup`, `kv restore <zip>`), every flag verified against the real `kv/bin/kv --help` output, plus matching rows in the Command map table.
- Gave the manual index (`README.md`) an entry point for "how do I take it all down for a few months, and bring it back" — a Start here question, two new rows in "The pages", and a `kv pause` step slotted into "Turning it off" between scaling the phone side to zero and full decommission.
- Extended `scripts/verify-operator-docs.sh`'s `DOCS` array to link-check both `docs/ops/*.md` runbooks (8 → 10 pages checked), without touching the flag/default check lists (those run against a stale PATH `kv`; Task 1's own gate already covers the new flags against the local build).
- All four operator-critical gotchas are present where an operator will hit them: the `infra/.envrc` prerequisite up top in Group A and cross-referenced from Group B, the main-only + clean-tree refusal, the two-required-reviewer round trip warning, and the explicit "kill-switch is untouched, separate mechanism" statement with a cross-link.

## Task Commits

Each task was committed atomically:

1. **Task 1: Add the five lifecycle command entries to the kv CLI reference** — `b8ec8f9` (docs)
2. **Task 2: Give the manual index an entry point for the lifecycle question** — `55e48ef` (docs)
3. **Task 3: Extend the doc verifier to cover the two runbooks, then pass the gate clean** — `ddd83ff` (chore)

_No plan-metadata commit for quick tasks — STATE.md is updated separately per the quick-mode contract._

## Files Modified

- `docs/operators/kv-cli-reference.md` — two new level-1 groups (`kv pause` / `kv resume`, `kv backup` / `kv restore <zip>`), five level-3 entries, two new Command map rows
- `docs/operators/README.md` — one Start here entry, two pages-table rows, one Turning it off step (renumbered)
- `scripts/verify-operator-docs.sh` — `DOCS` array gains `docs/ops/pause-resume.md` and `docs/ops/backup-restore.md`

## Decisions Made

- Documented `kv restore`'s full real flag surface (`--dry-run`, `--tables`, `--ledger`, `--skip-ephemeral` default true) rather than only the flags the original task framing named — this is reporting reality, per the plan's explicit instruction.
- Left `sync-wiki.py` untouched. Its hardcoded page map (`docs/operators/*` only) means the new `../ops/pause-resume.md` and `../ops/backup-restore.md` links will 404 on the published GitHub wiki. Mitigation applied: every group/row mentions the `docs/ops/` repo path in prose, so a wiki reader hitting a dead link still knows where the file lives in-repo. **Whether to publish the two runbooks to the wiki (by extending `sync-wiki.py`'s page map) is an open operator decision, not made here.**
- Confirmed `kv destroy` does not exist (no `destroy.go`, nothing registered in `root.go`) and did not document it anywhere, per the plan's explicit prohibition.

## Deviations from Plan

None — plan executed exactly as written. All flags, defaults, and gotchas came directly from the plan's verified-facts table and were independently re-confirmed against `kv/bin/kv --help` output during Task 1 rather than trusted blindly.

## Issues Encountered

None. The pre-existing 13-FAIL result from the stale `~/go/bin/kv` on PATH was anticipated by the plan and deliberately avoided by running the gate as `KV=kv/bin/kv bash scripts/verify-operator-docs.sh` throughout — this finding is recorded here so it is not rediscovered as a "bug" later. It is unrelated to this task and was not touched.

## User Setup Required

None — no external service configuration required.

## Next Phase Readiness

- The operator manual now fully covers all four Phase 16 lifecycle commands; an operator reading only `docs/operators/` can find the pause/resume and backup/restore path without knowing `docs/ops/` exists ahead of time.
- **Open decision for the operator:** publish `docs/ops/pause-resume.md` and `docs/ops/backup-restore.md` to the public wiki (requires extending `scripts/sync-wiki.py`'s hardcoded page map at lines 48-57) — or leave them repo-only. Not acted on in this task.
- No blockers.

---
*Quick task: 260826-i2c*
*Completed: 2026-08-26*

## Self-Check: PASSED

All modified files exist on disk; all three task commit hashes (`b8ec8f9`, `55e48ef`, `ddd83ff`) are present in git log.
