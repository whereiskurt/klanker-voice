# Phase 1: Local Pipeline & Latency Harness - Research

**Researched:** 2026-07-04
**Domain:** Low-latency cascaded voice pipeline (Pipecat 1.5.0: Deepgram STT → Claude Haiku → ElevenLabs TTS), latency instrumentation, turn-detection A/B
**Confidence:** MEDIUM (per classify-confidence seam; core findings read directly from the pinned pipecat v1.5.0 source tag, cross-checked with vendor docs)

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Voice identity & vibe**
- **D-01:** Agent name is **KPH**, introduces itself and goes by **"K"** (pronounced "kay").
- **D-02:** Voice is chosen by a **3-voice side-by-side audition** during tuning — no direction locked in advance. The audition (same script rendered through 3 shortlisted ElevenLabs voices) is a Phase 1 deliverable; the winner's voice id lands in config and `docs/TUNING.md`.
- **D-03:** Delivery is **fast & punchy** — quick tempo, short sentences, ElevenLabs speed slightly above default. Persona prompt and TTS settings should both reinforce this.

**Conversation behavior**
- **D-04:** **K greets first** the moment the connection lands — short opener that names itself and invites a question (proves audio path instantly, no dead air).
- **D-05:** Default answers are **1–2 sentences with a depth hook** ("want the longer story?"). Keeps turns fast and TTS spend low.
- **D-06:** Off-topic policy: **roll with it, steer back** — answers general questions gamely, weaves back toward Kurt/klanker/defcon.run territory after a turn or two. Never refuses on-topic-adjacent questions; refusing kills the demo.
- **D-07:** Sass level: **playful with teeth** — witty, a little cheeky, will roast gently if invited; no profanity unprompted.

**Local dev experience**
- **D-08:** Two run modes from day one: **localhost web page** (SmallWebRTC + Pipecat JS client — the same transport path as prod) and **terminal mic/speaker mode** for fast prompt-tuning iteration. Web mode is the verification surface; terminal mode is the iteration surface.
- **D-09:** Pipeline configuration lives in a **checked-in TOML file** (`pipeline.toml` or similar): stage selection (STT/LLM/TTS providers + models), endpointing knobs, persona file path, voice id, speed. **Secrets never in TOML** — API keys come from `.env` (gitignored).
- **D-10:** Key bootstrap: a small script (`make env` or equivalent) reads the three `/kmv/bootstrap/*` SSM parameters using the `klanker-application` profile and writes `.env`. SSM is the single source of truth from day one; nothing plaintext in the repo. (User stores keys at `/kmv/bootstrap/{deepgram_api_key,anthropic_api_key,elevenlabs_api_key}` in us-east-1.)

**Harness output & verdicts**
- **D-11:** Each harness run produces a **console table + JSON artifact**: per-stage breakdown (VAD-stop, STT-final, LLM TTFT, TTS first-audio, voice-to-voice) with p50/p95 across scripted turns. JSON is the diffable record for A/B comparisons.
- **D-12:** Tuning verdicts (endpointing A/B winner — Deepgram Flux vs Nova-3+VAD, SmartTurn verdict, chosen voice, final knob values) are recorded in **`docs/TUNING.md`** with the measured tables and reasoning. Later phases (HUD, prod config) inherit the winning values from there.
- **D-13:** Latency thresholds are **informational in Phase 1** (✅/⚠️ against 1.2s ceiling / ~800ms target, never nonzero exit). Turning them into a CI regression gate is Phase 5 (conference freeze) work.

### Claude's Discretion
- Exact TOML schema, harness CLI shape, and test-script content.
- Which 3 ElevenLabs voices make the audition shortlist (pick for fast-punchy fit and demo intelligibility; user picks winner by ear).
- Barge-in test scenario design (research pitfall list names the cases to cover).
- Repo layout details within the agreed monorepo shape (apps/voice etc. per ARCHITECTURE.md).

### Deferred Ideas (OUT OF SCOPE)
None — discussion stayed within phase scope.
</user_constraints>

## Summary

Every version-sensitive open question from project research is now answered by reading the **pinned pipecat v1.5.0 source directly** (shallow clone of the `v1.5.0` git tag). The headline: pipecat 1.x restructured turn-taking, aggregation, and metrics so thoroughly that most 0.x-era tutorial advice — including some of our own project research — is stale. Three findings change the plan materially:

1. **The infamous `aggregation_timeout=1.0` latency trap no longer exists.** The 1.5.0 user aggregator is driven by a `UserTurnController` with pluggable start/stop strategies. The **default stop strategy is `TurnAnalyzerUserTurnStopStrategy(LocalSmartTurnAnalyzerV3)`** — Smart Turn v3 is a bundled 8 MB ONNX model (no torch, ~12ms CPU inference) and is on by default. VAD is now configured on the aggregator (`LLMUserAggregatorParams(vad_analyzer=SileroVADAnalyzer())`), not the transport. Deepgram Flux ships as a first-class `DeepgramFluxSTTService` that automatically installs `ExternalUserTurnStrategies` (Flux owns start/stop of turn server-side), which eliminates the VAD/endpointing silence double-count trap *by design*.
2. **Pipecat 1.5.0 ships a built-in eval harness** (`pipecat eval run`, `[evals]` extra): YAML scenarios with per-expectation `within_ms` latency budgets, `send_after: {event: llm_started, delay_ms: N}` scheduling designed explicitly for barge-in tests, a virtual microphone that plays deterministic cached WAV utterances through the real audio input path, session recording, and local (no-API-key) TTS/STT for the simulated user. This is the backbone for PIPE-02's named barge-in scenarios and much of PIPE-05. For the D-11 per-stage p50/p95 numbers, `UserBotLatencyObserver` emits voice-to-voice latency plus a per-service TTFB breakdown out of the box — the custom harness work reduces to one observer subclass that serializes to JSON and renders a table.
3. **Word-timestamp-accurate context truncation on interruption works in 1.5.0 with ElevenLabs WS TTS.** The ElevenLabs service parses per-chunk alignment into word timestamps, the output transport paces `TTSTextFrame`s to audio playout, and the assistant aggregator commits only words that were actually spoken when an `InterruptionFrame` lands. The pipecat #3986-era regressions are four minor versions behind the pin; barge-in still needs its named test scenarios, but no custom truncation code is needed.

**Primary recommendation:** Build one `build_pipeline(config)` factory driven by `pipeline.toml` (parsed with stdlib `tomllib`), consumed by three entrypoints — the pipecat runner (`-t webrtc` localhost page via bundled prebuilt UI, `-t eval` for the eval harness) and a thin terminal-mode entry using `LocalAudioTransport`. Endpointing A/B is a config-selected turn-strategy matrix: (A) Nova-3 + Silero VAD + speech-timeout stop, (B) Nova-3 + SmartTurn v3, (C) Flux (external strategies, `eot_threshold` knob). Use 1.5.0's `Settings(...)` parameter objects everywhere — bare constructor kwargs are deprecated and removed in 2.0.

## Project Constraints (from CLAUDE.md)

| Directive | Implication for this phase |
|-----------|---------------------------|
| Never use the term "voiceai" anywhere | Repo paths, package names, prompts, docs — always "klanker-voice" / site label "kmv" |
| ≤1.2s voice-to-voice; every pipeline stage must stream | Harness thresholds (informational per D-13); all services below are streaming |
| Tech stack: Pipecat (Python) for the pipeline | Locked; pinned `~=1.5.0` |
| Budget: quotas/kill-switch bound API burn | Phase 1: short-answer persona (D-05) + sentence-chunked TTS limit ElevenLabs spend during tuning |
| Public mic wired to metered APIs — quota-gated via OIDC | Out of scope for Phase 1 (local only), but keys must never land in git |
| GSD workflow enforcement — edits go through GSD commands | Planner/executor concern; no direct edits outside workflow |
| Authoritative design spec: `docs/superpowers/specs/2026-07-04-klanker-voice-design.md` | Planner must read before writing plans |

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| PIPE-01 | ≤1.2s voice-to-voice conversation (tuned toward ~800ms) | Latency budget table + knob inventory (Pattern 4, Pitfall 1); `UserBotLatencyObserver` measures it; Flux/eager-EOT and Haiku 4.5 are the levers |
| PIPE-02 | Barge-in: playback stops promptly; context truncated to words actually spoken | Word-timestamp truncation verified in 1.5.0 source (Pattern 5); eval harness `send_after` barge-in scenarios (Pattern 3) |
| PIPE-03 | Full in-session conversation memory | `LLMContext` + `LLMContextAggregatorPair` is the 1.5.0 default context machinery — nothing to build |
| PIPE-04 | STT/LLM/TTS config-swappable; endpointing A/B (Flux vs Nova-3+VAD; SmartTurn) with measured verdicts | TOML factory registry (Pattern 2); turn-strategy matrix (Pattern 4); verdicts recorded in `docs/TUNING.md` per D-12 |
| PIPE-05 | Latency harness: per-stage + voice-to-voice ms from recorded audio | `UserBotLatencyObserver` breakdown + custom JSON/table writer; eval harness virtual mic plays deterministic cached WAVs through the real input path (Pattern 3) |
| PIPE-06 | KlankerMaker concierge persona via versioned markdown prompt | Persona file at `apps/voice/prompts/concierge.md`, loaded by config; greet-first pattern (Pattern 6) |
| PIPE-07 | Full bot runs locally with only three provider API keys | Verified: eval harness user-audio (Kokoro) and bot-audio transcription (Moonshine) are local; judge LLM can be Anthropic via `factory` hook (`AnthropicLLMService.run_inference` exists); SSM→.env bootstrap (D-10) verified live — all 3 params exist |
</phase_requirements>

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Audio capture/playback (web mode) | Browser (prebuilt UI) | — | Mic/speaker + WebRTC peer live in the browser; Phase 1 uses the bundled prebuilt page, not a custom client |
| Audio capture/playback (terminal mode) | Local process (PyAudio) | — | `LocalAudioTransport` for prompt-iteration loop |
| WebRTC signaling + media | Backend (FastAPI runner + aiortc) | Browser | `/api/offer` + SmallWebRTC; same transport path as prod |
| VAD / turn detection / endpointing | Backend (aggregator strategies or Flux server-side) | Deepgram (Flux variant) | 1.5.0 turn system lives in the user aggregator; Flux moves it into the STT vendor |
| STT / LLM / TTS | Vendor APIs (Deepgram/Anthropic/ElevenLabs) | Backend service classes | Streaming hosted APIs per project constraint |
| Conversation context + truncation | Backend (LLMContext + aggregators) | — | Context is server-side state; truncation driven by word-timestamped TTS frames |
| Latency measurement | Backend (observers) | Harness CLI (report) | Observers see frame timings in-process; harness formats/persists |
| Barge-in test scenarios | Harness (pipecat eval + YAML) | Backend (eval transport route) | Eval client drives the bot over RTVI WebSocket |
| Config (pipeline.toml) & persona markdown | Backend (loaded at construction) | — | Designed as prod artifacts consumed unchanged in Phase 4 |
| Secrets | SSM → `.env` → process env | — | D-10; nothing plaintext in repo |

## Standard Stack

### Core

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| Python | 3.12.x | Runtime | pipecat needs ≥3.11; kokoro-onnx (evals extra) requires `<3.14` — 3.12 satisfies everything `[VERIFIED: PyPI metadata]` |
| pipecat-ai | 1.5.0 (pin `~=1.5.0`) | Pipeline framework | Confirmed latest on PyPI (released 2026-07-04) `[VERIFIED: pip index + source tag]` |
| pipecat extras | `[webrtc,deepgram,anthropic,runner,local,evals]` | Providers + runner + laptop audio + eval harness | Verified against v1.5.0 pyproject: `webrtc`→aiortc+opencv; `deepgram`→deepgram-sdk 6.1.1–<8; `anthropic`→anthropic <1; `runner`→fastapi/uvicorn/python-dotenv + **pipecat-ai-prebuilt** (localhost web UI); `local`→pyaudio; `evals`→cli(typer,rich)+kokoro+moonshine. `silero`/`elevenlabs` extras exist but are **empty** (core) `[VERIFIED: v1.5.0 pyproject.toml]` |
| Deepgram Nova-3 | `nova-3-general` via `DeepgramSTTService` | Baseline streaming STT | 1.5.0 source default model `[VERIFIED: v1.5.0 source]` |
| Deepgram Flux | `flux-general-en` via `DeepgramFluxSTTService` | A/B challenger: STT with integrated end-of-turn | First-class 1.5.0 service; `eot_threshold` default 0.7 (0.5–0.9), `eager_eot_threshold` off by default; requires `linear16` `[VERIFIED: v1.5.0 source]` |
| Claude Haiku 4.5 | `claude-haiku-4-5` via `AnthropicLLMService` | LLM | Source default is `claude-sonnet-4-6` — **must override** in Settings `[VERIFIED: v1.5.0 source]` |
| ElevenLabs Flash v2.5 | `eleven_flash_v2_5` via `ElevenLabsTTSService` | Streaming WS TTS with word timestamps | Listed valid realtime model in source; WS `speed` range **0.7–1.2** (D-03: slightly above 1.0) `[VERIFIED: v1.5.0 source]` |
| Silero VAD | `SileroVADAnalyzer` (`pipecat.audio.vad.silero`) | VAD for the Nova-3+VAD A/B arm | Bundled in core (onnxruntime base dep) `[VERIFIED: v1.5.0 source]` |
| SmartTurn v3 | `LocalSmartTurnAnalyzerV3` (bundled `smart-turn-v3.2-cpu.onnx`) | Turn-analyzer A/B arm (and 1.5.0 default) | 8 MB int8 ONNX, ~12ms modern CPU / ~60ms cheap cloud CPU — no torch `[VERIFIED: v1.5.0 source + CITED: daily.co/blog/announcing-smart-turn-v3-with-cpu-inference-in-just-12ms]` |
| tomllib | stdlib (3.11+) | Parse `pipeline.toml` | Zero-dep; read-only is all we need `[VERIFIED: stdlib]` |
| rich | pulled via `[evals]`→`[cli]` extra (`rich>=13,<14`) | Harness console tables | Already in the dependency tree — no new dep `[VERIFIED: v1.5.0 pyproject.toml]` |

### Supporting

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| pipecat-ai-prebuilt | 1.0.3 (pulled by `[runner]`) | Localhost web page for `-t webrtc` mode | D-08 web verification surface in Phase 1; the real client is Phase 5 `[VERIFIED: pip index]` |
| kokoro-onnx | 0.5.0 (pulled by `[evals]`) | Local TTS to synthesize the *user's* utterances for eval scenarios | Keeps PIPE-07's three-key constraint — no fourth API key `[VERIFIED: PyPI JSON API; requires-python <3.14]` |
| moonshine-voice | 0.0.65 (pulled by `[evals]`) | Local STT to transcribe bot audio in audio-mode scenarios | Same three-key rationale `[VERIFIED: pip index]` |
| pyaudio / portaudio | `pyaudio~=0.2.14` + brew `portaudio` | Terminal mic/speaker mode | `[local]` extra; **portaudio is NOT installed on this machine** — see Environment Availability |
| pytest + pytest-asyncio | latest stable | Unit tests (config parsing, factory, harness report) | Wave 0 — no test infra exists yet `[ASSUMED]` (versions to pin at install time) |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Built-in `pipecat eval` harness for barge-in scenarios | Fully custom WAV-injection harness | Custom gives arbitrary recorded-audio input, but re-implements the virtual mic, RTVI client, scheduling, and judging that 1.5.0 ships; eval's TTS-generated cached WAVs are deterministic "recorded audio" for scripted turns |
| `UserBotLatencyObserver` + thin JSON writer | Hand-rolled frame-timestamp observer | The built-in one already anchors VAD/user-stop→bot-start and collects per-service TTFB; only serialization is custom |
| `tomllib` (stdlib) | pydantic-settings / dynaconf / tomlkit | Overkill; read-only TOML + a small dataclass validator is simpler and has zero supply-chain surface |
| Terminal mode via `LocalAudioTransport` | Forcing runner to do terminal audio | The 1.5.0 runner has **no** local-audio route (webrtc/daily/telephony/eval only) — a thin separate entrypoint reusing the same pipeline factory is the intended shape `[VERIFIED: v1.5.0 runner source]` |
| Anthropic judge via `judge.eval.factory` | Ollama (default judge) or OpenAI | Ollama needs a local model install; OpenAI needs a 4th key. `AnthropicLLMService.run_inference` exists, so a one-line factory keeps three keys `[VERIFIED: v1.5.0 source]` |

**Installation:**

```bash
# apps/voice (Python 3.12, uv)
uv init --python 3.12
uv add "pipecat-ai[webrtc,deepgram,anthropic,runner]~=1.5.0"
uv add --group dev "pipecat-ai[local,evals]~=1.5.0" pytest pytest-asyncio
# macOS prerequisite for pyaudio:
brew install portaudio uv
```

**Version verification (performed 2026-07-04):**
- `pipecat-ai` 1.5.0 latest on PyPI `[VERIFIED: pip index versions]`
- `pipecat-ai-prebuilt` 1.0.3; `kokoro-onnx` 0.5.0 (JSON API; pip filters it out on Python 3.14 hosts — project uses 3.12); `moonshine-voice` 0.0.65 `[VERIFIED: PyPI]`
- npm (not needed until Phase 5, checked anyway): `@pipecat-ai/client-js` 1.12.0, `@pipecat-ai/small-webrtc-transport` 1.10.5 `[VERIFIED: npm view]`

## Package Legitimacy Audit

Seam command run: `gsd-tools query package-legitimacy check --ecosystem pypi pipecat-ai pipecat-ai-prebuilt kokoro-onnx moonshine-voice`

| Package | Registry | Age | Downloads | Source Repo | Verdict | Disposition |
|---------|----------|-----|-----------|-------------|---------|-------------|
| pipecat-ai | PyPI | years (0.0.9→1.5.0, 100+ releases) | unknown (PyPI API) | github.com/pipecat-ai/pipecat | [SUS] (`too-new`: 1.5.0 published today; `unknown-downloads`) | Flagged — keep; identity independently confirmed by cloning the official repo's v1.5.0 tag |
| pipecat-ai-prebuilt | PyPI | 4 releases | unknown | none on PyPI metadata | [SUS] (`no-repository`, `too-new`) | Flagged — keep; it is a declared dependency of pipecat-ai's own `[runner]` extra in the official pyproject (trust inherited), installed transitively |
| kokoro-onnx | PyPI | 18+ releases since 0.1.0 | unknown | github.com/thewh1teagle/kokoro-onnx | [SUS] (`unknown-downloads`) | Flagged — keep; declared dep of official `[kokoro]` extra, installed transitively |
| moonshine-voice | PyPI | 30+ releases | unknown | github.com/moonshine-ai/moonshine | [SUS] (`too-new` latest, `unknown-downloads`) | Flagged — keep; declared dep of official `[evals]` extra, installed transitively |
| pytest / pytest-asyncio | PyPI | 15+ yrs / mature | massive | pytest-dev | not run through seam | `[ASSUMED]` ubiquitous; planner may gate install |

**Packages removed due to [SLOP] verdict:** none
**Packages flagged as suspicious [SUS]:** pipecat-ai, pipecat-ai-prebuilt, kokoro-onnx, moonshine-voice — all four flags are artifacts of PyPI not exposing download counts to the seam plus very recent release dates on actively developed packages. Mitigating evidence: long release histories, official GitHub orgs, and the three transitive ones are declared in pipecat's own pyproject. **Planner must still add a `checkpoint:human-verify` task before the first `uv add` per protocol.**

## Architecture Patterns

### System Architecture Diagram

```
                          ┌────────────────────────────────────────────┐
   Entry points           │        apps/voice  (one codebase)          │
                          │                                            │
 Browser localhost page   │  bot.py ── runner: -t webrtc ──┐           │
 (pipecat-ai-prebuilt) ──▶│                                │           │
   HTTP /api/offer + UDP  │  pipecat eval run ─ -t eval ───┤           │
                          │  (YAML scenarios, virtual mic) │           │
 Terminal mic/speaker ───▶│  console.py ─ LocalAudioTransport          │
   (PyAudio)              │                                │           │
                          │                                ▼           │
                          │        build_pipeline(cfg: pipeline.toml)  │
                          │                                │           │
                          │   transport.input()            │           │
                          │        │                       │           │
                          │        ▼                       │           │
                          │   STT (config switch) ─────────┼─────────┐ │
                          │    A/B: DeepgramSTTService     │         │ │
                          │         (nova-3-general)       │         │ │
                          │      or DeepgramFluxSTTService │         │ │
                          │         (flux-general-en,      │  Deepgram │
                          │          owns turn detection)  │   API   │ │
                          │        │                       │         │ │
                          │        ▼                       │         ◀─┘
                          │   user_aggregator  ◀── turn strategies:   │
                          │   (LLMContext)          A: VAD+timeout    │
                          │        │                B: SmartTurn v3   │
                          │        ▼                C: external(Flux) │
                          │   AnthropicLLMService (claude-haiku-4-5) ─┼──▶ Anthropic API
                          │        │  streaming tokens → sentences    │
                          │        ▼                                  │
                          │   ElevenLabsTTSService (WS, word          │
                          │   timestamps, eleven_flash_v2_5) ─────────┼──▶ ElevenLabs WS
                          │        │                                  │
                          │        ▼                                  │
                          │   transport.output() ── paces TTSText     │
                          │        │                 frames to audio  │
                          │        ▼                                  │
                          │   assistant_aggregator (commits only      │
                          │   words actually spoken; truncates on     │
                          │   InterruptionFrame)                      │
                          │                                           │
                          │   observers: UserBotLatencyObserver ──▶ JSON artifact
                          │              + harness report writer  ──▶ rich console table
                          └────────────────────────────────────────────┘
```

Interruption path (barge-in): user speech → turn-start strategy (VAD start / Flux StartOfTurn → `BotInterruptionFrame`) → `InterruptionFrame` broadcast → TTS resets word timestamps + closes ElevenLabs context → output transport flushes → assistant aggregator commits partial (spoken-only) text.

### Recommended Project Structure

```
apps/voice/
├── pyproject.toml            # uv; pipecat-ai[webrtc,deepgram,anthropic,runner]~=1.5.0
├── uv.lock
├── .env                      # gitignored; written by scripts/bootstrap_env.*
├── pipeline.toml             # checked in — stage selection, knobs, persona path, voice id
├── prompts/
│   └── concierge.md          # versioned persona prompt v1 (KPH/"K")
├── src/klanker_voice/        # package name avoids the banned term
│   ├── config.py             # tomllib → frozen dataclasses + validation
│   ├── factories.py          # registry: stt/llm/tts/turn-strategy builders from config
│   ├── pipeline.py           # build_pipeline(cfg, transport) → PipelineWorker parts
│   ├── observers.py          # LatencyReportObserver (subclasses UserBotLatencyObserver use)
│   └── harness/
│       ├── report.py         # p50/p95 aggregation → rich table + JSON artifact
│       └── __main__.py       # harness CLI entry
├── bot.py                    # runner entry: transport_params {webrtc, eval}; main()
├── console.py                # terminal mode: LocalAudioTransport + same build_pipeline
├── scenarios/                # pipecat eval YAML: greeting, turns, barge-in cases
│   ├── greeting.yaml
│   ├── bargein_early.yaml    # interrupt ≤200ms after bot speech starts
│   ├── bargein_mid.yaml      # interrupt mid-sentence
│   └── bargein_monologue.yaml
├── scripts/
│   └── bootstrap_env.sh      # aws ssm get-parameter /kmv/bootstrap/* → .env (D-10)
└── tests/                    # pytest: config, factories, report math
docs/
└── TUNING.md                 # D-12 verdict record
```

### Pattern 1: 1.5.0 canonical pipeline shape (Settings objects, aggregator-owned VAD)

**What:** The current blessed bot shape, from pipecat's own 1.5.0 CLI templates.
**When to use:** Everywhere. Bare constructor kwargs (`model=...` directly on services) are deprecated since 0.0.105 and removed in 2.0 — use each service's `Settings` object.
**Example:**

```python
# Source: pipecat v1.5.0 cli/templates/server/bot_cascade.py.jinja2 + _macros (adapted)
from pipecat.audio.vad.silero import SileroVADAnalyzer
from pipecat.pipeline.pipeline import Pipeline
from pipecat.pipeline.worker import PipelineWorker, PipelineParams
from pipecat.processors.aggregators.llm_response_universal import (
    LLMContextAggregatorPair, LLMUserAggregatorParams,
)
from pipecat.processors.aggregators.llm_context import LLMContext
from pipecat.services.anthropic.llm import AnthropicLLMService
from pipecat.services.deepgram.stt import DeepgramSTTService
from pipecat.services.elevenlabs.tts import ElevenLabsTTSService

stt = DeepgramSTTService(api_key=..., settings=DeepgramSTTService.Settings(model="nova-3-general"))
llm = AnthropicLLMService(api_key=..., settings=AnthropicLLMService.Settings(model="claude-haiku-4-5"))
tts = ElevenLabsTTSService(api_key=..., voice_id=cfg.voice_id,
                           settings=ElevenLabsTTSService.Settings(model="eleven_flash_v2_5", speed=1.1))

context = LLMContext()   # system prompt = prompts/concierge.md contents
user_agg, assistant_agg = LLMContextAggregatorPair(
    context,
    user_params=LLMUserAggregatorParams(
        vad_analyzer=SileroVADAnalyzer(),      # VAD lives HERE now, not on the transport
        # user_turn_strategies=...             # A/B selection point (Pattern 4)
    ),
)

pipeline = Pipeline([
    transport.input(), stt, user_agg, llm, tts, transport.output(), assistant_agg,
])
worker = PipelineWorker(pipeline, params=PipelineParams(enable_metrics=True, enable_usage_metrics=True))
```

(`PipelineTask`/`PipelineRunner` still exist as compat subclasses of `PipelineWorker`/`WorkerRunner` — use the new names.) `[VERIFIED: v1.5.0 source]`

### Pattern 2: TOML → dataclass → factory registry (PIPE-04 swap mechanism)

**What:** `pipeline.toml` selects providers/knobs; `config.py` parses with stdlib `tomllib` into frozen dataclasses; `factories.py` maps `(kind, provider)` → builder function. API keys come only from env (`.env` loaded by the runner's python-dotenv).
**When to use:** All service construction. This file is a Phase-4 prod artifact — keep the schema stable and boring.
**Example sketch (Claude's discretion on exact schema):**

```toml
# pipeline.toml — no secrets, ever
[stt]
provider = "deepgram-nova3"     # or "deepgram-flux"
model = "nova-3-general"

[turn]                          # only read when stt.provider != deepgram-flux
strategy = "smart_turn_v3"      # "vad_timeout" | "smart_turn_v3"
vad_stop_secs = 0.2
user_speech_timeout = 0.6

[stt.flux]                      # only read when provider = deepgram-flux
eot_threshold = 0.7
eager_eot_threshold = 0.0       # 0 = disabled

[llm]
provider = "anthropic"
model = "claude-haiku-4-5"

[tts]
provider = "elevenlabs"
model = "eleven_flash_v2_5"
voice_id = ""                   # filled after the D-02 audition
speed = 1.1                     # WS range 0.7–1.2

[persona]
prompt_path = "prompts/concierge.md"
```

### Pattern 3: Latency harness = built-in observer + eval scenarios (PIPE-05, PIPE-02)

**What:** Two complementary instruments, both mostly built-in:
1. **Numbers (D-11):** attach `UserBotLatencyObserver` (`pipecat.observers.user_bot_latency_observer`) to `PipelineWorker(observers=[...])` with `enable_metrics=True`. It emits `on_latency_measured` (user-stop→bot-start voice-to-voice, seconds), `on_latency_breakdown` (timeline of per-service `TTFBBreakdownMetrics` — STT/LLM/TTS TTFB — plus text-aggregation durations), and `on_first_bot_speech_latency`. A thin custom handler accumulates turns → p50/p95 → rich table + JSON file. Stage names in the JSON must stay stable (Phase 5 HUD/CI consumes them).
2. **Named scenarios (barge-in + behavior):** `pipecat eval run scenarios/*.yaml --bot-url ws://localhost:7860` against `python bot.py -t eval`. Scenarios support `user.modality: audio` with `user.speech` (Kokoro local TTS → cached WAVs played through the real input path by the virtual microphone — deterministic "recorded audio"), `within_ms` per expectation, and `send_after: {event: llm_started, delay_ms: 500}` — built for barge-in timing. `--record-dir` captures session audio for ear-verification of truncation.
**When to use:** Harness runs during tuning; scenarios as the named barge-in verification (bargein_early / bargein_mid / bargein_monologue per PITFALLS.md #3) and greeting check (D-04).
**Verification of truncation (PIPE-02):** after each interruption scenario, log/inspect the last assistant message in `LLMContext` and assert it is a prefix of the full generated response (the eval judge or a custom expectation can check the next turn's coherence).

### Pattern 4: Endpointing A/B as a turn-strategy matrix (PIPE-04)

**What:** Three config-selectable arms; each is a different `user_turn_strategies` value on `LLMUserAggregatorParams`:

| Arm | STT | Turn stop | Knobs | Notes |
|-----|-----|-----------|-------|-------|
| A: Nova-3 + VAD | `DeepgramSTTService` | `SpeechTimeoutUserTurnStopStrategy(user_speech_timeout=0.6)` | VAD `stop_secs` (default 0.2), `user_speech_timeout` | The two timers run **after VAD stop** and the STT wait short-circuits on finalized transcripts — 1.5.0 already avoids naive double-counting, but total ≈ `stop_secs + user_speech_timeout` of intentional silence |
| B: Nova-3 + SmartTurn | `DeepgramSTTService` | `TurnAnalyzerUserTurnStopStrategy(turn_analyzer=LocalSmartTurnAnalyzerV3())` | `SmartTurnParams.stop_secs` (ceiling), analyzer confidence | This is the 1.5.0 **default** — ~12ms CPU inference, laptop + 1 vCPU Fargate feasible |
| C: Flux | `DeepgramFluxSTTService` | *(none — service auto-installs `ExternalUserTurnStrategies`)* | `eot_threshold` 0.5–0.9 (default 0.7); optionally `eager_eot_threshold` | Flux `StartOfTurn` also drives barge-in (`should_interrupt=True` default); median EOT <300ms, p95 1.5s |

**Critical wiring rule:** for arm C, do **not** pass your own `user_turn_strategies` — Flux's `service_metadata_frame()` recommends external strategies and the aggregator honors it *unless the user passed their own*, which would re-enable local turn detection and reintroduce the double-count trap. `[VERIFIED: v1.5.0 source, services/deepgram/flux/base.py]`
**Latency budget (informational, D-13):** endpointing silence (A: ~400–800ms; B: model-driven; C: <300ms median) + STT final + Haiku TTFT + first-sentence aggregation + ElevenLabs Flash first audio (~75–150ms) + transport. Flux's headroom is why it's the challenger to beat.

### Pattern 5: Barge-in truncation — trust the frame path, verify by test

**What:** No custom truncation code. ElevenLabs WS returns per-chunk `alignment` (char start/durations); the service converts to word timestamps (`calculate_word_times`) and registers them (`add_word_timestamps`); the output transport releases `TTSTextFrame`s in sync with audio playout; the assistant aggregator appends only text frames that arrived; `InterruptionFrame` → `_trigger_assistant_turn_stopped(interrupted=True)` commits the partial (spoken-only) aggregation, and the TTS handler resets word timestamps, clears the serialization queue, and closes the ElevenLabs context (`close_context` message) so synthesis stops server-side.
**When to use:** Always — this is default behavior. The named eval scenarios are the safety net, not the mechanism.
**Known edge:** ElevenLabs alignment restarts can garble words (upstream issue #4316) — 1.5.0 contains a mitigation (`_select_alignment` normalized-fallback). If transcripts of interrupted turns look garbled, that's the place to look. `[VERIFIED: v1.5.0 source]`

### Pattern 6: Greet-first (D-04) and persona loading (PIPE-06)

**What:** Load `prompts/concierge.md` as the system message of `LLMContext` at construction. On `transport.event_handler("on_client_connected")`, queue an `LLMRunFrame` so the LLM produces the greeting immediately — the standard pipecat greet-first shape. Keep the greeting instruction in the persona prompt itself ("Open with: introduce yourself as K…") so copy iterates without code changes.
**Note for eval mode:** the eval transport suppresses the greeting in text-mode scenarios via `LLMConfigureOutputFrame` before `on_client_connected` — greet-first is compatible with the harness. `[VERIFIED: v1.5.0 source, evals/transport.py]`

### Anti-Patterns to Avoid

- **0.x tutorial shapes:** `vad_analyzer=` on `TransportParams`, `aggregation_timeout=`, `allow_interruptions=` on PipelineParams — none of these exist in 1.5.0. Any snippet showing them is stale; recheck against source.
- **"Baseline VAD" that is secretly SmartTurn:** omitting `user_turn_strategies` gives you SmartTurn v3 (the new default), not plain VAD. The A/B arms must set strategies **explicitly** or the verdict is measuring the wrong thing.
- **Adding a fourth provider dependency for the harness:** eval user-audio (Kokoro) and transcription (Moonshine) run locally; the judge uses an Anthropic factory. Don't reach for OpenAI/Cartesia.
- **Custom truncation bookkeeping:** re-implementing spoken-word tracking on top of the aggregator will fight the frame path and break on upgrades.
- **Secrets in TOML or committed .env** — D-09/D-10 are absolute; `.env` is gitignored, SSM is the source of truth.
- **Terminal mode through the runner:** the runner has no local-audio route; don't hack one in — separate thin entrypoint.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Turn detection / endpointing | Silence-timer state machine | SmartTurn v3 / Flux EOT / `SpeechTimeoutUserTurnStopStrategy` | 1.5.0's strategy system already handles VAD/STT races, watchdogs, finalized-transcript short-circuits |
| Voice-to-voice + per-stage timing | Frame-timestamp logger | `UserBotLatencyObserver` (+ thin JSON writer) | Correct anchoring (VAD events, function-call exclusion) already done |
| Barge-in test driver | Custom WebSocket client injecting WAVs | `pipecat eval run` + YAML `send_after` | Virtual mic feeds real-time-paced audio + silence exactly like a live mic; scheduling anchors on bot events |
| Spoken-word context truncation | Post-hoc transcript diffing | Word-timestamped `TTSTextFrame` path (built-in) | Verified working in 1.5.0; custom code would race the frame pipeline |
| TOML parsing | Regex/ini hacks or new deps | stdlib `tomllib` | Python 3.11+ built-in |
| .env loading | Custom env parser | `python-dotenv` (already in `[runner]` extra) | Runner already calls `load_dotenv()` |
| Console tables | printf formatting | `rich` (already in tree via `[evals]`) | Free, consistent output |
| Localhost web client | Hand-rolled HTML/JS page | `pipecat-ai-prebuilt` (runner serves it) | Phase 1 needs a verification surface, not a product client |

**Key insight:** Pipecat 1.5.0 absorbed almost everything this phase needs (turn detection, latency metrics, eval harness, truncation). Phase-1 custom code is: config/factories, the JSON/table report writer, the persona prompt, scenario YAMLs, the SSM bootstrap script, and two thin entrypoints. If a task looks like framework plumbing, check the source first — it probably exists.

## Common Pitfalls

### Pitfall 1: Tuning knobs moved — stale advice measures the wrong thing
**What goes wrong:** Plans built from 0.x-era guidance (including parts of `.planning/research/PITFALLS.md` #1) set `aggregation_timeout`, transport-level VAD, or assume SmartTurn pulls torch. None applies to 1.5.0.
**Why it happens:** pipecat restructured aggregation/turn-taking across 1.x; most blog posts predate it.
**How to avoid:** The 1.5.0 latency knob inventory is: `LLMUserAggregatorParams(audio_idle_timeout=1.0, user_turn_stop_timeout=5.0)`, VAD params (`stop_secs=0.2`, `start_secs=0.2`, `confidence=0.7`), `SpeechTimeoutUserTurnStopStrategy(user_speech_timeout=0.6)`, `SmartTurnParams.stop_secs`, Flux `eot_threshold`/`eager_eot_threshold`. Record all non-default values in `pipeline.toml` + `docs/TUNING.md`.
**Warning signs:** A task references a parameter that greps to nothing in the pinned source.

### Pitfall 2: The default turn stop strategy is SmartTurn, not VAD
**What goes wrong:** "Nova-3+VAD" A/B arm accidentally runs SmartTurn v3 because no explicit strategies were set; the endpointing verdict is garbage.
**How to avoid:** Arm A must pass `UserTurnStrategies(stop=[SpeechTimeoutUserTurnStopStrategy(...)])` explicitly; arm B passes the turn-analyzer strategy explicitly too (even though it matches the default) so the config is self-documenting.
**Warning signs:** Log line "Loading Local Smart Turn v3.x model" during an arm-A run.

### Pitfall 3: Explicit strategies override Flux's external-turn recommendation
**What goes wrong:** Passing `user_turn_strategies` while using `DeepgramFluxSTTService` silently reinstates local turn detection alongside Flux's server-side EOT — double endpointing, double latency, double interruptions.
**How to avoid:** Factory rule: when `stt.provider == deepgram-flux`, never set `user_turn_strategies` (and don't set `vad_analyzer` unless VAD events are wanted for metrics-only anchoring — validate this combination during tuning before relying on it).
**Warning signs:** Two user-stopped-speaking signals per turn in logs; turn latency higher with Flux than Nova-3.

### Pitfall 4: eager EOT trades money for milliseconds
**What goes wrong:** Enabling `eager_eot_threshold` fires speculative LLM runs (EagerEndOfTurn → inference; TurnResumed → wasted call) — 50–70% more LLM calls for 150–250ms.
**How to avoid:** A/B it consciously; it's a Haiku-cost lever, cheap at demo scale but should be a recorded verdict in TUNING.md, not an accident.

### Pitfall 5: macOS terminal mode fails at install, not runtime
**What goes wrong:** `uv add pipecat-ai[local]` fails building pyaudio because portaudio isn't installed (it currently isn't on this machine); separately `uv` itself is not installed.
**How to avoid:** Environment-setup task runs `brew install uv portaudio` before any Python work; keep `[local]` in the dev group only so CI/Fargate images never need portaudio.

### Pitfall 6: Echo self-interruption in terminal/speaker testing
**What goes wrong:** Laptop speakers feed bot audio back into the mic; VAD/Flux detects "user speech," bot interrupts itself in a loop (PITFALLS.md #7 — still true).
**How to avoid:** Prompt-tuning with headphones; run barge-in scenarios through the eval harness (no acoustic loop); speakerphone testing is a deliberate later exercise. Flux `should_interrupt` and VAD `confidence`/`start_secs` are the anti-self-trigger knobs.

### Pitfall 7: Harness numbers vs. eval budgets measure different clocks
**What goes wrong:** `within_ms` in eval scenarios is measured from the harness's send; `UserBotLatencyObserver` measures from in-pipeline user-stop. Mixing them in one table produces incoherent A/B deltas.
**How to avoid:** D-11's p50/p95 table comes from the observer only; eval `within_ms` is pass/fail plumbing for named scenarios. Document the anchor of every number in the JSON schema.

### Pitfall 8: ElevenLabs voice-settings changes mid-session
**What goes wrong:** Changing `speed`/`stability` at runtime doesn't apply until the current WS context closes — audition tooling that flips settings mid-stream hears no difference and misleads the D-02 audition.
**How to avoid:** Render audition candidates as three separate short sessions (or via the HTTP service) — one voice per render. `[VERIFIED: v1.5.0 source docstring]`

## Code Examples

### Terminal mode entry (console.py)

```python
# Source: pipecat v1.5.0 transports/local/audio.py (constructor surface verified)
from pipecat.transports.local.audio import LocalAudioTransport, LocalAudioTransportParams

transport = LocalAudioTransport(LocalAudioTransportParams(
    audio_in_enabled=True,
    audio_out_enabled=True,
))
# then: pipeline = build_pipeline(cfg, transport); worker = PipelineWorker(...); await runner.run()
```

### Runner entry with webrtc + eval routes (bot.py)

```python
# Source: pipecat v1.5.0 cli/templates/server/_macros/transport_setup.jinja2 (adapted)
from pipecat.transports.base_transport import TransportParams
from pipecat.evals.transport import EvalTransportParams
from pipecat.runner.types import RunnerArguments
from pipecat.runner.utils import create_transport

transport_params = {
    "webrtc": lambda: TransportParams(audio_in_enabled=True, audio_out_enabled=True),
    "eval":   lambda: EvalTransportParams(audio_in_enabled=True, audio_out_enabled=True),
}

async def bot(runner_args: RunnerArguments):
    transport = await create_transport(runner_args, transport_params)
    await run_bot(transport, runner_args)   # builds pipeline from pipeline.toml

if __name__ == "__main__":
    from pipecat.runner.run import main
    main()          # python bot.py -t webrtc  → localhost page (prebuilt UI)
                    # python bot.py -t eval    → eval harness target
```

### Flux arm construction

```python
# Source: pipecat v1.5.0 services/deepgram/flux/stt.py docstring
from pipecat.services.deepgram.flux import DeepgramFluxSTTService

stt = DeepgramFluxSTTService(
    api_key=os.environ["DEEPGRAM_API_KEY"],
    settings=DeepgramFluxSTTService.Settings(
        model="flux-general-en",
        eot_threshold=0.7,          # 0.5–0.9; lower = faster, more false EOTs
        # eager_eot_threshold=0.5,  # optional: −150–250ms for +50–70% LLM calls
    ),
)
# Do NOT set user_turn_strategies / vad in this arm — Flux installs ExternalUserTurnStrategies.
```

### Latency report observer

```python
# Source: pipecat v1.5.0 observers/user_bot_latency_observer.py (event surface verified)
from pipecat.observers.user_bot_latency_observer import UserBotLatencyObserver

latency_obs = UserBotLatencyObserver()

@latency_obs.event_handler("on_latency_measured")
async def on_latency(obs, latency_secs: float):
    report.add_voice_to_voice(latency_secs)

@latency_obs.event_handler("on_latency_breakdown")
async def on_breakdown(obs, breakdown):    # per-service TTFBs + text aggregation, timeline-ordered
    report.add_breakdown(breakdown)        # → JSON artifact + rich table at session end

worker = PipelineWorker(pipeline,
                        params=PipelineParams(enable_metrics=True, enable_usage_metrics=True),
                        observers=[latency_obs])
```

### Barge-in eval scenario (sketch)

```yaml
# Source: pipecat v1.5.0 evals/scenario.py + harness.py docstrings (schema verified)
name: bargein_mid_sentence
user:
  modality: audio
  speech: { service: kokoro }          # local TTS — no 4th API key
judge:
  eval: { factory: "klanker_voice.harness.judge_factory" }   # returns AnthropicLLMService
turns:
  - expect:                             # K greets first (D-04)
      - event: tts_response
        within_ms: 3000
  - user: "Tell me the long version of the klanker story"
    expect:
      - event: llm_started
  - user: "wait, stop — shorter please"
    send_after: { event: llm_started, delay_ms: 1500 }   # interrupt mid-monologue
    expect:
      - event: user_started_speaking
      - event: llm_response
        eval: "Bot acknowledges being cut off or answers briefly; does not repeat the interrupted sentence verbatim"
        within_ms: 4000
```

## State of the Art

| Old Approach (0.x / prior research) | Current Approach (1.5.0) | When Changed | Impact |
|--------------------------------------|--------------------------|--------------|--------|
| `aggregation_timeout=1.0` latency trap | Gone; `UserTurnController` + stop strategies (`user_speech_timeout=0.6` default in timeout strategy) | 1.x line | Pitfall #1 from PITFALLS.md is obsolete in its specifics; knobs renamed |
| `vad_analyzer` on TransportParams | `LLMUserAggregatorParams(vad_analyzer=...)` | 1.x | Pipeline wiring changed |
| SmartTurn = torch/2GB bloat, "skip it" (STACK.md) | SmartTurn **v3** bundled ONNX, 8 MB, ~12ms CPU, **the default** | pipecat ~0.0.87+/1.x, model v3 2025 | STACK.md's smart-turn advice is outdated; the `local-smart-turn` extra (torch) is only for legacy v2 |
| Flux "flag for A/B, check support" | `DeepgramFluxSTTService` first-class, auto external turn strategies | present by 1.5.0 | A/B arm C is config, not integration work |
| Custom harness required for latency/barge-in tests | Built-in `pipecat eval` + `UserBotLatencyObserver` | 1.x | Harness scope shrinks dramatically |
| Bare ctor kwargs (`model=`, `params=InputParams(...)`) | `Settings` objects per service | deprecated 0.0.105, removed 2.0 | Write all construction the new way |
| `PipelineTask` / `PipelineRunner` | `PipelineWorker` / `WorkerRunner` (old names are compat subclasses) | 1.x | Use new names in fresh code |

**Deprecated/outdated:**
- `pipecat-ai[silero]`, `pipecat-ai[elevenlabs]` extras: exist but empty (core) — harmless, unnecessary.
- Deepgram `LiveOptions`: compatibility shim, removed in 2.0 — use `DeepgramSTTService.Settings`.
- `pipecat-ai-small-webrtc-prebuilt` (separate pkg): superseded by `pipecat-ai-prebuilt` bundled in `[runner]`.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | Flux pricing $0.0065/min (en) / $0.0078/min (multi) PAYG | Standard Stack / Pattern 4 | Cost model off; verdict economics unchanged (both trivial at demo scale) `[ASSUMED — web roundup, not vendor page fetch]` |
| A2 | SmartTurn v3 at ~60ms on 1 vCPU Fargate is acceptable in-budget | Pattern 4 | If slower on Fargate, arm B loses in Phase 4 re-verification; Flux unaffected `[ASSUMED — extrapolated from "low-cost AWS instance" claim]` |
| A3 | Anthropic judge via `judge.eval.factory` works end-to-end | Pattern 3 | `run_inference` exists on `AnthropicLLMService` (source-verified) but the factory path wasn't executed; fallback = `text_contains` expectations, no LLM judge |
| A4 | ~800ms typical voice-to-voice is achievable with Flux + Haiku + Flash from a laptop in us-east-1 proximity | Summary / Pattern 4 | D-13 keeps thresholds informational; if unreachable, PIPE-08 (ack masking) is the v2 lever |
| A5 | pytest/pytest-asyncio latest stable are appropriate pins | Supporting stack | Trivial; pin at install |
| A6 | Eval harness `user.modality: audio` + virtual mic satisfies "from recorded audio" in PIPE-05 | Pattern 3 | If UAT insists on human-recorded WAVs, add a small custom send path or record once through `--record-dir` and reuse; scope bump is small |

## Open Questions

1. **Does VAD-anchored latency measurement work in the Flux arm?**
   - What we know: `UserBotLatencyObserver` watches both VAD frames and `UserStoppedSpeakingFrame`; Flux emits the latter (no local VAD running by default).
   - What's unclear: whether the "VAD-stop" stage in the D-11 table is meaningful/populated under Flux, or collapses into user-stop.
   - Recommendation: harness JSON schema should mark stage anchors per arm; verify observer output in the first Flux run and document in TUNING.md.
2. **Exact wire format for the greeting trigger in 1.5.0** (`LLMRunFrame` vs context-frame push on `on_client_connected`).
   - What we know: templates register standard handlers; greet-first is the documented pattern; eval transport pre-suppresses greetings in text mode.
   - Recommendation: copy the generated template's handler body (`pipecat init` output or `_macros/event_handlers.jinja2`) during implementation — 15-minute verification, not a design risk.
3. **3-voice audition shortlist** (Claude's discretion, D-02): pick three fast-punchy, high-intelligibility ElevenLabs voices at build time from the current voice library — voice IDs change too often to lock in research.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Python 3.12 | runtime | ✓ | 3.12.12 (`/opt/homebrew/bin/python3.12`) | — |
| uv | package mgmt (constraint) | ✗ | — | `brew install uv` (setup task; no fallback desired) |
| portaudio | pyaudio / terminal mode | ✗ | — | `brew install portaudio` (setup task); web+eval modes work without it |
| ffmpeg | audio tooling (optional) | ✓ | 8.0.1 | — |
| node | (Phase 5 client; not needed now) | ✓ | v22.1.0 | — |
| AWS CLI | SSM bootstrap (D-10) | ✓ | 2.32.25 | — |
| `klanker-application` profile | SSM bootstrap | ✓ | profile exists | — |
| `/kmv/bootstrap/*` SSM params | key bootstrap | ✓ | all 3 present (`anthropic_api_key`, `deepgram_api_key`, `elevenlabs_api_key`, us-east-1) | — |
| Deepgram/Anthropic/ElevenLabs reachability | pipeline | assumed | — | keys verified present; first smoke run validates |

**Missing dependencies with no fallback:** none.
**Missing dependencies with fallback/fix:** `uv`, `portaudio` — both one-line brew installs; make them an explicit environment-setup task (Pitfall 5).

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | pytest + pytest-asyncio (none installed — greenfield, Wave 0) |
| Config file | none — Wave 0 (`apps/voice/pyproject.toml` `[tool.pytest.ini_options]`) |
| Quick run command | `uv run pytest tests/ -x -q` |
| Full suite command | `uv run pytest tests/ && uv run pipecat eval run scenarios/*.yaml --bot-url ws://localhost:7860` (eval part requires bot running with `-t eval`) |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| PIPE-01 | v2v latency measured, ✅/⚠️ vs 1.2s/800ms | harness (informational per D-13) | harness CLI run → JSON artifact | ❌ Wave 0 (harness is a deliverable) |
| PIPE-02 | Barge-in stops playback; context = spoken words | eval scenario (named) | `pipecat eval run scenarios/bargein_*.yaml` | ❌ Wave 0 (scenarios are deliverables) |
| PIPE-03 | In-session memory | eval scenario (multi-turn recall) | `pipecat eval run scenarios/memory.yaml` | ❌ Wave 0 |
| PIPE-04 | Config swap + A/B verdicts | unit (factory) + harness A/B runs | `uv run pytest tests/test_factories.py -x`; harness runs per arm | ❌ Wave 0 |
| PIPE-05 | Per-stage + v2v ms from recorded audio | unit (report math) + harness run | `uv run pytest tests/test_report.py -x` | ❌ Wave 0 |
| PIPE-06 | Persona speaks as K/KPH | eval scenario (judge/text_contains on greeting + identity Qs) | `pipecat eval run scenarios/greeting.yaml` | ❌ Wave 0 |
| PIPE-07 | Runs locally with 3 keys | smoke: bootstrap script + `python bot.py -t webrtc` connect | manual-only justification: requires human mic/ears for full check; automated smoke = process starts + `/api/offer` responds | ❌ Wave 0 |

### Sampling Rate
- **Per task commit:** `uv run pytest tests/ -x -q` (<30s — unit tests only)
- **Per wave merge:** unit suite + at least `greeting.yaml` eval scenario against a running bot
- **Phase gate:** full unit suite + all eval scenarios green + one harness JSON artifact per A/B arm recorded in `docs/TUNING.md` before `/gsd-verify-work`

### Wave 0 Gaps
- [ ] `apps/voice/pyproject.toml` pytest config + `tests/conftest.py` — shared fixtures (sample pipeline.toml, tmp .env)
- [ ] `tests/test_config.py` — covers PIPE-04 (TOML parse/validation, secret-rejection)
- [ ] `tests/test_factories.py` — covers PIPE-04 (arm construction incl. Flux no-strategies rule)
- [ ] `tests/test_report.py` — covers PIPE-05 (p50/p95 math, JSON schema stability)
- [ ] `scenarios/*.yaml` — covers PIPE-02/03/06
- [ ] Framework install: `uv add --group dev pytest pytest-asyncio`

## Security Domain

### Applicable ASVS Categories (Level 1; local-only phase — reduced surface)

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no (Phase 3) | — |
| V3 Session Management | no (Phase 3/4) | — |
| V4 Access Control | no (local) | — |
| V5 Input Validation | yes | Config validation in `config.py` (reject unknown providers, range-check knobs); scenario YAMLs parsed by pipecat's SafeLoader subclass |
| V6 Cryptography | no direct use | Never hand-roll; TLS to vendors handled by SDKs/websockets |
| V14 Configuration / Secrets | **yes — the phase's real security surface** | Keys only in `.env` (gitignored) sourced from SSM SecureString via `klanker-application` profile (D-10); assert `.env` in `.gitignore` before first write; TOML schema has no key fields by construction; bootstrap script must not echo secrets to stdout/logs |

### Known Threat Patterns for this stack

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| API key leakage via git commit | Information Disclosure | `.env` gitignored + bootstrap script writes with 0600; no keys in TOML/TUNING.md; pre-commit awareness in code review |
| Key leakage via logs | Information Disclosure | Never log service constructor args; loguru default formats are fine, avoid dumping Settings objects |
| Supply-chain (PyPI) | Tampering | Package Legitimacy Audit above; `uv.lock` committed; installs gated by checkpoint per SUS flags |
| Prompt injection via user speech | Tampering (LLM) | Low stakes in Phase 1 (no tools, no data access); persona prompt should still instruct steer-back behavior (D-06) rather than obedience to meta-instructions |

## Sources

### Primary (strongest available: pinned source tag, read directly)
- pipecat-ai v1.5.0 source (git tag `v1.5.0`, github.com/pipecat-ai/pipecat) — turn system (`turns/`), aggregators (`processors/aggregators/llm_response_universal.py`), Flux (`services/deepgram/flux/`), ElevenLabs (`services/elevenlabs/tts.py`), SmartTurn v3 (`audio/turn/smart_turn/local_smart_turn_v3.py`), observers (`observers/user_bot_latency_observer.py`), evals (`evals/`), runner (`runner/`), CLI templates, pyproject extras
- PyPI registry (pip index / JSON API, 2026-07-04): pipecat-ai 1.5.0, pipecat-ai-prebuilt 1.0.3, kokoro-onnx 0.5.0, moonshine-voice 0.0.65
- Live AWS check: `klanker-application` profile + all three `/kmv/bootstrap/*` SSM parameters present (us-east-1)

### Secondary (MEDIUM per classify-confidence seam — websearch, cross-checked)
- Deepgram Flux docs: [configuration](https://developers.deepgram.com/docs/flux/configuration), [eager EOT](https://developers.deepgram.com/docs/flux/voice-agent-eager-eot), [quickstart](https://developers.deepgram.com/docs/flux/quickstart) — eot_threshold range/default, eager tradeoff, <300ms median EOT
- Daily blog: [Smart Turn v3 — CPU inference in 12ms](https://www.daily.co/blog/announcing-smart-turn-v3-with-cpu-inference-in-just-12ms/), [Smart Turn v3.1 accuracy](https://www.daily.co/blog/improved-accuracy-in-smart-turn-v3-1/)

### Tertiary (LOW — single web roundups, marked ASSUMED)
- Flux pricing figures ($0.0065/min en): diyai.io / deepgram.com/pricing roundups (A1)

## Metadata

**Confidence breakdown:**
- Standard stack & versions: MEDIUM-high — registry-verified pins + extras read from the pinned pyproject
- Architecture patterns (turn system, truncation, harness, runner): MEDIUM — read directly from v1.5.0 source (strongest evidence available this session; seam caps web-verified at MEDIUM); behavior not executed
- Pitfalls: MEDIUM — derived from source deltas vs prior research + vendor docs
- Vendor pricing/latency figures: LOW-MEDIUM — web roundups, tagged in Assumptions Log

**Research date:** 2026-07-04
**Valid until:** ~2026-07-18 (pipecat releases fast; the `~=1.5.0` pin insulates code, but re-check the changelog before any bump — Pitfall 11 in project PITFALLS.md stands)
