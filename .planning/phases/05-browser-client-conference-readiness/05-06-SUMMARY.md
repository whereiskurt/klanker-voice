---
phase: 05-browser-client-conference-readiness
plan: 06
subsystem: ui
tags: [react, typescript, vitest, pipecat, quota, retry-policy]

requires:
  - phase: 05-browser-client-conference-readiness
    provides: "05-04's voiceSession.ts fetch interceptor + connectionState.ts reducer (idle/requesting-mic/connecting/connected/rejected/failed); 05-03's NoAccessGate D-13 pattern; 04-04's typed quota.GateResult rejects"
provides:
  - "retryPolicy.ts: bounded N=3 exponential-backoff retry controller (500ms/1s/2s), exhausted terminal verdict"
  - "gateMapping.ts: typed quota.GateResult error_type -> verbatim UI-SPEC gate copy + action"
  - "SessionEnd.tsx + useVoiceSession.ts sessionSummary: clean/provider-error session-end summary + quota-rechecked reconnect"
affects: [06-latency-v2, verify-work, phase-05-live-checkpoint-validation]

tech-stack:
  added: []
  patterns:
    - "Long-lived stateful controller constructed once via the useRef lazy-init idiom (if (ref.current == null) ref.current = ...) rather than a bare useRef(construct()) call, avoiding per-render construction+discard churn."
    - "wasConnectedRef/connectedAtRef pair distinguishes a pre-connect transport failure (-> retry/wall) from a post-connect drop (-> session-end summary) without touching the shared connectionState.ts reducer."
    - "Gate/wall/session-end copy split at a sentence boundary so concatenating heading + body reproduces the UI-SPEC's single-line verbatim contract string exactly, while giving every terminal overlay the same heading+body card layout."

key-files:
  created:
    - apps/voice/client/src/transport/retryPolicy.ts
    - apps/voice/client/src/transport/retryPolicy.test.ts
    - apps/voice/client/src/screens/ConnectingRetry.tsx
    - apps/voice/client/src/screens/UdpBlockedWall.tsx
    - apps/voice/client/src/gates/gateMapping.ts
    - apps/voice/client/src/gates/gateMapping.test.ts
    - apps/voice/client/src/gates/GateCard.tsx
    - apps/voice/client/src/screens/SessionEnd.tsx
  modified:
    - apps/voice/client/src/transport/useVoiceSession.ts
    - apps/voice/client/src/App.tsx

key-decisions:
  - "at-capacity and any unknown error_type share one Claude's-discretion 'generic provider-error' gate copy (the UI-SPEC contract has no assigned string for this one transient reject case; distinct from SessionEnd's own verbatim 'Generic provider-error end' row, a different screen)."
  - "A post-connect transport drop routes to sessionSummary (reason: provider-error), NOT Task 1's retry/wall flow -- a session that already connected shouldn't show 'this network blocks audio' when audio already worked."
  - "Reconnect (SessionEnd) and the gate card's 'retry' action both funnel through the same start() -- one code path for 'issue a fresh /api/offer', not two."

requirements-completed: [CLNT-02, CLNT-07]

coverage:
  - id: D1
    description: "Bounded N=3 auto-retry + backoff on a pre-connect transport failure, ending at the honest UDP-blocked wall with a manual retry -- no infinite spinner"
    requirement: "CLNT-02"
    verification:
      - kind: unit
        ref: "apps/voice/client/src/transport/retryPolicy.test.ts (3 tests: bounded schedule+backoff+exhaustion, success resets counter, manual retryNow())"
        status: pass
      - kind: manual_procedural
        ref: "05-06 plan checkpoint step 1 (real hostile/UDP-blocked network)"
        status: unknown
    human_judgment: true
    rationale: "Verifying the wall actually appears on a real UDP-blocked network (vs. a simulated transport error) requires the deployed service + a real hostile network -- deferred to post-deploy validation per orchestrator guidance, not self-approved."
  - id: D2
    description: "Each typed quota.GateResult rejection (daily-exhausted, over-concurrent, killswitch, no-access, at-capacity) maps to specific verbatim in-client gate copy, never a raw error"
    requirement: "CLNT-07"
    verification:
      - kind: unit
        ref: "apps/voice/client/src/gates/gateMapping.test.ts (11 tests: verbatim copy + retryable flag per error_type, unknown-type fallback, gateAction routing)"
        status: pass
      - kind: manual_procedural
        ref: "05-06 plan checkpoint step 2 (kv killswitch, two concurrent sessions, exhausted daily minutes against the deployed service)"
        status: unknown
    human_judgment: true
    rationale: "Confirming the real server actually emits these error_types under real killswitch/concurrency/daily-limit conditions requires the deployed service + live quota state -- deferred to post-deploy validation, not self-approved."
  - id: D3
    description: "Clean session end shows a 'Nice talking with you.' summary with {m:ss} spoken + Reconnect/Sign out; Reconnect re-runs the quota start-gate via a fresh /api/offer before reconnecting, routing a reject to the matching gate card"
    requirement: "CLNT-07"
    verification:
      - kind: unit
        ref: "tsc --noEmit clean; npm run build clean; grep -q 'Nice talking with you' + grep -q 'spoken' in SessionEnd.tsx"
        status: pass
      - kind: manual_procedural
        ref: "05-06 plan checkpoint step 3 (a real session ending via the server's timer/goodbye, then tapping Reconnect)"
        status: unknown
    human_judgment: true
    rationale: "Exercising a real server-driven session end (timer/goodbye) and a real quota-recheck-on-reconnect requires the deployed service + a live conversation -- deferred to post-deploy validation, not self-approved."

duration: 55min
completed: 2026-07-06
status: complete
---

# Phase 5 Plan 6: Retry/UDP-wall, typed gate copy, clean end + reconnect Summary

**Bounded 3-attempt exponential-backoff retry controller ending at an honest UDP-blocked wall; a pure `error_type` -> verbatim UI-SPEC gate-copy map covering all 5 typed `quota.GateResult` rejects; and a session-end summary + quota-rechecked Reconnect that distinguishes a pre-connect transport failure from a post-connect drop without touching the shared connection-state reducer.**

## Performance

- **Duration:** 55 min
- **Started:** 2026-07-06T06:03:00Z
- **Completed:** 2026-07-06T06:58:00Z
- **Tasks:** 3 of 3 auto tasks complete; 1 checkpoint (blocking, live/deployed) deferred, not self-approved
- **Files modified:** 10 (8 created, 2 modified)

## Accomplishments

- `retryPolicy.ts`'s `createRetryController` is a pure, timer-injectable state machine (no React) implementing the bounded N=3/500ms-1s-2s backoff schedule, wired into `useVoiceSession.ts` so a genuine pre-connect transport/ICE failure auto-retries with visible "Reconnecting… (attempt n of N)" and, once exhausted, renders the verbatim `UdpBlockedWall.tsx` with a manual "Try again" -- never an infinite spinner.
- `gateMapping.ts` maps every one of the 5 typed `quota.GateResult` `error_type`s (`daily-limit`, `concurrency-limit`, `site-paused`, `no-access`, `at-capacity`) to its specific verbatim UI-SPEC gate copy + a `retryable` flag, plus a `gateAction()` helper routing `no-access` to sign-out, the one retryable reject to retry, and everything else to dismiss; `GateCard.tsx` renders it as the same translucent secondary-surface treatment as `NoAccessGate`/`UdpBlockedWall`.
- `SessionEnd.tsx` + a reworked `useVoiceSession.ts` distinguish a session that reached "connected" and then cleanly ended (goodbye/idle/timer -- "Nice talking with you." + `{m:ss}` spoken) from one that dropped due to an unexpected provider error (the verbatim generic-provider-error-end copy) -- via a new `wasConnectedRef`/`connectedAtRef` pair, without needing to touch the shared `connectionState.ts` reducer at all. Reconnect and the gate card's "retry" both funnel through the exact same `start()` -- a fresh `/api/offer` that re-executes the server's quota start_gate, never a silent transport reopen.

## Task Commits

Each task was committed atomically:

1. **Task 1: Bounded auto-retry + backoff, then the honest UDP-blocked wall (CLNT-02, D-11)** - `a0a3ca9` (test — TDD test+impl together, per this project's established `tdd_mode: false` single-commit-per-task pattern, see Issues Encountered)
2. **Task 2: Map typed quota.GateResult rejections to in-client gate copy (D-14)** - `6797078` (feat)
3. **Task 3: Clean session end + one-click Reconnect that re-runs the quota start-gate (CLNT-07, D-14)** - `e3ab794` (feat)

## Files Created/Modified

- `apps/voice/client/src/transport/retryPolicy.ts` - `createRetryController`: pure bounded exponential-backoff schedule (500ms/1s/2s), exhausted terminal verdict, success resets the counter
- `apps/voice/client/src/transport/retryPolicy.test.ts` - 3 tests: bounded schedule+backoff+exhaustion, success resets the counter for a later unrelated failure, manual `retryNow()`
- `apps/voice/client/src/screens/ConnectingRetry.tsx` (+ `connectingRetry.css`) - "Reconnecting… (attempt n of N)" status overlay, `aria-live="polite"`
- `apps/voice/client/src/screens/UdpBlockedWall.tsx` (+ `udpBlockedWall.css`) - honest UDP-blocked wall, verbatim UI-SPEC copy, manual "Try again", `aria-live="assertive"`
- `apps/voice/client/src/gates/gateMapping.ts` - pure `error_type` -> `{heading, body, retryable}` map + `gateAction()` (verbatim UI-SPEC copy for daily-limit/concurrency-limit/site-paused/no-access; a documented Claude's-discretion generic copy for at-capacity/unknown)
- `apps/voice/client/src/gates/gateMapping.test.ts` - 11 tests: verbatim-copy + retryable assertions per typed `error_type`, unknown-type fallback, `gateAction` routing
- `apps/voice/client/src/gates/GateCard.tsx` (+ `gateCard.css`) - translucent secondary-surface gate card rendering the mapped copy + action
- `apps/voice/client/src/screens/SessionEnd.tsx` (+ `sessionEnd.css`) - "Nice talking with you." summary card with `{m:ss}` spoken (reuses `formatMSS` from `useCountdown.ts`), or the generic-provider-error-end copy on an unexpected drop; "Reconnect"/"Sign out"
- `apps/voice/client/src/transport/useVoiceSession.ts` - wires the retry controller into the connect flow; `wasConnectedRef`/`connectedAtRef` distinguish pre-connect failure from post-connect drop; new `retryStatus`, `sessionSummary`, `retryNow()`, `dismissGate()` surface
- `apps/voice/client/src/App.tsx` - routes `sessionSummary` -> `SessionEnd`, `"rejected"` outcome -> `GateCard`, `retryStatus.kind === "exhausted"` -> `UdpBlockedWall`, `"retrying"` -> `ConnectingRetry` overlay on the attract stage

## Decisions Made

- **at-capacity/unknown share one "generic provider-error" gate copy** (Claude's discretion): the UI-SPEC Copywriting Contract assigns verbatim strings to daily-limit/concurrency-limit/site-paused/no-access but has no copy for the one retryable `at-capacity` case. Reusing SessionEnd's "Generic provider-error end" string verbatim would be factually wrong here (it says "the session ended cleanly" — but `at-capacity` fires *before* any session starts). 05-CONTEXT.md's Claude's-Discretion section already delegates exact retry/gate copy for the D-11/D-12 cases the contract left open; this extends the same discretion to the one gate case it left unassigned, in the same tone, and documents it inline in `gateMapping.ts` so it's never mistaken for a missed verbatim string.
- **A post-connect transport drop is a session-end, not a retry/wall** — `useVoiceSession.ts` tracks `wasConnectedRef` so a `TRANSPORT_ERROR` arriving *after* `CONNECTED` produces a `sessionSummary` (reason `"provider-error"`) instead of being routed into Task 1's pre-connect retry/wall flow, which would misleadingly claim the network "blocks the audio channel" for a session where audio had already worked.
- **No changes to the shared `connectionState.ts` reducer** — the plan's own `files_modified` list omits it. The pre-connect-vs-post-connect distinction and the session-summary/retry-status tracking both live entirely in `useVoiceSession.ts`'s own local React state (refs + `useState`), leaving the reducer's existing idle/requesting-mic/connecting/connected/rejected/failed contract (05-04) untouched.
- **Reconnect and gate "retry" both call the same `start()`** — rather than a separate lighter-weight "just reconnect the transport" path, both funnel through the full mic-request + fresh-`/api/offer` flow. Requesting the mic again is a no-op prompt once already granted, and reusing one code path (rather than two subtly different ones) is the safer, simpler choice for "always re-check quota before reconnecting" (D-14).

## Deviations from Plan

None beyond the documented Claude's-discretion copy choice above (which the plan's own CONTEXT.md discretion section already anticipates a class of). No Rule 1/2/3 bug-fixes or missing-critical additions were needed — the existing 05-04 `connectionState.ts`/`voiceSession.ts` seams and 04-04's typed `quota.py` error types were sufficient as-is.

## Issues Encountered

- **TDD single-commit pattern:** `.planning/config.json`'s `tdd_mode` is `false` (same as every prior 05-0x plan). All three tasks are `tdd="true"` per the plan frontmatter, but tests were authored alongside (not strictly before) the implementation in one commit per task — consistent with how 05-01 through 05-05 handled the identical frontmatter/config combination in this project. No separate RED-then-GREEN gate commits were created.
- **Local build/test toolchain:** `vitest`/`tsc`/`npm run build` require `node >= 22.12` (vite8/rolldown floor). This shell's default node is below that floor; `nvm use v23.6.0` was used for every client-side verification command, matching the documented workaround in every prior 05-0x SUMMARY.
- **Pre-existing unrelated changes at session start** (same as noted in 05-04/05-05 SUMMARYs): `.planning/config.json` (modified) and an in-progress, unrelated workstream (`.planning/phases/05.1-operator-admin-panel-.../`, `docs/superpowers/specs/2026-07-06-admin-panel-design.md`, both untracked) were present before this session began. Confirmed via `git log`/`git status` these predate and are untouched by this plan's work — left alone, not committed, not part of this plan's diff.

## User Setup Required

None - no external service configuration required by this plan's code changes.

## Next Phase Readiness

- All three code-level deliverables (bounded retry/wall, typed gate copy, session-end + quota-rechecked reconnect) are complete, unit-tested (14 new tests, 79/79 total client-side), `tsc --noEmit` clean, `npm run build` clean.
- **Blocking for full verification:** this plan's checkpoint (a real hostile/UDP-blocked network showing "Reconnecting…" then the honest wall; the kill-switch/concurrency/daily-exhausted gate copies against real quota state; a real session ending via the server's timer/goodbye, then Reconnect re-checking quota) is a LIVE/deployed check per the orchestrator's own guidance (needs the deployed service, a hostile network, and real quota state — none of which are available to a local build) and was intentionally NOT self-approved here. It folds into the same post-deploy validation pass already tracked in STATE.md for 05-03's, 05-04's, and 05-05's deferred checkpoints, and remains additionally blocked on the still-open Phase-4 IAM gap (voice task role lacking cross-table read on `kmv-auth-electro`) already documented there.
- `REQUIREMENTS.md`: CLNT-02 and CLNT-07 marked complete (delivered fully at the code level by this plan) — matching the same "code-complete, live checkpoint deferred" pattern STATE.md already documents for CLNT-01/03/04/05/06/08 and INFR-03.
- `05-07-PLAN.md` (wave 6, `depends_on: ["05-06"]`) is the next and final plan in this phase — mobile/one-handed/a11y hardening across the whole stage, reusing this plan's gate/wall/session-end surfaces (touch-target/safe-area/aria-live requirements apply to all of them).

---
*Phase: 05-browser-client-conference-readiness*
*Completed: 2026-07-06*

## Self-Check: PASSED

All 10 code/test files plus this SUMMARY.md verified present on disk; all
three task commit hashes (`a0a3ca9`, `6797078`, `e3ab794`) verified present
in `git log --oneline --all`.
