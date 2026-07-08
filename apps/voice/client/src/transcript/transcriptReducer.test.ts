import { describe, expect, it } from "vitest";
import { INITIAL_TRANSCRIPT_STATE, transcriptReducer, type TranscriptState } from "./transcriptReducer";

const run = (events: Parameters<typeof transcriptReducer>[1][]): TranscriptState =>
  events.reduce(transcriptReducer, INITIAL_TRANSCRIPT_STATE);

describe("transcriptReducer", () => {
  it("firms an interim user turn in place, then appends the next user turn", () => {
    const state = run([
      { type: "USER_TRANSCRIPT", text: "tell me", final: false },
      { type: "USER_TRANSCRIPT", text: "tell me about km", final: true },
      { type: "USER_TRANSCRIPT", text: "and defcon", final: false },
    ]);
    expect(state.map((t) => [t.speaker, t.text, t.final])).toEqual([
      ["user", "tell me about km", true],
      ["user", "and defcon", false],
    ]);
  });

  it("concatenates consecutive agent chunks into one turn", () => {
    const state = run([
      { type: "AGENT_TRANSCRIPT", text: "Kurt built it." },
      { type: "AGENT_TRANSCRIPT", text: "km drives it." },
    ]);
    expect(state).toHaveLength(1);
    expect(state[0]).toMatchObject({ speaker: "agent", text: "Kurt built it. km drives it.", final: true });
  });

  it("alternating speakers append distinct turns and never clear history", () => {
    const state = run([
      { type: "AGENT_TRANSCRIPT", text: "hey" },
      { type: "USER_TRANSCRIPT", text: "hi", final: true },
      { type: "AGENT_TRANSCRIPT", text: "what's up" },
    ]);
    expect(state.map((t) => t.speaker)).toEqual(["agent", "user", "agent"]);
  });

  it("assigns stable unique ids", () => {
    const state = run([
      { type: "USER_TRANSCRIPT", text: "a", final: true },
      { type: "AGENT_TRANSCRIPT", text: "b" },
    ]);
    expect(new Set(state.map((t) => t.id)).size).toBe(2);
  });

  it("RESET clears to empty", () => {
    const state = run([{ type: "USER_TRANSCRIPT", text: "a", final: true }, { type: "RESET" }]);
    expect(state).toEqual([]);
  });
});
