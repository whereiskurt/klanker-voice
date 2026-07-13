import { describe, it, expect, vi, beforeAll, beforeEach } from "vitest";

/**
 * Plan 15-05 Task 2 (LEDG-03 gate, T-15-05-01): /admin's ADMIN_EMAILS
 * allowlist gate. Mocks @/config/auth's `auth` + next/navigation's
 * `notFound` (env-first-then-dynamic-import pattern per
 * app/tel/__tests__/tel-route.test.ts) — no real session/DynamoDB needed.
 *
 * The real notFound() throws a special Next.js digest to halt rendering; the
 * mock reproduces that "throws and halts" contract so the assertions prove
 * the SAME control-flow shape production gets (component execution stops
 * exactly at the gate check, before reaching the <html> shell).
 */

const authMock = vi.fn();
const notFoundMock = vi.fn(() => {
  throw new Error("NEXT_NOT_FOUND");
});

vi.mock("@/config/auth", () => ({ auth: authMock }));
vi.mock("next/navigation", () => ({ notFound: notFoundMock }));

let AdminLayout: typeof import("../layout").default;

beforeAll(async () => {
  ({ default: AdminLayout } = await import("../layout"));
});

beforeEach(() => {
  authMock.mockReset();
  notFoundMock.mockClear();
  delete process.env.ADMIN_EMAILS;
});

describe("/admin layout — ADMIN_EMAILS gate (LEDG-03, T-15-05-01)", () => {
  it("no session at all triggers notFound()", async () => {
    process.env.ADMIN_EMAILS = "admin@example.com";
    authMock.mockResolvedValue(null);

    await expect(AdminLayout({ children: null })).rejects.toThrow("NEXT_NOT_FOUND");
    expect(notFoundMock).toHaveBeenCalledTimes(1);
  });

  it("a session whose email is NOT in ADMIN_EMAILS triggers notFound()", async () => {
    process.env.ADMIN_EMAILS = "admin@example.com";
    authMock.mockResolvedValue({ user: { email: "someone-else@example.com" } });

    await expect(AdminLayout({ children: null })).rejects.toThrow("NEXT_NOT_FOUND");
    expect(notFoundMock).toHaveBeenCalledTimes(1);
  });

  it("a session whose email IS in ADMIN_EMAILS (case-insensitive) renders children — notFound() not called", async () => {
    process.env.ADMIN_EMAILS = "Admin@Example.com, other@example.com";
    authMock.mockResolvedValue({ user: { email: "admin@example.com" } });

    const result = await AdminLayout({ children: "hello" as any });

    expect(notFoundMock).not.toHaveBeenCalled();
    expect(result).toBeTruthy();
    expect((result as any).type).toBe("html");
  });

  it("an empty/unset ADMIN_EMAILS allowlists nobody", async () => {
    // process.env.ADMIN_EMAILS deliberately left unset (see beforeEach).
    authMock.mockResolvedValue({ user: { email: "whereiskurt@gmail.com" } });

    await expect(AdminLayout({ children: null })).rejects.toThrow("NEXT_NOT_FOUND");
    expect(notFoundMock).toHaveBeenCalledTimes(1);
  });
});
