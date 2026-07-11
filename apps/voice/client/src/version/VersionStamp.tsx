import { useSyncExternalStore } from "react";
import { APP_VERSION, APP_BUILT_AT } from "../version";
import { getServerVersion, subscribeServerVersion } from "./serverVersionStore";
import "./versionStamp.css";

/**
 * A faint, always-on build stamp in the top-left corner, present on every
 * screen so you can read the deployed version at a glance (mirrors the quiet
 * style of the KPH(v1)/KPH(v2) variant label). Mounted once at the React root
 * (main.tsx) so it survives every screen transition.
 *
 * Shows the UI ASSET build (`ui <sha>`) always, and — once a session has
 * connected — the SERVER/PIPELINE build (`pipe <sha>`) from the /api/offer
 * answer. When the two disagree the pipeline part is flagged (a deploy is
 * mid-roll: assets landed on CloudFront but ECS hasn't finished). Hover reveals
 * the full UI build time.
 */
export default function VersionStamp() {
  const serverVersion = useSyncExternalStore(subscribeServerVersion, getServerVersion);
  const title = APP_BUILT_AT ? `ui build ${APP_VERSION} · ${APP_BUILT_AT}` : `ui build ${APP_VERSION}`;
  const mismatch = serverVersion != null && serverVersion !== APP_VERSION;

  return (
    <span className="version-stamp" title={title} data-testid="version-stamp">
      <span className="version-stamp-ui">ui {APP_VERSION}</span>
      {serverVersion != null ? (
        <span
          className={`version-stamp-pipe${mismatch ? " version-stamp-pipe--mismatch" : ""}`}
          title={mismatch ? "pipeline is a different build than the UI — a deploy is still rolling" : undefined}
        >
          {" · "}pipe {serverVersion}
        </span>
      ) : null}
    </span>
  );
}
