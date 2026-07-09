import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useVoiceSession } from "./useVoiceSession";
import { requestMic } from "../media/getMic";
import { createVoiceSession } from "./voiceSession";
import { playRandomGreeting, primeGreeting } from "../greeting/greetingPlayer";
import type { ConnectionEvent } from "./connectionState";

// Same module-mock pattern as the existing transport tests: mock the mic
// gate, the voiceSession factory, and the greeting player so `start()` can
// be driven deterministically without a real getUserMedia/WebRTC/Audio
// stack. `unlockAudioPlayback` and `primeGreeting` must also be mocked --
// `start()` imports both alongside `playRandomGreeting` (voice-flow-redesign
// Task 9 / UX hardening), and an undefined import would throw when `start()`
// calls them.
vi.mock("../media/getMic", () => ({ requestMic: vi.fn() }));
vi.mock("./voiceSession", () => ({ createVoiceSession: vi.fn() }));
vi.mock("../greeting/greetingPlayer", () => ({
  playRandomGreeting: vi.fn(),
  unlockAudioPlayback: vi.fn(),
  primeGreeting: vi.fn(),
}));

afterEach(() => { vi.clearAllMocks(); });

/** Mocked mic grant with an empty-track stream (matches the real `stop()`
 * cleanup path in `start()`, which stops every track on the probe stream). */
function grantedMic() {
  return { status: "granted" as const, stream: { getTracks: () => [] } as unknown as MediaStream };
}

describe("useVoiceSession greeting decoupling (voice-flow-redesign Task 9)", () => {
  // The greeting is NO LONGER played inside start() (moved to Live mount), and
  // CONNECTED is dispatched as soon as the transport connects -- not held
  // behind any greeting-ended promise.
  it("dispatches connected immediately on transport connect (no greeting hold)", async () => {
    vi.mocked(requestMic).mockResolvedValue(grantedMic());

    let capturedOnEvent!: (event: ConnectionEvent) => void;
    vi.mocked(createVoiceSession).mockImplementation((options) => {
      capturedOnEvent = options.onEvent;
      return {
        client: {} as never,
        connect: async () => { capturedOnEvent({ type: "CONNECTED" }); },
        disconnect: async () => {},
      };
    });

    const { result, unmount } = renderHook(() => useVoiceSession());

    await act(async () => { await result.current.start(); });

    // No greeting-ended promise was ever created/awaited -- CONNECTED lands
    // synchronously once the transport reports it.
    await waitFor(() => expect(result.current.outcome.state).toBe("connected"));

    unmount();
  });

  it("start() does not itself play a greeting clip", async () => {
    vi.mocked(requestMic).mockResolvedValue(grantedMic());

    vi.mocked(createVoiceSession).mockImplementation((options) => ({
      client: {} as never,
      connect: async () => { options.onEvent({ type: "CONNECTED" }); },
      disconnect: async () => {},
    }));

    const { result, unmount } = renderHook(() => useVoiceSession());

    await act(async () => { await result.current.start(); });

    // The clip is played by Live on mount (Task 10), not by start() itself.
    expect(playRandomGreeting).not.toHaveBeenCalled();

    unmount();
  });

  // Test D (UX hardening): the real greeting Audio element must be armed
  // inside the SAME tap gesture as the mic grant/unlock, not deferred to
  // Live mount -- that's the whole fix for the intermittent-silent-greeting
  // defect (a later out-of-gesture play() on a fresh element isn't reliably
  // authorized by WebKit/Safari's autoplay policy).
  it("start() calls primeGreeting() within the gesture after a granted mic", async () => {
    vi.mocked(requestMic).mockResolvedValue(grantedMic());

    vi.mocked(createVoiceSession).mockImplementation((options) => ({
      client: {} as never,
      connect: async () => { options.onEvent({ type: "CONNECTED" }); },
      disconnect: async () => {},
    }));

    const { result, unmount } = renderHook(() => useVoiceSession());

    await act(async () => { await result.current.start(); });

    expect(primeGreeting).toHaveBeenCalledTimes(1);

    unmount();
  });
});

describe("useVoiceSession — endChat latches against trailing teardown noise (hardening)", () => {
  it("swallows a trailing TRANSPORT_ERROR after endChat() — no 'failed' stomp, no retry, exactly one clean summary", async () => {
    vi.mocked(requestMic).mockResolvedValue(grantedMic());

    let capturedOnEvent!: (event: ConnectionEvent) => void;
    vi.mocked(createVoiceSession).mockImplementation((options) => {
      capturedOnEvent = options.onEvent;
      return {
        client: {} as never,
        connect: async () => { capturedOnEvent({ type: "CONNECTED" }); },
        // Real vendor teardown: disconnect() itself resolves cleanly, but the
        // transport can still emit a trailing event asynchronously afterward
        // (simulated below by calling capturedOnEvent directly) -- exactly the
        // race endChat's latch exists to guard against.
        disconnect: async () => {},
      };
    });

    const { result, unmount } = renderHook(() => useVoiceSession());

    await act(async () => { await result.current.start(); });
    await waitFor(() => expect(result.current.outcome.state).toBe("connected"));

    await act(async () => { await result.current.endChat(); });

    // endChat's own clean summary + RESET have landed.
    expect(result.current.outcome.state).toBe("idle");
    expect(result.current.sessionSummary?.reason).toBe("clean");

    // The vendor transport's trailing teardown noise arrives AFTER endChat has
    // already resolved -- must be swallowed entirely by endedRef.
    act(() => { capturedOnEvent({ type: "TRANSPORT_ERROR" }); });

    // Not stomped to "failed" -- still the idle outcome endChat's RESET produced.
    expect(result.current.outcome.state).not.toBe("failed");
    expect(result.current.outcome.state).toBe("idle");
    // No background reconnect was armed.
    expect(result.current.retryStatus.kind).toBe("idle");
    expect(vi.mocked(createVoiceSession)).toHaveBeenCalledTimes(1);
    // The summary is still exactly the one clean summary endChat produced --
    // not overwritten/duplicated by the trailing event.
    expect(result.current.sessionSummary?.reason).toBe("clean");

    unmount();
  });

  // Regression: endChat() nulls connectedAtRef then awaits disconnect();
  // setClient(null) only runs after that await, so Live stays mounted and
  // "End chat" stays clickable during the window. A second tap used to
  // recompute elapsedSeconds from the now-null ref -> a spurious "0:00
  // spoken" summary, clobbering the real one. endChat must guard re-entry
  // via endedRef so a double-tap is a no-op.
  it("a double-tap of endChat() does not clobber the first summary with 0:00", async () => {
    vi.mocked(requestMic).mockResolvedValue(grantedMic());

    let capturedOnEvent!: (event: ConnectionEvent) => void;
    vi.mocked(createVoiceSession).mockImplementation((options) => {
      capturedOnEvent = options.onEvent;
      return {
        client: {} as never,
        connect: async () => { capturedOnEvent({ type: "CONNECTED" }); },
        disconnect: async () => {},
      };
    });

    const { result, unmount } = renderHook(() => useVoiceSession());

    await act(async () => { await result.current.start(); });
    await waitFor(() => expect(result.current.outcome.state).toBe("connected"));

    // Let real wall-clock time elapse so the first summary's elapsedSeconds
    // is meaningfully greater than 0 (distinguishable from a "reset to 0:00").
    await new Promise((resolve) => setTimeout(resolve, 20));

    await act(async () => { await result.current.endChat(); });
    const firstSummary = result.current.sessionSummary;
    expect(firstSummary).not.toBeNull();
    expect(firstSummary!.elapsedSeconds).toBeGreaterThan(0);

    // The double-tap: without the endedRef re-entry guard, this would
    // recompute elapsedSeconds from the now-null connectedAtRef -> 0.
    await act(async () => { await result.current.endChat(); });

    expect(result.current.sessionSummary).toBe(firstSummary); // no-op: same object, not recomputed
    expect(result.current.sessionSummary!.elapsedSeconds).toBe(firstSummary!.elapsedSeconds);
    expect(result.current.sessionSummary!.elapsedSeconds).toBeGreaterThan(0);

    unmount();
  });
});
