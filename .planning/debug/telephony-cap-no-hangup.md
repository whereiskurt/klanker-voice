---
slug: telephony-cap-no-hangup
status: awaiting_human_verify
trigger: "Telephony §24 session cap WARNS but never HANGS UP on the PSTN path — the 3-min public cap fires its soft winddown warning at ~2:30 but never terminates/hangs up the call at 3:00; the call runs indefinitely until the caller manually hangs up."
created: 2026-07-14
updated: 2026-07-15
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

## Current Focus (CORRECTED — the prior reasoning_checkpoint below is DISPROVEN)

**The first fix (release_heartbeat guard, b9c1e5e) was WRONG as the root cause.** The
corrected live re-investigation of the 2026-07-15 call proved: at the cap there was NO
release, NO release_heartbeat exception, NO botocore/IAM error — release() was simply never
invoked. The telephony-edge task-role DOES have dynamodb:UpdateItem on kmv-voice-usage. So
the "IAM gap / heartbeat raise" premise is FALSE. The heartbeat guard is KEPT as harmless
hardening, but it does not explain or fix this bug.

reasoning_checkpoint (CORRECTED, faithfully reproduced):
  hypothesis: "The D-02 hard stop is gated behind the BEST-EFFORT spoken-goodbye leg. At
    session_max, _service_timer -> _fire_wind_down (sets _wind_down_fired=True) -> on_stop
    (_on_stop in call_runtime.py) runs `await speak_goodbye(...)` -> `await sleep(grace)` ->
    `await runner.cancel(...)`. runner.cancel is the ONLY load-bearing teardown trigger (it
    unblocks CallSession.run -> its finally -> lifecycle.release() -> on_released -> the
    telephony composed hook that ARI-hangs-up the SIP channel). If the goodbye leg RAISES
    (a worker in a bad state at the cap, a transport hiccup — queue_frames failing), the
    exception propagates out of _on_stop, out of _fire_wind_down (which has ALREADY set
    _wind_down_fired=True), and out of _service_timer (which catches ONLY CancelledError),
    killing the timer task BEFORE runner.cancel ever runs. release()/on_released never fire,
    the runner keeps generating turns, the SIP line stays open past session_max, and because
    _wind_down_fired is already True no later trigger can recover."
  confirming_evidence:
    - "FAITHFUL controller-level repro (real _service_timer at tiny session_max, real tick
      loop available, real _on_stop, faithful CallSession.run bracket): with speak_goodbye
      RAISING, ari hangup(chan-1)=0, _wind_down_fired=True, _stopped=False, call still in
      the registry — the EXACT prod symptom (warns, never hangs up). With the fix applied:
      hangup(chan-1)=1, _stopped=True, registry empty. RED->GREEN."
    - "The soft warning fires from _service_timer BEFORE _fire_wind_down, so it is
      unaffected — matches 'warns at 2:30 but never hangs up at 3:00'."
    - "Tick-exhaustion pre-fire (mechanism 1, the flagged top suspect) was TESTED faithfully
      and does NOT strand: a daily_exhausted tick's _fire_wind_down tears the call down
      correctly (hangup fires). So the flag was not set early by a tick — it was set by the
      service timer at the cap, whose on_stop then failed before runner.cancel."
    - "PR #48 (faf0c7a) changed ONLY config; the hard stop was never reachable on telephony
      before (kph-tier=24h), exposing a pre-existing latent fragility in the wind-down leg."
  falsification_test: "If _on_stop runs runner.cancel in a `finally` (so it fires even when
    speak_goodbye raises), the telephony hard-stop ARI-hangs-up despite the goodbye failure.
    Repro proves: fails before the fix (no hangup), passes after."
  fix_rationale: "speak_goodbye + the grace sleep are best-effort courtesy; runner.cancel ->
    release -> on_released (ARI hangup) is load-bearing. Move runner.cancel into _on_stop's
    `finally` so it ALWAYS fires; swallow the goodbye exception in _fire_wind_down so the
    timer/tick task ends cleanly (a raise would strand AND spam unretrieved-task-exception).
    Addresses the root cause (teardown gated on a fragile best-effort leg), not a symptom."
  blind_spots: "The exact LEAF trigger in the live call (why speak_goodbye/queue_frames
    would raise at the cap) is not captured — the live evidence shows NO exception, because
    the ENTIRE hard-stop path had ZERO logging (the reason it was invisible). The repro
    proves the strand mechanism and the fix; the added INFO instrumentation will confirm the
    exact branch on the next live call held past 3:00. The fix makes teardown resilient
    regardless of which leg fails."

--- DISPROVEN prior checkpoint (kept for the record) ---
reasoning_checkpoint:
  hypothesis: "SessionLifecycle.release() calls the best-effort quota.release_heartbeat (a DynamoDB UpdateItem) UNGUARDED, and BEFORE the on_released hook. On the telephony path on_released is the ONLY thing that (a) cancels the pipeline runner and (b) ARI-hangs-up the PSTN channel. When release_heartbeat raises (scoped telephony-edge task-role IAM lacking dynamodb:UpdateItem on the usage table, OR any transient DynamoDB throttle/error), the exception propagates out of release(), on_released is skipped, the runner keeps generating turns and the SIP line is never hung up."
  DISPROVEN_BY: "Live re-investigation: no release, no heartbeat exception, no IAM/botocore error at the cap; task-role DOES have dynamodb:UpdateItem. release() was never reached — so a raise inside release() cannot be the cause."

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

## Evidence (CORRECTED re-investigation)

- timestamp: re-investigation (live call 2026-07-15)
  checked: CloudWatch for the whole call; telephony-edge task-role IAM
  found: At the 180s cap — NO release, NO goodbye_copy, NO wind-down, NO exception, NO
    release_heartbeat error, NO botocore/IAM error, NO "Task exception was never retrieved".
    The task-role HAS dynamodb:UpdateItem on kmv-voice-usage. Only teardown activity was the
    caller's manual hangup at 00:55:19 (+18 base_output NoneType errors in a 0.35s burst),
    NOT at the cap.
  implication: release() was never invoked at the cap; the prior "heartbeat raise inside
    release()" root cause is impossible (release never ran). The strand is UPSTREAM of
    release() — in the _service_timer -> _fire_wind_down -> _on_stop chain, before
    runner.cancel.

- timestamp: faithful-repro
  checked: controller-level repro with real _service_timer at tiny session_max, real
    _on_stop, faithful CallSession.run bracket, tick loop available; two variants
  found: (1) TICK PRE-FIRE (daily_exhausted): teardown completes, ARI hangup fires — NOT the
    bug. (2) GOODBYE LEG RAISES (speak_goodbye raises at the cap): hangup(chan-1)=0,
    _wind_down_fired=True, _stopped=False, call still registered — EXACT prod symptom.
    Applying the fix flips it to hangup(chan-1)=1, _stopped=True, registry empty.
  implication: ROOT CAUSE = the hard stop is gated behind the best-effort spoken-goodbye
    leg; a raise there kills the timer task before runner.cancel, stranding teardown.

## Resolution (CORRECTED)

root_cause: |
  The D-02 hard stop is gated behind the BEST-EFFORT spoken-goodbye leg. At session_max,
  SessionLifecycle._service_timer -> _fire_wind_down (sets _wind_down_fired=True) ->
  on_stop, which on every transport is call_runtime._on_stop:
      await speak_goodbye(worker, goodbye_copy)   # queue a TTSSpeakFrame
      await asyncio.sleep(goodbye_grace_seconds)
      await runner.cancel("session wind-down complete")   # <-- the ONLY load-bearing teardown
  runner.cancel is what unblocks CallSession.run -> its finally -> lifecycle.release() ->
  on_released (on telephony, the composed hook that ARI-hangs-up the SIP channel; on WebRTC,
  just runner.cancel). If the goodbye leg RAISES before reaching runner.cancel (a worker in
  a bad state at the cap, a transport/queue_frames hiccup), the exception propagates out of
  _on_stop, out of _fire_wind_down (which has ALREADY set _wind_down_fired=True), and out of
  _service_timer (which caught ONLY CancelledError) — killing the timer task before
  runner.cancel ever ran. release()/on_released never fire; the runner keeps generating
  turns; the SIP line stays open past session_max; and because _wind_down_fired is already
  True, no later trigger can recover. The soft warning fires from _service_timer BEFORE
  _fire_wind_down, so it is unaffected — hence "warns at 2:30 but never hangs up at 3:00."
  The entire hard-stop path had ZERO logging, which is why the live call showed "nothing" at
  the cap. WebRTC never surfaced it: the browser tearing down its transport is an
  independent teardown backstop; the PSTN line has none. Exposed (not introduced) by PR #48
  (faf0c7a), which only swapped unlock_tier_id kph-tier(24h)->pstn-public-tier(180s), making
  the telephony hard stop reachable for the first time.

  NOTE: the exact leaf trigger (why the goodbye leg raised in the live call) is not captured
  — the live evidence shows no exception precisely because that path was unlogged. The
  faithful repro proves the strand mechanism and the fix; the added instrumentation will
  pin the exact branch on the next live call.

fix: |
  1. call_runtime._on_stop: wrap the best-effort goodbye leg (speak_goodbye + grace sleep)
     in try/except and run `runner.cancel` in a `finally`, so the load-bearing hard close
     ALWAYS fires even if the goodbye leg raises. This is the load-bearing fix and covers
     BOTH transports (webrtc + pstn).
  2. session._fire_wind_down: swallow a non-CancelledError from on_stop (teardown already
     triggered in on_stop's finally) so the timer/tick task ends cleanly and never strands
     or spams "Task exception was never retrieved".
  3. INSTRUMENTATION (INFO, telephony-safe, no secrets): _service_timer reaching session_max
     + firing the hard stop; _fire_wind_down entry with trigger label + guard-hit ("already
     fired") log; on_stop-complete; _tick daily_exhausted branch (with on_daily_exhausted
     set/none); release() entry + on_released invocation. The whole hard-stop path is now
     diagnosable.
  4. KEPT the prior release_heartbeat try/except guard (b9c1e5e) as harmless hardening.

verification: |
  TDD RED->GREEN. Two NEW tests fail on the pre-fix code, pass with the fix (confirmed by
  git-stashing the src fix and re-running):
  - tests/test_call_runtime.py::test_on_stop_hard_close_fires_even_if_goodbye_raises
    (unit: _on_stop still reaches runner.cancel / sets the runner shutdown event when
    speak_goodbye raises).
  - tests/test_telephony_lifecycle.py::test_session_max_hard_stop_hangs_up_even_if_goodbye_leg_raises
    (faithful: real _service_timer to a tiny session_max, real _on_stop with speak_goodbye
    raising; asserts the soft warning STILL fires exactly once AND the SIP channel is hung
    up + bridge destroyed + registry emptied).
  The prior heartbeat-guard tests still pass (kept). Full suite: 509 passed (507 baseline +
  these 2). The only failures/errors (test_quota.py, test_slot_leak.py, session auto-trip)
  are pre-existing dynamodb-local integration failures that fail identically with the src
  fix stashed — NOT caused by this change.
  FAITHFUL REPRO REPRODUCED THE BUG: YES (via a failing spoken-goodbye leg).
  REMAINING (human): one live PSTN call held past 3:00 to (a) confirm the server-side
  auto-hangup and (b) capture — via the new INFO instrumentation — the exact leaf trigger.

files_changed:
  - apps/voice/src/klanker_voice/call_runtime.py (_on_stop: runner.cancel in finally + guard goodbye leg + INFO log)
  - apps/voice/src/klanker_voice/session.py (_fire_wind_down: swallow on_stop raise + trigger label; _service_timer / _tick / release() INFO instrumentation)
  - apps/voice/tests/test_call_runtime.py (new _on_stop hard-close unit regression)
  - apps/voice/tests/test_telephony_lifecycle.py (new faithful session-max goodbye-raise integration test)

  (Prior b9c1e5e files — session.py release_heartbeat guard + its two tests — remain in place.)
