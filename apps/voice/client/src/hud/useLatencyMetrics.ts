import { useCallback, useEffect, useState } from "react";
import { RTVIEvent, type PipecatClient } from "@pipecat-ai/client-js";

/** The `kmv-latency` RTVIServerMessageFrame payload type (05-01,
 * observers.py `_build_latency_payload`) -- rendered verbatim, never
 * recomputed. A stage the observer never saw serializes as JSON `null`. */
export const KMV_LATENCY_MESSAGE_TYPE = "kmv-latency";

export interface LatencyMetrics {
  sttMs: number | null;
  llmTtftMs: number | null;
  ttsFirstAudioMs: number | null;
  voiceToVoiceMs: number | null;
  v2vP50Ms: number | null;
}

export const INITIAL_LATENCY_METRICS: LatencyMetrics = {
  sttMs: null,
  llmTtftMs: null,
  ttsFirstAudioMs: null,
  voiceToVoiceMs: null,
  v2vP50Ms: null,
};

interface KmvLatencyData {
  stt_ms: number | null;
  llm_ttft_ms: number | null;
  tts_first_audio_ms: number | null;
  voice_to_voice_ms: number | null;
  v2v_p50_ms: number | null;
}

function isKmvLatencyMessage(
  value: unknown,
): value is { type: string; data: KmvLatencyData } {
  if (typeof value !== "object" || value === null) return false;
  const candidate = value as { type?: unknown; data?: unknown };
  return candidate.type === KMV_LATENCY_MESSAGE_TYPE && typeof candidate.data === "object" && candidate.data !== null;
}

/** Pure reduction of one RTVI `serverMessage` event into the latest
 * per-stage HUD values. Non-`kmv-latency` messages (or malformed shapes)
 * are ignored, returning `current` unchanged -- never throws, matching
 * observers.py's own "never raising" contract for an unobserved stage. */
export function reduceLatencyMessage(current: LatencyMetrics, message: unknown): LatencyMetrics {
  if (!isKmvLatencyMessage(message)) return current;
  const { data } = message;
  return {
    sttMs: data.stt_ms ?? null,
    llmTtftMs: data.llm_ttft_ms ?? null,
    ttsFirstAudioMs: data.tts_first_audio_ms ?? null,
    voiceToVoiceMs: data.voice_to_voice_ms ?? null,
    v2vP50Ms: data.v2v_p50_ms ?? null,
  };
}

/** "142 ms" / "—" for a null stage -- never "0 ms" (0 would misread as a
 * real, suspiciously-fast measurement rather than "not observed yet"). */
export function formatStageMs(value: number | null): string {
  if (value == null) return "—";
  return `${Math.round(value)} ms`;
}

/** "1.40 s" / "—" -- the HUD's one seconds-unit row (voice->voice p50). */
export function formatP50Seconds(value: number | null): string {
  if (value == null) return "—";
  return `${(value / 1000).toFixed(2)} s`;
}

/**
 * Subscribes to the live RTVI `serverMessage` stream and holds the latest
 * `kmv-latency` per-stage breakdown (CLNT-06, D-09) -- the 05-01
 * `LatencyReportObserver` pushes one per finalized turn. `client === null`
 * (not yet connected) reports all-null/dash values.
 */
export function useLatencyMetrics(client: PipecatClient | null): LatencyMetrics {
  const [metrics, setMetrics] = useState<LatencyMetrics>(INITIAL_LATENCY_METRICS);

  useEffect(() => {
    setMetrics(INITIAL_LATENCY_METRICS);
    if (!client) return undefined;

    const onServerMessage = (data: unknown) => {
      setMetrics((current) => reduceLatencyMessage(current, data));
    };

    client.on(RTVIEvent.ServerMessage, onServerMessage);
    return () => {
      client.off(RTVIEvent.ServerMessage, onServerMessage);
    };
  }, [client]);

  return metrics;
}

/** Case-insensitive 'H' key (ignoring an in-progress text input/contentEditable
 * focus, though the client has none today -- guarded anyway per the plan). */
function isHudHotkey(event: KeyboardEvent): boolean {
  const target = event.target as HTMLElement | null;
  const isTyping =
    !!target &&
    (target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.isContentEditable);
  return !isTyping && event.key.toLowerCase() === "h";
}

/**
 * Open/closed state for the latency HUD (CLNT-06, D-09): OFF by default,
 * toggled by the 'H' key (global keydown listener) or by calling the
 * returned `toggle()` (wired to the "Latency" affordance's `onClick` in
 * `LatencyHud.tsx`).
 */
export function useHudOpen(): [boolean, () => void] {
  const [open, setOpen] = useState(false);

  useEffect(() => {
    const onKeydown = (event: KeyboardEvent) => {
      if (isHudHotkey(event)) setOpen((current) => !current);
    };
    window.addEventListener("keydown", onKeydown);
    return () => window.removeEventListener("keydown", onKeydown);
  }, []);

  const toggle = useCallback(() => setOpen((current) => !current), []);

  return [open, toggle];
}
