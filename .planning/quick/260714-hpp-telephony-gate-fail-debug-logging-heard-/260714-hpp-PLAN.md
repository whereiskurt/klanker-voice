---
phase: quick-260714-hpp
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
  - apps/voice/src/klanker_voice/telephony/gate.py
  - apps/voice/src/klanker_voice/telephony/config.py
  - apps/voice/src/klanker_voice/telephony/controller.py
  - apps/voice/configs/telephony.toml
  - apps/voice/tests/test_telephony_gate.py
  - apps/voice/tests/test_telephony_config.py
autonomous: true
must_haves:
  truths:
    - "With gate_debug_log_heard=true, a failed (window-expiry) gate attempt emits ONE gate_fail_heard log line with call_id, caller_id, heard_tokens, token_count."
    - "Default (flag off) behavior is byte-identical to today's D-05e posture — no gate_fail_heard line ever."
    - "The configured passphrase words / PIN never appear in any log (we log only what the caller said, on the fail path only, never on success)."
  artifacts:
    - apps/voice/src/klanker_voice/telephony/gate.py
    - apps/voice/configs/telephony.toml
  key_links:
    - "GateProcessor._fire_fail_closed has self._accumulated_tokens (heard words) + self._call_id; caller_id + flag threaded in via constructor from controller.py:707."
---

<objective>
Give the operator debugging visibility into WHY a caller failed the §24 passphrase gate
(an accent/STT mismatch on "hack the planet"): on a fail-closed (gate-window expiry with no
unlock), optionally log the caller's phone number + the tokens STT actually heard. Opt-in,
fail-path only, never the secret. This DELIBERATELY relaxes D-05e for the fail path only —
operator has consciously accepted (everyone's told they're recorded; a failed attempt's heard
words are by definition not the passphrase, so no secret leaks; operator-CloudWatch only).
</objective>

<locked_decisions>
- D1 (accepted): log heard words on FAILED gate attempts, behind an opt-in flag.
- HARD constraints (do NOT regress):
  - NEVER on the success/unlock path.
  - NEVER log the configured passphrase words or the DTMF PIN — only the caller's accumulated
    speech tokens (which never include DTMF digits: the PIN never reaches this processor).
  - Behind `gate_debug_log_heard` flag (default false); default posture byte-identical to D-05e.
  - telephony-edge CloudWatch log only — not the ledger, not any LLM/persona/router path.
</locked_decisions>

<tasks>

<task type="auto">
  <name>Task 1 (TDD): gate_fail_heard opt-in log on the fail-closed path</name>
  <files>apps/voice/src/klanker_voice/telephony/gate.py, apps/voice/tests/test_telephony_gate.py</files>
  <action>
    TDD. First add failing tests to test_telephony_gate.py (using the existing loguru_caplog
    fixture + _gate helper), then implement:
    - GateProcessor.__init__ gains `caller_id: str | None = None` and `debug_log_heard: bool = False`,
      stored as self._caller_id / self._debug_log_heard.
    - In _fire_fail_closed, AFTER the existing `gate fail-closed call_id=...` line: if
      self._debug_log_heard, emit ONE INFO line:
        gate_fail_heard{call_id: ..., caller_id: ..., heard_tokens: [sorted...], token_count: N, window_expired: true}
      Log sorted(self._accumulated_tokens); NEVER self._secret_words.
    - Update the module + _fire_fail_closed docstrings to note the opt-in D-05e relaxation.
    Tests:
      1. flag OFF (default) + non-matching speech then fail-closed -> NO 'gate_fail_heard' in log.
      2. flag ON + caller_id + non-matching speech "the weather is nice today" then fail-closed
         -> log contains gate_fail_heard, the caller_id, the heard tokens, token_count.
      3. flag ON but caller UNLOCKS (passphrase) -> NO gate_fail_heard emitted (success path clean).
      4. flag ON: a secret word the caller did NOT say is absent from the log (no secret reconstruction).
  </action>
  <verify>
    <automated>cd apps/voice && uv run pytest tests/test_telephony_gate.py -q</automated>
  </verify>
  <done>
    New tests fail first, then pass; existing test_unlock_and_fail_closed_never_log_secrets_or_transcript
    still green (flag defaults off). Secret/PIN never logged; success path never emits the line.
  </done>
</task>

<task type="auto">
  <name>Task 2 (TDD): config flag gate_debug_log_heard + telephony.toml</name>
  <files>apps/voice/src/klanker_voice/telephony/config.py, apps/voice/configs/telephony.toml, apps/voice/tests/test_telephony_config.py</files>
  <action>
    TDD: add a config test asserting the new field parses (default False; True when set in the
    table), then implement: TelephonyConfig gains `gate_debug_log_heard: bool = False`;
    load_telephony_config parses `bool(table.get("gate_debug_log_heard", False))`. Add
    `gate_debug_log_heard = false` to [telephony] in telephony.toml with a comment explaining
    the opt-in D-05e relaxation (fail-path only, never the secret, operator-CloudWatch only).
  </action>
  <verify>
    <automated>cd apps/voice && uv run pytest tests/test_telephony_config.py -q</automated>
  </verify>
  <done>Config field parses (default False + True-when-set); telephony.toml carries the flag off with an explanatory comment.</done>
</task>

<task type="auto">
  <name>Task 3: wire caller_id + flag into GateProcessor at the controller construction site</name>
  <files>apps/voice/src/klanker_voice/telephony/controller.py</files>
  <action>
    At the GateProcessor(...) construction (controller.py:~707), pass `caller_id=caller_id` and
    `debug_log_heard=self._telephony_cfg.gate_debug_log_heard`. caller_id is already in scope in
    that method. No other controller behavior changes.
  </action>
  <verify>
    <automated>cd apps/voice && uv run pytest tests/test_telephony_controller.py tests/test_telephony_lifecycle.py -q</automated>
  </verify>
  <done>Controller threads caller_id + the flag into the gate; controller/lifecycle suites stay green.</done>
</task>

</tasks>

<verification>
- Full telephony suite green: `cd apps/voice && uv run pytest tests/test_telephony_gate.py tests/test_telephony_config.py tests/test_telephony_controller.py tests/test_telephony_lifecycle.py -q`
- Default (flag off) = byte-identical D-05e posture; secret/PIN never logged; success path never emits.
</verification>

<deploy_note>
apps/voice/** change -> build-telephony-edge.yml -> deploy.yml (human deploy after merge).
The flag ships OFF; the operator flips gate_debug_log_heard=true in telephony.toml + redeploys
telephony-edge when they want to gather a few real failed attempts. Phase-2 fix (data-driven,
not in this task): Deepgram keyterm boosting of the passphrase words, or leaning on the DTMF
PIN as the accent-proof fallback.
</deploy_note>

<output>
Create .planning/quick/260714-hpp-telephony-gate-fail-debug-logging-heard-/260714-hpp-SUMMARY.md when done.
</output>
