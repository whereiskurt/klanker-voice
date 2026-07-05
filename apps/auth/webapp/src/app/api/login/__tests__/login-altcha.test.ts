import { describe, it, expect, vi, beforeEach } from "vitest";
import crypto from "node:crypto";

/**
 * RED (Wave 0): asserts POST /api/login (a) rejects a missing/invalid Altcha
 * payload with 400/403, and (b) rejects a replayed (already-used) Altcha
 * payload with 403 — mirroring the ported `markChallengeUsed` in-memory
 * replay guard (T-03-02, AUTH-05).
 *
 * The route does not exist yet at this point in the port (Task 2 brings it
 * over via the wholesale snapshot-copy) — importing it below is expected to
 * fail, which is the RED signal this task's <verify> greps for.
 */

const AUTH_JWT_SECRET = "test-jwt-secret";
const CSRF_TOKEN = "test-csrf-token";
const ALTCHA_HMAC_KEY = "test-altcha-hmac-key";

function csrfCookieValue(): string {
  const hash = crypto
    .createHash("sha256")
    .update(`${CSRF_TOKEN}${AUTH_JWT_SECRET}`)
    .digest("hex");
  return `${CSRF_TOKEN}|${hash}`;
}

vi.mock("next/headers", () => ({
  cookies: vi.fn(async () => ({
    get: (name: string) =>
      name === "csrf_auth" ? { value: csrfCookieValue() } : undefined,
  })),
}));

vi.mock("@auth", () => ({
  signIn: vi.fn(async () => undefined),
}));

vi.mock("altcha-lib", () => ({
  verifySolution: vi.fn(),
}));

function makeRequest(body: Record<string, unknown>) {
  return { json: async () => body } as any;
}

describe("POST /api/login — Altcha verification + replay guard (AUTH-05, T-03-02)", () => {
  beforeEach(async () => {
    vi.clearAllMocks();
    process.env.AUTH_JWT_SECRET = AUTH_JWT_SECRET;
    process.env.ALTCHA_HMAC_KEY = ALTCHA_HMAC_KEY;
    const { verifySolution } = await import("altcha-lib");
    vi.mocked(verifySolution).mockResolvedValue(true as any);
  });

  it("rejects a request with a missing Altcha payload (400/403)", async () => {
    const { POST } = await import("@/app/api/login/route");
    const res = await POST(
      makeRequest({
        email: "user@example.com",
        csrfToken: CSRF_TOKEN,
        altcha: undefined,
      })
    );
    expect([400, 403]).toContain(res.status);
  });

  it("rejects an invalid Altcha solution (403)", async () => {
    const { verifySolution } = await import("altcha-lib");
    vi.mocked(verifySolution).mockResolvedValue(false as any);
    const { POST } = await import("@/app/api/login/route");
    const res = await POST(
      makeRequest({
        email: "user@example.com",
        csrfToken: CSRF_TOKEN,
        altcha: "bad-payload",
      })
    );
    expect(res.status).toBe(403);
  });

  it("rejects a replayed (already-used) Altcha payload on the second submission (403)", async () => {
    const { POST } = await import("@/app/api/login/route");
    const payload = "same-altcha-payload-used-twice";

    const first = await POST(
      makeRequest({
        email: "user@example.com",
        csrfToken: CSRF_TOKEN,
        altcha: payload,
      })
    );
    expect(first.status).toBe(200);

    const second = await POST(
      makeRequest({
        email: "user@example.com",
        csrfToken: CSRF_TOKEN,
        altcha: payload,
      })
    );
    expect(second.status).toBe(403);
  });
});
