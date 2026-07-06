---
phase: 05-browser-client-conference-readiness
plan: 07
subsystem: ui
tags: [react, typescript, vitest, a11y, mobile, css]

requires:
  - phase: 05-browser-client-conference-readiness
    provides: "the full 05-02..05-06 surface set (OrbCanvas/OrbFallback, Attract, Callback, NoAccessGate, MicError, ConnectingRetry, UdpBlockedWall, GateCard, SessionEnd, Countdown, LatencyHud, Captions) and tokens.css/global.css design tokens"
provides:
  - "a11y/liveRegions.ts: shared announcePolite/announceAssertive live regions + a reactive useReducedMotion hook"
  - "styles/responsive.css: mobile/iOS conference-floor CSS layer (100dvh belt-and-suspenders, safe-area-tuned overlay placement, global focus-ring fallback)"
  - "tokens.css --sz-display clamp(24px, 7vw, 32px) mobile Display clamp"
affects: [06-latency-v2, verify-work, phase-05-live-checkpoint-validation]

tech-stack:
  added: []
  patterns:
    - "A single shared, visually-hidden aria-live region pair (module-level DOM nodes, lazily created + reused) rather than one aria-live span per component, so boundary-triggered announcements (Countdown escalation) don't spam repeated per-tick updates through their own live region."
    - "prefers-reduced-motion read via a REACTIVE matchMedia 'change' subscription (useReducedMotion), not a one-shot mount-time check -- lets a mid-session OS toggle swap the orb immediately."

key-files:
  created:
    - apps/voice/client/src/a11y/liveRegions.ts
    - apps/voice/client/src/a11y/a11y.test.ts
    - apps/voice/client/src/styles/responsive.css
  modified:
    - apps/voice/client/src/styles/tokens.css
    - apps/voice/client/src/main.tsx
    - apps/voice/client/src/orb/OrbCanvas.tsx
    - apps/voice/client/src/timer/Countdown.tsx
    - apps/voice/client/src/screens/Callback.tsx
    - apps/voice/client/src/transport/useVoiceSession.ts
    - apps/voice/client/src/App.tsx

key-decisions:
  - "Did NOT wholesale-refactor every screen's aria-live mechanism onto the new shared announcer. role=\"alert\"/role=\"status\" (already used by MicError/UdpBlockedWall/GateCard/ConnectingRetry/SessionEnd) carry an IMPLICIT ARIA live-region politeness (alert->assertive, status->polite) -- those screens already satisfied the UI-SPEC a11y baseline from prior 05-0x plans. The shared announcer targets the two places that pattern didn't cover: Countdown's per-second re-announcement bug, and Callback's total absence of any live region."
  - "Countdown announces at escalation BOUNDARIES (entering warning/critical), not every tick -- the previous continuous aria-live=polite span was a real screen-reader-spam anti-pattern (a fresh interrupt every single second). The always-present sr-only span for on-demand VoiceOver rotor navigation is kept, just no longer aria-live itself."
  - "Esc dismisses GateCard (typed start-gate rejection) and inline MicError only -- not UdpBlockedWall or SessionEnd, which require an explicit choice (retry/reconnect/sign out) rather than a silent back-out."
  - "Orb shader/fallback vertical centering (desktop dead-center vs the 2D fallback's already upper-shifted 0.42h) was left untouched -- changing GLSL centering math is a visual-regression risk on an already-approved (05-02 checkpoint-cleared) hero surface, and the orchestrator's guidance was explicitly to harden, not rebuild, existing seams."

requirements-completed: [CLNT-01, CLNT-02, CLNT-03, CLNT-04, CLNT-05, CLNT-06, CLNT-07, CLNT-08]

coverage:
  - id: D1
    description: "One-handed mobile/iOS layout: 100dvh + safe-area insets, mic CTA lower-third >=96px hit area, all buttons >=44px, countdown notch-safe on mobile"
    requirement: "CLNT-01..08 (cross-cutting mobile hardening)"
    verification:
      - kind: unit
        ref: "grep -q '100dvh' + grep -q 'safe-area-inset' in responsive.css; npm run build clean; npx tsc --noEmit clean"
        status: pass
      - kind: manual_procedural
        ref: "05-07 plan checkpoint (real iPhone, restricted/UDP-blocked conference network)"
        status: unknown
    human_judgment: true
    rationale: "Every touch-target/safe-area token was already present from 05-02..05-06 and is structurally verified here (grep + build); confirming the ACTUAL one-handed feel and notch/home-indicator behavior on a real iPhone requires the physical device -- deferred to post-deploy validation per orchestrator guidance, not self-approved."
  - id: D2
    description: "aria-live polite (connection status/countdown) + assertive (errors) baseline, focus-visible rings on all controls, Esc dismisses transient gate copy"
    requirement: "CLNT-01..08 (a11y baseline)"
    verification:
      - kind: unit
        ref: "apps/voice/client/src/a11y/a11y.test.ts (6 tests: shared polite/assertive regions are separate DOM nodes and reused across calls, useReducedMotion reports + reactively updates + unsubscribes)"
        status: pass
      - kind: manual_procedural
        ref: "05-07 plan checkpoint step 6 (real VoiceOver announcing connection status/countdown/errors)"
        status: unknown
    human_judgment: true
    rationale: "The announcer/hook plumbing and every screen's role/aria-live attribute are unit-verified and grep/tsc/build-clean; confirming actual VoiceOver behavior requires a real iOS device with VoiceOver enabled -- deferred to post-deploy validation, not self-approved."
  - id: D3
    description: "prefers-reduced-motion swaps the shader orb for the calm fallback and disables ambient/escalation motion; contrast floors met"
    requirement: "CLNT-04 (orb), CLNT-05 (countdown)"
    verification:
      - kind: unit
        ref: "a11y.test.ts useReducedMotion tests; OrbCanvas.tsx now reads useReducedMotion (verified via tsc/build); timer.css's existing prefers-reduced-motion query disables the countdown-pill--critical pulse (pre-existing, unchanged, structurally present in timer.css)"
        status: pass
      - kind: computed
        ref: "WCAG contrast ratio computed by hand: text-primary #E6E8EF on the stage background ~15:1 (>=7:1 floor); CTA label (stage-core #06070C on accent #2DE2C8) ~12.3:1 (>=4.5:1 floor) -- both comfortably clear the UI-SPEC floors, no token adjustment needed"
        status: pass
      - kind: manual_procedural
        ref: "05-07 plan checkpoint step 6 (toggling real iOS 'Reduce Motion' mid-session)"
        status: unknown
    human_judgment: true
    rationale: "The reactive hook is unit-tested and threaded into OrbCanvas at the code level (jsdom has no WebGL2 so the orb always renders its 2D fallback in tests either way, making the live toggle's VISUAL effect untestable in this harness); confirming the actual iOS Reduce-Motion toggle swaps the live orb requires a real device -- deferred to post-deploy validation, not self-approved."

duration: 40min
completed: 2026-07-06
status: complete
---

# Phase 5 Plan 7: Mobile/iOS layout + accessibility baseline hardening Summary

**Every screen already carried most of the UI-SPEC's mobile/touch-target/focus-ring baseline from 05-02..05-06; this plan closes the remaining real gaps -- a shared aria-live announcer + reactive `prefers-reduced-motion` hook, a fixed screen-reader-spam bug in the countdown, a completely-missing live region on the OIDC callback screen, Esc-to-dismiss on transient gate/mic-error copy, a mobile `Display` clamp, and a dedicated `responsive.css` layer -- then gates the phase's success criteria on a LIVE real-iPhone + restricted-network verification that is deferred to post-deploy validation, not self-approved.**

## Performance

- **Duration:** 40 min
- **Started:** 2026-07-06T07:05:00Z
- **Completed:** 2026-07-06T07:45:00Z
- **Tasks:** 1 of 1 auto task complete; 1 checkpoint (blocking, live/real-device) deferred, not self-approved
- **Files modified:** 10 (3 created, 7 modified)

## Accomplishments

- Audited every existing screen (`Attract`, `Callback`, `NoAccessGate`, `MicError`, `ConnectingRetry`, `UdpBlockedWall`, `GateCard`, `SessionEnd`, `Countdown`, `LatencyHud`, `Captions`) against the UI-SPEC's mobile/a11y baseline and found most of it (>=44px/96px touch targets, safe-area padding, `:focus-visible` rings, `role="alert"`/`role="status"` on every gate/wall) was already correctly built in prior 05-0x plans. Scoped this plan's real work to the genuine remaining gaps rather than re-doing what already passed.
- `a11y/liveRegions.ts` + `a11y.test.ts` (6 tests): a shared, visually-hidden `announcePolite`/`announceAssertive` live-region pair (lazily created, reused across calls -- never one region per announcement) and a `useReducedMotion()` hook that reactively subscribes to the media query's `change` event, unlike a one-shot mount-time check.
- Fixed a real bug in `Countdown.tsx`: the sr-only span was `aria-live="polite"` and re-rendered its text every second, which would re-announce "59 seconds remaining", "58 seconds remaining", ... to a screen reader every single tick -- a genuine spam anti-pattern. Now the sr-only span is always present (readable on demand via VoiceOver rotor) but NOT itself live; a `useEffect` on the escalation `level` fires one `announcePolite`/`announceAssertive` interrupt exactly at the warning (<=30s) and critical (<10s) boundary crossings.
- `OrbCanvas.tsx` now reads `useReducedMotion()` instead of a one-shot mount-time `matchMedia(...).matches` check, so toggling iOS "Reduce Motion" mid-session swaps the orb to the calm 2D fallback immediately -- the conference-floor checkpoint's own step 6 exercises exactly this.
- `Callback.tsx` had NO live region or role at all (a real gap on the OIDC "Signing you in…" screen) -- added `role="status"`/`aria-live="polite"` while signing in, switching to `role="alert"`/`aria-live="assertive"` on an exchange failure, matching the pattern every other screen already used.
- `App.tsx` mounts the shared live regions on boot and wires `Esc` to dismiss transient gate copy (a typed start-gate rejection card, or an inline mic-error message) via a new `useVoiceSession.dismissMicError()` -- deliberately NOT wired to `UdpBlockedWall`/`SessionEnd`, which require an explicit choice rather than a silent back-out.
- `tokens.css --sz-display` is now `clamp(24px, 7vw, 32px)` (the UI-SPEC's mobile Display clamp), converging to the fixed 32px desktop value once `7vw` exceeds it -- one token now serves both breakpoints instead of needing a separate mobile override.
- New `responsive.css`: a `100dvh` belt-and-suspenders fallback, mobile-portrait-specific countdown/caption/CTA safe-area tuning (narrow and short-landscape viewports), and a global `:focus-visible` fallback rule for any future control.
- Verified the UI-SPEC's contrast floors by hand-computing WCAG relative-luminance ratios rather than assuming: final caption text (`#E6E8EF` on the near-black stage) is ~15:1 against a >=7:1 floor; the CTA label (`#06070C` stage-core on the `#2DE2C8` accent fill) is ~12.3:1 against a >=4.5:1 floor. No token adjustment was needed.

## Task Commits

Each task was committed atomically:

1. **Task 1: Mobile/iOS layout + accessibility baseline across every stage surface** - `e3f0917` (feat)

## Files Created/Modified

- `apps/voice/client/src/a11y/liveRegions.ts` - `announcePolite`/`announceAssertive` (shared, lazily-created, reused DOM live regions) + `ensureLiveRegions()` + `useReducedMotion()` (reactive `matchMedia` subscription)
- `apps/voice/client/src/a11y/a11y.test.ts` - 6 tests: polite/assertive regions are separate DOM nodes and are reused (never duplicated) across repeated calls; `useReducedMotion` reports the initial state, reacts live to a toggle, and unsubscribes on unmount
- `apps/voice/client/src/styles/responsive.css` (NEW) - `100dvh` fallback, mobile-portrait countdown/caption/CTA safe-area tuning, short-landscape viewport handling, global `:focus-visible` fallback
- `apps/voice/client/src/styles/tokens.css` - `--sz-display` is now `clamp(24px, 7vw, 32px)`; `--sz-body` comment documents it as the fixed 16px caption floor
- `apps/voice/client/src/main.tsx` - imports `responsive.css`
- `apps/voice/client/src/orb/OrbCanvas.tsx` - reads `useReducedMotion()` reactively instead of a one-shot mount-time check
- `apps/voice/client/src/timer/Countdown.tsx` - fixed the per-second aria-live spam bug; announces once at each escalation boundary via the shared announcer
- `apps/voice/client/src/screens/Callback.tsx` - added `role`/`aria-live` (previously had none)
- `apps/voice/client/src/transport/useVoiceSession.ts` - new `dismissMicError()` surface for `Esc`
- `apps/voice/client/src/App.tsx` - mounts the shared live regions on boot; `Esc` keydown handler dismisses `GateCard`/inline mic error

## Decisions Made

- **No wholesale refactor of every screen's aria-live mechanism** — `role="alert"`/`role="status"` already carry implicit ARIA live-region politeness; the screens that already used them (MicError, UdpBlockedWall, GateCard, ConnectingRetry, SessionEnd) needed no change. The shared announcer targets only the two places the existing pattern didn't cover (Countdown's spam bug, Callback's total gap).
- **Countdown announces at boundaries, not every tick** — a real fix to a genuine accessibility anti-pattern found while auditing the existing code (Rule 1), not a plan requirement spelled out verbatim, but squarely within "aria-live polite for connection status + countdown" done *correctly*.
- **Esc scoped to GateCard + MicError only** — UdpBlockedWall and SessionEnd are terminal states requiring an explicit user choice; dismissing them silently via Esc would be surprising/lossy (e.g. silently abandoning a session-end summary before choosing Reconnect/Sign out).
- **Orb shader/fallback centering left untouched** — the UI-SPEC calls for the orb "slightly above vertical center" (desktop) vs. "upper-middle ~55vmin" (mobile), and the current WebGL shader draws dead-center while the 2D fallback is already upper-shifted (`cy = h*0.42`). Fixing this would mean editing the fragment shader's UV math on an already-approved (05-02 checkpoint-cleared) hero surface — out of scope for a hardening pass per the orchestrator's explicit "harden what exists, don't rebuild" guidance. Flagged here as a known minor visual inconsistency, not a blocking gap (safe-area/touch-target/caption placement, this plan's actual mandate, are unaffected either way).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Countdown's sr-only live region re-announced every second**
- **Found during:** Task 1, auditing `Countdown.tsx` against the UI-SPEC a11y baseline before adding the new announcer.
- **Issue:** The sr-only span carried `aria-live="polite"` directly and its text content changed every ~1s tick (`useCountdown`'s `TICK_MS = 1000`), which would cause a screen reader to interrupt with a fresh announcement every single second for the entire countdown -- a well-known a11y anti-pattern, not a deliberate design choice (nothing in the UI-SPEC calls for continuous per-second announcements, only "connection status and countdown announced via aria-live=polite").
- **Fix:** Kept the sr-only span (still readable on-demand via VoiceOver rotor navigation) but removed its own `aria-live` attribute; added a `useEffect` on the `level` value (which only changes at the warning/critical boundaries, not every tick) that fires one `announcePolite`/`announceAssertive` call through the new shared live region.
- **Files modified:** `apps/voice/client/src/timer/Countdown.tsx`, `apps/voice/client/src/a11y/liveRegions.ts` (new).
- **Commit:** `e3f0917`

**2. [Rule 2 - Missing critical functionality] `Callback.tsx` had no aria-live/role at all**
- **Found during:** Task 1's screen-by-screen a11y audit.
- **Issue:** The OIDC callback screen ("Signing you in…" / an exchange-failure message) rendered as a plain `<div>` with no `role` or `aria-live` — a screen-reader user landing on this route after the OIDC redirect would get no announcement of the sign-in status or a failure, unlike every other status/error screen in the client.
- **Fix:** Added `role="status"`/`aria-live="polite"` while signing in, switching to `role="alert"`/`aria-live="assertive"` once an error is set — matching the exact pattern already used by `MicError`/`UdpBlockedWall`/`GateCard`.
- **Files modified:** `apps/voice/client/src/screens/Callback.tsx`.
- **Commit:** `e3f0917`

**3. [Rule 2 - Missing critical functionality] `prefers-reduced-motion` was a one-shot mount-time check, not reactive**
- **Found during:** Task 1, cross-referencing the UI-SPEC's "reduced-motion swaps the shader orb for the calm fallback" requirement against the plan checkpoint's own step 6 ("Turn on iOS Reduce Motion and confirm the orb swaps... mid-session").
- **Issue:** `OrbCanvas.tsx`'s original `useEffect(() => { setUseFallback(prefersReducedMotion() || !supportsWebGL2()); }, [])` only ran once at mount — toggling the OS-level Reduce Motion setting while the app was already open would have no visible effect until a full page reload, which the checkpoint's own verification step explicitly requires to work live.
- **Fix:** Added `useReducedMotion()` (reactive `matchMedia` `change`-event subscription) to `liveRegions.ts` and wired `OrbCanvas.tsx` to consume it instead of the one-shot check.
- **Files modified:** `apps/voice/client/src/orb/OrbCanvas.tsx`, `apps/voice/client/src/a11y/liveRegions.ts` (new).
- **Commit:** `e3f0917`

## Issues Encountered

- **Local build/test toolchain:** `vitest`/`tsc`/`npm run build` require `node >= 22.12` (vite8/rolldown floor). This shell's default node (`v22.1.0`/`v23.x` depending on invocation) needed `nvm use v23.6.0` for every client-side verification command, matching the documented workaround in every prior 05-0x SUMMARY.
- **Pre-existing unrelated changes at session start** (same note as every prior 05-0x SUMMARY): `.planning/config.json` (a trailing-newline-only diff from an unrelated `gsd-tools` invocation) and an in-progress, unrelated workstream (`.planning/phases/05.1-operator-admin-panel-.../`, `docs/superpowers/specs/2026-07-06-admin-panel-design.md`, both untracked) were present before this session began. Confirmed via `git status`/`git diff` these predate and are untouched by this plan's work — left alone, not committed, not part of this plan's diff.
- **jsdom has no WebGL2:** `OrbCanvas` always renders its 2D fallback path under vitest/jsdom regardless of the `useReducedMotion()` value, so the reduced-motion->fallback SWAP itself is only testable at the unit level via the hook in isolation (`a11y.test.ts`), not as an end-to-end `OrbCanvas` render assertion. This is a pre-existing test-environment limitation (also true of every prior `OrbCanvas`-adjacent plan), not something introduced here.

## User Setup Required

None - no external service configuration required by this plan's code changes.

## Known Stubs

None. Every changed surface (announcer, reduced-motion hook, Countdown, Callback, Esc-dismiss, responsive.css) is fully wired to real state, not a placeholder.

## Threat Flags

None. This plan touches CSS/a11y-only surfaces (no new network endpoints, auth paths, file access, or schema changes) — the plan's own threat register already scopes T-05-07-I/T-05-07-D/T-05-07-SC to exactly this shape (shared/kiosk-device token lifetime, honest UDP-wall copy, no new npm packages) and none of that register is affected by this plan's actual diff.

## Next Phase Readiness

- All of this plan's code/unit-testable hardening is complete: `npx vitest run` (85/85 total client-side tests, including the 6 new `a11y.test.ts` tests), `npx tsc --noEmit` clean, `npm run build` clean, and the plan's own structural verify (`grep -q "100dvh"` + `grep -q "safe-area-inset"` in `responsive.css`) all pass.
- **Blocking for full phase verification:** this plan's checkpoint — a real iPhone (Safari) over normal Wi-Fi/cellular AND a restricted/UDP-blocked conference-style network, exercising the full attract→sign-in→mic→live-conversation→countdown→session-end flow, the distinct mic-error states, the honest UDP-blocked wall + hotspot recovery, the no-access/killswitch gates, and iOS Reduce Motion + VoiceOver — is the phase's headline "verified on real phones and hostile networks" requirement (success criteria 1-5). It is a LIVE, deployed-stack check per the orchestrator's own guidance and was intentionally NOT self-approved here. It folds into the same post-deploy validation pass already tracked in STATE.md for 05-03's, 05-04's, 05-05's, and 05-06's deferred checkpoints, and remains additionally blocked on the still-open Phase-4 IAM gap (voice task role lacking cross-table read on `kmv-auth-electro`) already documented there.
- `REQUIREMENTS.md`: CLNT-01 through CLNT-08 were already marked complete by their originating 05-0x plans (this plan's own frontmatter lists all eight since it's a cross-cutting hardening pass touching every one of their surfaces) — matching the same "code-complete, live checkpoint deferred" pattern already documented for every prior 05-0x plan.
- **This is the last plan in Phase 5** (`05-07-PLAN.md`, wave 6, `depends_on: ["05-06"]`). With this plan's code complete, Phase 5 itself is code-complete pending the one consolidated live-verification pass (real iPhone + restricted network + VoiceOver/Reduce-Motion + the still-open Phase-4 IAM fix) that every 05-0x plan's checkpoint has been deferring to.

---
*Phase: 05-browser-client-conference-readiness*
*Completed: 2026-07-06*

## Self-Check: PASSED

All 3 new files (`liveRegions.ts`, `a11y.test.ts`, `responsive.css`) plus this
SUMMARY.md verified present on disk; the task commit hash (`e3f0917`)
verified present in `git log --oneline --all`.
