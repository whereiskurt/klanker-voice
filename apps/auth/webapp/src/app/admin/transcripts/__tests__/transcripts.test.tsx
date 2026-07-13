import { describe, it, expect, vi, beforeAll, beforeEach } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";

/**
 * Plan 15-05 Task 3 (LEDG-03 acceptance bar): the threaded chat detail page
 * + session list page. Mocks @/lib/ledger entirely (Task 1's own
 * ledger.test.ts already covers the S3 read helper itself) and renders the
 * resolved server-component JSX with react-dom/server's
 * renderToStaticMarkup — no jsdom/testing-library needed for a pure
 * server-rendered read view.
 */

const readSessionMock = vi.fn();
const listSessionsMock = vi.fn();

vi.mock("@/lib/ledger", () => ({
  readSession: readSessionMock,
  listSessions: listSessionsMock,
}));

let DetailPage: typeof import("../[sessionId]/page").default;
let ListPage: typeof import("../page").default;

beforeAll(async () => {
  ({ default: DetailPage } = await import("../[sessionId]/page"));
  ({ default: ListPage } = await import("../page"));
});

beforeEach(() => {
  readSessionMock.mockReset();
  listSessionsMock.mockReset();
});

function record(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    role: "user",
    text: "hi",
    email: null,
    caller_id: null,
    did: null,
    ts: 1752350400,
    session_id: "sess-1",
    turn_seq: 1,
    code_hash: null,
    tier_id: null,
    channel: "webrtc",
    interrupted: false,
    ...overrides,
  };
}

describe("/admin/transcripts/[sessionId] — threaded chat detail (LEDG-03 acceptance bar)", () => {
  it("renders turns in turn_seq order (out-of-order input, sorted output)", async () => {
    // readSession's own contract already sorts by turn_seq (Task 1); this
    // proves the page renders whatever order it's handed, in that order —
    // out-of-order here would be a real regression if the page re-sorted
    // wrongly or reordered by array position instead of turn_seq.
    readSessionMock.mockResolvedValue([
      record({ turn_seq: 1, text: "first" }),
      record({ turn_seq: 2, role: "assistant", text: "second" }),
      record({ turn_seq: 3, text: "third" }),
    ]);

    const jsx = await DetailPage({
      params: Promise.resolve({ sessionId: "sess-1" }),
      searchParams: Promise.resolve({ dt: "2026-07-13" }),
    });
    const html = renderToStaticMarkup(jsx as any);

    const firstIdx = html.indexOf("first");
    const secondIdx = html.indexOf("second");
    const thirdIdx = html.indexOf("third");
    expect(firstIdx).toBeGreaterThan(-1);
    expect(secondIdx).toBeGreaterThan(firstIdx);
    expect(thirdIdx).toBeGreaterThan(secondIdx);
  });

  it("renders transcript text as escaped React children — a script-bearing utterance never renders as markup", async () => {
    readSessionMock.mockResolvedValue([
      record({ turn_seq: 1, text: "<script>alert(1)</script>" }),
    ]);

    const jsx = await DetailPage({
      params: Promise.resolve({ sessionId: "sess-1" }),
      searchParams: Promise.resolve({ dt: "2026-07-13" }),
    });
    const html = renderToStaticMarkup(jsx as any);

    expect(html).not.toContain("<script>alert(1)</script>");
    expect(html).toContain("&lt;script&gt;");
  });

  it("produces alternating user/assistant bubbles keyed off role", async () => {
    readSessionMock.mockResolvedValue([
      record({ turn_seq: 1, role: "user", text: "hello" }),
      record({ turn_seq: 2, role: "assistant", text: "hi there" }),
    ]);

    const jsx = await DetailPage({
      params: Promise.resolve({ sessionId: "sess-1" }),
      searchParams: Promise.resolve({ dt: "2026-07-13" }),
    });
    const html = renderToStaticMarkup(jsx as any);

    expect(html).toContain("flex-end"); // user bubble
    expect(html).toContain("flex-start"); // assistant bubble
  });

  it("marks an interrupted assistant turn visibly", async () => {
    readSessionMock.mockResolvedValue([
      record({ turn_seq: 1, role: "assistant", text: "cut off", interrupted: true }),
    ]);

    const jsx = await DetailPage({
      params: Promise.resolve({ sessionId: "sess-1" }),
      searchParams: Promise.resolve({ dt: "2026-07-13" }),
    });
    const html = renderToStaticMarkup(jsx as any);

    expect(html.toLowerCase()).toContain("interrupted");
  });

  it("shows a friendly empty state when the session has no turns", async () => {
    readSessionMock.mockResolvedValue([]);

    const jsx = await DetailPage({
      params: Promise.resolve({ sessionId: "sess-empty" }),
      searchParams: Promise.resolve({ dt: "2026-07-13" }),
    });
    const html = renderToStaticMarkup(jsx as any);

    expect(html).toContain("No turns found");
  });
});

describe("/admin/transcripts list page", () => {
  it("lists sessions for the day, each linking to its detail page and labeled with participant + turn count", async () => {
    listSessionsMock.mockResolvedValue([
      { sessionId: "sess-1", day: "2026-07-13", firstSeenHms: "120000", objectCount: 1 },
    ]);
    readSessionMock.mockResolvedValue([
      record({ turn_seq: 1, email: "dad@example.com" }),
      record({ turn_seq: 2, role: "assistant", text: "reply" }),
    ]);

    const jsx = await ListPage({ searchParams: Promise.resolve({ dt: "2026-07-13" }) });
    const html = renderToStaticMarkup(jsx as any);

    expect(html).toContain("/admin/transcripts/sess-1");
    expect(html).toContain("dad@example.com");
    expect(html).toContain("2 turns");
  });

  it("falls back to caller_id, then 'anonymous', when no email is present", async () => {
    listSessionsMock.mockResolvedValue([
      { sessionId: "sess-pstn", day: "2026-07-13", firstSeenHms: "130000", objectCount: 1 },
      { sessionId: "sess-anon", day: "2026-07-13", firstSeenHms: "140000", objectCount: 1 },
    ]);
    readSessionMock.mockImplementation(async (sessionId: string) => {
      if (sessionId === "sess-pstn") {
        return [record({ session_id: "sess-pstn", turn_seq: 1, caller_id: "+16135551234" })];
      }
      return [record({ session_id: "sess-anon", turn_seq: 1 })];
    });

    const jsx = await ListPage({ searchParams: Promise.resolve({ dt: "2026-07-13" }) });
    const html = renderToStaticMarkup(jsx as any);

    expect(html).toContain("+16135551234");
    expect(html).toContain("anonymous");
  });

  it("shows a friendly empty state when there are no sessions for the day", async () => {
    listSessionsMock.mockResolvedValue([]);

    const jsx = await ListPage({ searchParams: Promise.resolve({ dt: "2026-07-13" }) });
    const html = renderToStaticMarkup(jsx as any);

    expect(html).toContain("No sessions for 2026-07-13");
  });
});
