---
phase: 12-voip-ms-telephony-inbound-did
plan: 04
subsystem: infra
tags: [asterisk, pjsip, voip.ms, sip-trunk, registration-trunking]

requires:
  - phase: 11-voip-ms-telephony-local-asterisk-edge
    provides: apps/voice/asterisk/pjsip.conf (transport-udp + dev-softphone endpoint), extensions.conf (from-klanker-inbound Stasis context), render_configs.py (${VAR} placeholder substitution into gitignored .rendered/)
provides:
  - VoIP.ms registration-based SIP trunk sections in pjsip.conf (voipms-auth/voipms-registration/voipms-aor/voipms-endpoint/voipms-identify)
  - Structural-lint tests proving the trunk is inbound-only, ulaw-only, and never carries a committed secret
affects: [12-05, 12-06, 12-07, 12-08]

tech-stack:
  added: []
  patterns:
    - "Registration-based PJSIP trunking (outbound REGISTER to a single Toronto POP; no public inbound SIP port)"
    - "Per-section structural-lint parsing (_sections() groups a .conf file's stripped lines by bracket header, scoping assertions to one endpoint without a full PJSIP parser)"

key-files:
  created: []
  modified:
    - apps/voice/asterisk/pjsip.conf
    - apps/voice/tests/test_asterisk_configs.py

key-decisions:
  - "VoIP.ms endpoint is its own standalone concrete section (not a template application of the dev-softphone [softphone] template) so the trunk's config is self-contained and independently auditable"
  - "type=identify matches all 8 Toronto POP IPs (not just the registration POP) since VoIP.ms may deliver inbound traffic from any POP in the Toronto cluster (12-RESEARCH.md pitfall)"
  - "Widened the pre-existing file-wide ulaw-only test from 'exactly one allow= line' to 'every allow= line is allow=ulaw' -- the new legitimate second (VoIP.ms) endpoint breaks the old single-line assumption; the security invariant (no non-ulaw codec anywhere) is unchanged and still mechanically enforced"

requirements-completed: [D-01, D-04, SC-1]

coverage:
  - id: D1
    description: "VoIP.ms registration-based trunk (auth/registration/aor/endpoint/identify) added to pjsip.conf, registering outbound to a single Toronto POP with no public inbound SIP port"
    requirement: "D-01"
    verification:
      - kind: unit
        ref: "tests/test_asterisk_configs.py::TestVoipmsTrunkPosture::test_voipms_registration_section_exists_and_targets_toronto"
        status: pass
      - kind: unit
        ref: "tests/test_asterisk_configs.py::TestVoipmsTrunkPosture::test_voipms_aor_max_contacts_one"
        status: pass
      - kind: unit
        ref: "tests/test_asterisk_configs.py::TestVoipmsTrunkPosture::test_voipms_identify_routes_to_endpoint"
        status: pass
    human_judgment: false
  - id: D2
    description: "VoIP.ms endpoint is inbound-only (context=from-klanker-inbound, the only dialplan context) and ulaw-only (disallow=all/allow=ulaw), matching Phase 10/11's codec commitment"
    requirement: "D-01"
    verification:
      - kind: unit
        ref: "tests/test_asterisk_configs.py::TestVoipmsTrunkPosture::test_voipms_endpoint_context_is_inbound_only"
        status: pass
      - kind: unit
        ref: "tests/test_asterisk_configs.py::TestVoipmsTrunkPosture::test_voipms_endpoint_is_ulaw_only"
        status: pass
      - kind: unit
        ref: "tests/test_asterisk_configs.py::TestPjsipConfUlawOnly::test_pjsip_conf_is_ulaw_only"
        status: pass
    human_judgment: false
  - id: D3
    description: "VoIP.ms SIP password is a ${VAR} placeholder in the tracked pjsip.conf (never a literal secret), rendered from the environment only into the gitignored .rendered/ dir at container start"
    requirement: "D-04"
    verification:
      - kind: unit
        ref: "tests/test_asterisk_configs.py::TestVoipmsTrunkPosture::test_voipms_auth_password_is_placeholder_not_literal"
        status: pass
      - kind: unit
        ref: "tests/test_asterisk_configs.py::TestVoipmsTrunkPosture::test_voipms_render_substitutes_env_secrets"
        status: pass
      - kind: unit
        ref: "tests/test_asterisk_configs.py::TestVoipmsTrunkPosture::test_voipms_render_leaves_placeholder_when_env_unset"
        status: pass
    human_judgment: false

duration: 20min
completed: 2026-07-12
status: complete
---

# Phase 12 Plan 04: VoIP.ms Registration Trunk in pjsip.conf Summary

**Registration-based VoIP.ms SIP trunk in pjsip.conf (voipms-auth/registration/aor/endpoint/identify, ulaw-only, context-locked to from-klanker-inbound) plus 8 new structural-lint tests proving the D-01/D-04 security posture mechanically.**

## Performance

- **Duration:** 20 min
- **Started:** 2026-07-12T19:19:00Z (approx)
- **Completed:** 2026-07-12T19:39:41Z
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments
- `pjsip.conf` gained a self-contained VoIP.ms registration trunk: `[voipms-auth]` (type=auth, `${VOIPMS_SIP_USERNAME}`/`${VOIPMS_SIP_PASSWORD}` placeholders), `[voipms-registration]` (outbound REGISTER to `sip:toronto.voip.ms`, retry_interval=300, expiration=3600), `[voipms-aor]` (max_contacts=1), `[voipms-endpoint]` (context=from-klanker-inbound only, disallow=all/allow=ulaw, direct_media=no, NAT-tolerant), and `[voipms-identify]` (matches all 8 Toronto POP IPs)
- Confirmed `render_configs.py` needs no code change: it already substitutes any `${VAR}` from `os.environ` wholesale via `string.Template.safe_substitute` with no explicit enumerated var set to extend — proved this with both an env-set render (substitutes) and an env-unset render (leaves the literal placeholder, never crashes/empties)
- 8 new structural-lint tests (`TestVoipmsTrunkPosture`) mechanically enforce: registration targets a Toronto POP, endpoint context is `from-klanker-inbound`, endpoint allows exactly `ulaw`, aor `max_contacts=1`, identify routes to the endpoint, auth password is a placeholder never a literal, and render substitutes/leaves-placeholder correctly
- Proved all three new security invariants genuinely bite by temporarily mutating the config (non-ulaw codec, wrong context, literal password) and re-running the affected test, then reverting — documented in the test class docstring per this repo's existing proof convention

## Task Commits

Each task was committed atomically:

1. **Task 1: VoIP.ms registration trunk sections in pjsip.conf + render extension** - `96a58e9` (feat)
2. **Task 2: structural-lint tests for the VoIP.ms trunk posture** - `225be0c` (test)

**Plan metadata:** (this commit)

## Files Created/Modified
- `apps/voice/asterisk/pjsip.conf` - added the VoIP.ms registration trunk (5 new sections) after the existing dev-softphone sections
- `apps/voice/tests/test_asterisk_configs.py` - added `_sections()`/`_load_render_configs()` helpers, `TestVoipmsTrunkPosture` (8 tests), and widened `TestPjsipConfUlawOnly.test_pjsip_conf_is_ulaw_only`'s assertion

## Decisions Made
- VoIP.ms endpoint is a standalone concrete section, not a template application of the Phase-11 `[softphone]` dev template — the production trunk's config stays self-contained and independently auditable rather than inheriting from a dev-harness template
- `type=identify` matches all 8 Toronto POP IPs (not just the single registration-target POP), per 12-RESEARCH.md's documented pitfall that VoIP.ms may deliver inbound traffic from any POP in the Toronto cluster even when registration targets one specific host — this mirrors the 12-07 SG allow-list's own 8-IP list
- Widened the pre-existing `test_pjsip_conf_is_ulaw_only` from "exactly one `allow=` line in the whole file" to "every `allow=` line in the file is `allow=ulaw`" — see Deviations below

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed a now-incorrect pre-existing test assertion (file-wide single-`allow=`-line assumption)**
- **Found during:** Task 2 (writing the VoIP.ms-scoped codec tests)
- **Issue:** The Phase-11 `test_pjsip_conf_is_ulaw_only` asserted `allow_lines == ["allow=ulaw"]` across the *entire file* — true when there was exactly one endpoint (dev-softphone). Adding the VoIP.ms trunk's own legitimate `disallow=all`/`allow=ulaw` pair (its own endpoint, its own codec declaration) produces a second `allow=ulaw` line, which the old assertion would reject even though the config is correct — a false-positive test failure that would have blocked every future build after this plan's own Task 1 commit.
- **Fix:** Widened the assertion to `all(line == "allow=ulaw" for line in allow_lines)` — still zero-tolerance for any codec other than ulaw anywhere in the file (the actual security invariant), just no longer assumes there's only one endpoint ever declaring it. Added a new section-scoped test (`test_voipms_endpoint_is_ulaw_only`) that independently proves the VoIP.ms endpoint's own section allows exactly one codec line, so per-endpoint precision isn't lost.
- **Files modified:** apps/voice/tests/test_asterisk_configs.py
- **Verification:** Full suite green (411 passed); manually proved both the widened file-wide test AND the new section-scoped test still fail correctly when a non-ulaw codec is injected (proof documented in the class docstring, mutation reverted, not committed)
- **Committed in:** `225be0c` (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (1 Rule 1 bug fix in a pre-existing test)
**Impact on plan:** Necessary correctness fix — without it, this plan's own committed change would have broken CI on the very next run. No scope creep; the fix is scoped entirely to the codec-lint test's assertion logic.

## Issues Encountered
None.

## User Setup Required
None - no external service configuration required. The VoIP.ms SIP credentials themselves (VOIPMS_SIP_USERNAME/VOIPMS_SIP_PASSWORD) are provisioned via SSM as documented in `docs/operators/voipms-provisioning-runbook.md` (12-01) and injected at deploy time (a later plan's Terraform work, not this plan's scope).

## Next Phase Readiness
- The VoIP.ms registration trunk config is complete and structurally proven; ready for 12-05/12-06 (controller wiring + tier composition) and 12-07 (the SG-to-POP lock, the network-level defense-in-depth this trunk's `type=identify` already anticipates with the same 8-IP list) and 12-08 (deploy + manual cellular proof).
- Not yet exercised: an actual live registration against a real VoIP.ms account (no billed VoIP.ms credentials in this session) — deferred to the deploy/live-verification plan(s), same pattern as every prior Phase 11/12 config-only plan's own live-proof deferral.
- No blockers.

---
*Phase: 12-voip-ms-telephony-inbound-did*
*Completed: 2026-07-12*

## Self-Check: PASSED

- FOUND: apps/voice/asterisk/pjsip.conf
- FOUND: apps/voice/tests/test_asterisk_configs.py
- FOUND commit: 96a58e9
- FOUND commit: 225be0c
- Full suite: 411 passed, 53 skipped, 0 failed (`uv run pytest -q`)
