import { act, createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { formatMSS, levelForRemaining, useCountdown, type CountdownState } from "./useCountdown";

// No react-hooks-testing-library in this project's devDependencies (Rule 3
// excludes package-manager installs from auto-fix) -- this tiny harness
// mounts a one-hook component via react-dom/client + React's own `act`
// (both already ship with the react/react-dom deps this project has), the
// same pattern the rest of the client's hooks would need if tested directly.
function renderCountdown(initialMax: number, initialStartedAt: number | null) {
  const container = document.createElement("div");
  let latest!: CountdownState;
  let root!: Root;

  function Harness({ max, startedAt }: { max: number; startedAt: number | null }) {
    latest = useCountdown(max, startedAt);
    return null;
  }

  act(() => {
    root = createRoot(container);
    root.render(createElement(Harness, { max: initialMax, startedAt: initialStartedAt }));
  });

  return {
    get value(): CountdownState {
      return latest;
    },
    rerender(max: number, startedAt: number | null) {
      act(() => {
        root.render(createElement(Harness, { max, startedAt }));
      });
    },
    unmount() {
      act(() => root.unmount());
    },
  };
}

describe("useCountdown", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("reports the full cap and 'normal' before startedAt is known", () => {
    const countdown = renderCountdown(120, null);
    expect(countdown.value.remainingSeconds).toBe(120);
    expect(countdown.value.level).toBe("normal");
    countdown.unmount();
  });

  it("returns remaining seconds decreasing over time once started", () => {
    vi.useFakeTimers();
    const start = Date.now();
    const countdown = renderCountdown(60, start);

    expect(countdown.value.remainingSeconds).toBeCloseTo(60, 0);

    act(() => {
      vi.advanceTimersByTime(5000);
    });

    expect(countdown.value.remainingSeconds).toBeCloseTo(55, 0);
    expect(countdown.value.remainingSeconds).toBeLessThan(60);
    countdown.unmount();
  });

  it("never goes negative once the cap is exceeded", () => {
    vi.useFakeTimers();
    const start = Date.now();
    const countdown = renderCountdown(2, start);

    act(() => {
      vi.advanceTimersByTime(10_000);
    });

    expect(countdown.value.remainingSeconds).toBe(0);
    countdown.unmount();
  });

  it("escalates normal -> warning (<=30s) -> critical (<10s)", () => {
    vi.useFakeTimers();
    const start = Date.now();
    const countdown = renderCountdown(35, start);

    expect(countdown.value.level).toBe("normal");

    act(() => {
      vi.advanceTimersByTime(5_000); // 30s left -- warning boundary, inclusive
    });
    expect(countdown.value.level).toBe("warning");

    act(() => {
      vi.advanceTimersByTime(21_000); // 9s left -- critical
    });
    expect(countdown.value.level).toBe("critical");
    countdown.unmount();
  });
});

describe("levelForRemaining", () => {
  it("is normal above the warning threshold", () => {
    expect(levelForRemaining(45)).toBe("normal");
  });

  it("is warning at exactly the 30s boundary (synced to the spoken -30s warning)", () => {
    expect(levelForRemaining(30)).toBe("warning");
  });

  it("is still warning at exactly 10s (critical is strictly <10s)", () => {
    expect(levelForRemaining(10)).toBe("warning");
  });

  it("is critical just under 10s", () => {
    expect(levelForRemaining(9.9)).toBe("critical");
  });
});

describe("formatMSS", () => {
  it("renders m:ss for the countdown label", () => {
    expect(formatMSS(107)).toBe("1:47");
  });

  it("zero-pads seconds under 10", () => {
    expect(formatMSS(65)).toBe("1:05");
  });

  it("clamps negative input to 0:00", () => {
    expect(formatMSS(-5)).toBe("0:00");
  });
});
