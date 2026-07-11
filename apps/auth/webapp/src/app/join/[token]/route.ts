import { NextRequest, NextResponse } from "next/server";
import { resolveBypassToken } from "@/entities/access-code";
import { mintAnonToken } from "@/lib/bypass-token";
import { config } from "@/config";

/**
 * Bypass /join auto-login route (2026-07-10-bypass-join-login-design).
 *
 * GET /use1/join/<bypassToken>  (basePath "/use1" from next.config.ts)
 *
 * Resolves a per-access-code bypass token to its tier, mints a short-lived
 * ANONYMOUS voice OIDC access token (sub=anon:<code>:<uuid>), and 302-redirects
 * the browser straight to the voice app's callback, carrying the JWT in the URL
 * FRAGMENT:
 *
 *   https://voice.<domain>/callback#access_token=<jwt>&token_type=bearer&expires_in=3600&anon=1
 *
 * The token rides in the fragment (after '#') on purpose: fragments are never
 * sent to the server and are stripped from the Referer header, so the bearer
 * credential stays out of auth/CDN access logs and out of any downstream
 * Referer. The voice client reads `window.location.hash`, stores the token in
 * memory, scrubs the hash, and drops into the mic.
 *
 * The voice service (auth.py, PyJWT + PyJWKClient) is COMPLETELY UNCHANGED: this
 * JWT is signed with the same OIDC_JWKS key/kid, issuer, audience, and
 * namespaced tier_id/group claims as a normal PKCE-exchanged token, so it
 * validates against the same published /jwks endpoint.
 *
 * SECURITY / no-oracle: every failure mode (unknown token, bypass-disabled
 * code, expired code, or any minting error) returns an identical minimal 404 so
 * the endpoint offers no token-enumeration signal.
 */
export async function GET(
  _request: NextRequest,
  { params }: { params: Promise<{ token: string }> }
) {
  const notFound = () =>
    new NextResponse("Not found", {
      status: 404,
      headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" },
    });

  try {
    const { token } = await params;
    const resolved = await resolveBypassToken(token);
    if (!resolved) {
      return notFound();
    }

    const { token: jwt, expiresIn } = await mintAnonToken({
      code: resolved.code,
      tierId: resolved.tierId,
      group: resolved.group,
    });

    // voiceAudience IS "https://voice.klankermaker.ai" — the same origin the
    // minted token is audienced to; derive from siteDomain so a non-default
    // SITE_DOMAIN still points at the right voice host.
    const voiceOrigin = `https://voice.${config.siteDomain}`;
    const fragment = new URLSearchParams({
      access_token: jwt,
      token_type: "bearer",
      expires_in: String(expiresIn),
      anon: "1",
    }).toString();

    const res = NextResponse.redirect(`${voiceOrigin}/callback#${fragment}`, 302);
    // CRITICAL: the Location header carries a freshly-minted bearer token — it
    // must never be cached by CloudFront/any proxy and re-served to another
    // visitor. A new token is minted on every visit.
    res.headers.set("cache-control", "no-store");
    return res;
  } catch {
    // Uniform failure — never leak whether the token existed or minting failed.
    return notFound();
  }
}
