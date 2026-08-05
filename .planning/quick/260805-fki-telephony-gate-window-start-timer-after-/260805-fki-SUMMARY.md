---
phase: quick-260805-fki
plan: 01
subsystem: telephony
tags: [telephony, gate, pipecat, toml, observability]

requires: []
provides:
  - "GateProcessor deadline-based fail-closed timer with a bounded, one-shot defer_for_cue rebase"
  - "pickup_cue.pickup_cue_duration_seconds() -- the computed ring+hey cue duration input to the defer"
  - "TelephonyConfig.gate_cue_lead_max_seconds (default 8.0) and gate_debug_log_dtmf (default False) knobs"
  - "gate_window_seconds 8 -> 10 in the shipped configs/telephony.toml, cue-relative"
  - "Redaction-safe opt-in DTMF-arrival debug logging in on_channel_dtmf_received"
affects: [telephony-controller, telephony-gate, telephony-config]

tech-stack:
  added: []
  patterns:
    - "Monotonic-deadline timer loop (loop.time()-based) instead of a fixed asyncio.sleep, so a running timer's fire point can be rebased mid-flight"
    - "One-shot, clamped 'defer' seam (defer_for_cue) layered on top of an unconditional backstop timer -- the backstop can only ever be bounded-extended, never removed or delayed"

key-files:
  created: []
  modified:
    - apps/voice/src/klanker_voice/telephony/gate.py
    - apps/voice/src/klanker_voice/telephony/pickup_cue.py
    - apps/voice/src/klanker_voice/telephony/config.py
    - apps/voice/src/klanker_voice/telephony/controller.py
    - apps/voice/configs/telephony.toml
    - apps/voice/tests/test_telephony_gate.py
    - apps/voice/tests/test_pickup_cue_player.py
    - apps/voice/tests/test_telephony_config.py
    - apps/voice/tests/test_telephony_controller.py

key-decisions:
  - "Cue-relative timer start is implemented as a REBASE of an already-started, unconditional timer (defer_for_cue), not by deferring timer creation -- the fail-closed guarantee is a plain arithmetic invariant (deadline can only move forward, capped) rather than depending on the cue seam ever firing."
  - "seconds_to_outcome in game_call_event stays ANSWER-relative, not rebased to the cue -- see the dedicated section below."
  - "gate_cue_lead_max_seconds defaults to 8.0 (comfortably covers the measured 4.265s cue plus media-setup slack); worst-case fail-closed bound is now gate_window_seconds(10) + gate_cue_lead_max_seconds(8.0) = 18s from timer start on ANY path."
  - "DTMF arrival logging is placed BEFORE every existing guard in on_channel_dtmf_received (a pure hoist of the existing channel_id/digit parsing + lookup, not a restructure) so it is visible for an unknown channel and for a gate_mode that excludes DTMF -- precisely the two invisible cases the flag exists to diagnose."

requirements-completed: [QT-260805-fki]

metrics:
  duration: ~70min
  completed: 2026-08-05

status: complete
---

# Quick Task 260805-fki: Telephony gate window — cue-relative timer, 10s, DTMF arrival logging — Summary

**Fail-closed gate timer rebased to fire relative to the end of the ring+hey pickup cue (via a bounded, one-shot `defer_for_cue`), `gate_window_seconds` raised 8→10, and an opt-in DTMF-arrival debug line added — giving a real ~10s post-cue dialing budget instead of today's ~4s, with a strictly stronger (not weaker) fail-closed guarantee.**

## Accomplishments

- `GateProcessor`'s fail-closed timer converted from a fixed `asyncio.sleep` to a monotonic-deadline loop. New `defer_for_cue(cue_seconds)` rebases the deadline forward by a clamped, one-shot lead — `start_timer()` stays the unconditional backstop at both of its real call sites (`_finish_stasis_start_gated`, and the pipeline's first `StartFrame`).
- New `pickup_cue.pickup_cue_duration_seconds()` computes the ring+hey cue's wall-clock duration (there is no completion signal for the queued frames) — this is the value the controller passes into `defer_for_cue`.
- Two new `TelephonyConfig` knobs: `gate_cue_lead_max_seconds` (default 8.0, the D-05d safety cap) and `gate_debug_log_dtmf` (default False, opt-in arrival-only DTMF debug logging).
- `configs/telephony.toml`: `gate_window_seconds` 8 → 10, with the inline history comment extended to record the 260805 live-telemetry evidence and the cue-relative rationale; both new knobs added beneath `gate_debug_log_heard`.
- `_register_pickup_cue` now accepts an optional `gate` kwarg; on the gated path, `on_client_connected` calls `gate.defer_for_cue(pickup_cue_duration_seconds())` immediately before queuing the cue. The ungated path is untouched (no `GateProcessor` exists there).
- `on_channel_dtmf_received`: `channel_id`/`digit` parsing and the `self.calls.get(...)` lookup hoisted above every guard (a pure hoist — every existing guard/branch keeps its exact order and semantics after that point). With `gate_debug_log_dtmf=true`, emits one `dtmf_received{call_id, caller_id, arrival_count, tracked}` INFO line per ARI event — even for an unknown channel or a `gate_mode` that excludes DTMF.
- New `ActiveCall.dtmf_arrivals` counter (distinct from `dtmf_count`) feeds only the debug line; `call_event.py` is untouched.

## Task Commits

Each task was committed atomically:

1. **Task 1: Deadline-based gate timer + bounded cue defer + a cue-duration helper** — `724ecb0` (feat)
2. **Task 2: Config knobs + telephony.toml 8 → 10 with documented history** — `0b19cdd` (feat)
3. **Task 3: Controller wiring — cue defer at the pickup-cue seam + redaction-safe DTMF arrival logging** — `1531248` (feat)

**Plan metadata:** `31d4b89` (docs: telephony gate window plan, committed before execution)

## Files Created/Modified

- `apps/voice/src/klanker_voice/telephony/gate.py` — deadline-based timer, `defer_for_cue`, `max_cue_lead_seconds` param
- `apps/voice/src/klanker_voice/telephony/pickup_cue.py` — `pickup_cue_duration_seconds()`
- `apps/voice/src/klanker_voice/telephony/config.py` — `gate_cue_lead_max_seconds`, `gate_debug_log_dtmf` fields + parsing
- `apps/voice/src/klanker_voice/telephony/controller.py` — `_register_pickup_cue(gate=...)`, `ActiveCall.dtmf_arrivals`, `on_channel_dtmf_received` hoist + debug log
- `apps/voice/configs/telephony.toml` — `gate_window_seconds` 8→10 + history, two new knobs
- `apps/voice/tests/test_telephony_gate.py` — 9 new deadline/defer/clamp/order/no-op tests (57 total, was 48)
- `apps/voice/tests/test_pickup_cue_player.py` — 3 new `pickup_cue_duration_seconds` tests
- `apps/voice/tests/test_telephony_config.py` — 5 new config tests (defaults, parses, shipped-toml)
- `apps/voice/tests/test_telephony_controller.py` — 12 new tests (cue-defer wiring, D-05d backstop x2, DTMF debug logging x7, plus a misplaced pre-existing assertion line restored to its original test during editing — see Deviations)

## Decisions Made

### `seconds_to_outcome` stays ANSWER-relative (REQUIRED explicit statement)

`seconds_to_outcome` in `game_call_event` was **NOT** rebased to the pickup cue — it still reads `active_call.answered_at` unchanged, exactly as before this task. Rebasing it to the cue would have silently invalidated every timing number already in the CONTEXT evidence table and every past `kv telephony stats` comparison (median 6.0s, max 12.1s, the 8.1s external-timeout fingerprint, etc.) — those are all answer-relative today. Keeping it answer-relative means those historical numbers stay directly comparable to future runs. An operator who wants a cue-relative figure can recover one by subtracting the measured cue duration (~4.265s with the real asset) from `seconds_to_outcome`. `call_event.py` was not modified at all (confirmed via `git diff --stat` — zero lines changed).

### Measured cue duration (REQUIRED explicit statement)

`pickup_cue_duration_seconds()` returns **4.265s** against the real committed asset (1.2s ring + 3.065s hey clip — verified directly: `apps/voice/assets/telephony/kph-hey.wav` is 24kHz, 73561 frames = 3.0650416...s). If a future re-render of the hey clip changes its length, this function's return value (and therefore the controller's defer lead) changes automatically — no code change needed, but the CONTEXT evidence table's "~4.3s cue" framing would then need a fresh telemetry pull to stay accurate. With the hey clip asset absent/unreadable, it degrades to 1.2s (ring only), verified by a dedicated test.

### `gate_cue_lead_max_seconds` and the resulting worst-case bound (REQUIRED explicit statement)

Chosen value: **8.0** (the D-05d safety cap, `TelephonyConfig.gate_cue_lead_max_seconds` default, also shipped in `configs/telephony.toml`). This comfortably covers the measured 4.265s cue plus media-setup slack while staying tightly bounded. Combined with `gate_window_seconds = 10`, the **absolute worst-case fail-closed fire time on ANY path** (cue plays normally, cue never plays, cue errors, cue is barge-in-flushed, or the cue lead is pathologically large) is now:

```
timer_start + gate_window_seconds(10) + gate_cue_lead_max_seconds(8.0) = timer_start + 18s
```

For the expected/normal path (cue plays, ~4.265s lead applied), the caller's real dialing budget is `gate_window_seconds(10)` measured from the end of that ~4.265s cue — i.e. roughly `4.265s + 10s ≈ 14.3s` from timer start to fail-closed, versus today's ~4s of real dialing time inside an 8s window measured from pipeline start.

### Live production path change + operator action required (REQUIRED explicit statement)

This is a **live production telephony code path change** (the §24 answer-gate that every PSTN call to every klanker-voice DID passes through) — it requires a **telephony-edge redeploy** to take effect; nothing here is live until that redeploy ships. Separately, `gate_debug_log_dtmf` is shipped **off** (`false`) in `configs/telephony.toml` by design — it must be flipped to `true` in that file **and telephony-edge redeployed** before it produces any `dtmf_received{...}` CloudWatch evidence. Until both of those operator actions happen, the DTMF-visibility question raised in CONTEXT (the caller who placed 12 calls with zero registered digits while STT heard them speak) remains unanswered.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - editing accident, self-caught and fixed] A pre-existing assertion line was momentarily displaced during Task 3 test editing**
- **Found during:** Task 3, editing `test_telephony_controller.py`
- **Issue:** A `Read` call used a line-limited window that cut off the true end of `test_wrong_code_gate_timeout_redacts_digits_words_and_code` (its final line, `assert "zorblattflibber" not in payload_text`, was one line beyond the window shown). An `Edit` anchored on the visible 3-line suffix inserted the new Task-3 test section immediately after those 3 lines, stranding the real 4th assertion line at the very end of the file (inside an unrelated new test, causing a `NameError`).
- **Fix:** Restored `assert "zorblattflibber" not in payload_text` to its correct position at the end of the original test, and removed the orphaned duplicate from the new test. Verified against `git show HEAD:...` that the restored line matches the pre-change file byte-for-byte.
- **Files modified:** `apps/voice/tests/test_telephony_controller.py`
- **Commit:** `1531248` (folded into the Task 3 commit; caught and fixed before that commit was made, so no broken intermediate commit exists)

### Corrections to CONTEXT/PLAN

None — every "verified fact" and "Claude's Discretion" item in CONTEXT/PLAN held as written; no plan correction was needed.

## Verification Results

- `cd apps/voice && uv run pytest tests/ -q -k telephony` — **335 passed** (this run; the pre-existing, plan-documented flake `test_telephony_lifecycle.py::test_session_max_hard_stop_hangs_up_even_if_goodbye_leg_raises` did not trigger in this run, and separately confirmed passing in isolation when it has appeared in other runs during this task — unrelated to this change, a timing flake in a file this plan does not touch).
- `cd apps/voice && uv run pytest tests/ -q` (the FULL suite, not just telephony) — **719 passed, 1 skipped**, plus 4 failures + 23 errors that are **pre-existing environment issues, unrelated to this change**: `tests/test_quota.py`, `tests/test_session.py::test_auto_trip_flips_control_item_when_ceiling_crossed`, and three `tests/test_slot_leak.py` tests all fail with `botocore.errorfactory.ResourceNotFoundException: ... non-existent table` — the local `dynamodb-local` container at `localhost:8888` is reachable but does not currently have the `kmv-voice-usage`/`kmv-auth-electro` tables provisioned in this environment. Confirmed via `git diff --stat` against the base commit that none of `session.py`, `quota.py`, `test_session.py`, `test_slot_leak.py`, or `test_quota.py` were touched by any commit in this task — this is pre-existing local-environment state, not a regression.
- **Fail-closed proof (D-05d)** — three distinct passing tests, each asserting the fail-closed callback actually fires:
  - cue handler never fires → `test_gated_call_cue_handler_never_fires_still_fails_closed_at_gate_window_seconds` (controller-level) + `test_never_deferred_gate_still_fires_at_plain_gate_window_seconds` (gate-level)
  - cue asset missing (ring-only defer) → `test_gated_call_missing_cue_asset_defers_by_ring_duration_only_still_fails_closed`
  - absurdly large cue lead, clamped → `test_defer_for_cue_lead_is_clamped_to_max_cue_lead_seconds`
- **Redaction proof (D-05e)** — `test_dtmf_debug_log_exact_key_set_and_never_carries_the_pressed_digit`: the captured `dtmf_received` line's key set is exactly `["call_id", "caller_id", "arrival_count", "tracked"]` and the pressed digit character is absent. `git diff --stat` confirms `apps/voice/src/klanker_voice/telephony/call_event.py` is untouched.
- **Default-off proof** — `test_dtmf_debug_log_off_by_default_emits_no_arrival_line`: with the flag at its default, no `dtmf_received` line is emitted and the existing PIN-unlock path is unaffected; the full pre-existing telephony suite passes unmodified (see the "editing accident" deviation above for the one line that was momentarily displaced then restored byte-identical).
- CLI config check:
  ```
  $ uv run python -c "from klanker_voice.telephony.config import load_telephony_config; c = load_telephony_config('configs/telephony.toml'); print(c.gate_window_seconds, c.gate_cue_lead_max_seconds, c.gate_debug_log_dtmf)"
  10 8.0 False
  ```
- Diff-read confirmation: `gate.start_timer()` is still called unconditionally at the end of `_finish_stasis_start_gated` (`controller.py:1652`, no new conditional wrapping it) and still on the first `StartFrame` inside `GateProcessor.process_frame` (`gate.py:513`).

## Known Stubs

None.

## Threat Flags

None — every new surface (the two config knobs, the debug log line, the cue-defer seam) was already registered in the plan's own threat model (T-fki-01 through T-fki-06) and mitigated as designed; no new unregistered surface was introduced.

## Self-Check: PASSED

- `apps/voice/src/klanker_voice/telephony/gate.py` — FOUND
- `apps/voice/src/klanker_voice/telephony/pickup_cue.py` — FOUND
- `apps/voice/src/klanker_voice/telephony/config.py` — FOUND
- `apps/voice/src/klanker_voice/telephony/controller.py` — FOUND
- `apps/voice/configs/telephony.toml` — FOUND
- Commit `724ecb0` — FOUND in `git log --oneline`
- Commit `0b19cdd` — FOUND in `git log --oneline`
- Commit `1531248` — FOUND in `git log --oneline`
