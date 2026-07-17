---
phase: 260717-mc2-retire-dead-67-vegas-per-did-sub-account
plan: 01
status: complete
date: 2026-07-17
commits:
  - c5756bc  # code+config+tests: remove sub-account scaffolding
  - cb651ca  # infra: drop vegas SIP secret injections
---

# Quick Task 260717-mc2 — retire dead #67 Vegas sub-account scaffolding

## Goal
Approach C (CALLERID name prefix, #71) replaced the #67 Option-A per-DID VoIP.ms sub-account
approach entirely. Remove the now-INERT scaffolding (no DID routes to a per-DID sub-account).

## Removed (code / config / infra-source)
- **pjsip.conf** — the 4 `[voipms-{auth,registration}-vegas{3234,3283}]` sections + their comment
  block (replaced with a short note explaining why they're gone). klanker-pbx trunk untouched.
- **config.py** — `TelephonyConfig.subaccount_did_map` field, `_parse_subaccount_dids`, and its
  call in `load_telephony_config`.
- **controller.py** — the `subaccount_did_map.get(did, "")` resolution term; `on_stasis_start`
  now resolves `dialed_did = _dialed_did_from_cidname(...) or _dialed_did_from_sip_to(...)`.
- **telephony.toml** — the `[telephony.subaccount_dids]` table + comment.
- **service.hcl** — the 4 `VOIPMS_SIP_{USERNAME,PASSWORD}_VEGAS{3234,3283}` container secrets.
- **tests** — the 6 dead sub-account tests (asterisk-config registration, 4 config-parse, lifecycle
  resolution). Every Approach-C / cid_prefix test kept.

## Kept deliberately
- `entrypoint.sh` vegas `unset` lines — harmless no-ops that still scrub the vegas SIP creds from
  the controller env during the deploy/terragrunt-apply window (D-09). Dropped in a follow-up
  after the task-def stops injecting them.
- Live klanker-pbx trunk (registration/auth/endpoint/identify/aor) + base SIP secret.

## Verification
- `git grep subaccount` over src/tests/configs → clean. `grep VEGAS service.hcl` → 0.
- Full telephony+asterisk suite: **236 passed, 0 failed**.

## Orchestrator operational teardown (post-merge, safe order)
1. Merge → `build-telephony-edge.yml` deploys the new image (no vegas pjsip/config).
2. terragrunt apply the telephony-edge service unit → task-def drops the 4 vegas secret injections.
3. Verify new task healthy + registration `yes`.
4. Delete the 4 SSM params `/kmv/secrets/use1/voipms/sip_{username,password}_vegas{3234,3283}`.
5. Delete the 2 VoIP.ms sub-accounts vegas3234 / vegas3283 (`delSubAccount`).
6. Tiny follow-up commit removing the now-no-op entrypoint.sh vegas unsets.
