---
slug: telephony-cap-no-hangup
status: awaiting_human_verify
trigger: "Telephony §24 session cap WARNS but never HANGS UP on the PSTN path — the 3-min public cap fires its soft winddown warning at ~2:30 but never terminates/hangs up the call at 3:00; the call runs indefinitely until the caller manually hangs up."
created: 2026-07-14
updated: 2026-07-14
---

# Debug: telephony session cap warns but never hangs up (PSTN)

## Symptoms

**Expected behavior:** On a PSTN call, at `session_max` seconds after unlock (pstn-public-tier
sessionMaxSeconds=180), the telephony session should speak the goodbye copy and then HARD hang up
the ARI channel (the same teardown a caller-initiated hangup triggers), ending the call at ~3:00.

**Actual behavior:** The soft winddown WARNING fires correctly at ~150s (session_max − 30s), but
the hard stop at 180s never fires. The call keeps running (LLM + TTS turns continue) until the
caller manually hangs up. Observed live to ~6.5 minutes with no server-side hangup.

**Error messages / evidence (live prod, 2026-07-15 00:49–00:56 UTC, telephony-edge task
9c8bc1c, image faf0c7a, ECS service telephony-edge-use1):**
- 00:49:31Z — `unlocked{method: 'dtmf', call_id: '1784076567.2'}` (gate unlocked; session clock starts here)
- Tier = `pstn-public-tier`, sessionMaxSeconds=180 (live DynamoDB `kmv-auth-electro`).
- 00:52:02Z (~150s) — winddown WARNING injected: warning_copy text
  "Just a heads up: we're coming up on the time limit for this chat, so let's start wrapping up."
  → proves the SessionLifecycle service timer IS running with session_max=180.
- 00:52:11Z (~180s, session_max) — NOTHING. No goodbye_copy spoken, no ARI Hangup, no StasisEnd,
  no on_released / wind_down / on_stop / lifecycle-terminate marker anywhere in CloudWatch.
- Call continued generating turns; bot even said "I don't control the hangup on my end — that's
  on the platform side." Caller manually hung up ~00:56 (~6.5 min in).
- CloudWatch grep over the whole call window for
  `wind_down|winddown|goodbye|Hangup|StasisEnd|on_released|_service_timer|on_stop` found ONLY the
  soft warning (plus normal conversation) — the hard-terminate path never logged.

**Timeline:** Introduced/observed on the just-shipped `pstn-public-tier` 3-min cap
(PR #48, merged to main = faf0c7a). First live test of a PSTN call held past 3:00. Likely a
pre-existing gap in the telephony path (never hung up at session_max), only now exposed because
before this the unlock tier was kph-tier (24h cap) so the hard stop never got a chance to fire on
telephony. The WEB/WebRTC path DOES disconnect at session_max (works in browser demos).

**Reproduction:** Place a PSTN call to a live DID, unlock (DTMF PIN or "hack the planet"
passphrase), stay on the line past 3:00 — observe the warning at ~2:30 but NO hangup at 3:00.

## Leading hypothesis (from orchestrator)

The telephony `CallSession`'s hard-stop is unwired or no-ops: the `SessionLifecycle` service
timer fires `on_warning` (soft, TTS/context injection — works) but the hard `wind_down`/`on_stop`
callback that should (a) speak goodbye_copy, (b) wait goodbye_grace_seconds, (c) HANG UP the ARI
channel is either not connected on the telephony path, or fires but has no telephony hangup
callback (unlike the web path which disconnects the WebRTC transport). Confirm by comparing the
web path (`apps/voice/server.py`) session-lifecycle wiring vs. the telephony path
(`telephony/controller.py`, `telephony/call_runtime.py`).

## Key files
- apps/voice/src/klanker_voice/session.py — SessionLifecycle: `_service_timer`, `on_warning`,
  the hard `_fire_wind_down`/`on_stop`, `on_released`, goodbye/grace handling.
- apps/voice/src/klanker_voice/telephony/controller.py — ARI channel hangup, `on_released`
  teardown, how the caller-hangup path tears down (self.calls.pop, media.close, ARI Hangup).
- apps/voice/src/klanker_voice/telephony/call_runtime.py — goodbye_grace_seconds + hangup wiring.
- apps/voice/server.py — the WEB path that DOES hang up at session_max (reference for the correct wiring).
- apps/voice/configs/telephony.toml [quota] — winddown_warning_seconds=30, goodbye_grace_seconds=5,
  goodbye_copy, warning_copy (the session cap VALUE comes from the tier, not the toml).

## Constraints on the fix
- TDD: write a FAILING test first — that the telephony `SessionLifecycle` hard-stop (session_max
  reached) invokes the ARI hangup/teardown, not just the soft warning. Then make it pass.
- Do NOT regress the soft warning (it works) or the caller-initiated hangup teardown.
- Do NOT touch the WebRTC/web path's working behavior.
- Keep the goodbye_copy + goodbye_grace_seconds sequencing (speak goodbye, brief grace, then hangup).
- Separate from the already-live `gate_debug_log_heard=true` flag (unrelated).

## Current Focus

reasoning_checkpoint:
  hypothesis: "SessionLifecycle.release() calls the best-effort quota.release_heartbeat (a DynamoDB UpdateItem) UNGUARDED, and BEFORE the on_released hook. On the telephony path on_released is the ONLY thing that (a) cancels the pipeline runner and (b) ARI-hangs-up the PSTN channel. When release_heartbeat raises (scoped telephony-edge task-role IAM lacking dynamodb:UpdateItem on the usage table, OR any transient DynamoDB throttle/error), the exception propagates out of release(), on_released is skipped, the runner keeps generating turns and the SIP line is never hung up."
  confirming_evidence:
    - "Faithful controller-level repro (real service timer at tiny session_max, faithful CallSession.run() release bracket): with release_heartbeat succeeding, ARI hangup on chan-1 fires correctly. With release_heartbeat RAISING, ARI calls stop at add_channel — NO hangup chan-1, NO destroy_bridge — and the run task shows the RuntimeError propagating out of release()."
    - "The soft warning fires from the INDEPENDENT _service_timer coroutine BEFORE release() runs, so it is unaffected — exactly matching 'warns at 2:30 but never hangs up at 3:00'."
    - "quota.release_heartbeat's own docstring declares it best-effort ('TTL cleanup is the backstop either way'), yet release() lets a raise there abort the hard teardown."
    - "PR #48 (faf0c7a) changed ONLY config (unlock_tier_id kph-tier->pstn-public-tier, session 180s) — not the wind-down code. The hard stop was simply never reachable on telephony before (kph-tier=24h), exposing a pre-existing latent defect."
  falsification_test: "If release() were made to always run on_released regardless of release_heartbeat's outcome, the telephony hard-stop would ARI-hang-up even when the heartbeat write fails. Repro proves: fails before the fix (no hangup), passes after."
  fix_rationale: "release_heartbeat / _emit_metric / _reconcile are best-effort bookkeeping; the ARI hangup + runner cancel (on_released) is the load-bearing teardown. Wrap the bookkeeping in try/except and move on_released into a finally so it ALWAYS runs. Addresses root cause (teardown gated on best-effort write), not a symptom."
  blind_spots: "Did not reproduce against the live telephony-edge IAM policy directly; the exact trigger (IAM gap vs transient DynamoDB error) is inferred. Either way the defect and fix are identical: teardown must not be gated on a best-effort write. WebRTC path is unaffected (browser connection teardown is an independent backstop; its on_released is only runner.cancel)."

## Evidence

- timestamp: investigation
  checked: session.py _service_timer / _fire_wind_down / release; call_runtime.py _on_stop/_on_released; controller.py _finish_stasis_start_gated on_released composition; pipecat WorkerRunner.run/cancel
  found: The full wind-down chain (_service_timer -> _fire_wind_down -> _on_stop -> runner.cancel -> CallSession.run finally -> lifecycle.stop -> release -> on_released -> ARI hangup) is correctly WIRED. Three hermetic repros (fake-run, controller-level, real WorkerRunner) all fire the ARI hangup when release() bookkeeping succeeds.
  implication: The wiring is not missing; the failure is inside release() when its best-effort bookkeeping fails.

- timestamp: root-cause confirmation
  checked: controller-level repro with quota.release_heartbeat monkeypatched to RAISE (models a scoped task-role IAM gap / transient DynamoDB error)
  found: ARI calls = [answer, create_external_media, create_bridge, add_channel, add_channel] — NO hangup, NO destroy_bridge. The RuntimeError propagates out of release() at the release_heartbeat line, SKIPPING on_released. Runner never cancelled (turns continue), SIP line never hung up.
  implication: ROOT CAUSE. release() must guarantee on_released runs even if release_heartbeat raises.

## Eliminated

- hypothesis: "The telephony on_released ARI-hangup hook is not wired / on_stop has no telephony hangup callback."
  evidence: controller.py _finish_stasis_start_gated line 759 (and ungated line 573) compose on_released = runner.cancel + ARI hangup + _close_active_call; three repros confirm it fires when bookkeeping succeeds.
  timestamp: investigation

- hypothesis: "The _service_timer never reaches _fire_wind_down after firing on_warning (timer coroutine dies/cancelled between warning and stop)."
  evidence: repros show the timer reliably reaches _fire_wind_down; the actual break is downstream inside release()'s bookkeeping, not in the timer.
  timestamp: investigation

## Resolution

root_cause: |
  SessionLifecycle.release() (apps/voice/src/klanker_voice/session.py) called the
  explicitly best-effort quota.release_heartbeat (a DynamoDB UpdateItem) UNGUARDED and
  BEFORE the on_released hook. Every teardown trigger funnels through release() — the D-02
  wall-clock hard stop (session_max) and every D-06 idle layer. On the telephony path
  on_released is the ONLY hook that both cancels the pipeline runner AND ARI-hangs-up the
  PSTN channel. When release_heartbeat raised (a scoped telephony-edge task-role IAM gap on
  the usage table's UpdateItem, or any transient DynamoDB throttle/error), the exception
  propagated out of release() before on_released, so the runner kept generating turns and
  the SIP line stayed open past session_max. The soft warning fires from the INDEPENDENT
  _service_timer coroutine BEFORE release(), so it was unaffected — hence "warns at 2:30 but
  never hangs up at 3:00." WebRTC never surfaced it: the browser tearing down the transport
  is an independent teardown backstop; the PSTN line has none. Exposed (not introduced) by
  PR #48 (faf0c7a), which only swapped unlock_tier_id kph-tier(24h)->pstn-public-tier(180s),
  making the telephony hard stop reachable for the first time.

fix: |
  In release(), guard the best-effort quota.release_heartbeat in its own try/except (log +
  swallow on failure — TTL cleanup is the documented backstop), so a heartbeat-release
  failure can never abort teardown. _emit_metric and _reconcile_scale_in_protection (which
  already swallow their own errors) and on_released now ALWAYS run. Surgical, not a blanket
  try/finally, so a heartbeat failure still runs the scale-in-protection reconcile (avoids
  re-stranding protection ON — the black-screen regression risk).

verification: |
  TDD RED->GREEN. Two new tests fail on original code, pass with fix:
  - tests/test_session.py::test_release_still_fires_on_released_when_heartbeat_release_raises
  - tests/test_telephony_lifecycle.py::test_session_max_hard_stop_hangs_up_even_if_heartbeat_release_fails
    (drives the REAL _service_timer to a tiny session_max through the full live wind-down
    chain with release_heartbeat raising; asserts ARI hangup on chan-1 fires).
  Full suite: 507 passed; the only failures (test_quota.py, test_slot_leak.py, session
  auto-trip) are pre-existing dynamodb-local integration failures that fail identically on
  the original code (dynamodb-local not running) — NOT caused by this change.
  REMAINING (human): live PSTN call held past 3:00 must now auto-hang-up server-side.

files_changed:
  - apps/voice/src/klanker_voice/session.py (release(): guard best-effort release_heartbeat)
  - apps/voice/tests/test_session.py (new regression unit test)
  - apps/voice/tests/test_telephony_lifecycle.py (new session-max hard-stop integration test)
