import type { GateAction, GateCopy } from "./gateMapping";
import "./gateCard.css";

export interface GateCardProps {
  copy: GateCopy;
  action: GateAction;
  onAction: () => void;
}

const ACTION_LABEL: Record<GateAction, string> = {
  retry: "Try again",
  "sign-out": "Sign out",
  dismiss: "Reconnect",
};

/**
 * Renders a mapped typed start-gate rejection (D-14, `gateMapping.ts`) as a
 * translucent secondary-surface card over the still-alive orb -- the same
 * treatment as `NoAccessGate.tsx`/`UdpBlockedWall.tsx` (D-13 exclusive-
 * adjacent tone carried through every gate/wall surface). `role="alert"` +
 * `aria-live="assertive"` per the a11y baseline (errors announced, never
 * color-only). The single CTA's label/behavior is driven by `action`:
 * "Sign out" for the no-access reject (reuses the D-04 low-consequence,
 * no-modal pattern), "Try again" for the one retryable transient reject, or
 * "Reconnect" (dismiss back to attract) for everything else.
 */
export default function GateCard({ copy, action, onAction }: GateCardProps) {
  return (
    <div className="gate-card-overlay" role="alert" aria-live="assertive">
      <div className="gate-card">
        <h1 className="gate-card-heading">{copy.heading}</h1>
        <p className="gate-card-body">{copy.body}</p>
        <button type="button" className="gate-card-cta" onClick={onAction}>
          {ACTION_LABEL[action]}
        </button>
      </div>
    </div>
  );
}
