import { useEffect, useState } from "react";

/**
 * Session-countdown escalation levels (CLNT-05, D-10 · UI-SPEC §Color /
 * §Motion Tokens). "warning" aligns with the server's spoken -30s wind-down
 * warning (QUOT-03); "critical" is the last-10-seconds red pulse.
 */
export type CountdownLevel = "normal" | "warning" | "critical";

export interface CountdownState {
  /** Seconds left, clamped to >=0 -- never negative once the cap is hit. */
  remainingSeconds: number;
  level: CountdownLevel;
}

/** Synced to the server's `winddown_warning_seconds` default (config.py) --
 * the pill turns amber at the same moment the agent starts speaking its
 * -30s warning (QUOT-03). */
export const WARNING_THRESHOLD_SECONDS = 30;
/** UI-SPEC §Color: "<10s" red pill + motion-escalate pulse. */
export const CRITICAL_THRESHOLD_SECONDS = 10;

const TICK_MS = 1000;

/** Pure escalation-level lookup -- exported so both the hook and its tests
 * can reason about boundaries without re-deriving the thresholds. */
export function levelForRemaining(remainingSeconds: number): CountdownLevel {
  if (remainingSeconds < CRITICAL_THRESHOLD_SECONDS) return "critical";
  if (remainingSeconds <= WARNING_THRESHOLD_SECONDS) return "warning";
  return "normal";
}

/** "{m:ss}" per the UI-SPEC Copywriting Contract's countdown label. Rounds
 * to the nearest whole second and clamps to >=0 (never "-0:01"). */
export function formatMSS(totalSeconds: number): string {
  const clamped = Math.max(0, Math.round(totalSeconds));
  const minutes = Math.floor(clamped / 60);
  const seconds = clamped % 60;
  return `${minutes}:${String(seconds).padStart(2, "0")}`;
}

/**
 * Derives the live countdown from a fixed `sessionMaxSeconds` cap (the tier
 * session_max, sourced from the `/api/offer` connect flow -- see
 * `useVoiceSession.ts`) and `startedAt` (the moment the session reached
 * "connected", a wall-clock `Date.now()` timestamp owned by the caller,
 * e.g. `Live.tsx`).
 *
 * Ticks ~1/s while `startedAt` is set; before that (`startedAt === null`,
 * e.g. a not-yet-connected caller) it reports the full cap with no ticking.
 * This is DISPLAY ONLY -- the server's own service timer is the
 * authoritative hard-stop (T-05-05-T); a tampered/paused client clock
 * cannot extend a session.
 */
export function useCountdown(sessionMaxSeconds: number, startedAt: number | null): CountdownState {
  const [now, setNow] = useState<number>(() => Date.now());

  useEffect(() => {
    if (startedAt == null) return undefined;
    const id = setInterval(() => setNow(Date.now()), TICK_MS);
    return () => clearInterval(id);
  }, [startedAt]);

  if (startedAt == null) {
    return { remainingSeconds: sessionMaxSeconds, level: levelForRemaining(sessionMaxSeconds) };
  }

  const elapsedSeconds = (now - startedAt) / 1000;
  const remainingSeconds = Math.max(0, sessionMaxSeconds - elapsedSeconds);
  return { remainingSeconds, level: levelForRemaining(remainingSeconds) };
}
