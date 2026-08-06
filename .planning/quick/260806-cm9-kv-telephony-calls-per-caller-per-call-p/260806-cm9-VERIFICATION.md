---
phase: quick-260806-cm9
verified: 2026-08-06T17:10:00Z
status: passed
score: 7/7 must-haves verified
behavior_unverified: 0
overrides_applied: 0
---

# Quick Task 260806-cm9: `kv telephony calls` Verification Report

**Phase Goal:** New `kv telephony calls` subcommand — operator call report (who called
which number, when) from telephony-edge CloudWatch logs. Four views (per-caller rollup,
chronological per-call log, per-number rollup, new-caller/first-seen). Raw caller numbers
always shown. `kv telephony stats` must stay behaviorally byte-identical.

**Verified:** 2026-08-06
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Raw caller numbers always shown | ✓ VERIFIED | `CallRecord.Caller`/`CallerRollup.Caller` are plain string fields with no redaction path; `--json` and every table renderer emit them verbatim (`kv/internal/app/cmd/telephony_calls.go:319-349,776-804`); no `--mask`/`--identify` flag exists. Runtime-confirmed by orchestrator: `--view callers` returns 8 real callers with real numbers. |
| 2 | Event time derived from channel-id epoch / loguru stamp, never CloudWatch `@timestamp` | ✓ VERIFIED | Traced the full data path: `extractMessages` (telephony_stats.go:200-211) extracts **only** the `@message` field from Insights rows — `@timestamp` is discarded before any parser in `telephony_calls.go` ever sees a row. `grep` for `@timestamp`/`Timestamp` in `telephony_calls.go` shows it appears only in query-string text/comments, never read back. `JoinCallRecords` (telephony_calls.go:465-480) resolves `StartedAt` via `channelEpoch` → earliest `loguruTimestamp` → `"unknown"`, in that order, with no other source. Regression-tested by `TestJoinCallRecords_BatchedIngestionOrdersByChannelEpoch` (3 channels sharing one loguru stamp, ~18s/~29s apart by epoch, must sort by epoch not input/stamp order — passes) plus the two required-addition tests below. |
| 3 | Stasis-only call (no teardown) still appears, outcome rendered unknown | ✓ VERIFIED | `TestJoinCallRecords_StasisOnlyChannelHasNoTeardownAndIsPreResolution` passes; `HasTeardown=false` records render `(no teardown event)`/`-` duration (`printCallLogTable`, telephony_calls.go:791-802) and are counted in `Totals.WithoutTeardown`, never dropped. |
| 4 | Pre-resolution vs untagged-DID are two distinct, honestly-labelled buckets, never merged/invented | ✓ VERIFIED (one narrow, pre-flagged edge case noted) | Read controller.py directly: the identity line (1128) always exists; the dialed-DID line (1157, unconditionally logging `dialed_did={dialed_did or '<none>'}`) only exists in the post-~2026-07-17 CID-resolution code path. This is a genuine **structural/temporal** difference in the log stream (old deploy vs. new deploy), not a value-based heuristic — confirmed by `ResolutionSeen` being set purely from "was a `dialed_did=` key present on the line at all" (telephony_calls.go:142-149). `TestJoinCallRecords_NonePlaceholderIsUntaggedNotPreResolution` and `TestParseStasisLine_DialedDIDNonePlaceholder` both pass. See narrow gap noted below re: orphan-teardown-with-empty-`dialed_did`. |
| 5 | `UnicastRTP` external-media leg never counted as a call | ✓ VERIFIED | `parseStasisLine` returns `false` on `externalMediaMarker` match (telephony_calls.go:125-127); `TestParseStasisLine_ExternalMediaLegExcluded` passes; the three other keyless `on_stasis_start:` shapes (unexpected-context, media/bridge-failure, quota-denied) are independently confirmed excluded by the same single structural rule, each traced against its real controller.py line (1116-1123, 1179-1182, 1330-1332) and covered by `TestParseStasisLine_KeylessLinesExcluded`. |
| 6 | All four views reachable; `--json` emits every view's data regardless of `--view` | ✓ VERIFIED | `newTelephonyCallsCmd` wires `--view` to `callers/calls/numbers/new/all`, validated before any AWS call (`TestTelephonyCallsCmd_InvalidViewErrorsBeforeAWSCall`); `printTelephonyCalls`'s `asJSON` branch encodes the whole `report` independent of `view` (telephony_calls.go:719-725); `TestCallsJSON_AlwaysCarriesAllFourViewsRegardlessOfView` iterates all 5 `--view` values and asserts `Calls`/`Callers`/`Numbers`/`NewCallers` are all populated in each. Runtime-confirmed live (8 callers, 210 calls). |
| 7 | `kv telephony stats` byte-identical; only `Long` help gains one pointer line | ✓ VERIFIED | `git diff origin/main -- kv/internal/app/cmd/telephony_stats.go` shows a pure extract-method refactor (`RunInsightsQuery` → 2-line wrapper calling new `runInsightsQueryString`, byte-identical query string, pinned by `TestRunInsightsQuery_ExactQueryString`) plus exactly one appended `Long`-string sentence. No other line in `ParseCallEvents`, `AggregateCallStats`, `buildDIDStats`, `printTelephonyStats`, or any constant changed. Already independently confirmed at runtime by the orchestrator (`--json` and table output byte-identical against live AWS data over `--since 720h`). |

**Score:** 7/7 truths verified (0 present-but-behavior-unverified)

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `kv/internal/app/cmd/telephony_calls.go` | Full command: query, parsing, joining, report building, rendering, registration | ✓ VERIFIED | 917 lines, all sections present and read in full; matches plan section-by-section. |
| `kv/internal/app/cmd/telephony_calls_test.go` | Full test coverage | ✓ VERIFIED | 834 lines; covers every behavior in the plan plus the plan-checker's required addition. |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `on_stasis_start`'s `channel=` | `game_call_event`'s `call_id` | ARI channel id (join key) | ✓ WIRED | Confirmed exact by direct read of controller.py (every `build_call_event`/teardown call site passes `dialed_did=`/uses `sip_channel_id`); `JoinCallRecords` keys its accumulator map on this exact string from both parse paths (telephony_calls.go:419-455). `TestJoinCallRecords_TwoStasisLinesAndTeardownCollapseToOneRecord` proves the collapse. |
| `extractMessages` | `@timestamp` unreachability | Structural (only `@message` extracted) | ✓ WIRED | Read `extractMessages` (telephony_stats.go:198-211) directly — loops rows, only keeps the field named `@message`. Reused verbatim by `RunCallsInsightsQuery` via `runInsightsQueryString`; no other extraction path exists. |
| `RunInsightsQuery` | `runInsightsQueryString` | Extract-method delegation | ✓ WIRED | `RunInsightsQuery` (telephony_stats.go:135-147) builds the identical query string and returns `runInsightsQueryString(...)`. `TestRunInsightsQuery_ExactQueryString` pins the exact string. |
| `parseTOMLScalarLine` (telephony.go) | `parseCIDPrefixDIDs` (telephony_calls.go) | Verbatim reuse, key additionally unquoted | ✓ WIRED | `parseCIDPrefixDIDs` calls `parseTOMLScalarLine(line)` then trims quotes off the key itself (telephony_calls.go:280-288); no new TOML dependency (`kv/go.mod` unchanged, confirmed via git diff — not in the changed-files list). |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Full build | `go -C kv build ./...` | exit 0 | ✓ PASS |
| Vet | `go -C kv vet ./...` | exit 0, no output | ✓ PASS |
| Full test suite | `go -C kv test ./...` | all packages `ok` | ✓ PASS |
| `stats` byte-identical against live AWS | (orchestrator, pre-verified) | identical `--json` and table output, `--since 720h` | ✓ PASS (not re-run, per instructions) |
| Live four-view sanity | (orchestrator, pre-verified) | 8 callers, 210 calls, led by 5197101515/160 | ✓ PASS (not re-run, per instructions) |

### Anti-Patterns Found

None. `grep` for `TBD|FIXME|XXX|TODO|HACK|PLACEHOLDER` and empty-return/console-log stub patterns across all 6 changed files found no debt markers or stubs — the only "placeholder" hits are legitimate references to the `<none>` log token (`noneToken`), not code stubs.

### `.gitignore` Deviation Review

Confirmed correct and scoped:
- The removed line was a bare `kv` (no slash), which git's ignore-pattern matching treats as `**/kv` — this silently matched and ignored the entire `kv/` source directory (added inadvertently in `3f8933b`, "-skip temp and binary").
- `kv/bin/` (line 29) and `kv/bin/kv` remain and still cover the built operator binary — verified `git check-ignore -v kv/bin/kv` and `kv/bin/somefile` both still match `kv/bin/`.
- `git status --porcelain --ignored=matching kv/` shows only `kv/bin/` as ignored; no source file that should be ignored is now tracked.
- `git ls-files kv/` minus `.go`/`go.mod`/`go.sum`/`Makefile`/`.md` turned up only pre-existing tracked files (studio testdata golden files, studio web assets) that predate this branch — nothing unintended was newly committed. `git show --stat` on all three commits confirms only the intended files changed.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|--------------|--------|----------|
| QUICK-260806-cm9 | 260806-cm9-PLAN.md | `kv telephony calls` operator report | ✓ SATISFIED | All 7 must-have truths verified above. |

### Noted Gap (not a blocker)

**Narrow mislabeling edge case in the pre-resolution/untagged distinction.** `JoinCallRecords`
only sets `resolutionSeen=true` from a teardown event when that event's `DialedDID` is
**non-empty** (telephony_calls.go:451-454, matching the plan's literal wording: "A teardown
event with a non-empty `DialedDID` also implies `resolutionSeen = true`"). controller.py's
`dialed_did` variable can legitimately be an empty string for a resolution-era call that
failed to resolve (confirmed by reading controller.py:1153 — `_dialed_did_from_cidname(...)
or _dialed_did_from_sip_to(...)`, which can yield `""`), and that empty string is passed
straight through to every `build_call_event` call site. So an **orphan teardown record**
(a channel whose `on_stasis_start` lines were never captured by the query — e.g. truncated
out by `callsQueryLimit`) whose teardown JSON carries `dialed_did=""` would be mislabeled
`unknown (pre-resolution)` instead of the correct `concierge (untagged DIDs)`.

This is real but narrow: it requires BOTH an orphan teardown (no stasis line for that
channel at all, in a query window where `callsQueryLimit=20000` vs. ~200 actual log lines
makes truncation implausible in practice) AND an unresolved DID on that specific call. It
was pre-flagged, unprompted, by the executor in the SUMMARY's "Decisions Made" section
("flagging it here in case a future operator expects orphan-teardown-with-empty-DID to
read as untagged instead"), and the record itself is never dropped — only its DID-bucket
label could be off by one category in this narrow combination. Not counted as a gap against
the must-have (the structural distinction holds for the overwhelming common case, which is
what the CONTEXT's own worked example and live sanity check exercise), but worth an
operator's awareness if a future `--view numbers` count for `unknown (pre-resolution)`
looks off by a small amount.

## Gaps Summary

No blocking gaps. The one edge case above is pre-flagged, narrow, non-data-losing, and does
not require a closure plan — it's a documentation/awareness item, not a functional defect
against this task's must-haves.

---
_Verified: 2026-08-06T17:10:00Z_
_Verifier: Claude (gsd-verifier)_
