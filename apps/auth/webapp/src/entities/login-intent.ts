import { Entity } from "electrodb";
import { electroClient, ELECTRO_TABLE } from "./client";

/**
 * LoginIntent Entity — the email->token tier bridge (03-RESEARCH.md
 * Pattern 3, Pitfall 3).
 *
 * The access code is entered at POST /api/login BEFORE the user/token
 * exists (a brand-new user has no `userId` until the magic-link callback
 * creates them via the adapter). This entity carries the resolved
 * `{tierId, group, code}` across that gap, keyed by EMAIL (the one thing
 * both requests share). The nodemailer branch of auth.ts's `jwt` callback
 * consumes it once `userId` is known and stamps AuthProfile.activeTierId/
 * activeGroup (Task 3).
 *
 * Stores the resolved `code` (not just tierId) so the unique-user
 * redemption count in code-redemption.ts targets the right CODE, not the
 * tier — two codes sharing a tier must not share a redemption count
 * (03-RESEARCH.md Open Question 2, resolved).
 *
 * Latest-wins (D-05): `.upsert()` overwrites any prior intent for the same
 * email, so re-entering a different code before the first magic link is
 * clicked changes which tier the eventual login lands on.
 *
 * Two expiry mechanisms, same ~15min deadline:
 *  - `expiresAt` (epoch ms) — checked in-app by the jwt callback so a stale,
 *    never-clicked intent is never applied even if DynamoDB's TTL sweep
 *    (which is not immediate — up to ~48h in production, per AWS docs)
 *    hasn't run yet.
 *  - `ttl` (epoch SECONDS, derived from expiresAt via `watch`) — the
 *    DynamoDB-native TTL attribute so abandoned intents are eventually
 *    physically deleted without a cron job. Requires the table's TTL to be
 *    enabled with attribute name "ttl" (infra follow-up — see SUMMARY).
 *
 * Key template: pk "loginintent#${email}"  sk "loginintent#"
 */
export const LoginIntent = new Entity(
  {
    model: {
      entity: "LoginIntent",
      version: "1",
      service: "kmv",
    },
    attributes: {
      email: {
        type: "string",
        required: true,
        set: (val?: string) => String(val ?? "").trim().toLowerCase(),
      },
      // The resolved, normalized access code (may be "" if none entered).
      code: {
        type: "string",
      },
      tierId: {
        type: "string",
        required: true,
      },
      group: {
        type: "string",
      },
      // Epoch ms deadline; app-level check (belt) alongside DynamoDB TTL
      // (suspenders — see class doc above).
      expiresAt: {
        type: "number",
        required: true,
      },
      // DynamoDB-native TTL attribute, epoch SECONDS, derived from
      // expiresAt whenever it changes.
      ttl: {
        type: "number",
        watch: ["expiresAt"],
        set: (_val: number | undefined, item: any) =>
          item?.expiresAt
            ? Math.floor(item.expiresAt / 1000)
            : Math.floor(Date.now() / 1000) + 15 * 60,
      },
    },
    indexes: {
      primary: {
        pk: {
          field: "pk",
          composite: ["email"],
          template: "loginintent#${email}",
        },
        sk: { field: "sk", composite: [], template: "loginintent#" },
      },
    },
  },
  { client: electroClient, table: ELECTRO_TABLE }
);
