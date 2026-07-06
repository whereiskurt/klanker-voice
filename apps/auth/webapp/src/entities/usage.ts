import { Entity } from "electrodb";
import { electroClient, ELECTRO_TABLE } from "./client";

/**
 * Usage Entities — race-safe quota bookkeeping for the voice service
 * (design spec §6; Phase-4 CONTEXT.md D-01/D-02/D-08/D-09/D-10).
 *
 * Four independent ElectroDB entities share the voice service's own table
 * (`kmv-voice-usage`, NOT `kmv-auth-electro` — see client note below) and
 * are grouped in this one file for discoverability. `apps/voice/src/
 * klanker_voice/quota.py` writes/reads these item shapes directly via boto3
 * (byte-compat discipline, same as `kv` <-> `tier.ts`/`client.ts`) — the pk/sk
 * template strings documented on each entity below are the CONTRACT quota.py
 * MUST reproduce byte-for-byte. `expiresAt` is the table's TTL attribute
 * (infra/terraform/live/site/services/voice/service.hcl: `ttl_attribute_name
 * = "expiresAt"`), which is what lets a crashed task's heartbeat lease
 * self-expire with no reaper process (D-01).
 *
 * NOTE: `electroClient`/`ELECTRO_TABLE` (from `./client`) point at the AUTH
 * service's table (`kmv-auth-electro`) by default. These entities are
 * defined here (co-located with `Tier`/`AccessCode` for a single TypeScript
 * schema source of truth across the two services) but are constructed
 * against the voice usage table name via the `AUTH_VOICE_USAGE_DBNAME` env
 * override below — they do NOT share physical storage with `Tier`.
 */

import { DynamoDBDocument } from "@aws-sdk/lib-dynamodb";
import { DynamoDB } from "@aws-sdk/client-dynamodb";

const voiceUsageEndpoint = process.env.AUTH_VOICE_USAGE_ENDPOINT;

// A dedicated client pointed at kmv-voice-usage (Phase 4's own table, not
// the auth service's kmv-auth-electro) — falls back to the shared
// electroClient's underlying DynamoDB config (region/credentials) so local
// dev only needs one set of AWS_* env vars, per the existing client.ts
// pattern.
const voiceUsageClient = process.env.AUTH_VOICE_USAGE_ID
  ? DynamoDBDocument.from(
      new DynamoDB({
        credentials: {
          accessKeyId: process.env.AUTH_VOICE_USAGE_ID!,
          secretAccessKey: process.env.AUTH_VOICE_USAGE_SECRET!,
        },
        region: process.env.AWS_REGION,
        ...(voiceUsageEndpoint ? { endpoint: voiceUsageEndpoint } : {}),
      }),
      {
        marshallOptions: {
          convertEmptyValues: true,
          removeUndefinedValues: true,
          convertClassInstanceToMap: true,
        },
      }
    )
  : electroClient; // dev/test fallback: reuse the auth electro client's AWS config

export const VOICE_USAGE_TABLE = process.env.AUTH_VOICE_USAGE_DBNAME || "kmv-voice-usage";

/**
 * Heartbeat lease — the D-01 concurrency slot. One item per active session;
 * a crashed task stops renewing and the item's `expiresAt` (TTL attribute)
 * lets DynamoDB reclaim it with no reaper. `quota.py`'s
 * `count_active_heartbeats(user_id)` queries all items under a user's pk and
 * counts those with `expiresAt > now` (belt) ahead of DynamoDB's TTL sweep
 * (suspenders) — mirrors the LoginIntent pattern from Phase 3.
 *
 * FINAL key templates — quota.py MUST reproduce these byte-for-byte:
 *   primary pk: "session#${userId}"      sk: "heartbeat#${sessionId}"
 */
export const UsageHeartbeat = new Entity(
  {
    model: {
      entity: "UsageHeartbeat",
      version: "1",
      service: "kmv",
    },
    attributes: {
      userId: { type: "string", required: true },
      sessionId: { type: "string", required: true },
      taskId: { type: "string" },
      acquiredAt: { type: "number", default: () => Date.now() },
      expiresAt: { type: "number", required: true }, // TTL attribute, epoch SECONDS
    },
    indexes: {
      primary: {
        pk: { field: "pk", composite: ["userId"], template: "session#${userId}" },
        sk: { field: "sk", composite: ["sessionId"], template: "heartbeat#${sessionId}" },
      },
    },
  },
  { client: voiceUsageClient, table: VOICE_USAGE_TABLE }
);

/**
 * Daily-per-user usage — durable `seconds_used` accounting, persisted by the
 * D-02 15s tick (durability, not the stop-clock authority — the in-memory
 * service timer owns the precise cutoff).
 *
 * FINAL key templates:
 *   primary pk: "user#${userId}"         sk: "day#${day}"   (day = yyyy-mm-dd)
 */
export const UsageDaily = new Entity(
  {
    model: {
      entity: "UsageDaily",
      version: "1",
      service: "kmv",
    },
    attributes: {
      userId: { type: "string", required: true },
      day: { type: "string", required: true }, // yyyy-mm-dd (UTC)
      secondsUsed: { type: "number", default: 0 },
      updatedAt: { type: "number", default: () => Date.now() },
    },
    indexes: {
      primary: {
        pk: { field: "pk", composite: ["userId"], template: "user#${userId}" },
        sk: { field: "sk", composite: ["day"], template: "day#${day}" },
      },
    },
  },
  { client: voiceUsageClient, table: VOICE_USAGE_TABLE }
);

/**
 * Global daily rollup — a single "today" item aggregating site-wide spend
 * (D-10): O(1) read for both `kv usage` (KV-03) and the D-09 auto-trip check
 * (no scan). `sessionCount` increments once per session (on its first tick,
 * not every tick); `estCost` is a coarse `seconds * est_cost_per_second`
 * estimate (CONTEXT.md "Deferred": finer cost attribution is out of scope).
 *
 * FINAL key templates:
 *   primary pk: "rollup#"                sk: "day#${day}"   (day = yyyy-mm-dd)
 */
export const UsageRollup = new Entity(
  {
    model: {
      entity: "UsageRollup",
      version: "1",
      service: "kmv",
    },
    attributes: {
      day: { type: "string", required: true },
      totalSeconds: { type: "number", default: 0 },
      sessionCount: { type: "number", default: 0 },
      estCost: { type: "number", default: 0 },
      updatedAt: { type: "number", default: () => Date.now() },
    },
    indexes: {
      primary: {
        pk: { field: "pk", composite: [], template: "rollup#" },
        sk: { field: "sk", composite: ["day"], template: "day#${day}" },
      },
    },
  },
  { client: voiceUsageClient, table: VOICE_USAGE_TABLE }
);

/**
 * Kill-switch control item — a single item read on every `/api/offer` start
 * gate (D-08, hot-path) and flipped by conditional write, either manually by
 * `kv killswitch` (KV-04) or automatically by the D-09 auto-trip check when
 * `UsageRollup` crosses `ceilingSeconds`/`ceilingDollars`.
 *
 * FINAL key templates:
 *   primary pk: "control#"               sk: "killswitch#"
 */
export const UsageControl = new Entity(
  {
    model: {
      entity: "UsageControl",
      version: "1",
      service: "kmv",
    },
    attributes: {
      engaged: { type: "boolean", default: false },
      reason: { type: "string" }, // e.g. "auto-trip" | "operator"
      ceilingSeconds: { type: "number" },
      ceilingDollars: { type: "number" },
      updatedAt: { type: "number", default: () => Date.now() },
    },
    indexes: {
      primary: {
        pk: { field: "pk", composite: [], template: "control#" },
        sk: { field: "sk", composite: [], template: "killswitch#" },
      },
    },
  },
  { client: voiceUsageClient, table: VOICE_USAGE_TABLE }
);

/** Convenience namespace grouping all four Usage item shapes. */
export const Usage = {
  Heartbeat: UsageHeartbeat,
  Daily: UsageDaily,
  Rollup: UsageRollup,
  Control: UsageControl,
};
