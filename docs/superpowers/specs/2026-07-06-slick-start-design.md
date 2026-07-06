# Slick Start — single-tap + instant greeting

**Date:** 2026-07-06
**Status:** Approved (brainstorm), pending spec review → implementation plan
**Author:** KPH + Claude (brainstorming session)
**Related:** `2026-07-04-klanker-voice-design.md` (D-04 auth, D-05 attract, greet-first),
Phase 5 client (`apps/voice/client`), `apps/voice/src/klanker_voice/pipeline.py` (greet-first),
memory `phase5-voice-pipeline-live.md` (double-tap + Mark voice swap).

## Problem

The session start feels clunky in two separable ways:

1. **Double-tap.** The access token is **in-memory only** (`tokenStore.ts`, D-04 — a
   deliberate XSS-safer choice: reload drops it, re-auth is "one redirect"). So on every
   fresh page load `auth.isAuthenticated` is `false`, and `App.handleTapToTalk` spends the
   **first tap on a sign-in redirect** to `auth.klankermaker.ai`. For a returning user whose
   next-auth session cookie is still valid, that redirect is *silent but full-page* — a jarring
   navigation "stutter" — after which the user taps **again** to actually start + grant the mic.

2. **Dead air before the greeting.** KPH already greets first on connect
   (`register_greet_first` + persona "Opening move"), but that greeting only fires **after** the
   WebRTC connection lands (~1–2s after the tap). During that gap there is silence and no clear
   signal that it's the user's turn, or that they may interrupt.

## Goals

- **One tap → talking.** A returning user taps once and goes straight to mic + connect; no
  visible sign-in bounce on the tap itself.
- **Instant, warm greeting.** The moment the user taps, KPH speaks — masking the connect gap and
  making it obvious KPH is here, speaks first, and that the user may interrupt.
- **Voice consistency.** The pre-rendered greeting always matches the *currently configured* TTS
  voice (we just swapped KPHv1→Mark; the mechanism must not drift).

## Non-goals

- Changing the in-memory-token security posture beyond the minimal breadcrumb below (no persisted
  access token — the "persist the token" option was explicitly rejected).
- Solving the mobile speakerphone self-loop in general (parked; headphones remain the demo
  belt-and-suspenders). This spec only avoids *adding* overlap between the greeting clip and live STT.
- Varying greeting wording via the LLM at runtime (the clip is pre-rendered; variety comes from a
  small random set).

## Design overview

Two independent workstreams that compose into one experience:

- **A — Single tap:** silent SSO on load (top-level `prompt=none`), gated by a returning-user
  breadcrumb, so returning visitors are already authenticated when they tap.
- **B — Instant greeting:** a small set of pre-rendered KPH greetings (rendered from the configured
  voice), one picked at random per conversation, played the instant the tap gesture fires while
  WebRTC connects underneath; the server's greet-first is disabled so KPH doesn't greet twice.

---

## Workstream A — Single tap (silent SSO on load)

### Mechanism

- **Breadcrumb** (`localStorage` key `kmv_returning=1`): set on a successful *interactive* sign-in
  (in `Callback` after a real code exchange), cleared on explicit sign-out and on a silent
  `login_required`. Holds **no token** — only "this person has signed in before on this device."
- **Silent attempt on mount** (`useAuth` / `App`): if the breadcrumb is set **and**
  `!isAuthenticated` **and** we have not already tried this load → perform **one top-level
  navigation** to the issuer `authorize` endpoint with `prompt=none` and `redirect_uri=/callback`.
  Top-level (not iframe) is the key: the `auth` session cookie flows as first-party, so **iOS
  Safari ITP does not block it** (an iframe-based silent auth would fail on the demo device).
- **Redirect-loop guard** (`sessionStorage` key `kmv_silent_tried=1`): set before the silent
  navigation, checked on mount, so returning from `/callback` does not immediately re-trigger the
  silent attempt. Cleared naturally when the tab/session ends.
- **`Callback` handles two outcomes:**
  - `?code=…` present → existing PKCE exchange → token in memory → `refresh()` → `replaceState("/")`
    → land authenticated. (Also set the breadcrumb here for the interactive path.)
  - `?error=login_required` (or `interaction_required`) present → **no** session at the issuer →
    clear the breadcrumb, `replaceState("/")`, land on Attract signed-out. No error UI — this is the
    expected "session expired" path.

### Auth server

- Confirm the `voice` OIDC client (oidc-provider, `apps/auth`) permits `prompt=none` and returns
  `login_required` to the registered `redirect_uri` when there is no next-auth session.
  **Assumption / verify-first:** oidc-provider does this by default; the `voice` client's
  `redirect_uris` already include the `/callback` URI. No new client config expected, but this must
  be confirmed against the live issuer before shipping (it is the one external dependency).

### Result / UX

- **First-ever visit** (no breadcrumb): Attract loads instantly; sign-in happens on tap exactly as
  today (unavoidable — interactive OIDC needs a redirect). First visit remains two interactions.
- **Returning visit** (breadcrumb, in-memory token gone): one **silent bounce during initial load**
  (Attract screen / orb intro covers it), then the **first tap goes straight to mic + connect**.

### Why not the alternatives (recorded)

- *Auto-resume after redirect* (carry "start" intent through the sign-in bounce): breaks on iOS —
  `getUserMedia` needs a fresh user gesture a post-redirect auto-start doesn't have.
- *Hidden-iframe `prompt=none`*: Safari ITP blocks the cross-subdomain `auth` cookie in an iframe.
- *Persist the token*: reverses the deliberate in-memory/XSS-safer D-04 decision.

---

## Workstream B — Instant pre-rendered greeting

### Greeting set (approved copy)

One picked at random per conversation. The "sounds a lot like Kurt" line is intentional and becomes
accurate again once the retrained Kurt voice is swapped back in (Mark voices it in the interim).

1. "Hey! I'm KPH — a concierge that sounds a lot like Kurt and knows his whole world. Let's dig into some of the projects. Feel free to interrupt me anytime, and just be yourself."
2. "Hey, I'm KPH. Ask me anything about Kurt, the klanker platform, or DEFCON dot run — and don't be shy, cut me off whenever you want."
3. "KPH here. I've got the lowdown on Kurt's repos and projects. Jump in with a question, interrupt me if I ramble, and let's have some fun."
4. "Hey there — I'm KPH, your KlankerMaker concierge. Curious about Kurt, the platform, or DEFCON dot run? Just start talking, and cut me off anytime."

Conventions from the persona: written for the ear (no emojis/markdown/lists), "DEFCON dot run"
spelled out for TTS.

### Build-time render + voice-drift guard

- **Source of truth:** a JSON file listing the greeting texts (e.g.
  `apps/voice/client/public/greetings/greetings.source.json`).
- **Render script** (`apps/voice/scripts/render_greetings.py`, run via `make -C apps/voice greetings`):
  reads the configured `voice_id` from `apps/voice/pipeline.toml` and each source text, calls
  ElevenLabs (`eleven_flash_v2_5`, same model as live TTS), and emits **`.mp3`** clips (MP3 for
  universal iOS Safari `<audio>` support — not opus) plus a manifest
  `greetings.manifest.json` = `[{ text, file, voiceId }]`. Rendered assets + manifest are committed
  (no build-time ElevenLabs secret needed).
- **CI drift guard:** a test asserts every `manifest.voiceId === pipeline.toml voice_id`. If someone
  swaps the voice (as we just did KPHv1→Mark) without re-running `make greetings`, **CI fails** —
  closing the stale-voice gap. Re-render is a deliberate one-command step, wired into the
  voice-swap checklist.

### Client playback + handoff

- On `useVoiceSession.start()`, after `requestMic()` succeeds (the same tap gesture that unlocks iOS
  audio playback), **immediately** pick a random manifest entry and play its `.mp3` on a dedicated
  greeting `<audio>` element, then run `beginConnect()` underneath.
- **Handoff coordination (self-loop guard):** do not surface the "Live/listening" state until
  **both** (a) the greeting clip has ended **and** (b) the session has reached `connected`. If
  connect wins the race, wait for clip-end before showing Live; if the clip ends first, remain in a
  "connecting" visual until `connected`. This keeps the greeting audio and live STT from overlapping
  (which on a speakerphone would risk feeding the greeting back into STT).
- **Stop/cleanup:** stop and release the greeting element on clip-end, on `stop()`, and on any
  transport error, so a retry/reconnect never double-plays it.

### Server

- **Config toggle** `greet_first` (e.g. in `pipeline.toml`, default **off** for the WebRTC path) so
  KPH does not greet a second time after the client clip. Prod sets it off (client owns the opening);
  a dev without rendered clips can set it on to still hear a greeting. The console/local terminal
  path keeps its own `greet_now` regardless (no client clip there).
- **Persona tweak** (`prompts/concierge.md` "Opening move"): the client plays the opening; when the
  user speaks their first turn, KPH answers directly and does **not** re-introduce itself. Keep the
  brevity/interrupt guidance.

### Edge cases

- **Connect fails after the clip played** → existing bounded retry / UDP-blocked wall takes over
  (user heard a greeting, then "reconnecting" — acceptable, no special handling).
- **User talks during the clip** → their speech is captured once `connected`; the clip is short
  (~2–4s) and the handoff guard means active STT starts at/after clip-end. Full barge-in applies to
  the *live* pipeline as normal once connected.
- **No greeting assets present** (dev before first render) → client skips clip playback gracefully
  and connect proceeds; to still hear a greeting in that dev case, flip the `greet_first` config
  toggle on. Prod always ships rendered clips with `greet_first` off.

---

## Sequence (returning user, happy path)

1. Load → breadcrumb present, no token, silent not yet tried → set `kmv_silent_tried`, top-level
   `authorize?prompt=none` → `/callback?code=…` → exchange → token in memory → `/` authenticated.
   (Attract/orb intro covers the bounce.)
2. Tap → `requestMic()` (gesture: mic granted + iOS audio unlocked) → play random greeting `.mp3`.
3. In parallel: `beginConnect()` → `/api/offer` (Bearer) → SmallWebRTC → `connected`.
4. Handoff when clip-ended AND connected → Live; KPH is listening (no second greeting) → user
   responds (may interrupt normally).

## Testing

- **Auth:** unit tests for breadcrumb set/clear transitions; the `login_required` branch in
  `Callback` (clears breadcrumb, no error UI); the `kmv_silent_tried` loop guard (no re-trigger on
  return); first-time visitor (no breadcrumb) does no silent attempt.
- **Greeting:** the CI voice-drift guard (`manifest.voiceId` vs `pipeline.toml`); a client test that
  `start()` plays a clip and defers the Live handoff until **both** clip-end and `connected`; the
  no-assets fallback.
- **Server:** greet-first disabled on the WebRTC path (no bot greeting frame on connect); console
  path still greets.

## Risks / open items

- **`prompt=none` feasibility** on the live oidc-provider + next-auth issuer is the one external
  dependency — verify against the deployed `auth.klankermaker.ai` early (feasibility spike before
  building the rest of Workstream A).
- **Voice-name accuracy:** "sounds a lot like Kurt" is voiced by Mark until the Kurt clone is
  retrained + re-swapped; re-render greetings at that point (the drift guard will remind if the
  `voice_id` changes but not if only the *timbre* behind the same id changes — retraining reuses the
  Kurt id, so also re-render on retrain by convention).
- **Self-loop:** the handoff guard avoids *added* overlap, but does not solve the general
  speakerphone loop (parked).

## Rollout

- Workstream A and B are independent and can ship in either order; A benefits from the `prompt=none`
  spike landing first. Deploy via the now-working CI build→deploy chain (client is bundled into the
  voice image).
