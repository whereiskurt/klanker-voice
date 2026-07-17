---
phase: 260716-wgz-diagnostic-capture-all-candidate-sip-hea
plan: 01
status: complete
date: 2026-07-17
commits:
  - 254891b  # extensions.conf: stash 5 more candidate SIP headers before Stasis
  - d9b4111  # controller.py: log 5 candidate headers in on_stasis_start
  - 642bd7b  # test_asterisk_configs.py: assert the 5 captures before Stasis
---

# Quick Task 260716-wgz — SIP-header diagnostic (per-did-sms-reply-v2 Step 1)

## Goal
Find which inbound SIP header (if any) carries the **dialed DID** on the shared
`klanker-pbx` VoIP.ms sub-account. The `To:` header only ever carries the
shared sub-account name (`557010_klanker-pbx`), live-proven — so we probe five
more candidate values. Pure observe-only instrumentation; **zero routing/gate/
SMS behavior change → zero outage risk.**

## What changed
1. **`apps/voice/asterisk/extensions.conf`** — after the existing
   `Set(KLANKER_SIP_TO=…)` and before `Stasis(klanker)`, five new read-only
   captures:
   - `KLANKER_SIP_PCPID`     = `${PJSIP_HEADER(read,P-Called-Party-ID)}`
   - `KLANKER_SIP_DIVERSION` = `${PJSIP_HEADER(read,Diversion)}`
   - `KLANKER_SIP_RPID`      = `${PJSIP_HEADER(read,Remote-Party-ID)}`
   - `KLANKER_SIP_CONTACT`   = `${PJSIP_HEADER(read,Contact)}`
   - `KLANKER_SIP_DNID`      = `${CALLERID(dnid)}` (dialplan function, not a header)
   Security-posture comment extended; no `Dial(`, no new context → T-11-02-01
   invariants (`test_extensions_conf_has_no_dial_and_one_context`) still green.
2. **`apps/voice/src/klanker_voice/telephony/controller.py`** — in
   `on_stasis_start`, after the existing `KLANKER_SIP_TO` read, read the five
   vars and emit ONE greppable `on_stasis_start SIP-HEADER-PROBE: …` INFO line.
   `dialed_did` / gate / `_select_sms_send_dids` untouched.
3. **`apps/voice/tests/test_asterisk_configs.py`** — new positive-grep test
   `test_extensions_conf_captures_candidate_sip_headers` asserting all 5
   captures exist and precede `Stasis(`.

## Verification
- `uv run pytest tests/test_asterisk_configs.py tests/test_telephony_controller.py -q` → 33 passed.
- Full telephony+asterisk suite → **227 passed, 0 failed**.

## NOT done (separate, human-gated)
- **Deploy**: merge `apps/voice/**` to `main` → `build-telephony-edge.yml` builds/deploys telephony-edge. (Reminder from memory: voice/auth CI deploys revert telephony-edge to an older image — watch for that; restore `:N` after.)
- **Live capture**: someone dials one `klanker-pbx` DID; then grep CloudWatch
  log group `/ecs/telephony-edge-telephony-edge-use1-kmv` for `SIP-HEADER-PROBE`
  to read the five values.

## Next step (branches on the result)
- **DID appears in a probed header** → trivial: point dialed-DID resolution at
  that header, reuse `_select_sms_send_dids`/`sms_reply_dids`. Then remove this
  probe instrumentation.
- **DID in none** → v2 Step 2 (SIP-URI routing to a static inbound IP).
- Parallel: v2 Step 3 VoIP.ms support ticket (operator-owned).
