/**
 * Gesture-gated `getUserMedia` (CLNT-01, D-12).
 *
 * `requestMic()` must be called from inside a user-gesture handler (the
 * "Tap to talk" tap) — never on load. This matters on iOS: the same
 * gesture that grants mic permission is also what unlocks audio *playback*
 * (the agent's spoken replies), so calling this eagerly on mount would
 * both prompt for permission before the user has done anything AND forfeit
 * the iOS audio-unlock the tap provides.
 *
 * Three honest, distinct failure states (never merged into one generic
 * "mic error", per D-12):
 *  - "unsupported": the browser has no `navigator.mediaDevices.getUserMedia`
 *    at all (legacy browser / insecure context) — feature-detected FIRST,
 *    so this path never calls `getUserMedia`.
 *  - "denied": the user (or the browser/OS) refused the permission prompt
 *    (`NotAllowedError` / `SecurityError`).
 *  - "no-device": no microphone hardware could satisfy the request
 *    (`NotFoundError` / `OverconstrainedError`).
 *  - anything else unclassified falls back to the "denied"-style honest
 *    message per the plan's action spec (a generic denied-style fallback,
 *    not a fourth invented state).
 */

export type MicError = "denied" | "no-device" | "unsupported";

export type MicResult = { status: "granted"; stream: MediaStream } | { status: MicError };

function classifyGetUserMediaError(err: unknown): MicError {
  const name = err instanceof DOMException ? err.name : undefined;
  if (name === "NotAllowedError" || name === "SecurityError") return "denied";
  if (name === "NotFoundError" || name === "OverconstrainedError") return "no-device";
  // Anything else (NotReadableError, AbortError, etc.) — generic denied-style
  // fallback per the action spec; still an honest "try again" affordance.
  return "denied";
}

/** Feature-detects, then requests mic access. Call only from a user gesture. */
export async function requestMic(): Promise<MicResult> {
  if (!navigator.mediaDevices?.getUserMedia) {
    return { status: "unsupported" };
  }
  try {
    const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
    return { status: "granted", stream };
  } catch (err) {
    return { status: classifyGetUserMediaError(err) };
  }
}
