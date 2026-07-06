/**
 * PKCE (RFC 7636) utilities for the voice SPA's authorization-code + PKCE
 * sign-in (CLNT-08, D-04). Built entirely on the Web Crypto API — no
 * jwt/oidc npm dependency is added (T-05-03-SC).
 */

function base64UrlEncode(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

/**
 * A cryptographically random PKCE code_verifier: 32 random bytes, base64url
 * encoded (43 chars, no padding) — within the RFC 7636 43-128 char range.
 */
export function generateCodeVerifier(): string {
  const bytes = new Uint8Array(32);
  crypto.getRandomValues(bytes);
  return base64UrlEncode(bytes);
}

/**
 * A cryptographically random `state` value, used to bind the authorize
 * redirect to its callback (CSRF protection, T-05-03-S) — validated by
 * useAuth/Callback against the value stashed in sessionStorage before the
 * redirect.
 */
export function generateState(): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return base64UrlEncode(bytes);
}

/**
 * The S256 code_challenge for a given code_verifier (RFC 7636 S4.2):
 * base64url(SHA-256(ascii(verifier))).
 */
export async function codeChallenge(verifier: string): Promise<string> {
  const encoded = new TextEncoder().encode(verifier);
  const digest = await crypto.subtle.digest("SHA-256", encoded);
  return base64UrlEncode(new Uint8Array(digest));
}
