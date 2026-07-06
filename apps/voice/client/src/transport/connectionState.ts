/**
 * Pure connection-state reducer for the live voice session (CLNT-02).
 *
 * States:
 *  - "idle": not yet started.
 *  - "requesting-mic": the gesture-gated `getUserMedia` (Task 1) is in flight.
 *  - "connecting": the SmallWebRTC offer/answer + ICE negotiation is in flight.
 *  - "connected": bot-ready, audio flowing both ways.
 *  - "rejected": `/api/offer` answered with a non-2xx -- a 401 (bad/expired
 *    token) or a typed 403/429 quota reject (`quota.GateResult` body
 *    `{error, message}`, see server.py). Media never started.
 *  - "failed": a transport/ICE error, surfaced as a distinct outcome from a
 *    rejection (see `voiceSession.ts` for why the vendor SmallWebRTCTransport
 *    needs help distinguishing the two).
 *
 * "rejected" is deliberately distinct from "failed": rejected means the
 * server's start_gate refused the session outright (T-05-04-E -- the
 * server is authoritative and the client never bypasses it, no media ever
 * starts); "failed" means something else went wrong with the transport
 * (ICE/network) after a gate-accepted attempt. 05-06 owns the actual
 * retry/backoff *policy* built on top of these two distinct outcomes.
 */
export type ConnectionState = "idle" | "requesting-mic" | "connecting" | "connected" | "rejected" | "failed";

/** `/api/offer`'s non-2xx JSON body shape (`quota.GateResult` rejects, or the plain 401 body). */
export interface OfferRejection {
  /** HTTP status: 401 unauthorized, or a typed 403/429 quota reject. */
  status: number;
  error?: string;
  message?: string;
}

export type ConnectionEvent =
  | { type: "REQUEST_MIC" }
  | { type: "MIC_GRANTED" }
  | { type: "MIC_ERROR" }
  | { type: "CONNECTED" }
  | { type: "OFFER_REJECTED"; rejection: OfferRejection }
  | { type: "TRANSPORT_ERROR"; message?: string }
  | { type: "DISCONNECTED" }
  | { type: "RESET" };

export interface ConnectionOutcome {
  state: ConnectionState;
  rejection?: OfferRejection;
  error?: string;
}

export const INITIAL_CONNECTION_OUTCOME: ConnectionOutcome = { state: "idle" };

/** One reducer step. Pure -- no I/O, no timers; callers own the side effects. */
export function connectionReducer(current: ConnectionOutcome, event: ConnectionEvent): ConnectionOutcome {
  switch (event.type) {
    case "REQUEST_MIC":
      return { state: "requesting-mic" };
    case "MIC_GRANTED":
      return { state: "connecting" };
    case "MIC_ERROR":
      return INITIAL_CONNECTION_OUTCOME;
    case "CONNECTED":
      return { state: "connected" };
    case "OFFER_REJECTED":
      return { state: "rejected", rejection: event.rejection };
    case "TRANSPORT_ERROR":
      return { state: "failed", error: event.message };
    case "DISCONNECTED":
      // Only a *clean* end of a live session resets to idle. A stray
      // onDisconnected firing after we've already recorded a rejection or
      // failure (e.g. our own cleanup disconnect in voiceSession.ts) must
      // not stomp on that outcome.
      return current.state === "connected" ? INITIAL_CONNECTION_OUTCOME : current;
    case "RESET":
      return INITIAL_CONNECTION_OUTCOME;
    default:
      return current;
  }
}
