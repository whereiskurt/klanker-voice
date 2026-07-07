---
slug: voice-concurrency-slot-leak
status: fixing
created: 2026-07-07
severity: high
area: voice quota / session lifecycle (server) + client transport
---

# Debug: Voice concurrency slot not released on teardown

## Trigger (verbatim, treat as data)
Reload/reconnect to voice.klankermaker.ai gets rejected with `concurrency-limit` (403) — "revisiting bug". Bumping the tier `max_concurrent` 1→2 did not fix it. Blocks the slick-start demo.

## Status
investigating — **root cause largely established from production evidence** (below). Go to hypothesis-test + fix; do not re-investigate from scratch.

## Symptoms / Confirmed Evidence (production, deployed rev voice-use1-kmv:12, cluster app-use1-kmv, service voice-use1)

1. **Server logs** (`/ecs/voice-app-voice-use1-kmv`, AWS_PROFILE=klanker-application us-east-1):
   - Repeated `server:offer:290 - /api/offer start_gate rejected sub=<sub>: concurrency-limit` → `POST /api/offer 403 Forbidden`, then `PATCH /api/offer 422` (trickle-ICE PATCH with no pc — expected noise).
   - A session DOES connect + work when a slot is free: `04:25:19 POST /api/offer 200`, ICE `checking → completed`, pipeline linked (Deepgram/Anthropic/ElevenLabs), **3 turns, voice_to_voice 1446ms**. So the pipeline + Burt Fundeck voice are fine — the wall is purely quota.
   - Abrupt drop path logs `pipecat...on_iceconnectionstatechange: ICE connection state is closed` + `request_handler:handle_disconnected: Discarding peer connection for pc_id: ...` with **NO release afterward**.

2. **DynamoDB `kmv-voice-usage`** (pk=`session#<sub>`, sk=`heartbeat#<id>`, attr `expiresAt`):
   - After a session ends, its heartbeat lease row **lingers** and is only ever removed by aging out (no delete).
   - **KEY:** a lease from a just-ended (clean-looking, pipeline-finalize) session had `expiresAt = last_renewal + 45s` (the full `heartbeat_ttl`), **NOT an immediate-expiry** → proves `SessionLifecycle.release()` → `quota.release_heartbeat()` did **NOT** run on teardown. `count_active_heartbeats` counts leases with `expiresAt > now`, so each dropped session keeps blocking for up to 45s. Rapid churn stacks multiple LIVE leases → exceeds `max_concurrent` even at 2.
   - Observed: 3 lingering lease rows at once for the test user, all aged-out via TTL (not released). Cleared manually to unblock.

3. **chrome://webrtc-internals**: `getUserMedia` fires **~2x per start** (two mic streams ~1s apart, e.g. 00:25:13 + 00:25:14), across many page loads / process ids — a **retry/re-tap storm** that churns sessions and stacks the un-released leases.

## Bugs to fix (start with BUG 1)

### BUG 1 (server, PRIMARY) — heartbeat lease not released on session teardown
Why doesn't `SessionLifecycle.release()` / `quota.release_heartbeat()` fire (or set immediate-expiry) on teardown?
- Clean path: `_start_and_run_tracked_session` `finally: await lifecycle.stop()` (server.py ~204-219) → `release()` (session.py ~160) → `quota.release_heartbeat`.
- Abrupt path: pipecat `@transport.event_handler("on_client_disconnected")` → `lifecycle.on_transport_disconnected()` (server.py ~188) → `_reconnect_grace()` (session.py ~241, waits reconnect_grace_seconds=12) → `release()`.
- **Verify whether pipecat's `on_client_disconnected` even fires on an abrupt tab-close / ICE-close** (the `handle_disconnected: Discarding peer connection` log may bypass the transport event). If it doesn't fire, and the runner task also isn't cancelled, neither release path runs → lease only ages out at 45s.
- Also check `quota.release_heartbeat` actually writes an immediate/past `expiresAt` (or deletes the row) and that `count_active_heartbeats` respects it.
- **Fix goal:** a dropped session frees its slot within a few seconds regardless of how it ends (clean stop, ICE-close, or abrupt tab-close). Consider: delete the lease row on release (not just expire it); ensure the ICE-close/ connection-discard path invokes release; optionally shorten the leak window.
- **TDD:** unit test proving the lease is released/deleted (or expiresAt set to past) when the session lifecycle tears down — for both the clean stop() path and a simulated transport-disconnect path.

### BUG 2 (client) — retry/re-tap storm + double getUserMedia
`apps/voice/client/src/transport/useVoiceSession.ts`, `media/getMic.ts`, `transport/retryPolicy.ts`:
- Mic captured ~2x per start (probe stream in getMic vs PipecatClient enableMic). Memory phase5 fix `1f835b1` claims to stop the probe stream's tracks — verify it's still effective after the slick-start greeting changes to start().
- start()/connect appears retried aggressively. A `concurrency-limit` rejection must be **terminal** (a gate), NOT retried — the retryPolicy is for transport/ICE failures only. Confirm concurrency-limit doesn't feed the retry controller.

### BUG 3 (client UX) — concurrency-limit shows "just the welcome", no clear gate
On a `concurrency-limit` 403, `App.tsx` should render the `GateCard` (outcome.state==="rejected" → `gates/gateMapping`) with clear copy — not silently play the greeting clip and appear stuck. Verify the rejection reaches the reducer as `rejected` even though the greeting is playing (the deferred-handoff only gates CONNECTED, not rejections). Greeting-not-interruptible is BY DESIGN (pipeline not listening until clip-end+connected) — note only, don't fix unless trivial.

## Config
`apps/voice/pipeline.toml [quota]`: heartbeat_ttl=45, reconnect_grace_seconds=12, heartbeat renew ~15s, user_silence_timeout=50, goodbye_grace_seconds=5. Per-tier `max_concurrent` in `kmv-auth-electro` (demo-tier / kphdemo-tier now = 2).

## File map
- Server: `apps/voice/server.py` (offer ~290, on_client_disconnected ~188, _start_and_run_tracked_session ~204-219), `apps/voice/src/klanker_voice/session.py` (SessionLifecycle.release ~160, on_transport_disconnected ~229, _reconnect_grace ~241), `apps/voice/src/klanker_voice/quota.py` (acquire_heartbeat ~261, release_heartbeat, count_active_heartbeats ~247).
- Client: `apps/voice/client/src/transport/useVoiceSession.ts`, `media/getMic.ts`, `transport/retryPolicy.ts`, `gates/gateMapping.ts`, `gates/GateCard.tsx`, `App.tsx`.
- Tests: server `apps/voice/tests/` (pytest via `.venv/bin/pytest`; there are existing quota/session tests to mirror — e.g. test_quota*/test_session*); client vitest (`cd apps/voice/client && nvm use 23 && npx vitest run` — ambient Node 22.1.0 has a jsdom bug, use Node ≥22.12).

## Ops (unblock + deploy during debugging)
- Clear ghost leases: `aws dynamodb delete-item --table-name kmv-voice-usage --key '{"pk":{"S":"session#<sub>"},"sk":{"S":"heartbeat#<id>"}}'` (AWS_PROFILE=klanker-application us-east-1). Test user sub = `db2a6cd0-9f2e-4ddd-b408-65b9bc7fc0f8`.
- Deploy: push `apps/voice/**` to main → `build-voice.yml` builds + auto-deploys (terragrunt ecs-task/ecs-service). Then force single-task cutover: `aws ecs stop-task` the OLD rev (cluster app-use1-kmv, service voice-use1) since WebRTC isn't ALB-sticky. Verify via `/greetings/greetings.manifest.json` served + a fresh `/api/offer 200` in logs.

## Current Focus (BUG 1 — root cause CONFIRMED via code trace)

reasoning_checkpoint:
  hypothesis: "On an abrupt client vanish (tab-close / ICE-close / retry-storm churn), release() is never invoked, so quota.release_heartbeat never runs and the lease ages out at heartbeat_ttl (45s). The concurrency gate (count_active_heartbeats: expiresAt > now) keeps counting the dead lease, causing concurrency-limit 403 on reconnect. The two release triggers both depend on signals the abrupt path does not deliver: (a) the runner finally: stop() only runs when runner.run() RETURNS, but on a silent client vanish the pipeline keeps running (read_audio_frame loops forever on is_connected() checks, never exits) so run() never returns; (b) transport on_client_disconnected -> on_transport_disconnected -> _reconnect_grace -> release only fires if the SmallWebRTCConnection fires its 'closed' event AND the transport's 'closed' handler is registered — but that handler is built inside _run_session AFTER `await lifecycle.start()` (slow AWS calls), so a close during that window is caught only by the request_handler's own 'closed' handler ('Discarding peer connection' — seen in prod) which does NOT release the lease."
  confirming_evidence:
    - "Production lease had expiresAt = last_renewal + 45s (full TTL), NOT now-1 -> release_heartbeat provably never ran (release() not called)."
    - "server.py: the ONLY release wiring for a live pipeline is (1) `_start_and_run_tracked_session` finally: stop() after `await runner.run()`, and (2) `transport.event_handler('on_client_disconnected')` -> lifecycle.on_transport_disconnected (registered inside _run_session, only after the awaited lifecycle.start())."
    - "pipecat transport.py _handle_client_closed fires on_client_disconnected only when the connection's 'closed' event fires; connection fires 'closed' only when pc.close() runs (aiortc 'failed', graceful disconnect, restart_pc, or connecting-timeout). aiortc does NOT reliably transition to 'failed' on abrupt disconnect (pipecat's own repeated comments); nothing actively polls is_connected() to force a close."
    - "request_handler.py registers its OWN connection 'closed' handler at connection-creation (logs 'Discarding peer connection for pc_id') — this fired in prod but only pops _pcs_map; it never touches the lifecycle/lease."
    - "Fire-and-forget task: asyncio.create_task(_start_and_run_tracked_session(...)) is spawned with NO strong reference retained (SESSIONS holds the SessionRecord, not the Task) — a secondary hardening concern (GC of pending tasks can drop the finally)."
  falsification_test: "If, after wiring the raw SmallWebRTCConnection 'closed' event to lifecycle.on_transport_disconnected() at connection-creation time, an abrupt 'closed' event releases the heartbeat lease within reconnect_grace, the hypothesis holds. If the lease still lingers at full TTL, the release trigger is elsewhere."
  fix_rationale: "Root cause is a MISSING release trigger on the abrupt path, not a broken release() (release()/release_heartbeat work correctly when called — verified: keys session#<user>/heartbeat#<session> match across acquire/renew/release, no indirection bug). Fix: register a connection-level teardown handler (SmallWebRTCConnection 'closed' -> lifecycle.on_transport_disconnected()) at connection-creation in _connection_callback — BEFORE the fire-and-forget task and independent of the transport handler's timing — so every abrupt close reaches release() via the idempotent grace path. Also retain a strong ref to the session task. This is belt-and-suspenders with the existing transport handler; release()'s _stopped guard makes double-fire a no-op."
  blind_spots: "Cannot unit-test real aiortc close semantics or task-GC without a live peer; the RED test simulates the connection 'closed' event via a BaseObject-backed fake. Whether pipecat also needs restart_pc handling is untested. Deleting-vs-expiring the lease row (IAM DeleteItem grant) is out of code scope and NOT part of this fix."

test: pytest tests/test_slot_leak.py — simulate SmallWebRTCConnection 'closed' event; assert heartbeat lease released within grace.
expecting: GREEN after wiring connection 'closed' -> release.
next_action: BUG 1 GREEN + full suite green + committed. Checkpoint back to orchestrator (deploy_ready). BUG 2 (client retry/re-tap storm) and BUG 3 (client UX gate) remain for later cycles.

tdd_checkpoint:
  test_file: "apps/voice/tests/test_slot_leak.py"
  test_name: "test_abrupt_connection_close_releases_heartbeat_lease"
  status: "green"
  green_output: "2 passed (test_abrupt_connection_close_releases_heartbeat_lease + test_clean_stop_releases_heartbeat_lease). Full server suite: 172 passed — no regressions (quota, session lifecycle, reconnect-grace, idle-teardown all green)."
  planned_fix: "Add server._wire_connection_teardown(connection, lifecycle): register a handler on the raw SmallWebRTCConnection 'closed' event -> lifecycle.on_transport_disconnected(); call it from _negotiate_webrtc._connection_callback at connection-creation (before the fire-and-forget task, independent of the transport handler's post-lifecycle.start() timing). Also retain a strong ref to the session task + pop SESSIONS on teardown. release()'s _stopped guard makes the double-path idempotent."

## Resolution (BUG 1)

root_cause: "Missing abrupt-path release trigger. The only two lease-release wirings for a live pipeline — (1) `_start_and_run_tracked_session`'s `finally: stop()` after `runner.run()` returns, and (2) the transport `on_client_disconnected` handler registered inside `_run_session` only AFTER the awaited `lifecycle.start()` — both depend on signals an abrupt client vanish never delivers. On a silent tab-close/ICE-close, aiortc discards the peer and `SmallWebRTCConnection` fires its own `closed` event, but the pipeline `runner.run()` never returns (audio-read loop spins on is_connected()) and the transport-level handler may not yet exist / may not see the close. So `SessionLifecycle.release()` never runs, `quota.release_heartbeat()` never fires, and the heartbeat lease ages out only at the full 45s `heartbeat_ttl`. `count_active_heartbeats` (expiresAt > now) keeps counting the dead lease → `concurrency-limit` 403 on reconnect. Confirmed by production lease with expiresAt = last_renewal + 45s (full TTL, not now-1)."

fix: "server.py — added `_wire_connection_teardown(connection, lifecycle)` which registers a handler on the raw `SmallWebRTCConnection` `closed` event that calls `lifecycle.on_transport_disconnected()`. Called from `_negotiate_webrtc._connection_callback` at connection-creation time, BEFORE the fire-and-forget session task and independent of the transport handler's post-`lifecycle.start()` timing — so every abrupt close reaches `release()` via the idempotent D-07 reconnect-grace path (capping the leak at reconnect_grace_seconds=12 instead of the 45s TTL, while preserving quick-reconnect semantics via on_transport_reconnected). Belt-and-suspenders with the existing transport handler; release()'s `_stopped` guard makes the double-fire a no-op, and `on_released=runner.cancel` also stops the zombie pipeline runner. Hardening: added `SESSION_TASKS` dict holding a strong ref to the fire-and-forget session task (asyncio keeps only a weak ref, so a pending task could be GC'd mid-run and drop its `finally: release()`); a done-callback pops both SESSION_TASKS and SESSIONS on task completion. No IAM/DynamoDB schema changes; lease is expired (via existing release_heartbeat), not deleted."

verification: "test_abrupt_connection_close_releases_heartbeat_lease now GREEN (was RED at count==1): after the simulated abrupt `closed` event the lease is released within the reconnect grace (active_session_count==0, count_active_heartbeats==0, lease expiresAt <= now). Companion clean-stop regression lock still GREEN. Full server suite: 172 passed, 0 regressions. Live production verification (fresh /api/offer 200 on reconnect after an abrupt drop) deferred to orchestrator post-deploy."

files_changed:
  - "apps/voice/server.py (added _wire_connection_teardown + SESSION_TASKS strong-ref registry + done-callback cleanup; wired into _connection_callback)"
  - "apps/voice/tests/test_slot_leak.py (RED test written in prior cycle, now GREEN)"

## Resolution (BUG 2 — client: quota rejection is terminal, no retry-storm)

root_cause: "In voiceSession.ts, a non-2xx /api/offer (401/403/429 — e.g. concurrency-limit) is surfaced as a typed `OFFER_REJECTED`, then the vendor SmallWebRTCTransport is proactively `client.disconnect()`ed to stop its silent internal reconnect loop. That disconnect — and any reconnection its own `negotiate()` catch block had already scheduled before we disconnected — emits a stray `onTransportStateChanged('error')`/`onError`, i.e. a late `TRANSPORT_ERROR`. `handleSessionEvent` did NOT guard `TRANSPORT_ERROR`/`DISCONNECTED` against a prior rejection, so the stray error (a) stomped the clear `rejected` outcome to `failed` in the reducer, and (b) called `retryController.reportFailure()` — feeding the bounded transport-retry schedule with a QUOTA reject. The retry then fired a fresh `/api/offer` (a new SmallWebRTC session), which the still-busy start-gate rejects again → churn. Combined with the ~2x getUserMedia per start (probe + client capture), this is the client half of the concurrency slot pressure / re-tap storm. Note: the probe-stream-stop from phase5 1f835b1 (useVoiceSession.start() line ~238, `mic.stream.getTracks().forEach(t => t.stop())`) is STILL present and effective after the slick-start greeting changes — verified by regression-lock test; the double getUserMedia is the intentional probe+client pair, and the probe is released."

fix: "useVoiceSession.ts — added a `rejectedRef` latch set the instant an `OFFER_REJECTED` is handled (dispatched immediately, NOT deferred behind the greeting handoff, which gates only CONNECTED). Once latched, any subsequent `TRANSPORT_ERROR`/`DISCONNECTED` is swallowed whole: no `failed` stomp and no `retryController.reportFailure()`. A quota/auth rejection is therefore terminal and can never feed the transport retry controller (which is for pre-connect ICE/transport failures only, D-11). The latch is reset by `start()` and `stop()` so a fresh user-initiated attempt starts clean. No change to retryPolicy.ts (its contract was already 'transport failures only' — the fix is upstream at the call site that was misrouting quota rejects into it). No change to getMic.ts — the probe-stop was already correct."

verification: "New useVoiceSession.rejection.test.ts: (2b) `keeps a concurrency-limit rejection terminal…` was RED (outcome='failed') before the fix, GREEN after — after OFFER_REJECTED + a trailing TRANSPORT_ERROR the outcome stays `rejected` (error='concurrency-limit'), retryStatus stays `idle`, and createVoiceSession is called exactly once (no re-attempt). (2a) `stops the probe-stream tracks…` GREEN — start() calls track.stop() once (1f835b1 regression lock). Full client suite: 104 passed (102 baseline + 2 new), 0 regressions. tsc --noEmit clean."

files_changed:
  - "apps/voice/client/src/transport/useVoiceSession.ts (rejectedRef latch; OFFER_REJECTED dispatched immediately + latched; TRANSPORT_ERROR/DISCONNECTED swallowed post-rejection; latch reset in start()/stop())"
  - "apps/voice/client/src/transport/useVoiceSession.rejection.test.ts (new — 2a probe-stop regression lock, 2b terminal-rejection RED->GREEN)"

## Evidence (appended during confirmation)
- checked: server.py release wiring (_start_and_run_tracked_session finally, _run_session transport handlers), session.py release/_reconnect_grace/on_transport_disconnected, quota.py release_heartbeat/count_active_heartbeats/_heartbeat_pk/_heartbeat_sk, record_tick renewal.
  found: release()/release_heartbeat correct and key-consistent; no connection-level teardown wiring; transport handler registered only after awaited lifecycle.start(); session task is fire-and-forget with no retained ref.
  implication: BUG 1 root cause = missing abrupt-path release trigger. Fix belongs in server.py connection wiring, not session.py/quota.py.
- checked: pipecat smallwebrtc transport.py + connection.py + request_handler.py disconnect state machine.
  found: on_client_disconnected depends on connection 'closed'; aiortc doesn't reliably fire 'failed' on abrupt drop; request_handler's own 'closed' handler only discards the pc, never releases the lease.
  implication: abrupt close ('Discarding peer connection' in prod logs) bypasses every lease-release path -> lease ages out at 45s -> concurrency-limit wall. Matches production evidence exactly.
- checked (GREEN cycle): SmallWebRTCConnection registers a 'closed' event (verified via inspect); BaseObject._run_handler calls the handler as `handler(self, *args)`, so a 'closed' handler receives the connection as its single positional arg; connection.py fires it via `_call_event_handler(state)` with no extra args.
  found: `connection.event_handler("closed")` is a valid, real seam identical in shape to the test's _FakeWebRTCConnection.fire_closed dispatch. Wired _wire_connection_teardown at _connection_callback time.
  implication: the fix uses the genuine pipecat event API — the RED test's fake exercises the exact same BaseObject dispatch path, so GREEN here reflects real runtime behavior.
- checked: pytest tests/test_slot_leak.py (2 tests) then full `.venv/bin/pytest -q` (apps/voice).
  found: slot-leak GREEN (abrupt-close now releases within grace; clean-stop still releases); full suite 172 passed, 0 regressions.
  implication: BUG 1 fixed and regression-locked. Ready for orchestrator deploy decision. BUG 2/BUG 3 remain.
