import { NextRequest, NextResponse } from "next/server";
import { normalizeE164 } from "@/lib/phone-normalization";
import { resolvePhoneToCode } from "@/entities/access-code";
import { mintAnonToken } from "@/lib/bypass-token";

/**
 * The §23 VoIP.ms caller-ID mint route (12-CONTEXT.md D-02, Phase 12 Plan
 * 02). Mirrors the shipped bypass `/join/[token]/route.ts` almost exactly —
 * resolve -> mint -> return, same uniform no-oracle failure helper — with two
 * deliberate differences:
 *
 *  1. This route is INTERNAL-ONLY, never internet-exposed like `/join`. It is
 *     a token-minting oracle (T-12-02-01/T-12-02-03): the deploy-time network
 *     lock (12-07) is the primary boundary, and this route ALSO enforces an
 *     optional shared bearer token (TELEPHONY_ENDPOINT_AUTH_TOKEN, SSM-backed
 *     per D-04) as defense-in-depth. A missing/wrong bearer returns the SAME
 *     uniform failure as any other miss — it must never reveal that auth
 *     specifically failed (that would itself be an oracle).
 *  2. The 12-06 telephony controller consumes the minted token directly from
 *     the JSON response body (server-side HTTP call), NOT a browser redirect
 *     fragment — there is no browser in this flow.
 *
 * GET /use1/tel/<e164>  (basePath "/use1" from next.config.ts)
 * Authorization: Bearer <TELEPHONY_ENDPOINT_AUTH_TOKEN>  (required only if that env is set)
 *
 * Response (success): { token: "<jwt>", expiresIn: 3600 }, cache-control: no-store
 * Response (EVERY failure mode — unmapped number, disabled/expired code, mint
 * error, or bad bearer): an identical minimal 404, exactly like /join.
 *
 * The minted token is byte-compatible with a bypass /join token — SAME
 * issuer/aud/jwks/kid — so it validates in the voice service completely
 * unchanged (mintAnonToken is reused verbatim, not modified).
 */
export async function GET(
  request: NextRequest,
  { params }: { params: Promise<{ e164: string }> }
) {
  const notFound = () =>
    new NextResponse("Not found", {
      status: 404,
      headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" },
    });

  try {
    // Optional shared-bearer defense-in-depth (D-04). A mismatch returns the
    // SAME notFound() as every other failure mode — never a distinct 401/403
    // shape that would leak "auth failed specifically" to a probing caller.
    const expectedToken = process.env.TELEPHONY_ENDPOINT_AUTH_TOKEN;
    if (expectedToken) {
      const authHeader = request.headers.get("authorization");
      if (authHeader !== `Bearer ${expectedToken}`) {
        return notFound();
      }
    }

    const { e164 } = await params;
    const normalized = normalizeE164(decodeURIComponent(e164));
    if (!normalized) {
      return notFound();
    }

    // resolvePhoneToCode already returns null uniformly for unmapped/
    // disabled/expired — no branch here distinguishes those cases.
    const resolved = await resolvePhoneToCode(normalized);
    if (!resolved) {
      return notFound();
    }

    const { token, expiresIn } = await mintAnonToken({
      code: resolved.code,
      tierId: resolved.tierId,
      group: resolved.group,
    });

    // Log resolution outcome BY TIER ONLY — never the caller ID, and never a
    // mapped/not-mapped distinction (only the success path logs at all), so
    // the log stream itself carries no enumeration signal.
    console.info(`tel_resolved tier=${resolved.tierId}`);

    const res = NextResponse.json({ token, expiresIn }, { status: 200 });
    res.headers.set("cache-control", "no-store");
    return res;
  } catch {
    // Uniform failure — never leak whether the caller ID existed, whether
    // resolution succeeded, or whether minting itself threw.
    return notFound();
  }
}
