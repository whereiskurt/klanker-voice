import { render, screen, fireEvent } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { PipecatClient } from "@pipecat-ai/client-js";
import ComposeBar from "./ComposeBar";

function makeClient() {
  return { sendText: vi.fn().mockResolvedValue(undefined) } as unknown as PipecatClient;
}

describe("ComposeBar", () => {
  it("sends typed text via sendText(run_immediately) and echoes it locally", () => {
    const client = makeClient();
    const onLocalEcho = vi.fn();
    render(<ComposeBar client={client} onLocalEcho={onLocalEcho} />);

    const input = screen.getByLabelText(/type or paste text/i);
    fireEvent.change(input, { target: { value: "tell me about defcon.run" } });
    fireEvent.keyDown(input, { key: "Enter" });

    expect(client.sendText).toHaveBeenCalledWith("tell me about defcon.run", { run_immediately: true });
    expect(onLocalEcho).toHaveBeenCalledWith("tell me about defcon.run");
    expect((input as HTMLTextAreaElement).value).toBe(""); // cleared after send
  });

  it("Shift+Enter inserts a newline instead of sending", () => {
    const client = makeClient();
    render(<ComposeBar client={client} onLocalEcho={vi.fn()} />);
    const input = screen.getByLabelText(/type or paste text/i);
    fireEvent.change(input, { target: { value: "line one" } });
    fireEvent.keyDown(input, { key: "Enter", shiftKey: true });
    expect(client.sendText).not.toHaveBeenCalled();
  });

  it("does not send blank/whitespace-only text", () => {
    const client = makeClient();
    render(<ComposeBar client={client} onLocalEcho={vi.fn()} />);
    const input = screen.getByLabelText(/type or paste text/i);
    fireEvent.change(input, { target: { value: "   " } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(client.sendText).not.toHaveBeenCalled();
  });
});
