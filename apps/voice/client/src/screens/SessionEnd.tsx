import { formatMSS } from "../timer/useCountdown";
import "./sessionEnd.css";

export interface SessionEndProps {
  /** Elapsed spoken time from "connected" to close, seconds (CLNT-07). */
  elapsedSeconds: number;
  /** "clean": normal server-driven end (goodbye/idle/timer) -- the
   * standard "Nice talking with you." summary. "provider-error": the
   * transport dropped unexpectedly after a live session -- the generic
   * provider-error end copy instead. */
  reason: "clean" | "provider-error";
  /** Re-runs the FULL start flow -- a fresh `/api/offer` that re-executes
   * the server's quota start_gate, NOT a silent transport reopen (D-14). If
   * that offer is rejected, the caller routes to the matching `GateCard`
   * rather than showing a raw error. */
  onReconnect: () => void;
  /** Single tap, no modal (D-04: low consequence, in-memory token only). */
  onSignOut: () => void;
}

/** Verbatim UI-SPEC "Generic provider-error end" contract string, split at
 * its sentence boundary so it fits the same heading+body card layout as
 * every other gate/wall surface -- concatenating heading + " " + body
 * reproduces the exact contract string. */
const PROVIDER_ERROR_HEADING = "Something hiccuped on our side — the session ended cleanly.";
const PROVIDER_ERROR_BODY = "Tap Reconnect to try again.";

/**
 * Clean session end + one-click reconnect (CLNT-07, D-14): a brief summary
 * card (verbatim UI-SPEC copy) with a single "Reconnect" CTA and a "Sign
 * out" secondary. No modal confirmations (low-consequence, in-memory token,
 * server-managed lifecycle -- UI-SPEC Destructive-actions note).
 */
export default function SessionEnd({ elapsedSeconds, reason, onReconnect, onSignOut }: SessionEndProps) {
  const heading = reason === "provider-error" ? PROVIDER_ERROR_HEADING : "Nice talking with you.";
  const body =
    reason === "provider-error" ? PROVIDER_ERROR_BODY : `That session's done — ${formatMSS(elapsedSeconds)} spoken.`;

  return (
    <div className="session-end" role="status" aria-live="polite">
      <div className="session-end-card">
        <h1 className="session-end-heading">{heading}</h1>
        <p className="session-end-body">{body}</p>
        <button type="button" className="session-end-reconnect" onClick={onReconnect}>
          Reconnect
        </button>
        <button type="button" className="session-end-signout" onClick={onSignOut}>
          Sign out
        </button>
      </div>
    </div>
  );
}
