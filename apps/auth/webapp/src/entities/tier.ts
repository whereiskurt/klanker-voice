import { Entity } from "electrodb";
import { electroClient, ELECTRO_TABLE } from "./client";

/**
 * Tier Entity
 *
 * Session/period/concurrency limits (D-01: the JWT access token carries only
 * `tier_id`+`group`; the voice service reads THIS table for the actual
 * limits at session start — thin token, tiers table is the single source of
 * truth, so editing a tier's numbers never requires re-issuing tokens).
 *
 * Fields per design spec §6: tier_id, session_max_seconds, period_max_seconds,
 * max_concurrent (camelCased here to match the rest of this codebase's
 * ElectroDB entities).
 *
 * FINAL key templates — Plan 04's kv CLI MUST reproduce these byte-for-byte:
 *   primary pk: "tier#${tierId}"   sk: "tier#"
 *   gsi1      pk: "tiers#"         sk: "tier#${tierId}"
 */
export const Tier = new Entity(
  {
    model: {
      entity: "Tier",
      version: "1",
      service: "kmv",
    },
    attributes: {
      tierId: {
        type: "string",
        required: true,
        set: (val?: string) => String(val ?? "").trim().toLowerCase(),
      },
      group: {
        type: "string",
      },
      sessionMaxSeconds: {
        type: "number",
      },
      periodMaxSeconds: {
        type: "number",
      },
      maxConcurrent: {
        type: "number",
      },
      createdAt: {
        type: "number",
        default: () => Date.now(),
        readOnly: true,
      },
    },
    indexes: {
      primary: {
        pk: { field: "pk", composite: ["tierId"], template: "tier#${tierId}" },
        sk: { field: "sk", composite: [], template: "tier#" },
      },
      all: {
        index: "gsi1pk-gsi1sk-index",
        pk: { field: "gsi1pk", composite: [], template: "tiers#" },
        sk: {
          field: "gsi1sk",
          composite: ["tierId"],
          template: "tier#${tierId}",
        },
      },
    },
  },
  { client: electroClient, table: ELECTRO_TABLE }
);
