/**
 * Shared E.164 phone-number normalization (12-CONTEXT.md D-02, 12-RESEARCH.md
 * "E.164 Normalization" / Pitfall 3).
 *
 * This is the SINGLE normalization source used on BOTH the write path (the
 * `phone` attribute's `set` transform on the `AccessCode` entity, and the
 * `kv code phone --add` operator command) AND the lookup path (the private
 * `/tel/<e164>` route, and the 12-06 telephony controller's caller-ID
 * extraction). Defining it once and reusing it everywhere is what makes the
 * `byPhone` sparse GSI lookup reliable — a phone stored via one normalization
 * path and queried via a divergent one would silently 404 forever (Pitfall
 * 3's exact failure mode).
 *
 * Deliberately a PURE function with no import of entity/db code — it must be
 * safely importable from both the write side (access-code.ts) and the
 * lookup side (the /tel route) without creating a dependency cycle.
 */
export function normalizeE164(phone: string | null | undefined): string {
  const raw = String(phone ?? "").trim();
  if (!raw) {
    return "";
  }

  // Keep only digits and a leading '+' (drop spaces, dashes, parens, dots).
  let cleaned = raw.replace(/[^\d+]/g, "");
  if (!cleaned) {
    return "";
  }

  // Strip the leading '+' — re-added at the end once the digit string is
  // in its final canonical form.
  if (cleaned.startsWith("+")) {
    cleaned = cleaned.slice(1);
  }

  // Drop a leading trunk '0' (e.g. a domestic dialing prefix).
  cleaned = cleaned.replace(/^0+/, "");

  // North-American number shapes: a bare 10-digit local number, or an
  // 11-digit number already carrying the leading country code '1'. Either
  // way the canonical form is 11 digits starting with '1'.
  if (cleaned.length === 10) {
    cleaned = "1" + cleaned;
  }

  return "+" + cleaned;
}
