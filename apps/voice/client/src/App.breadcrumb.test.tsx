import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render } from "@testing-library/react";
import App from "./App";
import { isReturningUser, markReturningUser, markSilentTried } from "./auth/returningStore";
import * as navigateModule from "./auth/navigate";

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
});
