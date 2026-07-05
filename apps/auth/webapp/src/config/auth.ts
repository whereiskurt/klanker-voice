import { DynamoDBAdapter } from "@auth/dynamodb-adapter";
import { DynamoDB, DynamoDBClientConfig } from "@aws-sdk/client-dynamodb";
import { DynamoDBDocument } from "@aws-sdk/lib-dynamodb";
import { SESv2Client, SendEmailCommand } from "@aws-sdk/client-sesv2";
import type { Provider } from "next-auth/providers";

import { createTransport } from "nodemailer";

import NextAuth, { type DefaultSession } from "next-auth";
import { upsertAuthProfile, getAuthProfile } from "@/entities/auth-profile";
import { config } from "@/config";

declare module "next-auth" {
  interface Session {
    user: {
      id: string;
      displayName?: string;
      services: string[];
    } & DefaultSession["user"];
  }
  interface User {
    services?: string[];
  }
}

declare module "@auth/core/jwt" {
  interface JWT {
    userId: string;
    displayName?: string;
    services: string[];
  }
}

import "@auth/core/jwt"; // Import the module augmentation
import Email from "@auth/core/providers/nodemailer";

// DynamoDB client configuration
const dynamoConfig: DynamoDBClientConfig = {
  credentials: config.dynamodb.credentials,
  region: config.dynamodb.region,
  ...(config.dynamodb.endpoint ? { endpoint: config.dynamodb.endpoint } : {}),
};

const client = DynamoDBDocument.from(new DynamoDB(dynamoConfig), {
  marshallOptions: {
    convertEmptyValues: true,
    removeUndefinedValues: true,
    convertClassInstanceToMap: true,
  },
});

const adapter = DynamoDBAdapter(client, {
  tableName: config.dynamodb.tableName,
});

// SES client configuration
const sesClient = new SESv2Client({
  ...(config.ses.credentials ? { credentials: config.ses.credentials } : {}),
  region: config.ses.region,
});

// Single provider (D-09): email-only magic-link login. run.auth's five
// Discord/GitHub/Strava OAuth providers (and their run.* client config) are
// dropped for klanker-voice — the voice concierge only needs an email
// identity gated by an access code (Phase 3 Plan 02).
const providers: Provider[] = [
  Email({
    server: {}, // Required by nodemailer provider, but unused since we use custom sendVerificationRequest
    from: config.ses.from,
    async sendVerificationRequest({
      identifier: email,
      url,
      provider: { from },
      theme,
    }) {
      const { host, searchParams: params } = new URL(url);

      const token = params.get("token")!;

      const transport = createTransport({
        SES: { sesClient, SendEmailCommand },
      });
      await transport.sendMail({
        from,
        to: email,
        subject: `${token}`, //this subject value enables click through on iOS!
        html: signupHTML({
          url,
          host,
          theme,
          email: email.replace("+", "%2B"),
          token,
        }),
        text: signupText({ url }),
      });
    },
    async generateVerificationToken() {
      const alphabet = "0123456789";
      return `${randomString(3, alphabet)}${randomString(3, alphabet)}`;
    },
  }),
];

const randomString = (length: number, alphabet: string): string =>
  Array.from(
    { length },
    () => alphabet[Math.floor(Math.random() * alphabet.length)]
  ).join("");

// Cookie options helper
const cookieOptions = (httpOnly: boolean) => ({
  domain: config.auth.cookieDomain,
  path: "/",
  httpOnly,
  sameSite: "lax" as const,
  secure: config.auth.secureCookies,
});

export const { handlers, signIn, signOut, auth } = NextAuth({
  // debug: true,
  trustHost: true,
  basePath: config.auth.basePath,
  session: {
    strategy: "jwt",
    maxAge: config.session.maxAge,
    updateAge: config.session.updateAge,
  },
  theme: {
    colorScheme: "dark",
  },
  secret: config.auth.jwtSecret,
  providers,
  adapter,
  pages: {
    signIn: config.urls.loginPage,
    verifyRequest: config.urls.verifyPage,
  },
  callbacks: {
    signIn({ user, profile }) {
      const emails = config.auth.allowedEmails;
      const email = user?.email ?? profile?.email!;
      if (
        !emails ||
        emails[0] === "" ||
        emails[0] === "all" ||
        emails?.includes(email)
      ) {
        return true;
      }

      console.log(
        `SECURITY: Blocked email address ${email!} from login ${JSON.stringify(
          emails
        )}.`
      );
      return false;
    },

    async jwt({ token, account, trigger, session, user }) {
      if (trigger === "update") {
        // token.theme = session.user.theme;
      } else if (account && account.provider === "nodemailer") {
        // There is no ${profile} for nodemailer.
        // Persist email profile to AuthProfile entity
        const userId = (typeof user?.id === "string" && user.id)
          || (typeof token.sub === "string" && token.sub)
          || (typeof token.userId === "string" && token.userId);
        if (userId && token.email) {
          upsertAuthProfile(userId, "email", {
            email: token.email as string,
          }).catch((err) => console.error("Failed to upsert email profile:", err));
        }
      }

      // Fetch services from AuthProfile and store in token
      // This runs on every JWT refresh, so services will be updated
      const userId = (typeof user?.id === "string" && user.id)
        || (typeof token.sub === "string" && token.sub)
        || (typeof token.userId === "string" && token.userId);
      if (userId) {
        try {
          const profile = await getAuthProfile(userId);
          // Use profile services if available, otherwise keep existing token services or default to empty
          token.services = profile?.services ?? token.services ?? [];
          // Store the rabbit displayName in the token
          token.displayName = profile?.displayName ?? token.displayName;
        } catch (err) {
          console.error("Failed to fetch services for token:", err);
          // Keep existing services on error
          token.services = token.services ?? [];
        }
      } else {
        token.services = token.services ?? [];
      }

      return token;
    },

    session({ session, token }) {
      session.user.id = (token.sub ?? token.userId) as string;
      session.user.email = token.email as string;
      session.user.displayName = token.displayName as string | undefined;
      session.user.name = session.user.displayName;
      session.user.services = (token.services ?? []) as string[];
      return session;
    },
  },
  cookies: {
    sessionToken: {
      name: config.cookies.session.name,
      options: cookieOptions(true),
    },
    csrfToken: {
      name: config.cookies.csrf.name,
      options: cookieOptions(false),
    },
    callbackUrl: {
      name: config.cookies.callback.name,
      options: cookieOptions(false),
    },
  },
});

// AUTH-01 / T-03-01: the primary button targets the net-new interstitial
// /login/confirm page (src/app/(authlogin)/login/confirm/page.tsx) — NOT the
// direct-consumption /api/auth/callback/nodemailer URL. A bare GET/prefetch
// of /login/confirm by a corporate link-scanner renders the confirm button
// only; it does NOT touch the callback, so the one-time token is never
// consumed until the human explicitly clicks through. The numeric one-time
// code fallback (typed into /login/verify) is unaffected.
export function signupHTML(params: {
  url: any;
  host: any;
  theme: any;
  email: string;
  token: string;
}) {
  const { host, theme, token, email } = params;
  const url = `${config.urls.baseUrl}/login/confirm?token=${token}&email=${email}`;
  const escapedHost = host.replace(/\./g, "&#8203;.");

  const brandColor = "#686EA0";
  const color = {
    background: "#f9f9f9",
    text: "#444",
    mainBackground: "#fff",
    buttonBackground: brandColor,
    buttonBorder: brandColor,
    buttonText: theme.buttonText || "#fff",
  };

  return `
  <body style="background: ${color.background};">
    <table width="100%" border="0" cellspacing="10" cellpadding="0"
      style="background: ${color.mainBackground}; max-width: 600px; margin: auto; border-radius: 10px;">
      <tr>
        <td align="center"
          style="padding: 0px 0px; font-size: 22px; font-family: Helvetica, Arial, sans-serif; color: ${color.text};">
          <strong>KlankerMaker Concierge</strong>
        </td>
      </tr>
      <tr>
        <td align="center"
          style="padding: 0px 0px 10px 0px; font-size: 16px; line-height: 22px; font-family: Helvetica, Arial, sans-serif; color: ${color.text};">
          To complete your sign-in click:
        </td>
      </tr>
      <tr>
        <td align="center" style="padding: 0px 0;">
          <table border="0" cellspacing="0" cellpadding="0">
            <tr>
              <td align="center" style="border-radius: 5px;" bgcolor="${color.buttonBackground}"><a href="${url}"
                  target="_blank"
                  style="font-size: 22px; font-family: Helvetica, Arial, sans-serif; color: ${color.buttonText}; text-decoration: none; border-radius: 5px; padding: 10px 50px; border: 1px solid ${color.buttonBorder}; display: inline-block; font-weight: bold;">🚀 Sign-in</a></td>
            </tr>
          </table>
        </td>
      </tr>

      <tr>
        <td align="center"
          style="padding: 0px 0px 10px 0px; font-size: 16px; line-height: 22px; font-family: Helvetica, Arial, sans-serif; color: ${color.text};">
          <p>Or! Copy & paste this one time code into app:</p><p style="font-size: 22px;"><strong>${token}</strong></p>
        </td>
      </tr>

      <tr>
        <td align="center"
          style="padding: 0px 0px 10px 0px; font-size: 16px; line-height: 22px; font-family: Helvetica, Arial, sans-serif; color: ${color.text};">
          If you did not request this email you can safely ignore it.
        </td>
      </tr>
    </table>
  </body>
  `;
}
export function signupText(params: { url: any }) {
  const { url } = params;
  return `Complete your sign in to ${config.siteDomain} with this URL:\n${url}\n\n`;
}
