import { useEffect, useRef } from "react";
import type { OrbState } from "./orbState";
import { ORB_STATE_VISUALS } from "./orbState";
import "./orb.css";

export interface OrbFallbackProps {
  state: OrbState;
  /** [0..1], already EMA-smoothed by the caller. */
  amplitude: number;
}

/**
 * The mandatory calm 2D radial-glow fallback (UI-SPEC Orb Visual Spec) —
 * rendered when WebGL2 is unavailable OR `prefers-reduced-motion` is set.
 * Ported from sketch 001 variant B ("Calm glow"): no noise churn, no
 * particle ring — just a state-colored radial gradient that scales gently
 * with amplitude, legible and calm on constrained devices.
 */
export default function OrbFallback({ state, amplitude }: OrbFallbackProps) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const stateRef = useRef(state);
  const amplitudeRef = useRef(amplitude);
  stateRef.current = state;
  amplitudeRef.current = amplitude;

  useEffect(() => {
    const canvas = canvasRef.current;
    const ctx = canvas?.getContext("2d");
    if (!canvas || !ctx) return undefined;

    const smoothColor: [number, number, number] = [...ORB_STATE_VISUALS.idle.core.rgb];
    let raf = 0;

    const rgbString = (c: readonly [number, number, number]) =>
      `${Math.round(c[0] * 255)}, ${Math.round(c[1] * 255)}, ${Math.round(c[2] * 255)}`;

    const render = () => {
      const dpr = Math.min(window.devicePixelRatio || 1, 2);
      const w = canvas.clientWidth;
      const h = canvas.clientHeight;
      if (canvas.width !== w * dpr || canvas.height !== h * dpr) {
        canvas.width = w * dpr;
        canvas.height = h * dpr;
      }
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      ctx.clearRect(0, 0, w, h);

      const target = ORB_STATE_VISUALS[stateRef.current].core.rgb;
      for (let i = 0; i < 3; i++) smoothColor[i] += (target[i] - smoothColor[i]) * 0.05; // ~600ms-ish morph

      const amp = amplitudeRef.current;
      const cx = w / 2;
      const cy = h * 0.42;
      const radius = Math.min(w, h) * 0.2 * (1 + amp * 0.12);
      const rgb = rgbString(smoothColor);

      const halo = ctx.createRadialGradient(cx, cy, radius * 0.2, cx, cy, radius * 3.4);
      halo.addColorStop(0, `rgba(${rgb}, ${0.5 + amp * 0.35})`);
      halo.addColorStop(0.4, `rgba(${rgb}, 0.12)`);
      halo.addColorStop(1, "rgba(0, 0, 0, 0)");
      ctx.fillStyle = halo;
      ctx.beginPath();
      ctx.arc(cx, cy, radius * 3.4, 0, Math.PI * 2);
      ctx.fill();

      const core = ctx.createRadialGradient(cx, cy - radius * 0.15, radius * 0.1, cx, cy, radius);
      core.addColorStop(0, `rgba(255, 255, 255, ${0.5 + amp * 0.3})`);
      core.addColorStop(0.5, `rgb(${rgb})`);
      core.addColorStop(1, `rgba(${rgb}, 0.5)`);
      ctx.fillStyle = core;
      ctx.beginPath();
      ctx.arc(cx, cy, radius, 0, Math.PI * 2);
      ctx.fill();

      raf = requestAnimationFrame(render);
    };

    raf = requestAnimationFrame(render);
    return () => cancelAnimationFrame(raf);
  }, []);

  return <canvas ref={canvasRef} className="orb-canvas orb-canvas--fallback" aria-hidden="true" />;
}
