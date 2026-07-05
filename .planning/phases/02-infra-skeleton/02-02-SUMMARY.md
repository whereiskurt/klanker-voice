---
phase: 02-infra-skeleton
plan: 02
subsystem: infra
tags: [terraform, terragrunt, kmv, route53, ses, sops, webrtc, github-oidc]
requires:
  - phase: 02-infra-skeleton plan 01
    provides: "State backend tf-kmv-use1-6e913c73, infra/.envrc contract, AGENTS.md anchor, DMARC route 2 decision"
provides:
  - "Complete infra/terraform/ tree: providers pair, 11 verbatim modules, kmv live tree — validates and plans clean against the real backend"
  - "site.hcl kmv definition (label kmv, klankermaker.ai, [auth, voice], six secrets, ecs_tasks/ecs_services disabled)"
  - "services/{auth,voice}/service.hcl data stubs (voice assign_public_ip=true, zero filesystem reads)"
  - "region/us-east-1/dmarc inline unit (apex _dmarc p=quarantine via global-management)"
  - "network module webrtc_udp SG + security_group_ids output entry (Phase 4 consumes)"
  - "SECRETS.md + .secrets.sops.json.template (kmv shape); gitignored plaintext fallback in place"
affects: [02-03, 02-04, 02-05, 02-06, 02-07, phase-3-auth, phase-4-voice]
tech-stack:
  added: [hashicorp/aws 6.53.0 (lock files), hashicorp/random 3.9.0]
  patterns: [verbatim-clone-then-single-rewrite-pass, inline live-tree unit for one-off resources, data-only service.hcl stubs]
key-files:
  created:
    - infra/terraform/providers/{global,regional}.hcl
    - infra/terraform/modules/ (11 modules, config.hcl + v1.0.0)
    - infra/terraform/live/site/site.hcl
    - infra/terraform/live/site/services/{auth,voice}/service.hcl
    - infra/terraform/live/site/region/us-east-1/dmarc/{terragrunt.hcl,main.tf}
    - infra/terraform/live/site/region/us-east-1/{email/email.hcl,network/network.hcl}
    - infra/terraform/live/site/SECRETS.md
    - infra/terraform/live/site/.secrets.sops.json.template
  modified:
    - infra/terraform/modules/network/v1.0.0/securitygroups.tf
    - infra/terraform/modules/network/v1.0.0/outputs.tf
key-decisions:
  - "ALB idle-timeout NOT exposed by network module alb.tf — deferred to Phase 4 (Open Question 4), noted in network.hcl"
  - "A2 held: pre-created bucket/table sufficed — no ambient terraform-profile creds, no tg-local.sh helper needed"
  - "A3 held: user_uploads/upload_processors/waffaw/cloudtrail blocks deleted outright — site module declares only site/dns/waf variables, plans clean"
  - "fwd_rules single-sourced in region email.hcl (site.hcl email.fwd_rules = []) to avoid concat duplication"
  - "Regenerated .terraform.lock.hcl files committed (multi-platform zh: hashes — linux CI safe)"
patterns-established:
  - "Pure copy first, single rewrite pass second — clone diff and rewrite diff separable in history"
  - "Inline live-tree unit (dmarc) for one-off resources keeps versioned modules verbatim"
requirements-completed: [INFR-01]
coverage:
  - id: D1
    description: "Providers + 11 modules cloned verbatim; excluded modules/units absent; no cache/lock artifacts copied"
    requirement: INFR-01
    verification:
      - kind: other
        ref: "diff -r per module vs source (-x caches) + test ! -d for excluded dirs"
        status: pass
    human_judgment: false
  - id: D2
    description: "site.hcl delta table fully applied (kmv label, SGUID default 6e913c73, make_site_domain=false, six secret definitions, ecs flags off); stubs carry zero file() reads; dmarc unit exists with p=quarantine; webrtc SG present"
    requirement: INFR-01
    verification:
      - kind: other
        ref: "Task 2 automated grep battery (git check-ignore + greps + stub file() count == 0)"
        status: pass
    human_judgment: false
  - id: D3
    description: "Whole tree validates and plans clean against the real backend; excluded units skip; forbidden strings zero; hcl fmt clean"
    requirement: INFR-01
    verification:
      - kind: integration
        ref: "terragrunt run --all validate && terragrunt run --all plan from live/site (10 succeeded / 2 excluded, exit 0, creates-only)"
        status: pass
    human_judgment: false
duration: 16min
completed: 2026-07-05
status: complete
---

# Phase 2 Plan 02: Verbatim Module Clone + kmv Rewrites Summary

**Full kmv terragrunt tree cloned from the proven source layout — 11 verbatim modules + kmv-rewritten live tree — validating and planning clean (creates-only) against the tf-kmv-use1-6e913c73 backend, with the apex-DMARC inline unit and WebRTC UDP SG groundwork in place.**

## Performance

- **Duration:** 16 min
- **Started:** 2026-07-05T00:35:05Z
- **Completed:** 2026-07-05T00:51:44Z
- **Tasks:** 3
- **Files modified:** 126 across three commits

## Accomplishments

- **Verbatim clone (Task 1):** providers/{global,regional}.hcl, all 11 in-scope modules (site, certs, network, ecs-cluster, ecr, dynamodb, secrets, email, github-oidc, ecs-task, ecs-service) byte-identical to source (diff -r verified), plus the live-tree verbatim subset (root terragrunt.hcl, waf.hcl data file, github-oidc unit, skip.hcl, region.hcl, nine unit terragrunt.hcl, five thin wrappers, email forwarder lambda). No excluded modules/units, no cache/lock/.DS_Store artifacts.
- **kmv rewrite pass (Task 2):** every row of the research delta table applied to site.hcl — label kmv, repo klanker-voice, tf-kmv prefix, `random_suffix` default `6e913c73`, zone klankermaker.ai, subdomains [auth, voice], local_ports {auth 3002, voice 7860}, `make_site_domain = false` (apex-MX trap), six-secret definitions (deepgram/anthropic/elevenlabs/jwt/oidc/altcha), ecs_tasks/ecs_services disabled, github_oidc pruned to terragrunt/readonly/release/deploy with single-region SOPS KMS ARNs, ec2_runner_instance_profile disabled, and PassRole patterns matching the ecs-task module's actual `<task>-<region_label>-kmv-execution-role` naming.
- **New units/stubs:** data-only auth/voice service.hcl stubs (voice `assign_public_ip = true`; hardcoded image tags; zero `file()` tokens — the grep gate proves it), apex-DMARC inline unit writing `_dmarc.klankermaker.ai` `p=quarantine` via the global-management provider, email.hcl without receive_rules, network.hcl with nlb disabled.
- **WebRTC groundwork:** additive `webrtc_udp` SG (UDP 1024–65535 ingress) in modules/network/v1.0.0/securitygroups.tf + appended to the `security_groups` map and `security_group_ids` list in outputs.tf — **the exact network-module diff vs source is these two files only** (diff -rq verified).
- **Gates (Task 3):** forbidden-string greps zero (kmk, voiceai across all of infra/; source-site labels across all .hcl); `terragrunt hcl fmt --check` clean; `terragrunt run --all validate` 10 succeeded / 2 excluded; `terragrunt run --all plan` exit 0, all units creates-only (site unit plans both hosted zones + NS delegation records; certs plans the four primary-zone validation records in the mgmt zone; email plans the auth.klankermaker.ai identity record set; dmarc plans the apex TXT).

## Plan-Mandated Dispositions

| Item | Outcome |
|------|---------|
| ALB idle-timeout (Open Question 4) | **Not exposed** — network module alb.tf/variables.tf has no idle-timeout input. Deferred to Phase 4; noted as a comment in network.hcl. Module NOT modified (D-02). |
| A2 (backend auth without ambient creds) | **Held** — with bucket/table pre-created and `profile = klanker-terraform` in remote_state config, `terragrunt init`/validate/plan succeeded with no exported credentials. No scripts/tg-local.sh helper needed. |
| A3 (deleted site.hcl blocks) | **Held** — site module declares only `site`/`dns`/`waf` variables; user_uploads/upload_processors/waffaw/cloudtrail blocks deleted outright, no enabled=false stubs needed; site unit plans clean. |
| Network module diff | Exactly two files: `modules/network/v1.0.0/securitygroups.tf` (webrtc_udp SG) and `modules/network/v1.0.0/outputs.tf` (map entry + list append). |

## Task Commits

| Task | Name | Commit |
|------|------|--------|
| 1 | Verbatim copies — providers, 11 modules, live-tree fixed files | 7ee8bf8 |
| 2 | kmv rewrites — site.hcl delta, stubs, dmarc, webrtc SG, secrets template | f5510e4 |
| 3 | Gates — forbidden strings, hcl fmt, validate, plan, lock files | 997caf0 |

## Decisions Made

- fwd_rules live only in region-level email.hcl (site.hcl `email.fwd_rules = []`) — the email module concats site+region lists, so single-sourcing avoids duplicate rules when `TF_VAR_FWD_EMAIL_TO_ADDRESS` is set.
- Regenerated `.terraform.lock.hcl` committed per unit (source convention; hashes include full multi-platform `zh:` entries, so linux CI plans will verify).
- Release/deploy role policies dropped dead CloudFront/EC2-runner permissions (see deviations) — CloudFront and self-hosted runners are explicitly out of scope (D-04).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Worktree base drift — fast-forwarded to expected base**
- **Found during:** startup branch check
- **Issue:** Worktree branch forked from 27a9525, but the expected base 3b4b329 (phase-1 wave-1 merge) was a descendant — mismatch would make this branch appear to delete phase-1 files in a base-relative diff.
- **Fix:** `git merge --ff-only 3b4b329` (zero local commits existed; pure fast-forward, non-destructive by construction).
- **Files modified:** none (history pointer only)
- **Verification:** `git merge-base --is-ancestor 3b4b329 HEAD` passes
- **Committed in:** n/a (pre-commit branch state)

**2. [Rule 3 - Blocking] Shared terraform plugin cache corrupted by parallel first init**
- **Found during:** Task 3 first `run --all validate`
- **Issue:** `~/.terraformrc` enables a global plugin_cache_dir; concurrent unit inits raced and corrupted the cached aws 6.53.0 package (checksum mismatch + truncated binary).
- **Fix:** Session-scoped plugin cache via `TF_CLI_CONFIG_FILE` pointing at a scratchpad terraformrc; first validate run with `--parallelism 1` to warm the cache serially. User's home-dir cache and ~/.terraformrc left untouched (the corrupt `aws/6.53.0` entry in `~/.terraform.d/plugin-cache` remains — safe to delete manually; terraform re-downloads it).
- **Files modified:** none in repo
- **Verification:** validate + plan both exit 0 afterwards
- **Committed in:** n/a

**3. [Rule 1 - Bug] Deploy role PassRole patterns matched nothing the ecs-task module produces**
- **Found during:** Task 2 step 1 (PassRole confirmation read of ecs-task/v1.0.0/main.tf)
- **Issue:** The plan mandated fixing the release role's patterns; the deploy role's source patterns (`kmv-*-task-*` / `kmv-*-execution-*` prefix style) equally fail to match the module's `<task>-use1-kmv-execution-role` naming and would break Plan 07 CI deploys.
- **Fix:** Applied the same corrected suffix patterns (`*-kmv-task-role` / `*-kmv-execution-role`) to the deploy role's iam-pass-role policy.
- **Files modified:** infra/terraform/live/site/site.hcl
- **Verification:** pattern matches module naming `${task.name}-${region.label}-${site.label}-execution-role`
- **Committed in:** f5510e4

**4. [Rule 2 - Missing critical/least privilege] Dead CloudFront + EC2-runner permissions removed from CI roles**
- **Found during:** Task 2 github_oidc rewrite
- **Issue:** Source release/deploy roles carry cloudfront-invalidate and ec2-runner inline policies plus cf-assets S3 ARNs — CloudFront and self-hosted runners are out of scope for kmv (D-04, ec2_runner disabled). Dead grants on public-repo CI roles are unnecessary surface.
- **Fix:** Dropped cloudfront-invalidate (release+deploy), ec2-runner (release), and cf-assets-* ARNs from s3-assets.
- **Files modified:** infra/terraform/live/site/site.hcl
- **Verification:** validate + plan clean; remaining policies cover the D-08 CI flows (ECR push, state, ECS deploy, SSM/KMS read)
- **Committed in:** f5510e4

---

**Total deviations:** 4 auto-fixed (2 × Rule 3 blocking, 1 × Rule 1 bug, 1 × Rule 2 least-privilege). **Impact:** no scope creep; all fixes protect later plans (merge safety, CI deploy correctness, reduced CI-role surface).

## Issues Encountered

- **Benign sops stderr noise during plan:** terragrunt eagerly evaluates the unselected ternary branch of `secret_values` in site.hcl, so `sops --decrypt .secrets.sops.json` prints "cannot operate on non-existent file" once per unit parse even though the plaintext fallback branch is selected. Exit codes unaffected; same behavior exists in the source tree. Disappears in Plan 03 when `.secrets.sops.json` is created.
- **SES receive MX on auth subdomain:** the email module hardcodes `enable_receive_mx = true` per identity, so the plan includes a receive MX for `auth.klankermaker.ai` (NOT the apex — apex stays MX-free per the zone audit). Known module behavior, accepted as-is per D-02.

## Known Stubs

| Stub | File | Reason |
|------|------|--------|
| Placeholder task/service maps, hardcoded `*:0.0.0` image tags | services/{auth,voice}/service.hcl | Intentional per plan — unused while ecs_tasks/ecs_services disabled; Phase 3/4 fill real definitions |
| `.secrets.json` placeholder values (untracked) | infra/terraform/live/site/.secrets.json | Intentional per plan — parse-time decrypt target until Plan 03 creates .secrets.sops.json and deletes this file |
| `dynamodb.tables = []` in both stubs | services/{auth,voice}/service.hcl | Phase 3 adds auth tables; dynamodb unit plans zero tables cleanly |

## Verification Results

- Task 1 automated verify: PASS (11 × diff -r clean; providers + waf.hcl present; excluded dirs absent; zero cache/lock artifacts)
- Task 2 automated verify: PASS (check-ignore, kmv label, zone, make_site_domain=false, dmarc p=quarantine, webrtc grep, stub file() count = 0)
- Task 3 automated verify: PASS (three forbidden-string greps zero; hcl fmt --check clean)
- Deep gate: `terragrunt run --all validate` → 10 succeeded / 2 excluded; `terragrunt run --all plan` → exit 0, creates-only plans (1–39 resources per unit), site unit plans auth+voice hosted zones and both NS delegation records
- git status clean; `.secrets.json` present on disk and ignored

## Next Phase Readiness

- Tree is apply-ready for Plans 04–06 (site → certs → network → parallel regional units → github-oidc).
- Plan 03 (SOPS key + secrets migration) replaces the plaintext fallback with `.secrets.sops.json` and fills `TF_VAR_SOPS_KMS_KEY_ID` in infra/.envrc.
- ALB idle-timeout bump and UDP range tightening are Phase 4 items (module exposes no idle-timeout input today).
- Note for the operator: a corrupt `aws/6.53.0` entry remains in `~/.terraform.d/plugin-cache` (pre-existing global cache, damaged by a parallel-init race) — deleting that directory is safe; terraform re-downloads it.

---
*Phase: 02-infra-skeleton*
*Completed: 2026-07-05*

## Self-Check: PASSED

- All key created files exist on disk (providers, site.hcl, stubs, dmarc, SECRETS.md, template, email.hcl, network.hcl)
- Commits 7ee8bf8 / f5510e4 / 997caf0 / a3b3da3 present in git log; no file deletions across the plan's commit range
- STATE.md / ROADMAP.md untouched (orchestrator-owned)
