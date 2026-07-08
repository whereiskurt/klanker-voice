# Voice Flow Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the voice client's flat tap-to-talk cascade with a linear, ceremonial flow — forced auth-on-arrival → "Let's start talking" → theatrical boot ceremony → orb + a growing scrollable transcript → end/restart.

**Architecture:** The client becomes a linear state machine whose current screen is chosen by one pure function (`resolveScreen`). The last-exchange-only fading caption band is replaced by an append-only transcript reducer + scrollable component (which also eliminates the "transcript permanently dies" bug class). The KPH greeting is decoupled from the connect-hold so it plays as the orb appears. No pipeline, auth-service, infra, or provider changes.

**Tech Stack:** Vite + React 19 + TypeScript, Vitest + @testing-library/react, `@pipecat-ai/client-js` RTVI events. All work is under `apps/voice/client/`.

## Global Constraints

- All paths below are relative to `apps/voice/client/` unless stated otherwise. Run all commands from `apps/voice/client/`.
- Naming: "klanker-voice" / "KPH" everywhere; never "voiceai".
- Transcript/caption text is ALWAYS rendered as plain React text, never `dangerouslySetInnerHTML` (preserves the T-05-04-T XSS disposition).
- Tokens only: no hardcoded hex/px in CSS — consume `src/styles/tokens.css` custom properties (e.g. `var(--accent)`, `var(--orb-listening)`, `var(--md)`).
- Test runner: `npm test` (= `vitest run`). Single file: `npx vitest run src/path/file.test.ts`.
- Type-check + build gate: `npm run build` (= `tsc --noEmit && vite build`) must stay green.
- The no-access tier id is the string `"no-access"` (matches `App.tsx`'s `NO_ACCESS_TIER_ID`, mirrors auth.py `NO_ACCESS_TIER_ID`).
- Commit after every task (each task ends at a green test/build state).

## Component inventory (locks the decomposition)

**Create:**
- `src/transcript/transcriptReducer.ts` — append-only transcript model (pure).
- `src/transcript/Transcript.tsx` (+ `transcript.css`) — scrollable growing log.
- `src/flow/resolveScreen.ts` — pure screen router.
- `src/flow/landDecision.ts` — pure unauth-arrival decision.
- `src/screens/ReadyToStart.tsx` (+ `readyToStart.css`) — authenticated "Let's start talking".
- `src/screens/Ceremony.tsx` (+ `ceremony.css`) + `src/screens/ceremonyScript.ts` — boot ceremony.
- `src/screens/LandBounce.tsx` (+ `landBounce.css`) — holding/nudge surface during forced auth.

**Modify:**
- `src/auth/returningStore.ts` — add one-shot interactive-redirect guard.
- `src/greeting/greetingPlayer.ts` — add `unlockAudioPlayback()`.
- `src/transport/useVoiceSession.ts` — remove greeting-play + CONNECTED greeting-hold; unlock audio on gesture; add `endChat()`.
- `src/screens/Live.tsx` (+ `live.css`) — compact orb + Transcript + greeting-on-mount + End chat.
- `src/screens/SessionEnd.tsx` — relabel primary action to "Start another".
- `src/App.tsx` — linear machine via `resolveScreen` + forced-auth land effect + `startConversation`.

**Retire (delete file + its tests):**
- `src/screens/Attract.tsx`, `src/screens/attract.css`.
- `src/captions/Captions.tsx`, `src/captions/captionReducer.ts`, `src/captions/Captions.test.tsx`, `src/captions/captionReducer.test.ts`, `src/captions/captions.css`.
- `src/screens/ConnectingRetry.tsx`, `src/screens/connectingRetry.css` (retry "retrying" now folds into the Ceremony hold).

**Reused unchanged:** orb (`OrbCanvas`, `useOrbBinding`), `useAuth`, `Callback`, `NoAccessGate`, `GateCard`, `UdpBlockedWall`, `MicError`, `Countdown`, `LatencyHud`, `retryPolicy`, `connectionState`, `voiceSession`, `greetingPlayer` (playback fn).

---

### Task 1: Append-only transcript reducer

**Files:**
- Create: `src/transcript/transcriptReducer.ts`
- Test: `src/transcript/transcriptReducer.test.ts`

**Interfaces:**
- Consumes: nothing.
- Produces: `TranscriptTurn`, `TranscriptState` (= `TranscriptTurn[]`), `TranscriptEvent`, `INITIAL_TRANSCRIPT_STATE`, `transcriptReducer(state, event): TranscriptState`.

- [ ] **Step 1: Write the failing test**

```ts
// src/transcript/transcriptReducer.test.ts
import { describe, expect, it } from "vitest";
import { INITIAL_TRANSCRIPT_STATE, transcriptReducer, type TranscriptState } from "./transcriptReducer";

const run = (events: Parameters<typeof transcriptReducer>[1][]): TranscriptState =>
  events.reduce(transcriptReducer, INITIAL_TRANSCRIPT_STATE);

describe("transcriptReducer", () => {
  it("firms an interim user turn in place, then appends the next user turn", () => {
    const state = run([
      { type: "USER_TRANSCRIPT", text: "tell me", final: false },
      { type: "USER_TRANSCRIPT", text: "tell me about km", final: true },
      { type: "USER_TRANSCRIPT", text: "and defcon", final: false },
    ]);
    expect(state.map((t) => [t.speaker, t.text, t.final])).toEqual([
      ["user", "tell me about km", true],
      ["user", "and defcon", false],
    ]);
  });

  it("concatenates consecutive agent chunks into one turn", () => {
    const state = run([
      { type: "AGENT_TRANSCRIPT", text: "Kurt built it." },
      { type: "AGENT_TRANSCRIPT", text: "km drives it." },
    ]);
    expect(state).toHaveLength(1);
    expect(state[0]).toMatchObject({ speaker: "agent", text: "Kurt built it. km drives it.", final: true });
  });

  it("alternating speakers append distinct turns and never clear history", () => {
    const state = run([
      { type: "AGENT_TRANSCRIPT", text: "hey" },
      { type: "USER_TRANSCRIPT", text: "hi", final: true },
      { type: "AGENT_TRANSCRIPT", text: "what's up" },
    ]);
    expect(state.map((t) => t.speaker)).toEqual(["agent", "user", "agent"]);
  });

  it("assigns stable unique ids", () => {
    const state = run([
      { type: "USER_TRANSCRIPT", text: "a", final: true },
      { type: "AGENT_TRANSCRIPT", text: "b" },
    ]);
    expect(new Set(state.map((t) => t.id)).size).toBe(2);
  });

  it("RESET clears to empty", () => {
    const state = run([{ type: "USER_TRANSCRIPT", text: "a", final: true }, { type: "RESET" }]);
    expect(state).toEqual([]);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/transcript/transcriptReducer.test.ts`
Expected: FAIL — cannot find module `./transcriptReducer`.

- [ ] **Step 3: Write minimal implementation**

```ts
// src/transcript/transcriptReducer.ts
/**
 * Append-only transcript model (voice-flow-redesign). Unlike the retired
 * last-exchange-only caption band, this NEVER clears mid-conversation — every
 * frame either updates the tail turn or appends a new one. That absence of a
 * clearing path is what removes the "transcript permanently dies" bug class.
 */
export interface TranscriptTurn {
  id: string;
  speaker: "user" | "agent";
  text: string;
  /** false = interim (provisional, gray); true = firmed/final. */
  final: boolean;
}

export type TranscriptState = TranscriptTurn[];

export const INITIAL_TRANSCRIPT_STATE: TranscriptState = [];

export type TranscriptEvent =
  | { type: "USER_TRANSCRIPT"; text: string; final: boolean }
  | { type: "AGENT_TRANSCRIPT"; text: string }
  | { type: "RESET" };

let nextId = 0;
const makeId = (): string => `t${nextId++}`;

export function transcriptReducer(state: TranscriptState, event: TranscriptEvent): TranscriptState {
  switch (event.type) {
    case "USER_TRANSCRIPT": {
      const tail = state[state.length - 1];
      // Firm an in-progress interim user turn in place; otherwise append.
      if (tail && tail.speaker === "user" && tail.final === false) {
        return [...state.slice(0, -1), { ...tail, text: event.text, final: event.final }];
      }
      return [...state, { id: makeId(), speaker: "user", text: event.text, final: event.final }];
    }
    case "AGENT_TRANSCRIPT": {
      const tail = state[state.length - 1];
      // Sentence-aggregated agent chunks concatenate onto the current agent turn.
      if (tail && tail.speaker === "agent") {
        return [...state.slice(0, -1), { ...tail, text: `${tail.text} ${event.text}`.trim() }];
      }
      return [...state, { id: makeId(), speaker: "agent", text: event.text, final: true }];
    }
    case "RESET":
      return INITIAL_TRANSCRIPT_STATE;
    default:
      return state;
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/transcript/transcriptReducer.test.ts`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add src/transcript/transcriptReducer.ts src/transcript/transcriptReducer.test.ts
git commit -m "feat(voice-client): append-only transcript reducer"
```

---

### Task 2: Transcript component (scrollable growing log)

**Files:**
- Create: `src/transcript/Transcript.tsx`, `src/transcript/transcript.css`
- Test: `src/transcript/Transcript.test.tsx`

**Interfaces:**
- Consumes: `TranscriptState`, `TranscriptTurn` from Task 1.
- Produces: default export `Transcript({ turns }: { turns: TranscriptState })`.

- [ ] **Step 1: Write the failing test**

```tsx
// src/transcript/Transcript.test.tsx
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import Transcript from "./Transcript";
import type { TranscriptState } from "./transcriptReducer";

describe("Transcript", () => {
  it("renders each turn with a speaker chip and its text, oldest first", () => {
    const turns: TranscriptState = [
      { id: "t0", speaker: "agent", text: "hey, what's up", final: true },
      { id: "t1", speaker: "user", text: "tell me about km", final: true },
    ];
    render(<Transcript turns={turns} />);
    const rows = screen.getAllByTestId("turn");
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveTextContent("KPH");
    expect(rows[0]).toHaveTextContent("hey, what's up");
    expect(rows[1]).toHaveTextContent("You");
  });

  it("marks interim (non-final) user turns", () => {
    render(<Transcript turns={[{ id: "t0", speaker: "user", text: "and def", final: false }]} />);
    expect(screen.getByTestId("turn")).toHaveClass("turn--interim");
  });

  it("renders text as plain text (no HTML injection)", () => {
    render(<Transcript turns={[{ id: "t0", speaker: "user", text: "<img src=x onerror=1>", final: true }]} />);
    expect(screen.getByText("<img src=x onerror=1>")).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/transcript/Transcript.test.tsx`
Expected: FAIL — cannot find module `./Transcript`.

- [ ] **Step 3: Write minimal implementation**

```tsx
// src/transcript/Transcript.tsx
import { useEffect, useRef, useState } from "react";
import type { TranscriptState } from "./transcriptReducer";
import "./transcript.css";

export interface TranscriptProps {
  turns: TranscriptState;
}

/**
 * The growing, scrollable conversation log (voice-flow-redesign). Auto-scrolls
 * to the newest turn ONLY when the user is already pinned near the bottom; if
 * they have scrolled up to read history, it leaves them there and offers a
 * "jump to latest" affordance. Text is plain React text (never HTML).
 */
export default function Transcript({ turns }: TranscriptProps) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const [pinned, setPinned] = useState(true);

  useEffect(() => {
    if (pinned && scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [turns, pinned]);

  const onScroll = () => {
    const el = scrollRef.current;
    if (!el) return;
    setPinned(el.scrollHeight - el.scrollTop - el.clientHeight < 40);
  };

  const jumpToLatest = () => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
    setPinned(true);
  };

  return (
    <div className="transcript-wrap">
      <div ref={scrollRef} className="transcript" onScroll={onScroll} aria-live="polite" aria-label="Conversation transcript">
        {turns.map((turn) => (
          <div key={turn.id} data-testid="turn" className={`turn turn--${turn.speaker}${turn.final ? "" : " turn--interim"}`}>
            <span className={`turn-chip turn-chip--${turn.speaker}`}>{turn.speaker === "agent" ? "KPH" : "You"}</span>
            <span className="turn-text">{turn.text}</span>
          </div>
        ))}
      </div>
      {!pinned ? (
        <button type="button" className="transcript-jump" onClick={jumpToLatest}>
          ↓ jump to latest
        </button>
      ) : null}
    </div>
  );
}
```

```css
/* src/transcript/transcript.css */
.transcript-wrap { position: absolute; inset: 176px 0 96px; z-index: 2; }
.transcript {
  height: 100%; overflow-y: auto; padding: var(--sm) var(--lg) var(--lg);
  -webkit-mask-image: linear-gradient(to bottom, transparent 0, #000 22px, #000 100%);
          mask-image: linear-gradient(to bottom, transparent 0, #000 22px, #000 100%);
}
.turn { margin: var(--md) 0; }
.turn-chip {
  display: inline-block; margin-bottom: var(--xs); padding: 2px var(--sm);
  border-radius: 999px; font-size: var(--sz-label); font-weight: var(--w-semibold); letter-spacing: 0.06em;
}
.turn-chip--user { color: var(--text-secondary); background: color-mix(in srgb, var(--text-primary) 6%, transparent); }
.turn-chip--agent { color: var(--stage-core); background: var(--accent); }
.turn-text { display: block; font-size: var(--sz-body); line-height: var(--lh-prose); color: var(--text-primary); }
.turn--interim .turn-text { color: var(--text-interim); font-style: italic; }
.transcript-jump {
  position: absolute; right: var(--md); bottom: var(--md); min-height: var(--hit-min);
  padding: 0 var(--md); border: 1px solid var(--border-subtle); border-radius: 999px;
  background: var(--scrim); color: var(--text-primary); font-size: var(--sz-label); cursor: pointer;
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/transcript/Transcript.test.tsx`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add src/transcript/Transcript.tsx src/transcript/transcript.css src/transcript/Transcript.test.tsx
git commit -m "feat(voice-client): scrollable growing transcript component"
```

---

### Task 3: Interactive-redirect guard in returningStore

**Files:**
- Modify: `src/auth/returningStore.ts`
- Test: `src/auth/returningStore.test.ts` (create if absent)

**Interfaces:**
- Consumes: nothing.
- Produces: `markInteractiveTried(): void`, `wasInteractiveTried(): boolean` (sessionStorage-backed, key `kmv_interactive_tried`).

- [ ] **Step 1: Write the failing test**

```ts
// src/auth/returningStore.test.ts (append; create file if it does not exist)
import { beforeEach, describe, expect, it } from "vitest";
import { markInteractiveTried, wasInteractiveTried } from "./returningStore";

describe("interactive-redirect guard", () => {
  beforeEach(() => sessionStorage.clear());

  it("defaults to false and latches true once marked", () => {
    expect(wasInteractiveTried()).toBe(false);
    markInteractiveTried();
    expect(wasInteractiveTried()).toBe(true);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/auth/returningStore.test.ts`
Expected: FAIL — `markInteractiveTried` is not exported.

- [ ] **Step 3: Write minimal implementation**

Append to `src/auth/returningStore.ts` (after `wasSilentTried`):

```ts
/**
 * One-shot guard for the forced-auth bounce (voice-flow-redesign §3.1). Set
 * when the app auto-fires a FULL interactive redirect. If the user returns
 * still unauthenticated (they bailed at auth), the app shows a manual "Sign
 * in" nudge instead of auto-redirecting again — no redirect storm.
 */
const INTERACTIVE_TRIED_KEY = "kmv_interactive_tried";

export function markInteractiveTried(): void {
  try { sessionStorage.setItem(INTERACTIVE_TRIED_KEY, "1"); } catch { /* no-op */ }
}
export function wasInteractiveTried(): boolean {
  try { return sessionStorage.getItem(INTERACTIVE_TRIED_KEY) === "1"; } catch { return false; }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/auth/returningStore.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/auth/returningStore.ts src/auth/returningStore.test.ts
git commit -m "feat(voice-client): one-shot interactive-redirect guard"
```

---

### Task 4: Pure land decision (holding vs redirect vs nudge)

**Files:**
- Create: `src/flow/landDecision.ts`
- Test: `src/flow/landDecision.test.ts`

**Interfaces:**
- Consumes: nothing (takes plain booleans).
- Produces: `type LandAction = "holding" | "redirect" | "nudge"`, `decideLandAction(i): LandAction`.

- [ ] **Step 1: Write the failing test**

```ts
// src/flow/landDecision.test.ts
import { describe, expect, it } from "vitest";
import { decideLandAction } from "./landDecision";

describe("decideLandAction (unauthenticated arrivals only)", () => {
  it("holds while an invisible silent SSO is still possible (returning, not yet tried)", () => {
    expect(decideLandAction({ isReturning: true, silentTried: false, interactiveTried: false })).toBe("holding");
  });

  it("force-redirects a first-timer with no silent path and no prior interactive try", () => {
    expect(decideLandAction({ isReturning: false, silentTried: false, interactiveTried: false })).toBe("redirect");
  });

  it("force-redirects a returning user whose silent SSO already failed this load", () => {
    expect(decideLandAction({ isReturning: false, silentTried: true, interactiveTried: false })).toBe("redirect");
  });

  it("shows a manual nudge once an interactive redirect was already attempted (bail-out guard)", () => {
    expect(decideLandAction({ isReturning: false, silentTried: true, interactiveTried: true })).toBe("nudge");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/flow/landDecision.test.ts`
Expected: FAIL — cannot find module `./landDecision`.

- [ ] **Step 3: Write minimal implementation**

```ts
// src/flow/landDecision.ts
/**
 * Pure decision for what an UNAUTHENTICATED arrival should do on the voice
 * SPA (voice-flow-redesign §3.1). Callers only invoke this when the user is
 * not authenticated. The invisible silent SSO attempt (useAuth.attemptSilentSso)
 * is fired by App FIRST; this decides what happens once silent SSO is no
 * longer a live option.
 */
export interface LandInputs {
  /** localStorage breadcrumb: this device has interactively signed in before. */
  isReturning: boolean;
  /** sessionStorage: a silent prompt=none attempt already ran this load. */
  silentTried: boolean;
  /** sessionStorage: a full interactive redirect was already auto-fired this session. */
  interactiveTried: boolean;
}

export type LandAction = "holding" | "redirect" | "nudge";

export function decideLandAction({ isReturning, silentTried, interactiveTried }: LandInputs): LandAction {
  // A returning user who has not yet attempted silent SSO this load: the
  // invisible prompt=none navigation is about to happen — just hold.
  if (isReturning && !silentTried) return "holding";
  // Already bounced through a full interactive redirect and came back
  // unauthenticated (bailed at auth): stop auto-bouncing, offer a manual button.
  if (interactiveTried) return "nudge";
  // Otherwise force the interactive redirect.
  return "redirect";
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/flow/landDecision.test.ts`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add src/flow/landDecision.ts src/flow/landDecision.test.ts
git commit -m "feat(voice-client): pure forced-auth land decision"
```

---

### Task 5: Pure screen router (`resolveScreen`)

**Files:**
- Create: `src/flow/resolveScreen.ts`
- Test: `src/flow/resolveScreen.test.ts`

**Interfaces:**
- Consumes: `ConnectionState` from `../transport/connectionState`.
- Produces: `type Screen`, `interface ScreenInputs`, `resolveScreen(i: ScreenInputs): Screen`.

- [ ] **Step 1: Write the failing test**

```ts
// src/flow/resolveScreen.test.ts
import { describe, expect, it } from "vitest";
import { resolveScreen, type ScreenInputs } from "./resolveScreen";

const base: ScreenInputs = {
  onCallbackRoute: false, hasSessionSummary: false, isAuthenticated: true, isNoAccessTier: false,
  outcomeState: "idle", retryExhausted: false, hasMicError: false, ceremonyDone: false, hasClient: false,
};

describe("resolveScreen precedence", () => {
  it("callback route wins over everything", () => {
    expect(resolveScreen({ ...base, onCallbackRoute: true, hasSessionSummary: true })).toBe("callback");
  });
  it("a session summary shows the ended screen", () => {
    expect(resolveScreen({ ...base, hasSessionSummary: true })).toBe("ended");
  });
  it("unauthenticated lands (forced-auth surface)", () => {
    expect(resolveScreen({ ...base, isAuthenticated: false })).toBe("land");
  });
  it("no-access tier gates before any conversation", () => {
    expect(resolveScreen({ ...base, isNoAccessTier: true })).toBe("no-access");
  });
  it("mic error interrupts", () => {
    expect(resolveScreen({ ...base, hasMicError: true })).toBe("mic-error");
  });
  it("a quota rejection shows the gate", () => {
    expect(resolveScreen({ ...base, outcomeState: "rejected" })).toBe("gate");
  });
  it("exhausted retry shows the udp wall", () => {
    expect(resolveScreen({ ...base, retryExhausted: true })).toBe("udp-wall");
  });
  it("connecting shows the ceremony", () => {
    expect(resolveScreen({ ...base, outcomeState: "connecting" })).toBe("ceremony");
  });
  it("connected but ceremony still running holds on the ceremony", () => {
    expect(resolveScreen({ ...base, outcomeState: "connected", ceremonyDone: false, hasClient: true })).toBe("ceremony");
  });
  it("connected AND ceremony done AND client present goes live", () => {
    expect(resolveScreen({ ...base, outcomeState: "connected", ceremonyDone: true, hasClient: true })).toBe("live");
  });
  it("a pre-connect transport failure (still retrying) holds on the ceremony", () => {
    expect(resolveScreen({ ...base, outcomeState: "failed" })).toBe("ceremony");
  });
  it("authenticated and idle is ready-to-start", () => {
    expect(resolveScreen(base)).toBe("ready");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/flow/resolveScreen.test.ts`
Expected: FAIL — cannot find module `./resolveScreen`.

- [ ] **Step 3: Write minimal implementation**

```ts
// src/flow/resolveScreen.ts
import type { ConnectionState } from "../transport/connectionState";

/**
 * Every foreground/interrupt surface the voice SPA can show
 * (voice-flow-redesign §2). `land` is the forced-auth surface; its sub-mode
 * (holding vs nudge) is chosen separately by `decideLandAction`.
 */
export type Screen =
  | "callback" | "ended" | "land" | "no-access"
  | "mic-error" | "gate" | "udp-wall" | "ceremony" | "live" | "ready";

export interface ScreenInputs {
  onCallbackRoute: boolean;
  hasSessionSummary: boolean;
  isAuthenticated: boolean;
  isNoAccessTier: boolean;
  outcomeState: ConnectionState;
  retryExhausted: boolean;
  hasMicError: boolean;
  ceremonyDone: boolean;
  hasClient: boolean;
}

/** The single source of truth for "what screen are we on". Pure — App wires
 * live signals in and renders the returned enum. Order IS the precedence. */
export function resolveScreen(i: ScreenInputs): Screen {
  if (i.onCallbackRoute) return "callback";
  if (i.hasSessionSummary) return "ended";
  if (!i.isAuthenticated) return "land";
  if (i.isNoAccessTier) return "no-access";
  if (i.hasMicError) return "mic-error";
  if (i.outcomeState === "rejected") return "gate";
  if (i.retryExhausted) return "udp-wall";
  if (i.outcomeState === "connected" && i.ceremonyDone && i.hasClient) return "live";
  // requesting-mic / connecting / failed(retrying) / connected-but-ceremony-not-done
  if (
    i.outcomeState === "requesting-mic" ||
    i.outcomeState === "connecting" ||
    i.outcomeState === "failed" ||
    i.outcomeState === "connected"
  ) {
    return "ceremony";
  }
  return "ready";
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/flow/resolveScreen.test.ts`
Expected: PASS (12 tests).

- [ ] **Step 5: Commit**

```bash
git add src/flow/resolveScreen.ts src/flow/resolveScreen.test.ts
git commit -m "feat(voice-client): pure screen router for the linear flow"
```

---

### Task 6: Ceremony script + Ceremony screen

**Files:**
- Create: `src/screens/ceremonyScript.ts`, `src/screens/Ceremony.tsx`, `src/screens/ceremony.css`
- Test: `src/screens/Ceremony.test.tsx`

**Interfaces:**
- Consumes: `OrbCanvas` from `../orb/OrbCanvas`.
- Produces: `CEREMONY_SCRIPT: { line: string; sub: string }[]`, `LINE_MS: number`; default export `Ceremony({ onScriptDone }: { onScriptDone: () => void })`.

- [ ] **Step 1: Write the failing test**

```tsx
// src/screens/Ceremony.test.tsx
import { render, screen, act } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import Ceremony from "./Ceremony";
import { CEREMONY_SCRIPT, LINE_MS } from "./ceremonyScript";

describe("Ceremony", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("advances through the scripted lines and fires onScriptDone once at the end", () => {
    const onScriptDone = vi.fn();
    render(<Ceremony onScriptDone={onScriptDone} />);
    expect(screen.getByTestId("ceremony-line")).toHaveTextContent(CEREMONY_SCRIPT[0].line);

    act(() => vi.advanceTimersByTime(LINE_MS));
    expect(screen.getByTestId("ceremony-line")).toHaveTextContent(CEREMONY_SCRIPT[1].line);

    act(() => vi.advanceTimersByTime(LINE_MS * CEREMONY_SCRIPT.length));
    expect(onScriptDone).toHaveBeenCalledTimes(1);
    // Holds on the final line after finishing (does not blank out).
    expect(screen.getByTestId("ceremony-line")).toHaveTextContent(CEREMONY_SCRIPT[CEREMONY_SCRIPT.length - 1].line);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/screens/Ceremony.test.tsx`
Expected: FAIL — cannot find module `./Ceremony`.

- [ ] **Step 3: Write minimal implementation**

```ts
// src/screens/ceremonyScript.ts
/** Theatrical boot-ceremony copy (voice-flow-redesign §3.3). Config constant
 * so the personality can be tuned without touching logic. Final line is the
 * "hold" line shown until the real connection lands. */
export const CEREMONY_SCRIPT: { line: string; sub: string }[] = [
  { line: "initializing…", sub: "waking the mic" },
  { line: "paging KPH…", sub: "sending the signal" },
  { line: "let me see if he's out there…", sub: "negotiating the connection" },
  { line: "do do do…", sub: "almost there" },
  { line: "he's warming up…", sub: "waiting for KPH to pick up" },
];

/** Per-line dwell. Total floor ≈ LINE_MS * CEREMONY_SCRIPT.length. */
export const LINE_MS = 850;
```

```tsx
// src/screens/Ceremony.tsx
import { useEffect, useState } from "react";
import OrbCanvas from "../orb/OrbCanvas";
import { CEREMONY_SCRIPT, LINE_MS } from "./ceremonyScript";
import "./ceremony.css";

export interface CeremonyProps {
  /** Fires once when the scripted timeline finishes. App gates the handoff to
   * Live on max(this, connection reached "connected"). */
  onScriptDone: () => void;
}

/**
 * The boot-up ceremony (voice-flow-redesign §3.3): a theatrical fixed-timeline
 * script over the orb in a "thinking" (booting) look. The script advances on a
 * timer and, on the last line, both fires `onScriptDone` and HOLDS on that
 * final line — App keeps this mounted until the real connection lands, so a
 * slow connect shows "he's warming up…" rather than a dead orb.
 */
export default function Ceremony({ onScriptDone }: CeremonyProps) {
  const [index, setIndex] = useState(0);

  useEffect(() => {
    if (index >= CEREMONY_SCRIPT.length - 1) {
      const done = window.setTimeout(onScriptDone, LINE_MS);
      return () => window.clearTimeout(done);
    }
    const next = window.setTimeout(() => setIndex((i) => i + 1), LINE_MS);
    return () => window.clearTimeout(next);
  }, [index, onScriptDone]);

  const step = CEREMONY_SCRIPT[index];

  return (
    <div className="ceremony">
      <OrbCanvas state="thinking" amplitude={0} />
      <div className="ceremony-copy">
        <p data-testid="ceremony-line" className="ceremony-line">{step.line}</p>
        <p className="ceremony-sub">{step.sub}</p>
      </div>
    </div>
  );
}
```

```css
/* src/screens/ceremony.css */
.ceremony { position: relative; width: 100%; height: 100%; }
.ceremony-copy {
  position: absolute; left: 50%; bottom: 22%; transform: translateX(-50%);
  width: 90%; text-align: center; z-index: 12;
}
.ceremony-line { margin: 0; font-size: var(--sz-heading); font-weight: var(--w-semibold); color: var(--text-primary); }
.ceremony-sub { margin-top: var(--sm); font-size: var(--sz-label); color: var(--text-interim); }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/screens/Ceremony.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/screens/ceremonyScript.ts src/screens/Ceremony.tsx src/screens/ceremony.css src/screens/Ceremony.test.tsx
git commit -m "feat(voice-client): theatrical boot-up ceremony screen"
```

---

### Task 7: ReadyToStart screen

**Files:**
- Create: `src/screens/ReadyToStart.tsx`, `src/screens/readyToStart.css`
- Test: `src/screens/ReadyToStart.test.tsx`

**Interfaces:**
- Consumes: `OrbCanvas`.
- Produces: default export `ReadyToStart({ onStart }: { onStart: () => void })`.

- [ ] **Step 1: Write the failing test**

```tsx
// src/screens/ReadyToStart.test.tsx
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import ReadyToStart from "./ReadyToStart";

describe("ReadyToStart", () => {
  it("fires onStart when the CTA is tapped", () => {
    const onStart = vi.fn();
    render(<ReadyToStart onStart={onStart} />);
    fireEvent.click(screen.getByRole("button", { name: /let's start talking/i }));
    expect(onStart).toHaveBeenCalledTimes(1);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/screens/ReadyToStart.test.tsx`
Expected: FAIL — cannot find module `./ReadyToStart`.

- [ ] **Step 3: Write minimal implementation**

```tsx
// src/screens/ReadyToStart.tsx
import OrbCanvas from "../orb/OrbCanvas";
import "./readyToStart.css";

export interface ReadyToStartProps {
  /** The single gesture that unlocks mic + audio and begins the connect +
   * ceremony (voice-flow-redesign §3.2). Authenticated-only screen. */
  onStart: () => void;
}

/** The authenticated landing (voice-flow-redesign §3.2): idle ambient orb and
 * one "Let's start talking" CTA. Replaces the retired unauthenticated Attract. */
export default function ReadyToStart({ onStart }: ReadyToStartProps) {
  return (
    <div className="ready">
      <OrbCanvas state="idle" amplitude={0} />
      <div className="ready-wordmark">voice<b>.klankermaker.ai</b></div>
      <div className="ready-cta-wrap">
        <button type="button" className="ready-cta" onClick={onStart}>Let's start talking</button>
        <p className="ready-cta-sub">This taps the mic awake and pages KPH. Ready when you are.</p>
      </div>
    </div>
  );
}
```

```css
/* src/screens/readyToStart.css — mirrors the retired attract.css layout tokens */
.ready { position: relative; width: 100%; height: 100%; }
.ready-wordmark {
  position: absolute; top: max(var(--lg), env(safe-area-inset-top)); left: var(--xl); z-index: 10;
  font-size: var(--sz-label); letter-spacing: 0.14em; text-transform: uppercase;
  color: var(--text-secondary); font-weight: var(--w-semibold);
}
.ready-wordmark b { color: var(--text-primary); }
.ready-cta-wrap {
  position: absolute; left: 50%; transform: translateX(-50%);
  bottom: max(var(--3xl), calc(env(safe-area-inset-bottom) + var(--3xl))); z-index: 12; text-align: center;
}
.ready-cta {
  display: inline-flex; align-items: center; justify-content: center; min-height: var(--hit-cta);
  padding: 0 var(--xl); font-size: var(--sz-body); font-weight: var(--w-semibold);
  color: var(--stage-core); background: var(--accent); border: none; border-radius: 999px; cursor: pointer;
  box-shadow: 0 0 0 6px color-mix(in srgb, var(--accent) 18%, transparent), 0 8px 40px color-mix(in srgb, var(--accent) 30%, transparent);
  transition: var(--motion-fast);
}
.ready-cta:hover { transform: translateY(-1px); }
.ready-cta:focus-visible { outline: 2px solid var(--text-primary); outline-offset: 4px; }
.ready-cta-sub { margin-top: var(--md); font-size: var(--sz-label); color: var(--text-secondary); }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/screens/ReadyToStart.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/screens/ReadyToStart.tsx src/screens/readyToStart.css src/screens/ReadyToStart.test.tsx
git commit -m "feat(voice-client): authenticated ReadyToStart screen"
```

---

### Task 8: LandBounce screen (forced-auth surface)

**Files:**
- Create: `src/screens/LandBounce.tsx`, `src/screens/landBounce.css`
- Test: `src/screens/LandBounce.test.tsx`

**Interfaces:**
- Consumes: `OrbCanvas`, `LandAction` from `../flow/landDecision`.
- Produces: default export `LandBounce({ mode, onSignIn }: { mode: LandAction; onSignIn: () => void })`.

- [ ] **Step 1: Write the failing test**

```tsx
// src/screens/LandBounce.test.tsx
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import LandBounce from "./LandBounce";

describe("LandBounce", () => {
  it("shows a quiet holding status while redirecting/holding", () => {
    render(<LandBounce mode="holding" onSignIn={() => {}} />);
    expect(screen.getByText(/checking your session/i)).toBeInTheDocument();
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("shows a manual sign-in button in nudge mode", () => {
    const onSignIn = vi.fn();
    render(<LandBounce mode="nudge" onSignIn={onSignIn} />);
    fireEvent.click(screen.getByRole("button", { name: /sign in/i }));
    expect(onSignIn).toHaveBeenCalledTimes(1);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/screens/LandBounce.test.tsx`
Expected: FAIL — cannot find module `./LandBounce`.

- [ ] **Step 3: Write minimal implementation**

```tsx
// src/screens/LandBounce.tsx
import OrbCanvas from "../orb/OrbCanvas";
import type { LandAction } from "../flow/landDecision";
import "./landBounce.css";

export interface LandBounceProps {
  /** "holding"/"redirect" both render the quiet holding state (a redirect is
   * a navigation about to happen); "nudge" renders the manual button. */
  mode: LandAction;
  onSignIn: () => void;
}

/** The forced-auth surface (voice-flow-redesign §3.1). An unauthenticated
 * arrival never sees a real landing page — only this brief holding state while
 * they are bounced to auth, or a single manual "Sign in" nudge if an automatic
 * redirect already happened and they came back unauthenticated. */
export default function LandBounce({ mode, onSignIn }: LandBounceProps) {
  return (
    <div className="land">
      <OrbCanvas state="idle" amplitude={0} />
      <div className="land-wordmark">voice<b>.klankermaker.ai</b></div>
      {mode === "nudge" ? (
        <div className="land-center">
          <p className="land-title">Sign-in needed</p>
          <button type="button" className="land-cta" onClick={onSignIn}>Sign in</button>
        </div>
      ) : (
        <p className="land-status" role="status">checking your session…</p>
      )}
    </div>
  );
}
```

```css
/* src/screens/landBounce.css */
.land { position: relative; width: 100%; height: 100%; }
.land-wordmark {
  position: absolute; top: max(var(--lg), env(safe-area-inset-top)); left: var(--xl); z-index: 10;
  font-size: var(--sz-label); letter-spacing: 0.14em; text-transform: uppercase;
  color: var(--text-secondary); font-weight: var(--w-semibold);
}
.land-wordmark b { color: var(--text-primary); }
.land-status {
  position: absolute; left: 50%; bottom: 22%; transform: translateX(-50%);
  font-size: var(--sz-label); color: var(--text-interim); z-index: 12;
}
.land-center {
  position: absolute; left: 50%; bottom: 20%; transform: translateX(-50%);
  text-align: center; z-index: 12;
}
.land-title { margin: 0 0 var(--md); font-size: var(--sz-heading); color: var(--text-primary); }
.land-cta {
  min-height: var(--hit-min); padding: 0 var(--xl); font-size: var(--sz-body); font-weight: var(--w-semibold);
  color: var(--stage-core); background: var(--accent); border: none; border-radius: 999px; cursor: pointer;
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/screens/LandBounce.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/screens/LandBounce.tsx src/screens/landBounce.css src/screens/LandBounce.test.tsx
git commit -m "feat(voice-client): forced-auth LandBounce surface"
```

---

### Task 9: Greeting audio unlock + decouple greeting from the connect-hold

**Files:**
- Modify: `src/greeting/greetingPlayer.ts`
- Modify: `src/transport/useVoiceSession.ts`
- Modify: `src/transport/useVoiceSession.greeting.test.ts`
- Test: `src/greeting/greetingPlayer.test.ts` (add a case)

**Interfaces:**
- Consumes: existing `playRandomGreeting` (unchanged signature).
- Produces: `unlockAudioPlayback(): void` in greetingPlayer; `useVoiceSession` no longer plays the greeting nor holds CONNECTED behind it; adds `endChat(): Promise<void>` to `UseVoiceSessionResult`.

**Why:** The redesign wants the orb to appear and THEN KPH to greet (approved). Today the greeting plays on the gesture and CONNECTED is held until it ends, so the orb only appears after the greeting. This task moves greeting playback to Live's mount (Task 10) and unlocks iOS audio on the gesture so the deferred play is permitted.

> ⚠️ **iOS autoplay UAT flag:** deferring greeting playback past the gesture relies on the standard "unlock on gesture, play later" pattern. `playRandomGreeting` already resolves gracefully if `play()` is blocked, so worst case degrades to a silent orb — but this MUST be verified on a real iPhone during UAT.

- [ ] **Step 1: Write the failing tests**

Add to `src/greeting/greetingPlayer.test.ts`:

```ts
import { unlockAudioPlayback } from "./greetingPlayer";

it("unlockAudioPlayback does not throw when called within a gesture", () => {
  expect(() => unlockAudioPlayback()).not.toThrow();
});
```

Rewrite the greeting-hold expectations in `src/transport/useVoiceSession.greeting.test.ts`. First, that file mocks the greeting module with `vi.mock("../greeting/greetingPlayer", () => ({ playRandomGreeting: vi.fn() }))` — extend the mock so the newly-imported `unlockAudioPlayback` is defined, else `start()` throws on an undefined import:

```ts
vi.mock("../greeting/greetingPlayer", () => ({ playRandomGreeting: vi.fn(), unlockAudioPlayback: vi.fn() }));
```

Then replace the assertion that CONNECTED is held until the greeting ends with its inverse — CONNECTED now dispatches immediately, and `start()` no longer calls `playRandomGreeting`:

```ts
// The greeting is NO LONGER played inside start() (moved to Live mount), and
// CONNECTED is dispatched as soon as the transport connects — not held behind
// any greeting-ended promise (voice-flow-redesign Task 9).
it("dispatches connected immediately on transport connect (no greeting hold)", async () => {
  // ...arrange the hook + a fake session that emits CONNECTED...
  // emit CONNECTED
  // expect result.current.outcome.state === "connected" synchronously after flush,
  // with no dependency on any greeting clip finishing.
});

it("start() does not itself play a greeting clip", async () => {
  // spy on greetingPlayer.playRandomGreeting; call start(); expect it NOT called.
});
```

(Adapt the arrange/act to the file's existing harness/mocks; the intent above is the contract.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `npx vitest run src/greeting/greetingPlayer.test.ts src/transport/useVoiceSession.greeting.test.ts`
Expected: FAIL — `unlockAudioPlayback` undefined; greeting-hold tests still assert old behavior.

- [ ] **Step 3: Write the implementation**

Add to `src/greeting/greetingPlayer.ts`:

```ts
/**
 * iOS audio unlock (voice-flow-redesign Task 9). Play + immediately pause a
 * muted, silent audio element inside the start gesture so a LATER
 * `playRandomGreeting()` (fired on Live mount, after the ceremony) is
 * permitted by Safari's autoplay policy. No-op-safe: swallows a blocked play.
 */
export function unlockAudioPlayback(): void {
  try {
    const el = new Audio();
    el.muted = true;
    // A 1-sample silent wav data URI — enough to satisfy the gesture unlock.
    el.src =
      "data:audio/wav;base64,UklGRiQAAABXQVZFZm10IBAAAAABAAEAgD4AAIA+AAABAAgAZGF0YQAAAAA=";
    void el.play().then(() => el.pause()).catch(() => { /* blocked: greeting will retry on mount */ });
  } catch { /* no Audio ctor (SSR/test): no-op */ }
}
```

In `src/transport/useVoiceSession.ts`:

1. Import `unlockAudioPlayback` alongside the existing greeting import:

```ts
import { playRandomGreeting, unlockAudioPlayback, type GreetingHandle } from "../greeting/greetingPlayer";
```

2. In the CONNECTED branch of `handleSessionEvent`, dispatch immediately (remove the greeting-ended hold). Replace:

```ts
        // Hold the visible "connected/Live" state until the greeting clip has
        // finished -- greeting audio (speaker) and live STT (mic) must not overlap.
        void greetingEndedRef.current.then(() => dispatch(event));
        return;
```

with:

```ts
        // voice-flow-redesign: the orb appears BEFORE the greeting (the greeting
        // now plays on Live mount), so CONNECTED is no longer held behind it.
        dispatch(event);
        return;
```

3. Delete the now-unused greeting refs and their uses:
   - Remove the `greetingRef` and `greetingEndedRef` declarations.
   - In the `TRANSPORT_ERROR` pre-connect branch, remove the `greetingRef.current?.stop(); greetingRef.current = null;` lines (no greeting is playing pre-connect anymore).
   - In `start()`, replace the greeting block:

```ts
    // Instant greeting: play a random pre-rendered clip on this same gesture ...
    greetingRef.current?.stop();
    const handle = await playRandomGreeting();
    greetingRef.current = handle;
    greetingEndedRef.current = handle ? handle.ended : Promise.resolve();

    await beginConnect();
```

   with:

```ts
    // Unlock iOS audio on this gesture so the greeting can play on Live mount
    // (after the ceremony). The clip itself is played by Live, not here
    // (voice-flow-redesign: orb appears, THEN KPH greets).
    unlockAudioPlayback();

    await beginConnect();
```

   - In `stop()`, remove the `greetingRef.current?.stop(); greetingRef.current = null;` lines.

4. Add `endChat` (user-initiated clean end that produces a summary), after `stop`:

```ts
  /** User-initiated "End chat" (voice-flow-redesign §3.4/§3.5): tears the
   * session down and produces a CLEAN session summary so App shows the ended
   * screen. Mirrors the post-connect DISCONNECTED path but on demand. */
  const endChat = useCallback(async () => {
    const elapsedSeconds =
      connectedAtRef.current != null ? Math.max(0, (Date.now() - connectedAtRef.current) / 1000) : 0;
    // Clear the connected latch BEFORE disconnecting so the trailing transport
    // teardown event can't also synthesize a second summary.
    wasConnectedRef.current = false;
    connectedAtRef.current = null;
    retryControllerRef.current?.cancel();
    await sessionRef.current?.disconnect();
    sessionRef.current = null;
    setClient(null);
    setSessionMaxSeconds(null);
    setSessionSummary({ elapsedSeconds, reason: "clean" });
    dispatch({ type: "RESET" });
  }, [dispatch]);
```

5. Add `endChat` to both the `UseVoiceSessionResult` interface and the returned object:

```ts
  /** User-initiated clean end from the Live "End chat" button. */
  endChat: () => Promise<void>;
```
```ts
    stop,
    endChat,
    retryNow,
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npx vitest run src/greeting/greetingPlayer.test.ts src/transport/useVoiceSession.greeting.test.ts`
Expected: PASS. Then run the whole transport suite: `npx vitest run src/transport/` — Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/greeting/greetingPlayer.ts src/greeting/greetingPlayer.test.ts src/transport/useVoiceSession.ts src/transport/useVoiceSession.greeting.test.ts
git commit -m "feat(voice-client): orb-before-greeting — decouple greeting from connect-hold + iOS unlock + endChat"
```

---

### Task 10: Rework Live — compact orb + growing transcript + greeting-on-mount + End chat

**Files:**
- Modify: `src/screens/Live.tsx`, `src/screens/live.css`
- Test: `src/screens/Live.test.tsx` (create)

**Interfaces:**
- Consumes: `PipecatClient`, `useOrbBinding`, `transcriptReducer` (Task 1), `Transcript` (Task 2), `playRandomGreeting`, `Countdown`, `LatencyHud`.
- Produces: default export `Live({ client, sessionMaxSeconds, onEndChat })`.

- [ ] **Step 1: Write the failing test**

```tsx
// src/screens/Live.test.tsx
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import Live from "./Live";

vi.mock("../greeting/greetingPlayer", () => ({
  playRandomGreeting: vi.fn().mockResolvedValue({ ended: Promise.resolve(), stop: vi.fn() }),
}));

// Minimal fake RTVI client: records handlers so the test can push transcript frames.
function makeClient() {
  const handlers: Record<string, (d: unknown) => void> = {};
  return {
    on: (evt: string, cb: (d: unknown) => void) => { handlers[evt] = cb; },
    off: () => {},
    emit: (evt: string, d: unknown) => handlers[evt]?.(d),
  } as never;
}

describe("Live", () => {
  it("plays the greeting once on mount (orb-then-greeting)", async () => {
    const { playRandomGreeting } = await import("../greeting/greetingPlayer");
    render(<Live client={makeClient()} sessionMaxSeconds={null} onEndChat={() => {}} />);
    expect(playRandomGreeting).toHaveBeenCalledTimes(1);
  });

  it("fires onEndChat from the End chat button", () => {
    const onEndChat = vi.fn();
    render(<Live client={makeClient()} sessionMaxSeconds={null} onEndChat={onEndChat} />);
    fireEvent.click(screen.getByRole("button", { name: /end chat/i }));
    expect(onEndChat).toHaveBeenCalledTimes(1);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/screens/Live.test.tsx`
Expected: FAIL — `Live` does not accept `onEndChat` / does not call `playRandomGreeting`.

- [ ] **Step 3: Write the implementation**

Replace `src/screens/Live.tsx` with:

```tsx
import { useEffect, useReducer } from "react";
import { RTVIEvent, type PipecatClient } from "@pipecat-ai/client-js";
import OrbCanvas from "../orb/OrbCanvas";
import { useOrbBinding } from "../orb/useOrbBinding";
import Transcript from "../transcript/Transcript";
import { transcriptReducer, INITIAL_TRANSCRIPT_STATE } from "../transcript/transcriptReducer";
import { playRandomGreeting, type GreetingHandle } from "../greeting/greetingPlayer";
import Countdown from "../timer/Countdown";
import LatencyHud from "../hud/LatencyHud";
import "./live.css";

export interface LiveProps {
  client: PipecatClient;
  sessionMaxSeconds: number | null;
  /** User tapped "End chat" — App routes this to useVoiceSession.endChat(). */
  onEndChat: () => void;
}

/**
 * The live-conversation stage (voice-flow-redesign §3.4): a COMPACT orb header
 * over a growing, scrollable transcript. Mounted by App only once the
 * connection reached "connected" AND the ceremony finished, so mount time is
 * exactly "the orb appears" — which is where the KPH greeting now plays
 * (orb-before-greeting; iOS audio was unlocked on the start gesture).
 */
export default function Live({ client, sessionMaxSeconds, onEndChat }: LiveProps) {
  const orb = useOrbBinding(client);
  const [turns, dispatch] = useReducer(transcriptReducer, INITIAL_TRANSCRIPT_STATE);

  // Play the greeting exactly once as the orb appears.
  useEffect(() => {
    let handle: GreetingHandle | null = null;
    let cancelled = false;
    void playRandomGreeting().then((h) => {
      if (cancelled) { h?.stop(); return; }
      handle = h;
    });
    return () => { cancelled = true; handle?.stop(); };
  }, []);

  useEffect(() => {
    const onUserTranscript = (data: { text: string; final: boolean }) =>
      dispatch({ type: "USER_TRANSCRIPT", text: data.text, final: data.final });
    const onBotTranscript = (data: { text: string }) =>
      dispatch({ type: "AGENT_TRANSCRIPT", text: data.text });

    client.on(RTVIEvent.UserTranscript, onUserTranscript);
    client.on(RTVIEvent.BotTranscript, onBotTranscript);
    return () => {
      client.off(RTVIEvent.UserTranscript, onUserTranscript);
      client.off(RTVIEvent.BotTranscript, onBotTranscript);
    };
  }, [client]);

  return (
    <div className="live">
      <div className="live-orb"><OrbCanvas state={orb.state} amplitude={orb.amplitude} /></div>
      <Transcript turns={turns} />
      <div className="live-bar">
        <button type="button" className="live-endchat" onClick={onEndChat}>End chat</button>
        {sessionMaxSeconds != null && sessionMaxSeconds > 0 ? (
          <Countdown sessionMaxSeconds={sessionMaxSeconds} startedAt={Date.now()} />
        ) : null}
      </div>
      <LatencyHud client={client} />
    </div>
  );
}
```

Append to `src/screens/live.css` (add; keep existing rules the orb relies on):

```css
/* voice-flow-redesign: compact orb header band + bottom action bar. */
.live-orb { position: absolute; top: 0; left: 0; right: 0; height: 176px; z-index: 1; }
.live-orb canvas { max-height: 176px; }
.live-bar {
  position: absolute; bottom: 0; left: 0; right: 0; height: 96px; z-index: 6;
  display: flex; align-items: center; justify-content: space-between; padding: 0 var(--lg);
  background: linear-gradient(to top, color-mix(in srgb, var(--stage-edge) 95%, transparent), transparent);
}
.live-endchat {
  min-height: var(--hit-min); padding: 0 var(--lg); border-radius: 999px; cursor: pointer;
  background: transparent; color: var(--destructive);
  border: 1px solid color-mix(in srgb, var(--destructive) 50%, transparent);
  font-size: var(--sz-label); font-weight: var(--w-semibold);
}
```

> Note: `OrbCanvas` renders a full-bleed canvas; if it does not honor the 176px band via CSS alone, pass any existing size prop it supports, otherwise the `.live-orb` clip is sufficient for the mockup-level layout. Confirm visually in UAT.

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/screens/Live.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/screens/Live.tsx src/screens/live.css src/screens/Live.test.tsx
git commit -m "feat(voice-client): compact-orb + growing-transcript Live stage with End chat"
```

---

### Task 11: SessionEnd — relabel primary action to "Start another"

**Files:**
- Modify: `src/screens/SessionEnd.tsx`
- Test: `src/screens/SessionEnd.test.tsx` (create)

**Interfaces:**
- Produces: `SessionEndProps` gains `onStartAnother` (replaces `onReconnect`); button label becomes "Start another".

- [ ] **Step 1: Write the failing test**

```tsx
// src/screens/SessionEnd.test.tsx
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import SessionEnd from "./SessionEnd";

describe("SessionEnd", () => {
  it("offers Start another and Sign out", () => {
    const onStartAnother = vi.fn();
    const onSignOut = vi.fn();
    render(<SessionEnd elapsedSeconds={252} reason="clean" onStartAnother={onStartAnother} onSignOut={onSignOut} />);
    fireEvent.click(screen.getByRole("button", { name: /start another/i }));
    fireEvent.click(screen.getByRole("button", { name: /sign out/i }));
    expect(onStartAnother).toHaveBeenCalledTimes(1);
    expect(onSignOut).toHaveBeenCalledTimes(1);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/screens/SessionEnd.test.tsx`
Expected: FAIL — `onStartAnother` not a prop; no "Start another" button.

- [ ] **Step 3: Write the implementation**

In `src/screens/SessionEnd.tsx`:
- Rename the prop `onReconnect` → `onStartAnother` in `SessionEndProps` and the function signature.
- Change the primary button text `Reconnect` → `Start another` and its `onClick={onStartAnother}`.
- Update the JSDoc on the prop to: "Returns to the ReadyToStart screen; the next start replays the full ceremony (voice-flow-redesign §3.5)."

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/screens/SessionEnd.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/screens/SessionEnd.tsx src/screens/SessionEnd.test.tsx
git commit -m "feat(voice-client): SessionEnd primary action -> Start another"
```

---

### Task 12: Wire App.tsx to the linear machine + forced-auth land effect

**Files:**
- Modify: `src/App.tsx`
- Test: rewrite `src/App.gate.test.tsx`, `src/App.breadcrumb.test.tsx`; add `src/App.flow.test.tsx`

**Interfaces:**
- Consumes: `resolveScreen`/`ScreenInputs` (Task 5), `decideLandAction`/`LandAction` (Task 4), `wasInteractiveTried`/`markInteractiveTried` (Task 3), `isReturningUser`/`wasSilentTried` (existing), all new screens, and `useVoiceSession.endChat` (Task 9).

- [ ] **Step 1: Write the failing tests**

Add `src/App.flow.test.tsx` asserting the machine end-to-end via mocked hooks:

```tsx
// src/App.flow.test.tsx
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

// Mock useAuth + useVoiceSession so we can drive the flow deterministically.
const auth = { isAuthenticated: false, tierId: null, group: null,
  beginSignIn: vi.fn(), attemptSilentSso: vi.fn().mockResolvedValue(undefined),
  markReturningUser: vi.fn(), clearReturningUser: vi.fn(), signOut: vi.fn(), refresh: vi.fn() };
const voice: any = { outcome: { state: "idle" }, micError: null, client: null, sessionMaxSeconds: null,
  retryStatus: { kind: "idle" }, sessionSummary: null, start: vi.fn(), stop: vi.fn(), endChat: vi.fn(),
  retryNow: vi.fn(), dismissGate: vi.fn(), dismissMicError: vi.fn() };
vi.mock("./auth/useAuth", () => ({ useAuth: () => auth }));
vi.mock("./transport/useVoiceSession", () => ({ useVoiceSession: () => voice }));

import App from "./App";

describe("App linear flow", () => {
  beforeEach(() => { sessionStorage.clear(); localStorage.clear(); vi.clearAllMocks();
    auth.isAuthenticated = false; voice.outcome = { state: "idle" }; voice.sessionSummary = null; });

  it("unauthenticated first-timer is force-redirected to auth (no landing button)", async () => {
    render(<App />);
    // holding surface, and beginSignIn fired automatically
    expect(await screen.findByText(/checking your session/i)).toBeInTheDocument();
    expect(auth.beginSignIn).toHaveBeenCalled();
  });

  it("authenticated + idle shows ReadyToStart", () => {
    auth.isAuthenticated = true;
    render(<App />);
    expect(screen.getByRole("button", { name: /let's start talking/i })).toBeInTheDocument();
  });

  it("connecting shows the ceremony, not Ready", () => {
    auth.isAuthenticated = true; voice.outcome = { state: "connecting" };
    render(<App />);
    expect(screen.getByTestId("ceremony-line")).toBeInTheDocument();
  });
});
```

Update `App.breadcrumb.test.tsx`: keep its anti-loop intent — assert that when `wasInteractiveTried()` is already set and the user returns unauthenticated, App renders the manual nudge and does NOT call `beginSignIn` automatically. Update `App.gate.test.tsx` to the new screen structure (rejection still renders `GateCard`), removing any `Attract`/"Tap to talk" assumptions.

- [ ] **Step 2: Run tests to verify they fail**

Run: `npx vitest run src/App.flow.test.tsx src/App.breadcrumb.test.tsx src/App.gate.test.tsx`
Expected: FAIL — App still renders the old Attract cascade / lacks the land effect.

- [ ] **Step 3: Write the implementation**

Rewrite `src/App.tsx` around `resolveScreen`. Key pieces:

```tsx
import { useEffect, useState } from "react";
import { ensureLiveRegions } from "./a11y/liveRegions";
import Callback from "./screens/Callback";
import NoAccessGate from "./screens/NoAccessGate";
import MicError from "./screens/MicError";
import Live from "./screens/Live";
import UdpBlockedWall from "./screens/UdpBlockedWall";
import SessionEnd from "./screens/SessionEnd";
import ReadyToStart from "./screens/ReadyToStart";
import Ceremony from "./screens/Ceremony";
import LandBounce from "./screens/LandBounce";
import OrbCanvas from "./orb/OrbCanvas";
import GateCard from "./gates/GateCard";
import { gateAction, gateMapping } from "./gates/gateMapping";
import { useAuth } from "./auth/useAuth";
import { useVoiceSession } from "./transport/useVoiceSession";
import { resolveScreen } from "./flow/resolveScreen";
import { decideLandAction } from "./flow/landDecision";
import { isAuthenticated as tokenIsAuthenticated } from "./auth/tokenStore"; // confirmed export: tokenStore exports isAuthenticated()
import { isReturningUser, wasSilentTried, markInteractiveTried, wasInteractiveTried } from "./auth/returningStore";

const NO_ACCESS_TIER_ID = "no-access";
const CALLBACK_PATH = "/callback";

export default function App() {
  const auth = useAuth();
  const voice = useVoiceSession();
  const [onCallbackRoute, setOnCallbackRoute] = useState(() => window.location.pathname === CALLBACK_PATH);
  const [ceremonyDone, setCeremonyDone] = useState(false);

  useEffect(() => { ensureLiveRegions(); }, []);

  // Forced-auth land sequence (voice-flow-redesign §3.1): try invisible silent
  // SSO first (no-op unless returning & not yet tried — may navigate away). If
  // still unauthenticated and no interactive redirect has run this session,
  // auto-fire the full redirect. Otherwise fall through to the manual nudge.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      await auth.attemptSilentSso();
      if (cancelled) return;
      if (!tokenIsAuthenticated() && !onCallbackRoute) {
        const action = decideLandAction({
          isReturning: isReturningUser(),
          silentTried: wasSilentTried(),
          interactiveTried: wasInteractiveTried(),
        });
        if (action === "redirect") {
          markInteractiveTried();
          void auth.beginSignIn();
        }
      }
    })();
    return () => { cancelled = true; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const startConversation = () => {
    setCeremonyDone(false);
    void voice.start();
  };

  const handleAuthenticated = () => {
    auth.refresh();
    window.history.replaceState({}, "", "/");
    setOnCallbackRoute(false);
  };

  const handleGateAction = () => {
    const action = gateAction(voice.outcome.rejection?.error);
    if (action === "sign-out") { auth.signOut(); voice.dismissGate(); return; }
    if (action === "retry") { startConversation(); return; }
    voice.dismissGate();
  };

  // Esc dismisses transient gate/mic copy (unchanged intent).
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      if (voice.outcome.state === "rejected") voice.dismissGate();
      else if (voice.micError) voice.dismissMicError();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [voice.outcome.state, voice.micError, voice.dismissGate, voice.dismissMicError]);

  const screen = resolveScreen({
    onCallbackRoute,
    hasSessionSummary: voice.sessionSummary != null,
    isAuthenticated: auth.isAuthenticated,
    isNoAccessTier: auth.isAuthenticated && auth.tierId === NO_ACCESS_TIER_ID,
    outcomeState: voice.outcome.state,
    retryExhausted: voice.retryStatus.kind === "exhausted",
    hasMicError: voice.micError != null,
    ceremonyDone,
    hasClient: voice.client != null,
  });

  const withStage = (node: React.ReactNode, orbIdle = true) => (
    <div className="stage">{orbIdle ? <OrbCanvas state="idle" amplitude={0} /> : null}{node}</div>
  );

  switch (screen) {
    case "callback":
      return <div className="stage"><Callback onAuthenticated={handleAuthenticated} /></div>;
    case "ended":
      return withStage(
        <SessionEnd
          elapsedSeconds={voice.sessionSummary!.elapsedSeconds}
          reason={voice.sessionSummary!.reason}
          onStartAnother={() => voice.dismissGate()}
          onSignOut={() => { auth.signOut(); voice.dismissGate(); }}
        />,
      );
    case "land": {
      const mode = decideLandAction({ isReturning: isReturningUser(), silentTried: wasSilentTried(), interactiveTried: wasInteractiveTried() });
      return <div className="stage"><LandBounce mode={mode} onSignIn={() => void auth.beginSignIn()} /></div>;
    }
    case "no-access":
      return withStage(<NoAccessGate onSignOut={auth.signOut} />);
    case "mic-error":
      return withStage(<MicError error={voice.micError!} onRetry={startConversation} />);
    case "gate":
      return withStage(
        <GateCard copy={gateMapping(voice.outcome.rejection?.error)} action={gateAction(voice.outcome.rejection?.error)} onAction={handleGateAction} />,
      );
    case "udp-wall":
      return withStage(<UdpBlockedWall onTryAgain={voice.retryNow} />);
    case "ceremony":
      return <div className="stage"><Ceremony onScriptDone={() => setCeremonyDone(true)} /></div>;
    case "live":
      return <div className="stage"><Live client={voice.client!} sessionMaxSeconds={voice.sessionMaxSeconds} onEndChat={() => void voice.endChat()} /></div>;
    case "ready":
    default:
      return <div className="stage"><ReadyToStart onStart={startConversation} /></div>;
  }
}
```

> `tokenStore` exports `isAuthenticated()` (confirmed) — the aliased import above is correct. The "ended" `onStartAnother` uses `voice.dismissGate()` because it clears `sessionSummary` and resets outcome to idle → App then renders `ready`. `MicError` expects `error` typed as its `MicErrorType`; `voice.micError!` already satisfies this (the current App passes it identically), so no cast beyond the non-null assertion is needed.

- [ ] **Step 4: Run tests to verify they pass**

Run: `npx vitest run src/App.flow.test.tsx src/App.breadcrumb.test.tsx src/App.gate.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/App.tsx src/App.flow.test.tsx src/App.breadcrumb.test.tsx src/App.gate.test.tsx
git commit -m "feat(voice-client): linear flow machine + forced-auth bounce in App"
```

---

### Task 13: Retire Attract, Captions, ConnectingRetry + full green gate

**Files:**
- Delete: `src/screens/Attract.tsx`, `src/screens/attract.css`, `src/screens/ConnectingRetry.tsx`, `src/screens/connectingRetry.css`, `src/captions/Captions.tsx`, `src/captions/captionReducer.ts`, `src/captions/Captions.test.tsx`, `src/captions/captionReducer.test.ts`, `src/captions/captions.css`
- Verify: no remaining imports of the deleted modules.

- [ ] **Step 1: Delete retired files**

```bash
git rm src/screens/Attract.tsx src/screens/attract.css \
       src/screens/ConnectingRetry.tsx src/screens/connectingRetry.css \
       src/captions/Captions.tsx src/captions/captionReducer.ts \
       src/captions/Captions.test.tsx src/captions/captionReducer.test.ts \
       src/captions/captions.css
```

- [ ] **Step 2: Verify no dangling imports**

Run: `grep -rnE "Attract|captionReducer|/Captions|ConnectingRetry" src/ || echo "clean"`
Expected: `clean` (no matches). If any match remains, remove the import/usage.

- [ ] **Step 3: Full type-check + test + build gate**

Run: `npm test`
Expected: PASS (entire suite).

Run: `npm run build`
Expected: PASS (`tsc --noEmit` clean, `vite build` succeeds).

- [ ] **Step 4: (If any failure) fix inline, re-run**

Address any remaining reference/type errors surfaced by Steps 2–3, then re-run both commands until green.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "chore(voice-client): retire Attract/Captions/ConnectingRetry; green build"
```

---

## Post-implementation: UAT + deploy (human-driven)

These are NOT code tasks — they are the handoffs called out in the design:

1. **Live mic UAT (required, human):** run the client against a real session and verify, on desktop AND a real iPhone:
   - Unauthenticated arrival bounces straight to auth; return lands on ReadyToStart.
   - "Let's start talking" → ceremony → orb appears → **greeting plays as the orb appears** (the iOS-autoplay flag from Task 9 — confirm audio is NOT silent on iPhone Safari).
   - Transcript grows, scrolls, and does NOT permanently die after a dropped/duplicated frame; "jump to latest" appears when scrolled up.
   - End chat → SessionEnd → "Start another" → full ceremony replays.
   - No echo / talk-over regression from the relaxed greeting-hold (the pre-existing echo verify item).
2. **Deploy:** ship via the existing CI (`build-voice.yml` + `deploy.yml`); confirm the flagged CI-deploy IAM gap is closed first. Client builds to `dist/`, COPY'd into the voice image, served by `server.py` StaticFiles behind CloudFront.

## Self-review notes (author checklist — done)

- **Spec coverage:** §2 machine → Tasks 5/12; §3.1 forced auth + loop guard → Tasks 3/4/8/12; §3.2 ReadyToStart → Task 7; §3.3 ceremony seam → Tasks 6/12; §3.4 compact-orb + transcript + End chat → Tasks 1/2/10; greeting-on-entry (approved orb-then-greeting) → Task 9/10; §3.5 ENDED/Start another → Tasks 9/11/12; §4 transcript model + bug-class fix → Task 1; retirements → Task 13.
- **Type consistency:** `resolveScreen`/`ScreenInputs`, `decideLandAction`/`LandInputs`/`LandAction`, `transcriptReducer`/`TranscriptState`/`TranscriptTurn`, `endChat`, `onStartAnother`, `onEndChat`, `onScriptDone` are used identically across the tasks that define and consume them.
- **Placeholder scan:** the only deferred items are (a) adapting Task 9's transport-test arrange/act to the existing harness mocks, and (b) confirming the `tokenStore` export name in Task 12 — both are explicit "verify against the real file" steps, not vague TODOs.
