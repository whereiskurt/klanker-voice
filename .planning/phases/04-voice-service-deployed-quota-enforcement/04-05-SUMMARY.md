---
phase: 04-voice-service-deployed-quota-enforcement
plan: 05
subsystem: voice-service (session lifecycle / quota wind-down)
tags: [pipecat, asyncio, webrtc, dynamodb, quota, teardown]

requires:
  - phase: 04-04
    provides: "SessionLifecycle (service timer, 15s tick, ActiveSessions metric, ECS scale-in protection) with named on_warning/on_stop/on_daily_exhausted callback hooks left as no-ops for this plan to fill"
provides:
  - "apps/voice/src/klanker_voice/pipeline.py: inject_warning_instruction() (LLM-context push) + speak_goodbye() (TTSSpeakFrame bypassing the LLM)"
  - "apps/voice/src/klanker_voice/session.py: SessionLifecycle.release() (renamed idempotent core, stop() now a back-compat alias), three D-06 idle-teardown layers (on_transport_disconnected/on_transport_reconnected, on_user_speech + _silence_watchdog, on_pipeline_stall), on_released hook, and TeardownObserver"
  - "apps/voice/server.py: builds the pipeline before constructing lifecycle callbacks, wires on_warning/on_stop to the real inject_warning_instruction/speak_goodbye, registers on_client_disconnected/on_client_connected -> the new reconnect-grace methods, and sets on_released -> WorkerRunner.cancel"
  - "apps/voice/pipeline.toml + config.py: QuotaConfig gains winddown_warning_seconds, goodbye_grace_seconds, user_silence_timeout, reconnect_grace_seconds, warning_copy, goodbye_copy"
affects: ["Task 3 of this same plan (checkpoint:human-verify, deferred to the orchestrator — requires a live redeployed service)", "04-06 (kv usage/killswitch — reads the same DynamoDB items, unaffected by this plan)"]

tech-stack:
  added: []
  patterns:
    - "SessionLifecycle never holds a worker/transport/context reference (module docstring) — server.py builds the pipeline first, then assigns lifecycle.on_warning/on_stop/on_released as closures over the real worker/context/runner, closing the loop 04-04 deliberately left open"
    - "A single idempotent release() (renamed from stop(), which is now a thin alias) is the one path every teardown trigger funnels through — the guard check-and-set is synchronous (no await in between), so concurrent triggers race safely with no lock needed"
    - "The D-04 wind-down (service-timer cutoff vs. mid-session daily/period exhaustion) is guarded by a single-fire latch (_fire_wind_down) so on_stop can never double-invoke even if both triggers race close together"
    - "D-06 layers 2/3 (user-silence, pipeline stall) are wired onto real pipecat frames via a non-intrusive TeardownObserver — the same observer seam LatencyReportObserver already established, so no changes to the pipeline's processor graph"
    - "WorkerRunner.cancel is pipecat's own documented hangup call ('typically on transport disconnect') and is idempotent — used both for the normal wind-down hard-close and as the on_released hook every idle-teardown layer funnels through, so an abandoned session actually stops burning STT/LLM/TTS spend once its slot is freed, not just its DB bookkeeping"

key-files:
  created:
    - apps/voice/tests/test_winddown.py
    - apps/voice/tests/test_teardown.py
  modified:
    - apps/voice/src/klanker_voice/pipeline.py
    - apps/voice/src/klanker_voice/session.py
    - apps/voice/server.py
    - apps/voice/src/klanker_voice/config.py
    - apps/voice/pipeline.toml

key-decisions:
  - "release() vs stop(): the plan's <artifacts_produced>/<threat_model> language calls for a single idempotent 'release()' path; renamed the existing stop() body to release() and kept stop() as a thin alias so 04-04's existing (unmodified) test_session.py and server.py's finally-block call site both keep working unchanged."
  - "SessionLifecycle stays worker/transport-agnostic by design (04-04's Known Gap explicitly deferred this coupling) — server.py's _run_session was restructured to build the pipeline BEFORE lifecycle.start() can fire the service timer, then assigns on_warning/on_stop/on_released as closures; this required moving pipeline construction out of the old _run_session(connection)-only signature into _run_session(connection, lifecycle)."
  - "The goodbye grace is implemented as a flat asyncio.sleep(goodbye_grace_seconds) cap rather than racing a BotStoppedSpeakingFrame signal — simpler, deterministic for tests, and matches the plan's literal 'await up to goodbye_grace_seconds' wording; a future refinement could short-circuit early on bot-stopped-speaking."
  - "D-06 layer 3 (pipeline stall / unrecoverable error) is implemented as ErrorFrame(fatal=True) only — non-fatal ErrorFrames are left alone (tested explicitly) since pipecat's own ErrorFrame.fatal flag is exactly the 'unrecoverable' signal the plan calls for."
  - "A new on_released hook (Callback | None, called once at the end of release()) closes a gap the plan's <action> text didn't spell out: the three idle-teardown layers release the DB/metric bookkeeping on their own, but SessionLifecycle has no way to actually stop the running WorkerRunner itself — on_released lets server.py wire that in (-> runner.cancel) without SessionLifecycle needing a worker reference."

requirements-completed: []  # QUOT-03/QUOT-05 NOT marked complete yet — Task 3 (checkpoint:human-verify) below is still pending a live deployed session.

coverage:
  - id: D1
    description: "D-04 natural warning (LLM-context injection) + deterministic goodbye (TTSSpeakFrame bypassing the LLM), the goodbye-grace + hard-close, and mid-session daily/period exhaustion reusing the identical wind-down sequence"
    requirement: QUOT-03
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_winddown.py (6 tests): inject_warning_instruction pushes a developer-role context message + queues an LLMRunFrame; speak_goodbye queues a TTSSpeakFrame with append_to_context=False; the real SessionLifecycle wiring speaks-goodbye/waits-grace/hard-closes; daily exhaustion reuses the same on_stop path; the wind-down never double-fires when both triggers race"
        status: pass
    human_judgment: true
    rationale: "Unit tests prove the mechanics (frame types, callback sequencing, single-fire guard) against stubbed workers/a real event loop — but QUOT-03's actual requirement is that the warning sounds natural and the goodbye is clean when spoken by the real LLM+TTS stack in a live browser session. That is Task 3 of this plan (checkpoint:human-verify), deferred to the orchestrator: it needs a redeployed service and a human ear, neither available to this executor."
  - id: D2
    description: "Three D-06 idle-teardown layers (transport disconnect + D-07 reconnect grace, user-silence watchdog, pipeline stall/fatal error) atop the D-02 wall-clock bound, all funneled through a single idempotent release()"
    requirement: QUOT-05
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_teardown.py (11 tests): each layer releases exactly once; reconnect within the grace window cancels teardown; release() is idempotent under concurrent triggers (asyncio.gather of three racing callers); the wall-clock cutoff and the on_released hook both funnel through the same release(); TeardownObserver correctly routes UserStartedSpeakingFrame and fatal-vs-non-fatal ErrorFrame"
        status: pass
    human_judgment: true
    rationale: "Unit tests exercise SessionLifecycle's teardown-layer mechanics directly (fast, tiny-interval timers) and confirm server.py's real wiring compiles/imports cleanly — but QUOT-05's actual requirement is that a live deployed session torn down by silence, a network blip, or a kill actually frees the concurrency slot and (for the reconnect case) actually resumes the same conversation. That behavioral proof is Task 3 of this plan (checkpoint:human-verify), deferred to the orchestrator."

duration: ~20min
completed: 2026-07-05
status: in-progress
---

# Phase 4 Plan 05: Spoken Wind-Down + Layered Teardown (code) Summary

**Sessions now speak a natural −30s warning via LLM-context injection and a deterministic goodbye via a TTS-bypass frame, then hard-close after a grace cap; abandoned sessions are torn down by three idle layers (transport disconnect + reconnect grace, user-silence watchdog, pipeline stall) that all funnel through one idempotent `release()` — the two `type="auto"` tasks are done and tested; the `checkpoint:human-verify` listen-and-confirm task (Task 3) is deferred to the orchestrator, which drives the redeploy and live session.**

## Performance

- **Duration:** ~20 min (Task 1 + Task 2, `type="auto"` only)
- **Started:** 2026-07-05T21:37:00-04:00 (approx.)
- **Completed:** 2026-07-05T21:47:05-04:00 (Task 2 commit)
- **Tasks:** 2 of 3 (`type="auto"` tasks complete; Task 3 `checkpoint:human-verify` pending)
- **Files modified:** 7 (2 new test files, 5 modified)

## Accomplishments

- **Task 1 — Spoken wind-down** (QUOT-03, D-04/D-05): `pipeline.py` gained `inject_warning_instruction(worker, context, copy)` (pushes a high-priority developer-role message into the `LLMContext` + queues an `LLMRunFrame`, mirroring the existing `greet_now` kick pattern, so the concierge weaves the time warning into its own next turn) and `speak_goodbye(worker, copy)` (a `TTSSpeakFrame` with `append_to_context=False`, recognized directly by the TTS service regardless of pipeline position — a guaranteed LLM-bypass, no prompt-injection surface per T-04-19). `session.py`'s `SessionLifecycle` now reads `winddown_warning_seconds`/`goodbye_grace_seconds`/`warning_copy`/`goodbye_copy` from `QuotaConfig` instead of a hardcoded constant, and guards `on_stop` behind a single-fire latch (`_fire_wind_down`) so the D-02 wall-clock cutoff and D-04's mid-session daily/period-exhaustion hook can never double-invoke the same wind-down sequence.
- **Task 2 — Layered idle teardown + reconnect grace** (QUOT-05, D-06/D-07): `session.py` renamed the previous `stop()` body to `release()` (idempotent core; `stop()` is now a thin back-compat alias) and added three teardown layers: `on_transport_disconnected()`/`on_transport_reconnected()` (a `reconnect_grace_seconds` timer, cancelled by a same-session reconnect within the window), `on_user_speech()` + `_silence_watchdog()` (resets on real speech, releases after `user_silence_timeout` with none), and `on_pipeline_stall()` (immediate release on a fatal `ErrorFrame`, no grace). A new `TeardownObserver` wires layers 2/3 onto real pipecat frames via the same non-intrusive observer seam `LatencyReportObserver` already uses. `server.py` was restructured so `_run_session` builds the pipeline *before* the service timer can fire (closing 04-04's deliberately-left-open coupling gap), assigns `lifecycle.on_warning`/`on_stop` to the real `inject_warning_instruction`/`speak_goodbye` calls, registers `on_client_disconnected`/`on_client_connected` → the new reconnect-grace methods, and sets a new `on_released` hook (→ `WorkerRunner.cancel`) so an idle-teardown layer actually ends the running pipeline (STT/LLM/TTS spend), not just the DB/metric bookkeeping.
- **Task 3 — Listen-and-confirm on a deployed session** (`checkpoint:human-verify`, `gate="blocking"`): **not started by this executor.** It requires redeploying the voice service and holding real sessions to confirm by ear/behavior that the warning sounds natural, the goodbye is clean, and all three teardown layers + reconnect grace actually free the concurrency slot — none of which this code-only, no-deploy executor can do. Deferred to the orchestrator per this plan's explicit instructions.

## Task Commits

Each `type="auto"` task was committed atomically:

1. **Task 1: Spoken wind-down — natural warning (LLM inject) + deterministic goodbye (TTS bypass)** - `f5f3b4e` (feat)
2. **Task 2: Three idle-teardown layers + reconnect grace atop the wall-clock bound** - `dc2b64b` (feat)

**Task 3 (checkpoint:human-verify) is not committed — it is a live-session listen-and-confirm step, deferred to the orchestrator.**

This plan runs on the main working tree (sequential executor, no worktree).

## Files Created/Modified

- `apps/voice/src/klanker_voice/pipeline.py` - `inject_warning_instruction()`, `speak_goodbye()`
- `apps/voice/src/klanker_voice/session.py` - `SessionLifecycle.release()` (idempotent core; `stop()` alias), `_fire_wind_down()`, `on_transport_disconnected`/`on_transport_reconnected`, `on_user_speech`/`_silence_watchdog`, `on_pipeline_stall`, `_reconnect_grace`, `on_released` hook, `TeardownObserver`
- `apps/voice/server.py` - `_run_session(connection, lifecycle)` now builds the pipeline first and wires all lifecycle callbacks + transport event handlers; `_start_and_run_tracked_session` passes `lifecycle` through
- `apps/voice/src/klanker_voice/config.py` - `QuotaConfig` gains `winddown_warning_seconds`, `goodbye_grace_seconds`, `user_silence_timeout`, `reconnect_grace_seconds`, `warning_copy`, `goodbye_copy` (+ `DEFAULT_WARNING_COPY`/`DEFAULT_GOODBYE_COPY`); `load_quota_config()` parses/validates all six with sensible defaults so existing `[quota]`-table tests (which predate these fields) are unaffected
- `apps/voice/pipeline.toml` - new `[quota]` knobs + real copy strings (not code comments, per the plan's negative-grep note)
- `apps/voice/tests/test_winddown.py` (new) - 6 tests: pure `pipeline.py` helper behavior + real-event-loop `SessionLifecycle` wiring
- `apps/voice/tests/test_teardown.py` (new) - 11 tests: each teardown layer, reconnect-within-grace cancellation, idempotency under concurrent triggers, `TeardownObserver` frame routing, `on_released` funnel-through

## Decisions Made

See frontmatter `key-decisions`. Highlights:
- `release()`/`stop()` split preserves 04-04's existing test/call-site contract while giving the new teardown layers the literally-named `release()` the plan's `<threat_model>`/`<artifacts_produced>` text calls for.
- `SessionLifecycle` still never imports `server.py` or holds a transport/worker reference (04-04's design intent, restated in the module docstring) — the coupling is closed entirely from server.py's side, via closures assigned onto the lifecycle's callback fields after the real pipeline exists.
- Goodbye grace is a flat sleep cap, not a bot-stopped-speaking race — simpler and deterministic; flagged as a possible future refinement, not a gap in the required behavior (the plan asks for a cap, which this delivers exactly).
- `on_released` is a small addition beyond the plan's literal action text, needed for actual correctness: without it, an idle-teardown layer would free the DB/metric slot while the real pipeline (and its STT/LLM/TTS connections) kept running unattended. Documented here as a Rule 2 (missing critical functionality) addition.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Critical] Added an `on_released` hook so idle-teardown layers actually stop the running pipeline, not just the DB/metric bookkeeping**
- **Found during:** Task 2, while wiring the three idle-teardown layers into `server.py`.
- **Issue:** The plan's `<action>` text for Task 2 describes each idle-teardown layer calling `release()`, and `release()` (as specified) only touches quota/metric/scale-in bookkeeping. `SessionLifecycle` deliberately holds no worker/transport reference, so nothing would actually end the still-running `WorkerRunner` — an abandoned session (silence, a stalled pipeline, or a transport drop past the reconnect grace) would free its concurrency slot administratively while its real STT/LLM/TTS connections kept running and burning spend, directly contradicting this phase's stated goal ("a public mic wired to metered APIs... so a crash or race can't leak spend or strand a slot").
- **Fix:** Added `SessionLifecycle.on_released: Callback | None = None`, invoked once at the end of `release()` (after the existing idempotency guard, so it also only fires once). `server.py` sets `lifecycle.on_released = runner.cancel` — `WorkerRunner.cancel` is pipecat's own documented, idempotent hangup call.
- **Files modified:** `apps/voice/src/klanker_voice/session.py`, `apps/voice/server.py` (already in this plan's working set).
- **Verification:** `apps/voice/tests/test_teardown.py::test_on_released_hook_fires_exactly_once_regardless_of_trigger` (asserts the hook fires exactly once under two racing callers); full suite green.
- **Committed in:** `dc2b64b` (Task 2 commit).

---

**Total deviations:** 1 auto-fixed (Rule 2, caught and fixed before commit).
**Impact on plan:** Necessary for the plan's own stated correctness goal (a torn-down session must not keep spending). No scope creep beyond that one hook.

## Known Stubs

None — `inject_warning_instruction`/`speak_goodbye`/all teardown layers are wired to real pipecat frames and the real worker/transport, not stubbed data. The one thing genuinely unproven by this executor is *how it sounds/behaves live*, which is exactly Task 3.

## Issues Encountered

None beyond the `on_released` gap documented above.

## User Setup Required

None for local test execution — dynamodb-local (already running on `localhost:8888` from 04-04) is the only dependency, same as prior plans in this phase. For the live deployment this plan's Task 3 depends on: no new SSM secrets or environment variables are required beyond what 04-03/04-04 already wired.

## Next Phase Readiness

- **Task 3 of this plan (checkpoint:human-verify) is the next step** — the orchestrator must redeploy the voice service with this plan's changes (Phase-2 build/deploy path) and hold real sessions to confirm: the −30s warning sounds natural (not robotic/canned), the goodbye plays cleanly within ~5s then the call ends and the mic disconnects, silence for ~60s ends a session, a brief (<10s) network kill reconnects into the same session while a longer kill ends it, the concurrency slot frees each time (a new session can start immediately after), and (optionally) mid-session daily exhaustion fires the same wind-down. **QUOT-03/QUOT-05 are not marked complete in REQUIREMENTS.md and ROADMAP.md is not advanced until that checkpoint is approved.**
- **04-06 (kv usage/killswitch/operator loop):** unaffected by this plan — still unblocked from 04-04, reads the same `UsageDaily`/`UsageRollup`/`UsageControl` items this plan didn't touch.

---
*Phase: 04-voice-service-deployed-quota-enforcement*
*Completed (auto tasks only — Task 3 pending): 2026-07-05*

## Self-Check: PASSED

- FOUND: apps/voice/src/klanker_voice/pipeline.py
- FOUND: apps/voice/src/klanker_voice/session.py
- FOUND: apps/voice/server.py
- FOUND: apps/voice/src/klanker_voice/config.py
- FOUND: apps/voice/pipeline.toml
- FOUND: apps/voice/tests/test_winddown.py
- FOUND: apps/voice/tests/test_teardown.py
- FOUND commit: f5f3b4e (Task 1)
- FOUND commit: dc2b64b (Task 2)

---
## Task 3 — Wind-down/teardown live behavior (DEFERRED to Phase 5 real-device pass)

Code + 151 unit tests complete (hybrid warning + deterministic goodbye, three teardown layers, reconnect grace). The remaining verification is human-sensory — "confirm the goodbye sounds natural" and network-drop teardown/reconnect on a real audio session — which requires the browser client (Phase 5, CLNT-*). This matches the design's own deferral of real-device UX verification to Phase 5. QUOT-03/QUOT-05 logic is unit-verified; live behavioral sign-off rides the Phase-5 device pass.
