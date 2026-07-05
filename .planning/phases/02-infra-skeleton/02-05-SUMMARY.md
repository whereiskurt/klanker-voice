---
phase: 02-infra-skeleton
plan: 05
subsystem: infra
tags: [terragrunt, ecs, ecr, dynamodb, ssm, kms, sops, ses, dkim, dmarc, route53]
requires:
  - phase: 02-infra-skeleton plan 03
    provides: "SOPS key + encrypted .secrets.sops.json (all six objects real), /kmv/bootstrap/* params pending retirement"
  - phase: 02-infra-skeleton plan 04
    provides: "auth./voice. zones live (Z0555375BRDXI4K3061A / Z057866123L3INGTPP7YI), VPC/ALB/SG stack, cert_map/zone_map/network state outputs"
provides:
  - "ECS Fargate cluster app-use1-kmv ACTIVE + Cloud Map namespace app-use1-kmv.local; ECR repos kmv-auth-app / kmv-voice-app (IMMUTABLE) — container plumbing before Phase 3 CI pushes"
  - "SSM SecureStrings /kmv/secrets/use1/{deepgram,anthropic,elevenlabs}/api_key + jwt/{secret,internal_secret} + oidc/cookie_keys + altcha/secret, all under the secrets module CMK (INFR-05 complete; /kmv/bootstrap/* retired)"
  - "SES identity auth.klankermaker.ai VERIFIED with Easy-DKIM (3 tokens), MAIL FROM s.auth.* MX+SPF, per-identity DMARC, receipt rule set, SMTP IAM user + /kmv/ses/* params (INFR-04)"
  - "Apex TXT _dmarc.klankermaker.ai p=quarantine live in mgmt zone Z036807010CWM2JH60RKQ (D-11); zero apex MX (Pitfall 6 gate passed)"
  - "Module CMKs for Plan 06 TF_VAR_SSM_KMS_KEY_ARNS: alias/kmv-ssm-use1, alias/kmv-dynamodb-ssm-use1, alias/kmv-email-ssm-use1"
affects: [02-06, 02-07, phase-3-auth, phase-4-voice]
tech-stack:
  added: []
  patterns: [plan-review-before-apply-with-apex-record-gate, only-then-bootstrap-retirement, session-scoped-tf-plugin-cache]
key-files:
  created: []
  modified: []
key-decisions:
  - "All six applies were creates-only and state-only — zero repo file changes, so no per-task commits exist (same as 02-04)"
  - "Bootstrap retirement executed for ALL THREE keys (deepgram, anthropic, elevenlabs) — 02-03 had migrated elevenlabs fully, so no pending-key preservation was needed; decrypt lengths matched migration-time records exactly (40/108/51)"
  - "Physical cluster name is app-use1-kmv (module appends region+label to the logical name 'app') — verify adapted accordingly; ECR repos likewise prefixed kmv-"
requirements-completed: [INFR-04, INFR-05]
coverage:
  - id: D1
    description: "Container/data plumbing: cluster ACTIVE, both ECR repos IMMUTABLE, dynamodb unit applied with zero tables (empty concat)"
    requirement: INFR-01 (partial — github-oidc remains for Plan 06)
    verification:
      - kind: command
        ref: "ecs describe-clusters app-use1-kmv == ACTIVE; ecr describe-repositories greps for auth-app + voice-app"
        status: pass
    human_judgment: false
  - id: D2
    description: "INFR-05 second hop: SOPS file → terraform → SSM SecureString under module CMK; bootstrap namespace retired only after verified round-trip"
    requirement: INFR-05
    verification:
      - kind: command
        ref: "get-parameter --with-decryption length checks (>10) on all three api_keys; existence checks on jwt/oidc/altcha; ParameterNotFound on /kmv/bootstrap/deepgram_api_key"
        status: pass
    human_judgment: false
  - id: D3
    description: "INFR-04: SES identity + DKIM Success, MAIL FROM SPF, apex DMARC p=quarantine, zero apex MX"
    requirement: INFR-04
    verification:
      - kind: command
        ref: "get-identity-verification-attributes == Success; get-identity-dkim-attributes == Success (3 tokens); dig TXT _dmarc.klankermaker.ai contains p=quarantine; mgmt-zone apex MX query rc=0 AND empty"
        status: pass
    human_judgment: false
metrics:
  duration: "10 min"
  started: "2026-07-05T01:16:07Z"
  completed: "2026-07-05T01:26:00Z"
  tasks: 3
  files: 0
status: complete
---

# Phase 2 Plan 05: APPLY Wave — ecs-cluster/ecr/dynamodb, secrets→SSM, SES+DMARC Summary

The full parallel regional band is applied: Fargate cluster `app-use1-kmv` + ECR repos live before any CI push, all seven provider/auth secrets are SecureStrings under the secrets module's own CMK with the `/kmv/bootstrap/*` namespace safely retired after a verified decrypt round-trip, and `sign-in@auth.klankermaker.ai` is a production-ready sender — SES identity and DKIM both `Success` on the first poll, apex `_dmarc` p=quarantine resolving publicly, and zero apex MX.

## Plan-Mandated Record

| Item | Value |
|------|-------|
| SSM paths created | `/kmv/secrets/use1/{deepgram,anthropic,elevenlabs}/api_key`, `/kmv/secrets/use1/jwt/{secret,internal_secret}`, `/kmv/secrets/use1/oidc/cookie_keys`, `/kmv/secrets/use1/altcha/secret` (7 SecureStrings) + `/kmv/ses/*` (module params incl. 3 SMTP SecureStrings) |
| SecureString CMK | `5c9b878b-122c-41b2-acd4-5fe5c83531bb` (`alias/kmv-ssm-use1`) — NOT `alias/aws/ssm` (verified via describe-parameters KeyId) |
| Bootstrap params deleted | ALL THREE: `/kmv/bootstrap/{deepgram,anthropic,elevenlabs}_api_key` — each only after its `/kmv/secrets/use1/<k>/api_key` decrypt length check passed (40/108/51, matching 02-03 migration lengths exactly) |
| Bootstrap params preserved | none — no pending keys (elevenlabs was fully migrated in 02-03); no follow-up outstanding |
| SES send quota (D-12 evidence) | Max24HourSend **50,000**, MaxSendRate **14/s**, SentLast24Hours 0 — production access confirmed, no review clock |
| DKIM token count | **3** CNAMEs, DkimVerificationStatus `Success` |
| DMARC record | `_dmarc.klankermaker.ai TXT "v=DMARC1; p=quarantine; rua=mailto:dmarc-reports@auth.klankermaker.ai; aspf=r; adkim=r;"` (TTL 600, mgmt zone Z036807010CWM2JH60RKQ) — public dig resolved on attempt 1 |
| Apex MX gate | `list-resource-record-sets` on mgmt zone rc=0 AND empty result — no inbound-mail takeover (make_site_domain stayed false) |
| Cluster / namespace | `app-use1-kmv` (ACTIVE, FARGATE) / Cloud Map `app-use1-kmv.local` (ns-ufqeovvt7c4prds4); task role `ecs-task-role-app-use1-kmv-6e913c73` |
| ECR repos | `kmv-auth-app`, `kmv-voice-app` — both IMMUTABLE, lifecycle + repo policies applied |
| Module CMKs for Plan 06 `TF_VAR_SSM_KMS_KEY_ARNS` | `alias/kmv-ssm-use1` (5c9b878b…), `alias/kmv-dynamodb-ssm-use1` (d5d6f906…), `alias/kmv-email-ssm-use1` (60b07929…) |

## Accomplishments

- **Task 1 — ecs-cluster / ecr / dynamodb:** three creates-only applies (6 + 6 + 3 adds). Cluster `app-use1-kmv` ACTIVE with Cloud Map namespace; both ECR repos IMMUTABLE from the two service.hcl stubs; dynamodb unit applied clean with **zero tables** (empty concat — only its regional CMK + alias + random_id), so Phase 3 adds auth tables by editing its service.hcl only.
- **Task 2 — secrets (INFR-05):** 9 adds (CMK + alias + 7 SecureStrings). Fail-closed round-trip: decrypt length checks passed for all three provider keys (values never printed), existence confirmed for jwt/oidc/altcha paths, KeyId verified as the module CMK. ONLY THEN were the three `/kmv/bootstrap/*` params deleted; post-delete describe returns empty and get-parameter returns ParameterNotFound.
- **Task 3 — email + dmarc (INFR-04, D-10/D-11/D-12):** email plan (39 adds) REVIEWED BEFORE APPLY — every Route53 record targets the app-account auth zone `Z0555375BRDXI4K3061A`; zero records in the mgmt zone, zero at the bare apex (the `auth.klankermaker.ai MX` receive record is the module's subdomain inbound, not the apex). dmarc inline unit: exactly 1 add, the apex TXT in the mgmt zone via global-management provider. SES identity + DKIM both `Success` on poll attempt 1 (delegated NS already hot); public dig for p=quarantine hit on attempt 1.

## Task Commits

| Task | Name | Commit |
|------|------|--------|
| 1 | Apply ecs-cluster, ecr, dynamodb | none — state-only apply (S3 backend), zero repo file changes |
| 2 | Apply secrets + retire /kmv/bootstrap/* | none — state-only apply + AWS CLI deletions |
| 3 | Apply email + dmarc | none — state-only apply |

`git status` stayed clean throughout; the only repo commit for this plan is this SUMMARY (plan's `files_modified` anticipated "state only — applies").

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Stale worktree — reset to expected base**
- **Found during:** startup branch check
- **Issue:** Worktree forked from 27a9525 (02-01 tip); expected base ab39e4a6 (wave-4 tracking) is a descendant — infra/terraform/live tree and .secrets.sops.json would have been missing/stale.
- **Fix:** Verified clean tree + zero local commits + HEAD-is-ancestor, then `git reset --hard ab39e4a6` per spawn instructions (pure fast-forward, non-destructive by construction).
- **Files modified:** none (history pointer only)
- **Committed in:** n/a

### Notes (not deviations)

- **Cluster/repo physical names:** plan text says cluster "app" and repos "auth-app"/"voice-app"; the modules render `app-use1-kmv` and `kmv-auth-app`/`kmv-voice-app` (dc34 naming convention: label/region embedding). Verification commands adapted to real names; the plan's greps (`grep -q 'voice-app'`) still pass on the prefixed names. Wording artifact, not drift.
- **Plugin-cache workaround applied proactively:** session-scoped `TF_CLI_CONFIG_FILE` → fresh `plugin_cache_dir` (per 02-02's documented corrupt global `aws/6.53.0` entry). No repo or home-config changes.
- **No elevenlabs follow-up:** the plan's contingency for a pending elevenlabs migration is moot — 02-03 migrated it fully, so its bootstrap param was retired with the others under the same verified-round-trip rule.
- **Pre-existing `km-*` ECR repos** (platform repos, MUTABLE) share the account; untouched and unrelated to this plan's two `kmv-*` repos.

## Authentication Gates

None — SSO live on all three profiles at preflight (klanker-terraform/klanker-application → 052251888500 AdministratorAccess; klanker-management → 481723467561 HostedZoneAdmin). No Route53 denial on any mgmt-zone call.

## Known Stubs

None introduced by this plan (no repo files touched). dynamodb unit intentionally has zero tables until Phase 3 (plan-specified).

## Threat Flags

None beyond the plan's threat model. T-2-13 mitigated (DKIM + SPF + per-identity DMARC + apex p=quarantine live); T-2-14 mitigated (plan-review gate + automated empty-apex-MX check both passed); T-2-15 unchanged (secrets transit state in the Plan-01-locked bucket); T-2-16 mitigated (ONLY-THEN rule enforced — deletions strictly after length-verified decrypts).

## Verification Results

- Task 1: PASS — cluster `app-use1-kmv` ACTIVE; `kmv-auth-app` + `kmv-voice-app` both present and IMMUTABLE; dynamodb applied with tables = {}.
- Task 2: PASS — decrypt lengths 40/108/51 (>10) for deepgram/anthropic/elevenlabs; jwt/oidc/altcha paths exist as SecureStrings; all params on `alias/kmv-ssm-use1` CMK; `/kmv/bootstrap/` empty, ParameterNotFound confirmed.
- Task 3: PASS — identity VerificationStatus `Success`, DkimVerificationStatus `Success` (3 tokens); `s.auth.klankermaker.ai` MX (feedback-smtp) + SPF TXT present in auth zone; dig `_dmarc.klankermaker.ai` returned p=quarantine on attempt 1; apex MX query succeeded AND returned empty; send quota 50,000/24h @ 14/s recorded.
- Success criteria: all six units applied clean (creates-only, exit 0); every automated verify green; no elevenlabs follow-up outstanding.

## Next Phase Readiness

- **Plan 06 (github-oidc):** unblocked — `TF_VAR_SOPS_KMS_KEY_ID` persisted since 02-03; fill `TF_VAR_SSM_KMS_KEY_ARNS` from the three module CMKs recorded above (`alias/kmv-ssm-use1`, `alias/kmv-dynamodb-ssm-use1`, `alias/kmv-email-ssm-use1`).
- **Phase 3 (auth):** SES sender production-ready (`sign-in@auth.klankermaker.ai`); SMTP creds at `/kmv/ses/smtp/default/auth.klankermaker.ai/*`; jwt/oidc/altcha SecureStrings consumable via `valueFrom`; add auth tables via services/auth/service.hcl and flip site.hcl flags.
- **Phase 4 (voice):** deepgram/anthropic/elevenlabs keys live at `/kmv/secrets/use1/<name>/api_key`; cluster + `kmv-voice-app` repo ready for image pushes.

---
*Phase: 02-infra-skeleton*
*Completed: 2026-07-05*

## Self-Check: PASSED

- AWS state re-verified live at summary time: cluster `app-use1-kmv` ACTIVE; `/kmv/bootstrap/` param count 0; SES identity VerificationStatus Success
- No per-task commits expected or made (state-only applies; git status clean before SUMMARY)
- STATE.md / ROADMAP.md / REQUIREMENTS.md untouched (orchestrator-owned)
