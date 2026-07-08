import { act, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

// Mock useAuth + useVoiceSession so we can drive the flow deterministically.
const auth = {
  isAuthenticated: false,
  tierId: null,
  group: null,
  beginSignIn: vi.fn(),
  attemptSilentSso: vi.fn().mockResolvedValue(undefined),
  markReturningUser: vi.fn(),
  clearReturningUser: vi.fn(),
  signOut: vi.fn(),
  refresh: vi.fn(),
};
const voice: any = {
  outcome: { state: "idle" },
  micError: null,
  client: null,
  sessionMaxSeconds: null,
  retryStatus: { kind: "idle" },
  sessionSummary: null,
  start: vi.fn(),
  stop: vi.fn(),
  endChat: vi.fn(),
  retryNow: vi.fn(),
  dismissGate: vi.fn(),
  dismissMicError: vi.fn(),
};
vi.mock("./auth/useAuth", () => ({ useAuth: () => auth }));
vi.mock("./transport/useVoiceSession", () => ({ useVoiceSession: () => voice }));

import App from "./App";
import { markReturningUser, markSilentTried } from "./auth/returningStore";

describe("App linear flow", () => {
  beforeEach(() => {
    sessionStorage.clear();
    localStorage.clear();
    vi.clearAllMocks();
    auth.isAuthenticated = false;
    voice.outcome = { state: "idle" };
    voice.sessionSummary = null;
  });

  it("unauthenticated first-timer is force-redirected to auth (no landing button)", async () => {
    render(<App />);
    // holding surface, and beginSignIn fired automatically
    expect(await screen.findByText(/checking your session/i)).toBeInTheDocument();
    expect(auth.beginSignIn).toHaveBeenCalled();
  });

  it("authenticated + idle shows ReadyToStart", async () => {
    auth.isAuthenticated = true;
    render(<App />);
    expect(screen.getByRole("button", { name: /let's start talking/i })).toBeInTheDocument();
    // Flush the land effect's microtasks (attemptSilentSso resolves, then the
    // auth.isAuthenticated guard is checked) — an authenticated arrival must
    // never fire the forced interactive redirect (split-brain auth regression).
    await act(async () => {});
    expect(auth.beginSignIn).not.toHaveBeenCalled();
    expect(sessionStorage.getItem("kmv_interactive_tried")).toBeNull();
  });

  it("connecting shows the ceremony, not Ready", () => {
    auth.isAuthenticated = true;
    voice.outcome = { state: "connecting" };
    render(<App />);
    expect(screen.getByTestId("ceremony-line")).toBeInTheDocument();
  });

  // Regression for the land-effect ordering bug: attemptSilentSso marks
  // silentTried (in sessionStorage) BEFORE it navigates, so re-deriving
  // decideLandAction AFTER the await with the post-mutation wasSilentTried()
  // collapses a returning user's intended "holding" into "redirect" and
  // fires a redundant beginSignIn()+markInteractiveTried() in the same tick.
  // App.tsx must snapshot wasSilentTried() BEFORE the await and use that
  // snapshot. This test drives the REAL side effect (the mocked
  // attemptSilentSso calls the real markSilentTried(), exactly like
  // useAuth's real implementation does) so it fails without the fix.
  it("returning user holds for silent SSO instead of also firing a redundant interactive redirect", async () => {
    markReturningUser();
    auth.attemptSilentSso.mockImplementation(async () => {
      markSilentTried();
    });
    auth.isAuthenticated = false;
    render(<App />);
    await act(async () => {});
    expect(auth.beginSignIn).not.toHaveBeenCalled();
    expect(sessionStorage.getItem("kmv_interactive_tried")).toBeNull();
  });
});
