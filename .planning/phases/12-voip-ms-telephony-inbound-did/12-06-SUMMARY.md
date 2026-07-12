---
phase: 12-voip-ms-telephony-inbound-did
plan: 06
subsystem: telephony
tags: [asterisk, ari, e164, jwt, fail-closed, gate, quota]

# Dependency graph
requires:
  - phase: 12-voip-ms-telephony-inbound-did (12-02)
    provides: "GET /tel/<e164> mint route, normalizeE164 canonical form, no-oracle 404 contract"
  - phase: 11-voip-ms-telephony-local-asterisk-edge (11-06)
    provides: "GateProcessor / §24 silent answer-gate, SessionLifecycle.upgrade_from_bypass, the gated StasisStart controller flow"
provides:
  - "CallIdentity.tier_id/caller_id/did (additive, telephony-only)"
  - "TelephonyConfig.tel_mint_url / tel_mint_env_var (plain, non-secret config; opt-in mint integration)"
  - "AsteriskCallController._mint_tier_from_caller_id / _fetch_tel_token (the /tel HTTP call + offline token validation)"
  - "ActiveCall.grant_tier_id -- the single seam _gate_unlock reads to decide what tier to grant"
affects: [12-07-telephony-edge-deploy, 12-08-manual-cellular-proof]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Opt-in composition at the config layer: tel_mint_url empty (default) == byte-identical Phase-11 behavior; non-empty == the new §23 mint path. No feature flag beyond the existing config field."
    - "Module-level HTTP seam (_fetch_tel_token) monkeypatched directly in tests, mirroring the existing quota.start_gate/greet_now/speak_goodbye stubbing convention -- no aioresponses/mock-server dependency needed."
    - "Fail-closed-before-gate-window: a mint failure short-circuits straight into the EXISTING _gate_fail_closed (deterministic goodbye + single idempotent teardown) instead of ever starting the DTMF/passphrase window."

key-files:
  created:
    - apps/voice/tests/test_telephony_controller.py
  modified:
    - apps/voice/src/klanker_voice/call_runtime.py
    - apps/voice/src/klanker_voice/telephony/config.py
    - apps/voice/src/klanker_voice/telephony/controller.py
    - apps/voice/tests/test_telephony_config.py

key-decisions:
  - "tel_mint_url empty (default) disables the mint step entirely -- every existing Phase-11 fixture/test (which never sets it) keeps granting the static telephony.unlock_tier_id unchanged, so this plan is additive/opt-in, not a breaking rewire of the gate."
  - "A mint failure fails closed by reusing the pipeline already built for the gate (so a TTS-capable worker exists to speak the goodbye) rather than skipping pipeline construction -- consistent with the existing gate-window-expiry/quota-denied fail-closed precedent; the gate window itself is simply never started for a failed-mint caller."
  - "_gate_unlock double-guards against a mint failure by checking active_call.grant_tier_id is None and refusing to grant any tier, even if a DTMF/passphrase factor matches during the fail-closed goodbye's grace-period race window -- never an open grant for an unmapped caller."
  - "The Bearer token env-var NAME (tel_mint_env_var) is a plain config field; the token VALUE is read from os.environ at mint-call time inside the controller, never stored in TelephonyConfig or TOML."

requirements-completed: [D-02, D-05, D-04, SC-2, SC-4]

coverage:
  - id: D1
    description: "CallIdentity carries the resolved telephony tier/caller_id/did; TelephonyConfig exposes the /tel endpoint URL + the token env-var NAME without letting any secret into config"
    requirement: "D-04"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_config.py#test_tel_mint_fields_parse"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_config.py#test_credential_looking_tel_mint_field_rejected"
        status: pass
    human_judgment: false
  - id: D2
    description: "A mapped caller ID's /tel mint grants the token-derived entitled tier at gate-unlock, not the static unlock_tier_id"
    requirement: "D-02"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_controller.py#test_gated_mint_success_grants_entitled_tier_not_static_unlock_tier"
        status: pass
    human_judgment: false
  - id: D3
    description: "An unmapped caller ID / /tel failure fails closed with zero quota.start_gate calls, a static goodbye, and exactly one teardown -- no STT/LLM/TTS metered session ever starts"
    requirement: "D-05, SC-4"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_controller.py#test_gated_mint_failure_fails_closed_no_quota_no_greet"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_controller.py#test_gated_no_caller_id_fails_closed"
        status: pass
    human_judgment: false
  - id: D4
    description: "Legacy behavior preserved when tel_mint_url is unconfigured (every pre-existing Phase-11 fixture); gate.py/pipeline.py/session.py byte-unchanged; bearer token never a literal"
    requirement: "SC-2"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_controller.py#test_mint_unconfigured_uses_legacy_static_unlock_tier_id"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_controller.py#test_bearer_token_read_from_configured_env_var_never_a_literal"
        status: pass
      - kind: other
        ref: "git diff --stat -- gate.py pipeline.py session.py (empty)"
        status: pass
    human_judgment: true
    rationale: "A real cellular call through the deployed telephony-edge against a live /tel endpoint (mapped and unmapped caller IDs) is Phase 12's own end-of-milestone manual proof (12-08), not something this offline unit-test plan can exercise."

# Metrics
duration: 25min
completed: 2026-07-12
status: complete
---

# Phase 12 Plan 06: §23 Caller-ID Mint Wired Into the §24 Gate Summary

**The Asterisk controller now normalizes a caller's ANI to E.164, mints an entitled tier via the private `/tel` endpoint, and grants THAT tier at gate-unlock instead of a hardcoded static tier — failing closed with zero metered quota burn for any unmapped or failed-mint caller — while leaving `gate.py`, `pipeline.py`, and `session.py` byte-unchanged.**

## Performance

- **Duration:** ~25 min
- **Started:** 2026-07-12T19:40Z (session start, per STATE.md)
- **Completed:** 2026-07-12T19:59:28Z
- **Tasks:** 2
- **Files modified:** 5 (1 created, 4 modified)

## Accomplishments

- `CallIdentity` (`call_runtime.py`) gains additive `tier_id`/`caller_id`/`did` fields (default `None`) — the WebRTC construction path (`subject`-only) stays byte-unchanged; every existing `CallIdentity(...)` call site across `server.py`/`webrtc.py`/tests keeps working unmodified.
- `TelephonyConfig` gains two plain, non-secret fields: `tel_mint_url` (the `/tel` endpoint base URL the controller composes `f"{url}/{e164}"` onto) and `tel_mint_env_var` (the NAME of the env var holding the shared bearer token — the token VALUE is read from `os.environ` at call time, never stored in config/TOML). Empty `tel_mint_url` (the default) means "mint integration not configured" — every existing `[telephony]` fixture/checked-in config keeps granting the legacy static `unlock_tier_id`, unaffected.
- `AsteriskCallController._finish_stasis_start_gated` now: normalizes the ARI caller number via a new `_normalize_e164` helper (a line-for-line Python port of the auth-app's `normalizeE164` TS helper, so the SAME canonical form hits the `byPhone` GSI lookup on both ends); when `tel_mint_url` is configured, calls the private `/tel` mint endpoint (`_mint_tier_from_caller_id` → the module-level `_fetch_tel_token` seam, Bearer header from the configured env var, 3s timeout) and validates the returned token via the existing offline `klanker_voice.auth.validate_access_token` path to derive the caller's entitled `tier_id`.
- The entitled tier is stored on a new `ActiveCall.grant_tier_id` field, which `_gate_unlock` now grants (instead of the static `telephony_cfg.unlock_tier_id`) — a caller's own code/tier, never a spoofable caller-ID-alone grant of a high tier (D-05).
- Any mint failure (unmapped caller ID, no caller ID at all, `/tel` non-200/timeout/network error, an invalid/unvalidatable token) skips the gate window entirely and routes straight into the pre-existing `_gate_fail_closed` (deterministic goodbye via TTS + the single idempotent `_close_active_call` teardown) — `quota.start_gate` is never called, so a scanner/unmapped caller burns zero metered STT/LLM/TTS accounting (SC-4). `_gate_unlock` itself also refuses to grant any tier when `grant_tier_id is None`, closing the narrow async race where a DTMF/passphrase factor could otherwise match during the fail-closed goodbye's grace period.

## Task Commits

Each task was committed atomically (Task 2 followed the TDD RED→GREEN cycle per its `tdd="true"` frontmatter):

1. **Task 1: Extend CallIdentity + add /tel endpoint config fields** - `03227ee` (feat)
2. **Task 2 RED: failing tests for /tel caller-ID mint + fail-closed** - `58aec68` (test)
2. **Task 2 GREEN: wire /tel caller-ID mint into StasisStart + gate unlock** - `91c09a0` (feat)

**Plan metadata:** (this commit)

## Files Created/Modified

- `apps/voice/src/klanker_voice/call_runtime.py` — `CallIdentity` gains `tier_id`/`caller_id`/`did` (additive, default `None`)
- `apps/voice/src/klanker_voice/telephony/config.py` — `TelephonyConfig.tel_mint_url` / `tel_mint_env_var` + `load_telephony_config` parsing
- `apps/voice/src/klanker_voice/telephony/controller.py` — `_normalize_e164`, `_fetch_tel_token`, `AsteriskCallController._mint_tier_from_caller_id`, `ActiveCall.grant_tier_id`, `_finish_stasis_start_gated`/`_gate_unlock` rewiring
- `apps/voice/tests/test_telephony_controller.py` — new file: mint-success/mint-failure/no-caller-id/legacy-fallback/bearer-token-grep tests (5 tests)
- `apps/voice/tests/test_telephony_config.py` — new tests for the two `tel_mint_*` fields + a Phase-12-shaped credential-field rejection regression (5 tests)

## Decisions Made

- `tel_mint_url` empty is the explicit "mint disabled" signal, not a separate boolean flag — keeps `TelephonyConfig`'s surface minimal and makes the opt-in self-evident from one field.
- The mint failure fail-closed path reuses the SAME pipeline the gate itself builds (rather than constructing a lighter-weight goodbye-only path) so the existing `_gate_fail_closed`/`speak_goodbye`/`_close_active_call` machinery is reused verbatim — zero new teardown code, zero risk of a second, subtly different close path.
- `_gate_unlock` treats `active_call.grant_tier_id is None` as a hard "never grant" guard, not just an initial-state default — this closes the narrow async race between a mint-failure fail-closed teardown starting and a DTMF/passphrase match still landing during its grace period.

## Deviations from Plan

None — plan executed exactly as written. The one implementation judgment call (URL-encoding the `+` in the normalized E.164 caller ID via `urllib.parse.quote`) was verified against the 12-02 `/tel` route's own `decodeURIComponent(e164)` call, confirming the encode/decode pair is correct, not a deviation.

## Issues Encountered

None.

## User Setup Required

None — no external service configuration required. Live SSM wiring for `TELEPHONY_ENDPOINT_AUTH_TOKEN` and the real `tel_mint_url` value are 12-07 (deploy) concerns.

## Next Phase Readiness

- The controller-side half of the §23 caller-ID mint is complete and unit-tested; ready for 12-07 to wire `telephony.tel_mint_url` / the `TELEPHONY_ENDPOINT_AUTH_TOKEN` SSM secret into the deployed `telephony-edge` task definition.
- 12-08's manual cellular proof can now exercise the full mapped/unmapped caller-ID flow end-to-end once the edge is deployed and DNS/SSM are wired.
- No blockers for the next plan in this phase.

## Self-Check: PASSED

- All 5 key files verified present on disk (`[ -f ]`).
- All 3 task commits (`03227ee`, `58aec68`, `91c09a0`) verified in `git log`.
- All task-level `<acceptance_criteria>` re-run and passing.
- Plan-level `<verification>` re-run: `cd apps/voice && python -m pytest tests/test_telephony_controller.py tests/test_telephony_config.py` → 22/22 passed; `git diff --stat` for `gate.py`/`pipeline.py`/`session.py`/`media.py`/`transport.py` empty; fail-closed proven to skip `quota.start_gate` (both mint-failure tests assert zero calls via an `AssertionError`-raising spy).
- Full project suite re-run: `cd apps/voice && python -m pytest tests/` → 421 passed, 53 skipped, 0 failed.

---
*Phase: 12-voip-ms-telephony-inbound-did*
*Completed: 2026-07-12*
