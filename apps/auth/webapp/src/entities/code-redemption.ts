import { Entity } from "electrodb";
import { electroClient, ELECTRO_TABLE } from "./client";

/**
 * CodeRedemption Entity — unique-user redemption marker (D-06, AUTH-04,
 * T-03-06).
 *
 * `.create()` is conditional in ElectroDB by default (it adds an
 * `attribute_not_exists(pk)` condition to the underlying PutItem) — callers
 * rely on THAT to gate the `AccessCode.redemptionCount` increment (Task 3):
 * attempt `CodeRedemption.create({code, userId})`, and only increment the
 * count when that create succeeds. A repeat login by the same user on the
 * same code throws (conditional-fail), so the count is unique-users, not
 * login events, even under concurrent duplicate requests.
 *
 * Keyed on `code`, NOT `tierId` — two codes sharing a tier must not share a
 * redemption count (03-RESEARCH.md Open Question 2, resolved).
 *
 * Key template: pk "redemption#${code}"  sk "user#${userId}"
 */
export const CodeRedemption = new Entity(
  {
    model: {
      entity: "CodeRedemption",
      version: "1",
      service: "kmv",
    },
    attributes: {
      code: {
        type: "string",
        required: true,
        set: (val?: string) => String(val ?? "").trim().toLowerCase(),
      },
      userId: {
        type: "string",
        required: true,
      },
      redeemedAt: {
        type: "number",
        default: () => Date.now(),
        readOnly: true,
      },
    },
    indexes: {
      primary: {
        pk: { field: "pk", composite: ["code"], template: "redemption#${code}" },
        sk: {
          field: "sk",
          composite: ["userId"],
          template: "user#${userId}",
        },
      },
    },
  },
  { client: electroClient, table: ELECTRO_TABLE }
);
