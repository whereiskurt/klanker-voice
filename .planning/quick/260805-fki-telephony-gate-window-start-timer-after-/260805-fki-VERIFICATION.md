---
phase: quick-260805-fki
verified: 2026-08-05T00:00:00Z
status: passed
score: 8/8 must-haves verified
behavior_unverified: 0
overrides_applied: 0
---

# Quick Task 260805-fki: Telephony gate window — Verification Report

**Task Goal:** Rebase the §24 fail-closed gate timer to start when the ring+"hey" pickup
cue finishes rather than at ARI answer; raise `gate_window_seconds` 8 → 10; add opt-in,
redaction-safe logging of DTMF digit arrival at the edge.
**Verified:** 2026-08-05
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Caller's dialing budget is measured from the end of the pickup cue, not ARI answer | ✓ VERIFIED | `defer_for_cue` rebases `_deadline` to `now + lead + gate_window_seconds`; wired at the sole `on_client_connected` seam in `_register_pickup_cue` (`controller.py:1286-1290`), called BEFORE `play_pickup_cue`. Test `test_defer_for_cue_rebases_deadline_by_the_lead` proves the timing effect directly (gate.py-level); `test_gated_flow_defers_gate_deadline_before_playing_the_cue` proves call order at the controller seam. |
| 2 | `gate_window_seconds = 10` in shipped toml, history comment extended | ✓ VERIFIED | `configs/telephony.toml` diff shows `8` → `10` and the inline comment appends a full 260805 entry (telemetry: median 6.0s/max 12.1s/3 of 24 exceeding 8s, partial-digit fingerprint, cue-relative rationale) without truncating the prior 260714/260729 history. `test_shipped_telephony_toml_gate_window_is_ten` (loads real file) passes. |
| 3 | Gate ALWAYS fails closed within a bounded window on every path (cue missing, handler never fires, cue interrupted) | ✓ VERIFIED | Read `start_timer`/`defer_for_cue`/`_run_timer` directly (see Fail-Closed Analysis below) — the invariant holds by construction, not just by test. Explicit tests: `test_gated_call_cue_handler_never_fires_still_fails_closed_at_gate_window_seconds`, `test_gated_call_missing_cue_asset_defers_by_ring_duration_only_still_fails_closed`, `test_defer_for_cue_after_resolved_is_noop_never_resurrects_timer`, `test_unlock_still_cancels_a_deferred_timer`, `test_cancel_for_takeover_still_cancels_a_deferred_timer` — all pass. |
| 4 | Absolute fail-closed fire time bounded by `timer_start + gate_window_seconds + gate_cue_lead_max_seconds` on any path | ✓ VERIFIED | `defer_for_cue`'s cap arithmetic uses `_timer_started_at` (the fixed anchor), not the (possibly late) time the cue-ready event fires — so a late-firing `on_client_connected` cannot push the deadline past the cap. `test_defer_for_cue_lead_is_clamped_to_max_cue_lead_seconds` (lead=1000 clamps to 0.1) passes. |
| 5 | `gate_debug_log_dtmf=true` emits exactly one `dtmf_received{...}` line per ARI event with running arrival count, including for untracked channels | ✓ VERIFIED | `on_channel_dtmf_received` (controller.py:2000-2012) hoists parsing/lookup above all guards and logs unconditionally when the flag is set. Tests `test_dtmf_debug_log_on_emits_one_line_per_event_with_incrementing_count`, `test_dtmf_debug_log_on_emits_for_unknown_channel_tracked_false`, `test_dtmf_debug_log_on_emits_even_when_gate_mode_excludes_dtmf` all pass. |
| 6 | That line NEVER carries a digit value, buffer, PIN, or announcement code; `build_call_event` untouched | ✓ VERIFIED | Log f-string (controller.py:2008-2012) references only `channel_id`, `caller_id_for_log`, `arrival_count`, `active_call is not None` — `digit` is never interpolated. `test_dtmf_debug_log_exact_key_set_and_never_carries_the_pressed_digit` asserts exact 4-key set and digit absence; passes. `git diff origin/main -- call_event.py` is empty. |
| 7 | `gate_debug_log_dtmf=false` (default) → byte-identical log output | ✓ VERIFIED | `test_dtmf_debug_log_off_by_default_emits_no_arrival_line` passes; full pre-existing telephony suite (335 tests) green with the flag at its default in all pre-existing tests. |
| 8 | `seconds_to_outcome` stays ANSWER-relative | ✓ VERIFIED | `git diff origin/main -- call_event.py` is empty (zero changes); SUMMARY states the decision explicitly as required. |

**Score:** 8/8 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `src/klanker_voice/telephony/gate.py` | deadline-based timer + `defer_for_cue` | ✓ VERIFIED | Read in full; logic traced (see below) |
| `src/klanker_voice/telephony/pickup_cue.py` | `pickup_cue_duration_seconds()` | ✓ VERIFIED | Present, computes 1.2s ring + hey-clip duration, never raises |
| `src/klanker_voice/telephony/config.py` | two new knobs | ✓ VERIFIED | `gate_cue_lead_max_seconds` (default 8.0), `gate_debug_log_dtmf` (default False), both parsed in `load_telephony_config` |
| `src/klanker_voice/telephony/controller.py` | cue-defer wiring + DTMF logging | ✓ VERIFIED | `_register_pickup_cue(gate=...)`, `on_channel_dtmf_received` hoist, both confirmed by direct diff read |
| `configs/telephony.toml` | `gate_window_seconds=10` + history + new knobs | ✓ VERIFIED | Confirmed via diff and CLI load |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `GateProcessor._deadline` | `start_timer`/`defer_for_cue`/`_run_timer` | single source of truth | ✓ WIRED | `_run_timer` only reads `_deadline`; only `start_timer` and `defer_for_cue` write it |
| `_register_pickup_cue`'s `on_client_connected` handler | `defer_for_cue` | sole caller | ✓ WIRED | `grep` confirms `defer_for_cue` is called only at controller.py:1289 |
| `_finish_stasis_start_gated` | `gate.start_timer()` | unconditional backstop | ✓ WIRED | Confirmed still unconditional at controller.py:1652 (pre-existing mint-failure early-return at line 1646-1647 is unchanged from before this task and already resolves the gate via `_gate_fail_closed` before this line, so it doesn't weaken the invariant) |
| `ActiveCall.dtmf_arrivals` | `on_channel_dtmf_received` entry | increments before guards | ✓ WIRED | Confirmed at controller.py:2003-2006, before the `gate_mode` guard at line 2013 |

## Fail-Closed Analysis (D-05d) — read, not assumed

Traced `start_timer`, `defer_for_cue`, and `_run_timer` directly in `gate.py`:

- `start_timer()` is guarded by `self._timer_task is None and not self._resolved` — idempotent, and it stamps `_timer_started_at` + computes the initial `_deadline` **before** creating the task, so a `defer_for_cue` that arrived earlier (`_pending_cue_lead`) is honored from tick one.
- `defer_for_cue()` is single-shot via `_cue_deferred` (set immediately, before any early return), so it can never accumulate multiple leads even under a hypothetical duplicate call.
- Because Python asyncio is single-threaded and both methods are fully synchronous (no `await` inside either), there is no genuine re-entrancy hazard — interleaving can only happen at `await` points, and neither method has one.
- The clamp cap in `defer_for_cue` is anchored to `_timer_started_at` (the fixed, already-stamped anchor), not to "now" at defer time — so a late-firing `on_client_connected` cannot push the absolute worst case past `gate_window_seconds + max_cue_lead_seconds` from timer start, regardless of how late the cue-ready event arrives.
- `_run_timer`'s `while True` / re-check-`_deadline`-after-every-wake loop is what makes a *mid-flight* extension (a `defer_for_cue` call that lands while the task is already sleeping on the old deadline) actually take effect — `asyncio.sleep` itself can't be "extended" once started, so re-checking on wake is required, and it's present.
- `defer_for_cue` after `_resolved` is a plain early return before any state mutation — cannot resurrect a cancelled timer. Cancellation (`unlock`/`cancel_for_takeover`) both flip `_resolved = True` synchronously before cancelling `_timer_task`, so no unlock-then-fail-closed race is possible.
- Conclusion: the deadline can never fail to fire once `start_timer()` has run, and `start_timer()` runs unconditionally on every real call path (explicit call in `_finish_stasis_start_gated`, plus the idempotent StartFrame trigger in `process_frame`) except the pre-existing (unchanged) mint-failure early return, which itself already resolves the gate via `_gate_fail_closed` first. No path was found that leaves the gate open indefinitely.

## Redaction Analysis (D-05e) — read, not assumed

Read `on_channel_dtmf_received`'s logging block directly (controller.py:2003-2012): the f-string interpolates exactly `channel_id`, `caller_id_for_log`, `arrival_count`, and `active_call is not None` — `digit` (the actual pressed key) is parsed at line 2001 but never referenced anywhere in the logging block or the log line. `active_call.dtmf_buffer`, `self._pin`, and announcement codes are likewise absent from the new code. Diffed `call_event.py` against `origin/main`: zero lines changed, confirming `build_call_event`'s keyword-only signature is untouched and the new counter (`ActiveCall.dtmf_arrivals`) never reaches the telemetry event payload.

## Deviation Claim Verification

The SUMMARY claims a stranded assertion line in `test_telephony_controller.py` was restored byte-identical. Diffed the full file against `origin/main`: **320 insertions, 0 deletions** — no pre-existing line was removed or modified, only new test functions were appended after the last pre-existing test (`test_wrong_code_gate_timeout_redacts_digits_words_and_code`, whose final `assert "zorblattflibber" not in payload_text` line is present and unchanged). Same check on `test_telephony_gate.py`, `test_telephony_config.py`, and `test_pickup_cue_player.py`: all pure additions, zero deletions.

### Anti-Patterns Found

None. No TBD/FIXME/XXX/TODO/HACK/PLACEHOLDER markers in any modified file. No stub returns, no hardcoded empty log payloads.

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Config loads shipped toml with new values | `uv run python -c "...load_telephony_config('configs/telephony.toml')..."` | `10 8.0 False` | ✓ PASS |
| Gate test suite | `uv run pytest tests/test_telephony_gate.py -q` | 57 passed (48 pre-existing + 9 new) | ✓ PASS |
| Config test suite | `uv run pytest tests/test_telephony_config.py -q` | 76 passed | ✓ PASS |
| Telephony-filtered suite | `uv run pytest tests/ -q -k telephony` | 335 passed | ✓ PASS |
| Full apps/voice suite | `uv run pytest tests/ -q` | 719 passed, 1 skipped, 4 failed, 23 errors | ✓ PASS (failures pre-existing, see below) |

**Full-suite failures/errors triaged:** all 4 failures (`test_session.py::test_auto_trip_flips_control_item_when_ceiling_crossed`, 3x `test_slot_leak.py`) and all 23 errors (`test_quota.py`) are `botocore.errorfactory.ResourceNotFoundException: ... non-existent table` against local dynamodb-local (missing `kmv-voice-usage`/`kmv-auth-electro` tables in this environment). Confirmed via `git diff --stat origin/main -- src/klanker_voice/session.py src/klanker_voice/quota.py tests/test_session.py tests/test_slot_leak.py tests/test_quota.py` — none of these files were touched by this task's commits. The documented pre-existing flake (`test_telephony_lifecycle.py::test_session_max_hard_stop_hangs_up_even_if_goodbye_leg_raises`) did not trigger in this run.

### Requirements Coverage

| Requirement | Description | Status | Evidence |
|-------------|-------------|--------|----------|
| QT-260805-fki | Cue-relative gate timer, 10s window, DTMF arrival logging | ✓ SATISFIED | All 3 sub-goals verified above |

### Human Verification Required

None. This is a backend timing/logging change fully verifiable via code inspection and automated tests; there is no UI/visual/real-time component that automated checks cannot cover. (Live production effect requires a telephony-edge redeploy, correctly flagged by the executor as an operator follow-up — not a verification gap.)

### Gaps Summary

None found. All 8 must-have truths verified against actual code (not SUMMARY claims); the two highest-risk areas (D-05d fail-closed and D-05e redaction) were independently traced through the source rather than taken on faith from passing tests. Test-file diffs against `origin/main` confirm the self-reported "stranded assertion" deviation was genuinely a pure restoration, not a silent weakening.

---

_Verified: 2026-08-05_
_Verifier: Claude (gsd-verifier)_
