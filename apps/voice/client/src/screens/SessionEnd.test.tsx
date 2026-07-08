import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import SessionEnd from "./SessionEnd";

describe("SessionEnd", () => {
  it("offers Start another and Sign out", () => {
    const onStartAnother = vi.fn();
    const onSignOut = vi.fn();
    render(<SessionEnd elapsedSeconds={252} reason="clean" onStartAnother={onStartAnother} onSignOut={onSignOut} />);
    fireEvent.click(screen.getByRole("button", { name: /start another/i }));
    fireEvent.click(screen.getByRole("button", { name: /sign out/i }));
    expect(onStartAnother).toHaveBeenCalledTimes(1);
    expect(onSignOut).toHaveBeenCalledTimes(1);
  });
});
