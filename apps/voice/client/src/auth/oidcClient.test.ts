import { afterEach, describe, expect, it, vi } from "vitest";
import type { OidcConfig } from "../config/oidc";
import { buildAuthorizeUrl, exchangeCode } from "./oidcClient";

const testConfig: OidcConfig = {
  issuer: "https://auth.klankermaker.ai/use1/api/oidc",
  clientId: "voice",
  audience: "https://voice.klankermaker.ai",
  redirectUri: "https://voice.klankermaker.ai/callback",
};

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("buildAuthorizeUrl", () => {
  it("targets the issuer's authorize endpoint with PKCE S256 + scope voice + audience + a random state", async () => {
    const url = new URL(
      await buildAuthorizeUrl(testConfig, { verifier: "a-verifier-value", state: "a-state-value" }),
    );

    expect(url.origin + url.pathname).toBe("https://auth.klankermaker.ai/use1/api/oidc/auth");
    expect(url.searchParams.get("response_type")).toBe("code");
    expect(url.searchParams.get("client_id")).toBe("voice");
    expect(url.searchParams.get("redirect_uri")).toBe("https://voice.klankermaker.ai/callback");
    expect(url.searchParams.get("scope")).toBe("voice");
    expect(url.searchParams.get("resource")).toBe("https://voice.klankermaker.ai");
    expect(url.searchParams.get("state")).toBe("a-state-value");
    expect(url.searchParams.get("code_challenge_method")).toBe("S256");
    // The challenge is the S256 digest of the verifier, not the verifier itself.
    expect(url.searchParams.get("code_challenge")).toBeTruthy();
    expect(url.searchParams.get("code_challenge")).not.toBe("a-verifier-value");
  });

  it("produces the RFC 7636 known-answer challenge for a known verifier", async () => {
    const url = new URL(
      await buildAuthorizeUrl(testConfig, {
        verifier: "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk",
        state: "irrelevant",
      }),
    );
    expect(url.searchParams.get("code_challenge")).toBe(
      "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM",
    );
  });
});

describe("buildAuthorizeUrl prompt", () => {
  it("omits prompt by default (interactive sign-in)", async () => {
    const url = await buildAuthorizeUrl(testConfig, { verifier: "v".repeat(43), state: "s" });
    expect(new URL(url).searchParams.has("prompt")).toBe(false);
  });
  it("adds prompt=none when requested (silent SSO)", async () => {
    const url = await buildAuthorizeUrl(testConfig, { verifier: "v".repeat(43), state: "s", prompt: "none" });
    expect(new URL(url).searchParams.get("prompt")).toBe("none");
  });
});

describe("exchangeCode", () => {
  it("POSTs code + code_verifier to the token endpoint with NO client secret", async () => {
    let capturedUrl: string | undefined;
    let capturedBody: URLSearchParams | undefined;

    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init: RequestInit) => {
        capturedUrl = url;
        capturedBody = new URLSearchParams(init.body as string);
        return new Response(
          JSON.stringify({ access_token: "the-access-token", expires_in: 3600 }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        );
      }),
    );

    const result = await exchangeCode(testConfig, { code: "the-code", verifier: "the-verifier" });

    expect(capturedUrl).toBe("https://auth.klankermaker.ai/use1/api/oidc/token");
    expect(capturedBody?.get("grant_type")).toBe("authorization_code");
    expect(capturedBody?.get("code")).toBe("the-code");
    expect(capturedBody?.get("code_verifier")).toBe("the-verifier");
    expect(capturedBody?.get("redirect_uri")).toBe(testConfig.redirectUri);
    expect(capturedBody?.get("client_id")).toBe("voice");
    expect(capturedBody?.has("client_secret")).toBe(false);
    expect(capturedBody?.has("secret")).toBe(false);

    expect(result.accessToken).toBe("the-access-token");
    expect(result.expiresIn).toBe(3600);
  });

  it("throws when the token endpoint responds with a non-OK status", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("bad request", { status: 400 })),
    );

    await expect(
      exchangeCode(testConfig, { code: "bad-code", verifier: "v" }),
    ).rejects.toThrow(/token exchange failed/);
  });
});
