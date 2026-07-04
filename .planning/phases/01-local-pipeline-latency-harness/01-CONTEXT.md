# Phase 1: Local Pipeline & Latency Harness - Context

**Gathered:** 2026-07-04
**Status:** Ready for planning

<domain>
## Phase Boundary

A developer can hold a slick, measured, interruption-safe conversation with the
KlankerMaker concierge on a laptop using only the three provider API keys
(Deepgram, Anthropic, ElevenLabs). Deliverables: the tuned Pipecat pipeline
(PIPE-01..07), the latency harness, the endpointing A/B verdicts, and the persona
prompt v1. No cloud deployment, no auth, no quotas — those are Phases 2–4.

</domain>

<decisions>
## Implementation Decisions

### Voice identity & vibe
- **D-01:** Agent name is **KPH**, introduces itself and goes by **"K"** (pronounced "kay").
- **D-02:** Voice is chosen by a **3-voice side-by-side audition** during tuning — no direction locked in advance. The audition (same script rendered through 3 shortlisted ElevenLabs voices) is a Phase 1 deliverable; the winner's voice id lands in config and `docs/TUNING.md`.
- **D-03:** Delivery is **fast & punchy** — quick tempo, short sentences, ElevenLabs speed slightly above default. Persona prompt and TTS settings should both reinforce this.

### Conversation behavior
- **D-04:** **K greets first** the moment the connection lands — short opener that names itself and invites a question (proves audio path instantly, no dead air).
- **D-05:** Default answers are **1–2 sentences with a depth hook** ("want the longer story?"). Keeps turns fast and TTS spend low.
- **D-06:** Off-topic policy: **roll with it, steer back** — answers general questions gamely, weaves back toward Kurt/klanker/defcon.run territory after a turn or two. Never refuses on-topic-adjacent questions; refusing kills the demo.
- **D-07:** Sass level: **playful with teeth** — witty, a little cheeky, will roast gently if invited; no profanity unprompted.

### Local dev experience
- **D-08:** Two run modes from day one: **localhost web page** (SmallWebRTC + Pipecat JS client — the same transport path as prod) and **terminal mic/speaker mode** for fast prompt-tuning iteration. Web mode is the verification surface; terminal mode is the iteration surface.
- **D-09:** Pipeline configuration lives in a **checked-in TOML file** (`pipeline.toml` or similar): stage selection (STT/LLM/TTS providers + models), endpointing knobs, persona file path, voice id, speed. **Secrets never in TOML** — API keys come from `.env` (gitignored).
- **D-10:** Key bootstrap: a small script (`make env` or equivalent) reads the three `/kmk/bootstrap/*` SSM parameters using the `klanker-application` profile and writes `.env`. SSM is the single source of truth from day one; nothing plaintext in the repo. (User stores keys at `/kmk/bootstrap/{deepgram_api_key,anthropic_api_key,elevenlabs_api_key}` in us-east-1.)

### Harness output & verdicts
- **D-11:** Each harness run produces a **console table + JSON artifact**: per-stage breakdown (VAD-stop, STT-final, LLM TTFT, TTS first-audio, voice-to-voice) with p50/p95 across scripted turns. JSON is the diffable record for A/B comparisons.
- **D-12:** Tuning verdicts (endpointing A/B winner — Deepgram Flux vs Nova-3+VAD, SmartTurn verdict, chosen voice, final knob values) are recorded in **`docs/TUNING.md`** with the measured tables and reasoning. Later phases (HUD, prod config) inherit the winning values from there.
- **D-13:** Latency thresholds are **informational in Phase 1** (✅/⚠️ against 1.2s ceiling / ~800ms target, never nonzero exit). Turning them into a CI regression gate is Phase 5 (conference freeze) work.

### Claude's Discretion
- Exact TOML schema, harness CLI shape, and test-script content.
- Which 3 ElevenLabs voices make the audition shortlist (pick for fast-punchy fit and demo intelligibility; user picks winner by ear).
- Barge-in test scenario design (research pitfall list names the cases to cover).
- Repo layout details within the agreed monorepo shape (apps/voice etc. per ARCHITECTURE.md).

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Design & requirements
- `docs/superpowers/specs/2026-07-04-klanker-voice-design.md` — approved system design (pipeline, transport, latency budget, persona scope)
- `.planning/REQUIREMENTS.md` — PIPE-01..07 are this phase's contract

### Research (read before planning)
- `.planning/research/STACK.md` — pinned versions; **install-string trap:** pipecat-ai 1.5.0 has no `silero`/`elevenlabs` extras (moved to core); correct: `pipecat-ai[webrtc,deepgram,anthropic,runner]~=1.5.0` + `local` for laptop dev
- `.planning/research/PITFALLS.md` — latency traps (Pipecat `aggregation_timeout=1.0` default, VAD/endpointing silence double-count), barge-in bugs (pipecat #3986), idle-detection holes
- `.planning/research/ARCHITECTURE.md` — multi-session process model, repo layout
- `.planning/research/SUMMARY.md` — synthesis + phase research flags

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- No code exists in this repo yet (greenfield — docs and planning only).
- `/Users/khundeck/working/defcon.run.34` — reference for repo conventions (apps/ layout, Dockerfile patterns); nothing pipeline-related to reuse in this phase.

### Established Patterns
- Monorepo layout per `.planning/research/ARCHITECTURE.md`: voice service under `apps/voice/` (Python, uv), persona prompt at `apps/voice/prompts/concierge.md`.

### Integration Points
- `pipeline.toml` + persona markdown are consumed unchanged by the deployed service in Phase 4 — design them as the prod artifacts, not throwaways.
- Harness JSON schema feeds the Phase 5 latency HUD and CI gate — keep stage names stable.

</code_context>

<specifics>
## Specific Ideas

- Greeting should introduce the name: K/KPH — e.g. "Hey — I'm K." then an invitation. Exact copy is persona-tuning territory.
- The voice audition is an explicit, user-in-the-loop step: prepare 3 candidates, render the same lines, user picks by ear.

</specifics>

<deferred>
## Deferred Ideas

None — discussion stayed within phase scope.

</deferred>

---

*Phase: 1-Local Pipeline & Latency Harness*
*Context gathered: 2026-07-04*
