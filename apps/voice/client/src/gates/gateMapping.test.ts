import { describe, expect, it } from "vitest";
import { gateAction, gateMapping } from "./gateMapping";

describe("gateMapping", () => {
  it("maps daily-limit to the daily-exhausted copy, not retryable", () => {
    const copy = gateMapping("daily-limit");
    expect(`${copy.heading} ${copy.body}`).toBe(
      "That's a wrap for today. You've used your daily minutes — come back tomorrow for more.",
    );
    expect(copy.retryable).toBe(false);
  });

  it("maps concurrency-limit to the over-concurrent copy, not retryable", () => {
    const copy = gateMapping("concurrency-limit");
    expect(`${copy.heading} ${copy.body}`).toBe(
      "You've got a conversation running already. End that one, then start again here.",
    );
    expect(copy.retryable).toBe(false);
  });

  it("maps site-paused to the killswitch copy, not retryable", () => {
    const copy = gateMapping("site-paused");
    expect(`${copy.heading} ${copy.body}`).toBe(
      "The demo's resting. It's over capacity for today — check back soon.",
    );
    expect(copy.retryable).toBe(false);
  });

  it("maps no-access to the D-13 no-access gate copy, not retryable", () => {
    const copy = gateMapping("no-access");
    expect(copy.heading).toBe("You're on the list — almost.");
    expect(copy.body).toBe(
      "This is an exclusive demo — Kurt needs to give you access. You'll need an access code to start a conversation.",
    );
    expect(copy.retryable).toBe(false);
  });

  it("maps at-capacity to a retryable, transient copy", () => {
    const copy = gateMapping("at-capacity");
    expect(copy.retryable).toBe(true);
    expect(copy.heading.length).toBeGreaterThan(0);
    expect(copy.body.length).toBeGreaterThan(0);
  });

  it("falls back to the generic provider-error copy for an unknown error_type, retryable", () => {
    const known = gateMapping("at-capacity");
    const unknown = gateMapping("something-new-the-server-invented");
    expect(unknown).toEqual(known);
    expect(unknown.retryable).toBe(true);
  });

  it("falls back to the generic provider-error copy for an undefined error_type", () => {
    const copy = gateMapping(undefined);
    expect(copy.retryable).toBe(true);
  });
});

describe("gateAction", () => {
  it("routes no-access to sign-out", () => {
    expect(gateAction("no-access")).toBe("sign-out");
  });

  it("routes the retryable at-capacity reject to retry", () => {
    expect(gateAction("at-capacity")).toBe("retry");
  });

  it("routes daily-limit / concurrency-limit / site-paused to dismiss", () => {
    expect(gateAction("daily-limit")).toBe("dismiss");
    expect(gateAction("concurrency-limit")).toBe("dismiss");
    expect(gateAction("site-paused")).toBe("dismiss");
  });

  it("routes an unknown error_type to retry (same bucket as at-capacity)", () => {
    expect(gateAction("something-new")).toBe("retry");
  });
});
