import { NextRequest, NextResponse } from "next/server";
import { computeTotp } from "@/lib/ctf-totp";

/**
 * The CTF phone-OTP announcement DID's issuer route (quick task 260715-oq0,
 * docs/superpowers/specs/2026-07-15-ctf-phone-otp-announcement-did-design.md;
 * extended by quick task 260727-qfq with a per-game dimension). Mirrors
 * `apps/auth/webapp/src/app/tel/[e164]/route.ts`'s structure almost
 * exactly -- a single `notFound()` helper, one try/catch whose catch returns
 * the SAME uniform 404, and an optional shared-bearer defense-in-depth via a
 * NEW env var (NOT reusing TELEPHONY_ENDPOINT_AUTH_TOKEN).
 *
 * GET /use1/ctf/otp[?g=<game>]  (basePath "/use1" from next.config.ts)
 * Authorization: Bearer <CTF_OTP_AUTH_TOKEN>  (required only if that env is set)
 *
 * Response (success): { code, digits: 6, period: 120, expiresIn }, cache-control: no-store.
 * Response (EVERY failure mode -- missing secret, bad/absent bearer, unknown
 * game, or any internal error): an identical minimal 404, exactly like /tel.
 *
 * This is an internal-only, no-mint issuer: it computes the current TOTP
 * step from a seed (SSM base32, env-only -- NEVER TOML) and returns it. TOTP
 * params (period=120, digits=6) are fixed constants, never request input --
 * the issuer emits ONLY the current-step code (no skew range; +-1 skew is a
 * verifier-only, out-of-scope meshtk concern).
 *
 * Game dimension (quick task 260727-qfq, D-01..D-05; fourth game
 * 260728-tfn): `?g=<game>` selects WHICH seed to compute from, via the
 * static `GAME_SECRET_ENV_VARS` allowlist below. `<game>` is one of
 * `"3234"`, `"3283"`, `"8283"`, `"1800"` (the toll-free game), mapping to
 * env vars `CTF_OTP_SECRET_3234`/`_3283`/`_8283`/`_1800` respectively (SSM
 * `/kmv/secrets/use1/ctf/otp_secret_{3234,3283,8283,1800}`).
 *
 * NO `g` param (or an empty one) is the LEGACY path and stays BYTE-IDENTICAL
 * to pre-260727-qfq behavior: it computes from `CTF_OTP_SECRET`. This is the
 * cutover-safety property (D-03) -- the live telephony-edge still calls the
 * bare URL until it redeploys, so this route must keep serving it exactly as
 * before. An UNKNOWN game, or a known game whose mapped env var is absent,
 * collapses into the SAME uniform 404 as a bad bearer or an internal error
 * (D-04) -- no distinct status, no oracle for which games exist or are
 * seeded.
 *
 * Logging discipline: never log the code, any secret, the game key, or any
 * caller-distinguishing value. This route has no log line at all -- unlike
 * /tel's tier-only success log, there is no non-sensitive dimension worth
 * logging here.
 */

/**
 * Static allowlist: game key (from the request's `?g=` query) -> the env
 * var NAME holding that game's TOTP seed (quick task 260727-qfq, D-01/D-02).
 * This Map is the ONLY bridge between request input and `process.env` --
 * request input is used solely as a lookup KEY here, and is NEVER
 * concatenated into an env-var name or logged. A `Map` has no prototype
 * chain, so a request sending a JS member name as `g` (`constructor`,
 * `__proto__`, `toString`) gets `undefined` from `.get()`, not an inherited
 * function -- no `hasOwnProperty` ceremony needed to stay safe.
 */
const GAME_SECRET_ENV_VARS: Map<string, string> = new Map([
  ["3234", "CTF_OTP_SECRET_3234"],
  ["3283", "CTF_OTP_SECRET_3283"],
  ["8283", "CTF_OTP_SECRET_8283"],
  ["1800", "CTF_OTP_SECRET_1800"],
]);

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

    // Defensive game-key read (quick task 260727-qfq, D-03): a request
    // object with no URL at all (this route's own test stub, pre-qfq)
    // degrades to the legacy path rather than a 404 -- this IS the
    // cutover-safety property, since the live telephony-edge still calls
    // the bare URL until it redeploys.
    const gameKey = request.nextUrl?.searchParams?.get("g")?.trim() ?? "";

    let secret: string | undefined;
    if (gameKey === "") {
      // Legacy path (D-03): byte-identical to pre-260727-qfq behavior.
      secret = process.env.CTF_OTP_SECRET;
    } else {
      // Per-game path (D-01). An allowlist miss (unknown game, or a
      // prototype member name like "constructor") is the SAME uniform 404
      // as every other failure mode (D-02/D-04) -- never a distinct shape.
      const envVarName = GAME_SECRET_ENV_VARS.get(gameKey);
      if (!envVarName) {
        return notFound();
      }
      secret = process.env[envVarName];
    }

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
