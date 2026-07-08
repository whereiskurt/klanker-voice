import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import LandBounce from "./LandBounce";

describe("LandBounce", () => {
  it("shows a quiet holding status while redirecting/holding", () => {
    render(<LandBounce mode="holding" onSignIn={() => {}} />);
    expect(screen.getByText(/checking your session/i)).toBeInTheDocument();
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("shows a manual sign-in button in nudge mode", () => {
    const onSignIn = vi.fn();
    render(<LandBounce mode="nudge" onSignIn={onSignIn} />);
    fireEvent.click(screen.getByRole("button", { name: /sign in/i }));
    expect(onSignIn).toHaveBeenCalledTimes(1);
  });
});
