import { useEffect, useReducer, useState } from "react";
import { RTVIEvent, type PipecatClient } from "@pipecat-ai/client-js";
import OrbCanvas from "../orb/OrbCanvas";
import { useOrbBinding } from "../orb/useOrbBinding";
import Captions from "../captions/Captions";
import { captionReducer, INITIAL_CAPTION_STATE } from "../captions/captionReducer";
import Countdown from "../timer/Countdown";
import LatencyHud from "../hud/LatencyHud";
import "./live.css";

export interface LiveProps {
  client: PipecatClient;
  /** Tier session_max_seconds from the `/api/offer` connect flow (CLNT-05,
   * D-10) -- `null` until it lands or for a no-cap (bypass) session, in
   * which case the countdown pill simply doesn't render. */
  sessionMaxSeconds: number | null;
}

/**
 * The live-conversation stage (CLNT-03/04/05/06): the hero orb now driven by
 * real RTVI state/amplitude (`useOrbBinding`) plus the subtitle caption band
 * (`captionReducer` fed by RTVI transcript frames), the persistent session
 * countdown, and the off-by-default latency HUD. Mounted by `App.tsx` only
 * once `connectionState` reaches "connected" — no conversation UI exists
 * before that (T-05-04-E), which also makes THIS component's mount time
 * exactly "the moment the session reaches connected" (D-10's countdown
 * start clock).
 */
export default function Live({ client, sessionMaxSeconds }: LiveProps) {
  const orb = useOrbBinding(client);
  const [captions, dispatchCaption] = useReducer(captionReducer, INITIAL_CAPTION_STATE);
  const [startedAt] = useState<number>(() => Date.now());

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
      {sessionMaxSeconds != null && sessionMaxSeconds > 0 ? (
        <Countdown sessionMaxSeconds={sessionMaxSeconds} startedAt={startedAt} />
      ) : null}
      <LatencyHud client={client} />
    </div>
  );
}
