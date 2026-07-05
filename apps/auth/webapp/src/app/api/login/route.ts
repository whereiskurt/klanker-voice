import { signIn } from "@auth";
import { AuthError } from "next-auth";
import { NextRequest, NextResponse } from "next/server";
import { verifySolution } from "altcha-lib";
import { resolveAccessCode, normalizeCode } from "@/entities/access-code";
import { LoginIntent, normalizeEmail } from "@/entities/login-intent";

const ALTCHA_HMAC_KEY = process.env.ALTCHA_HMAC_KEY;
// Login-time login_intent TTL (D-05/Pitfall 3): long enough to cover a slow
// human clicking the magic link, short enough that an abandoned code
// resolution doesn't leak stale tier state indefinitely.
const LOGIN_INTENT_TTL_MS = 15 * 60 * 1000;

import { cookies } from "next/headers";
import crypto from "crypto";

// In-memory LRU cache for used Altcha challenges (replay protection)
// Key: challenge hash, Value: expiry timestamp
const usedChallenges = new Map<string, number>();
const CHALLENGE_TTL_MS = 2 * 60 * 1000; // 2 minutes

// Cleanup expired challenges every 30 seconds
setInterval(() => {
  const now = Date.now();
  for (const [hash, expiry] of usedChallenges) {
    if (expiry < now) {
      usedChallenges.delete(hash);
    }
  }
}, 30000);

/**
 * Check if a challenge has been used and mark it as used if not.
 * Returns true if the challenge is fresh (not replayed), false if it's a replay.
 */
const markChallengeUsed = (altchaPayload: string): boolean => {
  const challengeHash = crypto
    .createHash("sha256")
    .update(altchaPayload)
    .digest("hex");

  if (usedChallenges.has(challengeHash)) {
    return false; // Replay detected
  }

  usedChallenges.set(challengeHash, Date.now() + CHALLENGE_TTL_MS);
  return true; // Fresh challenge
};

//This function may not be necessary but does work as describe. Next.js handles CSRF tokens automatically, apparently.
export const verifyCsrfToken = async (csrf: string): Promise<boolean> => {
  try {
    const cookie = (await cookies()).get("csrf_auth");
    if (!cookie || !cookie.value || cookie.value.length < 1) {
      throw new Error("1. Invalid CSRF token - not found");
    }

    const csrfCookie = cookie.value;
    const delim = csrfCookie.indexOf("|") !== -1 ? "|" : "%7C"; //TODO: Remember why I did this...

    const [csrfToken, requestHash] = csrfCookie.split(delim);

    if (csrfToken !== csrf || !requestHash) {
      throw new Error("2. Mismatch token or no hash");
    }

    const secrets = (process.env.AUTH_JWT_SECRET || "").split(",");
    for (const secret of secrets) {
      if (!secret) continue;

      const expectedHash = crypto
        .createHash("sha256")
        .update(`${csrfToken}${secret}`)
        .digest("hex");

      if (expectedHash === requestHash) {
        return true;
      }
    }
  } catch (err) {
    console.error("Caught: CSRF verification error: ", err);
  }

  return false;
};

export async function POST(req: NextRequest) {
  const data = await req.json();

  const { email, csrfToken, inviteCode, altcha } = data;

  // Validate CSRF token here (optional if NextAuth.js already handles it)
  if (!verifyCsrfToken(csrfToken)) {
    return NextResponse.json(
      { message: "Invalid CSRF submission." },
      { status: 403 }
    );
  }

  // Verify Altcha proof-of-work challenge
  if (!ALTCHA_HMAC_KEY) {
    console.error("ALTCHA_HMAC_KEY not configured");
    return NextResponse.json(
      { error: "Captcha service not configured" },
      { status: 500 }
    );
  }

  if (!altcha) {
    return NextResponse.json(
      { error: "Please complete the verification challenge" },
      { status: 400 }
    );
  }

  const isValidChallenge = await verifySolution(altcha, ALTCHA_HMAC_KEY, true);
  if (!isValidChallenge) {
    return NextResponse.json(
      { error: "Invalid or expired verification. Please try again." },
      { status: 403 }
    );
  }

  // Check for replay attack (same challenge used twice)
  if (!markChallengeUsed(altcha)) {
    return NextResponse.json(
      { error: "Verification already used. Please refresh and try again." },
      { status: 403 }
    );
  }

  // AUTH-03/D-07: any code (or none) is accepted here — login ALWAYS
  // proceeds to signIn() below regardless of whether the code resolves.
  // Unknown/blank/expired/over-cap codes all resolve to the no-access tier
  // uniformly (T-03-07: no enumeration oracle); resolveAccessCode() never
  // throws or rejects the request.
  const resolved = await resolveAccessCode(inviteCode);

  // Bridge the resolved tier across to the (not-yet-existing, for a
  // first-time user) post-auth token via a short-lived, email-keyed
  // login_intent (Pitfall 3). Latest-wins: upsert overwrites any prior
  // intent for this email (D-05).
  try {
    await LoginIntent.upsert({
      email: normalizeEmail(email),
      code: normalizeCode(inviteCode),
      tierId: resolved.tierId,
      group: resolved.group ?? undefined,
      expiresAt: Date.now() + LOGIN_INTENT_TTL_MS,
    }).go();
  } catch (err) {
    // Non-fatal: login must still proceed even if the intent write fails
    // (e.g. transient DynamoDB error) — the user simply lands on no-access
    // until they try again with the code.
    console.error("Failed to write login_intent:", err);
  }

  try {
    await signIn("nodemailer", {
      email: encodeURI(email),
      csrfToken,
      redirect: false,
    });
  } catch (error) {
    if (error instanceof AuthError) {
      return NextResponse.json(
        { error: "Not authorized to login." },
        { status: 401 }
      );
    }
    return NextResponse.json({ error: JSON.stringify(error) }, { status: 400 });
  }
  return NextResponse.json(
    { message: "Success. Check your email." },
    { status: 200 }
  );
}
