import { describe, it, expect, beforeAll, afterEach } from "vitest";
import * as jose from "jose";

/**
 * Plan 12-02 Task 3: the private §23 caller-ID mint route (12-CONTEXT.md
 * D-02). SELF-CONTAINED like bypass-token.test.ts: mintAnonToken is
 * exercised against a locally-generated RS256 JWK Set injected via
 * OIDC_JWKS, and resolvePhoneToCode is exercised against a real
 * dynamodb-local `kmv-auth-electro` table (same backend as
 * phone-resolution.test.ts) — no mocking of the resolve/mint wiring under
 * test, matching login-access-code.test.ts's "exactly the wiring under
 * test" precedent.
 */

let GET: typeof import("../[e164]/route").GET;

function makeRequest(headers: Record<string, string> = {}): any {
  return {
    headers: {
      get: (name: string) => headers[name] ?? headers[name.toLowerCase()] ?? null,
    },
  };
}

function makeParams(e164: string) {
  return { params: Promise.resolve({ e164 }) };
}

function unique(label: string): string {
  return `${label}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

function uniquePhone(): string {
  const digits = String(Date.now()).slice(-7).padStart(7, "0");
  return `416${digits}`;
}

async function normalizedBody(res: Response) {
  return { status: res.status, text: await res.text() };
}

beforeAll(async () => {
  process.env.AUTH_ELECTRO_ENDPOINT = "http://localhost:8888";
  process.env.AUTH_ELECTRO_DBNAME = "kmv-auth-electro";
  process.env.AUTH_ELECTRO_ID = "local";
  process.env.AUTH_ELECTRO_SECRET = "local";
  process.env.AWS_REGION = process.env.AWS_REGION || "us-east-1";

  const { privateKey } = await jose.generateKeyPair("RS256", { extractable: true });
  const privateJwk = await jose.exportJWK(privateKey);
  privateJwk.kid = "tel-test-kid";
  privateJwk.use = "sig";
  process.env.OIDC_JWKS = JSON.stringify({ keys: [privateJwk] });

  ({ GET } = await import("../[e164]/route"));
});

describe("GET /tel/<e164> (§23 caller-ID mint, no-oracle contract)", () => {
  afterEach(() => {
    delete process.env.TELEPHONY_ENDPOINT_AUTH_TOKEN;
  });

  it("a mapped caller ID returns 200 with a token + expiresIn, cache-control no-store", async () => {
    const { AccessCode } = await import("@/entities/access-code");
    const code = unique("tel-mapped");
    const phone = uniquePhone();
    await AccessCode.create({
      code,
      tierId: "tel-tier",
      group: "pstn",
      phone,
      phoneEnabled: true,
    }).go();

    const res = await GET(makeRequest(), makeParams(`+1${phone}`));
    expect(res.status).toBe(200);
    expect(res.headers.get("cache-control")).toBe("no-store");

    const body = await res.json();
    expect(typeof body.token).toBe("string");
    expect(body.expiresIn).toBe(3600);

    const payload = jose.decodeJwt(body.token);
    expect(payload.sub).toMatch(new RegExp(`^anon:${code}:[0-9a-f-]{36}$`));
  });

  it("unknown, disabled, and expired numbers all return an identical 404", async () => {
    const { AccessCode } = await import("@/entities/access-code");

    const unknownPhone = uniquePhone();

    const disabledCode = unique("tel-disabled");
    const disabledPhone = uniquePhone();
    await AccessCode.create({
      code: disabledCode,
      tierId: "disabled-tier",
      phone: disabledPhone,
      phoneEnabled: false,
    }).go();

    const expiredCode = unique("tel-expired");
    const expiredPhone = uniquePhone();
    await AccessCode.create({
      code: expiredCode,
      tierId: "expired-tier",
      phone: expiredPhone,
      phoneEnabled: true,
      expiresAt: Date.now() - 60_000,
    }).go();

    const unknownRes = await normalizedBody(await GET(makeRequest(), makeParams(`+1${unknownPhone}`)));
    const disabledRes = await normalizedBody(await GET(makeRequest(), makeParams(`+1${disabledPhone}`)));
    const expiredRes = await normalizedBody(await GET(makeRequest(), makeParams(`+1${expiredPhone}`)));

    expect(unknownRes.status).toBe(404);
    expect(disabledRes).toEqual(unknownRes);
    expect(expiredRes).toEqual(unknownRes);
  });

  it("when the bearer env is set, a missing or wrong bearer returns the SAME 404 as any other miss", async () => {
    const { AccessCode } = await import("@/entities/access-code");
    const code = unique("tel-bearer");
    const phone = uniquePhone();
    await AccessCode.create({
      code,
      tierId: "bearer-tier",
      phone,
      phoneEnabled: true,
    }).go();

    process.env.TELEPHONY_ENDPOINT_AUTH_TOKEN = "shared-secret-token";

    const unmappedRes = await normalizedBody(
      await GET(makeRequest(), makeParams(`+1${uniquePhone()}`))
    );
    const missingBearerRes = await normalizedBody(
      await GET(makeRequest(), makeParams(`+1${phone}`))
    );
    const wrongBearerRes = await normalizedBody(
      await GET(
        makeRequest({ authorization: "Bearer wrong-token" }),
        makeParams(`+1${phone}`)
      )
    );

    expect(missingBearerRes.status).toBe(404);
    expect(missingBearerRes).toEqual(unmappedRes);
    expect(wrongBearerRes).toEqual(unmappedRes);

    // Correct bearer + a real mapping succeeds.
    const correctRes = await GET(
      makeRequest({ authorization: "Bearer shared-secret-token" }),
      makeParams(`+1${phone}`)
    );
    expect(correctRes.status).toBe(200);
  });

  it("imports and calls mintAnonToken and resolvePhoneToCode (not a reimplementation)", async () => {
    const routeSource = await import("node:fs/promises").then((fs) =>
      fs.readFile(new URL("../[e164]/route.ts", import.meta.url), "utf-8")
    );
    expect(routeSource).toContain("mintAnonToken");
    expect(routeSource).toContain("resolvePhoneToCode");
    // No log line emits the raw caller ID or a mapped/not-mapped distinction —
    // the only log call is the tier-only success line.
    expect(routeSource).not.toMatch(/console\.(info|log|warn|error)\([^)]*normalized/);
    expect(routeSource).not.toMatch(/console\.(info|log|warn|error)\([^)]*e164/);
  });
});
