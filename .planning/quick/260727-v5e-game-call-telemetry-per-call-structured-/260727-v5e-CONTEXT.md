# Quick Task 260727-v5e: Game-call telemetry - Context

**Gathered:** 2026-07-27 (operator approved the proposal verbatim; capacity slice explicitly deferred)
**Status:** Ready for planning

<domain>
## Task Boundary

The operator is about to share the three phone-game DIDs (725-404-3234/3283/8283)
with a large group and needs call analytics: per-DID call volume, how callers
enter codes (attempts/timing), and game outcomes. Today this is only derivable
by hand-diving CloudWatch logs. Two deliverables:

1. ONE structured telemetry event line per call, emitted by the telephony
   controller at teardown.
2. `kv telephony stats` — a CloudWatch Logs Insights-backed summary command.

</domain>

<decisions>
## Implementation Decisions

### The per-call event (voice side)
- Exactly ONE line per call, emitted at the single teardown seam every call
  passes through (`_close_active_call` or equivalent — the place that already
  logs `_close_active_call: channel=... reason=...`), never lost to an early
  hangup path. Structured, greppable, single-line JSON with a stable marker
  prefix, e.g.: `game_call_event {"call_id": ..., "dialed_did": ...,
  "outcome": ..., ...}`.
- Fields (counts and timings ONLY — see redaction):
  - `call_id`, `dialed_did` (empty string if unresolved), `caller_id` (already
    logged today at on_stasis_start; no new exposure), `otp_only` (bool)
  - `outcome`: one of `announcement_code`, `announcement_words`,
    `concierge_unlock_dtmf`, `concierge_unlock_passphrase`, `gate_timeout`,
    `early_hangup` (caller hung up pre-unlock), `error` (pipeline/teardown
    error path). Derive from existing state (gate unlock method, announcement
    fired, fail-closed) — prefer reusing what the code already tracks over new
    state where possible.
  - `digits_entered`: count of DTMF digits received pre-unlock (count only)
  - `words_heard`: count of STT tokens accumulated pre-unlock (count only)
  - `seconds_to_outcome`: answer→unlock/announcement/fail, 1-decimal float
  - `duration_seconds`: answer→teardown, 1-decimal float
- D-05e REDACTION IS NON-NEGOTIABLE: never the DTMF digits themselves, never
  heard/matched words, never code values, never which game's code was wrong.
  Counts, outcome labels, and timings only. The existing gate_fail_heard
  opt-in fail-path debug plumbing is UNCHANGED by this task.
- Concierge (non-game) calls emit the same event (outcome
  concierge_unlock_*/gate_timeout/early_hangup) — volume analytics should
  cover all five DIDs, not just games.

### `kv telephony stats` (Go side)
- New subcommand under the existing `kv telephony` tree (cmd/telephony.go).
- Backed by CloudWatch Logs Insights (aws-sdk-go-v2 cloudwatchlogs — align
  with however kv already talks to AWS; SSO/env creds, same as other kv cmds).
- Log group: the telephony-edge group (/ecs/telephony-edge-telephony-edge-use1-kmv)
  — resolve it the way existing kv code finds telephony resources if such a
  seam exists, else a flag with that default.
- Flags: `--since` duration (default 24h), optional `--did` filter.
- Output (lipgloss-table or plain aligned text matching existing kv telephony
  list style):
  - Per-DID: total calls, outcome breakdown, median/max seconds_to_outcome,
    median duration, distinct callers count
  - A totals row. Scriptable: `--json` flag dumps the raw aggregation.
- Insights queries parse the `game_call_event ` marker + JSON fields. Never
  print caller numbers in the default table (distinct COUNT only); `--json`
  may include per-outcome counts but ALSO no raw caller numbers — hash or
  omit. (Operator can always go to raw logs for specifics.)

### Out of scope
- NO capacity/concurrency changes (operator explicitly deferred).
- NO studio panel for stats (CLI only for this task).
- NO CloudWatch dashboards/metrics/EMF — Logs Insights over the event line is
  enough at this scale.
- NO changes to gate_fail_heard or the transcript ledger.

### Claude's Discretion
- Exact event marker/prefix naming and outcome label spellings.
- How outcome derivation threads through ActiveCall state (small additive
  fields on ActiveCall are fine).
- Insights query shape vs. client-side aggregation split.

</decisions>

<specifics>
## Specific Ideas

- The stats command exists so the operator can answer, during the event:
  "how many people are calling each number, are they getting through the
  code entry, and where do they give up?"
- Event volume is tiny (a few hundred calls) — favor simple/robust over clever.

</specifics>

<canonical_refs>
## Canonical References

- apps/voice/src/klanker_voice/telephony/controller.py (teardown seam
  `_close_active_call` ~line 1746; ActiveCall state; announcement dispatch;
  gate unlock callback wiring)
- apps/voice/src/klanker_voice/telephony/gate.py (D-05e redaction contract;
  token accumulation — words_heard count source; unlock methods)
- apps/voice/tests/test_telephony_controller.py, test_telephony_gate.py,
  test_telephony_lifecycle.py (test patterns)
- kv/internal/app/cmd/telephony.go (kv telephony tree + games section
  display style), kv/internal/app/cmd/usage.go (existing AWS-backed kv
  command patterns, cloudwatchlogs usage if any)
- .planning/quick/260727-pdh-*/260727-pdh-SUMMARY.md and
  260727-qfq-*/260727-qfq-SUMMARY.md (the per-game architecture this
  instruments)

</canonical_refs>
