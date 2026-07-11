import { useState } from "react";
import type { PipecatClient } from "@pipecat-ai/client-js";
import "./micMute.css";

export interface MicMuteButtonProps {
  client: PipecatClient;
}

const MicOnIcon = () => (
  <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor"
    strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <rect x="9" y="2" width="6" height="12" rx="3" />
    <path d="M5 10a7 7 0 0 0 14 0" />
    <line x1="12" y1="19" x2="12" y2="22" />
  </svg>
);

const MicOffIcon = () => (
  <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor"
    strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <line x1="3" y1="3" x2="21" y2="21" />
    <path d="M9 5a3 3 0 0 1 6 0v4" />
    <path d="M15 11.5V13a3 3 0 0 1-5 2.2" />
    <path d="M5 10a7 7 0 0 0 10.6 6" />
    <line x1="12" y1="19" x2="12" y2="22" />
  </svg>
);

/**
 * An always-visible, obvious mic toggle for the live stage.
 *
 * Muting calls the Pipecat client's `enableMic(false)`, which DISABLES the
 * local microphone track — nothing is sent over WebRTC, so KPH (STT/LLM)
 * literally cannot hear the room. This is a privacy control for talking to
 * people nearby mid-session; it is distinct from "End chat", which tears the
 * whole session down. Re-enabling restores capture with no reconnect.
 */
export default function MicMuteButton({ client }: MicMuteButtonProps) {
  // Initialise from the client's own state so a re-mount reflects reality.
  const [muted, setMuted] = useState(() => !client.isMicEnabled);

  const toggle = () => {
    const next = !muted;
    client.enableMic(!next); // next === true (muted) -> enableMic(false)
    setMuted(next);
  };

  return (
    <button
      type="button"
      className={`mic-mute${muted ? " mic-mute--muted" : ""}`}
      onClick={toggle}
      aria-pressed={muted}
      aria-label={muted ? "Unmute microphone so KPH can hear you" : "Mute microphone so KPH cannot hear you"}
    >
      <span className="mic-mute-icon">{muted ? <MicOffIcon /> : <MicOnIcon />}</span>
      <span className="mic-mute-label">{muted ? "Muted — tap to talk to KPH" : "Mute mic"}</span>
    </button>
  );
}
