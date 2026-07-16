import { NextRequest, NextResponse } from "next/server";
import { sendSmsPool } from "@/lib/voipms-sms";

/**
 * POST /use1/ctf/sms — internal CTF OTP SMS relay (quick task 260716-hg5
 * follow-up, design doc docs/superpowers/specs/2026-07-16-ctf-otp-sms-during-
 * call-design.md).
 *
 * telephony-edge cannot call the VoIP.ms REST API directly: that API is
 * IP-allowlisted and telephony-edge's Fargate egress IP is ephemeral. The auth
 * app egresses from the STABLE NAT EIP (whitelisted), so telephony-edge POSTs
 * the already-built SMS here and this route relays it to VoIP.ms `sendSMS`.
 *
 * Mirrors /ctf/otp's no-oracle posture: an OPTIONAL shared bearer, and a
 * UNIFORM 404 for EVERY failure mode (bad/absent bearer, malformed body,
 * missing creds, or every send attempt failing). ONLY a genuine send success
 * returns 200 `{ sent: true }`.
 *
 * Request (JSON): { to: string (10-digit NANP), message: string (GSM-7),
 *                   dids: string[] (ordered sending-DID pool) }
 * Authorization: Bearer <CTF_OTP_AUTH_TOKEN>  (required only if that env is set)
 *
 * Logging discipline: NEVER logs the destination, the message, or the creds.
 * On failure it logs ONLY the VoIP.ms status ENUM (a non-secret token like
 * "ip_not_enabled") so an operator can diagnose a rejected send.
 */
export async function POST(request: NextRequest) {
  const notFound = () =>
    new NextResponse("Not found", {
      status: 404,
      headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" },
    });

  try {
    // Optional shared-bearer defense-in-depth — a mismatch returns the SAME
    // 404 as every other failure (no distinct 401/403 oracle).
    const expectedToken = process.env.CTF_OTP_AUTH_TOKEN;
    if (expectedToken) {
      const authHeader = request.headers.get("authorization");
      if (authHeader !== `Bearer ${expectedToken}`) return notFound();
    }

    const apiUsername = process.env.VOIPMS_API_USERNAME;
    const apiPassword = process.env.VOIPMS_API_PASSWORD;
    if (!apiUsername || !apiPassword) return notFound();

    let body: unknown;
    try {
      body = await request.json();
    } catch {
      return notFound();
    }
    const b = (body ?? {}) as Record<string, unknown>;
    const to = typeof b.to === "string" ? b.to : "";
    const message = typeof b.message === "string" ? b.message : "";
    const dids = Array.isArray(b.dids)
      ? b.dids.filter((d): d is string => typeof d === "string" && d.length > 0)
      : [];
    if (!to || !message || dids.length === 0) return notFound();

    const { sent, lastStatus } = await sendSmsPool(dids, to, message, {
      apiUsername,
      apiPassword,
    });
    if (!sent) {
      // Non-secret status enum ONLY (e.g. "ip_not_enabled") — never the
      // destination, message, or credentials.
      console.warn(`ctf/sms: relay send failed status=${lastStatus}`);
      return notFound();
    }

    const res = NextResponse.json({ sent: true }, { status: 200 });
    res.headers.set("cache-control", "no-store");
    return res;
  } catch {
    return notFound();
  }
}
