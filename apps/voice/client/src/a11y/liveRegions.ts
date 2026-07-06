import { useEffect, useState } from "react";

/**
 * Shared accessibility primitives for the whole client (05-07 mobile/a11y
 * hardening pass · UI-SPEC §Accessibility Baseline: "connection status and
 * countdown announced via aria-live=polite; errors assertive").
 *
 * Most screens (MicError, UdpBlockedWall, GateCard, ConnectingRetry,
 * SessionEnd) already satisfy this via `role="alert"`/`role="status"` on
 * their own root element -- both roles carry an IMPLICIT ARIA live-region
 * politeness ("alert" -> assertive, "status" -> polite), so they need no
 * change here. This module exists for the two cases that pattern doesn't
 * cover:
 *
 * 1. `Countdown.tsx` used to re-render a raw `aria-live="polite"` span every
 *    single second -- a real screen-reader-spam bug (announcing "59 seconds
 *    remaining", "58 seconds remaining", ... every tick). `announcePolite`/
 *    `announceAssertive` back a single shared, visually-hidden live region
 *    so callers can announce at meaningful BOUNDARIES (e.g. the 30s warning
 *    and 10s critical crossings) instead of every tick.
 * 2. `useReducedMotion` is a REACTIVE `prefers-reduced-motion` subscription
 *    (unlike `OrbCanvas`'s old one-shot mount-time check) so the conference-
 *    floor checkpoint's "toggle iOS Reduce Motion mid-session" step takes
 *    effect immediately, no reload required -- threaded into the orb
 *    (`OrbCanvas`) and used by `Countdown` to skip its own boundary
 *    announcements when the visual escalation pulse is already suppressed.
 */

const POLITE_REGION_ID = "kv-live-region-polite";
const ASSERTIVE_REGION_ID = "kv-live-region-assertive";

function getOrCreateRegion(id: string, politeness: "polite" | "assertive"): HTMLElement {
  let el = document.getElementById(id);
  if (!el) {
    el = document.createElement("div");
    el.id = id;
    el.setAttribute("aria-live", politeness);
    // Implicit-role belt-and-suspenders: makes intent explicit even though
    // aria-live alone is sufficient for every current screen reader.
    el.setAttribute("role", politeness === "assertive" ? "alert" : "status");
    el.className = "sr-only";
    document.body.appendChild(el);
  }
  return el;
}

/** Announces connection-status / countdown-boundary text via a shared,
 * visually-hidden `aria-live="polite"` region. */
export function announcePolite(message: string): void {
  getOrCreateRegion(POLITE_REGION_ID, "polite").textContent = message;
}

/** Announces error text via a shared, visually-hidden `aria-live="assertive"`
 * region -- separate DOM node from the polite one so an in-flight polite
 * announcement never gets overwritten by (or delays) an error. */
export function announceAssertive(message: string): void {
  getOrCreateRegion(ASSERTIVE_REGION_ID, "assertive").textContent = message;
}

/** Ensures both regions exist in the DOM as early as possible (called once
 * from `App.tsx`) so the very first announcement isn't delayed by lazy
 * node creation. Safe to call repeatedly -- idempotent. */
export function ensureLiveRegions(): void {
  getOrCreateRegion(POLITE_REGION_ID, "polite");
  getOrCreateRegion(ASSERTIVE_REGION_ID, "assertive");
}

const REDUCED_MOTION_QUERY = "(prefers-reduced-motion: reduce)";

/**
 * Reactive `prefers-reduced-motion` state -- subscribes to the media
 * query's `change` event (unlike a one-shot mount-time `matchMedia(...)
 * .matches` read) so a mid-session OS-level toggle takes effect
 * immediately. Consumed by `OrbCanvas` (swap to the calm 2D fallback) and
 * `Countdown` (skip the escalation-pulse boundary announcement when the
 * pulse itself is already CSS-suppressed).
 */
export function useReducedMotion(): boolean {
  const [reduced, setReduced] = useState<boolean>(() => window.matchMedia(REDUCED_MOTION_QUERY).matches);

  useEffect(() => {
    const mql = window.matchMedia(REDUCED_MOTION_QUERY);
    const onChange = () => setReduced(mql.matches);
    mql.addEventListener("change", onChange);
    return () => mql.removeEventListener("change", onChange);
  }, []);

  return reduced;
}
