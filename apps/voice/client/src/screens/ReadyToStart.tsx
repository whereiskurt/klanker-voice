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
