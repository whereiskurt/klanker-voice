import { NextRequest, NextResponse } from "next/server";
import { computeTotp } from "@/lib/ctf-totp";

/**
 * The CTF phone-OTP announcement DID's issuer route (quick task 260715-oq0,
 * docs/superpowers/specs/2026-07-15-ctf-phone-otp-announcement-did-design.md).
 * Mirrors `apps/auth/webapp/src/app/tel/[e164]/route.ts`'s structure almost
 * exactly -- a single `notFound()` helper, one try/catch whose catch returns
 * the SAME uniform 404, and an optional shared-bearer defense-in-depth via a
 * NEW env var (NOT reusing TELEPHONY_ENDPOINT_AUTH_TOKEN).
 *
 * GET /use1/ctf/otp  (basePath "/use1" from next.config.ts)
 * Authorization: Bearer <CTF_OTP_AUTH_TOKEN>  (required only if that env is set)
 *
 * Response (success): { code, digits: 6, period: 120, expiresIn }, cache-control: no-store.
 * Response (EVERY failure mode -- missing secret, bad/absent bearer, or any
 * internal error): an identical minimal 404, exactly like /tel.
 *
 * This is an internal-only, no-mint issuer: it computes the current TOTP
 * step from CTF_OTP_SECRET (SSM base32, env-only -- NEVER TOML) and returns
 * it. TOTP params (period=120, digits=6) are fixed constants, never request
 * input -- the issuer emits ONLY the current-step code (no skew range; +-1
 * skew is a verifier-only, out-of-scope meshtk concern).
 *
 * Logging discipline: never log the code, the secret, or any
 * caller-distinguishing value. This route has no log line at all -- unlike
 * /tel's tier-only success log, there is no non-sensitive dimension worth
 * logging here.
 */
export async function GET(request: NextRequest) {
  const notFound = () =>
    new NextResponse("Not found", {
      status: 404,
      headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" },
    });

  try {
    // Optional shared-bearer defense-in-depth. A mismatch returns the SAME
    // notFound() as every other failure mode -- never a distinct 401/403
    // shape that would leak "auth failed specifically" to a probing caller.
    const expectedToken = process.env.CTF_OTP_AUTH_TOKEN;
    if (expectedToken) {
      const authHeader = request.headers.get("authorization");
      if (authHeader !== `Bearer ${expectedToken}`) {
        return notFound();
      }
    }

    const secret = process.env.CTF_OTP_SECRET;
    if (!secret) {
      return notFound();
    }

    const { code, expiresIn } = computeTotp(secret, { period: 120, digits: 6 });

    const res = NextResponse.json({ code, digits: 6, period: 120, expiresIn }, { status: 200 });
    res.headers.set("cache-control", "no-store");
    return res;
  } catch {
    // Uniform failure -- never leak whether the secret was malformed or
    // computeTotp itself threw.
    return notFound();
  }
}
