import { describe, it, expect, vi, beforeAll } from "vitest";
// Deep-import the provider's internal weak-cache accessor, same technique as
// oidc-resource-token.test.ts (Plan 03-03) — lets us call `extraTokenClaims`
// directly off the live provider's `configuration` object without minting a
// real token through the HTTP/Koa layer.
// @ts-expect-error -- no type declarations for this internal deep import
import instance from "oidc-provider/lib/helpers/weak_cache.js";

/**
 * Plan 15-01 Task 1 (LEDG-01): proves the access token carries namespaced
 * `email` + `code` claims resolved from AuthProfile, alongside the existing
 * tier_id/group pair.
 *
 * Unlike oidc-resource-token.test.ts, this suite mocks `getAuthProfile`
 * (module mock, mock-then-import pattern per app/tel/__tests__/tel-route.test.ts
 * and app/api/login/__tests__/login-access-code.test.ts) instead of hitting
 * real dynamodb-local — extraTokenClaims is a pure function of whatever
 * AuthProfile.get() would have returned, so a mock is sufficient and keeps
 * this suite hermetic.
 */

vi.mock("@/entities/auth-profile", () => ({
  getAuthProfile: vi.fn(),
}));

let oidcMod: typeof import("../oidc");
let configMod: typeof import("../index");
let getAuthProfileMock: ReturnType<typeof vi.fn>;

beforeAll(async () => {
  process.env.OIDC_VOICE_CLIENT_ID = process.env.OIDC_VOICE_CLIENT_ID || "voice-test-client";
  process.env.OIDC_VOICE_SECRET = process.env.OIDC_VOICE_SECRET || "voice-test-secret";
  process.env.OIDC_COOKIE_KEYS = process.env.OIDC_COOKIE_KEYS || "test-cookie-key";
  process.env.AWS_REGION = process.env.AWS_REGION || "us-east-1";

  configMod = await import("../index");
  oidcMod = await import("../oidc");
  ({ getAuthProfile: getAuthProfileMock } = (await import(
    "@/entities/auth-profile"
  )) as unknown as { getAuthProfile: ReturnType<typeof vi.fn> });
});

function accessToken(accountId?: string) {
  return { kind: "AccessToken", accountId } as any;
}

describe("access-token email + code claims (LEDG-01)", () => {
  it("config.oidc.claimNames pins email and code to the namespaced URIs (the voice-service contract)", () => {
    const { config } = configMod;
    expect(config.oidc.claimNames.email).toBe("https://klankermaker.ai/email");
    expect(config.oidc.claimNames.code).toBe("https://klankermaker.ai/code");
  });

  it("resolves email + code claims from AuthProfile alongside the existing tier_id/group claims", async () => {
    getAuthProfileMock.mockResolvedValueOnce({
      activeTierId: "demo-tier",
      activeGroup: "conference",
      email: "kurt@example.com",
      activeCode: "kphdemo123",
    });

    const cfg = instance(oidcMod.oidc).configuration;
    const claims = await cfg.extraTokenClaims({} as any, accessToken("acct-1"));

    const { config } = configMod;
    expect(claims[config.oidc.claimNames.email]).toBe("kurt@example.com");
    expect(claims[config.oidc.claimNames.code]).toBe("kphdemo123");
    expect(claims[config.oidc.claimNames.tierId]).toBe("demo-tier");
    expect(claims[config.oidc.claimNames.group]).toBe("conference");
  });

  it("resolves to null (never undefined) when email/activeCode are unset on the profile, or the profile itself is missing", async () => {
    const { config } = configMod;

    getAuthProfileMock.mockResolvedValueOnce({ activeTierId: "demo-tier" });
    const cfg = instance(oidcMod.oidc).configuration;
    const claimsPartialProfile = await cfg.extraTokenClaims(
      {} as any,
      accessToken("acct-2")
    );
    expect(claimsPartialProfile[config.oidc.claimNames.email]).toBeNull();
    expect(claimsPartialProfile[config.oidc.claimNames.code]).toBeNull();
    expect(claimsPartialProfile[config.oidc.claimNames.email]).not.toBeUndefined();
    expect(claimsPartialProfile[config.oidc.claimNames.code]).not.toBeUndefined();

    getAuthProfileMock.mockResolvedValueOnce(undefined);
    const claimsNoProfile = await cfg.extraTokenClaims(
      {} as any,
      accessToken("acct-3")
    );
    expect(claimsNoProfile[config.oidc.claimNames.email]).toBeNull();
    expect(claimsNoProfile[config.oidc.claimNames.code]).toBeNull();
  });

  it("a non-AccessToken or a token with no accountId still returns {} without ever calling getAuthProfile (existing early-return preserved)", async () => {
    getAuthProfileMock.mockClear();
    const cfg = instance(oidcMod.oidc).configuration;

    const notAccessToken = await cfg.extraTokenClaims(
      {} as any,
      { kind: "ClientCredentials", accountId: "acct" } as any
    );
    expect(notAccessToken).toEqual({});

    const noAccountId = await cfg.extraTokenClaims({} as any, accessToken(undefined));
    expect(noAccountId).toEqual({});

    expect(getAuthProfileMock).not.toHaveBeenCalled();
  });
});
