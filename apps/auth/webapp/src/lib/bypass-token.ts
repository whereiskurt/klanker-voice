/**
 * Anonymous OIDC access-token minting for the bypass /join auto-login flow
 * (2026-07-10-bypass-join-login-design).
 *
 * A GET to auth.klankermaker.ai/use1/join/<bypassToken> resolves the token to a
 * code's tier, then calls mintAnonToken() to produce a short-lived RS256 JWT
 * that is byte-compatible with what oidc-provider issues to the voice client at
 * /token — SAME issuer, SAME audience, SAME namespaced tier_id/group claims,
 * SAME signing key set (OIDC_JWKS) and `kid`. That compatibility is the whole
 * point: the voice service (auth.py, PyJWT + PyJWKClient) validates this token
 * against the identical published /jwks endpoint and issuer/audience it already
 * enforces for the normal PKCE path — it is COMPLETELY UNCHANGED. The only
 * difference from a normal token is the `sub` (anon:<code>:<uuid>) and the fact
 * that it is minted directly rather than through an authorization-code exchange.
 *
 * We sign with `jose` (already present transitively via oidc-provider 9.x — we
 * pin the same major, ^6) loading the SAME private JWK set the provider signs
 * with, so the published public JWKS verifies both.
 */

import { randomUUID } from "node:crypto";
import { SignJWT, importJWK, type JWK } from "jose";
import { config } from "@/config";

export interface MintAnonTokenInput {
  code: string;
  tierId: string;
  group: string | null;
}

export interface MintedAnonToken {
  token: string;
  /** Seconds until expiry — mirrors oidc-provider's accessToken TTL contract. */
  expiresIn: number;
}

// One hour, matching config.oidc.ttl.accessToken (the pinned Phase-4 contract).
const EXPIRES_IN_SECONDS = 60 * 60;

interface JwksDocument {
  keys: JWK[];
}

/**
 * Select the RSA *private* signing key from the OIDC JWK Set. Prefers a key
 * flagged `use: "sig"`; otherwise falls back to the first RSA key that carries a
 * private `d` member. Throws if OIDC_JWKS is unset or has no usable signing key
 * — the /join route turns any throw into a uniform 404, so the failure never
 * leaks to the caller.
 */
function selectSigningJwk(): JWK {
  const raw = process.env.OIDC_JWKS;
  if (!raw) {
    throw new Error("bypass-token: OIDC_JWKS is not configured");
  }
  const parsed = JSON.parse(raw) as JwksDocument;
  const keys = Array.isArray(parsed?.keys) ? parsed.keys : [];

  const byUse = keys.find((k) => k.kty === "RSA" && k.use === "sig" && typeof k.d === "string");
  const byPrivate = keys.find((k) => k.kty === "RSA" && typeof k.d === "string");
  const jwk = byUse ?? byPrivate;
  if (!jwk) {
    throw new Error("bypass-token: no RSA private signing key in OIDC_JWKS");
  }
  return jwk;
}

/**
 * Mint a short-lived anonymous voice access token for a bypass /join visit.
 * The claim set is deliberately identical in shape to oidc-provider's
 * resource-indicator access token (config/oidc.ts extraTokenClaims): only the
 * two namespaced tier_id/group claims beyond the standard iss/aud/sub/iat/exp.
 */
export async function mintAnonToken({
  code,
  tierId,
  group,
}: MintAnonTokenInput): Promise<MintedAnonToken> {
  const jwk = selectSigningJwk();
  const key = await importJWK(jwk, "RS256");

  const claims: Record<string, unknown> = {
    [config.oidc.claimNames.tierId]: tierId,
    [config.oidc.claimNames.group]: group ?? null,
  };

  const token = await new SignJWT(claims)
    .setProtectedHeader({ alg: "RS256", kid: jwk.kid })
    .setIssuedAt()
    .setIssuer(config.oidc.issuer)
    .setAudience(config.oidc.voiceAudience)
    .setSubject(`anon:${code}:${randomUUID()}`)
    .setExpirationTime("1h")
    .sign(key);

  return { token, expiresIn: EXPIRES_IN_SECONDS };
}
