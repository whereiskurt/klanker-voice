import { useEffect, useReducer } from "react";
import { RTVIEvent, type PipecatClient } from "@pipecat-ai/client-js";
import OrbCanvas from "../orb/OrbCanvas";
import { useOrbBinding } from "../orb/useOrbBinding";
import Transcript from "../transcript/Transcript";
import { transcriptReducer, INITIAL_TRANSCRIPT_STATE } from "../transcript/transcriptReducer";
import { playRandomGreeting, type GreetingHandle } from "../greeting/greetingPlayer";
import Countdown from "../timer/Countdown";
import LatencyHud from "../hud/LatencyHud";
import "./live.css";

export interface LiveProps {
  client: PipecatClient;
  sessionMaxSeconds: number | null;
  /** User tapped "End chat" — App routes this to useVoiceSession.endChat(). */
  onEndChat: () => void;
}

/**
 * The live-conversation stage (voice-flow-redesign §3.4): a COMPACT orb header
 * over a growing, scrollable transcript. Mounted by App only once the
 * connection reached "connected" AND the ceremony finished, so mount time is
 * exactly "the orb appears" — which is where the KPH greeting now plays
 * (orb-before-greeting; iOS audio was unlocked on the start gesture).
 */
export default function Live({ client, sessionMaxSeconds, onEndChat }: LiveProps) {
  const orb = useOrbBinding(client);
  const [turns, dispatch] = useReducer(transcriptReducer, INITIAL_TRANSCRIPT_STATE);

  // Play the greeting exactly once as the orb appears.
  useEffect(() => {
    let handle: GreetingHandle | null = null;
    let cancelled = false;
    void playRandomGreeting().then((h) => {
      if (cancelled) { h?.stop(); return; }
      handle = h;
    });
    return () => { cancelled = true; handle?.stop(); };
  }, []);

  useEffect(() => {
    const onUserTranscript = (data: { text: string; final: boolean }) =>
      dispatch({ type: "USER_TRANSCRIPT", text: data.text, final: data.final });
    const onBotTranscript = (data: { text: string }) =>
      dispatch({ type: "AGENT_TRANSCRIPT", text: data.text });

    client.on(RTVIEvent.UserTranscript, onUserTranscript);
    client.on(RTVIEvent.BotTranscript, onBotTranscript);
    return () => {
      client.off(RTVIEvent.UserTranscript, onUserTranscript);
      client.off(RTVIEvent.BotTranscript, onBotTranscript);
    };
  }, [client]);

  return (
    <div className="live">
      <div className="live-orb"><OrbCanvas state={orb.state} amplitude={orb.amplitude} /></div>
      <Transcript turns={turns} />
      <div className="live-bar">
        <button type="button" className="live-endchat" onClick={onEndChat}>End chat</button>
        {sessionMaxSeconds != null && sessionMaxSeconds > 0 ? (
          <Countdown sessionMaxSeconds={sessionMaxSeconds} startedAt={Date.now()} />
        ) : null}
      </div>
      <LatencyHud client={client} />
    </div>
  );
}
