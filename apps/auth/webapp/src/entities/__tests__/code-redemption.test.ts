import { describe, it, expect, beforeAll } from "vitest";

/**
 * RED (Plan 03-02 Task 1): asserts unique-user redemption counting (D-06,
 * AUTH-04, T-03-06) — `CodeRedemption` does not exist yet, so every import
 * below fails (the RED signal this task's <verify> greps for).
 *
 * Same dynamodb-local backend as access-code-resolution.test.ts.
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

/**
 * Mirrors the increment-on-new-redemption-only pattern Task 3 wires into
 * auth.ts's jwt callback: attempt the conditional CodeRedemption.create();
 * only increment AccessCode.redemptionCount when that create succeeds.
 */
async function attemptRedeem(code: string, userId: string) {
  const { CodeRedemption } = await import("../code-redemption");
  const { AccessCode } = await import("../access-code");
  try {
    await CodeRedemption.create({ code, userId }).go();
    await AccessCode.patch({ code }).add({ redemptionCount: 1 }).go();
  } catch {
    // conditional-fail on a repeat (code,userId) redemption — skip increment
  }
}

describe("CodeRedemption — unique-user counting (D-06, AUTH-04)", () => {
  it("same (code,userId) applied twice increments redemptionCount exactly once", async () => {
    const { AccessCode } = await import("../access-code");
    const code = uniqueCode("uniq-same-user");
    await AccessCode.create({ code, tierId: "t1", redemptionCount: 0 }).go();

    await attemptRedeem(code, "user-1");
    await attemptRedeem(code, "user-1"); // repeat login, same user

    const { data } = await AccessCode.get({ code }).go();
    expect(data?.redemptionCount).toBe(1);
  });

  it("two distinct userIds on the same code increment redemptionCount to 2", async () => {
    const { AccessCode } = await import("../access-code");
    const code = uniqueCode("uniq-two-users");
    await AccessCode.create({ code, tierId: "t1", redemptionCount: 0 }).go();

    await attemptRedeem(code, "user-a");
    await attemptRedeem(code, "user-b");

    const { data } = await AccessCode.get({ code }).go();
    expect(data?.redemptionCount).toBe(2);
  });

  it("CodeRedemption.create is conditional — a duplicate (code,userId) throws", async () => {
    const { CodeRedemption } = await import("../code-redemption");
    const code = uniqueCode("dupe-throws");
    await CodeRedemption.create({ code, userId: "u1" }).go();

    await expect(
      CodeRedemption.create({ code, userId: "u1" }).go()
    ).rejects.toThrow();
  });
});
