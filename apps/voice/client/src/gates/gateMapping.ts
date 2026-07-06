/**
 * Maps a `/api/offer` typed start-gate rejection's `error_type`
 * (`quota.GateResult`/`QuotaError.error_type` in `quota.py`) to specific
 * in-client gate copy (D-14, D-13). Pure -- no I/O, no React; `GateCard.tsx`
 * and `App.tsx` are the only consumers.
 */

export interface GateCopy {
  heading: string;
  body: string;
  /** `true` only for the transient `at-capacity` reject (D-14) -- the demo
   * task is momentarily full, offer again shortly. Every other typed
   * reject requires something to change (tomorrow's reset, ending another
   * session, the operator lifting the kill-switch, an access code) before
   * retrying would help, so `retryable` is `false`. */
  retryable: boolean;
}

// --- Verbatim UI-SPEC Copywriting Contract strings ---
//
// The contract lists daily-exhausted / over-concurrent / killswitch as a
// single sentence-pair string each. Splitting at the sentence boundary
// below reproduces the exact contract string when concatenated with a
// single space (see gateMapping.test.ts) while giving GateCard.tsx the same
// heading+body layout as NoAccessGate.tsx/UdpBlockedWall.tsx.

const DAILY_LIMIT: GateCopy = {
  heading: "That's a wrap for today.",
  body: "You've used your daily minutes — come back tomorrow for more.",
  retryable: false,
};

const CONCURRENCY_LIMIT: GateCopy = {
  heading: "You've got a conversation running already.",
  body: "End that one, then start again here.",
  retryable: false,
};

const SITE_PAUSED: GateCopy = {
  heading: "The demo's resting.",
  body: "It's over capacity for today — check back soon.",
  retryable: false,
};

/** The D-13 no-access gate copy (see `screens/NoAccessGate.tsx`), reused
 * verbatim so a mid-flight no-access reject (e.g. a tier changed between
 * `App.tsx`'s own pre-connect check and this connect attempt actually
 * landing) shows the same exclusive-adjacent message, not a raw error. */
const NO_ACCESS: GateCopy = {
  heading: "You're on the list — almost.",
  body: "This is an exclusive demo — Kurt needs to give you access. You'll need an access code to start a conversation.",
  retryable: false,
};

/**
 * NOT a UI-SPEC verbatim string -- the Copywriting Contract has no assigned
 * copy for the retryable `at-capacity` reject (only the SessionEnd screen's
 * "Generic provider-error end" row, a DIFFERENT screen for a session that
 * had already connected -- see `screens/SessionEnd.tsx` -- whose "the
 * session ended cleanly" framing would be factually wrong here, since
 * `at-capacity` fires before any session ever starts). 05-CONTEXT.md's
 * Claude's-Discretion section already delegates exact retry/gate copy
 * wording to Claude for the D-11/D-12 cases the contract didn't spell out;
 * this extends the same discretion to the one gate case the contract left
 * unassigned, in the same exclusive-adjacent, honest, never-dead-end tone.
 * Also the fallback for a genuinely unknown `error_type` (should not happen
 * given the 5 known server-side types, but the client must never surface a
 * raw error string to the user).
 */
const GENERIC_PROVIDER_ERROR: GateCopy = {
  heading: "This demo's popular right now.",
  body: "We're at capacity for a moment — try again shortly.",
  retryable: true,
};

const GATE_COPY_BY_ERROR_TYPE: Record<string, GateCopy> = {
  "daily-limit": DAILY_LIMIT,
  "concurrency-limit": CONCURRENCY_LIMIT,
  "site-paused": SITE_PAUSED,
  "no-access": NO_ACCESS,
  "at-capacity": GENERIC_PROVIDER_ERROR,
};

/** Maps a typed `error_type` to its gate copy (D-14). Unknown/undefined
 * types fall back to the same transient/retryable generic copy as
 * `at-capacity` -- never a raw error. */
export function gateMapping(errorType: string | undefined): GateCopy {
  if (!errorType) return GENERIC_PROVIDER_ERROR;
  return GATE_COPY_BY_ERROR_TYPE[errorType] ?? GENERIC_PROVIDER_ERROR;
}

export type GateAction = "retry" | "sign-out" | "dismiss";

/** The appropriate `GateCard` CTA for a given `error_type` (Task 2 action
 * spec): "Sign out" for `no-access` (D-04 reuse), "Try again" for the one
 * retryable transient reject, "Reconnect"/dismiss (back to attract) for
 * everything else -- nothing to retry until tomorrow/quota
 * resets/killswitch lifts. */
export function gateAction(errorType: string | undefined): GateAction {
  if (errorType === "no-access") return "sign-out";
  return gateMapping(errorType).retryable ? "retry" : "dismiss";
}
