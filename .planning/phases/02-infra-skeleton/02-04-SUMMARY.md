---
phase: 02-infra-skeleton
plan: 04
subsystem: infra
tags: [terragrunt, route53, acm, cross-account-dns, vpc, alb, webrtc]
requires:
  - phase: 02-infra-skeleton plan 02
    provides: "Apply-ready kmv terragrunt tree (site/certs/network units), webrtc_udp SG diff, plugin-cache workaround"
  - phase: 02-infra-skeleton plan 03
    provides: "SOPS key + .secrets.sops.json (parse-time decrypt), AWS_PROFILE=klanker-terraform in infra/.envrc"
provides:
  - "Hosted zones auth./voice.klankermaker.ai live in 052251888500 with NS delegation in mgmt zone Z036807010CWM2JH60RKQ (INFR-02 cross-account DNS, A1 closed affirmatively)"
  - "ACM certs ISSUED: auth (+*.auth), voice (+*.voice), site (apex + *, use1, *.use1) — Phase 2 TLS gate met"
  - "VPC/subnets/NAT/ALB/SG stack applied in us-east-1 incl. webrtc-udp SG (attached to nothing)"
  - "State objects in tf-kmv-use1-6e913c73 for site (use1 global key), region/us-east-1/certs, region/us-east-1/network — zone_map + cert_map + network outputs consumable by Plan 05"
affects: [02-05, 02-06, 02-07, phase-3-auth, phase-4-voice]
tech-stack:
  added: []
  patterns: [plan-review-before-apply-with-mgmt-zone-baseline-diff, session-scoped-tf-plugin-cache]
key-files:
  created: []
  modified: []
key-decisions:
  - "All three applies were creates-only and state-only — zero repo file changes, so no per-task commits exist (plan anticipated 'hcl fixes only if plans surface drift'; none surfaced)"
  - "Mgmt-zone safety gate implemented as a before/after record-set diff: only the two NS delegation records were added, nothing modified/removed (T-2-11 mitigated)"
requirements-completed: [INFR-02]
coverage:
  - id: D1
    description: "Cross-account DNS: subdomain zones in app account + NS delegation written via klanker-management (HostedZoneAdmin) — no denial, no manual console step"
    requirement: INFR-02
    verification:
      - kind: command
        ref: "list-hosted-zones (app) + list-resource-record-sets Z036807010CWM2JH60RKQ (mgmt) greps + dig +short NS for both subdomains"
        status: pass
    human_judgment: false
  - id: D2
    description: "TLS gate: auth./voice./site ACM certs all ISSUED (DNS-validated cross-account for the site cert)"
    requirement: INFR-02
    verification:
      - kind: command
        ref: "aws acm list-certificates Status==ISSUED greps for all three domains"
        status: pass
    human_judgment: false
  - id: D3
    description: "Network slice applied: VPC/subnets/ALB active, HTTPS listener bound to ISSUED site cert, webrtc-udp SG exists with UDP ingress, zero ECS resources"
    requirement: INFR-01 (partial — regional plumbing continues in Plan 05)
    verification:
      - kind: command
        ref: "describe-security-groups (udp perm) + describe-load-balancers (active) + describe-listener-certificates + ecs list-clusters (empty)"
        status: pass
    human_judgment: false
metrics:
  duration: "9 min"
  started: "2026-07-05T01:03:54Z"
  completed: "2026-07-05T01:13:00Z"
  tasks: 3
  files: 0
status: complete
---

# Phase 2 Plan 04: APPLY Wave — site → certs → network Summary

Cross-account DNS is live (auth./voice.klankermaker.ai zones in 052251888500, NS-delegated from mgmt zone Z036807010CWM2JH60RKQ via HostedZoneAdmin with zero manual steps), all three ACM certificates reached ISSUED within one apply cycle, and the full VPC/ALB/SG stack — including the dormant webrtc-udp SG — is applied and verified in us-east-1.

## Key Values for Later Plans (plan-mandated record)

| Item | Value |
|------|-------|
| auth.klankermaker.ai zone ID | `Z0555375BRDXI4K3061A` (052251888500) |
| voice.klankermaker.ai zone ID | `Z057866123L3INGTPP7YI` (052251888500) |
| mgmt apex zone ID | `Z036807010CWM2JH60RKQ` (481723467561, pre-existing) |
| auth cert ARN | `arn:aws:acm:us-east-1:052251888500:certificate/1016e695-cf20-4e73-b69c-2f77e9ea2f15` (SAN `*.auth.`) — ISSUED |
| voice cert ARN | `arn:aws:acm:us-east-1:052251888500:certificate/bd42c4ae-e370-4a6b-9b5e-4e784e61b433` (SAN `*.voice.`) — ISSUED |
| site cert ARN | `arn:aws:acm:us-east-1:052251888500:certificate/7aecf6cd-336e-42f1-8361-88ea5f191826` (apex + `*.`, `use1.`, `*.use1.`) — ISSUED, validated in mgmt zone |
| ALB DNS name | `alb-use1-klankermaker-ai-246817828.us-east-1.elb.amazonaws.com` (state: active) |
| ALB ARN | `arn:aws:elasticloadbalancing:us-east-1:052251888500:loadbalancer/app/alb-use1-klankermaker-ai/67d1cd7b9c2c12d7` |
| HTTPS listener cert | bound to the ISSUED site cert (`7aecf6cd…`, IsDefault=true) |
| webrtc SG ID | `sg-02f86d3fc6240fff6` (`use1.klankermaker.ai-webrtc-udp`) — UDP 1024–65535 from 0.0.0.0/0, attached to zero ENIs |
| VPC ID | `vpc-0221b52880c0067e9` (`use1.klankermaker.ai`, 10.0.0.0/16, 2 AZ × public/private subnets, 1 NAT) |
| Propagation | Instant — `dig +short NS` resolved both subdomains on attempt 1; all three certs ISSUED on first poll (certs apply itself completed in ~60s) |

## Accomplishments

- **Task 1 — site unit:** plan reviewed before apply (5 add / 0 change / 0 destroy: two `aws_route53_zone.account_zonenames`, two `aws_route53_record.forward_ns_to_zones`, one `random_id.rnd`). Mgmt-zone baseline snapshot taken pre-apply; post-apply diff shows exactly `+NS auth.klankermaker.ai` / `+NS voice.klankermaker.ai`, nothing modified or removed (the pre-existing apex NS/SOA and `sandboxes.` delegation untouched — T-2-11 gate satisfied). HostedZoneAdmin sufficed for the data lookup + record writes: **A1 closed affirmatively**, no mgmt-console action needed.
- **Task 2 — certs unit:** creates-only apply (12 add: 3 certs, 3 validation waiters, 4 primary-zone validation CNAMEs in the mgmt zone, 2 subdomain validation CNAMEs in the app-account zones). Terraform's validation waiters completed inside the apply (~60s) since NS delegation already resolved publicly; the bounded 15-minute poll passed on attempt 1 with all three domains ISSUED.
- **Task 3 — network unit:** creates-only apply (30 add). VPC 10.0.0.0/16 with 2-AZ public (10.0.1–2.0/24) / private (10.0.101–102.0/24) subnets, IGW + single NAT, ALB with access-log S3 bucket (encrypted, public-access-blocked, lifecycle rule), HTTPS listener bound to the ISSUED site cert, and the six-SG set including `webrtc_udp` (UDP 1024–65535 ingress, attached to nothing — `ecs_services.enabled=false`, zero live exposure). `aws ecs list-clusters` empty — no ECS resources created, flags honored.

## Task Commits

| Task | Name | Commit |
|------|------|--------|
| 1 | Apply site — zones + NS delegation | none — state-only apply (S3 backend), zero repo file changes |
| 2 | Apply certs — 3 × ACM ISSUED | none — state-only apply |
| 3 | Apply network — VPC/ALB/SGs + webrtc-udp | none — state-only apply |

The plan's `files_modified` anticipated "state only — hcl fixes only if plans surface drift"; no drift surfaced, `git status` stayed clean throughout, so the only repo commit for this plan is this SUMMARY.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Stale worktree — reset to expected base**
- **Found during:** startup branch check
- **Issue:** Worktree forked from 27a9525 (02-01 tip); expected base 9d0c51cb (wave-3 tracking) is a descendant — `infra/terraform/live/site/.secrets.sops.json` was missing entirely, which would have broken every terragrunt parse.
- **Fix:** Verified clean tree + zero local commits + HEAD-is-ancestor, then `git reset --hard 9d0c51cb` per spawn instructions (pure fast-forward, non-destructive by construction).
- **Files modified:** none (history pointer only)
- **Committed in:** n/a

### Notes (not deviations)

- **Plugin-cache workaround applied proactively:** session-scoped `TF_CLI_CONFIG_FILE` → scratchpad `plugin_cache_dir`, per 02-02's documented corrupt global `aws/6.53.0` cache entry. No repo or home-dir changes.
- **Plan-text expectation vs module reality:** the Task 3 action text mentioned "flow logs, VPC endpoints" among expected resources; the network module's actual plan contains neither (it creates ALB *access logs* to S3, not VPC flow logs, and no VPC endpoints). Applied state matches Plan 02's plan-time output exactly (30 creates), so this is a wording artifact, not drift — nothing was added or removed.
- **Second 10.0.0.0/16 VPC exists in the account** (`vpc-027ba3e68c2e32549`, km shared-sandbox VPC). Separate VPC, no interaction; ours is tagged `Name=use1.klankermaker.ai`.

## Authentication Gates

None — SSO live on all three profiles at preflight (`klanker-terraform`/`klanker-application` → 052251888500 AdministratorAccess; `klanker-management` → 481723467561 HostedZoneAdmin). No HostedZoneAdmin denial on any call.

## Known Stubs

None introduced by this plan (no repo files touched).

## Threat Flags

None — no new surface beyond the plan's threat model. T-2-10/T-2-11 mitigations executed (plan review + baseline diff); T-2-12 accepted state confirmed (webrtc SG attached to zero ENIs).

## Verification Results

- Task 1: PASS — both zones in app-account `list-hosted-zones`; both NS record sets present in Z036807010CWM2JH60RKQ; `dig +short NS` non-empty for auth **and** voice on first attempt; mgmt-zone diff = exactly the two delegation adds.
- Task 2: PASS — `list-certificates Status==ISSUED` contains klankermaker.ai, auth.klankermaker.ai, voice.klankermaker.ai (ARNs above).
- Task 3: PASS — webrtc SG `IpPermissions[0].IpProtocol == udp`; ALB `State.Code == active`; HTTPS listener cert = ISSUED site cert; `ecs list-clusters` empty.
- Success criteria: all three units applied clean (creates-only, exit 0); all automated verifies green; no manual mgmt-console action needed.

## Next Phase Readiness

- Plan 05's parallel regional units (ecr, dynamodb, secrets, email, ecs-cluster) have every dependency output: `zone_map` (site state), `cert_map` (certs state), VPC/subnet/SG/ALB outputs (network state).
- Email unit's SES DNS records will land in the now-live `auth.klankermaker.ai` zone; the dmarc inline unit writes to the mgmt zone via the same proven HostedZoneAdmin path.
- Phase 4 items unchanged: attach + tighten webrtc SG, ALB idle-timeout.

---
*Phase: 02-infra-skeleton*
*Completed: 2026-07-05*

## Self-Check: PASSED

- AWS state verified live: zones Z0555375BRDXI4K3061A / Z057866123L3INGTPP7YI, three ISSUED cert ARNs, ALB active, sg-02f86d3fc6240fff6 present
- No per-task commits expected or made (state-only applies; git status clean before SUMMARY)
- STATE.md / ROADMAP.md / REQUIREMENTS.md untouched (orchestrator-owned)
