import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import VersionStamp from "./VersionStamp";
import { APP_VERSION } from "../version";

describe("VersionStamp", () => {
  it("renders the build version (falls back to 'dev' with no build-time env)", () => {
    render(<VersionStamp />);
    const stamp = screen.getByTestId("version-stamp");
    // No VITE_APP_VERSION is injected under vitest, so it is the "dev" fallback.
    expect(stamp).toHaveTextContent(APP_VERSION);
    expect(APP_VERSION).toBe("dev");
  });

  it("exposes a hover title for the full build info", () => {
    render(<VersionStamp />);
    expect(screen.getByTestId("version-stamp")).toHaveAttribute("title");
  });
});
