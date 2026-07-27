import { describe, it, expect, beforeAll, afterEach } from "vitest";
import { computeTotp } from "@/lib/ctf-totp";

/**
 * Quick task 260715-oq0, Task 1: GET /ctf/otp no-oracle contract. Extended
 * by quick task 260727-qfq with a per-game dimension (D-01..D-05) and its
 * own regression tests. Mirrors tel-route.test.ts's shape -- makeRequest
 * header stub, env set/clear in beforeAll/afterEach, dynamic import of the
 * route after env is set.
 *
 * Self-contained (no DynamoDB/JWKS dependency -- this route computes a TOTP
 * locally, it never mints a token or resolves a phone number).
 */

let GET: typeof import("../otp/route").GET;

/**
 * Builds a request stub carrying a real `nextUrl` (quick task 260727-qfq --
 * `search` is an optional raw query string, e.g. "?g=3234", so per-game
 * cases become expressible). Existing call sites that pass only `headers`
 * keep working unchanged (`search` defaults to "", i.e. no `g` at all --
 * the legacy path).
 */
function makeRequest(headers: Record<string, string> = {}, search = ""): any {
  return {
    headers: {
      get: (name: string) => headers[name] ?? headers[name.toLowerCase()] ?? null,
    },
    nextUrl: new URL(`https://auth.klankermaker.ai/use1/ctf/otp${search}`),
  };
}

/**
 * A request stub with NO url at all (quick task 260727-qfq) -- proves the
 * route's defensive `?.` read of `nextUrl` degrades toward the legacy path
 * rather than throwing (and thus 404ing via the catch), which is the
 * cutover-safety property (D-03).
 */
function makeRequestNoUrl(headers: Record<string, string> = {}): any {
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
    // Quick task 260727-qfq: a leaked per-game secret would silently
    // invalidate the legacy-path proofs below, so these are cleaned up
    // alongside the two pre-existing env vars.
    delete process.env.CTF_OTP_SECRET_3234;
    delete process.env.CTF_OTP_SECRET_3283;
    delete process.env.CTF_OTP_SECRET_8283;
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

  // --- Quick task 260727-qfq: legacy path is the cutover-safety property ---
  // (D-03) -- the live telephony-edge still calls the bare URL until it
  // redeploys, so a no-`g` request must keep behaving exactly as before.

  it("a request with NO url at all still takes the legacy path and returns 200 (never 404)", async () => {
    process.env.CTF_OTP_SECRET = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ";

    const res = await GET(makeRequestNoUrl());
    expect(res.status).toBe(200);
  });

  it("a no-g request ignores all three per-game env vars, even when they hold invalid base32 (deterministic proof)", async () => {
    process.env.CTF_OTP_SECRET = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ";
    process.env.CTF_OTP_SECRET_3234 = "not-valid-base32!!!";
    process.env.CTF_OTP_SECRET_3283 = "not-valid-base32!!!";
    process.env.CTF_OTP_SECRET_8283 = "not-valid-base32!!!";

    // Had ANY per-game env been read here, computeTotp would throw and the
    // route would return its uniform 404 instead of 200 -- so 200 is
    // deterministic proof the per-game envs are never consulted on this path.
    const res = await GET(makeRequest());
    expect(res.status).toBe(200);

    const body = await res.json();
    const { code: expectedCode } = computeTotp(process.env.CTF_OTP_SECRET!, {
      period: 120,
      digits: 6,
    });
    expect(body.code).toBe(expectedCode);
  });

  it("an explicitly empty ?g= behaves exactly like no g at all", async () => {
    process.env.CTF_OTP_SECRET = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ";

    const noGRes = await normalizedBody(await GET(makeRequest({}, "")));
    const emptyGRes = await normalizedBody(await GET(makeRequest({}, "?g=")));
    expect(emptyGRes.status).toBe(200);
    expect(emptyGRes).toEqual(noGRes);
  });

  // --- Quick task 260727-qfq: per-game path (D-01, D-05) -------------------

  describe.each([
    ["3234", "CTF_OTP_SECRET_3234"],
    ["3283", "CTF_OTP_SECRET_3283"],
    ["8283", "CTF_OTP_SECRET_8283"],
  ])("game %s", (game, envVar) => {
    it(`?g=${game} resolves from ${envVar}, ignoring the legacy secret (D-01/D-05)`, async () => {
      process.env[envVar] = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ";
      // Deliberately INVALID base32 -- if the per-game path ever fell back
      // to the legacy secret, computeTotp would throw and the route would
      // return its uniform 404, not 200 (deterministic proof, mirrors the
      // no-g proof above in the opposite direction).
      process.env.CTF_OTP_SECRET = "not-valid-base32!!!";

      const res = await GET(makeRequest({}, `?g=${game}`));
      expect(res.status).toBe(200);
      expect(res.headers.get("cache-control")).toBe("no-store");

      const body = await res.json();
      expect(body.digits).toBe(6);
      expect(body.period).toBe(120);
      expect(body.expiresIn).toBeGreaterThanOrEqual(1);
      expect(body.expiresIn).toBeLessThanOrEqual(120);

      const { code: expectedCode } = computeTotp(process.env[envVar]!, {
        period: 120,
        digits: 6,
      });
      expect(body.code).toBe(expectedCode);
    });
  });

  // --- Quick task 260727-qfq: uniform 404, no oracle (D-04) -----------------
  // Every failure mode below must equal a CAPTURED baseline response --
  // same status AND same body text -- not just a bare 404 assertion.

  it("every failure mode returns the SAME 404 as a captured missing-secret baseline", async () => {
    const baseline = await normalizedBody(await GET(makeRequest()));
    expect(baseline.status).toBe(404);

    // An unknown game.
    const unknownGame = await normalizedBody(await GET(makeRequest({}, "?g=9999")));
    expect(unknownGame).toEqual(baseline);

    // A known game whose mapped env var is absent.
    const missingMappedSecret = await normalizedBody(
      await GET(makeRequest({}, "?g=3234"))
    );
    expect(missingMappedSecret).toEqual(baseline);

    // A wrong or missing bearer while CTF_OTP_AUTH_TOKEN is set, WITH a
    // ?g= present.
    process.env.CTF_OTP_AUTH_TOKEN = "shared-secret-token";
    process.env.CTF_OTP_SECRET_3234 = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ";
    const wrongBearerWithGame = await normalizedBody(
      await GET(makeRequest({ authorization: "Bearer wrong-token" }, "?g=3234"))
    );
    expect(wrongBearerWithGame).toEqual(baseline);
    const missingBearerWithGame = await normalizedBody(
      await GET(makeRequest({}, "?g=3234"))
    );
    expect(missingBearerWithGame).toEqual(baseline);
    delete process.env.CTF_OTP_AUTH_TOKEN;

    // A known game whose mapped env var holds malformed base32 (the catch path).
    process.env.CTF_OTP_SECRET_3283 = "not-valid-base32!!!";
    const malformedMappedSecret = await normalizedBody(
      await GET(makeRequest({}, "?g=3283"))
    );
    expect(malformedMappedSecret).toEqual(baseline);
  });

  // --- Quick task 260727-qfq: prototype safety (D-02) -----------------------

  it.each(["constructor", "__proto__", "toString"])(
    "?g=%s never resolves to an inherited member -- returns the uniform 404",
    async (protoKey) => {
      const baseline = await normalizedBody(await GET(makeRequest()));
      const res = await normalizedBody(await GET(makeRequest({}, `?g=${protoKey}`)));
      expect(res).toEqual(baseline);
    }
  );

  // --- Quick task 260727-qfq: source-level discipline (D-02) ---------------

  it("the route source never interpolates request input into a process.env index", async () => {
    const routeSource = await import("node:fs/promises").then((fs) =>
      fs.readFile(new URL("../otp/route.ts", import.meta.url), "utf-8")
    );
    // No interpolated env index (e.g. `process.env[\`...${x}...\`]`) -- the
    // structural encoding of "request input never becomes an env-var name".
    expect(routeSource).not.toMatch(/process\.env\[[^\]]*\$\{/);
  });
});
