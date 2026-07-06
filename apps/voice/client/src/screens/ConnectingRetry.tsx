import "./connectingRetry.css";

export interface ConnectingRetryProps {
  /** 1-indexed current attempt number. */
  attempt: number;
  /** Bounded schedule length (see `retryPolicy.ts` MAX_RETRY_ATTEMPTS). */
  totalAttempts: number;
}

/**
 * Bounded auto-retry status (CLNT-02, D-11): "Reconnecting... (attempt n of
 * N)" -- verbatim UI-SPEC copy. `aria-live="polite"` so the a11y baseline's
 * "connection status ... announced" requirement holds without a visual-only
 * cue. Distinct from `UdpBlockedWall.tsx`, which only renders once the
 * bounded schedule is exhausted.
 */
export default function ConnectingRetry({ attempt, totalAttempts }: ConnectingRetryProps) {
  return (
    <div className="connecting-retry" role="status" aria-live="polite">
      <p className="connecting-retry-body">
        Reconnecting… (attempt {attempt} of {totalAttempts})
      </p>
    </div>
  );
}
