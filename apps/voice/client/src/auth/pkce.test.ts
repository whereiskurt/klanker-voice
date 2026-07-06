import { describe, expect, it } from "vitest";
import { codeChallenge, generateCodeVerifier, generateState } from "./pkce";

// RFC 7636 Appendix B known-answer vector (independently re-derived via
// Python hashlib/base64 during planning — see 05-03-SUMMARY.md).
const RFC7636_VERIFIER = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk";
const RFC7636_CHALLENGE = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM";

describe("generateCodeVerifier", () => {
  it("returns a base64url string within the RFC 7636 43-128 char range", () => {
    const verifier = generateCodeVerifier();
    expect(verifier.length).toBeGreaterThanOrEqual(43);
    expect(verifier.length).toBeLessThanOrEqual(128);
    expect(verifier).toMatch(/^[A-Za-z0-9\-._~]+$/);
  });

  it("returns a different verifier on each call", () => {
    expect(generateCodeVerifier()).not.toBe(generateCodeVerifier());
  });
});

describe("generateState", () => {
  it("returns a random base64url string", () => {
    const state = generateState();
    expect(state.length).toBeGreaterThan(0);
    expect(state).toMatch(/^[A-Za-z0-9\-._~]+$/);
  });

  it("returns a different state on each call", () => {
    expect(generateState()).not.toBe(generateState());
  });
});

describe("codeChallenge", () => {
  it("matches the RFC 7636 Appendix B S256 known-answer vector", async () => {
    await expect(codeChallenge(RFC7636_VERIFIER)).resolves.toBe(RFC7636_CHALLENGE);
  });

  it("is deterministic for the same verifier", async () => {
    const a = await codeChallenge("some-arbitrary-verifier-value-1234567890");
    const b = await codeChallenge("some-arbitrary-verifier-value-1234567890");
    expect(a).toBe(b);
  });
});
