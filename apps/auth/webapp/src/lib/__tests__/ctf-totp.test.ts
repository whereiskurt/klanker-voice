import { describe, it, expect } from "vitest";
import { computeTotp } from "../ctf-totp";

/**
 * Quick task 260715-oq0, Task 1: the zero-dep HMAC-SHA1 TOTP helper backing
 * the CTF phone-OTP announcement DID's /ctf/otp issuer.
 */
describe("computeTotp", () => {
  it("matches the RFC 6238 SHA1 test vector (proves HMAC-SHA1 + dynamic truncation)", () => {
    // RFC 6238 Appendix B: base32 secret "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
    // (ASCII "12345678901234567890"), Time=59s, X=30, T0=0, 8 digits -> "94287082".
    const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ";
    const { code } = computeTotp(secret, { period: 30, digits: 8, now: 59_000 });
    expect(code).toBe("94287082");
  });

  it("returns a 6-digit zero-padded code by default", () => {
    const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ";
    const { code } = computeTotp(secret, { now: 1_000_000 });
    expect(code).toMatch(/^\d{6}$/);
  });

  it("expiresIn is always within [1, period]", () => {
    const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ";
    // Sweep across a full 120s period at 1s ticks -- expiresIn must never
    // fall to 0 or exceed the period.
    for (let s = 0; s < 130; s += 7) {
      const { expiresIn } = computeTotp(secret, { period: 120, now: s * 1000 });
      expect(expiresIn).toBeGreaterThanOrEqual(1);
      expect(expiresIn).toBeLessThanOrEqual(120);
    }
  });

  it("is deterministic for a fixed secret + clock (same step -> same code)", () => {
    const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ";
    const a = computeTotp(secret, { period: 120, now: 5_000 });
    const b = computeTotp(secret, { period: 120, now: 5_500 });
    expect(a.code).toBe(b.code);
  });

  it("emits a different code once the step boundary is crossed", () => {
    const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ";
    const a = computeTotp(secret, { period: 120, now: 0 });
    const b = computeTotp(secret, { period: 120, now: 120_000 });
    expect(a.code).not.toBe(b.code);
  });

  it("tolerates lowercase and padded base32 secrets", () => {
    const upper = computeTotp("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", { now: 42_000 });
    const lower = computeTotp("gezdgnbvgy3tqojqgezdgnbvgy3tqojq", { now: 42_000 });
    const padded = computeTotp("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ======", { now: 42_000 });
    expect(lower.code).toBe(upper.code);
    expect(padded.code).toBe(upper.code);
  });
});
