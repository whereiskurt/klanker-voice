import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import Live from "./Live";

vi.mock("../greeting/greetingPlayer", () => ({
  playRandomGreeting: vi.fn().mockResolvedValue({ ended: Promise.resolve(), stop: vi.fn() }),
}));

// Minimal fake RTVI client: records handlers so the test can push transcript frames.
function makeClient() {
  const handlers: Record<string, (d: unknown) => void> = {};
  return {
    on: (evt: string, cb: (d: unknown) => void) => { handlers[evt] = cb; },
    off: () => {},
    emit: (evt: string, d: unknown) => handlers[evt]?.(d),
  } as never;
}

describe("Live", () => {
  it("plays the greeting once on mount (orb-then-greeting)", async () => {
    const { playRandomGreeting } = await import("../greeting/greetingPlayer");
    render(<Live client={makeClient()} sessionMaxSeconds={null} onEndChat={() => {}} />);
    expect(playRandomGreeting).toHaveBeenCalledTimes(1);
  });

  it("fires onEndChat from the End chat button", () => {
    const onEndChat = vi.fn();
    render(<Live client={makeClient()} sessionMaxSeconds={null} onEndChat={onEndChat} />);
    fireEvent.click(screen.getByRole("button", { name: /end chat/i }));
    expect(onEndChat).toHaveBeenCalledTimes(1);
  });
});
