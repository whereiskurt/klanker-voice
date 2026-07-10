/**
 * Pipeline-variant selection from the page path (full-duplex, 2026-07-10).
 *
 * One deployed site, several front doors: `/voice1` is the shipped half-duplex
 * concierge, `/voice2` the full-duplex experiment. The page derives its variant
 * from the first path segment and sends it to `POST /api/offer?variant=<name>`;
 * the server (klanker_voice.variants) re-validates against its own allowlist,
 * so this is a UX convenience, never the trust boundary.
 *
 * Keep this allowlist in sync with `_VARIANT_CONFIGS` in
 * `src/klanker_voice/variants.py`.
 */

export const DEFAULT_VARIANT = "voice1";

const KNOWN_VARIANTS = new Set<string>([DEFAULT_VARIANT, "voice2"]);

/** First path segment, lowercased, if it's a known variant; else the default. */
export function variantFromPath(pathname: string): string {
  const first = pathname.replace(/^\/+/, "").split("/")[0]?.toLowerCase() ?? "";
  return KNOWN_VARIANTS.has(first) ? first : DEFAULT_VARIANT;
}

/** The variant for the current page (SSR-safe: defaults off-window). */
export function currentVariant(): string {
  if (typeof window === "undefined") return DEFAULT_VARIANT;
  return variantFromPath(window.location.pathname);
}
