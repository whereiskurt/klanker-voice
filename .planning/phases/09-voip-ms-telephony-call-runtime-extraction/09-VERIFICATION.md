---
phase: 09-voip-ms-telephony-call-runtime-extraction
verified: 2026-07-11T00:00:00Z
status: passed
score: 6/6 must-haves verified
behavior_unverified: 0
overrides_applied: 0
---

# Phase 9: VoIP.ms Telephony — Call Runtime Extraction Verification Report

**Phase Goal:** Extract a transport-neutral shared call runtime (`apps/voice/src/klanker_voice/call_runtime.py`) from `server.py` so both the existing WebRTC path and future telephony construct, run, and idempotently close one live voice session through the same seam — a behavior-preserving refactor with the browser voice path unchanged. (Spec Phase A, §6 / §19-A / §21.)

**Verified:** 2026-07-11
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `call_runtime.py` exposes a narrow, transport-neutral API (`CallSession`/`create_call_session`) owning gate-result → lifecycle → build_pipeline → observers → callbacks → greeting → one idempotent close | ✓ VERIFIED | Read `apps/voice/src/klanker_voice/call_runtime.py` in full: `CallIdentity` (frozen dataclass, subject/authenticated/auth_method), `CallSession` (session_id/worker/lifecycle/runner + `run()`/`close()`), `create_call_session(*, transport, identity, gate_result, cfg, knowledge_cfg, duplex_cfg, quota_cfg, channel, metadata)` matching the D-01 signature exactly. Ran the plan's own import+signature check equivalent by direct code read; all 9 named params present. |
| 2 | The WebRTC `/api/offer` path is converted to use the shared runtime; start-gate, lifecycle, observers, greeting, warning+goodbye, reconnect grace, RTVI, ambience are preserved | ✓ VERIFIED | Read `apps/voice/server.py`: `_connection_callback` (inside `_negotiate_webrtc`) builds variant config, ambience mixer + `TransportParams`, constructs `SmallWebRTCTransport`, then calls `create_call_session(..., channel="webrtc", metadata={"pc_id": connection.pc_id})`. `_wire_connection_teardown` wired before task spawn (unchanged, byte-identical). `offer()` still calls `start_gate(identity)` (L329) strictly before `_negotiate_webrtc` (L339) — T-09-01 ordering intact. |
| 3 | Browser voice works exactly as before — every existing lifecycle/quota/greeting/connection-teardown test still passes (spec §19-A) | ✓ VERIFIED | Ran `cd apps/voice && uv run pytest -q` myself (not trusting SUMMARY): **287 passed, 53 skipped, 0 failed** in 134.49s — exact match to SUMMARY's claim. Skips are the pre-existing dynamodb-local-gated pattern (matches test_session.py convention). |
| 4 | `close()` is idempotent and `lifecycle.release()` fires exactly once on worker OR transport termination; WebRTC reconnect-race teardown stays in server.py, not generalized | ✓ VERIFIED | `CallSession.close()` body is exactly `logger.info(...); await self.lifecycle.release()`. Ran `uv run pytest tests/test_call_runtime.py -v`: all 4 named tests (`test_create_call_session_is_transport_neutral`, `test_close_is_idempotent`, `test_release_fires_once_on_worker_termination`, `test_transport_termination_triggers_single_release`) PASSED, asserting exactly-once metric emission / release-hook firing across racing close paths. `_wire_connection_teardown` in server.py is untouched (166-line docstring/comments intact, still registers on the raw `connection.closed` event, not moved into call_runtime.py). |
| 5 | No SIP/Asterisk/RTP/codec/infra code introduced; no provider construction duplicated (factories.py remains single source) | ✓ VERIFIED | Ran `grep -v '^#' src/klanker_voice/call_runtime.py \| grep -c -iE 'aiortc\|smallwebrtc\|asterisk\|\brtp\b\|pcmu\|\bsip\b\|codec\|build_stt\|build_llm\|build_tts'` myself → **0**. Module only imports transport-neutral pipecat base types (`BaseTransport`, `PipelineWorker`, `WorkerRunner`, `RTVIObserver`) plus `klanker_voice` modules; reuses `build_pipeline`/`build_worker`/`factories.py` verbatim, never constructs a provider directly. |
| 6 | Focused tests prove transport-neutral construction/idempotent close/release-once; an architecture + coupling note documents the extracted seam | ✓ VERIFIED | `apps/voice/tests/test_call_runtime.py` (211 lines) defines a genuinely non-WebRTC `FakeTransport(BaseTransport)` stub and the 4 named tests — grep for `SmallWebRTC\|aiortc\|TestClient` in the file matches only docstring prose ("no aiortc... in this test"), never an actual import/usage. The D-08 note is present both in `09-01-SUMMARY.md` ("Architecture + Coupling Note (D-08)" section, 3 documented couplings) and in `call_runtime.py`'s module docstring (same 3 couplings, condensed). |

**Score:** 6/6 truths verified (0 present-but-behavior-unverified)

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `apps/voice/src/klanker_voice/call_runtime.py` | New module: CallIdentity/CallSession/create_call_session | ✓ VERIFIED | Exists, 205 lines, all 3 symbols present with correct shapes; substantive (not a stub) — full body implements the extracted `_run_session` logic verbatim per read-through. |
| `apps/voice/tests/test_call_runtime.py` | 4 focused tests against a fake BaseTransport | ✓ VERIFIED | Exists, 211 lines, 4 named tests all present and passing. |
| Architecture + coupling note (D-08) | In 09-01-SUMMARY.md + call_runtime.py docstring | ✓ VERIFIED | Both locations confirmed by direct read; 3 couplings documented consistently in both places. |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `create_call_session` | `build_pipeline(cfg, transport, ...)` | direct call, L149-156 of call_runtime.py | ✓ WIRED | Confirmed — passes `rtvi`, `knowledge_cfg`, `duplex_cfg`, `remaining_seconds_fn=lifecycle.remaining_seconds`. |
| `CallSession.close()` / `CallSession.run()`'s finally | `SessionLifecycle.release()` / `.stop()` | direct await | ✓ WIRED | `close()` → `lifecycle.release()`; `run()`'s `finally` → `lifecycle.stop()` (matches old bracketing). |
| `server.py._wire_connection_teardown(connection, call_session.lifecycle)` | the lifecycle returned by `create_call_session` | direct call in `_connection_callback`, before task spawn | ✓ WIRED | Confirmed at server.py L259: `_wire_connection_teardown(connection, call_session.lifecycle)` called immediately after `create_call_session` returns and before `asyncio.create_task(call_session.run())`. |
| `offer()` → `start_gate` → `_negotiate_webrtc` | quota gate ordering | sequential awaits in `offer()` | ✓ WIRED | L328-339: `start_gate(identity)` is called and any `QuotaError`/exception short-circuits with a JSON error response before `_negotiate_webrtc` is ever awaited (L339). |

### Behavioral Spot-Checks / Test Execution (self-run, not SUMMARY-trusted)

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Focused call_runtime tests | `uv run pytest tests/test_call_runtime.py -v` | 4 passed | ✓ PASS |
| Full pre-existing suite (§19-A exit criterion) | `uv run pytest -q` | 287 passed, 53 skipped, 0 failed | ✓ PASS |
| No transport/codec/SIP leakage into shared runtime | `grep -v '^#' call_runtime.py \| grep -c -iE '...'` | 0 | ✓ PASS |
| `_run_session`/`_start_and_run_tracked_session` removed from server.py | `grep -nE 'def _run_session\|def _start_and_run_tracked_session' server.py` | no matches | ✓ PASS |
| `_wire_connection_teardown`/`SmallWebRTCTransport`/`create_call_session` present in server.py | `grep -c -E '...' server.py` | 6 | ✓ PASS |
| 3 task commits exist | `git cat-file -e 1c7083a / f794295 / 14d75c6` | all exist, correct messages | ✓ PASS |

### Requirements Coverage

Not applicable — this phase has `requirements: []` intentionally (telephony milestone has no REQ-IDs mapped yet; coverage is via the 6 ROADMAP success criteria, all VERIFIED above). No orphaned requirements found (grepped ROADMAP.md/REQUIREMENTS.md for Phase 9 mappings — none exist beyond the success criteria already covered).

### Anti-Patterns Found

None. Scanned `call_runtime.py`, `server.py`, `test_call_runtime.py`, `test_rtvi.py`, `test_smoke.py` for `TBD|FIXME|XXX|TODO|HACK|PLACEHOLDER` and empty-implementation patterns (`return null`, `return {}`, `return []`, `=> {}`) — zero matches in all files.

### Human Verification Required

None. This is a behavior-preserving internal refactor with no new UI/UX surface, no new external service integration, and no runtime behavior that can't be exercised by the existing automated test suite (which was run directly, not merely trusted from SUMMARY.md).

### Gaps Summary

No gaps. All 6 ROADMAP Phase 9 success criteria, all 5 PLAN must_haves.truths, all 3 artifacts, all 4 key_links, and all 6 prohibitions were independently verified by reading the actual code (not the SUMMARY narrative) and by re-running the test suite and grep gates myself:

- `call_runtime.py` genuinely contains no SIP/Asterisk/RTP/codec/provider-construction code (grep gate: 0 matches).
- `create_call_session` does not call `lifecycle.start()` or `runner.run()` — those live only in `CallSession.run()`.
- `close()` delegates to the single idempotent `lifecycle.release()`.
- `server.py` no longer defines `_run_session`/`_start_and_run_tracked_session`; `_wire_connection_teardown` and `SmallWebRTCTransport` construction remain, and `create_call_session` is now called with `channel="webrtc"`.
- The quota `start_gate` still precedes `_negotiate_webrtc` in `offer()` (T-09-01 threat mitigation intact).
- 4 focused tests in `test_call_runtime.py` use a genuinely non-WebRTC `FakeTransport` and all pass.
- The full 287-test pre-existing suite passes unchanged (self-run, exit code 0, 0 failures) — the decisive §19-A "browser voice works exactly as before" proof.
- The D-08 architecture/coupling note is present in both `09-01-SUMMARY.md` and the module docstring, with consistent content.
- The 3 task commits (`1c7083a`, `f794295`, `14d75c6`) exist in git history with matching messages.

---

_Verified: 2026-07-11_
_Verifier: Claude (gsd-verifier)_
