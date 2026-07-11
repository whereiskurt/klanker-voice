import { act, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import VersionStamp from "./VersionStamp";
import { APP_VERSION } from "../version";
import { setServerVersion } from "./serverVersionStore";

afterEach(() => setServerVersion(null));

describe("VersionStamp", () => {
  it("renders the UI build version (falls back to 'dev' with no build-time env)", () => {
    render(<VersionStamp />);
    const stamp = screen.getByTestId("version-stamp");
    // No VITE_APP_VERSION is injected under vitest, so it is the "dev" fallback.
    expect(stamp).toHaveTextContent(`ui ${APP_VERSION}`);
    expect(APP_VERSION).toBe("dev");
  });

  it("shows no pipe segment until a server version is published", () => {
    render(<VersionStamp />);
    expect(screen.getByTestId("version-stamp").textContent).not.toContain("pipe");
  });

  it("adds the pipeline segment once the server version arrives", () => {
    render(<VersionStamp />);
    act(() => setServerVersion("abc1234"));
    expect(screen.getByTestId("version-stamp")).toHaveTextContent("pipe abc1234");
  });

  it("exposes a hover title for the full build info", () => {
    render(<VersionStamp />);
    expect(screen.getByTestId("version-stamp")).toHaveAttribute("title");
  });
});
