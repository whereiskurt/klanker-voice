---
phase: 02-infra-skeleton
plan: 03
subsystem: infra
tags: [sops, kms, secrets, ssm, terragrunt, direnv]
requires:
  - phase: 02-infra-skeleton plan 01
    provides: "infra/.envrc contract (empty TF_VAR_SOPS_KMS_KEY_ID placeholder), public repo, gh auth"
  - phase: 02-infra-skeleton plan 02
    provides: "site.hcl parse-time sops decrypt path, .secrets.sops.json.template, tree that validates"
provides:
  - "Single-region SOPS KMS key 76235c7b-90ba-4ca8-a87d-19870c7c112f + alias/sops in 052251888500 us-east-1 (MultiRegion=False, D-03)"
  - ".sops.yaml creation rule pinning .secrets*.json to arn:aws:kms:us-east-1:052251888500:alias/sops"
  - "infra/terraform/live/site/.secrets.sops.json — committed, encrypted; all six objects with REAL values (all three provider keys migrated, jwt/oidc/altcha freshly generated)"
  - "TF_VAR_SOPS_KMS_KEY_ID persisted in infra/.envrc AND as GitHub repo variable (Plan 06 github-oidc unblocked, Pitfall 5)"
  - "scripts/setup-sops.sh — idempotent single-region SOPS bootstrap"
affects: [02-05, 02-06, 02-07, phase-3-auth, phase-4-voice]
tech-stack:
  added: []
  patterns: [sops-kms-encrypted-secrets-in-public-repo, idempotent-alias-check-bootstrap, scratchpad-only-plaintext-migration]
key-files:
  created:
    - scripts/setup-sops.sh
    - .sops.yaml
    - infra/terraform/live/site/.secrets.sops.json
  modified:
    - infra/.envrc
key-decisions:
  - "SOPS KMS key ID = 76235c7b-90ba-4ca8-a87d-19870c7c112f (plain UUID, single-region — Plan 06 github-oidc apply reads it from env/repo var)"
  - "AWS_PROFILE=klanker-terraform added to infra/.envrc — site.hcl's parse-time sops decrypt needs ambient KMS creds locally (profile name is non-secret, D-06 compliant)"
  - "ElevenLabs bootstrap param existed (CONTEXT said in-progress) — migrated fully, no pending placeholder"
requirements-completed: []
coverage:
  - id: D1
    description: "SOPS half of INFR-05: provider keys encrypted-at-rest in repo, decryptable only via KMS; SSM half lands in Plan 05"
    requirement: INFR-05 (partial — first hop)
    verification:
      - kind: command
        ref: "sops decrypt round-trip (six keys, length checks only) + kms describe-key MultiRegion=False + terragrunt run --all validate 10/2"
        status: pass
    human_judgment: false
metrics:
  duration: "6 min"
  started: "2026-07-05T00:55:57Z"
  completed: "2026-07-05T01:02:00Z"
  tasks: 2
  files: 4
status: complete
---

# Phase 2 Plan 03: SOPS KMS Key + Bootstrap Secrets Migration Summary

Single-region SOPS KMS key `76235c7b-90ba-4ca8-a87d-19870c7c112f` (alias/sops, us-east-1) now encrypts a committed `.secrets.sops.json` carrying all three real provider keys migrated from `/kmv/bootstrap/*` plus freshly generated jwt/oidc/altcha values — the whole tree validates against the real sops decrypt path, and the key ID is persisted everywhere Plan 06's github-oidc policies will read it.

## Key Values for Later Plans

| Item | Value |
|------|-------|
| **SOPS KMS Key ID** | `76235c7b-90ba-4ca8-a87d-19870c7c112f` (plain UUID, MultiRegion=False) |
| Alias | `alias/sops` → `arn:aws:kms:us-east-1:052251888500:alias/sops` |
| TF_VAR_SOPS_KMS_KEY_ID | set in `infra/.envrc` AND as GitHub repo variable (verified via `gh variable list`) — **Plan 06 github-oidc apply is unblocked** (Pitfall 5 ordering satisfied) |
| ElevenLabs migration | **DONE** — the `/kmv/bootstrap/elevenlabs_api_key` param existed (CONTEXT had it as in-progress); migrated like the others, no `sops edit` follow-up needed |
| `/kmv/bootstrap/*` params | **All 3 still present** — deletion deferred to Plan 05, only after the secrets-module apply proves the SSM round-trip |
| TF_VAR_SSM_KMS_KEY_ARNS | still empty — Plan 06 fills after module CMKs exist |

## Accomplishments

- **scripts/setup-sops.sh (Task 1):** single-region adaptation of the source env.sops.sh — keeps the alias-existence check (describe-key on alias/sops), .sops.yaml writer, infra/.envrc upsert, and gh-variable printout; drops the replica-regions loop and `--multi-region` flag (D-03). First run created the key + alias; **second run reused the same key ID as a clean no-op** (idempotence proven). The script also *runs* `gh variable set` (repo exists since Plan 01), not just prints it.
- **.sops.yaml (repo root):** exactly one creation rule / one KMS ARN — `path_regex: \.secrets(\.sops)?\.json$` → `arn:aws:kms:us-east-1:052251888500:alias/sops`.
- **Secrets migration (Task 2):** all three provider keys (deepgram 40 chars, anthropic 108, elevenlabs 51 — lengths only, values never printed) fetched with `--with-decryption` into a umask-077 scratchpad workfile via `jq --arg`, jwt.secret / jwt.internal_secret / oidc.cookie_keys / altcha.secret each generated as fresh `openssl rand -hex 32` (verified 64-hex, zero CHANGEME strings remain), encrypted with `sops encrypt`, workfile destroyed with `rm -P`.
- **Round-trip + tree gate:** `sops --decrypt | jq keys` = exactly `altcha,anthropic,deepgram,elevenlabs,jwt,oidc`; `terragrunt run --all validate` → 10 succeeded / 2 excluded, now exercising the **real** sops decrypt branch of site.hcl (no fallback file exists). The benign "non-existent file" stderr noise from 02-02 is gone.
- **Ciphertext-only commit proven:** `git show` of the committed blob contains 8 `ENC[...]` strings + sops metadata with the us-east-1 ARN; plaintext-marker scan (sk-ant / CHANGEME) count = 0; `git log --all -- .secrets.json` empty (plaintext never entered history).

## Task Commits

| Task | Name | Commit |
|------|------|--------|
| 1 | setup-sops.sh, .sops.yaml, TF_VAR persistence | 1810da0 |
| 2 | /kmv/bootstrap/* migration → encrypted .secrets.sops.json | eea4251 |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Stale worktree — reset to expected base**
- **Found during:** startup branch check
- **Issue:** Worktree forked from 27a9525 (pre-wave-2); expected base 87a58508 is a descendant — infra/terraform/ was missing entirely.
- **Fix:** Verified clean tree + zero local commits + HEAD-is-ancestor, then `git reset --hard 87a58508` per the spawn instructions (pure fast-forward, non-destructive by construction).
- **Files modified:** none (history pointer only)
- **Committed in:** n/a

**2. [Rule 3 - Blocking] Parse-time sops decrypt has no ambient AWS creds**
- **Found during:** Task 2 step 5 (pre-validate probe)
- **Issue:** With `.secrets.sops.json` now present, site.hcl's `run_cmd sops --decrypt` runs on every terragrunt parse — and sops falls through the default credential chain to EC2 IMDS (fails) because nothing exports a profile. Every local terragrunt run would break. dc34's env.sh solved this by exporting terraform-profile creds ambiently.
- **Fix:** Added `export AWS_PROFILE=klanker-terraform` (with explanatory comment) to `infra/.envrc` — a profile *name* is non-secret, D-06 compliant. CI is unaffected (Plan 06's kms-sops-decrypt policy grants the OIDC roles kms:Decrypt; workflows never source .envrc).
- **Files modified:** infra/.envrc
- **Committed in:** eea4251

### Notes (not deviations)

- **Plaintext fallback already gone:** the temporary Plan 02 `infra/terraform/live/site/.secrets.json` was untracked and lived only in the removed 02-02 worktree — it exists nowhere on disk (worktree + main checkout both checked) and never entered git history. The plan's deletion requirement is satisfied.
- **ElevenLabs param existed** despite CONTEXT listing it as in-progress — migrated normally; the tolerance path (leave placeholder, `sops edit` later) was implemented in the migration script but not needed.

## Authentication Gates

None — AWS SSO was live for klanker-terraform and klanker-application; gh was authenticated.

## Known Stubs

| Stub | File | Reason |
|------|------|--------|
| `TF_VAR_SSM_KMS_KEY_ARNS=` (empty) | infra/.envrc | Intentional per Plan 01 — Plan 06 fills after module CMKs exist |

## Verification Results

- Task 1 battery: PASS — `bash -n` + executable; `describe-key alias/sops` MultiRegion=**False** (plain UUID, not mrk-); .sops.yaml contains the 052251888500 us-east-1 ARN (grep count of `arn:aws:kms` = 1); `^export TF_VAR_SOPS_KMS_KEY_ID=[0-9a-f-]{36}$` matches in infra/.envrc; `gh variable list` shows TF_VAR_SOPS_KMS_KEY_ID; second script run reused the same key ID.
- Task 2 battery: PASS — no `.secrets.json` anywhere; `.secrets.sops.json` tracked and NOT gitignored; `"sops"` metadata present; decrypt keys = `altcha,anthropic,deepgram,elevenlabs,jwt,oidc`; deepgram api_key length 40 (> 10); `/kmv/bootstrap/` param count still 3; scratchpad workdir removed.
- Success criteria: `terragrunt run --all validate` green (10/2) against the real encrypted file; only ciphertext committed (git show inspection).

## Next Phase Readiness

- Plan 05 (secrets/SSM apply) consumes the encrypted file via site.hcl; after the SSM round-trip proves out, delete the three `/kmv/bootstrap/*` params (research step 5 "ONLY THEN" rule).
- Plan 06 (github-oidc) can apply: `TF_VAR_SOPS_KMS_KEY_ID` resolves from both infra/.envrc (local) and the repo variable (CI) — no placeholder-ARN drift loop.
- Operators on fresh shells: `direnv allow infra` (or `source infra/.envrc`) now also sets `AWS_PROFILE=klanker-terraform`, required for any terragrunt command since parse-time decrypt is live.

---
*Phase: 02-infra-skeleton*
*Completed: 2026-07-05*

## Self-Check: PASSED

- scripts/setup-sops.sh, .sops.yaml, infra/terraform/live/site/.secrets.sops.json, infra/.envrc all exist on disk
- Commits 1810da0 / eea4251 present in git log; no file deletions in either commit
- Committed secrets blob is ciphertext-only (ENC[ values + sops metadata; plaintext-marker scan zero; .secrets.json absent from all history)
- STATE.md / ROADMAP.md untouched (orchestrator-owned)
