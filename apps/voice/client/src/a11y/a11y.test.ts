import { act, createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { announceAssertive, announcePolite, useReducedMotion } from "./liveRegions";

// Same tiny hook-test harness as useCountdown.test.ts -- no
// react-hooks-testing-library in this project's devDependencies (Rule 3
// excludes package-manager installs from auto-fix).
function renderReducedMotion() {
  const container = document.createElement("div");
  let latest = false;
  let root!: Root;

  function Harness() {
    latest = useReducedMotion();
    return null;
  }

  act(() => {
    root = createRoot(container);
    root.render(createElement(Harness));
  });

  return {
    get value(): boolean {
      return latest;
    },
    unmount() {
      act(() => root.unmount());
    },
  };
}

describe("announcePolite / announceAssertive", () => {
  afterEach(() => {
    document.body.innerHTML = "";
  });

  it("writes connection-status/countdown text into a visually-hidden aria-live=polite region", () => {
    announcePolite("Reconnecting… (attempt 1 of 3)");
    const region = document.querySelector('[aria-live="polite"]');
    expect(region).not.toBeNull();
    expect(region?.textContent).toBe("Reconnecting… (attempt 1 of 3)");
    expect(region?.className).toContain("sr-only");
  });

  it("writes error text into a SEPARATE aria-live=assertive region, distinct from the polite one", () => {
    announcePolite("Connecting…");
    announceAssertive("Mic's blocked. Enable microphone access in your browser settings, then try again.");

    const polite = document.querySelector('[aria-live="polite"]');
    const assertive = document.querySelector('[aria-live="assertive"]');

    expect(assertive).not.toBeNull();
    expect(assertive).not.toBe(polite);
    expect(assertive?.getAttribute("role")).toBe("alert");
    expect(polite?.getAttribute("role")).toBe("status");
    expect(assertive?.textContent).toBe(
      "Mic's blocked. Enable microphone access in your browser settings, then try again.",
    );
    // The earlier polite announcement is untouched by the later assertive one.
    expect(polite?.textContent).toBe("Connecting…");
  });

  it("reuses the same shared DOM node across repeated calls (never one region per announcement)", () => {
    announcePolite("first");
    const first = document.querySelector('[aria-live="polite"]');
    announcePolite("second");
    const second = document.querySelector('[aria-live="polite"]');

    expect(second).toBe(first);
    expect(second?.textContent).toBe("second");
    expect(document.querySelectorAll('[aria-live="polite"]').length).toBe(1);
  });
});

describe("useReducedMotion", () => {
  let matches = false;
  const listeners = new Set<() => void>();

  beforeEach(() => {
    matches = false;
    listeners.clear();
    vi.stubGlobal(
      "matchMedia",
      vi.fn().mockImplementation((query: string) => ({
        media: query,
        get matches() {
          return matches;
        },
        addEventListener: (_event: string, cb: () => void) => listeners.add(cb),
        removeEventListener: (_event: string, cb: () => void) => listeners.delete(cb),
      })),
    );
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("reports the initial prefers-reduced-motion state at mount", () => {
    matches = true;
    const hook = renderReducedMotion();
    expect(hook.value).toBe(true);
    hook.unmount();
  });

  it("threads a live OS-level toggle without requiring a reload -- the orb/countdown react immediately", () => {
    const hook = renderReducedMotion();
    expect(hook.value).toBe(false);

    matches = true;
    act(() => {
      listeners.forEach((cb) => cb());
    });

    expect(hook.value).toBe(true);
    hook.unmount();
  });

  it("unsubscribes its change listener on unmount", () => {
    const hook = renderReducedMotion();
    expect(listeners.size).toBeGreaterThan(0);
    hook.unmount();
    expect(listeners.size).toBe(0);
  });
});
