---
phase: 05-browser-client-conference-readiness
plan: 01
subsystem: api
tags: [pipecat, rtvi, fastapi, staticfiles, websocket-events, latency-hud]

# Dependency graph
requires:
  - phase: 04-voice-service-deployed-quota-enforcement
    provides: build_pipeline/build_worker, LatencyReportObserver per-stage timings, server.py FastAPI app with /health + /api/offer
provides:
  - RTVIProcessor placed in the cascade pipeline right after transport.input(), exposed on BuiltPipeline
  - RTVIObserver attached to the worker with audio-level flags on (bot_audio_level_enabled, user_audio_level_enabled)
  - Composed kmv-latency RTVIServerMessageFrame emitted once per finalized turn (stt/llm-ttft/tts-first-audio/v2v/v2v-p50)
  - StaticFiles SPA mount (client/dist) with 404-fallback to index.html for client-side deep links, /api/* excluded
affects: [05-02, 05-03, 05-04, 05-05, 05-06, 05-07]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "RTVI wiring: RTVIProcessor built per-session via a factory, placed immediately after transport.input(); RTVIObserver attached to the worker's observers list alongside existing observers (additive, never replacing)"
    - "Observer-originated frame injection without a threaded processor reference: cache the most recently observed FramePushed.destination in on_push_frame, then call .push_frame(...) on it later — works because every processor's push notifies the same pipeline-wide observer set regardless of origin"
    - "SPA fallback via a global 404 exception handler (not a catch-all route) so a root-mounted StaticFiles(html=True) still yields deep-link fallback without shadowing declared routes registered earlier in the router"

key-files:
  created:
    - apps/voice/src/klanker_voice/rtvi.py
    - apps/voice/tests/test_rtvi.py
    - apps/voice/tests/test_server_static.py
  modified:
    - apps/voice/src/klanker_voice/pipeline.py
    - apps/voice/src/klanker_voice/observers.py
    - apps/voice/server.py

key-decisions:
  - "kmv-latency frame is pushed from the last-observed downstream FrameProcessor (cached in on_push_frame), not from a constructor-injected RTVIProcessor reference — keeps observers.py's change self-contained (matches the plan's Task 2 file scope of observers.py + tests only, no server.py change)"
  - "SPA deep-link fallback implemented via a global @app.exception_handler(404) rather than a second catch-all route — a root-mounted StaticFiles(\"/\") prefix-matches every path in Starlette's router, so a catch-all route registered after it would never be reached; a 404 handler is the correct place to intercept the mount's own 404 and re-serve index.html"
  - "Unknown /api/* paths return a plain 404 JSON, not the SPA fallback (T-05-01-I) — the exception handler explicitly excludes the /api prefix"

patterns-established:
  - "Per-session RTVIProcessor: build_rtvi_processor() constructs a fresh instance per call (never shared across connections), matching every other per-session service in build_pipeline"

requirements-completed: [CLNT-03, CLNT-04, CLNT-06]

coverage:
  - id: D1
    description: "RTVIProcessor placed in the pipeline (right after transport.input()) and RTVIObserver attached to the worker, with audio-level flags on — client-js now receives transcripts, bot/user speaking events, and audio levels over the RTVI data channel (CLNT-03/CLNT-04)"
    requirement: "CLNT-03"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_rtvi.py::TestTask1RTVIPlacement (4 tests)"
        status: pass
    human_judgment: false
  - id: D2
    description: "One composed kmv-latency RTVIServerMessageFrame (stt_ms/llm_ttft_ms/tts_first_audio_ms/voice_to_voice_ms/v2v_p50_ms) pushed per finalized turn — the HUD's live per-turn data source (CLNT-06/D-09)"
    requirement: "CLNT-06"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_rtvi.py::TestTask2KmvLatencyEmission (5 tests)"
        status: pass
    human_judgment: false
  - id: D3
    description: "StaticFiles SPA mount (client/dist) with 404-fallback to index.html for deep-linked client routes (e.g. the OIDC callback route), /api/* excluded from the fallback and unshadowed"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_server_static.py (6 tests)"
        status: pass
    human_judgment: false

duration: 25min
completed: 2026-07-06
status: complete
---

# Phase 5 Plan 01: Server-side RTVI wiring + SPA mount Summary

**RTVIProcessor/RTVIObserver wired into the voice pipeline for transcripts/speaking/audio-levels, a composed per-turn kmv-latency RTVIServerMessageFrame for the HUD, and a StaticFiles SPA mount with 404-based deep-link fallback — all unit-tested, 166/166 total suite passing.**

## Performance

- **Duration:** ~25 min
- **Started:** 2026-07-06T04:24:00Z (approx.)
- **Completed:** 2026-07-06T04:45:43Z
- **Tasks:** 3
- **Files modified:** 6 (3 new, 3 modified)

## Accomplishments

- `build_pipeline` accepts an optional `RTVIProcessor` and places it immediately after `transport.input()` (the standard RTVI placement), exposing it on `BuiltPipeline.rtvi`; `server._run_session` builds one per session via `rtvi.build_rtvi_processor()` and attaches `RTVIObserver(rtvi, params=build_rtvi_observer_params())` to the worker's observers, additive to the existing `LatencyReportObserver` and `TeardownObserver`.
- `build_rtvi_observer_params()` enables `bot_audio_level_enabled` and `user_audio_level_enabled` (the library defaults already cover `bot_speaking_enabled`, `user_transcription_enabled`, `metrics_enabled`) — client-js now has everything it needs for captions (CLNT-03), the audio-reactive orb (CLNT-04), and RTVI metrics.
- `LatencyReportObserver` now pushes one `RTVIServerMessageFrame` (`type: "kmv-latency"`) per finalized turn, carrying `stt_ms`, `llm_ttft_ms`, `tts_first_audio_ms`, `voice_to_voice_ms`, and a running `v2v_p50_ms` — the Phase 5 latency HUD's live data source (CLNT-06/D-09). Additive: the existing harness JSON artifact and p50 table are unaffected.
- `server.py` mounts the built SPA (`client/dist`) via `StaticFiles(html=True)` at the app root (registered after `/health` and `/api/offer` so those routes always win) plus a global `404` exception handler that re-serves `index.html` for any other GET path — the OIDC callback route and any other client-side deep link resolve to the SPA. `/api/*` paths are excluded and 404 normally. Tolerant of a missing `client/dist` (skips the mount, logs once, no import-time crash) since the client SPA source doesn't exist yet (05-02..05-07).

## Task Commits

Each task was committed atomically:

1. **Task 1: Add RTVIProcessor + RTVIObserver so client-js receives transcripts, speaking, and audio levels** - `c44c22a` (feat)
2. **Task 2: Emit one composed per-turn latency message as an RTVIServerMessageFrame for the HUD** - `fda8827` (feat)
3. **Task 3: Mount the built SPA (client/dist) on the voice FastAPI with SPA-fallback for the callback route** - `b8926d8` (feat)

## Files Created/Modified

- `apps/voice/src/klanker_voice/rtvi.py` - New: `build_rtvi_processor()` factory + `build_rtvi_observer_params()` (audio levels on)
- `apps/voice/src/klanker_voice/pipeline.py` - `build_pipeline` accepts optional `rtvi:` param, places it after `transport.input()`; `BuiltPipeline.rtvi` field added
- `apps/voice/src/klanker_voice/observers.py` - `LatencyReportObserver` caches the last-seen downstream processor and pushes a `kmv-latency` `RTVIServerMessageFrame` per finalized turn
- `apps/voice/server.py` - `_run_session` builds/wires the RTVIProcessor + RTVIObserver; new `CLIENT_DIST_DIR` constant + `_mount_client_spa()` SPA mount registered after the API routes
- `apps/voice/tests/test_rtvi.py` - New: `TestTask1RTVIPlacement` (4 tests) + `TestTask2KmvLatencyEmission` (5 tests)
- `apps/voice/tests/test_server_static.py` - New: 6 tests covering the SPA mount, deep-link fallback, and `/api/*`/`/health` non-shadowing

## Decisions Made

- **kmv-latency emission source:** rather than threading an `RTVIProcessor` reference into `LatencyReportObserver`'s constructor (which would have required changing `server.py`'s instantiation of it, outside Task 2's declared file scope), the observer caches the most recently observed `FramePushed.destination` from its own `on_push_frame` and pushes the new frame from that. Any live `FrameProcessor` in the running pipeline works as the origin — pipecat's observer notification is pipeline-wide, not scoped to one processor — so this needed zero changes to `server.py` for Task 2, matching the plan's file list exactly.
- **SPA fallback mechanism:** a root-mounted `StaticFiles("/")` prefix-matches every HTTP path in Starlette's router (confirmed via `Mount.matches()`), so a catch-all route registered *after* it would never be reached — Starlette commits to the first full match found while iterating routes in registration order. A global `@app.exception_handler(404)` correctly intercepts the mount's own 404 (for both missing static files and truly unmatched paths) and re-serves `index.html`, while excluding `/api/*` from the rewrite.

## Deviations from Plan

None — plan executed exactly as written, with one internal design refinement documented above (kmv-latency emission source) that kept the change within the plan's declared per-task file scope rather than expanding it.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- The server-side RTVI contract (transcripts, speaking, audio levels, kmv-latency metrics) and the SPA hosting seam (`client/dist` mount + deep-link fallback) are both in place and unit-tested — Plans 05-02 through 05-07 can now build the actual Vite+React SPA client against a stable, already-wired backend.
- `client/dist` does not exist yet (by design — this plan is server-only); `_mount_client_spa` logs a warning and skips the mount until the client is built, so the app boots cleanly in the interim.
- Manual verification note (non-gating, from the plan's `<verification>` block): once a real browser client connects, confirm transcript + bot-speaking + a `kmv-latency` server-message arrive in the browser console. Not required for this plan's completion; deferred to the client plans' own verification.

---
*Phase: 05-browser-client-conference-readiness*
*Completed: 2026-07-06*

## Self-Check: PASSED

- All 6 created/modified files verified present on disk.
- All 3 task commits (c44c22a, fda8827, b8926d8) verified present in git log.
- Full suite: 166/166 passing; ruff clean.
