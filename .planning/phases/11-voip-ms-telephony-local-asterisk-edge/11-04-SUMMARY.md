---
phase: 11-voip-ms-telephony-local-asterisk-edge
plan: 04
subsystem: telephony
tags: [aiohttp, ari, asterisk, websocket, event-dispatch]

requires:
  - phase: 11-voip-ms-telephony-local-asterisk-edge
    plan: 01
    provides: "TelephonyConfig/load_telephony_config -- media + gate knobs the controller (Plan 05) will pair with this client"
provides:
  - "AriClient: raw-aiohttp ARI REST client (answer/create_external_media/create_bridge/add_channel/hangup/destroy_bridge)"
  - "AriClient.on()/run(): events-WebSocket dispatch loop routing StasisStart/ChannelDtmfReceived/ChannelDestroyed to registered handlers"
  - "AriError: typed, credential-safe REST failure exception"
affects: [11-05, 11-06, 11-07]

tech-stack:
  added: []
  patterns:
    - "AriClient wraps raw aiohttp.ClientSession (D-06) -- no third-party ARI library; session-level BasicAuth applies automatically to both REST and ws_connect (aiohttp falls back to ClientSession's _default_auth when no per-call auth is passed)"
    - "One _request(method, path, **params) helper handles both JSON-bodied (externalMedia/bridges creation) and 204-No-Content (answer/addChannel/hangup/destroy) ARI responses uniformly"
    - "Events dispatch loop: unregistered event types and handler exceptions are both caught/logged, never fatal -- one bad event/handler cannot kill call control (T-11-04-02)"

key-files:
  created:
    - apps/voice/src/klanker_voice/telephony/ari.py
    - apps/voice/tests/test_telephony_ari.py
  modified:
    - apps/voice/pyproject.toml
    - apps/voice/uv.lock

key-decisions:
  - "aiohttp.ClientSession(auth=BasicAuth(...)) is constructed once in connect(); ws_connect() is called WITHOUT an explicit auth= kwarg -- verified against the installed aiohttp 3.14.1 source that session-level default auth is applied automatically to ws_connect the same way it is to REST requests (auth = auth or self._default_auth), and explicitly passing auth= to ws_connect is now deprecated in this aiohttp version"
  - "DTMF accumulation is NOT implemented in AriClient (Landmine 5) -- run() dispatches one ChannelDtmfReceived event per digit verbatim; the controller (Plan 05) owns accumulating digits across its own gate window"

patterns-established:
  - "AriError(status, path) carries only HTTP status + request path, never the password/Basic-Auth header -- proven by a test asserting the configured password string is absent from str(exc)"

requirements-completed: [D-06, D-01]

coverage:
  - id: D1
    description: "Each of the six REST methods issues the correct method+path+params and returns the documented id (externalMedia/bridge) or completes silently (answer/addChannel/hangup/destroy)"
    requirement: "D-06"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_ari.py#test_answer_posts_to_correct_path"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_ari.py#test_create_external_media_sends_only_supported_param_values_and_returns_id"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_ari.py#test_create_bridge_posts_mixing_type_and_returns_id"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_ari.py#test_add_channel_posts_bridge_and_channel_ids"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_ari.py#test_hangup_deletes_channel"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_ari.py#test_destroy_bridge_deletes_bridge"
        status: pass
    human_judgment: false
  - id: D2
    description: "externalMedia params include the only-supported values (connection_type=client, encapsulation=rtp, transport=udp, direction=both, format=ulaw)"
    requirement: "D-06"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_ari.py#test_create_external_media_sends_only_supported_param_values_and_returns_id"
        status: pass
    human_judgment: false
  - id: D3
    description: "A non-2xx REST response raises AriError and the message never leaks the password"
    requirement: "D-06"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_ari.py#test_non_2xx_response_raises_ari_error_without_leaking_password"
        status: pass
      - kind: manual
        ref: "grep -rni 'password|basicauth' apps/voice/src/klanker_voice/telephony/ari.py -- password only ever passed to aiohttp.BasicAuth"
        status: pass
    human_judgment: false
  - id: D4
    description: "on()/run() dispatch StasisStart/ChannelDtmfReceived/ChannelDestroyed to registered handlers in order with parsed dicts"
    requirement: "D-01"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_ari.py#test_run_dispatches_registered_handlers_in_order_with_parsed_dicts"
        status: pass
    human_judgment: false
  - id: D5
    description: "An unregistered event type and a handler exception both leave the dispatch loop running to the next frame"
    requirement: "D-01"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_ari.py#test_unregistered_event_type_is_ignored_not_fatal"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_ari.py#test_handler_exception_is_caught_and_loop_continues_to_next_frame"
        status: pass
    human_judgment: false
  - id: D6
    description: "A WS-close frame ends run() cleanly without raising (no busy-loop)"
    requirement: "D-01"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_ari.py#test_ws_close_frame_ends_run_without_raising"
        status: pass
    human_judgment: false

duration: 20min
completed: 2026-07-12
status: complete
---

# Phase 11 Plan 04: ARI Client (REST + Events WebSocket) Summary

**`AriClient` -- a raw-`aiohttp`, no-new-dependency wrapper (D-06) covering the exact six ARI REST calls Phase 11 needs (answer, externalMedia, mixing bridge, addChannel, hangup, destroy bridge) plus the one long-lived events WebSocket, dispatching `StasisStart`/`ChannelDtmfReceived`/`ChannelDestroyed` to registered handlers with credential-safe errors and a hostile-event-tolerant dispatch loop.**

## Performance

- **Duration:** ~20 min
- **Started:** 2026-07-12T00:17:00-04:00
- **Completed:** 2026-07-12T00:25:00-04:00
- **Tasks:** 2 (each its own TDD RED/GREEN commit pair)
- **Files modified:** 4 (2 new, 2 modified)

## Accomplishments

- `klanker_voice.telephony.ari.AriClient(base_url, username, password, app_name)`: holds an `aiohttp.ClientSession` created in `connect()` with `aiohttp.BasicAuth`, closed idempotently via `close()`.
- Six REST methods funneled through one `_request(method, path, **params)` helper that handles both JSON-bodied responses (`create_external_media` → external-media channel id, `create_bridge` → bridge id) and empty `204 No Content` responses (`answer`, `add_channel`, `hangup`, `destroy_bridge`) uniformly.
- `create_external_media` passes exactly the External-Media-only-supported param values (`connection_type=client`, `encapsulation=rtp`, `transport=udp`, `direction=both`) — these are documented by Asterisk as the only values currently supported, not planner-chosen defaults.
- `AriError(status, path)` raised on any non-2xx response — carries only the HTTP status and request path, verified (both by a unit test and a literal `grep`) to never embed the password or Basic-Auth header (T-11-04-01, §13).
- `on(event_type, handler)` / `run()`: `run()` `ws_connect`s to `GET /ari/events?app=<name>&subscribeAll=true` and dispatches every JSON text frame to its registered handler by `event["type"]`. An unregistered event type or a handler that raises both leave the loop running to the next frame (T-11-04-02); a WS close/error frame ends `run()` cleanly with no busy-loop (reconnection policy deliberately deferred to the caller, per R6).
- Confirmed against the installed aiohttp 3.14.1 source that `ClientSession`'s session-level `auth=` (set once in `connect()`) is automatically applied to `ws_connect()` as well as REST calls (`auth = auth or self._default_auth`), so the events WebSocket authenticates without a second, now-deprecated explicit `auth=` kwarg on `ws_connect`.
- Optional explicit `aiohttp>=3.14,<4` dependency line added to `pyproject.toml` (documenting the already-transitive pin, D-06); `uv lock` re-resolved clean (142 packages, no conflicts).

## Task Commits

Each task was committed atomically (TDD RED -> GREEN):

1. **Task 1 RED: failing test** - `61210e7` (test) -- REST-surface fakes + 9 tests: per-method path/params assertions, externalMedia's only-supported values, AriError + no-password-leak, pre-connect RuntimeError
2. **Task 1 GREEN: implementation** - `f7e044c` (feat) -- `AriClient` REST surface (`connect`/`close`/`_request`/`answer`/`create_external_media`/`create_bridge`/`add_channel`/`hangup`/`destroy_bridge`) + `AriError`; explicit `aiohttp` pyproject pin + relocked `uv.lock`; 9/9 pass
3. **Task 2 RED: failing test** - `0559e80` (test) -- events-WS fakes (`_FakeWSMessage`/`_FakeWebSocket`/`_FakeWsSession`) + 5 tests: connect-params, ordered dispatch, unregistered-event tolerance, handler-exception tolerance, clean close-frame exit
4. **Task 2 GREEN: implementation** - `e724ff0` (feat) -- `on()`/`run()`/`_dispatch()`; 14/14 pass; full project suite 367 passed / 53 skipped / 0 failed

**Plan metadata:** (this commit, following)

## Files Created/Modified

- `apps/voice/src/klanker_voice/telephony/ari.py` (created) - `AriClient` + `AriError`; module docstring records the D-06 decision and cites R1's per-library rejection reasoning
- `apps/voice/tests/test_telephony_ari.py` (created) - 14 tests across REST surface (Task 1) and events dispatch (Task 2), entirely fake-`aiohttp`-backed (no real network I/O)
- `apps/voice/pyproject.toml` (modified) - added explicit `aiohttp>=3.14,<4` dependency line (documentation of the transitive pin)
- `apps/voice/uv.lock` (modified) - relocked after the explicit `aiohttp` pin; 142 packages, no resolution conflicts

## Decisions Made

- Session-level `BasicAuth` (set once in `connect()`) covers both REST and the events WebSocket — verified by reading the installed aiohttp 3.14.1 source (`ClientSession._request`/`_ws_connect` both fall back to `self._default_auth` when no per-call `auth` is passed), avoiding a second, now-deprecated explicit `auth=` kwarg on `ws_connect`.
- DTMF accumulation deliberately stays out of `AriClient` (Landmine 5, matches the plan's own `<action>` instruction): `run()` dispatches exactly one `ChannelDtmfReceived` event per digit; the Plan-05 controller owns accumulating digits across its `gate_window_seconds` window.
- Task-implementation ordering: Task 1's GREEN implementation was written with `on()`/`run()` initially included ahead of schedule, then deliberately stripped back out before committing so Task 2's RED tests would genuinely fail first (fail-fast TDD gate) rather than passing immediately against already-present code. No functional impact — final code is identical to what a strict two-pass implementation would produce.

## Deviations from Plan

### Auto-fixed Issues

None — plan executed exactly as written. All six REST methods, the externalMedia param set, `AriError`'s credential-safety, and the events dispatch loop's resilience matched the plan's `<behavior>`/`<action>` sections and 11-RESEARCH.md's R1 without requiring any bug fix, missing-functionality addition, or blocking-issue workaround.

## Issues Encountered

None.

## User Setup Required

None — no external service configuration required. `AriClient` is not yet wired into any controller/entrypoint (that is Plan 05, per D-08); no live Asterisk connection was exercised in this plan (all tests run against in-process fakes standing in for `aiohttp.ClientSession`/`ws_connect`).

## Verification

- `cd apps/voice && uv run pytest tests/test_telephony_ari.py -x -q` — 14 passed (plan's own `<verify>` command, run verbatim for both tasks).
- `grep -rni 'password|basicauth' apps/voice/src/klanker_voice/telephony/ari.py` — password only ever appears next to `aiohttp.BasicAuth(...)`, never a log/format string (plan's own `<verification>` command).
- Full project test suite (`uv run pytest -q` from `apps/voice`): 367 passed, 53 skipped (live-provider/eval-dependent tests, pre-existing and unrelated to this plan), 0 failed.
- `uv run ruff check src/klanker_voice/telephony/ari.py tests/test_telephony_ari.py` — all checks passed.
- `uv lock` after the explicit `aiohttp` pin — resolved 142 packages, no conflicts, no version drift from the existing transitive pin.

## Next Phase Readiness

- `AriClient` is ready for the Plan-05 controller to consume: construct once, `await connect()`, register `StasisStart`/`ChannelDtmfReceived`/`ChannelDestroyed` handlers via `on()`, then `await run()` as the controller's event loop; call the six REST methods from within those handlers (e.g. `answer()` then `create_external_media()` on `StasisStart`, paired with `SocketRtpMediaSession.open()` from 11-03 which must bind BEFORE `create_external_media()` is called, per R2).
- Known carried-forward note (11-02, unresolved by this plan, not required by it): Asterisk's `ari.conf`/`http.conf` bind ARI to `127.0.0.1` only — a host-run controller cannot yet reach ARI through the docker-compose harness's published port. This is explicitly flagged for the controller/entrypoint plan (D-08) to resolve; `AriClient` itself is transport-agnostic (its `base_url` is caller-supplied), so it is not blocked by this.
- No blockers for 11-05 onward.

---
*Phase: 11-voip-ms-telephony-local-asterisk-edge*
*Completed: 2026-07-12*
