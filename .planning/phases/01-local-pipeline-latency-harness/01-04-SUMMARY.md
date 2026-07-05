---
phase: 01-local-pipeline-latency-harness
plan: 04
subsystem: voice-pipeline
tags: [pipecat, endpointing, smart-turn-v3, deepgram-flux, vad, latency-ab, tuning, deepgram-nova3]

# Dependency graph
requires:
  - phase: "01-03"
    provides: "harness JSON schema v1 + LatencyReportObserver + report/compare CLI + five audio-path eval scenarios + baseline artifact (untuned nova3+smart_turn_v3: v2v p50 1488ms/p95 1896ms)"
provides:
  - "measured endpointing A/B verdict (D-12) in docs/TUNING.md: winner Nova-3 + SmartTurn v3"
  - "three committed diffable harness artifacts docs/tuning/arm-{a,b,c}.json from an identical scenario suite"
  - "three checked-in arm configs apps/voice/configs/arm-{a,b,c}.toml (diffable pipeline.toml clones)"
  - "RESEARCH Open Question 1 resolved: Flux nulls vad_stop AND yields zero UserBotLatencyObserver turns; needs a Flux-native observer to score"
  - "pipeline.toml confirmed as the tuned winner (smart_turn_v3, stop_secs 0.2) — the Phase 4 prod default"
  - "OPEN ESCALATION: winner voice_to_voice p95 2210.7ms > 1.2s ceiling — human tuning/accept/scope decision pending"
affects: [01-05, phase-4-prod-config, phase-5-hud, phase-5-ci-gate, PIPE-08]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Arm configs are full pipeline.toml clones differing only in [stt]/[turn] so runs are byte-diffable and KLANKER_PIPELINE_CONFIG-selectable"
    - "Flux is structurally unmeasurable by an observer that anchors on the local VAD-stop frame — endpointing A/B for a server-side-EOT STT needs its own frame anchors (Open Question 1 disposition)"
    - "Escalation over the roadmap ceiling is an execution pause for a human decision, never a tooling gate — exit codes stay 0 (D-13)"

key-files:
  created:
    - apps/voice/configs/arm-a.toml
    - apps/voice/configs/arm-b.toml
    - apps/voice/configs/arm-c.toml
    - docs/tuning/arm-a.json
    - docs/tuning/arm-b.json
    - docs/tuning/arm-c.json
    - docs/TUNING.md
  modified:
    - apps/voice/.gitignore

key-decisions:
  - "Endpointing A/B winner is Nova-3 + SmartTurn v3 (v2v p50 1460.9ms vs 1799.8ms best VAD-timeout); the ~340ms edge is pure turn release (vad_stop 401 vs 801ms)"
  - "SmartTurn v3 verdict KEEP — it is the 1.5.0 default and an 8MB int8 ONNX model (~12ms CPU, no torch); the 2GB-torch caution applies to the OLD smart-turn, not v3"
  - "Flux could not be crowned on measured evidence: it removes the local VAD-stop frame the harness anchors on, so it recorded zero turns despite running correctly; deferred to a Flux-native observer"
  - "Eager EOT stays disabled (unmeasurable on this harness; conservative default per Pitfall 4)"
  - "Winner p95 (2210.7ms) exceeds the 1.2s ceiling -> escalated for an explicit human decision rather than closing silently (plan Task 2 rule); decision recorded as pending in TUNING.md"

patterns-established:
  - "Pattern: per-arm TOML clones + committed docs/tuning/*.json as the diffable A/B record; artifacts/ stays gitignored"

requirements-completed: [PIPE-01, PIPE-04]

coverage:
  - id: D1
    description: "Three endpointing arms measured over the identical five-scenario suite with committed diffable harness artifacts; compare CLI renders the side-by-side"
    requirement: "PIPE-04"
    verification:
      - kind: integration
        ref: "python -m klanker_voice.harness compare docs/tuning/arm-a.json arm-b.json arm-c.json (exit 0, renders per-stage p50/p95)"
        status: pass
      - kind: other
        ref: "arm-integrity: arm-a logs show SpeechTimeoutUserTurnStopStrategy + no Smart Turn model-load (Pitfall 2); arm-c 20 start/20 stop balanced (Pitfall 3)"
        status: pass
    human_judgment: false
  - id: D2
    description: "docs/TUNING.md records the endpointing A/B winner, SmartTurn v3 verdict, eager-EOT decision, arm-A stop_secs sweep table, and final knob values with measured p50/p95 tables and reasoning (D-12)"
    requirement: "PIPE-04"
    verification:
      - kind: other
        ref: "grep p95 + flux + smartturn in docs/TUNING.md all present; tables generated from committed artifacts"
        status: pass
    human_judgment: false
  - id: D3
    description: "pipeline.toml equals the winning configuration and still passes config tests; greeting + barge-in scenarios pass under it"
    requirement: "PIPE-01, PIPE-04"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_config.py (21 passed)"
        status: pass
      - kind: integration
        ref: "confirmatory evals under final pipeline.toml: greeting/bargein_early/bargein_mid/bargein_monologue/memory all done: PASS"
        status: pass
    human_judgment: false
  - id: D4
    description: "Latency assessment vs the 1.2s ceiling / ~800ms target, and the escalation decision when the winner exceeds the ceiling"
    requirement: "PIPE-01"
    verification: []
    human_judgment: true
    rationale: "Winner voice_to_voice p95 2210.7ms exceeds the 1.2s ceiling; plan Task 2 requires an explicit human tuning/accept/scope decision (recorded in TUNING.md) before the phase closes over the ceiling. Decision pending."

# Metrics
duration: 35min
completed: 2026-07-05
status: complete
---

# Phase 1 Plan 04: Endpointing A/B — Measured Verdicts Summary

**Three-arm endpointing A/B measured on pipecat 1.5.0: Nova-3 + SmartTurn v3 wins (voice_to_voice p50 1460.9 ms vs 1799.8 ms for the best Nova-3 + VAD-timeout sweep, the ~340 ms edge coming entirely from turn release at vad_stop 401 ms vs 801 ms); Deepgram Flux ran correctly but is structurally unmeasurable by the plan-03 observer (it removes the local VAD-stop frame the observer anchors on — RESEARCH Open Question 1 resolved); winner landed in pipeline.toml, but its p95 (2210.7 ms) exceeds the 1.2 s ceiling and is ESCALATED for an explicit human decision**

## Performance

- **Duration:** ~35 min (live A/B measurement runs completed in the prior, interrupted session ~20 min; this resumed session mined the surviving artifacts and produced the verdicts/docs ~15 min — no live runs re-executed)
- **Completed:** 2026-07-05
- **Tasks:** 2/2 deliverable tasks complete; 1 escalation open (human decision)
- **Files created:** 7, modified: 1

## Accomplishments

- **Three arms measured over the identical scripted five-scenario suite**, with committed diffable artifacts `docs/tuning/arm-{a,b,c}.json`. The `compare` CLI renders the side-by-side and exits 0.
- **Endpointing A/B winner: Nova-3 + SmartTurn v3** — `voice_to_voice` p50 **1460.9 ms** vs **1799.8 ms** for the fastest Nova-3 + VAD-timeout config. The whole gap is turn release: SmartTurn's `vad_stop` p50 is **401 ms** against VAD-timeout's **801 ms**, because it detects a semantic end-of-turn instead of waiting a fixed silence timeout. LLM/TTS stages are statistically identical across the two, confirming the win is purely endpointing.
- **Arm A stop_secs sweep captured** (0.2 / 0.3 / 0.5 → v2v p50 1799.8 / 1836.9 / 2071.7 ms): endpointing silence scales linearly; 0.2 is fastest and is the committed `arm-a.toml` value.
- **SmartTurn v3 verdict: KEEP** — the 1.5.0 default, and an 8 MB int8 ONNX model (~12 ms CPU, no torch), so negligible deployment cost. (The "~2 GB torch bloat" caution in the stack notes is about the *older* smart-turn, not v3.)
- **RESEARCH Open Question 1 resolved:** under Flux `vad_stop` is null **and** the `UserBotLatencyObserver` records zero turns — it anchors every turn on the local `VADUserStoppedSpeakingFrame`, which Flux (server-side `ExternalUserTurnStrategies`) never fires. Flux ran correctly (barge-in via `StartOfTurn`, balanced 20/20 turn signals, no double endpointing) but has no comparable latency number, so it cannot be crowned on measured evidence. Scoring it needs a Flux-native observer — a future lever tied to PIPE-08.
- **Eager EOT decision: disabled** — unmeasurable on this harness for the same root cause; kept off as the conservative default (Pitfall 4: it trades speculative LLM spend for latency).
- **pipeline.toml confirmed as the tuned winner** (it was already on `smart_turn_v3`, stop_secs 0.2 — the A/B validates that default). 21 config tests pass; greeting + all three barge-in + memory scenarios pass under the final config.

## Escalation — Decision Required (open)

The winning configuration's measured `voice_to_voice` **p95 is 2210.7 ms** (p50 1460.9 ms) — **both exceed the 1.2 s roadmap ceiling**, and the ~800 ms target is well out of reach. Per the plan Task 2 escalation rule, execution is **paused for an explicit human decision** rather than closing the phase silently over the ceiling. This preserves D-13 exactly: no CI gate was added, and no run/report/CLI exits nonzero — the pause is an execution decision, not a tooling failure.

Decomposition of the p50 (~1461 ms): `vad_stop` ~401 ms + Haiku `llm_ttft` ~587 ms + Flash `tts_first_audio` ~164 ms + first-sentence aggregation/transport ≈ 300 ms. With SmartTurn already reclaiming the turn-release time, **the LLM TTFT is now the dominant remaining cost** (its p95 tail of 1433 ms is what pushes voice-to-voice p95 past 2 s). RESEARCH Assumption A4 (that ~800 ms is reachable untuned with this cascade) is in tension with this floor.

**Options (record the choice + reasoning in `docs/TUNING.md`):**
1. **Tune further in this plan** — add a Flux-native observer and measure Arm C; trim persona/context to cut Haiku TTFT; test lower `stop_secs` / eager EOT.
2. **Accept the number** with recorded reasoning — cascaded-pipeline floor with hosted APIs; barge-in feels slick; conference-demo tolerance — treating ~800 ms as a v2 goal.
3. **Scope a later phase** for the PIPE-08 ack-masking lever and/or the Flux-native measurement.

## Task Commits

1. **Task 1: arm configs + measured harness artifacts** - `e84555f` (feat)
2. **Task 2: TUNING.md endpointing A/B verdicts (D-12)** - `b920330` (docs)

_(pipeline.toml already carried the winner from the walking skeleton, so Task 2 landed no change to it — the A/B confirmed the existing default rather than altering it.)_

## Files Created/Modified

- `apps/voice/configs/arm-a.toml` - Nova-3 + Silero VAD + `vad_timeout`, stop_secs 0.2 (sweep winner)
- `apps/voice/configs/arm-b.toml` - Nova-3 + SmartTurn v3 (the A/B winner)
- `apps/voice/configs/arm-c.toml` - Deepgram Flux, external turn strategies, eot_threshold 0.7
- `docs/tuning/arm-a.json` - committed harness artifact, Arm A stop_secs 0.2 run
- `docs/tuning/arm-b.json` - committed harness artifact, Arm B (winner)
- `docs/tuning/arm-c.json` - committed harness artifact, Arm C (zero-turn; documents the Flux/observer anchor gap)
- `docs/TUNING.md` - D-12 verdict record: measured tables, sweep table, endpointing/SmartTurn/eager verdicts, final knob values, latency assessment + escalation, chosen-voice stub
- `apps/voice/.gitignore` - ignore transient `*.eval.log` run logs (the diffable record is `docs/tuning/*.json`)

## Decisions Made

See key-decisions frontmatter. In short: SmartTurn v3 wins and is kept (cheap ONNX, no torch); Flux is deferred as unmeasurable-here despite running correctly; eager EOT stays off; the winner is landed but its p95 over the ceiling is escalated, not swept under the rug.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Corrected a spurious eager-EOT measurement claim in arm-c.toml**
- **Found during:** Task 1 (mining the surviving Flux artifact)
- **Issue:** The arm-c.toml comment (written in the prior session) claimed the eager run "saved ~1 ms p50 for +50-70% speculative LLM calls." The Flux arm records **zero** observer turns, so no p50 exists to support that figure — it was an unfounded number.
- **Fix:** Rewrote the comment to state honestly that eager EOT is unmeasurable on this harness (Flux emits no observer turns), kept disabled as the conservative default (Pitfall 4). TUNING.md carries the same honest verdict.
- **Files modified:** apps/voice/configs/arm-c.toml
- **Verification:** No fabricated latency figure remains in configs or docs; the eager decision reads as a conscious conservative default.
- **Committed in:** e84555f (Task 1 commit)

**2. [Rule 3 - Blocking/hygiene] Gitignored transient eval run logs**
- **Found during:** Task 1 (untracked `*.eval.log` files in apps/voice/)
- **Issue:** Per-scenario eval logs were left untracked; committing them would add churn/noise, and the plan designates `docs/tuning/*.json` as the diffable record.
- **Fix:** Added `*.eval.log` to `apps/voice/.gitignore`.
- **Files modified:** apps/voice/.gitignore
- **Committed in:** e84555f (Task 1 commit)

---

**Total deviations:** 2 (1 Rule-1 accuracy fix, 1 Rule-3 hygiene). **Impact:** None architectural; both keep the record honest and clean.

## Issues Encountered

- **Resumed after a session-limit kill mid-Flux-arm.** All five live measurement runs (three Arm-A sweep points, Arm B, Arm C) had already produced surviving artifacts in the prior session's scratchpad and in `apps/voice/artifacts/harness/`. Per the resume guidance, no live API runs were re-executed — the verdicts were derived from the existing artifacts, and the confirmatory evals (which had also already run and passed under the final config) were referenced rather than re-burned.
- **The Flux "incomplete" artifact is not corrupt — it is the finding.** The zero-turn Flux artifact faithfully records that the plan-03 observer cannot anchor a turn under server-side EOT. A re-run would reproduce the same empty artifact, so none was performed.

## User Setup Required

None - no external service configuration required. (The three provider API keys were already present for the measurement runs.)

## Next Phase Readiness

- **Plan 01-05** (voice audition) is unblocked: `docs/TUNING.md` has a stubbed "Chosen voice" section for it to complete, and `pipeline.toml` `voice_id` remains empty (interim premade voice per 01-03).
- **Phase 4** inherits `pipeline.toml` as the tuned prod default (Nova-3 + SmartTurn v3, stop_secs 0.2) unchanged.
- **Blocker for phase close:** the open latency escalation above must get a recorded human decision in `docs/TUNING.md`. A future Flux-native observer (PIPE-08 neighborhood) is the path to actually score Flux and chase the ~800 ms target.

## Self-Check: PASSED

All 7 created files exist on disk and are tracked in git (arm-{a,b,c}.toml, docs/tuning/arm-{a,b,c}.json, docs/TUNING.md); both task commits (e84555f, b920330) present in git log; compare CLI exits 0 and renders the three-arm table; 21 config tests pass; all five confirmatory eval scenarios PASS under the final pipeline.toml; committed artifacts carry config metadata only (no key-like strings, T-1-09); no "voiceai"/"kmk" string in any produced file. One deliverable (D4, the latency/ceiling decision) is intentionally human_judgment: true and open — the escalation above.

---
*Phase: 01-local-pipeline-latency-harness*
*Completed: 2026-07-05*
