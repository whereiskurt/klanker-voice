---
phase: quick-260727-v5e
plan: 01
subsystem: telephony
tags: [pipecat, loguru, cloudwatch-logs-insights, cobra, aws-sdk-go-v2, telemetry]

requires:
  - phase: 11-voip-ms-telephony-local-asterisk-edge
    provides: "the §24 GateProcessor / AsteriskCallController teardown seams this task instruments"
provides:
  - "one `game_call_event` structured log line per telephony call, emitted at teardown"
  - "`kv telephony stats` -- a CloudWatch Logs Insights-backed per-DID call summary CLI"
affects: [telephony, kv-cli, operator-tooling]

tech-stack:
  added: [aws-sdk-go-v2/service/cloudwatchlogs]
  patterns:
    - "D-05e redaction-by-construction: a keyword-only payload builder whose parameter list admits only counts/labels/timings, never raw transcript/digit/code values"
    - "first-wins outcome recording on a per-call mutable record, read by the single teardown emission seam as a fallback-only source"

key-files:
  created:
    - apps/voice/src/klanker_voice/telephony/call_event.py
    - kv/internal/app/cmd/telephony_stats.go
    - kv/internal/app/cmd/telephony_stats_test.go
  modified:
    - apps/voice/src/klanker_voice/telephony/gate.py
    - apps/voice/src/klanker_voice/telephony/controller.py
    - apps/voice/tests/test_telephony_gate.py
    - apps/voice/tests/test_telephony_controller.py
    - apps/voice/tests/test_telephony_lifecycle.py
    - kv/internal/app/cmd/telephony.go
    - kv/go.mod
    - kv/go.sum

key-decisions:
  - "Marker literal is `game_call_event` exactly, defined once in Python (CALL_EVENT_MARKER) and once in Go (callEventMarker), bound by a Go-side test that reads the Python source file directly."
  - "The require_gate=False dev/test-only escape hatch gets its own outcome label, `ungated_grant`, purely so that path also satisfies 'exactly one line per call' -- production never emits it."
  - "A quota rejection discovered right after gate unlock, and one on the ungated path, both map to outcome=`error` (Claude's discretion, CONTEXT-approved) -- neither is a window timeout nor a successful factor."
  - "The announcement outcome is stamped when the DTMF/spoken TRIGGER fires (the caller got through), not after the OTP fetch succeeds -- a later fetch failure still reads as announcement_code/announcement_words, which is the 'did they get through the code entry' signal the operator asked for."
  - "Aggregation for `kv telephony stats` is client-side in Go over a filtered Insights result set (event volume is a few hundred calls) rather than a clever Insights `stats` query -- CONTEXT's own discretion, favoring simple/robust."

requirements-completed: [QUICK-260727-V5E]

duration: ~50min
completed: 2026-07-27
status: complete
---

# Quick Task 260727-v5e: Game-call telemetry Summary

**One structured `game_call_event` log line per telephony call at teardown, plus a `kv telephony stats` CLI that turns those lines into a per-DID CloudWatch Logs Insights summary.**

## Performance

- **Tasks:** 2/2 completed
- **Files modified:** 12 (3 new, 9 modified)

## Accomplishments

- Every telephony call -- concierge or game, gated or ungated -- now emits exactly one `game_call_event` line at teardown, on every path: normal hangup, early hangup, gate timeout, announcement complete, and the pre-registration allocation/quota-denied error path.
- The emitted line is redaction-safe by construction: the builder function's own parameter list has no slot through which an entered digit sequence, a heard/matched word, or a code value could ever enter the payload -- proven by a dedicated wrong-code redaction test.
- `kv telephony stats` gives the operator a one-liner during the event: per-DID call volume, outcome breakdown, code-entry timing, and distinct-caller counts, with zero raw caller numbers in either output mode.

## 1. The emitted line shape

Every event is one line: the marker, one space, then compact JSON with exactly nine fields, in this order:

```
game_call_event {"call_id":...,"dialed_did":...,"caller_id":...,"otp_only":...,"outcome":...,"digits_entered":...,"words_heard":...,"seconds_to_outcome":...,"duration_seconds":...}
```

**Why it's redaction-safe by construction (D-05):** the Python builder function (`build_call_event`) is keyword-only and accepts ONLY ints, floats, bools, and a small set of pre-approved label/identifier strings (`call_id`, `dialed_did`, `caller_id`, `outcome`) -- there is no parameter through which a caller's raw DTMF digits, a heard or matched spoken word, or any code value could enter the payload. `digits_entered` and `words_heard` are plain counters/counts, never the underlying sequence or token set.

## 2. Outcome-label table

| Label | Recorded by | Site |
|---|---|---|
| `concierge_unlock_dtmf` | Real DTMF PIN unlock | `_gate_unlock`, reading `GateProcessor.unlock_method` |
| `concierge_unlock_passphrase` | Real spoken-passphrase unlock | `_gate_unlock`, reading `GateProcessor.unlock_method` |
| `gate_timeout` | Gate-window expiry, or a caller-ID mint failure | `_gate_fail_closed`, via the `GATE_FAIL_OUTCOMES` reason map |
| `announcement_code` | A DTMF-code game trigger fires | `on_channel_dtmf_received`, immediately before dispatch |
| `announcement_words` | A spoken-words game trigger fires | the gate's `_on_announcement_words` closure, immediately before dispatch |
| `early_hangup` | Caller hangs up before any resolution | `_close_active_call`'s fallback label (never stamped as a real outcome) |
| `error` | Media/bridge allocation failure, pre-registration quota denial, or a quota rejection discovered right after gate unlock | `_teardown_gate_resources` (its own default), or `_gate_fail_closed` via the reason map |
| `ungated_grant` | The `require_gate=False` dev/test-only escape hatch | `_close_active_call`'s fallback label when `active_call.gate is None` |

Two discretionary calls (both CONTEXT-approved, recorded above): `ungated_grant` exists purely so the dev-only escape hatch still satisfies "one line per call"; a quota rejection (either post-unlock or on the ungated pre-registration path) maps to `error` rather than inventing a ninth label.

## 3. The two emission seams

- **`_close_active_call`** is the ONE emission seam for every call that ever registered an `ActiveCall` -- the method every close trigger (`ChannelDestroyed`, a hard-timeout release, a fail-closed goodbye, an announcement's own teardown) already funnels through. It is already idempotent: a synchronous check-and-set of `active_call.closed` under `active_call.lock` runs first, so emitting immediately after that flag flip gives exactly-once telemetry for free, including a simultaneous hangup + hard-timeout race -- proven by extending the existing race test to also assert exactly one marker line.
- **`_teardown_gate_resources`** is the OTHER emission seam, for the only teardown path `_close_active_call` can never see: a call whose bridge/external-media allocation failed, or whose ungated pre-registration quota check was denied, before any `ActiveCall` was ever constructed.

## 4. The marker's dual definition

- Python: `CALL_EVENT_MARKER = "game_call_event"` in `apps/voice/src/klanker_voice/telephony/call_event.py`.
- Go: `callEventMarker = "game_call_event"` in `kv/internal/app/cmd/telephony_stats.go`.
- Drift guard: `TestCallEventMarkerMatchesPython` (`kv/internal/app/cmd/telephony_stats_test.go`) reads the Python source file directly and asserts it contains the Go constant's value as the `CALL_EVENT_MARKER` assignment -- renaming the marker on either side fails the Go build's test suite.

## 5. Operator recipe

During the event, from the `kv/` directory:

```
kv telephony stats --since 2h --did <the-dialed-DID>
```

Omit `--did` for every DID at once; add `--json` for scriptable output. `--since` defaults to `24h`. The event line only starts appearing in CloudWatch once the current code rides the NEXT normal telephony-edge deploy -- there is no separate rollout step for the line itself.

**No pending operator setup:** no SSM seeding, no terraform apply, and no IAM change are required by this task. `kv telephony stats` reads CloudWatch Logs with the operator's existing SSO/env credentials via the same `loadAWS` path every other `kv` command already uses.

## Task Commits

1. **Task 1: Emit one D-05e-safe `game_call_event` line per call at teardown** - `9ffc6c7` (feat)
2. **Task 2: `kv telephony stats` -- Insights-backed per-DID call summary** - `74df6bd` (feat)

## Files Created/Modified

- `apps/voice/src/klanker_voice/telephony/call_event.py` - the D-02/D-03 payload builder/emitter (new)
- `apps/voice/src/klanker_voice/telephony/gate.py` - `GateProcessor.unlock_method` / `.token_count` read-only views
- `apps/voice/src/klanker_voice/telephony/controller.py` - `ActiveCall` telemetry fields, `_record_outcome`, the two emission call sites
- `apps/voice/tests/test_telephony_gate.py` - builder/clock-helper tests + the two new GateProcessor view tests
- `apps/voice/tests/test_telephony_controller.py` - game-outcome tests + the D-05 wrong-code redaction proof
- `apps/voice/tests/test_telephony_lifecycle.py` - teardown-path coverage across all documented outcomes
- `kv/internal/app/cmd/telephony_stats.go` - `kv telephony stats` (new)
- `kv/internal/app/cmd/telephony_stats_test.go` - 10 offline tests (new)
- `kv/internal/app/cmd/telephony.go` - registers the new subcommand
- `kv/go.mod`, `kv/go.sum` - adds `aws-sdk-go-v2/service/cloudwatchlogs`

## Decisions Made

See `key-decisions` in frontmatter above.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] `_teardown_gate_resources` has two call sites, not three**

- **Found during:** Task 1
- **Issue:** the plan's action text says "Update its three call sites to pass the values they already hold in scope," but `_teardown_gate_resources` is only ever called from two places in `controller.py`: the shared media/bridge allocation-failure branch in `on_stasis_start` (which runs before the gated/ungated branch, so it already covers "both gate modes" as one code path) and the ungated quota-denial branch in `_finish_stasis_start_ungated`. There is no third call site -- a gated-flow quota denial routes through `_gate_fail_closed` instead, since by that point an `ActiveCall` is already registered.
- **Fix:** updated both actual call sites (not three) with the documented kwargs (`caller_id`, `dialed_did`, `answered_at`); left the method's own default `outcome="error"` to cover both, matching the plan's own instruction that "both call sites... use `outcome="error"`."
- **Files modified:** `apps/voice/src/klanker_voice/telephony/controller.py`
- **Verification:** Test 15 (`test_allocation_failure_emits_one_error_event_no_registered_call`) exercises the allocation-failure site; the existing `test_quota_denied_leaves_no_bridge`-style coverage plus the ungated path's own teardown behavior exercises the other.
- **Committed in:** `9ffc6c7` (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 -- a plan-text/reality mismatch, not a functional gap; both real call sites are wired).
**Impact on plan:** None on scope or correctness -- every teardown path documented in `<behavior>`/`<done>` is covered and tested.

## Issues Encountered

- The Python full-suite run (`uv run pytest -q`, the plan's own broader verification step) surfaces 4 failed + 23 errored pre-existing tests in `tests/test_quota.py`, `tests/test_session.py`, and `tests/test_slot_leak.py` -- all `botocore.errorfactory.ResourceNotFoundException: Cannot do operations on a non-existent table`, an environment/fixture issue (a DynamoDB-local table not provisioned in this sandbox) in files this task never touches. Confirmed pre-existing by running those three files in isolation with identical failures. Out of scope per the deviation rules' scope boundary; not auto-fixed.

## User Setup Required

None - no external service configuration required. See "No pending operator setup" in item 5 above.

## Next Phase Readiness

- The event line rides the next normal telephony-edge deploy; no separate ship step.
- `kv telephony stats` is usable locally today against the operator's existing SSO credentials, once at least one call has produced a `game_call_event` line in the deployed log group.
- No blockers identified for this task's own scope. Out of scope by design (CONTEXT D-10): capacity/concurrency changes, a `kv studio` panel, CloudWatch dashboards/metrics/EMF, and any change to `gate_debug_log_heard` or the transcript ledger.

---
*Phase: quick-260727-v5e*
*Completed: 2026-07-27*

## Self-Check: PASSED

All created/modified files verified present on disk; both task commit hashes (`9ffc6c7`, `74df6bd`) verified present in git log.
