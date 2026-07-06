# Phase 5: Browser Client & Conference Readiness - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-05
**Phase:** 5-Browser Client & Conference Readiness
**Areas discussed:** Client stack & hosting, 'Whoa' aesthetic & the orb, On-screen composition, Failure & gate UX

---

## Client stack & hosting

| Option | Description | Selected |
|--------|-------------|----------|
| Vite + React SPA, served by voice | Vite+TS+React build mounted as static on the voice FastAPI; honors "served by the voice service" + zero extra infra, gives React + animation stack | ✓ |
| Vanilla HTML/JS/TS, served by voice | Spec-literal minimal single index.html + esbuild bundle; least tooling but hand-rolled orb/HUD | |
| Separate Next.js app | New Next.js app reusing auth HeroUI/map aesthetic; max polish but deviates from served-by-voice + adds deploy surface | |

**User's choice:** Vite + React SPA, served by voice (lives at `apps/voice/client/`)

### Build integration

| Option | Description | Selected |
|--------|-------------|----------|
| Multi-stage Docker build | Node build stage → dist/ copied into python image, StaticFiles mount; one image/deploy | ✓ |
| CI builds, committed/artifact dist | Separate CI job produces dist as artifact; keeps python image node-free but splits build | |

**User's choice:** Multi-stage Docker build

### OIDC token flow

| Option | Description | Selected |
|--------|-------------|----------|
| Auth-code + PKCE, token in memory | Full-page redirect → PKCE exchange → access token in memory → Bearer to /api/offer | ✓ |
| You decide (research safest SPA pattern) | Defer token-handling mechanics to researcher/planner | |

**User's choice:** Auth-code + PKCE, token in memory

---

## 'Whoa' aesthetic & the orb

| Option | Description | Selected |
|--------|-------------|----------|
| Full-screen immersive, orb-centric | Dark minimal near-full-bleed stage, orb is hero, restrained overlays; its own look | ✓ |
| Match auth 'map'/defcon aesthetic | Carry auth HeroUI/map look for brand continuity; busy backdrop competes with orb | |
| You decide / sketch options | Defer to UI-SPEC / sketch phase | |

**User's choice:** Full-screen immersive, orb-centric

### Orb behavior

| Option | Description | Selected |
|--------|-------------|----------|
| Audio-reactive + state-colored | Pulses/deforms to live mic + TTS amplitude, distinct color/motion per state | ✓ |
| State-colored only | Three states via color + simple animation, not audio-driven; simpler, less wow | |
| You decide | Defer orb technique to UI-SPEC/researcher | |

**User's choice:** Audio-reactive + state-colored

### Landing moment

| Option | Description | Selected |
|--------|-------------|----------|
| Live 'attract' orb + single mic CTA | Orb alive/idling on landing with one "Tap to talk" CTA; whoa before a word spoken | ✓ |
| Minimal splash, orb wakes on connect | Quieter landing, orb comes alive on connect; softens 10s whoa | |
| You decide / sketch it | Defer landing choreography to UI-SPEC/sketch | |

**User's choice:** Live 'attract' orb + single mic CTA

---

## On-screen composition

| Option | Description | Selected |
|--------|-------------|----------|
| Subtitle-style, last exchange only | Ephemeral bottom captions, current/last utterance per side, interim→final; keeps orb hero | ✓ |
| Rolling transcript log | Scrollable full-session chat log; useful record but pulls focus from orb | |
| You decide | Defer caption presentation to UI-SPEC | |

**User's choice:** Subtitle-style, last exchange only

### Latency HUD

| Option | Description | Selected |
|--------|-------------|----------|
| Off by default, rich when toggled | Hidden for clean demo; toggle reveals per-stage STT/LLM-TTFT/TTS/v2v live per turn | ✓ |
| On by default, compact | Small always-visible v2v, expandable; undercuts pure whoa for general audience | |
| You decide | Defer default + metric set to planner | |

**User's choice:** Off by default, rich when toggled

---

## Failure & gate UX

| Option | Description | Selected |
|--------|-------------|----------|
| Auto-retry (bounded) then honest wall | Retry x3 backoff + status, then honest UDP-blocked message + manual retry; no infinite spinner | ✓ |
| Manual retry only | Immediate failure + retry button, no auto-retry; simpler but worse for transient blips | |
| You decide | Defer retry count/backoff/copy to planner | |

**User's choice:** Auto-retry (bounded) then honest wall

### No-access gate + reconnect

| Option | Description | Selected |
|--------|-------------|----------|
| In-client gate + quota-rechecked reconnect | Typed rejects → gate copy; clean end summary + Reconnect button that re-runs start-gate | ✓ |
| You decide | Defer typed-reject → copy mapping to planner | |

**User's choice:** In-client gate + quota-rechecked reconnect

**Notes:** User added mid-discussion that the no-access gate should carry an
exclusive / invite-only TONE — *"Kurt needs to give you exclusive access to this"* —
reframing the access-code requirement as desirability rather than a barrier (captured
as D-13 + Specific Ideas in CONTEXT.md).

---

## Claude's Discretion

- Countdown timer placement + visual escalation near cutoff (D-10).
- Mic error-state copy: denied / no device / unsupported browser (D-12).
- Exact retry count / backoff / failure copy (D-11).
- Orb render technique (WebGL/canvas/SVG) + animation curves (D-06).
- Component library within the React SPA (or none) — bespoke immersive look.

## Deferred Ideas

- TURN fallback for UDP-blocked networks (CLNT-09 — already deferred).
- Rolling full-session transcript / export (rejected for immersive stage).
- Auth-aesthetic brand continuity in the voice client (rejected for v1).
- Accessibility / i18n / analytics — note for a hardening pass if warranted.
