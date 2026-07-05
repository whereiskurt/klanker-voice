import { config } from "@/config";

const basePath = process.env.NODE_ENV === "production"
  ? `/${process.env.NEXT_PUBLIC_REGION_SHORT || "use1"}`
  : "";

type ConfirmSearchParams = {
  token?: string;
  email?: string;
};

/**
 * Interstitial magic-link confirm page (net-new, AUTH-01 / T-03-01).
 *
 * run.auth's magic-link email links directly to the token-consuming
 * `/api/auth/callback/nodemailer` GET route — a corporate link-scanner that
 * prefetches/follows links found in the email body burns the one-time token
 * before the human ever clicks it. This page breaks that chain: the email
 * (src/config/auth.ts signupHTML) now links HERE instead, and this page
 * does nothing but render a plain server-rendered `<form>` whose submit
 * button targets the real callback.
 *
 * Why this is safe against scanners:
 * - This is an async Server Component with NO client-side JS, NO useEffect,
 *   NO onLoad handler, and NO auto-submitting script. Rendering it (a bare
 *   GET/HEAD, or a prefetch that fetches the HTML) only ever produces this
 *   static form — it never itself issues a request to the callback route.
 * - The callback is only reached when a human explicitly clicks the
 *   "Confirm sign-in" submit button, which the browser turns into a *new*,
 *   separate GET request to `/api/auth/callback/nodemailer` — a request no
 *   automated scanner triggers, because scanners don't click buttons.
 *
 * The one-time numeric code fallback (typed into /login/verify) is
 * untouched by this change.
 */
export default async function ConfirmSignInPage({
  searchParams,
}: {
  searchParams: Promise<ConfirmSearchParams>;
}) {
  const { token, email } = await searchParams;
  const callbackAction = `${basePath}/api/auth/callback/nodemailer`;

  return (
    <div className="space-y-6 animate-fade-up">
      <div className="text-center space-y-2">
        <h1 className="font-museo text-4xl font-bold tracking-tight text-foreground">
          klanker<span className="teal-dot">.</span>voice
        </h1>
        <p className="font-mono text-xs text-default-400 tracking-widest uppercase">
          KlankerMaker Concierge
        </p>
      </div>

      <div className="glass-card overflow-hidden rounded-xl">
        <div className="space-y-4 px-5 py-5">
          <h2 className="font-museo text-lg font-bold text-foreground">
            Confirm sign-in
          </h2>
          <p className="text-sm text-default-500">
            {email
              ? <>Click below to finish signing in as <span className="font-mono text-foreground">{email}</span>.</>
              : <>Click below to finish signing in.</>}
          </p>

          {/*
            Plain HTML GET form — intentionally no client-side JavaScript.
            The browser only navigates to the callback action when the
            human submits this form (button click / Enter key).
          */}
          <form method="GET" action={callbackAction} className="pt-1">
            <input type="hidden" name="token" value={token ?? ""} />
            <input type="hidden" name="email" value={email ?? ""} />
            <input
              type="hidden"
              name="callbackUrl"
              value={config.urls.callbackPath}
            />
            <button
              type="submit"
              className="w-full rounded-lg bg-primary px-4 py-3 font-semibold text-primary-foreground transition-opacity hover:opacity-90"
            >
              Confirm sign-in
            </button>
          </form>
        </div>
      </div>
    </div>
  );
}
