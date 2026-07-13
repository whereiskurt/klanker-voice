import { describe, it, expect, beforeAll } from "vitest";

/**
 * Plan 15-01 Task 2 (LEDG-01): `setActiveTier`'s new, additive, optional
 * fourth `code` parameter — stamps `activeCode` onto AuthProfile alongside
 * `activeTierId`/`activeGroup`, same latest-wins (D-05) semantics. Real
 * dynamodb-local backend, same convention as tier-and-login-intent.test.ts /
 * access-code-resolution.test.ts.
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

describe("setActiveTier — activeCode stamp (LEDG-01)", () => {
  it("setActiveTier(userId, tierId, group, code) writes activeCode alongside activeTierId/activeGroup", async () => {
    const { setActiveTier, getAuthProfile, upsertAuthProfile } = await import(
      "../auth-profile"
    );
    const userId = unique("user");
    await upsertAuthProfile(userId, "email", { email: `${userId}@example.com` });

    await setActiveTier(userId, "demo-tier", "conference", "kphdemo123");

    const profile = await getAuthProfile(userId);
    expect(profile?.activeTierId).toBe("demo-tier");
    expect(profile?.activeGroup).toBe("conference");
    expect(profile?.activeCode).toBe("kphdemo123");
  });

  it("code = undefined/null leaves activeCode unset (no spurious overwrite) — same posture as activeGroup", async () => {
    const { setActiveTier, getAuthProfile, upsertAuthProfile } = await import(
      "../auth-profile"
    );
    const userId = unique("user-nocode");
    await upsertAuthProfile(userId, "email", { email: `${userId}@example.com` });

    await setActiveTier(userId, "demo-tier", undefined, undefined);
    let profile = await getAuthProfile(userId);
    expect(profile?.activeTierId).toBe("demo-tier");
    expect(profile?.activeCode).toBeUndefined();

    await setActiveTier(userId, "demo-tier", "conference", null);
    profile = await getAuthProfile(userId);
    expect(profile?.activeCode).toBeUndefined();
  });

  it("latest-wins: a later stamp with a new code overwrites the prior activeCode", async () => {
    const { setActiveTier, getAuthProfile, upsertAuthProfile } = await import(
      "../auth-profile"
    );
    const userId = unique("user-relogin");
    await upsertAuthProfile(userId, "email", { email: `${userId}@example.com` });

    await setActiveTier(userId, "tier-a", "group-a", "code-a");
    await setActiveTier(userId, "tier-b", "group-b", "code-b");

    const profile = await getAuthProfile(userId);
    expect(profile?.activeCode).toBe("code-b");
  });
});
