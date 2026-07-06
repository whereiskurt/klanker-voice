import { afterEach, describe, expect, it, vi } from "vitest";
import { BACKOFF_SCHEDULE_MS, MAX_RETRY_ATTEMPTS, createRetryController } from "./retryPolicy";

describe("createRetryController", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("yields a bounded schedule of N=3 attempts with increasing backoff, then reports exhausted", () => {
    vi.useFakeTimers();
    const attemptConnect = vi.fn();
    const onRetrying = vi.fn();
    const onExhausted = vi.fn();
    const controller = createRetryController({ attemptConnect, onRetrying, onExhausted });

    expect(MAX_RETRY_ATTEMPTS).toBe(3);
    expect(BACKOFF_SCHEDULE_MS).toEqual([500, 1000, 2000]);

    // Attempt 1 of 3
    controller.reportFailure();
    expect(onRetrying).toHaveBeenNthCalledWith(1, 1, 3);
    vi.advanceTimersByTime(500);
    expect(attemptConnect).toHaveBeenCalledTimes(1);

    // Attempt 2 of 3 -- backoff increases
    controller.reportFailure();
    expect(onRetrying).toHaveBeenNthCalledWith(2, 2, 3);
    vi.advanceTimersByTime(1000);
    expect(attemptConnect).toHaveBeenCalledTimes(2);

    // Attempt 3 of 3 -- backoff increases again
    controller.reportFailure();
    expect(onRetrying).toHaveBeenNthCalledWith(3, 3, 3);
    vi.advanceTimersByTime(2000);
    expect(attemptConnect).toHaveBeenCalledTimes(3);

    // A 4th failure exhausts the bounded schedule -- no more retries scheduled.
    controller.reportFailure();
    expect(onExhausted).toHaveBeenCalledTimes(1);
    expect(onRetrying).toHaveBeenCalledTimes(3);
    vi.advanceTimersByTime(10_000);
    expect(attemptConnect).toHaveBeenCalledTimes(3);
  });

  it("stops retrying once a connection succeeds and resets the attempt counter for next time", () => {
    vi.useFakeTimers();
    const attemptConnect = vi.fn();
    const onRetrying = vi.fn();
    const onExhausted = vi.fn();
    const controller = createRetryController({ attemptConnect, onRetrying, onExhausted });

    // First attempt fails and is scheduled...
    controller.reportFailure();
    expect(onRetrying).toHaveBeenNthCalledWith(1, 1, 3);
    vi.advanceTimersByTime(500);
    expect(attemptConnect).toHaveBeenCalledTimes(1);

    // ...but the retried connection succeeds (attempt 2 overall) -- stop
    // retrying, never report exhausted for this schedule.
    controller.reportSuccess();
    expect(onExhausted).not.toHaveBeenCalled();
    vi.advanceTimersByTime(10_000);
    expect(attemptConnect).toHaveBeenCalledTimes(1); // no further scheduled retry fired

    // A LATER, unrelated failure starts a fresh bounded schedule from
    // attempt 1 again -- proves reportSuccess() reset the counter.
    controller.reportFailure();
    expect(onRetrying).toHaveBeenLastCalledWith(1, 3);
  });

  it("retryNow() resets the counter and attempts immediately, not on a timer", () => {
    vi.useFakeTimers();
    const attemptConnect = vi.fn();
    const onRetrying = vi.fn();
    const onExhausted = vi.fn();
    const controller = createRetryController({ attemptConnect, onRetrying, onExhausted });

    controller.reportFailure();
    controller.reportFailure();
    controller.reportFailure();
    controller.reportFailure(); // exhausted
    expect(onExhausted).toHaveBeenCalledTimes(1);

    controller.retryNow();
    expect(attemptConnect).toHaveBeenCalledTimes(1);

    // The schedule is fresh again after a manual retry.
    controller.reportFailure();
    expect(onRetrying).toHaveBeenLastCalledWith(1, 3);
  });
});
