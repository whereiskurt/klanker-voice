import { createHmac } from "node:crypto";

/**
 * Zero-dependency RFC 6238 TOTP helper for the CTF phone-OTP announcement DID
 * (quick task 260715-oq0, design doc
 * docs/superpowers/specs/2026-07-15-ctf-phone-otp-announcement-did-design.md).
 *
 * The auth webapp has NO otplib/otpauth dependency (only `jose` for JWT) --
 * HMAC-SHA1 is zero-dep via Node's built-in `crypto`, so this stays a tiny
 * local implementation rather than adding a package for one endpoint.
 *
 * Shared cross-repo TOTP contract (meshtk verifies independently, same
 * secret + params, out of scope here): algorithm HMAC-SHA1, T0=0, base32
 * secret. This module's caller (the /ctf/otp route) fixes digits=6,
 * period=120 -- TOTP params are constants, never request input, and this
 * issuer emits ONLY the current-step code (never a skew range; +-1 skew is a
 * verifier-only, out-of-scope meshtk concern).
 */

/** RFC 4648 base32 alphabet (no padding assumed on input). */
const BASE32_ALPHABET = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";

/**
 * Decode an RFC 4648 base32 string into raw bytes. Case-insensitive, tolerant
 * of `=` padding and stray whitespace (typical of a hand-copied SSM secret).
 * Throws on a character outside the base32 alphabet.
 */
export function base32Decode(input: string): Buffer {
  const cleaned = input.trim().toUpperCase().replace(/=+$/g, "").replace(/\s+/g, "");
  let bits = "";
  for (const char of cleaned) {
    const value = BASE32_ALPHABET.indexOf(char);
    if (value === -1) {
      throw new Error(`ctf-totp: invalid base32 character ${JSON.stringify(char)}`);
    }
    bits += value.toString(2).padStart(5, "0");
  }
  const bytes: number[] = [];
  for (let i = 0; i + 8 <= bits.length; i += 8) {
    bytes.push(parseInt(bits.slice(i, i + 8), 2));
  }
  return Buffer.from(bytes);
}

export interface ComputeTotpOptions {
  /** TOTP step period in seconds. Fixed constant for this issuer -- defaults to 120. */
  period?: number;
  /** Number of digits in the emitted code. Fixed constant for this issuer -- defaults to 6. */
  digits?: number;
  /** Injectable clock (ms since epoch) for deterministic tests. Defaults to Date.now(). */
  now?: number;
}

export interface TotpResult {
  /** Zero-padded numeric code, `digits` characters long. */
  code: string;
  /** Seconds remaining until the current step ends, always within [1, period]. */
  expiresIn: number;
}

/**
 * Compute the current-step RFC 6238 TOTP code for `secretBase32`.
 *
 * T0=0, counter = floor(unixSeconds / period), 8-byte big-endian counter,
 * HMAC-SHA1(key, counter), dynamic truncation (offset from the low nibble of
 * the last HMAC byte), masked to a 31-bit int, mod 10^digits, left-padded
 * with zeros.
 *
 * Emits ONLY the current step -- never a range. A verifier that wants +-1
 * step skew tolerance (meshtk, out of scope here) computes that itself from
 * the same secret + params.
 */
export function computeTotp(secretBase32: string, opts: ComputeTotpOptions = {}): TotpResult {
  const period = opts.period ?? 120;
  const digits = opts.digits ?? 6;
  const nowMs = opts.now ?? Date.now();
  const nowSeconds = Math.floor(nowMs / 1000);

  const counter = Math.floor(nowSeconds / period);
  const counterBuffer = Buffer.alloc(8);
  // 8-byte big-endian counter; counter fits in the low 32 bits for any
  // realistic clock/period, so only the low word is ever non-zero.
  counterBuffer.writeUInt32BE(0, 0);
  counterBuffer.writeUInt32BE(counter >>> 0, 4);

  const key = base32Decode(secretBase32);
  const hmac = createHmac("sha1", key).update(counterBuffer).digest();

  const offset = hmac[hmac.length - 1] & 0x0f;
  const truncated =
    ((hmac[offset] & 0x7f) << 24) |
    ((hmac[offset + 1] & 0xff) << 16) |
    ((hmac[offset + 2] & 0xff) << 8) |
    (hmac[offset + 3] & 0xff);

  const modulus = 10 ** digits;
  const code = (truncated % modulus).toString().padStart(digits, "0");

  const elapsedInStep = nowSeconds % period;
  const expiresIn = period - elapsedInStep;

  return { code, expiresIn };
}
