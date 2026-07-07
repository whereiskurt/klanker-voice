import { afterEach, describe, expect, it, vi } from "vitest";
import { render } from "@testing-library/react";
import Callback from "./Callback";
import { isReturningUser, markReturningUser } from "../auth/returningStore";

afterEach(() => { localStorage.clear(); sessionStorage.clear(); });

describe("Callback login_required", () => {
  it("clears the breadcrumb and routes onward without error UI", async () => {
    markReturningUser();
    window.history.replaceState({}, "", "/callback?error=login_required");
    const onAuthenticated = vi.fn();
    render(<Callback onAuthenticated={onAuthenticated} />);
    await vi.waitFor(() => expect(onAuthenticated).toHaveBeenCalled());
    expect(isReturningUser()).toBe(false);
  });
});
