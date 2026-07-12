---
phase: 12-voip-ms-telephony-inbound-did
plan: 05
subsystem: infra
tags: [dynamodb, gsi, ssm, kv-cli, seed-data, e164, tiers]

# Dependency graph
requires:
  - phase: 12-voip-ms-telephony-inbound-did
    provides: "12-02's byPhone GSI entity/resolver (the read side this seed data feeds) and 12-03's kv code phone command (the write path used for seeding)"
  - phase: 03-auth-service-access-codes
    provides: "kmv-auth-electro live table + kv tier define / kv code create commands"
provides:
  - "gsi3pk-gsi3sk-index (byPhone GSI) verified ACTIVE on the LIVE kmv-auth-electro table (closes the ElectroDB false-positive gap)"
  - "kph-tier Tier row live (86400s/1000000s/5 concurrent — effectively unlimited, D-05)"
  - "pstn-baseline-tier Tier row live (600s/1800s/1 concurrent — the §11 constrained caller-ID ceiling)"
  - "defcon34 access code live, mapped to kph-tier, with the admin phone -> defcon34 byPhone mapping round-trip-verified through the live GSI"
  - "/kmv/operators/use1/admin_phone SSM SecureString (operator-only, bot-unreadable) + documented access-isolation constraints for 12-07"
  - "docs/operators/phase12-seed-data.md — exact-command operator record with placeholder-only phone handling"
affects: [12-06, 12-07, 12-08]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Operator-only SSM prefix /kmv/operators/* — deliberately disjoint from the /kmv/secrets/use1/* container-secret prefix; never valueFrom-wired, never task-role-granted"
    - "Live-GSI verification before any dependent lookup (describe-table IndexStatus ACTIVE), closing the ElectroDB types-pass-without-live-index false positive"

key-files:
  created:
    - docs/operators/phase12-seed-data.md
  modified: []

key-decisions:
  - "Baseline caller tier named pstn-baseline-tier (group pstn) with the research doc's representative §11 values: 600s session / 1800s daily period / 1 concurrent"
  - "kph-tier seeded with the research doc's effectively-unlimited values: 86400s session / 1000000s period / 5 concurrent (group kph)"
  - "defcon34 created fresh (pre-checked kv code list — it did not exist, so no clobber-guard branch was needed) mapped to kph-tier per §23"
  - "Admin phone additionally stored at SSM /kmv/operators/use1/admin_phone (SecureString) per user directive — path chosen disjoint from every container-consumed prefix; real value exists ONLY in the live table item + this parameter, never in git"

patterns-established:
  - "Secrecy contract for operator PII: docs/commits use <ADMIN_PHONE_E164> placeholder; git grep for the real digits gated every commit"

requirements-completed: [D-05, SC-1, SC-2]

coverage:
  - id: D1
    description: "gsi3pk-gsi3sk-index (byPhone GSI) is ACTIVE on the live kmv-auth-electro table, with no table replacement"
    requirement: SC-1
    verification:
      - kind: manual_procedural
        ref: "aws dynamodb describe-table --table-name kmv-auth-electro --query \"Table.GlobalSecondaryIndexes[?IndexName=='gsi3pk-gsi3sk-index'].IndexStatus\" (ACTIVE; TableId/CreationDateTime unchanged from Phase-3 provisioning)"
        status: pass
    human_judgment: false
  - id: D2
    description: "kph-tier (effectively unlimited) + pstn-baseline-tier (1 concurrent/600s/1800s daily) Tier rows exist in the live table with the documented limits"
    requirement: D-05
    verification:
      - kind: manual_procedural
        ref: "aws dynamodb get-item on tier#kph-tier and tier#pstn-baseline-tier (values match: 86400/1000000/5 and 600/1800/1)"
        status: pass
    human_judgment: false
  - id: D3
    description: "defcon34 code exists mapped to kph-tier, and the admin phone byPhone mapping round-trips through the LIVE gsi3pk-gsi3sk-index to defcon34 -> kph-tier with phoneEnabled=true"
    requirement: SC-2
    verification:
      - kind: manual_procedural
        ref: "aws dynamodb query --index-name gsi3pk-gsi3sk-index for the mapped number returned {code: defcon34, tierId: kph-tier, phoneEnabled: true} against the live table"
        status: pass
    human_judgment: false
  - id: D4
    description: "Admin phone stored operator-only in SSM (/kmv/operators/use1/admin_phone), provably unreadable by any running bot task role, with 12-07 constraints documented; real number never committed to git"
    requirement: D-05
    verification:
      - kind: manual_procedural
        ref: "aws ssm describe-parameters (SecureString v1 exists); aws iam get-role-policy on auth/voice task roles (zero ssm:*); aws ecs describe-task-definition (no active task uses the shared ssm:* role); git grep for the real digits across the tree (zero hits)"
        status: pass
    human_judgment: false

# Metrics
duration: 25min
completed: 2026-07-12
status: complete
---

# Phase 12 Plan 05: Live byPhone GSI + D-05 Seed Data Summary

**Verified the byPhone GSI ACTIVE on the live kmv-auth-electro table, seeded kph-tier (unlimited) + pstn-baseline-tier (constrained §11 caller ceiling) + defcon34→kph-tier with the admin phone mapping round-trip-proven through the live GSI, and locked the admin number into an operator-only SSM parameter no bot task role can read.**

## Performance

- **Duration:** ~25 min (including one checkpoint pause awaiting the real phone number)
- **Started:** 2026-07-12T20:04Z
- **Completed:** 2026-07-12T20:20Z
- **Tasks:** 2 (+1 user-directed in-scope addition: the SSM admin_phone parameter)
- **Files modified:** 1 (created)

## Accomplishments

- **Task 1 (SC-1):** `gsi3pk-gsi3sk-index` confirmed **ACTIVE** on the live `kmv-auth-electro` table (account 052251888500, us-east-1) via `describe-table` — no terraform apply needed (the shared dynamodb module already declared it and Phase 3's provisioning already rolled it out; all three GSIs gsi1/gsi2/gsi3 live). Table identity (`TableId` b5a933b5…, `CreationDateTime` 2026-07-05) unchanged — no replacement occurred (T-12-05-01 mitigated). This closes the ElectroDB false-positive gap: builds/types pass without the live index, so only this live check proves the §23 byPhone lookup can actually run.
- **Task 2 (D-05, SC-2):** seeded via the shipped `kv` commands (12-03), every exact invocation recorded in `docs/operators/phase12-seed-data.md`:
  - `kv tier define kph-tier --group kph --session-max 86400 --period-max 1000000 --max-concurrent 5` (effectively unlimited)
  - `kv tier define pstn-baseline-tier --group pstn --session-max 600 --period-max 1800 --max-concurrent 1` (the §11 constrained caller-ID ceiling — T-12-05-03 mitigated: kph-tier is only reachable via the §24 gate composed in 12-06)
  - `kv code create defcon34 --tier kph-tier --group telephony` (pre-checked: did not exist, no clobber)
  - `kv code phone defcon34 --add <ADMIN_PHONE_E164>` (real number supplied by the operator at the checkpoint, never committed)
  - **Round-trip proof:** a direct `aws dynamodb query` against the live `gsi3pk-gsi3sk-index` for the mapped number returned `{code: defcon34, tierId: kph-tier, phoneEnabled: true}` — the exact lookup shape 12-02's `resolvePhoneToCode()` / `GET /tel/<e164>` performs.
- **User-directed addition:** the admin phone is stored in SSM SecureString `/kmv/operators/use1/admin_phone` — an operator-only path deliberately disjoint from `/kmv/secrets/use1/*` (the prefix every container `valueFrom` uses). Access isolation proven against both IaC and the live account: the dedicated auth/voice task roles (the only roles any ACTIVE task definition uses) carry **zero** `ssm:*` actions; the execution-role `parameter/*` wildcard is valueFrom-injection-only; the shared cluster task role's wide-open `ssm:* on *` is unused by any active task definition but documented as a hazard. Hard constraints for 12-07 (dedicated task role required; never grant `/kmv/operators/*`) recorded in the operator doc.

## Task Commits

Each task was committed atomically:

1. **Task 1 + Task 2 seeding (GSI verify, tiers, defcon34):** `713c7c8` (feat)
2. **Task 2 completion (phone mapping + round-trip) + SSM admin_phone:** `9e33cb9` (feat)

_Note: this plan's "files" are one operator doc — most of its output is live AWS state (DynamoDB rows, an SSM parameter), captured as exact commands + redacted results in that doc._

## Files Created/Modified

- `docs/operators/phase12-seed-data.md` — the operator record: GSI verification evidence, every seed command, the byPhone round-trip result (redacted), the SSM parameter + full access-isolation analysis and 12-07 constraints

## Decisions Made

- Baseline tier named `pstn-baseline-tier` with the research doc's representative values (600s/1800s/1) — §11's "1 concurrent, ~10 min, small daily cap".
- `kph-tier` uses the research doc's representative unlimited values (86400s/1000000s/5).
- SSM path `/kmv/operators/use1/admin_phone` chosen over anything under `/kmv/secrets/use1/` specifically because the latter is the container-secret prefix — path disjointness makes accidental valueFrom-wiring structurally less likely, though the real guarantees are the zero-ssm task roles + the never-valueFrom constraint (the execution-role wildcard covers all of `parameter/*`, so no path escapes it).

## Deviations from Plan

**1. [User-directed addition] SSM operator-only admin_phone parameter**
- **Found during:** Task 2 (checkpoint response)
- **Issue:** Not in the original plan — the user directed (as an authorized in-scope addition) that the admin phone be stored in one SSM SecureString the bot can never read, with the isolation documented.
- **Fix:** Created `/kmv/operators/use1/admin_phone` (SecureString v1, tagged operator-only), verified live that no active task role can read any SSM parameter, documented the execution-role/shared-role caveats and the 12-07 constraints in the operator doc.
- **Files modified:** docs/operators/phase12-seed-data.md
- **Verification:** `aws ssm describe-parameters` (exists), `aws iam get-role-policy` on both dedicated task roles (zero ssm), `aws ecs describe-task-definition` (no active task uses the shared `ssm:*` role), repo grep for `kmv/operators` in valueFrom/IaC (zero hits)
- **Committed in:** `9e33cb9`

---

**Total deviations:** 1 (user-directed in-scope addition; zero Rule 1-4 auto-fixes)
**Impact on plan:** Additive hardening only — no change to the planned GSI/seed outcomes.

## Security Notes (secrecy contract)

- The real admin phone number exists in exactly two places: the live `kmv-auth-electro` item (`defcon34`'s `phone` attribute / byPhone GSI keys) and the SSM parameter. Every doc/commit uses the `<ADMIN_PHONE_E164>` placeholder.
- `git grep` for the real digits was run and confirmed **zero hits** before both commits (staged-diff grep also zero). T-12-05-02 mitigated.

## Issues Encountered

- The ambient default AWS credentials were stale (`InvalidClientTokenId`); the live `klanker-application` SSO profile was used instead (`AWS_PROFILE=klanker-application AWS_REGION=us-east-1`) — no auth gate needed, the SSO session was already valid.

## User Setup Required

None further — all live actions were completed this session with operator approval at the checkpoint.

## Next Phase Readiness

- The live table is fully ready for the §23 byPhone lookup: GSI ACTIVE, tiers seeded, mapping round-trip-proven. 12-06's controller mint path and 12-07's deployed edge can rely on real data.
- 12-07 MUST honor the recorded constraints: dedicated least-privilege task role (never the shared cluster role), SSM grants only under `/kmv/secrets/use1/*`, never `/kmv/operators/*`, and `/kmv/operators/use1/admin_phone` never in any `valueFrom`.
- Open item for a future tier decision (not blocking): the caller-ID mint currently resolves the admin phone to `defcon34` → `kph-tier` directly; D-05's baseline-vs-gate composition is enforced in 12-06's controller (mint grants baseline; gate upgrades), with `pstn-baseline-tier` now live as the constrained ceiling row.

## Self-Check: PASSED

- FOUND: docs/operators/phase12-seed-data.md
- FOUND commit: 713c7c8
- FOUND commit: 9e33cb9
- All Task 1 acceptance criteria re-run: PASS (GSI ACTIVE, no table replacement, status recorded in doc)
- All Task 2 acceptance criteria re-run: PASS (both tiers live with documented limits, defcon34→kph-tier, byPhone round-trip resolves, doc placeholder-only)
- Plan-level `<verification>` re-run: PASS (GSI ACTIVE; tier get-item returns kph-tier; seed-data doc committed with placeholders)
- Secrecy grep (real digits) across tree + staged diffs: zero hits

---
*Phase: 12-voip-ms-telephony-inbound-did*
*Completed: 2026-07-12*
