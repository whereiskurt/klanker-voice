---
phase: quick-260727-qfq
plan: 01
subsystem: telephony
tags: [nextjs, totp, pipecat-telephony, terraform, ecs-secrets]

# Dependency graph
requires:
  - phase: quick-260727-pdh
    provides: three per-DID [[telephony.announcement]] game entries (3234/3283/8283), each with its own code_env_var/words_env_var/sms_reply_dids
provides:
  - "?g=<game> dimension on the auth /ctf/otp route, backed by a static Map allowlist (D-01/D-02)"
  - "byte-identical no-g legacy path for cutover safety (D-03)"
  - "uniform 404 across every failure mode (D-04)"
  - "per-entry sms_claim_url_template on AnnouncementEntry, {code}-validated, byte-identical legacy fallback (D-07)"
  - "three shipped telephony.toml entries each pointed at their own game query + their own didhtp claim slug (D-06/D-07)"
  - "three additive per-game valueFrom secrets on auth's service.hcl, legacy CTF_OTP_SECRET kept (D-08)"
affects: [ctf-otp-cutover, telephony-edge-redeploy, dc34-q-defcon-run-c-verifier]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Map-based static allowlist for request-key -> env-var-NAME lookups (no prototype-chain fallthrough, no interpolation)"
    - "Uniform-404 no-oracle contract proven via captured-baseline equality, not bare status assertions"
    - "Optional per-entry TOML field with byte-identical-legacy-fallback + {placeholder}-validated-when-present pattern (mirrors line_template/words_env_var)"

key-files:
  created: []
  modified:
    - apps/auth/webapp/src/app/ctf/otp/route.ts
    - apps/auth/webapp/src/app/ctf/__tests__/ctf-otp-route.test.ts
    - apps/voice/src/klanker_voice/telephony/config.py
    - apps/voice/src/klanker_voice/telephony/controller.py
    - apps/voice/configs/telephony.toml
    - apps/voice/tests/test_telephony_config.py
    - apps/voice/tests/test_telephony_sms.py
    - infra/terraform/live/site/services/auth/service.hcl

key-decisions:
  - "D-01: GET /use1/ctf/otp?g=<game> (3234/3283/8283) resolves TOTP from CTF_OTP_SECRET_<game>, mapped via a static allowlist"
  - "D-02: allowlist is a Map (no prototype-chain fallthrough); request input is a lookup KEY only, never interpolated into an env-var name or logged"
  - "D-03: no-g (or empty-g) request stays byte-identical to pre-qfq behavior -- the cutover-safety property, since telephony-edge still calls the bare URL until it redeploys"
  - "D-04: every failure mode (unknown game, unset mapped secret, bad bearer, malformed base32) collapses into the SAME uniform 404, proven by captured-baseline equality"
  - "D-05: TOTP params (period=120, digits=6, SHA1) unchanged on every path"
  - "D-06: each telephony.toml entry's otp_url now carries its own ?g= query"
  - "D-07: new optional AnnouncementEntry.sms_claim_url_template replaces ONLY the URL portion of the claim SMS; absent -> byte-identical legacy default; field name deliberately clears the credential-regex gate"
  - "D-08: auth service.hcl gains three additive valueFrom secrets, keeping the legacy CTF_OTP_SECRET entry until post-cutover"
  - "D-09 (out of scope): the q.defcon.run/c verifier is DC34-side and still unbuilt -- this task ships only the URL shape (c=<slug>&v=<code>) as a forward contract"

patterns-established:
  - "Static Map allowlist for any future request-keyed env-var selection (mirrors this route's GAME_SECRET_ENV_VARS)"
  - "Prefix-constant + default-template-constant split so a legacy module constant stays byte-identical while becoming per-entry-overridable"

requirements-completed: [QUICK-260727-QFQ]

coverage:
  - id: D1
    description: "GET /use1/ctf/otp?g=<game> computes from that game's own CTF_OTP_SECRET_<game>, ignoring the legacy secret; no-g stays byte-identical to the legacy path"
    requirement: "QUICK-260727-QFQ"
    verification:
      - kind: unit
        ref: "apps/auth/webapp/src/app/ctf/__tests__/ctf-otp-route.test.ts (per-game + legacy-path suites)"
        status: pass
    human_judgment: false
  - id: D2
    description: "Every /ctf/otp failure mode (unknown game, unset mapped secret, bad bearer, malformed base32) returns the identical uniform 404, and prototype-member game keys (constructor/__proto__/toString) never resolve"
    requirement: "QUICK-260727-QFQ"
    verification:
      - kind: unit
        ref: "apps/auth/webapp/src/app/ctf/__tests__/ctf-otp-route.test.ts (uniform-404 baseline + prototype-safety suites)"
        status: pass
    human_judgment: false
  - id: D3
    description: "AnnouncementEntry.sms_claim_url_template parses with {code} validation, clears the credential-field gate, and an entry without one is byte-identical to the pre-qfq SMS body"
    requirement: "QUICK-260727-QFQ"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_config.py::test_sms_claim_url_template_* and apps/voice/tests/test_telephony_sms.py::test_build_sms_claim_body_*"
        status: pass
    human_judgment: false
  - id: D4
    description: "The three shipped telephony.toml entries each carry a distinct game-query otp_url and a distinct didhtp claim template; the shared telephony/go/config suites (auth, voice, kv) all stay green"
    requirement: "QUICK-260727-QFQ"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_config.py::test_shipped_telephony_toml_per_game_otp_urls_and_claim_templates"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_sms.py::test_hook_templated_entry_posts_own_slug_and_static_second_beat"
        status: pass
      - kind: other
        ref: "cd apps/voice && uv run pytest tests/test_telephony_config.py tests/test_telephony_controller.py tests/test_telephony_sms.py tests/test_telephony_lifecycle.py tests/test_telephony_gate.py -q (206 passed)"
        status: pass
      - kind: other
        ref: "cd kv && go test ./internal/app/studio/... ./internal/app/cmd/... (ok)"
        status: pass
    human_judgment: false
  - id: D5
    description: "Auth service.hcl gains exactly three new additive per-game valueFrom secrets, keeps the legacy CTF_OTP_SECRET entry, and neither site.hcl nor telephony-edge's service.hcl has any pending change"
    requirement: "QUICK-260727-QFQ"
    verification:
      - kind: other
        ref: "Task 3 verify command (grep-based structural check) -> INFRA_OK"
        status: pass
    human_judgment: false
  - id: D6
    description: "This is code-only, infra-additive-only work with no live deploy, no SSM writes, no DC34-side changes -- the cutover choreography and the q.defcon.run/c verifier build-out are deliberately deferred, and should be reviewed as a deploy sequencing decision, not auto-approved"
    human_judgment: true
    rationale: "Deploying auth, redeploying telephony-edge, and flipping the DC34 flag rows in the right order is an operational/coordination judgment call outside what a test suite can verify; flagged explicitly rather than silently assumed safe."

# Metrics
duration: ~20min
completed: 2026-07-27
status: complete
---

# Quick Task 260727-qfq: Per-game OTP wiring Summary

**Auth `/ctf/otp` route gains a static-allowlist `?g=<game>` dimension (Map-based, byte-identical no-g legacy path, uniform-404 no-oracle contract), voice's telephony.toml now points each of the three shipped games at its own OTP seed and its own SMS claim slug via a new optional `sms_claim_url_template` field, and auth's task definition gains three additive per-game SSM secrets.**

## Performance

- **Duration:** ~20 min
- **Completed:** 2026-07-27T23:21:30Z
- **Tasks:** 3/3 completed
- **Files modified:** 8

## Accomplishments

- `GET /use1/ctf/otp?g={3234,3283,8283}` each compute a TOTP from their OWN `CTF_OTP_SECRET_<game>` seed via a static `Map<string,string>` allowlist -- no prototype-chain fallthrough, no request input ever concatenated into an env-var name, no new log line.
- The no-`g` (legacy) path stays byte-identical to pre-260727-qfq behavior -- deterministically proven by setting all three per-game secrets to invalid base32 and confirming the legacy request still succeeds (D-03, the cutover-safety property the live telephony-edge depends on until it redeploys).
- Every failure mode (unknown game, a known game whose mapped secret is absent, a bad/missing bearer, malformed base32, and a prototype-member game key like `constructor`/`__proto__`/`toString`) collapses into the SAME uniform 404, proven by equality against a captured baseline response rather than bare status assertions.
- `AnnouncementEntry` gains an optional `sms_claim_url_template` (D-07): a public claim-URL template validated to contain `{code}` when present, replacing ONLY the URL portion of the mid-call claim SMS via a new `_build_sms_claim_body(entry, code)` helper. An entry without one is proven byte-identical to the legacy `ANNOUNCEMENT_SMS_BODY_TEMPLATE.format(code=code)` by direct equality against the real (now-split) module constant.
- `telephony.toml`'s three shipped game entries (3234/3283/8283) each got their `otp_url` extended with their own `?g=` query and a new `sms_claim_url_template` carrying their own `didhtp<game>` slug (`c=didhtp3234&v={code}`, etc.) -- so each game's spoken OTP AND its texted claim link are now cryptographically/identifiably distinct per game, closing the loop quick tasks 260727-ohq/-pdh left open.
- `infra/terraform/live/site/services/auth/service.hcl` gains three additive `valueFrom` secrets (`CTF_OTP_SECRET_3234`/`_3283`/`_8283`) pointing at the three already-seeded, readback-verified SSM parameters, alongside the untouched legacy `CTF_OTP_SECRET` entry -- no IAM, environment, `site.hcl`, or telephony-edge `service.hcl` change.

## Task Commits

Each task was committed atomically:

1. **Task 1: auth /ctf/otp game dimension (D-01..D-05) + no-oracle regression tests** - `9168043` (feat)
2. **Task 2: voice — per-entry sms_claim_url_template + per-game otp_url TOML edits (D-06, D-07)** - `bd2e392` (feat)
3. **Task 3: auth service.hcl — three additive per-game valueFrom secrets (D-08)** - `eba6bee` (feat)

_No TDD RED/GREEN split commits were used -- each task's tests were authored and committed alongside the implementation in a single task commit, matching this plan's own commit-per-task instruction._

## Files Created/Modified

- `apps/auth/webapp/src/app/ctf/otp/route.ts` - adds the `GAME_SECRET_ENV_VARS` static `Map` allowlist and the `?g=` branch, ahead of the unchanged bearer/secret/TOTP/response logic
- `apps/auth/webapp/src/app/ctf/__tests__/ctf-otp-route.test.ts` - reworked `makeRequest` to take an optional search string + a new `makeRequestNoUrl` helper; 11 new tests covering the legacy path, per-game path, uniform 404, prototype safety, and source discipline
- `apps/voice/src/klanker_voice/telephony/config.py` - `AnnouncementEntry.sms_claim_url_template` field + its `{code}`-validated parse rule in `_parse_announcements`
- `apps/voice/src/klanker_voice/telephony/controller.py` - splits `ANNOUNCEMENT_SMS_BODY_TEMPLATE` into `ANNOUNCEMENT_SMS_CLAIM_PREFIX` + `ANNOUNCEMENT_SMS_DEFAULT_CLAIM_URL_TEMPLATE`, adds `_build_sms_claim_body`, and rewires `_gate_announcement`'s SMS build site to call it
- `apps/voice/configs/telephony.toml` - all three game entries' `otp_url` gain a `?g=` query; all three gain a `sms_claim_url_template` with their own didhtp slug; one shared explanatory comment block above the first entry
- `apps/voice/tests/test_telephony_config.py` - new `sms_claim_url_template` parse/validation/credential-gate tests + an updated shipped-config assertion covering all three per-game `otp_url`/`sms_claim_url_template` values
- `apps/voice/tests/test_telephony_sms.py` - new `_build_sms_claim_body` unit tests + one hook test proving a templated entry posts its own slug while msg2 stays the unchanged static beat
- `infra/terraform/live/site/services/auth/service.hcl` - three new additive `valueFrom` secrets alongside the untouched legacy `CTF_OTP_SECRET` entry

## Decisions Made

All decisions were locked in `260727-qfq-CONTEXT.md` before this plan ran (D-01 through D-09, cross-referenced in the plan's `<locked_decisions>` block); none were reinterpreted during execution. See the frontmatter `key-decisions` list above for the full set.

## Deviations from Plan

None - plan executed exactly as written, including all four `constraints_discovered` items (defensive `nextUrl` read for the pre-existing test-stub gap, the `sms_claim_url_template` credential-gate clearance, the worktree's cold `node_modules`/node-23 requirement, and the no-IAM-change confirmation).

## Issues Encountered

None. All three verify commands passed on the first run; the full auth vitest suite (116/116) and the full telephony/kv verification block (206 Python + kv Go tests) both stayed green after every task.

## User Setup Required

None - no external service configuration required. This task performed NO SSM writes, NO terraform apply, and NO deploys (per D-09 scope).

## Cutover Choreography (recorded for the operator, NOT performed by this task)

Per CONTEXT.md's Specific Ideas section, the live rollout sequence is:

1. **Deploy auth first** (additive-only change, safe on its own -- the no-`g` path keeps working unchanged for any caller still on the bare URL).
2. **Then deploy telephony-edge**, which starts calling the new `?g=<game>` `otp_url` values and the new per-game `sms_claim_url_template` claim links.
3. **Then, on the DC34 side**, enable `didhtp3234`/`didhtp3283`/`didhtp8283` and disable the retired `didhtp1` flag row.
4. **Only after that**, delete the legacy SSM params and the legacy `CTF_OTP_SECRET` wiring in `service.hcl` -- alongside the legacy `announcement_code` param deletion already documented in `260727-pdh-SUMMARY.md`.

## Out of Scope / Known Follow-ups (flagged, not built here)

- **The `q.defcon.run/c` verifier is DC34-side and still unbuilt (D-09).** This task ships only the URL SHAPE (`c=<didhtp-slug>&v=<otp-code>`) as the contract the DC34 side must implement.
- **Doc drift, pre-existing and now widened:** `infra/terraform/live/site/SECRETS.md` and `site.hcl`'s `secrets.definitions` describe only the original three `ctf` keys; they already omitted 260727-pdh's four announcement params and now also omit these three `otp_secret_<game>` params. All are seeded out-of-band (operator-managed, not SOPS-managed) -- a documentation-only follow-up should record this so nobody applies the secrets unit expecting them to appear there.
- **No SSM writes, no terraform apply, no deploys, no DC34 changes were performed** during this task, per D-09.

## Next Phase Readiness

- Code-complete and fully test-verified on all three surfaces (auth, voice, infra-data). Ready for the operator-driven deploy sequence documented above.
- No blockers. The only remaining work is the cutover choreography itself (an operational/deploy-ordering action, not code) and the DC34-side `q.defcon.run/c` verifier build-out (tracked separately, out of this task's scope).

---
*Phase: quick-260727-qfq*
*Completed: 2026-07-27*

## Self-Check: PASSED
