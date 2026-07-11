/// <reference types="vite/client" />

// Public OIDC client config (CLNT-08, D-04) — no secret exists, this is a
// PKCE public client; see apps/voice/client/.env.example.
interface ImportMetaEnv {
  readonly VITE_OIDC_ISSUER: string;
  readonly VITE_OIDC_CLIENT_ID: string;
  readonly VITE_OIDC_AUDIENCE: string;
  readonly VITE_OIDC_REDIRECT_URI: string;
  // Build stamp (VERSION concept) — short git SHA + UTC build time, injected at
  // docker build time; absent (undefined) on local builds, hence the "|| dev"
  // fallbacks in version.ts.
  readonly VITE_APP_VERSION: string;
  readonly VITE_APP_BUILT_AT: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
