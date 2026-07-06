import { useState } from "react";
import Attract from "./screens/Attract";
import Callback from "./screens/Callback";
import NoAccessGate from "./screens/NoAccessGate";
import MicError from "./screens/MicError";
import Live from "./screens/Live";
import OrbCanvas from "./orb/OrbCanvas";
import { useAuth } from "./auth/useAuth";
import { useVoiceSession } from "./transport/useVoiceSession";

/** Matches auth.py's NO_ACCESS_TIER_ID default — the no-access gate trigger (D-13). */
const NO_ACCESS_TIER_ID = "no-access";

const CALLBACK_PATH = "/callback";

// klanker-voice — App shell.
//
// Renders the full-bleed 100dvh immersive stage background (D-05). Routing
// is a plain pathname check (no router lib — server.py's 404 SPA fallback
// already makes /callback a valid deep link, 05-01-SUMMARY.md): "/callback"
// renders the OIDC callback screen; otherwise attract -> (no-access gate |
// mic-ready stage) based on the in-memory token (CLNT-08, D-04).
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

  if (onCallbackRoute) {
    return (
      <div className="stage">
        <Callback onAuthenticated={handleAuthenticated} />
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

  return (
    <div className="stage">
      <Attract onTapToTalk={handleTapToTalk} />
      {voice.micError ? <MicError error={voice.micError} onRetry={handleTapToTalk} /> : null}
    </div>
  );
}
