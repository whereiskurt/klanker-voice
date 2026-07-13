---
phase: 11-voip-ms-telephony-local-asterisk-edge
plan: 05
subsystem: telephony
tags: [asterisk, ari, pipecat, quota, lifecycle, controller]

# Dependency graph
requires:
  - phase: 11-voip-ms-telephony-local-asterisk-edge (11-01)
    provides: TelephonyConfig / load_telephony_config
  - phase: 11-voip-ms-telephony-local-asterisk-edge (11-03)
    provides: SocketRtpMediaSession (socket-backed RtpMediaSession, D-03)
  - phase: 11-voip-ms-telephony-local-asterisk-edge (11-04)
    provides: AriClient (raw-aiohttp ARI REST + events-WebSocket dispatch, D-06)
provides:
  - AsteriskCallController with an ActiveCall registry keyed by SIP channel id (D-02, Sec13)
  - on_stasis_start allocation: answer -> bind-first socket media session (R2) -> externalMedia
    channel + mixing bridge -> quota gate -> CallSession construction -> tracked background worker
  - _close_active_call: the single idempotent teardown every close trigger funnels through
    (ChannelDestroyed, hard timeout, simultaneous racing calls) -- no leaked bridge/channel/socket
  - Hard-timeout lifecycle.on_released composed with ari.hangup(sip_channel_id) (R6) -- a
    wind-down always reaches the SIP channel, never a silent open PSTN call
  - Quota-denied path: no CallSession constructed, gate bridge/external-media/socket torn down,
    SIP channel hung up (R6 "quota-denied leaves no bridge")
affects: [11-06 (§24 silent answer-gate), 11-07 (standalone telephony entrypoint)]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Dependency-injected media_session_opener (defaults to SocketRtpMediaSession.open) --
      lets tests inject a FakeRtpMediaSession with no real UDP socket"
    - "ActiveCall.lock + closed check-and-set under the lock (mirrors SessionLifecycle._stopped)
      for exactly-once teardown under racing callers"
    - "_safe_ari() wraps every ARI teardown call, swallowing AriError so one already-gone
      Asterisk-side resource never aborts the rest of a teardown sequence"

key-files:
  created:
    - apps/voice/src/klanker_voice/telephony/controller.py
    - apps/voice/tests/test_telephony_lifecycle.py
  modified: []

key-decisions:
  - "quota.start_gate is invoked directly inside on_stasis_start at an interim placement
    (right after bridge/channel allocation, granting telephony_cfg.unlock_tier_id) -- Plan 06
    moves the real grant to the §24 unlock boundary without changing the teardown contract"
  - "rtp_bind_host/rtp_advertise_host/app_name/expected_context are AsteriskCallController
    constructor kwargs with sane defaults, not TelephonyConfig fields -- they are network/process
    plumbing, not the [telephony] table's behavior-only surface (D-09), and stay config-driven
    for the Plan 07 entrypoint to resolve without a code change (11-02's own follow-up note)"
  - "The gate-bridge quota-denied teardown path is NOT routed through _close_active_call --
    no ActiveCall was ever registered for it (R6), so a dedicated _teardown_gate_resources
    helper handles it directly"

patterns-established:
  - "MediaSessionOpener DI seam: AsteriskCallController(..., media_session_opener=...) --
    the same shape a later plan can reuse for any other swappable-resource test seam"

requirements-completed: [D-02, D-04]

coverage:
  - id: D1
    description: "AsteriskCallController + ActiveCall registry keyed by SIP channel id, all §13 fields populated on allocation"
    requirement: "D-02"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_lifecycle.py#test_stasis_start_allocates_and_registers"
        status: pass
    human_judgment: false
  - id: D2
    description: "on_stasis_start rejects unexpected context/app with no allocation"
    requirement: "D-02"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_lifecycle.py#test_unexpected_context_no_allocation"
        status: pass
    human_judgment: false
  - id: D3
    description: "ChannelDestroyed funnels through the single idempotent teardown -- close exactly once, full resource cleanup, empty registry"
    requirement: "D-04"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_lifecycle.py#test_channel_destroyed_closes_exactly_once"
        status: pass
    human_judgment: false
  - id: D4
    description: "Simultaneous hangup+timeout races on the same ActiveCall tear down exactly once"
    requirement: "D-04"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_lifecycle.py#test_simultaneous_close_calls_release_exactly_once"
        status: pass
    human_judgment: false
  - id: D5
    description: "A hard-timeout release ARI-hangs-up the original SIP channel (never a silent open PSTN call)"
    requirement: "D-04"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_lifecycle.py#test_hard_timeout_hangs_up_sip_channel"
        status: pass
    human_judgment: false
  - id: D6
    description: "A quota.start_gate rejection leaves no CallSession and no bridge/external-media channel/socket/registry entry"
    requirement: "D-02"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_lifecycle.py#test_quota_denied_leaves_no_bridge"
        status: pass
    human_judgment: false
  - id: D7
    description: "Live Asterisk end-to-end call through the real controller (this plan's own manual §19-C proof) -- deferred to a later plan/checkpoint"
    verification: []
    human_judgment: true
    rationale: "No live Asterisk instance was run in this plan; only fakes (FakeAriClient/FakeRtpMediaSession) prove the controller's allocation/teardown logic. The real live proof is Plan 07's standalone entrypoint + manual softphone run (D-07)."

# Metrics
duration: 25min
completed: 2026-07-12
status: complete
---

# Phase 11 Plan 05: AsteriskCallController + Lifecycle Teardown Summary

**`AsteriskCallController` (StasisStart allocation + one idempotent teardown funneling ChannelDestroyed/hard-timeout/quota-denied) against a fully faked ARI client and RTP media session — 373/373 project tests green, 0 network calls.**

## Performance

- **Duration:** ~25 min
- **Started:** ~2026-07-12T04:18Z
- **Completed:** 2026-07-12T04:43Z
- **Tasks:** 3
- **Files modified:** 2 (both new)

## Accomplishments

- `telephony/controller.py`: `ActiveCall` dataclass (§13 field shape: `sip_channel_id`,
  `external_media_channel_id`, `bridge_id`, `media_session`, `call_session`, `caller_id`, `did`,
  `created_at`, `closed`, `lock`) + `AsteriskCallController` with the `calls: dict[str, ActiveCall]`
  registry.
- `on_stasis_start`: context/app gate (only `application="klanker"` + `context="from-klanker-inbound"`
  is accepted, matching the 11-02 Asterisk configs — anything else is hung up with zero allocation);
  normalizes ANI/DID; answers the channel; opens the socket media session **before** creating
  Asterisk's External Media channel (R2 bind-first ordering, verified by an explicit call-order
  assertion in the test); creates the mixing bridge; attaches both channels; evaluates
  `quota.start_gate` (interim placement, granting `telephony_cfg.unlock_tier_id`); constructs the
  `CallSession` via `create_call_session(channel="pstn")`; registers the `ActiveCall`; runs the
  worker as a tracked background task (mirrors `server.py`'s `SESSION_TASKS` strong-reference
  pattern, so `asyncio.create_task`'s weak reference can never silently GC a live call).
- `_close_active_call`: the single idempotent teardown every close trigger funnels through —
  `call_session.close()` → `lifecycle.release()` exactly once, then bridge/external-media-channel/
  socket teardown (each wrapped in `_safe_ari`, swallowing `AriError` so one already-gone
  Asterisk-side resource never aborts the rest of the sequence), then the registry entry is popped.
  Guarded by an `asyncio.Lock` + `closed` check-and-set (mirrors `SessionLifecycle._stopped`) so
  racing callers (simultaneous ChannelDestroyed + hard-timeout) tear down exactly once.
- Hard-timeout wiring (R6/T-11-05-02): `lifecycle.on_released` is composed with
  `runner.cancel(...)` (the existing default) **and** `ari.hangup(sip_channel_id)` **and**
  `_close_active_call(...)` — a hard wall-clock cutoff always reaches the SIP channel, never just
  the Klanker-side pipeline.
- Quota-denied path (R6/T-11-05-03): if `quota.start_gate` raises `QuotaError` after the
  bridge/external-media channel are already allocated (unavoidable — the gate needs a live media
  path before any tier decision is possible for a PSTN caller), the controller never constructs a
  `CallSession`; `_teardown_gate_resources` tears down the bridge, external-media channel, and
  socket, then hangs up the SIP channel directly. No `ActiveCall` is ever registered for this path.
- `test_telephony_lifecycle.py`: `FakeAriClient` (records every REST call in order, returns
  predictable ids) + `FakeRtpMediaSession` (no socket) + a `stub_call_session_run` autouse fixture
  that brackets `lifecycle.start()` only — never `runner.run()`, so the real pipeline (and its
  live Deepgram/Anthropic/ElevenLabs provider connections) never starts. 6 tests covering the full
  §16/§17 matrix.

## Task Commits

1. **Task 1+2: AsteriskCallController + ActiveCall registry + single idempotent teardown** -
   `7951b91` (feat)
2. **Task 3: §16 lifecycle unit-test matrix** - `df0374e` (test)

_Note: Tasks 1 and 2 were committed together — both operate on the same single file
(`controller.py`) with tightly-coupled allocation/teardown logic that was authored as one
cohesive unit; splitting them into two artificial partial-file commits would not have reflected
genuine incremental milestones._

**Plan metadata:** _pending (this commit)_

## Files Created/Modified

- `apps/voice/src/klanker_voice/telephony/controller.py` - `ActiveCall` + `AsteriskCallController`
  (`on_stasis_start`, `on_channel_destroyed`, `_close_active_call`, `_teardown_gate_resources`,
  `_safe_ari`, `register`)
- `apps/voice/tests/test_telephony_lifecycle.py` - the §16/§17 lifecycle matrix (6 tests) against
  `FakeAriClient`/`FakeRtpMediaSession`

## Decisions Made

- `quota.start_gate` is called directly inside `on_stasis_start` at an **interim placement**
  (right after the bridge/external-media channel are wired, granting
  `telephony_cfg.unlock_tier_id`) — this plan's job is the allocate/teardown plumbing and its
  exactly-once guarantee, not the real §24 unlock boundary. Plan 06 moves the tier grant to the
  actual DTMF-PIN/passphrase unlock without touching this module's teardown contract.
- `rtp_bind_host` / `rtp_advertise_host` / `app_name` / `expected_context` are
  `AsteriskCallController` constructor keyword arguments with sane defaults (`"0.0.0.0"` /
  `"127.0.0.1"` / `"klanker"` / `"from-klanker-inbound"`), not new `TelephonyConfig` fields — the
  §14 `[telephony]` table (D-09) is behavior-only (media/gate knobs), and network/process
  plumbing like the RTP advertise address needs to stay config-driven at the *entrypoint* layer
  (11-02-SUMMARY.md's own flagged follow-up: "the controller's ARI base URL must remain
  config-driven so that resolution can happen without code changes") — Plan 07 will resolve these
  from env/deployment context, not a hardcoded TOML value.
- The quota-denied gate-bridge teardown is **not** routed through `_close_active_call` — per R6,
  no `ActiveCall` was ever registered for a rejected gate (deliberately, so the registry only ever
  holds calls that reached a real `CallSession`), so a dedicated `_teardown_gate_resources` helper
  handles that one distinct failure state directly.
- Dependency-injected `media_session_opener` (default `SocketRtpMediaSession.open`) lets the §16
  test matrix inject a `FakeRtpMediaSession` — no real UDP socket bound during any lifecycle test,
  and no production code path needed monkeypatching.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Critical] `_safe_ari` wrapper around every teardown-path ARI call**
- **Found during:** Task 2 (single idempotent teardown)
- **Issue:** The plan's literal action text calls `ari.destroy_bridge`/`ari.hangup` directly
  inside `_close_active_call`. In real Asterisk behavior, a bridge can auto-destroy once its last
  channel leaves it, or the external-media channel can already be gone by the time
  `ChannelDestroyed` fires for the original SIP channel — an unguarded second `DELETE` would raise
  `AriError` and abort the rest of the teardown sequence, potentially leaking the socket/registry
  entry (violating T-11-05-01's "no leaked resources" requirement, the plan's own threat-model
  mitigation for this exact task).
- **Fix:** Added `_safe_ari(coro, description)`, catching `AriError` and logging a warning so
  teardown always runs to completion regardless of which Asterisk-side resource is already gone.
  Applied consistently in `_close_active_call` and `_teardown_gate_resources`.
- **Files modified:** `apps/voice/src/klanker_voice/telephony/controller.py`
- **Verification:** All 6 lifecycle tests pass; full suite 373/373.
- **Committed in:** `7951b91` (Task 1+2 commit)

**2. [Rule 3 - Blocking] `stub_call_session_run` fixture to keep the lifecycle test matrix
hermetic**
- **Found during:** Task 3 (writing the test matrix)
- **Issue:** Production `on_stasis_start` correctly spawns `call_session.run()` as a background
  task (per the plan's own action text). `CallSession.run()` calls `runner.run()`, which starts
  the real pipecat pipeline and would attempt live Deepgram/Anthropic/ElevenLabs connections with
  the tests' stub API keys — directly contradicting this plan's own instruction ("do not require
  real Deepgram/Anthropic/ElevenLabs or a live socket") and risking slow/flaky CI runs against a
  sandboxed network.
- **Fix:** An autouse `stub_call_session_run` fixture monkeypatches `CallSession.run` to bracket
  only `lifecycle.start()`, never `runner.run()`. This preserves the exact teardown contract under
  test (`call_session.close()` → `lifecycle.release()`) while eliminating the live provider round
  trip. Production `controller.py` is unaffected — the stub lives entirely in the test module.
- **Files modified:** `apps/voice/tests/test_telephony_lifecycle.py`
- **Verification:** Full suite 373 passed/53 skipped/0 failed in 135s (no network-timeout stalls).
- **Committed in:** `df0374e` (Task 3 commit)

---

**Total deviations:** 2 auto-fixed (1 missing-critical robustness, 1 blocking test-hermeticity fix)
**Impact on plan:** Both were necessary for the plan's own stated correctness/threat-model
requirements (no leaked resources) and its own explicit hermeticity instruction. No scope creep —
no new files beyond the plan's declared `files_modified`.

## Issues Encountered

None beyond the two auto-fixes above.

## User Setup Required

None - no external service configuration required. Everything in this plan is pure Python
against fakes; the real Asterisk/ARI wiring is exercised manually in a later plan (D-07/Plan 07).

## Next Phase Readiness

- The controller's allocate/teardown plumbing is proven against the §16/§17 lifecycle matrix and
  ready for Plan 06 to insert the real §24 silent answer-gate (DTMF PIN / spoken passphrase) at
  the tier-grant point this plan deliberately left as an interim placeholder.
- `AsteriskCallController.register()` is ready for Plan 07's standalone telephony entrypoint to
  wire onto a real, connected `AriClient` — no changes to this plan's public surface expected.
- `rtp_bind_host`/`rtp_advertise_host` remain constructor defaults (`0.0.0.0`/`127.0.0.1`); Plan
  07 must resolve these from the real deployment/dev-harness context (the same 11-02-flagged ARI
  loopback-reachability gap applies here by analogy — host-run vs. container-run controller needs
  the right advertise address).
- `webrtc.py`/`server.py` remain byte-unchanged (`git diff --stat` confirmed empty) — the browser
  path is fully isolated from this plan's work (D-08).

---
*Phase: 11-voip-ms-telephony-local-asterisk-edge*
*Completed: 2026-07-12*

## Self-Check: PASSED

- FOUND: apps/voice/src/klanker_voice/telephony/controller.py
- FOUND: apps/voice/tests/test_telephony_lifecycle.py
- FOUND: .planning/phases/11-voip-ms-telephony-local-asterisk-edge/11-05-SUMMARY.md
- FOUND commit: 7951b91
- FOUND commit: df0374e
