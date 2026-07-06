/**
 * Bounded auto-retry + backoff policy for connection failures (CLNT-02, D-11).
 *
 * On a transport/ICE failure (never a quota/auth rejection -- see
 * `useVoiceSession.ts`, which is careful to only ever call `reportFailure()`
 * for a genuine pre-connect transport failure), the client auto-retries a
 * bounded number of times with visible "Reconnecting... (attempt n of N)"
 * status, then STOPS at an honest terminal "exhausted" verdict -- no
 * infinite spinner. All tunable values live in one place below.
 */

/** Exponential backoff schedule in ms -- index 0 is the delay before the
 * FIRST retry attempt, etc. Length of this array IS `MAX_RETRY_ATTEMPTS`. */
export const BACKOFF_SCHEDULE_MS: readonly number[] = [500, 1000, 2000];

/** Bounded retry count (D-11: "a few times", then stop). */
export const MAX_RETRY_ATTEMPTS = BACKOFF_SCHEDULE_MS.length;

export type RetryStatus =
  | { kind: "idle" }
  | { kind: "retrying"; attempt: number; totalAttempts: number }
  | { kind: "exhausted" };

export const IDLE_RETRY_STATUS: RetryStatus = { kind: "idle" };

export interface RetryControllerOptions {
  /** Re-attempts the connection (a fresh `/api/offer` + transport connect,
   * NOT a silent re-open) -- called once immediately for a manual
   * `retryNow()`, or after the scheduled backoff delay for an automatic
   * retry. */
  attemptConnect: () => void;
  /** Fired synchronously when a retry has been scheduled (before its delay
   * elapses) -- drives `ConnectingRetry.tsx`'s "attempt n of N" text. */
  onRetrying: (attempt: number, totalAttempts: number) => void;
  /** Fired once the bounded schedule is exhausted -- drives
   * `UdpBlockedWall.tsx`. No further automatic retries occur after this. */
  onExhausted: () => void;
  /** Injectable timer fns -- tests drive these with `vi.useFakeTimers()`. */
  setTimeoutFn?: typeof setTimeout;
  clearTimeoutFn?: typeof clearTimeout;
}

export interface RetryController {
  /** Call on a transport/ICE failure. Schedules the next bounded, backed-off
   * retry (see `onRetrying`), or reports `exhausted` once the schedule runs
   * out. Safe to call repeatedly -- each call consumes one schedule slot. */
  reportFailure: () => void;
  /** Call once the connection actually succeeds -- cancels any pending
   * retry timer and resets the attempt counter back to zero so a LATER,
   * unrelated failure starts a fresh bounded schedule. */
  reportSuccess: () => void;
  /** The manual "Try again" CTA on the exhausted wall: resets the attempt
   * counter to zero and re-attempts immediately (not on a timer). */
  retryNow: () => void;
  /** Resets the attempt counter to zero without attempting a connection
   * (e.g. a brand-new `start()` flow that owns its own first attempt). */
  reset: () => void;
  /** Cancels any pending retry timer without resetting the counter (e.g.
   * component unmount / explicit `stop()`). */
  cancel: () => void;
}

/**
 * Builds a stateful (but otherwise pure/deterministic) retry controller.
 * Holds no React state itself -- `useVoiceSession.ts` owns a single
 * long-lived instance (via `useRef`) and mirrors `onRetrying`/`onExhausted`
 * into its own React state for rendering.
 */
export function createRetryController(options: RetryControllerOptions): RetryController {
  const {
    attemptConnect,
    onRetrying,
    onExhausted,
    setTimeoutFn = setTimeout,
    clearTimeoutFn = clearTimeout,
  } = options;

  let attemptsMade = 0;
  let timer: ReturnType<typeof setTimeout> | null = null;

  function clearTimer(): void {
    if (timer != null) {
      clearTimeoutFn(timer);
      timer = null;
    }
  }

  function reportFailure(): void {
    clearTimer();
    if (attemptsMade >= MAX_RETRY_ATTEMPTS) {
      onExhausted();
      return;
    }
    const delayMs = BACKOFF_SCHEDULE_MS[attemptsMade];
    attemptsMade += 1;
    onRetrying(attemptsMade, MAX_RETRY_ATTEMPTS);
    timer = setTimeoutFn(() => {
      timer = null;
      attemptConnect();
    }, delayMs);
  }

  function reportSuccess(): void {
    clearTimer();
    attemptsMade = 0;
  }

  function retryNow(): void {
    clearTimer();
    attemptsMade = 0;
    attemptConnect();
  }

  function reset(): void {
    clearTimer();
    attemptsMade = 0;
  }

  function cancel(): void {
    clearTimer();
  }

  return { reportFailure, reportSuccess, retryNow, reset, cancel };
}
