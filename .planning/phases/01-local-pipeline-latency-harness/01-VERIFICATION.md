---
phase: 01-local-pipeline-latency-harness
verified: 2026-07-05T08:37:00Z
status: passed
score: 5/5 must-haves verified
behavior_unverified: 0
overrides_applied: 0
re_verification:
  # No previous VERIFICATION.md — initial verification.
notes:
  - "Success criterion 2 verified against the ROADMAP AMENDMENT (2026-07-05, 01-04 re-escalation): ~1402ms p50 ACCEPTED as the Phase-1 number; ≤1.2s / ~800ms moved to Phase 6. Original number was NOT used to fail the phase, per the amendment on record in docs/TUNING.md § RE-ESCALATION."
  - "Conversational-feel and barge-in are behavior-dependent, but the human phase gate is already CLOSED and recorded (01-05-SUMMARY.md: APPROVED) and the named eval scenarios are recorded PASS (01-04-SUMMARY.md). Per the verification brief these records were checked for existence, not re-run; no live API calls were made. This is therefore NOT a pending human_needed item."
side_findings:
  - "apps/voice/scripts/bootstrap_env.sh still sources keys from SSM /kmv/bootstrap/* — a namespace RETIRED in Phase 2 (02-05-SUMMARY.md). The `make env` re-bootstrap path is stale, but does NOT block Phase-1 criterion 1: apps/voice/.env exists and the runtime load path (load_dotenv in bot.py/console.py) is current. Documented, not a Phase-1 gap; the local .env flow is the current source."
---

# Phase 1: Local Pipeline & Latency Harness Verification Report

**Phase Goal:** A developer can hold a slick, measured, interruption-safe conversation with the KlankerMaker concierge on a laptop using only three provider API keys
**Verified:** 2026-07-05T08:37:00Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria — verified against the 2026-07-05 amendment)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Developer can run the full bot locally with only the three provider API keys and hold a natural spoken conversation | ✓ VERIFIED | `bot.py` (webrtc) and `console.py` (terminal, D-08) both wire `load_config → build_pipeline → build_worker(observers=[LatencyReportObserver]) → greet-first`. Only three keys required: `factories._require_env` guards `DEEPGRAM_API_KEY`, `ANTHROPIC_API_KEY`, `ELEVENLABS_API_KEY` and nothing else; `load_dotenv(override=True)` reads `apps/voice/.env`. Human phase gate APPROVED and recorded (01-05-SUMMARY.md, Task 4). 60 unit tests pass. |
| 2 | Latency harness reports per-stage and voice-to-voice ms from recorded audio; measured v2v ≤1.2s — **AMENDED: ~1402ms p50 accepted** | ✓ VERIFIED | `observers.py` (LatencyReportObserver) + `harness/report.py` emit per-stage p50/p95 (vad_stop, stt_final, llm_ttft, tts_first_audio, voice_to_voice) as JSON + rich table. Committed measured artifact `docs/tuning/arm-b-trimmed.json`: 10 turns, `voice_to_voice.p50_ms = 1401.7`. Amendment on record in docs/TUNING.md § RE-ESCALATION and ROADMAP line 34. Harness is informational-only (D-13): `finalize()` never raises, verdicts are check/warn marks. |
| 3 | User can interrupt mid-speech: playback stops promptly and context truncates to words actually spoken, verified by named barge-in test scenarios | ✓ VERIFIED | Named scenarios `bargein_early.yaml`, `bargein_mid.yaml`, `bargein_monologue.yaml` run deterministic kokoro audio through the real input path with an Anthropic judge (`judge_factory`) checking cut-off coherence. Recorded PASS for all three (01-04-SUMMARY.md: "greeting/bargein_early/bargein_mid/bargein_monologue/memory all done: PASS"). Truncation uses pipecat 1.5.0's built-in word-timestamp path (Pattern 5) — documented design, not hand-rolled. Human sign-off explicitly covers "natural barge-in" (01-05-SUMMARY.md). |
| 4 | Agent remembers the full conversation within a session and speaks as the concierge via a versioned markdown system prompt | ✓ VERIFIED | `pipeline.py` seeds `LLMContext` with `load_persona(cfg)` and uses `LLMContextAggregatorPair` (user+assistant) for in-session memory. `scenarios/memory.yaml` (name given → subject change → recall) recorded PASS. Persona is versioned markdown `prompts/concierge.md` (v3, dated header) loaded from `cfg.persona.prompt_path`; existence-checked at config load. |
| 5 | STT/LLM/TTS stages swap via config, and the endpointing A/B has measured verdicts recorded | ✓ VERIFIED | `config.py` + `factories.py` drive provider/strategy selection from `pipeline.toml`; `KLANKER_PIPELINE_CONFIG` env var selects arm configs. Confirmed live: all of `pipeline.toml`, `configs/arm-{a,b,c}.toml` load with differing stt/turn (nova3+smart_turn_v3 / nova3+vad_timeout / flux). docs/TUNING.md records three measured arms, winner (Arm B SmartTurn v3), SmartTurn verdict KEEP, Flux verdict (measured, loses, deferred with evidence), eager-EOT REJECTED. Committed artifacts: `docs/tuning/arm-{a,b,c}.json`, `arm-b-trimmed.json`, `arm-c-eager.json`. |

**Score:** 5/5 truths verified (0 present, behavior-unverified)

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `apps/voice/pipeline.toml` | Stage selection surface, zero secret fields | ✓ VERIFIED | voice_id `bIHbv24MWmeRgasZH58o` (Will), speed 1.1, smart_turn_v3, stop_secs 0.2, claude-haiku-4-5, eleven_flash_v2_5 |
| `apps/voice/src/klanker_voice/config.py` | tomllib → frozen dataclasses + validation | ✓ VERIFIED | 236 lines; credential-field rejection regex, provider allowlists, range checks, env-var override |
| `apps/voice/src/klanker_voice/factories.py` | (kind,provider) registry + 3 turn arms | ✓ VERIFIED | 214 lines; BUILDERS registry, Flux double-endpointing guard raises on misconfig |
| `apps/voice/src/klanker_voice/pipeline.py` | build_pipeline + greet-first | ✓ VERIFIED | 133 lines; canonical cascade, greet_now/register_greet_first, metrics on |
| `apps/voice/src/klanker_voice/observers.py` | LatencyReportObserver → JSON | ✓ VERIFIED | 233 lines; 5 stable stages, Flux-native anchor, incremental artifact write |
| `apps/voice/prompts/concierge.md` | Versioned persona | ✓ VERIFIED | Persona v3 (KPH self-reference, TTS-safe DEFCON rule) |
| `apps/voice/bot.py` / `console.py` | Two run modes, observer + greet wired | ✓ VERIFIED | Both entrypoints load config, attach observer, greet-first |
| `apps/voice/scenarios/*.yaml` | Named barge-in/memory/greeting scenarios | ✓ VERIFIED | 5 scenarios; real audio path + judge |
| `apps/voice/configs/arm-*.toml` | 3 A/B arm configs | ✓ VERIFIED | All 3 load with distinct stt/turn |
| `docs/TUNING.md` | D-12 verdict record | ✓ VERIFIED | Endpointing winner, SmartTurn/Flux/eager verdicts, RE-ESCALATION accept, chosen voice |
| `docs/tuning/*.json` | Committed measured artifacts | ✓ VERIFIED | 5 real JSON artifacts with populated turns + p50/p95 summaries |

### Key Link Verification

| From | To | Via | Status |
|------|----|----|--------|
| bot.py / console.py | pipeline.build_pipeline | build_pipeline(cfg, transport) | ✓ WIRED |
| pipeline.py | prompts/concierge.md | cfg.persona.prompt_path → LLMContext system msg | ✓ WIRED |
| factories.py | pipeline.toml | provider/strategy from parsed config | ✓ WIRED |
| bot.py / console.py | observers.LatencyReportObserver | build_worker(observers=[...]) | ✓ WIRED |
| observers.py | harness/report.py | TurnRecord → Report.write JSON + render table | ✓ WIRED |
| configs/arm-*.toml | bot.py | KLANKER_PIPELINE_CONFIG env var | ✓ WIRED |
| docs/TUNING.md | docs/tuning/arm-*.json | verdict tables from committed harness artifacts | ✓ WIRED |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Unit test suite | `uv run pytest -q` | 60 passed | ✓ PASS |
| All configs load through config.py | `load_config` on pipeline.toml + arm-a/b/c | 4/4 load with distinct stt/turn | ✓ PASS |
| Measured artifact is real structured data | parse `docs/tuning/arm-b-trimmed.json` | 10 turns, v2v p50 1401.7ms | ✓ PASS |
| Live spoken conversation | (not run — no live API calls per brief) | human phase gate APPROVED on record | ? SKIP (human-verified, recorded) |

### Requirements Coverage

| Requirement | Description | Status | Evidence |
|-------------|-------------|--------|----------|
| PIPE-01 | Tune voice-to-voice toward latency target | ✓ SATISFIED | Measured + accepted ~1402ms; TUNING.md |
| PIPE-02 | Barge-in interrupt handling | ✓ SATISFIED | 3 named barge-in scenarios PASS |
| PIPE-03 | In-session memory | ✓ SATISFIED | memory.yaml PASS; LLMContext aggregator pair |
| PIPE-04 | Config-swappable stages | ✓ SATISFIED | config.py/factories.py; arm configs load |
| PIPE-05 | Latency harness | ✓ SATISFIED | observers.py + report.py + JSON artifacts |
| PIPE-06 | Versioned concierge persona | ✓ SATISFIED | concierge.md v3 loaded from config |
| PIPE-07 | Runs on three provider keys only | ✓ SATISFIED | _require_env guards exactly 3 keys |

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| bootstrap_env.sh | 78 | `mktemp .env.XXXXXX` | ℹ️ Info | Not a debt marker — mktemp template. No action. |

No `TBD`/`FIXME`/`XXX`/`HACK`/`PLACEHOLDER` debt markers in phase source files.

### Human Verification Required

None pending. The conversational-feel + barge-in phase gate was already exercised by the user and recorded APPROVED (01-05-SUMMARY.md, Task 4, with the persona-v3 correction landed and re-verified live). Per the verification brief, this record was checked for existence rather than re-run; no live API calls were made.

### Gaps Summary

No gaps. All five (amended) success criteria are verified in the codebase with substantive, wired implementations backed by committed measured artifacts, passing eval scenarios (on record), 60 passing unit tests, and a recorded human sign-off on conversational feel. Criterion 2 was correctly evaluated against the 2026-07-05 amendment (~1402ms p50 accepted; ≤1.2s scoped to Phase 6) and was not failed on the original number.

One documented side-finding (non-blocking): `apps/voice/scripts/bootstrap_env.sh` still targets the SSM `/kmv/bootstrap/*` namespace that Phase 2 (02-05) retired. The `make env` re-bootstrap path is stale, but Phase-1 criterion 1 stands because `apps/voice/.env` exists and the runtime `load_dotenv` path is current — the local `.env` flow is the current source of keys. This is a Phase-2 consequence, already documented in 02-05-SUMMARY.md, and out of Phase-1 scope to fix.

---

_Verified: 2026-07-05T08:37:00Z_
_Verifier: Claude (gsd-verifier)_
