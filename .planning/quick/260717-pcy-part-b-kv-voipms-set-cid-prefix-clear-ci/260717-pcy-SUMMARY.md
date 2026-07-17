---
phase: quick-260717-pcy
plan: 01
subsystem: infra
tags: [go, cobra, voipms, cli, telephony, kv]

requires:
  - phase: n/a
    provides: "existing kv voipms subcommand seam (voipmsClient.do, ListInboundDIDs, resolveVoipmsCreds)"
provides:
  - "kv voipms set-cid-prefix <did> <tag> — sets callerid_prefix on a DID via a full-snapshot-preserve setDIDInfo call, forcing cnam=0, with a readback verify"
  - "kv voipms clear-cid-prefix <did> — same full-snapshot-preserve dance, clears callerid_prefix to \"\""
affects: [ctf-per-did-sms-reply, per-did-gate-policy-spec]

tech-stack:
  added: []
  patterns:
    - "Bounded-retry wrapper (voipmsDoWithRetry, N=3) distinguishing transport failures (retryable) from a clean *voipmsStatusError (terminal, real API rejection)"
    - "Full-replace API preserve-allowlist: snapshot via getVoipmsDIDInfo, forward an explicit field list, force one field, readback-verify"

key-files:
  created: []
  modified:
    - kv/internal/app/cmd/voipms.go
    - kv/internal/app/cmd/voipms_test.go

key-decisions:
  - "voipmsDIDPreserveFields is an explicit named list (did/routing/pop/dialtime/billing_type/description/note/failover_*/voicemail/canada_routing), not a blind forward-everything loop over the snapshot map — keeps the setDIDInfo param set intentional and auditable"
  - "cnam is force-set to \"0\" unconditionally after the preserve-loop runs, overriding whatever the snapshot's cnam value was — the live-proven silent-failure guard from DID 3283"
  - "voipmsDoWithRetry routes both getVoipmsDIDInfo and setVoipmsDIDPrefix's setDIDInfo call through the same 3-attempt/500ms-1s backoff wrapper; a *voipmsStatusError short-circuits immediately without consuming a retry"
  - "Readback failure (routing drifted, or callerid_prefix mismatch) returns a descriptive error rather than trusting a setDIDInfo 200/success envelope — catches a cnam-clobbered prefix the API itself would report as success"

requirements-completed: [CID-TOOL-B]

coverage:
  - id: D1
    description: "kv voipms set-cid-prefix <did> <tag> sets callerid_prefix on the DID via full-snapshot-preserve setDIDInfo, forces cnam=0, and readback-verifies"
    requirement: CID-TOOL-B
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestVoipmsSetCidPrefix_AssemblesFullSnapshotForcesCnam0"
        status: pass
    human_judgment: false
  - id: D2
    description: "kv voipms clear-cid-prefix <did> empties callerid_prefix via the same full-snapshot-preserve dance"
    requirement: CID-TOOL-B
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestVoipmsClearCidPrefix_EmptiesPrefixPreservesRest"
        status: pass
    human_judgment: false
  - id: D3
    description: "VoIP.ms calls in this path are wrapped in a bounded retry (N=3) that retries transport failures but not a clean *voipmsStatusError"
    requirement: CID-TOOL-B
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestVoipmsDoWithRetry_RetriesTransportFailureThenSucceeds"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestVoipmsDoWithRetry_DoesNotRetryStatusError"
        status: pass
    human_judgment: false
  - id: D4
    description: "Both subcommands are registered on kv voipms; getVoipmsDIDInfo rejects a blank DID before any network call and errors clearly on not-found"
    requirement: CID-TOOL-B
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestVoipmsCidPrefixSubcommandsRegistered"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestGetVoipmsDIDInfo_RejectsBlankDid"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestGetVoipmsDIDInfo_NotFound"
        status: pass

duration: 15min
completed: 2026-07-17
status: complete
---

# Quick Task 260717-pcy: kv voipms set-cid-prefix/clear-cid-prefix Summary

**Two new `kv voipms` subcommands automate the hand-run setDIDInfo dance for enrolling a DID's caller-ID name prefix, baking in the four live-proven gotchas (full-snapshot preserve, forced cnam=0, readback verify, bounded retry) so the operator never re-learns them.**

## Performance

- **Duration:** ~15 min
- **Completed:** 2026-07-17T22:22:00Z
- **Tasks:** 2/2 completed
- **Files modified:** 2

## Accomplishments
- `getVoipmsDIDInfo(ctx, vc, did)` returns the full raw `getDIDsInfo` snapshot map for one DID (not the 4-field `InboundDIDRecord`), defensively handling both the array and single-bare-object `dids` shapes exactly like the existing `ListInboundDIDs`; rejects a blank DID before any network call and errors clearly on not-found.
- `setVoipmsDIDPrefix(ctx, vc, did, prefix)` performs the full-replace dance: snapshot → forward the `voipmsDIDPreserveFields` allowlist (did/routing/pop/dialtime/billing_type/description/note/failover_busy/failover_unreachable/failover_noanswer/voicemail/canada_routing) → force `cnam=0` → set `callerid_prefix=prefix` (may be `""`) → call `setDIDInfo` → readback via a second `getVoipmsDIDInfo` and verify routing was preserved and the prefix landed, returning a descriptive error if either check fails.
- `voipmsDoWithRetry(ctx, vc, method, params)` bounds retries to N=3 (500ms/1s backoff), retrying only transport-layer failures; a clean `*voipmsStatusError` (a real API rejection like `no_did`) returns immediately without consuming a retry. Both the snapshot/readback `getDIDsInfo` calls and the `setDIDInfo` call route through it.
- `kv voipms set-cid-prefix <did> <tag>` (`cobra.ExactArgs(2)`) and `kv voipms clear-cid-prefix <did>` (`cobra.ExactArgs(1)`) wired into `NewVoipmsCmd`, mirroring `route-did`'s structure (`resolveVoipmsCreds` → `newVoipmsClient` → human confirmation line to `c.OutOrStdout()`). Doc comment's "Sub-commands:" line updated.
- Added `voipmsMethodSetDIDInfo = "setDIDInfo"` as the only new constant in the centralized method-name block — the "rest.php base URL appears exactly once" invariant is untouched (still exactly 1 occurrence).
- Test coverage: a fake `getDIDsInfo`/`setDIDInfo` server (closure-tracked last-set `callerid_prefix`/`cnam`, reflected back on subsequent snapshot reads so the readback verification has something real to check) proves preserve, forced `cnam=0`, prefix set/cleared, and the readback path runs (>=2 `getDIDsInfo` calls observed). Separate tests cover the blank-did guard, not-found, retry-then-succeed on a hijacked-connection transport failure, and no-retry on a clean `*voipmsStatusError`. Subcommand registration covered by a dedicated test (per the plan's "or add a sibling test" option, rather than editing the existing `TestVoipmsCmdHelpListsSubcommands` want-map).

## Task Commits

Each task was committed atomically:

1. **Task 1: Implement getDIDInfo snapshot + setDIDInfo prefix helpers and the two subcommands** - `343ca29` (feat)
2. **Task 2: Unit-test the setDIDInfo param assembly against a fake client** - `63a1741` (test)

_Both tasks were plan-marked `tdd="true"`; implementation and its tests were written and verified together per the plan's task boundaries, then committed as two atomic commits matching the plan's declared `files_modified` split (voipms.go / voipms_test.go)._

## Files Created/Modified
- `kv/internal/app/cmd/voipms.go` - added `voipmsMethodSetDIDInfo` constant, `voipmsStringField`, `voipmsDoWithRetry`, `getVoipmsDIDInfo`, `voipmsDIDPreserveFields`, `setVoipmsDIDPrefix`, and the `set-cid-prefix`/`clear-cid-prefix` cobra subcommands
- `kv/internal/app/cmd/voipms_test.go` - added `newFakeCidPrefixServer` fake harness + 7 new tests covering preserve/force/verify, blank-did/not-found guards, bounded retry (retries transport, not a status error), and subcommand registration

## Decisions Made
- Preserve-allowlist is an explicit named field list rather than a blind "forward everything present in the snapshot" loop — matches the plan's `<action>` spec exactly and keeps the setDIDInfo param set auditable against a real getDIDsInfo response shape.
- Retry wrapper is a small standalone function (`voipmsDoWithRetry`), not a method on `voipmsClient`, since it needs to distinguish `*voipmsStatusError` from transport failures via `errors.As` and the existing `do()` method's signature already returns that typed error cleanly.

## Deviations from Plan

None - plan executed exactly as written. Both hard-requirement gotchas (full-snapshot preserve, forced cnam=0, readback verify, bounded retry-but-not-on-status-error) and the credential-leak invariant (no new log line stringifies params/URL/creds; verified via `grep -n "log\."` returning nothing and manual read of the new `Fprintf` lines) are implemented and tested as specified.

## Issues Encountered
None.

## User Setup Required
None - no external service configuration required. The new subcommands reuse the existing `VOIPMS_API_USERNAME`/`VOIPMS_API_PASSWORD` env-first/SSM-fallback credential resolution already in place.

## Next Phase Readiness
`kv voipms set-cid-prefix`/`clear-cid-prefix` are ready for operator use to enroll a DID's caller-ID prefix for Approach C's edge `dialed_did` resolution and per-DID SMS reply (see the CTF per-DID SMS reply memory item — the v2 plan's Step 2, SIP-URI routing to a static IP, is unblocked by having this tooling in place). No blockers. Out of scope for this quick task (per its own scope guard): Part A gate policy changes in `apps/voice` and the Part C `kv studio` surface remain untouched.

---
*Quick task: 260717-pcy*
*Completed: 2026-07-17*

## Self-Check: PASSED

- FOUND: kv/internal/app/cmd/voipms.go
- FOUND: kv/internal/app/cmd/voipms_test.go
- FOUND: .planning/quick/260717-pcy-part-b-kv-voipms-set-cid-prefix-clear-ci/260717-pcy-SUMMARY.md
- FOUND commit: 343ca29
- FOUND commit: 63a1741
