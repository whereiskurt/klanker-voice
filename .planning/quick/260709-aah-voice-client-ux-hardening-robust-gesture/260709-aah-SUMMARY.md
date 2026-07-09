---
phase: 260709-aah
plan: 01
status: complete
subsystem: voice-client
tags: [voice-client, greeting-playback, autoplay, ux, css-layout]
dependency-graph:
  requires: []
  provides:
    - "primeGreeting() / setGreetingErrorHandler() exports in greetingPlayer.ts"
    - "gesture-primed greeting playback (resume-of-primed-element pattern)"
    - ".live-bar right-aligned layout (End chat clear of the Latency toggle)"
  affects:
    - apps/voice/client/src/greeting/greetingPlayer.ts
    - apps/voice/client/src/transport/useVoiceSession.ts
    - apps/voice/client/src/screens/Live.tsx
    - apps/voice/client/src/screens/live.css
tech-stack:
  added: []
  patterns:
    - "Arm-under-activation + resume-on-mount for autoplay-gated media (played-then-paused inside the gesture, later .play() resumes the SAME element)"
    - "Single surfaced-failure helper (console.warn + injectable hook) replacing every silent .catch(finish) swallow"
key-files:
  created: []
  modified:
    - apps/voice/client/src/greeting/greetingPlayer.ts
    - apps/voice/client/src/greeting/greetingPlayer.test.ts
    - apps/voice/client/src/transport/useVoiceSession.ts
    - apps/voice/client/src/transport/useVoiceSession.greeting.test.ts
    - apps/voice/client/src/transport/useVoiceSession.rejection.test.ts
    - apps/voice/client/src/App.gate.test.tsx
    - apps/voice/client/src/screens/Live.tsx
    - apps/voice/client/src/screens/live.css
decisions:
  - "Store the primed Audio element in the primedGreeting singleton even when its arming play() rejects — a rejected arm doesn't preclude a later resume attempt, and the failure is still surfaced via console.warn/hook either way (per plan's explicit action text)."
  - "Fixed two other test files (App.gate.test.tsx, useVoiceSession.rejection.test.ts) that mock greetingPlayer but were outside this plan's declared file_modified list — Rule 3 (blocking issue): start() now imports primeGreeting, and their existing vi.mock factories didn't return it, so the mocked module threw 'No primeGreeting export is defined' at runtime, failing 4 previously-passing tests."
metrics:
  duration: "~35min"
  completed: "2026-07-09"
---

# Phase 260709-aah Plan 01: Voice client UX hardening — robust gesture-primed greeting + reachable End chat Summary

One-liner: gesture-primed real Audio element (play-then-pause under activation, resumed on Live mount) with surfaced play/prime failures via console.warn + an injectable hook, plus a right-aligned `.live-bar` so End chat clears the bottom-left Latency toggle.

## What Changed

### Task 1: Robust gesture-primed greeting playback + surfaced failures (commit `77469d2`)

`apps/voice/client/src/greeting/greetingPlayer.ts`:
- Added a module-level `primedGreeting: { audio: HTMLAudioElement } | null` singleton.
- Added an injectable error hook: `onGreetingError` + exported `setGreetingErrorHandler(fn)`, and a private `reportGreetingFailure(err)` helper that always `console.warn`s and (if set) invokes the hook — this is now the single place every prime/play rejection flows through, replacing the previous silent `.catch(finish)` swallow.
- Added `primeGreeting(): void` — synchronous and gesture-safe. Reads `manifestCache` directly (no await), no-ops if the manifest isn't cached yet or there's no `Audio` constructor (SSR/test). Otherwise constructs the real greeting `Audio` element, stores it in `primedGreeting` immediately (even before the arm resolves), then `play()`s it and in `.then()` immediately `pause()`s + resets `currentTime` — arming it under the current user activation. The whole body is try/catch-guarded through the failure helper so it can never throw out of a gesture handler.
- Reworked `playRandomGreeting()`: if `primedGreeting` is set, it's consumed (cleared) and the SAME element is resumed (`currentTime = 0`, `play()` again) rather than constructing a second `Audio`. A rejected resume routes through `reportGreetingFailure` and still resolves `ended` (never hangs). If not primed, falls back to the original deferred `loadManifest()` + `new Audio()` path, with its own `play()` rejection now also routed through the failure helper instead of the old bare `.catch(finish)`.
- Added an early module-scope preload: `void loadManifest();` fires on import so `primeGreeting()` usually finds `manifestCache` already populated by the time the tap gesture fires.
- `unlockAudioPlayback()` unchanged — still useful as the muted iOS unlock; priming the real element is additive robustness.

`apps/voice/client/src/transport/useVoiceSession.ts`:
- Imports `primeGreeting` alongside `unlockAudioPlayback`.
- `start()` now calls `primeGreeting()` immediately after `unlockAudioPlayback()`, still inside the tap gesture, before `await beginConnect()`.

`apps/voice/client/src/screens/Live.tsx`: no functional change needed — the existing mount-effect `playRandomGreeting()` call now transparently resumes the primed element; the `ended`/`stop` cleanup contract is unchanged.

Tests:
- `greetingPlayer.test.ts` — added Test A (prime-then-resume: one `Audio` construction, same element resumed, `ended` resolves on the element's `ended` event), Test B (a rejected resume `play()` surfaces via `console.warn` + the injectable hook, `ended` still resolves — never hangs), Test C (two cases: fallback-without-priming still returns a handle and surfaces a rejected `play()` via `console.warn` without hanging; returns `null` on an empty manifest with no priming). All existing tests (plays a clip / returns null / `unlockAudioPlayback` doesn't throw) still pass unmodified.
- `useVoiceSession.greeting.test.ts` — added `primeGreeting: vi.fn()` to the `vi.mock` factory and Test D (`start()` calls `primeGreeting()` exactly once). Existing decoupling + endChat-latch tests unchanged and still pass.
- `useVoiceSession.rejection.test.ts` and `App.gate.test.tsx` — **deviation (Rule 3)**: both files independently mock `../greeting/greetingPlayer` / `./greeting/greetingPlayer` for their own `useVoiceSession`-driving tests, and neither was in this plan's declared `files_modified` list. Once `start()` began importing `primeGreeting`, their existing mock factories (which didn't return it) threw `[vitest] No "primeGreeting" export is defined on the mock` at runtime, failing 4 tests that were passing before this change. Added `primeGreeting: vi.fn()` to both mock factories — a blocking issue directly caused by this plan's own `useVoiceSession.ts` edit, not a pre-existing unrelated failure, so in-scope per Rule 3.

### Task 2: Move End chat button off the Latency toggle (commit `bb5eb20`)

`apps/voice/client/src/screens/live.css`: `.live-bar`'s `justify-content: space-between` changed to `flex-end` with `gap: var(--md)` added, so the countdown and End chat cluster together on the right instead of `.live-endchat` (the first child) landing at the same bottom-left corner `.hud-toggle` (hud.css, `z-index: 10`) occupies. Height, background, padding, and z-index left untouched.

`apps/voice/client/src/screens/Live.tsx`: reordered `.live-bar`'s children — `Countdown` (conditional, unchanged) now renders first, `<button className="live-endchat">` renders after it, so End chat lands furthest right in the right-aligned cluster. No behavior change to the countdown's own conditional render or the button's `onClick`.

## Verification

All commands run from `apps/voice/client` under Node 23.6.0 (`nvm use 23`; ambient Node was 22.1.0, which trips a jsdom/html-encoding-sniffer bug on this suite — `npm ci` first, since `node_modules` was absent).

- `npm test -- greetingPlayer useVoiceSession.greeting` → **12/12 passed** (2 test files) — used mid-implementation to drive TDD RED→GREEN for Task 1.
- `npm test` (full suite, after Task 1 + the two deviation-fixed mock files) → **144/144 passed** (28 test files).
- `npm run build` (`tsc --noEmit && vite build`) → clean, no type errors; `dist/` built (`index-*.js` 646.17 kB / gzip 182.89 kB, pre-existing chunk-size warning unrelated to this change).
- `npm test -- Live` (Task 2) → **2/2 passed**.
- `npm run build` (Task 2) → clean.
- Final full-suite re-run after both tasks → **144/144 passed**, build clean.

(A stray `ECONNREFUSED 127.0.0.1:3000` / `::1:3000` `AggregateError` appears in the `npm test` console output on every run, before and unrelated to this plan's changes — an unmocked fetch attempt in some other test file's background code path. It does not fail any test or test file; all 144 tests and 28 files pass in every run.)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking issue] Two greetingPlayer-mocking test files outside this plan's declared scope broke when `start()` began importing `primeGreeting`**
- **Found during:** Task 1, full-suite verification (`npm test` without a file filter)
- **Issue:** `useVoiceSession.rejection.test.ts` and `App.gate.test.tsx` each independently `vi.mock("../greeting/greetingPlayer" | "./greeting/greetingPlayer", () => ({ playRandomGreeting: vi.fn(), unlockAudioPlayback: vi.fn() }))` — neither returned `primeGreeting`. Once `useVoiceSession.ts`'s `start()` imported and called `primeGreeting()`, both mocks threw `[vitest] No "primeGreeting" export is defined on the "..." mock`, failing 4 previously-passing tests (`App.gate.test.tsx` x2, and the corresponding rejection-flow assertions).
- **Fix:** Added `primeGreeting: vi.fn()` to both mock factories, matching the pattern already used in `useVoiceSession.greeting.test.ts`.
- **Files modified:** `apps/voice/client/src/transport/useVoiceSession.rejection.test.ts`, `apps/voice/client/src/App.gate.test.tsx`
- **Commit:** `77469d2` (folded into Task 1's commit since both files exist purely to make Task 1's own source change type/runtime-safe)

**2. [Test-scaffolding fix, no behavior change] `mockAudioCtor` test helper's TypeScript typing**
- **Found during:** Task 1, `npm run build` (tsc)
- **Issue:** `vi.fn().mockImplementation(fn as unknown as typeof Audio)` doesn't type-check under `strict` — `vi.fn()`'s generic signature doesn't accept a construct-signature type. Also the mock element object literal was missing `addEventListener` in its declared `MockAudioEl` interface.
- **Fix:** Built the mock constructor as a plain typed `function` expression (cast once to `typeof Audio`) instead of wrapping it in `vi.fn()` (the test only asserts on the recorded `elements` array, never on constructor-call counts directly), and added `addEventListener` to `MockAudioEl`.
- **Files modified:** `apps/voice/client/src/greeting/greetingPlayer.test.ts`
- **Commit:** `77469d2`

None otherwise — Task 2 executed exactly as written.

## Known Stubs

None.

## Threat Flags

None — this plan touches no new trust boundary; the two STRIDE entries in the plan's own `<threat_model>` (DoS mitigation via the surfaced-failure helper's guaranteed `ended` resolution, and the accepted low-severity info-disclosure of a generic `console.warn` message) are both implemented exactly as specified.

## Self-Check: PASSED

Files:
- FOUND: apps/voice/client/src/greeting/greetingPlayer.ts
- FOUND: apps/voice/client/src/greeting/greetingPlayer.test.ts
- FOUND: apps/voice/client/src/transport/useVoiceSession.ts
- FOUND: apps/voice/client/src/transport/useVoiceSession.greeting.test.ts
- FOUND: apps/voice/client/src/transport/useVoiceSession.rejection.test.ts
- FOUND: apps/voice/client/src/App.gate.test.tsx
- FOUND: apps/voice/client/src/screens/Live.tsx
- FOUND: apps/voice/client/src/screens/live.css

Commits:
- FOUND: 77469d2 (fix(voice-client): gesture-prime greeting playback + surface play() failures)
- FOUND: bb5eb20 (fix(voice-client): move End chat button off the latency toggle)
