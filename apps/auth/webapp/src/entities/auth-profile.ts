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
