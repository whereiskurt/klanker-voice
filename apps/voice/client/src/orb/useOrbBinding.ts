import { useEffect, useState } from "react";
import { RTVIEvent, type PipecatClient } from "@pipecat-ai/client-js";
import { smoothAmplitude, type OrbState } from "./orbState";

export interface OrbBinding {
  state: OrbState;
  /** [0..1], EMA-smoothed -- ready to feed straight into `<OrbCanvas>`. */
  amplitude: number;
}

/**
 * Subscribes to the live RTVI event stream and derives the orb's state +
 * audio-reactive amplitude (CLNT-04, D-06 · UI-SPEC §Orb Visual Spec /
 * §Interaction & Data Binding):
 *
 *  - State machine: idle -> listening (user-started-speaking) -> thinking
 *    (user-stopped-speaking, awaiting bot output) -> speaking
 *    (bot-started-speaking) -> idle (bot-stopped-speaking).
 *  - Amplitude target: local mic RMS while listening, bot TTS RMS while
 *    speaking, 0 (ambient/internal-churn -- OrbCanvas/OrbFallback's own
 *    idle/thinking motion) otherwise. Smoothed every animation frame via
 *    `orbState.smoothAmplitude`'s fast-attack/slow-release EMA (60ms/180ms)
 *    -- the actual RTVI audio-level events arrive on their own cadence, not
 *    every frame, so this hook owns the continuous smoothing the caller
 *    (`OrbCanvas`/`OrbFallback`) expects to already be done for it.
 *
 * `client` is `null` before a voice session exists (e.g. the Attract
 * screen's static idle orb, D-07, never needs this hook).
 */
export function useOrbBinding(client: PipecatClient | null): OrbBinding {
  const [orbState, setOrbState] = useState<OrbState>("idle");
  const [amplitude, setAmplitude] = useState(0);

  useEffect(() => {
    const orbStateRef = { current: "idle" as OrbState };
    const targetAmplitudeRef = { current: 0 };
    const smoothedAmplitudeRef = { current: 0 };

    setOrbState("idle");
    setAmplitude(0);

    if (!client) return undefined;

    const setState = (next: OrbState) => {
      orbStateRef.current = next;
      setOrbState(next);
      // A state change resets the amplitude target so a stale mic/TTS level
      // from the previous turn never bleeds into the new state's motion.
      if (next === "idle" || next === "thinking") targetAmplitudeRef.current = 0;
    };

    const onUserStartedSpeaking = () => setState("listening");
    const onUserStoppedSpeaking = () => setState("thinking");
    const onBotStartedSpeaking = () => setState("speaking");
    const onBotStoppedSpeaking = () => setState("idle");
    const onLocalAudioLevel = (level: number) => {
      if (orbStateRef.current === "listening") targetAmplitudeRef.current = level;
    };
    const onRemoteAudioLevel = (level: number) => {
      if (orbStateRef.current === "speaking") targetAmplitudeRef.current = level;
    };

    client.on(RTVIEvent.UserStartedSpeaking, onUserStartedSpeaking);
    client.on(RTVIEvent.UserStoppedSpeaking, onUserStoppedSpeaking);
    client.on(RTVIEvent.BotStartedSpeaking, onBotStartedSpeaking);
    client.on(RTVIEvent.BotStoppedSpeaking, onBotStoppedSpeaking);
    client.on(RTVIEvent.LocalAudioLevel, onLocalAudioLevel);
    client.on(RTVIEvent.RemoteAudioLevel, onRemoteAudioLevel);

    let raf = 0;
    let lastTs = performance.now();
    const tick = (ts: number) => {
      const dtMs = ts - lastTs;
      lastTs = ts;
      smoothedAmplitudeRef.current = smoothAmplitude(
        smoothedAmplitudeRef.current,
        targetAmplitudeRef.current,
        dtMs,
      );
      setAmplitude(smoothedAmplitudeRef.current);
      raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);

    return () => {
      cancelAnimationFrame(raf);
      client.off(RTVIEvent.UserStartedSpeaking, onUserStartedSpeaking);
      client.off(RTVIEvent.UserStoppedSpeaking, onUserStoppedSpeaking);
      client.off(RTVIEvent.BotStartedSpeaking, onBotStartedSpeaking);
      client.off(RTVIEvent.BotStoppedSpeaking, onBotStoppedSpeaking);
      client.off(RTVIEvent.LocalAudioLevel, onLocalAudioLevel);
      client.off(RTVIEvent.RemoteAudioLevel, onRemoteAudioLevel);
    };
  }, [client]);

  return { state: orbState, amplitude };
}
