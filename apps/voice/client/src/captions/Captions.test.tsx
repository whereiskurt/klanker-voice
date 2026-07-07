import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";
import Captions from "./Captions";
import type { CaptionState } from "./captionReducer";

describe("Captions — agent reply fully rendered + scrollable (07.1)", () => {
  it("renders the whole long agent reply with the scroll class (no clamp-to-first-sentence)", () => {
    const longReply =
      "Sentence one about klanker-maker. Sentence two with more detail. " +
      "Sentence three that the old 2-line clamp would have hidden. " +
      "Sentence four confirming the whole reply is present.";
    const captions: CaptionState = { user: null, agent: { text: longReply, final: true } };

    const { container } = render(<Captions captions={captions} />);

    const agentSpan = container.querySelector(".caption-text--scroll");
    expect(agentSpan).not.toBeNull();
    // The full text is in the DOM — the old bug was CSS-visibility (line-clamp),
    // not missing content; the scroll variant now reveals all of it.
    expect(agentSpan?.textContent).toBe(longReply);
  });

  it("does not apply the scroll variant to the user caption", () => {
    const captions: CaptionState = {
      user: { text: "a user question", final: true },
      agent: null,
    };
    const { container } = render(<Captions captions={captions} />);
    expect(container.querySelector(".caption-text--scroll")).toBeNull();
  });
});
