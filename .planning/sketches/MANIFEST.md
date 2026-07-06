# Sketch Manifest

## Design Direction
A public, conference-ready voice client at **voice.klankermaker.ai** whose core value is a
demo that makes people say *"whoa" in the first ten seconds*. The look is a bespoke,
full-screen **immersive dark stage** with a state-aware, audio-reactive **orb as the hero**;
everything else (subtitle captions, corner countdown, latency HUD, "Tap to talk" CTA, gate
cards) is a restrained overlay that never competes with the orb. Palette is a deep near-black
`#06070C` stage (60%), translucent `#10121C` overlays (30%), and a single electric-aqua
`#2DE2C8` accent (10%) that shifts hue by orb state. Direction is locked by
`.planning/phases/05-browser-client-conference-readiness/05-UI-SPEC.md` (approved 2026-07-05);
sketches explore the one open area — the orb's render treatment.

## Reference Points
- The project's own `05-UI-SPEC.md` (tokens, orb spec, motion) — authoritative
- auth.klankermaker.ai webapp (brand handoff: aqua accent, near-black bg — harmonize, not copy)
- Genre touchstones: Siri/voice-assistant orbs, ElevenLabs/agent-platform "live" visualizers

## Sketches

| # | Name | Design Question | Winner | Tags |
|---|------|----------------|--------|------|
| 001 | immersive-orb-stage | Which orb treatment lands the "whoa" on the immersive dark stage? | **A — shader nebula + particle ring** | orb, immersive, whoa, webgl |
