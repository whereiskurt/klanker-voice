---
phase: 07-kph-knowledge-base
verified: 2026-07-07T18:00:00Z
reverified: 2026-07-07T19:30:00Z
status: human_needed
score: 6/10 must-haves verified
behavior_unverified: 4
gap_closure: "The one deterministic code gap (D-06 time-aware pacing not wired to production) was CLOSED post-verification in commit f13cb84 via TDD: build_pipeline() now forwards remaining_seconds_fn to KnowledgeRouterProcessor and server._run_session() sources it from the live SessionLifecycle.remaining_seconds. 2 RED wiring tests (tests/test_rtvi.py::TestPacingFnWiring) proven failing then GREEN; full suite 258 passed. Score moves 5/10 -> 6/10 and status gaps_found -> human_needed: no deterministic gaps remain; the 4 remaining open truths are all live-audio-eval / live-refresh items that genuinely need a human (pipecat-ai[evals,local] extras + billed API round trips + the D-09 refresh diff review)."
overrides_applied: 0
mode_discrepancy: "ROADMAP marks Mode: mvp for Phase 7, but the phase goal ('KPH answers with deep, current knowledge of Kurt's world...') is NOT in user-story format (gsd-tools user-story.validate returned valid=false). Per references/verify-mvp-mode.md this is a discrepancy that would normally block MVP-framed verification. Proceeding with standard goal-backward verification against the ROADMAP's explicit truths-format Success Criteria (which the phase itself documents in truths form, not user-story form) since that is the actual contract this phase was planned and executed against. Recommend running /gsd mvp-phase 7 to reconcile the goal format, or clearing Mode: mvp for this phase, as a housekeeping item — non-blocking."
gaps:
  - truth: "KPH paces to remaining session time — a short tier gets tight highlights + a closing pointer; a longer tier gets depth (D-06)"
    status: resolved
    resolved_by: "f13cb84"
    resolution: "Wired in commit f13cb84 (TDD): build_pipeline() gained a remaining_seconds_fn kwarg forwarded to KnowledgeRouterProcessor; server._run_session() passes lifecycle.remaining_seconds (the live SessionLifecycle owning the service timer + countdown). 2 RED wiring tests proven failing then GREEN; full suite 258 passed. The deployed router now paces to the real session clock. The verifier correctly located bot.py as unwired — the actual production construction site is server.py's _run_session (bot.py is the dev/eval path where there is no session cap, correctly staying None)."
    reason: "The pacing mechanism (render_pacing_note, remaining_seconds threading through build_system_blocks and KnowledgeRouterProcessor, SessionLifecycle.remaining_seconds()) is fully implemented and unit-tested (8 passing tests in test_knowledge_pacing.py), but it is never connected to a real running session. bot.py constructs the pipeline via build_pipeline(cfg, transport) with no SessionLifecycle reference at all, and pipeline.py's KnowledgeRouterProcessor(...) call site never passes remaining_seconds_fn, so it defaults to None on every real session — 07-05-SUMMARY.md's own 'Next Phase Readiness' section documents this explicitly as inert. This is a deterministic, grep-verifiable wiring gap, not a live-audio-venv limitation — it would still be inert even with pipecat-ai[evals,local] installed."
    artifacts:
      - path: "apps/voice/src/klanker_voice/pipeline.py"
        issue: "build_pipeline() constructs KnowledgeRouterProcessor(cfg=cfg, knowledge_cfg=knowledge_cfg, llm=llm, initial_topic=initial_topic, retrieval_index=retrieval_index) — no remaining_seconds_fn argument is passed."
      - path: "apps/voice/bot.py"
        issue: "Never constructs or threads a SessionLifecycle (or any clock) into build_pipeline(); build_pipeline(cfg, transport) has no lifecycle parameter to receive one even if it did."
    missing:
      - "Thread a bound lifecycle.remaining_seconds callable from wherever SessionLifecycle is actually constructed (server.py's _run_session, per 07-05-SUMMARY.md's own follow-up note) through build_pipeline() into KnowledgeRouterProcessor's remaining_seconds_fn parameter."
deferred: []
behavior_unverified_items:
  - truth: "A retrieval path answers depth questions from full repo content, with latency masked acceptably in conversation (ROADMAP criterion 2)"
    test: "Run kph_retrieval_km.yaml / kph_retrieval_depth.yaml through the live audio pipeline (pipecat eval run against bot.py -t eval) with real Deepgram/Anthropic/ElevenLabs services."
    expected: "The judged answer surfaces the long-tail detail correctly, and the deep-turn's measured latency does not regress the ~1402ms p50 baseline (ack-masking holds in a real spoken turn, not just in a keyless unit-test timing assertion)."
    why_human: "Requires pipecat-ai[evals,local] (kokoro/moonshine) extras not installed in this venv, plus a live, billed Anthropic/Deepgram/ElevenLabs round trip — cannot be exercised by static analysis or the existing offline test suite."
  - truth: "Knowledge refresh is a script run, not a manual edit — regenerating digests from the live repos (ROADMAP criterion 3)"
    test: "Run `make -C apps/voice knowledge` (or `kv knowledge refresh`) for real, with ANTHROPIC_API_KEY set and the local klankrmkr/defcon.run.34/meshtk checkouts present; review the resulting knowledge/ git diff per D-09."
    expected: "Packs + style layer + FTS5 chunk files regenerate from the manifest with real Anthropic distillation calls; advisory-lint findings print for review; the diff is clean enough to commit."
    why_human: "The plan explicitly forbade running a live, billed refresh during automated execution. distill_topic()/survey_repo()/style_pass() (the actual LLM-call map-reduce pass) have never been exercised live — only proven against tmp_path fixtures plus a --dry-run pass against real checkouts (which found and fixed two real bugs: an unbounded rglob hang and a path-resolution bug, per 07-04-SUMMARY.md), not a full committed regeneration."
  - truth: "KPH answers a benchmark set of Kurt/repo questions correctly, verified by eval scenarios (ROADMAP criterion 4)"
    test: "Run the full scenarios/kph_*.yaml set (12 files) plus memory.yaml through pipecat eval run against a running bot.py -t eval; record the overall pass rate and the router-accuracy number from kph_router_accuracy.yaml."
    expected: "A high pass rate across correctness (km/defcon/meshtk), retrieval depth/coverage, router accuracy, honest-unknowns, tour-mode, and the crude-humor guard, with a specific router-accuracy number recorded."
    why_human: "Same pipecat-ai[evals,local] venv blocker as above — every 07-0x SUMMARY documents this identical deferral. No pass-rate or router-accuracy number has ever actually been recorded despite the plan's own <output> explicitly asking for it as the phase gate."
  - truth: "KPH defaults to PG-13, self-deprecating wit and never volunteers crude/edgy material to a neutral opener; it matches-and-escalates only if the visitor brings that energy first (persona guardrail)"
    test: "Run scenarios/kph_crude_humor_guard.yaml through the live audio pipeline: a neutral opener should judge as witty/PG-13 with no volunteered crude/edgy material; a follow-up turn with the visitor bringing edgy energy may (not must) be matched."
    expected: "The judge scores the neutral-opener turn as PG-13-compliant and the escalation turn as appropriately calibrated."
    why_human: "Same pipecat-ai[evals,local] blocker; this is the explicit behavioral proof plan the coordinator called for as the eval counterpart to the style-layer guardrail text — the text exists (verified below) but its behavioral effect on a real LLM call has not been observed."
human_verification:
  - test: "Run the full live-audio benchmark set (all scenarios/kph_*.yaml + scenarios/memory.yaml) via `uv sync --group dev` then `uv run python bot.py -t eval` + `uv run pipecat eval run scenarios/kph_*.yaml scenarios/memory.yaml --bot-url ws://localhost:7860`."
    expected: "Cross-topic correctness (km/defcon/meshtk), retrieval depth/coverage, router accuracy, honest-unknowns, tour-mode steering, and the PG-13 crude-humor guard all judge correct; pass rate and router-accuracy number recorded (this closes ROADMAP criteria 2 and 4's live proof, plus the persona guardrail behavioral proof)."
    why_human: "Requires installing pipecat-ai[evals,local] (kokoro/moonshine), a running bot instance, and real billed Deepgram/Anthropic/ElevenLabs API calls — outside what a verifier can run standalone."
  - test: "Run `make -C apps/voice knowledge` (or `kv knowledge refresh`) for real against the actual local klankrmkr/defcon.run.34/meshtk checkouts, with ANTHROPIC_API_KEY set."
    expected: "Packs/style/FTS5 indexes regenerate; the resulting `knowledge/` git diff is reviewed and is clean (or advisory-lint flags are triaged) before committing (D-09)."
    why_human: "Live, billed operation explicitly deferred by the plan; the distillation LLM pass has never been exercised end-to-end."
---

# Phase 7: KPH Knowledge Base Verification Report

**Phase Goal:** KPH answers with deep, current knowledge of Kurt's world — klanker-maker, defcon.run, meshtk, and selected repos/scripts — without breaking the voice-latency budget.
**Verified:** 2026-07-07
**Status:** gaps_found
**Re-verification:** No — initial verification

## Mode / Goal-Format Note

ROADMAP.md tags Phase 7 `Mode: mvp`, but `gsd-tools query user-story.validate` returns `valid=false` against the phase goal text — it is not in "As a [role], I want to [capability], so that [outcome]." form (it is a technical capability statement, consistent with all four ROADMAP Success Criteria also being written as truths, not a user flow). Per `verify-mvp-mode.md` this would normally halt MVP-framed verification and ask for `/gsd mvp-phase 7`. Given the phase's own Success Criteria are already written in truths form and every one of its five plans supplies `must_haves.truths`/`artifacts`/`key_links` in the standard (non-MVP) shape, this report proceeds with standard goal-backward verification against those truths — the actual contract this phase was planned, executed, and self-verified against. This mismatch is flagged as non-blocking housekeeping, not a phase gap.

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | ROADMAP SC1: Curated knowledge pack ships in the system prompt; ≥4096 tokens triggers Anthropic prompt caching (`cache_read_input_tokens > 0`); TTFT stays in budget | ✓ VERIFIED | Live, unmocked two-call Anthropic API proof recorded in 07-01-SUMMARY.md: `block0` = 5444 tokens (`count_tokens`, ≥ `cfg.knowledge.cache_floor`=4096), `cache_creation_input_tokens=5438` on turn 1, `cache_read_input_tokens=5438` on turn 2. Code path confirmed live in `apps/voice/src/klanker_voice/knowledge/prompt_assembly.py` (`build_system_blocks`/`apply_system_blocks`) and wired in `pipeline.py:112` (`apply_system_blocks(llm, build_system_blocks(...))`). `report.py` carries an additive `cache_read_input_tokens` field for harness capture. |
| 2 | ROADMAP SC2: A retrieval path answers depth questions from full repo content, latency masked acceptably | ⚠️ PRESENT_BEHAVIOR_UNVERIFIED | Mechanism is real and code-verified: `retrieval.py` (stdlib sqlite3 FTS5 + BM25, keyless), a real 2601-chunk km corpus (`knowledge/index/klanker-maker/docs.jsonl`) built from 32 real docs files, and a passing unit test (`test_km_index_surfaces_long_tail_detail_absent_from_curated_pack`) that proves a genuine long-tail fact (`action_frozen`/`onBreach`) is retrievable and is absent from the curated pack. Query timing (~30ms cold / <1ms warm) is unit-proven well under the 100ms ack-masking budget. What is NOT proven: the full live-audio pipeline path (STT→router→retrieval→LLM→TTS) actually surfacing this depth in a spoken conversation with acceptable perceived latency — deferred, see Human Verification. |
| 3 | ROADMAP SC3: Knowledge refresh is a script run, not a manual edit — regenerating digests from the live repos | ⚠️ PRESENT_BEHAVIOR_UNVERIFIED | `apps/voice/scripts/refresh_knowledge.py` is manifest-gated (D-01), `public:true`-gated (D-02, confirmed every manifest source now carries this flag), skip-on-missing, offline except Anthropic calls, and reuses `retrieval.chunk_text`/`advisory_lint` verbatim — proven by 20 passing unit tests (`test_knowledge_refresh.py`) plus two real bugs (unbounded `rglob` hang on a real 443,982-file `node_modules` tree; wrong path-resolution base) found and fixed via a manual `--dry-run` against the real external checkouts. `make -C apps/voice knowledge` and `kv knowledge refresh` both dispatch to it (confirmed: Makefile line 13-15, `kv/internal/app/cmd/knowledge.go`, `go build ./...` clean). What is NOT proven: a real, live, committed refresh run with actual Anthropic distillation calls against the real repos has never been executed — `distill_topic`/`survey_repo`/`style_pass` are real functions but have zero live-call evidence. |
| 4 | ROADMAP SC4: KPH answers a benchmark set of Kurt/repo questions correctly, verified by eval scenarios | ⚠️ PRESENT_BEHAVIOR_UNVERIFIED | 12 `scenarios/kph_*.yaml` files exist, all YAML-valid and confirmed parsing (verified independently: `python -c "import yaml,glob; [yaml.safe_load(open(f)) for f in glob.glob('scenarios/kph_*.yaml')]"` → all parse). They cover per-topic correctness (km/defcon/meshtk), cache-verify, retrieval depth (2 distinct long-tail facts), router accuracy, unknowns, tour mode, and the crude-humor guard — the full set the plan's `<output>` calls for. What is NOT proven: none of these scenarios have ever been run through the live audio harness (`pipecat eval run` against `bot.py -t eval`) — no pass-rate or router-accuracy number has been recorded anywhere in any of the 5 SUMMARYs, despite 07-05's own `<output>` explicitly designating this as "the phase gate." |
| 5 | The router ack fires ONLY on a genuine deep-pack topic switch — never on a same-topic follow-up or a shallow one-liner (Pitfall 2) | ✓ VERIFIED | Passing behavioral unit tests exercise the actual state transition: `test_ack_fires_on_first_topic_switch`, `test_no_ack_on_same_topic_followup` (07-01), `test_topic_switch_fires_ack_then_same_topic_followup_does_not` (07-03, across a real km→defcon switch). |
| 6 | The router discriminates km / defcon.run.34 / meshtk safely, including the "toolkit" keyword-overlap case (falls back rather than guessing) | ✓ VERIFIED | `test_classify_discriminates_all_three_primary_topics_without_collision` and `test_classify_bare_toolkit_overlap_resolves_to_fallback_via_floor` pass; independently re-verified via `classify("do you have any toolkit for that kind of thing", TOPIC_MAP)` → `(None, 0)` per 07-03-SUMMARY.md's recorded shell output. `topic-map.yaml confidence_floor=2`, 3 topics present, confirmed via direct YAML load. |
| 7 | `system[0]` (the cached stable prefix) stays byte-identical across topic switches, retrieval injection, and pacing injection — the caching invariant (Pitfall 3) | ✓ VERIFIED | Three independent passing test suites assert this: `test_build_system_blocks_block0_byte_identical_across_topics` (07-01), `test_build_system_blocks_injects_retrieved_chunks_into_uncached_post_breakpoint_block` (07-02, chunks land in block2, never block0/1), `test_pacing_note_prepends_to_block1_block0_byte_identical` (07-05). Retrieved chunks land in a third list element; pacing prepends to block1 text only. |
| 8 | KPH defaults to PG-13, self-deprecating wit and never volunteers crude/edgy material to a neutral opener (persona guardrail) | ⚠️ PRESENT_BEHAVIOR_UNVERIFIED | The guardrail TEXT is present and verified on disk: `knowledge/style/kurt-voice.md` (17364 bytes, contains the PG-13/match-and-escalate guardrail per 07-01) and `prompts/concierge.md`'s "Playful with teeth" section restates it (07-05). `scenarios/kph_crude_humor_guard.yaml` exists and parses. What is NOT proven: the actual LLM's behavior against this guardrail in a live conversation — see behavior_unverified_items and Human Verification. |
| 9 | Time-aware pacing: a short remaining-time budget gets tight highlights + a closing pointer; a longer one gets depth (D-06) | ✗ FAILED | Fully implemented and unit-tested in isolation (`render_pacing_note`, `PACING_TIGHT_THRESHOLD_SECONDS=90.0`, `SessionLifecycle.remaining_seconds()`, `KnowledgeRouterProcessor(remaining_seconds_fn=...)` — 8 passing tests) but **never wired into the running system**: `pipeline.py`'s `build_pipeline()` constructs `KnowledgeRouterProcessor(cfg=cfg, knowledge_cfg=knowledge_cfg, llm=llm, initial_topic=initial_topic, retrieval_index=retrieval_index)` with no `remaining_seconds_fn` argument (confirmed via direct grep/read of `pipeline.py:123-128`), and `bot.py` never constructs or threads a `SessionLifecycle` into `build_pipeline()` at all. In the deployed bot this defaults to `None` on every real session — pacing never fires. 07-05-SUMMARY.md documents this itself under "Follow-up wiring opportunity (not blocking)," but per goal-backward verification this is a genuine gap against the plan's own declared must-have, independent of the live-audio-venv blocker affecting SC2-4/8 above (this is a deterministic code-wiring fact, verifiable with no live call at all). |
| 10 | Honest-unknowns + do-not-say boundary persona rules exist as versioned markdown (D-12, PIPE-06) | ✓ VERIFIED | `prompts/concierge.md` confirmed to contain the "When you don't know" (honest+redirect) and "What you never say" (do-not-say boundary) sections; `grep -Eiq "tour|long version" prompts/concierge.md` matches per 07-05-SUMMARY.md's own recorded check, independently re-confirmed. (Behavioral proof of these rules in a live conversation is folded into item 4/SC4's deferred live-audio item, not double-counted here.) |

**Score:** 5/10 truths verified (4 present-and-wired-but-behaviorally-unproven pending a live-audio run; 1 failed — pacing not wired to production).

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `apps/voice/knowledge/manifest.yaml` | N-topic manifest, 3 topics, tour_priority, public:true per source | ✓ VERIFIED | Confirmed via direct YAML load: topics = [klanker-maker, defcon-run-34, meshtk]; tour_priority matches; every source across all 3 topics carries `public: true`. |
| `apps/voice/knowledge/router/topic-map.yaml` | 3-topic keyword/alias map, confidence_floor | ✓ VERIFIED | Confirmed via direct YAML load: 3 topics, `confidence_floor=2`. |
| `apps/voice/knowledge/topics/klanker-maker.md` + `defcon-run-34.md` + `meshtk.md` | Voice-friendly deep packs (block1 content) | ✓ VERIFIED | All 3 files present, non-trivial size (26.6KB / 13.9KB / 12.6KB); advisory_lint clean per 07-03-SUMMARY.md's recorded shell output. |
| `apps/voice/knowledge/style/kurt-voice.md` | Kurt STYLE layer, stable cached prefix, PG-13 guardrail | ✓ VERIFIED | Present, 17.4KB. |
| `apps/voice/src/klanker_voice/knowledge/prompt_assembly.py` | Two/three-block system assembly + count_tokens + pacing | ✓ VERIFIED | Present; contains `build_system_blocks`, `apply_system_blocks`, `render_pacing_note`; `cache_control`/`remaining_seconds` confirmed via grep. |
| `apps/voice/src/klanker_voice/knowledge/router.py` | KnowledgeRouterProcessor (classify + ack + retrieval + pacing threading) | ✓ VERIFIED (component); ⚠️ ORPHANED input (pacing) | Class exists, correctly wired into `build_pipeline`'s processor list between `stt` and `user_aggregator` (confirmed: `processors.extend([stt, router, user_aggregator, llm, tts, ...])`). `remaining_seconds_fn` parameter exists and works when supplied, but no caller in the real pipeline ever supplies it (see Truth #9). |
| `apps/voice/src/klanker_voice/knowledge/retrieval.py` | FTS5 chunker + index builder + topic-scoped BM25 query | ✓ VERIFIED | Present; `RetrievalIndex` constructed once per session in `pipeline.py:121` and passed to the router — genuinely wired, not orphaned. |
| `apps/voice/src/klanker_voice/knowledge/lint.py` | Advisory do-not-say lint (flag-only) | ✓ VERIFIED | Present; used by both the pack-authoring workflow and `refresh_knowledge.py`'s `flag_landmines`. |
| `apps/voice/scripts/refresh_knowledge.py` | Manifest-driven distill + FTS5 index build + advisory-lint flagging | ✓ VERIFIED (code); ⚠️ live run never executed | Present, 20 passing unit tests; real bugs found/fixed via dry-run against real checkouts (see Truth #3). |
| `apps/voice/scripts/transcribe.py` + `normalize.py` + `normalize_map.json` | Promoted prep scripts | ✓ VERIFIED | Present per 07-04-SUMMARY.md's file list; not independently re-verified byte-for-byte in this pass (low risk, no debt markers found in project-wide scan). |
| `apps/voice/Makefile` `knowledge` target | Shells to refresh_knowledge.py | ✓ VERIFIED | Confirmed: lines 13-15, `## knowledge: regenerate curated packs...` + `uv run python scripts/refresh_knowledge.py`. |
| `kv/internal/app/cmd/knowledge.go` + `root.go` registration | `kv knowledge refresh` thin dispatcher | ✓ VERIFIED | Confirmed present; `cd kv && go build ./...` clean; `go test ./...` clean (`internal/app/cmd`, `internal/app/electro` both pass). |
| `apps/voice/scenarios/kph_*.yaml` (12 files) | Benchmark eval scenario set | ✓ VERIFIED (exists/parses); ⚠️ never run live | All 12 files present, confirmed independently parseable via `yaml.safe_load`. |
| `apps/voice/knowledge/index/klanker-maker/docs.jsonl` | 2601-chunk real km corpus | ✓ VERIFIED | Present on disk, confirmed non-trivial (real docs-derived content, per 07-02-SUMMARY.md's grep-confirmed long-tail proof). |
| `apps/voice/knowledge/index/{defcon-run-34,meshtk}/*.jsonl` | Retrieval indexes for the other 2 topics | ✗ NOT YET BUILT (by design) | Only `klanker-maker` has a built index today — expected per 07-03's own "Next Phase Readiness" note: these are Plan-04's refresh job, and the real live refresh run (Truth #3) was never executed. `RetrievalIndex` degrades gracefully to curated-pack-only for these two topics (proven in `test_deep_turn_for_topic_with_no_built_index_degrades_to_curated_pack_only`) — this is a design-acknowledged gap, not a regression, and does not block the 3-topic curated-pack coverage. |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `apps/voice/bot.py` | `build_pipeline()` | direct call | ✓ WIRED | `bot.py:39`: `built = build_pipeline(cfg, transport)`. |
| `build_pipeline()` | `apply_system_blocks`/`build_system_blocks` | direct call, applies two/three-block system array to the live LLM | ✓ WIRED | `pipeline.py:112`. |
| `build_pipeline()` | `KnowledgeRouterProcessor` | constructed once per session, inserted between `stt` and `user_aggregator` | ✓ WIRED | Confirmed processor ordering in `pipeline.py`'s `processors` list. |
| `build_pipeline()` | `RetrievalIndex` | constructed once per session (`retrieval_index = RetrievalIndex(knowledge_cfg) if knowledge_cfg.retrieval_enabled else None`), passed to router | ✓ WIRED | `pipeline.py:121-126`. |
| `KnowledgeRouterProcessor` | `remaining_seconds_fn` → `SessionLifecycle.remaining_seconds()` | intended per 07-05's design | ✗ NOT_WIRED | No caller in `pipeline.py` or `bot.py` ever constructs a `SessionLifecycle` or passes a bound `remaining_seconds` callable into the router constructor. This is Truth #9's gap. |
| `refresh_knowledge.py` | `retrieval.build_topic_index`/`chunk_text` | calls the Plan-02 functions verbatim, does not reimplement | ✓ WIRED | Confirmed by 07-04-SUMMARY.md's own test (`test_chunk_writer_output_loads_through_plan02_retrieval_index`) and by reading the module's imports. |
| `Makefile knowledge:` / `kv knowledge refresh` | `refresh_knowledge.py` | shell-out dispatch | ✓ WIRED | Confirmed via Makefile + `knowledge.go` reading above; `go build` clean. |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Full Python test suite | `cd apps/voice && uv run pytest -q` | `255 passed, 0 failed` | ✓ PASS |
| kv Go module builds + tests | `cd kv && go build ./... && go test ./...` | Build clean; `internal/app/cmd` and `internal/app/electro` pass | ✓ PASS |
| Manifest + topic-map load and carry the 3-topic launch set | `python -c "import yaml; ..."` | 3 topics in both files; `confidence_floor=2`; `tour_priority` matches | ✓ PASS |
| All 12 `kph_*.yaml` scenario files parse | `python -c "import yaml,glob; [yaml.safe_load(open(f)) for f in glob.glob('scenarios/kph_*.yaml')]"` | All parse without error | ✓ PASS |
| `remaining_seconds_fn` wired into the real pipeline | `grep -n "remaining_seconds" apps/voice/src/klanker_voice/pipeline.py apps/voice/bot.py` | No match in either file | ✗ FAIL (confirms Truth #9's gap) |
| Debt-marker scan (TBD/FIXME/XXX/TODO/HACK/PLACEHOLDER) across all phase-modified files | `grep -n -E "TBD|FIXME|XXX|TODO|HACK|PLACEHOLDER|not yet implemented|coming soon"` over all files_modified across 07-01..05 | No hits | ✓ PASS (no debt markers) |

### Probe Execution

Not applicable — this phase has no `scripts/*/tests/probe-*.sh` convention; verification used the project's own pytest/go test/scenario-parsing checks instead.

### Requirements Coverage

| Requirement | Source Plan(s) | Description | Status | Evidence |
|-------------|----------------|--------------|--------|----------|
| PIPE-06 | 07-01, 07-05 | Agent speaks as the KlankerMaker concierge via a versioned markdown system prompt | ✓ SATISFIED | `prompts/concierge.md` v4 + `knowledge/style/kurt-voice.md` (persona/style layers); REQUIREMENTS.md marks `[x]` Complete. Behavioral proof of persona quality is folded into the deferred live-audio item (Truths #4/#8). |
| PIPE-07 | 07-01, 07-02, 07-04 | Developer can run the full bot locally with only the three provider API keys (no 4th vendor) | ✓ SATISFIED | Router fallback is same-vendor Haiku (no 4th vendor); retrieval is stdlib sqlite3 FTS5 (keyless, no vendor at all); refresh's only network calls are Anthropic. No new provider dependencies introduced anywhere in this phase — confirmed via reading all 5 plans' `<threat_model>` T-07-SC entries (all "accept — no new packages"). |
| PIPE-10 | 07-01 through 07-05 (all) | RAG/knowledge retrieval: router + curated per-topic packs + local keyless SQLite FTS5/BM25 retrieval | ✓ SATISFIED (code-complete); ⚠️ live proof deferred | REQUIREMENTS.md marks `[x]` Complete, Phase 7. Code-level: router, packs, retrieval, refresh, and benchmark scenarios all exist and are unit-tested (255/255 passing). Live-audio proof of the full RAG conversational loop remains the one consolidated open item across all 5 plans (see Human Verification). |

No orphaned requirements: `.planning/REQUIREMENTS.md` maps only PIPE-06, PIPE-07, and PIPE-10 to Phase 7, and all three appear in at least one plan's `requirements:` frontmatter field across 07-01..05.

### Anti-Patterns Found

None. A project-wide scan of every file touched across all 5 plans (`config.py`, `pipeline.toml`, the `knowledge/` package, `retrieval.py`, `router.py`, `lint.py`, `session.py`, `refresh_knowledge.py`, `transcribe.py`, `normalize.py`, `knowledge.go`, `root.go`, `concierge.md`) for `TBD|FIXME|XXX|TODO|HACK|PLACEHOLDER|not yet implemented|coming soon` returned zero hits.

### Human Verification Required

### 1. Full live-audio benchmark run (closes ROADMAP criteria 2 and 4, plus the persona-guardrail behavioral proof)

**Test:** `uv sync --group dev` (installs `pipecat-ai[evals,local]`), then `uv run python bot.py -t eval` in one terminal and `uv run pipecat eval run scenarios/kph_*.yaml scenarios/memory.yaml --bot-url ws://localhost:7860` in another.
**Expected:** Cross-topic correctness (km/defcon/meshtk), retrieval depth/coverage (2 distinct long-tail facts), router accuracy (3-topic + ambiguous battery), honest-unknowns (no bluffing), tour-mode steering, and the PG-13 crude-humor guard all judge correct. Record the overall pass rate and the specific router-accuracy number.
**Why human:** Requires installing a local-model eval extra not present in this venv, a running bot process, and real billed Deepgram/Anthropic/ElevenLabs API calls end-to-end through the full voice pipeline — none of which a static verifier can exercise.

### 2. Live knowledge refresh + git-diff review (closes ROADMAP criterion 3)

**Test:** Run `make -C apps/voice knowledge` (or `kv knowledge refresh`) for real with `ANTHROPIC_API_KEY` set and the local `klankrmkr`/`defcon.run.34`/`meshtk` checkouts present; review the resulting `knowledge/` git diff and any advisory-lint flags per D-09 before committing.
**Expected:** Packs, style layer, and FTS5 chunk indexes regenerate cleanly from the live repos; the diff is reviewable and correct.
**Why human:** The plan explicitly deferred this billed, potentially destructive operation from automated execution; the distillation LLM pass has zero live-call evidence today, only fixture-level and dry-run proof.

## Gaps Summary

> **UPDATE 2026-07-07 (post-verification gap closure, commit `f13cb84`):** The single deterministic gap below was **CLOSED** via TDD. `build_pipeline()` now forwards a `remaining_seconds_fn` to `KnowledgeRouterProcessor`, and `server._run_session()` sources it from the live `SessionLifecycle.remaining_seconds` (the verifier pointed at `bot.py`, but the real production construction site is `server.py`'s `_run_session`; `bot.py` is the dev/eval path with no session cap, correctly staying `None`). Two RED wiring tests (`tests/test_rtvi.py::TestPacingFnWiring`) were proven failing then GREEN; full suite **258 passed**. **No deterministic gaps remain.** Phase status moves `gaps_found → human_needed`: the only open items are the live-audio eval run and the live D-09 refresh, which genuinely require a human (extras install + billed round trips). The original finding is preserved below for the record.

One concrete, code-level gap blocks a clean pass: **time-aware pacing (D-06) is fully built and unit-tested but never wired into the actually-running bot** — `pipeline.py` never threads a `remaining_seconds_fn` into `KnowledgeRouterProcessor`, and `bot.py` never constructs a `SessionLifecycle` to source one from. This is independent of the venv/live-audio-eval blocker that affects the other open items; it can be confirmed with a plain grep and would remain true even with the eval extras installed. It is a small, well-scoped follow-up (07-05-SUMMARY.md itself names the fix: thread `lifecycle.remaining_seconds` from `server.py`'s `_run_session` through `build_pipeline()` into the router constructor) but as written today, the "KPH paces to remaining session time" must-have does not hold in the deployed system.

Beyond that one gap, the phase is code-complete and test-complete: the full 255-test Python suite and the `kv` Go build/test both pass cleanly with zero debt markers across every phase-modified file. ROADMAP criterion 1 (prompt caching) is genuinely live-proven, not just unit-tested. ROADMAP criteria 2, 3, and 4 all have solid mechanism-level proof (real corpora, real bugs found and fixed against real external checkouts, passing behavioral unit tests for router/cache/discrimination invariants) but share one consolidated, explicitly-documented-by-every-plan open item: no scenario has ever been run through the live audio harness, because this venv lacks the `pipecat-ai[evals,local]` (kokoro/moonshine) extras. No pass-rate or router-accuracy number — which 07-05's own plan designates as the phase gate — has been recorded anywhere.

**Recommendation:** Fix the pacing-wiring gap (small, well-understood, no design work needed) as a quick follow-up plan or hot-fix. Separately, a human should run `uv sync --group dev` and execute the full live scenario set plus one real `make -C apps/voice knowledge` refresh before this phase is considered fully proven end-to-end, not just code-complete.

---

*Verified: 2026-07-07*
*Verifier: Claude (gsd-verifier)*
