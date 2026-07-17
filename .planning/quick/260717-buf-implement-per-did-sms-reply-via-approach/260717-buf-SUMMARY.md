---
phase: 260717-buf-implement-per-did-sms-reply-via-approach
plan: 01
status: complete
date: 2026-07-17
commits:
  - d969b43  # config: cid_prefix_dids map + empty sms_dids
  - f02f43e  # controller: _dialed_did_from_cidname parser + wiring + probe removal
  - 747eee3  # dialplan: trim probe to To:+CALLERID(name); config-lint
---

# Quick Task 260717-buf — per-DID SMS reply via Approach C

## Goal
Make the 2 Las Vegas DIDs (7254043234/7254043283) text the CTF OTP **FROM the number dialed**,
using VoIP.ms's per-DID **Caller ID name prefix** (Approach C, live-confirmed 2026-07-17) —
the prefix rides in `CALLERID(name)`. 613 reserved. **No infra, no routing change, no sub-accounts.**

## What changed (edge code + config only)
1. **config** (`telephony.toml`, `config.py`, `test_telephony_config.py`) — new
   `[telephony.cid_prefix_dids]` table (`KVD3234→7254043234`, `KVD3283→7254043283`) +
   `TelephonyConfig.cid_prefix_did_map` / `_parse_cid_prefix_dids` (mirrors `subaccount_did_map`).
   Emptied `sms_dids` (`["6134805878"]`→`[]`) so an unresolved DID sends **no text** (613 reserved).
2. **controller** (`controller.py`, `test_telephony_sms.py`) — `_dialed_did_from_cidname(cidname, map)`
   parses the tag at the START of `CALLERID(name)` (exact, or prepended-to-CNAM at a non-alnum
   boundary, longest-tag-first). `on_stasis_start` resolves `dialed_did = subaccount_map OR
   cidname-prefix OR To:-header`. Removed the 260716-wgz diagnostic probe; kept the cidname read.
3. **dialplan** (`extensions.conf`, `test_asterisk_configs.py`) — trimmed to two captures:
   `KLANKER_SIP_TO` (dead fallback) + `KLANKER_SIP_CIDNAME` (`=CALLERID(name)`, live path).

`_select_sms_send_dids` / `sms_reply_dids` unchanged (the 2 Vegas DIDs already enrolled).

## Verification
- Named suites: **145 passed**. Broad `-k "telephony or asterisk"`: **242 passed, 0 failed**.

## NOT done here (orchestrator / human-gated — see below)
- VoIP.ms `callerid_prefix` on the 2 live DIDs (`KVD3234` / `KVD3283`) — orchestrator sets via
  setDIDInfo (snapshot+verify+rollback). 7254043234 currently holds the `KVDIDTEST` test tag.
- Deploy (merge `apps/voice/**` → main).
- Live confirmation calls (each Vegas DID should text from itself; a 613 call → no text).
- Cleanup of the dead #67 vegas sub-account scaffolding (separate follow-up task).
