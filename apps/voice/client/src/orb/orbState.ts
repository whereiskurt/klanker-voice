/**
 * Single source of truth for orb state (D-06: audio-reactive + state-colored
 * orb). `ORB_STATE_VISUALS` is the state -> {core, bloom, motion} map every
 * orb surface reads from: `OrbCanvas` (shader + particle ring), `OrbFallback`
 * (2D fallback), and — once 05-04 wires real RTVI amplitude + state — the
 * live pipeline too. Colors are the exact UI-SPEC §Color / §Orb Visual Spec
 * values; nothing here is invented.
 */

export type OrbState = "idle" | "listening" | "thinking" | "speaking";

export interface OrbColor {
  /** RGB tuple in [0,1], consumed directly as a WebGL `uColor` uniform. */
  rgb: readonly [number, number, number];
  /** CSS hex, consumed by the 2D fallback / DOM chrome (chips, halos). */
  hex: string;
}

export interface OrbMotionProfile {
  /** Runs its own ambient breathe loop with zero external amplitude — the
   * D-07 "alive before a word is spoken" attract state. Only `idle`. */
  ambient: boolean;
  /** Particle-ring orbital speed multiplier. UI-SPEC: "particle orbital
   * speed increases in the thinking state." */
  ringSpeedMultiplier: number;
  /** Where `uAmplitude` is sourced from once 05-04 wires real RTVI levels. */
  amplitudeSource: "none" | "mic-rms" | "tts-rms" | "internal-churn";
}

export interface OrbStateVisual {
  core: OrbColor;
  bloom: OrbColor;
  /** Bloom/halo alpha at rest (UI-SPEC "Bloom/halo" column, e.g. "@ 0.25"). */
  bloomAlpha: number;
  motion: OrbMotionProfile;
}

export const ORB_STATE_VISUALS: Record<OrbState, OrbStateVisual> = {
  idle: {
    core: { rgb: [0.176, 0.886, 0.784], hex: "#2DE2C8" },
    bloom: { rgb: [0.176, 0.886, 0.784], hex: "#2DE2C8" },
    bloomAlpha: 0.25,
    motion: { ambient: true, ringSpeedMultiplier: 0.6, amplitudeSource: "none" },
  },
  listening: {
    core: { rgb: [0.306, 0.659, 1.0], hex: "#4EA8FF" },
    bloom: { rgb: [0.306, 0.659, 1.0], hex: "#4EA8FF" },
    bloomAlpha: 0.3,
    motion: { ambient: false, ringSpeedMultiplier: 0.6, amplitudeSource: "mic-rms" },
  },
  thinking: {
    core: { rgb: [0.545, 0.486, 0.965], hex: "#8B7CF6" },
    bloom: { rgb: [0.545, 0.486, 0.965], hex: "#8B7CF6" },
    bloomAlpha: 0.22,
    motion: { ambient: false, ringSpeedMultiplier: 1.6, amplitudeSource: "internal-churn" },
  },
  speaking: {
    core: { rgb: [0.204, 0.961, 0.816], hex: "#34F5D0" },
    bloom: { rgb: [0.204, 0.961, 0.816], hex: "#34F5D0" },
    bloomAlpha: 0.35,
    motion: { ambient: false, ringSpeedMultiplier: 0.6, amplitudeSource: "tts-rms" },
  },
};

/** UI-SPEC §Motion Tokens: "Orb amplitude smoothing — attack ~60ms / release
 * ~180ms — EMA — uAmplitude uniform from RTVI RMS." */
export const ORB_AMPLITUDE_EMA = {
  attackMs: 60,
  releaseMs: 180,
} as const;

/**
 * One EMA smoothing step toward `target`, fast-attack / slow-release, over a
 * `dtMs` frame delta. Shared by `OrbCanvas`/`OrbFallback` now (fed 0/ambient)
 * and by the real RTVI amplitude wiring in 05-04.
 */
export function smoothAmplitude(current: number, target: number, dtMs: number): number {
  const tau = target > current ? ORB_AMPLITUDE_EMA.attackMs : ORB_AMPLITUDE_EMA.releaseMs;
  const k = 1 - Math.exp(-dtMs / tau);
  return current + (target - current) * k;
}
