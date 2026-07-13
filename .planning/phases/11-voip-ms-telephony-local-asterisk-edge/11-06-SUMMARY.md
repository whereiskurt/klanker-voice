---
phase: 11-voip-ms-telephony-local-asterisk-edge
plan: 06
subsystem: telephony
tags: [pipecat, frame-processor, quota, security, dtmf, asterisk, ari]

# Dependency graph
requires:
  - phase: 11-voip-ms-telephony-local-asterisk-edge (11-05)
    provides: AsteriskCallController + ActiveCall registry, the single idempotent _close_active_call teardown
  - phase: 11-voip-ms-telephony-local-asterisk-edge (11-01)
    provides: TelephonyConfig (gate_mode/gate_window_seconds/unlock_tier_id already present, D-09)
provides:
  - GateProcessor (FrameProcessor) -- the §24 silent answer-gate, inline in the persistent pipeline
  - match_passphrase / accumulate_dtmf pure functions (order-independent set match; early-exit DTMF buffer)
  - pipeline.build_pipeline(..., gate_processor=...) additive seam (WebRTC path unaffected)
  - call_runtime.create_call_session(..., gate_processor=...) + CallSession.context field
  - session.SessionLifecycle.upgrade_from_bypass() -- promotes a bypass placeholder lifecycle to real accounting at unlock
  - AsteriskCallController gated on_stasis_start flow (require_gate branch), DTMF unlock (on_channel_dtmf_received),
    fail-closed teardown (_gate_fail_closed), the real tier grant at unlock (_gate_unlock)
affects: [11-07 (standalone telephony entrypoint + live SIP integration)]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "GateProcessor occupies the exact KnowledgeRouterProcessor architectural slot (after stt, before
      duplex/router) -- redaction by NEVER calling push_frame for locked-state transcription/speaking
      frames, not a later scrub"
    - "Bypass-placeholder GateResult (bypass_accounting=True, zeroed tier) lets create_call_session build
      the persistent pipeline + a SessionLifecycle up front with NO real accounting/timer engaged, then
      SessionLifecycle.upgrade_from_bypass() mutates that SAME object in place at unlock -- no second
      lifecycle ever constructed for one call"
    - "DTMF PIN comparison lives entirely at the controller/ARI-event layer (accumulate_dtmf, a pure
      buffer-compare helper) -- the PIN never touches the pipeline's frame stream"

key-files:
  created:
    - apps/voice/src/klanker_voice/telephony/gate.py
    - apps/voice/tests/test_telephony_gate.py
  modified:
    - apps/voice/src/klanker_voice/pipeline.py
    - apps/voice/src/klanker_voice/call_runtime.py
    - apps/voice/src/klanker_voice/session.py
    - apps/voice/src/klanker_voice/telephony/controller.py
    - apps/voice/tests/test_telephony_lifecycle.py

key-decisions:
  - "Task 1 spike CONFIRMED the persistent-pipeline design (Open Question 1): verified against installed
    pipecat 1.5.0 + telephony/transport.py that TelephonyInputTransport/TelephonyOutputTransport route
    stop()/cancel()/cleanup() through one _teardown() that closes the RTP socket -- a gate-pipeline-then-
    full-pipeline sequential design would tear down the live call mid-way. One persistent Pipeline/
    PipelineWorker/CallSession with GateProcessor inline is the only safe design."
  - "Open Question 5 CONFIRMED: the gate's fail-closed timer is a self-contained asyncio task scoped to
    GateProcessor itself, not SessionLifecycle -- it genuinely runs and can fire BEFORE any real
    SessionLifecycle accounting begins (D-05d), since the real lifecycle only gains real timers via
    upgrade_from_bypass() at unlock."
  - "GateResult bypass-placeholder + SessionLifecycle.upgrade_from_bypass(): rather than deferring
    CallSession/pipeline construction until unlock (which would require a second pipeline build, ruled
    out by the R5 finding above), create_call_session is called ONCE at answer time with a zeroed
    bypass_accounting=True GateResult -- SessionLifecycle.start() then skips its tick/timer/watchdog
    loops entirely while gated. upgrade_from_bypass() (session.py, Rule 2 auto-add, same precedent as
    07-05's remaining_seconds()) mutates tier/session_id/user_id/bypass_accounting in place and starts
    the loops start() itself would have started, at the real unlock moment -- the SAME lifecycle object
    the TeardownObserver/on_released wiring already references, so no second lifecycle is ever built."
  - "telephony_cfg.require_gate branches on_stasis_start into two flows: gated (default/production,
    the new §24 flow) and ungated (require_gate=False, Plan 05's interim immediate-grant behavior
    preserved byte-for-byte -- TelephonyConfig's own docstring already documented this as a
    test/dev-only escape hatch). test_telephony_lifecycle.py's _telephony_cfg() helper now defaults
    require_gate=False so every pre-existing bridge/teardown-plumbing test (not itself testing the
    gate) stays green unchanged; the gated flow gets its own dedicated test section in the same file."
  - "_gate_fail_closed does NOT call ari.hangup() explicitly -- _close_active_call's cascade
    (call_session.close() -> lifecycle.release() -> the composed on_released hook, already wired to
    hang up the SIP channel for the R6 hard-timeout case) does it exactly once. A real bug (found while
    writing the quota-denied-after-unlock test): an explicit hangup call here double-hung-up the
    channel via that same cascade."
  - "gate_mode='dtmf' passes an EMPTY passphrase_words set into GateProcessor (match_passphrase never
    matches an empty secret set) rather than a second code path; gate_mode='passphrase' similarly
    short-circuits the DTMF handler at the top via a gate_mode check -- one GateProcessor/one DTMF
    handler implementation, config-gated, not two parallel gate implementations."

patterns-established:
  - "loguru -> stdlib logging/caplog bridge fixture (test_telephony_gate.py's loguru_caplog): this
    codebase logs via loguru everywhere, which bypasses stdlib logging/caplog by default -- any future
    plan needing a log-content assertion can reuse this exact PropagateHandler pattern."

requirements-completed: [D-05]

coverage:
  - id: D1
    description: "GateProcessor stays silent (no greeting/LLM/TTS) until the caller proves access via DTMF PIN or a 4-word passphrase; STT runs during the gate"
    requirement: "D-05"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_gate.py#test_locked_window_swallows_all_gated_frame_types"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_lifecycle.py#test_gated_stasis_start_stays_locked_no_quota_no_greet"
        status: pass
    human_judgment: false
  - id: D2
    description: "Both factors unlock (default gate_mode='either'): order-independent 4-word passphrase matched inside the pipeline, DTMF PIN matched entirely at the controller/ARI layer"
    requirement: "D-05"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_gate.py#test_passphrase_split_across_two_frames_in_any_order_unlocks"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_lifecycle.py#test_gated_dtmf_unlock_never_touches_pipeline"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_gate.py#test_dtmf_unlock_via_direct_unlock_call"
        status: pass
    human_judgment: false
  - id: D3
    description: "On unlock: real quota.start_gate grant via the minimal SessionIdentity seam, then greet_now -> LLM -> TTS -- the greeting fires on unlock, not on answer"
    requirement: "D-05"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_lifecycle.py#test_gated_passphrase_unlock_grants_tier_and_greets"
        status: pass
    human_judgment: false
  - id: D4
    description: "Fail-closed: gate-window expiry with no unlock, or a quota rejection right after unlock, both play a static goodbye and hang up -- never a silent open PSTN call"
    requirement: "D-05"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_gate.py#test_fail_closed_fires_exactly_once_on_timer_expiry"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_lifecycle.py#test_gate_fail_closed_on_window_expiry_no_greet"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_lifecycle.py#test_gate_fail_closed_on_quota_denied_after_unlock"
        status: pass
    human_judgment: false
  - id: D5
    description: "Structural redaction boundary: pre-unlock transcript never forwarded downstream (never LLM/ledger/logs); no secret word/PIN/raw utterance/partial-match count ever logged"
    requirement: "D-05"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_gate.py#test_locked_window_swallows_all_gated_frame_types"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_gate.py#test_unlock_and_fail_closed_never_log_secrets_or_transcript"
        status: pass
    human_judgment: false
  - id: D6
    description: "WebRTC regression: build_pipeline(gate_processor=None) stays byte-identical processor order; full existing suite still green"
    requirement: "D-05"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_regreet_suppression.py, test_rtvi.py, test_telephony_transport.py (build_pipeline callers)"
        status: pass
      - kind: other
        ref: "cd apps/voice && uv run pytest -q -> 399 passed, 53 skipped, 0 failed"
        status: pass
    human_judgment: false

# Metrics
duration: 30min
completed: 2026-07-12
status: complete
---

# Phase 11 Plan 06: The §24 Silent Answer-Gate Summary

**GateProcessor -- an inline FrameProcessor that keeps a PSTN call dark (no greeting/LLM/TTS) until DTMF PIN or a 4-word passphrase unlocks it, with a bypass-placeholder GateResult + SessionLifecycle.upgrade_from_bypass() deferring all real quota/accounting to that unlock moment.**

## Performance

- **Duration:** ~30 min
- **Started:** 2026-07-12T00:48Z (right after 11-05's completion commit)
- **Completed:** 2026-07-12T01:15Z
- **Tasks:** 3 (Task 1 spike folded into Task 2's commit -- see Deviations)
- **Files modified:** 7 (2 created, 5 modified)

## Accomplishments

- **Task 1 (spike, confirmed):** verified against installed pipecat 1.5.0 + this repo's own
  `telephony/transport.py` that a gate-pipeline-then-full-pipeline sequential design would close
  the live RTP socket mid-call (`_teardown()` cascades from `stop()`/`cancel()`/`cleanup()`).
  Confirmed the one-persistent-pipeline-with-inline-GateProcessor design (Open Question 1) and
  that the gate's own fail-closed timer runs independently of (and before) any real
  `SessionLifecycle` (Open Question 5). Findings recorded in `gate.py`'s module docstring.
- **Task 2:** `klanker_voice.telephony.gate.GateProcessor` + `match_passphrase` (order-independent
  set match) + `accumulate_dtmf` (pure, early-exit buffer compare) + a self-contained fail-closed
  `asyncio` timer. While locked, `process_frame` never calls `push_frame` for
  `TranscriptionFrame`/`InterimTranscriptionFrame`/`UserStartedSpeakingFrame`/
  `UserStoppedSpeakingFrame` -- the structural redaction boundary. `unlock(method)` is idempotent
  and callable both internally (passphrase match) and externally (the controller's DTMF path).
  Logs only `unlocked{method, call_id}` / `gate fail-closed call_id=...`. 20 new tests, including a
  loguru->caplog bridge fixture proving no secret/PIN/transcript ever appears in a log record.
- **Task 3:** wired the gate end-to-end. `pipeline.build_pipeline` gains an additive
  `gate_processor` param (inserted after `stt`, before duplex/router -- `None` reproduces the
  byte-identical WebRTC order). `call_runtime.create_call_session` threads it through, skips
  `register_greet_first` when gated, and `CallSession` gains a `context` field so the controller can
  call `greet_now` itself at the real unlock boundary. `session.SessionLifecycle` gains
  `upgrade_from_bypass()` (Rule 2 auto-add) to promote an already-`start()`-ed bypass placeholder
  into a real metered session in place, at unlock. `telephony.controller.AsteriskCallController`'s
  `on_stasis_start` now branches on `telephony_cfg.require_gate`: the gated flow builds the
  persistent pipeline immediately (bypass placeholder, no real accounting while locked), runs it as
  a background task, and defers the real `quota.start_gate` call + `greet_now` to unlock
  (`_gate_unlock`); a new `on_channel_dtmf_received` ARI handler accumulates digits and calls
  `gate.unlock("dtmf")` on an exact PIN match, entirely outside the pipeline. Both fail-closed
  triggers (gate-window expiry, quota rejection right after unlock) route through
  `_gate_fail_closed` -> the existing single idempotent `_close_active_call` teardown.

## Task Commits

Each task was committed atomically (TDD for Task 2, per its `tdd="true"` frontmatter):

1. **Task 1 (spike) + Task 2 RED: add failing test for §24 answer-gate** - `90f10fc` (test)
2. **Task 2 GREEN: implement §24 answer-gate GateProcessor** - `a4c128c` (feat) -- Task 1's spike
   findings are documented in this commit's `gate.py` module docstring (no separate diff exists
   for a pure investigation task; see Deviations)
3. **Task 3: wire the §24 gate into pipeline + controller** - `20b6714` (feat)
4. **Task 3 (test extension): controller-level integration tests** - `a60a269` (test) -- includes
   a real bug fix found while writing these tests (see Deviations)

**Plan metadata:** (this commit)

## Files Created/Modified

- `apps/voice/src/klanker_voice/telephony/gate.py` - `GateProcessor`, `match_passphrase`,
  `accumulate_dtmf`; the Task-1 architecture confirmation lives in the module docstring
- `apps/voice/tests/test_telephony_gate.py` - 20 hermetic unit tests for the gate in isolation
- `apps/voice/src/klanker_voice/pipeline.py` - additive `gate_processor` param on `build_pipeline`
- `apps/voice/src/klanker_voice/call_runtime.py` - `gate_processor` threaded through
  `create_call_session`; `CallSession.context` field; `register_greet_first` skipped when gated
- `apps/voice/src/klanker_voice/session.py` - `SessionLifecycle.upgrade_from_bypass()` (Rule 2
  auto-add)
- `apps/voice/src/klanker_voice/telephony/controller.py` - gated/ungated `on_stasis_start` branch,
  `_finish_stasis_start_gated`, `_gate_unlock`, `_gate_fail_closed`, `on_channel_dtmf_received`,
  `_bypass_gate_result`, PIN/passphrase-word env reading at construction
- `apps/voice/tests/test_telephony_lifecycle.py` - `_telephony_cfg()` now defaults
  `require_gate=False` (preserves every pre-existing test unchanged); new gated-flow test section
  (12 new tests)

## Decisions Made

See `key-decisions` in the frontmatter above for the full list. Headline: the bypass-placeholder
`GateResult` + `SessionLifecycle.upgrade_from_bypass()` pattern lets one `create_call_session` call
build the whole persistent pipeline up front (satisfying the R5 architectural constraint) while
still deferring ALL real accounting/timers/quota consumption to the actual unlock moment (D-05d) --
without ever constructing a second `SessionLifecycle` or a second pipeline.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] `_gate_fail_closed` double-hung-up the SIP channel**
- **Found during:** Task 3 (writing `test_gate_fail_closed_on_quota_denied_after_unlock`)
- **Issue:** `_gate_fail_closed` called `ari.hangup(sip_channel_id)` explicitly, then
  `_close_active_call`, whose `call_session.close()` -> `lifecycle.release()` cascades into the
  already-wired `on_released` hook (the R6 hard-timeout composition), which ALSO hangs up the same
  channel -- a real double-hangup (harmless against real ARI, a swallowed 404, but wasteful and
  incorrect against the fake test client, which caught it).
- **Fix:** Removed the explicit hangup call from `_gate_fail_closed`; the `on_released` cascade is
  now the single hangup path, documented in the method's docstring.
- **Files modified:** `apps/voice/src/klanker_voice/telephony/controller.py`
- **Verification:** `test_gate_fail_closed_on_window_expiry_no_greet` and
  `test_gate_fail_closed_on_quota_denied_after_unlock` both assert `hangup(sip_channel_id) == 1`.
- **Committed in:** `a60a269` (part of the Task 3 test-extension commit)

**2. [Rule 2 - Missing Critical] `SessionLifecycle.upgrade_from_bypass()` added to session.py**
- **Found during:** Task 3 (wiring the real tier grant to the actual §24 unlock moment, D-05a/c)
- **Issue:** `session.py` is not in this plan's declared `files_modified`, but no existing seam let
  a `SessionLifecycle` constructed as a `bypass_accounting=True` placeholder (necessary so
  `create_call_session` can build the persistent gated pipeline up front, per the Task-1-confirmed
  architecture, with zero real accounting engaged while locked) later gain real
  tick/timer/watchdog/tier state once `quota.start_gate` actually grants a tier at unlock.
- **Fix:** Added `upgrade_from_bypass(*, tier, session_id, user_id)`: mutates the dataclass's
  fields in place (not frozen) and starts the loops `start()` itself would have started had
  `bypass_accounting` been False from the beginning, guarded by the same `_stopped` check `start()`
  uses. Same precedent as 07-05's `remaining_seconds()` addition to this same file for an
  analogous "no existing accessor" reason.
- **Files modified:** `apps/voice/src/klanker_voice/session.py`
- **Verification:** `test_gated_passphrase_unlock_grants_tier_and_greets` asserts
  `lifecycle.bypass_accounting is False` post-unlock with the correct tier/identity threaded
  through; full `test_session.py` suite still green (unaffected).
- **Committed in:** `20b6714` (part of the Task 3 wiring commit)

---

**Total deviations:** 2 auto-fixed (1 Rule 1 bug fix, 1 Rule 2 missing-critical-functionality
auto-add). **Impact on plan:** Both were necessary for correctness of the exact behavior D-05
requires (never a silent open PSTN call; real accounting genuinely deferred to unlock). No scope
creep -- neither changes WebRTC behavior or any file outside the telephony surface.

## Issues Encountered

- **Task 1's own literal deliverable ("record the confirmed design ... in the `gate.py` module
  docstring") doesn't exist as a file until Task 2 creates it.** Resolved by folding Task 1's
  investigation output directly into `gate.py`'s module docstring as part of the Task 2 GREEN
  commit -- there is no separate Task-1-only diff, since a pure investigation task with no planned
  file changes has nothing else to commit. Documented here rather than silently merging the tasks.
- **Architectural ambiguity in the plan's own text** ("Thread an optional `gate_processor` param
  through `create_call_session`" vs. D-05d's "the metered turn loop never engages until a pass" vs.
  needing STT live during the gate) required resolving a real design question the plan left implicit:
  how can `create_call_session` be called exactly once (as instructed) while still deferring all
  real quota/accounting to unlock? Resolved via the bypass-placeholder + `upgrade_from_bypass()`
  pattern (see Decisions above) -- confirmed against the R5 architecture constraint and tested
  end-to-end.

## User Setup Required

None - no external service configuration required. `TELEPHONY_ACCESS_PIN` /
`TELEPHONY_PASSPHRASE_WORDS` are read from env (already documented in 11-CONTEXT.md D-09/D-05e) by
`AsteriskCallController`'s constructor; setting real values for a live harness run is Plan 07's
concern (the standalone entrypoint that actually starts the controller against a real Asterisk
instance), not this plan's.

## Next Phase Readiness

Plan 07 (standalone telephony entrypoint + live SIP integration + the §19-C exit-criterion proof)
can now wire `AsteriskCallController` (fully gate-aware) into a real process against a real
Asterisk instance. Nothing here blocks it: the gate is additive/optional at every seam
(`build_pipeline`, `create_call_session`), the WebRTC path is verified byte-unchanged (full 399-test
suite green), and `require_gate`/`gate_mode`/`gate_window_seconds`/`unlock_tier_id` are already
config-driven via the existing `TelephonyConfig` (11-01). One open item for Plan 07 to resolve
operationally (not a code gap): the controller currently reads `TELEPHONY_ACCESS_PIN` /
`TELEPHONY_PASSPHRASE_WORDS` from env at construction -- Plan 07's entrypoint must actually set
these for the harness/manual softphone proof documented in 11-CONTEXT.md D-07.

## Self-Check: PASSED

All files created/modified verified present on disk; all 4 task commit hashes
(`90f10fc`, `a4c128c`, `20b6714`, `a60a269`) verified present in git log.

---
*Phase: 11-voip-ms-telephony-local-asterisk-edge*
*Completed: 2026-07-12*
