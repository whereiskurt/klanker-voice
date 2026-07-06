import { useCallback, useRef, useState } from "react";
import type { PipecatClient } from "@pipecat-ai/client-js";
import { requestMic, type MicError } from "../media/getMic";
import { getToken } from "../auth/tokenStore";
import {
  connectionReducer,
  INITIAL_CONNECTION_OUTCOME,
  type ConnectionEvent,
  type ConnectionOutcome,
} from "./connectionState";
import { createVoiceSession, type VoiceSession } from "./voiceSession";

export interface UseVoiceSessionResult {
  outcome: ConnectionOutcome;
  /** Set when `start()` stopped at the mic-permission step (Task 1). */
  micError: MicError | null;
  /** The live RTVI client once connecting has begun -- Task 3 subscribes
   * orb/caption bindings to this via `client.on(RTVIEvent.X, handler)`. */
  client: PipecatClient | null;
  /** Gesture-gated: call this from the "Tap to talk" handler, never on mount. */
  start: () => Promise<void>;
  stop: () => Promise<void>;
}

/**
 * The live-connect React hook (CLNT-01/02): on `start()`, requests the mic
 * (Task 1's honest, distinct error states), and on success builds + connects
 * a `voiceSession` (Bearer token -> `/api/offer` -> SmallWebRTC, Task 2).
 * `connectionState` only ever reaches "connected" after a real bot-ready
 * signal -- no conversation UI can mount before that (T-05-04-E).
 */
export function useVoiceSession(): UseVoiceSessionResult {
  const [outcome, setOutcome] = useState<ConnectionOutcome>(INITIAL_CONNECTION_OUTCOME);
  const [micError, setMicError] = useState<MicError | null>(null);
  const [client, setClient] = useState<PipecatClient | null>(null);
  const sessionRef = useRef<VoiceSession | null>(null);

  const dispatch = useCallback((event: ConnectionEvent) => {
    setOutcome((current) => connectionReducer(current, event));
  }, []);

  const start = useCallback(async () => {
    setMicError(null);
    dispatch({ type: "REQUEST_MIC" });

    const mic = await requestMic();
    if (mic.status !== "granted") {
      setMicError(mic.status);
      dispatch({ type: "MIC_ERROR" });
      return;
    }
    dispatch({ type: "MIC_GRANTED" });

    const session = createVoiceSession({ getToken, onEvent: dispatch });
    sessionRef.current = session;
    setClient(session.client);
    await session.connect();
  }, [dispatch]);

  const stop = useCallback(async () => {
    await sessionRef.current?.disconnect();
    sessionRef.current = null;
    setClient(null);
    dispatch({ type: "RESET" });
  }, [dispatch]);

  return { outcome, micError, client, start, stop };
}
