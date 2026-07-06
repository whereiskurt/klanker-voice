---
phase: 05-browser-client-conference-readiness
plan: 04
subsystem: ui
tags: [pipecat, client-js, small-webrtc-transport, webrtc, rtvi, react, vite]

# Dependency graph
requires:
  - phase: 05-browser-client-conference-readiness (plan 01)
    provides: "server.py POST /api/offer contract + RTVIProcessor/RTVIObserver wiring (transcripts, bot/user audio levels, speaking events)"
  - phase: 05-browser-client-conference-readiness (plan 02)
    provides: "OrbCanvas/OrbFallback + orbState.ts (ORB_STATE_VISUALS, smoothAmplitude/ORB_AMPLITUDE_EMA) -- the amplitude/state contract this plan feeds real values into"
  - phase: 05-browser-client-conference-readiness (plan 03)
    provides: "tokenStore.getToken()/useAuth() in-memory Bearer token -- the credential this plan sends to /api/offer"
provides:
  - "Gesture-gated getUserMedia with three distinct, honest mic-error states (denied/no-device/unsupported), verbatim UI-SPEC copy"
  - "voiceSession.ts: PipecatClient + SmallWebRTCTransport wired to POST /api/offer with the Bearer token as an Authorization header"
  - "connectionState.ts: pure reducer (idle/requesting-mic/connecting/connected/rejected/failed) -- 'rejected' (401/403/429) kept distinct from 'connected' and from 'failed'"
  - "A fetch-interception fix for a real vendor-library gap: SmallWebRTCTransport 1.10.5 swallows the /api/offer POST's HTTP error internally and never rejects/resolves connect() on an auth/quota reject"
  - "useOrbBinding.ts: live RTVI state machine (idle/listening/thinking/speaking) + EMA-smoothed uAmplitude (mic RMS listening, bot RMS speaking) feeding the existing OrbCanvas"
  - "captionReducer.ts + Captions.tsx: subtitle-style, last-exchange-only caption band, interim gray firming to final, agent chip in the reserved accent"
  - "Live.tsx + App.tsx wiring: tap-to-talk -> requestMic -> connect -> Live screen, mounted only once connectionState reaches 'connected'"
affects: ["05-05 (countdown/session lifecycle)", "05-06 (retry/backoff + gate-copy UX, explicitly builds on connectionState's rejected/failed distinction)", "05-07 (session-end/reconnect)"]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Authorization-header Bearer attachment for SmallWebRTCTransport, NOT request_data -- small-webrtc-transport 1.10.5 nests the caller's requestData back into the offer POST body under the camelCase key 'requestData', not the snake_case 'request_data' server.py's pre-gate token check reads"
    - "Short-lived window.fetch interceptor scoped to a single connect() call, to detect a non-2xx /api/offer response the vendor client itself swallows -- always restored in the same tick it settles, before any side-effecting disconnect() call"
    - "Refs (targetAmplitudeRef/orbStateRef) + a dedicated requestAnimationFrame loop inside useOrbBinding own the continuous EMA smoothing between RTVI's own (slower) audio-level event cadence -- OrbCanvas/OrbFallback only ever receive an already-smoothed amplitude prop, per their existing doc comments"
    - "captionReducer detects 'new exchange' purely from the previous user line's own final flag (no separate turn-id) -- once final, any further USER_TRANSCRIPT is necessarily a new utterance"

key-files:
  created:
    - apps/voice/client/src/media/getMic.ts
    - apps/voice/client/src/media/getMic.test.ts
    - apps/voice/client/src/screens/MicError.tsx
    - apps/voice/client/src/screens/micError.css
    - apps/voice/client/src/transport/connectionState.ts
    - apps/voice/client/src/transport/connectionState.test.ts
    - apps/voice/client/src/transport/voiceSession.ts
    - apps/voice/client/src/transport/useVoiceSession.ts
    - apps/voice/client/src/orb/useOrbBinding.ts
    - apps/voice/client/src/captions/captionReducer.ts
    - apps/voice/client/src/captions/captionReducer.test.ts
    - apps/voice/client/src/captions/Captions.tsx
    - apps/voice/client/src/captions/captions.css
    - apps/voice/client/src/screens/Live.tsx
    - apps/voice/client/src/screens/live.css
  modified:
    - apps/voice/client/src/App.tsx

key-decisions:
  - "Bearer token sent ONLY as an Authorization header (not request_data.access_token) -- discovered small-webrtc-transport's actual wire shape doesn't match server.py's snake_case pre-gate read; the header is the one reliable path and needed no server.py change"
  - "Added a window.fetch interceptor (Rule 2) around the SmallWebRTCTransport connect() call to detect and surface 401/403/429 offer rejections, because the vendor client's negotiate() silently swallows the offer POST's HTTP error and retries rather than rejecting -- without this, the app would show an infinite 'Connecting...' spinner on every gate reject"
  - "EMA amplitude smoothing lives in useOrbBinding (a continuous requestAnimationFrame loop), not in OrcCanvas/OrbFallback -- matches those components' existing doc comments ('already EMA-smoothed by the caller') from 05-02"
  - "connectionState's 'rejected' vs 'failed' distinction is deliberate: rejected = the server's start_gate refused outright (no media ever attempted); failed = a transport/ICE problem. 05-06 builds its retry/backoff policy on top of this distinction, not on a single generic error state"

requirements-completed: [CLNT-01, CLNT-03, CLNT-04]

coverage:
  - id: D1
    description: "requestMic() feature-detects getUserMedia and classifies DOMException names into denied/no-device/unsupported (CLNT-01)"
    requirement: "CLNT-01"
    verification:
      - kind: unit
        ref: "apps/voice/client/src/media/getMic.test.ts (7 tests: granted, NotAllowedError, SecurityError, NotFoundError, OverconstrainedError, unrecognized-error fallback, unsupported-browser)"
        status: pass
    human_judgment: false
  - id: D2
    description: "MicError.tsx renders the exact UI-SPEC copy per error type as a distinct inline message with a >=44px retry affordance"
    requirement: "CLNT-01"
    verification:
      - kind: other
        ref: "grep -q \"Try Chrome or Safari\" apps/voice/client/src/screens/MicError.tsx; tsc --noEmit; npm run build"
        status: pass
    human_judgment: false
  - id: D3
    description: "connectionState.ts reducer covers idle/requesting-mic/connecting/connected/rejected/failed, with rejected kept distinct from both connected and failed; buildConnectParams attaches the Bearer token as an Authorization header"
    requirement: "CLNT-02"
    verification:
      - kind: unit
        ref: "apps/voice/client/src/transport/connectionState.test.ts (9 tests)"
        status: pass
    human_judgment: false
  - id: D4
    description: "voiceSession.ts's window.fetch interceptor actually detects a real 401/403/429 /api/offer response and dispatches OFFER_REJECTED before the vendor client's negotiate() swallows it"
    verification: []
    human_judgment: true
    rationale: "The reducer transition is unit-tested (D3), but the interceptor itself can only be proven against a real HTTP round-trip to a deployed /api/offer returning an actual 401 (expired/bad token) or 403/429 (quota gate) -- not reproducible in jsdom without a live server. Deferred to the orchestrator's deployed-AWS validation pass alongside the plan's checkpoint."
  - id: D5
    description: "useOrbBinding derives OrbState (idle/listening/thinking/speaking) + EMA-smoothed uAmplitude from real RTVI events and drives OrbCanvas"
    requirement: "CLNT-04"
    verification: []
    human_judgment: true
    rationale: "Requires a live PipecatClient emitting real userStartedSpeaking/botStartedSpeaking/localAudioLevel/remoteAudioLevel events over an actual WebRTC session -- covered by the plan's own checkpoint (\"watch the orb: it should breathe at idle, spike/tighten to your voice...\"), not reproducible in jsdom."
  - id: D6
    description: "captionReducer keeps only the last exchange per side, interim gray firming to final, agent frames concatenating within an exchange; Captions.tsx renders both sides with the agent chip in accent"
    requirement: "CLNT-03"
    verification:
      - kind: unit
        ref: "apps/voice/client/src/captions/captionReducer.test.ts (7 tests)"
        status: pass
    human_judgment: false
  - id: D7
    description: "End-to-end: tap -> mic grant -> connect -> live conversation with reactive orb + dual captions + distinct mic-error states, against the deployed voice service"
    verification: []
    human_judgment: true
    rationale: "This IS the plan's own checkpoint task (type=checkpoint:human-verify, gate=blocking) -- needs a live deployed service, a real microphone, and real browser<->task WebRTC media. Per orchestrator guidance this is not self-approved; deferred to the orchestrator's post-05-04 deployed-AWS validation pass."

# Metrics
duration: 45min
completed: 2026-07-06
status: complete
---

# Phase 5 Plan 04: Live Conversation — Mic, SmallWebRTC Connect, Reactive Orb, Subtitle Captions Summary

**Gesture-gated mic with three honest error states, a Bearer-token SmallWebRTC connect (with a real vendor-library rejection-detection fix), and the orb/captions now wired to live RTVI events -- all code/unit-testable work complete; the live deployed conversation checkpoint is deferred to the orchestrator per explicit instruction.**

## Performance

- **Duration:** ~45 min
- **Tasks:** 3 of 3 code tasks complete; the plan's 4th task (a live end-to-end checkpoint) is deferred, not self-approved
- **Files modified:** 16 (15 created, 1 modified)

## Accomplishments

- `getMic.ts` feature-detects `navigator.mediaDevices.getUserMedia` before ever calling it, then classifies `DOMException` names into a typed `denied`/`no-device`/`unsupported` union; `MicError.tsx` renders the exact UI-SPEC copy per state as a distinct message (never a merged generic error) with a `>=44px` "Try again" affordance
- `voiceSession.ts` wraps `@pipecat-ai/client-js`'s `PipecatClient` + `@pipecat-ai/small-webrtc-transport`'s `SmallWebRTCTransport`, pointed at `POST /api/offer` with the Bearer token attached as an `Authorization` header
- `connectionState.ts` is a pure reducer over `idle/requesting-mic/connecting/connected/rejected/failed` -- `rejected` (401/403/429) is a distinct outcome from both `connected` and `failed`, so no conversation UI can ever be mistaken for having started before the server's start_gate actually accepted the session (T-05-04-E)
- **Found and fixed a real vendor-library gap** (see Deviations): the shipped `@pipecat-ai/small-webrtc-transport` swallows the offer POST's HTTP error internally rather than rejecting, so `PipecatClient.connect()` would otherwise hang forever on every auth/quota reject
- `useOrbBinding.ts` subscribes the live RTVI event stream and derives the orb's state machine + a continuously EMA-smoothed `uAmplitude`, feeding 05-02's existing `OrbCanvas`/`OrbFallback` real values for the first time
- `captionReducer.ts` + `Captions.tsx` render the subtitle-style, last-exchange-only caption band (interim gray firming to final, agent chip in the one reserved accent) fed by RTVI transcript events
- `Live.tsx` composes the live stage (orb + captions); `App.tsx` now calls `useVoiceSession()`, routes mic errors inline, and mounts `Live` only once `connectionState` reaches `connected`

## Task Commits

Each task was committed atomically:

1. **Task 1: Gesture-gated getUserMedia with distinct mic-error states (CLNT-01)** — `02a6397` (feat)
2. **Task 2: SmallWebRTC connect via client-js with Bearer token + connection state machine (CLNT-02)** — `cf2fb1e` (feat)
3. **Task 3: Wire the orb to live RTVI amplitude/state + subtitle captions (CLNT-03/04)** — `ab75ca2` (feat)

**Plan metadata:** (this commit, docs: complete plan)

## Files Created/Modified

- `apps/voice/client/src/media/getMic.ts` — gesture-gated, feature-detected `requestMic()` + typed `MicError` union
- `apps/voice/client/src/media/getMic.test.ts` — 7 classification/success test cases
- `apps/voice/client/src/screens/MicError.tsx` + `micError.css` — verbatim distinct mic-error copy, `>=44px` retry
- `apps/voice/client/src/transport/connectionState.ts` — pure connection-state reducer + `OfferRejection` type
- `apps/voice/client/src/transport/connectionState.test.ts` — 9 reducer/`buildConnectParams` test cases
- `apps/voice/client/src/transport/voiceSession.ts` — `PipecatClient`/`SmallWebRTCTransport` wiring + the fetch-interception rejection fix
- `apps/voice/client/src/transport/useVoiceSession.ts` — the React hook: `requestMic()` -> `connect()`, exposing `connectionState` + the live `PipecatClient`
- `apps/voice/client/src/orb/useOrbBinding.ts` — live RTVI -> `OrbState`/`uAmplitude` derivation with continuous EMA smoothing
- `apps/voice/client/src/captions/captionReducer.ts` — last-exchange-only caption reducer
- `apps/voice/client/src/captions/captionReducer.test.ts` — 7 reducer test cases
- `apps/voice/client/src/captions/Captions.tsx` + `captions.css` — the subtitle caption band
- `apps/voice/client/src/screens/Live.tsx` + `live.css` — the live-conversation stage composition
- `apps/voice/client/src/App.tsx` — wired `useVoiceSession()`; tap-to-talk now starts the mic/connect flow; routes to `Live` on `connected`, renders `MicError` inline on mic failure

## Decisions Made

- Bearer token sent as an `Authorization` header only (not `request_data.access_token`) -- see Deviations #1.
- `connectionState`'s `DISCONNECTED` event only resets to `idle` when the current state is `connected` -- a stray `onDisconnected` callback firing after our own cleanup `disconnect()` call (see Deviations #2) must never stomp on an already-recorded `rejected`/`failed` outcome.
- Agent (bot) transcript frames concatenate within the same exchange rather than replace, since `BOT_TRANSCRIPTION` is sentence-aggregated and a single reply can span several frames; user transcript frames replace in place (each carries the full accumulating text for that utterance per Deepgram/pipecat convention).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Corrected the Bearer-token attachment mechanism from `request_data` to an `Authorization` header**
- **Found during:** Task 2 (reading `server.py`'s `_extract_bearer_token` alongside the installed `@pipecat-ai/small-webrtc-transport` source to wire the offer request)
- **Issue:** The plan's own `<action>` text names both options ("Authorization header if the transport supports request headers, else the `request_data.access_token` field"). Tracing the actual installed `small-webrtc-transport@1.10.5` source (`negotiate()`) shows it nests the caller's `webrtcRequestParams.requestData` back into the offer POST body under the **camelCase** key `requestData` -- but `server.py`'s `_extract_bearer_token()` reads `body.get("request_data")` (**snake_case**) *before* `SmallWebRTCRequest.from_dict()`'s camelCase->snake_case normalization runs (that normalization happens later, inside `_negotiate_webrtc`, after the auth/gate check has already executed). So the `request_data` fallback the plan named would silently never be found by the server's pre-gate token check.
- **Fix:** Used the `Authorization: Bearer <token>` header exclusively (`buildConnectParams` in `voiceSession.ts`), which `_extract_bearer_token()` checks first and which the transport's `webrtcRequestParams.headers` demonstrably carries through to the offer POST (traced the vendor `negotiate()`/`makeRequest` source directly).
- **Files modified:** `apps/voice/client/src/transport/voiceSession.ts`
- **Verification:** `connectionState.test.ts`'s `buildConnectParams` cases confirm the header is set/omitted correctly; `tsc --noEmit` + `npm run build` clean.
- **Committed in:** `cf2fb1e` (Task 2 commit)

**2. [Rule 2 - Missing Critical] Added a fetch-interception fix for the vendor transport's swallowed offer-rejection**
- **Found during:** Task 2 (tracing `@pipecat-ai/small-webrtc-transport@1.10.5`'s `negotiate()`/`attemptReconnection()`/`stop()` source to understand exactly how a 401/403/429 offer response would surface to the app)
- **Issue:** `SmallWebRTCTransport.negotiate()` wraps its `/api/offer` POST in a `try { ... } catch (e) { ...; setTimeout(() => this.attemptReconnection(true), 2000); }` -- ANY error, including a non-2xx HTTP response from `makeRequest`, is swallowed and turned into a silent retry rather than a rejection. After `maxReconnectionAttempts` (3, hard-coded in the vendor library) it just calls `stop()`, which also does not reject. Result: `PipecatClient.connect()`'s returned promise **never settles** (never resolves, never rejects) on an auth/quota gate reject, and there is no public callback hook for the raw HTTP response either. Left unfixed, the app would show an infinite "Connecting…" spinner on every 401/403/429 -- violating the plan's own CRITICAL acceptance requirement that a reject "must NOT start the conversation; surface it... the state machine must expose a 'rejected' outcome distinct from 'connected'" (an outcome that would otherwise never actually fire).
- **Fix:** `voiceSession.ts`'s `connect()` installs a short-lived `window.fetch` interceptor scoped to exactly one `connect()` call. It inspects (never mutates) the response for the `/api/offer` POST; on a non-2xx it immediately dispatches a typed `OFFER_REJECTED` event with the real status + JSON error body, then proactively calls `client.disconnect()` to stop the vendor client's silent retry loop, and resolves our own `connect()` promise so callers never hang. The interceptor is always restored (`finally`/inline-restore on every branch).
- **Files modified:** `apps/voice/client/src/transport/voiceSession.ts`
- **Verification:** `connectionState.test.ts`'s `OFFER_REJECTED` reducer case proves the resulting state transition is correct and distinct from both `connected` and `failed`; the interceptor's actual HTTP-detection behavior itself needs a live 401/403/429 round-trip to fully prove (flagged as coverage item D4, `human_judgment: true`, deferred to the orchestrator's deployed validation alongside this plan's checkpoint).
- **Known residual limitation (documented in code, not further hardened here):** because the vendor transport's retry scheduling isn't fully cancelable through its public API, an already-in-flight `setTimeout`-based reconnection attempt scheduled by `negotiate()`'s own catch block before our `disconnect()` call lands could still fire once in the background after we've surfaced "rejected" to the UI -- at most a few extra, cheap (no-media) auth/gate checks server-side within ~6s. Not a security or spend concern (server's `start_gate` remains authoritative either way), but flagged in the `voiceSession.ts` docstring for 05-06 (the retry/backoff owner) to confirm live and harden further if it proves user-visible.
- **Committed in:** `cf2fb1e` (Task 2 commit)

---

**Total deviations:** 2 auto-fixed (1 bug-class Bearer-attachment correction, 1 missing-critical rejection-detection fix)
**Impact on plan:** Both were required for the plan's own stated CRITICAL requirement (no conversation UI before a real offer acceptance; a rejected outcome distinct from connected) to actually hold true once deployed -- no unrelated scope creep. Both are called out above with their live-verification coverage items (D4).

## Issues Encountered

- Local `vitest` runs failed on this shell's default Node v22.1.0 with the same pre-existing `@exodus/bytes`/`html-encoding-sniffer` ESM/CJS interop error 05-03-SUMMARY.md already documented. Worked around via `nvm use v23.6.0` for all verification in this plan, matching the prior plans' documented workaround; no code change needed. Full suite: 40/40 tests passing (6 files) once on v23.6.0.
- `.planning/config.json` shows an unrelated one-line trailing-newline diff and there are unrelated untracked files from a different in-progress workstream (`05.1-operator-admin-panel...`, `docs/superpowers/specs/2026-07-06-admin-panel-design.md`) present in the working tree at session start -- confirmed via `git log`/`git status` these predate and are untouched by this plan's session; left alone, not committed.

## User Setup Required

None - no external service configuration required by this plan's code changes. (The Phase-4 voice-task IAM gap noted below is an existing, separately-tracked deploy blocker, not something this plan's code introduces or fixes.)

## Deploy Implications (for the orchestrator's post-05-04 deploy pass)

- **Existing Phase-4 IAM gap (not introduced by this plan):** the voice task role currently lacks cross-table read access on `kmv-auth-electro`, so a live quota-gated `/api/offer` request fails closed at `read_tier()` today. This means the plan's checkpoint (a real end-to-end conversation) cannot pass against the current deployed image until that IAM gap is closed -- per the orchestrator's own guidance, this is being fixed as part of the deploy, not by this plan.
- **Auth + voice images must both be current** for the full flow to work end-to-end (carried forward from 05-03: the corrected `voice` OIDC client registration requires the `apps/auth` redeploy; this plan's Bearer-token/`/api/offer` wiring requires the current voice image).
- No new environment variables, secrets, or infra are introduced by this plan -- `voiceSession.ts`/`useOrbBinding.ts`/`captionReducer.ts` are pure client-side additions consuming existing `/api/offer` + RTVI contracts.

## Next Phase Readiness

- The full "tap -> mic -> connect -> live orb + captions" client-side flow is code-complete and ready for the orchestrator's deployed-AWS validation pass.
- `connectionState`'s `rejected`/`failed` distinction is the foundation 05-06 needs for its retry/backoff + typed gate-copy UX (D-11/D-14) -- 05-06 should read `voiceSession.ts`'s docstring for the known residual background-retry limitation before building on top of it.
- **Blocking for full verification:** this plan's checkpoint (a live end-to-end conversation with reactive orb + dual captions + distinct mic errors against the deployed stack) is NOT yet exercised -- see `## CHECKPOINT REACHED` in the executor's return message. Per orchestrator guidance, this is intentionally deferred and folded into the post-05-04 deployed-AWS validation pass (which also needs the Phase-4 IAM gap closed first), not self-approved here.
- `REQUIREMENTS.md`: CLNT-01/CLNT-03/CLNT-04 marked complete (this plan delivers them fully at the code level, mirroring 05-01/05-02's earlier partial claims now backed by the live wiring). CLNT-02 is intentionally left "Pending" -- this plan only delivers its happy path; the requirement's own text bundles auto-retry/UDP-blocked messaging, which is explicitly 05-06's job.

---
*Phase: 5-Browser Client & Conference Readiness*
*Completed: 2026-07-06*

## Self-Check: PASSED

All 16 files listed above verified present on disk; all 3 task commit hashes
(`02a6397`, `cf2fb1e`, `ab75ca2`) verified present in `git log --oneline --all`.
