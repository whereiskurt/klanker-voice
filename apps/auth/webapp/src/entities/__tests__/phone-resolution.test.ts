import { describe, it, expect, beforeAll } from "vitest";

/**
 * Plan 12-02 Task 2: the §23 caller-ID -> code -> tier mint path
 * (12-CONTEXT.md D-02, 12-RESEARCH.md "The §23 Mint Path"). Asserts the
 * `phone` attribute + sparse `byPhone` gsi3 + `resolvePhoneToCode` against a
 * real dynamodb-local `kmv-auth-electro` table — same real-table pattern as
 * access-code-resolution.test.ts (Phase 3), extended to the gsi3 index.
 *
 * Local backend: dynamodb-local on http://localhost:8888 (see 03-01
 * user_setup + from-aws.tmpl AUTH_ELECTRO_ENDPOINT), table `kmv-auth-electro`.
 * Each test uses a unique, timestamp-suffixed code + phone so runs don't
 * collide with leftover items.
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

function uniquePhone(): string {
  // Unique 10-digit North-American-shaped local number -> unique canonical
  // +1XXXXXXXXXX after normalization, so parallel test runs never collide.
  const digits = String(Date.now()).slice(-7).padStart(7, "0");
  return `416${digits}`;
}

describe("resolvePhoneToCode (§23 caller-ID mint, no-oracle contract)", () => {
  it("a phone-mapped, phone-enabled code resolves to its tier", async () => {
    const { AccessCode, resolvePhoneToCode } = await import("../access-code");
    const code = uniqueCode("phone-demo");
    const phone = uniquePhone();
    await AccessCode.create({
      code,
      tierId: "phone-tier",
      group: "pstn",
      phone,
      phoneEnabled: true,
    }).go();

    const result = await resolvePhoneToCode(`+1${phone}`);
    expect(result).toEqual({ code, tierId: "phone-tier", group: "pstn" });
  });

  it("write-time normalization: a messy dashed/spaced phone is stored canonical and found by a canonical lookup", async () => {
    const { AccessCode, resolvePhoneToCode } = await import("../access-code");
    const code = uniqueCode("phone-messy");
    const phone = uniquePhone();
    const messy = `(${phone.slice(0, 3)}) ${phone.slice(3, 6)}-${phone.slice(6)}`;
    await AccessCode.create({
      code,
      tierId: "messy-tier",
      phone: messy,
      phoneEnabled: true,
    }).go();

    const result = await resolvePhoneToCode(`+1${phone}`);
    expect(result).toEqual({ code, tierId: "messy-tier", group: null });
  });

  it("returns null for empty/blank input without querying a live match", async () => {
    const { resolvePhoneToCode } = await import("../access-code");
    expect(await resolvePhoneToCode("")).toBeNull();
    expect(await resolvePhoneToCode(null)).toBeNull();
    expect(await resolvePhoneToCode(undefined)).toBeNull();
  });

  it("returns null for an unmapped number", async () => {
    const { resolvePhoneToCode } = await import("../access-code");
    const neverMapped = uniquePhone();
    expect(await resolvePhoneToCode(`+1${neverMapped}`)).toBeNull();
  });

  it("returns null when the code's phone mapping is disabled", async () => {
    const { AccessCode, resolvePhoneToCode } = await import("../access-code");
    const code = uniqueCode("phone-disabled");
    const phone = uniquePhone();
    await AccessCode.create({
      code,
      tierId: "disabled-tier",
      phone,
      phoneEnabled: false,
    }).go();

    expect(await resolvePhoneToCode(`+1${phone}`)).toBeNull();
  });

  it("returns null when the phone-mapped code is expired", async () => {
    const { AccessCode, resolvePhoneToCode } = await import("../access-code");
    const code = uniqueCode("phone-expired");
    const phone = uniquePhone();
    await AccessCode.create({
      code,
      tierId: "expired-tier",
      phone,
      phoneEnabled: true,
      expiresAt: Date.now() - 60_000,
    }).go();

    expect(await resolvePhoneToCode(`+1${phone}`)).toBeNull();
  });

  it("a phone-less code is not indexed on byPhone (sparse GSI)", async () => {
    const { AccessCode } = await import("../access-code");
    const code = uniqueCode("no-phone");
    await AccessCode.create({ code, tierId: "some-tier" }).go();

    const { data } = await AccessCode.query.byPhone({ phone: "+19999999999" }).go();
    expect(data.find((r) => r.code === code)).toBeUndefined();
  });
});
