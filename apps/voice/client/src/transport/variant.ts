/**
 * Pipeline-variant selection from the page path (full-duplex, 2026-07-10).
 *
 * One deployed site, several front doors: `/voice1` is the original half-duplex
 * concierge (still explicitly reachable), `/voice2` the full-duplex experience
 * -- now the DEFAULT for the root URL and any unknown path (260710-ixf). The
 * page derives its variant from the first path segment and sends it to
 * `POST /api/offer?variant=<name>`; the server (klanker_voice.variants)
 * re-validates against its own allowlist, so this is a UX convenience, never
 * the trust boundary.
 *
 * Keep this allowlist in sync with `_VARIANT_CONFIGS` in
 * `src/klanker_voice/variants.py`.
 */

export const DEFAULT_VARIANT = "voice2";

const KNOWN_VARIANTS = new Set<string>(["voice1", "voice2"]);

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

/** Per-variant orb tint (a CSS `hue-rotate` degree string) so v1/v2 read as
 * visually distinct. voice1 keeps the shipped teal (0deg); voice2 shifts to a
 * warmer hue. Tune the number here to taste. */
export function variantOrbHue(variant: string = currentVariant()): string {
  return variant === "voice2" ? "150deg" : "0deg";
}

/** Client-side display label for the current page's variant (derived from the
 * URL). Used as an immediate fallback for the server's `variant_label` so the
 * KPH(v1)/KPH(v2) tag ALWAYS shows, even if the /api/offer answer-peek didn't
 * land. */
export function variantDisplayLabel(variant: string = currentVariant()): string {
  return variant === "voice1" ? "KPH(v1)" : "KPH(v2)";
}
