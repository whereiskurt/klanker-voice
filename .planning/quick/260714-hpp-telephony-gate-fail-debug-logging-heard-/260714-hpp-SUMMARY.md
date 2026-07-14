---
phase: quick-260714-hpp
plan: 01
status: complete
date: 2026-07-14
commits:
  - eb92a8d  # feat: gate_fail_heard opt-in log on fail-closed path (+4 gate tests)
  - 5f0f5a4  # feat: gate_debug_log_heard config flag (default off) (+2 config tests)
  - ed72038  # feat: thread caller_id + flag into GateProcessor at controller
---

# Quick Task 260714-hpp — Telephony gate-fail debug logging (heard words + caller)

## What shipped

An **opt-in** debug log so the operator can see WHY a caller failed the §24 passphrase gate
(the trigger: a friend with an Indian accent couldn't get past "hack the planet"). Built with
TDD (RED→GREEN each step). Three atomic commits, all telephony suites green.

1. **`gate.py`** (commit `eb92a8d`) — `GateProcessor` gains `caller_id` + `debug_log_heard`
   (default `False`). In `_fire_fail_closed` (the only fail path — gate-window expiry with no
   unlock), when the flag is on, it emits ONE INFO line:
   `gate_fail_heard{call_id, caller_id, heard_tokens: [sorted...], token_count: N, window_expired: true}`
   — logging `sorted(self._accumulated_tokens)` (what STT heard), never `self._secret_words`.
2. **`config.py` + `telephony.toml`** (commit `5f0f5a4`) — `TelephonyConfig.gate_debug_log_heard:
   bool = False`, parsed from `[telephony]`. `telephony.toml` ships it `false` with a comment
   documenting the opt-in D-05e relaxation.
3. **`controller.py`** (commit `ed72038`) — passes `caller_id` (already in scope) + the flag
   into the `GateProcessor` at construction (`controller.py:707`).

## Security posture — the D-05e relaxation, scoped tight

This deliberately relaxes the locked D-05e redaction boundary **for the FAIL path only**, which
the operator has consciously accepted. The relaxation is bounded by design + tests:

- **Opt-in, default off** → default behavior is byte-identical to today's D-05e posture (no
  `gate_fail_heard` line ever). Proven by `test_fail_closed_debug_log_off_by_default_emits_no_heard_line`
  and the pre-existing `test_unlock_and_fail_closed_never_log_secrets_or_transcript` still green.
- **Fail path only, NEVER success** → unlock cancels the timer; heard-logging lives only in
  `_fire_fail_closed`. Proven by `test_debug_log_on_never_emits_on_success_path` (flag on, caller
  unlocks → no line).
- **Never the secret** → logs only the caller's accumulated speech tokens (a failed attempt is
  by definition NOT the passphrase). Never logs `self._secret_words`. The DTMF PIN never reaches
  this processor (controller compares it), so PIN digits can't appear either. Proven by
  `test_debug_log_on_never_reconstructs_unspoken_secret_words` (caller says 1 of 4 secret words →
  only that spoken word logged; the 3 unspoken secret words absent).
- **Operator-CloudWatch only** → it's a `logger.info`, telephony-edge log stream. Not the ledger,
  not any LLM/persona/router path (those are still structurally unreachable pre-unlock).

## Verification evidence

- TDD RED confirmed each step (gate: `TypeError: unexpected keyword argument 'caller_id'`; config:
  `AttributeError: no attribute 'gate_debug_log_heard'`) before implementing.
- `uv run pytest tests/test_telephony_gate.py -q` → **24 passed** (20 existing + 4 new).
- `uv run pytest tests/test_telephony_config.py -q` → **19 passed** (17 + 2 new).
- Full regression `test_telephony_gate.py + test_telephony_config.py + test_telephony_controller.py
  + test_telephony_lifecycle.py` → **64 passed**. (2 warnings are pre-existing audioop /
  AudioContextTTSService deprecations, unrelated.)

## How the operator uses it + the real fix (phase 2, data-driven)

Flip `gate_debug_log_heard = true` in `telephony.toml` and redeploy telephony-edge to gather a
few real failed attempts (`gate_fail_heard{...}` lines in CloudWatch). Then choose the actual
accent fix from the data — do NOT build it blind:
- **Deepgram keyterm/keyword boosting** of the passphrase words (nudges Nova-3 toward
  "hack"/"planet") — cheapest, no gate-logic change.
- Lean on the **DTMF PIN** as the accent-proof fallback (`gate_mode="either"` already supports it).
- Fuzzy/phonetic `match_passphrase` — riskier (weakens the gate); only if boosting isn't enough.

## Deploy path (human, after merge)

`apps/voice/**` change → `build-telephony-edge.yml` → `deploy.yml`. Deploy is a human step. Ships
with the flag OFF (no behavior change until the operator flips it + redeploys). Watch for the
standing telephony-edge deploy-revert gotcha.

## Follow-ups

- Operator: flip the flag + redeploy when ready to debug the accent miss; gather a few
  `gate_fail_heard` lines, then pick the phase-2 fix (keyterm boosting favored).
- This completes both pending telephony todos on `spec/telephony-3min-4concurrent` (call limits
  260714-hhj + gate debugging 260714-hpp). The two pending todo files can be moved to done.
