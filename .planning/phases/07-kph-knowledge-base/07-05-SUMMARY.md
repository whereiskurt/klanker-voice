---
phase: 07-kph-knowledge-base
plan: 05
subsystem: ai
tags: [anthropic, pipecat, prompt-caching, yaml, voice-agent, knowledge-base, pacing, evals]

# Dependency graph
requires:
  - phase: 07-kph-knowledge-base (07-01)
    provides: "[knowledge] config seam, two-block cached prompt assembly (build_system_blocks), KnowledgeRouterProcessor deep-turn ack, klanker-maker curated pack + N-topic-ready manifest/topic-map, judge_factory scenario shape"
  - phase: 07-kph-knowledge-base (07-02)
    provides: "local BM25/FTS5 retrieval (RetrievalIndex, retrieved_chunks post-breakpoint block), the remaining_seconds seam left present-but-unused for this plan"
  - phase: 07-kph-knowledge-base (07-03)
    provides: "full primary topic set (klanker-maker/defcon-run-34/meshtk) with router discrimination proven"
provides:
  - "concierge.md persona v4: adaptive steering (directed answer-then-hook vs. tour-mode offer, D-04/D-05), honest-unknowns rule (D-12), spoken do-not-say boundary, PG-13/match-and-escalate guardrail restated in the persona itself"
  - "prompt_assembly.render_pacing_note()/PACING_TIGHT_THRESHOLD_SECONDS: build_system_blocks(..., remaining_seconds=...) now actually fills the D-06 pacing seam, prepending a tight-highlights or go-deeper note to block1 ONLY -- block0 byte-identical, None reproduces the Plan-01/02 shape"
  - "router.KnowledgeRouterProcessor(..., remaining_seconds_fn=...): a zero-arg callable invoked synchronously on the same deep-turn block1 rebuild that fires the ack, threading pacing through with no second timer"
  - "session.SessionLifecycle.remaining_seconds(): a pure read of the session's own clock/tier state (session_max_seconds - elapsed since construction), None for a bypass session -- the pre-existing-state source router.py's seam reads from"
  - "tests/test_knowledge_pacing.py: 8 new tests covering pacing/block0-identity, tight-vs-depth notes, router threading with/without the fn, and SessionLifecycle arithmetic + composition"
  - "scenarios/kph_unknowns.yaml, kph_tour_mode.yaml, kph_crude_humor_guard.yaml, kph_retrieval_depth.yaml, kph_router_accuracy.yaml: the 5 benchmark eval scenarios completing ROADMAP criterion 4"
affects: []

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "A pacing note is text PREPENDED to block1 (never a new block), computed by a pure render_pacing_note(remaining_seconds) function -- keeps block0/block2 completely untouched by time-awareness"
    - "remaining_seconds_fn is a caller-supplied zero-arg callable, not a value -- KnowledgeRouterProcessor invokes it synchronously at the exact moment of a deep-turn block1 rebuild, so the value is always fresh without a second polling loop"
    - "SessionLifecycle.remaining_seconds() is a pure computed property off __post_init__-stamped construction time + the existing clock callable -- no new asyncio task, thread, or timer; bypass sessions (D-15) report None rather than a stale/misleading zero"

key-files:
  created:
    - apps/voice/tests/test_knowledge_pacing.py
    - apps/voice/scenarios/kph_unknowns.yaml
    - apps/voice/scenarios/kph_tour_mode.yaml
    - apps/voice/scenarios/kph_crude_humor_guard.yaml
    - apps/voice/scenarios/kph_retrieval_depth.yaml
    - apps/voice/scenarios/kph_router_accuracy.yaml
  modified:
    - apps/voice/prompts/concierge.md
    - apps/voice/src/klanker_voice/knowledge/prompt_assembly.py
    - apps/voice/src/klanker_voice/knowledge/router.py
    - apps/voice/src/klanker_voice/session.py

key-decisions:
  - "Added SessionLifecycle.remaining_seconds() to session.py even though this plan's own files_modified frontmatter didn't list it -- the plan's own read_first note explicitly says 'SessionLifecycle already tracks session time/max -- the source of remaining_seconds; do NOT recompute', but no public accessor existed to read it from. A minimal, additive __post_init__-stamped _started_at field + a pure remaining_seconds() read (no new timer) is the smallest change that satisfies that instruction (Rule 2 auto-add)."
  - "Did NOT wire a real SessionLifecycle instance into pipeline.py/server.py's KnowledgeRouterProcessor construction -- pipeline.py and session.py's production wiring were both explicitly outside this plan's declared files_modified, and build_pipeline() has no lifecycle reference to thread through today (server.py constructs SessionLifecycle separately from build_pipeline()). remaining_seconds_fn defaults to None, reproducing Plan 01/02's behavior unchanged; wiring a bound lifecycle.remaining_seconds into production is left as a follow-up (see Next Phase Readiness)."
  - "PACING_TIGHT_THRESHOLD_SECONDS = 90.0 -- a single binary threshold (tight vs. depth), not a graduated scale, so the pacing note stays a short, unambiguous instruction the LLM can act on consistently inside a spoken turn."
  - "kph_retrieval_depth.yaml deliberately opens on a DIFFERENT topic (defcon.run.34) before asking its klanker-maker long-tail question -- the router only fires retrieval on a genuine topic SWITCH, and klanker-maker is already the pipeline's initial topic (manifest tour_priority[0]), so a scenario opening directly on km would never actually exercise the live retrieval path. Documented in the file's own header as a scenario-design note, not a code bug (out of this plan's scope to fix scenarios/kph_retrieval_km.yaml, which may share this latent design gap)."
  - "kph_retrieval_depth.yaml targets a DIFFERENT long-tail fact (klanker-maker's spec.agent: per-tool/per-CLI autoApprove/deny gating, deny-wins-over-allow) than 07-02's kph_retrieval_km.yaml (the action-quota freeze-quarantine mechanism) -- confirmed absent from the curated pack via grep, so this is genuinely new depth/coverage proof, not a duplicate."

requirements-completed: [PIPE-10, PIPE-06]

coverage:
  - id: D1
    description: "build_system_blocks(cfg, knowledge_cfg, topic, remaining_seconds=N) prepends a concise pacing note to block1 ONLY; block0 stays byte-identical with/without pacing, and a large vs. small remaining_seconds produce different (tight vs. depth) notes"
    requirement: "PIPE-10"
    verification:
      - kind: unit
        ref: "tests/test_knowledge_pacing.py::test_pacing_note_prepends_to_block1_block0_byte_identical"
        status: pass
      - kind: unit
        ref: "tests/test_knowledge_pacing.py::test_tight_vs_depth_pacing_notes_differ_block0_still_identical"
        status: pass
      - kind: unit
        ref: "tests/test_knowledge_pacing.py::test_remaining_seconds_none_is_byte_identical_to_omitted_call"
        status: pass
    human_judgment: false
  - id: D2
    description: "KnowledgeRouterProcessor threads a caller-supplied remaining_seconds_fn into the per-turn block1 rebuild on a genuine deep-turn switch, with remaining_seconds_fn=None (the default) reproducing Plan 01/02's exact block1 text -- no regression for existing callers"
    requirement: "PIPE-10"
    verification:
      - kind: unit
        ref: "tests/test_knowledge_pacing.py::test_router_threads_remaining_seconds_into_block1_on_switch"
        status: pass
      - kind: unit
        ref: "tests/test_knowledge_pacing.py::test_router_without_remaining_seconds_fn_unchanged_from_plan01_shape"
        status: pass
    human_judgment: false
  - id: D3
    description: "remaining_seconds is read from SessionLifecycle's own pre-existing clock/tier state (session_max_seconds minus elapsed time since construction) -- a pure computation, never a second timer/thread -- and is None for a bypass session rather than a misleading zero; the router's remaining_seconds_fn seam composes with a real SessionLifecycle instance"
    requirement: "PIPE-10"
    verification:
      - kind: unit
        ref: "tests/test_knowledge_pacing.py::test_session_lifecycle_remaining_seconds_reads_existing_state_no_new_clock"
        status: pass
      - kind: unit
        ref: "tests/test_knowledge_pacing.py::test_session_lifecycle_remaining_seconds_none_for_bypass_session"
        status: pass
      - kind: unit
        ref: "tests/test_knowledge_pacing.py::test_router_remaining_seconds_fn_composes_with_real_session_lifecycle"
        status: pass
    human_judgment: false
  - id: D4
    description: "concierge.md carries adaptive steering (directed answer-then-hook vs. tour-mode offer, D-04), honest-unknowns (D-12), the do-not-say boundary, and a persona-level restatement of the PG-13/match-and-escalate guardrail already in the style layer"
    requirement: "PIPE-06"
    verification:
      - kind: unit
        ref: "shell check: grep -Eiq 'tour|long version' prompts/concierge.md -- matches"
        status: pass
    human_judgment: false
  - id: D5
    description: "The 5-file benchmark scenario set (unknowns, tour mode, crude-humor guard, retrieval depth, router accuracy) exists, is YAML-valid, and reuses the existing judge_factory -- completing ROADMAP criterion 4 together with the per-topic correctness scenarios (07-01/07-03) and the retrieval-depth/cache-verify scenarios (07-01/07-02)"
    requirement: "PIPE-10"
    verification:
      - kind: unit
        ref: "shell check: python -c \"import yaml,glob; [yaml.safe_load(open(f)) for f in glob.glob('scenarios/kph_*.yaml')]\" -- all parse"
        status: pass
    human_judgment: false
  - id: D6
    description: "LIVE (human-check): the full benchmark set (correctness, retrieval depth/coverage, router accuracy, unknowns, tour mode, crude-humor guard) passes judging against the real pipeline (bot.py -t eval + pipecat eval run), with the pass rate and router-accuracy number recorded here"
    verification: []
    human_judgment: true
    rationale: "Same documented blocker as every prior 07-0x plan (07-01/02/03/04-SUMMARY.md): this venv's pipecat-ai[evals,local] (kokoro/moonshine) extras are not installed, so a live pipecat eval run against a real ANTHROPIC_API_KEY-backed bot.py -t eval could not be exercised in this offline execution session. Installing new packages is excluded from auto-fix (deviation Rule 3 exclusion) -- requires a human uv sync --group dev, then a live run, before the pass-rate/router-accuracy numbers this plan's <output> asks for can be recorded."

# Metrics
duration: 25min
completed: 2026-07-07
status: complete
---

# Phase 7 Plan 05: KPH Knowledge Base -- Production Polish (Steering, Pacing, Honest Unknowns, Benchmark Evals) Summary

**concierge.md persona v4 adds adaptive tour-vs-answer-then-hook steering, an honest-unknowns rule, and a spoken do-not-say boundary; `build_system_blocks`'s long-present `remaining_seconds` seam now actually renders a tight-vs-depth pacing note into block1 only (block0 byte-identical), threaded through the router via a new `remaining_seconds_fn` callable and a new `SessionLifecycle.remaining_seconds()` pure read; five new benchmark scenarios (unknowns, tour mode, crude-humor guard, retrieval depth, router accuracy) complete ROADMAP criterion 4's eval set.**

## Performance

- **Duration:** ~25 min
- **Completed:** 2026-07-07
- **Tasks:** 2 (both `type="auto"`, no checkpoints)
- **Files modified:** 9 (4 modified pre-existing source files, 1 new test file, 5 new scenario files)

## Accomplishments

- `apps/voice/prompts/concierge.md` (persona v4): new "Steering the conversation" section (directed questions get a tight answer-then-hook, D-04; quiet/aimless visitors get a tour offer walking `tour_priority`'s order, D-05); new "When you don't know" section (D-12 honest+redirect, never a bluff); new "What you never say" section (spoken do-not-say boundary for account IDs, internal hostnames, unreleased-roadmap specifics); the "Playful with teeth" bullet now explicitly restates the PG-13/match-and-escalate guardrail already carried in full in the style layer (`kurt-voice.md`/`kurt-humor-personality.md`), per the coordinator's own note that this guardrail should live in the persona too.
- `prompt_assembly.py`: `render_pacing_note(remaining_seconds)` + `PACING_TIGHT_THRESHOLD_SECONDS = 90.0` -- a pure function rendering a short, spoken-friendly pacing note (tight-highlights-and-close vs. room-to-go-deeper) that `build_system_blocks` now prepends to block1's text. `remaining_seconds=None` (the default, and every pre-07-05 call site) reproduces the exact Plan-01/02 block1 shape byte-for-byte; block0 and the optional retrieval block2 are never touched by pacing.
- `router.py`: `KnowledgeRouterProcessor` gained `remaining_seconds_fn: Callable[[], float | None] | None = None`. On the same genuine deep-turn switch that already fires the ack and (optionally) queries retrieval, it now also calls this fn synchronously and threads the result into `build_system_blocks`'s `remaining_seconds` -- a read, never a new timer. `remaining_seconds_fn=None` (the default) is behaviorally identical to Plan 01/02.
- `session.py`: `SessionLifecycle` gained a `_started_at` field (stamped once in a new `__post_init__`, off the SAME `clock` callable the D-02 service timer already uses) and a public `remaining_seconds()` method: `max(0, tier.session_max_seconds - elapsed)`, returning `None` for a bypass/smoke session (D-15, never subject to the wall-clock cutoff) rather than a misleading zero. This is the "existing session state" source the plan's own read_first note pointed at -- no second timer, no new AWS/asyncio dependency (construction alone, no `.start()` call, is enough to compute it).
- `tests/test_knowledge_pacing.py`: 8 new tests -- pacing-note prepend + block0 byte-identity, tight-vs-depth notes differing, `None`-is-identical-to-omitted, the router threading a `remaining_seconds_fn` into block1 on a genuine switch, the router's unchanged behavior with no fn supplied, `SessionLifecycle.remaining_seconds()`'s pure arithmetic (elapsed-time decrement, clamped at 0), its `None` return for a bypass session, and the fn seam composing with a real `SessionLifecycle` instance (not a stand-in).
- Five new benchmark scenarios (`scenarios/kph_*.yaml`), all YAML-valid and reusing the existing `judge_factory` verbatim (no harness code change):
  - `kph_unknowns.yaml` -- an overly-specific beyond-the-pack question (defcon.run.34's exact TLS cipher suite) judged for the D-12 honest-admission-plus-redirect and against any fabricated technical detail.
  - `kph_tour_mode.yaml` -- a quiet/aimless opener judged for a 60-second-tour offer (D-04/D-05), then an acceptance turn judged for a stop-by-stop itinerary rather than a data dump.
  - `kph_crude_humor_guard.yaml` -- the behavioral proof of the T-07-04 persona guardrail: a neutral opener judged that KPH stays witty/PG-13 and does NOT volunteer the humor deck's crude/edgy bits (TTP, trolls/lulz); a second, observational-only turn shows a visitor bringing edgier energy first (match-and-escalate permitted there, never required -- the guard is only about not leading with it).
  - `kph_retrieval_depth.yaml` -- a distinct long-tail km detail (the `spec.agent:` per-tool/per-CLI `autoApprove`/`deny` gating block, deny always winning over allow) confirmed absent from the curated pack via `grep`; deliberately opens on defcon.run.34 first so the km question is a genuine topic switch back into klanker-maker (see key-decisions for why -- the router only fires retrieval on a genuine switch, and km is already the initial topic).
  - `kph_router_accuracy.yaml` -- a battery of 3 directed utterances (one per primary topic) plus the exact Pitfall-1 ambiguous "toolkit" utterance already keyless-proven in `test_knowledge_router.py`, judged for correct per-topic content and safe non-guessing deferral on the ambiguous case.

## Task Commits

Each task was committed atomically:

1. **Task 1: Persona steering + honest unknowns + do-not-say boundary + time-aware pacing injection** - `ca0618f` (feat)
2. **Task 2: Benchmark eval set -- correctness, retrieval depth/coverage, router accuracy, unknowns, crude-humor guard, tour mode** - `fc43d32` (feat)

## Files Created/Modified

- `apps/voice/prompts/concierge.md` - persona v4: steering, honest-unknowns, do-not-say, restated PG-13 guardrail
- `apps/voice/src/klanker_voice/knowledge/prompt_assembly.py` - `render_pacing_note`, `PACING_TIGHT_THRESHOLD_SECONDS`, `build_system_blocks` now uses `remaining_seconds`
- `apps/voice/src/klanker_voice/knowledge/router.py` - `KnowledgeRouterProcessor(remaining_seconds_fn=...)`
- `apps/voice/src/klanker_voice/session.py` - `SessionLifecycle._started_at` (`__post_init__`) + `remaining_seconds()`
- `apps/voice/tests/test_knowledge_pacing.py` - 8 new tests (pacing, router threading, SessionLifecycle arithmetic/composition)
- `apps/voice/scenarios/kph_unknowns.yaml` - honest-unknowns eval
- `apps/voice/scenarios/kph_tour_mode.yaml` - tour-mode steering eval
- `apps/voice/scenarios/kph_crude_humor_guard.yaml` - PG-13 persona-guardrail eval
- `apps/voice/scenarios/kph_retrieval_depth.yaml` - a second, distinct retrieval-depth eval (per-tool agent gating)
- `apps/voice/scenarios/kph_router_accuracy.yaml` - 3-topic + ambiguous-case router-accuracy battery

## Decisions Made

- **Added `SessionLifecycle.remaining_seconds()` to `session.py`** even though it wasn't in this plan's declared `files_modified` -- the plan's own `read_first` note pointed at `session.py` as "the source of remaining_seconds; do NOT recompute," but no public accessor existed. A minimal, additive `__post_init__`-stamped `_started_at` + a pure `remaining_seconds()` read satisfies that instruction without adding a second timer (Rule 2 auto-add; see Deviations).
- **Did not wire a real `SessionLifecycle` into `pipeline.py`/`server.py`'s `KnowledgeRouterProcessor` construction** -- both files were explicitly outside this plan's declared scope, and `build_pipeline()` has no lifecycle reference today (`server.py` constructs `SessionLifecycle` separately). `remaining_seconds_fn` defaults to `None`, so production behavior is unchanged from Plan 01/02 until a future plan threads a bound `lifecycle.remaining_seconds` through (see Next Phase Readiness).
- **`PACING_TIGHT_THRESHOLD_SECONDS = 90.0`**, a single binary threshold rather than a graduated scale -- keeps the pacing note a short, unambiguous instruction inside a spoken system prompt.
- **`kph_retrieval_depth.yaml` opens on defcon.run.34 before its klanker-maker long-tail question** -- the router only fires retrieval on a genuine topic switch, and klanker-maker is already the pipeline's default initial topic (`manifest.yaml`'s `tour_priority[0]`), so a scenario opening directly on km would never actually exercise the live retrieval path. Documented as a scenario-design note in the file's own header, not a code fix (out of scope: `kph_retrieval_km.yaml` belongs to 07-02).
- **`kph_retrieval_depth.yaml` targets a different long-tail fact than `kph_retrieval_km.yaml`** (`spec.agent:` per-tool/per-CLI gating vs. the action-quota freeze-quarantine mechanism) -- confirmed absent from the curated pack via `grep`, so it's genuinely new depth/coverage proof.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing critical seam] `SessionLifecycle` had no public accessor for remaining session time**
- **Found during:** Task 1, implementing the router's `remaining_seconds_fn` threading per the plan's own `read_first` instruction to source it from `session.py`
- **Issue:** The plan's action text and read_first note both assume `SessionLifecycle` already exposes (or can cheaply expose) remaining time, but no such public method existed -- only a private `_last_tick_at` that mutates every 15s heartbeat (wrong semantics for "time since session start").
- **Fix:** Added a `_started_at` field stamped once via a new `__post_init__` hook (off the same `clock` callable the D-02 service timer already uses) and a public `remaining_seconds()` method doing `max(0, tier.session_max_seconds - elapsed)`, returning `None` for a bypass session. No new timer, thread, or AWS dependency -- pure arithmetic on existing state.
- **Files modified:** `src/klanker_voice/session.py`
- **Verification:** `tests/test_knowledge_pacing.py::test_session_lifecycle_remaining_seconds_reads_existing_state_no_new_clock` + `::test_session_lifecycle_remaining_seconds_none_for_bypass_session` pass; full suite 255/255.
- **Committed in:** `ca0618f` (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (Rule 2 missing critical seam)
**Impact on plan:** Necessary for correctness -- without it, the plan's own explicit instruction to source `remaining_seconds` from `session.py`'s existing state would have been impossible to fulfill or test. No scope creep: the addition is a single pure method plus one stamped field, nothing else in `session.py` changed.

## Live Verification

**Test suite:** `cd apps/voice && uv run pytest -q` -> **255 passed, 0 failed** (247 prior + 8 new; no flake this run -- the pre-existing `test_session.py` timing flake documented in 07-01/07-02-SUMMARY.md did not reproduce).

**Persona/prompt_assembly greps (plan's own automated verify):**
```
grep -Eiq "tour|long version" prompts/concierge.md   -> match
grep -q "remaining_seconds" src/klanker_voice/knowledge/prompt_assembly.py  -> match
```

**Benchmark scenario set (plan's own automated verify):**
```
python -c "import yaml,glob; [yaml.safe_load(open(f)) for f in glob.glob('scenarios/kph_*.yaml')]"  -> all kph scenarios parse
for f in kph_unknowns kph_tour_mode kph_crude_humor_guard kph_retrieval_depth kph_router_accuracy; do test -s scenarios/$f.yaml; done  -> benchmark set present
```

**Not exercised (deferred, not self-approved) -- same documented blocker as every prior 07-0x plan:** the plan's `<human-check>` bullet -- running the full benchmark set (`kph_knowledge_km/defconrun/meshtk.yaml`, `kph_cache_verify.yaml`, `kph_retrieval_km/depth.yaml`, `kph_unknowns.yaml`, `kph_tour_mode.yaml`, `kph_crude_humor_guard.yaml`, `kph_router_accuracy.yaml`) through the full live audio pipeline (`pipecat eval run` against a running `bot.py -t eval`, with real Deepgram STT + Anthropic + ElevenLabs TTS) and recording the pass rate + router-accuracy number -- was not exercised. This venv's `pipecat-ai[evals,local]` (kokoro/moonshine) extras are still not installed (same blocker as 07-01/02/03/04-SUMMARY.md). A human (or a follow-up session with the eval extras installed) should run:

```
uv sync --group dev   # installs pipecat-ai[evals,local] (kokoro/moonshine) -- human-verified, not auto-installed
uv run python bot.py -t eval   # in one terminal
uv run pipecat eval run scenarios/kph_*.yaml scenarios/memory.yaml --bot-url ws://localhost:7860
```

and record: (a) the overall pass rate across the full scenario set, (b) the router-accuracy number specifically from `kph_router_accuracy.yaml`'s 4 turns (3 directed + 1 ambiguous). This is the ROADMAP criterion 4 phase gate the plan's `<output>` explicitly asks to close out.

## Issues Encountered

None beyond the one auto-fixed deviation above.

## User Setup Required

None for the code itself -- all Task 1 changes are keyless/offline. For the deferred live evals (coverage `D6`): `uv sync --group dev` (or equivalent) to install `pipecat-ai[evals,local]` in this venv, plus the real `ANTHROPIC_API_KEY` already present in `apps/voice/.env` from prior phases.

## Next Phase Readiness

- **Phase 7 (KPH Knowledge Base) is now CODE-COMPLETE, 5/5 plans.** All ROADMAP criteria 1-4 have code-level proof (caching engagement, retrieval depth, N-topic discrimination, benchmark eval set); the one remaining gate across the whole phase is the single consolidated live-audio eval run every 07-0x plan has been deferring to (same `pipecat-ai[evals,local]` blocker), which would exercise all 12 `kph_*.yaml` scenarios plus `memory.yaml`/`kph_cache_verify.yaml` in one pass and produce the pass-rate + router-accuracy numbers this plan's `<output>` asks for.
- **Follow-up wiring opportunity (not blocking):** `KnowledgeRouterProcessor.remaining_seconds_fn` and `SessionLifecycle.remaining_seconds()` are both real and tested, but not yet connected in production -- `server.py`'s `_run_session` would need to pass `lifecycle.remaining_seconds` into `build_pipeline()` (which would need a new optional parameter threading it to the `KnowledgeRouterProcessor` constructor). This is a small, well-scoped follow-up if live pacing behavior is desired before the next milestone; today the pacing mechanism is fully proven at the unit level but inert in the deployed pipeline (defaults to `None`, identical to Plan 01/02 behavior).
- **Scenario-design note for future eval authors:** any scenario wanting to exercise the router's live retrieval-injection path must NOT open directly on klanker-maker (the pipeline's default initial topic) -- it must switch INTO klanker-maker from a different topic first, since retrieval only fires on a genuine topic switch. `kph_retrieval_km.yaml` (07-02) may have this same latent gap; flagged here for awareness, not fixed (out of this plan's scope).

---
*Phase: 07-kph-knowledge-base*
*Completed: 2026-07-07*

## Self-Check: PASSED

All 10 created/modified files verified present on disk; both task commit hashes (ca0618f, fc43d32) verified present in git log.
