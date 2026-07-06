/**
 * Orbiting particle-ring overlay — ported from
 * `.planning/sketches/001-immersive-orb-stage/index.html`'s shared
 * `makeParticleLayer` (overlay mode). A transparent Canvas2D layer drawn
 * above the WebGL2 shader canvas: particles orbit just outside the plasma
 * rim (~1.55x core radius), `lighter` blend, tuned dimmer than a standalone
 * ring so it accents rather than competes with the shader orb. Orbital speed
 * increases in the `thinking` state (via `ringSpeedMultiplier` from
 * `orbState.ts`); the ring shares the orb's per-state color morph (the
 * caller passes the same smoothed color it feeds the shader).
 */

interface RingParticle {
  angle: number;
  radiusFactor: number;
  speed: number;
  size: number;
}

export interface ParticleRingLayer {
  /** Draws one frame. Resizes the backing canvas to match its host element
   * (device-pixel aware) and clears to transparent before drawing. */
  draw(color: readonly [number, number, number], amplitude: number, ringSpeedMultiplier: number): void;
}

/** Sketch 001 variant A overlay tuning (ring-only, layered over the shader). */
const PARTICLE_COUNT = 120;
const RING_RADIUS_FACTOR = 1.55; // ~1.55x core radius — sketch-locked treatment
const RING_ALPHA_K = 0.42; // dimmer than a standalone ring so it accents, not competes

export function createParticleRing(canvas: HTMLCanvasElement): ParticleRingLayer | null {
  const ctx = canvas.getContext("2d");
  if (!ctx) return null;

  const t0 = performance.now();
  const particles: RingParticle[] = Array.from({ length: PARTICLE_COUNT }, (_, i) => ({
    angle: (i / PARTICLE_COUNT) * Math.PI * 2,
    radiusFactor: 0.8 + Math.random() * 0.4,
    speed: 0.2 + Math.random() * 0.5,
    size: 0.6 + Math.random() * 1.8,
  }));

  return {
    draw(color, amplitude, ringSpeedMultiplier) {
      const dpr = Math.min(window.devicePixelRatio || 1, 2);
      const w = canvas.clientWidth;
      const h = canvas.clientHeight;
      if (canvas.width !== w * dpr || canvas.height !== h * dpr) {
        canvas.width = w * dpr;
        canvas.height = h * dpr;
      }
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      ctx.clearRect(0, 0, w, h); // transparent — the shader canvas shows through

      const cx = w / 2;
      const cy = h * 0.42;
      const base = Math.min(w, h) * 0.16;
      const tSec = (performance.now() - t0) / 1000;

      ctx.globalCompositeOperation = "lighter";
      for (const particle of particles) {
        const ring = base * (RING_RADIUS_FACTOR + particle.radiusFactor * (0.5 + amplitude * 1.1));
        const angle = particle.angle + tSec * particle.speed * ringSpeedMultiplier;
        const x = cx + Math.cos(angle) * ring;
        const y = cy + Math.sin(angle) * ring * 0.92;
        const alpha = (0.22 + amplitude * RING_ALPHA_K) * (0.4 + particle.radiusFactor * 0.6);
        ctx.fillStyle = `rgba(${Math.round(color[0] * 255)}, ${Math.round(color[1] * 255)}, ${Math.round(color[2] * 255)}, ${alpha})`;
        ctx.beginPath();
        ctx.arc(x, y, particle.size * (1 + amplitude), 0, Math.PI * 2);
        ctx.fill();
      }
      ctx.globalCompositeOperation = "source-over";
    },
  };
}
