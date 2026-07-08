import { useCallback, useEffect, useState } from "react";
import { ensureLiveRegions } from "./a11y/liveRegions";
import Callback from "./screens/Callback";
import NoAccessGate from "./screens/NoAccessGate";
import MicError from "./screens/MicError";
import Live from "./screens/Live";
import UdpBlockedWall from "./screens/UdpBlockedWall";
import SessionEnd from "./screens/SessionEnd";
import ReadyToStart from "./screens/ReadyToStart";
import Ceremony from "./screens/Ceremony";
import LandBounce from "./screens/LandBounce";
import OrbCanvas from "./orb/OrbCanvas";
import GateCard from "./gates/GateCard";
import { gateAction, gateMapping } from "./gates/gateMapping";
import { useAuth } from "./auth/useAuth";
import { useVoiceSession } from "./transport/useVoiceSession";
import { resolveScreen } from "./flow/resolveScreen";
import { decideLandAction } from "./flow/landDecision";
import { isReturningUser, wasSilentTried, markInteractiveTried, wasInteractiveTried } from "./auth/returningStore";

/** Matches auth.py's NO_ACCESS_TIER_ID default — the no-access gate trigger (D-13). */
const NO_ACCESS_TIER_ID = "no-access";

const CALLBACK_PATH = "/callback";

// klanker-voice — App shell.
//
// Renders the full-bleed 100dvh immersive stage background (D-05). Routing
// is a plain pathname check (no router lib — server.py's 404 SPA fallback
// already makes /callback a valid deep link, 05-01-SUMMARY.md). The whole
// foreground is now a single linear machine (voice-flow-redesign §2):
// resolveScreen is the sole source of truth for "what's on screen", and
// this component's job is just wiring live signals into it and rendering
// the returned enum.
export default function App() {
  const auth = useAuth();
  const voice = useVoiceSession();
  const [onCallbackRoute, setOnCallbackRoute] = useState(() => window.location.pathname === CALLBACK_PATH);
  const [ceremonyDone, setCeremonyDone] = useState(false);

  // Mount the shared aria-live regions as early as possible (05-07
  // hardening — see a11y/liveRegions.ts) so the very first Countdown
  // boundary announcement isn't delayed by lazy DOM-node creation.
  useEffect(() => {
    ensureLiveRegions();
  }, []);

  // Forced-auth land sequence (voice-flow-redesign §3.1): an unauthenticated
  // arrival never sees a real landing page. First, try the invisible silent
  // SSO attempt (a no-op unless returning + not yet tried this load — it may
  // navigate away). If that didn't happen (or the browser is still here
  // afterwards) and we're still unauthenticated and not mid-callback, force
  // the full interactive redirect exactly once per session — a second
  // still-unauthenticated arrival gets the manual nudge instead of an
  // auto-bounce loop (the `interactiveTried` one-shot guard).
  useEffect(() => {
    let cancelled = false;
    (async () => {
      const silentTriedBefore = wasSilentTried(); // snapshot BEFORE attemptSilentSso marks it
      await auth.attemptSilentSso();
      if (cancelled) return;
      if (!auth.isAuthenticated && !onCallbackRoute) {
        const action = decideLandAction({
          isReturning: isReturningUser(),
          silentTried: silentTriedBefore, // use the pre-await snapshot
          interactiveTried: wasInteractiveTried(),
        });
        if (action === "redirect") {
          markInteractiveTried();
          void auth.beginSignIn();
        }
      }
    })();
    return () => {
      cancelled = true;
    };
    // Runs exactly once on mount — this is a one-shot land sequence, not a
    // reactive effect over auth/onCallbackRoute.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  /** Resets the ceremony gate then kicks off a fresh connect attempt (a new
   * `/api/offer`, never a silent transport reopen). Shared by the
   * ReadyToStart CTA, the gate card's "retry" action, and mic-error retry —
   * every one of them is "start a brand new attempt from scratch". */
  const startConversation = () => {
    setCeremonyDone(false);
    void voice.start();
  };

  // Stable identity REQUIRED: Ceremony's effect deps include onScriptDone, so
  // an inline arrow (new identity each render) would re-run the effect after
  // it fires and re-schedule the onScriptDone timer while Ceremony stays
  // mounted (slow connect). useCallback keeps it referentially stable so it
  // fires exactly once. setCeremonyDone from useState is already stable.
  const handleScriptDone = useCallback(() => setCeremonyDone(true), []);

  // NOTE: does NOT call markReturningUser() here (T-05.2-gap-1 fix) — Callback
  // already marks it itself on the successful code-exchange path, right after
  // setToken(). The login_required/interaction_required safe-degrade path
  // calls clearReturningUser() and then this same shared callback; if this
  // handler re-marked the breadcrumb unconditionally, it would undo that
  // clear in the same tick (05.2-VERIFICATION.md gap #1) and a signed-out
  // device would re-loop the silent SSO bounce forever.
  const handleAuthenticated = () => {
    auth.refresh();
    window.history.replaceState({}, "", "/");
    setOnCallbackRoute(false);
  };

  // Typed start-gate rejection (D-14): re-run the quota start-gate via
  // startConversation (a fresh /api/offer) or dismiss/sign-out.
  const handleGateAction = () => {
    const action = gateAction(voice.outcome.rejection?.error);
    if (action === "sign-out") {
      auth.signOut();
      voice.dismissGate();
      return;
    }
    if (action === "retry") {
      startConversation();
      return;
    }
    voice.dismissGate();
  };

  // Esc dismisses transient gate/mic copy (UI-SPEC a11y baseline: "Esc
  // dismisses transient gate copy where applicable").
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      if (voice.outcome.state === "rejected") voice.dismissGate();
      else if (voice.micError) voice.dismissMicError();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [voice.outcome.state, voice.micError, voice.dismissGate, voice.dismissMicError]);

  const screen = resolveScreen({
    onCallbackRoute,
    hasSessionSummary: voice.sessionSummary != null,
    isAuthenticated: auth.isAuthenticated,
    isNoAccessTier: auth.isAuthenticated && auth.tierId === NO_ACCESS_TIER_ID,
    outcomeState: voice.outcome.state,
    retryExhausted: voice.retryStatus.kind === "exhausted",
    hasMicError: voice.micError != null,
    ceremonyDone,
    hasClient: voice.client != null,
  });

  const withStage = (node: React.ReactNode, orbIdle = true) => (
    <div className="stage">
      {orbIdle ? <OrbCanvas state="idle" amplitude={0} /> : null}
      {node}
    </div>
  );

  switch (screen) {
    case "callback":
      return (
        <div className="stage">
          <Callback onAuthenticated={handleAuthenticated} />
        </div>
      );
    case "ended":
      return withStage(
        <SessionEnd
          elapsedSeconds={voice.sessionSummary!.elapsedSeconds}
          reason={voice.sessionSummary!.reason}
          onStartAnother={() => voice.dismissGate()}
          onSignOut={() => {
            auth.signOut();
            voice.dismissGate();
          }}
        />,
      );
    case "land": {
      const mode = decideLandAction({
        isReturning: isReturningUser(),
        silentTried: wasSilentTried(),
        interactiveTried: wasInteractiveTried(),
      });
      return (
        <div className="stage">
          <LandBounce mode={mode} onSignIn={() => void auth.beginSignIn()} />
        </div>
      );
    }
    case "no-access":
      return withStage(<NoAccessGate onSignOut={auth.signOut} />);
    case "mic-error":
      return withStage(<MicError error={voice.micError!} onRetry={startConversation} />);
    case "gate":
      return withStage(
        <GateCard
          copy={gateMapping(voice.outcome.rejection?.error)}
          action={gateAction(voice.outcome.rejection?.error)}
          onAction={handleGateAction}
        />,
      );
    case "udp-wall":
      return withStage(<UdpBlockedWall onTryAgain={voice.retryNow} />);
    case "ceremony":
      return (
        <div className="stage">
          <Ceremony onScriptDone={handleScriptDone} />
        </div>
      );
    case "live":
      return (
        <div className="stage">
          <Live client={voice.client!} sessionMaxSeconds={voice.sessionMaxSeconds} onEndChat={() => void voice.endChat()} />
        </div>
      );
    case "ready":
    default:
      return (
        <div className="stage">
          <ReadyToStart onStart={startConversation} />
        </div>
      );
  }
}
