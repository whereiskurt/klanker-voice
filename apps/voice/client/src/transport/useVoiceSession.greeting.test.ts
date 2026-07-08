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
// stack. `unlockAudioPlayback` must also be mocked -- `start()` imports it
// alongside `playRandomGreeting` (voice-flow-redesign Task 9), and an
// undefined import would throw when `start()` calls it.
vi.mock("../media/getMic", () => ({ requestMic: vi.fn() }));
vi.mock("./voiceSession", () => ({ createVoiceSession: vi.fn() }));
vi.mock("../greeting/greetingPlayer", () => ({ playRandomGreeting: vi.fn(), unlockAudioPlayback: vi.fn() }));

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
});
