import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useVoiceSession } from "./useVoiceSession";
import { requestMic } from "../media/getMic";
import { createVoiceSession } from "./voiceSession";
import { playRandomGreeting } from "../greeting/greetingPlayer";
import type { ConnectionEvent } from "./connectionState";

// Same module-mock pattern as the existing transport tests: mock the mic
// gate, the voiceSession factory, and the greeting player so `start()` can
// be driven deterministically without a real getUserMedia/WebRTC/Audio
// stack.
vi.mock("../media/getMic", () => ({ requestMic: vi.fn() }));
vi.mock("./voiceSession", () => ({ createVoiceSession: vi.fn() }));
vi.mock("../greeting/greetingPlayer", () => ({ playRandomGreeting: vi.fn() }));

afterEach(() => { vi.clearAllMocks(); });

/** Mocked mic grant with an empty-track stream (matches the real `stop()`
 * cleanup path in `start()`, which stops every track on the probe stream). */
function grantedMic() {
  return { status: "granted" as const, stream: { getTracks: () => [] } as unknown as MediaStream };
}

describe("useVoiceSession greeting handoff (B-05)", () => {
  it("withholds the connected outcome until the greeting clip ends", async () => {
    vi.mocked(requestMic).mockResolvedValue(grantedMic());
    let resolveGreetingEnded!: () => void;
    const ended = new Promise<void>((resolve) => { resolveGreetingEnded = resolve; });
    const stop = vi.fn();
    vi.mocked(playRandomGreeting).mockResolvedValue({ ended, stop });

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

    // The transport fired CONNECTED synchronously inside connect(), but the
    // greeting clip is still "playing" (its `ended` promise hasn't resolved
    // yet) -- the visible Live handoff must be held back.
    expect(result.current.outcome.state).not.toBe("connected");

    await act(async () => { resolveGreetingEnded(); });

    await waitFor(() => expect(result.current.outcome.state).toBe("connected"));

    unmount();
  });

  it("stops the greeting clip on a pre-connect transport error", async () => {
    vi.mocked(requestMic).mockResolvedValue(grantedMic());
    const stop = vi.fn();
    // Never resolves in this test -- the handoff should never depend on it
    // once a pre-connect transport error fires.
    vi.mocked(playRandomGreeting).mockResolvedValue({ ended: new Promise<void>(() => {}), stop });

    let capturedOnEvent!: (event: ConnectionEvent) => void;
    vi.mocked(createVoiceSession).mockImplementation((options) => {
      capturedOnEvent = options.onEvent;
      return {
        client: {} as never,
        connect: async () => { capturedOnEvent({ type: "TRANSPORT_ERROR" }); },
        disconnect: async () => {},
      };
    });

    const { result, unmount } = renderHook(() => useVoiceSession());

    await act(async () => { await result.current.start(); });

    await waitFor(() => expect(stop).toHaveBeenCalled());

    // Cancel the retry controller's scheduled backoff timer before the test
    // ends so it doesn't fire a background re-connect attempt afterwards.
    await act(async () => { await result.current.stop(); });
    unmount();
  });
});
