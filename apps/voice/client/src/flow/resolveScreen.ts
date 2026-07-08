import type { ConnectionState } from "../transport/connectionState";

/**
 * Every foreground/interrupt surface the voice SPA can show
 * (voice-flow-redesign §2). `land` is the forced-auth surface; its sub-mode
 * (holding vs nudge) is chosen separately by `decideLandAction`.
 */
export type Screen =
  | "callback" | "ended" | "land" | "no-access"
  | "mic-error" | "gate" | "udp-wall" | "ceremony" | "live" | "ready";

export interface ScreenInputs {
  onCallbackRoute: boolean;
  hasSessionSummary: boolean;
  isAuthenticated: boolean;
  isNoAccessTier: boolean;
  outcomeState: ConnectionState;
  retryExhausted: boolean;
  hasMicError: boolean;
  ceremonyDone: boolean;
  hasClient: boolean;
}

/** The single source of truth for "what screen are we on". Pure — App wires
 * live signals in and renders the returned enum. Order IS the precedence. */
export function resolveScreen(i: ScreenInputs): Screen {
  if (i.onCallbackRoute) return "callback";
  if (i.hasSessionSummary) return "ended";
  if (!i.isAuthenticated) return "land";
  if (i.isNoAccessTier) return "no-access";
  if (i.hasMicError) return "mic-error";
  if (i.outcomeState === "rejected") return "gate";
  if (i.retryExhausted) return "udp-wall";
  if (i.outcomeState === "connected" && i.ceremonyDone && i.hasClient) return "live";
  // requesting-mic / connecting / failed(retrying) / connected-but-ceremony-not-done
  if (
    i.outcomeState === "requesting-mic" ||
    i.outcomeState === "connecting" ||
    i.outcomeState === "failed" ||
    i.outcomeState === "connected"
  ) {
    return "ceremony";
  }
  return "ready";
}
