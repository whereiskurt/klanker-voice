/**
 * Centralized configuration for the klanker-voice auth webapp
 * All environment variables and derived config values in one place
 *
 * Ported from run.auth/webapp (D-08/D-09), trimmed to single-region,
 * Email-only, single voice OIDC client (D-08, D-09, D-10, D-11).
 */

import { fromNodeProviderChain } from "@aws-sdk/credential-providers";

// In prod (ECS Fargate) no static keys are supplied — resolve via an EXPLICIT
// provider chain so the ECS task role is used. Required because Next.js
// `output: 'standalone'` bundling drops the SDK's default (dynamic) provider
// chain; without an explicit import the task-role creds never resolve
// ("Resolved credential object is not valid"). Static AUTH_*_ID/SECRET still win locally.
const dynamoCreds =
  process.env.AUTH_DYNAMODB_ID && process.env.AUTH_DYNAMODB_SECRET
    ? { accessKeyId: process.env.AUTH_DYNAMODB_ID, secretAccessKey: process.env.AUTH_DYNAMODB_SECRET }
    : fromNodeProviderChain();
const sesCreds =
  process.env.AUTH_SES_ACCESS_KEY_ID && process.env.AUTH_SES_SECRET_ACCESS_KEY
    ? {
        accessKeyId: process.env.AUTH_SES_ACCESS_KEY_ID,
        secretAccessKey: process.env.AUTH_SES_SECRET_ACCESS_KEY,
        sessionToken: process.env.AUTH_SES_SESSION_TOKEN,
      }
    : fromNodeProviderChain();

const isDev = process.env.NODE_ENV !== "production";
const region = process.env.REGION_SHORT || "use1";

// Site domain from environment (defaults for local dev)
const siteDomain = process.env.SITE_DOMAIN || "klankermaker.ai";

// Local development ports (can be overridden via env vars)
const LOCAL_AUTH_PORT = process.env.LOCAL_AUTH_PORT || "3002";
const LOCAL_VOICE_PORT = process.env.LOCAL_VOICE_PORT || "7860";

export const config = {
  isDev,
  region,
  siteDomain,

  auth: {
    basePath: "/api/auth",
    jwtSecret: process.env.AUTH_JWT_SECRET?.split(","),
    cookieDomain: isDev ? "localhost" : process.env.AUTH_COOKIE_DOMAIN,
    secureCookies: !isDev,
    allowedEmails: process.env.AUTH_ALLOWED_EMAILS?.split(","),
  },

  urls: {
    /** Base URL for the auth server (browser-accessible) */
    baseUrl: process.env.AUTH_PUBLIC_URL || (isDev
      ? `http://localhost:${LOCAL_AUTH_PORT}`
      : `https://auth.${siteDomain}/${region}`),

    /** Login page path */
    loginPage: isDev ? "/login" : `/${region}/login`,

    /** Verify request page path */
    verifyPage: isDev ? "/login/verify" : `/${region}/login/verify`,

    /** Callback path for post-login redirects (voice client) */
    callbackPath: isDev
      ? `http://localhost:${LOCAL_VOICE_PORT}/`
      : `https://voice.${siteDomain}/`,
  },

  session: {
    maxAge: 15 * 24 * 60 * 60, // 15 days in seconds
    updateAge: 24 * 60 * 60, // 24 hours in seconds
  },

  oidc: {
    issuer: process.env.AUTH_PUBLIC_URL
      ? `${process.env.AUTH_PUBLIC_URL}/api/oidc`
      : (isDev
        ? `http://localhost:${LOCAL_AUTH_PORT}/api/oidc`
        : `https://auth.${siteDomain}/${region}/api/oidc`),

    routePrefix: isDev ? "/api/oidc" : `/${region}/api/oidc`,

    cookieKeys: process.env.OIDC_COOKIE_KEYS?.split(",") || ["oidc-dev-key-change-me"],

    // Single first-party relying party for this project (D-09): the voice
    // browser client at voice.{siteDomain}.
    clients: {
      voice: {
        clientId: process.env.OIDC_VOICE_CLIENT_ID!,
        clientSecret: process.env.OIDC_VOICE_SECRET!,
      },
    },

    ttl: {
      // Plan 03-03 / D-03: comfortably exceeds the longest tier (30 min) plus
      // a reconnect window. Also the pinned Phase-4 contract value (3600s) —
      // Phase 4's PyJWKClient/session logic assumes this exact TTL.
      accessToken: 60 * 60, // 1 hour (3600s, D-03 / pinned_phase4_contract)
      authorizationCode: 10 * 60, // 10 minutes
      idToken: 60 * 60, // 1 hour
      refreshToken: 14 * 24 * 60 * 60, // 14 days
      interaction: 60 * 60, // 1 hour
      session: 15 * 24 * 60 * 60, // 15 days
      grant: 14 * 24 * 60 * 60, // 14 days
    },

    /**
     * AUTH-02 / Resource-Indicator JWT access tokens (Plan 03-03).
     *
     * `voiceResource` is the Resource Indicator URI the voice client
     * authorizes against; `voiceAudience` is the `aud` claim value stamped
     * on the minted JWT access token. They are deliberately the SAME pinned
     * URI (see pinned_phase4_contract in 03-03-PLAN.md) — Phase 4's PyJWT
     * `audience=` check matches this string byte-for-byte.
     */
    voiceResource: "https://voice.klankermaker.ai",
    voiceAudience: "https://voice.klankermaker.ai",

    /**
     * Namespaced access-token claim names (D-01 thin token: tier_id + group
     * ONLY). Pinned verbatim in the Phase-4 contract — Phase 4's PyJWT reads
     * these two claim keys and no others.
     *
     * Phase 15 Plan 01 (LEDG-01) adds `email` + `code` — pinned byte-for-byte
     * against the voice service's `auth.py` EMAIL_CLAIM / CODE_CLAIM
     * constants (Plan 15-02), so the voice service can build a complete
     * ledger record from the validated token alone.
     */
    claimNames: {
      tierId: "https://klankermaker.ai/tier_id",
      group: "https://klankermaker.ai/group",
      email: "https://klankermaker.ai/email",
      code: "https://klankermaker.ai/code",
    },

    /**
     * Persistent, shared RS256 signing key set (Plan 03-03, T-03-13). Sourced
     * from the OIDC_JWKS env var (SSM SecureString /kmv/secrets/use1/oidc/jwks
     * in production — see from-aws.tmpl), NOT auto-generated per process, so
     * every task in the Fargate fleet signs with — and serves — the identical
     * JWKS across restarts. `undefined` when unset (local dev without the
     * secret configured) so oidc-provider falls back to its own dev-only
     * quick-start keys with a console warning, exactly as today.
     */
    jwks: process.env.OIDC_JWKS
      ? (JSON.parse(process.env.OIDC_JWKS) as { keys: Record<string, unknown>[] })
      : undefined,
  },

  dynamodb: {
    endpoint: process.env.AUTH_DYNAMODB_ENDPOINT,
    region: process.env.AUTH_DYNAMODB_REGION || process.env.AWS_REGION,
    tableName: process.env.AUTH_DYNAMODB_DBNAME,
    credentials: dynamoCreds,
  },

  ses: {
    region: process.env.AUTH_SES_REGION || "us-east-1",
    from: process.env.AUTH_SES_SMTP_FROM,
    credentials: sesCreds,
  },

  cookies: {
    session: { name: "sess_auth" },
    csrf: { name: "csrf_auth" },
    callback: { name: "callback_auth" },
  },
} as const;

export type Config = typeof config;
