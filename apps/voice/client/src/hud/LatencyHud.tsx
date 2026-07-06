import { Activity } from "lucide-react";
import type { PipecatClient } from "@pipecat-ai/client-js";
import { formatP50Seconds, formatStageMs, useHudOpen, useLatencyMetrics } from "./useLatencyMetrics";
import "./hud.css";

export interface LatencyHudProps {
  client: PipecatClient | null;
}

/**
 * Toggleable latency HUD (CLNT-06, D-09 · UI-SPEC §Latency HUD Spec): OFF
 * by default (pristine for audiences), revealed by the bottom-left
 * "Latency" affordance or the 'H' key. Translucent secondary-surface panel,
 * monospace 13px, one right-aligned row per stage -- purely informational,
 * escalates nothing (unlike the countdown). Values render exactly what the
 * server sent (05-01's `kmv-latency` RTVIServerMessageFrame), never
 * recomputed; a never-observed stage renders as a dash, not 0.
 */
export default function LatencyHud({ client }: LatencyHudProps) {
  const metrics = useLatencyMetrics(client);
  const [open, toggle] = useHudOpen();

  return (
    <>
      <button
        type="button"
        className="hud-toggle"
        onClick={toggle}
        aria-pressed={open}
        aria-label="Toggle latency HUD"
      >
        <Activity size={16} aria-hidden="true" />
        <span>Latency</span>
        <kbd className="hud-toggle-hint" aria-hidden="true">
          H
        </kbd>
      </button>

      {open ? (
        <div className="hud-panel" role="status">
          <div className="hud-row">
            <span className="hud-label">STT</span>
            <span className="hud-value">{formatStageMs(metrics.sttMs)}</span>
          </div>
          <div className="hud-row">
            <span className="hud-label">LLM TTFT</span>
            <span className="hud-value">{formatStageMs(metrics.llmTtftMs)}</span>
          </div>
          <div className="hud-row">
            <span className="hud-label">TTS 1st-audio</span>
            <span className="hud-value">{formatStageMs(metrics.ttsFirstAudioMs)}</span>
          </div>
          <div className="hud-row">
            <span className="hud-label">voice&rarr;voice p50</span>
            <span className="hud-value">{formatP50Seconds(metrics.v2vP50Ms)}</span>
          </div>
        </div>
      ) : null}
    </>
  );
}
