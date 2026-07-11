/**
 * Tiny external store for the server/pipeline version.
 *
 * The UI build SHA is known at bundle build time (version.ts), but the
 * server/pipeline SHA only arrives on a successful `/api/offer` answer — long
 * after `<VersionStamp/>` mounts at the React root, and from outside its
 * component tree. This module lets the connect flow publish that value and the
 * globally-mounted stamp subscribe to it via `useSyncExternalStore`.
 *
 * Sticky by design: NOT cleared on disconnect, so the last-known pipeline
 * version stays visible between sessions; the next successful offer refreshes it.
 */
type Listener = () => void;

let serverVersion: string | null = null;
const listeners = new Set<Listener>();

/** Publish the pipeline version from the `/api/offer` answer (no-op if same). */
export function setServerVersion(version: string | null): void {
  if (version === serverVersion) return;
  serverVersion = version;
  listeners.forEach((l) => l());
}

/** Current pipeline version, or null before the first successful offer. */
export function getServerVersion(): string | null {
  return serverVersion;
}

/** Subscribe (for useSyncExternalStore); returns an unsubscribe fn. */
export function subscribeServerVersion(listener: Listener): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}
