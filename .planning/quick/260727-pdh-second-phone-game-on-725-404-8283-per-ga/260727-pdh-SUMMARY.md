---
phase: quick-260727-pdh
plan: 01
subsystem: telephony
tags: [pipecat, asterisk, ari, toml, go, kv-cli, terraform, ecs, ssm]

requires:
  - phase: quick-260727-ohq
    provides: the optional per-entry `dids` field on AnnouncementEntry (unused until this task)
provides:
  - An optional per-entry `words_env_var` spoken-trigger field on AnnouncementEntry
  - A GateProcessor -> controller either-factor spoken-trigger seam (opaque key only, D-06/D-07 redaction preserved)
  - Three per-DID phone-game entries in telephony.toml (3234 / 3283 / 8283), each with its own numeric code and the 8283 entry's optional spoken trigger
  - service.hcl SSM valueFrom wiring for four per-DID announcement secrets
  - A shared Go TOML announcement-game parser (studio.ParseTelephonyGames/AnnotateGameEnv) consumed by both `kv telephony list` and `kv studio`
affects: [telephony-edge, kv-cli, kv-studio-console]

tech-stack:
  added: []
  patterns:
    - "Either-factor gate resolution: GateProcessor resolves via cancel_for_takeover() synchronously, THEN spawns the dispatch callback via asyncio.create_task — never awaited inline, so a slow downstream action can never stall the frame queue."
    - "Opaque non-secret dispatch keys: cross-module callbacks (GateProcessor -> controller, Go TOML parser -> both kv surfaces) pass a stable NAME (code_env_var / env var name) as the correlation key, never a resolved secret value."
    - "Deploy-safe-but-inert SSM wiring: a literal sentinel value (never an absent parameter) lets a new valueFrom wire go live without breaking ECS task launch, while the application layer treats the sentinel as 'disabled'."

key-files:
  created: []
  modified:
    - apps/voice/src/klanker_voice/telephony/config.py
    - apps/voice/src/klanker_voice/telephony/gate.py
    - apps/voice/src/klanker_voice/telephony/controller.py
    - apps/voice/configs/telephony.toml
    - infra/terraform/live/site/services/telephony-edge/service.hcl
    - kv/internal/app/studio/types.go
    - kv/internal/app/studio/repofile_adapter.go
    - kv/internal/app/studio/view.go
    - kv/internal/app/studio/server.go
    - kv/internal/app/studio/web/index.html
    - kv/internal/app/studio/web/app.js
    - kv/internal/app/cmd/telephony.go

key-decisions:
  - "words_env_var (not passphrase_env_var): the D-09 credential-field regex refuses any TOML key containing a `passphrase` token, so the field is named words_env_var — same precedent as the existing otp_env_var rename."
  - "The registry that gates the spoken factor is keyed by each entry's code_env_var NAME (an opaque, non-secret, stable handle), never by the resolved code or words VALUE."
  - "The __unset__ sentinel is a real (non-empty) SSM value, not an absent parameter — this is what lets the four-secret service.hcl wiring deploy safely while the 8283 spoken trigger stays inert until an operator replaces it."

requirements-completed: [QUICK-260727-PDH]

coverage:
  - id: D1
    description: "AnnouncementEntry gains an optional NAME-only words_env_var field; a passphrase_env_var key is refused by the shared credential gate"
    requirement: QUICK-260727-PDH
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_config.py::test_announcement_words_env_var_parses_and_strips, test_announcement_passphrase_env_var_key_rejected"
        status: pass
    human_judgment: false
  - id: D2
    description: "GateProcessor accumulates spoken tokens on OTP-only DIDs (D-06), matches via match_passphrase, resolves via cancel_for_takeover before spawning the callback exactly once, with zero pre-unlock frame/log leakage"
    requirement: QUICK-260727-PDH
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_gate.py::test_announcement_words_accumulate_and_match_when_concierge_disabled, test_announcement_words_match_cancels_fail_closed_timer, test_announcement_words_callback_fires_at_most_once, test_announcement_words_redaction_zero_frames_and_no_secret_in_logs"
        status: pass
    human_judgment: false
  - id: D3
    description: "Controller builds a code_env_var-keyed words registry (skipping all 4 disabled states gracefully), DID-filters it per call, and dispatches into the existing _gate_announcement"
    requirement: QUICK-260727-PDH
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_controller.py::test_words_registry_skips_four_disabled_states_numeric_trigger_unaffected, test_words_registry_sentinel_substring_not_treated_as_sentinel, test_words_registry_well_formed_arms_and_dispatches_via_gate_announcement, test_words_registry_did_filtered_scoped_entry_armed_only_on_own_did, test_words_registry_global_entry_armed_regardless_of_did_resolution"
        status: pass
    human_judgment: false
  - id: D4
    description: "telephony.toml ships three per-DID game entries (3234/3283/8283) sharing one line_template, plus service.hcl's four-secret valueFrom wiring replacing the retired legacy CTF_ANNOUNCEMENT_CODE entry"
    requirement: QUICK-260727-PDH
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_config.py::test_shipped_telephony_toml_announcement_dids_now_per_game_scoped, test_shipped_telephony_toml_three_games_share_template_distinct_code_names, test_shipped_telephony_toml_3283_game_entry, test_shipped_telephony_toml_8283_game_entry"
        status: pass
      - kind: other
        ref: "structural grep over infra/terraform/live/site/services/telephony-edge/service.hcl (4/4 pass, see Task 2 <verify>)"
        status: pass
    human_judgment: false
  - id: D5
    description: "kv telephony list and kv studio both render a phone-games section (env var NAMES + set/unset status + DID scope + sms_reply_dids) via one shared Go parser, degrading gracefully on an unreadable telephony.toml"
    requirement: QUICK-260727-PDH
    verification:
      - kind: unit
        ref: "kv/internal/app/studio/repofile_adapter_test.go::TestParseTelephonyGames_OneEntryPerBlock, TestParseTelephonyGames_CommentedDidsLineIgnored, TestParseTelephonyGames_ShippedConfigMatchesTask2Entries, TestAnnotateGameEnv_StatusRules; kv/internal/app/cmd/telephony_test.go::TestPrintTelephony_GamesSectionRendersDidsEnvVarsStatusesAndSms, TestPrintTelephony_GamesSectionNeverLeaksASecretValue"
        status: pass
    human_judgment: false
  - id: D6
    description: "Live phone verification of the 8283 game's numeric and (once seeded) spoken trigger"
    verification: []
    human_judgment: true
    rationale: "Requires a real dial-in call against the deployed telephony-edge task after the terraform apply/redeploy operator step below — not exercisable from this sandbox."

duration: ~25min
completed: 2026-07-27
status: complete
---

# Quick Task 260727-pdh: Second phone game on 725-404-8283 Summary

**A third per-DID CTF phone game on 725-404-8283 with an optional either-factor spoken trigger (DTMF code OR secret words), sharing the same GateProcessor/controller machinery as the existing 3234/3283 games — the 8283 game ships with its numeric trigger live and its spoken trigger deliberately inert behind a documented sentinel.**

## Performance

- **Duration:** ~25 min
- **Tasks:** 3/3 complete
- **Files modified:** 16 (12 code/config + 1 infra + 3 test-only where not already counted above)

## Accomplishments

- `AnnouncementEntry.words_env_var` (optional, NAME-only, mirrors `code_env_var`) — deliberately renamed off the design doc's proposed `passphrase_env_var`, which the shared credential-field gate refuses outright.
- `GateProcessor` now accumulates spoken tokens even on OTP-only DIDs (D-06 — previously accumulation was skipped entirely there), matches an armed announcement-words registry via the existing `match_passphrase`, resolves the gate synchronously via `cancel_for_takeover` before spawning the dispatch callback (never awaited inline, fires at most once), with the D-05e/D-07 redaction boundary completely unchanged (zero pre-unlock frames forwarded, zero heard/matched words or registry keys logged).
- `AsteriskCallController` builds a `code_env_var`-keyed words registry (four disabled states — no field, env var absent, empty value, `__unset__` sentinel — all skip gracefully and leave the numeric trigger unaffected), DID-filters it per call through the existing `_announcement_matches_did` guard, and dispatches a spoken match straight into the same `_gate_announcement` the DTMF factor already calls.
- `telephony.toml` now ships **three** `[[telephony.announcement]]` entries — one block per game (3234, 3283, 8283) — all sharing one identical OTP-gag `line_template`, each with its own `dids`, `code_env_var`, and (8283 only) `words_env_var`. `otp_only_dids` extended to all three DIDs.
- `service.hcl` wires four SSM `valueFrom` secrets (`CTF_ANNOUNCEMENT_CODE_3234/_3283/_UCTF`, `CTF_ANNOUNCEMENT_WORDS_UCTF`), replacing the single retired `CTF_ANNOUNCEMENT_CODE` entry; the legacy SSM *parameter* is deliberately left in place for now (see User Setup Required).
- `kv/internal/app/studio` gained `ParseTelephonyGames`/`ReadTelephonyGames`/`AnnotateGameEnv` — one shared parser consumed by both `kv telephony list` (new "Phone games" table/JSON section) and `kv studio` (new read-only games panel), printing env var **names** + set/unset status only, never a secret value.

## Task Commits

Each task was committed atomically:

1. **Task 1: Python — optional words_env_var, gate spoken-trigger seam, controller either-factor dispatch** - `f4040c5` (feat)
2. **Task 2: telephony.toml three per-DID game entries + otp_only_dids + service.hcl SSM wiring** - `33f09fe` (feat)
3. **Task 3: Go — shared announcement parser, kv telephony list games section, kv studio games panel** - `25f1d47` (feat)

## Files Created/Modified

- `apps/voice/src/klanker_voice/telephony/config.py` — `AnnouncementEntry.words_env_var` field + parsing
- `apps/voice/src/klanker_voice/telephony/gate.py` — `AnnouncementWordsCallback`, `announcement_words`/`on_announcement_words` constructor kwargs, reworked `TranscriptionFrame` branch
- `apps/voice/src/klanker_voice/telephony/controller.py` — `ANNOUNCEMENT_WORDS_UNSET_SENTINEL`, `_announcement_words_by_code_env_var` registry, `announcement_words` kwarg, per-call DID-filtered phrase map + `_on_announcement_words` closure in `_finish_stasis_start_gated`
- `apps/voice/configs/telephony.toml` — three per-DID game entries, extended `otp_only_dids`
- `infra/terraform/live/site/services/telephony-edge/service.hcl` — four-secret announcement `valueFrom` block
- `apps/voice/tests/test_telephony_config.py`, `test_telephony_gate.py`, `test_telephony_controller.py` — new/updated coverage for all of the above
- `kv/internal/app/studio/types.go` — `GameEntry`, `ConfigView.Games`
- `kv/internal/app/studio/repofile_adapter.go` — `ParseTelephonyGames`, `RepoFiles.ReadTelephonyGames`, `AnnotateGameEnv`, `parseTOMLArrayLine`, `announcementWordsUnsetSentinel`
- `kv/internal/app/studio/view.go`, `server.go` — `AssembleInput.Games` threaded through, `compilesToMap` entries
- `kv/internal/app/studio/web/index.html`, `app.js` — games panel markup + `renderGames`
- `kv/internal/app/cmd/telephony.go` — `TelephonyListReport.Games`, `readTelephonyGames`, games section rendering
- `kv/internal/app/studio/repofile_adapter_test.go`, `view_test.go`, `kv/internal/app/cmd/telephony_test.go` — new coverage

## Decisions Made

- **`words_env_var`, not `passphrase_env_var`.** CONTEXT.md's proposed name is refused outright by `klanker_voice.config._CREDENTIAL_FIELD_RE` (any TOML key containing a `passphrase` token at a compound boundary), the exact same class of collision that forced the existing `otp_env_var` rename from `otp_auth_env_var`. Semantics are byte-for-byte what CONTEXT.md specified; only the key spelling changed. A regression test (`test_announcement_passphrase_env_var_key_rejected`) proves the rejection so this stays documented in the suite rather than tribal memory.
- **The words registry is keyed by `code_env_var` (a NAME), never by the resolved code or words value.** This is what makes the `on_announcement_words(key)` callback carry only an opaque, non-secret correlation key across the GateProcessor -> controller boundary (T-pdh-01).
- **The `__unset__` sentinel is a real, non-empty SSM value — never an absent parameter.** ECS fails task launch outright on a missing `valueFrom`; seeding the words parameter with this literal (rather than leaving it absent) is what lets the four-secret service.hcl wiring deploy safely today while the spoken trigger stays inert. `controller.py`'s `ANNOUNCEMENT_WORDS_UNSET_SENTINEL` and `kv`'s `announcementWordsUnsetSentinel` (in `repofile_adapter.go`) apply the identical rule (stripped, lowercased, exact whole-value match — never a substring test) so neither operator surface can ever report an inert trigger as live.
- **The two new Go operator surfaces share ONE parser.** `cmd/telephony.go` already imports `studio` (per the codebase's existing "studio must never import cmd" rule); `ParseTelephonyGames`/`AnnotateGameEnv` live in `studio` and both `kv telephony list` and `kv studio` call into them, so the TOML-scanning logic is never duplicated.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 — Bug] `test_real_checked_in_telephony_toml_has_announcement_entry` would have broken after Task 2's TOML edit but wasn't in the plan's explicit "three assertions" list**
- **Found during:** Task 2
- **Issue:** The plan named three shipped-TOML test assertions that would break from narrowing/adding announcement entries, but a fourth (`test_real_checked_in_telephony_toml_has_announcement_entry`, asserting `len(cfg.announcements) == 1` and `code_env_var == "CTF_ANNOUNCEMENT_CODE"`) also breaks under the same edit and was not listed.
- **Fix:** Updated it alongside the three named assertions (now asserts 3 entries, `CTF_ANNOUNCEMENT_CODE_3234`).
- **Files modified:** `apps/voice/tests/test_telephony_config.py`
- **Committed in:** `33f09fe` (Task 2 commit)

**2. [Rule 1 — Bug] `test_telephony_controller.py`'s synthetic `_announcement_entry` helper and the `test_announcement_code_unset_env_var_arms_no_trigger` `monkeypatch.delenv` call both still referenced the retired bare `CTF_ANNOUNCEMENT_CODE` name**
- **Found during:** Task 2 (per the plan's own explicit instruction to rename this fixture)
- **Issue:** Left as-is, the test suite would still pass (it's an arbitrary fixture value) but a future reader could mistake the fixture default for a live env var name, and a repo-wide grep for the retired name would falsely flag this file.
- **Fix:** Renamed the fixture default to `CTF_ANNOUNCEMENT_CODE_3234` and the corresponding `delenv` call to match.
- **Files modified:** `apps/voice/tests/test_telephony_controller.py`
- **Committed in:** `33f09fe` (Task 2 commit)

**3. [Rule 1 — Bug] The generic (non-shipped-config) `VALID_ANNOUNCEMENT_TOML` fixture constant in `test_telephony_config.py` also used the bare `CTF_ANNOUNCEMENT_CODE` name**
- **Found during:** Task 2, while satisfying the plan's overall verification item 7 ("a repo-wide grep for the retired bare `CTF_ANNOUNCEMENT_CODE` env var name returns no live reference ... in ... the Python tests")
- **Issue:** This fixture is unrelated to the shipped config (it's used across many generic `AnnouncementEntry`-parsing tests), but its literal value collided with the retired name.
- **Fix:** Renamed to `CTF_ANNOUNCEMENT_CODE_TEST` (and the three assertions/replace-pairs that reference it).
- **Files modified:** `apps/voice/tests/test_telephony_config.py`
- **Committed in:** `33f09fe` (Task 2 commit)

**4. [Deferred, not fixed — out of scope] `test_telephony_lifecycle.py`'s local `_o2q_announcement_entry` helper (from a prior quick task, 260717-o2q) still uses the bare `CTF_ANNOUNCEMENT_CODE` name**
- **Found during:** Task 2's grep-for-retired-name check
- **Why not fixed:** `test_telephony_lifecycle.py` is not in this quick task's declared file scope, this is a purely synthetic fixture value unrelated to the shipped config or to any behavior this task changed, and the executor's scope-boundary rule restricts auto-fixes to issues directly caused by the current task's changes. Logged (not silently dropped) so a future reader isn't surprised by the residual grep hit.
- **Recommendation:** A trivial follow-up rename if/when that file is next touched.

**5. [Deferred, not fixed — out of scope] `gofmt -l .` over the whole `kv/` module reports two pre-existing unformatted files this task never touched**
- **Found during:** Task 3's own `<verify>` command
- **Files:** `kv/internal/app/cmd/code.go`, `kv/internal/app/cmd/voipms.go` (last touched by quick task 260721-te5, commit `fc90763`)
- **Why not fixed:** Pre-existing, unrelated to this task's changes; per the scope-boundary rule, out-of-scope formatting drift is logged, not auto-fixed. Every file this task actually touched is individually gofmt-clean (verified before each commit).
- **Full detail:** see `.planning/quick/260727-pdh-second-phone-game-on-725-404-8283-per-ga/deferred-items.md`.

---

**Total deviations:** 5 (3 auto-fixed under Rule 1, 2 logged-but-deferred as out of scope)
**Impact on plan:** All fixes were test-hygiene corrections directly caused by the Task 2 TOML edit; no scope creep, no behavior change beyond what the plan specified.

## Issues Encountered

None beyond the deviations documented above — full telephony suites (Python 197/197, Go `go test ./...` all packages) and every automated `<verify>` command in the plan passed on the first full run after implementation.

## User Setup Required

1. **Replace the `__unset__` sentinel in `/kmv/secrets/use1/ctf/announcement_words_uctf`** with real whitespace-separated spoken-trigger words. This is the ONLY step that turns the 8283 game's spoken trigger on — its numeric trigger (`CTF_ANNOUNCEMENT_CODE_UCTF`) is already seeded live and works today regardless.
2. **Apply the terraform change and redeploy telephony-edge** to publish the four per-DID `valueFrom` env var wirings in `service.hcl`. No terraform apply and no SSM writes were performed by this task.
3. **After that deploy is verified live**, delete the orphaned legacy SSM parameter `/kmv/secrets/use1/ctf/announcement_code`. Do **NOT** delete it before cutover — the currently-running task definition still references it, and an early deletion would fail task launch on the next restart.
4. **(Optional, later)** Give the 8283 entry its own `line_template` — a TOML-only edit that must keep a `{code}` placeholder. All three entries currently share one identical script.

**All four SSM parameters are ALREADY seeded and readback-verified** (`announcement_code_3234`, `announcement_code_3283`, `announcement_code_uctf` hold real per-game codes; `announcement_words_uctf` holds the literal `__unset__` sentinel) — so the `valueFrom` wiring in `service.hcl` is deploy-safe as written and there is **NO** seed-before-deploy step remaining. The words parameter intentionally holds a sentinel rather than being absent, precisely because ECS fails task launch on a missing `valueFrom` parameter.

**The `__unset__` sentinel contract:** defined as `ANNOUNCEMENT_WORDS_UNSET_SENTINEL` in `apps/voice/src/klanker_voice/telephony/controller.py`, and as `announcementWordsUnsetSentinel` in `kv/internal/app/studio/repofile_adapter.go`. Both `kv telephony list` and `kv studio` apply the identical rule (stripped, lowercased, exact whole-value match — never a substring test) via `AnnotateGameEnv`, so the 8283 game ships with its numeric trigger live and its spoken trigger correctly reported as inert on every operator surface.

## Next Phase Readiness

- All three code-level `<verify>` blocks pass: Python telephony suite 197/197, `kv` `go build ./...` + `go test ./...` clean, `service.hcl` structural greps 4/4.
- The shipped-config cross-check test (`TestParseTelephonyGames_ShippedConfigMatchesTask2Entries`) proves the Go parser and the Python loader agree on the three entries Task 2 wrote.
- No blockers for merge; the four User Setup Required items above are the only remaining live-deploy steps, none of which are code changes.

---
*Phase: quick-260727-pdh*
*Completed: 2026-07-27*

## Self-Check: PASSED

- Commits found in `git log --oneline --all`: `f4040c5`, `33f09fe`, `25f1d47`.
- Key files verified present on disk: `apps/voice/src/klanker_voice/telephony/config.py`, `gate.py`, `controller.py`, `apps/voice/configs/telephony.toml`, `infra/terraform/live/site/services/telephony-edge/service.hcl`, `kv/internal/app/studio/repofile_adapter.go`, `kv/internal/app/cmd/telephony.go`, this SUMMARY.
- No missing items.
