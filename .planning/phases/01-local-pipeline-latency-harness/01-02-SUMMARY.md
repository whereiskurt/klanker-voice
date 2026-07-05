---
phase: 01-local-pipeline-latency-harness
plan: 02
subsystem: voice-pipeline
tags: [pipecat, deepgram, anthropic, elevenlabs, tomllib, persona, greet-first, turn-strategies]

# Dependency graph
requires:
  - "01-01: apps/voice uv project with pipecat-ai 1.5.0 locked, .env bootstrap via make env"
provides:
  - "pipeline.toml: checked-in single stage-selection surface (stt/turn/llm/tts/persona), zero credential fields (D-09)"
  - "load_config(): tomllib -> frozen dataclasses with provider allowlists, knob range checks, credential-field rejection, KLANKER_PIPELINE_CONFIG override"
  - "(kind, provider) factory registry with three-arm turn-strategy matrix; Flux + explicit strategies raises ValueError (Pitfall 3 enforced in code)"
  - "build_pipeline(cfg, transport) -> BuiltPipeline (pipeline + context + aggregator handles for the 01-03 harness)"
  - "prompts/concierge.md: KPH/'K' persona v1 (D-01, D-03..D-07) with greeting instruction in-prompt"
  - "bot.py (-t webrtc localhost page | -t eval harness target) and console.py (LocalAudioTransport terminal mode)"
affects: [01-03, 01-04, 01-05, phase-4-deploy]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "All service construction via 1.5.0 Settings objects; bare ctor kwargs never used"
    - "Turn arms set user_turn_strategies EXPLICITLY (the 1.5.0 default is SmartTurn — implicit config would measure the wrong A/B arm)"
    - "Flux arm: no vad_analyzer, no user_turn_strategies — Flux service metadata installs ExternalUserTurnStrategies"
    - "Greet-first: LLMRunFrame on on_client_connected (web/eval); direct queue at startup for LocalAudioTransport"
    - "API keys read from env only at build time; Settings objects never logged (T-1-03)"

key-files:
  created:
    - apps/voice/pipeline.toml
    - apps/voice/src/klanker_voice/config.py
    - apps/voice/src/klanker_voice/factories.py
    - apps/voice/src/klanker_voice/pipeline.py
    - apps/voice/prompts/concierge.md
    - apps/voice/bot.py
    - apps/voice/console.py
    - apps/voice/tests/conftest.py
    - apps/voice/tests/test_config.py
    - apps/voice/tests/test_factories.py
  modified: []

key-decisions:
  - "Flux wiring rule enforced at params-build time: deepgram-flux + explicit user_turn_strategies raises ValueError immediately, not at runtime"
  - "Terminal greet-first: LocalAudioTransport fires no on_client_connected (source-verified), so console.py queues LLMRunFrame directly at startup via greet_now()"
  - "TOML credential rejection implemented as recursive key-name regex walk (api_key/key/secret/token/password/credential/bearer/auth), so a pasted secret fails loudly at load"
  - "eager_eot_threshold=0.0 means disabled and is never passed to Flux Settings; nonzero values pass through (Pitfall 4 is a deliberate TUNING.md lever)"
  - "Requirements marked in frontmatter only — REQUIREMENTS.md/STATE.md/ROADMAP.md writes belong to the orchestrator (parallel wave)"

requirements-completed: [PIPE-03, PIPE-04, PIPE-06, PIPE-07]

coverage:
  - id: D1
    description: "Config contract: TOML round-trips through load_config; bad providers, out-of-range knobs, credential-looking fields, missing persona all rejected"
    requirement: "PIPE-04"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_config.py (21 tests, green)"
        status: pass
    human_judgment: false
  - id: D2
    description: "Three turn arms constructible purely from config; Flux arm provably sets no local strategies and no VAD; flux + explicit strategy raises"
    requirement: "PIPE-04"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_factories.py (9 tests, green)"
        status: pass
    human_judgment: false
  - id: D3
    description: "Walking skeleton boots: bot.py -t webrtc serves the prebuilt localhost page on :7860 with only the three .env keys"
    requirement: "PIPE-07"
    verification:
      - kind: integration
        ref: "live smoke: server boot + HTTP response on localhost:7860 (307 -> prebuilt client), clean shutdown"
        status: pass
    human_judgment: false
  - id: D4
    description: "K greets first in persona, remembers the session, and holds the same conversation in terminal mode"
    requirement: "PIPE-03, PIPE-06"
    verification:
      - kind: manual_uat
        ref: "plan human-check harvested to end-of-phase UAT: open :7860, listen for unprompted greeting, ask 'who are you?' + memory follow-up, then console.py with headphones"
        status: pending
    human_judgment: true

# Metrics
duration: 14min
completed: 2026-07-04
status: complete
---

# Phase 1 Plan 02: Walking Skeleton Summary

**Complete config-driven conversation loop: pipeline.toml -> validated dataclasses -> (kind, provider) factory registry with three explicit turn arms -> persona-seeded pipeline with greet-first, exposed through webrtc/eval runner and terminal entrypoints — all surfaces cross-checked against the pinned pipecat 1.5.0 source**

## Performance

- **Duration:** ~14 min active execution
- **Started:** 2026-07-05T00:33:47Z
- **Completed:** 2026-07-05T00:47:30Z
- **Tasks:** 3/3
- **Files created:** 10

## Accomplishments

- `pipeline.toml` is the single stage-selection surface: providers, models, endpointing knobs, persona path, voice settings — zero credential fields by construction (D-09); `load_config()` honors `KLANKER_PIPELINE_CONFIG` so the A/B plans are pure config work (PIPE-04)
- Validation is loud (ASVS V5, T-1-04): provider allowlists, speed 0.7-1.2, eot_threshold 0.5-0.9, timer knobs positive-under-5s, recursive credential-key rejection, persona-path existence
- Three-arm turn matrix real and unit-tested: `vad_timeout` (explicit SpeechTimeout stop — never the silent SmartTurn default, Pitfall 2), `smart_turn_v3` (explicit TurnAnalyzer), Flux (no local strategies/VAD; combining flux with explicit strategies raises ValueError at build time, Pitfall 3)
- `build_pipeline()` returns the canonical 1.5.0 cascade (transport.input -> stt -> user_agg -> llm -> tts -> transport.output -> assistant_agg) with persona-seeded LLMContext and metrics enabled from day one; barge-in truncation left entirely to the 1.5.0 frame path (Pattern 5, no custom bookkeeping)
- Persona v1 (`prompts/concierge.md`) encodes D-01 (KPH/"K"), D-03 (fast & punchy), D-04 (greet-first instruction in-prompt), D-05 (1-2 sentences + depth hook), D-06 (roll with it, steer back), D-07 (playful with teeth), plus prompt-injection steer-back posture (T-1-06); versioned header per PIPE-06
- Both D-08 run modes exist and share one pipeline path; live smoke passed — `bot.py -t webrtc` boots, serves the pipecat-ai-prebuilt page on :7860, and shuts down cleanly

## Task Commits

Each task was committed atomically:

1. **Task 1: pipeline.toml schema, config module, and persona prompt v1** - `094ba4b` (feat)
2. **Task 2: Factory registry and build_pipeline with greet-first** - `8bbeeaf` (feat)
3. **Task 3: Entrypoints for both run modes and live smoke (D-08)** - `aa7927f` (feat)

## Files Created/Modified

- `apps/voice/pipeline.toml` - stage selection + knobs + persona path + voice settings; consumed unchanged by Phase 4
- `apps/voice/src/klanker_voice/config.py` - tomllib -> frozen dataclasses; `load_config(path)` with env override; full validation
- `apps/voice/src/klanker_voice/factories.py` - `BUILDERS[(kind, provider)]` registry; `build_stt/llm/tts`, `build_user_aggregator_params` (three-arm matrix + Flux guard)
- `apps/voice/src/klanker_voice/pipeline.py` - `build_pipeline`, `build_worker` (metrics on), `register_greet_first`, `greet_now`, `BuiltPipeline` handles for 01-03
- `apps/voice/prompts/concierge.md` - KPH/"K" persona v1 with version header
- `apps/voice/bot.py` - runner entry: `-t webrtc` (prebuilt localhost page) and `-t eval` (harness target)
- `apps/voice/console.py` - LocalAudioTransport terminal mode; headphones docstring (Pitfall 6)
- `apps/voice/tests/{conftest.py,test_config.py,test_factories.py}` - 30 new unit tests (32 total green with 01-01 smoke)

## Decisions Made

- Flux misconfiguration is impossible, not discouraged: the guard raises at `build_user_aggregator_params` time
- Terminal-mode greeting queues `LLMRunFrame` directly at startup because `LocalAudioTransport` never fires `on_client_connected` (verified by grep across the pinned transports tree)
- Greet trigger shape confirmed against the installed 1.5.0 CLI template (`_macros/event_handlers.jinja2`): `LLMRunFrame` queued on `on_client_connected` — RESEARCH Open Question 2 closed as designed
- ElevenLabs `voice_id=""` maps to `Settings(voice=None)` until the D-02 audition lands a real id
- Did NOT touch REQUIREMENTS.md/STATE.md/ROADMAP.md — orchestrator owns shared-file writes in this parallel wave; `requirements-completed` frontmatter carries the linkage

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Plan assumed console.py could reuse the on_client_connected greet path**
- **Found during:** Task 3 (entrypoints)
- **Issue:** `LocalAudioTransport` does not emit `on_client_connected` (no occurrence in the pinned local/base transport sources) — wiring `register_greet_first` there would mean K never greets in terminal mode, violating D-04
- **Fix:** Added `greet_now(worker)` helper in pipeline.py; console.py queues the greeting directly after `add_workers`, before `runner.run()`; web/eval modes keep the event-driven path
- **Files modified:** apps/voice/src/klanker_voice/pipeline.py, apps/voice/console.py
- **Verification:** entrypoint import check green; greet path identical LLMRunFrame in both modes
- **Committed in:** 8bbeeaf / aa7927f

**2. [Rule 1 - Bug] pipeline.toml comment tripped the plan's own no-secrets grep**
- **Found during:** Task 1 verification
- **Issue:** The verify gate `! grep -Ei 'secret|token *='` matched the literal word "SECRETS" in a documentation comment
- **Fix:** Reworded the comment to "No credential material, ever" — gate now passes with the assertion intact
- **Files modified:** apps/voice/pipeline.toml
- **Committed in:** 094ba4b

---

**Total deviations:** 2 auto-fixed (both plan-assumption corrections, no scope creep)
**Impact on plan:** None on architecture; terminal greet is a 3-line helper.

## Issues Encountered

None — SSM `.env` regeneration succeeded first try in the worktree (no auth gate), all pinned-source surface checks (Settings classes, turn strategies, runner discovery, template greet shape) grepped clean before writing code (Pitfall 1 warning sign never fired).

## Known Stubs

- `pipeline.toml` `voice_id = ""` — intentional: the D-02 three-voice audition (plan 01-05) selects the voice; ElevenLabs falls back to its default voice until then. Config validation deliberately allows empty voice_id.
- Persona knowledge section is v1 copy — grounded only in PROJECT.md/design-spec facts; depth/wording iterate during prompt tuning (terminal mode exists exactly for this).

## User Setup Required

None — `.env` regenerates with `make -C apps/voice env` (klanker-application SSO was live).

## Human Verification Deferred to End-of-Phase UAT

Per plan: open http://localhost:7860 after `uv run python bot.py -t webrtc`, listen for the unprompted in-persona greeting (D-01/D-04), ask "who are you?" plus a memory follow-up (PIPE-03), then hold the same conversation via `uv run python console.py` with headphones (D-08). No automated check hears audio.

## Next Phase Readiness

- 01-03 (harness) gets `BuiltPipeline` handles, `-t eval` target, and metrics already enabled — observer drops in without rework
- 01-04 (A/B) is pure config: three arm TOMLs selected via `KLANKER_PIPELINE_CONFIG`, all arms unit-proven constructible
- 01-05 (audition) fills `voice_id`; schema stable for Phase 4 deploy

## Self-Check: PASSED

All 10 created files exist on disk; all three task commits (094ba4b, 8bbeeaf, aa7927f) present in git log; full suite 32 passed; `.env` gitignored; no "voiceai" string anywhere in produced files.

---
*Phase: 01-local-pipeline-latency-harness*
*Completed: 2026-07-04*
