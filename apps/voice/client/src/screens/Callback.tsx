import { useEffect, useState } from "react";
import { clearReturningUser, markReturningUser } from "../auth/returningStore";
import { consumeStashedPkce } from "../auth/useAuth";
import { exchangeCode } from "../auth/oidcClient";
import { getOidcConfig } from "../config/oidc";
import { setToken } from "../auth/tokenStore";
import "./callback.css";

export interface CallbackProps {
  /** Called once the code exchange has succeeded and the token is in memory. */
  onAuthenticated: () => void;
}

/**
 * The OIDC authorization-code+PKCE callback route (CLNT-08, D-04): reads
 * `code`+`state` from the query string, validates `state` against the
 * value stashed before the redirect, exchanges the code (no client secret),
 * stores the access token in memory, then routes onward. `role`/`aria-live`
 * (05-07 hardening fix -- this screen previously had NO live-region at
 * all) mirror every other status/error screen's own pattern: `status`/
 * `polite` while signing in, `alert`/`assertive` once the exchange fails.
 */
export default function Callback({ onAuthenticated }: CallbackProps) {
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;

    async function run() {
      // Bypass /join auto-login (2026-07-10-bypass-join-login-design): the auth
      // app's /join route mints an anonymous OIDC token and 302-redirects here
      // with it in the URL FRAGMENT (#access_token=...&anon=1). When present,
      // ingest it directly and fully short-circuit the PKCE code exchange —
      // there is no `code`/`state`/verifier round-trip for this path. The
      // fragment (unlike a query string) never reaches the server or a Referer
      // header, keeping the bearer token out of access logs.
      const rawHash = window.location.hash.startsWith("#")
        ? window.location.hash.slice(1)
        : window.location.hash;
      const bypassToken = new URLSearchParams(rawHash).get("access_token");
      if (bypassToken) {
        try {
          setToken(bypassToken); // throws if the token isn't a well-formed JWT
          markReturningUser();
          // Scrub the token from the address bar / history before proceeding.
          window.history.replaceState({}, "", "/");
          if (!cancelled) onAuthenticated();
        } catch {
          if (!cancelled) setError("Sign-in didn't complete. Please try again.");
        }
        return;
      }

      const params = new URLSearchParams(window.location.search);
      const errorParam = params.get("error");
      if (errorParam === "login_required" || errorParam === "interaction_required") {
        // Silent SSO (Workstream A) found no live issuer session — expected
        // "session expired", not a failure. Clear the breadcrumb so a
        // signed-out device doesn't re-loop the silent attempt (T-05.2-01-D).
        clearReturningUser();
        if (!cancelled) { window.history.replaceState({}, "", "/"); onAuthenticated(); }
        return;
      }

      const code = params.get("code");
      const returnedState = params.get("state");
      const stashed = consumeStashedPkce();

      if (!code || !returnedState || !stashed || stashed.state !== returnedState) {
        if (!cancelled) setError("Sign-in didn't complete — the request expired or was tampered with.");
        return;
      }

      try {
        const { accessToken } = await exchangeCode(getOidcConfig(), {
          code,
          verifier: stashed.verifier,
        });
        setToken(accessToken);
        markReturningUser();
        if (!cancelled) onAuthenticated();
      } catch {
        if (!cancelled) setError("Sign-in didn't complete. Please try again.");
      }
    }

    void run();
    return () => {
      cancelled = true;
    };
    // Runs exactly once per mount — this screen only ever exists for the
    // single callback round-trip.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div className="callback" role={error ? "alert" : "status"} aria-live={error ? "assertive" : "polite"}>
      <p className="callback-status">{error ?? "Signing you in…"}</p>
      {error ? (
        <button
          type="button"
          className="callback-retry"
          onClick={() => {
            window.location.assign("/");
          }}
        >
          Back to start
        </button>
      ) : null}
    </div>
  );
}
