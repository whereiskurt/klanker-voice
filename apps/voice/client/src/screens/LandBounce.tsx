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
