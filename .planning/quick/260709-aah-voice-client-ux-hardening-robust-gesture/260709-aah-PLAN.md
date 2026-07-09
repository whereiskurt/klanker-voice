---
phase: 260709-aah
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
  - apps/voice/client/src/greeting/greetingPlayer.ts
  - apps/voice/client/src/transport/useVoiceSession.ts
  - apps/voice/client/src/greeting/greetingPlayer.test.ts
  - apps/voice/client/src/transport/useVoiceSession.greeting.test.ts
  - apps/voice/client/src/screens/Live.tsx
  - apps/voice/client/src/screens/live.css
autonomous: true
requirements: [QUICK-260709-aah]
must_haves:
  truths:
    - "The pre-rendered KPH greeting plays reliably on Live mount because the real Audio element was armed inside the tap gesture (played-then-paused under user activation)."
    - "A blocked/failed greeting play() is no longer swallowed silently — it surfaces via console.warn (and an injectable error hook)."
    - "If priming didn't happen (manifest not cached in time, no Audio ctor in SSR/test), Live mount still falls back to best-effort deferred playback and never throws or hangs."
    - "The End chat button is reachable — it no longer sits under the bottom-left Latency toggle."
  artifacts:
    - apps/voice/client/src/greeting/greetingPlayer.ts
    - apps/voice/client/src/screens/live.css
  key_links:
    - "useVoiceSession.start() (in-gesture) → primeGreeting() → module-level primed singleton → playRandomGreeting() resumes it on Live mount."
    - ".live-bar layout no longer overlaps .hud-toggle (hud.css bottom-left z-index:10)."
---

<objective>
Harden two voice-client UX defects diagnosed live at voice.klankermaker.ai:

1. The pre-rendered KPH greeting is sometimes silent, invisibly. It is played by
   `playRandomGreeting()` on Live mount — SECONDS after the tap gesture — but the only
   in-gesture audio unlock is a MUTED throwaway element, which does not authorize a later
   UNMUTED play on a different element (WebKit/Safari especially; intermittent on Chrome).
   The failed `play()` is swallowed with zero signal → total silence, no error. Fix:
   arm the REAL greeting Audio element inside the tap gesture (play→pause under activation),
   resume it on Live mount, preload the manifest early, and stop swallowing failures.

2. The "End chat" button sits at the bottom-left corner UNDER the Latency toggle
   (`.hud-toggle`, z-index:10) and is unclickable. Fix: group End chat with the countdown
   on the RIGHT, leaving the bottom-left corner solely to the latency toggle.

Purpose: A reliably-audible greeting is the "whoa in the first ten seconds" of the demo,
and a reachable End chat is basic session control.
Output: Robust gesture-primed greeting playback with surfaced failures + a repositioned
End chat button. Two atomic commits, no deploy.
</objective>

<execution_context>
@$HOME/.claude/gsd-core/workflows/execute-plan.md
</execution_context>

<context>
@.planning/STATE.md
@apps/voice/client/src/greeting/greetingPlayer.ts
@apps/voice/client/src/greeting/greetingPlayer.test.ts
@apps/voice/client/src/transport/useVoiceSession.ts
@apps/voice/client/src/transport/useVoiceSession.greeting.test.ts
@apps/voice/client/src/screens/Live.tsx
@apps/voice/client/src/screens/live.css
@apps/voice/client/src/hud/hud.css
@apps/voice/client/src/hud/LatencyHud.tsx
</context>

<environment_notes>
- Run all client verification FROM `apps/voice/client`.
- The ambient Node is 22.1.0, which trips a known jsdom bug for these tests; client tests
  require Node >= 22.12 — run `nvm use 23` (or any Node >= 22.12) before `npm test`.
- Test constructor-mock pattern (existing, MUST follow): `Audio` is mocked with a
  `function` EXPRESSION (not an arrow — arrows have no `[[Construct]]` slot, so
  `new Audio()` throws "X is not a constructor"). The module-level singletons
  (`manifestCache`, and the new primed-greeting singleton) require `vi.resetModules()`
  between cases plus a dynamic `await import("./greetingPlayer")` per case — exactly as
  the existing `greetingPlayer.test.ts` already does.
- Do NOT deploy. The prod warmer-audio redeploy is a separate step handled outside this plan.
</environment_notes>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Robust gesture-primed greeting playback + surfaced failures</name>
  <files>apps/voice/client/src/greeting/greetingPlayer.ts, apps/voice/client/src/transport/useVoiceSession.ts, apps/voice/client/src/greeting/greetingPlayer.test.ts, apps/voice/client/src/transport/useVoiceSession.greeting.test.ts, apps/voice/client/src/screens/Live.tsx</files>
  <behavior>
    greetingPlayer.ts:
    - Test A (prime-then-resume): with the manifest already cached, calling `primeGreeting()`
      constructs the real greeting Audio element and calls `.play()` on it once (arming under
      activation); a subsequent `playRandomGreeting()` RESUMES that same primed element
      (does NOT construct a second Audio) and returns a non-null handle whose `ended`
      resolves on the element's `ended`/`error` event.
    - Test B (surfaced failure): when the resumed/played element's `play()` rejects,
      `console.warn` is called (failure is no longer swallowed) AND `ended` still resolves
      (never hangs). If an error handler was registered via the injectable hook, it is invoked.
    - Test C (fallback, no prime): with no prior `primeGreeting()`, `playRandomGreeting()`
      falls back to the current deferred load-and-play best-effort — returns a handle when the
      manifest has clips, `null` when it has none, and never throws.
    - Existing tests (plays a clip / returns null on empty manifest / unlockAudioPlayback does
      not throw) MUST still pass.
    useVoiceSession.greeting.test.ts:
    - Test D: `start()` calls `primeGreeting()` (within the gesture) after a granted mic.
    - Existing tests MUST still pass (CONNECTED lands immediately with no greeting hold;
      `start()` does not itself call `playRandomGreeting()`; endChat latch tests unchanged).
  </behavior>
  <action>
In apps/voice/client/src/greeting/greetingPlayer.ts:
    - Add a module-level primed-greeting singleton (e.g. `primedGreeting: { audio: HTMLAudioElement } | null = null`) alongside the existing `manifestCache`.
    - Add an injectable error hook: a module-level `onGreetingError: ((err: unknown) =&gt; void) | null` and an exported `setGreetingErrorHandler(fn)` setter. Add a private helper that logs the failure via `console.warn` AND calls `onGreetingError` if set — this replaces every silent swallow.
    - Export `primeGreeting(): void` — synchronous, gesture-safe. Read `manifestCache` DIRECTLY (do NOT await — the gesture task must build the element synchronously). If the manifest is not yet cached, or there is no `Audio` constructor (SSR/test), no-op (the Live-mount fallback covers it). Otherwise pick a random clip, `const audio = new Audio(`/greetings/${clip.file}`)`, then arm it under activation: call `audio.play()`, and in its `.then()` immediately `audio.pause()` and set `audio.currentTime = 0`; route a `.catch(...)` through the surfaced-failure helper. Store the element in the `primedGreeting` singleton. Guard the whole body in try/catch routed to the failure helper — never throw out of a gesture handler.
    - Rework `playRandomGreeting()`: if `primedGreeting` is set, CONSUME it (clear the singleton), attach `ended`/`error` listeners that resolve the handle, set `currentTime = 0`, call `.play()` and route a rejection through the surfaced-failure helper AND resolve `ended` (never hang). Return a handle whose `stop()` pauses + clears `src` + resolves, preserving the existing `GreetingHandle` contract. If `primedGreeting` is NOT set, fall back to the current deferred `loadManifest()` + `new Audio()` best-effort path — but replace the silent `void audio.play().catch(finish)` swallow so a rejection ALSO routes through the surfaced-failure helper before `finish()`.
    - Kick off an EARLY manifest preload: at module scope, `void loadManifest()` on import so the gesture-time `primeGreeting()` usually finds `manifestCache` populated. `loadManifest()` already try/catches a missing/failed `fetch` and returns null, so this is safe under jsdom/SSR.
    - Keep `unlockAudioPlayback()` as-is (the muted iOS unlock is still useful); priming the real element is the added robustness, not a replacement.
In apps/voice/client/src/transport/useVoiceSession.ts:
    - Import `primeGreeting` alongside the existing `unlockAudioPlayback` import.
    - In `start()`, immediately AFTER the existing `unlockAudioPlayback()` call (still inside the gesture, before `await beginConnect()`), call `primeGreeting()` so the real greeting element is armed under user activation.
In apps/voice/client/src/screens/Live.tsx:
    - No functional change required: the mount effect already calls `playRandomGreeting()`, which now resumes the primed element. Verify the existing `ended`/`stop` cleanup contract still holds (cancelled → `h?.stop()`), and leave the orb-before-greeting UX untouched. Only touch this file if a comment update is warranted to note the resume-of-primed behavior.
Tests:
    - Extend greetingPlayer.test.ts with Tests A/B/C. Reuse the existing `function`-expression Audio constructor mock and `vi.resetModules()` + dynamic-import isolation. The mock element must expose `play`, `pause`, a writable `currentTime`, and `addEventListener` capturing `ended`/`error` handlers so a test can fire them. For Test A/B, stub `fetch` to return the manifest BEFORE the dynamic `import(...)` so the module-scope preload populates `manifestCache`, then `await` a tick so the preload resolves, then call `primeGreeting()` and `playRandomGreeting()`. For Test B, make the mock `play` return a rejected promise and spy on `console.warn`.
    - Update useVoiceSession.greeting.test.ts: add `primeGreeting: vi.fn()` to the existing `vi.mock("../greeting/greetingPlayer", ...)` factory (start() now imports it — an undefined import would throw). Add Test D asserting `primeGreeting` is called during `start()`. Do not disturb the existing greeting-decoupling / endChat-latch tests.
    - Do NOT place any greeting literal that a negative grep depends on into comment bodies.
  </action>
  <verify>
    <automated>cd apps/voice/client && nvm use 23 >/dev/null 2>&1; npm test -- greetingPlayer useVoiceSession.greeting && npm run build</automated>
  </verify>
  <done>
    `primeGreeting()` arms the real greeting Audio element inside the gesture and
    `playRandomGreeting()` resumes it on Live mount; play/prime rejections surface via
    console.warn (+ optional hook) instead of being swallowed; fallback path still works;
    all greetingPlayer + useVoiceSession.greeting tests pass and `npm run build`
    (tsc + vite) succeeds.
  </done>
</task>

<task type="auto">
  <name>Task 2: Move End chat button off the Latency toggle</name>
  <files>apps/voice/client/src/screens/live.css, apps/voice/client/src/screens/Live.tsx</files>
  <action>
The bottom-left corner is owned by `.hud-toggle` (hud.css: position:absolute; left; bottom;
z-index:10). `.live-bar` (live.css, z-index:6) uses `justify-content: space-between`, so its
first child `.live-endchat` lands at the same bottom-left corner, UNDER the toggle.
    - In apps/voice/client/src/screens/live.css: change `.live-bar`'s `justify-content: space-between` to `justify-content: flex-end` and add a `gap` (e.g. `gap: var(--md)`) so the countdown and End chat cluster together on the RIGHT, clear of the bottom-left toggle. Do not change the bar's height, background, padding, or z-index.
    - In apps/voice/client/src/screens/Live.tsx: reorder the `.live-bar` children so End chat is the RIGHTMOST element — render the `Countdown` first, then the `<button className="live-endchat">` after it (End chat ends up furthest right within the right-aligned cluster). Keep the countdown's own conditional render (`sessionMaxSeconds != null && > 0`) and behavior exactly as-is; only the sibling order and container alignment change.
    - Verify visually-by-rule: only the countdown + End chat now live in the bottom-right; the bottom-left is solely the latency toggle. No new overlap on the right (the toggle is bottom-left only).
  </action>
  <verify>
    <automated>cd apps/voice/client && nvm use 23 >/dev/null 2>&1; npm test -- Live && npm run build</automated>
  </verify>
  <done>
    `.live-bar` right-aligns its children with a gap; End chat renders to the right of the
    countdown and no longer overlaps `.hud-toggle`; Live tests pass and `npm run build`
    succeeds.
  </done>
</task>

</tasks>

<threat_model>
## Trust Boundaries

| Boundary | Description |
|----------|-------------|
| (none new) | Client-only UX change: TypeScript playback-arming logic + CSS layout. No new inputs cross a trust boundary, no new network calls (the greeting manifest/clip fetches already exist), no new packages. |

## STRIDE Threat Register

| Threat ID | Category | Component | Severity | Disposition | Mitigation Plan |
|-----------|----------|-----------|----------|-------------|-----------------|
| T-260709-aah-01 | Denial of Service | greetingPlayer play/prime path | low | mitigate | All prime/play rejections are caught and routed through the surfaced-failure helper; `ended` always resolves so a blocked autoplay can never hang the Live-mount handoff. |
| T-260709-aah-02 | Information Disclosure | console.warn on greeting failure | low | accept | Warnings log only that greeting playback failed (no tokens/PII); acceptable for a public demo client. |
</threat_model>

<verification>
- From `apps/voice/client` (Node >= 22.12 via `nvm use 23`): `npm test` passes and
  `npm run build` (tsc + vite) succeeds.
- Manual (optional, not required for this plan): reload voice.klankermaker.ai, tap to talk,
  confirm KPH greeting is audible on orb-appear and the End chat button is clickable
  (not under the Latency toggle). Deploy is out of scope.
</verification>

<success_criteria>
- Greeting Audio element is armed inside the tap gesture and resumed on Live mount.
- Greeting play/prime failures surface via console.warn (+ optional injectable hook), never
  swallowed; playback never hangs.
- Fallback deferred playback still works when priming didn't happen; nothing throws in SSR/test.
- End chat button is reachable, right-aligned with the countdown, clear of the Latency toggle.
- Two separate atomic commits (one per task). Client tests + build pass. No deploy.
</success_criteria>

<output>
Two atomic commits (one per task). Do NOT commit docs artifacts — the orchestrator handles
the docs commit.
</output>
