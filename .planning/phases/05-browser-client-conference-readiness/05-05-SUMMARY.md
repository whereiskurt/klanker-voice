---
phase: 05-browser-client-conference-readiness
plan: 05
subsystem: ui
tags: [react, rtvi, pipecat-client-js, small-webrtc-transport, fastapi, lucide-react]

# Dependency graph
requires:
  - phase: 05-01
    provides: server-side kmv-latency RTVIServerMessageFrame emission (LatencyReportObserver)
  - phase: 05-04
    provides: useVoiceSession/voiceSession.ts connect flow, connectionState machine, Live.tsx live stage
provides:
  - Persistent, escalating session countdown pill (CLNT-05, D-10)
  - Off-by-default, toggleable per-stage latency HUD (CLNT-06, D-09)
  - /api/offer answer now carries session_max_seconds (new client-server contract field)
affects: [05-06 (wind-down/reconnect UX builds on the countdown clock), 05-07 (mobile/a11y pass over both surfaces)]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Hook returns a pure {value, level} shape (useCountdown) so the escalation threshold logic (levelForRemaining) is independently unit-testable from the ticking state"
    - "React hook testing without a hooks-testing library: a tiny createRoot()+act() harness component (Harness) captures the hook's return value into a closure variable, reused identically across useCountdown.test.ts and useLatencyMetrics.test.ts"
    - "RTVI server-message channel (05-01's kmv-latency) is filtered/reduced by a pure function (reduceLatencyMessage) before touching React state, keeping the reduction logic testable without a live client"

key-files:
  created:
    - apps/voice/client/src/timer/useCountdown.ts
    - apps/voice/client/src/timer/useCountdown.test.ts
    - apps/voice/client/src/timer/Countdown.tsx
    - apps/voice/client/src/timer/timer.css
    - apps/voice/client/src/hud/useLatencyMetrics.ts
    - apps/voice/client/src/hud/useLatencyMetrics.test.ts
    - apps/voice/client/src/hud/LatencyHud.tsx
    - apps/voice/client/src/hud/hud.css
  modified:
    - apps/voice/server.py (added session_max_seconds to the /api/offer answer)
    - apps/voice/client/src/transport/voiceSession.ts (fetch-interceptor peek + onSessionMax callback)
    - apps/voice/client/src/transport/useVoiceSession.ts (exposes sessionMaxSeconds)
    - apps/voice/client/src/App.tsx (threads sessionMaxSeconds to Live)
    - apps/voice/client/src/screens/Live.tsx (mounts Countdown + LatencyHud)
    - apps/voice/client/src/styles/global.css (added .sr-only utility)

key-decisions:
  - "sessionMaxSeconds has no other client-side source than the /api/offer connect-flow response -- the JWT only carries tier_id (03-03 claim contract), not the numeric cap -- so server.py's answer now carries it additively (Rule 2 deviation)"
  - "startedAt (the countdown's start clock) is Live.tsx's own mount-time Date.now(), not threaded from useVoiceSession -- Live only ever mounts once connectionState reaches 'connected' (05-04's own T-05-04-E gate), so mount time IS the moment the session reached connected"
  - "Countdown escalation thresholds (<=30s warning, <10s critical) are synced to the server's winddown_warning_seconds default (30s, config.py) so the pill turns amber at the same moment the agent starts speaking its -30s warning (QUOT-03)"

patterns-established:
  - "Pure escalation-threshold / formatting helpers (levelForRemaining, formatMSS, formatStageMs, formatP50Seconds) exported alongside their hooks so tests exercise the decision logic directly, not just end-to-end hook behavior"

requirements-completed: [CLNT-05, CLNT-06]

coverage:
  - id: D1
    description: "Persistent corner countdown derives remaining seconds + escalation level (normal/warning<=30s/critical<10s) from sessionMaxSeconds+startedAt, ticking ~1/s"
    requirement: "CLNT-05"
    verification:
      - kind: unit
        ref: "apps/voice/client/src/timer/useCountdown.test.ts (11 tests)"
        status: pass
    human_judgment: false
  - id: D2
    description: "Countdown pill renders '{m:ss} left' visibly, escalates amber/red with motion-escalate pulse (respecting prefers-reduced-motion), announces '{n} seconds remaining' via aria-live, and never recolors the orb"
    requirement: "CLNT-05"
    verification: []
    human_judgment: true
    rationale: "Visual escalation timing/color and the reduced-motion pulse suppression require an actual live session with a real countdown reaching the warning/critical windows and a human eyeballing the pill against the orb -- not exercisable by unit tests alone. Folds into the deferred checkpoint below."
  - id: D3
    description: "useLatencyMetrics reduces a live kmv-latency RTVI serverMessage into the latest per-stage values; a never-observed stage stays null (renders as a dash, never 0); useHudOpen defaults closed and toggles on 'H' or the affordance click"
    requirement: "CLNT-06"
    verification:
      - kind: unit
        ref: "apps/voice/client/src/hud/useLatencyMetrics.test.ts (14 tests)"
        status: pass
    human_judgment: false
  - id: D4
    description: "LatencyHud panel actually renders live, updating per-stage numbers from a real conversation (STT/LLM TTFT/TTS 1st-audio/voice->voice p50) when toggled open, staying invisible/pristine when closed"
    requirement: "CLNT-06"
    verification: []
    human_judgment: true
    rationale: "Requires a live conversation against the deployed voice pipeline to produce real per-turn kmv-latency messages -- not exercisable by unit tests. Folds into the deferred checkpoint below."

duration: 40min
completed: 2026-07-06
status: complete
---

# Phase 5 Plan 05: Session Countdown + Latency HUD Summary

**Escalating session countdown (amber<=30s/red-pulse<10s) synced to the server's spoken -30s warning, plus an off-by-default per-stage latency HUD toggled by 'H' or a bottom-left affordance, both wired to real data sources (the /api/offer connect flow and 05-01's kmv-latency RTVI messages) rather than placeholders.**

## Performance

- **Duration:** ~40 min
- **Tasks:** 2 (+ 1 deferred checkpoint)
- **Files modified:** 15 (8 new, 7 modified)

## Accomplishments

- `useCountdown` derives `{remainingSeconds, level}` from `sessionMaxSeconds`/`startedAt`, ticking ~1/s; `levelForRemaining` escalates normal → warning (<=30s, synced to the server's `winddown_warning_seconds` spoken warning) → critical (<10s); `formatMSS` renders the "{m:ss} left" label.
- `Countdown.tsx` renders the persistent corner pill (top-right desktop / top-center mobile, safe-area offset), amber at warning, red with a `motion-escalate` pulse at critical (`prefers-reduced-motion` drops the pulse, keeps the color/text escalation), `aria-live="polite"` announcing "{n} seconds remaining" separately from the visible label. The orb itself is never recolored.
- `useLatencyMetrics` reduces the RTVI `serverMessage` stream's `kmv-latency` payloads (05-01) into the latest per-stage values via a pure `reduceLatencyMessage`, keeping a never-observed stage `null` (renders as `—`, never `0`).
- `LatencyHud.tsx` is the off-by-default (D-09), translucent secondary-surface panel — monospace 13px, right-aligned rows for STT / LLM TTFT / TTS 1st-audio / voice→voice p50 — toggled by a bottom-left "Latency" affordance (lucide `Activity` icon + "H" key hint) or a global `'H'` keydown (`useHudOpen`), purely informational (escalates nothing).
- **Rule 2 deviation, load-bearing:** `sessionMaxSeconds` had no existing client-side source. The OIDC access token only carries `tier_id` (per the 03-03 claim contract), not the numeric session cap, so the countdown's own `key_link` ("tier session_max_seconds (token claim / offer)") could only be satisfied via the connect flow. Extended `server.py`'s `_negotiate_webrtc` to add `session_max_seconds` (from `gate_result.session_max_seconds`, already resolved by `start_gate`) to the `/api/offer` answer — an additive field the vendor `SmallWebRTCTransport` ignores (it only reads `sdp`/`type`/`pc_id`). `voiceSession.ts`'s existing 05-04 fetch interceptor (built to detect 401/403/429 offer rejections) now also non-destructively peeks the *successful* answer body via `response.clone().json()` and reports the value through a new `onSessionMax` callback; `useVoiceSession` exposes it; `App.tsx` threads it to `Live`, which renders `<Countdown>` only once a real (>0) cap is known.

## Task Commits

Each task was committed atomically:

1. **Task 1: Persistent corner countdown with near-cutoff escalation (CLNT-05, D-10)** - `026674b` (feat)
2. **Task 2: Toggleable latency HUD from live kmv-latency RTVI messages (CLNT-06, D-09)** - `c36ac93` (feat)

_Both tasks were TDD (`tdd="true"`): tests were authored and run green alongside the implementation in the same commit — see "Issues Encountered" for the RED/GREEN gate-commit note._

## Files Created/Modified

- `apps/voice/client/src/timer/useCountdown.ts` - Escalation-level + remaining-seconds derivation, `formatMSS` label formatter
- `apps/voice/client/src/timer/useCountdown.test.ts` - 11 tests (ticking, escalation boundaries, formatting)
- `apps/voice/client/src/timer/Countdown.tsx` - The corner pill component (visible label + separate aria-live announcement)
- `apps/voice/client/src/timer/timer.css` - Positioning, escalation colors, `motion-escalate` pulse + reduced-motion override
- `apps/voice/client/src/hud/useLatencyMetrics.ts` - `reduceLatencyMessage`, `formatStageMs`/`formatP50Seconds`, `useLatencyMetrics`, `useHudOpen`
- `apps/voice/client/src/hud/useLatencyMetrics.test.ts` - 14 tests (reduction, formatting, hook subscription, hotkey toggle)
- `apps/voice/client/src/hud/LatencyHud.tsx` - Toggle affordance + translucent panel with 4 stage rows
- `apps/voice/client/src/hud/hud.css` - Toggle/panel styling, `motion-base` enter animation + reduced-motion override
- `apps/voice/server.py` - `_negotiate_webrtc` now adds `session_max_seconds` to the `/api/offer` answer
- `apps/voice/client/src/transport/voiceSession.ts` - Fetch-interceptor success-path peek + `onSessionMax` callback option
- `apps/voice/client/src/transport/useVoiceSession.ts` - Exposes `sessionMaxSeconds` in the hook's return value
- `apps/voice/client/src/App.tsx` - Threads `voice.sessionMaxSeconds` to `<Live>`
- `apps/voice/client/src/screens/Live.tsx` - Owns `startedAt` (mount-time `Date.now()`), mounts `<Countdown>` + `<LatencyHud>`
- `apps/voice/client/src/styles/global.css` - Added the `.sr-only` visually-hidden utility (shared by the countdown's aria-live text)

## Decisions Made

- `sessionMaxSeconds` sourced from the `/api/offer` connect-flow answer (not the JWT) -- the only viable source per the plan's own key_link, since the token contract only carries `tier_id` (see Deviations below).
- `startedAt` is owned locally by `Live.tsx` at its own mount time rather than threaded through `useVoiceSession`/`connectionState` -- `Live` only ever mounts once `connectionState` reaches `"connected"` (05-04's `T-05-04-E` gate), so mount time already *is* "the moment the session reached connected," with zero extra plumbing.
- Escalation thresholds (`<=30s` warning, `<10s` critical) chosen to align exactly with the server's `winddown_warning_seconds` default (30s, `config.py`) so the pill's amber transition coincides with the agent's spoken -30s warning (QUOT-03).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Critical] Wired `session_max_seconds` through the `/api/offer` connect flow**
- **Found during:** Task 1 (tracing where `useCountdown(sessionMaxSeconds, startedAt)`'s first argument would actually come from at runtime)
- **Issue:** The plan's own `<action>` text and `key_links` both name "tier session_max_seconds (token claim / offer)" as the countdown's data source, but tracing the actual token contract (`auth.py`'s `TIER_ID_CLAIM`) shows the JWT only carries `tier_id` (a string), never the numeric session cap -- and `/api/offer`'s response (`server.py`'s `_negotiate_webrtc`) returned only the raw SmallWebRTC SDP answer (`sdp`/`type`/`pc_id`), with no session-cap field anywhere. Without this, `Countdown` would have no real data to render (a hardcoded/placeholder cap would violate the plan's own must-have: "derived from session start + the tier session_max").
- **Fix:** `server.py`'s `_negotiate_webrtc` now adds `session_max_seconds: gate_result.session_max_seconds` to the answer dict (additive field; the vendor `SmallWebRTCTransport` only reads `sdp`/`type`/`pc_id` and ignores unknown keys). `voiceSession.ts`'s existing 05-04 fetch interceptor (already scoped to exactly one `connect()` call to detect offer rejections) now also peeks the *successful* response body non-destructively (`response.clone().json()`) and reports the value via a new `onSessionMax` callback option; `useVoiceSession.ts` exposes `sessionMaxSeconds` in its return value; `App.tsx` threads it to `<Live>`, which only renders `<Countdown>` once a real (`>0`) cap has landed.
- **Files modified:** `apps/voice/server.py`, `apps/voice/client/src/transport/voiceSession.ts`, `apps/voice/client/src/transport/useVoiceSession.ts`, `apps/voice/client/src/App.tsx`, `apps/voice/client/src/screens/Live.tsx`
- **Verification:** All 166 existing Python tests still pass unchanged (`test_server.py`'s `/api/offer` tests stub `_negotiate_webrtc` entirely, so the new field is untested by them but doesn't break them); `tsc --noEmit` + `npm run build` clean; the new field is a plain additive JSON key, no existing consumer reads or asserts on the answer's exact shape.
- **Committed in:** `026674b` (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (1 missing-critical connect-flow wiring)
**Impact on plan:** Required for the plan's own stated CLNT-05 truth ("derived from ... the tier session_max") to hold with real data rather than a placeholder constant. No unrelated scope creep -- touched only the files needed to carry one numeric value from the gate result already computed server-side to the one component that needs it.

## Issues Encountered

- Local `vitest`/`tsc`/`npm run build` runs need `node >= 22.12` (vite8/rolldown floor); this shell's default `node v22.1.0` fails -- used `nvm use v23.6.0` for all client-side verification, matching every prior 05-0x plan's documented workaround. The deployed image's `node:22-slim` build stage is above the floor.
- `.planning/config.json` and an unrelated in-progress workstream (`05.1-operator-admin-panel-...` planning directory, `docs/superpowers/specs/2026-07-06-admin-panel-design.md`) were present as pre-existing modified/untracked files at session start (same as noted in 05-04-SUMMARY.md) -- confirmed via `git log`/`git status` these predate and are untouched by this plan's session; left alone, not committed.
- Both tasks are `tdd="true"` per the plan frontmatter, but `.planning/config.json`'s project-level `tdd_mode` is `false` -- tests were authored alongside (not strictly before) the implementation in a single commit per task, consistent with how 05-01 through 05-04 handled the same frontmatter/config combination in this project. No RED-then-GREEN gate-commit sequence was enforced; noted here for traceability, not treated as a defect.

## User Setup Required

None - no external service configuration required by this plan's code changes.

## Deploy Implications (for the orchestrator's post-deploy validation pass)

- No new environment variables, secrets, or infra. `session_max_seconds` is a plain additive JSON field on an existing endpoint's existing response -- no schema migration, no new route.
- Depends on the same still-open Phase-4 IAM gap already tracked in STATE.md (`voice` task role lacks cross-table read on `kmv-auth-electro`): until that's fixed, a real deployed `/api/offer` call fails closed at `read_tier()` before `start_gate` ever returns a `gate_result` for this plan's `session_max_seconds` field to read from. Not introduced or worsened by this plan.

## Next Phase Readiness

- The countdown + HUD are both code-complete and wired to real data paths (the connect-flow answer and the live RTVI `kmv-latency` stream respectively) -- ready for a real conversation to exercise them end-to-end.
- 05-06 (wind-down/reconnect UX) can reuse `Countdown`'s `startedAt`/`sessionMaxSeconds` props for its own "how much time is left" UI if it needs one.
- **Blocking for full verification:** this plan's checkpoint (a live session showing the countdown escalate amber→red in sync with the agent's spoken -30s warning, and the HUD toggling to show live, updating per-turn numbers) is NOT yet exercised -- see `## CHECKPOINT REACHED` in the executor's return message. Per orchestrator guidance this is a LIVE/deployed check (needs a real conversation against the deployed pipeline, not just a local browser render) and was intentionally NOT self-approved here; it folds into the same post-deploy validation pass already tracked for 05-03's and 05-04's deferred checkpoints, and remains additionally blocked on the still-open Phase-4 IAM gap noted above.
- `REQUIREMENTS.md`: CLNT-05 marked complete (delivered fully at the code level by this plan). CLNT-06 was already marked `[x]` by 05-01 (which delivered the server-side emission half) -- this plan is what actually completes the client-side truth; treat both as genuinely done only once the deferred checkpoint above passes live, matching the same pattern STATE.md already documents for CLNT-01/03/04/08 and INFR-03.

---
*Phase: 5-Browser Client & Conference Readiness*
*Completed: 2026-07-06*

## Self-Check: PASSED

All 15 files listed above verified present on disk; both task commit hashes
(`026674b`, `c36ac93`) verified present in `git log --oneline --all`.
