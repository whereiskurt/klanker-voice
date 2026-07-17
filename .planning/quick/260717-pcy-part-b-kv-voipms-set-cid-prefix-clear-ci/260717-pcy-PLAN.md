---
phase: quick-260717-pcy
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
  - kv/internal/app/cmd/voipms.go
  - kv/internal/app/cmd/voipms_test.go
autonomous: true
requirements:
  - CID-TOOL-B
must_haves:
  truths:
    - "`kv voipms set-cid-prefix <did> <tag>` sets callerid_prefix=<tag> on the DID without wiping any other DID setting"
    - "`kv voipms clear-cid-prefix <did>` sets callerid_prefix=\"\" on the DID, same full-snapshot preserve"
    - "cnam is forced to 0 on every set/clear so the prefix rides through (CNAM lookup never overwrites the caller-ID name)"
    - "the setDIDInfo call is a full re-send of the getDIDsInfo snapshot (routing/pop/dialtime/billing_type/failover_* preserved), not a partial update"
    - "a follow-up getDIDsInfo readback verifies routing was preserved and the prefix landed"
    - "transient VoIP.ms Cloudflare failures are retried (bounded) rather than surfaced on first blip"
  artifacts:
    - kv/internal/app/cmd/voipms.go
    - kv/internal/app/cmd/voipms_test.go
  key_links:
    - "getDIDsInfo snapshot map -> setDIDInfo param assembly (the full-replace preserve is where a dropped field silently wipes a setting)"
    - "cnam forcing (cnam=0) is the live-proven silent-failure guard"
---

<objective>
Part B of the per-DID CID tooling spec: add two `kv voipms` subcommands that automate the
hand-run `setDIDInfo` dance for enrolling a DID's caller-ID name prefix (used by Approach C
edge `dialed_did` resolution and per-DID SMS reply).

- `kv voipms set-cid-prefix <did> <tag>` — sets `callerid_prefix=<tag>` on `<did>`.
- `kv voipms clear-cid-prefix <did>` — sets `callerid_prefix=""` on `<did>`.

Both bake in the hard-won, live-proven gotchas so the operator never re-learns them:
full-snapshot preserve (setDIDInfo is full-replace), forced `cnam=0`, verify readback, and
bounded retry against the flaky VoIP.ms Cloudflare front.

Purpose: turn a multi-step manual API dance into one safe, idempotent command.
Output: extended `kv/internal/app/cmd/voipms.go` + mirrored unit tests in `voipms_test.go`.

Scope guard: Part B ONLY. Do NOT touch the Part A gate policy (apps/voice) or the Part C
`kv studio` surface.
</objective>

<execution_context>
@$HOME/.claude/gsd-core/workflows/execute-plan.md
@$HOME/.claude/gsd-core/templates/summary.md
</execution_context>

<context>
@.planning/STATE.md
@docs/superpowers/specs/2026-07-17-per-did-gate-policy-and-cid-tooling.md

# The seam being extended — read fully before editing:
@kv/internal/app/cmd/voipms.go
@kv/internal/app/cmd/voipms_test.go
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Implement getDIDInfo snapshot + setDIDInfo prefix helpers and the two subcommands</name>
  <files>kv/internal/app/cmd/voipms.go</files>
  <behavior>
    - getVoipmsDIDInfo(ctx, vc, did) returns the raw snapshot map for a single DID:
      calls getDIDsInfo with a `did` param, defensively handles both the array and
      single-bare-object `dids` shapes (mirror ListInboundDIDs), returns the matching
      record's full map[string]any (not the 4-field InboundDIDRecord). Blank did -> error
      before any network call. Not-found -> clear error.
    - setVoipmsDIDPrefix(ctx, vc, did, prefix) performs the full-replace dance:
        1. snapshot := getVoipmsDIDInfo(did)
        2. build setDIDInfo params by forwarding the preserve-allowlist fields from the
           snapshot under their setDIDInfo param names (see <action> for the exact list),
           then FORCE cnam="0" and SET callerid_prefix=prefix (may be "").
        3. call setDIDInfo
        4. readback := getVoipmsDIDInfo(did); assert routing preserved (non-empty, equals
           the snapshot routing) AND callerid_prefix == prefix; error if either fails.
    - Each VoIP.ms call in this path is wrapped in a bounded retry (N=3, short backoff)
      that retries transport/5xx/Cloudflare-522-shaped failures but NOT a clean
      voipmsStatusError (a real API rejection like no_did is terminal, not retried).
  </behavior>
  <action>
Add a new centralized method constant next to the existing ones: `voipmsMethodSetDIDInfo = "setDIDInfo"` (VoIP.ms's DID-settings writer; getDIDsInfo already has `voipmsMethodGetDIDsInfo`). Keep the "rest.php base URL appears exactly once" invariant intact — only add a method-name const, not a second base URL.

Add `getVoipmsDIDInfo(ctx, vc *voipmsClient, did string) (map[string]any, error)`: reject blank did before the network; call `vc.do(ctx, voipmsMethodGetDIDsInfo, params-with-did)`; parse the `dids` value handling BOTH shapes exactly like ListInboundDIDs (`[]any` and bare `map[string]any`); return the record whose `did` field matches (or the sole record for the single-object shape); return a clear "DID %s not found" error if absent. Return the FULL map, not InboundDIDRecord — the caller needs every field to re-send.

Add a bounded-retry helper (VoIP.ms's Cloudflare front intermittently 522s from some egress — the spec calls this out; `vc.do()` today has NO retry, confirmed by reading voipms.go). Implement `voipmsDoWithRetry(ctx, vc, method, params)` (or an equivalent small wrapper) that calls `vc.do` up to 3 times with a short backoff (e.g. 500ms, 1s), retrying ONLY transport-layer errors / non-2xx transport failures, and returning immediately on success OR on a `*voipmsStatusError` (a real API-level rejection — do not hammer it). Route BOTH the getDIDsInfo and setDIDInfo calls in this feature through it. Do not change the existing subcommands' call sites. Never log creds (the do() envelope already guards *url.Error unwrap — preserve it; do not add any log line that stringifies params or the request URL).

Add `setVoipmsDIDPrefix(ctx, vc *voipmsClient, did, prefix string) error`:
- snapshot via getVoipmsDIDInfo.
- Build `url.Values` for setDIDInfo. Forward these snapshot fields (VoIP.ms getDIDsInfo key -> setDIDInfo param, names are 1:1) WHEN present and non-empty in the snapshot: `did`, `routing`, `pop`, `dialtime`, `billing_type`, `description`, `note`, `failover_busy`, `failover_unreachable`, `failover_noanswer`, `voicemail`, `canada_routing`. Read each via the same defensive string coercion pattern didRecordFromMap uses (string, else fmt.Sprint). Rationale comment: setDIDInfo is FULL-REPLACE — any accepted field omitted here is wiped server-side; this is the live-proven trap.
- Then unconditionally FORCE `params.Set("cnam", "0")` (overriding any snapshot cnam) — cnam=1 makes VoIP.ms overwrite the caller-ID NAME via CNAM lookup so the prefix never rides through (live-proven silent failure on 3283). Add a short comment saying so.
- Then `params.Set("callerid_prefix", prefix)` (prefix may be "" for the clear path).
- Call setDIDInfo through the retry wrapper; wrap any error with the did for context.
- Readback: getVoipmsDIDInfo(did) again; verify `routing` non-empty and equals the snapshot routing, and `callerid_prefix` equals `prefix`; return a descriptive error naming the did if the readback disagrees (this catches a cnam-clobbered prefix even when the API returned success).

Wire two subcommands into NewVoipmsCmd (mirror route-did's structure exactly — `cobra.ExactArgs`, resolveVoipmsCreds, newVoipmsClient, a human confirmation line to c.OutOrStdout()):
- `set-cid-prefix <did> <tag>` — `Args: cobra.ExactArgs(2)`; calls setVoipmsDIDPrefix(ctx, vc, args[0], args[1]); on success prints e.g. `set caller-ID prefix %q on DID %s (cnam forced 0, routing preserved)`.
- `clear-cid-prefix <did>` — `Args: cobra.ExactArgs(1)`; calls setVoipmsDIDPrefix(ctx, vc, args[0], ""); on success prints e.g. `cleared caller-ID prefix on DID %s`.
Register BOTH via `voipmsCmd.AddCommand(...)`. Update the NewVoipmsCmd doc comment's "Sub-commands:" line to include the two new names.
  </action>
  <verify>
    <automated>cd kv && go build ./...</automated>
  </verify>
  <done>kv builds clean; `kv voipms` exposes set-cid-prefix and clear-cid-prefix; setVoipmsDIDPrefix forwards the snapshot preserve-list, forces cnam=0, sets callerid_prefix, and readback-verifies; VoIP.ms calls in this path go through the bounded retry; no creds are logged.</done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: Unit-test the setDIDInfo param assembly against a fake client</name>
  <files>kv/internal/app/cmd/voipms_test.go</files>
  <behavior>
    - A fake handler serves getDIDsInfo (snapshot + readback) and setDIDInfo, branching on
      the `method` query param, and captures the setDIDInfo params it received.
    - set-cid-prefix path: assert (a) routing/pop/dialtime/billing_type from the snapshot are
      re-sent to setDIDInfo (preserve), (b) cnam == "0" even though the snapshot cnam was "1",
      (c) callerid_prefix == the requested tag, (d) the readback getDIDsInfo path ran (method
      seen at least twice), (e) did param present.
    - clear-cid-prefix path: same preserve + cnam=0 assertions, but callerid_prefix == "".
    - Subcommand registration test asserts `kv voipms` now lists set-cid-prefix and
      clear-cid-prefix (extend the existing TestVoipmsCmdHelpListsSubcommands want-map, or add
      a sibling test).
  </behavior>
  <action>
Mirror the existing fake-do harness (newTestVoipmsClient + httptest handler capturing `r.URL.Query()`). Write a handler that reads `method := r.URL.Query().Get("method")` and:
- for `getDIDsInfo`: returns a canned single-DID snapshot with a non-default `cnam` of "1" and a distinctive routing/pop/dialtime/billing_type so preservation is observable, e.g. `{"status":"success","dids":{"did":"7254043234","description":"Las Vegas","routing":"account:557010_klanker-pbx","pop":"5","dialtime":"60","cnam":"1","billing_type":"1","callerid_prefix":"<echo the last-set value>"}}`. To make the readback pass, have the handler track the last setDIDInfo `callerid_prefix`/`cnam` it received and reflect them in subsequent getDIDsInfo responses (a small closure var), so the readback verification in setVoipmsDIDPrefix sees the value it just set.
- for `setDIDInfo`: capture `r.URL.Query()` into an outer variable and return `{"status":"success"}`.

TestVoipmsSetCidPrefix_AssemblesFullSnapshotForcesCnam0: call setVoipmsDIDPrefix(ctx, vc, "7254043234", "KVD3234"); assert the captured setDIDInfo query has did=7254043234, routing=account:557010_klanker-pbx, pop=5, dialtime=60, billing_type=1 (preserve), cnam=0 (forced, NOT the snapshot "1"), callerid_prefix=KVD3234, and method=setDIDInfo.

TestVoipmsClearCidPrefix_EmptiesPrefixPreservesRest: call setVoipmsDIDPrefix(ctx, vc, "7254043234", ""); assert callerid_prefix=="" (present but empty), cnam=0, and routing/pop preserved.

Add a readback/verify-path assertion: track how many getDIDsInfo calls the handler saw and assert >= 2 (snapshot + readback) after a successful setVoipmsDIDPrefix.

Extend TestVoipmsCmdHelpListsSubcommands's want map with "set-cid-prefix" and "clear-cid-prefix" (or add TestVoipmsCidPrefixSubcommandsRegistered) so the command wiring is covered.

Do NOT assert on api_username/api_password beyond what the existing tests already cover; keep the credential-leak invariant tests untouched. Follow the existing test naming/style (t.Context(), table-free explicit asserts, t.Errorf messages naming the param).
  </action>
  <verify>
    <automated>cd kv && go test ./...</automated>
  </verify>
  <done>go test ./... passes; new tests prove snapshot preserve, forced cnam=0, prefix set for set / emptied for clear, the readback path runs, and both subcommands are registered.</done>
</task>

</tasks>

<threat_model>
## Trust Boundaries

| Boundary | Description |
|----------|-------------|
| operator CLI -> VoIP.ms REST API | api_username/api_password ride in the query string; must never be logged |
| VoIP.ms response -> setDIDInfo re-send | a dropped snapshot field silently wipes a live DID setting |

## STRIDE Threat Register

| Threat ID | Category | Component | Severity | Disposition | Mitigation Plan |
|-----------|----------|-----------|----------|-------------|-----------------|
| T-pcy-01 | Information Disclosure | voipmsClient.do() request URL / new retry wrapper | high | mitigate | Reuse the existing do() *url.Error unwrap; add NO log line that stringifies params/URL; retry wrapper must not print the request |
| T-pcy-02 | Tampering | setDIDInfo full-replace assembly | high | mitigate | Forward the snapshot preserve-allowlist verbatim + readback-verify routing preserved; forced cnam=0 guards the CNAM-clobber silent failure |
| T-pcy-03 | Denial of Service | flaky Cloudflare 522 front | low | accept | Bounded retry (N=3) only; never unbounded — a persistent outage surfaces to the operator |
</threat_model>

<verification>
- `cd kv && go build ./...` clean.
- `cd kv && go test ./...` green (existing + new tests).
- Manual read: no new log statement stringifies `params`, the request URL, or creds.
</verification>

<success_criteria>
- Two new `kv voipms` subcommands (`set-cid-prefix`, `clear-cid-prefix`) exist and are registered.
- setDIDInfo assembly preserves the snapshot fields, forces cnam=0, sets/empties callerid_prefix, and readback-verifies.
- Bounded retry wraps the VoIP.ms calls in this path; do()'s cred-leak guard is preserved.
- `go build ./...` and `go test ./...` both pass.
</success_criteria>

<output>
Create `.planning/quick/260717-pcy-part-b-kv-voipms-set-cid-prefix-clear-ci/260717-pcy-SUMMARY.md` when done.
</output>
