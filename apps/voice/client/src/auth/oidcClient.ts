import { authorizeEndpoint, tokenEndpoint, type OidcConfig } from "../config/oidc";
import { codeChallenge } from "./pkce";

/** Scope requested by the voice SPA — matches the Phase-4 token contract (auth.py). */
const SCOPE = "voice";

export interface AuthorizeParams {
  /** The PKCE code_verifier this browser generated for this sign-in attempt. */
  verifier: string;
  /** The CSRF `state` value this browser generated for this sign-in attempt. */
  state: string;
  /** When "none", requests a silent (no-UI) authorization — top-level only. */
  prompt?: "none";
}

/**
 * Builds the full-page redirect URL to the issuer's authorize endpoint:
 * authorization-code + PKCE (S256), scope `voice`, resource/audience pinned
 * to the voice API so the issued token's `aud` matches what auth.py
 * validates (T-05-03-T).
 */
export async function buildAuthorizeUrl(
  config: OidcConfig,
  { verifier, state, prompt }: AuthorizeParams,
): Promise<string> {
  const challenge = await codeChallenge(verifier);

  const url = new URL(authorizeEndpoint(config));
  url.searchParams.set("response_type", "code");
  url.searchParams.set("client_id", config.clientId);
  url.searchParams.set("redirect_uri", config.redirectUri);
  url.searchParams.set("scope", SCOPE);
  url.searchParams.set("resource", config.audience);
  url.searchParams.set("state", state);
  url.searchParams.set("code_challenge", challenge);
  url.searchParams.set("code_challenge_method", "S256");
  if (prompt) url.searchParams.set("prompt", prompt);

  return url.toString();
}

export interface ExchangeCodeParams {
  /** The authorization `code` returned on the callback query string. */
  code: string;
  /** The PKCE code_verifier generated before the redirect (never the challenge). */
  verifier: string;
}

export interface TokenResponse {
  accessToken: string;
  /** Seconds until expiry, as returned by the token endpoint (`expires_in`). */
  expiresIn: number;
}

/**
 * Exchanges an authorization code + PKCE verifier for an access token.
 * Sends NO client secret (public PKCE client, D-04, T-05-03-I) — only
 * grant_type, code, code_verifier, redirect_uri, and client_id.
 */
export async function exchangeCode(
  config: OidcConfig,
  { code, verifier }: ExchangeCodeParams,
): Promise<TokenResponse> {
  const body = new URLSearchParams({
    grant_type: "authorization_code",
    code,
    code_verifier: verifier,
    redirect_uri: config.redirectUri,
    client_id: config.clientId,
  });

  const response = await fetch(tokenEndpoint(config), {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body,
  });

  if (!response.ok) {
    throw new Error(`klanker-voice: token exchange failed (${response.status})`);
  }

  const data = (await response.json()) as { access_token: string; expires_in: number };
  return { accessToken: data.access_token, expiresIn: data.expires_in };
}
