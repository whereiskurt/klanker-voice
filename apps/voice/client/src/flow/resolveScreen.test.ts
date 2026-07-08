import { describe, expect, it } from "vitest";
import { resolveScreen, type ScreenInputs } from "./resolveScreen";

const base: ScreenInputs = {
  onCallbackRoute: false, hasSessionSummary: false, isAuthenticated: true, isNoAccessTier: false,
  outcomeState: "idle", retryExhausted: false, hasMicError: false, ceremonyDone: false, hasClient: false,
};

describe("resolveScreen precedence", () => {
  it("callback route wins over everything", () => {
    expect(resolveScreen({ ...base, onCallbackRoute: true, hasSessionSummary: true })).toBe("callback");
  });
  it("a session summary shows the ended screen", () => {
    expect(resolveScreen({ ...base, hasSessionSummary: true })).toBe("ended");
  });
  it("unauthenticated lands (forced-auth surface)", () => {
    expect(resolveScreen({ ...base, isAuthenticated: false })).toBe("land");
  });
  it("no-access tier gates before any conversation", () => {
    expect(resolveScreen({ ...base, isNoAccessTier: true })).toBe("no-access");
  });
  it("mic error interrupts", () => {
    expect(resolveScreen({ ...base, hasMicError: true })).toBe("mic-error");
  });
  it("a quota rejection shows the gate", () => {
    expect(resolveScreen({ ...base, outcomeState: "rejected" })).toBe("gate");
  });
  it("exhausted retry shows the udp wall", () => {
    expect(resolveScreen({ ...base, retryExhausted: true })).toBe("udp-wall");
  });
  it("connecting shows the ceremony", () => {
    expect(resolveScreen({ ...base, outcomeState: "connecting" })).toBe("ceremony");
  });
  it("connected but ceremony still running holds on the ceremony", () => {
    expect(resolveScreen({ ...base, outcomeState: "connected", ceremonyDone: false, hasClient: true })).toBe("ceremony");
  });
  it("connected AND ceremony done AND client present goes live", () => {
    expect(resolveScreen({ ...base, outcomeState: "connected", ceremonyDone: true, hasClient: true })).toBe("live");
  });
  it("a pre-connect transport failure (still retrying) holds on the ceremony", () => {
    expect(resolveScreen({ ...base, outcomeState: "failed" })).toBe("ceremony");
  });
  it("authenticated and idle is ready-to-start", () => {
    expect(resolveScreen(base)).toBe("ready");
  });
});
