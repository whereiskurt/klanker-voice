import { describe, it, expect, vi, beforeAll, beforeEach } from "vitest";

/**
 * Plan 15-05 Task 1 (LEDG-03): lib/ledger.ts's S3 read helper. Mocks
 * @aws-sdk/client-s3's send() (env-first-then-dynamic-import pattern per
 * app/tel/__tests__/tel-route.test.ts) — no live S3 needed, hermetic.
 */

const { sendMock, ctorArgs } = vi.hoisted(() => ({
  sendMock: vi.fn(),
  ctorArgs: [] as any[],
}));

vi.mock("@aws-sdk/client-s3", () => {
  class ListObjectsV2Command {
    input: any;
    constructor(input: any) {
      this.input = input;
    }
  }
  class GetObjectCommand {
    input: any;
    constructor(input: any) {
      this.input = input;
    }
  }
  class S3Client {
    send = sendMock;
    constructor(config: any) {
      ctorArgs.push(config);
    }
  }
  return { S3Client, ListObjectsV2Command, GetObjectCommand };
});

vi.mock("@aws-sdk/credential-providers", () => ({
  fromNodeProviderChain: vi.fn(() => ({ marker: "fromNodeProviderChain" })),
}));

let ledger: typeof import("../ledger");
let ListObjectsV2Command: any;
let GetObjectCommand: any;

beforeAll(async () => {
  process.env.LEDGER_BUCKET = "test-ledger-bucket";
  process.env.AWS_REGION = process.env.AWS_REGION || "us-east-1";

  ({ ListObjectsV2Command, GetObjectCommand } = await import("@aws-sdk/client-s3"));
  ledger = await import("../ledger");
});

beforeEach(() => {
  sendMock.mockReset();
});

describe("lib/ledger.ts — S3 credential chain (T-15-05-04)", () => {
  it("constructs the S3Client with an explicit credentials provider (fromNodeProviderChain) by default", () => {
    expect(ctorArgs.length).toBeGreaterThan(0);
    const config = ctorArgs[0];
    expect(config.credentials).toEqual({ marker: "fromNodeProviderChain" });
  });
});

describe("lib/ledger.ts — listSessions() (LEDG-03)", () => {
  it("calls ListObjectsV2 with Prefix ledger/dt=<day>/ and derives the distinct session_id set from object keys, without reading bodies", async () => {
    sendMock.mockImplementation(async (command: any) => {
      if (command instanceof ListObjectsV2Command) {
        expect(command.input.Bucket).toBe("test-ledger-bucket");
        expect(command.input.Prefix).toBe("ledger/dt=2026-07-13/");
        return {
          Contents: [
            { Key: "ledger/dt=2026-07-13/120000Z-7f3c1a2b-aaaa-4bbb-8ccc-111122223333-0000.jsonl" },
            { Key: "ledger/dt=2026-07-13/120500Z-7f3c1a2b-aaaa-4bbb-8ccc-111122223333-0001.jsonl" },
            { Key: "ledger/dt=2026-07-13/130000Z-9e9e9e9e-1111-2222-3333-444455556666-0000.jsonl" },
          ],
          IsTruncated: false,
        };
      }
      throw new Error(`unexpected command in listSessions: ${command?.constructor?.name}`);
    });

    const sessions = await ledger.listSessions("2026-07-13");

    expect(sessions.map((s) => s.sessionId).sort()).toEqual(
      [
        "7f3c1a2b-aaaa-4bbb-8ccc-111122223333",
        "9e9e9e9e-1111-2222-3333-444455556666",
      ].sort()
    );

    const multiObjSession = sessions.find(
      (s) => s.sessionId === "7f3c1a2b-aaaa-4bbb-8ccc-111122223333"
    );
    expect(multiObjSession?.objectCount).toBe(2);
    expect(multiObjSession?.firstSeenHms).toBe("120000");

    // Never reads bodies: no GetObjectCommand ever sent.
    for (const call of sendMock.mock.calls) {
      expect(call[0]).not.toBeInstanceOf(GetObjectCommand);
    }
  });

  it("skips malformed/unexpected key shapes without throwing", async () => {
    sendMock.mockResolvedValue({
      Contents: [
        { Key: "ledger/dt=2026-07-13/not-a-ledger-key.txt" },
        { Key: "ledger/dt=2026-07-13/120000Z-sess-a-0000.jsonl" },
      ],
      IsTruncated: false,
    });

    const sessions = await ledger.listSessions("2026-07-13");
    expect(sessions.map((s) => s.sessionId)).toEqual(["sess-a"]);
  });
});

describe("lib/ledger.ts — readSession() (LEDG-03)", () => {
  it("GetObjects the session's keys, parses newline-JSON, filters sessionId, and sorts by turn_seq ascending (NOT ts)", async () => {
    sendMock.mockImplementation(async (command: any) => {
      if (command instanceof ListObjectsV2Command) {
        return {
          Contents: [
            { Key: "ledger/dt=2026-07-13/120000Z-sess-1-0000.jsonl" },
            { Key: "ledger/dt=2026-07-13/999999Z-sess-2-0000.jsonl" },
          ],
          IsTruncated: false,
        };
      }
      if (command instanceof GetObjectCommand) {
        if (command.input.Key === "ledger/dt=2026-07-13/120000Z-sess-1-0000.jsonl") {
          const lines = [
            JSON.stringify({
              role: "assistant",
              text: "second",
              email: null,
              caller_id: null,
              did: null,
              ts: 100,
              session_id: "sess-1",
              turn_seq: 2,
              code_hash: null,
              tier_id: null,
              channel: "webrtc",
              interrupted: false,
            }),
            JSON.stringify({
              role: "user",
              text: "first",
              email: null,
              caller_id: null,
              did: null,
              ts: 200, // deliberately LATER ts than turn_seq=2's row — turn_seq must win
              session_id: "sess-1",
              turn_seq: 1,
              code_hash: null,
              tier_id: null,
              channel: "webrtc",
              interrupted: false,
            }),
          ];
          return { Body: { transformToString: async () => lines.join("\n") + "\n" } };
        }
        // sess-2's object should never be fetched when reading sess-1.
        throw new Error("unexpected GetObject for a different session's key");
      }
      throw new Error(`unexpected command in readSession: ${command?.constructor?.name}`);
    });

    const records = await ledger.readSession("sess-1", "2026-07-13");
    expect(records.map((r) => r.turn_seq)).toEqual([1, 2]);
    expect(records.map((r) => r.text)).toEqual(["first", "second"]);
    expect(records.every((r) => r.session_id === "sess-1")).toBe(true);
  });

  it("skips a malformed line without failing the whole read", async () => {
    sendMock.mockImplementation(async (command: any) => {
      if (command instanceof ListObjectsV2Command) {
        return {
          Contents: [{ Key: "ledger/dt=2026-07-13/120000Z-sess-3-0000.jsonl" }],
          IsTruncated: false,
        };
      }
      const lines = [
        "{ this is not valid json {{{",
        JSON.stringify({
          role: "user",
          text: "still readable",
          email: null,
          caller_id: null,
          did: null,
          ts: 1,
          session_id: "sess-3",
          turn_seq: 1,
          code_hash: null,
          tier_id: null,
          channel: "webrtc",
          interrupted: false,
        }),
      ];
      return { Body: { transformToString: async () => lines.join("\n") } };
    });

    const records = await ledger.readSession("sess-3", "2026-07-13");
    expect(records).toHaveLength(1);
    expect(records[0].text).toBe("still readable");
  });

  it("filters out records belonging to a different session_id embedded in the same batch object", async () => {
    sendMock.mockImplementation(async (command: any) => {
      if (command instanceof ListObjectsV2Command) {
        return {
          Contents: [{ Key: "ledger/dt=2026-07-13/120000Z-sess-4-0000.jsonl" }],
          IsTruncated: false,
        };
      }
      const lines = [
        JSON.stringify({
          role: "user",
          text: "mine",
          email: null,
          caller_id: null,
          did: null,
          ts: 1,
          session_id: "sess-4",
          turn_seq: 1,
          code_hash: null,
          tier_id: null,
          channel: "webrtc",
          interrupted: false,
        }),
        JSON.stringify({
          role: "user",
          text: "not mine",
          email: null,
          caller_id: null,
          did: null,
          ts: 1,
          session_id: "sess-other",
          turn_seq: 1,
          code_hash: null,
          tier_id: null,
          channel: "webrtc",
          interrupted: false,
        }),
      ];
      return { Body: { transformToString: async () => lines.join("\n") } };
    });

    const records = await ledger.readSession("sess-4", "2026-07-13");
    expect(records).toHaveLength(1);
    expect(records[0].text).toBe("mine");
  });
});
