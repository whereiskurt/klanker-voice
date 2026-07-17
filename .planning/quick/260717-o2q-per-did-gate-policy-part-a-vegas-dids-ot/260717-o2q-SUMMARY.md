---
phase: 260717-o2q
plan: 01
subsystem: telephony
tags: [pipecat, asterisk-ari, voipms, gate, otp, per-did]

requires:
  - phase: 260717-buf (per-DID SMS reply via CID-name-prefix, Approach C)
    provides: "telephony.cid_prefix_did_map + dialed_did resolution in on_stasis_start"
provides:
  - "TelephonyConfig.otp_only_dids (additive per-DID gate policy config)"
  - "GateProcessor.concierge_unlock_enabled flag (suppresses passphrase + DTMF PIN)"
  - "ActiveCall.otp_only + on_stasis_start/DTMF-handler threading"
affects: [per-did-gate-policy, kv-cid-prefix-tooling, ctf-phone-otp]

tech-stack:
  added: []
  patterns:
    - "Per-DID behavior policy resolved once (dialed_did in config set) before pipeline construction, threaded as a boolean flag through constructor kwargs"

key-files:
  created: []
  modified:
    - apps/voice/configs/telephony.toml
    - apps/voice/src/klanker_voice/telephony/config.py
    - apps/voice/src/klanker_voice/telephony/gate.py
    - apps/voice/src/klanker_voice/telephony/controller.py
    - apps/voice/tests/test_telephony_config.py
    - apps/voice/tests/test_telephony_gate.py
    - apps/voice/tests/test_telephony_lifecycle.py

key-decisions:
  - "concierge_unlock_enabled suppression enforced at BOTH layers (GateProcessor.unlock no-ops for passphrase/dtmf AND process_frame skips accumulation) so no single missed path can re-open concierge access on a Vegas DID"
  - "cancel_for_takeover left completely untouched -- the 333266 announcement takeover and the fail-closed timer are architecturally independent of concierge_unlock_enabled"
  - "otp_only resolved once in on_stasis_start from the same dialed_did Approach C already computes -- no new resolution mechanism"

patterns-established:
  - "Additive per-DID policy: empty otp_only_dids set is byte-identical to pre-change behavior; unresolved dialed_did (\"\") is never in the set and always defaults to the less-restrictive concierge bucket"

requirements-completed: [PERDID-A]

coverage:
  - id: D1
    description: "otp_only_dids config field: absent defaults to (), digit-normalizes + order-preserves when set, rejects non-list values, shipped telephony.toml seeds the 2 Vegas DIDs"
    requirement: "PERDID-A"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_config.py::test_otp_only_dids_absent_defaults_empty"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_config.py::test_otp_only_dids_parses_and_normalizes"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_config.py::test_otp_only_dids_non_list_rejected"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_config.py::test_shipped_telephony_toml_seeds_both_vegas_otp_only_dids"
        status: pass
    human_judgment: false
  - id: D2
    description: "GateProcessor concierge_unlock_enabled=False suppresses passphrase match AND explicit unlock(\"passphrase\")/unlock(\"dtmf\"); cancel_for_takeover (333266) stays fully functional; default True is byte-identical"
    requirement: "PERDID-A"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_gate.py::test_concierge_disabled_passphrase_never_unlocks"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_gate.py::test_concierge_disabled_explicit_unlock_passphrase_and_dtmf_are_noops"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_gate.py::test_concierge_disabled_cancel_for_takeover_still_resolves"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_gate.py::test_concierge_enabled_default_true_byte_identical"
        status: pass
    human_judgment: false
  - id: D3
    description: "End-to-end: a call resolving to a Vegas DID ignores concierge passphrase + PIN (no quota.start_gate/greet_now), 333266 still dispatches _gate_announcement; a call with unresolved dialed_did stays byte-identical to today's concierge unlock"
    requirement: "PERDID-A"
    verification:
      - kind: integration
        ref: "apps/voice/tests/test_telephony_lifecycle.py::test_otp_only_did_passphrase_is_inert"
        status: pass
      - kind: integration
        ref: "apps/voice/tests/test_telephony_lifecycle.py::test_otp_only_did_concierge_pin_is_inert"
        status: pass
      - kind: integration
        ref: "apps/voice/tests/test_telephony_lifecycle.py::test_otp_only_did_announcement_code_still_dispatches"
        status: pass
      - kind: integration
        ref: "apps/voice/tests/test_telephony_lifecycle.py::test_non_otp_only_did_stays_byte_identical"
        status: pass
    human_judgment: true
    rationale: "The live PSTN behavior (real Vegas-DID call, real 333266 DTMF entry against a deployed telephony-edge) has not been exercised against real Asterisk/ARI -- only the hermetic fake-ARI/fake-media lifecycle harness. A live-call verification is a natural follow-on before this policy is trusted in production."

duration: 35min
completed: 2026-07-17
status: complete
---

# Quick Task 260717-o2q: Per-DID Gate Policy Part A (Vegas DIDs OTP-only) Summary

**The two Las Vegas DIDs now suppress the concierge passphrase/PIN entirely (OTP-only via `otp_only_dids`), while every other DID stays byte-identical to today's concierge + OTP behavior, and the global 333266 announcement + fail-closed timer are untouched on both.**

## Performance

- **Duration:** ~35 min
- **Tasks:** 3/3 completed
- **Files modified:** 7 (4 source + 3 test files, config.py/gate.py/controller.py/telephony.toml, and their matching test files)

## Accomplishments
- Added an additive `otp_only_dids: tuple[str, ...] = ()` field to `TelephonyConfig` with a dedicated `_parse_otp_only_dids` parser (digit-normalized, order-preserved, mirrors `_parse_sms_dids`); shipped `configs/telephony.toml` now seeds the two Vegas DIDs (`7254043234`, `7254043283`).
- `GateProcessor` gained a `concierge_unlock_enabled: bool = True` constructor flag. When `False`, `process_frame` skips passphrase tokenization/accumulation entirely (never even builds a token set toward a match), and `unlock("passphrase")`/`unlock("dtmf")` both become no-ops. `cancel_for_takeover` (the 333266 announcement takeover) and the fail-closed timer are completely unmodified code paths, so they still resolve the gate on an OTP-only DID exactly as before.
- `ActiveCall` gained an `otp_only: bool = False` field. `on_stasis_start` resolves it (`dialed_did in telephony_cfg.otp_only_dids`) right after `dialed_did` is computed and threads it into `_finish_stasis_start_gated`, which builds the `GateProcessor` with `concierge_unlock_enabled=not otp_only` and records `otp_only` on the `ActiveCall`. `on_channel_dtmf_received` skips the PIN accumulate/match branch when `active_call.otp_only` is True but always still appends to `dtmf_raw` and runs the announcement-code loop, so 333266 keeps working.
- 4 new config tests, 4 new gate tests, 4 new end-to-end lifecycle tests (23 total in that file, up from 19) -- all proving both the OTP-only suppression and the byte-identical concierge path for every other DID.

## Task Commits

Each task was committed atomically (code + tests together, per explicit task instruction):

1. **Task 1: otp_only_dids config field + parser + shipped-toml seed** - `37a344e` (feat)
2. **Task 2: suppress concierge unlock per-DID (gate flag + controller threading)** - `e409fc4` (feat)
3. **Task 3: end-to-end per-DID behavior tests (OTP-only vs concierge)** - `a861a93` (test)

**Plan metadata:** commit pending (docs: complete plan, made by the orchestrator per this run's constraints)

## Files Created/Modified
- `apps/voice/src/klanker_voice/telephony/config.py` - `otp_only_dids` field + `_parse_otp_only_dids` + wiring into `load_telephony_config`
- `apps/voice/configs/telephony.toml` - seeds `otp_only_dids = ["7254043234", "7254043283"]`
- `apps/voice/src/klanker_voice/telephony/gate.py` - `GateProcessor(concierge_unlock_enabled=True)`, `unlock()`/`process_frame()` suppression logic
- `apps/voice/src/klanker_voice/telephony/controller.py` - `ActiveCall.otp_only`, `on_stasis_start` resolution, `_finish_stasis_start_gated(otp_only=...)`, `on_channel_dtmf_received` PIN-branch skip
- `apps/voice/tests/test_telephony_config.py` - 4 new tests for `otp_only_dids`
- `apps/voice/tests/test_telephony_gate.py` - 4 new tests for `concierge_unlock_enabled`
- `apps/voice/tests/test_telephony_lifecycle.py` - 4 new end-to-end lifecycle tests

## Decisions Made
- Suppression enforced at BOTH the `unlock()` no-op AND the `process_frame` accumulation-skip layers in `GateProcessor`, per the plan's own T-O2Q-01 mitigation instruction, so no single missed code path could re-open concierge access on a Vegas DID.
- `cancel_for_takeover` left completely untouched (not even a defensive check added) -- it resolves the gate via a wholly separate code path from `unlock()`, and the plan explicitly required it stay byte-identical.

## Deviations from Plan

None - plan executed exactly as written. All 5 CRITICAL invariants named in the task prompt were verified:
1. `otp_only_dids` empty ⇒ byte-identical to today (proven by every pre-existing test in the full telephony suite staying green, unmodified).
2. `cancel_for_takeover` stays UNTOUCHED (zero lines changed in that method; `test_otp_only_did_announcement_code_still_dispatches` proves 333266 still fires on an OTP-only DID).
3. 333266 OTP stays global (the announcement-code loop in `on_channel_dtmf_received` runs unconditionally regardless of `otp_only`).
4. Unresolved `dialed_did` (`""`) ⇒ default concierge bucket (`"" in otp_only_dids` is always `False`; proven by `test_non_otp_only_did_stays_byte_identical`).
5. The existing `_gate_fail_closed` timer is reused with no new copy (no changes made to `_gate_fail_closed`, `GATE_FAIL_CLOSED_COPY`, or the timer mechanism at all).

## Issues Encountered
None.

## User Setup Required
None - no external service configuration required. The shipped `configs/telephony.toml` seed is code-only; no VoIP.ms portal change is needed for Part A (the CID-name-prefix tags `KVD3234`/`KVD3283` this feature keys off of were already configured live in the prerequisite 260717-buf quick task).

## Next Phase Readiness
Part A (this task) is code-complete and unit/integration-tested (229/229 telephony suite green, including all 12 new tests). Parts B (kv CID-prefix tooling) and C (kv studio surface) are explicit follow-ons per the design spec and were NOT touched here. A live-call verification against the deployed telephony-edge (dialing a real Vegas DID, confirming the passphrase/PIN genuinely do nothing and 333266 still reads) is the natural next step before this policy governs live traffic — flagged as `human_judgment: true` in the D3 coverage entry above, not self-approved.

---
*Quick task: 260717-o2q*
*Completed: 2026-07-17*

## Self-Check: PASSED

All 7 modified source/test files verified present on disk; all 3 task commit hashes (`37a344e`, `e409fc4`, `a861a93`) verified present in git history.
