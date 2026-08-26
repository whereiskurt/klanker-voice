---
phase: 260826-ojd-verify-docs-script-bugfixes
plan: 01
subsystem: docs-tooling
tags: [verify-operator-docs, bash, access-codes, login-ux]
dependency-graph:
  requires: []
  provides: [verify-operator-docs-exit-code-fidelity, access-code-leak-check-accuracy]
  affects: [docs/operators, docs/ops, apps/auth/webapp login page]
tech-stack:
  added: []
  patterns:
    - "process substitution (while read < <(...)) instead of a trailing pipeline for loops that must mutate a variable in the current shell"
    - "node -e JSON parse in place of line-oriented sed extraction when a field needs correlating with a sibling field (expiresAt vs code)"
key-files:
  created: []
  modified:
    - scripts/verify-operator-docs.sh
    - "apps/auth/webapp/src/app/(authlogin)/login/page.tsx"
decisions:
  - "Kept the two SKIP: branches (kv-not-found, code-listing-read-failure) as direct printfs — they are informational, not failures, and must not drive the exit code or count against the zero-skip gate."
  - "A legitimately-empty active-code set (all codes retired) now prints an ok: line rather than nothing or a skip, so 'all codes retired' is a passing state distinct from 'could not read the listing'."
metrics:
  duration: "~35 min"
  completed: 2026-08-26
status: complete
---

# Phase 260826-ojd Plan 01: verify-operator-docs.sh bugfixes + login placeholder Summary

Fixed three defects in `scripts/verify-operator-docs.sh` (link-check exit code was structurally unreachable; access-code leak check false-positived on soft-expired and hyphen-adjacent codes) and refreshed one stale placeholder in the auth login page, watching each fix fail before it was applied.

## What Was Built

**Task 1 — link check now drives the exit code.** The inner `while read` loop was the tail of a `grep | sed | sort -u | while read` pipeline, so it ran in a subshell; any increment of `fails` inside it was discarded on subshell exit, and the failure was a bare `printf` rather than a call to `bad()` — so a broken relative link could never make the script exit non-zero, only print a line that scrolled by unnoticed. Fixed by (a) routing the failure through `bad()` and (b) moving the `grep`/`sed`/`sort -u` chain into a process substitution feeding the loop's stdin (`done < <(...)`), the same pattern the script already used for its phone-number loop. **Audit of every other status printf in the script found exactly 1 other direct-print failure path — the one just described. No others existed.** The two remaining direct printfs (kv-not-found, access-code-listing-read-failure) are the intentional `SKIP:` branches and were left untouched.

**Task 2 — access-code leak check: two independent defects, one block.**
- *Defect A (stale-forever false failures):* `kv code expire` sets `expiresAt` rather than deleting the row, so retired codes lingered in the listing forever and kept failing a check that should only assert no *current* credential leaked. The line-oriented `sed` extraction couldn't correlate a code with its own `expiresAt`, so it was replaced with a real `node -e` JSON parse (no `jq` added) that filters to codes whose `expiresAt` is absent (never-expiring, still checked) or in the future, drops expired ones, and exits non-zero only on unparseable/empty stdin so a genuine read failure (`SKIP:`) stays distinct from a legitimately-empty active set (`ok:`, not a skip — required by the zero-skip gate).
- *Defect B (word-boundary false positive):* `grep -rqiw` treats `-` as a word boundary, so the live code `demo` matched the tier name `demo-tier` in `docs/assets/terminal/code-tier-list.session`, where the code column was already correctly redacted to `<code-03>`. Replaced with an ERE pattern requiring the character before/after the code to be start/end-of-line or outside `[[:alnum:]_-]`, with the code's ERE metacharacters escaped before interpolation. Verified: `demo` no longer matches `demo-tier` or `kphdemo-tier`, but still matches bare `demo`, `demo.`, `"demo"`, `demo,`, and end-of-line `demo`.
- The JSON parse also incidentally fixes a second latent defect the plan flagged: the old `sed` was greedy against a compact single-line array and would only ever capture the *last* code in the listing.

**Task 3 — login placeholder + full gate.** `demo` → `demodemo2026` at `apps/auth/webapp/src/app/(authlogin)/login/page.tsx:224` (the `demo` code was soft-expired today, 2026-08-26T18:10:40Z; `demodemo2026` is its replacement on the same `demo-tier`/`conference` group). Placeholder-only change; the email input's placeholder is untouched. **This app is deployed separately (auth.klankermaker.ai) and will not show the new placeholder until the auth service is redeployed — this task does not deploy.**

**`scripts/verify-operator-docs.sh` is not wired into any CI workflow** — nothing currently gates on its exit code, which is exactly why the exit-code defect (Task 1) was able to survive undetected.

## Verification Results

Live listing at time of execution: **10 total access codes, 9 active** (1 retired — `demo`, soft-expired today).

| Test | Exit code observed | Result |
|---|---|---|
| Baseline (pre-fix) broken-link negative test | 0 (subshell swallowed the failure) — confirmed the defect before fixing | as expected, defect reproduced |
| Task 1 negative test (broken link, planted+reverted) | 2 (broken link + pre-existing Defect-B false positive, since Task 2 hadn't landed yet) | correctly non-zero, target named on FAIL line |
| Task 1 re-check after Task 2 landed (broken link, planted+reverted) | 1 (isolated to just the injected break) | correctly non-zero |
| Task 2 negative test (live code `wiggie`, 6 chars, planted+reverted) | 1, with the leak FAIL line present | correctly caught |
| Final clean-tree run | 0 | zero `FAIL:`, zero `SKIP:` |

`git status --porcelain docs/` was confirmed empty after every negative-test revert, before any commit.

All runs used `AWS_PROFILE=klanker-application AWS_REGION=us-east-1 KV=kv/bin/kv` — the `kv` on PATH is stale and produces 13 false failures; every run in this task-set was pinned.

## Deviations from Plan

None — plan executed exactly as written. The pre-verified facts (single offending failure path, JSON shape, hyphen-adjacency-only fix needed for Defect B, zero broken links in the clean tree) all held.

## Self-Check: PASSED

- `scripts/verify-operator-docs.sh` FOUND, contains both fixes (verified via final clean run: exit 0, 0 FAIL, 0 SKIP)
- `apps/auth/webapp/src/app/(authlogin)/login/page.tsx` FOUND, placeholder reads `demodemo2026`
- Commits `ccbd162`, `c902b98`, `5d2df1f` all present in `git log --oneline`
