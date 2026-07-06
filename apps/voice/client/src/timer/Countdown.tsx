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
 */
export default function Countdown({ sessionMaxSeconds, startedAt }: CountdownProps) {
  const { remainingSeconds, level } = useCountdown(sessionMaxSeconds, startedAt);
  const label = `${formatMSS(remainingSeconds)} left`;
  const announced = `${Math.max(0, Math.round(remainingSeconds))} seconds remaining`;

  return (
    <div className={`countdown-pill ${LEVEL_CLASS[level]}`.trim()}>
      <span aria-hidden="true">{label}</span>
      <span className="sr-only" aria-live="polite">
        {announced}
      </span>
    </div>
  );
}
