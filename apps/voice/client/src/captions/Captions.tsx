import type { CaptionState } from "./captionReducer";
import "./captions.css";

export interface CaptionsProps {
  captions: CaptionState;
}

/**
 * Subtitle-style, last-exchange-only caption band (CLNT-03, D-08 ·
 * UI-SPEC §Stage Layout): current/last utterance per side, interim gray
 * firming to final, fading as the conversation moves on — NOT a rolling
 * transcript. Text is rendered as plain React text (never HTML), matching
 * the threat register's T-05-04-T disposition. Agent speaker chip is the
 * one caption-side reserved-accent use (UI-SPEC Color: "Final-state
 * caption speaker chip for the agent side").
 */
export default function Captions({ captions }: CaptionsProps) {
  if (!captions.user && !captions.agent) return null;

  return (
    <div className="captions" aria-live="polite">
      {captions.agent ? (
        <p className="caption-row">
          <span className="caption-chip caption-chip--agent">KPH</span>
          <span className="caption-text caption-text--final">{captions.agent.text}</span>
        </p>
      ) : null}
      {captions.user ? (
        <p className="caption-row">
          <span className="caption-chip caption-chip--user">You</span>
          <span
            className={
              captions.user.final ? "caption-text caption-text--final" : "caption-text caption-text--interim"
            }
          >
            {captions.user.text}
          </span>
        </p>
      ) : null}
    </div>
  );
}
