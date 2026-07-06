/**
 * Pure reducer for the subtitle-style, last-exchange-only caption band
 * (CLNT-03, D-08 · UI-SPEC §Stage Layout). Holds only the CURRENT/last
 * utterance per side — never an appended rolling transcript.
 */

export interface CaptionLine {
  text: string;
  /** false = interim (gray, provisional); true = firmed/final. */
  final: boolean;
}

export interface CaptionState {
  user: CaptionLine | null;
  agent: CaptionLine | null;
}

export const INITIAL_CAPTION_STATE: CaptionState = { user: null, agent: null };

export type CaptionEvent =
  | { type: "USER_TRANSCRIPT"; text: string; final: boolean }
  | { type: "AGENT_TRANSCRIPT"; text: string }
  | { type: "RESET" };

export function captionReducer(current: CaptionState, event: CaptionEvent): CaptionState {
  switch (event.type) {
    case "USER_TRANSCRIPT": {
      // The previous user utterance already reached "final" -- any further
      // user-transcript frame is necessarily the START of a NEW exchange, so
      // the stale agent reply from the prior exchange is cleared (D-08:
      // last-exchange-only, never an appended history).
      const startsNewExchange = current.user?.final === true;
      return {
        user: { text: event.text, final: event.final },
        agent: startsNewExchange ? null : current.agent,
      };
    }
    case "AGENT_TRANSCRIPT": {
      // Bot transcript frames are sentence-aggregated (already "final" text
      // chunks, RTVIMessageType.BOT_TRANSCRIPTION); a multi-sentence reply
      // arrives as several frames within the same exchange, so they
      // concatenate onto the current agent utterance rather than replace it.
      const agent = current.agent
        ? { text: `${current.agent.text} ${event.text}`.trim(), final: true }
        : { text: event.text, final: true };
      return { ...current, agent };
    }
    case "RESET":
      return INITIAL_CAPTION_STATE;
    default:
      return current;
  }
}
