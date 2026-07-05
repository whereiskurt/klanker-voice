import Provider, { Configuration, errors, ClientMetadata } from "oidc-provider";
import { OIDCAdapter } from "../entities/oidc-adapter";
import { getAuthProfile } from "@/entities/auth-profile";
import { config } from "@/config";
import { makeLoadExistingGrant } from "./load-existing-grant";

// Local development ports (can be overridden via env vars)
const LOCAL_AUTH_PORT = process.env.LOCAL_AUTH_PORT || "3002";
const LOCAL_VOICE_PORT = process.env.LOCAL_VOICE_PORT || "7860";

// Site domain from config
const siteDomain = config.siteDomain;

/**
 * Registered OIDC clients (relying parties)
 *
 * klanker-voice is trimmed to a single first-party client (D-09): the voice
 * browser client at voice.{siteDomain}. run.auth's four other clients
 * (run.human, cmsStrapi, gpxStudio, flashTool, bib) are dropped entirely.
 *
 * Redirect URIs include both the /use1 basePath and non-prefixed variants,
 * since Auth.js doesn't include the Next.js basePath in callback URLs.
 * SECURITY: Localhost URIs are only included in development mode to prevent
 * local interception attacks.
 */
const clients: ClientMetadata[] = [
  {
    client_id: config.oidc.clients.voice.clientId,
    client_secret: config.oidc.clients.voice.clientSecret,
    redirect_uris: [
      `https://voice.${siteDomain}/api/auth/callback/voice.${siteDomain}`,
      `https://voice.${siteDomain}/use1/api/auth/callback/voice.${siteDomain}`,
      // Local development (only in dev mode)
      ...(config.isDev ? [
        `http://localhost:${LOCAL_VOICE_PORT}/api/auth/callback/voice.${siteDomain}`,
        `http://localhost:${LOCAL_AUTH_PORT}/api/auth/callback/voice.${siteDomain}`,
      ] : []),
    ],
    post_logout_redirect_uris: [
      `https://voice.${siteDomain}/`,
      `https://voice.${siteDomain}/use1`,
      // Local development (only in dev mode)
      ...(config.isDev ? [
        `http://localhost:${LOCAL_VOICE_PORT}/`,
      ] : []),
    ],
    grant_types: ["authorization_code", "refresh_token"],
    response_types: ["code"],
    scope: "openid profile email services",
    token_endpoint_auth_method: "client_secret_post",
  },
];

/**
 * OIDC Provider Configuration
 * @see https://github.com/panva/node-oidc-provider/blob/main/docs/README.md
 */
const configuration: Configuration = {
  // Use our custom DynamoDB adapter
  adapter: OIDCAdapter,

  // Static client registration
  clients,

  // Persistent, shared RS256 signing key set (Plan 03-03, T-03-13/T-03-14).
  // Sourced from OIDC_JWKS (SSM SecureString /kmv/secrets/use1/oidc/jwks in
  // production) so every task in the Fargate fleet signs with — and serves
  // at routes.jwks — the identical JWKS across restarts. When undefined
  // (OIDC_JWKS not set, e.g. local dev without the secret configured),
  // oidc-provider falls back to its own dev-only quick-start keys with a
  // console warning — same behavior as before this plan.
  jwks: config.oidc.jwks,

  // Route paths - must be full paths from host root (oidc-provider uses host + route for URLs)
  // In production: /{region}/api/oidc/auth, /{region}/api/oidc/token, etc.
  // In dev: /api/oidc/auth, /api/oidc/token, etc.
  routes: {
    authorization: `${config.oidc.routePrefix}/auth`,
    backchannel_authentication: `${config.oidc.routePrefix}/backchannel`,
    code_verification: `${config.oidc.routePrefix}/device`,
    device_authorization: `${config.oidc.routePrefix}/device/auth`,
    end_session: `${config.oidc.routePrefix}/session/end`,
    introspection: `${config.oidc.routePrefix}/token/introspection`,
    jwks: `${config.oidc.routePrefix}/jwks`,
    pushed_authorization_request: `${config.oidc.routePrefix}/request`,
    registration: `${config.oidc.routePrefix}/reg`,
    revocation: `${config.oidc.routePrefix}/token/revocation`,
    token: `${config.oidc.routePrefix}/token`,
    userinfo: `${config.oidc.routePrefix}/me`,
  },

  // Claims available for tokens
  // Note: By default claims go to userinfo. To include in ID token, we use conformIdTokenClaims: false
  claims: {
    openid: ["sub"],
    profile: ["name", "picture", "updated_at"],
    email: ["email", "email_verified"],
    services: ["services"],
  },

  // Include all requested claims in the ID token (not just userinfo)
  // This allows NextAuth to receive services directly
  conformIdTokenClaims: false,

  // Enabled features
  features: {
    // Disable dev interactions - we use Auth.js UI
    devInteractions: { enabled: false },

    // Enable refresh tokens
    revocation: { enabled: true },

    // Enable RP-initiated logout with auto-confirm (no confirmation page)
    rpInitiatedLogout: {
      enabled: true,
      logoutSource: async (ctx, form) => {
        // Auto-submit the logout form without user confirmation
        // The form contains the necessary CSRF token and logout parameters
        ctx.body = `<!DOCTYPE html>
<html>
<head><title>Logging out...</title></head>
<body onload="document.forms[0].submit()">
  ${form}
  <noscript>
    <p>JavaScript is required. Click the button to logout:</p>
    <button type="submit" form="op.logoutForm">Logout</button>
  </noscript>
</body>
</html>`;
      },
      postLogoutSuccessSource: async (ctx) => {
        // After OIDC logout succeeds, redirect to custom logout endpoint to clear Auth.js session
        // This avoids CSRF requirements of Auth.js's /api/auth/signout
        const paramValue = ctx.oidc.params?.post_logout_redirect_uri;
        const defaultRedirect = config.isDev
          ? `http://localhost:${LOCAL_VOICE_PORT}`
          : `https://voice.${siteDomain}`;
        const postLogoutRedirectUri = (typeof paramValue === 'string' ? paramValue : null) || defaultRedirect;

        // Redirect to our custom logout endpoint which clears sess_auth and redirects
        // URL must include region prefix for multi-region deployment
        const logoutPath = config.isDev ? "/api/logout" : `/${config.region}/api/logout`;
        const logoutUrl = `${logoutPath}?callbackUrl=${encodeURIComponent(postLogoutRedirectUri)}`;
        ctx.redirect(logoutUrl);
      },
    },

    /**
     * Resource-Indicator JWT access tokens (AUTH-02, D-01/D-02, Plan 03-03).
     * Turns the voice client's access token from an opaque string into a
     * signed RS256 JWT audienced to the voice resource. tier_id/group are
     * injected onto the ACCESS token via extraTokenClaims below — NOT here
     * and NOT via findAccount.claims (that feeds the ID token/userinfo).
     */
    resourceIndicators: {
      enabled: true,
      // No explicit `resource` param on the authorize/token request resolves
      // to the single voice resource, so the voice client always gets a
      // voice-audienced token without having to send `resource=` itself.
      // `oneOf` (present on code/refresh exchanges once a resource was
      // granted at authorization time) is honored when provided.
      defaultResource: async (ctx, client, oneOf) => {
        if (oneOf) return oneOf;
        return config.oidc.voiceResource;
      },
      // Reuse the resource granted at authorization time on token exchange,
      // so the browser client need not re-send `resource` at /token.
      useGrantedResource: async () => true,
      getResourceServerInfo: async (ctx, resourceIndicator, client) => ({
        scope: "voice",
        audience: config.oidc.voiceAudience,
        accessTokenTTL: config.oidc.ttl.accessToken,
        accessTokenFormat: "jwt",
        // Asymmetric only — never HS256. Offline PyJWT validation (Phase 4)
        // requires the public key published at routes.jwks; a symmetric key
        // can't be published (T-03-11).
        jwt: { sign: { alg: "RS256" } },
      }),
    },
    userinfo: { enabled: true },
    jwtUserinfo: { enabled: false },
  },

  // Cookie configuration
  cookies: {
    keys: config.oidc.cookieKeys,
    short: {
      signed: true,
      path: "/",
      httpOnly: true,
      sameSite: "lax" as const,
      ...(config.isDev ? {} : { secure: true, domain: `.${siteDomain}` }),
    },
    long: {
      signed: true,
      path: "/",
      httpOnly: true,
      sameSite: "lax" as const,
      ...(config.isDev ? {} : { secure: true, domain: `.${siteDomain}` }),
    },
  },

  // Token Time-To-Live configuration
  ttl: {
    AccessToken: config.oidc.ttl.accessToken,
    AuthorizationCode: config.oidc.ttl.authorizationCode,
    IdToken: config.oidc.ttl.idToken,
    RefreshToken: config.oidc.ttl.refreshToken,
    Interaction: config.oidc.ttl.interaction,
    Session: config.oidc.ttl.session,
    Grant: config.oidc.ttl.grant,
  },

  /**
   * Interaction URL - where to redirect for login/consent
   * This is the critical integration point with Auth.js
   * When oidc-provider needs user authentication, it redirects here
   */
  interactions: {
    url(ctx, interaction) {
      // Complete the interaction on the SERVER interaction-completion route (Pages API),
      // not the Auth.js login page. An already-authenticated user completes the interaction
      // with no HTML render. That route itself falls back to /{region}/login?oidc={uid} when
      // no sess_auth session is present. This path is derived from config.region and lives one
      // level OUTSIDE the provider routePrefix that owns /auth,/token (mirrors [uid].ts loginPath).
      const interactionBase = config.isDev
        ? "/api/oidc/interaction"
        : `/${config.region}/api/oidc/interaction`;
      return `${interactionBase}/${interaction.uid}`;
    },
  },

  /**
   * Auto-consent the registered first-party client allowlist so that a warm
   * `prompt=none` request succeeds without a consent interaction. Unknown clients
   * fall through to `undefined` (default flow). The allowlist is the single source
   * `config.oidc.clients` (not a second list); the grant-minting body mirrors the
   * canonical one in interaction/[uid].ts.
   */
  loadExistingGrant: makeLoadExistingGrant({
    firstPartyClientIds: Object.values(config.oidc.clients).map((c) => c.clientId),
    createGrant: async ({ accountId, clientId, scope }) => {
      const grant = new oidc.Grant({ accountId, clientId });
      if (scope) {
        grant.addOIDCScope(scope);
      }
      const grantId = await grant.save();
      return grantId;
    },
  }),

  /**
   * Find account by subject identifier
   * Called when oidc-provider needs user claims for tokens
   */
  async findAccount(ctx, sub) {
    // sub is the Auth.js user ID
    // Fetch the AuthProfile from DynamoDB for rich claims
    const profile = await getAuthProfile(sub);

    return {
      accountId: sub,
      async claims(use: string, scope: string, claims: Record<string, unknown>, rejected: string[]) {
        // sub is required by AccountClaims type
        const result: { sub: string; [key: string]: unknown } = { sub };

        if (profile) {
          // Profile claims (name, picture)
          if (scope.includes("profile")) {
            result.name = profile.name;
            result.picture = profile.picture;

            if (profile.updatedAt) {
              result.updated_at = Math.floor(profile.updatedAt / 1000);
            }
          }

          // Email claims
          if (scope.includes("email")) {
            result.email = profile.email;
            result.email_verified = profile.emailVerified ?? (!!result.email);
          }

          // Services claims - list of services the user can access
          if (scope.includes("services")) {
            result.services = profile.services || [];
          }
        }

        return result;
      },
    };
  },

  renderError: async (ctx, out, error) => {
    // Generate a request ID for correlation between user-facing error and server logs
    const requestId = crypto.randomUUID().slice(0, 8);

    // Log detailed error server-side with request ID for debugging
    console.error(`[OIDC Error ${requestId}]`, {
      error: out.error,
      error_description: out.error_description,
      details: error,
    });

    ctx.type = "html";
    // SECURITY: Return generic error message to users to prevent information disclosure
    // Detailed errors are logged server-side with request ID for debugging
    ctx.body = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Authentication Error - ${siteDomain}</title>
  <style>
    body {
      font-family: system-ui, -apple-system, sans-serif;
      background: #0a0a0a;
      color: #fff;
      display: flex;
      align-items: center;
      justify-content: center;
      min-height: 100vh;
      margin: 0;
    }
    .container {
      text-align: center;
      padding: 2rem;
      max-width: 400px;
    }
    h1 { color: #ef4444; margin-bottom: 1rem; }
    p { color: #a1a1aa; margin-bottom: 1.5rem; }
    .ref { color: #52525b; font-size: 0.75rem; margin-top: 1rem; }
    a {
      color: #3b82f6;
      text-decoration: none;
      padding: 0.75rem 1.5rem;
      border: 1px solid #3b82f6;
      border-radius: 0.5rem;
      display: inline-block;
    }
    a:hover { background: #3b82f6; color: #fff; }
  </style>
</head>
<body>
  <div class="container">
    <h1>Authentication Error</h1>
    <p>An error occurred during authentication. Please try again.</p>
    <a href="${config.urls.loginPage}">Try Again</a>
    <p class="ref">Reference: ${requestId}</p>
  </div>
</body>
</html>`;
  },

  pkce: {
    required: () => true,
  },

  rotateRefreshToken: true,

  // Extra access token claims — Plan 03-03 Task 3 wires tier_id/group here
  // now that resourceIndicators is enabled (D-01/D-02).
  extraTokenClaims: async (ctx, token) => {
    return {};
  },
};

// Create the OIDC provider instance
export const oidc = new Provider(config.oidc.issuer, configuration);

if (!config.isDev) {
  oidc.proxy = true;
}

// Debug event listeners to capture errors
// Note: oidc-provider only exposes certain typed events
oidc.on('grant.error', (ctx, error) => {
  console.error('[OIDC Event] grant.error:', error.message, error);
});

oidc.on('server_error', (ctx, error) => {
  console.error('[OIDC Event] server_error:', error.message, error);
});

// Re-export errors for use in route handlers
export { errors as OIDCErrors };

export function isSessionNotFound(error: unknown): boolean {
  return error instanceof errors.SessionNotFound;
}
