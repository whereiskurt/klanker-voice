import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import * as jose from "jose";

/**
 * Bypass /join minting + resolution tests (2026-07-10-bypass-join-login-design).
 *
 * These are deliberately SELF-CONTAINED — no dynamodb-local required:
 *  - mintAnonToken() is verified against a locally-generated RS256 JWK Set
 *    injected via OIDC_JWKS (the same env var config/oidc.ts reads), then the
 *    JWT is independently verified with jose against the matching PUBLIC key —
 *    exactly what the voice service's PyJWKClient does against /jwks.
 *  - resolveBypassToken() is exercised by spying on the ElectroDB access
 *    pattern (AccessCode.query.byBypassToken) so its null/no-oracle contract is
 *    proven without a live table.
 */

let publicJwk: jose.JWK;
let mintAnonToken: typeof import("../bypass-token").mintAnonToken;
let config: typeof import("@/config").config;

beforeAll(async () => {
  const { publicKey, privateKey } = await jose.generateKeyPair("RS256", {
    extractable: true,
  });
  const privateJwk = await jose.exportJWK(privateKey);
  publicJwk = await jose.exportJWK(publicKey);
  privateJwk.kid = "bypass-test-kid";
  publicJwk.kid = "bypass-test-kid";
  privateJwk.use = "sig";
  publicJwk.use = "sig";
  process.env.OIDC_JWKS = JSON.stringify({ keys: [privateJwk] });

  ({ mintAnonToken } = await import("../bypass-token"));
  ({ config } = await import("@/config"));
});

describe("mintAnonToken", () => {
  it("mints a three-segment RS256 JWT whose claims match the oidc-provider contract", async () => {
    const { token, expiresIn } = await mintAnonToken({
      code: "kphdemo123",
      tierId: "kphdemo123-tier",
      group: "conference",
    });

    expect(expiresIn).toBe(3600);
    expect(token.split(".")).toHaveLength(3);

    const header = jose.decodeProtectedHeader(token);
    expect(header.alg).toBe("RS256");
    expect(header.kid).toBe("bypass-test-kid");

    // Independent offline verification against our own copy of the public key —
    // exactly what Phase 4's PyJWT + PyJWKClient does against /jwks.
    const localJwks = jose.createLocalJWKSet({ keys: [publicJwk] });
    const { payload } = await jose.jwtVerify(token, localJwks, {
      issuer: config.oidc.issuer,
      audience: config.oidc.voiceAudience,
    });

    expect(payload.iss).toBe(config.oidc.issuer);
    expect(payload.aud).toBe(config.oidc.voiceAudience);
    expect(typeof payload.sub).toBe("string");
    expect(payload.sub).toMatch(/^anon:kphdemo123:[0-9a-f-]{36}$/);
    expect(payload[config.oidc.claimNames.tierId]).toBe("kphdemo123-tier");
    expect(payload[config.oidc.claimNames.group]).toBe("conference");
    expect(typeof payload.exp).toBe("number");
    expect(typeof payload.iat).toBe("number");
    expect((payload.exp as number) - (payload.iat as number)).toBe(3600);
  });

  it("emits a fresh sub (unique uuid) on every call", async () => {
    const a = await mintAnonToken({ code: "x", tierId: "t", group: null });
    const b = await mintAnonToken({ code: "x", tierId: "t", group: null });
    expect(jose.decodeJwt(a.token).sub).not.toBe(jose.decodeJwt(b.token).sub);
  });

  it("serializes a null group claim (not omitted)", async () => {
    const { token } = await mintAnonToken({ code: "x", tierId: "t", group: null });
    const payload = jose.decodeJwt(token);
    expect(payload[config.oidc.claimNames.group]).toBeNull();
    expect(config.oidc.claimNames.group in payload).toBe(true);
  });

  it("throws when OIDC_JWKS is missing (route turns this into a uniform 404)", async () => {
    const saved = process.env.OIDC_JWKS;
    delete process.env.OIDC_JWKS;
    try {
      await expect(
        mintAnonToken({ code: "x", tierId: "t", group: null })
      ).rejects.toThrow();
    } finally {
      process.env.OIDC_JWKS = saved;
    }
  });
});

describe("resolveBypassToken (no-oracle contract)", () => {
  afterEach(() => vi.restoreAllMocks());

  async function withQueryResult(records: unknown[]) {
    const mod = await import("@/entities/access-code");
    vi.spyOn(mod.AccessCode.query, "byBypassToken").mockReturnValue({
      go: async () => ({ data: records }),
    } as never);
    return mod.resolveBypassToken;
  }

  it("returns null for a blank token without querying", async () => {
    const mod = await import("@/entities/access-code");
    const spy = vi.spyOn(mod.AccessCode.query, "byBypassToken");
    expect(await mod.resolveBypassToken("")).toBeNull();
    expect(await mod.resolveBypassToken(null)).toBeNull();
    expect(spy).not.toHaveBeenCalled();
  });

  it("returns null when no record matches (unknown token)", async () => {
    const resolve = await withQueryResult([]);
    expect(await resolve("nope")).toBeNull();
  });

  it("returns null when the code has bypass disabled", async () => {
    const resolve = await withQueryResult([
      { code: "demo", tierId: "demo-tier", group: "g", bypassEnabled: false },
    ]);
    expect(await resolve("tok")).toBeNull();
  });

  it("returns null when the code is expired", async () => {
    const resolve = await withQueryResult([
      {
        code: "demo",
        tierId: "demo-tier",
        group: "g",
        bypassEnabled: true,
        expiresAt: Date.now() - 1000,
      },
    ]);
    expect(await resolve("tok")).toBeNull();
  });

  it("resolves an enabled, unexpired code to its tier + group", async () => {
    const resolve = await withQueryResult([
      {
        code: "demo",
        tierId: "demo-tier",
        group: "conference",
        bypassEnabled: true,
        expiresAt: Date.now() + 60_000,
      },
    ]);
    expect(await resolve("tok")).toEqual({
      code: "demo",
      tierId: "demo-tier",
      group: "conference",
    });
  });

  it("normalizes a missing group to null", async () => {
    const resolve = await withQueryResult([
      { code: "demo", tierId: "demo-tier", bypassEnabled: true },
    ]);
    expect(await resolve("tok")).toEqual({
      code: "demo",
      tierId: "demo-tier",
      group: null,
    });
  });
});
