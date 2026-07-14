# Telephony gate — log failed passphrase attempts (caller + heard words) for accent/STT debugging

**Captured:** 2026-07-14 (from KPH, same session as the ledger fix)
**Status:** SPEC / brief — ready for `/gsd-quick`, BUT gated on a conscious design decision (see ⚠️).

## Goal (operator's words)

> "A friend with an Indian accent wasn't able to get past the 'hack the planet' [gate].
> It'd be great if it logged the phone number and failed words that it heard so I can help
> debug a bit."

When a caller fails the §24 passphrase gate, capture **(a) their phone number** and **(b)
the words STT actually heard** (the failed/accumulated transcript), so the operator can see
*why* an accent-mismatched utterance didn't match "hack the planet" and decide how to fix it.

## ⚠️ This RELAXES a locked design decision (D-05e) — decide before building

`apps/voice/src/klanker_voice/telephony/gate.py` documents a strict redaction boundary
(D-05e): while the gate is locked, the transcript **never** reaches the controller/router/
LLM/ledger/**logs**. Explicitly today: `GateProcessor.unlock` logs only
`unlocked{method, call_id}`; the fail-closed path logs only `call_id` — "neither the
transcript, the matched words, the PIN, nor a partial-match ('N of 4') count is ever
logged." This request deliberately reverses that for the FAIL path.

**Decision D1:** Operator accepts logging heard words on failed gate attempts. Reasonable
because: (1) everyone's told they're recorded / signed off (operator's standing note);
(2) a *failed* attempt's heard words are BY DEFINITION not the correct passphrase, so
logging them does not leak the secret; (3) operator-only CloudWatch, not public. Proceed
only with this consciously accepted.

## Design (scoped to preserve the security intent)

- **Phone number:** already available — `on_stasis_start` logs `caller=<e164> did=<...>`
  correlated by `call_id`. Include `caller_id` directly in the new fail log so it's one line,
  no correlation needed.
- **Heard words:** the `GateProcessor` already tokenizes each `TranscriptionFrame` into a
  running lower-cased token set (`accumulated`). On the **fail-closed** path (gate-window
  expiry with no unlock), log that accumulated set. That set is exactly "what it heard but
  couldn't match."
- **Opt-in flag:** add `gate_debug_log_heard = false` to `telephony.toml` `[telephony]`
  (default OFF; on = emit the fail log). Keeps D-05e the default posture; debugging is a
  deliberate toggle.
- **Log shape (INFO, telephony-edge CloudWatch):**
  `gate_fail_heard{call_id, caller_id, heard_tokens=[...], token_count=N, window_expired=true}`
- **HARD constraints (do NOT regress):**
  - NEVER log on the SUCCESS path (no reason to; and success ≈ the secret).
  - NEVER log the configured `TELEPHONY_PASSPHRASE_WORDS` / PIN themselves — only what the
    caller said.
  - Keep it behind the flag; keep it telephony-edge-log-only (operator IAM), not the ledger,
    not any LLM/persona/router path (the rest of D-05e stays intact).
  - DTMF PIN digits: do NOT log the heard digits (a mistyped PIN is closer to the secret than
    mis-heard words) — passphrase-tokens only, or redact PIN digits.

## Likely fix this unblocks (phase 2, data-driven)

The logged heard-vs-expected is the input to the actual fix for accent misses:
- **Deepgram keyterm/keyword boosting** of the passphrase words at the STT layer (nudges
  Nova-3 toward "hack"/"planet") — cheapest, no gate-logic change.
- **Fuzzy / phonetic match** in `match_passphrase` (e.g. allow close variants) — riskier
  (weakens the gate); only if boosting isn't enough.
- Consider a more accent-robust passphrase, or the DTMF PIN path as the accent-proof
  fallback (gate_mode is already "either").
Don't build the fix blind — ship the logging, gather a few real failed attempts, then choose.

## Verify / done
- With `gate_debug_log_heard=true`, a failed call emits one `gate_fail_heard{...}` line with
  the caller's number and the heard tokens; a successful call emits nothing new.
- The configured passphrase/PIN never appears in any log.
- Default (flag off) behavior is byte-identical to today's D-05e posture.

## Pointers
- `apps/voice/src/klanker_voice/telephony/gate.py` — `GateProcessor` (accumulated token set,
  fail-closed callback), `match_passphrase`, the D-05e redaction docstring
- `apps/voice/src/klanker_voice/telephony/controller.py` — `on_stasis_start` (already logs
  caller_id), `_gate_unlock`, fail-closed goodbye/hangup
- `apps/voice/configs/telephony.toml` `[telephony]` — new `gate_debug_log_heard` flag; gate
  knobs (`gate_window_seconds` currently 20)
- Deploy: `apps/voice/**` change → `build-telephony-edge.yml` → `deploy.yml` (clean path)
