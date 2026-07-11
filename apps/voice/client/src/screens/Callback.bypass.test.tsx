import { afterEach, describe, expect, it, vi } from "vitest";
import { render } from "@testing-library/react";
import Callback from "./Callback";
import { isReturningUser } from "../auth/returningStore";
import { setToken } from "../auth/tokenStore";
import { exchangeCode } from "../auth/oidcClient";

// Mock the PKCE exchange and the token store so we can assert the bypass path
// ingests the fragment token WITHOUT touching the code-exchange round-trip.
vi.mock("../auth/oidcClient", () => ({ exchangeCode: vi.fn() }));
vi.mock("../auth/tokenStore", () => ({ setToken: vi.fn() }));

afterEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  vi.clearAllMocks();
  window.history.replaceState({}, "", "/");
});

describe("Callback bypass /join auto-login", () => {
  it("ingests a fragment access_token and skips the PKCE exchange", async () => {
    window.history.replaceState(
      {},
      "",
      "/callback#access_token=header.payload.sig&token_type=bearer&expires_in=3600&anon=1"
    );
    const onAuthenticated = vi.fn();
    render(<Callback onAuthenticated={onAuthenticated} />);

    await vi.waitFor(() => expect(onAuthenticated).toHaveBeenCalledTimes(1));

    // Token stored from the fragment, PKCE exchange never invoked.
    expect(setToken).toHaveBeenCalledWith("header.payload.sig");
    expect(exchangeCode).not.toHaveBeenCalled();

    // User marked returning, and the token is scrubbed from the address bar.
    expect(isReturningUser()).toBe(true);
    expect(window.location.hash).toBe("");
    expect(window.location.pathname).toBe("/");
  });

  it("still runs the PKCE path when there is no fragment token", async () => {
    // No access_token in the hash and no code/state -> PKCE path reports the
    // expired/tampered error (proves the bypass branch didn't hijack it).
    window.history.replaceState({}, "", "/callback");
    const onAuthenticated = vi.fn();
    const { findByRole } = render(<Callback onAuthenticated={onAuthenticated} />);

    const alert = await findByRole("alert");
    expect(alert).toBeTruthy();
    expect(setToken).not.toHaveBeenCalled();
    expect(onAuthenticated).not.toHaveBeenCalled();
  });
});
