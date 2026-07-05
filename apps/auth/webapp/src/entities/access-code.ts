import { Entity } from "electrodb";
import { electroClient, ELECTRO_TABLE } from "./client";

/**
 * AccessCode Entity
 *
 * Operator-managed access codes (Phase 3 Plan 02, D-04/D-07). A code carries
 * a tier + group, an optional expiry, and an optional unique-user redemption
 * cap. Resolution (expiry + cap enforcement, unknown/blank -> no-access) is
 * the `resolveAccessCode` helper below — the boundary AUTH-03/AUTH-04 hang
 * off of.
 *
 * Case policy (03-RESEARCH.md Open Question 3, resolved): codes are
 * normalized lowercase+trim on write (the `set` transform here, and by the
 * Plan 04 `kv` CLI which writes raw items directly) so "Demo" and "demo"
 * never diverge.
 *
 * Explicit index `template`s (Pattern 4, 03-RESEARCH.md) make the DynamoDB
 * key strings trivially reproducible by Plan 04's Go `kv` CLI without
 * reverse-engineering ElectroDB's default composed-key format (de-risks
 * Pitfall 1 / T-03-10).
 *
 * FINAL key templates — Plan 04's kv CLI MUST reproduce these byte-for-byte:
 *   primary pk: "code#${code}"      sk: "code#"
 *   gsi1      pk: "accesscodes#"    sk: "code#${code}"
 */
export const AccessCode = new Entity(
  {
    model: {
      entity: "AccessCode",
      version: "1",
      service: "kmv",
    },
    attributes: {
      code: {
        type: "string",
        required: true,
        set: (val?: string) => normalizeCode(val),
      },
      tierId: {
        type: "string",
        required: true,
      },
      group: {
        type: "string",
      },
      // Epoch ms. Undefined = never expires.
      expiresAt: {
        type: "number",
      },
      // Unique users, not login events (D-06). Undefined = unlimited.
      maxRedemptions: {
        type: "number",
      },
      redemptionCount: {
        type: "number",
        default: 0,
      },
      createdAt: {
        type: "number",
        default: () => Date.now(),
        readOnly: true,
      },
    },
    indexes: {
      primary: {
        pk: { field: "pk", composite: ["code"], template: "code#${code}" },
        sk: { field: "sk", composite: [], template: "code#" },
      },
      // GSI for `kv code list` (electro-schema table has gsi1..gsi3).
      all: {
        index: "gsi1pk-gsi1sk-index",
        pk: { field: "gsi1pk", composite: [], template: "accesscodes#" },
        sk: { field: "gsi1sk", composite: ["code"], template: "code#${code}" },
      },
    },
  },
  { client: electroClient, table: ELECTRO_TABLE }
);

export const NO_ACCESS_TIER_ID = "no-access";

export interface ResolvedAccessCode {
  tierId: string;
  group: string | null;
}

function normalizeCode(code: string | null | undefined): string {
  return String(code ?? "")
    .trim()
    .toLowerCase();
}

/**
 * Resolve a raw invite-code string entered at login into a tier (AUTH-03,
 * AUTH-04). Blank, unknown, expired, or at/over-cap codes ALL resolve to the
 * no-access tier uniformly — no oracle distinguishes "unknown" from "expired"
 * from "capped" to the caller (T-03-07: no brute-force/enumeration signal).
 *
 * This function does NOT decide whether login proceeds — it never throws for
 * a bad code. The caller (POST /api/login) always proceeds to signIn()
 * regardless of this result (D-07).
 */
export async function resolveAccessCode(
  inviteCode: string | null | undefined
): Promise<ResolvedAccessCode> {
  const normalized = normalizeCode(inviteCode);
  if (!normalized) {
    return { tierId: NO_ACCESS_TIER_ID, group: null };
  }

  const { data: record } = await AccessCode.get({ code: normalized }).go();
  if (!record) {
    return { tierId: NO_ACCESS_TIER_ID, group: null };
  }

  const notExpired = !record.expiresAt || record.expiresAt > Date.now();
  const underCap =
    record.maxRedemptions == null ||
    (record.redemptionCount ?? 0) < record.maxRedemptions;

  if (!notExpired || !underCap) {
    return { tierId: NO_ACCESS_TIER_ID, group: null };
  }

  return { tierId: record.tierId, group: record.group ?? null };
}
