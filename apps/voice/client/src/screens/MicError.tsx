import type { MicError as MicErrorType } from "../media/getMic";
import "./micError.css";

export interface MicErrorProps {
  error: MicErrorType;
  /** "Try again" affordance — re-runs the gesture-gated requestMic() flow. */
  onRetry: () => void;
}

/**
 * Distinct, honest mic-error states (CLNT-01, D-12) — verbatim UI-SPEC
 * copy, never merged into one generic "mic error" message. `role="alert"` +
 * `aria-live="assertive"` per the a11y baseline (errors announced,
 * never color-only).
 */
const MIC_ERROR_COPY: Record<MicErrorType, string> = {
  denied: "Mic's blocked. Enable microphone access in your browser settings, then try again.",
  "no-device": "No microphone found. Plug one in or switch devices, then try again.",
  unsupported: "This browser can't do live audio. Try Chrome or Safari.",
};

export default function MicError({ error, onRetry }: MicErrorProps) {
  return (
    <div className="mic-error" role="alert" aria-live="assertive">
      <p className="mic-error-body">{MIC_ERROR_COPY[error]}</p>
      <button type="button" className="mic-error-retry" onClick={onRetry}>
        Try again
      </button>
    </div>
  );
}
