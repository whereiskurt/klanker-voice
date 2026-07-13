---
phase: 15-private-transcription-ledger-s3-batch-athena-admin-conversat
verified: 2026-07-13T13:40:00Z
status: human_needed
score: 5/5 must-haves verified (code-level); 1 end-to-end live capability pending deploy
behavior_unverified: 0
overrides_applied: 0
human_verification:
  - test: "After merging phase 15 to main (CI builds + deploys new voice/auth container images), hold a real voice.klankermaker.ai session, then open https://auth.klankermaker.ai/use1/admin/transcripts as an ADMIN_EMAILS operator (whereiskurt@gmail.com) and confirm that session appears as a threaded, turn-ordered, alternating-bubble conversation."
    expected: "The session's turns (both user STT text and assistant replies) appear grouped under one session_id, ordered by turn_seq, with a non-null code_hash (or caller_id/did for PSTN) and no raw access code visible anywhere."
    why_human: "The currently-running ECS task images (voice kmv-voice-app:288f4bcc, auth kmv-auth-app:244dcdd5) predate this phase and contain none of ledger.py, the tap wiring, the token claims, or the /admin routes — confirmed by the orchestrator this session (not assumed). The private S3 bucket, IAM, and SSM salt are live and correctly wired, but nothing writes to or reads from the ledger until the app images are rebuilt from this phase's code and redeployed. This is a deploy-gating fact, not something any test in this repo (or this verifier) can exercise before that deploy happens. /use1/admin currently 404s because the route is absent from the deployed image, not because the ADMIN_EMAILS gate is broken."
  - test: "Confirm the pre-connect recording notice and the /admin transcript view are visually acceptable (legible small print, readable bubble layout) once the images are live."
    expected: "The 'Sessions may be recorded for quality and demo purposes.' notice is legible on mobile + desktop; the threaded conversation view is readable and correctly distinguishes user vs. assistant turns."
    why_human: "Visual/UX quality cannot be assessed via grep or unit tests; both plans (15-05, 15-06) explicitly deferred this as a phase-gate, non-checkpoint item."
---

# Phase 15: Private Transcription Ledger — S3 Batch + Athena + Admin Conversation View Verification Report

**Phase Goal:** A private, append-only, Athena-queryable transcription ledger captures both sides of every voice session and the operator reads any session as a threaded conversation via a gated /admin report.
**Verified:** 2026-07-13T13:40:00Z
**Status:** human_needed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria ↔ LEDG-01..05)

| # | Truth (Success Criterion) | Req | Status | Evidence |
|---|------|-----|--------|----------|
| 1 | Each turn is recorded with role, text, email-or-caller-identity, UTC ts, session_id, monotonic turn_seq, and a salted code_hash — never the raw code | LEDG-01 | ✓ VERIFIED | `apps/voice/src/klanker_voice/ledger.py` `LEDGER_FIELDS` tuple exactly matches the spec; `code_hash()` is salted HMAC-SHA256, strip+lower normalized, returns `None` on missing salt/code; `parse_code_from_sub` extracts code from PSTN mint sub. `tests/test_ledger.py` (14 tests, all pass) exercises monotonic turn_seq, dedupe, raw-code-never-in-record, and anon-sub-never-verbatim. Auth-side claims (`auth.py` `EMAIL_CLAIM`/`CODE_CLAIM`, `SessionIdentity.email/.code`) verified byte-for-byte against `apps/auth/webapp/src/config/index.ts`'s `claimNames.email`/`.code` (both literally `https://klankermaker.ai/email` / `.../code`). |
| 2 | Voice service batches newline-JSON records to a private S3 bucket (SSE, no public access, date-partitioned) every ~2–5 min or on session end; an Athena table queries it | LEDG-02 | ✓ VERIFIED | Writer batches on a 120s timer / 50-record threshold / close (never per-utterance) — `test_ledger.py` asserts exactly one `put_object` per flush with Key `ledger/dt=YYYY-MM-DD/HHMMSSZ-<session_id>-NNNN.jsonl`. Final flush rides `CallSession.run()`'s `finally`; SIGTERM drain (`ledger.flush_all`, bounded 10s) wired into `server.py`'s FastAPI lifespan and `telephony/__main__.py`'s finally — both grep- and test-confirmed. Infra: `infra/terraform/modules/ledger/v1.0.0/main.tf` declares SSE(AES256) + all-four-true PAB + partition-projection Glue table (no MSCK/crawler) matching `LEDGER_FIELDS` column-for-column (`test_ledger_schema.py`, 6/6 pass). **Live infra state** (per orchestrator-verified AWS calls this session, not independently re-run here due to no AWS credentials in this sandbox): bucket `kmv-ledger-use1-adba57e4419be01f` exists, PAB all-true, SSE AES256, SSM salt SecureString resolves. |
| 3 | Operator reads any session as a threaded conversation (session-grouped, turn-ordered, alternating bubbles) via a gated /admin report | LEDG-03 | ✓ VERIFIED (code) / ⚠ see human_verification | `app/admin/layout.tsx` gates on `ADMIN_EMAILS` allowlist, `notFound()` (404, not 403) for non-admins/no-session — `admin-gate.test.ts` (4/4 pass). `lib/ledger.ts` `listSessions`/`readSession` read S3 directly (List/Get only, grep confirms no PutObject/DeleteObject), sort by `turn_seq` (never `ts`) — `ledger.test.ts` (6/6 pass). `transcripts/[sessionId]/page.tsx` renders alternating user/assistant bubbles, text as plain React children only (grep: zero real `dangerouslySetInnerHTML` usages, one doc-comment mention) — `transcripts.test.tsx` (8/8 pass, including an XSS-escaping proof). Full auth webapp suite: 85/85 pass. **However**, the deployed auth container image predates this phase's code (confirmed by the orchestrator this session) — `/use1/admin` currently 404s in production because the route doesn't exist in the running image, not because the code is broken. No real session has yet been read through this view end-to-end. |
| 4 | Client shows a visible "sessions may be recorded" notice (no-expectation-of-privacy posture) | LEDG-04 | ✓ VERIFIED | `apps/voice/client/src/screens/ReadyToStart.tsx` renders `.ready-recording-notice` with copy "Sessions may be recorded for quality and demo purposes." as a sibling of the existing CTA, before the mic-start tap. `ReadyToStart.test.tsx` asserts both the notice and the pre-existing CTA render (2/2 pass); full client suite 162/162 pass. |
| 5 | Quota data stays in DynamoDB; transcripts live only in S3 — no co-mingling | LEDG-05 | ✓ VERIFIED | `grep -c 'dynamodb\|kmv-voice-usage\|aioboto3' ledger.py` → 0 hits each (structural guarantee). Terraform IAM: voice task role grants `s3:PutObject` on `ledger/*` ONLY (no Get/List/Delete); auth task role grants `s3:ListBucket` (prefix-conditioned `ledger/*`) + `s3:GetObject` on `ledger/*` ONLY (no Put/Delete) — grep-confirmed in `services/voice/service.hcl` and `services/auth/service.hcl`. No `aws_s3_bucket_policy`/delete action anywhere in the module. |

**Score:** 5/5 Success Criteria verified at the code level (artifacts exist, are substantive, are wired, and are covered by passing tests). 1 of the 5 (SC3/LEDG-03, and by extension the phase's overall "captures... every voice session" framing) cannot yet be demonstrated as a live, end-to-end fact because the running ECS images predate this phase's code — see Human Verification below.

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `apps/auth/webapp/src/config/index.ts` | `claimNames.email`/`.code` registry entries | ✓ VERIFIED | `https://klankermaker.ai/email` / `.../code`, byte-identical to voice `auth.py` constants |
| `apps/auth/webapp/src/config/oidc.ts` | `extraTokenClaims` emits the two claims from the already-fetched profile | ✓ VERIFIED | `[config.oidc.claimNames.email]: profile?.email ?? null`, `.code` likewise; single `getAuthProfile(` call preserved |
| `apps/auth/webapp/src/entities/auth-profile.ts` | `activeCode` attribute + `setActiveTier(..., code?)` | ✓ VERIFIED | present, additive 4th param |
| `apps/auth/webapp/src/config/login-intent-bridge.ts` | threads `intent.code` into `setActiveTier` | ✓ VERIFIED | `setActiveTier(userId, intent.tierId, intent.group, intent.code)` |
| `apps/voice/src/klanker_voice/ledger.py` | `LedgerWriter`, `code_hash`, `parse_code_from_sub`, `LEDGER_FIELDS`, `flush_all` | ✓ VERIFIED | full module read; matches plan spec exactly; 14/14 tests pass |
| `apps/voice/src/klanker_voice/auth.py` | `EMAIL_CLAIM`/`CODE_CLAIM`, `SessionIdentity.email/.code` | ✓ VERIFIED | present, additive defaults, smoke path unchanged |
| `apps/voice/src/klanker_voice/call_runtime.py` | ledger tap (`_ledger_user`/`_ledger_assistant`), writer field, `run()` finally close | ✓ VERIFIED | grep + 8 passing tests in `test_call_runtime.py` |
| `apps/voice/server.py` | webrtc identity threading + FastAPI shutdown lifespan drain | ✓ VERIFIED | `_lifespan` calls `ledger.flush_all(timeout=LEDGER_DRAIN_TIMEOUT_SECONDS)`; `CallIdentity(email=..., code=...)` |
| `apps/voice/src/klanker_voice/telephony/controller.py` | mint-sub return, PSTN identity, unlock-time writer enable | ✓ VERIFIED | `_mint_tier_from_caller_id` returns `(tier_id, sub)`; `_gate_unlock` flips `writer.enabled = True` at the real unlock boundary |
| `apps/voice/src/klanker_voice/telephony/__main__.py` | drains ledger on exit | ✓ VERIFIED | `await ledger.flush_all(timeout=LEDGER_DRAIN_TIMEOUT_SECONDS)` in `finally` |
| `infra/terraform/modules/ledger/v1.0.0/main.tf` | private SSE bucket, PAB, lifecycle, Athena workgroup, projection Glue table | ✓ VERIFIED | read in full; all fields match plan; `terraform validate` claimed green in SUMMARY (not re-run here — no backend init in this sandbox) |
| `apps/voice/tests/test_ledger_schema.py` | DDL-vs-`LEDGER_FIELDS` drift guard | ✓ VERIFIED | 6/6 pass |
| `apps/auth/webapp/src/lib/ledger.ts` | S3 `listSessions`/`readSession`, `turn_seq` sort | ✓ VERIFIED | read in full; matches plan; 6/6 tests pass |
| `apps/auth/webapp/src/app/admin/layout.tsx` | ADMIN_EMAILS gate, `notFound()` | ✓ VERIFIED | read in full; 4/4 tests pass |
| `apps/auth/webapp/src/app/admin/transcripts/page.tsx` | session list by day | ✓ VERIFIED | read in full |
| `apps/auth/webapp/src/app/admin/transcripts/[sessionId]/page.tsx` | threaded chat detail, escaped text | ✓ VERIFIED | read in full; no `dangerouslySetInnerHTML` |
| `apps/voice/client/src/screens/ReadyToStart.tsx` | "sessions may be recorded" notice | ✓ VERIFIED | `.ready-recording-notice` present; 2/2 tests pass |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| auth `config/index.ts` claim names | voice `auth.py` `EMAIL_CLAIM`/`CODE_CLAIM` | byte-identical namespaced strings | ✓ WIRED | Both sides read `https://klankermaker.ai/email` / `.../code`, confirmed by direct grep of both files |
| `ledger.LEDGER_FIELDS` | Athena Glue DDL columns | schema-drift test | ✓ WIRED | `test_ledger_schema.py` 6/6 pass; DDL column order matches tuple exactly |
| `call_runtime.py` aggregator event handlers | `LedgerWriter.append` | `built.user_aggregator`/`built.assistant_aggregator` `.event_handler(...)` | ✓ WIRED | tests fire real pipecat aggregator objects (not fakes) and assert appended records |
| `server.py` webrtc identity | `CallIdentity(email=, code=)` | `SessionIdentity.email/.code` threaded through `_negotiate_webrtc` | ✓ WIRED | grep + `test_server.py` test |
| `telephony/controller.py` mint sub | `CallIdentity.code` → `code_hash` | `_finish_stasis_start_gated` `replace()` | ✓ WIRED | `test_telephony_lifecycle.py`: unlock produces a non-null `code_hash` from the mint sub |
| `lib/ledger.ts` | `ledger.LEDGER_FIELDS` (Python) | `LedgerRecord` TS type mirrors the tuple field-for-field | ✓ WIRED | manual field comparison confirms parity (role/text/email/caller_id/did/ts/session_id/turn_seq/code_hash/tier_id/channel/interrupted) |
| `services/voice/service.hcl` IAM | S3 bucket | `LedgerPutOnly` sid, `s3:PutObject` on `ledger/*` only | ✓ WIRED | grep-confirmed, no Get/List/Delete |
| `services/auth/service.hcl` IAM | S3 bucket | `LedgerListBucket` + `LedgerGetObject`, no write/delete | ✓ WIRED | grep-confirmed |

### Behavioral Spot-Checks / Test Runs

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Voice ledger + auth unit tests | `uv run pytest tests/test_ledger.py tests/test_auth.py -q` | 27 passed | ✓ PASS |
| Voice tap-wiring tests | `uv run pytest tests/test_call_runtime.py tests/test_server.py tests/test_telephony_lifecycle.py -q` | 40 passed | ✓ PASS |
| Voice schema-drift guard | `uv run pytest tests/test_ledger_schema.py -q` | 6 passed | ✓ PASS |
| Full voice suite | `uv run pytest -q` | 465 passed, 23 failed, 23 errors | ⚠ PASS (see note) |
| Auth webapp — phase-15 tests only | `npm test -- token-claims / auth-profile-active-code / login-intent-bridge` | 14 passed | ✓ PASS |
| Full auth webapp suite | `npm test` (node 23) | 85 passed (18 files) | ✓ PASS |
| ReadyToStart notice test | `npm test -- ReadyToStart` | 2 passed | ✓ PASS |
| Full voice client suite | `npm test` (node 23) | 162 passed (32 files) | ✓ PASS |

**Note on the 23 failed / 23 errored voice tests:** all trace to `botocore.errorfactory.ResourceNotFoundException: Cannot do operations on a non-existent table` against a local `dynamodb-local` instance missing the `kmv-voice-usage`/`kmv-auth-electro` tables in this sandbox. Confirmed pre-existing and unrelated to phase 15: the failing files are `test_quota.py`, `test_session.py`, `test_slot_leak.py`, `test_teardown.py`, `test_winddown.py` — none of which phase 15 touches. Every file phase 15 *does* touch (`test_ledger.py`, `test_auth.py`, `test_call_runtime.py`, `test_server.py`, `test_telephony_lifecycle.py`, `test_ledger_schema.py`) is fully green.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|--------------|--------|----------|
| LEDG-01 | 15-01, 15-02, 15-03 | Access token carries namespaced email+code claims; voice service builds a complete ledger record from the validated token alone | ✓ SATISFIED | claim registry, `SessionIdentity`, `LedgerWriter` construction, PSTN mint-sub threading all present and tested |
| LEDG-02 | 15-02, 15-03, 15-04 | Private, append-only, Athena-queryable S3 ledger store: SSE + PAB + partition-projection Glue table + least-privilege IAM + SOPS salt | ✓ SATISFIED (code+tests); live infra confirmed by orchestrator | module + unit + service wiring + schema test all present; live apply attested this session |
| LEDG-03 | 15-05 | Operator reads any session as a threaded conversation via a gated /admin report | ✓ SATISFIED (code+tests) / pending live demonstration | gate, S3 reader, list+detail pages, all tested; no live session read yet (image not deployed) |
| LEDG-04 | 15-06 | Pre-connect "sessions may be recorded" notice | ✓ SATISFIED | notice present, tested, no regression |
| LEDG-05 | 15-02, 15-04 | Ledger writer touches only S3, never DynamoDB | ✓ SATISFIED | structurally grep-guaranteed + least-privilege IAM |

No orphaned requirements found — REQUIREMENTS.md's Phase 15 mapping (LEDG-01..05) exactly matches the union of `requirements:` fields declared across the six plans.

### Anti-Patterns Found

None. Scanned all 16 phase-15-touched source files for `TBD`/`FIXME`/`XXX`/`TODO`/`HACK`/`PLACEHOLDER`/"not yet implemented"/"coming soon" — zero hits (the only near-matches were legitimate code comments about the "bypass placeholder" / "no-access placeholder" domain concepts, not debt markers).

### Human Verification Required

See frontmatter `human_verification`. Summary:

1. **Live end-to-end capture + /admin read.** The running ECS task images (`kmv-voice-app:288f4bcc`, `kmv-auth-app:244dcdd5`) predate this phase's code. The ledger S3 bucket, IAM, and SSM salt are live and correctly wired (orchestrator-confirmed via AWS calls this session), but nothing writes to or reads from the ledger yet — the writer code and the `/admin` routes only activate once the voice and auth container images are rebuilt from this phase's code and redeployed (normally gated on merge to `main` + CI image build). This is a deployment-sequencing fact, not a code defect, and cannot be exercised by any test today.
2. **Visual/UX quality** of the recording notice and the threaded transcript view, once live — both plans explicitly deferred this as a non-blocking phase-gate item.

### Gaps Summary

No code-level gaps. Every artifact declared across all 6 plans exists, is substantive, is wired, and is covered by passing tests (unit + schema-drift + grep-gated structural checks). All 5 ROADMAP success criteria (LEDG-01..05) are satisfied at the code level. The only outstanding item is operational: the phase's code has not yet been deployed to the running voice/auth ECS tasks, so the phase's goal ("captures both sides of every voice session... via a gated /admin report") cannot yet be observed as a live fact in production. This routes to human verification per the deployment context supplied for this run, not to a gap — nothing here indicates the code itself would fail once deployed.

---

_Verified: 2026-07-13T13:40:00Z_
_Verifier: Claude (gsd-verifier)_
