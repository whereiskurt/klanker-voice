import { useState } from "react";
import "./noAccessGate.css";

export interface NoAccessGateProps {
  /** Single tap, no modal — clears the in-memory token and returns to attract. */
  onSignOut: () => void;
}

/**
 * D-13 exclusive/invite-only gate for an authenticated `no-access`-tier
 * user: a translucent secondary-surface card over the still-alive orb
 * stage. Aspirational, never a dead-end — copy is verbatim from the
 * UI-SPEC Copywriting Contract. The "How to get a code" affordance is the
 * panel's single reserved-accent CTA; "Sign out" is a plain secondary
 * action (D-04: low consequence, in-memory token only).
 */
export default function NoAccessGate({ onSignOut }: NoAccessGateProps) {
  const [expanded, setExpanded] = useState(false);

  return (
    <div className="no-access-gate">
      <div className="no-access-card">
        <h1 className="no-access-heading">You're on the list — almost.</h1>
        <p className="no-access-body">
          This is an exclusive demo — Kurt needs to give you access. You'll need an access code to
          start a conversation.
        </p>

        <button
          type="button"
          className="no-access-cta"
          aria-expanded={expanded}
          onClick={() => setExpanded((value) => !value)}
        >
          How to get a code
        </button>

        {expanded ? (
          <p className="no-access-expander">
            Find Kurt — he's usually easy to spot — and ask for a klanker-voice access code. Redeem
            it, then come back and tap the orb again.
          </p>
        ) : null}

        <button type="button" className="no-access-signout" onClick={onSignOut}>
          Sign out
        </button>
      </div>
    </div>
  );
}
