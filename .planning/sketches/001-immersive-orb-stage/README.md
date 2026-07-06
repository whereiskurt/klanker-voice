---
sketch: 001
name: immersive-orb-stage
question: "Which orb treatment lands the 'whoa in ten seconds' on the immersive dark stage?"
winner: "A"
tags: [orb, immersive, voice, stage, whoa, webgl]
---

# Sketch 001: Immersive Orb Stage

## Design Question
The whole phase rides on one moment: the full-screen dark stage where a state-aware,
audio-reactive orb is the hero. The visual/motion direction is locked by `05-UI-SPEC.md`
(dark 60/30/10 palette, orb-centric, subtitle captions, corner countdown, HUD toggle,
"Tap to talk" CTA). The **one genuinely open discretion** is the orb *render treatment* —
so this sketch compares three, inside the real stage chrome.

## How to View
```
open .planning/sketches/001-immersive-orb-stage/index.html
```
Use the **bottom dock** to drive it (no real mic in a sketch):
- **Orb state** — idle / listening / thinking / speaking, or **▶ auto-cycle** to watch the loop
- **Countdown** — normal / warn (amber) / crit (red pulse, `<10s`)
- **Screens** — live / no-access gate / UDP-blocked wall / session-end
- Press **H** (or tap **⌁ Latency**) to toggle the HUD
- Top tabs switch the three orb variants

## Variants
- **A: Shader nebula + particle ring ★ WINNER** — WebGL2 fragment-shader orb (noise-warped
  rim, blooming halo, amplitude-driven pulse, hot core) **with C's orbiting particle ring
  layered just outside the plasma rim** (transparent Canvas2D overlay, `lighter` blend,
  ~1.55× core radius, tuned dimmer to accent not compete; particles speed up on *thinking*).
  This is the UI-SPEC's recommended path (GPU 60fps, tiny bundle, organic deformation free)
  plus the constellation energy of C. Falls back to B automatically if no WebGL.
- **B: Calm glow (fallback)** — Canvas2D layered radial glow. The *mandatory* reduced-motion /
  no-WebGL fallback aesthetic — deliberately calmer, no noise churn.
- **C: Particle ring only** — the lateral take on its own: solid core + audio-reactive
  orbiting particles. Kept for reference; its ring was grafted onto A.

**Decision:** Variant **A** — shader plasma orb **+** particle-ring overlay. Locked by user
2026-07-05. This is the orb direction the Phase-5 build implements.

All three share identical stage chrome, palette, captions, countdown, HUD, and copy —
so you're comparing *only* the orb, everything else is the locked contract.

## What to Look For
- Which one makes you say "whoa" in the **idle/attract** state (before a word is spoken)?
- Does the **state color morph** (aqua→blue→violet→aqua-green) read clearly per state?
- Does **amplitude reactivity** feel alive on listening/speaking without being jittery?
- Does the orb still let the **captions + CTA** breathe, or does it fight them?
- Check the gate/wall/end cards over each orb — does the translucent scrim hold up?

## Notes
- The amplitude here is *simulated* (envelope math). The real client drives `uAmplitude`
  from RTVI mic RMS (listening) and bot-TTS RMS (speaking) per UI-SPEC §Interaction.
- Reduced-motion: if your OS has it on, the loop damps automatically (matches the a11y
  contract — shader swaps to calm fallback in the real build).
