import { describe, it, expect, beforeAll } from "vitest";
import * as jose from "jose";
// Deep-import the provider's internal weak-cache accessor. oidc-provider ships
// no "exports" map (package.json exports: null), so any lib/ path is reachable.
// This is the SAME WeakMap module instance the provider itself uses, so
// `instance(oidc)` here returns the live configuration/keystore the provider
// was actually constructed with — not a re-derived copy.
// @ts-expect-error -- no type declarations for this internal deep import
import instance from "oidc-provider/lib/helpers/weak_cache.js";

/**
 * Plan 03-03 Task 1 (RED): proves the AUTH-02 access-token contract by
 * minting a real voice-resource access token through the SAME code paths
 * the live /token endpoint uses (features.resourceIndicators.getResourceServerInfo
 * + extraTokenClaims), without going through the HTTP/Koa layer or touching
 * DynamoDB (JWT-format AccessToken.save() never calls adapter.upsert — see
 * node_modules/oidc-provider/lib/models/formats/jwt.js).
 *
 * A fixed, test-only RS256 JWK Set is generated once and injected via
 * OIDC_JWKS before `../oidc` (and its `../index` config) are imported, so the
 * provider signs with a known keypair we can independently verify against.
 */

let oidcMod: typeof import("../oidc");
let configMod: typeof import("../index");
let authProfileMod: typeof import("../../entities/auth-profile");
let publicJwk: jose.JWK;

function unique(label: string): string {
  return `${label}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

beforeAll(async () => {
  // Real dynamodb-local, same convention as login-intent-bridge.test.ts /
  // access-code-resolution.test.ts (Plan 03-02) — AuthProfile reads are real,
  // not mocked.
  process.env.AUTH_ELECTRO_ENDPOINT = "http://localhost:8888";
  process.env.AUTH_ELECTRO_DBNAME = "kmv-auth-electro";
  process.env.AUTH_ELECTRO_ID = "local";
  process.env.AUTH_ELECTRO_SECRET = "local";
  process.env.AUTH_DYNAMODB_ENDPOINT = process.env.AUTH_DYNAMODB_ENDPOINT || "http://localhost:8888";
  process.env.AUTH_DYNAMODB_DBNAME = process.env.AUTH_DYNAMODB_DBNAME || "kmv-auth-authjs";
  process.env.AUTH_DYNAMODB_ID = process.env.AUTH_DYNAMODB_ID || "local";
  process.env.AUTH_DYNAMODB_SECRET = process.env.AUTH_DYNAMODB_SECRET || "local";
  process.env.AWS_REGION = process.env.AWS_REGION || "us-east-1";
  process.env.OIDC_VOICE_CLIENT_ID = process.env.OIDC_VOICE_CLIENT_ID || "voice-test-client";
  process.env.OIDC_VOICE_SECRET = process.env.OIDC_VOICE_SECRET || "voice-test-secret";
  process.env.OIDC_COOKIE_KEYS = process.env.OIDC_COOKIE_KEYS || "test-cookie-key";

  // Fixed test JWK Set (RS256) — injected via OIDC_JWKS, the same env var
  // Task 2 wires into `configuration.jwks` (persistent/shared signing key).
  const { publicKey, privateKey } = await jose.generateKeyPair("RS256", {
    extractable: true,
  });
  const privateJwk = await jose.exportJWK(privateKey);
  publicJwk = await jose.exportJWK(publicKey);
  // Fix the kid explicitly (oidc-provider's initialize_keystore.js honors an
  // already-present kid via `key.kid ??= calculateKid(key)`) so our
  // independent local-JWKS verification below can match on it without
  // reimplementing the provider's internal kid-derivation algorithm.
  privateJwk.kid = "test-kid-1";
  publicJwk.kid = "test-kid-1";
  process.env.OIDC_JWKS = JSON.stringify({ keys: [privateJwk] });

  configMod = await import("../index");
  oidcMod = await import("../oidc");
  authProfileMod = await import("../../entities/auth-profile");
});

/**
 * Mint a voice-resource access token for `accountId` through the real
 * features.resourceIndicators.getResourceServerInfo hook (not a hand-rolled
 * resourceServer object), then extraTokenClaims via AccessToken.save().
 */
async function mintVoiceAccessToken(accountId: string): Promise<string> {
  const { oidc } = oidcMod;
  const { config } = configMod;
  const cfg = instance(oidc).configuration;
  const ri = cfg.features.resourceIndicators;

  const resourceServer = await ri.getResourceServerInfo(
    {} as any,
    (config as any).oidc.voiceResource,
    { clientId: config.oidc.clients.voice.clientId } as any
  );

  const AccessTokenModel = (oidc as any).AccessToken;
  const token = new AccessTokenModel({
    accountId,
    client: { clientId: config.oidc.clients.voice.clientId },
    grantId: unique("grant"),
  });
  token.resourceServer = resourceServer;
  token.scope = resourceServer.scope;

  return token.save();
}

describe("oidc resource-indicator access token (AUTH-02)", () => {
  it("resourceIndicators is enabled with a jwt/RS256 resource server for the voice resource", async () => {
    const { oidc } = oidcMod;
    const { config } = configMod;
    const cfg = instance(oidc).configuration;

    expect(cfg.features.resourceIndicators.enabled).toBe(true);

    const resourceServer = await cfg.features.resourceIndicators.getResourceServerInfo(
      {} as any,
      (config as any).oidc.voiceResource,
      {} as any
    );
    expect(resourceServer.accessTokenFormat).toBe("jwt");
    expect(resourceServer.jwt?.sign?.alg).toBe("RS256");
    expect(resourceServer.audience).toBe((config as any).oidc.voiceAudience);

    // defaultResource resolves to the voice resource when no oneOf is passed,
    // and honors an explicit oneOf when the client requests one.
    const resolved = await cfg.features.resourceIndicators.defaultResource(
      {} as any,
      {} as any,
      undefined
    );
    expect(resolved).toBe((config as any).oidc.voiceResource);

    const honored = await cfg.features.resourceIndicators.defaultResource(
      {} as any,
      {} as any,
      ["https://some-other-resource.example"]
    );
    expect(honored).toEqual(["https://some-other-resource.example"]);

    await expect(
      cfg.features.resourceIndicators.useGrantedResource({} as any, {} as any)
    ).resolves.toBe(true);
  });

  it("mints a three-segment RS256 JWT with a kid present in the provider's JWKS, audienced to the voice resource", async () => {
    const accountId = unique("acct");
    await authProfileMod.AuthProfile.upsert({
      userId: accountId,
      activeTierId: "kphdemo123-tier",
      activeGroup: "beta",
    }).go();

    const jwt = await mintVoiceAccessToken(accountId);

    expect(jwt.split(".")).toHaveLength(3);

    const header = jose.decodeProtectedHeader(jwt);
    expect(header.alg).toBe("RS256");
    expect(typeof header.kid).toBe("string");

    // The kid must be present in the SAME JWKS document the provider serves
    // at routes.jwks (instance(oidc).jwks is exactly what actions/jwks.js
    // renders — see node_modules/oidc-provider/lib/actions/jwks.js).
    const { oidc } = oidcMod;
    const servedJwks = instance(oidc).jwks;
    expect(servedJwks.keys.some((k: any) => k.kid === header.kid)).toBe(true);

    // Independent offline verification against our own copy of the public key
    // (not the provider's internals) — this is exactly what Phase 4's PyJWT
    // + PyJWKClient will do against the real /jwks endpoint.
    const localJwks = jose.createLocalJWKSet({ keys: [publicJwk as any] });
    const { payload } = await jose.jwtVerify(jwt, localJwks);

    const { config } = configMod;
    expect(payload.aud).toBe((config as any).oidc.voiceAudience);
    expect((payload as any)[(config as any).oidc.claimNames.tierId]).toBe(
      "kphdemo123-tier"
    );
    expect((payload as any)[(config as any).oidc.claimNames.group]).toBe(
      "beta"
    );

    // No custom claims beyond the two namespaced ones (D-01 thin token).
    const standardClaims = new Set([
      "jti",
      "sub",
      "iat",
      "exp",
      "scope",
      "client_id",
      "iss",
      "aud",
      "authorization_details",
    ]);
    const customClaims = Object.keys(payload).filter(
      (k) => !standardClaims.has(k)
    );
    expect(customClaims.sort()).toEqual(
      [(config as any).oidc.claimNames.tierId, (config as any).oidc.claimNames.group].sort()
    );
  });

  it("defaults tier_id to the no-access value when AuthProfile.activeTierId is unset", async () => {
    const accountId = unique("acct-no-profile");
    // Deliberately do NOT create an AuthProfile row for this account.

    const jwt = await mintVoiceAccessToken(accountId);
    const payload = jose.decodeJwt(jwt);
    const { config } = configMod;

    expect((payload as any)[(config as any).oidc.claimNames.tierId]).toBe(
      "no-access"
    );
    expect((payload as any)[(config as any).oidc.claimNames.group]).toBeNull();
  });
});
