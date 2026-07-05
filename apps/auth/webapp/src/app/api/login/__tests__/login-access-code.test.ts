import { describe, it, expect, vi, beforeEach, beforeAll } from "vitest";
import crypto from "node:crypto";

/**
 * Plan 03-02 Task 3: POST /api/login resolves the access code and writes a
 * login_intent BEFORE calling signIn() — login always proceeds regardless of
 * whether the code resolves (AUTH-03, D-07). Uses the same real
 * dynamodb-local backend as the entity/bridge tests (no mocking of
 * access-code/login-intent — this is exactly the wiring under test).
 *
 * Mocking pattern (next/headers, @auth, next-auth, altcha-lib) copied from
 * the sibling login-altcha.test.ts (Plan 03-01) for the same ESM-resolution
 * reasons documented there.
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

vi.mock("next-auth", () => ({
  AuthError: class AuthError extends Error {},
}));

vi.mock("altcha-lib", () => ({
  verifySolution: vi.fn(),
}));

function makeRequest(body: Record<string, unknown>) {
  return { json: async () => body } as any;
}

function unique(label: string): string {
  return `${label}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

beforeAll(() => {
  process.env.AUTH_ELECTRO_ENDPOINT = "http://localhost:8888";
  process.env.AUTH_ELECTRO_DBNAME = "kmv-auth-electro";
  process.env.AUTH_ELECTRO_ID = "local";
  process.env.AUTH_ELECTRO_SECRET = "local";
  process.env.AWS_REGION = process.env.AWS_REGION || "us-east-1";
});

describe("POST /api/login — access-code resolution + login_intent bridge (AUTH-03, D-07)", () => {
  beforeEach(async () => {
    vi.clearAllMocks();
    process.env.AUTH_JWT_SECRET = AUTH_JWT_SECRET;
    process.env.ALTCHA_HMAC_KEY = ALTCHA_HMAC_KEY;
    const { verifySolution } = await import("altcha-lib");
    vi.mocked(verifySolution).mockResolvedValue(true as any);
  });

  it("a known code writes a login_intent carrying the resolved tier + code, and login still succeeds", async () => {
    const { AccessCode } = await import("../../../../entities/access-code");
    const { LoginIntent } = await import("../../../../entities/login-intent");
    const code = unique("knowncode");
    await AccessCode.create({
      code,
      tierId: "demo-tier",
      group: "conference",
      redemptionCount: 0,
    }).go();

    const email = `${unique("known")}@example.com`;
    const { POST } = await import("../route");
    const res = await POST(
      makeRequest({
        email,
        csrfToken: CSRF_TOKEN,
        inviteCode: code.toUpperCase(), // case policy: uppercase input still resolves
        altcha: `payload-${email}`,
      })
    );

    expect(res.status).toBe(200);

    const { data: intent } = await LoginIntent.get({ email }).go();
    expect(intent?.tierId).toBe("demo-tier");
    expect(intent?.group).toBe("conference");
    expect(intent?.code).toBe(code); // normalized lowercase
  });

  it("an unknown code writes a no-access login_intent, and login still succeeds", async () => {
    const { LoginIntent } = await import("../../../../entities/login-intent");
    const email = `${unique("unknown")}@example.com`;
    const { POST } = await import("../route");
    const res = await POST(
      makeRequest({
        email,
        csrfToken: CSRF_TOKEN,
        inviteCode: "this-code-was-never-created",
        altcha: `payload-${email}`,
      })
    );

    expect(res.status).toBe(200);

    const { data: intent } = await LoginIntent.get({ email }).go();
    expect(intent?.tierId).toBe("no-access");
  });

  it("a blank code writes a no-access login_intent, and login still succeeds", async () => {
    const { LoginIntent } = await import("../../../../entities/login-intent");
    const email = `${unique("blank")}@example.com`;
    const { POST } = await import("../route");
    const res = await POST(
      makeRequest({
        email,
        csrfToken: CSRF_TOKEN,
        inviteCode: "",
        altcha: `payload-${email}`,
      })
    );

    expect(res.status).toBe(200);

    const { data: intent } = await LoginIntent.get({ email }).go();
    expect(intent?.tierId).toBe("no-access");
  });

  it("no longer references AUTH_INVITE_CODES (the removed static gate)", async () => {
    const routeSource = await import("node:fs/promises").then((fs) =>
      fs.readFile(new URL("../route.ts", import.meta.url), "utf-8")
    );
    expect(routeSource).not.toContain("AUTH_INVITE_CODES");
  });
});
