import "./udpBlockedWall.css";

export interface UdpBlockedWallProps {
  /** Manual "Try again" -- resets the bounded retry schedule and re-attempts
   * immediately (see `retryPolicy.ts` `retryNow()`). */
  onTryAgain: () => void;
}

/**
 * The honest UDP-blocked wall (CLNT-02, D-11): once the bounded auto-retry
 * schedule is exhausted, STOP and show a clear, honest message -- no TURN
 * fallback in v1 (CLNT-09, deferred), no infinite spinner. `role="alert"` +
 * `aria-live="assertive"` per the a11y baseline (errors announced, never
 * color-only). Copy is verbatim from the UI-SPEC Copywriting Contract.
 */
export default function UdpBlockedWall({ onTryAgain }: UdpBlockedWallProps) {
  return (
    <div className="udp-blocked-wall" role="alert" aria-live="assertive">
      <div className="udp-blocked-card">
        <h1 className="udp-blocked-heading">This network blocks the audio channel.</h1>
        <p className="udp-blocked-body">
          Some Wi-Fi networks block the live-audio connection. Try switching to cellular or a
          personal hotspot.
        </p>
        <button type="button" className="udp-blocked-cta" onClick={onTryAgain}>
          Try again
        </button>
      </div>
    </div>
  );
}
