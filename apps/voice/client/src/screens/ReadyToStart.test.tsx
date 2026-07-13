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

  it("shows the recording notice alongside the existing CTA", () => {
    const onStart = vi.fn();
    render(<ReadyToStart onStart={onStart} />);
    expect(screen.getByText(/recorded/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /let's start talking/i })).toBeInTheDocument();
    expect(screen.getByText(/this taps the mic awake/i)).toBeInTheDocument();
  });
});
