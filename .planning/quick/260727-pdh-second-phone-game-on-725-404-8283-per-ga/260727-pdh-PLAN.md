---
phase: quick-260727-pdh
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
  - apps/voice/src/klanker_voice/telephony/config.py
  - apps/voice/src/klanker_voice/telephony/gate.py
  - apps/voice/src/klanker_voice/telephony/controller.py
  - apps/voice/configs/telephony.toml
  - apps/voice/tests/test_telephony_config.py
  - apps/voice/tests/test_telephony_gate.py
  - apps/voice/tests/test_telephony_controller.py
  - infra/terraform/live/site/services/telephony-edge/service.hcl
  - kv/internal/app/studio/types.go
  - kv/internal/app/studio/repofile_adapter.go
  - kv/internal/app/studio/view.go
  - kv/internal/app/studio/server.go
  - kv/internal/app/studio/web/index.html
  - kv/internal/app/studio/web/app.js
  - kv/internal/app/studio/repofile_adapter_test.go
  - kv/internal/app/studio/view_test.go
  - kv/internal/app/cmd/telephony.go
  - kv/internal/app/cmd/telephony_test.go
autonomous: true
requirements: [QUICK-260727-PDH]
must_haves:
  truths:
    - "A caller who dials 725-404-8283 and SPEAKS the second game's secret words hears that game's script — the same script the game's DTMF code would have produced (either-factor)."
    - "A caller who dials 725-404-8283 and ENTERS the second game's DTMF code hears the same script (the existing DTMF path is byte-unchanged)."
    - "The existing 333266 game only fires when dialing 725-404-3234; a second numeric-only game (its own code) only fires when dialing 725-404-3283; neither works on 8283 or on 613."
    - "725-404-8283 is OTP-only: the concierge passphrase and concierge DTMF PIN cannot open its gate; the line stays silent until a game factor lands or the fail-closed timer expires."
    - "The gate accumulates spoken tokens on an OTP-only DID (it previously skipped accumulation entirely there), yet still never forwards a pre-unlock transcription frame downstream and never logs a heard or matched word."
    - "An announcement entry has no spoken trigger — gracefully, not fatally — in any of three states: no words env var configured, the env var absent, or its value empty/whitespace or the literal sentinel `__unset__`. The entry stays fully live via its numeric code in every one of those states."
    - "That is exactly the 8283 launch state: its code is seeded live and its numeric trigger works, while its words param holds the `__unset__` sentinel so the spoken trigger is inert until an operator replaces it."
    - "`kv telephony list` prints a phone-games section: DID scope, code env var NAME + set/unset, words env var NAME + set/unset, sms_reply_dids — and never a secret value."
    - "`kv studio` shows the same game entries in a read-only panel, degrading to an empty section when telephony.toml is missing or unreadable — never blocking the rest of the console."
  artifacts:
    - apps/voice/src/klanker_voice/telephony/config.py
    - apps/voice/src/klanker_voice/telephony/gate.py
    - apps/voice/src/klanker_voice/telephony/controller.py
    - apps/voice/configs/telephony.toml
    - infra/terraform/live/site/services/telephony-edge/service.hcl
    - kv/internal/app/studio/repofile_adapter.go
    - kv/internal/app/cmd/telephony.go
  key_links:
    - "GateProcessor -> controller seam: the gate matches spoken words itself and awaits an injected on_announcement_words(key) callback carrying ONLY an opaque, non-secret entry key — the accumulated token set never crosses the processor boundary."
    - "Per-call DID scoping for the SPOKEN factor is applied in _finish_stasis_start_gated by filtering the phrase registry through the existing _announcement_matches_did() guard BEFORE the GateProcessor is constructed — the same fail-closed predicate the DTMF loop uses."
    - "The spoken factor terminates in the SAME _gate_announcement(active_call, entry) method the DTMF factor already calls — OTP fetch, SMS relay, script build, and teardown are shared, not duplicated."
    - "kv cmd and kv studio share ONE TOML announcement parser (studio.ParseTelephonyGames) — cmd already imports studio (cmd/studio.go), and studio must never import cmd."
---

<objective>
Stand up 725-404-8283 as its own phone game, distinct from the existing OTP game on
3234/3283, and add a NEW per-entry spoken-passphrase trigger so EITHER the numeric DTMF
code OR the game's secret spoken words fire that game's script.

Purpose: quick task 260727-ohq shipped the `dids` scoping field but never used it. This
task is its first real consumer — it locks the existing 333266 game to the two 3234/3283
DIDs, adds a second one-block-per-game entry for 8283, and gives each game an optional
spoken factor alongside its numeric one. Operator visibility for game entries lands in
both `kv telephony list` and `kv studio` so the routing config stops being TOML-only.

Output: an optional per-entry words env var on `AnnouncementEntry`, a spoken-trigger seam
from `GateProcessor` to the controller's announcement dispatch, two scoped game entries in
telephony.toml, two new SSM valueFrom wirings in telephony-edge's service.hcl, and a games
section in both operator surfaces.
</objective>

<locked_decisions>

All of these come from `260727-pdh-CONTEXT.md` and are NOT open for reinterpretation.

- **D-01 — 8283 is game-only.** Add `"7254048283"` to `[telephony].otp_only_dids`. The
  concierge passphrase and DTMF PIN are suppressed on that DID; the existing fail-closed
  timer still applies.
- **D-02 — Split the existing game per DID (AMENDED by operator 2026-07-27).** The two
  original Vegas DIDs become SEPARATE one-block-per-game entries:
  - The shipped `[[telephony.announcement]]` entry narrows to `dids = ["7254043234"]` only,
    renames `code_env_var` to `"CTF_ANNOUNCEMENT_CODE_3234"` for per-DID naming consistency
    (AMENDED again 2026-07-27 — its code VALUE is unchanged, copied to a new
    `/kmv/secrets/use1/ctf/announcement_code_3234` param), and narrows
    `sms_reply_dids` to `["7254043234"]`.
  - A NEW entry for 3283: `dids = ["7254043283"]`,
    `code_env_var = "CTF_ANNOUNCEMENT_CODE_3283"`, the SAME `line_template` as the 3234
    entry (same OTP script, different access code), same `otp_url` / `sms_relay_url`,
    `sms_reply_dids = ["7254043283"]`, and NO words env var (numeric-only).
  - Net effect: the 3234 code works only when dialing 3234, the 3283 code only when dialing
    3283, and neither works on 8283 or 613.
  - The 3283 SSM parameter (`/kmv/secrets/use1/ctf/announcement_code_3283`) is ALREADY
    seeded live and readback-verified by the orchestrator — it is NOT an outstanding
    operator step. Its value never appears in TOML, code, or the SUMMARY.
- **D-03 — Spoken trigger is a general, OPTIONAL, per-entry field carrying an env var NAME
  only.** The words VALUE lives in env/SSM, never in TOML (D-09). An entry has no spoken
  trigger — graceful skip, exactly like `code_env_var` today — when the field is absent,
  when the env var is unset, when its value is empty/whitespace, **or when its value is the
  literal sentinel `__unset__`**. In every one of those states the entry remains fully live
  via its numeric code; only the spoken factor is inert.
- **D-03a — The `__unset__` sentinel (AMENDED by operator 2026-07-27).** ECS fails task
  launch on a missing `valueFrom` parameter, so the words param is seeded with the literal
  string `__unset__` rather than left absent. That makes the service.hcl wiring deploy-safe
  while keeping the spoken factor off. The application layer is what gives the sentinel its
  meaning — it MUST be recognized and treated as "disabled", identically to an empty value.
- **D-04 — Either-factor semantics.** The DTMF code OR the spoken words fire the SAME
  script for that entry, through the same `_gate_announcement` method. Both factors are
  subject to the same fail-closed `_announcement_matches_did` DID scoping.
- **D-05 — Reuse `match_passphrase`.** Word matching is the concierge gate's existing
  lowercased token-set containment (`gate.py`). Do not write a second matcher.
- **D-06 — The gate must accumulate spoken tokens even when `concierge_unlock_enabled` is
  False.** Today accumulation is skipped entirely on OTP-only DIDs (gate.py ~line 358).
- **D-07 — D-05e redaction contract holds unchanged.** No heard word, matched word, or code
  value is ever logged on any success path. A pre-unlock transcription frame is still never
  forwarded downstream. The existing opt-in `gate_fail_heard` FAIL-path debug logging is
  untouched.
- **D-08 — Wire the words for the 8283 game only.** Env var name `CTF_ANNOUNCEMENT_WORDS_UCTF`.
  The existing 333266 entry gets NO words env var — it stays numeric-only, unchanged.
- **D-09 — New 8283 entry shape (AMENDED by operator 2026-07-27).** `dids = ["7254048283"]`,
  `code_env_var = "CTF_ANNOUNCEMENT_CODE_UCTF"`, words env var `CTF_ANNOUNCEMENT_WORDS_UCTF`,
  `sms_reply_dids = ["7254048283"]`, `sms_relay_url` and `otp_url` same as the other entries.
  `line_template` is the **SAME OTP gag template as the 3234/3283 entries** — same script
  for now, NOT a placeholder. All three entries carry an identical template string; only
  `dids`, `code_env_var`, `words_env_var`, and `sms_reply_dids` differ between them. A
  different script for 8283 can land later as a TOML-only edit.
- **D-10 — Infra is valueFrom wiring only; all three params are already seeded (AMENDED by
  operator 2026-07-27).** Three new SSM secrets on telephony-edge's service.hcl pointing at
  `/kmv/secrets/use1/ctf/announcement_code_3283`, `/kmv/secrets/use1/ctf/announcement_code_uctf`,
  and `/kmv/secrets/use1/ctf/announcement_words_uctf`. **All three are seeded live and
  readback-verified** — the code params hold real codes and the words param holds the
  `__unset__` sentinel — so this wiring is deploy-safe and there is NO seed-before-deploy
  operator step. NO terraform apply and NO SSM writes in this task. The single remaining
  operator step is replacing the words param's `__unset__` sentinel with real words to turn
  the spoken trigger on.
- **D-11 — Operator surfaces print NAMES only.** `kv telephony list` and `kv studio` show
  DID scope, code env var NAME + set/unset, words env var NAME + set/unset, and
  `sms_reply_dids`. Never a secret value. Studio follows its existing degradation pattern.

</locked_decisions>

<constraint_discovered>

**The TOML key CANNOT be named `passphrase_env_var`.**

CONTEXT.md names the new field `passphrase_env_var`. That name is technically impossible:
`klanker_voice.config._CREDENTIAL_FIELD_RE` refuses any TOML key containing a `passphrase`
token at a compound boundary, and `_reject_credential_fields` runs over the WHOLE file in
`_load_toml_data` before `_parse_announcements` is ever reached. A config carrying
`passphrase_env_var` raises `ConfigError` at load, so the field could never be read.

Verified against the live regex:

| candidate key        | rejected by the D-09 gate |
|----------------------|---------------------------|
| `passphrase_env_var` | **yes**                   |
| `words_env_var`      | no                        |
| `code_env_var`       | no                        |
| `otp_env_var`        | no                        |

This is the EXACT precedent already documented in `AnnouncementEntry`'s own docstring:
the design doc proposed `otp_auth_env_var`, the gate refused any key containing `_auth_`,
and it shipped as `otp_env_var`.

**Resolution: the field is named `words_env_var`.** Semantics are byte-for-byte what
CONTEXT.md specified — an OPTIONAL per-entry env var NAME, value in env/SSM, mirroring
`code_env_var`, graceful-skip when unset. Only the key spelling changes. The env var NAME
CONTEXT.md locked (`CTF_ANNOUNCEMENT_WORDS_UCTF`) is unaffected — env var names are not
TOML keys. Task 1 ships a regression test proving `passphrase_env_var` is refused, so this
rename is documented in the suite rather than in tribal memory.

</constraint_discovered>

<execution_context>
@$HOME/.claude/gsd-core/workflows/execute-plan.md
@$HOME/.claude/gsd-core/templates/summary.md
</execution_context>

<context>
@.planning/quick/260727-pdh-second-phone-game-on-725-404-8283-per-ga/260727-pdh-CONTEXT.md
@.planning/quick/260727-ohq-add-per-did-scoping-to-telephony-announc/260727-ohq-SUMMARY.md
@apps/voice/src/klanker_voice/telephony/config.py
@apps/voice/src/klanker_voice/telephony/gate.py
@apps/voice/src/klanker_voice/telephony/controller.py
@apps/voice/configs/telephony.toml
@kv/internal/app/cmd/telephony.go
@kv/internal/app/studio/repofile_adapter.go
@kv/internal/app/studio/types.go
@infra/terraform/live/site/services/telephony-edge/service.hcl
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Python — optional words_env_var, gate spoken-trigger seam, controller either-factor dispatch</name>
  <files>apps/voice/src/klanker_voice/telephony/config.py, apps/voice/src/klanker_voice/telephony/gate.py, apps/voice/src/klanker_voice/telephony/controller.py, apps/voice/tests/test_telephony_config.py, apps/voice/tests/test_telephony_gate.py, apps/voice/tests/test_telephony_controller.py</files>
  <behavior>
    config.py
    - An announcement table without the new key parses to `words_env_var == ""` (backward compatible).
    - `words_env_var = "CTF_ANNOUNCEMENT_WORDS_UCTF"` parses to that exact string, stripped.
    - An announcement table using the key `passphrase_env_var` raises `ConfigError` from the shared D-09 credential gate (regression test for the rename above).

    gate.py
    - With `concierge_unlock_enabled=False` and a phrase registry armed, the words arriving across TWO separate TranscriptionFrames still match (proves D-06 accumulation now happens on OTP-only DIDs).
    - On a match: the injected `on_announcement_words` callback is awaited with the entry key, `gate.unlocked` stays False, and the `on_unlock` callback is never called.
    - On a match: the fail-closed timer is cancelled — a gate built with a very short window fires no fail-closed callback afterwards.
    - The callback fires AT MOST ONCE: further matching frames after the first match produce no second invocation.
    - Redaction: during and after the match, a downstream sink receives ZERO transcription/speaking-state frames, and captured logs contain neither a heard word, nor a matched word, nor the phrase key.
    - Byte-identical default: a GateProcessor constructed with no phrase kwargs behaves exactly as before (no callback, no task, `concierge_unlock_enabled=False` still declines to unlock on the concierge passphrase).

    controller.py
    - The phrase registry skips an entry in all four disabled states — no `words_env_var`, env var absent, value empty/whitespace, or value exactly the `__unset__` sentinel — and every one of them is a graceful skip, not an error.
    - An entry skipped for any of those reasons still arms its NUMERIC trigger normally: the registry that keys DTMF codes is unaffected by a disabled spoken factor. This is the 8283 launch state and must be asserted, not assumed.
    - A sentinel value that is merely a SUBSTRING of a real phrase (the sentinel token alongside other words) is NOT treated as the sentinel — only an exact whole-value match after strip and lowercase disables the trigger.
    - The per-call phrase map passed to the GateProcessor is filtered through `_announcement_matches_did`: a scoped entry is armed for its own dialed DID, absent for another DID, and absent for an unresolved dialed DID; a global entry is armed for every call.
    - The callback dispatches `_gate_announcement(active_call, entry)` with the entry the key maps to.
  </behavior>
  <action>
Implement the spoken-trigger factor end to end. Follow the file's existing docstring density
and cite the quick-task id `260727-pdh` in every new docstring block, matching how
`260727-ohq` / `260717-o2q` are cited today.

**config.py (per D-03).** Add `words_env_var: str = ""` to `AnnouncementEntry`, placed
next to `code_env_var` in the dataclass and documented in the class docstring's Attributes
list. Document three things there: it carries an env var NAME only (value in env/SSM per
D-09, mirroring `code_env_var`); absent or empty means this entry simply has no spoken
trigger; and it is deliberately NOT named with a `passphrase` token because
`klanker_voice.config._CREDENTIAL_FIELD_RE` refuses such a key outright — cite the existing
`otp_env_var` rename note directly above it as the precedent. In `_parse_announcements`,
parse it beside `code_env_var` with the same coerce-and-strip treatment, but with NO
non-empty validation: this field is optional, unlike `code_env_var`.

**gate.py (per D-05, D-06, D-07).** Add a `AnnouncementWordsCallback` type alias next to
the existing `UnlockCallback` / `FailClosedCallback` aliases: a callable taking one string
key and returning an awaitable. Add two optional keyword-only constructor params to
`GateProcessor`: a mapping of opaque entry key to that entry's secret word set, and the
callback. Normalize each word set on construction the same way `_secret_words` is
normalized (strip + lowercase, drop empties), and drop any entry whose normalized set is
empty. Store the registry preserving insertion order. Add an instance attribute holding a
strong reference to the spawned announcement task, initialized to None.

Rework the `TranscriptionFrame` branch of `process_frame`. Today accumulation is gated
behind `concierge_unlock_enabled`; the new rule is that tokens accumulate when the
concierge factor is enabled **or** the phrase registry is non-empty — that is exactly the
D-06 change. Then, in order:
  1. The concierge match attempt runs only when `concierge_unlock_enabled` is True
     (unchanged semantics, unchanged priority — it stays first).
  2. The announcement match attempt runs only when the registry is non-empty, the callback
     is set, and the gate is NOT already resolved. Iterate the registry in order and use
     `match_passphrase` (D-05 — do not write a second matcher). On the first match:
     synchronously call `self.cancel_for_takeover("announcement")`, THEN spawn the callback
     via `asyncio.create_task`, retaining the handle on the instance, and break.
  3. The frame is still swallowed with `return` in every case — the D-05e redaction
     boundary is completely unchanged.

Two things about step 2 are load-bearing and must not be "simplified":
  - **Resolve first, then spawn.** `cancel_for_takeover` runs synchronously before the task
    is created so the fail-closed timer cannot fire in the window between scheduling and
    execution. `_gate_announcement` calls `cancel_for_takeover` again on its own path; that
    second call is already idempotent, so no change is needed there.
  - **Spawn, do not await.** Awaiting the callback inline would block this processor's frame
    queue for the whole OTP fetch plus readout plus grace sleep — tens of seconds — stalling
    every frame that transits this processor including teardown control frames. The
    fire-and-forget-plus-strong-reference shape mirrors `ActiveCall.sms_task` in the
    controller.

Add NO new log line anywhere in this path. `cancel_for_takeover` already logs only its
reason plus the call id, which is the entire logging budget for the spoken factor (D-07).
The heard tokens, the matched words, and the registry key must never reach a logger on this
path. Update the module docstring's redaction-discipline paragraph to state that the
announcement-words factor accumulates tokens on OTP-only DIDs but still forwards nothing
and logs nothing.

**controller.py (per D-04, D-08).** In `AsteriskCallController.__init__`, add an optional
`announcement_words: dict[str, str] | None = None` keyword param — a NAME-to-raw-value
override map consulted INSTEAD of `os.environ` when provided, so tests never mutate the
process environment. Resolve the source explicitly with an `is not None` check, not a
truthiness check, so an intentionally empty override dict does not silently fall back to
`os.environ`. Build a registry attribute mapping each entry's `code_env_var` (used purely
as a stable, non-secret, log-safe handle) to a two-tuple of the entry and its normalized
frozenset of words, following the `_announcements_by_code` comprehension's shape and
docstring style.

An entry is skipped — spoken factor disabled, no registry row — in FOUR states (D-03):
no `words_env_var` on the entry; the env var absent from the resolution source; a resolved
value that is empty or whitespace-only; or a resolved value equal to the sentinel. Declare
the sentinel as a module-level constant beside the other announcement constants rather than
inlining the literal, and match it on the stripped, lowercased whole value — an exact
whole-value comparison, never a substring or token test, so a real phrase that happens to
contain the sentinel token is unaffected. Document on that constant WHY it exists: ECS
fails task launch on a missing `valueFrom` parameter, so the words param is seeded with a
sentinel instead of left absent, and the application layer is what makes the sentinel mean
"disabled" (D-03a).

Document one arming rule explicitly in the registry attribute's comment: the spoken factor
arms on its own env var independently of whether the entry's code env var resolved, and a
disabled spoken factor never disturbs the numeric one. That is what "either factor" means —
each factor arms on its own secret. The 8283 entry ships in exactly that split state: code
live, words inert behind the sentinel.

In `_finish_stasis_start_gated`, immediately before constructing the `GateProcessor`, build
the per-call phrase map by filtering the registry through the existing
`_announcement_matches_did(entry, dialed_did)` predicate — this is where fail-closed DID
scoping is applied to the spoken factor, and it must reuse that predicate rather than
re-deriving the rule. Pass the filtered map and a new `_on_announcement_words` closure to
the GateProcessor. Write that closure alongside the existing `_on_unlock` /
`_on_fail_closed` closures, resolving the call through the same `active_call_holder`
forward-reference dict, looking the key up in the registry, returning quietly if either
lookup misses, and otherwise awaiting `self._gate_announcement(active_call, entry)`.

That last line is the whole point of D-04: the spoken factor terminates in the exact method
the DTMF factor already calls, so the OTP fetch, the per-DID SMS selection via
`_select_sms_send_dids(entry, active_call.dialed_did)`, the script build, the grace sleep,
and the teardown are shared code, not a second copy. Do not duplicate any of it.

Extend `_finish_stasis_start_gated`'s docstring with a `260727-pdh` paragraph describing
the new either-factor wiring and noting that the DTMF path in `on_channel_dtmf_received` is
byte-unchanged.

**Tests.** Write them first and confirm RED before implementing, per the behavior block
above. In the gate tests, reuse the existing `_gate(**overrides)` helper and the
`loguru_caplog` fixture, and prove the redaction case through `pipecat.tests.utils.run_test`
the way the existing locked-window tests do. Because the callback is spawned rather than
awaited, gate tests must yield to the event loop (a zero-length sleep is enough) before
asserting on the callback. In the controller tests, reuse the existing `_announcement_entry`
helper — extend it with overrides rather than writing a second factory — and mirror however
the existing `announcement_codes` injection tests construct their controller.
  </action>
  <verify>
    <automated>cd apps/voice && uv run pytest tests/test_telephony_config.py tests/test_telephony_controller.py tests/test_telephony_gate.py tests/test_telephony_lifecycle.py tests/test_telephony_sms.py -q</automated>
  </verify>
  <done>
- `AnnouncementEntry.words_env_var` parses as an optional NAME-only field; an announcement table using a `passphrase`-token key raises `ConfigError`.
- `GateProcessor` accumulates spoken tokens when a phrase registry is armed even with `concierge_unlock_enabled=False`, matches via `match_passphrase`, resolves the gate via `cancel_for_takeover`, and spawns the injected callback exactly once without ever unlocking.
- Zero pre-unlock frames reach a downstream sink and no heard/matched word or registry key appears in captured logs.
- The controller builds a DID-filtered per-call phrase map through `_announcement_matches_did` and its callback dispatches the existing `_gate_announcement` with the matching entry.
- All four spoken-factor-disabled states (no field, env var absent, empty/whitespace value, `__unset__` sentinel) skip gracefully via a named module-level sentinel constant, leave the entry's numeric trigger fully armed, and are covered by tests — including one proving a phrase merely containing the sentinel token is NOT disabled.
- The full telephony suite passes.
  </done>
</task>

<task type="auto">
  <name>Task 2: telephony.toml three per-DID game entries + otp_only_dids + service.hcl SSM wiring</name>
  <files>apps/voice/configs/telephony.toml, infra/terraform/live/site/services/telephony-edge/service.hcl, apps/voice/tests/test_telephony_config.py</files>
  <action>
Data and config only — no Python source changes in this task. End state is THREE
`[[telephony.announcement]]` entries: the 3234 game, the 3283 game, and the 8283 game —
one block per game, in that order.

**telephony.toml — narrow the existing entry to 3234 (D-02, amended).** On the shipped
`[[telephony.announcement]]` entry, replace the commented-out `dids` example line with a
live `dids = ["7254043234"]`, and narrow that same entry's `sms_reply_dids` to
`["7254043234"]`. Both 3283 and 8283 are now served by their own entries, and since this
entry can no longer dispatch on either DID, dropping them here is provably a no-op that
prevents future misreading. Rename its `code_env_var` to `"CTF_ANNOUNCEMENT_CODE_3234"` so
all three entries follow one per-DID naming scheme; its code VALUE does not change (the
orchestrator copies it into a new `/kmv/secrets/use1/ctf/announcement_code_3234` param).
Leave its `line_template` untouched. Rewrite the surrounding comment block so it reads as this
entry's own game (the OTP game on 725-404-3234) rather than as the global entry it used to
be, and keep the existing distinct-code-value constraint note there — the other two entries
will cross-reference it.

**telephony.toml — add the 3283 game (D-02, amended).** Append a second
`[[telephony.announcement]]` block with a header comment naming it as the 725-404-3283
game and citing quick task `260727-pdh`. Set `dids = ["7254043283"]`,
`code_env_var = "CTF_ANNOUNCEMENT_CODE_3283"`, `sms_reply_dids = ["7254043283"]`,
`sms_dids = []`, and `otp_url` / `otp_env_var` / `sms_relay_url` / `line_template`
IDENTICAL to the 3234 entry — same OTP script, different access code. Do NOT give it a
words env var: it is numeric-only. Note in its comment that its SSM parameter
(`/kmv/secrets/use1/ctf/announcement_code_3283`) is already seeded live, so this entry is
armed the moment the task definition carries the env var — unlike the 8283 entry below.

**telephony.toml — add the 8283 game (D-09, amended).** Append a third
`[[telephony.announcement]]` block with a header comment naming it as the 725-404-8283
game. Set `dids = ["7254048283"]`, `otp_url` and `sms_relay_url` identical to the other two
entries, `otp_env_var = "CTF_OTP_AUTH_TOKEN"`, `code_env_var = "CTF_ANNOUNCEMENT_CODE_UCTF"`,
`words_env_var = "CTF_ANNOUNCEMENT_WORDS_UCTF"`, `sms_dids = []`, and
`sms_reply_dids = ["7254048283"]`.

Its `line_template` is the SAME OTP gag template string as the 3234 and 3283 entries — same
script for now, no placeholder. All three entries end up carrying an identical template;
only `dids`, `code_env_var`, `words_env_var`, and `sms_reply_dids` differ. Comment that a
distinct 8283 script can land later as a TOML-only edit, and that any replacement must keep
a `{code}` substitution or the loader rejects the whole config at boot.

Comment the 8283 entry's split launch state: its code param is seeded live so the numeric
trigger works immediately, while its words param holds the `__unset__` sentinel so the
spoken trigger is inert until an operator replaces that value. Point at the sentinel
constant in `controller.py` as the thing that gives the sentinel its meaning — do not
restate the rule.

All three entries must resolve to DISTINCT code values — the controller's armed-trigger
registry is keyed by the resolved code VALUE regardless of DID scope, so two entries
sharing a value collide. That constraint is already documented on the first entry;
cross-reference it from the two new blocks rather than restating it at length.

**telephony.toml — OTP-only (D-01).** Extend `otp_only_dids` to
`["7254043234", "7254043283", "7254048283"]` and update the comment above it, which
currently says "the two Las Vegas DIDs", to describe all three and to reference the
per-game entries rather than "the global 333266 announcement".

**service.hcl — SSM valueFrom wiring (D-10, amended: FOUR announcement secrets, legacy
entry removed).** Replace the existing `CTF_ANNOUNCEMENT_CODE` secrets block with four
entries, following its shape and comment style:
  - `CTF_ANNOUNCEMENT_CODE_3234` from
    `arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/ctf/announcement_code_3234`
  - `CTF_ANNOUNCEMENT_CODE_3283` from
    `arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/ctf/announcement_code_3283`
  - `CTF_ANNOUNCEMENT_CODE_UCTF` from
    `arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/ctf/announcement_code_uctf`
  - `CTF_ANNOUNCEMENT_WORDS_UCTF` from
    `arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/ctf/announcement_words_uctf`

The legacy `CTF_ANNOUNCEMENT_CODE` entry (pointing at
`/kmv/secrets/use1/ctf/announcement_code`) is REMOVED in this same edit — no TOML entry
references that env var name any more, so leaving it wired would be dead config. Do NOT
delete the legacy SSM PARAMETER itself: the currently-running task definition still
references it, and deleting it before cutover would break the live service. That deletion
is a post-verification operator step recorded in the SUMMARY.

If you leave a comment noting the removal, describe it by concept (the pre-per-DID legacy
code wiring) rather than reproducing the old assignment line or the old parameter path
verbatim — this task's verification greps for those exact structural forms to prove the
wiring is gone, and an echoed literal in a comment would defeat the check.

Do NOT touch `task_role_iam_statements`. Container `secrets` are resolved by the ECS task
EXECUTION role, not the task role — which is why the existing `/kmv/secrets/use1/ctf/*`,
`/deepgram/*`, `/anthropic/*`, `/elevenlabs/*`, and `/ledger/*` valueFrom entries already
work despite none of those prefixes appearing in the task-role list. Widening task-role IAM
here would be unnecessary privilege.

Comment the launch-ordering hazard on the new block: ECS fails task launch outright on a
missing valueFrom parameter. Record that ALL FOUR parameters are already seeded live and
readback-verified, so this wiring is deploy-safe as written — and that the words parameter
deliberately holds the `__unset__` sentinel rather than real words, which is precisely how
a deploy-safe wiring coexists with an inert spoken trigger. Do not run terraform and do not
write to or delete anything in SSM in this task.

**Tests.** Three existing shipped-TOML assertions WILL fail after this edit and must be
updated, not deleted:
  - `test_shipped_telephony_toml_announcement_dids_still_global` asserts ONE announcement
    entry with `dids == ()`. Retarget it to assert three entries with their per-DID scoping,
    and rename it accordingly.
  - `test_shipped_telephony_toml_arms_sms_did_and_relay` asserts entry zero's
    `sms_reply_dids` carries all three DIDs. Retarget it to the narrowed single-DID tuple.
  - `test_shipped_telephony_toml_seeds_both_vegas_otp_only_dids` covers two DIDs. Extend it
    to all three and rename to match.
Any assertion or fixture naming `CTF_ANNOUNCEMENT_CODE` as the live env var must move to
`CTF_ANNOUNCEMENT_CODE_3234`. That includes the synthetic `_announcement_entry` helper's
default in `test_telephony_controller.py` — it is an arbitrary fixture value, but renaming
it keeps a grep for the retired name at zero so no future reader mistakes it for a live one.

Add new assertions covering the two new entries: the 3283 entry's `dids`, its
`code_env_var` NAME, its `sms_reply_dids`, its empty words env var, and that its
`line_template` is identical to the 3234 entry's; and the 8283 entry's `dids`, both env var
NAMES, and its `sms_reply_dids`. Assert env var NAMES only — never a value. Add one
assertion that all THREE entries share one identical `line_template` string and that it
contains a `{code}` placeholder, and one that all three `code_env_var` names are distinct
(the cheapest available proxy for the distinct-code-value constraint, since the values
themselves live only in SSM).
  </action>
  <verify>
    <automated>cd apps/voice && uv run pytest tests/test_telephony_config.py tests/test_telephony_controller.py tests/test_telephony_gate.py tests/test_telephony_lifecycle.py tests/test_telephony_sms.py -q && cd .. && HCL=infra/terraform/live/site/services/telephony-edge/service.hcl && test "$(grep -v '^[[:space:]]*#' $HCL | grep -c 'CTF_ANNOUNCEMENT_\(CODE\|WORDS\)_\(3234\|3283\|UCTF\)')" = "4" && test "$(grep -v '^[[:space:]]*#' $HCL | grep -c 'announcement_\(code\|words\)_\(3234\|3283\|uctf\)')" = "4" && test "$(grep -c '= "CTF_ANNOUNCEMENT_CODE"' $HCL)" = "0" && test "$(grep -c 'ctf/announcement_code"' $HCL)" = "0"</automated>
  </verify>
  <done>
- The shipped telephony.toml parses to exactly three announcement entries: 3234 (`CTF_ANNOUNCEMENT_CODE_3234`, no words env var), 3283 (`CTF_ANNOUNCEMENT_CODE_3283`, no words env var), and 8283 (`CTF_ANNOUNCEMENT_CODE_UCTF` + `CTF_ANNOUNCEMENT_WORDS_UCTF`).
- All three entries carry one identical `line_template` containing `{code}`; they differ only in `dids`, `code_env_var`, `words_env_var`, and `sms_reply_dids`.
- Each entry's `dids` and `sms_reply_dids` are its own single DID; all three `code_env_var` names are distinct.
- `otp_only_dids` carries all three Vegas DIDs.
- service.hcl declares all four per-DID announcement env var names against their SSM parameter paths, removes the legacy `CTF_ANNOUNCEMENT_CODE` entry, records that all four params are already seeded (words = the `__unset__` sentinel) so the wiring is deploy-safe, and leaves `task_role_iam_statements` untouched.
- No env var name matching the legacy bare `CTF_ANNOUNCEMENT_CODE` remains in either service.hcl or telephony.toml.
- The full telephony suite passes against the shipped config.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 3: Go — shared announcement parser, kv telephony list games section, kv studio games panel</name>
  <files>kv/internal/app/studio/types.go, kv/internal/app/studio/repofile_adapter.go, kv/internal/app/studio/view.go, kv/internal/app/studio/server.go, kv/internal/app/studio/web/index.html, kv/internal/app/studio/web/app.js, kv/internal/app/studio/repofile_adapter_test.go, kv/internal/app/studio/view_test.go, kv/internal/app/cmd/telephony.go, kv/internal/app/cmd/telephony_test.go</files>
  <behavior>
    - `ParseTelephonyGames` on a fixture with multiple announcement blocks returns one entry per block, each carrying its `dids`, `code_env_var`, `words_env_var`, and `sms_reply_dids`.
    - A commented-out `# dids = [...]` example line inside a block is ignored, not parsed as live config.
    - An entry with no `words_env_var` line yields an empty words env var name (no spoken trigger).
    - A missing file returns a typed `*RepoFileError`, and every caller degrades it to an empty section rather than an error.
    - `AnnotateGameEnv` marks an env var name present-and-non-empty in the process environment as set; an absent, empty, or `__unset__`-sentinel value as not set; and an empty NAME as none — and never carries a value on the struct.
    - Parsing the SHIPPED `apps/voice/configs/telephony.toml` yields three entries (3234 / 3283 / 8283) whose DID scopes and env var names match what Task 2 wrote.
    - `printTelephony` renders a phone-games section listing DID scope, both env var names, both statuses, and `sms_reply_dids`; a global entry renders its scope as `global`; an entry with no words env var renders `(none)`.
    - The `--json` output carries a `games` array.
    - `AssembleConfig` always returns a non-nil `Games` slice, including on the ErrorBanner short-circuit path.
  </behavior>
  <action>
One parser, two consumers. `kv/internal/app/cmd/studio.go` already imports the studio
package, and `repofile_adapter.go` documents that studio must never import cmd — so the
shared parser lives in studio and cmd calls into it.

**studio/types.go.** Add a `GameEntry` struct with JSON tags in the file's existing style:
the DID scope slice, the code env var name, the code env status, the words env var name,
the words env status, and the sms reply DID slice. Document on the type that it carries env
var NAMES and set/unset status ONLY, never a secret value — the same name-only posture
`SecretRef` already documents. Add a `Games []GameEntry` field to `ConfigView`, documented
as always non-nil.

**studio/repofile_adapter.go.** Add `ParseTelephonyGames(path string) ([]GameEntry, error)`
as a package-level function taking an explicit path (so cmd can pass its own `--config`
flag value), plus a thin `RepoFiles.ReadTelephonyGames()` wrapper joining `Root` with the
existing `telephonyConfigPath` constant. Implement the parse as a minimal line scan in the
same no-new-dependency style as the existing `ReadTelephonyGate` — a `[[telephony.announcement]]`
header opens a new entry, any subsequent line beginning with `[` closes it (opening another
entry if it is another announcement header), and lines inside a block are read with the
existing `parseTOMLScalarLine`.

Add a `parseTOMLArrayLine` helper beside it for the bracketed-array keys, since
`parseTOMLScalarLine`'s quote-trimming mangles an array literal. It must skip full-line
comments exactly as `parseTOMLScalarLine` does — this is not optional hygiene: the shipped
telephony.toml contains a commented example array line inside an announcement block, and a
parser that ignored the comment marker would surface phantom config to the operator.

Add `AnnotateGameEnv(games []GameEntry) []GameEntry` in the same file: a pure function that
fills both status fields from the process environment via a lookup that treats present-but-
empty as not set. It reads names and returns statuses; it must never place a value on the
returned struct. Document on it that this reports the LOCAL shell's environment, and that
the deployed values live in SSM and reach the container through telephony-edge's task
definition — an operator reading `not set` locally is seeing their own shell, not prod.

It must also treat the `__unset__` sentinel exactly as it treats an empty value: not set.
Declare the sentinel as a package constant here so both operator surfaces agree with the
Python resolution rule (D-03a). Without this, a shell or container carrying the sentinel
would report a spoken trigger as live when it is inert — the console would be lying about
the one thing this panel exists to show.

**studio/view.go + server.go.** Add `Games []GameEntry` to `AssembleInput`, copy it through
in `AssembleConfig` defaulting to an empty non-nil slice, and set it to an empty non-nil
slice on the ErrorBanner short-circuit return as well. In `assembleConfig`, read via
`s.opts.Repo.ReadTelephonyGames()` and annotate on success, leaving the field empty on any
error — the same best-effort treatment `ReadManifest` / `ReadTopicMap` / `ReadTelephonyGate`
already get, so an unreadable telephony.toml never blocks the console. Add the two game env
var fields to `compilesToMap()` pointing at the `[[telephony.announcement]]` keys, matching
the existing telephony entries' phrasing.

**studio web UI.** In `index.html`, add a games section inside `#panel-rules` immediately
after the existing DID section, reusing that section's markup shape: an eyebrow heading and
an empty container div that app.js fills. In `app.js`, add `renderGames(cfg)` modelled
directly on `renderDids` — same row construction helpers, same lamp element for status,
same empty-state hint line when there are no entries — and call it from the same place
`renderDids(cfg)` is already called. Render the DID scope as `global` when the scope slice
is empty and the words env var as a `(none)` marker when unset. Print env var names and
status lamps only.

**cmd/telephony.go.** Add a `Games []studio.GameEntry` field to `TelephonyListReport`. In
the list command's RunE, parse via `studio.ParseTelephonyGames(configPath)` and annotate,
degrading any error to an empty slice — never a returned error, matching how
`readTelephonySecrets` and `readInboundDIDs` already refuse to let one section's failure
kill the report. In `printTelephony`, render the section after the gate-config section
using a `tabwriter` like the other tabular sections, with a following hint line stating
that the set/unset status reflects this shell's environment and that the deployed values
live in SSM via the telephony-edge task definition. Keep the empty case graceful with a
short note rather than an empty table.

**Tests.** Write them first and confirm RED. Cover the parser and annotator in
`studio/repofile_adapter_test.go` with temp-file fixtures in the style of the existing
`ReadTelephonyGate` tests, using `t.Setenv` for the status cases. Add the shipped-config
cross-check test that resolves `apps/voice/configs/telephony.toml` four directories up from
the studio package and skips if the file is absent — this is the guard that keeps Task 2's
TOML and this parser from drifting apart. Add the non-nil `Games` assertions to
`studio/view_test.go` for both the normal and ErrorBanner paths. Cover the rendered text
section, the JSON shape, and the empty-degradation case in `cmd/telephony_test.go`,
mirroring the existing `TestPrintTelephony_*` tests. At least one rendering test must assert
that a seeded secret VALUE does not appear anywhere in the output while its env var NAME
does.

Run `gofmt` over every touched Go file before committing.
  </action>
  <verify>
    <automated>cd kv && gofmt -l . | tee /dev/stderr | wc -l | grep -qx '[[:space:]]*0' && go build ./... && go test ./...</automated>
  </verify>
  <done>
- `studio.ParseTelephonyGames` + `RepoFiles.ReadTelephonyGames` + `studio.AnnotateGameEnv` exist, ignore commented lines, and are consumed by BOTH `kv telephony list` and `kv studio` with no duplicated parser.
- The shipped telephony.toml cross-check test passes against Task 2's three entries.
- `kv telephony list` renders a phone-games section (text and JSON) carrying DID scope, both env var names, both statuses, and sms_reply_dids — with no secret value in the output.
- `ConfigView.Games` is always non-nil, degrades to empty on an unreadable telephony.toml, and renders in a read-only studio panel.
- `AnnotateGameEnv` reports the `__unset__` sentinel as not set, matching the Python resolution rule, with a test covering it.
- `gofmt` clean; `go build ./...` and `go test ./...` pass.
  </done>
</task>

</tasks>

<threat_model>
## Trust Boundaries

| Boundary | Description |
|----------|-------------|
| PSTN caller speech → Deepgram STT → GateProcessor | Untrusted audio becomes untrusted text; this is the first point a caller can influence control flow via speech. |
| GateProcessor → controller announcement dispatch | New seam this task introduces; the pre-unlock redaction boundary sits exactly here. |
| SSM SecureString → ECS container env | Both new game secrets cross here at task launch, resolved by the ECS execution role. |
| telephony.toml (git) → kv operator surfaces | Public config read by two local operator tools; must never surface a secret value. |

## STRIDE Threat Register

| Threat ID | Category | Component | Severity | Disposition | Mitigation Plan |
|-----------|----------|-----------|----------|-------------|-----------------|
| T-pdh-01 | Information disclosure | GateProcessor spoken-trigger seam | high | mitigate | The gate matches words internally and passes only an opaque, non-secret entry key to the callback; the accumulated token set never crosses the processor boundary. No new log line on the path; `cancel_for_takeover` logs only reason + call_id. Proven by a `loguru_caplog` test asserting no heard word, matched word, or key is logged (Task 1). |
| T-pdh-02 | Information disclosure | Pre-unlock transcript forwarding | critical | mitigate | D-06 changes accumulation only; the `TranscriptionFrame` branch still `return`s without `push_frame` in every path, and `_unlocked` is never set by the announcement factor. Proven by a `run_test` assertion that a downstream sink receives zero frames during and after a spoken match (Task 1). |
| T-pdh-03 | Elevation of privilege | Spoken game trigger on a non-owning DID | high | mitigate | The per-call phrase map is filtered through the existing fail-closed `_announcement_matches_did` predicate before the GateProcessor is constructed, so an unresolved or foreign dialed DID can never arm a scoped entry's spoken factor. Proven by controller scoping tests (Task 1). |
| T-pdh-04 | Elevation of privilege | Concierge unlock on the new game DID | medium | mitigate | 8283 joins `otp_only_dids` (D-01), so `concierge_unlock_enabled=False` suppresses both concierge factors on that line; only a game factor or the fail-closed timer resolves its gate. |
| T-pdh-05 | Denial of service | Blocking the pipeline frame queue | high | mitigate | The announcement callback is spawned via `asyncio.create_task` with a retained strong reference, never awaited inside `process_frame` — an inline await would stall every frame including teardown control frames for the whole readout. The gate is resolved synchronously before the spawn so the fail-closed timer cannot race it. |
| T-pdh-06 | Denial of service | ECS task launch on a missing SSM parameter | high | mitigate | ECS fails task launch outright when a `valueFrom` parameter is absent, and this task wires FOUR. Mitigated at the source rather than by procedure: all four parameters are seeded live and readback-verified BEFORE this wiring lands, with the words parameter holding the `__unset__` sentinel so a deploy-safe wiring coexists with an inert spoken trigger (D-03a/D-10). Recorded as a comment on the new secrets block. |
| T-pdh-10 | Denial of service | Premature deletion of the legacy code parameter | medium | mitigate | The running task definition still references `/kmv/secrets/use1/ctf/announcement_code`; deleting it before the new task definition is deployed and verified would fail task launch on the next restart. Mitigated by scoping this task to the service.hcl `valueFrom` removal ONLY — no SSM deletion — and by recording the deletion as an explicitly post-cutover operator step in the SUMMARY. |
| T-pdh-09 | Spoofing | Sentinel treated as a live passphrase | medium | mitigate | If the `__unset__` sentinel were tokenized into a phrase set, speaking that token would fire the game. Prevented by skipping registry construction entirely on an exact whole-value sentinel match, before any tokenization — with a test asserting the sentinel value arms no spoken trigger, and a second asserting a real phrase merely CONTAINING the token is unaffected (Task 1). Both operator surfaces apply the same rule so the console cannot report an inert trigger as live (Task 3). |
| T-pdh-07 | Information disclosure | kv operator surfaces leaking secrets | medium | mitigate | Both surfaces read env var NAMES from git-backed TOML and compute a set/unset status without ever reading a value; `GameEntry` has no value field. Proven by a rendering test asserting a seeded value is absent from the output while its name is present (Task 3). |
| T-pdh-08 | Tampering | Comment-line misparse in the Go TOML scanner | low | mitigate | The new array-line parser skips full-line comments exactly as `parseTOMLScalarLine` does; the shipped config contains a commented example array inside an announcement block, and a fixture test asserts it is ignored (Task 3). |
| T-pdh-SC | Tampering | npm/pip/cargo installs | high | mitigate | Not applicable — this task adds NO new dependency in any ecosystem. The Go parser is deliberately a line scan rather than a new TOML module (existing precedent), and the Python changes use only stdlib plus already-vendored pipecat/loguru. |
</threat_model>

<verification>
1. `cd apps/voice && uv run pytest tests/test_telephony_config.py tests/test_telephony_controller.py tests/test_telephony_gate.py tests/test_telephony_lifecycle.py tests/test_telephony_sms.py -q` — full telephony suite green.
2. `cd kv && go build ./... && go test ./...` — green, `gofmt` clean.
3. The shipped-config cross-check test in the studio package passes, proving the Go parser and the Python loader agree on the three entries Task 2 wrote.
4. Backward compatibility is asserted, not assumed: the 3234 and 3283 entries have no words env var and keep numeric-only behavior; every spoken-factor-disabled state is a graceful skip that leaves the entry's numeric trigger armed.
5. `git diff` on `infra/terraform/live/site/services/telephony-edge/service.hcl` shows four announcement `secrets` entries added, the single legacy `CTF_ANNOUNCEMENT_CODE` entry removed, and no `task_role_iam_statements` change.
6. The 8283 launch state is provable from the tests alone: numeric trigger armed, spoken trigger inert behind the sentinel.
7. A repo-wide grep for the retired bare `CTF_ANNOUNCEMENT_CODE` env var name returns no live reference in telephony.toml, service.hcl, or the Python tests.

Out of scope, operator steps to record in the SUMMARY:
- Replacing the `__unset__` sentinel in `/kmv/secrets/use1/ctf/announcement_words_uctf` with real whitespace-separated words — the ONLY remaining step to turn the 8283 spoken trigger on.
- `terraform apply` / redeploy to publish the four per-DID env var wirings.
- **After that deploy is verified live**, delete the now-orphaned legacy SSM parameter `/kmv/secrets/use1/ctf/announcement_code`. NOT before: the currently-running task definition still references it.
- Optionally, a distinct `line_template` for the 8283 entry later (a TOML-only edit; must keep a `{code}` placeholder).

NOT operator steps — already done, and MUST NOT be listed as pending: all four SSM
parameters (`announcement_code_3234`, `announcement_code_3283`, `announcement_code_uctf`,
`announcement_words_uctf`) are seeded live and readback-verified, so the valueFrom wiring is
deploy-safe as written.
</verification>

<success_criteria>
- Entering the 8283 game's DTMF code on 725-404-8283 fires that game's script today; speaking its secret words fires the same script once the words param's sentinel is replaced (either-factor, D-04).
- The 3234 game's code fires only when dialing 3234, the 3283 game's code only when dialing 3283, and neither is redeemable on 8283 or 613 (D-02, amended).
- 725-404-8283 is OTP-only: neither concierge factor can open its gate (D-01).
- The gate accumulates tokens on OTP-only DIDs yet forwards no pre-unlock frame and logs no heard or matched word (D-06 + D-07).
- All four spoken-factor-disabled states — field absent, env var absent, empty value, `__unset__` sentinel — skip gracefully and leave the numeric trigger armed; the existing numeric-only entries are unchanged (D-03, D-03a, D-08).
- `kv telephony list` and `kv studio` both show the game entries with env var NAMES and set/unset status only, and studio degrades to an empty section on an unreadable config (D-11).
- service.hcl declares both new SSM valueFrom secrets with the seed-before-deploy hazard documented; no terraform run, no SSM seeding (D-10).
</success_criteria>

<output>
Create `.planning/quick/260727-pdh-second-phone-game-on-725-404-8283-per-ga/260727-pdh-SUMMARY.md` when done.

The SUMMARY MUST carry, under User Setup Required:
1. Replace the `__unset__` sentinel in `/kmv/secrets/use1/ctf/announcement_words_uctf` with real whitespace-separated spoken-trigger words. This is the ONLY step that turns the 8283 spoken trigger on — everything else about that game ships live.
2. Apply the terraform change and redeploy telephony-edge to publish the four per-DID env var wirings.
3. **After that deploy is verified live**, delete the orphaned legacy SSM parameter `/kmv/secrets/use1/ctf/announcement_code`. Do NOT delete it before cutover — the currently-running task definition still references it, and an early deletion would fail task launch on the next restart.
4. (Optional, later) Give the 8283 entry its own `line_template` — a TOML-only edit that must keep a `{code}` placeholder. All three entries currently share one identical script.

The SUMMARY MUST also state plainly that all four SSM parameters are ALREADY seeded and
readback-verified, so the `valueFrom` wiring is deploy-safe and there is NO seed-before-deploy
step — the words param intentionally holds a sentinel rather than being absent, precisely
because ECS fails task launch on a missing parameter.

It MUST record the `__unset__` sentinel contract: which module-level constant defines it in
`controller.py`, that both `kv` operator surfaces apply the same rule, and that the 8283
game therefore ships with its numeric trigger live and its spoken trigger inert.

The SUMMARY MUST NOT contain any code value, spoken-trigger word, or other secret material —
env var and SSM parameter NAMES only. The `__unset__` sentinel is a public, non-secret
placeholder and may be named.

It MUST also record the `passphrase_env_var` → `words_env_var` rename and its cause (the D-09 credential-field regex refuses any key carrying a `passphrase` token, same precedent as `otp_auth_env_var` → `otp_env_var`).
</output>
