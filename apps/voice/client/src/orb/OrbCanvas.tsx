import { useEffect, useRef, useState } from "react";
import type { OrbState } from "./orbState";
import { ORB_STATE_VISUALS } from "./orbState";
import { createOrbShaderProgram } from "./orbShader";
import { createParticleRing } from "./particleRing";
import { useReducedMotion } from "../a11y/liveRegions";
import OrbFallback from "./OrbFallback";
import "./orb.css";

export interface OrbCanvasProps {
  state: OrbState;
  /**
   * [0..1], already EMA-smoothed by the caller (see `orbState.smoothAmplitude`).
   * Fed 0/ambient here — 05-04 wires real RTVI mic/TTS RMS through this prop.
   */
  amplitude: number;
}

function supportsWebGL2(): boolean {
  try {
    const canvas = document.createElement("canvas");
    return !!canvas.getContext("webgl2");
  } catch {
    return false;
  }
}

/**
 * The hero orb (CLNT-04): WebGL2 shader plasma orb + orbiting particle-ring
 * overlay (sketch 001 winner A). Feature-detects WebGL2 support (one-shot —
 * it can't change mid-session) and `prefers-reduced-motion` REACTIVELY (via
 * `useReducedMotion`, 05-07 hardening) and swaps to the calm 2D fallback
 * (`OrbFallback`) when either applies — dropping the ring and the shader's
 * noise churn, per the UI-SPEC's mandatory-fallback requirement. The
 * reactive subscription means toggling iOS "Reduce Motion" mid-session
 * swaps the orb immediately, no reload needed (the conference-floor
 * checkpoint exercises exactly this).
 */
export default function OrbCanvas({ state, amplitude }: OrbCanvasProps) {
  const reducedMotion = useReducedMotion();
  // Default to unsupported until the effect below resolves feature
  // detection — safe for the first paint and for non-browser test runners
  // (jsdom has no WebGL2, so tests exercise the same fallback path).
  const [webgl2Supported, setWebgl2Supported] = useState(false);

  useEffect(() => {
    setWebgl2Supported(supportsWebGL2());
  }, []);

  const useFallback = reducedMotion || !webgl2Supported;

  if (useFallback) {
    return <OrbFallback state={state} amplitude={amplitude} />;
  }

  return <ShaderOrb state={state} amplitude={amplitude} onUnsupported={() => setWebgl2Supported(false)} />;
}

interface ShaderOrbProps extends OrbCanvasProps {
  onUnsupported: () => void;
}

function ShaderOrb({ state, amplitude, onUnsupported }: ShaderOrbProps) {
  const orbCanvasRef = useRef<HTMLCanvasElement | null>(null);
  const ringCanvasRef = useRef<HTMLCanvasElement | null>(null);
  const stateRef = useRef(state);
  const amplitudeRef = useRef(amplitude);
  stateRef.current = state;
  amplitudeRef.current = amplitude;

  useEffect(() => {
    const canvas = orbCanvasRef.current;
    const ringCanvas = ringCanvasRef.current;
    if (!canvas) return undefined;

    const gl = canvas.getContext("webgl2");
    if (!gl) {
      onUnsupported();
      return undefined;
    }
    const program = createOrbShaderProgram(gl);
    if (!program) {
      onUnsupported();
      return undefined;
    }
    const ring = ringCanvas ? createParticleRing(ringCanvas) : null;

    const t0 = performance.now();
    const smoothColor: [number, number, number] = [...ORB_STATE_VISUALS.idle.core.rgb];
    let raf = 0;

    const frame = () => {
      const dpr = Math.min(window.devicePixelRatio || 1, 2);
      const width = canvas.clientWidth * dpr;
      const height = canvas.clientHeight * dpr;
      if (canvas.width !== width || canvas.height !== height) {
        canvas.width = width;
        canvas.height = height;
      }

      const visual = ORB_STATE_VISUALS[stateRef.current];
      const target = visual.core.rgb;
      for (let i = 0; i < 3; i++) smoothColor[i] += (target[i] - smoothColor[i]) * 0.05; // ~600ms morph

      const timeSec = (performance.now() - t0) / 1000;
      program.draw(width, height, timeSec, amplitudeRef.current, smoothColor);
      ring?.draw(smoothColor, amplitudeRef.current, visual.motion.ringSpeedMultiplier);

      raf = requestAnimationFrame(frame);
    };
    raf = requestAnimationFrame(frame);
    return () => cancelAnimationFrame(raf);
  }, [onUnsupported]);

  return (
    <>
      <canvas ref={orbCanvasRef} className="orb-canvas orb-canvas--shader" aria-hidden="true" />
      <canvas ref={ringCanvasRef} className="orb-canvas orb-canvas--ring" aria-hidden="true" />
    </>
  );
}
