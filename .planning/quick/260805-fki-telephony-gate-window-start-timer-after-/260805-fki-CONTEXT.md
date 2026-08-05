# Quick Task 260805-fki: Telephony gate window — cue-relative timer, 10s, DTMF arrival logging — Context

**Gathered:** 2026-08-05
**Status:** Ready for planning

<domain>
## Task Boundary

Three changes to the §24 telephony answer-gate, all driven by live CloudWatch telemetry
(see `<evidence>` below):

1. **Start the fail-closed gate timer when the ring+"hey" pickup cue finishes**, not at
   ARI answer. Today the ~4s cue burns inside the window, leaving a first-time caller
   roughly 4s of a nominal 8s budget to dial a 4–6 digit code.
2. **Raise `gate_window_seconds` 8 → 10.**
3. **Add opt-in DEBUG logging of DTMF digit ARRIVAL at the edge**, so a call that
   registered zero digits can be told apart from a call whose digits never crossed the
   trunk. Redaction discipline: log that a digit arrived and how many, NEVER the digit
   values (D-05e).

Out of scope: changing any game code/passphrase, the rickroll fail-closed behavior,
`otp_only_dids` policy, or the pickup cue itself.

</domain>

<evidence>
## Live telemetry that motivated this (queried 2026-08-05)

Source: CloudWatch Logs Insights over `/ecs/telephony-edge-telephony-edge-use1-kmv`,
75 `game_call_event` lines (since the 260727-v5e telemetry deploy) + 68 `gate_fail_heard`
lines, joined against 202 `on_stasis_start` inbound calls (2026-07-12 → 2026-08-05).

**The 8s window has no margin left, even for someone who knows the code:**

| Operator (+15197101515) successful unlocks | n=24 |
|---|---|
| `seconds_to_outcome` min | 3.9s |
| median | 6.0s |
| max | **12.1s** |
| exceeded 8s (would fail today) | **3 of 24 (12.5%)** |
| landed with <2s margin | 12 of 24 (50%) |

**External callers are being truncated mid-dial.** Six external gate-timeout calls
registered partial digit counts of **2, 4, 5, 5, 6, 9** — the 2 and 5 counts are the
fingerprint of a window expiring mid-code. Every external gate timeout fired at
exactly 8.1s.

**65% of external gate timeouts made no attempt at all** (17 of 26 had 0 digits and
0 words) — that is a separate design question, deliberately NOT in this task's scope.

**The DTMF-visibility gap:** caller `+12672520810` placed 12 calls across all 4 game
DIDs and registered **zero DTMF digits on every one**, while STT demonstrably heard
them speak on one call (`heard_tokens: ['double','down','saloon']`). The AU caller
`+61437008930` had digits arrive normally (9, 6, 5, 2), so DTMF is not globally broken.
There is currently **no per-digit logging at the edge** — `digits_entered` in
`game_call_event` is the only DTMF visibility — so a carrier-side DTMF relay failure is
indistinguishable from a caller who simply never pressed a key. Change 3 exists to
settle exactly that question on the next call.

</evidence>

<decisions>
## Implementation Decisions

### Timer start point (LOCKED — operator KPH)
Start the gate fail-closed timer when the pickup cue completes. The cue is played from
a fire-once `on_client_connected` handler (`_register_pickup_cue` →
`play_pickup_cue`, `apps/voice/src/klanker_voice/telephony/controller.py:1228`), which
already runs after ARI answer. The window must still be bounded on paths where the cue
never plays or never completes — do NOT let a failed/absent cue leave the gate open
indefinitely. Fail closed is non-negotiable (D-05d).

### Window length (LOCKED — operator KPH)
`gate_window_seconds` 8 → 10 in `apps/voice/configs/telephony.toml`. Combined with the
cue-relative start this yields a real ~10s dialing budget rather than today's ~4s.
Update the knob's inline history comment (it currently documents 10→20→10→8 and the
260714 spoken-passphrase overrun mode) to record this change and its evidence.

### DTMF logging shape (LOCKED — operator KPH, D-05e discipline)
Opt-in, default OFF, mirroring the existing `gate_debug_log_heard` pattern exactly —
same config-knob style, same "deliberate D-05e relaxation, documented in-line" framing.
Log DTMF **arrival**, never digit values: a per-digit DEBUG line carrying call_id,
caller_id, and a running received-count is acceptable; the digit characters themselves
are not. The point is to prove digits crossed the trunk, not what they were.

### Claude's Discretion
- Exact knob name for the DTMF logging toggle (suggest something in the
  `gate_debug_log_*` family for consistency).
- Whether the cue-relative start is implemented by deferring timer creation or by
  restarting/rebasing an already-started timer — pick whichever keeps the fail-closed
  guarantee simplest to reason about and test.
- Whether `seconds_to_outcome` in `game_call_event` stays answer-relative or becomes
  cue-relative. If it changes, say so explicitly in the SUMMARY — it silently rebases
  every future `kv telephony stats` timing comparison against the numbers in
  `<evidence>` above.

</decisions>

<specifics>
## Specific Ideas

Key files (verified present on `main` @ 3f8933b):

- `apps/voice/configs/telephony.toml` — `gate_window_seconds` (line ~93),
  `gate_debug_log_heard` (line ~101), `gate_fail_audio`
- `apps/voice/src/klanker_voice/telephony/gate.py` — `GateProcessor`,
  `accumulate_dtmf` (line ~160), `unlock_method`
- `apps/voice/src/klanker_voice/telephony/controller.py` — `_register_pickup_cue`
  (line ~1228), `_gate_unlock`, `answered_at` capture (~line 1124),
  `_teardown_gate_resources`
- `apps/voice/src/klanker_voice/telephony/pickup_cue.py` — `play_pickup_cue`
- `apps/voice/src/klanker_voice/telephony/call_event.py` — `build_call_event`,
  `CALL_EVENT_MARKER` (keyword-only, redaction-safe by construction — do not widen its
  signature to admit digit values)
- Tests: `apps/voice/tests/test_telephony_gate.py`,
  `test_telephony_controller.py`, `test_telephony_lifecycle.py`

Prior art to imitate: quick tasks **260714-hpp** (the `gate_fail_heard` opt-in debug
logging, D-05e fail-path relaxation) and **260727-v5e** (the `game_call_event`
telemetry + `kv telephony stats`).

</specifics>

<canonical_refs>
## Canonical References

- `docs/superpowers/specs/2026-07-11-voipms-telephony-integration.md` — §24 answer-gate,
  D-05a..D-05e decisions (fail-closed, either-factor, redaction-by-construction)
- `.planning/quick/260714-hpp-telephony-gate-fail-debug-logging-heard-/` — the
  opt-in debug-logging pattern this task's change 3 must mirror
- `.planning/quick/260727-v5e-game-call-telemetry-per-call-structured-/` — the
  `game_call_event` emission seam and its D-05e redaction contract

</canonical_refs>
