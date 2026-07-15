import { describe, it, expect, beforeAll, afterEach } from "vitest";
import { computeTotp } from "@/lib/ctf-totp";

/**
 * Quick task 260715-oq0, Task 1: GET /ctf/otp no-oracle contract. Mirrors
 * tel-route.test.ts's shape -- makeRequest header stub, env set/clear in
 * beforeAll/afterEach, dynamic import of the route after env is set.
 *
 * Self-contained (no DynamoDB/JWKS dependency -- this route computes a TOTP
 * locally, it never mints a token or resolves a phone number).
 */

let GET: typeof import("../otp/route").GET;

function makeRequest(headers: Record<string, string> = {}): any {
  return {
    headers: {
      get: (name: string) => headers[name] ?? headers[name.toLowerCase()] ?? null,
    },
  };
}

async function normalizedBody(res: Response) {
  return { status: res.status, text: await res.text() };
}

beforeAll(async () => {
  ({ GET } = await import("../otp/route"));
});

describe("GET /ctf/otp (CTF phone-OTP issuer, no-oracle contract)", () => {
  afterEach(() => {
    delete process.env.CTF_OTP_SECRET;
    delete process.env.CTF_OTP_AUTH_TOKEN;
  });

  it("a configured secret returns 200 with the current-step TOTP, cache-control no-store", async () => {
    process.env.CTF_OTP_SECRET = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ";

    const res = await GET(makeRequest());
    expect(res.status).toBe(200);
    expect(res.headers.get("cache-control")).toBe("no-store");

    const body = await res.json();
    expect(body.digits).toBe(6);
    expect(body.period).toBe(120);
    expect(typeof body.code).toBe("string");
    expect(body.code).toMatch(/^\d{6}$/);
    expect(body.expiresIn).toBeGreaterThanOrEqual(1);
    expect(body.expiresIn).toBeLessThanOrEqual(120);

    // Compare code equality against a locally-computed TOTP in the same
    // tick, rather than a hardcoded value, to avoid clock flakiness.
    const { code: expectedCode } = computeTotp(process.env.CTF_OTP_SECRET!, {
      period: 120,
      digits: 6,
    });
    expect(body.code).toBe(expectedCode);
  });

  it("a missing CTF_OTP_SECRET returns 404 (uniform failure)", async () => {
    delete process.env.CTF_OTP_SECRET;

    const res = await normalizedBody(await GET(makeRequest()));
    expect(res.status).toBe(404);
  });

  it("when the bearer env is set, a missing or wrong bearer returns the SAME 404 as a missing secret", async () => {
    process.env.CTF_OTP_AUTH_TOKEN = "shared-secret-token";

    const missingSecretRes = await normalizedBody(await GET(makeRequest()));

    process.env.CTF_OTP_SECRET = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ";

    const missingBearerRes = await normalizedBody(await GET(makeRequest()));
    const wrongBearerRes = await normalizedBody(
      await GET(makeRequest({ authorization: "Bearer wrong-token" }))
    );

    expect(missingBearerRes.status).toBe(404);
    expect(missingBearerRes).toEqual(missingSecretRes);
    expect(wrongBearerRes).toEqual(missingSecretRes);

    // Correct bearer + a configured secret succeeds.
    const correctRes = await GET(
      makeRequest({ authorization: "Bearer shared-secret-token" })
    );
    expect(correctRes.status).toBe(200);
  });

  it("an internal error (malformed secret) still returns the identical uniform 404", async () => {
    // Not valid base32 -- computeTotp throws, the route's catch must
    // produce the SAME 404 shape as every other failure mode.
    process.env.CTF_OTP_SECRET = "not-valid-base32!!!";

    const malformedRes = await normalizedBody(await GET(makeRequest()));
    delete process.env.CTF_OTP_SECRET;
    const missingRes = await normalizedBody(await GET(makeRequest()));

    expect(malformedRes.status).toBe(404);
    expect(malformedRes).toEqual(missingRes);
  });

  it("imports and calls computeTotp (not a reimplementation); never logs the code or secret", async () => {
    const routeSource = await import("node:fs/promises").then((fs) =>
      fs.readFile(new URL("../otp/route.ts", import.meta.url), "utf-8")
    );
    expect(routeSource).toContain("computeTotp");
    expect(routeSource).not.toMatch(/console\.(info|log|warn|error)\(/);
  });
});
