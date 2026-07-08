import { render, screen, act } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import Ceremony from "./Ceremony";
import { CEREMONY_SCRIPT, LINE_MS } from "./ceremonyScript";

describe("Ceremony", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("advances through the scripted lines and fires onScriptDone once at the end", async () => {
    const onScriptDone = vi.fn();
    render(<Ceremony onScriptDone={onScriptDone} />);
    expect(screen.getByTestId("ceremony-line")).toHaveTextContent(CEREMONY_SCRIPT[0].line);

    for (let i = 1; i < CEREMONY_SCRIPT.length; i++) {
      await act(async () => { await vi.advanceTimersByTimeAsync(LINE_MS); });
      expect(screen.getByTestId("ceremony-line")).toHaveTextContent(CEREMONY_SCRIPT[i].line);
    }

    await act(async () => { await vi.advanceTimersByTimeAsync(LINE_MS); });
    expect(onScriptDone).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId("ceremony-line")).toHaveTextContent(CEREMONY_SCRIPT[CEREMONY_SCRIPT.length - 1].line);
  });
});
