# Phase 5: Browser Client & Conference Readiness - Context

**Gathered:** 2026-07-05
**Status:** Ready for planning

<domain>
## Phase Boundary

Deliver the public-facing web experience at **voice.klankermaker.ai**: a member of
the public signs in via OIDC, taps the mic through a gesture-gated flow, and holds a
slick, immersive conversation with the KlankerMaker concierge — with a state-aware
orb, live captions, a session countdown, a toggleable latency HUD, honest failure
handling on hostile networks, and a clean end → quota-rechecked reconnect. Proven on
real phones and restricted conference-style networks. (CLNT-01…08)

**This phase is the client only.** The voice pipeline, `/api/offer` signaling, quota
start-gate, typed rejections, session lifecycle/wind-down, and OIDC issuer already
exist (Phases 1/3/4). Phase 5 builds the browser surface on top and hardens it for
the conference.

**Not in scope:** TURN fallback for UDP-blocked networks (CLNT-09, deferred); any
change to the pipeline/latency (Phase 6); knowledge/RAG (Phase 7).

</domain>

<decisions>
## Implementation Decisions

### Client stack & hosting
- **D-01:** Client is a **Vite + TypeScript + React SPA** built to static assets and
  **served by the voice FastAPI service** via `StaticFiles` — honors the design spec's
  "single-page client served by the voice service" while giving a real component +
  animation stack for the orb/HUD/captions. No separate app, no separate deploy target.
- **D-02:** Client lives at **`apps/voice/client/`** (source) → `dist/` mounted by
  `server.py`. It is NOT a separate Next.js app and does NOT reuse the auth webapp's
  HeroUI/map aesthetic — the voice client is its own immersive look (see D-05).
- **D-03:** Built into the deployed image via a **multi-stage Docker build**: a Node
  build stage runs `npm ci && npm run build`; `dist/` is `COPY --from`'d into the
  final `python:3.12-slim` image and mounted via `StaticFiles`. One image, one deploy,
  CI builds both toolchains. No committed build artifacts.
- **D-04:** Auth uses **authorization-code + PKCE**: full-page redirect to
  auth.klankermaker.ai, code+PKCE exchange in the SPA, **access token held in memory**
  (not localStorage — XSS-safer; re-auth on refresh), sent as `Bearer` to `/api/offer`.
  The `voice` OIDC client + a client-side callback route must be registered/wired
  (planner detail; design spec §6 already names `voice` as an authz-code+PKCE client).

### 'Whoa' aesthetic & the orb
- **D-05:** **Full-screen immersive, orb-centric** visual direction — dark, minimal,
  near-full-bleed stage with the orb as the hero; captions/timer/HUD are restrained
  overlays. Purpose-built for the demo "whoa"; its own look, not the auth aesthetic.
- **D-06:** The orb (CLNT-04) is **audio-reactive + state-colored**: it pulses/deforms
  to live audio amplitude (mic level while listening; agent TTS output while speaking)
  AND carries a distinct color/motion per state (listening / thinking / speaking).
  RTVI exposes audio levels + bot-speaking events. Render technique (WebGL/canvas/SVG)
  is Claude/researcher discretion against feasibility + the latency budget.
- **D-07:** **Landing = live "attract" orb + single mic CTA.** The orb is already alive
  and idling (ambient motion) on the landing screen with one prominent "Tap to talk"
  CTA; the "whoa" lands before a word is spoken. Tapping triggers the gesture-gated
  mic + connect.

### On-screen composition
- **D-08:** Captions (CLNT-03, both sides) are **subtitle-style, last-exchange-only** —
  ephemeral near the bottom, current/last utterance per side, interim (gray) firming to
  final, fading as the conversation moves on. Keeps the orb the hero. NOT a rolling
  full-session transcript log.
- **D-09:** Latency HUD (CLNT-06) is **off by default**, revealed by a toggle (key/tap),
  showing a **rich per-stage breakdown** live per turn — STT / LLM-TTFT / TTS-first-audio
  / voice-to-voice p50. Doubles as the tuning instrument. Pristine for audiences, deep
  for the operator. **Requires the server to emit per-stage timings to the client via
  RTVI metrics frames** — `observers.py` already computes the breakdowns server-side but
  they are not yet surfaced per-turn to the client (flagged for researcher).
- **D-10:** Countdown timer (CLNT-05) is a **small persistent corner countdown** that
  escalates visually near cutoff, synced to the spoken −30s warning (Claude discretion,
  consistent with immersive-minimal).

### Failure & gate UX
- **D-11:** Connection failures (CLNT-02) use **bounded auto-retry + backoff → honest
  wall**: on ICE/connect failure, auto-retry a few times with visible status
  ("Reconnecting…"); if it still fails (likely UDP-blocked, no TURN in v1), STOP and
  show a clear honest message ("This network blocks the audio channel — try
  cellular/hotspot") with a manual retry. No infinite spinner. Retry count/backoff +
  exact copy are planner detail against pipecat/small-webrtc transport events.
- **D-12:** Mic error states (CLNT-01) are **distinct inline messages**: *denied*
  ("mic blocked — enable in browser settings"), *no device* ("no microphone found"),
  *unsupported browser* ("try Chrome or Safari"). (Claude discretion, honest-error style.)
- **D-13:** **No-access gate (D-07 carried from Phase 3) uses an exclusive / invite-only
  TONE** — reframe the barrier as desirability, not a dead-end. Copy direction:
  *"This is an exclusive demo — Kurt needs to give you access. You'll need an access
  code to start a conversation."* Aspirational, makes the visitor want in. Applies to
  the no-access tier and (adapted) to exhausted-quota / killswitch gates.
- **D-14:** **In-client gate + quota-rechecked reconnect** (CLNT-07): each **typed
  start-gate rejection** (no-access, daily-exhausted, over-concurrent, killswitch) maps
  to specific in-client gate copy. On clean session end (timer/quota/goodbye), show a
  brief summary + one "Reconnect" button that **re-runs the quota start-gate before
  reconnecting**.

### Claude's Discretion
- Orb render technique (WebGL vs canvas vs SVG) and exact animation curves.
- Countdown timer placement/visual-escalation details (D-10).
- Mic-error copy wording (D-12) and exact retry count/backoff/copy (D-11).
- Component library within the React SPA (or none) — the immersive look is bespoke, so
  a heavy UI kit is likely unnecessary; researcher/planner choose.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Authoritative design & requirements
- `docs/superpowers/specs/2026-07-04-klanker-voice-design.md` §Frontend, §Transport &
  scaling, §Session lifecycle, §Failure handling — locks "single-page client served by
  the voice service," OIDC redirect, mic button → captions/waveform/timer, auto-retry,
  UDP-blocked limitation, one-click reconnect.
- `.planning/REQUIREMENTS.md` — CLNT-01…08 (browser client) and CLNT-09 (deferred TURN).
- `.claude/CLAUDE.md` — Frontend-client tech-stack pins: `@pipecat-ai/client-js`
  **1.12.0** + `@pipecat-ai/small-webrtc-transport` **1.10.5** (RTVI events drive
  captions + bot-speaking state; pin both exactly, released independently).

### Server integration surface (already built)
- `apps/voice/server.py` — FastAPI `/api/offer` (SmallWebRTC signaling, Bearer token)
  + `/health`; this is where the client `StaticFiles` mount is added.
- `apps/voice/src/klanker_voice/quota.py` — typed `GateResult` / start-gate rejections
  the client maps to gate UI (D-14); note quota.py:89 explicitly cites "the Phase-5
  client maps."
- `apps/voice/src/klanker_voice/observers.py` — per-stage latency breakdowns; source
  for the HUD once surfaced to the client via RTVI metrics (D-09).
- `apps/voice/src/klanker_voice/session.py` — session lifecycle + reconnect-grace hooks
  the client's session-end/reconnect flow (D-14) coordinates with.
- `apps/voice/Dockerfile` — multi-stage build target for D-03.

### Carried-forward decisions
- `.planning/phases/03-auth-service-access-codes/03-CONTEXT.md` — D-07 no-access tier;
  `voice` registered as OIDC authz-code+PKCE client; the "need a code" moment moved to
  this client.
- `.planning/phases/04-voice-service-deployed-quota-enforcement/04-CONTEXT.md` — typed
  rejects, spoken wind-down (−30s warning / goodbye), idle teardown + reconnect grace.

### Aesthetic contrast reference (NOT a template to copy)
- `apps/auth/webapp/` — existing Next.js 16 + HeroUI + Tailwind v4 + framer-motion +
  map/defcon aesthetic + theme-switch. The voice client is deliberately its OWN
  immersive look (D-05), but this shows brand tone and the auth→voice hand-off.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `pipecat_ai_prebuilt/` (installed in the venv) — pipecat's vanilla prebuilt client is
  a working reference for the client-js + small-webrtc-transport wiring (offer → ICE →
  RTVI events), even though we build a bespoke React SPA rather than use it as-is.
- `apps/voice/server.py` FastAPI app — add a `StaticFiles` mount for `client/dist`; the
  `/api/offer` + `/health` routes already exist and are the client's only backend contract.
- `observers.py` stage-breakdown computation — already produces STT/LLM/TTS/v2v timings
  for the harness; the HUD reuses these once emitted as RTVI metrics frames.

### Established Patterns
- Auth webapp (`apps/auth/webapp`) proves the Next.js/HeroUI/Tailwind toolchain in-repo;
  the voice client intentionally diverges to a Vite+React SPA served by the voice service.
- Typed start-gate rejections (`quota.GateResult`) are the established contract the
  client maps to UI — no new API shape needed for the gate/quota UX.
- OIDC issuer + `voice` client registration pattern already lands in Phase 3.

### Integration Points
- Client `StaticFiles` mount in `server.py` (new).
- Bearer access token → `POST /api/offer` (existing endpoint, existing offline JWT
  validation).
- RTVI events (transcripts, bot-speaking, audio levels, metrics) → orb/captions/HUD.
- OIDC authz-code+PKCE callback route in the SPA ↔ auth.klankermaker.ai.

</code_context>

<specifics>
## Specific Ideas

- **No-access gate tone (user, this session):** frame it as *"Kurt needs to give you
  exclusive access to this"* — invite-only/exclusive, aspirational, not a dead-end
  barrier. This is the emotional register for the no-access panel (D-13).
- **"Whoa in the first ten seconds"** is the acceptance feel — the landing attract orb
  (D-07) is the mechanism, before any conversation starts.
- Immersive orb-centric stage (D-05) is the deliberate contrast to the auth app's
  busy map background — nothing competes with the orb.

</specifics>

<deferred>
## Deferred Ideas

- **TURN fallback for UDP-blocked networks** — CLNT-09, already deferred in
  REQUIREMENTS.md; v1 shows the honest wall (D-11). Revisit when measured failure rate
  justifies coturn/Cloudflare TURN.
- **Rolling full-session transcript / export** — considered for captions (D-08) but
  rejected for the immersive stage; could be a post-conference "session history" feature.
- **Auth-aesthetic brand continuity in the voice client** — considered (D-05) and
  rejected for v1 in favor of the bespoke immersive look; revisit if brand unification
  becomes a goal.
- **Accessibility / i18n / analytics** — not raised as v1 must-haves; note for a
  hardening pass if the conference audience warrants it.

</deferred>

---

*Phase: 5-Browser Client & Conference Readiness*
*Context gathered: 2026-07-05*
