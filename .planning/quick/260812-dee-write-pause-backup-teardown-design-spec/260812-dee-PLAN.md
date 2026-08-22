---
id: 260812-dee
title: Write pause/backup/teardown design spec
status: complete
mode: quick
date: 2026-08-12
---

# Quick Task 260812-dee: Write pause/backup/teardown design spec

## Goal

Capture the operator-approved design for pausing, backing up, and tearing down the
klanker-voice AWS footprint as a single implementation-ready spec. Document only — no
code, no infra changes.

## Context

Operator asked how hard it would be to stop/destroy all cloud resources and bring them
back, wanting to "pause" deployments. Brainstormed to a design across two rounds; scope
grew mid-discussion to include `kv backup` and a `kv destroy --with-backup` concept, since
a full teardown (including releasing the phone numbers) is considered likely later.

Design was validated against the live repo, not assumed. Key findings folded into the
spec: `desired_count` is terraform-owned (no `ignore_changes`), so a raw AWS API call
would be reverted by the next CI deploy; `aws_appautoscaling_target.min_capacity = 1`
actively fights a scale-to-zero and creates an apply-ordering hazard; the ledger bucket
has no `force_destroy` and will block a destroy; the Route53 parent zone is a `data`
source so a rebuild never touches the registrar; and the audio assets including the
hand-spliced `greeting-1.mp3` are already in git.

## Tasks

### T-01: Write the design spec

- **files:** `docs/superpowers/specs/2026-08-12-pause-backup-teardown-design.md`
- **action:** Author the spec covering the risk inventory, `kv backup`/`restore`,
  `kv pause`/`resume`, `kv destroy --with-backup`, cross-cutting concerns, cost outcomes,
  a decisions log, and open items.
- **verify:** File exists; all three command groups specified; every operator decision
  from the brainstorm recorded in the decisions log; all infra claims cite the file and
  line they were verified against.
- **done:** Spec committed.

## Must-haves

- **truths:**
  - Pause must flip both `desired_count` and `autoscaling.min_capacity` — either alone fails.
  - The pause mechanism must be git-tracked, not a raw API call.
  - Backup must be designed for the destroy case, not just the pause case.
  - DID release stays manual and outside the tool.
- **artifacts:**
  - `docs/superpowers/specs/2026-08-12-pause-backup-teardown-design.md`
- **key_links:**
  - `infra/terraform/modules/ecs-service/v1.0.0/main.tf`
  - `infra/terraform/modules/ledger/v1.0.0/main.tf`
  - `infra/terraform/live/site/site.hcl`

## Out of scope

Implementation of the commands, any infra change, and any live AWS action.
