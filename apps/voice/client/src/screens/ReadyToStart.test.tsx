import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import ReadyToStart from "./ReadyToStart";

describe("ReadyToStart", () => {
  it("fires onStart when the CTA is tapped", () => {
    const onStart = vi.fn();
    render(<ReadyToStart onStart={onStart} />);
    fireEvent.click(screen.getByRole("button", { name: /let's start talking/i }));
    expect(onStart).toHaveBeenCalledTimes(1);
  });
});
