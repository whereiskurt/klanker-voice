---
phase: 05-browser-client-conference-readiness
verified: 2026-07-06T03:30:00Z
status: human_needed
score: 8/8 requirements code-verified; 5/5 success criteria code-verified, 0/5 live-verified
behavior_unverified: 0
overrides_applied: 0
re_verification: null
gaps: []
deferred: []
human_verification:
  - test: "Live PKCE sign-in round-trip against deployed auth.klankermaker.ai (post-redeploy of apps/auth with the corrected public 'voice' OIDC client) + a deployed voice client. Confirm token held in memory only (devtools inspection), no-access gate for a real no-access account, page-refresh forces re-auth."
    expected: "Tap-to-talk with no token redirects to auth.klankermaker.ai; callback exchanges code for token; authenticated no-access user sees the exclusive 'You're on the list — almost.' gate; refresh drops the token."
    why_human: "Requires the deployed auth service (redeploy pending — 05-03 found the live 'voice' OIDC client registration was still the stale confidential-client shape) and a deployed voice client; PKCE/token-exchange code is unit-tested but the real network round-trip cannot be exercised locally."
  - test: "End-to-end live conversation on the deployed voice service: tap to talk, grant mic, hold a real spoken exchange."
    expected: "Orb transitions idle→listening→thinking→speaking and deforms to real mic/TTS RMS; subtitle captions render both sides, interim gray firming to final; audio flows both ways."
    why_human: "Requires a live PipecatClient/SmallWebRTC session against the deployed Fargate task with a real microphone — not reproducible in jsdom. Also blocked until the Phase-4 IAM gap (voice task role lacking cross-table read on kmv-auth-electro) is deployed/applied so `/api/offer` doesn't fail closed at `read_tier()`."
  - test: "Distinct mic-error states (denied / no-device / unsupported browser) exercised in a real browser by denying mic permission, unplugging a mic, and loading in an unsupported browser."
    expected: "Each condition shows its own verbatim UI-SPEC copy, never a merged generic error."
    why_human: "getUserMedia DOMException classification is unit-tested (7 cases) but real browser permission-prompt behavior needs a live browser."
  - test: "Connection state machine on a real hostile/UDP-blocked conference network: observe bounded auto-retry ('Reconnecting… attempt n of N') then the honest UDP-blocked wall."
    expected: "3 bounded retries with visible backoff, then STOP at 'This network blocks the audio channel.' with a manual 'Try again' — no infinite spinner."
    why_human: "retryPolicy.ts's schedule/backoff/exhaustion logic is unit-tested with fake timers, but only a real UDP-blocked network proves the wall actually appears from a genuine ICE/transport failure, not a simulated one."
  - test: "Typed start-gate rejection copy (daily-exhausted / over-concurrent / killswitch) against real quota state: exhaust a tier's daily minutes, open two concurrent sessions, and flip `kv killswitch`."
    expected: "Each condition shows its specific verbatim gate card, never a raw error."
    why_human: "gateMapping.ts's error_type→copy map is unit-tested (11 cases) but confirming the real server actually emits these error_types under live killswitch/concurrency/daily-limit conditions requires the deployed service and live quota state."
  - test: "A real session ends via the server's timer/goodbye (or mid-session daily-quota exhaustion) and the user taps Reconnect."
    expected: "'Nice talking with you.' summary with {m:ss} spoken; Reconnect issues a fresh /api/offer that re-runs the quota start-gate; a reject routes to the matching gate card, not a raw error."
    why_human: "Session-end/reconnect wiring is unit/tsc-verified but a server-driven wind-down/goodbye requires a live conversation reaching its natural end."
  - test: "Session countdown escalates amber→red in sync with the agent's spoken −30s warning, observed live."
    expected: "Countdown pill turns amber at ≤30s remaining (the moment the agent speaks the warning) and red with a pulse at <10s; aria-live announces once at each boundary."
    why_human: "useCountdown's threshold math is unit-tested; syncing to the agent's actual spoken warning requires a live session and a human ear/eye."
  - test: "Latency HUD toggled open during a live conversation shows real, updating per-turn numbers (STT / LLM TTFT / TTS first-audio / v2v p50)."
    expected: "HUD is invisible by default; 'H' key or the affordance opens it; rows update per turn with real numbers, never placeholders."
    why_human: "useLatencyMetrics's reduction logic is unit-tested against synthetic kmv-latency payloads; real per-turn values require a live pipeline run."
  - test: "Full one-handed mobile/iOS pass on a real iPhone (Safari): 100dvh + safe-area insets, mic CTA ≥96px in the lower third, all buttons ≥44px, VoiceOver announcing connection status/countdown/errors, and toggling iOS Reduce Motion mid-session swaps the orb to its calm fallback."
    expected: "The full attract→sign-in→mic→live-conversation→countdown→session-end flow works one-handed on an iPhone over both normal Wi-Fi/cellular and a restricted/UDP-blocked conference-style network."
    why_human: "This is the phase's own headline acceptance test ('verified on real phones and hostile networks') — jsdom has no WebGL2 (OrbCanvas always renders its 2D fallback in tests) and no real iOS VoiceOver/Reduce-Motion toggle; requires a physical device."
---

# Phase 5: Browser Client & Conference Readiness Verification Report

**Phase Goal:** A member of the public at voice.klankermaker.ai signs in, taps the mic, and holds
a slick conversation with full session UX — proven on real phones and hostile networks.

**User Story (mode: mvp):** *As a member of the public at a conference, I want to sign in at
voice.klankermaker.ai, tap the mic, and hold a slick low-latency conversation with the
KlankerMaker concierge — with a live orb, captions, a countdown, and honest failure handling —
so that I experience the "whoa" of the demo and can cleanly reconnect for another turn.*

**Verified:** 2026-07-06
**Status:** human_needed
**Re-verification:** No — initial verification

## Verification Approach (per explicit instruction)

Success criteria 1-5 and the majority of plan must_haves assert **live, on-device/on-network
runtime behavior** (real mic + WebRTC media + real quota state + a real iPhone + a UDP-blocked
network). These are DEFERRED BY DESIGN to a post-deploy AWS validation pass that is a user-awake
task — not because the code is missing, but because the deploy hasn't happened yet (auth service
undeployed; deploy.yml decoupled but not run) and every 05-0x plan's own checkpoint explicitly
declined to self-approve them. This report therefore separates two axes:

1. **Code-verified (pass/fail axis, checked directly against the codebase in this session):**
   presence, substance, wiring, data-flow, verbatim copy, and full local test-suite execution.
2. **Live-verified (human_needed axis):** anything requiring the deployed service, a real device,
   real quota state, or a real hostile network. Listed exhaustively below — none of these are
   marked FAILED.

## Full Local Test Suites (run directly in this session, not taken from SUMMARY claims)

| Suite | Command | Result |
|-------|---------|--------|
| Voice client (vitest) | `npx vitest run` (node v23.6.0 via nvm — v22.1.0 is below the vite8/rolldown floor) | **85/85 passed**, 11 files |
| Voice client (tsc) | `npx tsc --noEmit` | **clean** |
| Voice client (build) | `npm run build` | **clean** — produces `dist/index.html` + hashed assets |
| Voice service (pytest) | `uv run pytest -q` | **166/166 passed**, 3 warnings (pre-existing deprecation notices, unrelated) |
| Auth webapp (vitest, regression check for the 05-03 OIDC-client correction) | `npx vitest run` | **33/33 passed**, 9 files |

All numbers independently reproduced in this session — not copied from SUMMARY.md.

## Goal Achievement — User Flow Coverage (MVP mode)

| Step | Expected | Evidence | Status |
|------|----------|----------|--------|
| Sign in | Tap-to-talk with no token redirects (authz-code+PKCE) to auth.klankermaker.ai; callback exchanges code for an in-memory-only token | `auth/pkce.ts`, `auth/oidcClient.ts` (RFC 7636 Appendix-B vector test), `auth/tokenStore.ts` (module-scope var, zero `localStorage`/`document.cookie` hits repo-wide), `screens/Callback.tsx`, `App.tsx:38-46` wiring `handleTapToTalk`→`auth.beginSignIn()` | ✓ code-verified / live-deferred |
| Grant mic | Gesture-gated `getUserMedia`; distinct denied/no-device/unsupported errors | `media/getMic.ts` + 7-case test; `screens/MicError.tsx` verbatim copy (grep-confirmed) | ✓ code-verified / live-deferred |
| Hold a conversation | SmallWebRTC connect with Bearer token; orb reactive; captions both sides | `transport/voiceSession.ts` (Bearer header + vendor-swallowed-rejection fetch-interceptor fix), `orb/useOrbBinding.ts` (live RTVI state+RMS→amplitude, no placeholders), `captions/captionReducer.ts` (7 tests) | ✓ code-verified / live-deferred |
| See countdown + HUD | Persistent escalating countdown; toggleable per-stage latency HUD | `timer/useCountdown.ts` (11 tests, thresholds synced to server `winddown_warning_seconds`), `hud/useLatencyMetrics.ts` (14 tests, reduces real `kmv-latency` RTVI payload keys matching `observers.py::_build_latency_payload` exactly) | ✓ code-verified / live-deferred |
| Honest failure handling | Bounded auto-retry → honest UDP-blocked wall; typed gate copy; clean end + quota-rechecked reconnect | `transport/retryPolicy.ts` (3 tests), `gates/gateMapping.ts` (11 tests, verbatim UI-SPEC strings), `screens/SessionEnd.tsx` + `useVoiceSession.ts` reconnect→fresh `/api/offer` | ✓ code-verified / live-deferred |
| Outcome ("whoa" + clean reconnect) | The full flow above holds up on a real phone and a hostile network | `App.tsx` composes every screen above into one state machine (reviewed line-by-line, no orphaned screens); the **live** proof is the explicitly deferred 05-07 checkpoint | ⚠ human_needed |

## Success Criteria (ROADMAP.md, Phase 5)

| # | Criterion | Code Status | Live Status |
|---|-----------|--------------|-------------|
| 1 | OIDC redirect sign-in before mic; gesture-gated mic with distinct error states; no-access guidance | ✓ verified (05-03, 05-04) | human_needed — real auth.klankermaker.ai round-trip pending `apps/auth` redeploy with corrected `voice` OIDC client (found+fixed in 05-03, not yet deployed) |
| 2 | Connection state machine with ICE-failure/UDP-blocked messaging + auto-retry, verified on real iPhone + restricted network | ✓ verified (05-04, 05-06) | human_needed — explicit real-device + hostile-network requirement, never self-approved by any plan |
| 3 | Live captions both sides + state-aware orb + visible countdown | ✓ verified (05-01, 05-02, 05-04, 05-05) | human_needed — real audio-driven RTVI amplitude/state and the subjective "whoa" visual quality |
| 4 | Toggleable latency HUD | ✓ verified (05-01, 05-05) | human_needed — real per-turn `kmv-latency` values from a live pipeline |
| 5 | Clean session end + one-click reconnect that re-checks quota | ✓ verified (05-06) | human_needed — real server-driven wind-down/goodbye + live quota re-check |

**Score:** 5/5 success criteria code-verified (implementation present, substantive, wired, unit-tested);
0/5 live-verified (all deferred by design per objective's explicit instruction — none FAILED).

## Requirements Coverage (CLNT-01…08)

| Requirement | Description | Code Status | Live Status |
|-------------|-------------|--------------|-------------|
| CLNT-01 | Gesture-gated mic, distinct error states | ✓ `media/getMic.ts` (7 tests), `screens/MicError.tsx` verbatim copy | human_needed |
| CLNT-02 | Connection state machine, ICE-failure/UDP-blocked messaging, auto-retry | ✓ `transport/connectionState.ts` (9 tests), `transport/retryPolicy.ts` (3 tests), `screens/UdpBlockedWall.tsx` | human_needed |
| CLNT-03 | Live captions both sides | ✓ `captions/captionReducer.ts` (7 tests), `captions/Captions.tsx`, server `RTVIProcessor`+`RTVIObserver` wiring (`rtvi.py`, `test_rtvi.py` 4 tests) | human_needed |
| CLNT-04 | State-aware audio-reactive orb | ✓ `orb/orbState.ts` (9 tests), `orb/OrbCanvas.tsx`+`OrbFallback.tsx` (WebGL2+2D fallback), `orb/useOrbBinding.ts` live RTVI wiring | human_needed |
| CLNT-05 | Visible session countdown | ✓ `timer/useCountdown.ts` (11 tests), `server.py` `session_max_seconds` in `/api/offer` answer | human_needed |
| CLNT-06 | Toggleable latency HUD | ✓ `hud/useLatencyMetrics.ts` (14 tests), server `kmv-latency` emission (`observers.py`, `test_rtvi.py` 5 tests) | human_needed |
| CLNT-07 | Clean session end + quota-rechecked reconnect | ✓ `screens/SessionEnd.tsx`, `useVoiceSession.ts` reconnect flow, `gates/gateMapping.ts` (11 tests) | human_needed |
| CLNT-08 | OIDC sign-in gate before mic | ✓ `auth/pkce.ts` (RFC 7636 vector), `auth/oidcClient.ts`, `auth/tokenStore.ts` (in-memory only, grep-confirmed) | human_needed |

No orphaned requirements — all 8 CLNT-* IDs are claimed by at least one 05-0x plan's `requirements:` frontmatter and REQUIREMENTS.md maps all 8 to Phase 5.

## Required Artifacts (Level 1-3: exists / substantive / wired)

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `apps/voice/src/klanker_voice/rtvi.py` | RTVIProcessor/Observer factories | ✓ VERIFIED | Real implementation; imported+called in `pipeline.py`/`server.py` |
| `apps/voice/src/klanker_voice/observers.py` (kmv-latency) | Per-turn latency RTVIServerMessageFrame | ✓ VERIFIED | `_build_latency_payload` keys match client's `useLatencyMetrics.ts` field-for-field |
| `apps/voice/server.py` (StaticFiles mount) | SPA served at `/`, 404-fallback to `index.html`, `/api/*` excluded | ✓ VERIFIED | `CLIENT_DIST_DIR`, `_mount_client_spa`, global 404 handler present; 6 dedicated tests (`test_server_static.py`) |
| `apps/voice/client/package.json` | Exact-pinned `@pipecat-ai/client-js@1.12.0` + `@pipecat-ai/small-webrtc-transport@1.10.5` | ✓ VERIFIED | Grep-confirmed exact versions, no caret |
| `apps/voice/client/src/orb/OrbCanvas.tsx`, `orbState.ts`, `orbShader.ts`, `particleRing.ts`, `OrbFallback.tsx` | WebGL2 shader+ring hero orb with 2D/reduced-motion fallback | ✓ VERIFIED | Feature-detected fallback wired; reactive `useReducedMotion()` (05-07 fix, was one-shot) |
| `apps/voice/client/src/auth/{pkce,oidcClient,tokenStore,useAuth}.ts` | PKCE + OIDC client + in-memory token | ✓ VERIFIED | Zero persistent-storage hits repo-wide (grep) |
| `apps/voice/client/src/screens/NoAccessGate.tsx` | D-13 verbatim exclusive-gate copy | ✓ VERIFIED | Grep-confirmed verbatim heading/body |
| `apps/voice/client/src/transport/{voiceSession,useVoiceSession,connectionState,retryPolicy}.ts` | Connect/retry/state-machine | ✓ VERIFIED | Real vendor-library workaround (documented + tested); fresh token/session per `start()`/retry (traced) |
| `apps/voice/client/src/captions/{captionReducer,Captions}.tsx` | Subtitle captions | ✓ VERIFIED | Real RTVI transcript wiring in `Live.tsx` |
| `apps/voice/client/src/timer/{useCountdown,Countdown}.tsx` | Escalating countdown | ✓ VERIFIED | Real `session_max_seconds` source, not a placeholder constant |
| `apps/voice/client/src/hud/{useLatencyMetrics,LatencyHud}.tsx` | Toggleable latency HUD | ✓ VERIFIED | Real reduction of live server messages |
| `apps/voice/client/src/gates/{gateMapping,GateCard}.tsx`, `screens/{UdpBlockedWall,SessionEnd,ConnectingRetry}.tsx` | Failure/gate/end UX | ✓ VERIFIED | All routed in `App.tsx`, none orphaned |
| `apps/voice/client/src/a11y/liveRegions.ts`, `styles/responsive.css` | Shared aria-live + mobile CSS | ✓ VERIFIED | 6 tests; `100dvh`/`safe-area-inset` grep-confirmed |
| `apps/voice/Dockerfile` (D-03 multi-stage) | node build stage → COPY dist into python:3.12-slim | ✓ VERIFIED | Read directly; matches `CLIENT_DIST_DIR` mount path exactly |
| `apps/auth/webapp/src/config/oidc.ts` ('voice' client correction) | Public PKCE client, SPA `/callback` redirect_uri | ✓ VERIFIED (code) | Correct in code; **not yet deployed** — live `auth.klankermaker.ai` still runs the pre-correction registration until `apps/auth` is redeployed |

No STUB or MISSING artifacts found. No anti-pattern debt markers (`TODO`/`FIXME`/`XXX`/`TBD`/`placeholder`) in any file touched by this phase.

## Data-Flow Trace (Level 4)

| Artifact | Data Variable | Source | Produces Real Data | Status |
|----------|---------------|--------|---------------------|--------|
| `OrbCanvas` (via `useOrbBinding`) | `state`/`amplitude` | Live RTVI events (`UserStartedSpeaking`, `BotStartedSpeaking`, `LocalAudioLevel`, `RemoteAudioLevel`) | Yes — no hardcoded fallback; `amplitude` only feeds the shader via a continuous rAF EMA loop reading real event-driven targets | ✓ FLOWING |
| `LatencyHud` (via `useLatencyMetrics`) | per-stage ms | `kmv-latency` RTVIServerMessageFrame from `observers.py` | Yes — field names traced end-to-end server→client | ✓ FLOWING |
| `Countdown` | `sessionMaxSeconds` | `/api/offer` answer's `session_max_seconds` (added by 05-05, sourced from `gate_result.session_max_seconds`) | Yes — not a placeholder; `Live.tsx` renders `<Countdown>` only when a real (`>0`) value lands | ✓ FLOWING |
| `Captions` | transcript text | RTVI `UserTranscript`/`BotTranscript` events | Yes | ✓ FLOWING |
| `GateCard` | `error_type` | `/api/offer` non-2xx JSON body (`quota.GateResult`) via `voiceSession.ts`'s fetch interceptor | Yes | ✓ FLOWING |

No HOLLOW or DISCONNECTED artifacts found — every dynamic surface traced to a real upstream data source, not a static/empty stub.

## Key Link Verification

| From | To | Via | Status |
|------|-----|-----|--------|
| `rtvi.py` `RTVIProcessor` | `pipeline.py` `build_pipeline` | placed immediately after `transport.input()` | ✓ WIRED |
| `observers.py` `LatencyReportObserver` | client-js | `kmv-latency` `RTVIServerMessageFrame` pushed via last-observed downstream processor | ✓ WIRED |
| `server.py` `StaticFiles(client/dist)` | Dockerfile `client-build` stage COPY target | identical path (`client/dist`) | ✓ WIRED |
| `tokenStore.getToken()` | `voiceSession.ts` `buildConnectParams` | `Authorization: Bearer` header, read fresh on every `connect()`/retry (traced through `useVoiceSession.beginConnect`) | ✓ WIRED |
| `quota.GateResult` `error_type` | `gateMapping.ts` → `GateCard` | 401/403/429 non-2xx `/api/offer` body → fetch interceptor → `OFFER_REJECTED` → `connectionState` `rejected` → `App.tsx` routes to `GateCard` | ✓ WIRED |
| `retryPolicy.ts` exhaustion | `UdpBlockedWall` | `retryStatus.kind === "exhausted"` routed in `App.tsx` | ✓ WIRED |
| Server `session_max_seconds` | `Countdown` | `/api/offer` answer → `voiceSession.ts` peek → `useVoiceSession` → `App.tsx` → `Live.tsx` | ✓ WIRED |
| `apps/auth/webapp` 'voice' OIDC client registration | live `auth.klankermaker.ai` | code corrected (05-03) but **not yet applied/deployed** | ⚠ PARTIAL (code-correct, deploy-pending — see Deploy Blockers below) |

## Anti-Patterns Found

None. No `TODO`/`FIXME`/`XXX`/`TBD`/`placeholder`/`coming soon`/`not yet implemented` markers in any file this phase touched. No empty-implementation stubs (`return null`/`=> {}`/hardcoded empty state feeding a render path) found outside legitimate guard clauses (verified individually: `Captions.tsx` early-return, `orbShader.ts`/`particleRing.ts` WebGL null-checks, `useAuth.ts` no-verifier guard).

## Verbatim Copy Spot-Check (UI-SPEC Copywriting Contract)

Grepped directly against source (not taken from SUMMARY claims) — all match verbatim:
- "Tap to talk" (`Attract.tsx`)
- "No microphone found. Plug one in or switch devices, then try again." (`MicError.tsx`)
- "This browser can't do live audio. Try Chrome or Safari." (`MicError.tsx`)
- "This network blocks the audio channel." + "Some Wi-Fi networks block the live-audio connection. Try switching to cellular or a personal hotspot." (`UdpBlockedWall.tsx`)
- "You're on the list — almost." + "This is an exclusive demo — Kurt needs to give you access. You'll need an access code to start a conversation." (`NoAccessGate.tsx`, reused in `gateMapping.ts`)
- "That's a wrap for today." / "You've got a conversation running already." / "The demo's resting." + matching bodies (`gateMapping.ts`)
- "Nice talking with you." (`SessionEnd.tsx`)

## What the Live AWS Validation Pass Must Exercise (consolidated from every 05-0x deferred checkpoint)

This is the single, consolidated live pass every 05-0x plan's own checkpoint folds into (05-03 → 05-07,
per STATE.md). It is a user-awake task, not autonomous. Steps, in dependency order:

1. **Close the still-open Phase-4 IAM gap first** (blocks everything below): the deployed voice task
   role must have `dynamodb:GetItem` on `kmv-auth-electro` (the Phase-3 tiers table) — without it,
   every real `/api/offer` call fails closed at `read_tier()` before the quota gate ever runs.
   (Note: git history on other branches shows this may already be addressed — `feat(04): deploy
   reconciliation — tiers-table read IAM` — confirm it has actually been applied to this branch's
   deploy target before proceeding.)
2. **Redeploy `apps/auth`** so the corrected public-PKCE `voice` OIDC client registration
   (`token_endpoint_auth_method: none`, `/callback` redirect_uri — found+fixed in 05-03) is live
   against `auth.klankermaker.ai`. The pre-correction registration is a confidential-client shape
   that will reject the SPA's redirect_uri and has no client secret to present.
3. **Build + deploy the current voice image** (with `client/dist` baked in via the D-03 Docker stage)
   to the Fargate service.
4. **PKCE sign-in round-trip:** tap-to-talk → redirect → auth.klankermaker.ai → callback → in-memory
   token (inspect devtools: confirm no `localStorage`/cookie persistence) → refresh drops the token.
5. **No-access gate:** sign in with a no-access-tier account; confirm the exact D-13 exclusive copy,
   not a raw error.
6. **Live conversation:** grant mic, hold a real exchange; confirm the orb transitions
   idle→listening→thinking→speaking and deforms to real amplitude; confirm both-side subtitle
   captions (interim gray → final).
7. **Countdown + HUD:** let a session run near its cap; confirm the countdown pill escalates
   amber→red in sync with the agent's spoken −30s warning; toggle the HUD ('H' or tap) and confirm
   real, updating per-turn STT/LLM-TTFT/TTS-first-audio/v2v-p50 numbers.
8. **Hostile network:** on a real UDP-blocked/restricted conference-style network, confirm the
   bounded auto-retry ("Reconnecting… attempt n of N") then the honest UDP-blocked wall — no
   infinite spinner — then confirm the network un-blocked (hotspot) recovers via manual retry.
9. **Typed gates against real quota state:** exhaust a tier's daily minutes, open two concurrent
   sessions, and flip `kv killswitch` — confirm each produces its own distinct verbatim gate card.
10. **Clean end + reconnect:** let a session end via the server's timer/goodbye (or force
    mid-session daily-quota exhaustion); confirm the "Nice talking with you." summary with
    `{m:ss}` spoken, then tap Reconnect and confirm it re-runs the quota start-gate (a fresh
    `/api/offer`) before reconnecting — and that a reject routes to a gate card, not a raw error.
11. **Real iPhone (Safari), one-handed:** confirm `100dvh` + safe-area insets, mic CTA ≥96px in
    the lower third, all buttons ≥44px, captions legible above the CTA.
12. **VoiceOver + iOS Reduce Motion:** confirm connection status/countdown/errors are announced
    (polite for status/countdown, assertive for errors); toggle Reduce Motion mid-session and
    confirm the shader orb swaps to the calm 2D fallback immediately (not just on reload).
13. **"Whoa" sign-off:** confirm the attract-orb landing lands the intended visual/motion
    impression (subjective — human judgment call, the acceptance feel the whole phase targets).

## Deploy Blockers Tracked (context, not phase-5 scope)

- Auth service (`apps/auth`) has not been redeployed with the 05-03 OIDC-client correction —
  sign-in cannot complete against production until it is.
- The Phase-4 cross-table IAM read gap was flagged repeatedly through 05-04/05/06/07's SUMMARYs;
  git history shows a later fix (`feat(04): deploy reconciliation — tiers-table read IAM`) exists
  on another branch/line of work — confirm it is actually applied to the deploy target used for
  the live pass above before assuming quota-gated sessions will succeed.
- Neither blocker is a Phase-5 code defect; both are pre-conditions for the live validation pass.

## Gaps Summary

No gaps. Every phase-5 code deliverable (7/7 plans, all `must_haves.truths`/`artifacts`/`key_links`
declared in the 05-01…05-07 PLAN.md frontmatter) is present, substantive, wired, and covered by a
green local test suite reproduced directly in this session (85/85 client vitest, 166/166 pytest,
33/33 auth-webapp regression, tsc clean, build clean). No stub/placeholder/debt-marker anti-patterns
found. Verbatim UI-SPEC copy confirmed by direct grep against source, not SUMMARY claims.

The phase is **not FAILED** — it is **human_needed**: every one of the 5 ROADMAP success criteria
and all 8 CLNT-01…08 requirements have an outstanding live-verification step that requires the
deployed service, a real iPhone, real quota state, and a hostile network — none of which are
available in this session, and none of which any 05-0x plan self-approved (each explicitly deferred
to this consolidated post-deploy pass, per the context provided for this verification). Status
`human_needed` reflects that the code-complete/pass-fail axis is fully green, and the remaining
work is the one live pass enumerated above.

---
_Verified: 2026-07-06_
_Verifier: Claude (gsd-verifier)_
