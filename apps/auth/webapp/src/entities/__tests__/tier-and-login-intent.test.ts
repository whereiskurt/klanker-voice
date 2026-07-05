import { describe, it, expect, beforeAll } from "vitest";

/**
 * Coverage for Task 2's remaining <behavior> bullets not already exercised
 * by access-code-resolution.test.ts / code-redemption.test.ts:
 *   - Tier.get({tierId}) resolves session/period/concurrency fields
 *   - LoginIntent.upsert overwrites by email (latest-wins) and carries a
 *     ttl/expiresAt (verifying the ttl attribute is actually derived, not
 *     just declared)
 *
 * Same dynamodb-local backend as the sibling entity tests.
 */

beforeAll(() => {
  process.env.AUTH_ELECTRO_ENDPOINT = "http://localhost:8888";
  process.env.AUTH_ELECTRO_DBNAME = "kmv-auth-electro";
  process.env.AUTH_ELECTRO_ID = "local";
  process.env.AUTH_ELECTRO_SECRET = "local";
  process.env.AWS_REGION = process.env.AWS_REGION || "us-east-1";
});

function unique(label: string): string {
  return `${label}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

describe("Tier entity", () => {
  it("Tier.get({tierId}) resolves session/period/concurrency fields", async () => {
    const { Tier } = await import("../tier");
    const tierId = unique("demo-tier");
    await Tier.create({
      tierId,
      group: "conference",
      sessionMaxSeconds: 120,
      periodMaxSeconds: 1800,
      maxConcurrent: 200,
    }).go();

    const { data } = await Tier.get({ tierId }).go();
    expect(data?.sessionMaxSeconds).toBe(120);
    expect(data?.periodMaxSeconds).toBe(1800);
    expect(data?.maxConcurrent).toBe(200);
    expect(data?.group).toBe("conference");
  });
});

describe("LoginIntent entity", () => {
  it("upsert overwrites by email (latest-wins) and carries expiresAt", async () => {
    const { LoginIntent } = await import("../login-intent");
    const email = `${unique("overwrite")}@example.com`;

    await LoginIntent.upsert({
      email,
      code: "first",
      tierId: "tier-a",
      expiresAt: Date.now() + 60_000,
    }).go();
    await LoginIntent.upsert({
      email,
      code: "second",
      tierId: "tier-b",
      expiresAt: Date.now() + 120_000,
    }).go();

    const { data } = await LoginIntent.get({ email }).go();
    expect(data?.tierId).toBe("tier-b");
    expect(data?.code).toBe("second");
    expect(typeof data?.expiresAt).toBe("number");
  });

  it("derives the DynamoDB-native ttl attribute (epoch seconds) from expiresAt", async () => {
    const { LoginIntent } = await import("../login-intent");
    const email = `${unique("ttl")}@example.com`;
    const expiresAt = Date.now() + 15 * 60 * 1000;

    await LoginIntent.upsert({
      email,
      code: "demo",
      tierId: "demo-tier",
      expiresAt,
    }).go();

    const { data } = await LoginIntent.get({ email }).go();
    expect(data?.ttl).toBe(Math.floor(expiresAt / 1000));
  });
});
