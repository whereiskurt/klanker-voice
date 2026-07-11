import { useCallback, useRef, useState } from "react";
import type { PipecatClient } from "@pipecat-ai/client-js";
import "./compose.css";

export interface ComposeBarProps {
  client: PipecatClient;
  /** Local echo: show the typed line in the transcript as a "You" turn.
   * `sendText` injects into the LLM context directly (it never round-trips
   * through STT), so no UserTranscript event fires — we echo it ourselves. */
  onLocalEcho: (text: string) => void;
}

/** Max height (px) the input grows to before it scrolls internally. */
const MAX_HEIGHT_PX = 132;

/**
 * A slick, auto-growing text compose bar for the live stage: type or paste
 * text and KPH answers it exactly as if you'd spoken it. It calls the Pipecat
 * client's `sendText(..., { run_immediately: true })`, which appends the text
 * to the LLM context and runs a turn — same persona, same quota gate, just a
 * typed modality (and it skips STT, so it's cheaper than speaking). Handy for
 * testing responses with a fixed prompt without talking.
 *
 * Enter sends; Shift+Enter inserts a newline. The field starts as a single-line
 * pill and grows a few lines to fit pasted text, then scrolls.
 */
export default function ComposeBar({ client, onLocalEcho }: ComposeBarProps) {
  const [value, setValue] = useState("");
  const ref = useRef<HTMLTextAreaElement>(null);

  const resize = useCallback(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, MAX_HEIGHT_PX)}px`;
  }, []);

  const send = useCallback(() => {
    const text = value.trim();
    if (!text) return;
    onLocalEcho(text);
    void client.sendText(text, { run_immediately: true });
    setValue("");
    // Collapse back to one line after send.
    requestAnimationFrame(() => {
      if (ref.current) ref.current.style.height = "auto";
    });
  }, [value, client, onLocalEcho]);

  return (
    <form
      className="live-compose"
      onSubmit={(e) => {
        e.preventDefault();
        send();
      }}
    >
      <textarea
        ref={ref}
        className="live-compose-input"
        value={value}
        rows={1}
        placeholder="Type or paste text for KPH…"
        aria-label="Type or paste text for KPH to answer"
        onChange={(e) => {
          setValue(e.target.value);
          resize();
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter" && !e.shiftKey) {
            e.preventDefault();
            send();
          }
        }}
      />
      <button
        type="submit"
        className="live-compose-send"
        aria-label="Send text to KPH"
        disabled={!value.trim()}
      >
        ↑
      </button>
    </form>
  );
}
