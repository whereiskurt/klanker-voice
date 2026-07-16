import { describe, it, expect, afterEach, beforeAll, vi } from "vitest";

/**
 * Quick task 260716-hg5 follow-up: POST /ctf/sms internal SMS relay no-oracle
 * contract. Mirrors ctf-otp-route.test.ts — env set/clear per test, dynamic
 * import, uniform-404 for every failure. `fetch` is mocked so no real VoIP.ms
 * request is made.
 */

let POST: typeof import("../sms/route").POST;

function makeRequest(
  body: unknown,
  headers: Record<string, string> = {},
  { malformed = false }: { malformed?: boolean } = {},
): any {
  return {
    headers: { get: (n: string) => headers[n] ?? headers[n.toLowerCase()] ?? null },
    json: async () => {
      if (malformed) throw new Error("bad json");
      return body;
    },
  };
}

/** A fetch stub returning the given VoIP.ms JSON envelope, recording the URL. */
function stubFetch(status: string, { httpOk = true }: { httpOk?: boolean } = {}) {
  const calls: string[] = [];
  const fn = vi.fn(async (url: string) => {
    calls.push(url);
    return {
      ok: httpOk,
      status: httpOk ? 200 : 500,
      json: async () => ({ status }),
    } as any;
  });
  vi.stubGlobal("fetch", fn);
  return { calls, fn };
}

const VALID_BODY = { to: "5197101515", message: "CTF proof code: 482913 - go", dids: ["6134805878"] };

beforeAll(async () => {
  ({ POST } = await import("../sms/route"));
});

describe("POST /ctf/sms (internal SMS relay, no-oracle contract)", () => {
  afterEach(() => {
    delete process.env.CTF_OTP_AUTH_TOKEN;
    delete process.env.VOIPMS_API_USERNAME;
    delete process.env.VOIPMS_API_PASSWORD;
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  function withCreds() {
    process.env.VOIPMS_API_USERNAME = "u";
    process.env.VOIPMS_API_PASSWORD = "p";
  }

  it("a successful relay returns 200 { sent: true }", async () => {
    withCreds();
    const { calls } = stubFetch("success");
    const res = await POST(makeRequest(VALID_BODY));
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({ sent: true });
    expect(calls).toHaveLength(1);
    expect(calls[0]).toContain("method=sendSMS");
  });

  it("missing VoIP.ms creds returns 404 without any fetch", async () => {
    const { fn } = stubFetch("success");
    const res = await POST(makeRequest(VALID_BODY));
    expect(res.status).toBe(404);
    expect(fn).not.toHaveBeenCalled();
  });

  it("a VoIP.ms rejection (ip_not_enabled) returns the uniform 404", async () => {
    withCreds();
    stubFetch("ip_not_enabled");
    const res = await POST(makeRequest(VALID_BODY));
    expect(res.status).toBe(404);
  });

  it("tries each DID in order until one succeeds (auto-fallback)", async () => {
    withCreds();
    const calls: string[] = [];
    // first DID fails, second succeeds
    const fn = vi.fn(async (url: string) => {
      calls.push(url);
      const status = url.includes("did=222") ? "success" : "ip_not_enabled";
      return { ok: true, status: 200, json: async () => ({ status }) } as any;
    });
    vi.stubGlobal("fetch", fn);
    const res = await POST(makeRequest({ ...VALID_BODY, dids: ["111", "222", "333"] }));
    expect(res.status).toBe(200);
    expect(calls).toHaveLength(2); // stopped after the first success; "333" untried
    expect(calls[0]).toContain("did=111");
    expect(calls[1]).toContain("did=222");
  });

  it("a malformed body / missing fields returns 404", async () => {
    withCreds();
    stubFetch("success");
    expect((await POST(makeRequest(VALID_BODY, {}, { malformed: true }))).status).toBe(404);
    expect((await POST(makeRequest({ to: "5197101515", message: "x" }))).status).toBe(404); // no dids
    expect((await POST(makeRequest({ message: "x", dids: ["6134805878"] }))).status).toBe(404); // no to
  });

  it("when the bearer env is set, a missing/wrong bearer returns the SAME 404", async () => {
    withCreds();
    process.env.CTF_OTP_AUTH_TOKEN = "shared-token";
    stubFetch("success");
    expect((await POST(makeRequest(VALID_BODY))).status).toBe(404); // missing bearer
    expect(
      (await POST(makeRequest(VALID_BODY, { authorization: "Bearer wrong" }))).status,
    ).toBe(404);
    expect(
      (await POST(makeRequest(VALID_BODY, { authorization: "Bearer shared-token" }))).status,
    ).toBe(200); // correct bearer
  });

  it("never logs the destination, message, or credentials", async () => {
    withCreds();
    stubFetch("ip_not_enabled"); // force the failure log path
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    await POST(makeRequest(VALID_BODY));
    const logged = warn.mock.calls.flat().join(" ");
    expect(logged).toContain("ip_not_enabled"); // diagnostic enum IS surfaced
    expect(logged).not.toContain("5197101515"); // destination NOT logged
    expect(logged).not.toContain("482913"); // message/OTP NOT logged
    expect(logged).not.toContain("6134805878"); // sending DID NOT logged
  });
});
