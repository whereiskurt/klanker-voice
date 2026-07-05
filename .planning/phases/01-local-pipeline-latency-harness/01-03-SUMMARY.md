---
phase: 01-local-pipeline-latency-harness
plan: 03
subsystem: voice-pipeline
tags: [pipecat, latency-harness, evals, barge-in, anthropic-judge, kokoro, moonshine, rich]

# Dependency graph
requires:
  - "01-02: config module, factories, build_pipeline/build_worker handles, bot.py -t eval target, greet-first wiring, enable_metrics on"
provides:
  - "harness JSON schema v1: stage names vad_stop/stt_final/llm_ttft/tts_first_audio/voice_to_voice frozen; per-arm anchors; null-stage contract (consumed by Phase 5 HUD/CI)"
  - "LatencyReportObserver: UserBotLatencyObserver subclass -> per-turn TurnRecords -> incremental JSON artifact + rich table at session end"
  - "harness CLI: `python -m klanker_voice.harness report|compare` — re-render and A/B-diff artifacts from JSON alone (plan 01-04's TUNING.md instrument)"
  - "judge_factory -> AnthropicLLMService(claude-haiku-4-5) via judge.eval.factory hook (three-key constraint holds)"
  - "five passing eval scenarios: greeting, memory, bargein_early, bargein_mid, bargein_monologue — all audio-modality through the real input path"
affects: [01-04, 01-05, phase-5-hud, phase-5-ci-gate]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Observer numbers (D-11 table) and eval within_ms budgets are different clocks — never mixed; every JSON number's anchor documented in the artifact (Pitfall 7)"
    - "Stages an arm cannot populate serialize as null with an anchors entry explaining why — never silently omitted (Open Question 1)"
    - "Artifact written incrementally after every turn so a hard kill still leaves the record; table rendered once at EndFrame/CancelFrame"
    - "Barge-in interrupt assertions use user_transcription (discard-proof) rather than raw user_started_speaking (droppable by the harness's interruption discard)"
    - "Greet-first = developer kick message + LLMRunFrame (canonical 1.5.0 template shape) — a bare LLMRunFrame on a system-only context produces a briefing acknowledgment, not a greeting"

key-files:
  created:
    - apps/voice/src/klanker_voice/observers.py
    - apps/voice/src/klanker_voice/harness/__init__.py
    - apps/voice/src/klanker_voice/harness/report.py
    - apps/voice/src/klanker_voice/harness/judge.py
    - apps/voice/src/klanker_voice/harness/__main__.py
    - apps/voice/scenarios/greeting.yaml
    - apps/voice/scenarios/memory.yaml
    - apps/voice/scenarios/bargein_early.yaml
    - apps/voice/scenarios/bargein_mid.yaml
    - apps/voice/scenarios/bargein_monologue.yaml
    - apps/voice/tests/test_report.py
  modified:
    - apps/voice/bot.py
    - apps/voice/console.py
    - apps/voice/src/klanker_voice/pipeline.py
    - apps/voice/src/klanker_voice/factories.py
    - apps/voice/tests/test_factories.py

key-decisions:
  - "stt_final documented as often-null: streaming STT reports TTFB at the first partial while the user is still speaking (outside the user-stop->bot-start window); STT finalization wait is already inside vad_stop — verified in the first real artifact"
  - "Interim ElevenLabs premade voice (Rachel) hard-fallback when config voice_id is empty: the WS API rejects voice_id None outright (1008), plan-02's 'defaults to account voice' assumption was wrong"
  - "Greet kick uses the exact 1.5.0 template developer message so greet works on fresh AND reconnected (assistant-terminated) contexts"
  - "Barge-in scenarios assert user_transcription instead of user_started_speaking — the raw event races the harness's own interruption discard when kokoro utterances arrive as multiple VAD bursts"
  - "Judge criteria scoped to exactly the plan's assertions (brief + no verbatim restart) with explicit transcription-noise tolerance — coherence-quality grading of moonshine transcripts was the main flake source"

requirements-completed: [PIPE-02, PIPE-05]

coverage:
  - id: D1
    description: "Report math + schema v1: exact five stage names, anchors, null-stage handling, D-13 exit-free rendering"
    requirement: "PIPE-05"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_report.py (25 tests, green; 58 total suite)"
        status: pass
    human_judgment: false
  - id: D2
    description: "Both entrypoints emit JSON artifact + table with zero flags; CLI report/compare work from JSON alone, exit 0 despite warn verdicts"
    requirement: "PIPE-05"
    verification:
      - kind: integration
        ref: "live: eval session produced artifacts/harness/*.json (voice_to_voice n=10); compare rendered A/B diff of two real artifacts, exit 0"
        status: pass
    human_judgment: false
  - id: D3
    description: "Named scenarios pass from recorded audio through the real input path: greeting (D-04/PIPE-06), memory recall (PIPE-03), three barge-in cases with coherence judging (PIPE-02)"
    requirement: "PIPE-02, PIPE-03"
    verification:
      - kind: integration
        ref: "pipecat eval run — 5/5 passed against bot.py -t eval (final clean run, fresh bot)"
        status: pass
    human_judgment: false
  - id: D4
    description: "Barge-in stop-latency feel and audio abruptness at the interruption moment"
    requirement: "PIPE-02"
    verification:
      - kind: manual_uat
        ref: "play apps/voice/artifacts/eval-recordings/bargein_mid.wav — bot audio stops promptly (<~300ms subjective) and the follow-up acknowledges the cut-off"
        status: pending
    human_judgment: true

# Metrics
duration: 45min
completed: 2026-07-04
status: complete
---

# Phase 1 Plan 03: Latency Harness & Eval Scenarios Summary

**Measurement layer on pipecat 1.5.0 built-ins: LatencyReportObserver serializes UserBotLatencyObserver breakdowns into a frozen five-stage JSON schema v1 with per-arm anchors + rich verdict table (informational only, D-13), a report/compare CLI for A/B diffs, an Anthropic judge factory, and five passing audio-path eval scenarios covering greeting, memory, and the three named barge-in cases**

## Performance

- **Duration:** ~45 min active execution (includes 4 live eval-suite iterations)
- **Started:** 2026-07-05T00:49:42Z
- **Completed:** 2026-07-05T01:35:00Z
- **Tasks:** 3/3
- **Files created:** 11, modified: 5

## Accomplishments

- **Schema v1 is live and real** (PIPE-05, D-11): `artifacts/harness/*.json` with `schema_version: 1`, config metadata (arm/config path/providers/models — never env values, T-1-07), per-arm `anchors` documenting what every number is measured from, per-turn raw records, and `summary` keyed by exactly `vad_stop, stt_final, llm_ttft, tts_first_audio, voice_to_voice` (p50/p95/n). Unpopulatable stages serialize as null with the anchors entry explaining why.
- **D-13 enforced end-to-end:** the ⚠ verdict (first real run: voice_to_voice p50 1488ms / p95 1896ms — untuned, tuning is plan 01-04) renders in the table and the CLI still exits 0; unit tests pin the rule.
- **Zero-flag measurement:** both `bot.py` (webrtc/eval) and `console.py` attach the observer; the artifact writes incrementally per turn (hard kills still leave the record) and the table prints at EndFrame/CancelFrame.
- **A/B instrument ready for plan 01-04:** `python -m klanker_voice.harness compare a.json b.json` renders per-stage p50/p95 side-by-side with Δ columns — exercised against two real eval-session artifacts.
- **All five named scenarios pass** against `bot.py -t eval` using only local user-audio synthesis (kokoro), local bot-audio transcription (moonshine), and the Anthropic judge via `judge.eval.factory` — three keys total (PIPE-07 held).
- **Barge-in verified through the real audio path** (PIPE-02): early (~200ms after speech start), mid (~1500ms after llm_started), monologue (2500ms into speech). Traces show `bot_interrupted` firing and coherent brief follow-ups ("Got it — keeping it tight...") with no verbatim restart; recordings captured for ear-verification.

## Task Commits

1. **Task 1: LatencyReportObserver + p50/p95 report writer** - `f57f170` (feat)
2. **Task 2: Entrypoint wiring, harness CLI, judge factory** - `085b360` (feat)
3. **Task 3 deviations: voice fallback + greet kick** - `629c1ad` (fix)
4. **Task 3: five eval scenarios + anchor doc refinement** - `4e349e3` (feat)

## Files Created/Modified

- `apps/voice/src/klanker_voice/harness/report.py` - TurnRecord/Report, percentile math, schema v1 serialization, rich table + comparison table, informational verdicts
- `apps/voice/src/klanker_voice/observers.py` - LatencyReportObserver (subclasses UserBotLatencyObserver; no hand-rolled frame timing), arm naming, TTFB->stage classification, incremental artifact writes
- `apps/voice/src/klanker_voice/harness/judge.py` - `judge_factory(config)` -> AnthropicLLMService (claude-haiku-4-5), loads apps/voice/.env for the separate harness process
- `apps/voice/src/klanker_voice/harness/__main__.py` - typer CLI: `report` re-renders, `compare` diffs; exit 0 on verdicts, 1 only on genuine I/O/schema errors
- `apps/voice/scenarios/*.yaml` - five audio-modality scenarios sharing the kokoro user voice + moonshine transcription + Anthropic judge
- `apps/voice/tests/test_report.py` - 25 tests: percentile math (known values), schema stability, null stages, D-13 exit-free rendering, compare table
- `apps/voice/src/klanker_voice/pipeline.py` - greet_now/register_greet_first now take the context and append the canonical developer kick message
- `apps/voice/src/klanker_voice/factories.py` - interim ElevenLabs voice fallback (`INTERIM_ELEVENLABS_VOICE_ID`)

## Decisions Made

- **stt_final stage is usually null under this observer** — Deepgram reports TTFB at the first partial, while the user is still speaking, so it lands outside the user-stop->bot-start accumulator window; the STT finalization wait is already inside `vad_stop` (`user_turn_secs`). Documented in the anchors rather than hand-rolling custom STT timing (RESEARCH Don't Hand-Roll). Plan 01-04 should read the anchors when building TUNING.md tables.
- **First-bot-speech (greeting) latency lives in its own top-level field** (`first_bot_speech_ms`, 920.7ms in the captured run) — it anchors on client-connect, a different clock than voice_to_voice; mixing it into the turn stats would corrupt the anchors contract.
- **`--stop-bot` is per-scenario, not per-invocation** — passing it to a multi-scenario `pipecat eval run` tears the bot down after the first scenario (subsequent connects fail). Suite runs go without it; graceful SIGINT finalizes the observer.
- Judge criteria are scoped to exactly what the plan asserts and explicitly instruct the judge to ignore transcription noise — moonshine artifacts ("I'm K" -> "Dikki", "km CLI" -> "KM, C.L.I.", quoted speech -> "Eeeee...") were the dominant flake source, not bot behavior.
- Did NOT touch STATE.md/ROADMAP.md/REQUIREMENTS.md — orchestrator owns shared-file writes in this parallel wave; `requirements-completed` frontmatter carries the linkage.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] ElevenLabs WS rejects voice_id None — TTS could not speak at all**
- **Found during:** Task 3 (first live eval run: zero tts_response events; bot log showed 1008 "A voice with voice_id None does not exist")
- **Issue:** Plan-02's known stub `voice_id = ""` mapped to `Settings(voice=None)` on the assumption ElevenLabs falls back to a default voice; the WS API has no default and hard-rejects None
- **Fix:** `INTERIM_ELEVENLABS_VOICE_ID` (premade "Rachel", verified live against this account's key with a minimal TTS call) used whenever config voice_id is empty; the D-02 audition (plan 01-05) still lands the real voice
- **Files modified:** apps/voice/src/klanker_voice/factories.py, apps/voice/tests/test_factories.py (new regression test)
- **Committed in:** 629c1ad

**2. [Rule 1 - Bug] Greet-first produced a briefing acknowledgment (or nothing on reconnect) instead of greeting**
- **Found during:** Task 3 (greeting scenario: bot spoke "Got it. I'm **K** — ... I'm live and ready" with markdown/emoji; scenarios 3+ got no greeting at all)
- **Issue:** Plan-02 queued a bare `LLMRunFrame`; on a system-only context Claude responds *to the briefing*, and on a reconnect (context ending with an assistant message) the run behaves as a prefill continuation and yields nothing
- **Fix:** `greet_now`/`register_greet_first` now append the canonical 1.5.0 template kick `{"role": "developer", "content": "Start by concisely introducing yourself."}` before `LLMRunFrame` (source-verified in `cli/templates/server/_macros/event_handlers.jinja2`); signatures take the context, both entrypoints updated
- **Files modified:** apps/voice/src/klanker_voice/pipeline.py, apps/voice/bot.py, apps/voice/console.py
- **Committed in:** 629c1ad

**3. [Rule 1 - Bug] Barge-in scenarios raced the harness's interruption discard**
- **Found during:** Task 3 (bargein_mid flaked: trace showed `user_started_speaking` enqueued then dropped before the matcher dequeued it)
- **Issue:** kokoro utterances with pauses emit multiple `user-started-speaking` RTVI messages; each (and `bot-interrupted`) drains the harness event queue, dropping a just-enqueued `user_started_speaking`. `user_transcription` is the only event class the discard preserves
- **Fix:** interrupt-registration asserted via `user_transcription` (strictly downstream of user-speech detection, discard-proof); YAML comments document the substitution
- **Files modified:** apps/voice/scenarios/bargein_*.yaml
- **Committed in:** 4e349e3

### Scenario-authoring iterations (Task 3 scope, not code deviations)

- `within_ms` for post-interrupt `response` events widened 15000 -> 30000: the `response` event only exists after full reply playout + moonshine transcription (harness clock, Pitfall 7) — the barge-in itself was sub-second.
- Judge criteria hardened to two-point checks with explicit transcription-noise tolerance after three distinct judge-strictness failures on garbled moonshine transcripts.

---

**Total deviations:** 3 auto-fixed (all Rule 1; two were latent plan-02 bugs this plan's live instrument exposed)
**Impact on plan:** None architectural — the harness design landed as specified; the fixes made the walking skeleton actually speak and greet correctly.

## Issues Encountered

- **Flaky judge runs before criteria hardening:** the same bot behavior passed/failed across runs purely on transcription noise. Resolved by scoping criteria; final clean run is 5/5 on a fresh bot. ElevenLabs word-timestamp transcripts of interrupted turns looked fine (no Pattern 5 alignment-restart garble observed); the garble was all on the moonshine judge-side transcription.
- One pipeline serves all scenarios in a single `pipecat eval run` invocation (client disconnects do not fire `on_client_disconnected` by default), so the LLM context accumulates across scenarios — e.g. a later greeting said "Hey Marvin". Harmless for these assertions; worth remembering when authoring order-sensitive scenarios.

## Known Stubs

- `pipeline.toml` `voice_id = ""` remains (intentional, plan 01-05 audition) — but it now maps to the documented interim premade voice instead of a crash. `INTERIM_ELEVENLABS_VOICE_ID` should be removed or demoted once the audition lands a real id.

## Human Verification Deferred to End-of-Phase UAT

Per plan Task 3 human-check: play `apps/voice/artifacts/eval-recordings/bargein_mid.wav` and listen to the interruption moment — bot audio should stop subjectively under ~300ms when the interrupting utterance starts, and the follow-up should acknowledge the cut-off rather than replay the interrupted sentence. (The judge checks text; ears check audio abruptness.)

## Next Phase Readiness

- Plan 01-04 (A/B tuning) has its full instrument: per-arm artifacts land automatically, `compare` renders TUNING.md-ready diff tables, and the anchors field already documents the Flux null-stage semantics it must verify (Open Question 1 disposition).
- First real numbers (untuned nova3+smart_turn_v3, eval-session load): voice_to_voice p50 1488ms / p95 1896ms; vad_stop ~411ms; llm_ttft p50 505ms; tts_first_audio p50 157ms; first greeting audio 921ms after connect. The 1.2s ceiling work is exactly the plan-04 lever set (Flux, eager EOT, knobs).

## Self-Check: PASSED

All 11 created files exist on disk; all four commits (f57f170, 085b360, 629c1ad, 4e349e3) present in git log; unit suite 58 passed; final eval run 5/5; harness artifact JSON validated (schema_version 1, five stage keys, voice_to_voice n=10); artifacts/ gitignored; no secrets in artifacts or scenarios; no "voiceai" string in any produced file.

---
*Phase: 01-local-pipeline-latency-harness*
*Completed: 2026-07-04*
