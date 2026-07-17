---
phase: 260716-wgz-diagnostic-capture-all-candidate-sip-hea
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
  - apps/voice/asterisk/extensions.conf
  - apps/voice/src/klanker_voice/telephony/controller.py
  - apps/voice/tests/test_asterisk_configs.py
autonomous: true
requirements:
  - SMS-V2-STEP1
must_haves:
  truths:
    - "On a live inbound call the edge logs a single INFO line dumping all 5 candidate SIP header/function values alongside the existing To: capture."
    - "Routing, gate, dialed_did resolution and SMS-send selection behave byte-identically to before (observe-only change)."
  artifacts:
    - apps/voice/asterisk/extensions.conf
    - apps/voice/src/klanker_voice/telephony/controller.py
    - apps/voice/tests/test_asterisk_configs.py
  key_links:
    - "extensions.conf Set(KLANKER_SIP_*) captures → controller get_channel_var reads → single diagnostic INFO log line"
---

<objective>
Step 1 of the per-did-sms-reply-v2 plan (docs/superpowers/specs/2026-07-17-per-did-sms-reply-v2-plan.md):
PURE DIAGNOSTIC INSTRUMENTATION. VoIP.ms puts only the shared sub-account NAME in the SIP
To: header (live-proven 2026-07-16), so we cannot see the dialed DID there. This task stashes
FIVE additional candidate SIP header/function values into new channel vars before Stasis and
logs them once per call, so one live call to a klanker-pbx DID reveals which header (if any)
actually carries the dialed DID.

Purpose: find the header that carries the dialed DID, without any behavior change.
Output: 5 new read-only channel-var captures + one diagnostic INFO log line + updated config lint tests.
</objective>

<execution_context>
@$HOME/.claude/gsd-core/workflows/execute-plan.md
@$HOME/.claude/gsd-core/templates/summary.md
</execution_context>

<context>
@.planning/STATE.md
@docs/superpowers/specs/2026-07-17-per-did-sms-reply-v2-plan.md
@apps/voice/asterisk/extensions.conf
@apps/voice/src/klanker_voice/telephony/controller.py
@apps/voice/src/klanker_voice/telephony/ari.py
@apps/voice/tests/test_asterisk_configs.py
</context>

<tasks>

<task type="auto">
  <name>Task 1: Stash 5 additional candidate SIP headers into channel vars before Stasis</name>
  <files>apps/voice/asterisk/extensions.conf</files>
  <action>
In the `[from-klanker-inbound]` context, immediately AFTER the existing
`same => n,Set(KLANKER_SIP_TO=${PJSIP_HEADER(read,To)})` line and BEFORE `same => n,Answer()`
(order relative to Answer/Stasis does not matter for reads, but keep them grouped with the
existing To: capture and BEFORE Stasis so the vars persist for the controller), add exactly
these five `same => n,Set(...)` lines:
  - KLANKER_SIP_PCPID   = ${PJSIP_HEADER(read,P-Called-Party-ID)}
  - KLANKER_SIP_DIVERSION = ${PJSIP_HEADER(read,Diversion)}
  - KLANKER_SIP_RPID    = ${PJSIP_HEADER(read,Remote-Party-ID)}
  - KLANKER_SIP_CONTACT = ${PJSIP_HEADER(read,Contact)}
  - KLANKER_SIP_DNID    = ${CALLERID(dnid)}
CRITICAL SYNTAX: KLANKER_SIP_DNID uses the CALLERID(dnid) dialplan FUNCTION, NOT PJSIP_HEADER —
write it as `same => n,Set(KLANKER_SIP_DNID=${CALLERID(dnid)})`. The other four use
${PJSIP_HEADER(read,<HeaderName>)} exactly like the existing To: capture.

Extend the existing security-posture comment block (the paragraph starting "Per-DID SMS reply
(quick task 260716-hg5 follow-up)") to note these five are ADDITIONAL diagnostic READ-only
captures of inbound SIP headers/dialplan functions — they open no outbound path, are not a
Dial()/feature-code, and leave the T-11-02-01 posture unchanged. Keep the existing note that
${PJSIP_HEADER(...)} / ${CALLERID(...)} are Asterisk dialplan syntax and that extensions.conf
is bind-mounted verbatim (render_configs.py does NOT template it), so these are never
${VAR}-substituted at container start.

Do NOT touch pjsip.conf, the vegas sub-account scaffolding, or any routing/gate logic. This
context has zero Dial() calls and must keep exactly one context named from-klanker-inbound —
none of the new lines contain the literal Dial( or a new bracketed context, so the existing
negative-grep lint stays green.
  </action>
  <verify>
    <automated>cd apps/voice && uv run pytest tests/test_asterisk_configs.py -q</automated>
  </verify>
  <done>extensions.conf has 5 new Set(KLANKER_SIP_*) lines (4 PJSIP_HEADER reads + 1 CALLERID(dnid)) placed with the existing To: capture before Stasis; comment extended; still exactly one context and zero Dial() calls; existing config-lint tests pass.</done>
</task>

<task type="auto">
  <name>Task 2: Read the 5 new vars in on_stasis_start and emit one diagnostic INFO log line</name>
  <files>apps/voice/src/klanker_voice/telephony/controller.py</files>
  <action>
In `on_stasis_start` (~line 784), AFTER the existing
`sip_to = await self._ari.get_channel_var(sip_channel_id, "KLANKER_SIP_TO")` read and AFTER
`answer()` (so answer stays the first ARI REST call, matching the existing pattern), read each
of the five new channel vars via `await self._ari.get_channel_var(sip_channel_id, "<VAR>")`
for KLANKER_SIP_PCPID, KLANKER_SIP_DIVERSION, KLANKER_SIP_RPID, KLANKER_SIP_CONTACT, and
KLANKER_SIP_DNID. Each returns "" on unset/missing (get_channel_var never raises).

Add ONE new `logger.info(...)` line dumping all five values with clear labels, e.g. a single
f-string carrying channel=<id> pcpid=... diversion=... rpid=... contact=... dnid=... (use
`or '<none>'` for empties, mirroring the existing dialed_did/sip_to log line so blanks are
readable). Prefix the message so it is greppable in CloudWatch (e.g.
"on_stasis_start SIP-HEADER-PROBE: ..."). Sub-account names + DIDs are PUBLIC per the existing
comments, so logging these raw header values at INFO is safe.

Add a short comment noting this is diagnostic Step 1 of per-did-sms-reply-v2 (observe-only).

DO NOT change dialed_did resolution, `subaccount_did_map` lookup, `_dialed_did_from_sip_to`,
`_select_sms_send_dids`, or any gate/media/SMS logic. This block ONLY reads vars and logs them.
  </action>
  <verify>
    <automated>cd apps/voice && uv run pytest tests/test_telephony_controller.py -q</automated>
  </verify>
  <done>on_stasis_start reads the 5 new channel vars after the existing KLANKER_SIP_TO read and emits one new labelled INFO log line; no dialed_did/gate/SMS logic changed; telephony controller tests pass.</done>
</task>

<task type="auto">
  <name>Task 3: Assert the 5 new header/function captures in the config lint test</name>
  <files>apps/voice/tests/test_asterisk_configs.py</files>
  <action>
In `TestExtensionsConfInboundOnly`, add a new test (e.g.
`test_extensions_conf_captures_candidate_sip_headers`) that mirrors the existing
`test_extensions_conf_captures_dialed_did_before_stasis` style: load `_stripped_lines(EXTENSIONS_CONF)`
and assert each of the five new captures is present as a Set line BEFORE the Stasis line:
  - "Set(KLANKER_SIP_PCPID=" with "PJSIP_HEADER(read,P-Called-Party-ID)"
  - "Set(KLANKER_SIP_DIVERSION=" with "PJSIP_HEADER(read,Diversion)"
  - "Set(KLANKER_SIP_RPID=" with "PJSIP_HEADER(read,Remote-Party-ID)"
  - "Set(KLANKER_SIP_CONTACT=" with "PJSIP_HEADER(read,Contact)"
  - "Set(KLANKER_SIP_DNID=" with "CALLERID(dnid)"
For each, find its line index and assert it exists and is before the Stasis( index (reuse the
`stasis_idx = next((i for i,l in enumerate(lines) if "Stasis(" in l), None)` pattern already in
the file). Keep assertions positive (substring `in l`) — do not add any new negative grep. The
new lines contain neither "Dial(" nor a new bracketed context, so the existing
`test_extensions_conf_has_no_dial_and_one_context` invariant is unaffected.
  </action>
  <verify>
    <automated>cd apps/voice && uv run pytest tests/test_asterisk_configs.py tests/test_telephony_controller.py -q</automated>
  </verify>
  <done>New test asserts all 5 captures exist before Stasis and passes; full telephony config + controller suites green.</done>
</task>

</tasks>

<threat_model>
## Trust Boundaries

| Boundary | Description |
|----------|-------------|
| PSTN caller → Asterisk edge | Untrusted inbound SIP INVITE headers cross here into channel vars. |
| Asterisk edge → controller (ARI) | Controller reads dialplan-captured vars via authenticated loopback ARI. |

## STRIDE Threat Register

| Threat ID | Category | Component | Severity | Disposition | Mitigation Plan |
|-----------|----------|-----------|----------|-------------|-----------------|
| T-WGZ-01 | Information Disclosure | Diagnostic INFO log of raw SIP headers | low | accept | Sub-account names + DIDs are PUBLIC per existing code comments; no PII/secret is present in these headers. Logging at INFO is consistent with the existing dialed_did/sip_to log line. |
| T-WGZ-02 | Elevation of Privilege | extensions.conf dialplan | low | mitigate | New lines are READ-only Set() captures of inbound headers/functions — no Dial()/feature-code/outbound context added; existing negative-grep lint (zero Dial(), one context) still passes, keeping T-11-02-01 posture unchanged. |
| T-WGZ-03 | Tampering | Attacker-controlled SIP header values | low | accept | Values are only logged (observe-only); they do NOT feed dialed_did/gate/SMS selection, so a spoofed header cannot alter routing or cause a text to a third party. |
</threat_model>

<verification>
- `cd apps/voice && uv run pytest tests/test_asterisk_configs.py tests/test_telephony_controller.py -q` passes.
- Manual read-through confirms: exactly one context, zero Dial() calls, no pjsip.conf / vegas / dialed_did / SMS-selection changes.
</verification>

<success_criteria>
- 5 new read-only SIP header/function captures land in extensions.conf before Stasis, grouped with the existing To: capture.
- Controller emits one greppable diagnostic INFO log line dumping all 5 values per inbound call.
- Config-lint + telephony-controller test suites pass with zero behavior change to routing/gate/SMS.
- No deploy performed (deploy + live call are a separate human-gated step).
</success_criteria>

<output>
Create `.planning/quick/260716-wgz-diagnostic-capture-all-candidate-sip-hea/260716-wgz-SUMMARY.md` when done.
</output>
