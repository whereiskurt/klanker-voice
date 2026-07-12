import { Entity } from "electrodb";
import { electroClient, ELECTRO_TABLE } from "./client";
import { normalizeE164 } from "@/lib/phone-normalization";

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
 *   primary pk: "code#${code}"          sk: "code#"
 *   gsi1      pk: "accesscodes#"        sk: "code#${code}"
 *   gsi2      pk: "bypass#${bypassToken}" sk: "bypass#"   (SPARSE — bypass /join)
 *   gsi3      pk: "phone#${phone}"      sk: "phone#"      (SPARSE — §23 caller-ID mint)
 *
 * The gsi2 index (`byBypassToken`) powers the bypass /join auto-login feature
 * (2026-07-10-bypass-join-login-design). It is SPARSE: only codes that carry a
 * `bypassToken` get a gsi2pk and are therefore indexed — ElectroDB omits the
 * gsi2 key attributes entirely for codes whose optional `bypassToken` composite
 * is undefined, so bypass-less codes never appear in a byBypassToken query. The
 * kv CLI's `code bypass` command SETs gsi2pk/gsi2sk with these exact templates.
 *
 * The gsi3 index (`byPhone`) powers the §23 VoIP.ms caller-ID mint path
 * (Phase 12 Plan 02) — the EXACT same sparse-GSI pattern as gsi2, mirrored:
 * only codes that carry a `phone` get a gsi3pk and are therefore indexed.
 * `phone` is always written through `normalizeE164` (the `set` transform
 * below) so the stored key is always canonical, and `resolvePhoneToCode`
 * normalizes its input the same way before querying — a single normalization
 * source on both write and lookup (12-RESEARCH.md Pitfall 3).
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
      // Bypass /join auto-login (2026-07-10 design). When a code has bypass
      // enabled, its per-code `bypassToken` (a random base62 string minted by
      // `kv code bypass`) forms the gsi2 partition, and a GET to
      // /use1/join/<bypassToken> mints a short-lived anonymous OIDC token.
      // Undefined bypassToken -> the code is not indexed on gsi2 (sparse).
      bypassEnabled: {
        type: "boolean",
        default: false,
      },
      bypassToken: {
        type: "string",
      },
      // §23 VoIP.ms caller-ID mint (Phase 12 Plan 02). When a code has a
      // phone mapped (via `kv code phone <code> --add <e164>`), an inbound
      // PSTN call's normalized caller ID can resolve straight to this code's
      // tier through the sparse gsi3 `byPhone` index — mirrors bypassToken
      // above exactly. Undefined phone -> the code is not indexed on gsi3
      // (sparse). Always stored canonical via the `set` transform so a
      // messy write-time input (dashes/spaces/parens) is found by a
      // canonical lookup (12-RESEARCH.md Pitfall 3).
      phone: {
        type: "string",
        set: (val?: string) => (val ? normalizeE164(val) : val),
      },
      phoneEnabled: {
        type: "boolean",
        default: false,
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
      // Sparse GSI for the bypass /join lookup (resolveBypassToken). Only codes
      // with a `bypassToken` are indexed here — ElectroDB skips the gsi2 key
      // fields when the optional bypassToken composite is undefined. The gsi2
      // index name matches the electro-schema table's gsi2 (oidc-adapter.ts
      // uses the same "gsi2pk-gsi2sk-index").
      byBypassToken: {
        index: "gsi2pk-gsi2sk-index",
        pk: {
          field: "gsi2pk",
          // casing "none": bypassToken is a CASE-SIGNIFICANT opaque secret, and
          // the `kv code bypass` CLI writes gsi2pk raw (mixed case). ElectroDB
          // lowercases keys by DEFAULT, so without this the query for
          // "bypass#OQbBWkkDPBS5" would look up "bypass#oqbbwkkdpbs5" and never
          // match the item — the /join route would 404 every valid token.
          casing: "none",
          composite: ["bypassToken"],
          template: "bypass#${bypassToken}",
        },
        sk: { field: "gsi2sk", composite: [], template: "bypass#", casing: "none" },
      },
      // Sparse GSI for the §23 caller-ID mint lookup (resolvePhoneToCode).
      // Only codes with a `phone` are indexed here — mirrors byBypassToken
      // exactly (same table gsi3, same casing:"none" discipline so the
      // canonical "+1..." digit form is never lowercased/altered by
      // ElectroDB's default key casing).
      byPhone: {
        index: "gsi3pk-gsi3sk-index",
        pk: {
          field: "gsi3pk",
          casing: "none",
          composite: ["phone"],
          template: "phone#${phone}",
        },
        sk: { field: "gsi3sk", composite: [], template: "phone#", casing: "none" },
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

export function normalizeCode(code: string | null | undefined): string {
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

export interface ResolvedBypassCode {
  code: string;
  tierId: string;
  group: string | null;
}

/**
 * Resolve a bypass /join token (the per-code random string embedded in a
 * shareable auth.klankermaker.ai/use1/join/<token> URL) into the tier it
 * grants (2026-07-10 bypass-join design). Queries the sparse gsi2
 * `byBypassToken` index.
 *
 * Returns null UNIFORMLY for every failure mode — unknown token, a token whose
 * code has bypass disabled, or an expired code — so the /join route emits an
 * indistinguishable 404 in all cases and offers no enumeration oracle (mirrors
 * resolveAccessCode's no-oracle contract). maxRedemptions is deliberately NOT
 * enforced here: bypass sessions are anonymous per-visit, not unique-user
 * redemptions, so the redemption cap does not apply.
 */
export async function resolveBypassToken(
  token: string | null | undefined
): Promise<ResolvedBypassCode | null> {
  const bypassToken = String(token ?? "").trim();
  if (!bypassToken) {
    return null;
  }

  const { data } = await AccessCode.query.byBypassToken({ bypassToken }).go();
  const record = data?.[0];
  if (!record) {
    return null;
  }

  if (record.bypassEnabled !== true) {
    return null;
  }

  if (record.expiresAt && record.expiresAt <= Date.now()) {
    return null;
  }

  return { code: record.code, tierId: record.tierId, group: record.group ?? null };
}

export interface ResolvedPhoneCode {
  code: string;
  tierId: string;
  group: string | null;
}

/**
 * Resolve a normalized caller ID (§23 VoIP.ms inbound DID mint path, Phase 12
 * Plan 02) into the code/tier it grants. Queries the sparse gsi3 `byPhone`
 * index. Mirrors `resolveBypassToken` EXACTLY: normalizes the input via
 * `normalizeE164` first (so a raw or already-normalized caller ID both work),
 * then returns null UNIFORMLY for every failure mode — empty input, no
 * matching code, a phone-disabled code, or an expired code — so the private
 * `/tel` route can emit an indistinguishable 404 in every case and offers no
 * caller-ID enumeration oracle (T-12-02-01). maxRedemptions is deliberately
 * NOT enforced here, same rationale as resolveBypassToken: a caller-ID mint
 * is an anonymous per-call baseline identity, not a unique-user redemption.
 */
export async function resolvePhoneToCode(
  rawOrNormalizedPhone: string | null | undefined
): Promise<ResolvedPhoneCode | null> {
  const phone = normalizeE164(rawOrNormalizedPhone);
  if (!phone) {
    return null;
  }

  const { data } = await AccessCode.query.byPhone({ phone }).go();
  const record = data?.[0];
  if (!record) {
    return null;
  }

  if (record.phoneEnabled !== true) {
    return null;
  }

  if (record.expiresAt && record.expiresAt <= Date.now()) {
    return null;
  }

  return { code: record.code, tierId: record.tierId, group: record.group ?? null };
}
