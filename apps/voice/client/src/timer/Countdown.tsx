import { useEffect, useRef } from "react";
import { announceAssertive, announcePolite } from "../a11y/liveRegions";
import { formatMSS, useCountdown, type CountdownLevel } from "./useCountdown";
import "./timer.css";

export interface CountdownProps {
  /** The tier session_max in seconds -- the server-authoritative cap this
   * pill counts down against (display only, T-05-05-T). */
  sessionMaxSeconds: number;
  /** `Date.now()` at the moment the session reached "connected". */
  startedAt: number;
}

const LEVEL_CLASS: Record<CountdownLevel, string> = {
  normal: "",
  warning: "countdown-pill--warning",
  critical: "countdown-pill--critical",
};

/**
 * Small persistent corner countdown (CLNT-05, D-10 · UI-SPEC §Stage Layout /
 * §Color / §Motion Tokens): top-right desktop, top-center mobile, safe-area
 * offset. Escalates amber at <=30s (synced to the agent's spoken -30s
 * warning, QUOT-03) then red with a `motion-escalate` pulse at <10s.
 * `prefers-reduced-motion` drops the pulse (color/text still escalate) --
 * handled entirely in `timer.css`, no JS branch needed. The orb itself is
 * never recolored by this component (UI-SPEC: "orb unchanged").
 *
 * A visible, non-live `sr-only` span always carries the current spoken-form
 * value ("{n} seconds remaining") so VoiceOver users can swipe to the pill
 * on demand -- it is deliberately NOT `aria-live` (05-07 fix: the previous
 * version re-announced every single second, a real screen-reader-spam
 * anti-pattern). Instead, `announcePolite`/`announceAssertive` (shared
 * `a11y/liveRegions.ts`) fire once at each meaningful escalation BOUNDARY
 * (entering "warning" at <=30s, entering "critical" at <10s) -- one useful
 * interrupt instead of sixty noisy ones.
 */
export default function Countdown({ sessionMaxSeconds, startedAt }: CountdownProps) {
  const { remainingSeconds, level } = useCountdown(sessionMaxSeconds, startedAt);
  const label = `${formatMSS(remainingSeconds)} left`;
  const announced = `${Math.max(0, Math.round(remainingSeconds))} seconds remaining`;

  const prevLevelRef = useRef<CountdownLevel>(level);
  useEffect(() => {
    if (prevLevelRef.current === level) return;
    prevLevelRef.current = level;
    if (level === "warning") announcePolite("About 30 seconds left.");
    if (level === "critical") announceAssertive("Less than 10 seconds left.");
  }, [level]);

  return (
    <div className={`countdown-pill ${LEVEL_CLASS[level]}`.trim()}>
      <span aria-hidden="true">{label}</span>
      <span className="sr-only">{announced}</span>
    </div>
  );
}
