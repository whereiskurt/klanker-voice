import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import Transcript from "./Transcript";
import type { TranscriptState } from "./transcriptReducer";

describe("Transcript", () => {
  it("renders each turn with a speaker chip and its text, oldest first", () => {
    const turns: TranscriptState = [
      { id: "t0", speaker: "agent", text: "hey, what's up", final: true },
      { id: "t1", speaker: "user", text: "tell me about km", final: true },
    ];
    render(<Transcript turns={turns} />);
    const rows = screen.getAllByTestId("turn");
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveTextContent("KPH");
    expect(rows[0]).toHaveTextContent("hey, what's up");
    expect(rows[1]).toHaveTextContent("You");
  });

  it("marks interim (non-final) user turns", () => {
    render(<Transcript turns={[{ id: "t0", speaker: "user", text: "and def", final: false }]} />);
    expect(screen.getByTestId("turn")).toHaveClass("turn--interim");
  });

  it("renders text as plain text (no HTML injection)", () => {
    render(<Transcript turns={[{ id: "t0", speaker: "user", text: "<img src=x onerror=1>", final: true }]} />);
    expect(screen.getByText("<img src=x onerror=1>")).toBeInTheDocument();
  });
});
