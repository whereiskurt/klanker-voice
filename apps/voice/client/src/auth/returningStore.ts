/**
 * "Returning user" breadcrumb (Workstream A, slick-start). Holds NO token —
 * only a boolean that says this device has completed an interactive sign-in
 * before, so the app may attempt a silent top-level prompt=none SSO on load.
 * The per-load `silent_tried` guard prevents a redirect loop (load -> silent
 * -> /callback -> load -> silent ...).
 */
const RETURNING_KEY = "kmv_returning";
const SILENT_TRIED_KEY = "kmv_silent_tried";

export function markReturningUser(): void {
  try { localStorage.setItem(RETURNING_KEY, "1"); } catch { /* storage disabled: silent SSO just won't trigger */ }
}
export function isReturningUser(): boolean {
  try { return localStorage.getItem(RETURNING_KEY) === "1"; } catch { return false; }
}
export function clearReturningUser(): void {
  try { localStorage.removeItem(RETURNING_KEY); } catch { /* no-op */ }
}
export function markSilentTried(): void {
  try { sessionStorage.setItem(SILENT_TRIED_KEY, "1"); } catch { /* no-op */ }
}
export function wasSilentTried(): boolean {
  try { return sessionStorage.getItem(SILENT_TRIED_KEY) === "1"; } catch { return false; }
}

/**
 * One-shot guard for the forced-auth bounce (voice-flow-redesign §3.1). Set
 * when the app auto-fires a FULL interactive redirect. If the user returns
 * still unauthenticated (they bailed at auth), the app shows a manual "Sign
 * in" nudge instead of auto-redirecting again — no redirect storm.
 */
const INTERACTIVE_TRIED_KEY = "kmv_interactive_tried";

export function markInteractiveTried(): void {
  try { sessionStorage.setItem(INTERACTIVE_TRIED_KEY, "1"); } catch { /* no-op */ }
}
export function wasInteractiveTried(): boolean {
  try { return sessionStorage.getItem(INTERACTIVE_TRIED_KEY) === "1"; } catch { return false; }
}
