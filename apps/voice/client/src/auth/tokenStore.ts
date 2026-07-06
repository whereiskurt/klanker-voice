/**
 * In-memory-only access token store (D-04, T-05-03-I).
 *
 * The access token lives ONLY in this module-scope closure variable — never
 * in a persistent browser store (web storage APIs or a cookie). A page
 * refresh drops it (the whole module re-evaluates from scratch), which is
 * the intended XSS-safer trade-off: re-auth is one redirect, not a
 * persisted bearer credential.
 *
 * `getToken()` is the single Bearer source the live-connect plan (05-04)
 * sends to `POST /api/offer`.
 */

/** Namespaced claims from the Phase-3/4 pinned token contract (auth.py). */
const TIER_ID_CLAIM = "https://klankermaker.ai/tier_id";
const GROUP_CLAIM = "https://klankermaker.ai/group";

/** Matches auth.py's NO_ACCESS_TIER_ID default — the claim's absent-value fallback. */
const NO_ACCESS_TIER_ID = "no-access";

export interface TokenClaims {
  tierId: string;
  group: string | null;
}

interface StoredToken {
  accessToken: string;
  claims: TokenClaims;
}

let stored: StoredToken | null = null;

function base64UrlDecode(segment: string): string {
  const base64 = segment.replace(/-/g, "+").replace(/_/g, "/");
  const padded = base64.padEnd(base64.length + ((4 - (base64.length % 4)) % 4), "=");
  return atob(padded);
}

/**
 * Decodes the JWT payload segment (base64url JSON) WITHOUT verifying the
 * signature — this is a client-side UI routing hint only (T-05-03-E). The
 * server (auth.py) is the sole authority that validates the token.
 */
function decodeClaims(accessToken: string): TokenClaims {
  const parts = accessToken.split(".");
  if (parts.length !== 3) {
    throw new Error("klanker-voice: access token is not a JWT (unexpected format)");
  }
  const payload = JSON.parse(base64UrlDecode(parts[1])) as Record<string, unknown>;
  const tierId =
    typeof payload[TIER_ID_CLAIM] === "string" ? (payload[TIER_ID_CLAIM] as string) : NO_ACCESS_TIER_ID;
  const group = typeof payload[GROUP_CLAIM] === "string" ? (payload[GROUP_CLAIM] as string) : null;
  return { tierId, group };
}

/** Stores the access token in memory only, decoding its tier/group claims. */
export function setToken(accessToken: string): void {
  stored = { accessToken, claims: decodeClaims(accessToken) };
}

/** The current in-memory access token, or null if signed out / not yet signed in. */
export function getToken(): string | null {
  return stored?.accessToken ?? null;
}

/** The decoded tier_id/group claims of the current token, or null. */
export function getClaims(): TokenClaims | null {
  return stored?.claims ?? null;
}

/** True once a token is held in memory. */
export function isAuthenticated(): boolean {
  return stored !== null;
}

/** Drops the in-memory token (sign-out; also the refresh/reload default state). */
export function clearToken(): void {
  stored = null;
}
