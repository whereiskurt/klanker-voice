import { useEffect, useRef, useState } from "react";
import type { TranscriptState } from "./transcriptReducer";
import "./transcript.css";

export interface TranscriptProps {
  turns: TranscriptState;
}

/**
 * The growing, scrollable conversation log (voice-flow-redesign). Auto-scrolls
 * to the newest turn ONLY when the user is already pinned near the bottom; if
 * they have scrolled up to read history, it leaves them there and offers a
 * "jump to latest" affordance. Text is plain React text (never HTML).
 */
export default function Transcript({ turns }: TranscriptProps) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const [pinned, setPinned] = useState(true);

  useEffect(() => {
    if (pinned && scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [turns, pinned]);

  const onScroll = () => {
    const el = scrollRef.current;
    if (!el) return;
    setPinned(el.scrollHeight - el.scrollTop - el.clientHeight < 40);
  };

  const jumpToLatest = () => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
    setPinned(true);
  };

  return (
    <div className="transcript-wrap">
      <div ref={scrollRef} className="transcript" onScroll={onScroll} aria-live="polite" aria-label="Conversation transcript">
        {turns.map((turn) => (
          <div key={turn.id} data-testid="turn" className={`turn turn--${turn.speaker}${turn.final ? "" : " turn--interim"}`}>
            <span className={`turn-chip turn-chip--${turn.speaker}`}>{turn.speaker === "agent" ? "KPH" : "You"}</span>
            <span className="turn-text">{turn.text}</span>
          </div>
        ))}
      </div>
      {!pinned ? (
        <button type="button" className="transcript-jump" onClick={jumpToLatest}>
          ↓ jump to latest
        </button>
      ) : null}
    </div>
  );
}
