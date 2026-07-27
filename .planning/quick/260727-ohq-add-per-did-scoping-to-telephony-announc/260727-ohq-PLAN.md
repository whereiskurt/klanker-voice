---
phase: quick-260727-ohq
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
  - apps/voice/src/klanker_voice/telephony/config.py
  - apps/voice/src/klanker_voice/telephony/controller.py
  - apps/voice/configs/telephony.toml
  - apps/voice/tests/test_telephony_config.py
  - apps/voice/tests/test_telephony_controller.py
autonomous: true
requirements: [QUICK-260727-OHQ]
must_haves:
  truths:
    - "An [[telephony.announcement]] entry with a non-empty `dids` list only dispatches when the call's resolved dialed_did is in that list."
    - "An entry with absent or empty `dids` stays GLOBAL — it dispatches on every call exactly as it does today (the shipped live entry is byte-unaffected)."
    - "An unresolved dialed_did (empty string — CID-prefix/To: parse miss) matches ONLY global entries, never scoped ones (fail closed)."
    - "A scoped entry whose DID does not match does NOT unlock the concierge either — the gate stays locked and the DTMF loop simply continues to the next armed entry."
    - "configs/telephony.toml documents the new field; its live announcement entry keeps parsing to dids == () (no behavior change shipped in this task)."
  artifacts:
    - apps/voice/src/klanker_voice/telephony/config.py
    - apps/voice/src/klanker_voice/telephony/controller.py
    - apps/voice/configs/telephony.toml
  key_links:
    - "AnnouncementEntry.dids is parsed by _parse_announcements via the existing _parse_sms_dids normalizer (field='dids'), the same helper sms_dids/sms_reply_dids already use."
    - "The dispatch guard reads active_call.dialed_did, which on_stasis_start already resolves via _dialed_did_from_cidname(cid_prefix_did_map) or _dialed_did_from_sip_to — no new resolution path is introduced."
    - "The only dispatch site is the code-match loop in AsteriskCallController.on_channel_dtmf_received (controller.py ~line 1557)."
---

<objective>
Let one `[[telephony.announcement]]` entry be bound to specific dialed DIDs so a second CTF
game (its own DTMF code, script, and SMS reply) can live on 725-404-8283 without its code
being redeemable on the other DIDs — a one-block-per-game TOML layout.

Purpose: today every armed announcement code is global (Revision 2 made the announcement
deliberately DID-agnostic because the dialed DID was invisible at the edge). The CID-prefix
map (quick 260717-buf) now resolves the dialed DID reliably, so per-entry scoping is finally
possible — and it is the missing piece for running two games on one trunk.

Output: an optional `dids` field on `AnnouncementEntry`, a fail-closed match guard in the
controller's announcement-code loop, updated telephony.toml documentation, and tests.
</objective>

<locked_decisions>
- D-01: New OPTIONAL field `dids = ["7254048283", ...]` on each `[[telephony.announcement]]`
  entry. Absent or empty ⇒ the entry is GLOBAL (current behavior, backward compatible — the
  existing live entry must keep working unchanged).
- D-02: Match semantics — an entry with a non-empty `dids` list fires only when
  `active_call.dialed_did` is non-empty AND in that entry's `dids`. An unresolved dialed_did
  (`""`) matches ONLY global entries, never scoped ones (fail closed).
- D-03: Parse/normalize `dids` exactly like `sms_dids`/`sms_reply_dids` — reuse the existing
  `_parse_sms_dids` helper with `field="dids"` (bare-digit normalization, order preserved,
  same non-list ConfigError shape). The shared D-09 credential-key gate already covers the
  whole file.
- D-04: Do NOT change telephony.toml's live behavior. Update only its comments/docs to describe
  the new field. The real second-game entry for 7254048283 (new SSM code param + task-def env
  wiring) is a separate operator step, OUT of scope here.
- D-05: Tests — config parsing (absent / empty / populated+normalized / invalid) and controller
  dispatch (scoped fires on matching DID; scoped does NOT fire on another DID or on an
  unresolved DID; global still fires everywhere). Follow the existing style in each test file.
- D-06: Test command — `cd apps/voice && uv run pytest tests/test_telephony_config.py
  tests/test_telephony_controller.py -q` (the repo's documented invocation, docs/guides/testing.md).
</locked_decisions>

<context>
@.planning/STATE.md
@apps/voice/src/klanker_voice/telephony/config.py
@apps/voice/src/klanker_voice/telephony/controller.py
@apps/voice/configs/telephony.toml
@apps/voice/tests/test_telephony_config.py
@apps/voice/tests/test_telephony_controller.py

Interface notes the executor needs (already verified — do not re-derive):
- `AnnouncementEntry` (config.py:31) is a frozen dataclass; `otp_url` is the only field without
  a default, every other field is keyword-defaulted in this declared order: `otp_env_var`,
  `line_template`, `code_env_var`, `did`, `sms_dids`, `sms_reply_dids`, `sms_relay_url`.
  APPEND the new field AFTER `sms_relay_url` so no existing positional order shifts.
- `_parse_sms_dids(raw, i, field="sms_dids")` (config.py:364) is the shared DID-array
  normalizer; `_parse_announcements` (config.py:298) already calls it twice.
- `_select_sms_send_dids(entry, dialed_did)` (controller.py:584) is the existing per-DID
  selector — the new match helper belongs immediately after it and should mirror its shape
  and docstring conventions.
- `ActiveCall.dialed_did` (controller.py:719) is populated in `on_stasis_start` (~line 919)
  from `_dialed_did_from_cidname(cidname, cid_prefix_did_map) or _dialed_did_from_sip_to(sip_to)`;
  a total miss leaves it `""`.
- The dispatch loop is `for code, entry in self._announcements_by_code.items():` at
  controller.py:1557, inside `on_channel_dtmf_received`.
- Controller tests import `_build_controller`, `_gated_cfg`, `_stasis_event`, `FakeAriClient`
  from `tests.test_telephony_lifecycle`; `_build_controller(...)` returns
  `(controller, ari, sessions)`, takes `telephony_cfg=`, `access_pin=`, `passphrase_words=`,
  `announcement_codes=`, and the fake ARI exposes a writable `ari.channel_vars` dict
  (`KLANKER_SIP_CIDNAME` / `KLANKER_SIP_TO`). `_gated_cfg(**overrides)` accepts
  `cid_prefix_did_map=`, `otp_only_dids=`, `announcements=`.
- Existing local test helpers to reuse in test_telephony_controller.py: `ANNOUNCEMENT_CODE`
  (= "990011"), `_announcement_entry(**overrides)`, `_dial(digits)`.
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: optional `dids` field on AnnouncementEntry (config layer + TOML docs)</name>
  <files>apps/voice/src/klanker_voice/telephony/config.py, apps/voice/configs/telephony.toml, apps/voice/tests/test_telephony_config.py</files>
  <behavior>
    - No `dids` line in the entry table ⇒ `entry.dids == ()` (global; every other field unchanged).
    - `dids = []` ⇒ `()` (also global — empty is treated identically to absent).
    - `dids = ["725-404-8283", "+17254043234", "  "]` ⇒ `("7254048283", "17254043234")` —
      digits-only normalization, order preserved, blank/junk elements dropped (same rule and
      same observable results as the existing sms_dids normalization test).
    - A scalar `dids = "7254048283"` (not an array) raises ConfigError naming the field.
    - The shipped apps/voice/configs/telephony.toml still parses to exactly one announcement
      entry whose `dids` is `()` — global, live behavior unchanged (D-04).
  </behavior>
  <action>
    Write the tests FIRST in tests/test_telephony_config.py (new section appended at the end,
    headed with a comment naming quick task 260727-ohq, matching the existing per-feature
    section-header style), confirm they fail, then implement.

    config.py changes (per D-01/D-03):
    1. Add `dids: tuple[str, ...] = ()` to `AnnouncementEntry`, declared AFTER `sms_relay_url`
       so existing positional field order is untouched. Document it with a `#:` comment block
       in the same voice as the neighbouring `sms_reply_dids` comment: the set of DIALED DIDs
       this announcement entry is bound to; empty or absent means the entry is GLOBAL and fires
       on every call (byte-identical to before this field existed); non-empty means the entry
       only fires when the resolved dialed DID is one of these, and an UNRESOLVED dialed DID
       matches only global entries (fail closed — never guess). Note that a DID is a public
       phone number, never a credential, so the digits live safely in TOML.
    2. In `_parse_announcements`, alongside the existing sms_dids/sms_reply_dids parsing, add
       `dids = _parse_sms_dids(item.get("dids"), i, field="dids")` and pass `dids=dids` into the
       `AnnouncementEntry(...)` construction.
    3. Extend `_parse_sms_dids`'s docstring to note it now also normalizes the per-entry `dids`
       scoping array (one more caller, same rule).
    4. Add a short note to the `AnnouncementEntry` class docstring's attribute list documenting
       `dids`, AND record the one real constraint of the current dispatch registry: the
       controller keys armed entries by their resolved code VALUE, so two entries must resolve
       to DIFFERENT code values — if two entries' code env vars hold the same value, only one
       survives the registry regardless of scoping. Per-game entries are expected to have their
       own code env var and their own value.

    configs/telephony.toml changes (D-04 — documentation only, zero behavior change):
    5. In the comment block above the existing `[[telephony.announcement]]` entry, add a
       paragraph describing the new optional per-entry scoping array: absent/empty keeps the
       entry global (which is what the live entry below intentionally is), a populated list
       binds the entry to those dialed DIDs (resolved via the `[telephony.cid_prefix_dids]`
       tags above), an unresolved dialed DID fires only global entries, and each entry must use
       its own code env var with its own distinct value. Mention the intended one-block-per-game
       layout (DID list + code env var + line template + sms_reply_dids per block) and that
       adding a second game entry is a separate operator step (new SSM code param + task-def
       env wiring).
    6. Leave the live entry itself functionally untouched — you MAY add a commented-out example
       line inside it showing the field shape, but no active assignment: the live entry must
       keep parsing to an empty scoping tuple.

    Do not touch controller.py in this task.
  </action>
  <verify>
    <automated>cd apps/voice &amp;&amp; uv run pytest tests/test_telephony_config.py -q</automated>
    <automated>cd apps/voice &amp;&amp; uv run python -c "from klanker_voice.telephony.config import load_telephony_config; c=load_telephony_config('configs/telephony.toml'); assert len(c.announcements)==1 and c.announcements[0].dids==(), c.announcements; print('live entry still global OK')"</automated>
  </verify>
  <done>
    `AnnouncementEntry.dids` exists and defaults to `()`; `_parse_announcements` populates it via
    `_parse_sms_dids(..., field="dids")`; the 5 new config tests (absent, empty array, populated+
    normalized, non-list rejected, shipped-file-still-global) pass; the whole
    test_telephony_config.py file still passes; telephony.toml documents the field without
    changing its live parse result.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: fail-closed per-DID match guard in the announcement dispatch loop</name>
  <files>apps/voice/src/klanker_voice/telephony/controller.py, apps/voice/tests/test_telephony_controller.py</files>
  <behavior>
    - Scoped entry (`dids=("7254048283",)`) + `ari.channel_vars["KLANKER_SIP_CIDNAME"] = "KVD8283"`
      with `cid_prefix_did_map={"KVD8283": "7254048283"}` + the armed code dialed ⇒ exactly one
      `_gate_announcement` dispatch with that entry.
    - Same scoped entry, but the call resolves to a DIFFERENT dialed DID (e.g. tag KVD3234 →
      7254043234) ⇒ ZERO dispatches, and the gate is still locked afterwards (the code must not
      leak onto another DID, and must not unlock the concierge either).
    - Same scoped entry with NO channel vars set (dialed_did resolves to `""`) ⇒ ZERO dispatches
      (fail closed).
    - A GLOBAL entry (`dids=()`) dispatches in both cases: dialed DID resolved and dialed DID
      unresolved — today's behavior preserved exactly.
    - Two entries armed under different codes — one global, one scoped to a DID this call did
      NOT dial: dialing the scoped code dispatches nothing, dialing the global code still
      dispatches the global entry (the loop skips a non-matching entry rather than aborting).
  </behavior>
  <action>
    Write the tests FIRST in tests/test_telephony_controller.py (new section appended at the end,
    headed with a comment naming quick task 260727-ohq and stating the fail-closed rule), confirm
    they fail, then implement.

    controller.py changes (per D-02):
    1. Add a module-level pure helper immediately after `_select_sms_send_dids` (~line 584),
       named `_announcement_matches_did(entry: AnnouncementEntry, dialed_did: str) -> bool`.
       Semantics: an entry with an empty scoping tuple is global and always matches; otherwise it
       matches only when `dialed_did` is truthy AND present in `entry.dids`. Give it a docstring
       in the same voice as `_select_sms_send_dids`: name the quick task, spell out the three
       cases (global entry → always; scoped + resolved-and-listed → yes; scoped + resolved-but-
       unlisted OR unresolved → no), and state the fail-closed rationale — an unresolved dialed
       DID must never be able to redeem a DID-bound game code.
    2. In `on_channel_dtmf_received`'s code-match loop (~line 1557), add the helper call as an
       additional condition on the existing `if` so a non-matching entry simply falls through to
       the next iteration (never `break`, never `return`). Keep the existing suffix-match and
       `if code` truthiness checks and the PIN's strict priority exactly as they are.
    3. Update `on_channel_dtmf_received`'s docstring with a short paragraph for this quick task:
       every armed entry is still evaluated on every call, but a scoped entry is skipped unless
       this call's resolved dialed DID is one of its own; an unresolved dialed DID can only ever
       reach global entries. Also note the entries stay code-keyed, so distinct entries need
       distinct code values.
    4. Do NOT touch `_gate_announcement`, the OTP-only gate policy, `_select_sms_send_dids`, or
       any DID-resolution code — the guard consumes the already-resolved `active_call.dialed_did`
       and nothing else.

    Test construction notes: build the controller with
    `_build_controller(make_config_file, telephony_cfg=_gated_cfg(cid_prefix_did_map={...},
    announcements=(entry,)), announcement_codes={ANNOUNCEMENT_CODE: entry}, access_pin="4242")`,
    set `ari.channel_vars["KLANKER_SIP_CIDNAME"]` BEFORE `await controller.on_stasis_start(
    _stasis_event())`, assert `controller.calls["chan-1"].dialed_did` resolved as expected, then
    feed digits via the existing `_dial(...)` helper. Spy on `_gate_announcement` by monkeypatching
    `klanker_voice.telephony.controller.AsteriskCallController._gate_announcement` exactly as
    `test_announcement_code_dispatches_no_quota_no_greet` already does. Build scoped entries with
    `_announcement_entry(dids=("7254048283",))`. For the global-entry case covering both resolved
    and unresolved DIDs, either loop over the two channel-var setups building a fresh controller
    each time, or parametrize (adding `import pytest` if you parametrize).
  </action>
  <verify>
    <automated>cd apps/voice &amp;&amp; uv run pytest tests/test_telephony_controller.py -q</automated>
    <automated>cd apps/voice &amp;&amp; uv run pytest tests/test_telephony_config.py tests/test_telephony_controller.py tests/test_telephony_lifecycle.py tests/test_telephony_sms.py -q</automated>
  </verify>
  <done>
    `_announcement_matches_did` exists and is the only new gate on the dispatch loop; the 5 new
    controller tests pass; the pre-existing announcement/OTP-only/SMS telephony suites all still
    pass unchanged (no regression to global-entry behavior).
  </done>
</task>

</tasks>

<threat_model>
## Trust Boundaries

| Boundary | Description |
|----------|-------------|
| PSTN caller → telephony-edge DTMF | Untrusted keypad input arrives as ARI ChannelDtmfReceived events and is suffix-matched against armed announcement codes. |
| VoIP.ms CID name prefix → dialed_did | The DID identity used by this guard is derived from an inbound SIP display name, i.e. attacker-influencable metadata. |
| TOML config → announcement registry | Operator-authored scoping data; no credential may ever appear here (D-09). |

## STRIDE Threat Register

| Threat ID | Category | Component | Severity | Disposition | Mitigation Plan |
|-----------|----------|-----------|----------|-------------|-----------------|
| T-OHQ-01 | Elevation of Privilege | `on_channel_dtmf_received` code-match loop | medium | mitigate | A scoped entry requires a non-empty dialed_did present in its own list; an unresolved DID reaches global entries only (fail closed), so a game code cannot be redeemed from an un-enrolled DID. Covered by the "other DID" and "unresolved DID" tests. |
| T-OHQ-02 | Spoofing | CID-prefix-derived `dialed_did` | medium | accept | A caller who forged the exact per-DID CID prefix tag could present as that DID. Pre-existing property of quick 260717-buf's resolution (also gates otp_only_dids and per-DID SMS reply); this task adds no new resolution path and no new trust in it. The protected asset is a rotating CTF OTP, not access to the concierge or to quota. |
| T-OHQ-03 | Information Disclosure | new helper + docstrings | low | mitigate | The guard logs nothing; DTMF codes, OTP values, and bearers stay unlogged (T-OTP-04/D-05e untouched). Only public DID digits live in TOML. |
| T-OHQ-04 | Tampering | dependency installs | low | accept | No new package is added by this task (stdlib + existing helpers only), so no package-legitimacy checkpoint applies. |
</threat_model>

<verification>
- `cd apps/voice && uv run pytest tests/test_telephony_config.py tests/test_telephony_controller.py -q` passes (D-06).
- Regression sweep: `cd apps/voice && uv run pytest tests/test_telephony_lifecycle.py tests/test_telephony_sms.py tests/test_telephony_gate.py -q` passes — global-entry, OTP-only-DID, and per-DID SMS behavior all unchanged.
- The shipped configs/telephony.toml announcement entry still parses with an empty scoping tuple (asserted by a test and by the inline python check in Task 1).
</verification>

<success_criteria>
- An `[[telephony.announcement]]` entry can be bound to one or more dialed DIDs with an optional
  `dids` array, and its code is inert on every other DID and on an unresolved DID.
- Entries without the field remain global; the live config and every existing telephony test is
  behavior-unchanged.
- telephony.toml documents the one-block-per-game layout so the operator can add the second
  725-404-8283 game entry (plus its SSM code param and task-def env wiring) as a follow-up step.
</success_criteria>

<output>
Create `.planning/quick/260727-ohq-add-per-did-scoping-to-telephony-announc/260727-ohq-SUMMARY.md` when done.
</output>
