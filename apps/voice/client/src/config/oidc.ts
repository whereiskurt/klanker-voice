/**
 * Public OIDC client configuration for the voice SPA's authorization-code +
 * PKCE sign-in flow (CLNT-08, D-04). Every value here is PUBLIC — this is a
 * PKCE public client with NO client secret; PKCE (RFC 7636) plus the
 * `state` CSRF check are what secure it (T-05-03-I, T-05-03-S).
 *
 * Values are read from Vite's `import.meta.env` (VITE_-prefixed, inlined
 * into the built bundle at build time). Local dev: set them in
 * apps/voice/client/.env.local (see .env.example for the contract).
 * Production: baked in via apps/voice/Dockerfile's client-build stage ARGs.
 */

export interface OidcConfig {
  /** OIDC issuer base URL, e.g. https://auth.klankermaker.ai/use1/api/oidc */
  issuer: string;
  /** Public client_id registered on the issuer (no secret — PKCE public client) */
  clientId: string;
  /** Resource-indicator audience the issued access token must carry (matches auth.py) */
  audience: string;
  /** This SPA's own callback route, registered as a redirect_uri on the issuer */
  redirectUri: string;
}

function requireEnv(name: keyof ImportMetaEnv, value: string | undefined): string {
  if (!value) {
    throw new Error(
      `klanker-voice: missing required env var ${name} — see apps/voice/client/.env.example`,
    );
  }
  return value;
}

let cachedConfig: OidcConfig | null = null;

/**
 * Resolves (and memoizes) the runtime OIDC config from Vite's env. Lazy on
 * purpose: pure helpers below (authorizeEndpoint/tokenEndpoint) and
 * oidcClient.ts's buildAuthorizeUrl/exchangeCode take an `OidcConfig` as a
 * parameter instead of importing this directly, so they stay unit-testable
 * with a fixture config and never require real VITE_OIDC_* env vars to be
 * set just to import the module. Call this only where the real runtime
 * config is actually needed (useAuth.ts).
 */
export function getOidcConfig(): OidcConfig {
  if (!cachedConfig) {
    cachedConfig = {
      issuer: requireEnv("VITE_OIDC_ISSUER", import.meta.env.VITE_OIDC_ISSUER),
      clientId: requireEnv("VITE_OIDC_CLIENT_ID", import.meta.env.VITE_OIDC_CLIENT_ID),
      audience: requireEnv("VITE_OIDC_AUDIENCE", import.meta.env.VITE_OIDC_AUDIENCE),
      redirectUri: requireEnv("VITE_OIDC_REDIRECT_URI", import.meta.env.VITE_OIDC_REDIRECT_URI),
    };
  }
  return cachedConfig;
}

/** The OIDC authorize (authorization-code) endpoint for a given config. */
export function authorizeEndpoint(config: OidcConfig): string {
  return `${config.issuer}/auth`;
}

/** The OIDC token (code exchange) endpoint for a given config. */
export function tokenEndpoint(config: OidcConfig): string {
  return `${config.issuer}/token`;
}
