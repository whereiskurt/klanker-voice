/// <reference types="vite/client" />

// Public OIDC client config (CLNT-08, D-04) — no secret exists, this is a
// PKCE public client; see apps/voice/client/.env.example.
interface ImportMetaEnv {
  readonly VITE_OIDC_ISSUER: string;
  readonly VITE_OIDC_CLIENT_ID: string;
  readonly VITE_OIDC_AUDIENCE: string;
  readonly VITE_OIDC_REDIRECT_URI: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
