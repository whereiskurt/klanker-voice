---
phase: quick-260727-ohq
plan: 01
subsystem: telephony
tags: [toml-config, asterisk-ari, ctf, dtmf, fail-closed]

requires:
  - phase: quick-260717-buf
    provides: "[telephony.cid_prefix_dids] CID-name-prefix -> dialed_did resolution (Approach C), the input this task's match guard consumes"
provides:
  - "Optional AnnouncementEntry.dids field for binding one [[telephony.announcement]] entry to specific dialed DIDs"
  - "Fail-closed _announcement_matches_did() guard in the DTMF code-match dispatch loop"
affects: [telephony-config, telephony-controller, ctf-announcement-entries]

tech-stack:
  added: []
  patterns:
    - "Per-entry DID scoping reuses the existing _parse_sms_dids normalizer (bare-digit, order-preserved, same ConfigError shape) rather than introducing a new parser"
    - "Fail-closed match guard as a pure module-level predicate (_announcement_matches_did), mirroring the existing _select_sms_send_dids shape"

key-files:
  created: []
  modified:
    - apps/voice/src/klanker_voice/telephony/config.py
    - apps/voice/src/klanker_voice/telephony/controller.py
    - apps/voice/configs/telephony.toml
    - apps/voice/tests/test_telephony_config.py
    - apps/voice/tests/test_telephony_controller.py

key-decisions:
  - "New OPTIONAL dids field on AnnouncementEntry; absent/empty stays GLOBAL (byte-identical to today), non-empty scopes the entry to those dialed DIDs"
  - "An unresolved dialed_did (CID-prefix/To: parse miss) matches ONLY global entries, never scoped ones -- fail closed, never guess"
  - "Dispatch registry stays code-keyed (_announcements_by_code); two per-game entries must resolve to DISTINCT code env var values regardless of DID scope -- documented in the class docstring and telephony.toml comments, not enforced in code (out of scope)"

requirements-completed: [QUICK-260727-OHQ]

coverage:
  - id: D1
    description: "AnnouncementEntry.dids field parses (absent/empty -> global, populated -> normalized tuple, scalar -> ConfigError) and the shipped telephony.toml live entry keeps parsing to dids == ()"
    requirement: QUICK-260727-OHQ
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_config.py::test_announcement_dids_absent_defaults_empty"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_config.py::test_announcement_dids_empty_array_defaults_empty"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_config.py::test_announcement_dids_parses_and_normalizes"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_config.py::test_announcement_dids_non_list_rejected"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_config.py::test_shipped_telephony_toml_announcement_dids_still_global"
        status: pass
    human_judgment: false
  - id: D2
    description: "Fail-closed per-DID dispatch guard: scoped entry fires only on its own resolved dialed DID; unresolved DID and other DIDs never dispatch a scoped entry; global entries dispatch unconditionally exactly as before"
    requirement: QUICK-260727-OHQ
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_controller.py::test_scoped_announcement_dispatches_on_matching_did"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_controller.py::test_scoped_announcement_does_not_dispatch_on_other_did"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_controller.py::test_scoped_announcement_does_not_dispatch_on_unresolved_did"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_controller.py::test_global_announcement_dispatches_regardless_of_did_resolution"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_controller.py::test_scoped_code_no_dispatch_but_global_code_still_dispatches"
        status: pass
    human_judgment: false

duration: 12min
completed: 2026-07-27
status: complete
---

# Quick Task 260727-ohq: Per-DID scoping for telephony announcements Summary

**Optional `dids` field on `[[telephony.announcement]]` entries, matched by a new fail-closed `_announcement_matches_did()` guard in the DTMF dispatch loop, so a second CTF game can live on 725-404-8283 without its code being redeemable elsewhere.**

## Performance

- **Duration:** ~12 min
- **Tasks:** 2/2 completed
- **Files modified:** 5

## Accomplishments
- `AnnouncementEntry.dids: tuple[str, ...] = ()` parses via the shared `_parse_sms_dids` normalizer (`field="dids"`) exactly like `sms_dids`/`sms_reply_dids`; absent/empty stays GLOBAL, populated normalizes to bare-digit DIDs (order preserved, junk dropped), a scalar raises `ConfigError`.
- `_announcement_matches_did(entry, dialed_did)` is the new fail-closed guard: a global entry (`dids == ()`) always matches; a scoped entry matches only when `dialed_did` is truthy AND in `entry.dids`.
- `on_channel_dtmf_received`'s code-match loop now `continue`s past a non-matching entry instead of dispatching unconditionally, while keeping the PIN's strict priority and suffix-match semantics unchanged.
- `configs/telephony.toml` documents the new field and the one-block-per-game layout; the live announcement entry's active TOML is untouched (a commented-out example line shows the shape) and still parses to `dids == ()`.

## Task Commits

1. **Task 1: optional `dids` field on AnnouncementEntry (config layer + TOML docs)** - `1931789` (feat)
2. **Task 2: fail-closed per-DID match guard in the announcement dispatch loop** - `77c7204` (feat)

_Both tasks followed TDD (tests written first, confirmed RED, then implementation turned them GREEN) within a single commit each — the plan's `tdd="true"` tasks were small enough that RED/GREEN landed together per task rather than as separate test/feat commits._

## Files Created/Modified
- `apps/voice/src/klanker_voice/telephony/config.py` - `AnnouncementEntry.dids` field + docstrings; `_parse_announcements` now parses `dids` via `_parse_sms_dids(..., field="dids")`
- `apps/voice/src/klanker_voice/telephony/controller.py` - new `_announcement_matches_did()` guard; dispatch loop skips non-matching entries; docstring updated
- `apps/voice/configs/telephony.toml` - documentation-only additions describing the new field and one-block-per-game layout; live entry behavior unchanged
- `apps/voice/tests/test_telephony_config.py` - 5 new tests for `dids` parsing
- `apps/voice/tests/test_telephony_controller.py` - 5 new tests for the dispatch guard

## Decisions Made
- Kept the registry code-keyed rather than adding DID-aware key composition — the plan explicitly scoped the second-game entry (new SSM code param + task-def env wiring) as a separate operator step, so no code change was needed to support it, only documentation of the "distinct code value per entry" constraint.
- Used `continue` (not `break`) in the dispatch loop so a non-matching scoped entry never short-circuits evaluation of other armed entries (proven by the "scoped code inert, global code still dispatches" test).

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None specific to this task. Running the FULL project test suite (`uv run pytest -q`, not part of this plan's own verification block) surfaced pre-existing, unrelated failures/errors in `tests/test_quota.py` (botocore/moto AWS-mocking errors), `tests/test_session.py`, and `tests/test_slot_leak.py` — none of these files were touched by this task and the failures are unrelated to telephony announcement dispatch (quota/session/heartbeat plumbing, likely a local moto/botocore version drift). Confirmed out of scope per the executor's scope-boundary rule; not fixed. The plan's own required verification block (telephony config/controller/lifecycle/sms/gate suites, 181 tests) is 100% green.

## User Setup Required

None - no external service configuration required. The actual second-game entry for 725-404-8283 (new SSM `code_env_var` value + task-def env wiring) remains a separate operator step, explicitly out of scope for this task (D-04).

## Next Phase Readiness

The `dids` field and its fail-closed dispatch guard are ready to use. An operator can now add a second `[[telephony.announcement]]` block scoped to `dids = ["7254048283"]` with its own `code_env_var`/`line_template`/`sms_reply_dids`, seed a new distinct SSM code value, and wire it into the telephony-edge task definition — no further code change required.

---
*Quick task: 260727-ohq*
*Completed: 2026-07-27*

## Self-Check: PASSED

All 5 modified/created source files and both task commit hashes (1931789, 77c7204) verified present.
