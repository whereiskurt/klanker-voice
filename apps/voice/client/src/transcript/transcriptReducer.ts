/**
 * Append-only transcript model (voice-flow-redesign). Unlike the retired
 * last-exchange-only caption band, this NEVER clears mid-conversation — every
 * frame either updates the tail turn or appends a new one. That absence of a
 * clearing path is what removes the "transcript permanently dies" bug class.
 */
export interface TranscriptTurn {
  id: string;
  speaker: "user" | "agent";
  text: string;
  /** false = interim (provisional, gray); true = firmed/final. */
  final: boolean;
}

export type TranscriptState = TranscriptTurn[];

export const INITIAL_TRANSCRIPT_STATE: TranscriptState = [];

export type TranscriptEvent =
  | { type: "USER_TRANSCRIPT"; text: string; final: boolean }
  | { type: "AGENT_TRANSCRIPT"; text: string }
  | { type: "RESET" };

export function transcriptReducer(state: TranscriptState, event: TranscriptEvent): TranscriptState {
  switch (event.type) {
    case "USER_TRANSCRIPT": {
      const tail = state[state.length - 1];
      // Firm an in-progress interim user turn in place; otherwise append.
      if (tail && tail.speaker === "user" && tail.final === false) {
        return [...state.slice(0, -1), { ...tail, text: event.text, final: event.final }];
      }
      return [...state, { id: `t${state.length}`, speaker: "user", text: event.text, final: event.final }];
    }
    case "AGENT_TRANSCRIPT": {
      const tail = state[state.length - 1];
      // Sentence-aggregated agent chunks concatenate onto the current agent turn.
      if (tail && tail.speaker === "agent") {
        return [...state.slice(0, -1), { ...tail, text: `${tail.text} ${event.text}`.trim() }];
      }
      return [...state, { id: `t${state.length}`, speaker: "agent", text: event.text, final: true }];
    }
    case "RESET":
      return INITIAL_TRANSCRIPT_STATE;
    default:
      return state;
  }
}
