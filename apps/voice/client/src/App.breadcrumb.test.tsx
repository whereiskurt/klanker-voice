import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, screen } from "@testing-library/react";
import App from "./App";
import {
  isReturningUser,
  markReturningUser,
  markSilentTried,
  markInteractiveTried,
} from "./auth/returningStore";
import * as navigateModule from "./auth/navigate";
import { clearToken } from "./auth/tokenStore";

// Mirrors useAuth.ts's private PKCE sessionStorage keys (not exported) — the
// success-path test stashes them directly to simulate "we just came back
// from the issuer's authorize redirect with a matching state".
const PKCE_VERIFIER_KEY = "kmv_pkce_verifier";
const PKCE_STATE_KEY = "kmv_pkce_state";

/** A minimal-but-well-formed JWT (3 dot-separated segments, base64url JSON
 * payload) — tokenStore.setToken()/decodeClaims() only cares that it parses;
 * no signature verification happens client-side (T-05-03-E). */
function fixtureAccessToken(): string {
  const payload = { "https://klankermaker.ai/tier_id": "paid" };
  const base64 = btoa(JSON.stringify(payload))
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
  return `header.${base64}.signature`;
}

// This suite renders the REAL App -> Callback -> handleAuthenticated wiring
// (not a bare vi.fn() stub, unlike Callback.loginRequired.test.tsx) — it is
// the regression coverage for the reproduced bug documented in
// 05.2-VERIFICATION.md's gap #1: App.tsx's handleAuthenticated used to
// unconditionally call markReturningUser(), re-arming the breadcrumb in the
// same tick Callback.tsx's login_required branch cleared it.
vi.mock("./config/oidc", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./config/oidc")>();
  return {
    ...actual,
    getOidcConfig: () => ({
      issuer: "https://auth.klankermaker.ai/use1/api/oidc",
      clientId: "voice",
      audience: "https://voice.klankermaker.ai",
      redirectUri: "https://voice.klankermaker.ai/callback",
    }),
  };
});

vi.mock("./auth/oidcClient", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./auth/oidcClient")>();
  return {
    ...actual,
    exchangeCode: vi.fn(async () => ({ accessToken: fixtureAccessToken(), expiresIn: 3600 })),
  };
});

// App renders OrbCanvas on every non-callback stage transition, which uses
// useReducedMotion() (a11y/liveRegions.ts) -> window.matchMedia — not
// implemented by this jsdom version. Same stub as a11y.test.ts.
beforeEach(() => {
  vi.stubGlobal(
    "matchMedia",
    vi.fn().mockImplementation((query: string) => ({
      media: query,
      matches: false,
      addEventListener: () => {},
      removeEventListener: () => {},
    })),
  );
});

afterEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  clearToken(); // the code-exchange test above leaves a real in-memory token
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  window.history.replaceState({}, "", "/");
});

describe("App breadcrumb wiring (login_required must NOT re-arm it)", () => {
  it("stays cleared after a real login_required round-trip through App+Callback", async () => {
    // Simulate: this device signed in before (breadcrumb set), the silent
    // prompt=none bounce already ran this load (guard set so App's own
    // mount-effect attemptSilentSso() is a no-op — we're isolating the
    // Callback handoff, not re-testing the silent-SSO trigger itself), and
    // the issuer just redirected back with login_required (no live session).
    markReturningUser();
    markSilentTried();
    const navigateSpy = vi.spyOn(navigateModule, "navigate").mockImplementation(() => {});
    window.history.replaceState({}, "", "/callback?error=login_required");

    render(<App />);

    await act(async () => {
      await vi.waitFor(() => expect(window.location.pathname).toBe("/"));
    });

    expect(isReturningUser()).toBe(false);
    expect(navigateSpy).not.toHaveBeenCalled();
  });

  it("still marks the breadcrumb on a real successful code-exchange round-trip", async () => {
    // Guard against over-correction: the interactive success path must keep
    // marking the breadcrumb (Callback.tsx does this itself, right after
    // setToken — App no longer needs to, and must not, redo it).
    markSilentTried();
    sessionStorage.setItem(PKCE_VERIFIER_KEY, "the-verifier");
    sessionStorage.setItem(PKCE_STATE_KEY, "the-state");
    window.history.replaceState({}, "", "/callback?code=the-code&state=the-state");

    render(<App />);

    await act(async () => {
      await vi.waitFor(() => expect(isReturningUser()).toBe(true));
    });
  });

  // voice-flow-redesign §3.1 forced-auth land effect (Task 12): the
  // interactiveTried one-shot guard is the whole anti-loop mechanism -- once
  // a full interactive redirect has already auto-fired this session and the
  // device is STILL unauthenticated (bailed at auth / no live issuer
  // session), the app must land on the manual nudge, never fire another
  // automatic redirect. Uses the REAL useAuth hook (not mocked) so this
  // exercises the actual window.location.assign call beginSignIn makes --
  // happy-dom (unlike this repo's old jsdom) allows spying on
  // window.location.assign directly (verified: no "Cannot redefine
  // property" error here), so we can assert it was never invoked.
  it("does not auto-redirect again once interactiveTried is already set -- renders the manual nudge instead", async () => {
    markInteractiveTried();
    // Also mark silentTried so decideLandAction's first ("holding", a silent
    // SSO navigation about to happen) branch can never mask the guard this
    // test targets, regardless of isReturningUser() -- the interactiveTried
    // nudge branch is what's under test here, not the silent-SSO branch
    // (that's `landDecision.test.ts`'s job).
    markSilentTried();
    const assignSpy = vi.spyOn(window.location, "assign").mockImplementation(() => {});

    render(<App />);

    // Manual nudge, not the auto-bounce holding state.
    expect(await screen.findByText(/sign-in needed/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /sign in/i })).toBeInTheDocument();
    expect(screen.queryByText(/checking your session/i)).toBeNull();
    // No automatic navigation anywhere (neither the silent-SSO `navigate()`
    // path nor beginSignIn's direct window.location.assign).
    expect(assignSpy).not.toHaveBeenCalled();
  });
});
