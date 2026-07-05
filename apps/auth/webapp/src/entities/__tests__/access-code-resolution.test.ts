import { describe, it, expect, beforeAll } from "vitest";

/**
 * RED (Plan 03-02 Task 1): asserts the access-code resolution matrix
 * (AUTH-03/AUTH-04) against a real dynamodb-local `kmv-auth-electro` table —
 * `resolveAccessCode` and the `AccessCode` entity do not exist yet at this
 * point, so every import below fails. That import failure IS the RED signal
 * this task's <verify> greps for.
 *
 * Local backend: dynamodb-local on http://localhost:8888 (see plan 03-01
 * user_setup + from-aws.tmpl AUTH_ELECTRO_ENDPOINT), table `kmv-auth-electro`
 * created ad hoc for this executor session. Each test uses a unique,
 * timestamp-suffixed code so runs don't collide with leftover items.
 */

beforeAll(() => {
  process.env.AUTH_ELECTRO_ENDPOINT = "http://localhost:8888";
  process.env.AUTH_ELECTRO_DBNAME = "kmv-auth-electro";
  process.env.AUTH_ELECTRO_ID = "local";
  process.env.AUTH_ELECTRO_SECRET = "local";
  process.env.AWS_REGION = process.env.AWS_REGION || "us-east-1";
});

function uniqueCode(label: string): string {
  return `${label}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

describe("resolveAccessCode (AUTH-03, AUTH-04)", () => {
  it("known, non-expired, under-cap code resolves to its tier", async () => {
    const { AccessCode, resolveAccessCode } = await import("../access-code");
    const code = uniqueCode("demo");
    await AccessCode.create({
      code,
      tierId: "demo-tier",
      group: "conference",
      maxRedemptions: 5,
      redemptionCount: 0,
    }).go();

    const result = await resolveAccessCode(code);
    expect(result).toEqual({ tierId: "demo-tier", group: "conference" });
  });

  it("blank code resolves to no-access", async () => {
    const { resolveAccessCode } = await import("../access-code");
    const result = await resolveAccessCode("");
    expect(result).toEqual({ tierId: "no-access", group: null });
  });

  it("unknown code resolves to no-access", async () => {
    const { resolveAccessCode } = await import("../access-code");
    const result = await resolveAccessCode(uniqueCode("bogus-never-created"));
    expect(result).toEqual({ tierId: "no-access", group: null });
  });

  it("expired code (expiresAt < now) resolves to no-access", async () => {
    const { AccessCode, resolveAccessCode } = await import("../access-code");
    const code = uniqueCode("expired");
    await AccessCode.create({
      code,
      tierId: "demo-tier",
      expiresAt: Date.now() - 60_000,
    }).go();

    const result = await resolveAccessCode(code);
    expect(result).toEqual({ tierId: "no-access", group: null });
  });

  it("code at/over max_redemptions resolves to no-access", async () => {
    const { AccessCode, resolveAccessCode } = await import("../access-code");
    const code = uniqueCode("capped");
    await AccessCode.create({
      code,
      tierId: "demo-tier",
      maxRedemptions: 2,
      redemptionCount: 2,
    }).go();

    const result = await resolveAccessCode(code);
    expect(result).toEqual({ tierId: "no-access", group: null });
  });

  it("case policy: resolve('DEMO'-cased) matches a stored lowercase code", async () => {
    const { AccessCode, resolveAccessCode } = await import("../access-code");
    const code = uniqueCode("caseinsensitive");
    await AccessCode.create({ code, tierId: "demo-tier" }).go();

    const result = await resolveAccessCode(code.toUpperCase());
    expect(result.tierId).toBe("demo-tier");
  });
});
