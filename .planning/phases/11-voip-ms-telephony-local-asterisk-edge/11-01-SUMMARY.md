---
phase: 11-voip-ms-telephony-local-asterisk-edge
plan: 01
subsystem: config
tags: [toml, pydantic-free-config, credential-rejection, telephony, asterisk, pytest]

# Dependency graph
requires:
  - phase: 10-voip-ms-telephony-offline-media-adapter
    provides: "telephony/ package (types.py Protocol, transport.py, media.py) and the deferred D-09 [telephony] config-loader stub"
provides:
  - "klanker_voice.telephony.config.TelephonyConfig frozen dataclass (media + §24 gate knobs)"
  - "klanker_voice.telephony.config.load_telephony_config() loader, optional [telephony] table, safe defaults"
  - "Widened klanker_voice.config._CREDENTIAL_FIELD_RE (pin/passphrase/pass_word/exact-words rejection, D-09)"
  - "Default [telephony] table in pipeline.toml (enabled=false)"
affects: [11-02, 11-03, 11-04, 11-05, 11-06, 11-07]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Independent config-section loader (mirrors QuotaConfig/KnowledgeConfig/DuplexConfig): load_telephony_config() reuses config._resolve_config_path + config._load_toml_data so every loader shares one credential gate on one file"
    - "Optional-table-with-safe-defaults pattern (mirrors DuplexConfig): missing [telephony] table returns TelephonyConfig() rather than raising"

key-files:
  created:
    - apps/voice/src/klanker_voice/telephony/config.py
    - apps/voice/tests/test_telephony_config.py
  modified:
    - apps/voice/src/klanker_voice/config.py
    - apps/voice/pipeline.toml

key-decisions:
  - "\"words\" matched as an exact whole field name (^words$) rather than via the shared (?:^|_)...(?:_|$) compound-boundary group used by the other credential stems -- a compound match would have false-positive-rejected the existing, legitimate [duplex] backchannel_words / max_backchannel_words fields; passphrase_words is still independently caught via the \"passphrase\" stem"
  - "Task 2's tdd=\"true\" RED/GREEN cycle authored the full test_telephony_config.py test file (valid-parse, missing-table default, invalid gate_mode, each allowed gate_mode, D-09 credential rejection) -- Task 3's own \"author the config tests\" step became a no-op beyond adding the pipeline.toml table, since both tasks target the same shared test file"

patterns-established:
  - "Config credential-stem widening: when adding a new stem to _CREDENTIAL_FIELD_RE, cross-check it against every existing TOML field name in pipeline.toml/configs/*.toml before committing -- a stem can silently reject legitimate non-secret config elsewhere in the document (the recursive rejection walks the whole file, not just the table being added)"

requirements-completed: [D-09]

coverage:
  - id: D1
    description: "config.py's credential-field rejection regex widened to refuse pin/passphrase/words/pass_word-shaped TOML field names before parse (D-09)"
    requirement: "D-09"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_config.py::test_credential_looking_telephony_field_rejected"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_config.py (full 35-test suite, no regression)"
        status: pass
    human_judgment: false
  - id: D2
    description: "TelephonyConfig frozen dataclass + load_telephony_config() loader: valid-table parse, missing-table default (enabled=False), gate_mode validation"
    requirement: "D-09"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_config.py::test_valid_telephony_table_parses"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_config.py::test_missing_telephony_table_defaults_to_disabled"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_config.py::test_invalid_gate_mode_rejected"
        status: pass
    human_judgment: false
  - id: D3
    description: "Default [telephony] table added to pipeline.toml with enabled=false -- WebRTC path byte-unaffected"
    requirement: "D-09"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_config.py::test_real_checked_in_pipeline_toml_telephony_table_round_trips"
        status: pass
      - kind: other
        ref: "grep -A17 '^\\[telephony\\]' apps/voice/pipeline.toml (manual acceptance-criteria check)"
        status: pass
    human_judgment: false

duration: 20min
completed: 2026-07-12
status: complete
---

# Phase 11 Plan 01: Telephony Config Loader + Widened Credential Regex Summary

**`TelephonyConfig`/`load_telephony_config()` (frozen dataclass, media + §24 gate knobs only) plus a widened `_CREDENTIAL_FIELD_RE` that refuses pin/passphrase/pass_word-shaped TOML fields before parse, with a real false-positive collision found and fixed against the existing `[duplex]` `backchannel_words` fields**

## Performance

- **Duration:** ~20 min
- **Started:** 2026-07-12T03:46:00Z
- **Completed:** 2026-07-12T03:50:35Z
- **Tasks:** 3 completed
- **Files modified:** 4 (2 created, 2 modified)

## Accomplishments

- Widened `klanker_voice.config._CREDENTIAL_FIELD_RE` to reject `pin`/`passphrase`/`pass_word`-shaped field names (compound-boundary match, same pattern as the existing `key`/`secret`/`token` stems) plus an exact `words` field name — so `TELEPHONY_ACCESS_PIN`/`TELEPHONY_PASSPHRASE_WORDS`-shaped TOML keys can never be silently accepted as tunables (D-09).
- Built `klanker_voice.telephony.config.TelephonyConfig` (frozen dataclass) + `load_telephony_config()` — behavior-only surface (media/transport/§24-gate knobs), no credential field ever present, reusing `config._resolve_config_path`/`config._load_toml_data` so it shares the exact same credential gate as `load_config`/`load_quota_config`/`load_knowledge_config`/`load_duplex_config` on the same file.
- Added the default `[telephony]` table to `pipeline.toml` (`enabled = false`, every D-09 key at its documented default) — the WebRTC-only default config load is byte-unaffected.
- 12 new tests in `test_telephony_config.py` (valid parse, missing-table default, invalid/valid `gate_mode` values, 4 credential-rejection parametrized cases, malformed-table type check); full suite 339 passed / 53 skipped, no regressions.

## Task Commits

Each task was committed atomically:

1. **Task 1: Widen the credential-field rejection regex (D-09)** — `10179f1` (feat)
2. **Task 2: TelephonyConfig dataclass + load_telephony_config loader** — `5699c4b` (test, RED) + `9c90c4c` (feat, GREEN)
3. **Task 3: Add the default [telephony] table to pipeline.toml + author the config tests** — `28d8c9c` (feat)

_Task 2 was `tdd="true"`: the test file was written and verified failing (`ModuleNotFoundError`) before the implementation existed, then the implementation was restored and all 12 tests verified passing._

## Files Created/Modified

- `apps/voice/src/klanker_voice/config.py` — widened `_CREDENTIAL_FIELD_RE` (Task 1)
- `apps/voice/src/klanker_voice/telephony/config.py` — new `TelephonyConfig` + `load_telephony_config()` (Task 2)
- `apps/voice/tests/test_telephony_config.py` — new test file, 12 tests (Task 2)
- `apps/voice/pipeline.toml` — new `[telephony]` table, `enabled = false` (Task 3)

## Decisions Made

- **"words" matched as an exact whole field name, not a compound suffix.** The plan's literal instruction ("keep the leading/trailing `_`-or-boundary anchors") would have made a bare `words` stem match `(?:^|_)words(?:_|$)`, which also matches the suffix of the *existing, legitimate* `[duplex]` fields `backchannel_words` and `max_backchannel_words` (a listening-cue lexicon and a word-count knob — not secrets). Running the widened regex against the full existing test suite caught this as a real regression (`test_real_checked_in_voice2_toml_label` failed on `configs/voice2.toml`'s `max_backchannel_words` field). Fixed by anchoring `words` as `^words$` (exact match only) in its own alternative, outside the shared compound-boundary group. `passphrase_words` is still independently rejected via the `passphrase` stem, which does use the compound-boundary group (no collision found for that stem). Verified with a standalone regex truth-table (13 cases) plus the full 339-test suite before and after.
- **Task 2's TDD test file also satisfied Task 3's "author the config tests" step.** Both tasks name `test_telephony_config.py` as their target; since Task 2 (`tdd="true"`) required a RED/GREEN cycle against that same file, the full test matrix (rejection + valid parse + invalid `gate_mode` + missing-table default) was authored there. Task 3's remaining scope was just the `pipeline.toml` table addition.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed a real credential-regex false positive against existing `[duplex]` config fields**
- **Found during:** Task 1 (widen `_CREDENTIAL_FIELD_RE`)
- **Issue:** The plan's literal regex-widening instruction, applied verbatim, rejected the checked-in `configs/voice2.toml`'s legitimate `max_backchannel_words = 3` field as "credential material" — running the full existing test suite immediately surfaced this as a failing test (`test_real_checked_in_voice2_toml_label`), not a hypothetical.
- **Fix:** Anchored the `words` credential stem to an exact whole-field-name match (`^words$`) instead of the shared compound-boundary group every other stem uses. `passphrase_words` (the actual D-09 threat shape) is still caught via the independent `passphrase` stem.
- **Files modified:** `apps/voice/src/klanker_voice/config.py`
- **Verification:** Standalone regex truth-table (13 cases: `access_pin`, `passphrase_words`, `words`, `pass_word`, `max_backchannel_words`, `backchannel_words`, `emitter_phrases`, `spinner`, `keywords`, `password`, `api_key`, `gate_mode`, `unlock_tier_id` — all matched expected outcome); full test suite 327/327 passed, 53 skipped, before Task 2/3 additions.
- **Committed in:** `10179f1` (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (1 bug fix)
**Impact on plan:** Necessary correctness fix, caught by running the existing suite exactly as the plan's own verification step prescribes. No scope creep — no new files, no architectural change, same commit as the task it belongs to.

## Issues Encountered

None beyond the regex collision documented above (which is the one deviation).

## User Setup Required

None — no external service configuration required. `TELEPHONY_ACCESS_PIN`/`TELEPHONY_PASSPHRASE_WORDS`/`ASTERISK_ARI_*` env vars are consumed by later plans in this phase (controller/entrypoint), not this one.

## Next Phase Readiness

- Every downstream Phase 11 plan (ARI controller, §24 gate processor, standalone entrypoint) can now call `load_telephony_config()` for a validated, frozen `TelephonyConfig` — the config surface this plan's `<objective>` promised is complete.
- The widened `_CREDENTIAL_FIELD_RE` is the structural D-09 guarantee later plans rely on: no plan in this phase should ever need to add PIN/passphrase values to `pipeline.toml` — the loader will refuse them at parse time.
- No blockers for 11-02 (next wave).

---
*Phase: 11-voip-ms-telephony-local-asterisk-edge*
*Completed: 2026-07-12*
