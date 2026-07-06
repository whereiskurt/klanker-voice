import { describe, expect, it } from "vitest";
import { captionReducer, INITIAL_CAPTION_STATE, type CaptionState } from "./captionReducer";

describe("captionReducer — user side", () => {
  it("sets the user line to interim (gray) on an interim frame", () => {
    const state = captionReducer(INITIAL_CAPTION_STATE, {
      type: "USER_TRANSCRIPT",
      text: "hello",
      final: false,
    });
    expect(state.user).toEqual({ text: "hello", final: false });
  });

  it("firms the interim frame to final on the matching final frame", () => {
    const interim = captionReducer(INITIAL_CAPTION_STATE, {
      type: "USER_TRANSCRIPT",
      text: "hello",
      final: false,
    });
    const final = captionReducer(interim, {
      type: "USER_TRANSCRIPT",
      text: "hello world",
      final: true,
    });
    expect(final.user).toEqual({ text: "hello world", final: true });
  });

  it("replaces the last exchange (not appended history) once a new user utterance starts", () => {
    const finalized: CaptionState = {
      user: { text: "first question", final: true },
      agent: { text: "first answer", final: true },
    };
    const next = captionReducer(finalized, { type: "USER_TRANSCRIPT", text: "second", final: false });
    expect(next.user).toEqual({ text: "second", final: false });
    expect(next.agent).toBeNull();
  });

  it("does not clear the agent line mid-utterance (before the user frame is final)", () => {
    const midUtterance: CaptionState = {
      user: { text: "hel", final: false },
      agent: { text: "prior reply", final: true },
    };
    const next = captionReducer(midUtterance, { type: "USER_TRANSCRIPT", text: "hello", final: false });
    expect(next.agent).toEqual({ text: "prior reply", final: true });
  });
});

describe("captionReducer — agent side", () => {
  it("populates the agent line and marks it final (accent-chip-eligible)", () => {
    const state = captionReducer(INITIAL_CAPTION_STATE, { type: "AGENT_TRANSCRIPT", text: "Hi there." });
    expect(state.agent).toEqual({ text: "Hi there.", final: true });
  });

  it("concatenates multiple sentence-aggregated agent frames within the same exchange", () => {
    const first = captionReducer(INITIAL_CAPTION_STATE, { type: "AGENT_TRANSCRIPT", text: "Hi there." });
    const second = captionReducer(first, { type: "AGENT_TRANSCRIPT", text: "How can I help?" });
    expect(second.agent).toEqual({ text: "Hi there. How can I help?", final: true });
  });
});

describe("captionReducer — RESET", () => {
  it("clears both sides", () => {
    const populated: CaptionState = {
      user: { text: "x", final: true },
      agent: { text: "y", final: true },
    };
    expect(captionReducer(populated, { type: "RESET" })).toEqual(INITIAL_CAPTION_STATE);
  });
});
