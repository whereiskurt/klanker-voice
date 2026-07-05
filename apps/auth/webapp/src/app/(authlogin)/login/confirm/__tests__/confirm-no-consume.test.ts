import { describe, it, expect, vi, beforeEach } from "vitest";

/**
 * RED (Wave 0): asserts that rendering the net-new interstitial /login/confirm
 * page with only token+email query params does NOT consume the magic-link
 * token — i.e. it must not call/fetch the nodemailer callback during render.
 * Only an explicit human click on the page's confirm button may reach the
 * callback (AUTH-01, T-03-01).
 *
 * The page does not exist yet at this point in the port (Task 3 creates it)
 * — importing it below is expected to fail, which is the RED signal this
 * task's <verify> greps for.
 */

const fetchSpy = vi.fn();

beforeEach(() => {
  vi.clearAllMocks();
  // @ts-expect-error - minimal fetch shim for the assertion below
  global.fetch = fetchSpy;
});

describe("/login/confirm — interstitial does not auto-consume the magic-link token (AUTH-01, T-03-01)", () => {
  it("a bare GET/prefetch (token+email query params, no click) does not hit the callback", async () => {
    const mod = await import("@/app/(authlogin)/login/confirm/page");
    const Page = mod.default;

    const searchParams = Promise.resolve({
      token: "abc123",
      email: "user@example.com",
    });

    await Page({ searchParams } as any);

    expect(fetchSpy).not.toHaveBeenCalled();
  });
});
