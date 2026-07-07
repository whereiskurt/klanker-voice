---
phase: 07-kph-knowledge-base
plan: 04
subsystem: ai
tags: [python, go, cobra, sqlite, fts5, yaml, anthropic, offline-tooling, knowledge-base]

# Dependency graph
requires:
  - phase: 07-kph-knowledge-base (07-01)
    provides: "klanker_voice.knowledge.lint.advisory_lint() (Plan-01, advisory do-not-say lint), knowledge/manifest.yaml schema"
  - phase: 07-kph-knowledge-base (07-02)
    provides: "klanker_voice.knowledge.retrieval.chunk_text() + RetrievalIndex (Plan-02, the FTS5 chunk-file consumer this plan's writer targets)"
  - phase: 07-kph-knowledge-base (07-03)
    provides: "manifest.yaml defcon-run-34/meshtk topic entries with retrieval-source notes this plan's refresh consumes"
provides:
  - "apps/voice/scripts/refresh_knowledge.py: offline D-07 refresh -- read_manifest (D-01/D-02 public:true gate), resolve_topic_sources (skip-missing warning), build_topic_chunks/write_chunk_file (Plan-02 chunk_text reuse, per-topic knowledge/index/{topic}/docs.jsonl), generate_docs (swappable doc-gen seam, defaults to Amendment-5 no-op), flag_landmines/write_pack (Plan-01 advisory_lint reuse, flags-never-blocks), distill_topic/survey_repo/style_pass (map-reduce curated-pack distillation, LLM call behind an injectable seam)"
  - "apps/voice/knowledge/manifest.yaml: every source entry now carries an explicit public:true flag (D-02's actual enforced field, not just a header comment)"
  - "make -C apps/voice knowledge (Makefile .PHONY target, primary home) + kv knowledge refresh (kv/internal/app/cmd/knowledge.go, thin exec.Command dispatcher, registered in root.go)"
  - "apps/voice/tests/test_knowledge_refresh.py: 20 keyless tests covering manifest-gating, public-refusal, skip-missing, chunk-writer output (loads through a real Plan-02 RetrievalIndex), advisory-flag-not-block, the doc-gen seam's Amendment-5 no-op default, and CLI arg parsing"
affects: [07-05-pacing-evals]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "os.walk with in-place dirnames pruning (node_modules/vendor/dist/build/target/coverage/site-packages + any dotdir) instead of Path.rglob for any offline survey over a real external repo checkout -- rglob has no way to skip a subtree early and will walk an entire dependency tree"
    - "Manifest source paths are REPO-ROOT relative when not absolute (confirmed by the manifest's own apps/voice/ and .planning/ prefixes), resolved via a base_dir parameter defaulting to REPO_ROOT -- never cwd-relative or manifest-file-relative"
    - "A chunk-file overwrite guard compares newly-built chunk count against the committed file's line count and skips (with a warning, not silently) when smaller -- protects a partially-available local checkout from clobbering a fuller committed corpus"
    - "Swappable seam kept even when its default behavior is a no-op (generate_docs/default_doc_generator) -- documents the Amendment 3.D->5 reversal in code, not just in planning docs, so a future refresh can re-enable a real generator without a pipeline change"

key-files:
  created:
    - apps/voice/scripts/refresh_knowledge.py
    - apps/voice/tests/test_knowledge_refresh.py
    - kv/internal/app/cmd/knowledge.go
  modified:
    - apps/voice/knowledge/manifest.yaml
    - apps/voice/Makefile
    - kv/internal/app/cmd/root.go

key-decisions:
  - "Amendment 5 reversal followed over the plan's own literal Task 2 text: DESIGN-NOTES.md's Amendment 5 ('corpus prep revised: direct code indexing, grill-with-docs DROPPED') and 07-RESEARCH.md's own Q1 resolution note ('SUPERSEDED... Corpus prep is DIRECT code indexing (Amendment 5; grill-with-docs dropped), not doc-gen') both independently confirm the already-committed manifest.yaml's 'indexed as code, no doc-gen step' notes are the shipped design of record -- generate_docs() defaults to a no-op, not a grill-with-docs shell-out, even though the plan's Task 2 action text and user_setup block still describe the pre-Amendment-5 doc-gen requirement"
  - "manifest.yaml sources needed an explicit public:true flag added (this plan's own files_modified scope already listed manifest.yaml) -- the schema had no real D-02 enforcement field, only a header comment claiming every source was 'hand-picked as public-safe'; the refresh script's D-02 gate checks the flag, not the comment"
  - "os.walk + directory-name pruning instead of Path.rglob for iter_source_files -- found live: one real manifest source (defcon.run.34/apps) has 443,982 files under node_modules across ~10 sub-apps; an unbounded rglob effectively hangs a real refresh run"
  - "Relative manifest source paths resolve against REPO_ROOT (APP_ROOT.parent.parent), not cwd or the manifest file's own directory -- found live: manifest.yaml's real relative paths (apps/voice/knowledge/..., .planning/phases/...) only make sense as repo-root-relative, confirmed by their own prefixes; without this fix every in-repo source falsely reported 'not found'"
  - "write_chunk_file() never overwrites an existing committed chunk file with a smaller newly-built corpus (a --force override exists) -- an additional, non-test-execution-scoped extension of the plan's own T-07-05 destructive-regenerate threat mitigation, since a partially-missing local checkout on a real refresh run is exactly the scenario that would otherwise silently shrink a committed index"
  - "distill_topic()/survey_repo()/style_pass() (the curated-pack distillation map-reduce pass) are real, LLM-call-behind-a-seam functions, not stubs -- but were never exercised with a live Anthropic call in this execution session per the plan's own explicit instruction not to run a live/paid refresh"

requirements-completed: [PIPE-10, PIPE-07]

coverage:
  - id: D1
    description: "refresh_knowledge.py reads knowledge/manifest.yaml as the ONLY source list (D-01) and refuses any source not flagged public:true (D-02), never crashing on a refusal -- recorded in a report instead"
    requirement: "PIPE-10"
    verification:
      - kind: unit
        ref: "tests/test_knowledge_refresh.py::test_read_manifest_only_includes_public_sources"
        status: pass
      - kind: unit
        ref: "tests/test_knowledge_refresh.py::test_manifest_public_false_is_refused"
        status: pass
      - kind: unit
        ref: "tests/test_knowledge_refresh.py::test_manifest_missing_public_flag_is_refused"
        status: pass
      - kind: unit
        ref: "tests/test_knowledge_refresh.py::test_collect_source_text_never_opens_a_path_outside_the_manifest_source"
        status: pass
    human_judgment: false
  - id: D2
    description: "A missing local repo checkout is skipped with a clear warning, not a hard failure -- the run continues past it"
    requirement: "PIPE-10"
    verification:
      - kind: unit
        ref: "tests/test_knowledge_refresh.py::test_resolve_topic_sources_skips_missing_path_with_warning"
        status: pass
      - kind: unit
        ref: "tests/test_knowledge_refresh.py::test_resolve_topic_sources_all_missing_returns_empty_not_an_exception"
        status: pass
    human_judgment: false
  - id: D3
    description: "The chunk-writer produces per-topic knowledge/index/{topic}/*.jsonl files (text/source_path/heading JSONL, Plan-02's chunk_text reused verbatim) whose output loads through a real Plan-02 RetrievalIndex and returns results for a known term"
    requirement: "PIPE-10"
    verification:
      - kind: unit
        ref: "tests/test_knowledge_refresh.py::test_build_topic_chunks_uses_plan02_chunk_text"
        status: pass
      - kind: unit
        ref: "tests/test_knowledge_refresh.py::test_write_chunk_file_produces_parseable_jsonl_with_required_keys"
        status: pass
      - kind: unit
        ref: "tests/test_knowledge_refresh.py::test_chunk_writer_output_loads_through_plan02_retrieval_index"
        status: pass
    human_judgment: false
  - id: D4
    description: "The advisory do-not-say lint (Plan-01's advisory_lint, reused verbatim) FLAGS findings into the refresh report but NEVER blocks or refuses the write (Amendment 3.E)"
    requirement: "PIPE-10"
    verification:
      - kind: unit
        ref: "tests/test_knowledge_refresh.py::test_advisory_flag_never_blocks_the_write"
        status: pass
      - kind: unit
        ref: "tests/test_knowledge_refresh.py::test_flag_landmines_on_clean_text_returns_no_findings"
        status: pass
    human_judgment: false
  - id: D5
    description: "The doc-generation seam (generate_docs) is swappable but defaults to a no-op (Amendment 5's grill-with-docs-dropped reversal) -- a code source with the default generator indexes its raw text directly, never crashes, never requires an external skill"
    requirement: "PIPE-10"
    verification:
      - kind: unit
        ref: "tests/test_knowledge_refresh.py::test_generate_docs_default_generator_is_a_noop_amendment_5"
        status: pass
      - kind: unit
        ref: "tests/test_knowledge_refresh.py::test_generate_docs_swappable_seam_accepts_a_custom_generator"
        status: pass
      - kind: unit
        ref: "tests/test_knowledge_refresh.py::test_generate_docs_never_raises_even_if_generator_fails"
        status: pass
      - kind: unit
        ref: "tests/test_knowledge_refresh.py::test_build_topic_chunks_for_code_source_falls_back_to_raw_code_when_no_generator"
        status: pass
    human_judgment: false
  - id: D6
    description: "make -C apps/voice knowledge and kv knowledge refresh both invoke the refresh script (kv as a thin exec.Command dispatcher, never reimplementing distillation/doc-gen/indexing in Go); kv builds and its existing test suite still passes"
    requirement: "PIPE-10"
    verification:
      - kind: automated_ui
        ref: "make -C apps/voice -n knowledge | grep refresh_knowledge.py"
        status: pass
      - kind: unit
        ref: "cd kv && go build ./... && go test ./..."
        status: pass
      - kind: other
        ref: "cd kv && go run ./cmd/kv knowledge refresh --help"
        status: pass
    human_judgment: false
  - id: D7
    description: "A real, deliberate `make -C apps/voice knowledge` / `kv knowledge refresh` run (with ANTHROPIC_API_KEY set and full local klankrmkr/defcon.run.34/meshtk checkouts present) actually regenerates curated packs + retrieval indexes end-to-end and produces a clean, reviewable git diff (D-09)"
    verification: []
    human_judgment: true
    rationale: "The plan explicitly forbids running a live refresh that hits paid Anthropic APIs during this execution session; the pipeline is proven correct against tmp_path fixtures (D1-D6) plus two real bugs found and fixed via a --dry-run/--out-dir run against this machine's real external checkouts (node_modules-choking rglob, cwd-relative path resolution -- see Deviations). The distillation LLM pass (distill_topic/survey_repo/style_pass) itself was never exercised live. A human must run `make -C apps/voice knowledge` (or `kv knowledge refresh`) for real once and review the resulting git diff per D-09 before treating the curated-pack-regeneration half of this plan as fully proven end-to-end."

# Metrics
duration: 70min
completed: 2026-07-07
status: complete
---

# Phase 7 Plan 04: Offline Knowledge Refresh -- Distill + Chunk/Index Build + Advisory-Flag Summary

**`apps/voice/scripts/refresh_knowledge.py` -- a manifest-gated (D-01/D-02), offline (Amendment 3.G) refresh that reuses Plan-01's `advisory_lint()` and Plan-02's `chunk_text()` verbatim to rebuild per-topic FTS5 retrieval chunk files and curated packs, flags do-not-say findings without ever blocking a write (Amendment 3.E), defaults its doc-generation seam to Amendment 5's direct-code-indexing no-op (grill-with-docs dropped), and is exposed via both `make -C apps/voice knowledge` and a thin `kv knowledge refresh` Go dispatcher.**

## Performance

- **Duration:** ~70 min
- **Completed:** 2026-07-07
- **Tasks:** 3 (Task 1/2 `type="auto" tdd="true"`, Task 3 `type="auto"`; no checkpoints)
- **Files modified:** 6 (3 new files, 3 modified files)

## Accomplishments

- `apps/voice/scripts/refresh_knowledge.py`: mirrors `audition.py`'s shape (`APP_ROOT` anchor, `.env` load, hard `sys.exit(1)` with `make -C apps/voice env` guidance). `read_manifest()`/`parse_manifest()` implement D-01 (manifest is the only source list) and D-02 (any source missing an explicit `public: true` flag is excluded and recorded in a `RefusedSource` list, never crashes). `resolve_topic_sources()` skips a missing local checkout with a warning and continues (Environment Availability fallback). `build_topic_chunks()`/`write_chunk_file()` reuse Plan-02's `chunk_text()` verbatim and write `knowledge/index/{topic}/docs.jsonl` in the exact JSONL shape (`text`/`source_path`/`heading`) `RetrievalIndex` already loads -- proven by a real dry-run test building a `RetrievalIndex` against freshly-written chunk files and querying it successfully. `flag_landmines()`/`write_pack()` reuse Plan-01's `advisory_lint()` verbatim and FLAG findings into a report while ALWAYS still writing the output (Amendment 3.E, proven by a test whose landmine text is built at runtime via digit-concatenation so it never appears as a literal token in the test's own source).
- **The swappable doc-generation seam, Amendment 5-faithful:** `generate_docs()`/`default_doc_generator()` default to a no-op (`None`) rather than shelling out to Matt Pocock's `grill-with-docs` skill. This deliberately reverses the plan's own Task 2 action text and `user_setup` block (which still describe the pre-Amendment-5 doc-gen requirement) -- see Deviations for the full reconciliation against DESIGN-NOTES.md's Amendment 5 and 07-RESEARCH.md's own superseding note. The seam itself is kept (a custom `generator` callable can still be injected) so a real generator can be re-enabled later without touching the pipeline.
- `distill_topic()`/`survey_repo()`/`style_pass()`: the map-reduce curated-pack distillation pass (per-repo survey -> per-topic distill -> style pass, RESEARCH Pattern 3), with the LLM call behind an injectable `llm_call` seam so tests never touch the network; the default implementation constructs a real Anthropic client the same `_require_env`/`Settings` way `factories.py`/`judge.py` already do.
- `apps/voice/knowledge/manifest.yaml`: every source entry across all three topics now carries an explicit `public: true` flag -- the schema previously had no real D-02 enforcement mechanism, only a header comment claiming every source was "hand-picked as public-safe."
- `apps/voice/Makefile` gains a `.PHONY: knowledge` target (`## knowledge: regenerate curated packs + retrieval indexes from the corpus (D-07)`, body `uv run python scripts/refresh_knowledge.py`) -- the same one-line-doc, pure-shell-out convention as `env:`/`greetings:`.
- `kv/internal/app/cmd/knowledge.go`: `NewKnowledgeCmd` (tier.go's `Use`/`Short`/`RunE` cobra shape) adds a `knowledge` parent command with a `refresh` subcommand that resolves the repo root via `git rev-parse --show-toplevel`, locates `apps/voice/scripts/refresh_knowledge.py`, and shells to `uv run python scripts/refresh_knowledge.py` (forwarding `--dry-run`/`--skip-distill`/`--force`) -- registered in `root.go` alongside `tier`/`code`/`smoke`/`usage`/`killswitch`.
- `apps/voice/tests/test_knowledge_refresh.py`: 20 keyless, offline tests (manifest-gating, public-refusal, skip-missing, chunk-writer + real-`RetrievalIndex` round-trip, advisory-flag-not-block, the doc-gen seam's Amendment-5 default + swap-in + never-raises behaviors, code-source raw-fallback, CLI arg parsing).
- **Two real, live-verified bug fixes found via manual `--dry-run --out-dir` verification against this machine's actual external checkouts** (not covered by the tmp_path-fixture RED tests): an unbounded `Path.rglob` that would hang on `defcon.run.34/apps`'s real 443,982-file `node_modules` tree (switched to `os.walk` with directory-name pruning), and manifest relative paths resolving against the wrong base directory (switched to a `REPO_ROOT`-anchored resolution). See Deviations.

## Task Commits

Each task was committed atomically:

1. **Task 1: Wave-0 failing refresh-pipeline tests + manifest `public:true` gate** - `877228b` (test)
2. **Task 2: `refresh_knowledge.py` -- distill + swappable doc-gen + chunk/index build + advisory-flag** - `59c687c` (feat)
3. **Task 3: Operator command homes -- Make target + `kv knowledge` dispatcher** - `73129bf` (feat)

_Note: Task 1/2 are TDD-flavored (`tdd="true"`) but structured as one commit per task, matching 07-01/02/03's own precedent -- Task 1's commit is the RED state (confirmed failing with `ModuleNotFoundError` before Task 2 existed); Task 2's commit is the GREEN state (all 20 tests pass, plus the two live bug fixes discovered and folded into the same commit before it landed)._

## Files Created/Modified

- `apps/voice/scripts/refresh_knowledge.py` - the D-07 offline refresh: manifest read/gate, source collection, chunk build + FTS5-index write, advisory-flag, swappable doc-gen seam, distillation map-reduce, CLI
- `apps/voice/tests/test_knowledge_refresh.py` - 20 keyless tests exercising every refresh helper (not Plan-01/02's own already-tested `advisory_lint`/`chunk_text`)
- `apps/voice/knowledge/manifest.yaml` - added `public: true` to every existing source entry (D-02 enforcement field)
- `apps/voice/Makefile` - new `.PHONY: knowledge` target
- `kv/internal/app/cmd/knowledge.go` - `NewKnowledgeCmd` (knowledge/refresh cobra command, thin `exec.Command` dispatcher)
- `kv/internal/app/cmd/root.go` - registers `NewKnowledgeCmd(cfg)` alongside the other subcommands

## Decisions Made

- **Amendment 5 followed over the plan's own literal Task 2 text.** DESIGN-NOTES.md's Amendment 5 ("corpus prep revised: direct code indexing, grill-with-docs DROPPED") and 07-RESEARCH.md's own Q1 resolution note ("SUPERSEDED by DESIGN-NOTES Amendments 3-5... Corpus prep is DIRECT code indexing (Amendment 5; grill-with-docs dropped), not doc-gen") both independently confirm the already-committed `manifest.yaml`'s "indexed as code, no doc-gen step" source notes are the shipped design of record, not merely a proposal superseded by a stale plan draft. `generate_docs()` therefore defaults to a no-op even though the plan's own Task 2 action text and `user_setup` block still describe the pre-Amendment-5 grill-with-docs requirement. The seam is kept swappable (per the plan's own "keep it swappable" framing) so a real generator can be re-enabled later without a pipeline change.
- **`public: true` added to every manifest.yaml source entry** -- this plan's own `files_modified` frontmatter already scoped `manifest.yaml` for editing. The prior schema had no field D-02's gate could actually check (only a header comment). Missing this would have made D-02 either unenforceable or would have refused every real source in the manifest.
- **`os.walk` with directory-name pruning, not `Path.rglob`** -- found live during manual dry-run verification: one real manifest source (`defcon.run.34/apps`) has 443,982 files under `node_modules` across ~10 sub-apps (`run.gpx`, `run.auth`, `run.bib`, etc.). An unbounded `rglob("*")` has no way to skip a subtree early and effectively hangs a real refresh run. Fixed by pruning `node_modules`/`vendor`/`dist`/`build`/`target`/`coverage`/`site-packages` and any dotdir before descending.
- **Relative manifest source paths resolve against `REPO_ROOT`** (`APP_ROOT.parent.parent`), not cwd or the manifest file's own directory -- found live: `manifest.yaml`'s real relative paths (`apps/voice/knowledge/...`, `.planning/phases/...`) only make sense as repo-root-relative (confirmed by their own path prefixes). Before the fix, every in-repo transcript/diagram/digest source falsely reported "not found."
- **`write_chunk_file()`'s overwrite guard** -- a newly-built corpus with fewer chunks than an already-committed index file is not silently overwritten (a `--force` flag exists to override); this extends the plan's own T-07-05 destructive-regenerate threat mitigation (which the plan scoped to test execution) to the real-run case, since a partially-available local checkout is exactly the scenario that would otherwise quietly shrink a committed index.
- **`distill_topic`/`survey_repo`/`style_pass` are real functions, not stubs**, with the LLM call behind an injectable seam -- built to the plan's full map-reduce spec (RESEARCH Pattern 3) but never exercised with a live Anthropic call in this session, per the plan's own explicit instruction not to run a live/paid refresh during execution.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] `iter_source_files` used an unbounded `Path.rglob`, hanging on a real 443,982-file `node_modules` tree**
- **Found during:** Task 2, manual `--dry-run --out-dir` verification against this machine's real `knowledge/manifest.yaml` sources (not part of the RED test suite, which uses `tmp_path` fixtures only)
- **Issue:** `defcon.run.34/apps` (a real manifest source, `skip_if_missing: true`) contains ~10 sub-apps each with their own `node_modules`, totaling 443,982 files. `rglob("*")` cannot skip a directory subtree early, so the walk (plus reading/chunking every matched file) took long enough to blow past a 2-3 minute tool timeout in this sandboxed environment.
- **Fix:** Rewrote `iter_source_files` to use `os.walk` with in-place `dirnames` pruning, excluding `node_modules`/`vendor`/`dist`/`build`/`target`/`coverage`/`site-packages` and any dotdir before descending.
- **Files modified:** `apps/voice/scripts/refresh_knowledge.py`
- **Verification:** `tests/test_knowledge_refresh.py` (20/20) still pass; a subsequent dry-run against the real manifest (after also fixing Deviation 2) completed in well under a second and produced sane chunk counts (16,552 chunks for defcon-run-34 after pruning, vs. an earlier partial 3.7M-chunk / 3.9GB artifact from the unfixed code that was writing minified vendor JS content wholesale).
- **Committed in:** `59c687c` (Task 2 commit)

**2. [Rule 1 - Bug] Relative manifest source paths resolved against the wrong base directory**
- **Found during:** Task 2, the same manual dry-run verification pass
- **Issue:** `manifest.yaml`'s in-repo relative paths (e.g. `apps/voice/knowledge/diagrams/km-sandbox-aws.md`, `.planning/phases/07-kph-knowledge-base/corpus/km-digest.md`) are written repo-root-relative (confirmed by their own `apps/voice/`/`.planning/` prefixes -- these paths only make sense from the repo root, since the manifest file itself already lives inside `apps/voice/knowledge/`). The initial implementation left `Source.path` exactly as parsed, which resolved against the script's cwd (`apps/voice/` in the normal invocation) -- silently reporting every one of these real, existing files as "not found."
- **Fix:** Added `REPO_ROOT = APP_ROOT.parent.parent` and a `base_dir` parameter to `parse_manifest()`/`read_manifest()` (default `REPO_ROOT`) that resolves any relative source path against it via `_resolve_source_path()`. Absolute manifest paths (the sibling local checkouts like `/Users/khundeck/working/klankrmkr/docs`) are used verbatim, unaffected.
- **Files modified:** `apps/voice/scripts/refresh_knowledge.py`, `apps/voice/tests/test_knowledge_refresh.py` (updated one existing test's expectation to an explicit `base_dir=tmp_path` + added a new test pinning the `REPO_ROOT`-default behavior against a real checked-in file)
- **Verification:** `tests/test_knowledge_refresh.py::test_read_manifest_resolves_relative_paths_against_repo_root_by_default` asserts the resolved path against a real file (`apps/voice/knowledge/style/kurt-voice.md`) and that it `.is_file()`; full suite 20/20 (then 247/247 project-wide) pass.
- **Committed in:** `59c687c` (Task 2 commit)

### Plan-Text Reconciliation (no code change beyond what's already documented above)

**3. [Rule 1 - Plan/reality drift, same class as 07-02/07-03] Task 2's doc-gen requirement + `user_setup` (grill-with-docs) reflects the PRE-Amendment-5 design**
- **Found during:** Reading the plan's Task 2 action text and `user_setup` block against `.planning/phases/07-kph-knowledge-base/07-DESIGN-NOTES.md` and `07-RESEARCH.md` before implementing
- **Issue:** The plan's own frontmatter (`user_setup`) and Task 2 action text describe installing and shelling out to Matt Pocock's `grill-with-docs` skill as the default doc-generation behavior for defcon.run.34/meshtk (Amendment 3.D, as it stood 2026-07-06). DESIGN-NOTES.md's Amendment 5 (dated 2026-07-07, one day later) explicitly reverses this: "corpus prep revised: direct code indexing, grill-with-docs DROPPED." 07-RESEARCH.md's own Q1 resolution note independently confirms: "Corpus prep is DIRECT code indexing (Amendment 5; grill-with-docs dropped), not doc-gen." The already-committed `knowledge/manifest.yaml` (07-03's own output) already carries source notes saying "indexed as code, no doc-gen step" for every defcon/meshtk code source -- meaning 07-03 itself was authored consistent with Amendment 5, while this plan's (07-04) own text was not regenerated to match.
- **Fix:** Implemented `generate_docs()`/`default_doc_generator()` to default to a no-op (Amendment 5's actual behavior), keeping the function as a genuinely swappable seam (a custom `generator` callable is still accepted) so the plan's "keep it swappable" framing is honored even though the DEFAULT is now Amendment 5's choice, not Amendment 3.D's original one. Did NOT require the operator to install `grill-with-docs` (the plan's `user_setup` block is treated as superseded).
- **Files modified:** none beyond what's already listed in Deviation 1/2 (this is a design-intent reconciliation, not a separate code change)
- **Committed in:** `59c687c` (Task 2 commit, module docstring documents the full reconciliation at length)

**4. [Rule 1 - Plan-text reconciliation, same class as 07-02/07-03] Task 2's literal import snippet references a non-existent `klanker_voice.knowledge.scrub.lint`**
- **Found during:** Task 2, before writing imports
- **Issue:** The plan's action text and `<verify>` shell snippet say `from klanker_voice.knowledge.scrub import lint`. That module/function has never existed -- 07-01 shipped it as `klanker_voice.knowledge.lint.advisory_lint()` (confirmed by reading the real, already-committed `lint.py`).
- **Fix:** Imported and used the real, already-shipped names (`from klanker_voice.knowledge.lint import advisory_lint`) throughout `refresh_knowledge.py` and its tests. Verified the plan's literal `<verify>` command's intent using the real module path instead.
- **Files modified:** none beyond the already-listed files (verification-only, same as 07-02/07-03's precedent)
- **Committed in:** `59c687c` (Task 2 commit)

**5. [Rule 1 - Plan-text reconciliation] Task 3's literal `<verify>` snippet (`cd kv && go run . knowledge refresh --help`) targets the wrong package**
- **Found during:** Task 3, running the plan's literal verify command
- **Issue:** `go run .` from the `kv/` module root fails with "no Go files in .../kv" -- the module's `main()` lives at `kv/cmd/kv/main.go`, not the module root (confirmed by `grep -rln "func main"`).
- **Fix:** Verified the same intent via `go run ./cmd/kv knowledge refresh --help` (exits 0, describes the shell-out) instead of the plan's literal (non-functional in this repo layout) invocation.
- **Files modified:** none (verification-only)
- **Committed in:** `73129bf` (Task 3 commit)

---

**Total deviations:** 2 auto-fixed Rule-1 bugs (both found via manual dry-run verification against real external checkouts, outside the RED test suite's tmp_path scope) + 3 Rule-1 plan-text/design reconciliations (Amendment 5's doc-gen reversal, the `scrub.lint` stale import, and the `go run .` module-path mismatch).
**Impact on plan:** Both bug fixes were necessary for correctness -- without them, a real operator run of `make -C apps/voice knowledge` on Kurt's actual machine would either hang indefinitely (rglob over `node_modules`) or silently skip every real in-repo source (wrong path base), defeating the plan's own goal of making the refresh "a script run, never a risky manual edit." All three reconciliations kept implementation faithful to the actual, most-recent design of record (DESIGN-NOTES.md Amendment 5, the real shipped 07-01 module names, and the real kv module layout) rather than stale/superseded plan text. No scope creep -- all fixes stayed within this plan's declared files.

## Issues Encountered

One environment interaction, not a code bug: manually exercising `--dry-run --out-dir` against this machine's real external checkouts (`/Users/khundeck/working/{klankrmkr,defcon.run.34,meshtk}`) is outside this plan's actual scope (the plan explicitly calls for tmp_path/fixture-based testing, not a real run against external directories) and triggered a sandbox interaction ("THINK: Are you sure?" style prompt) plus tool-timeout kills mid-run in this restricted agent environment when scanning hundreds of thousands of files. This is expected -- those checkouts exist on Kurt's own machine for his own deliberate refresh runs, not for exercise inside this sandboxed executor. The two real bugs it surfaced (Deviations 1/2) were fixed and are now covered by fast, tmp_path-scoped regression tests; no further attempts were made to run the script against the real external checkouts from within this session.

## User Setup Required

**The plan's own `user_setup` block (installing `grill-with-docs` via `npx skills add mattpocock/skills --skill=grill-with-docs`) is superseded by Amendment 5** -- see Decisions/Deviation 3. No operator action is required for that.

What genuinely remains for a human, before treating this plan's curated-pack-regeneration half as proven end-to-end (see coverage D7): run `make -C apps/voice knowledge` (or `kv knowledge refresh`) for real, with `ANTHROPIC_API_KEY` set and the local `klankrmkr`/`defcon.run.34`/`meshtk` checkouts present, and review the resulting `knowledge/` git diff per D-09 before committing it. This was not self-approved here -- it is a deliberate, billed, human-reviewed act by design (D-07/Amendment 3.G), and running it live during this execution session was explicitly out of scope.

## Next Phase Readiness

- The refresh pipeline (manifest-gate -> skip-missing -> chunk/index-build -> advisory-flag) is fully proven against fixtures and is safe to run for real: it never opens a path outside the manifest, degrades gracefully on missing checkouts, never silently shrinks a committed chunk index, and never blocks on an advisory-lint finding.
- 07-05 (pacing/evals) can build on this plan's chunk-file format and manifest schema unchanged -- no code from this plan needs to change for the next plan's work.
- **Open, not blocking:** a real, live `make -C apps/voice knowledge` run (with full local checkouts + a billed Anthropic call for the distillation pass) has not been executed -- flagged in coverage `D7` as a human-judgment item, consistent with every prior 07-0x plan's deferred-live-run pattern (07-01's cache/eval runs, 07-02's retrieval eval, 07-03's scenario evals all share the same `pipecat-ai[evals,local]`-class deferral; this plan's deferral is instead "don't spend real Anthropic dollars/don't clobber real content during automated execution").
- Full project test suite: 247/247 Python tests pass (227 prior + 20 new, zero flake this run); `kv` Go module builds clean and its existing test suite (`internal/app/cmd`, `internal/app/electro`) still passes with the new `knowledge.go` file added.

---
*Phase: 07-kph-knowledge-base*
*Completed: 2026-07-07*

## Self-Check: PASSED

All 7 created/modified files verified present on disk; all 3 task commit hashes (877228b, 59c687c, 73129bf) verified present in git log.
