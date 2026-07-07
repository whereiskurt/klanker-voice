import { useCallback, useState } from "react";
import { getOidcConfig } from "../config/oidc";
import { navigate } from "./navigate";
import { buildAuthorizeUrl } from "./oidcClient";
import { generateCodeVerifier, generateState } from "./pkce";
import {
  isReturningUser, markSilentTried, wasSilentTried, markReturningUser, clearReturningUser,
} from "./returningStore";
import { clearToken, getClaims, isAuthenticated as tokenIsAuthenticated } from "./tokenStore";

/**
 * The PKCE verifier + state are the ONLY things allowed in sessionStorage,
 * and only across the redirect (D-04) — Callback consumes+clears them.
 * Never the access token itself.
 */
const VERIFIER_KEY = "kmv_pkce_verifier";
const STATE_KEY = "kmv_pkce_state";

export interface StashedPkce {
  verifier: string;
  state: string;
}

function stashPkce(verifier: string, state: string): void {
  sessionStorage.setItem(VERIFIER_KEY, verifier);
  sessionStorage.setItem(STATE_KEY, state);
}

/** Reads + clears the stashed PKCE verifier/state (Callback's one-time read). */
export function consumeStashedPkce(): StashedPkce | null {
  const verifier = sessionStorage.getItem(VERIFIER_KEY);
  const state = sessionStorage.getItem(STATE_KEY);
  sessionStorage.removeItem(VERIFIER_KEY);
  sessionStorage.removeItem(STATE_KEY);
  if (!verifier || !state) return null;
  return { verifier, state };
}

export interface AuthState {
  isAuthenticated: boolean;
  tierId: string | null;
  group: string | null;
}

function readAuthState(): AuthState {
  if (!tokenIsAuthenticated()) {
    return { isAuthenticated: false, tierId: null, group: null };
  }
  const claims = getClaims();
  return { isAuthenticated: true, tierId: claims?.tierId ?? null, group: claims?.group ?? null };
}

/**
 * The voice SPA's sign-in surface (CLNT-08, D-04). `beginSignIn()` starts
 * the full-page authorization-code+PKCE redirect; Callback.tsx does the
 * code exchange and then calls `refresh()` so components re-render against
 * the freshly in-memory token. `signOut()` is a single tap, no modal (low
 * consequence — the token is in-memory only; re-auth is one redirect).
 */
export function useAuth() {
  const [authState, setAuthState] = useState<AuthState>(() => readAuthState());

  const refresh = useCallback(() => {
    setAuthState(readAuthState());
  }, []);

  const beginSignIn = useCallback(async () => {
    const verifier = generateCodeVerifier();
    const state = generateState();
    stashPkce(verifier, state);
    const url = await buildAuthorizeUrl(getOidcConfig(), { verifier, state });
    window.location.assign(url);
  }, []);

  /**
   * Guarded silent SSO (Workstream A, slick-start, CLNT-08): a no-op unless
   * (returning user) AND (not authenticated) AND (not already tried this
   * load) — otherwise stashes fresh PKCE, marks the per-load guard, and does
   * a TOP-LEVEL navigation (never an iframe — iOS Safari ITP first-party
   * cookie, T-05.2-01-T) to the prompt=none authorize URL.
   */
  const attemptSilentSso = useCallback(async () => {
    if (tokenIsAuthenticated() || !isReturningUser() || wasSilentTried()) return;
    markSilentTried();
    const verifier = generateCodeVerifier();
    const state = generateState();
    stashPkce(verifier, state);
    const url = await buildAuthorizeUrl(getOidcConfig(), { verifier, state, prompt: "none" });
    navigate(url); // top-level — iOS-safe first-party cookie
  }, []);

  const signOut = useCallback(() => {
    clearToken();
    clearReturningUser();
    refresh();
  }, [refresh]);

  return {
    isAuthenticated: authState.isAuthenticated,
    tierId: authState.tierId,
    group: authState.group,
    beginSignIn,
    attemptSilentSso,
    markReturningUser,
    clearReturningUser,
    signOut,
    refresh,
  };
}
