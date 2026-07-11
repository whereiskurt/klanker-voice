import { fireEvent, render, screen, waitFor } from "@testing-library/react";
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
    render(
      <Live client={makeClient()} sessionMaxSeconds={null} variantLabel={null} onEndChat={() => {}} />,
    );
    expect(playRandomGreeting).toHaveBeenCalledTimes(1);
  });

  it("fires onEndChat from the End chat button", () => {
    const onEndChat = vi.fn();
    render(
      <Live client={makeClient()} sessionMaxSeconds={null} variantLabel={null} onEndChat={onEndChat} />,
    );
    fireEvent.click(screen.getByRole("button", { name: /end chat/i }));
    expect(onEndChat).toHaveBeenCalledTimes(1);
  });

  it("renders the variant label when provided", () => {
    render(
      <Live client={makeClient()} sessionMaxSeconds={null} variantLabel="KPH(v1)" onEndChat={() => {}} />,
    );
    expect(screen.getByText("KPH(v1)")).toBeInTheDocument();
  });

  it("suppresses the mic while the greeting plays, then restores it (mobile-feedback fix)", async () => {
    const enableMic = vi.fn();
    const handlers: Record<string, (d: unknown) => void> = {};
    const client = {
      on: (evt: string, cb: (d: unknown) => void) => { handlers[evt] = cb; },
      off: () => {},
      isMicEnabled: true,
      enableMic,
    } as never;
    render(<Live client={client} sessionMaxSeconds={null} variantLabel={null} onEndChat={() => {}} />);
    // Muted on mount so the greeting can't feed back into the mic.
    expect(enableMic).toHaveBeenCalledWith(false);
    // Restored once the greeting's `ended` promise resolves.
    await waitFor(() => expect(enableMic).toHaveBeenCalledWith(true));
  });

  it("renders no variant label element when null", () => {
    const { container } = render(
      <Live client={makeClient()} sessionMaxSeconds={null} variantLabel={null} onEndChat={() => {}} />,
    );
    expect(container.querySelector(".live-variant-label")).toBeNull();
  });
});
