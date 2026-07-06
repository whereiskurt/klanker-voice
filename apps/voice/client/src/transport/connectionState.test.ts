import { describe, expect, it } from "vitest";
import { connectionReducer, INITIAL_CONNECTION_OUTCOME, type ConnectionOutcome } from "./connectionState";
import { buildConnectParams } from "./voiceSession";

describe("connectionReducer", () => {
  it("transitions idle -> requesting-mic -> connecting -> connected on the ordered events", () => {
    let outcome: ConnectionOutcome = INITIAL_CONNECTION_OUTCOME;
    expect(outcome.state).toBe("idle");

    outcome = connectionReducer(outcome, { type: "REQUEST_MIC" });
    expect(outcome.state).toBe("requesting-mic");

    outcome = connectionReducer(outcome, { type: "MIC_GRANTED" });
    expect(outcome.state).toBe("connecting");

    outcome = connectionReducer(outcome, { type: "CONNECTED" });
    expect(outcome.state).toBe("connected");
  });

  it("transitions connecting -> failed on a transport error", () => {
    const connecting: ConnectionOutcome = { state: "connecting" };
    const outcome = connectionReducer(connecting, { type: "TRANSPORT_ERROR", message: "ICE failed" });
    expect(outcome.state).toBe("failed");
    expect(outcome.error).toBe("ICE failed");
  });

  it("transitions connecting -> rejected (distinct from connected/failed) on an offer rejection", () => {
    const connecting: ConnectionOutcome = { state: "connecting" };
    const outcome = connectionReducer(connecting, {
      type: "OFFER_REJECTED",
      rejection: { status: 401, error: "unauthorized" },
    });
    expect(outcome.state).toBe("rejected");
    expect(outcome.state).not.toBe("connected");
    expect(outcome.state).not.toBe("failed");
    expect(outcome.rejection).toEqual({ status: 401, error: "unauthorized" });
  });

  it("does not let a stray DISCONNECTED stomp on an already-rejected outcome", () => {
    const rejected: ConnectionOutcome = { state: "rejected", rejection: { status: 429 } };
    const outcome = connectionReducer(rejected, { type: "DISCONNECTED" });
    expect(outcome.state).toBe("rejected");
  });

  it("resets a connected session to idle on a clean DISCONNECTED", () => {
    const connected: ConnectionOutcome = { state: "connected" };
    const outcome = connectionReducer(connected, { type: "DISCONNECTED" });
    expect(outcome.state).toBe("idle");
  });
});

describe("buildConnectParams", () => {
  it("attaches the Bearer token as an Authorization header on /api/offer", () => {
    const params = buildConnectParams("abc123");
    expect(params.endpoint).toBe("/api/offer");
    expect(params.headers?.get("Authorization")).toBe("Bearer abc123");
  });

  it("omits the Authorization header when there is no token", () => {
    const params = buildConnectParams(null);
    expect(params.headers?.get("Authorization")).toBeNull();
  });
});
