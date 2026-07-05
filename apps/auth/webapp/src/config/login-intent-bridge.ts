import { setActiveTier } from "@/entities/auth-profile";
import { LoginIntent, normalizeEmail } from "@/entities/login-intent";
import { CodeRedemption } from "@/entities/code-redemption";
import { AccessCode } from "@/entities/access-code";

/**
 * Consume the email-keyed login_intent written at POST /api/login and stamp
 * its resolved tier onto the now-known user's AuthProfile (03-RESEARCH.md
 * Pattern 3, Pitfall 3 — THE bridge that makes a first-time user's known
 * code resolve to the correct tier instead of no-access).
 *
 * Extracted into its own module (mirroring the `load-existing-grant.ts`
 * precedent from Plan 01) so it can be unit-tested directly against
 * dynamodb-local without needing to construct the full NextAuth() config
 * (DynamoDBAdapter, SESv2Client, nodemailer transport, etc.).
 *
 * Called from the nodemailer branch of auth.ts's jwt() callback, where
 * `userId` and `token.email` are both known for the first time — including
 * the very first login of a brand-new user (the case Pitfall 3 warns about:
 * stamping AuthProfile by userId at /api/login is impossible because no
 * userId exists yet at that point).
 */
export async function applyLoginIntentBridge(
  userId: string,
  email: string
): Promise<void> {
  const normalizedEmail = normalizeEmail(email);
  const { data: intent } = await LoginIntent.get({
    email: normalizedEmail,
  }).go();
  if (!intent) return;

  // Belt-and-suspenders in-app expiry check alongside DynamoDB's TTL sweep
  // (which is not immediate) — never apply a stale, never-clicked intent.
  if (intent.expiresAt && intent.expiresAt < Date.now()) {
    await LoginIntent.delete({ email: normalizedEmail })
      .go()
      .catch(() => {});
    return;
  }

  await setActiveTier(userId, intent.tierId, intent.group);

  // Unique-user redemption count (D-06, T-03-06): only increment
  // AccessCode.redemptionCount when this (code, userId) redemption is
  // genuinely new — CodeRedemption.create() is conditional and throws on a
  // repeat, so concurrent duplicate logins by the same user cannot
  // double-count.
  if (intent.code) {
    try {
      await CodeRedemption.create({ code: intent.code, userId }).go();
      await AccessCode.patch({ code: intent.code })
        .add({ redemptionCount: 1 })
        .go();
    } catch {
      // conditional-fail: already redeemed by this user -> skip increment
    }
  }

  await LoginIntent.delete({ email: normalizedEmail })
    .go()
    .catch((err) =>
      console.error("Failed to delete consumed login_intent:", err)
    );
}
