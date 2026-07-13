/**
 * S3 read helper for the /admin transcripts report (Plan 15-05, LEDG-03).
 *
 * Reads the newline-JSON ledger objects the voice service's LedgerWriter
 * writes (apps/voice/src/klanker_voice/ledger.py, Plan 15-02) directly from
 * S3 via ListObjectsV2 + GetObject over the auth task's read-only IAM grant.
 * Athena stays the out-of-band ad-hoc surface — this module is the
 * request-path reader for the threaded conversation view.
 *
 * Credentials mirror entities/client.ts verbatim (the load-bearing
 * gotcha): Next.js `output: 'standalone'` bundling drops the AWS SDK's
 * default (dynamically-required) provider chain, so `fromNodeProviderChain`
 * MUST be statically imported and used explicitly — otherwise the ECS
 * task-role credentials never resolve in production
 * ("Resolved credential object is not valid"). Locally, explicit
 * AUTH_LEDGER_ID/AUTH_LEDGER_SECRET still win (env-override escape hatch,
 * same shape as AUTH_DYNAMODB_ID/SECRET).
 */
import {
  GetObjectCommand,
  ListObjectsV2Command,
  S3Client,
} from "@aws-sdk/client-s3";
import { fromNodeProviderChain } from "@aws-sdk/credential-providers";

const ledgerCreds =
  process.env.AUTH_LEDGER_ID && process.env.AUTH_LEDGER_SECRET
    ? {
        credentials: {
          accessKeyId: process.env.AUTH_LEDGER_ID,
          secretAccessKey: process.env.AUTH_LEDGER_SECRET,
        },
      }
    : { credentials: fromNodeProviderChain() };

export const s3Client = new S3Client({
  ...ledgerCreds,
  region: process.env.AWS_REGION,
});

/** Ledger bucket name (env-derived; mirrors entities/client.ts's ELECTRO_TABLE constant). */
export const LEDGER_BUCKET = process.env.LEDGER_BUCKET || "";

/**
 * One ledger turn record. Field-for-field mirror of
 * `klanker_voice.ledger.LEDGER_FIELDS` (apps/voice, Plan 15-02) — do not
 * reorder/rename without updating that tuple (and the Plan 15-04 Athena DDL)
 * in lockstep (schema-drift guard).
 */
export interface LedgerRecord {
  role: "user" | "assistant";
  text: string;
  email: string | null;
  caller_id: string | null;
  did: string | null;
  ts: number;
  session_id: string;
  turn_seq: number;
  code_hash: string | null;
  tier_id: string | null;
  channel: string;
  interrupted: boolean;
}

export interface SessionSummary {
  sessionId: string;
  day: string;
  /** Earliest HHMMSS (UTC, from the object key) seen for this session. */
  firstSeenHms: string;
  /** Number of batch objects this session has for the day. */
  objectCount: number;
}

// Object key shape written by LedgerWriter.flush() (apps/voice ledger.py):
//   ledger/dt=<day>/<HHMMSS>Z-<session_id>-<batch_seq:04d>.jsonl
// session_id is typically a uuid4 and MAY itself contain hyphens, so the
// middle capture is greedy — only the trailing 4-digit batch sequence and
// leading 6-digit time are anchored.
const KEY_PATTERN = /^(\d{6})Z-(.+)-(\d{4})\.jsonl$/;

function parseKey(filename: string): { hms: string; sessionId: string } | null {
  const match = KEY_PATTERN.exec(filename);
  if (!match) return null;
  return { hms: match[1], sessionId: match[2] };
}

async function listDayKeys(day: string): Promise<string[]> {
  const prefix = `ledger/dt=${day}/`;
  const keys: string[] = [];
  let continuationToken: string | undefined;
  do {
    const res = await s3Client.send(
      new ListObjectsV2Command({
        Bucket: LEDGER_BUCKET,
        Prefix: prefix,
        ContinuationToken: continuationToken,
      })
    );
    for (const obj of res.Contents ?? []) {
      if (obj.Key) keys.push(obj.Key);
    }
    continuationToken = res.IsTruncated ? res.NextContinuationToken : undefined;
  } while (continuationToken);
  return keys;
}

/**
 * Lists distinct sessions for a day by deriving session ids from object
 * KEYS ALONE (ListObjectsV2 only) — never reads object bodies.
 */
export async function listSessions(day: string): Promise<SessionSummary[]> {
  const prefix = `ledger/dt=${day}/`;
  const keys = await listDayKeys(day);
  const sessions = new Map<string, SessionSummary>();
  for (const key of keys) {
    const filename = key.startsWith(prefix) ? key.slice(prefix.length) : key;
    const parsed = parseKey(filename);
    if (!parsed) continue; // malformed/unexpected key — skip, not fatal
    const existing = sessions.get(parsed.sessionId);
    if (existing) {
      existing.objectCount += 1;
      if (parsed.hms < existing.firstSeenHms) existing.firstSeenHms = parsed.hms;
    } else {
      sessions.set(parsed.sessionId, {
        sessionId: parsed.sessionId,
        day,
        firstSeenHms: parsed.hms,
        objectCount: 1,
      });
    }
  }
  return Array.from(sessions.values()).sort((a, b) =>
    a.firstSeenHms.localeCompare(b.firstSeenHms)
  );
}

/**
 * Reads one session's turns for a day: GetObjects the session's keys,
 * parses each newline-JSON line, filters to `sessionId`, and returns
 * records sorted by `turn_seq` ascending — NEVER `ts` (turns can share a
 * wall-clock second; turn_seq is the LOCKED ordering rule). A malformed
 * line is skipped, never fatal.
 */
export async function readSession(
  sessionId: string,
  day: string
): Promise<LedgerRecord[]> {
  const allKeys = await listDayKeys(day);
  const sessionKeys = allKeys.filter((key) => key.includes(`-${sessionId}-`));

  const records: LedgerRecord[] = [];
  for (const key of sessionKeys) {
    const res = await s3Client.send(
      new GetObjectCommand({ Bucket: LEDGER_BUCKET, Key: key })
    );
    const body = await res.Body?.transformToString();
    if (!body) continue;
    for (const line of body.split("\n")) {
      const trimmed = line.trim();
      if (!trimmed) continue;
      let record: LedgerRecord;
      try {
        record = JSON.parse(trimmed) as LedgerRecord;
      } catch {
        continue; // malformed line — skip, not fatal
      }
      if (record.session_id === sessionId) records.push(record);
    }
  }
  records.sort((a, b) => a.turn_seq - b.turn_seq);
  return records;
}
