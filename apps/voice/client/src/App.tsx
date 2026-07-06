import { useState } from "react";
import Attract from "./screens/Attract";
import Callback from "./screens/Callback";
import NoAccessGate from "./screens/NoAccessGate";
import MicError from "./screens/MicError";
import Live from "./screens/Live";
import ConnectingRetry from "./screens/ConnectingRetry";
import UdpBlockedWall from "./screens/UdpBlockedWall";
import SessionEnd from "./screens/SessionEnd";
import OrbCanvas from "./orb/OrbCanvas";
import { useAuth } from "./auth/useAuth";
import { useVoiceSession } from "./transport/useVoiceSession";
import GateCard from "./gates/GateCard";
import { gateAction, gateMapping } from "./gates/gateMapping";

/** Matches auth.py's NO_ACCESS_TIER_ID default — the no-access gate trigger (D-13). */
const NO_ACCESS_TIER_ID = "no-access";

const CALLBACK_PATH = "/callback";

// klanker-voice — App shell.
//
// Renders the full-bleed 100dvh immersive stage background (D-05). Routing
// is a plain pathname check (no router lib — server.py's 404 SPA fallback
// already makes /callback a valid deep link, 05-01-SUMMARY.md): "/callback"
// renders the OIDC callback screen; otherwise:
//   session-end summary (CLNT-07) -> no-access gate | typed gate rejection
//   (CLNT-07/D-14) -> live conversation | UDP-blocked wall / reconnecting
//   status (CLNT-02/D-11) -> attract (CLNT-08, D-04).
export default function App() {
  const auth = useAuth();
  const voice = useVoiceSession();
  const [onCallbackRoute, setOnCallbackRoute] = useState(
    () => window.location.pathname === CALLBACK_PATH,
  );

  const handleTapToTalk = () => {
    if (!auth.isAuthenticated) {
      void auth.beginSignIn();
      return;
    }
    // Authenticated + has access: gesture-gated mic -> connect (CLNT-01/02).
    // The no-access case never reaches this handler (Attract isn't rendered).
    void voice.start();
  };

  const handleAuthenticated = () => {
    auth.refresh();
    window.history.replaceState({}, "", "/");
    setOnCallbackRoute(false);
  };

  // Typed start-gate rejection (D-14): re-run the quota start-gate (Task 2
  // action for "retry", and Task 3's reconnect flow both funnel through
  // `voice.start()` -- a fresh /api/offer, never a silent transport reopen)
  // or dismiss/sign-out back to the attract stage.
  const handleGateAction = () => {
    const errorType = voice.outcome.rejection?.error;
    const action = gateAction(errorType);
    if (action === "sign-out") {
      auth.signOut();
      voice.dismissGate();
      return;
    }
    if (action === "retry") {
      void voice.start();
      return;
    }
    voice.dismissGate();
  };

  const handleSignOutFromSessionEnd = () => {
    auth.signOut();
    voice.dismissGate();
  };

  if (onCallbackRoute) {
    return (
      <div className="stage">
        <Callback onAuthenticated={handleAuthenticated} />
      </div>
    );
  }

  // A live session just ended (CLNT-07, D-14) -- summary + reconnect, before
  // any other gate/wall/attract check (a fresh reconnect attempt below may
  // itself land back on one of those states, which then take over normally).
  if (voice.sessionSummary) {
    return (
      <div className="stage">
        <OrbCanvas state="idle" amplitude={0} />
        <SessionEnd
          elapsedSeconds={voice.sessionSummary.elapsedSeconds}
          reason={voice.sessionSummary.reason}
          onReconnect={() => void voice.start()}
          onSignOut={handleSignOutFromSessionEnd}
        />
      </div>
    );
  }

  if (auth.isAuthenticated && auth.tierId === NO_ACCESS_TIER_ID) {
    return (
      <div className="stage">
        <OrbCanvas state="idle" amplitude={0} />
        <NoAccessGate onSignOut={auth.signOut} />
      </div>
    );
  }

  // The live conversation (CLNT-02/03/04): mounted only once the connection
  // state machine has actually reached "connected" -- no conversation UI
  // exists before a real bot-ready signal (T-05-04-E).
  if (voice.outcome.state === "connected" && voice.client) {
    return (
      <div className="stage">
        <Live client={voice.client} sessionMaxSeconds={voice.sessionMaxSeconds} />
      </div>
    );
  }

  // A typed start-gate rejection (D-14): specific in-client gate copy, never
  // a raw error and never the UDP-blocked wall (that's only for a genuine
  // transport/ICE failure, Task 1).
  if (voice.outcome.state === "rejected") {
    const errorType = voice.outcome.rejection?.error;
    return (
      <div className="stage">
        <OrbCanvas state="idle" amplitude={0} />
        <GateCard copy={gateMapping(errorType)} action={gateAction(errorType)} onAction={handleGateAction} />
      </div>
    );
  }

  // Bounded auto-retry exhausted (CLNT-02, D-11): the honest UDP-blocked
  // wall -- STOP, no infinite spinner.
  if (voice.retryStatus.kind === "exhausted") {
    return (
      <div className="stage">
        <OrbCanvas state="idle" amplitude={0} />
        <UdpBlockedWall onTryAgain={voice.retryNow} />
      </div>
    );
  }

  return (
    <div className="stage">
      <Attract onTapToTalk={handleTapToTalk} />
      {voice.micError ? <MicError error={voice.micError} onRetry={handleTapToTalk} /> : null}
      {voice.retryStatus.kind === "retrying" ? (
        <ConnectingRetry attempt={voice.retryStatus.attempt} totalAttempts={voice.retryStatus.totalAttempts} />
      ) : null}
    </div>
  );
}
