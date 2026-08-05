---
phase: quick-260805-fki
plan: 01
type: execute
subsystem: telephony
tags: [telephony, gate, pipecat, toml, observability]
wave: 1
depends_on: []
autonomous: true
requirements: [QT-260805-fki]
files_modified:
  - apps/voice/src/klanker_voice/telephony/gate.py
  - apps/voice/src/klanker_voice/telephony/pickup_cue.py
  - apps/voice/src/klanker_voice/telephony/config.py
  - apps/voice/src/klanker_voice/telephony/controller.py
  - apps/voice/configs/telephony.toml
  - apps/voice/tests/test_telephony_gate.py
  - apps/voice/tests/test_telephony_config.py
  - apps/voice/tests/test_telephony_controller.py

must_haves:
  truths:
    - "A caller on a gated DID gets the full gate_window_seconds of dialing budget measured from the END of the ring+hey pickup cue, not from ARI answer — a real ~10s budget instead of today's ~4s."
    - "gate_window_seconds is 10 in the shipped apps/voice/configs/telephony.toml, and its inline history comment records this change plus the live-telemetry evidence that drove it."
    - "The gate ALWAYS fails closed within a bounded window on every path — cue missing, cue handler never fires, media never ready, cue errors, or cue interrupted by barge-in. No path leaves the gate open indefinitely (D-05d)."
    - "The absolute fail-closed fire time is provably bounded by (timer start) + gate_window_seconds + gate_cue_lead_max_seconds, regardless of what the cue does."
    - "With telephony.gate_debug_log_dtmf = true, every ARI ChannelDtmfReceived event reaching the edge emits exactly one log line carrying call_id, caller_id and a running arrival count — proving digits crossed the trunk — including for a channel the controller does not track."
    - "That DTMF line NEVER carries a digit value, the DTMF buffer, the PIN, or an announcement code (D-05e). build_call_event's keyword-only signature is untouched."
    - "With gate_debug_log_dtmf = false (the default), telephony-edge log output is byte-identical to today."
    - "seconds_to_outcome in game_call_event stays ANSWER-relative (unchanged), so the timing numbers in the CONTEXT evidence table stay directly comparable to future kv telephony stats runs."
  artifacts:
    - apps/voice/src/klanker_voice/telephony/gate.py
    - apps/voice/src/klanker_voice/telephony/pickup_cue.py
    - apps/voice/src/klanker_voice/telephony/config.py
    - apps/voice/src/klanker_voice/telephony/controller.py
    - apps/voice/configs/telephony.toml
  key_links:
    - "GateProcessor._deadline is the SINGLE source of truth for when the fail-closed timer fires. start_timer() and defer_for_cue() are its only writers; _run_timer() only reads it."
    - "_register_pickup_cue's on_client_connected handler is the ONLY caller of defer_for_cue() — the cue and the deadline rebase are wired at exactly one seam."
    - "gate.start_timer() at the end of _finish_stasis_start_gated (plus the idempotent StartFrame start inside process_frame) stays UNCONDITIONAL — that is the fail-closed backstop the cue defer can only ever bound, never remove."
    - "ActiveCall.dtmf_arrivals increments at on_channel_dtmf_received entry, BEFORE the gate_mode / known-channel / non-empty-digit guards — that ordering is what makes a dropped-or-untracked arrival visible at all."
---

<objective>
Three changes to the live §24 telephony answer-gate, all driven by the CloudWatch
telemetry in CONTEXT:

1. Rebase the fail-closed gate timer so the caller's window starts when the ring+"hey"
   pickup cue finishes, not at ARI answer (the cue currently burns ~4.3s of an 8s budget).
2. Raise `gate_window_seconds` 8 → 10.
3. Add opt-in, default-off DEBUG logging of DTMF **arrival** at the edge — arrival and a
   running count only, never digit values (D-05e).

Purpose: operator telemetry shows 12.5% of the operator's OWN successful unlocks already
exceed 8s, half land with <2s margin, and six external callers were truncated mid-code.
Separately, one caller placed 12 calls across all four game DIDs with zero registered
digits while STT demonstrably heard them speak — there is no way today to tell a
carrier-side DTMF relay failure from a caller who never pressed a key.

Output: a real ~10s post-cue dialing budget, a bounded fail-closed guarantee that is
*stronger* to reason about than today's, and a one-flag switch that settles the DTMF
question on the next call.
</objective>

<execution_context>
@$HOME/.claude/gsd-core/workflows/execute-plan.md
@$HOME/.claude/gsd-core/templates/summary.md
</execution_context>

<context>
@.planning/quick/260805-fki-telephony-gate-window-start-timer-after-/260805-fki-CONTEXT.md
@.claude/CLAUDE.md

@apps/voice/src/klanker_voice/telephony/gate.py
@apps/voice/src/klanker_voice/telephony/pickup_cue.py
@apps/voice/src/klanker_voice/telephony/config.py

Prior art to imitate (read only if the pattern is unclear from the source itself):
- `.planning/quick/260714-hpp-telephony-gate-fail-debug-logging-heard-/` — the
  `gate_debug_log_heard` opt-in debug-logging pattern change 3 must mirror.
- `.planning/quick/260727-v5e-game-call-telemetry-per-call-structured-/` — the
  `game_call_event` emission seam and its D-05e redaction contract.

**Verified facts about the current code (do not re-derive; correct the plan and say so in
the SUMMARY if any turns out false):**

- `pickup_cue.play_pickup_cue(worker)` only *queues* frames via `worker.queue_frames(...)`
  and returns immediately. **It does NOT await playback and there is no completion
  signal.** CONTEXT's phrasing "when the pickup cue completes" therefore cannot be
  implemented by awaiting the cue — it must be a *computed audio duration*. This is the
  single most important correction to CONTEXT's `<specifics>`.
- The cue is deterministic and measurable: `generate_ringback()` is
  `_DEFAULT_DURATION_S = 1.2`s at 24 kHz, and the committed
  `assets/telephony/kph-hey.wav` is 16-bit mono 24 kHz, 73561 frames = 3.065s. Total
  4.265s — matching the "~4s cue" in the CONTEXT evidence.
- `GateProcessor._run_timer` is today a single `await asyncio.sleep(self._gate_window_seconds)`
  with no deadline concept; `start_timer()` is idempotent and creates the task.
- `gate.start_timer()` is called from TWO places: explicitly at the end of
  `_finish_stasis_start_gated` (controller.py, after the mint-failure early return), and
  idempotently from `GateProcessor.process_frame` on the first `StartFrame`. Either can
  win the race. Both must keep working.
- `_register_pickup_cue(transport, call_session)` is called from BOTH the gated
  (`_finish_stasis_start_gated`) and ungated (`_finish_stasis_start_ungated`) finish
  paths. Only the gated path has a `GateProcessor`.
- `on_channel_dtmf_received` currently returns early — before any counting — when
  `gate_mode` excludes DTMF, when the channel is unknown, when `active_call.gate is None`,
  or when the digit is empty. `active_call.dtmf_count` increments only *after* all of
  those guards.
- `TelephonyConfig` is a frozen dataclass; `load_telephony_config` reads each field
  explicitly from the `[telephony]` table. `config._CREDENTIAL_FIELD_RE` rejects
  credential-shaped TOML keys before parse — neither new knob name
  (`gate_cue_lead_max_seconds`, `gate_debug_log_dtmf`) matches any of its tokens.
- Tests run as `cd apps/voice && uv run pytest ...` (confirmed: `tests/test_telephony_gate.py`
  is 48 passed today). Gate tests build processors via the `_gate(**overrides)` helper;
  controller/lifecycle tests via `_build_controller` + `_gated_cfg(**overrides)`.
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Deadline-based gate timer + bounded cue defer + a cue-duration helper</name>
  <files>apps/voice/src/klanker_voice/telephony/gate.py, apps/voice/src/klanker_voice/telephony/pickup_cue.py, apps/voice/tests/test_telephony_gate.py</files>
  <behavior>
    - `pickup_cue_duration_seconds()` returns 4.265 (1.2s ring + the committed 3.065s hey clip) with the real asset present.
    - `pickup_cue_duration_seconds()` returns 1.2 (ring only) when the hey clip is absent or unreadable, and never raises.
    - A gate whose timer starts and is never deferred fires fail-closed after exactly `gate_window_seconds` — the existing timing behavior, unchanged.
    - A gate deferred by a cue lead L fires fail-closed at approximately `L + gate_window_seconds` after the defer, not at `gate_window_seconds` after timer start.
    - Deferring is one-shot: a second `defer_for_cue` call does not extend the window again.
    - A cue lead LARGER than `max_cue_lead_seconds` is clamped — the gate still fires within `gate_window_seconds + max_cue_lead_seconds` of timer start.
    - `defer_for_cue` before `start_timer` (either call order) yields the same deferred deadline as after.
    - `defer_for_cue` after the gate has already resolved (unlock, fail-closed, or `cancel_for_takeover`) is a no-op and never resurrects a cancelled timer.
    - A negative or zero cue lead is a no-op that never SHORTENS the window.
    - `unlock()` and `cancel_for_takeover()` still cancel a deferred timer and fail-closed still fires exactly once.
  </behavior>
  <action>
Add to `pickup_cue.py` a pure `pickup_cue_duration_seconds()` returning the total
wall-clock duration of the frames `play_pickup_cue` queues: `_DEFAULT_DURATION_S` for the
ring, plus the hey clip's duration derived from `load_hey_clip()` as
bytes / 2 / sample_rate (16-bit mono). Guard a zero/absent sample rate and empty PCM so a
missing asset degrades to ring-only, mirroring `load_hey_clip`'s own never-raise
discipline. It reuses the existing `lru_cache`d loader, so it is cheap to call per call.
Document in the docstring that this is a COMPUTED duration because `play_pickup_cue` has
no completion signal, and that it is the input to the gate's cue defer.

In `gate.py`, convert `GateProcessor`'s fail-closed timer from a fixed sleep to a
monotonic-deadline loop, keeping the fail-closed guarantee strictly stronger, never weaker:

- Add a `max_cue_lead_seconds: float` keyword-only constructor param (default 8.0) plus
  private state: `_deadline` (monotonic, `None` until the timer starts), `_timer_started_at`
  (monotonic), `_pending_cue_lead` (float, 0.0), and `_cue_deferred` (bool, one-shot guard).
- `start_timer()` keeps its existing idempotency and `_resolved` guard, and additionally
  stamps `_timer_started_at = loop.time()` and
  `_deadline = _timer_started_at + _gate_window_seconds + _pending_cue_lead` BEFORE creating
  the task — so a defer that arrived before the timer started is honoured, and so
  `_run_timer` never has to compute its own start point.
- `_run_timer()` becomes a loop: read `_deadline`, sleep the remaining interval, re-check;
  break and call `_fire_fail_closed()` only once the deadline has actually passed. This is
  what makes a mid-flight deadline extension take effect. Cancellation during the sleep
  propagates exactly as today (`unlock` / `cancel_for_takeover` cancel the task).
- Add `defer_for_cue(cue_seconds: float) -> None`, per the CONTEXT "Claude's Discretion"
  note choosing the REBASE approach over deferring timer creation, because the timer then
  always exists and the fail-closed bound is a plain arithmetic invariant:
  no-op if `_resolved` or `_cue_deferred`; set `_cue_deferred` immediately (one-shot);
  clamp the lead to `[0.0, max_cue_lead_seconds]` and return on a non-positive result;
  if the timer has not started yet, store the clamped lead in `_pending_cue_lead` and
  return; otherwise set `_deadline` to
  `min(max(_deadline, loop.time() + lead + _gate_window_seconds), _timer_started_at + _gate_window_seconds + max_cue_lead_seconds)`.
  The inner `max` guarantees the window can never be shortened; the outer `min` is the
  fail-closed cap. It is synchronous and never raises.
- Document the invariant explicitly in both the module docstring and `defer_for_cue`'s own
  docstring: the fail-closed timer is started unconditionally by the controller and by the
  first StartFrame, and `defer_for_cue` can only ever move the deadline forward by a bounded
  amount, so a cue that never plays, errors, is interrupted, or never signals still fails
  closed at `_timer_started_at + gate_window_seconds` — and the absolute worst case on any
  path is `_timer_started_at + gate_window_seconds + max_cue_lead_seconds`. That sentence is
  the D-05d proof for this change; keep it accurate if the implementation shifts.
- Log nothing new here (D-05e posture unchanged in this task).

Write the tests in `tests/test_telephony_gate.py` FIRST (RED), extending the existing
`_gate(**overrides)` helper style with small windows (0.05–0.2s) and small leads so the
suite stays fast. Cover every bullet in the behavior block above, including a dedicated
test named for the fail-closed bound that asserts an absurdly large cue lead still fires
within the clamp.
  </action>
  <verify>
    <automated>cd apps/voice && uv run pytest tests/test_telephony_gate.py -q</automated>
  </verify>
  <done>
`pickup_cue_duration_seconds()` returns 4.265 against the committed asset and 1.2 with the
asset absent. `tests/test_telephony_gate.py` passes with the 48 pre-existing tests still
green plus the new deadline/defer/clamp/ordering/resolved-no-op cases. No behavior change
for a gate that is never deferred.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: Config knobs + telephony.toml 8 → 10 with documented history</name>
  <files>apps/voice/src/klanker_voice/telephony/config.py, apps/voice/configs/telephony.toml, apps/voice/tests/test_telephony_config.py</files>
  <behavior>
    - `gate_cue_lead_max_seconds` defaults to 8.0 when the TOML key is absent, and parses a float when set.
    - `gate_debug_log_dtmf` defaults to False when the TOML key is absent, and parses True when set.
    - Both knobs survive the shared credential-field gate (`_load_toml_data` does not reject either key).
    - The shipped `apps/voice/configs/telephony.toml` parses cleanly and yields `gate_window_seconds == 10`.
  </behavior>
  <action>
Add two fields to the frozen `TelephonyConfig` dataclass, in the §24 answer-gate block
next to `gate_debug_log_heard`, each with the inline comment style that block already uses:

- `gate_cue_lead_max_seconds: float = 8.0` — the HARD upper bound on how far the ring+hey
  pickup cue may push the fail-closed deadline. Comment it as the D-05d safety cap: the
  gate always fails closed within `gate_window_seconds + gate_cue_lead_max_seconds` of
  timer start no matter what the cue does. 8.0 comfortably covers the measured 4.265s cue
  plus media-setup slack while staying tightly bounded.
- `gate_debug_log_dtmf: bool = False` — opt-in DTMF ARRIVAL logging, framed exactly like
  `gate_debug_log_heard`'s comment: a deliberate, operator-accepted, documented D-05e
  relaxation limited to arrival evidence. State plainly that it logs THAT a digit arrived
  and how many have arrived on this call, and never the digit value, the accumulated DTMF
  buffer, the PIN, or any announcement code. Off = byte-identical D-05e posture.

Read both in `load_telephony_config` alongside the existing keys
(`float(table.get("gate_cue_lead_max_seconds", 8.0))`,
`bool(table.get("gate_debug_log_dtmf", False))`). Extend the `Attributes:` docstring block
for both.

In `apps/voice/configs/telephony.toml`, change `gate_window_seconds` from 8 to 10 and
EXTEND (do not truncate) its existing inline history comment. The comment currently records
10→20 (260714, spoken-passphrase overrun) and 20→10→8 (260729). Append a 260805 entry
recording: 8→10 together with this task's cue-relative timer start; the live-telemetry
evidence (operator unlocks median 6.0s / max 12.1s, 3 of 24 already exceeding 8s, six
external callers registering partial digit counts of 2/4/5/5/6/9 with every external gate
timeout firing at exactly 8.1s); and the fact that the window is now measured from the END
of the ~4.3s pickup cue, so the caller's real budget goes from ~4s to ~10s. Note in the same
comment that the 260714 spoken-passphrase overrun mode is helped by both halves of this
change.

Add the two new knobs to `telephony.toml` beneath `gate_debug_log_heard`, each with a short
inline comment: `gate_cue_lead_max_seconds = 8.0` (the fail-closed cap) and
`gate_debug_log_dtmf = false` with a one-line note that flipping it to true plus a
telephony-edge redeploy answers "did the caller's digits ever reach us" — citing the CONTEXT
caller who placed 12 calls across all four game DIDs registering zero digits while STT heard
them speak.

Add config tests to `tests/test_telephony_config.py` mirroring the existing
`test_gate_debug_log_heard_defaults_off` / `..._parses_true_when_set` pair for BOTH new
knobs, plus a test that loads the real shipped `apps/voice/configs/telephony.toml` and
asserts `gate_window_seconds == 10`.
  </action>
  <verify>
    <automated>cd apps/voice && uv run pytest tests/test_telephony_config.py -q</automated>
  </verify>
  <done>
Both knobs parse with the stated defaults and set values; the shipped telephony.toml loads
and reports `gate_window_seconds == 10`; the history comment on that knob names the 260805
change and its evidence.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 3: Controller wiring — cue defer at the pickup-cue seam + redaction-safe DTMF arrival logging</name>
  <files>apps/voice/src/klanker_voice/telephony/controller.py, apps/voice/tests/test_telephony_controller.py</files>
  <behavior>
    - On a gated call, firing the transport's `on_client_connected` handler defers the gate's deadline by the computed cue duration exactly once, and still plays the cue.
    - On an ungated call (`require_gate=False`), the same handler plays the cue and touches no gate — behavior byte-identical to today.
    - A gated call whose `on_client_connected` never fires still fails closed at `gate_window_seconds` after timer start (the D-05d backstop, tested explicitly).
    - A gated call whose cue asset is missing (ring-only) still defers by only the ring duration and still fails closed within the bound.
    - With `gate_debug_log_dtmf=True`, one `dtmf_received` line is emitted per ARI event with a running per-call arrival count that increments 1, 2, 3 across three events.
    - That line is emitted even when the channel is unknown to the controller and even when `gate_mode` excludes DTMF — those are precisely the invisible cases.
    - The emitted line carries only the allowed keys and does NOT contain the pressed digit character.
    - With `gate_debug_log_dtmf=False` (default), no such line is emitted and the existing PIN / announcement-code / `dtmf_count` behavior is unchanged.
    - Unlock-by-PIN and announcement-code dispatch still work identically with the flag on and off.
  </behavior>
  <action>
Cue defer:

- Give `_register_pickup_cue` an optional keyword-only `gate: GateProcessor | None = None`
  parameter. Inside the `on_client_connected` handler, when `gate` is not None, call
  `gate.defer_for_cue(pickup_cue_duration_seconds())` BEFORE `play_pickup_cue(...)` — the
  cue's audio begins at approximately the moment it is queued, so deferring immediately
  before the queue call is the truest "window starts when the cue ends". Import
  `pickup_cue_duration_seconds` alongside the existing `pickup_cue` imports.
- Pass `gate=gate` from the `_finish_stasis_start_gated` call site. Leave the
  `_finish_stasis_start_ungated` call site unchanged (no gate exists there).
- Do NOT move, condition, or remove the existing unconditional `gate.start_timer()` at the
  end of `_finish_stasis_start_gated`, and do NOT touch the StartFrame-triggered
  `start_timer()` inside `GateProcessor.process_frame`. Those two are the fail-closed
  backstop; the defer is only allowed to bound-shift a deadline that already exists.
  Extend `_register_pickup_cue`'s docstring to say exactly that, and to note that a
  barge-in that flushes the cue mid-playback still leaves the full deferred window granted
  (deliberately generous, never shorter).

DTMF arrival logging (D-05e is non-negotiable here):

- Add `dtmf_arrivals: int = 0` to the `ActiveCall` dataclass, documented as a pure counter
  of ARI `ChannelDtmfReceived` events observed for this call — distinct from `dtmf_count`,
  which only counts digits that survived the handler's guards. This field is for the debug
  line only and is NOT threaded into `build_call_event`.
- In `on_channel_dtmf_received`, hoist the `channel_id` / `digit` parsing and the
  `self.calls.get(channel_id)` lookup ABOVE the `gate_mode` guard, then, when
  `self._telephony_cfg.gate_debug_log_dtmf` is true, increment `active_call.dtmf_arrivals`
  (when an `ActiveCall` exists) and emit ONE line. Keep every existing guard and every
  existing branch in the same order and with the same semantics after that point — this is
  a pure hoist plus an added logging block, not a restructure of the PIN or
  announcement-code logic.
- Log via `logger.info`, not `logger.debug`, and use the `gate_fail_heard` line shape as
  the template: a `dtmf_received{...}` marker followed by comma-separated
  `key: value` pairs. Emit exactly four keys: `call_id` (the ARI channel id), `caller_id`
  (the `ActiveCall`'s caller_id, or a literal unknown placeholder when the channel is not
  tracked), `arrival_count` (the running per-call counter, 0 when untracked), and `tracked`
  (whether an `ActiveCall` was found). INFO, not DEBUG, because the deployed telephony-edge
  log level is what the existing `gate_fail_heard` line already relies on to reach
  CloudWatch — a DEBUG line would likely never be emitted, defeating the entire purpose.
- The pressed digit, `active_call.dtmf_buffer`, `active_call.dtmf_raw`, `self._pin`, and any
  announcement code MUST NOT appear in the line or anywhere else new. Do not widen
  `build_call_event`'s keyword-only signature; do not change `call_event.py` at all.
  Add an inline comment above the logging block, in the same voice as the
  `gate_debug_log_heard` block in `gate.py`, stating that this is a deliberate, bounded
  D-05e relaxation carrying arrival evidence only, and that it is placed before the guards
  ON PURPOSE so an arrival for an unknown or non-DTMF-mode channel is still visible.

Telemetry decision to record: `seconds_to_outcome` stays ANSWER-relative — it keeps reading
`active_call.answered_at`, unchanged. This is the CONTEXT "Claude's Discretion" item, and
the choice is deliberate: rebasing it to the cue would silently invalidate every timing
number in the CONTEXT evidence table and every past `kv telephony stats` comparison. State
this explicitly in the SUMMARY, and note that the operator can recover a cue-relative figure
by subtracting the ~4.3s cue. `call_event.py` is not modified.

Write the tests in `tests/test_telephony_controller.py` FIRST (RED), reusing the existing
`_build_controller` / `_gated_cfg(**overrides)` / fake rig. For the redaction assertion,
capture the emitted line and assert both that its key set is exactly the four allowed keys
and that the pressed digit character does not appear anywhere in it — choose a digit that
does not collide with the fixture's call_id, caller_id, or the arrival count so the
assertion is genuine rather than vacuous.
  </action>
  <verify>
    <automated>cd apps/voice && uv run pytest tests/ -q -k telephony</automated>
  </verify>
  <done>
Every behavior bullet above is covered by a passing test, including the explicit
"cue handler never fires → still fails closed at gate_window_seconds" case and the
"digit value absent from the log line" case. The full telephony suite is green with no
pre-existing test modified except where a signature genuinely changed.
  </done>
</task>

</tasks>

<threat_model>
## Trust Boundaries

| Boundary | Description |
|----------|-------------|
| PSTN caller → Asterisk ARI → controller | Fully untrusted inbound: caller-controlled DTMF, caller-controlled speech, caller-controlled call duration. This is the §24 gate's whole reason to exist. |
| Gate-locked pipeline → LLM/TTS/ledger | The D-05e redaction boundary. Nothing the caller says or presses may cross it before unlock. |
| Edge process → CloudWatch logs | Operator-readable. Anything logged here is readable by anyone with log access, forever. |

## STRIDE Threat Register

| Threat ID | Category | Component | Severity | Disposition | Mitigation Plan |
|-----------|----------|-----------|----------|-------------|-----------------|
| T-fki-01 | Denial of Service | `GateProcessor` fail-closed timer | critical | mitigate | The cue defer REBASES an always-running deadline rather than deferring timer creation. `start_timer()` stays unconditional at both existing call sites, and `defer_for_cue` clamps to `max_cue_lead_seconds` and is one-shot — so a cue that never plays, errors, hangs, or is barge-in-flushed cannot hold a metered PSTN line open. Bound proven by a dedicated clamp test and a "handler never fires" test (Tasks 1 and 3). |
| T-fki-02 | Elevation of Privilege | Widened gate window (8 → 10s, plus ~4.3s cue lead) | medium | accept | A brute-forcing caller gets ~10s instead of ~4s of dialing time per call. Against a 4–6 digit code space this is a negligible change in guessing throughput, and the per-call teardown, `max_concurrent_calls = 4`, and quota tiers all remain in force. Operator-locked decision; the truncation of legitimate callers is the far larger real-world harm. |
| T-fki-03 | Information Disclosure | `dtmf_received` debug line | high | mitigate | Log arrival evidence ONLY: `call_id`, `caller_id`, `arrival_count`, `tracked`. Never the digit, the DTMF buffer, the raw suffix buffer, the PIN, or an announcement code. Enforced by a test asserting the exact key set AND the absence of the pressed digit character. Default off, so the shipped posture is unchanged until the operator flips it. |
| T-fki-04 | Information Disclosure | `build_call_event` redaction-by-construction | high | mitigate | `call_event.py` is explicitly out of scope for edits. Its keyword-only signature — which admits only ints, floats, bools, and pre-approved identifier strings — is not widened. The new counter lives on `ActiveCall`, never in the event payload. |
| T-fki-05 | Tampering | Dependencies | low | accept | No package installs. No new imports beyond one intra-package function (`pickup_cue_duration_seconds`). The Package Legitimacy Gate does not apply. |
| T-fki-06 | Repudiation | Timing-metric rebase | medium | mitigate | `seconds_to_outcome` deliberately stays answer-relative so historical `kv telephony stats` comparisons remain valid; the decision and its rationale are recorded in the SUMMARY rather than left implicit. |
</threat_model>

<verification>
1. `cd apps/voice && uv run pytest tests/ -q` — the FULL Python suite, not just telephony.
   `gate.py`, `controller.py`, and `config.py` are shared by the WebRTC path; a regression
   there is a production outage on voice.klankermaker.ai, not just the phone lines.
2. **Fail-closed proof (D-05d), the non-negotiable gate.** Three distinct tests must exist
   and pass, each asserting the fail-closed callback actually fires:
   - cue handler never fires → fails closed at `gate_window_seconds` after timer start;
   - cue asset missing (ring-only defer) → fails closed within the bound;
   - absurdly large cue lead → clamped, fails closed within
     `gate_window_seconds + gate_cue_lead_max_seconds`.
   If any of these cannot be made to pass, STOP and report — do not ship a weakened gate.
3. **Redaction proof (D-05e).** With `gate_debug_log_dtmf=True`, the captured
   `dtmf_received` line's key set is exactly the four allowed keys and the pressed digit
   character is absent. `git diff --stat` shows `apps/voice/src/klanker_voice/telephony/call_event.py`
   is UNTOUCHED.
4. **Default-off proof.** With the flag at its default, no `dtmf_received` line is emitted
   and the pre-existing telephony tests pass unmodified.
5. `cd apps/voice && uv run python -c "from klanker_voice.telephony.config import load_telephony_config; c = load_telephony_config('configs/telephony.toml'); print(c.gate_window_seconds, c.gate_cue_lead_max_seconds, c.gate_debug_log_dtmf)"`
   prints `10 8.0 False`.
6. Confirm by reading the diff that `gate.start_timer()` is still called unconditionally at
   the end of `_finish_stasis_start_gated` and still on the first `StartFrame` in
   `GateProcessor.process_frame`.
</verification>

<success_criteria>
- Full `apps/voice` pytest suite green, with no pre-existing test deleted or weakened.
- `gate_window_seconds = 10` in `apps/voice/configs/telephony.toml`, its history comment
  extended with the 260805 change and the telemetry that drove it.
- A gated caller's dialing window measurably starts after the ~4.3s pickup cue, and the
  fail-closed deadline is bounded on every path by
  `gate_window_seconds + gate_cue_lead_max_seconds` from timer start.
- `gate_debug_log_dtmf` exists, defaults to false, and when true emits one arrival line per
  ARI DTMF event carrying no digit value.
- `call_event.py` unmodified; `seconds_to_outcome` still answer-relative, with that decision
  stated explicitly in the SUMMARY.
</success_criteria>

<output>
Create `.planning/quick/260805-fki-telephony-gate-window-start-timer-after-/260805-fki-SUMMARY.md` when done.

The SUMMARY MUST explicitly state:
- That `seconds_to_outcome` stayed ANSWER-relative (not rebased to the cue), and why.
- The measured cue duration used (`pickup_cue_duration_seconds()`), so a future asset
  re-render that changes it is traceable.
- The chosen `gate_cue_lead_max_seconds` value and the resulting worst-case fail-closed bound.
- That this is a live production telephony path change requiring a telephony-edge redeploy
  to take effect, and that `gate_debug_log_dtmf` must be flipped to true in
  `configs/telephony.toml` (plus a redeploy) before it produces any evidence.
</output>
