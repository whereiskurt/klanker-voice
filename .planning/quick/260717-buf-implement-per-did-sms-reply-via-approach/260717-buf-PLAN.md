---
phase: 260717-buf-implement-per-did-sms-reply-via-approach
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
  - apps/voice/configs/telephony.toml
  - apps/voice/src/klanker_voice/telephony/config.py
  - apps/voice/src/klanker_voice/telephony/controller.py
  - apps/voice/asterisk/extensions.conf
  - apps/voice/tests/test_telephony_config.py
  - apps/voice/tests/test_telephony_controller.py
  - apps/voice/tests/test_asterisk_configs.py
autonomous: true
must_haves:
  truths:
    - "On a live call to a Vegas DID, the CALLERID(name) prefix resolves dialed_did → OTP text sent FROM that DID."
    - "An unresolved dialed_did sends NO text (sms_dids pool emptied → 613 reserved)."
  artifacts:
    - apps/voice/configs/telephony.toml
    - apps/voice/src/klanker_voice/telephony/config.py
    - apps/voice/src/klanker_voice/telephony/controller.py
---

<objective>
Approach C (VoIP.ms Caller ID name prefix — CONFIRMED live 2026-07-17): resolve the dialed DID
from a per-DID tag in CALLERID(name) instead of the dead SIP To: header, so the 2 Vegas DIDs
(7254043234/7254043283) text the OTP FROM the dialed number; 613 reserved (no text). Edge code +
config only; VoIP.ms prefix changes / deploy / live call / #67 cleanup are separate steps.
</objective>

<tasks>
<task type="auto"><name>Task 1: config — cid_prefix→DID map + empty sms_dids</name>
<files>apps/voice/configs/telephony.toml, apps/voice/src/klanker_voice/telephony/config.py, apps/voice/tests/test_telephony_config.py</files>
<action>
- telephony.toml: add `[telephony.cid_prefix_dids]` table with `KVD3234 = "7254043234"`, `KVD3283 = "7254043283"` (with a comment: VoIP.ms per-DID Caller ID name prefix rides in CALLERID(name); keys matched anchored at the START of CALLERID(name)). Set `sms_dids = []` (was ["6134805878"]) to reserve 613 (unresolved dialed_did → no text). Update the sms_reply_dids comment to note resolution is now via CALLERID(name) prefix (To: header stays as a dead fallback).
- config.py: add `cid_prefix_did_map: dict[str, str] = field(default_factory=dict)` to TelephonyConfig; add `_parse_cid_prefix_dids(raw)` mirroring `_parse_subaccount_dids` (table → {tag: bare-digit-DID}, absent→{}, non-table→ConfigError, key stripped, value digits-normalized); wire `cid_prefix_did_map=_parse_cid_prefix_dids(table.get("cid_prefix_dids"))` into load_telephony_config.
- test_telephony_config.py: test the map parses, and that the shipped telephony.toml has sms_dids empty + the 2 tags.
</action>
<verify><automated>cd apps/voice && uv run pytest tests/test_telephony_config.py -q</automated></verify>
<done>cid_prefix_did_map loads {KVD3234:7254043234, KVD3283:7254043283}; sms_dids empty; config tests pass.</done>
</task>

<task type="auto"><name>Task 2: controller — CALLERID(name) parser + wiring + probe trim</name>
<files>apps/voice/src/klanker_voice/telephony/controller.py, apps/voice/tests/test_telephony_controller.py</files>
<action>
- Add pure `_dialed_did_from_cidname(cidname, prefix_map)`: iterate prefix keys longest-first; if `cidname` (stripped) startswith a key (exact, or `key` followed by a non-alnum boundary / space for the prepended-CNAM case) → return mapped DID; else "". Never raises.
- In on_stasis_start: read cidname from KLANKER_SIP_CIDNAME; resolve `dialed_did = subaccount_did_map.get(did,"") or _dialed_did_from_cidname(cidname, cfg.cid_prefix_did_map) or _dialed_did_from_sip_to(sip_to)`. Keep the dialed_did log line (add cidname).
- Trim probe: remove the KLANKER_SIP_PCPID/DIVERSION/RPID/CONTACT/DNID/FROM reads + the SIP-HEADER-PROBE log line. Keep the KLANKER_SIP_CIDNAME read (now used).
- test_telephony_controller.py: unit-test the parser (exact tag, tag+" CNAM", no match, empty, unknown tag); keep existing tests green.
</action>
<verify><automated>cd apps/voice && uv run pytest tests/test_telephony_controller.py tests/test_telephony_sms.py tests/test_telephony_lifecycle.py -q</automated></verify>
<done>dialed_did resolves from CALLERID(name); probe log removed; suites pass.</done>
</task>

<task type="auto"><name>Task 3: dialplan probe trim + config lint</name>
<files>apps/voice/asterisk/extensions.conf, apps/voice/tests/test_asterisk_configs.py</files>
<action>
- extensions.conf: remove the 6 diagnostic Set() lines (PCPID/DIVERSION/RPID/CONTACT/DNID/FROM). KEEP KLANKER_SIP_TO and KLANKER_SIP_CIDNAME (=${CALLERID(name)}) before Stasis. Rewrite the comment: CALLERID(name) carries the per-DID prefix → resolves the dialed DID (Approach C); To: kept as dead fallback. Still zero Dial(), one context.
- test_asterisk_configs.py: replace the 7-capture probe test with one asserting KLANKER_SIP_CIDNAME (CALLERID(name)) + KLANKER_SIP_TO captures exist before Stasis. Keep no-Dial/one-context invariant.
</action>
<verify><automated>cd apps/voice && uv run pytest tests/test_asterisk_configs.py -q</automated></verify>
<done>Only To: + CIDNAME captured before Stasis; lint green.</done>
</task>
</tasks>

<verification>
cd apps/voice && uv run pytest tests/test_asterisk_configs.py tests/test_telephony_controller.py tests/test_telephony_sms.py tests/test_telephony_config.py tests/test_telephony_lifecycle.py -q  → all pass.
</verification>

<output>
Create .planning/quick/260717-buf-implement-per-did-sms-reply-via-approach/260717-buf-SUMMARY.md when done.
</output>
