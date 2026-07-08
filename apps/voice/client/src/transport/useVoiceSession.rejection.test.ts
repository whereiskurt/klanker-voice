import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useVoiceSession } from "./useVoiceSession";
import { requestMic } from "../media/getMic";
import { createVoiceSession } from "./voiceSession";
import { playRandomGreeting } from "../greeting/greetingPlayer";
import type { ConnectionEvent, OfferRejection } from "./connectionState";

// Same module-mock pattern as useVoiceSession.greeting.test.ts: mock the mic
// gate, the voiceSession factory, and the greeting player so `start()` can be
// driven deterministically without a real getUserMedia/WebRTC/Audio stack.
vi.mock("../media/getMic", () => ({ requestMic: vi.fn() }));
vi.mock("./voiceSession", () => ({ createVoiceSession: vi.fn() }));
// `unlockAudioPlayback` must also be mocked -- `start()` imports it alongside
// `playRandomGreeting` (voice-flow-redesign Task 9); an undefined import
// would throw when `start()` calls it.
vi.mock("../greeting/greetingPlayer", () => ({ playRandomGreeting: vi.fn(), unlockAudioPlayback: vi.fn() }));

afterEach(() => { vi.clearAllMocks(); });

const CONCURRENCY_REJECTION: OfferRejection = {
  status: 403,
  error: "concurrency-limit",
  message: "You've got a conversation running already.",
};

/** Mocked mic grant. The `track.stop` spy lets a test assert the probe
 * stream was released (1f835b1) so the client's own `enableMic` capture is
 * the sole live one. */
function grantedMic(track: { stop: ReturnType<typeof vi.fn> } = { stop: vi.fn() }) {
  return {
    status: "granted" as const,
    stream: { getTracks: () => [track] } as unknown as MediaStream,
  };
}

describe("useVoiceSession — quota rejection is terminal (BUG 2)", () => {
  it("stops the probe-stream tracks so the client capture is the only live mic (1f835b1 regression lock)", async () => {
    const track = { stop: vi.fn() };
    vi.mocked(requestMic).mockResolvedValue(grantedMic(track));
    vi.mocked(playRandomGreeting).mockResolvedValue({ ended: Promise.resolve(), stop: vi.fn() });
    vi.mocked(createVoiceSession).mockImplementation(() => ({
      client: {} as never,
      connect: async () => {},
      disconnect: async () => {},
    }));

    const { result, unmount } = renderHook(() => useVoiceSession());
    await act(async () => { await result.current.start(); });

    // The probe stream requestMic() opened must be torn down; only
    // PipecatClient's own enableMic getUserMedia stays live.
    expect(track.stop).toHaveBeenCalledTimes(1);

    unmount();
  });

  it("keeps a concurrency-limit rejection terminal — a trailing transport error must NOT feed the retry controller or stomp the gate", async () => {
    vi.mocked(requestMic).mockResolvedValue(grantedMic());
    // Greeting still 'playing' (ended never resolves) so this also proves a
    // rejection is not gated behind the greeting handoff (that gates CONNECTED
    // only). stop() is a no-op spy.
    vi.mocked(playRandomGreeting).mockResolvedValue({ ended: new Promise<void>(() => {}), stop: vi.fn() });

    let capturedOnEvent!: (event: ConnectionEvent) => void;
    vi.mocked(createVoiceSession).mockImplementation((options) => {
      capturedOnEvent = options.onEvent;
      return {
        client: {} as never,
        // The exact production sequence: the fetch interceptor surfaces the
        // typed 403 reject, then voiceSession.disconnect()s the vendor client,
        // whose teardown / still-scheduled reconnection emits a stray
        // transport error. That error must be swallowed, not retried.
        connect: async () => {
          capturedOnEvent({ type: "OFFER_REJECTED", rejection: CONCURRENCY_REJECTION });
          capturedOnEvent({ type: "TRANSPORT_ERROR", message: "peer connection closed" });
        },
        disconnect: async () => {},
      };
    });

    const { result, unmount } = renderHook(() => useVoiceSession());
    await act(async () => { await result.current.start(); });

    // Outcome stays the clear typed gate, not stomped to "failed".
    expect(result.current.outcome.state).toBe("rejected");
    expect(result.current.outcome.rejection?.error).toBe("concurrency-limit");
    // The retry controller (transport/ICE failures only) must never be fed by
    // a quota rejection — no "Reconnecting… (attempt n of N)" spinner.
    expect(result.current.retryStatus.kind).toBe("idle");
    // And no fresh /api/offer re-attempt was scheduled/fired.
    expect(vi.mocked(createVoiceSession)).toHaveBeenCalledTimes(1);

    await act(async () => { await result.current.stop(); });
    unmount();
  });
});
