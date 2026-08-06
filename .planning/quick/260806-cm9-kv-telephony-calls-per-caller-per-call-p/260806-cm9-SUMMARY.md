---
phase: quick-260806-cm9
plan: 01
subsystem: telephony
tags: [cobra, aws-sdk-go-v2, cloudwatch-logs-insights, loguru, go, operator-tooling]

requires:
  - phase: quick-260727-v5e
    provides: "game_call_event teardown telemetry + kv telephony stats' RunInsightsQuery/extractMessages/ParseCallEvents seams this task extends and reuses"
provides:
  - "`kv telephony calls` -- an identity-bearing operator call report joining on_stasis_start log lines with game_call_event teardown lines by ARI channel id"
  - "runInsightsQueryString -- the extracted Insights poll lifecycle shared by RunInsightsQuery (stats) and RunCallsInsightsQuery (calls)"
  - "channel-id-epoch / loguru-timestamp event-time derivation that never touches CloudWatch's batched @timestamp"
affects: [telephony, kv-cli, operator-tooling]

tech-stack:
  added: []
  patterns:
    - "extract-method refactor with an exact-string regression test pinning the unchanged caller's behavior (RunInsightsQuery -> runInsightsQueryString)"
    - "three-rung TimeSource fallback chain (channel-id epoch -> earliest loguru timestamp -> unknown), with the zero time deliberately used as an 'unknown sorts first' sentinel"
    - "two distinct, honestly-labelled 'unknown DID' buckets (pre-resolution vs. resolution-era-but-untagged) instead of merging them or inventing a number"

key-files:
  created:
    - kv/internal/app/cmd/telephony_calls.go
    - kv/internal/app/cmd/telephony_calls_test.go
  modified:
    - kv/internal/app/cmd/telephony_stats.go
    - kv/internal/app/cmd/telephony_stats_test.go
    - kv/internal/app/cmd/telephony.go
    - .gitignore

key-decisions:
  - "RunInsightsQuery's poll lifecycle was extracted into unexported runInsightsQueryString(..., queryString) so RunCallsInsightsQuery could reuse it with a different filter; RunInsightsQuery itself is now a two-line wrapper building the byte-identical query string it always built, pinned by TestRunInsightsQuery_ExactQueryString."
  - "parseStasisLine excludes lines by a single structural rule -- channel non-empty AND (caller present OR a dialed_did key was present at all) -- rather than a hand-maintained blocklist of the three keyless on_stasis_start shapes (unexpected-context warning, media/bridge failure, quota denied)."
  - "ResolutionSeen tracks whether a dialed_did key was present on a line at all (even when its value is the <none> placeholder), which is what lets an empty-but-resolved DID land in the untaggedDIDLabel bucket instead of being conflated with a genuinely pre-resolution call."
  - "JoinCallRecords derives StartedAt via channelEpoch first, then the earliest loguruTimestamp seen on that channel's own lines, then leaves it as the zero time -- never the CloudWatch @timestamp, which extractMessages structurally cannot even surface to this file's parsers."
  - "parseCIDPrefixDIDs inverts [telephony.cid_prefix_dids] to DID->tag (the TOML itself is tag->DID) since the report groups by DID; it trims quotes off the TOML key itself, which the existing parseTOMLScalarLine helper does not do (that helper only unquotes the value)."

requirements-completed: [QUICK-260806-cm9]

duration: ~65min
completed: 2026-08-06
status: complete
---

# Quick Task 260806-cm9: `kv telephony calls` Summary

**A new `kv telephony calls` CLI subcommand that joins telephony-edge's `on_stasis_start` and `game_call_event` CloudWatch log lines by ARI channel id into per-call records, and renders four views (per-caller rollup, chronological call log, per-number rollup, new-caller alerts) with raw caller numbers always shown -- the identity-bearing sibling of the caller-anonymous `kv telephony stats`.**

## Performance

- **Tasks:** 3/3 completed
- **Files modified:** 6 (2 new, 4 modified)

## Accomplishments

- **Task 1 -- query seam + line parsing.** Extracted `RunInsightsQuery`'s Insights poll lifecycle into `runInsightsQueryString`, added `RunCallsInsightsQuery` (matching both the `on_stasis_start:` and `game_call_event` line families), `parseStasisLine` (both real controller.py shapes, excluding the `UnicastRTP` external-media leg and the three keyless lines), `channelEpoch` and `loguruTimestamp` (both range/format-bounded event-time sources), and `parseCIDPrefixDIDs` (reuses `telephony.go`'s hand-rolled `parseTOMLScalarLine`, no new TOML dependency).
- **Task 2 -- joining + four views.** `JoinCallRecords` merges both stasis shapes and the existing `ParseCallEvents` teardown decoder by channel id, derives each record's time via the channel-id-epoch -> loguru-timestamp -> unknown chain, and labels the DID via `didLabel` (tagged / bare digits / `concierge (untagged DIDs)` / `unknown (pre-resolution)`). `BuildCallsReport` groups joined records into the per-caller, per-number, and new-caller views with consistent DID/caller filtering.
- **Task 3 -- command + rendering.** `newTelephonyCallsCmd` registers `kv telephony calls` with all eight flags; `--view` is validated against the allowed set before any AWS call; `printTelephonyCalls` renders each view as a `tabwriter` table (or all four for `--view=all`), and `--json` always emits the complete four-view result set regardless of `--view`. `kv telephony stats`' `Long` help gained one pointer line to the new sibling; its report output is otherwise byte-identical (guarded by the untouched `TestPrintTelephonyStats_NeverRendersCallerNumber` and the new exact-string query regression test).

## Task Commits

Each task was committed atomically:

1. **Task 1: Parameterize the Insights query seam and parse the raw log-line shapes** - `a2dc784` (feat)
2. **Task 2: Join the two log sources into call records and build all four views** - `7302e77` (feat)
3. **Task 3: Wire up the `kv telephony calls` command, its four renderers, and JSON** - `e1d6508` (feat)

_Note: this plan was not TDD-gated (autonomous type="auto" tasks); tests were written alongside each task's implementation and verified green before commit, per the plan's own `tdd="true"` behavior lists._

## Files Created/Modified

- `kv/internal/app/cmd/telephony_calls.go` - the whole `calls` subcommand: query, parsing, joining, report building, rendering, command registration
- `kv/internal/app/cmd/telephony_calls_test.go` - full test coverage for all of the above
- `kv/internal/app/cmd/telephony_stats.go` - extract-method refactor (`runInsightsQueryString`) + one added `Long` help line; `RunInsightsQuery`'s behavior is unchanged
- `kv/internal/app/cmd/telephony_stats_test.go` - purely additive `lastQueryString` capture field on the existing fake client
- `kv/internal/app/cmd/telephony.go` - registers `newTelephonyCallsCmd` next to the existing `stats` registration
- `.gitignore` - see Deviations (Rule 3 blocking-issue fix)

## Decisions Made

- **`ResolutionSeen` as a first-class flag, not an empty-string check.** A `dialed_did` key with the `<none>` placeholder still counts as "resolution attempted" -- this is exactly what lets a resolution-era untagged call (`concierge (untagged DIDs)`) stay distinct from a genuinely pre-resolution call (`unknown (pre-resolution)`), matching the CONTEXT's explicit "two different unknowns, never merge them" requirement.
- **Unknown-time records sort first via the zero `time.Time` value itself**, rather than a separate sort key -- `time.Time{}` is earlier than any real timestamp, so the existing ascending-`StartedAt` sort already produces the desired "unknown rows first" behavior with no special-casing.
- **A teardown event's own `dialed_did` sets `ResolutionSeen=true` only when non-empty** (per the plan's literal wording) -- an orphan teardown record with an empty `dialed_did` therefore lands in the pre-resolution bucket rather than untagged, even though the teardown telemetry itself postdates the CID-resolution deploy. This follows the plan's action text exactly as written; flagging it here in case a future operator expects orphan-teardown-with-empty-DID to read as untagged instead.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Removed a stray `.gitignore` pattern that blanket-ignored the entire `kv/` source tree**
- **Found during:** Task 1, attempting to stage the new `telephony_calls.go`/`telephony_calls_test.go` files
- **Issue:** The prior commit (`3f8933b`, "-skip temp and binary") added a bare `kv` line to `.gitignore` alongside `kv/bin/` and `kv/bin/kv`. Git's bare-pattern matching treats `kv` (no slash) as `**/kv`, which matches the top-level `kv/` directory itself -- silently gitignoring every new file anywhere under `kv/`, including new Go source. `git status`/`git add` gave no error, just silently omitted the new files (confirmed via `git check-ignore -v`).
- **Fix:** Removed the bare `kv` line. `kv/bin/` (line 29) and `kv/bin/kv` (kept) already cover the built operator binary, which was the stated intent of that commit.
- **Files modified:** `.gitignore`
- **Verification:** `git check-ignore -v kv/internal/app/cmd/telephony_calls.go` now exits 1 (not ignored); `git status --short` shows the new files as untracked/staged as expected.
- **Committed in:** `a2dc784` (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (1 blocking)
**Impact on plan:** Necessary to complete Task 1 at all -- without it, no new file under `kv/` could ever be committed by any future task either. No scope creep beyond the one-line gitignore fix.

## Issues Encountered

None beyond the deviation above.

## Required Addition (plan-checker follow-up)

Per the orchestrator's instructions, Task 2 was extended beyond the written plan with dedicated coverage for the middle rung of the `TimeSource` fallback chain, which neither Task 1's isolated `loguruTimestamp()` tests nor the channel-epoch-primary-path tests exercised:

- `TestJoinCallRecords_LoguruTimestampFallbackRung` -- a channel id `channelEpoch` rejects (`"bad.1"`), with two lines carrying different loguru stamps; asserts `TimeSource == "log-timestamp"` and that `StartedAt` is the **earliest** of the two stamps for that channel.
- `TestJoinCallRecords_AllRungsFailSortsFirst` -- a channel with neither a plausible epoch nor any loguru-prefixed line; asserts `TimeSource == "unknown"`, `StartedAt` is the zero time, and the record sorts first among the joined set.

Both are in `kv/internal/app/cmd/telephony_calls_test.go`, committed as part of Task 2 (`7302e77`).

## Next Phase Readiness

- `kv telephony calls` is fully wired, tested, and ready for operator use. No AWS credentials were available in this worktree, so the plan's optional live sanity check (`--since 720h --view callers` should show 8 distinct callers led by `+15197101515`; `--view numbers` should show `unknown (pre-resolution)` as the largest bucket) was **not** run -- the operator should run it once against real telephony-edge logs to confirm the parsers hold up against the full live corpus.
- No blockers for merge. `go build ./...`, `go vet ./...`, and `go test ./...` are all green across `kv/`.

## Self-Check: PASSED

- `kv/internal/app/cmd/telephony_calls.go` -- FOUND
- `kv/internal/app/cmd/telephony_calls_test.go` -- FOUND
- `kv/internal/app/cmd/telephony_stats.go` -- FOUND (modified)
- `kv/internal/app/cmd/telephony_stats_test.go` -- FOUND (modified)
- `kv/internal/app/cmd/telephony.go` -- FOUND (modified)
- `.gitignore` -- FOUND (modified)
- Commit `a2dc784` (Task 1) -- FOUND in `git log --oneline --all`
- Commit `7302e77` (Task 2) -- FOUND in `git log --oneline --all`
- Commit `e1d6508` (Task 3) -- FOUND in `git log --oneline --all`
- `go build ./...`, `go vet ./...`, `go test ./...` -- all green as of the final Task 3 commit

---
*Phase: quick-260806-cm9*
*Completed: 2026-08-06*
