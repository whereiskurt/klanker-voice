/**
 * Pure decision for what an UNAUTHENTICATED arrival should do on the voice
 * SPA (voice-flow-redesign §3.1). Callers only invoke this when the user is
 * not authenticated. The invisible silent SSO attempt (useAuth.attemptSilentSso)
 * is fired by App FIRST; this decides what happens once silent SSO is no
 * longer a live option.
 */
export interface LandInputs {
  /** localStorage breadcrumb: this device has interactively signed in before. */
  isReturning: boolean;
  /** sessionStorage: a silent prompt=none attempt already ran this load. */
  silentTried: boolean;
  /** sessionStorage: a full interactive redirect was already auto-fired this session. */
  interactiveTried: boolean;
}

export type LandAction = "holding" | "redirect" | "nudge";

export function decideLandAction({ isReturning, silentTried, interactiveTried }: LandInputs): LandAction {
  // A returning user who has not yet attempted silent SSO this load: the
  // invisible prompt=none navigation is about to happen — just hold.
  if (isReturning && !silentTried) return "holding";
  // Already bounced through a full interactive redirect and came back
  // unauthenticated (bailed at auth): stop auto-bouncing, offer a manual button.
  if (interactiveTried) return "nudge";
  // Otherwise force the interactive redirect.
  return "redirect";
}
