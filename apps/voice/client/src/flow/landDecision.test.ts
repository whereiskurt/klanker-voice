import { describe, expect, it } from "vitest";
import { decideLandAction } from "./landDecision";

describe("decideLandAction (unauthenticated arrivals only)", () => {
  it("holds while an invisible silent SSO is still possible (returning, not yet tried)", () => {
    expect(decideLandAction({ isReturning: true, silentTried: false, interactiveTried: false })).toBe("holding");
  });

  it("force-redirects a first-timer with no silent path and no prior interactive try", () => {
    expect(decideLandAction({ isReturning: false, silentTried: false, interactiveTried: false })).toBe("redirect");
  });

  it("force-redirects a returning user whose silent SSO already failed this load", () => {
    expect(decideLandAction({ isReturning: false, silentTried: true, interactiveTried: false })).toBe("redirect");
  });

  it("shows a manual nudge once an interactive redirect was already attempted (bail-out guard)", () => {
    expect(decideLandAction({ isReturning: false, silentTried: true, interactiveTried: true })).toBe("nudge");
  });
});
