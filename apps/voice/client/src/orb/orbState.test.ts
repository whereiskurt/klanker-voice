import { describe, expect, it } from "vitest";
import { ORB_STATE_VISUALS, smoothAmplitude } from "./orbState";

describe("ORB_STATE_VISUALS", () => {
  it("maps idle to the UI-SPEC aqua accent and its ambient bloom alpha", () => {
    expect(ORB_STATE_VISUALS.idle.core.hex).toBe("#2DE2C8");
    expect(ORB_STATE_VISUALS.idle.bloom.hex).toBe("#2DE2C8");
    expect(ORB_STATE_VISUALS.idle.bloomAlpha).toBeCloseTo(0.25);
  });

  it("maps listening to electric blue", () => {
    expect(ORB_STATE_VISUALS.listening.core.hex).toBe("#4EA8FF");
    expect(ORB_STATE_VISUALS.listening.bloomAlpha).toBeCloseTo(0.3);
  });

  it("maps thinking to indigo-violet with a faster ring speed than idle", () => {
    expect(ORB_STATE_VISUALS.thinking.core.hex).toBe("#8B7CF6");
    expect(ORB_STATE_VISUALS.thinking.motion.ringSpeedMultiplier).toBeGreaterThan(
      ORB_STATE_VISUALS.idle.motion.ringSpeedMultiplier,
    );
  });

  it("maps speaking to the intensified aqua-green with the brightest bloom", () => {
    expect(ORB_STATE_VISUALS.speaking.core.hex).toBe("#34F5D0");
    const allAlphas = Object.values(ORB_STATE_VISUALS).map((v) => v.bloomAlpha);
    expect(ORB_STATE_VISUALS.speaking.bloomAlpha).toBe(Math.max(...allAlphas));
  });

  it("only idle runs the ambient 'alive before a word is spoken' loop", () => {
    const ambientStates = Object.entries(ORB_STATE_VISUALS)
      .filter(([, visual]) => visual.motion.ambient)
      .map(([state]) => state);
    expect(ambientStates).toEqual(["idle"]);
  });

  it("wires each non-idle state to its RTVI amplitude source", () => {
    expect(ORB_STATE_VISUALS.listening.motion.amplitudeSource).toBe("mic-rms");
    expect(ORB_STATE_VISUALS.speaking.motion.amplitudeSource).toBe("tts-rms");
    expect(ORB_STATE_VISUALS.thinking.motion.amplitudeSource).toBe("internal-churn");
    expect(ORB_STATE_VISUALS.idle.motion.amplitudeSource).toBe("none");
  });
});

describe("smoothAmplitude", () => {
  it("attacks faster than it releases over an identical time delta", () => {
    const attacked = smoothAmplitude(0, 1, 60);
    const released = smoothAmplitude(1, 0, 60);
    // Moving toward louder (attack) should cover more ground in 60ms than
    // moving toward quieter (release) covers in the same 60ms.
    const attackDelta = attacked - 0;
    const releaseDelta = 1 - released;
    expect(attackDelta).toBeGreaterThan(releaseDelta);
  });

  it("converges to the target as dt grows", () => {
    const nearlyThere = smoothAmplitude(0, 1, 5000);
    expect(nearlyThere).toBeGreaterThan(0.99);
  });

  it("is a no-op when already at the target", () => {
    expect(smoothAmplitude(0.5, 0.5, 100)).toBeCloseTo(0.5);
  });
});
