import { Entity } from "electrodb";
import { randomBytes } from "crypto";
import { electroClient, ELECTRO_TABLE } from "./client";

const DEFAULT_SERVICES = ["auth", "voice"];

/**
 * Generate a random displayName like "rabbit_A1B2"
 */
function generateDisplayName(): string {
  const hex = randomBytes(2).toString("hex").toUpperCase();
  return `rabbit_${hex}`;
}

/**
 * AuthProfile Entity
 *
 * Stores cached user profile information. This entity is managed by the
 * OIDC provider code and populated after successful Auth.js authentication.
 *
 * klanker-voice is Email-only (D-09): the run.auth Discord/GitHub/Strava
 * profile caches and the DEF CON quota tier field are dropped (D-11). Phase
 * 3 Plan 02 adds `activeTierId`/`activeGroup` here as the login->token
 * bridge (access-code resolution); Phase 4 owns usage/quota state.
 *
 * `activeTierId`/`activeGroup` are stamped by the LoginIntent bridge
 * (Task 3, auth.ts's nodemailer jwt callback) on EVERY login — latest-wins
 * (D-05). This is deliberately NOT a permanent per-user tier: re-entering a
 * different code at a later login overwrites both fields. Do NOT reuse
 * run.auth's DEF CON `quotaTier` pattern (a sticky per-user stamp) as the
 * tier source — that would violate D-05 (T-03-09).
 */
export const AuthProfile = new Entity(
  {
    model: {
      entity: "AuthProfile",
      version: "1",
      service: "oidc",
    },
    attributes: {
      // Primary identifier - Auth.js user ID
      userId: {
        type: "string",
        required: true,
      },
      // Generated displayName (e.g., "rabbit_A1B2")
      // Created on first login, never changes
      displayName: {
        type: "string",
      },
      // Primary email address
      email: {
        type: "string",
      },
      emailVerified: {
        type: "boolean",
        default: false,
      },
      // Display name (computed from best available source)
      name: {
        type: "string",
      },
      // Profile picture URL (computed from best available source)
      picture: {
        type: "string",
      },
      // Last provider used to login ("email" — the only provider ported)
      lastProvider: {
        type: "string",
      },
      // Authorized services for this user
      services: {
        type: "list",
        items: { type: "string" },
      },
      // Session invalidation fields
      // Increment sessionVersion to invalidate all existing sessions
      sessionVersion: {
        type: "number",
        default: 1,
      },
      // Lock user out completely - prevents new logins and invalidates sessions
      lockedOut: {
        type: "boolean",
        default: false,
      },
      // Optional: reason for lockout (for admin reference)
      lockoutReason: {
        type: "string",
      },
      // Optional: when the lockout was applied
      lockedAt: {
        type: "number",
      },
      // Access-code -> tier bridge (Phase 3 Plan 02, D-04/D-05/D-07).
      // Stamped by the LoginIntent bridge on every login; NOT permanent —
      // latest-wins, see class doc above. Defaults to "no-access" for
      // brand-new profiles until their first login_intent is applied.
      activeTierId: {
        type: "string",
        default: "no-access",
      },
      activeGroup: {
        type: "string",
      },
      // Redeemed access code (Phase 15 Plan 01, LEDG-01). Stamped by the
      // SAME LoginIntent bridge, on the SAME login, as activeTierId/
      // activeGroup above — latest-wins, identical D-05 semantics. Feeds the
      // `code` namespaced access-token claim so the voice service can
      // compute a salted code_hash for the transcription ledger without a
      // second DynamoDB round-trip.
      activeCode: {
        type: "string",
      },
      // Timestamps
      createdAt: {
        type: "number",
        default: () => Date.now(),
        readOnly: true,
      },
      updatedAt: {
        type: "number",
        default: () => Date.now(),
        watch: "*",
        set: () => Date.now(),
      },
    },
    indexes: {
      primary: {
        pk: { field: "pk", composite: ["userId"] },
        sk: { field: "sk", composite: [] },
      },
      byEmail: {
        index: "gsi1pk-gsi1sk-index",
        pk: { field: "gsi1pk", composite: ["email"] },
        sk: { field: "gsi1sk", composite: [] },
      },
    },
  },
  { client: electroClient, table: ELECTRO_TABLE }
);

/**
 * Create or update an AuthProfile from provider data
 */
export async function upsertAuthProfile(
  userId: string,
  provider: "email",
  data: {
    email?: string;
  }
): Promise<void> {
  // First try to get existing profile
  const existing = await AuthProfile.get({ userId }).go();

  const name: string | undefined = existing.data?.name;
  const picture: string | undefined = existing.data?.picture;

  // Build update payload
  const payload: Record<string, any> = {
    userId,
    lastProvider: provider,
    // Preserve existing services, or use default if this is a new profile
    ...(!existing.data ? { services: DEFAULT_SERVICES } : {}),
    // Generate displayName only on first login (new profile)
    ...(!existing.data ? { displayName: generateDisplayName() } : {}),
    ...(data.email ? { email: data.email, emailVerified: true } : {}),
    ...(name ? { name } : {}),
    ...(picture ? { picture } : {}),
  };

  await AuthProfile.upsert(payload).go();
}

/**
 * Get an AuthProfile by user ID
 */
export async function getAuthProfile(userId: string) {
  const result = await AuthProfile.get({ userId }).go();
  return result.data;
}

/**
 * Get an AuthProfile by email
 */
export async function getAuthProfileByEmail(email: string) {
  const result = await AuthProfile.query.byEmail({ email }).go();
  return result.data?.[0];
}

/**
 * Stamp the resolved tier/group from a consumed LoginIntent onto a user's
 * AuthProfile (the login->token bridge, Phase 3 Plan 02 Task 3). Latest-wins
 * by design (D-05) — this OVERWRITES any prior activeTierId/activeGroup.
 *
 * `code` (Phase 15 Plan 01, LEDG-01) is a fourth, optional, additive
 * parameter: the redeemed access code, stamped alongside the tier in the
 * SAME patch. `undefined`/`null` leaves activeCode unset rather than
 * overwriting it with an empty value — same `?? undefined` posture as
 * `activeGroup`. Never logged (raw code — T-15-01-03).
 */
export async function setActiveTier(
  userId: string,
  tierId: string,
  group: string | null | undefined,
  code?: string | null
): Promise<void> {
  await AuthProfile.patch({ userId })
    .set({ activeTierId: tierId, activeGroup: group ?? undefined, activeCode: code ?? undefined })
    .go();
}
