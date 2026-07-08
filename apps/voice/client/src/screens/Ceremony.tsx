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
