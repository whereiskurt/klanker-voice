# klanker-voice — Front-of-House Flow Redesign

**Status:** Approved design (2026-07-08). Ready for implementation planning.
**Scope:** The browser client (`apps/voice/client`) interaction model only. No pipeline,
auth-service, or infra changes. The OIDC endpoints, `/api/offer` connect flow, quota
gates, and orb rendering are all reused as-is.

## 1. Why

The current front-of-house is a flat cascade in `App.tsx` with a single dual-purpose
"Tap to talk" button that both signs you in and connects. The experience is: see orb →
push button → get bounced to auth → come back → push button → chat. The auth bounce is
button-gated and feels like an interruption, and there is no explicit "I am about to
start a conversation" moment.

The redesign makes the flow a deliberate, ceremonial linear sequence:

1. Arriving unauthenticated bounces you **straight to auth** — no landing page, no button.
2. Once authenticated you land on a **"Let's start talking"** decision screen.
3. Starting a chat plays a **theatrical boot-up ceremony** ("paging KPH…") while the real
   connection establishes underneath.
4. The live stage is a **compact orb header over a growing, scrollable transcript** of the
   whole conversation — replacing today's single-line fading caption band.
5. **End chat** returns you to the start screen; starting again replays the full ceremony.

**Core value preserved:** the ≤1.2s voice-to-voice loop and the "whoa" orb are untouched.
The ceremony is theatre layered on top of the same connect path, and the transcript is a
pure client-side render of RTVI frames already being received.

## 2. State machine

The client is a linear foreground machine with four primary states, plus the existing
error/gate screens as interrupts reachable from `CEREMONY`.

```
LAND
  │  attemptSilentSso()  (prompt=none, invisible — already implemented in useAuth)
  ├─ authenticated ─────────────────► READY_TO_START
  └─ login_required / no token ─────► [auto beginSignIn() full redirect]
                                          └─► auth.klankermaker.ai ─► /callback ─► READY_TO_START

READY_TO_START
  │  authenticated-only screen: idle orb + "Let's start talking" CTA
  │  (no-access tier → NoAccessGate instead of the CTA)
  └─ CTA click (== iOS mic/audio unlock gesture) ──► CEREMONY

CEREMONY
  │  theatrical script plays on a fixed timeline
  │  voice.start() runs the REAL connect concurrently (mic → /api/offer → ICE → bot-ready)
  ├─ script done AND connected ─────► LIVE
  ├─ quota rejection ───────────────► GateCard      (existing)
  ├─ UDP/ICE exhausted ─────────────► UdpBlockedWall (existing)
  └─ mic permission denied ─────────► MicError       (existing)

LIVE
  │  compact orb header + growing scrollable Transcript + End chat
  │  KPH greeting plays on entry (existing greeting player)
  └─ End chat / session cap / disconnect ──► ENDED

ENDED
  │  SessionEnd summary (existing, adapted)
  └─ "Start another" ──► READY_TO_START   (replays full CEREMONY next time)
```

### 2.1 Mapping to today's code

| Today | After |
|-------|-------|
| `Attract.tsx` (orb + "Tap to talk", unauthenticated landing) | **Retired.** Unauthenticated users never see a client screen. |
| Dual-purpose `handleTapToTalk` (auth *or* connect) | Split: auth is automatic on `LAND`; connect is the `READY_TO_START` CTA. |
| Silent SSO falls back to `Attract` on `login_required` | Falls back to **automatic `beginSignIn()`** full redirect. |
| `captionReducer` + `Captions.tsx` (last-exchange-only fade band) | **Replaced** by an append-only transcript reducer + `Transcript.tsx`. |
| `Live.tsx` orb hero + caption band | `Live.tsx` compact orb header + scrollable transcript column. |
| n/a | New `ReadyToStart.tsx` and `Ceremony.tsx` screens. |
| `SessionEnd.tsx` "reconnect" | Adapted: primary action returns to `READY_TO_START`. |

`Callback.tsx`, `NoAccessGate.tsx`, `GateCard.tsx`, `UdpBlockedWall.tsx`, `MicError.tsx`,
`ConnectingRetry.tsx`, the orb (`OrbCanvas` + shaders), `useAuth`, `useVoiceSession`, and
the greeting player are all reused unchanged (or with only wiring changes).

## 3. Screen contracts

### 3.1 LAND (auth bounce)

- On mount: render a minimal, brand-consistent "checking…" holding view (idle orb, no
  controls) so there is no flash of an unauthenticated landing.
- Run `attemptSilentSso()` (already guarded internally: returning-user AND not
  authenticated AND not already tried this load).
- Resolution:
  - **authenticated** → `READY_TO_START`.
  - **login_required / interaction_required / no token** → automatically call
    `beginSignIn()` (full interactive redirect). No button.
- **Loop guard (critical):** a one-shot session flag records that a *full interactive*
  redirect has already been attempted this browser session. If the user returns from a
  full redirect still unauthenticated (they bailed at the auth screen), do **not**
  re-bounce. Instead render a minimal "Sign-in needed" nudge with a single manual
  "Sign in" button. This preserves the existing anti-bounce protection called out in the
  current `handleAuthenticated` breadcrumb logic; the redesign must not regress it.

### 3.2 READY_TO_START

- Authenticated-only. Idle orb with ambient motion (zero amplitude), wordmark top-left.
- One prominent CTA: **"Let's start talking"**. Sub-line: short concierge framing.
- The CTA click is the single user gesture that unlocks the mic and audio playback (iOS
  requirement) — this is the architectural reason the connect cannot be automatic.
- If `auth.tierId === NO_ACCESS_TIER_ID`: render `NoAccessGate` instead of the CTA.
- On CTA click → transition to `CEREMONY` and immediately begin `voice.start()`.

### 3.3 CEREMONY

- **Theatrical script:** an ordered list of narration lines, each shown for a fixed
  duration, e.g.:
  1. "initializing…"
  2. "paging KPH…"
  3. "let me see if he's out there…"
  4. "do do do…"
  5. "he's warming up…"
  Copy is a config constant so it can be tuned without touching logic. Visual treatment:
  the orb in a distinct "booting" look (pulsing / spinning ring) with the current line
  beneath it.
- **Honest seam (the one piece of logic beyond a dumb timer):** the script plays on its
  own fixed timeline, but the flip to `LIVE` fires at **max(script finished, connection
  reached `connected`)**.
  - Normal case: connect completes before the script does → feels perfectly scripted.
  - Slow network: the script reaches its last line and **holds** ("almost there…") until
    `connected`, rather than dropping the user onto a not-yet-live orb.
- **Interrupts** (connect resolves to a non-connected terminal state):
  - quota rejection → `GateCard` (existing typed rejection copy).
  - retry exhausted / UDP-blocked → `UdpBlockedWall`.
  - mic permission denied → `MicError`.
  These reuse today's routing; the ceremony is simply the waiting surface that precedes
  them.

### 3.4 LIVE

Layout (chosen: **orb top, transcript below**):

```
┌───────────────────────────────┐
│         ( compact orb )        │   live-driven state + amplitude (useOrbBinding)
├───────────────────────────────┤
│  KPH   hey, what's up          │   ▲ growing, append-only
│  You   tell me about km        │   │ auto-scrolls to bottom
│  KPH   Kurt built it to…       │   │ UNLESS the user has scrolled up
│  You   …                       │   ▼
│                                │
│   [ End chat ]      ⏱ 4:12      │   End chat + existing Countdown pill
└───────────────────────────────┘
```

- **Compact orb header:** the same `OrbCanvas`, sized to a top band rather than full-bleed
  hero, still driven by real RTVI state/amplitude via `useOrbBinding`.
- **Transcript column:** the main scrolling region (see §4).
- **End chat** button → `ENDED`. Reuses the session teardown already triggered on session
  cap / disconnect so slot release stays correct (per the known concurrency-slot-leak fix
  — teardown must release the slot exactly once).
- **Countdown** pill and **LatencyHud** carry over unchanged.
- KPH greeting plays once on entry via the existing greeting player.

### 3.5 ENDED

- `SessionEnd` summary (elapsed, reason) carries over.
- Primary action relabeled to **"Start another"** → `READY_TO_START` (which then replays
  the full ceremony on the next start — intended, confirmed).
- Secondary action: **Sign out**.

## 4. Transcript data model (the core code change)

Replace the last-exchange-only caption model with an append-only ordered log.

```ts
interface TranscriptTurn {
  id: string;            // stable key; monotonic counter per session
  speaker: "user" | "agent";
  text: string;
  final: boolean;        // false = interim (provisional, gray), true = firmed
}

type TranscriptState = TranscriptTurn[];   // chronological, append-only
```

**Reducer events** (fed by the same RTVI frames `Live.tsx` already subscribes to):

- `USER_TRANSCRIPT { text, final }`
  - If the last turn is a **non-final user** turn → update it in place (interim firming).
  - Otherwise → **append** a new user turn.
  - A `final: true` user turn is never mutated again; the next user frame appends.
- `AGENT_TRANSCRIPT { text }` (sentence-aggregated, already final chunks)
  - If the last turn is an **agent** turn → append the chunk to its text (multi-sentence
    reply within one turn).
  - Otherwise → append a new agent turn.
- `RESET` → `[]` (only on entering a brand-new session, not mid-conversation).

**Why this kills the "transcript permanently dies" bug.** Today's `captionReducer` holds
only the current user/agent lines and *clears* them on exchange boundaries; any desync in
the interim/final firming (a dropped or duplicated `final` frame) can leave it stuck in a
cleared state that never re-renders. The append-only model **has no clearing path** — every
frame either updates the tail turn or appends a new one — so the entire failure class is
gone. The exact original trigger should be confirmed during implementation, but the rewrite
is the fix, not a patch on the fragile reducer.

**Scroll behavior** (`Transcript.tsx`):

- Auto-scroll to bottom on new/updated turn **only if** the user is already pinned near
  the bottom (within a small threshold).
- If the user has scrolled up to read history, **do not** yank them down; show an optional
  "jump to latest" affordance.
- Interim (non-final) turns render provisional (e.g. reduced opacity); final turns render
  solid. Speaker chip ("You" / "KPH") per turn, matching current caption chip styling.
- Text is always rendered as plain React text, never HTML (preserves the T-05-04-T XSS
  disposition from the current `Captions` component).

## 5. Out of scope

- No changes to the Python pipeline, `bot.py`, `server.py`, or `/api/offer`.
- No changes to the auth service or OIDC provider.
- No transcript **persistence** (the S3/Athena utterance ledger is a separate, server-side
  concern; this transcript is client-render-only and discarded on session end).
- No new provider/model choices.

## 6. Risks & mitigations

| Risk | Mitigation |
|------|------------|
| Auth bounce loop for a user who bails at the auth screen | One-shot full-redirect session flag → manual "Sign in" nudge (§3.1). |
| Theatrical script "lies" (says ready before connected) | max(script, connected) seam; hold on last line if connect is slow (§3.3). |
| Slot leak when End chat / disconnect race | Route End chat through the same single-release teardown as session cap (§3.4). |
| Growing transcript memory over a long session | Sessions are quota/time-capped; log is bounded by session length. If needed, cap retained turns to a rolling window (defer until measured). |
| Removing `Attract` breaks existing tests (`App.gate.test.tsx`, `App.breadcrumb.test.tsx`) | Update tests to the new machine as part of implementation; the breadcrumb anti-loop assertions must be preserved, not deleted. |

## 7. Deliverables

1. **This spec** — the build-from artifact.
2. **`docs/superpowers/specs/mockups/2026-07-08-voice-flow-redesign-mockup.html`** — a
   single self-contained, throwaway HTML mockup (no app wiring) that clicks through:
   forced-auth stub → "Let's start talking" → ceremony animation → live orb + growing
   transcript + End chat → back to start. For feel/layout validation only; not production
   code.

## 8. Implementation notes for the eventual plan

- New files: `screens/ReadyToStart.tsx` (+css), `screens/Ceremony.tsx` (+css),
  `transcript/transcriptReducer.ts`, `transcript/Transcript.tsx` (+css).
- Changed: `App.tsx` (linear machine + forced-auth bounce + loop guard), `Live.tsx`
  (compact orb + transcript), `SessionEnd.tsx` (relabel action).
- Retired: `screens/Attract.tsx` (+css), `captions/Captions.tsx`,
  `captions/captionReducer.ts` (and their tests) — migrate the XSS-safe rendering and
  chip styling into `Transcript.tsx`.
- Reused unchanged: orb, `useAuth`, `useVoiceSession`, `Callback`, `NoAccessGate`,
  `GateCard`, `UdpBlockedWall`, `MicError`, `Countdown`, `LatencyHud`, greeting player.
- Tests to add: `transcriptReducer` (interim firming, append-on-new-speaker, agent
  concatenation, no-clear invariant), the LAND loop guard, the ceremony seam
  (max(script, connected)).
