---
phase: 15
slug: private-transcription-ledger-s3-batch-athena-admin-conversat
status: ready
nyquist_compliant: true
wave_0_complete: false
created: 2026-07-13
---

# Phase 15 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.
> Three code surfaces (voice pytest, auth webapp vitest, voice client vitest) plus
> terraform validate; one manual operator apply and one live end-to-end phase gate.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework (voice service)** | pytest ≥9.1.1 + pytest-asyncio ≥1.4.0 (`apps/voice/pyproject.toml:15-16`, `asyncio_mode = "auto"`) |
| **Framework (auth webapp)** | vitest — `npm test` → `vitest run` (`apps/auth/webapp/package.json` scripts.test) |
| **Framework (voice client)** | vitest — `npm test` → `vitest run` (`apps/voice/client/package.json`; needs node ≥22.12 / `nvm use 23`) |
| **Framework (infra)** | `terraform validate` (module) + `terragrunt hclfmt --terragrunt-check` / `terragrunt plan` (unit) |
| **Config file** | `apps/voice/pyproject.toml [tool.pytest.ini_options]` (testpaths=["tests"]); webapp + client vitest configs present |
| **Quick run command** | `cd apps/voice && uv run pytest tests/test_ledger.py -x` (the Wave-0 scaffold) |
| **Full suite command** | `cd apps/voice && uv run pytest` (baseline 421 passed / 53 skipped) **and** `cd apps/auth/webapp && npm test` (baseline 59/59) **and** `cd apps/voice/client && npm test` |
| **Estimated runtime** | voice full ~60–90s · webapp ~10–15s · client ~10s · per-task file `-x` runs < 30s |

---

## Sampling Rate

- **After every task commit:** Run the touched module's test file with `-x` (the row's Automated Command).
- **After every plan wave:** Run all three full suites — `cd apps/voice && uv run pytest`, `cd apps/auth/webapp && npm test`, `cd apps/voice/client && npm test`.
- **Before `/gsd-verify-work`:** All three suites green + `terraform validate` / `terragrunt hclfmt` clean.
- **Phase gate (beyond suites):** all three suites green **plus** the live end-to-end below (one browser session + one PSTN call → rows in S3 → admin thread renders).
- **Max feedback latency:** 90 seconds (worst case = full voice suite).

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| 15-01-01 | 01 | 1 | LEDG-01 | T-15-01-01/02/03 | Email+code claims resolved from AuthProfile, null-safe; no raw code logged | unit (vitest) | `cd apps/auth/webapp && npm test -- src/config/__tests__/token-claims.test.ts` | ❌ W0 | ⬜ pending |
| 15-01-02 | 01 | 1 | LEDG-01 | T-15-01-03 | `activeCode` stamped latest-wins via existing bridge; raw code never `console.*`-logged | unit (vitest) | `cd apps/auth/webapp && npm test` | ✅ | ⬜ pending |
| 15-02-01 | 02 | 1 | LEDG-01, LEDG-05 | T-15-02-01/02/03 | RED scaffold: asserts salted code_hash, no raw code / no `anon:` in record dict, no text in logs | unit (pytest-asyncio) | `cd apps/voice && uv run pytest tests/test_ledger.py -x` | ❌ W0 | ⬜ pending |
| 15-02-02 | 02 | 1 | LEDG-01, LEDG-05 | T-15-02-01/02/03 | Writer is S3-only (no DynamoDB/`kmv-voice-usage`); HMAC-SHA256 code_hash; flush error re-buffers, never leaks text | unit (pytest-asyncio) | `cd apps/voice && uv run pytest tests/test_ledger.py -x` | ❌ W0 | ⬜ pending |
| 15-02-03 | 02 | 1 | LEDG-01 | T-15-02-01 | Validated token surfaces email+code; smoke/service path unchanged (email/code None) | unit (pytest) | `cd apps/voice && uv run pytest tests/test_auth.py -x` | ✅ | ⬜ pending |
| 15-03-01 | 03 | 2 | LEDG-01, LEDG-02 | T-15-03-01/02 | Bypass/smoke writer disabled (appends nothing); final flush rides run() finally | unit (pytest-asyncio) | `cd apps/voice && uv run pytest tests/test_call_runtime.py -x` | ✅ | ⬜ pending |
| 15-03-02 | 03 | 2 | LEDG-01, LEDG-02 | T-15-03-04 | SIGTERM drain bounded by `flush_all(timeout=10)`; a hung writer never blocks past the bound | unit (pytest) | `cd apps/voice && uv run pytest tests/ -k "server or ledger or drain" -x` | ✅ | ⬜ pending |
| 15-03-03 | 03 | 2 | LEDG-01, LEDG-02 | T-15-03-01/03 | No turn captured while §24 gate locked; capture starts at unlock; mint token never logged | unit (pytest) | `cd apps/voice && uv run pytest tests/test_telephony_lifecycle.py -x` | ✅ | ⬜ pending |
| 15-04-01 | 04 | 1 | LEDG-02, LEDG-05 | T-15-04-01/02/03 | Private SSE bucket, PAB all-true, no public policy, no delete/IAM in module | infra (terraform validate) | `cd infra/terraform/modules/ledger/v1.0.0 && terraform init -backend=false >/dev/null 2>&1 && terraform validate` | — (tf) | ⬜ pending |
| 15-04-02 | 04 | 1 | LEDG-02, LEDG-05 | T-15-04-04/05 | Voice=PutObject-only, auth=List/Get-only on ledger/* (no delete); salt only in SOPS; DDL≡LEDGER_FIELDS | infra fmt + unit (pytest) | `cd infra/terraform/live/site/region/us-east-1/ledger && terragrunt hclfmt --terragrunt-check; cd apps/voice && uv run pytest tests/test_ledger_schema.py -x` | ❌ W0 | ⬜ pending |
| 15-04-03 | 04 | 1 | LEDG-02, LEDG-05 | T-15-04-01/02/SC | Operator-SSO apply; bucket private (PAB all-true, SSE AES256); salt SSM SecureString live | manual (checkpoint) | see Manual-Only Verifications | — manual | ⬜ pending |
| 15-05-01 | 05 | 2 | LEDG-03 | T-15-05-03/04 | Reader uses only ListObjectsV2+GetObject (no Put/Delete); `fromNodeProviderChain` scoped creds; sort by turn_seq not ts | unit (vitest) | `cd apps/auth/webapp && npm test -- src/lib/__tests__/ledger.test.ts` | ❌ W0 | ⬜ pending |
| 15-05-02 | 05 | 2 | LEDG-03 | T-15-05-01 | Non-admin + no-session → `notFound()` (404, not 403 — no route disclosure); ADMIN_EMAILS case-insensitive | unit (vitest) | `cd apps/auth/webapp && npm test -- src/app/admin/__tests__/admin-gate.test.ts` | ❌ W0 | ⬜ pending |
| 15-05-03 | 05 | 2 | LEDG-03 | T-15-05-02 | Transcript text rendered as escaped React children (no `dangerouslySetInnerHTML`); turns in turn_seq order | unit (vitest) | `cd apps/auth/webapp && npm test` | ✅ | ⬜ pending |
| 15-06-01 | 06 | 1 | LEDG-04 | T-15-06-01 | Visible "recorded" notice renders on pre-connect screen before the mic gesture; CTA unregressed | unit (vitest) | `cd apps/voice/client && npm test -- ReadyToStart` | ✅ | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*
*File Exists: ✅ extends a pre-existing test file · ❌ W0 test file is newly created within this phase (see Wave 0 Requirements) · — (tf) infra validate, no test file · — manual operator checkpoint*

---

## Wave 0 Requirements

This phase uses **task-level TDD (RED-first)** rather than a separate Wave-0 plan: each new test file is
authored as the first (failing) task inside its own plan, before the task that implements against it.
The RED scaffold must exist and fail before the GREEN implementation task runs.

- [ ] `apps/voice/tests/test_ledger.py` — writer / flush key+body / put-failure-keeps-buffer / 50-record + idempotent close / double-fire dedupe / code_hash stability + null-salt / disabled-writer no-op / `parse_code_from_sub`. **Created by 15-02-01 (RED); drives 15-02-02 (GREEN).**
- [ ] `apps/auth/webapp/src/config/__tests__/token-claims.test.ts` — email/code claim names + null-safe `extraTokenClaims`. **Created by 15-01-01.**
- [ ] `apps/auth/webapp/src/lib/__tests__/ledger.test.ts` — list-from-keys / read+parse+turn_seq-sort / malformed-line-skip / scoped-creds. **Created by 15-05-01.**
- [ ] `apps/auth/webapp/src/app/admin/__tests__/admin-gate.test.ts` — ADMIN_EMAILS gate → `notFound()` for non-admin/no-session. **Created by 15-05-02.**
- [ ] `apps/voice/tests/test_ledger_schema.py` — Glue DDL columns ≡ `ledger.LEDGER_FIELDS` drift guard (Pitfall 6). **Created by 15-04-02.**

**Fixtures:** no new shared `conftest.py` — `test_ledger.py` copies the recording `fake_aws`/`_FakeAwsClient`
monkeypatch shape from `apps/voice/tests/test_session.py:60-99`; webapp/client tests use the existing
env-first / mock-then-import pattern (`apps/auth/webapp/src/app/tel/__tests__/tel-route.test.ts`).

**Existing infrastructure covers the rest:** `tests/test_auth.py`, `tests/test_call_runtime.py`,
`tests/test_telephony_lifecycle.py`, and `apps/voice/client/src/screens/ReadyToStart.test.tsx` already
exist and are extended in place (marked ✅ in the map).

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Operator-SSO apply of the ledger infra (task 15-04-03) | LEDG-02, LEDG-05 | Org SCP `DenyInfraAndStorage` blocks bucket + IAM creation from CI (proven Phase 12-07); apply needs the operator-SSO profile | 1. `cd infra/terraform/live/site/region/us-east-1/ledger && terragrunt plan` — confirm it creates one S3 bucket (SSE AES256, PAB all-true), the Athena workgroup, the Glue database + projection table, and the SSM salt parameter; confirm voice role adds ONLY `s3:PutObject` on `ledger/*` and auth role adds ONLY List/Get on `ledger/*`; 0 destroys. 2. With operator SSO creds, apply the ledger unit then the voice + auth service units. 3. `aws s3api get-public-access-block --bucket <ledger-bucket>` → all four flags true; `aws s3api get-bucket-encryption --bucket <ledger-bucket>` → AES256. 4. `aws ssm get-parameter --name /kmv/secrets/use1/ledger/code_hash_salt --with-decryption` resolves. |
| Live end-to-end phase gate | LEDG-01, LEDG-02, LEDG-03, LEDG-04 | Requires real speech through the full pipeline + a real PSTN call + a live private bucket + the gated /admin UI — no unit test can span all four | 1. Open voice.klankermaker.ai, confirm the "sessions may be recorded" notice on the pre-connect screen, then hold one short browser conversation. 2. Place one PSTN call to a live DID and speak a couple of turns (if a DID is available). 3. Confirm newline-JSON objects land under `s3://<ledger-bucket>/ledger/dt=<today>/…` (operator: `aws s3 ls`), both roles present, `turn_seq` monotonic, `email` set for browser / `caller_id`+`did` set + `email` null for PSTN, `code_hash` present (never a raw code). 4. As an `ADMIN_EMAILS` operator open `/admin/transcripts`, pick today, drill into the session, and confirm it renders as a threaded, turn_seq-ordered, alternating user/assistant conversation with escaped text. 5. Confirm a non-admin session gets a 404 at `/admin`. |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or a documented manual checkpoint — only 15-04-03 is manual (SCP-gated apply), captured in Manual-Only Verifications
- [x] Sampling continuity: no 3 consecutive tasks without automated verify — 15-04-03 is the sole manual task; every other task across all six plans has an automated command
- [x] Wave 0 covers all MISSING references — each ❌ W0 test file is authored RED-first inside its own plan before the implementing task; no dependent task runs against a non-existent file
- [x] No watch-mode flags — webapp/client `npm test` = `vitest run`; pytest uses `-x`; terraform/terragrunt use `validate`/`hclfmt --terragrunt-check`/`plan`
- [x] Feedback latency < 90s — worst case is the full voice suite (~60–90s); per-task `-x` file runs < 30s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** approved 2026-07-13
