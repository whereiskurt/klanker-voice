---
id: 260812-dee
title: Write pause/backup/teardown design spec
status: complete
date: 2026-08-12
---

# Quick Task 260812-dee — Summary

## What was done

Wrote `docs/superpowers/specs/2026-08-12-pause-backup-teardown-design.md`, the approved
design for three new `kv` command groups: `backup`/`restore`, `pause`/`resume`, and
`destroy --with-backup`. Documentation only — no code or infra changed.

## Design, in short

**Pause** is a single `paused` boolean in `site.hcl` that overrides `desired_count → 0`
and `autoscaling.min_capacity → 0` for all three services. `kv pause` flips it, commits to
`main`, dispatches `terragrunt-apply.yml -f modules=ecs-service`, streams the run, then
verifies via `describe-services`. ~$190/mo → ~$60/mo, ~5 minutes each way, NAT EIP and all
data preserved.

**Backup** is one self-contained unencrypted zip: all three DynamoDB tables as JSONL, the
full transcript ledger, and an `external/` manifest (DID inventory, NAT EIP, non-secret
SSM params). Designed for the destroy case rather than the pause case.

**Destroy** backs up, verifies, drains, explicitly empties the ledger bucket, then
destroys in dependency order. DID release stays manual.

## Findings that shaped it (verified against the repo, not assumed)

- `desired_count` is terraform-owned — `modules/ecs-service/v1.0.0/main.tf:246` has no
  `lifecycle { ignore_changes }`. A raw `aws ecs update-service --desired-count 0` would be
  reverted by the next CI deploy, so the pause had to be a git-tracked flag.
- `aws_appautoscaling_target` sets `min_capacity = 1` (`main.tf:313`) and Application Auto
  Scaling *enforces* MinCapacity, so `desired_count = 0` alone bounces back. Worse, its
  `depends_on` ordering means a pause apply lowers the count before the floor, opening a
  race that can leave a service pinned at 1 while terraform believes it is 0. The spec
  closes this in the verification step rather than hoping the race does not fire.
- The ledger bucket has **no `force_destroy`** (`modules/ledger/v1.0.0/main.tf:13`) — a
  destroy fails on it while it holds objects. Good accident, but destroy must handle it.
- The Route53 parent zone is a `data` source (`modules/site/v1.0.0/route35.tf:1`), so the
  sub-zone and its NS delegation are both terraform-managed — **a rebuild never touches the
  registrar**. This was the scariest unknown going in.
- `client/public/greetings/greeting-1.mp3` (the hand-spliced take) and the other audio
  assets are already committed, so they need no backup.
- `vpc_endpoints.enabled = false` — six interface endpoints would have been ~$44/mo; the
  ~$60/mo paused floor is correct.

## Operator decisions recorded

Scale-to-zero over deep pause or full destroy; single all-or-nothing boolean; commit to
`main` then dispatch CI; watch the run and verify task count; config-is-the-guard for CI;
ledger always in the backup; plain unencrypted zip with a loud warning; one spec covering
all three commands.

## Follow-ups

- Ledger size unmeasured (no valid AWS credentials during the session).
- Implementation not started — spec is the deliverable.
- ElevenLabs Pro ($99/mo) is unaffected by any of this and becomes the largest line item
  during a long pause; downgrading is a manual vendor-console action.
