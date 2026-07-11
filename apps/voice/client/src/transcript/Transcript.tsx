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
  // Live mirror of `pinned` so the follow-effect reads the current value
  // without re-subscribing, and a record of the last scrollTop so onScroll can
  // tell a genuine user scroll-up from our own tail-follow landing short.
  const pinnedRef = useRef(true);
  const lastTopRef = useRef(0);

  const setPinnedState = (next: boolean) => {
    pinnedRef.current = next;
    setPinned(next);
  };

  // Follow the tail whenever pinned and content grows. Deliberately NOT gated
  // through React state: it reads pinnedRef so a burst of turns (e.g. streamed
  // agent chunks, or a reconnect flush after an error) always re-follows.
  useEffect(() => {
    const el = scrollRef.current;
    if (!el || !pinnedRef.current) return;
    el.scrollTop = el.scrollHeight;
    lastTopRef.current = el.scrollTop;
  }, [turns]);

  // Only a genuine UPWARD user scroll unpins. A downward move — including our
  // own programmatic follow that lost a race with freshly-appended content and
  // landed above the true bottom — can only ever (re-)pin. That asymmetry is
  // the fix for auto-scroll latching off mid-session (it used to set
  // pinned=false on any stale "not at bottom" measurement and never recover).
  const onScroll = () => {
    const el = scrollRef.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
    const scrolledUp = el.scrollTop < lastTopRef.current - 4;
    lastTopRef.current = el.scrollTop;
    if (atBottom) setPinnedState(true);
    else if (scrolledUp) setPinnedState(false);
  };

  const jumpToLatest = () => {
    const el = scrollRef.current;
    if (el) {
      el.scrollTop = el.scrollHeight;
      lastTopRef.current = el.scrollTop;
    }
    setPinnedState(true);
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
