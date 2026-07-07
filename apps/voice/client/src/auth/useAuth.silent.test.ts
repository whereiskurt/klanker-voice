import { afterEach, describe, expect, it, vi } from "vitest";
import { renderHook } from "@testing-library/react";
import { useAuth } from "./useAuth";
import * as navigateModule from "./navigate";
import { markReturningUser, markSilentTried } from "./returningStore";
import * as tokenStore from "./tokenStore";

// Note: this jsdom version does not permit `vi.spyOn(window.location, "assign")`
// (`TypeError: Cannot redefine property: assign`) — spying on the `navigate`
// helper module is the source plan's documented fallback (Task 3).
afterEach(() => { localStorage.clear(); sessionStorage.clear(); vi.restoreAllMocks(); });

describe("attemptSilentSso guard", () => {
  it("does nothing for a first-time visitor (no breadcrumb)", async () => {
    const assign = vi.spyOn(navigateModule, "navigate").mockImplementation(() => {});
    const { result } = renderHook(() => useAuth());
    await result.current.attemptSilentSso();
    expect(assign).not.toHaveBeenCalled();
  });
  it("does nothing if already tried this load", async () => {
    markReturningUser(); markSilentTried();
    const assign = vi.spyOn(navigateModule, "navigate").mockImplementation(() => {});
    const { result } = renderHook(() => useAuth());
    await result.current.attemptSilentSso();
    expect(assign).not.toHaveBeenCalled();
  });
  it("does nothing if already authenticated", async () => {
    markReturningUser();
    vi.spyOn(tokenStore, "isAuthenticated").mockReturnValue(true);
    const assign = vi.spyOn(navigateModule, "navigate").mockImplementation(() => {});
    const { result } = renderHook(() => useAuth());
    await result.current.attemptSilentSso();
    expect(assign).not.toHaveBeenCalled();
  });
});
