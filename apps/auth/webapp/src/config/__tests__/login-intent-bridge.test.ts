import { describe, it, expect, beforeAll } from "vitest";

/**
 * Plan 03-02 Task 3: the email->token bridge (Pitfall 3) — the highest-risk
 * item in this plan. Proves that a first-time (never-before-seen) user with
 * a known code resolves to the correct tier, NOT no-access, once the
 * magic-link callback runs applyLoginIntentBridge().
 *
 * Backed by the same dynamodb-local `kmv-auth-electro` table as the entity
 * tests (see access-code-resolution.test.ts header comment).
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

describe("applyLoginIntentBridge (Pitfall 3 — the login->token bridge)", () => {
  it("a first-time user with a known code resolves to the correct tier, not no-access", async () => {
    const { LoginIntent } = await import("../../entities/login-intent");
    const { AccessCode } = await import("../../entities/access-code");
    const { upsertAuthProfile, getAuthProfile } = await import(
      "../../entities/auth-profile"
    );
    const { applyLoginIntentBridge } = await import("../login-intent-bridge");

    const code = unique("firsttime");
    const email = `${unique("newuser")}@example.com`;
    const userId = unique("user");

    // 1. Operator-created code exists (kv CLI, out of scope here).
    await AccessCode.create({
      code,
      tierId: "demo-tier",
      group: "conference",
      maxRedemptions: 10,
      redemptionCount: 0,
    }).go();

    // 2. POST /api/login resolved the code and wrote the pre-user intent.
    await LoginIntent.upsert({
      email,
      code,
      tierId: "demo-tier",
      group: "conference",
      expiresAt: Date.now() + 15 * 60 * 1000,
    }).go();

    // 3. Magic link clicked: auth.ts's jwt callback creates the brand-new
    // AuthProfile (no prior row for this userId) THEN applies the bridge —
    // this is the exact sequence the nodemailer branch performs.
    await upsertAuthProfile(userId, "email", { email });
    await applyLoginIntentBridge(userId, email);

    const profile = await getAuthProfile(userId);
    expect(profile?.activeTierId).toBe("demo-tier");
    expect(profile?.activeGroup).toBe("conference");
    expect(profile?.activeTierId).not.toBe("no-access");
  });

  it("stamps activeCode from intent.code (LEDG-01) so the token claim resolves to a real value", async () => {
    const { LoginIntent } = await import("../../entities/login-intent");
    const { AccessCode } = await import("../../entities/access-code");
    const { upsertAuthProfile, getAuthProfile } = await import(
      "../../entities/auth-profile"
    );
    const { applyLoginIntentBridge } = await import("../login-intent-bridge");

    const code = unique("ledgcode");
    const email = `${unique("ledguser")}@example.com`;
    const userId = unique("user");

    await AccessCode.create({
      code,
      tierId: "demo-tier",
      group: "conference",
      maxRedemptions: 10,
      redemptionCount: 0,
    }).go();
    await LoginIntent.upsert({
      email,
      code,
      tierId: "demo-tier",
      group: "conference",
      expiresAt: Date.now() + 15 * 60 * 1000,
    }).go();
    await upsertAuthProfile(userId, "email", { email });

    await applyLoginIntentBridge(userId, email);

    const profile = await getAuthProfile(userId);
    expect(profile?.activeCode).toBe(code);
  });

  it("records the redemption and increments AccessCode.redemptionCount once", async () => {
    const { LoginIntent } = await import("../../entities/login-intent");
    const { AccessCode } = await import("../../entities/access-code");
    const { CodeRedemption } = await import("../../entities/code-redemption");
    const { upsertAuthProfile } = await import("../../entities/auth-profile");
    const { applyLoginIntentBridge } = await import("../login-intent-bridge");

    const code = unique("redeem");
    const email = `${unique("redeemer")}@example.com`;
    const userId = unique("user");

    await AccessCode.create({ code, tierId: "demo-tier", redemptionCount: 0 }).go();
    await LoginIntent.upsert({
      email,
      code,
      tierId: "demo-tier",
      expiresAt: Date.now() + 60_000,
    }).go();
    await upsertAuthProfile(userId, "email", { email });

    await applyLoginIntentBridge(userId, email);

    const { data: redemption } = await CodeRedemption.get({ code, userId }).go();
    expect(redemption).toBeTruthy();

    const { data: accessCode } = await AccessCode.get({ code }).go();
    expect(accessCode?.redemptionCount).toBe(1);
  });

  it("consumes (deletes) the login_intent so it cannot be reapplied", async () => {
    const { LoginIntent } = await import("../../entities/login-intent");
    const { AccessCode } = await import("../../entities/access-code");
    const { upsertAuthProfile } = await import("../../entities/auth-profile");
    const { applyLoginIntentBridge } = await import("../login-intent-bridge");

    const code = unique("consume");
    const email = `${unique("consumer")}@example.com`;
    const userId = unique("user");

    await AccessCode.create({ code, tierId: "demo-tier" }).go();
    await LoginIntent.upsert({
      email,
      code,
      tierId: "demo-tier",
      expiresAt: Date.now() + 60_000,
    }).go();
    await upsertAuthProfile(userId, "email", { email });

    await applyLoginIntentBridge(userId, email);

    const { data: intent } = await LoginIntent.get({ email }).go();
    expect(intent).toBeFalsy();
  });

  it("an expired login_intent is not applied (AuthProfile stays no-access)", async () => {
    const { LoginIntent } = await import("../../entities/login-intent");
    const { AccessCode } = await import("../../entities/access-code");
    const { upsertAuthProfile, getAuthProfile } = await import(
      "../../entities/auth-profile"
    );
    const { applyLoginIntentBridge } = await import("../login-intent-bridge");

    const code = unique("expiredintent");
    const email = `${unique("laggard")}@example.com`;
    const userId = unique("user");

    await AccessCode.create({ code, tierId: "demo-tier" }).go();
    await LoginIntent.upsert({
      email,
      code,
      tierId: "demo-tier",
      expiresAt: Date.now() - 60_000, // already expired
    }).go();
    await upsertAuthProfile(userId, "email", { email });

    await applyLoginIntentBridge(userId, email);

    const profile = await getAuthProfile(userId);
    expect(profile?.activeTierId).toBe("no-access");
  });

  it("no-op when there is no login_intent for the email", async () => {
    const { upsertAuthProfile, getAuthProfile } = await import(
      "../../entities/auth-profile"
    );
    const { applyLoginIntentBridge } = await import("../login-intent-bridge");

    const email = `${unique("nointent")}@example.com`;
    const userId = unique("user");

    await upsertAuthProfile(userId, "email", { email });
    await applyLoginIntentBridge(userId, email);

    const profile = await getAuthProfile(userId);
    // Default activeTierId ("no-access") from the AuthProfile entity — the
    // bridge must not throw or fabricate a tier when no intent exists.
    expect(profile?.activeTierId).toBe("no-access");
  });

  it("latest-wins: re-entering a different code before clicking overwrites the pending intent's tier", async () => {
    const { LoginIntent } = await import("../../entities/login-intent");
    const { AccessCode } = await import("../../entities/access-code");
    const { upsertAuthProfile, getAuthProfile } = await import(
      "../../entities/auth-profile"
    );
    const { applyLoginIntentBridge } = await import("../login-intent-bridge");

    const codeA = unique("first-code");
    const codeB = unique("second-code");
    const email = `${unique("upgrader")}@example.com`;
    const userId = unique("user");

    await AccessCode.create({ code: codeA, tierId: "tier-a" }).go();
    await AccessCode.create({ code: codeB, tierId: "tier-b" }).go();

    await LoginIntent.upsert({
      email,
      code: codeA,
      tierId: "tier-a",
      expiresAt: Date.now() + 60_000,
    }).go();
    // User re-enters a better code before clicking the first magic link.
    await LoginIntent.upsert({
      email,
      code: codeB,
      tierId: "tier-b",
      expiresAt: Date.now() + 60_000,
    }).go();

    await upsertAuthProfile(userId, "email", { email });
    await applyLoginIntentBridge(userId, email);

    const profile = await getAuthProfile(userId);
    expect(profile?.activeTierId).toBe("tier-b");
  });
});
