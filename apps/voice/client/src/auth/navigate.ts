/**
 * Top-level navigation helper (Workstream A, slick-start). Exists as its own
 * module purely so tests can `vi.spyOn` it — this jsdom version does not
 * permit redefining `window.location.assign` directly
 * (`TypeError: Cannot redefine property: assign`), the exact fallback the
 * source plan calls out (docs/superpowers/plans/2026-07-06-slick-start.md
 * Task 3). Always top-level, never an iframe — iOS Safari ITP first-party
 * cookie (T-05.2-01-T).
 */
export function navigate(url: string): void {
  window.location.assign(url);
}
