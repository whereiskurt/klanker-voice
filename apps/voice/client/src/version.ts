/**
 * Build stamp — which commit built this bundle.
 *
 * Injected at `docker build` time (Dockerfile ARG -> ENV VITE_APP_* ->
 * import.meta.env, the same path the VITE_OIDC_* config uses). CI passes the
 * short git SHA + a UTC timestamp as --build-arg; a local `npm run build` or
 * the dev server passes nothing, so both fall back to "dev".
 *
 * NOTE: this reflects the CLIENT ASSET build (served from CloudFront/S3). It
 * tells you which UI bundle is live; the voice pipeline (ECS) deploys from the
 * same commit but can lag a build by a couple minutes.
 */
export const APP_VERSION: string = import.meta.env.VITE_APP_VERSION || "dev";
export const APP_BUILT_AT: string = import.meta.env.VITE_APP_BUILT_AT || "";

/** Full hover/tooltip text, e.g. "build 8048938 · 2026-07-10T20:45Z". */
export function versionTitle(): string {
  return APP_BUILT_AT ? `build ${APP_VERSION} · ${APP_BUILT_AT}` : `build ${APP_VERSION}`;
}
