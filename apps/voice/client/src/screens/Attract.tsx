import OrbCanvas from "../orb/OrbCanvas";
import "./attract.css";

export interface AttractProps {
  /**
   * Fires on the CTA tap — the single user gesture that unlocks mic + audio
   * playback (iOS) and begins the flow. 05-03 routes this to the OIDC
   * authorization-code+PKCE sign-in redirect; a stub here.
   */
  onTapToTalk: () => void;
}

/**
 * D-07 attract landing: the orb is already alive (ambient idle motion, zero
 * amplitude) with one prominent "Tap to talk" CTA — the "whoa" lands before
 * a word is spoken. Wordmark top-left, orb centered as hero (full-bleed
 * canvas per OrbCanvas), CTA lower-third. Copy is verbatim from the UI-SPEC
 * Copywriting Contract.
 */
export default function Attract({ onTapToTalk }: AttractProps) {
  return (
    <div className="attract">
      <OrbCanvas state="idle" amplitude={0} />

      <div className="attract-wordmark">
        voice<b>.klankermaker.ai</b>
      </div>

      <div className="attract-cta-wrap">
        <button type="button" className="attract-cta" onClick={onTapToTalk}>
          Tap to talk
        </button>
        <p className="attract-cta-sub">A live conversation with the KlankerMaker concierge.</p>
      </div>
    </div>
  );
}
