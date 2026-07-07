---
phase: 07-kph-knowledge-base
plan: 01
subsystem: ai
tags: [anthropic, pipecat, prompt-caching, yaml, fastapi-adjacent, voice-agent, knowledge-base]

# Dependency graph
requires: []
provides:
  - "[knowledge] config seam (KnowledgeConfig + load_knowledge_config, config.py)"
  - "knowledge/manifest.yaml + knowledge/router/topic-map.yaml (N-topic-ready schema, km populated)"
  - "knowledge/topics/klanker-maker.md (km deep pack, promoted from corpus/km-digest.md + diagram + transcript quotes)"
  - "knowledge/style/kurt-voice.md (Kurt STYLE layer, stable cached prefix, ~5.4k tokens combined with persona)"
  - "klanker_voice.knowledge package: prompt_assembly.py (build_system_blocks/apply_system_blocks/count_tokens), router.py (classify/KnowledgeRouterProcessor), lint.py (advisory_lint)"
  - "pipeline.py wired: two-block cached system prompt + KnowledgeRouterProcessor between stt and user_aggregator"
  - "scenarios/kph_knowledge_km.yaml + scenarios/kph_cache_verify.yaml eval scenarios"
  - "harness/report.py TurnRecord.cache_read_input_tokens (additive usage-surface field)"
affects: [07-02-retrieval, 07-03-more-topics, 07-04-refresh-workflow, 07-05-pacing-evals]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Independent [table]-scoped config loaders (load_knowledge_config mirrors load_quota_config) so new config tables never force every existing fixture to change"
    - "Two-block Anthropic system array (block0 cached/stable, block1 swappable) built via a pure function (build_system_blocks) and applied to a live pipecat LLM service by direct Settings.system_instruction assignment, bypassing pipecat's own LLMContext system-message flattening"
    - "Keyword-weighted router with a same-vendor LLM fallback below a confidence floor -- never guesses, never adds a 4th vendor"
    - "Advisory-only (flag, never block) do-not-say lint for offline corpus review"

key-files:
  created:
    - apps/voice/knowledge/manifest.yaml
    - apps/voice/knowledge/router/topic-map.yaml
    - apps/voice/knowledge/topics/klanker-maker.md
    - apps/voice/knowledge/style/kurt-voice.md
    - apps/voice/src/klanker_voice/knowledge/__init__.py
    - apps/voice/src/klanker_voice/knowledge/prompt_assembly.py
    - apps/voice/src/klanker_voice/knowledge/router.py
    - apps/voice/src/klanker_voice/knowledge/lint.py
    - apps/voice/scenarios/kph_knowledge_km.yaml
    - apps/voice/scenarios/kph_cache_verify.yaml
    - apps/voice/tests/test_knowledge_pack.py
    - apps/voice/tests/test_knowledge_router.py
  modified:
    - apps/voice/src/klanker_voice/config.py
    - apps/voice/pipeline.toml
    - apps/voice/src/klanker_voice/pipeline.py
    - apps/voice/src/klanker_voice/harness/report.py
    - apps/voice/tests/test_report.py

key-decisions:
  - "Renamed the planned cache_min_tokens TOML/field to cache_floor -- the existing credential-field regex flags any field ending in _token(s), so cache_min_tokens was rejected as credential-looking material at config load time (Rule 1 auto-fix)"
  - "build_system_blocks(cfg, knowledge_cfg, topic, ...) takes an explicit knowledge_cfg argument -- the plan's build_system_blocks(cfg, topic, ...) shorthand didn't specify how [knowledge] config reaches the function; KnowledgeConfig is loaded independently per Task 1's own explicit instruction"
  - "The two-block cached system array is applied directly to the live LLM service's Settings.system_instruction (apply_system_blocks), not via an LLMContext system-role message -- pipecat 1.5.0's AnthropicLLMAdapter flattens list-content system messages into a joined string before the API call, silently discarding cache_control markers. Settings.system_instruction is the one path AnthropicLLMAdapter._resolve_system_instruction returns verbatim. Documented at length in prompt_assembly.py; must never be touched via LLMUpdateSettingsFrame or append_system_instruction (both assume a string and would discard the block list)"
  - "LLMContext no longer seeds an initial system message at all -- the greet-first developer kick becomes context message 0 instead, unaffected by the system-message change"
  - "TurnRecord.cache_read_input_tokens is an additive dataclass field, not one of the five frozen STAGE_NAMES -- keeps the schema-v1 stability contract for existing Phase 5 HUD/CI consumers"

requirements-completed: [PIPE-10, PIPE-06, PIPE-07]

coverage:
  - id: D1
    description: "[knowledge] config table + KnowledgeConfig loader validates manifest/topic-map/packs/style paths, rejects credential-looking fields, and existing 168+ tests stay green"
    requirement: "PIPE-10"
    verification:
      - kind: unit
        ref: "tests/test_knowledge_pack.py::test_real_checked_in_knowledge_table_round_trips"
        status: pass
      - kind: unit
        ref: "tests/test_knowledge_pack.py::test_knowledge_credential_looking_field_rejected"
        status: pass
    human_judgment: false
  - id: D2
    description: "build_system_blocks() returns a two-block Anthropic system array, cache_control on block0 only, block0 byte-identical across topics and measured >=4096 tokens live (5444 measured)"
    requirement: "PIPE-10"
    verification:
      - kind: unit
        ref: "tests/test_knowledge_pack.py::test_build_system_blocks_returns_two_blocks_cache_control_on_first_only"
        status: pass
      - kind: unit
        ref: "tests/test_knowledge_pack.py::test_build_system_blocks_block0_byte_identical_across_topics"
        status: pass
      - kind: integration
        ref: "live client.messages.count_tokens call, see 'Live verification' section below -- block0 = 5444 tokens"
        status: pass
    human_judgment: false
  - id: D3
    description: "Anthropic prompt caching genuinely engages end to end: cache_creation_input_tokens > 0 on the first call, cache_read_input_tokens > 0 on the second call with a byte-identical system[0] (ROADMAP success criterion 1)"
    requirement: "PIPE-10"
    verification:
      - kind: integration
        ref: "live two-call Anthropic API proof against build_system_blocks() output, see 'Live verification' section below -- turn1 cache_creation_input_tokens=5438, turn2 cache_read_input_tokens=5438"
        status: pass
    human_judgment: false
  - id: D4
    description: "KnowledgeRouterProcessor classifies km, acks ONLY on a genuine deep-pack switch (never on a same-topic follow-up or a below-confidence-floor guess), falls back to a same-vendor Haiku call before declining, and is wired between stt and user_aggregator"
    requirement: "PIPE-07"
    verification:
      - kind: unit
        ref: "tests/test_knowledge_router.py::TestKnowledgeRouterProcessor::test_ack_fires_on_first_topic_switch"
        status: pass
      - kind: unit
        ref: "tests/test_knowledge_router.py::TestKnowledgeRouterProcessor::test_no_ack_on_same_topic_followup"
        status: pass
      - kind: unit
        ref: "tests/test_knowledge_router.py::TestKnowledgeRouterProcessor::test_low_confidence_uses_fallback_classify_then_switches"
        status: pass
    human_judgment: false
  - id: D5
    description: "KPH answers a directed km question correctly, in Kurt's voice, PG-13 by default -- judged live via the full audio pipeline (kph_knowledge_km.yaml scenario) against real Deepgram/Anthropic/ElevenLabs services"
    verification: []
    human_judgment: true
    rationale: "The scenario file is written and the km deep pack + STYLE layer are authored per the plan, but this venv's kokoro/moonshine local eval dependencies (pipecat-ai[evals,local]) are not installed, so pipecat eval run could not be exercised in this offline execution session. Installing new packages is excluded from auto-fix per the deviation rules (Rule 3 exclusion) and requires a human-verified checkpoint, not a silent install. A human must run `pipecat eval run scenarios/kph_knowledge_km.yaml --bot-url ws://localhost:7860` against a running `bot.py -t eval` (after installing the local eval extra) to close this out."

# Metrics
duration: 65min
completed: 2026-07-07
status: complete
---

# Phase 7 Plan 01: KPH Knowledge Base -- km Walking Slice Summary

**Two-block cached system prompt (persona + Kurt STYLE layer + topic-map hooks in a cached ~5.4k-token block0, per-topic deep pack in an uncached block1) + a keyword-first KnowledgeRouterProcessor with a same-vendor Haiku fallback, live-proven to actually engage Anthropic prompt caching (cache_read_input_tokens=5438 on the second same-topic call).**

## Performance

- **Duration:** ~65 min
- **Completed:** 2026-07-07
- **Tasks:** 3 (all `type="auto" tdd="true"`, no checkpoints)
- **Files modified:** 17 (2 modified pre-existing test files, 5 modified source files, 12 new files, +2002/-10 lines)

## Accomplishments

- `[knowledge]` config seam (`KnowledgeConfig` + `load_knowledge_config()`) mirroring the `load_quota_config` precedent -- an independent loader so the 168+-test pre-existing suite never had to change its fixtures.
- N-topic-ready `knowledge/manifest.yaml` + `knowledge/router/topic-map.yaml`, populated with klanker-maker (km) only; 07-03 can append defcon-run-34/meshtk without touching the schema.
- `knowledge/topics/klanker-maker.md`: the km deep pack, promoted from the already-curated `corpus/km-digest.md`, folding in the sandbox-architecture diagram legend and five verbatim quotes lifted directly from Kurt's recorded transcripts.
- `knowledge/style/kurt-voice.md`: the Kurt STYLE layer living in the cached stable prefix -- distilled transcript cadence, the `kurt-humor-personality.md` profile incorporated in full, verbatim exemplar lines, a reference-vocabulary glossary, five flat-fact-to-Kurt-voiced style-transfer examples, and the PG-13 match-and-escalate persona guardrail.
- `klanker_voice.knowledge` package: `prompt_assembly.py` (`build_system_blocks`, `apply_system_blocks`, `count_tokens`, `render_topic_hooks`), `router.py` (`classify`, `KnowledgeRouterProcessor`, `default_haiku_fallback_classify`), `lint.py` (`advisory_lint`).
- `pipeline.py` rewired: the persona-only system message is replaced by the two-block cached array (applied directly to the live LLM service, see Deviations), and `KnowledgeRouterProcessor` sits between `stt` and `user_aggregator`.
- **Live-proven caching**: a direct two-call Anthropic API test using `build_system_blocks()`'s real output shows `cache_creation_input_tokens=5438` on the first call and `cache_read_input_tokens=5438` on the second -- ROADMAP success criterion 1, verified against the real API, not mocked.
- `scenarios/kph_knowledge_km.yaml` and `scenarios/kph_cache_verify.yaml` eval scenarios (judge-based, matching `memory.yaml`'s convention); `harness/report.py`'s `TurnRecord` gained an additive `cache_read_input_tokens` field for the harness's own usage-surface capture.

## Task Commits

Each task was committed atomically:

1. **Task 1: `[knowledge]` config seam + manifest/topic-map schema + Wave-0 failing tests** - `09bcb04` (feat)
2. **Task 2: km deep pack + Kurt STYLE layer + two-block cached prompt assembly + advisory lint** - `5eb42c7` (feat)
3. **Task 3: KnowledgeRouterProcessor + pipeline wiring + km eval + live cache-verify** - `5117e60` (feat)

_Note: this plan's tasks are TDD-flavored (`tdd="true"`) but structured as one commit per task rather than separate RED/GREEN/REFACTOR commits -- Task 1's commit itself contains the RED state (the two Wave-0 test files exist and fail with ImportError until Task 2/3 build the modules they import), verified at Task 1 commit time before proceeding._

## Files Created/Modified

- `apps/voice/src/klanker_voice/config.py` - `KnowledgeConfig` dataclass + `load_knowledge_config()`
- `apps/voice/pipeline.toml` - `[knowledge]` table (manifest/topic_map/packs_dir/style_path/cache_floor)
- `apps/voice/knowledge/manifest.yaml` - N-topic manifest, km populated, tour_priority
- `apps/voice/knowledge/router/topic-map.yaml` - weighted keyword topic map, km entry, confidence_floor
- `apps/voice/knowledge/topics/klanker-maker.md` - km deep pack (block1 content)
- `apps/voice/knowledge/style/kurt-voice.md` - Kurt STYLE layer (block0 content, stable/cached)
- `apps/voice/src/klanker_voice/knowledge/__init__.py` - package docstring
- `apps/voice/src/klanker_voice/knowledge/prompt_assembly.py` - two-block system assembly + count_tokens + apply_system_blocks
- `apps/voice/src/klanker_voice/knowledge/router.py` - classify() + KnowledgeRouterProcessor
- `apps/voice/src/klanker_voice/knowledge/lint.py` - advisory_lint()
- `apps/voice/src/klanker_voice/pipeline.py` - build_pipeline() wires the two-block prompt + router
- `apps/voice/src/klanker_voice/harness/report.py` - TurnRecord.cache_read_input_tokens (additive)
- `apps/voice/scenarios/kph_knowledge_km.yaml` - km eval scenario
- `apps/voice/scenarios/kph_cache_verify.yaml` - two-same-topic-turn cache scenario
- `apps/voice/tests/test_knowledge_pack.py` - KnowledgeConfig + build_system_blocks + advisory_lint tests (21 tests)
- `apps/voice/tests/test_knowledge_router.py` - classify() + KnowledgeRouterProcessor tests (10 tests)
- `apps/voice/tests/test_report.py` - 3 new tests for the additive cache_read_input_tokens field

## Decisions Made

- **`cache_min_tokens` -> `cache_floor` rename** (Rule 1 auto-fix): the plan's own suggested TOML field name collides with `config.py`'s existing `_CREDENTIAL_FIELD_RE` guard (it flags any `_token`/`_tokens`-suffixed field as credential-looking). Renamed everywhere (TOML, dataclass field, tests) before it ever shipped broken.
- **`build_system_blocks(cfg, knowledge_cfg, topic, ...)`** takes `knowledge_cfg` explicitly rather than assuming a `cfg.knowledge` attribute -- Task 1's own action text explicitly chose an independent `load_knowledge_config()` over adding a field to `PipelineConfig`, so the function needs both configs passed in.
- **`apply_system_blocks()` bypasses `LLMContext`'s system-message convention entirely.** Investigated pipecat 1.5.0's `AnthropicLLMAdapter` source directly: `_extract_initial_system` joins a list-content system message's text parts into a single string (discarding `cache_control`) before the API call, and `_compose_system_instruction` (triggered by `LLMUpdateSettingsFrame`/`append_system_instruction`) assumes `system_instruction` is a plain string and silently discards a list. The one path that survives intact is a direct `Settings.system_instruction` assignment, which `_resolve_system_instruction` returns verbatim. This is documented at length in `prompt_assembly.py`'s `apply_system_blocks` docstring so a future edit doesn't accidentally route through the broken paths.
- **`LLMContext()` starts empty** (no initial system message) -- the greet-first developer kick (`GREET_KICK_MESSAGE`) becomes context message 0 instead of message 1. Verified this doesn't change `AnthropicLLMAdapter._extract_initial_system`'s behavior (it only inspects a `"system"`-role `messages[0]`, and the developer-role kick was never that).
- **`TurnRecord.cache_read_input_tokens`** is additive, not one of the five frozen `STAGE_NAMES` -- `report.py`'s own docstring calls its JSON schema "a stability contract" for Phase 5's HUD/CI; adding a new dict key is safe, changing the five-stage contract would not have been.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] `cache_min_tokens` field name collided with the credential-field rejection regex**
- **Found during:** Task 1, first `pytest tests/test_config.py` run after adding `[knowledge]` to `pipeline.toml`
- **Issue:** `config.py`'s `_CREDENTIAL_FIELD_RE` (`.../(?:token|tokens)(?:_|$)/`) matched `cache_min_tokens`, raising `ConfigError: ... looks like credential material` on load of the real checked-in `pipeline.toml` -- a genuine bug that would have broken every existing test touching `load_config`/`load_quota_config` against the real file.
- **Fix:** Renamed the field/TOML key to `cache_floor` everywhere (pipeline.toml, config.py, tests).
- **Files modified:** `pipeline.toml`, `src/klanker_voice/config.py`, `tests/test_knowledge_pack.py`
- **Verification:** `tests/test_config.py` (32/32) and `tests/test_knowledge_pack.py` (21/21) pass.
- **Committed in:** `09bcb04` (Task 1 commit)

**2. [Rule 3 - Blocking gap] pipecat's `LLMContext` system-message path can't carry `cache_control` block-level markers**
- **Found during:** Task 2, while wiring `build_system_blocks()`'s output into `pipeline.py`
- **Issue:** Read `AnthropicLLMAdapter`/`LLMService` source directly (pipecat 1.5.0, installed in `.venv`). Confirmed `_extract_initial_system` flattens a list-content system message into a joined string before the API call, discarding `cache_control`. There is no supported way to send a two-block cached system prompt through `LLMContext`'s normal `{"role": "system", "content": [...]}` convention in this pipecat version.
- **Fix:** `apply_system_blocks()` sets the two-block array directly on the live LLM service's `Settings.system_instruction`, which `_resolve_system_instruction` returns verbatim (bypassing the flattening). Documented at length in code so it isn't "fixed" back into the broken path later.
- **Files modified:** `src/klanker_voice/knowledge/prompt_assembly.py`, `src/klanker_voice/pipeline.py`
- **Verification:** Live two-call Anthropic API proof (see below) shows the `cache_control` marker genuinely reaching the API and caching engaging.
- **Committed in:** `5eb42c7` (Task 2 commit)

---

**Total deviations:** 2 auto-fixed (1 Rule 1 bug, 1 Rule 3 blocking gap)
**Impact on plan:** Both were necessary for correctness -- the first would have broken config loading outright, the second would have silently defeated the entire caching mechanism this plan exists to prove. No scope creep; both fixes stayed within the plan's declared files.

## Live Verification

**Test suite:** `cd apps/voice && uv run pytest -q` -> **205 passed, 1 failed** (out of 206 total). The one failure, `tests/test_session.py::test_auto_trip_flips_control_item_when_ceiling_crossed`, is a pre-existing, timing-sensitive test (unrelated to this plan -- untouched by any of this plan's commits) that passed 3/3 times when run in isolation; it only intermittently fails as part of the full-suite run against dynamodb-local. Confirmed pre-existing and out of scope per the deviation rules' scope boundary.

**Live cache-engagement proof** (ROADMAP success criterion 1), run directly against `build_system_blocks()` -- the exact function `pipeline.py` calls -- using the real `ANTHROPIC_API_KEY` already present in `apps/voice/.env`:

```
block0 tokens (count_tokens):        5444   (>= cfg.knowledge.cache_floor = 4096)
block0 has cache_control:            {'type': 'ephemeral'}
block1 has cache_control:            False

turn1 usage: cache_creation_input_tokens=5438  cache_read_input_tokens=0
turn2 usage: cache_creation_input_tokens=0     cache_read_input_tokens=5438
```

Turn 1 (first call this session with this exact `system` array) writes the cache; turn 2 (byte-identical `system[0]`, a different user question) reads it -- `cache_read_input_tokens > 0` on the second call is exactly the ROADMAP success criterion. This is a real, live, unmocked Anthropic API call using the production `build_system_blocks()` code path.

**Not exercised (deferred, not self-approved):** the plan's Task 3 `<human-check>` bullet -- running `scenarios/kph_knowledge_km.yaml` through the full live audio pipeline (`pipecat eval run` against a running `bot.py -t eval`, with real Deepgram STT + ElevenLabs TTS + the router's frame-path classify/ack) -- was not exercised. This venv's `kokoro`/`moonshine_onnx` local eval dependencies (`pipecat-ai[evals,local]`) are not installed, and installing new packages is excluded from auto-fix by the deviation rules (Rule 3 exclusion) -- it requires a human-verified checkpoint, not a silent install during execution. See `coverage: D5` above. A human (or a follow-up session with the eval extras installed) should run:

```
uv run python bot.py -t eval   # in one terminal
uv run pipecat eval run scenarios/kph_knowledge_km.yaml scenarios/kph_cache_verify.yaml --bot-url ws://localhost:7860
```

and confirm the km scenario judges correct and observe `cache_read_input_tokens` in the resulting harness artifact.

## Issues Encountered

None beyond the two auto-fixed deviations above.

## User Setup Required

None - no external service configuration required. `ANTHROPIC_API_KEY` was already present in `apps/voice/.env` from prior phases.

## Next Phase Readiness

- The router/prompt-assembly/config seams are all N-topic-ready: 07-03 (more topics) only needs to append entries to `manifest.yaml` and `topic-map.yaml` plus author new `knowledge/topics/*.md` packs -- no schema or code change.
- `build_system_blocks()`'s `retrieved_chunks`/`remaining_seconds` parameters are present-but-unused, exactly per 07-02 (local BM25 retrieval) and 07-05 (time-aware pacing)'s stated seams -- both inject into block1 only.
- `harness/report.py`'s `TurnRecord.cache_read_input_tokens` field exists for 07-04/07-05's harness work to populate from a real LLM-usage-frame observer (not yet wired to an observer in this plan -- the field exists, the wiring is future work).
- **Blocker for a full live UAT pass:** `pipecat-ai[evals,local]` (kokoro/moonshine) needs to be installed in this environment before `kph_knowledge_km.yaml`/`kph_cache_verify.yaml` can be run through the real audio harness. This is a package-legitimacy-adjacent install (already declared in `pyproject.toml`'s dev group, just not synced into this particular `.venv`) -- flagging for a human `uv sync --group dev` (or equivalent) rather than doing it silently here.

---
*Phase: 07-kph-knowledge-base*
*Completed: 2026-07-07*

## Self-Check: PASSED

All 12 created files verified present on disk; all 3 task commit hashes (09bcb04, 5eb42c7, 5117e60) verified present in git log.
