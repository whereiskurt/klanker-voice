import { APP_VERSION, versionTitle } from "../version";
import "./versionStamp.css";

/**
 * A faint, always-on build stamp in the top-left corner, present on every
 * screen so you can read the deployed version at a glance (mirrors the quiet
 * style of the KPH(v1)/KPH(v2) variant label). Hover reveals the full build
 * time via the title tooltip. Mounted once at the React root (main.tsx) so it
 * survives every screen transition.
 */
export default function VersionStamp() {
  return (
    <span className="version-stamp" title={versionTitle()} data-testid="version-stamp">
      {APP_VERSION}
    </span>
  );
}
