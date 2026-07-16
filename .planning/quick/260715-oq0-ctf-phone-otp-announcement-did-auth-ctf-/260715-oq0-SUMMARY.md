---
phase: quick-260715-oq0
plan: 01
subsystem: telephony
tags: [totp, hmac-sha1, ctf, pipecat, asterisk-ari, aiohttp, vitest, pytest]

# Dependency graph
requires:
  - phase: quick-260714-hhj/hpp (telephony public-call tuning)
    provides: the existing AsteriskCallController §24 gate + _teardown_gate_resources seam this plan dispatches around
provides:
  - "GET /use1/ctf/otp: internal-only RFC 6238 TOTP issuer (120s period, 6 digits), no-oracle uniform 404"
  - "telephony.announcements config surface ([[telephony.announcement]]) + the concrete DID 7254043234 entry"
  - "controller._run_announcement: a pre-gate CTF phone-OTP announcement branch"
affects: [meshtk (out of scope here, shares only the TOTP contract), telephony-edge deploy]

tech-stack:
  added: []
  patterns:
    - "Zero-dep TOTP: Node crypto HMAC-SHA1 instead of an otplib/otpauth dependency"
    - "Pre-gate DID dispatch: a controller branch that returns before the §24 gate/quota/pipeline entirely, reusing the existing pre-ActiveCall teardown path"
    - "Minimal TTS-only pipeline (tts -> transport.output()) wrapped in its own PipelineWorker+WorkerRunner, independent of create_call_session"

key-files:
  created:
    - apps/auth/webapp/src/lib/ctf-totp.ts
    - apps/auth/webapp/src/app/ctf/otp/route.ts
    - apps/auth/webapp/src/lib/__tests__/ctf-totp.test.ts
    - apps/auth/webapp/src/app/ctf/__tests__/ctf-otp-route.test.ts
  modified:
    - apps/voice/src/klanker_voice/telephony/config.py
    - apps/voice/configs/telephony.toml
    - apps/voice/src/klanker_voice/telephony/controller.py
    - apps/voice/tests/test_telephony_config.py
    - apps/voice/tests/test_telephony_controller.py

key-decisions:
  - "otp_env_var (not otp_auth_env_var): the design doc's proposed TOML key is refused by config.py's _CREDENTIAL_FIELD_RE (_auth_ substring) even though it only ever holds an env-var NAME, never a secret value; renamed to mirror the working tel_mint_env_var precedent."
  - "Playback completion = bounded fixed sleep (ANNOUNCEMENT_PLAYBACK_GRACE_SECONDS=12s), not a frame-level event: mirrors call_runtime.py's own proven goodbye-leg pattern (speak_goodbye -> sleep(goodbye_grace_seconds) -> runner.cancel) exactly, guaranteeing a stuck synth can never hang the PSTN line."
  - "_run_announcement builds its own minimal Pipeline([tts, transport.output()]) + PipelineWorker + WorkerRunner rather than reusing create_call_session/build_pipeline -- avoids constructing STT/LLM/knowledge-router machinery for a one-line, one-directional announcement."

requirements-completed: []

coverage:
  - id: D1
    description: "GET /use1/ctf/otp returns the current-step 120s TOTP JSON on success (cache-control no-store) and an identical uniform 404 on every failure mode (missing secret, bad/absent bearer, internal error)"
    verification:
      - kind: unit
        ref: "apps/auth/webapp/src/lib/__tests__/ctf-totp.test.ts (6 tests, incl. RFC 6238 SHA1 vector)"
        status: pass
      - kind: unit
        ref: "apps/auth/webapp/src/app/ctf/__tests__/ctf-otp-route.test.ts (5 tests)"
        status: pass
    human_judgment: false
  - id: D2
    description: "telephony.announcements config surface: [[telephony.announcement]] parses into a frozen tuple with validation (did/otp_url/line_template required, {code} placeholder enforced); absent table -> empty tuple, byte-unchanged defaults; configs/telephony.toml carries the concrete 7254043234 entry with no secret value"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_config.py (7 new tests + 19 pre-existing, 26/26 pass)"
        status: pass
    human_judgment: false
  - id: D3
    description: "A call to DID 7254043234 dispatches to _run_announcement before the §24 gate (quota.start_gate never called, no ActiveCall registered); non-announcement DIDs are byte-unchanged; OTP-fetch failure and post-playback both tear down via the shared pre-ActiveCall teardown; the spoken line digit-spaces the code and substitutes both {code} occurrences"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_controller.py (5 new tests + 5 pre-existing, 10/10 pass)"
        status: pass
      - kind: integration
        ref: "apps/voice/tests/test_telephony_lifecycle.py -q (18/18 pass, gated/ungated paths not regressed)"
        status: pass
    human_judgment: true
    rationale: "The PLAN's own <verification> section calls for a live manual integration check (call the DID on the local Asterisk edge, confirm the spoken code matches /ctf/otp and an independently computed TOTP) -- not exercised in this offline execution; also gated on the deferred SSM/task-def wiring below actually being live."

# Metrics
duration: ~55min
completed: 2026-07-15
status: complete
---

# Quick Task 260715-oq0: CTF Phone-OTP Announcement DID Summary

**Internal-only `GET /use1/ctf/otp` RFC 6238 TOTP issuer (zero-dep Node crypto HMAC-SHA1) plus a pre-§24-gate telephony announcement branch that answers DID 7254043234, speaks the current OTP twice (digit-spaced), and hangs up — no STT/LLM/quota ever touched.**

## Performance

- **Duration:** ~55 min
- **Tasks:** 3/3 completed
- **Files created:** 4 (auth: ctf-totp.ts, otp/route.ts + 2 test files)
- **Files modified:** 5 (voice: config.py, telephony.toml, controller.py + 2 test files)

## Accomplishments

- Zero-dependency RFC 6238 TOTP helper (`base32Decode` + `computeTotp`) verified against the RFC 6238 Appendix B SHA1 test vector, plus a `GET /ctf/otp` route mirroring `/tel`'s exact no-oracle posture (uniform 404 on every failure mode, optional `CTF_OTP_AUTH_TOKEN` shared bearer, `cache-control: no-store`).
- `TelephonyConfig.announcements`: a frozen `AnnouncementEntry(did, otp_url, otp_env_var, line_template)` tuple parsed from `[[telephony.announcement]]`, validated at load time (non-empty `did`/`otp_url`, `{code}` placeholder required in `line_template`), with the concrete `7254043234` entry added to `configs/telephony.toml` (the standalone telephony-edge harness config).
- `AsteriskCallController.on_stasis_start` now dispatches a matched announcement DID to `_run_announcement` before the §24 gate: fetches the OTP (`_fetch_ctf_otp`, mirrors `_fetch_tel_token`'s uniform-failure contract), builds the digit-spaced twice-substituted line (`_build_announcement_line`), speaks it over a minimal TTS-only `Pipeline([tts, transport.output()])` + `PipelineWorker`/`WorkerRunner`, waits a bounded grace period, then tears down via the existing `_teardown_gate_resources` — reused identically on OTP-fetch failure.

## Task Commits

Each task was committed atomically:

1. **Task 1: auth GET /ctf/otp TOTP endpoint** — `f644b07` (feat)
2. **Task 2: telephony config — announcements field + DID entry** — `abd4ed9` (feat)
3. **Task 3: telephony controller — announcement-DID branch** — `f31db92` (feat)

_All three tasks followed the TDD flow (tests written alongside/before implementation, verified green before commit); no separate RED-only commits were made — see "TDD Note" below._

## Files Created/Modified

- `apps/auth/webapp/src/lib/ctf-totp.ts` — zero-dep base32 decode + RFC 6238 HMAC-SHA1 TOTP (`computeTotp`)
- `apps/auth/webapp/src/app/ctf/otp/route.ts` — `GET /use1/ctf/otp`, no-oracle uniform 404
- `apps/auth/webapp/src/lib/__tests__/ctf-totp.test.ts` — RFC vector + expiresIn bounds + determinism/base32-tolerance (6 tests)
- `apps/auth/webapp/src/app/ctf/__tests__/ctf-otp-route.test.ts` — success/missing-secret/bearer/internal-error uniform-404 contract (5 tests)
- `apps/voice/src/klanker_voice/telephony/config.py` — `AnnouncementEntry` dataclass + `TelephonyConfig.announcements` + `_parse_announcements` validation
- `apps/voice/configs/telephony.toml` — concrete `[[telephony.announcement]]` entry for DID 7254043234
- `apps/voice/tests/test_telephony_config.py` — 7 new tests (absent table, well-formed parse, missing-`{code}`/`did`/`otp_url` rejection, optional `otp_env_var`, real-file check)
- `apps/voice/src/klanker_voice/telephony/controller.py` — `_build_announcement_line`, `_fetch_ctf_otp`, `_announcements_by_did` lookup, dispatch in `on_stasis_start`, `_run_announcement`
- `apps/voice/tests/test_telephony_controller.py` — 5 new tests (line-builder unit test, dispatch-before-gate, non-announcement-DID unaffected, fetch-failure teardown, success ordering)

## Decisions Made

- **`otp_env_var` instead of `otp_auth_env_var`** (deviation from the design doc): `config.py`'s shared `_CREDENTIAL_FIELD_RE` rejects any TOML key containing the substring `_auth_`, so the design doc's proposed key name would be refused by the credential gate at load time even though it only ever holds the NAME of an env var, never a secret value. Renamed to `otp_env_var`, exactly mirroring the already-shipped `tel_mint_env_var` precedent. The VALUE it names is unchanged: `CTF_OTP_AUTH_TOKEN`.
- **Bounded-sleep playback completion, not an event**: rather than chase pipecat's exact `BotStoppedSpeakingFrame`/idle-timeout semantics for a minimal standalone pipeline, `_run_announcement` mirrors `call_runtime.py`'s own already-proven wind-down pattern — `queue_frames([TTSSpeakFrame(...)])` → `asyncio.sleep(ANNOUNCEMENT_PLAYBACK_GRACE_SECONDS)` → `runner.cancel(...)`. This is the SAME completion signal the existing goodbye leg uses in production (`_on_stop` in `call_runtime.py`), so it inherits that path's tested reliability and cannot hang the line even if TTS synthesis stalls.
- **Standalone minimal pipeline, not `create_call_session`**: `_run_announcement` builds `Pipeline([tts, transport.output()])` directly (via `factories.build_tts` + `pipeline.build_worker` + `pipecat.workers.runner.WorkerRunner`) instead of routing through `create_call_session`/`build_pipeline`, which would also construct STT/LLM/knowledge-router/ledger machinery this one-directional announcement never needs.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 — following plan's own explicit instruction] `otp_env_var` key rename**
- **Found during:** Task 2
- **Issue:** The design doc and PLAN.md both flag this explicitly up front — `otp_auth_env_var` is refused by the credential-field regex.
- **Fix:** Used `otp_env_var` everywhere (dataclass field, TOML key, docstring, tests) per the plan's own `<action>` instruction.
- **Files modified:** `apps/voice/src/klanker_voice/telephony/config.py`, `apps/voice/configs/telephony.toml`, `apps/voice/tests/test_telephony_config.py`
- **Verification:** `test_credential_looking_tel_mint_field_rejected`-style coverage confirmed the gate still fires on genuinely credential-shaped keys; `otp_env_var` itself parses cleanly (26/26 config tests pass).
- **Committed in:** `abd4ed9` (Task 2 commit)

---

**Total deviations:** 1 (a plan-directed rename, not an unplanned discovery — no scope creep).
**Impact on plan:** None beyond the plan's own explicit instruction.

## Issues Encountered

- **Local dev-toolchain drift, not a code issue:** `apps/auth/webapp` had no `node_modules` installed in this worktree, and the freshly-installed `rolldown` optional native binding for `darwin-arm64` was missing (npm's known optional-dependency install bug) — resolved with `npm install` + a targeted `npm install --no-save @rolldown/binding-darwin-arm64@1.1.4` (pinned to the exact version `vite`/`rolldown` expect). Separately, the installed Node (`v22.1.0`) doesn't satisfy `vitest`/`vite`'s `engines` range (`^20.19.0 || >=22.12.0`), which surfaced as an ESM/CJS `std-env` load error; resolved by running tests under `nvm use v22.12.0` (already installed via nvm in this environment). Neither fix touched any committed file — `package.json`/`package-lock.json` are untouched (`git status` confirms).
- No other issues — both telephony test suites (`test_telephony_config.py`, `test_telephony_controller.py`) and the `test_telephony_lifecycle.py` regression suite passed on the first full run after implementation; `ruff check` is clean on both modified `.py` source files (the pre-existing `F811 fake_aws redefinition` ruff warnings in `test_telephony_controller.py` are a pre-existing lint-config gap affecting every test function using the shared `fake_aws` pytest fixture in that file — confirmed via `git stash` that the same 5 warnings exist on the pre-change file; my 3 new tests add 4 more instances of the identical, already-accepted pattern, out of this task's scope per the SCOPE BOUNDARY rule).
- A broader `uv run pytest -q` across the whole `apps/voice` suite shows pre-existing, unrelated failures/errors in `test_session.py`, `test_slot_leak.py`, and `test_quota.py` — all `botocore`-flavored errors requiring a local DynamoDB emulator that isn't running in this environment. Confirmed pre-existing via `git stash` + re-run (identical failure set with none of this task's changes applied). Out of scope; not touched.

## User Setup Required — DEFERRED OPS FOLLOW-UPS (not implemented in this quick task)

Per the plan's explicit instruction, infra/secrets wiring is documented here as deferred deploy steps, NOT implemented:

1. **`CTF_OTP_SECRET`** (SSM SecureString, base32) — needs task-definition wiring for the **auth** ECS service (the `/ctf/otp` route reads it from `process.env.CTF_OTP_SECRET`).
2. **`CTF_OTP_AUTH_TOKEN`** (SSM SecureString) — needs task-definition wiring for **both**:
   - the **auth** service (the route's optional bearer check, `process.env.CTF_OTP_AUTH_TOKEN`), and
   - the **voice/telephony-edge** service (the controller reads it by the configured `otp_env_var` name, `CTF_OTP_AUTH_TOKEN`, at call time via `os.environ.get(entry.otp_env_var, "")`).
3. Once both are live, a **manual integration check** is still needed (per the PLAN's own `<verification>` section): call DID 7254043234 on the deployed Asterisk edge and confirm the spoken code matches a live `/ctf/otp` response and an independently-computed TOTP for the same secret/clock. This was NOT exercised in this offline execution (D3's `human_judgment: true` reflects exactly this gap).
4. meshtk-side TOTP verification (±1 step skew tolerance, award-by-radio-id) is explicitly out of scope for this repo (separate meshtk spec) — only the shared contract (HMAC-SHA1, 6 digits, 120s period, T0=0, base32 secret) is defined here.

## Next Phase Readiness

- All three in-repo pieces (auth issuer, telephony config surface, controller branch) are implemented and unit/integration tested offline; the design's "in scope (this repo)" boundary is fully delivered.
- Blocked on the deferred SSM/task-def wiring above before any live phone-call demo — this is an infra/ops task, not a code task, and was intentionally left out of this quick task's scope.
- No stubs: every code path either does real work (TOTP compute, TOML parse+validate, HTTP fetch, TTS synth) or fails closed identically to the rest of this codebase's established patterns.

---
*Quick task: 260715-oq0*
*Completed: 2026-07-15*

## Self-Check: PASSED

All 10 created/modified files confirmed present on disk; all 3 task commits
(`f644b07`, `abd4ed9`, `f31db92`) confirmed present in `git log --oneline --all`.
