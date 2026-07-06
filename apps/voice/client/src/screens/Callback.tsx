import { useEffect, useState } from "react";
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
 * stores the access token in memory, then routes onward.
 */
export default function Callback({ onAuthenticated }: CallbackProps) {
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;

    async function run() {
      const params = new URLSearchParams(window.location.search);
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
    <div className="callback">
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
