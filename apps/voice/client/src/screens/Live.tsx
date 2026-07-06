import { useEffect, useReducer } from "react";
import { RTVIEvent, type PipecatClient } from "@pipecat-ai/client-js";
import OrbCanvas from "../orb/OrbCanvas";
import { useOrbBinding } from "../orb/useOrbBinding";
import Captions from "../captions/Captions";
import { captionReducer, INITIAL_CAPTION_STATE } from "../captions/captionReducer";
import "./live.css";

export interface LiveProps {
  client: PipecatClient;
}

/**
 * The live-conversation stage (CLNT-03/04): the hero orb now driven by real
 * RTVI state/amplitude (`useOrbBinding`) plus the subtitle caption band
 * (`captionReducer` fed by RTVI transcript frames). Mounted by `App.tsx`
 * only once `connectionState` reaches "connected" — no conversation UI
 * exists before that (T-05-04-E).
 */
export default function Live({ client }: LiveProps) {
  const orb = useOrbBinding(client);
  const [captions, dispatchCaption] = useReducer(captionReducer, INITIAL_CAPTION_STATE);

  useEffect(() => {
    const onUserTranscript = (data: { text: string; final: boolean }) => {
      dispatchCaption({ type: "USER_TRANSCRIPT", text: data.text, final: data.final });
    };
    const onBotTranscript = (data: { text: string }) => {
      dispatchCaption({ type: "AGENT_TRANSCRIPT", text: data.text });
    };

    client.on(RTVIEvent.UserTranscript, onUserTranscript);
    client.on(RTVIEvent.BotTranscript, onBotTranscript);

    return () => {
      client.off(RTVIEvent.UserTranscript, onUserTranscript);
      client.off(RTVIEvent.BotTranscript, onBotTranscript);
    };
  }, [client]);

  return (
    <div className="live">
      <OrbCanvas state={orb.state} amplitude={orb.amplitude} />
      <Captions captions={captions} />
    </div>
  );
}
