---
phase: 07-kph-knowledge-base
plan: 03
subsystem: ai
tags: [anthropic, pipecat, yaml, voice-agent, knowledge-base, prompt-caching]

# Dependency graph
requires:
  - phase: 07-kph-knowledge-base (07-01)
    provides: "[knowledge] config seam, two-block cached prompt assembly (prompt_assembly.build_system_blocks/load_topic_pack_text), KnowledgeRouterProcessor deep-turn ack, klanker-maker curated pack + N-topic-ready manifest/topic-map schema"
provides:
  - "knowledge/topics/defcon-run-34.md + knowledge/topics/meshtk.md: curated, voice-friendly DEEP packs (swappable system[1] content) distilled from the phase's own corpus digests + verbatim Kurt-voiced transcript quotes, advisory-lint clean"
  - "knowledge/router/topic-map.yaml: full defcon-run-34 + meshtk entries (hook + weighted keyword lists), deliberately omitting a bare 'toolkit' keyword (Pitfall 1 keyword-overlap guard)"
  - "knowledge/manifest.yaml: defcon-run-34 + meshtk topics appended with pack references and per-source notes marking their retrieval corpus 'doc-gen then index' for Plan 04's refresh (Amendment 3.D/5 -- code indexed directly, no doc-gen step)"
  - "tests/test_knowledge_router.py: 3 new keyless tests -- three-topic discrimination without collision, the toolkit-overlap-guard fallback, and a real topic-switch-then-same-topic-followup ack/no-reack proof"
  - "scenarios/kph_knowledge_defconrun.yaml + kph_knowledge_meshtk.yaml: per-topic eval scenarios mirroring kph_knowledge_km.yaml's shape"
affects: [07-04-refresh-workflow, 07-05-pacing-evals]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "manifest.yaml owns pack: path references (consumed by prompt_assembly.load_topic_pack_text); topic-map.yaml owns router classification only (hook + weighted keywords) -- confirmed and extended, not changed, from 07-01's actual shipped split"
    - "Deliberate keyword omission as a router-safety technique: a topic's keyword list can OMIT a generic/ambiguous term (meshtk's 'toolkit') specifically to keep it below the confidence floor rather than trying to disambiguate it after the fact"

key-files:
  created:
    - apps/voice/knowledge/topics/defcon-run-34.md
    - apps/voice/knowledge/topics/meshtk.md
    - apps/voice/scenarios/kph_knowledge_defconrun.yaml
    - apps/voice/scenarios/kph_knowledge_meshtk.yaml
  modified:
    - apps/voice/knowledge/router/topic-map.yaml
    - apps/voice/knowledge/manifest.yaml
    - apps/voice/tests/test_knowledge_router.py

key-decisions:
  - "Followed 07-01/07-02's ACTUAL shipped architecture (manifest.yaml owns pack: paths; topic-map.yaml is classification-only) rather than this plan's own literal acceptance-criteria snippet, which asked for a pack: field inside topic-map.yaml itself and called a non-existent klanker_voice.knowledge.pack module/load_topic_pack function. Verified every acceptance criterion's real intent against the actual API (prompt_assembly.load_topic_pack_text, lint.advisory_lint) instead -- same class of plan/reality drift 07-02-SUMMARY.md already documented for its own acceptance snippets."
  - "No 'Landmines / do-not-say' section in either new curated pack, unlike 07-01's klanker-maker.md precedent -- this plan's own prohibitions explicitly state 'MUST NOT include any Landmines/do-not-say content from the defcon or meshtk digests in a curated pack.' Followed the plan's explicit instruction over the earlier precedent; landmine avoidance is achieved by simply never writing the excluded facts into the pack at all, verified by a zero-finding advisory_lint pass on both packs."
  - "meshtk's topic-map keyword list deliberately omits a bare 'toolkit' keyword (Pitfall 1, RESEARCH's documented meshtk/generic-dev-tools overlap example) -- only distinctive multi-word forms ('mesh tk', 'meshtk', 'meshtastic toolkit', 'meshtastic') count toward its score, so a generic 'do you have a toolkit for X' utterance scores 0 and falls back rather than confidently misclassifying."
  - "Cross-topic cache-warmth check (km turn -> defcon turn, cache_read_input_tokens > 0 on the switched turn) is documented as a deferred human-check in the plan's own <verify> block rather than a new/edited scenario file -- per the plan's own explicit coordination note not to touch scenarios/kph_cache_verify.yaml (07-01's file)."

requirements-completed: [PIPE-10]

coverage:
  - id: D1
    description: "defcon-run-34.md and meshtk.md curated DEEP packs exist, are voice-friendly, and resolve via load_topic_pack_text() for all three primary topics"
    requirement: "PIPE-10"
    verification:
      - kind: unit
        ref: "manual shell check: load_topic_pack_text(load_knowledge_config(), 'defcon-run-34') and (..., 'meshtk') both non-empty"
        status: pass
      - kind: unit
        ref: "tests/test_knowledge_pack.py (21/21, unchanged) -- stable prefix byte-identical, no regression from the new packs"
        status: pass
    human_judgment: false
  - id: D2
    description: "Both curated packs are advisory-lint clean (Amendment 3.E) -- no residual AWS account IDs, ARNs, key blocks, or internal hostnames slipped in from the source digests"
    requirement: "PIPE-10"
    verification:
      - kind: unit
        ref: "manual shell check: klanker_voice.knowledge.lint.advisory_lint() returns [] for both knowledge/topics/defcon-run-34.md and knowledge/topics/meshtk.md"
        status: pass
    human_judgment: false
  - id: D3
    description: "topic-map.yaml + manifest.yaml together give the router and the pack loader everything needed for all three primary topics -- no schema change, only append, per 07-01's own N-topic-ready design"
    requirement: "PIPE-10"
    verification:
      - kind: unit
        ref: "manual shell check: yaml.safe_load of both files; manifest.yaml's per-topic pack: field present for klanker-maker/defcon-run-34/meshtk"
        status: pass
    human_judgment: false
  - id: D4
    description: "The router discriminates all three primary topics without collision, and the known 'toolkit' keyword-overlap utterance resolves to fallback (None) rather than a confident wrong meshtk pick (Pitfall 1)"
    requirement: "PIPE-10"
    verification:
      - kind: unit
        ref: "tests/test_knowledge_router.py::test_classify_discriminates_all_three_primary_topics_without_collision"
        status: pass
      - kind: unit
        ref: "tests/test_knowledge_router.py::test_classify_bare_toolkit_overlap_resolves_to_fallback_via_floor"
        status: pass
    human_judgment: false
  - id: D5
    description: "A real topic switch (km -> defcon) fires the ack and swaps block1; a same-topic follow-up on the new topic does NOT re-ack -- proven across an actual topic change, not just within one topic"
    requirement: "PIPE-10"
    verification:
      - kind: unit
        ref: "tests/test_knowledge_router.py::TestKnowledgeRouterProcessor::test_topic_switch_fires_ack_then_same_topic_followup_does_not"
        status: pass
    human_judgment: false
  - id: D6
    description: "Both new per-topic eval scenarios are YAML-valid and reference the shared judge_factory; KPH answers a directed defcon.run.34 / meshtk question correctly, in Kurt's voice, judged live via the full audio pipeline"
    verification:
      - kind: unit
        ref: "manual shell check: yaml.safe_load of both scenario files; judge.eval.factory == klanker_voice.harness.judge.judge_factory"
        status: pass
    human_judgment: true
    rationale: "Same documented blocker as 07-01-SUMMARY.md's D5 and 07-02-SUMMARY.md's D8: this venv's pipecat-ai[evals,local] (kokoro/moonshine) extras are not installed, so a live pipecat eval run could not be exercised in this offline execution session. Package installs are excluded from auto-fix (Rule 3 exclusion) -- requires a human-verified checkpoint (uv sync --group dev), not a silent install."
  - id: D7
    description: "LIVE (human-check): a topic-switch run (km turn -> defcon turn) shows cache_read_input_tokens > 0 on the switched turn -- the stable prefix survives a system[1] pack swap -- and the ack fires on the switch but not on a same-topic follow-up"
    verification: []
    human_judgment: true
    rationale: "Same pipecat-ai[evals,local] blocker as D6 above, plus a real ANTHROPIC_API_KEY-billed live run. Mechanism-level proof already exists for the SAME-topic case (07-01-SUMMARY.md's two-call cache-engagement test); the cross-topic variant specifically needs a full live audio pipeline run per this plan's own <verify> human-check bullet, which explicitly asked this to be documented rather than a new scenario file (coordination note: do not touch 07-01's kph_cache_verify.yaml). A human should run kv smoke / pipecat eval run with a km-then-defcon two-turn script against a running bot.py -t eval and inspect the harness JSON artifact's cache_read_input_tokens field on turn 2."

# Metrics
duration: 30min
completed: 2026-07-07
status: complete
---

# Phase 7 Plan 03: KPH Knowledge Base -- Full Primary Topic Set Summary

**Extends the Plan-01 km walking slice to all three of Kurt's headline systems: curated, advisory-lint-clean DEEP packs for defcon.run.34 and meshtk (each folding in verbatim Kurt-voiced transcript quotes), full topic-map/manifest entries, and router tests proving three-way discrimination plus the Pitfall-1 "toolkit" keyword-overlap guard -- all with zero changes to the Plan-01/02 loader, router, or retrieval code.**

## Performance

- **Duration:** ~30 min
- **Completed:** 2026-07-07
- **Tasks:** 2 (both `type="auto" tdd="true"`, no checkpoints)
- **Files modified:** 7 (2 modified pre-existing config files, 1 modified pre-existing test file, 4 new files)

## Accomplishments

- `knowledge/topics/defcon-run-34.md` and `knowledge/topics/meshtk.md`: curated, voice-friendly DEEP packs distilled from `corpus/defcon-run-34-digest.md` and `corpus/meshtk-digest.md`, each folding in 3-4 verbatim quotes lifted directly from Kurt's recorded transcripts (`right.literally.the.instance.clean.md`/`irsa.clean.md` for defcon; `alrighty.clean.md` for meshtk). Both packs are advisory-lint clean (`klanker_voice.knowledge.lint.advisory_lint` returns `[]` for each) and deliberately exclude every category the source digests flag as do-not-say -- secrets/secret plumbing, security gaps, WAF evasion specifics, easter-egg spoilers, registrant/PII, node keys/IDs/coordinates, and unreleased-feature specifics (spoken only as "coming soon").
- `knowledge/router/topic-map.yaml`: promoted both topics from a bare candidate comment to full entries -- hook + weighted keyword lists. meshtk's list deliberately omits a bare `"toolkit"` keyword (RESEARCH's own documented Pitfall-1 example: "toolkit" alone is ambiguous with a generic dev-tools question) so only distinctive multi-word forms count toward its score.
- `knowledge/manifest.yaml`: appended both topics with `pack:` references and per-source `note:` annotations marking their retrieval corpus as "doc-gen then index" (Amendment 3.D/5 -- their retrieval indexes are Plan 04's job, code indexed directly with no doc-gen step per the corrected Amendment 5). `tour_priority` now lists all three topics.
- `tests/test_knowledge_router.py`: 3 new keyless tests -- three-topic discrimination without collision (`test_classify_discriminates_all_three_primary_topics_without_collision`), the toolkit-overlap-guard fallback (`test_classify_bare_toolkit_overlap_resolves_to_fallback_via_floor`), and a real km-\>defcon topic switch proving the ack fires once and a same-topic follow-up does not re-ack (`test_topic_switch_fires_ack_then_same_topic_followup_does_not`).
- `scenarios/kph_knowledge_defconrun.yaml` and `scenarios/kph_knowledge_meshtk.yaml`: per-topic eval scenarios mirroring `kph_knowledge_km.yaml`'s shape (greeting-observe first turn, then a directed question judged by the shared `judge_factory` against a short expected-facts list, lenient close-STT-rendering acceptance).

## Task Commits

Each task was committed atomically:

1. **Task 1: defcon.run.34 + meshtk curated deep packs + topic-map/manifest promotion** - `0136fe8` (feat)
2. **Task 2: multi-topic router discrimination tests + per-topic eval scenarios** - `d6d3968` (feat)

_Note: TDD-flavored (`tdd="true"`) but one commit per task, matching 07-01/07-02's own precedent -- each task's tests were written and run to green before committing that task's content together._

## Files Created/Modified

- `apps/voice/knowledge/topics/defcon-run-34.md` - defcon.run.34 deep pack (block1 content)
- `apps/voice/knowledge/topics/meshtk.md` - meshtk deep pack (block1 content)
- `apps/voice/knowledge/router/topic-map.yaml` - full defcon-run-34 + meshtk entries (hook + weighted keywords)
- `apps/voice/knowledge/manifest.yaml` - defcon-run-34 + meshtk topics appended, tour_priority extended
- `apps/voice/tests/test_knowledge_router.py` - meshtk added to the TOPIC_MAP/knowledge_cfg test fixtures + 3 new tests (13 total in this file, up from 10)
- `apps/voice/scenarios/kph_knowledge_defconrun.yaml` - defcon.run.34 eval scenario
- `apps/voice/scenarios/kph_knowledge_meshtk.yaml` - meshtk eval scenario

## Decisions Made

- **Followed the real, already-shipped 07-01/07-02 architecture over this plan's own stale acceptance-criteria snippets** (Rule 1 - Plan text reconciliation, same class 07-02-SUMMARY.md already documented): the plan's literal acceptance-criteria shell commands ask for a `pack:` field *inside* `topic-map.yaml` itself and reference a `klanker_voice.knowledge.pack.load_topic_pack` function that has never existed -- 07-01 actually split concerns as manifest.yaml owns `pack:` path references (consumed by `prompt_assembly.load_topic_pack_text`) and topic-map.yaml is classification-only (hook + weighted keywords). Even klanker-maker's own topic-map entry has never had a `pack:` field, contradicting the plan's own "(previously hook-only)" framing for the OTHER two topics. Verified every acceptance criterion's real intent against the actual shipped API instead of the plan's non-functional literal snippet.
- **No "Landmines / do-not-say" section in either new pack**, unlike 07-01's `klanker-maker.md` precedent -- this plan's own `<prohibitions>` explicitly forbid it ("MUST NOT include any Landmines/do-not-say content from the defcon or meshtk digests in a curated pack"). Followed the explicit instruction: landmine avoidance is achieved purely by never writing the excluded categories into the pack text at all, confirmed by a zero-finding `advisory_lint()` pass on both files.
- **meshtk's keyword list omits a bare "toolkit" keyword** by design (Pitfall 1) -- RESEARCH's own worked example is exactly this ambiguity; only "mesh tk", "meshtk", "meshtastic toolkit", and "meshtastic" count toward meshtk's classification score.
- **Cross-topic cache-warmth check documented as a deferred human-check, not a new/edited scenario file** -- the plan's own action text explicitly says "do NOT edit files Plan 02 owns" and "prefer documenting the switch check as a human-check here" regarding `scenarios/kph_cache_verify.yaml` (07-01's file). Left that file untouched; the switch-specific check is captured in this plan's own `<verify>` human-check bullet and in coverage `D7` above.

## Deviations from Plan

### Auto-fixed Issues

None beyond the plan-text reconciliation already documented under Decisions Made above (that reconciliation required zero code changes -- both acceptance behaviors were verified against the real, already-correct API).

**Total deviations:** 0 code-affecting deviations. One documentation-only reconciliation (plan's stale acceptance-criteria snippets vs. the real shipped API), same class already established by 07-02.

## Live Verification

**Test suite:** `cd apps/voice && uv run pytest -q` -> **227 passed, 0 failed** (full suite, no flake this run -- the pre-existing `test_session.py` timing flake documented in 07-01/07-02-SUMMARY.md did not reproduce in either full-suite run performed during this plan's execution).

**Advisory lint (Amendment 3.E, non-blocking):**

```
knowledge/topics/defcon-run-34.md -> []  (zero findings)
knowledge/topics/meshtk.md       -> []  (zero findings)
```

Both hand-authored packs are clean -- no residual AWS-account-ID-shaped numbers, ARNs, key blocks, or internal/`.local` hostnames slipped in from the source digests.

**Loader resolution (all three primary topics):**

```python
load_topic_pack_text(load_knowledge_config(), 'defcon-run-34')  # non-empty
load_topic_pack_text(load_knowledge_config(), 'meshtk')          # non-empty
```

**Router discrimination + Pitfall-1 guard (keyless, no Anthropic call):**

- `classify("tell me about klanker maker", TOPIC_MAP)` -> `("klanker-maker", 5)`
- `classify("what is defcon run all about", TOPIC_MAP)` -> `("defcon-run-34", 6)`
- `classify("how does the meshtastic toolkit work", TOPIC_MAP)` -> `("meshtk", 5)`
- `classify("do you have any toolkit for that kind of thing", TOPIC_MAP)` -> `(None, 0)` -- below the floor, never a confident meshtk guess.

**Not exercised (deferred, not self-approved) -- same documented blocker as 07-01/07-02:**

1. The plan's `<human-check>` bullet for both new eval scenarios (`kph_knowledge_defconrun.yaml`, `kph_knowledge_meshtk.yaml`) via a full live audio pipeline run (`pipecat eval run` against a running `bot.py -t eval`) -- this venv's `pipecat-ai[evals,local]` (kokoro/moonshine) extras are still not installed. See coverage `D6`.
2. The plan's own cross-topic cache-warmth `<human-check>` bullet (a km-turn-then-defcon-turn run showing `cache_read_input_tokens > 0` on the switched turn, and the ack firing on the switch but not on a same-topic follow-up) -- same blocker, plus needs a real billed `ANTHROPIC_API_KEY` call through the full pipeline. See coverage `D7`. Mechanism-level proof for the SAME-topic case already exists (07-01-SUMMARY.md's direct two-call `build_system_blocks()` proof); the cross-topic variant is architecturally guaranteed by the same code path (block0 is never touched on any switch, per `prompt_assembly.py`'s own Pitfall-3 discipline) but has not been exercised live.

A human (or a follow-up session with the eval extras installed) should run:

```
uv run python bot.py -t eval   # in one terminal
uv run pipecat eval run scenarios/kph_knowledge_defconrun.yaml scenarios/kph_knowledge_meshtk.yaml --bot-url ws://localhost:7860
```

and confirm both new scenarios judge correct, plus separately script a km-turn-then-defcon-turn conversation against the live pipeline and inspect the harness JSON artifact's `cache_read_input_tokens` field on the second (defcon) turn.

## Issues Encountered

None beyond the plan-text reconciliation documented above (zero code impact).

## User Setup Required

None for the code itself -- both packs, the topic-map/manifest entries, and the router tests are fully keyless and offline. For the deferred live evals (coverage `D6`/`D7`): `uv sync --group dev` (or equivalent) to install `pipecat-ai[evals,local]` in this venv, plus the real `ANTHROPIC_API_KEY` already present in `apps/voice/.env`.

## Next Phase Readiness

- All three primary topics (klanker-maker, defcon-run-34, meshtk) now have curated packs, topic-map entries, and manifest entries -- the loader/router/retrieval code from 07-01/07-02 required zero changes, confirming their N-topic-ready design.
- `knowledge/manifest.yaml`'s per-source `note:` annotations mark defcon-run-34 and meshtk's retrieval corpus as "doc-gen then index" for **Plan 04's refresh workflow** to build `knowledge/index/{defcon-run-34,meshtk}/*.jsonl` (Amendment 3.D/5: index the real code directly -- `infra/terraform/` + `apps/` for defcon.run.34, `README.md` + `cmd/`/`internal/`/`pkg/`/`protos/` for meshtk -- no doc-gen step). Until those indexes exist, `RetrievalIndex` degrades gracefully to the curated pack alone (already proven in 07-02's own tests) -- both new packs work standalone today and gain retrieval depth automatically once Plan 04 runs.
- The cross-topic cache-warmth mechanism is architecturally sound (block0 is built once per session and never touched on any topic switch -- verified by `test_topic_switch_fires_ack_then_same_topic_followup_does_not`'s assertion that the pack stays swapped and by `prompt_assembly.py`'s own Pitfall-3 discipline) but the LIVE proof (a real `cache_read_input_tokens > 0` reading on a switched turn) remains an open item folded into the same `pipecat-ai[evals,local]` blocker every 07-0x plan has deferred.
- `tour_priority` in `manifest.yaml` now lists all three topics in launch order -- ready for any future tour-mode feature to re-pitch them in sequence.

---
*Phase: 07-kph-knowledge-base*
*Completed: 2026-07-07*

## Self-Check: PASSED

All 7 created/modified files verified present on disk; both task commit hashes (0136fe8, d6d3968) verified present in git log.
