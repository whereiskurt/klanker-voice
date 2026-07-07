import { afterEach, describe, expect, it } from "vitest";
import {
  markReturningUser, isReturningUser, clearReturningUser,
  markSilentTried, wasSilentTried,
} from "./returningStore";

afterEach(() => {
  localStorage.clear();
  sessionStorage.clear();
});

describe("returning-user breadcrumb", () => {
  it("is false before any sign-in", () => {
    expect(isReturningUser()).toBe(false);
  });
  it("is true after marking, false after clearing", () => {
    markReturningUser();
    expect(isReturningUser()).toBe(true);
    clearReturningUser();
    expect(isReturningUser()).toBe(false);
  });
  it("stores no token — only a boolean flag", () => {
    markReturningUser();
    expect(localStorage.getItem("kmv_returning")).toBe("1");
  });
});

describe("silent-tried per-load guard", () => {
  it("is false until marked, then true", () => {
    expect(wasSilentTried()).toBe(false);
    markSilentTried();
    expect(wasSilentTried()).toBe(true);
  });
  it("lives in sessionStorage (per tab/load), not localStorage", () => {
    markSilentTried();
    expect(sessionStorage.getItem("kmv_silent_tried")).toBe("1");
    expect(localStorage.getItem("kmv_silent_tried")).toBeNull();
  });
});
