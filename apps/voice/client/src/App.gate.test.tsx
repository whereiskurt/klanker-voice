import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import App from "./App";
import { setToken, clearToken } from "./auth/tokenStore";
import { requestMic } from "./media/getMic";
import { createVoiceSession } from "./transport/voiceSession";
import { playRandomGreeting } from "./greeting/greetingPlayer";
import type { ConnectionEvent, OfferRejection } from "./transport/connectionState";

// BUG 3: on a concurrency-limit 403, the REAL App -> useVoiceSession ->
// connectionReducer -> gateMapping path must land on the GateCard (the clear
// typed gate), never the UDP-blocked wall / reconnecting spinner. Drives the
// genuine App + hook + reducer together; only the leaf transport/mic/greeting
// side effects are mocked (same seams as useVoiceSession.rejection.test.ts).
vi.mock("./media/getMic", () => ({ requestMic: vi.fn() }));
vi.mock("./transport/voiceSession", () => ({ createVoiceSession: vi.fn() }));
// `unlockAudioPlayback`/`primeGreeting` must also be mocked --
// `useVoiceSession.start()` imports both alongside `playRandomGreeting`
// (voice-flow-redesign Task 9 / UX hardening); an undefined import would
// throw when `start()` calls them.
vi.mock("./greeting/greetingPlayer", () => ({
  playRandomGreeting: vi.fn(),
  unlockAudioPlayback: vi.fn(),
  primeGreeting: vi.fn(),
}));

const CONCURRENCY_REJECTION: OfferRejection = {
  status: 403,
  error: "concurrency-limit",
  message: "You've got a conversation running already.",
};

/** A minimal well-formed JWT carrying a real (non-"no-access") tier, so
 * useAuth reports authenticated and the ReadyToStart CTA routes into
 * voice.start() rather than the land/sign-in redirect. */
function authenticateAsPaidTier(): void {
  const payload = { "https://klankermaker.ai/tier_id": "paid" };
  const base64 = btoa(JSON.stringify(payload)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  setToken(`header.${base64}.signature`);
}

function grantedMic() {
  return { status: "granted" as const, stream: { getTracks: () => [] } as unknown as MediaStream };
}

beforeEach(() => {
  authenticateAsPaidTier();
  // OrbCanvas / liveRegions -> useReducedMotion -> matchMedia, not implemented
  // by this jsdom version (same stub as App.breadcrumb.test.tsx / a11y.test.ts).
  vi.stubGlobal(
    "matchMedia",
    vi.fn().mockImplementation((query: string) => ({
      media: query,
      matches: false,
      addEventListener: () => {},
      removeEventListener: () => {},
    })),
  );
  vi.mocked(requestMic).mockResolvedValue(grantedMic());
});

afterEach(() => {
  cleanup(); // no globals/auto-cleanup in this vitest config — unmount between tests
  clearToken();
  vi.unstubAllGlobals();
  vi.clearAllMocks();
  window.history.replaceState({}, "", "/");
});

describe("App — concurrency-limit renders the GateCard (BUG 3)", () => {
  it("shows the concurrency gate copy (not the UDP-blocked wall) even with the stray post-reject transport error", async () => {
    // Greeting resolved immediately here; the terminal-rejection guard is what
    // keeps the trailing transport error from stomping the gate.
    vi.mocked(playRandomGreeting).mockResolvedValue({ ended: Promise.resolve(), stop: vi.fn() });
    vi.mocked(createVoiceSession).mockImplementation((options) => ({
      client: {} as never,
      connect: async () => {
        const onEvent = options.onEvent as (event: ConnectionEvent) => void;
        onEvent({ type: "OFFER_REJECTED", rejection: CONCURRENCY_REJECTION });
        // The vendor client.disconnect() teardown noise (BUG 2 production seq).
        onEvent({ type: "TRANSPORT_ERROR", message: "peer connection closed" });
      },
      disconnect: async () => {},
    }));

    render(<App />);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /let's start talking/i }));
    });

    await waitFor(() =>
      expect(screen.getByText("You've got a conversation running already.")).toBeTruthy(),
    );
    expect(screen.getByText("End that one, then start again here.")).toBeTruthy();
    // Never the transport wall / retry spinner for a quota reject.
    expect(screen.queryByText("This network blocks the audio channel.")).toBeNull();
    expect(screen.getByRole("button", { name: "Reconnect" })).toBeTruthy();
  });

  it("renders the gate immediately even while the greeting clip is still playing (rejection is not deferred behind the greeting handoff)", async () => {
    // `ended` never resolves: only CONNECTED is held behind the greeting, so a
    // rejection must reach the GateCard without waiting for the clip to finish.
    vi.mocked(playRandomGreeting).mockResolvedValue({ ended: new Promise<void>(() => {}), stop: vi.fn() });
    vi.mocked(createVoiceSession).mockImplementation((options) => ({
      client: {} as never,
      connect: async () => {
        const onEvent = options.onEvent as (event: ConnectionEvent) => void;
        onEvent({ type: "OFFER_REJECTED", rejection: CONCURRENCY_REJECTION });
      },
      disconnect: async () => {},
    }));

    render(<App />);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /let's start talking/i }));
    });

    await waitFor(() =>
      expect(screen.getByText("You've got a conversation running already.")).toBeTruthy(),
    );
  });
});
