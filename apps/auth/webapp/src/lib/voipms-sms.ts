/**
 * Minimal VoIP.ms `sendSMS` relay helper (quick task 260716-hg5 follow-up,
 * design doc docs/superpowers/specs/2026-07-16-ctf-otp-sms-during-call-design.md).
 *
 * WHY this lives in the auth app: VoIP.ms's REST API is IP-allowlisted, and the
 * telephony-edge Fargate task egresses from an EPHEMERAL public IP that cannot
 * be reliably whitelisted. The auth app runs on a private subnet and egresses
 * from the STABLE NAT-gateway EIP, which IS whitelisted. So telephony-edge posts
 * the already-built SMS to `/ctf/sms` and this helper relays it to VoIP.ms.
 *
 * Logging discipline: this module NEVER logs the destination, the message, or
 * the credentials. It returns the VoIP.ms status ENUM (a short non-secret token
 * like "ip_not_enabled") so the ROUTE can log ONLY that for diagnosis.
 */

const VOIPMS_SMS_API_URL = "https://voip.ms/api/v1/rest.php";
// VoIP.ms `sendSMS` responds fast on a rejection (e.g. ip_not_enabled) but
// takes several seconds when it ACTUALLY sends (it contacts the carrier). A
// 4s timeout aborted real sends AFTER VoIP.ms had already queued them ->
// spurious `transport_error` with the SMS actually delivered (observed live
// 2026-07-16). 10s comfortably covers a genuine send. This runs server-side in
// the internal relay, not on any user-facing latency path.
const SEND_TIMEOUT_MS = 10000;

export interface VoipmsCreds {
  apiUsername: string;
  apiPassword: string;
}

/**
 * Send ONE SMS via VoIP.ms `sendSMS`. Resolves `{ ok, status }` where `ok` is
 * true ONLY on HTTP 200 with a JSON `status === "success"`. NEVER throws — any
 * transport/parse/timeout failure resolves to `ok: false` with a diagnostic
 * status token. Credentials cross only the outbound query string, never a log.
 */
export async function sendOneSms(
  did: string,
  to: string,
  message: string,
  creds: VoipmsCreds,
): Promise<{ ok: boolean; status: string }> {
  if (!did || !to || !message || !creds.apiUsername || !creds.apiPassword) {
    return { ok: false, status: "missing_params" };
  }
  const params = new URLSearchParams({
    api_username: creds.apiUsername,
    api_password: creds.apiPassword,
    method: "sendSMS",
    did,
    dst: to,
    message,
  });
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), SEND_TIMEOUT_MS);
  try {
    const resp = await fetch(`${VOIPMS_SMS_API_URL}?${params.toString()}`, {
      method: "GET",
      signal: controller.signal,
    });
    if (!resp.ok) return { ok: false, status: `http_${resp.status}` };
    const data: unknown = await resp.json();
    const status =
      data && typeof data === "object" && "status" in data
        ? String((data as Record<string, unknown>).status)
        : "malformed";
    return { ok: status === "success", status };
  } catch {
    return { ok: false, status: "transport_error" };
  } finally {
    clearTimeout(timer);
  }
}

/**
 * Try each DID in `dids` IN ORDER until one send succeeds (runtime
 * auto-fallback for a DID that is not SMS-enabled). Resolves
 * `{ sent, lastStatus }`: `sent` true on the first success (and stops — exactly
 * one text delivered), false if the pool is empty or every attempt failed.
 * Never throws.
 */
export async function sendSmsPool(
  dids: string[],
  to: string,
  message: string,
  creds: VoipmsCreds,
): Promise<{ sent: boolean; lastStatus: string }> {
  let lastStatus = "no_dids";
  for (const did of dids) {
    const { ok, status } = await sendOneSms(did, to, message, creds);
    lastStatus = status;
    if (ok) return { sent: true, lastStatus: status };
  }
  return { sent: false, lastStatus };
}
