# Quick Task 260806-cm9: `kv telephony calls` — call report — Context

**Gathered:** 2026-08-06
**Status:** Ready for planning

<domain>
## Task Boundary

Add a new `kv telephony calls` subcommand: an operator call report answering
**who called which number and when**, from the telephony-edge CloudWatch logs.

This has been produced twice as an ad-hoc Logs Insights query + Python script during
operator sessions. Making it a first-class command is the point of this task.

Out of scope: changing `kv telephony stats` in any way; changing anything in
`apps/voice/**`; any new AWS infrastructure.

</domain>

<decisions>
## Implementation Decisions (LOCKED — operator KPH, answered 2026-08-06)

### Command shape
A NEW `kv telephony calls` subcommand, sibling to `kv telephony stats`.
`stats` stays **byte-identical** — do not add flags to it, do not refactor its
aggregation, do not touch `telephony_stats.go`'s existing exported behavior.
(Shared helpers like `RunInsightsQuery` SHOULD be reused — reuse is fine, mutation is not.)

### Redaction posture
**Raw caller numbers are ALWAYS shown** in this command. No `--identify` flag, no
`--mask` flag — a flag to disable the command's entire purpose would be dead weight.

The D-05e boundary becomes **which command you run**: `stats` is the caller-anonymous
rollup (distinct-caller COUNTS only, never numbers — that is its documented contract);
`calls` is the identity-bearing report. Say this explicitly in the new command's
`Long` help text so the distinction is discoverable, and add a one-line pointer in
`stats`' own help noting that `calls` is where identity lives.

### Views (all four in v1)
1. **Per-caller rollup** — who called, count, first/last seen, breakdown of which DIDs
   they hit. (The default view.)
2. **Chronological per-call log** — one row per call: when, caller, DID, outcome,
   digits/words, duration.
3. **Per-number rollup** — per DID: call count, distinct callers, active range.
4. **New-caller / first-seen alerts** — callers whose first-ever appearance falls inside
   the queried window. Useful live during an event to spot fresh players.

Claude's discretion on how these are selected (subcommands vs `--view` flag vs printing
several in one pass) and on flag naming, but `--json` must emit the whole structured
result set (all views' underlying data), not just whichever view is being rendered.

</decisions>

<verified_facts>
## Facts verified against live data + source this session — do NOT re-derive

**⚠️ TRAP: CloudWatch `@timestamp` is NOT the event time.** Lines are batched at
ingestion, so several genuinely distinct calls can share one `@timestamp`. Proven:
channels `1785303830.6`, `1785303848.7`, `1785303877.8` (~18s and ~29s apart by their
own epoch-encoded ids) all carry `@timestamp = 2026-07-29 05:46:13`. The **loguru
timestamp embedded at the start of the message** is the real event time; the **ARI
channel id also encodes epoch seconds** before the `.` (`1785303830.6` → epoch
1785303830). Use one of those, not `@timestamp`, or the chronological view and the
first/last-seen columns will be wrong. `kv telephony stats` sorts by `@timestamp`
today — that is a pre-existing wrinkle in `stats`, NOT something to fix here.

**Coverage is not 1:1 — the report MUST join two sources.**
- `game_call_event` (teardown telemetry, quick 260727-v5e) carries `call_id`,
  `dialed_did`, `caller_id`, `outcome`, `digits_entered`, `words_heard`,
  `seconds_to_outcome`, `duration_seconds` — but only exists since ~2026-07-28,
  and even after that does not cover every call: **92 inbound calls since 2026-07-28
  vs only 82 `game_call_event` lines.** Calls that die very early can produce a stasis
  line and no teardown event.
- `on_stasis_start` gives full history back to 2026-07-12 and comes in TWO line shapes
  that must be joined **by ARI channel id**:
  - `on_stasis_start: channel=<id> caller=<number> did=<exten>` — carries the caller.
    NOTE `did=` here is the **sub-account name** (`557010_klanker-pbx`), NOT the dialed
    DID. Ignore it.
  - `on_stasis_start: channel=<id> dialed_did=<digits> exten=… cidname=… sip_to=…` —
    carries the real dialed DID (or `<none>`). Only exists since the Approach-C
    CID-prefix resolution deploy (~2026-07-17).
  - A third shape, `on_stasis_start: ignoring external-media leg channel=…`
    (`UnicastRTP`), is an internal media leg — **must be excluded**, it is not a call.
- Net over the full history: **209 real inbound calls**, of which 81 (all before
  ~2026-07-17) have a caller + time but **no recoverable dialed DID** — render those as
  an explicit "unknown (pre-resolution)" bucket, never silently drop or mislabel them.

**DID labelling.** `kv` already reads the voice config best-effort at
`defaultTelephonyConfigPath = "apps/voice/configs/telephony.toml"`
(`kv/internal/app/cmd/telephony.go:406`) — reuse that path constant and whatever parsing
`kv telephony list` already does. `kv/go.mod` has **no TOML library**, so do not add one
without checking how the existing reader works first. DID→label mapping lives in
`[telephony.cid_prefix_dids]` (`KVD3234 = "7254043234"` etc.). An unresolved/empty
`dialed_did` means an untagged DID — in practice the 613/347 concierge lines, which have
no `callerid_prefix` set and therefore collapse into one bucket. Label that bucket
honestly (e.g. "concierge (untagged DIDs)"), do not invent a specific number for it.

**Reusable seams in `kv/internal/app/cmd/telephony_stats.go`:**
- `RunInsightsQuery(ctx, client, logGroup, start, end, pollInterval)` — start-query +
  poll loop, already handles the Insights lifecycle. Reuse it.
- `defaultTelephonyLogGroup = "/ecs/telephony-edge-telephony-edge-use1-kmv"`
- `callEventMarker = "game_call_event"`, bound to the Python `CALL_EVENT_MARKER` by an
  existing Go test that reads the Python source directly — if you introduce new marker
  literals for the `on_stasis_start` shapes, consider the same discipline.
- `cfg.CloudWatchLogsClient(ctx)`, `--since` / `--json` / `--log-group` flag patterns.

**Expected output against real data** (full history as of 2026-08-06 13:52 UTC), useful
as a sanity check while developing — 209 calls, 8 distinct callers:
| caller | calls |
|---|---|
| +15197101515 (operator test line) | 160 |
| +61437008930 | 19 |
| +12672520810 | 13 |
| +18022337051 | 10 |
| +13124432920 | 3 |
| +16479213102 | 2 |
| +14167979698 | 1 |
| +16135313189 | 1 |
Per DID: 3234 → 51, concierge/untagged → 22, toll-free 8559164636 → 22, 3283 → 21,
8283 → 12, unknown/pre-resolution → 81.

</verified_facts>

<specifics>
## Specific Ideas

- New file `kv/internal/app/cmd/telephony_calls.go` + `telephony_calls_test.go`,
  registered onto `telephonyCmd` in `telephony.go` exactly the way
  `newTelephonyStatsCmd` already is.
- Aggregation client-side in Go over the filtered result set (a few hundred calls),
  mirroring the explicit precedent set by `stats` (CONTEXT of quick 260727-v5e chose
  simple/robust over a clever Insights `stats` query).
- Table output via whatever `stats` uses; `--json` for scripting.
- Test the parsers against **real captured log lines** (all three `on_stasis_start`
  shapes including the `UnicastRTP` one that must be skipped, plus a `game_call_event`
  line), and include a regression test for the batched-`@timestamp` trap — two distinct
  channels sharing one `@timestamp` must still sort and report by their true times.

</specifics>

<canonical_refs>
## Canonical References

- `.planning/quick/260727-v5e-game-call-telemetry-per-call-structured-/` — the
  `game_call_event` emission contract and the `kv telephony stats` precedent this
  command is a sibling to.
- `.planning/quick/260805-fki-telephony-gate-window-start-timer-after-/` — the most
  recent telephony change; its CONTEXT holds the telemetry analysis that motivated
  wanting this report as a real command.

</canonical_refs>
