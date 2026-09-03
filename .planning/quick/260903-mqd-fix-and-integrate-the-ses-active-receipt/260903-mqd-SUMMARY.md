---
phase: 260903-mqd-fix-and-integrate-the-ses-active-receipt
plan: 01
status: complete
date: 2026-09-03
commits:
  - 0018ff8
  - 50a7880
files_modified:
  - docs/operators/ses-active-rule-set-fix.md
  - docs/operators/README.md
  - scripts/verify-operator-docs.sh
---

# Quick Task 260903-mqd — Summary

Reviewed and repaired `docs/operators/ses-active-rule-set-fix.md`, which arrived untracked from
another session, then wired it into the operator manual. **Documentation only — no Terraform,
no infra, nothing applied.** The underlying fix the doc describes is still unapplied.

## What the review confirmed (unchanged in the doc)

Every mechanical claim held up against the tree:

- `infra/terraform/modules/email/v1.0.0/ses.tf:7-9` is exactly as quoted, including that
  `rule_set_name` is a reference rather than a literal.
- The state-address reasoning is right. `live/site/region/us-east-1/email/terragrunt.hcl:65-67`
  points `terraform { source }` at the module, making it the root module, so
  `aws_ses_active_receipt_rule_set.main` is unprefixed. Corroborated independently: only one
  `email` unit exists in the whole tree — `ap-southeast-1` and `ca-central-1` have none — so
  there is no second unit contending for the account-wide pointer.
- `lifecycle { destroy = false }` is genuinely load-bearing. Deleting that resource type calls
  `SetActiveReceiptRuleSet` with no name, deactivating inbound mail account-wide. A
  `count = 0` toggle would have the same hazard, which is why `removed` is the right mechanism.
- The module-versioning section is honest: all 13 modules under `infra/terraform/modules/` are
  `v1.0.0` and none has been bumped, so there is no precedent to appeal to either way.

## What was wrong, and fixed

**1. A command that cannot run.** §5 said to check the Terraform version with
`terragrunt terraform -- version`. Ran it: terragrunt 0.99.1 rejects it —
`unknown command: "terraform"`. Replaced with the fact rather than the errand: Terraform is
pinned to **1.14.3** at `.github/workflows/deploy.yml:120`, `terragrunt-apply.yml:84` and
`terragrunt-plan.yml:81` (local toolchain matches), so `removed` blocks (>=1.7) are available
and the entire older-Terraform fallback branch was moot. `terragrunt run -- version` given as
the correct incantation. Checked §4's `terragrunt state list` separately — `state` *is* a
supported terragrunt shortcut, so that command was left alone.

**2. An imprecise state address.** §7 warned against deleting
`aws_ses_receipt_rule.support`, but that resource is declared in the nested `ses-domain` module
and created with `count`, so its real address is
`module.ses_root["auth.klankermaker.ai"].aws_ses_receipt_rule.support[0]` — a bare `state rm` of
the short name matches nothing. Corrected, with the contrast to §4's unprefixed address made
explicit, since the doc makes a point of state addressing.

**3. The recommendation's blind spot — new §9.** Both §5 (forget the pointer) and §8 (own the
pointer) stop the revert. Neither changes where this project's rules live, which leaves two
live problems the doc did not name:

- *Every rule this repo creates still lands in an inert set.* All six rule attachments bind to
  `aws_ses_receipt_rule_set.main` = `kmv-email`: `ses.tf:26,57,87,118` (the four `module.ses_*`
  blocks, reaching `ses-domain/ses.tf:39`), `forwarding.tf:134`, `receive.tf:14`. The trap is one
  environment variable away — `email.hcl:16-21` creates a forwarding rule as soon as
  `TF_VAR_FWD_EMAIL_TO_ADDRESS` is set, and it would forward nothing while applying cleanly.
  Latent today because `auth.klankermaker.ai` is the only inbound address (`site.hcl:59`), and it
  would fail silently — the same failure mode as the original incident.
- *The rule that actually works is managed by nobody.* `kmv-auth-inbound` was created by hand;
  it is in neither repo's state. If `sandbox-email-shared` is recreated, inbound mail dies and
  no plan in either repo shows drift. `km doctor` checks which set is *active*, not that this
  rule is *in* it.

Added **Option 3** — parameterize the rule set name via a `coalesce`d local, point the unit at
`sandbox-email-shared`, `removed` the activation resource, and `terragrunt import` the hand-made
rule — which closes both. Stated its real cost rather than selling it: the rule-name mismatch
(`auth.klankermaker.ai` vs `kmv-auth-inbound`) must be reconciled or the next plan proposes a
create-and-destroy; it couples this repo to another system's rule set; and `kmv-email` is left as
an empty set. Recommendation kept as §5 now (small, stops the recurring outage), Option 3 as a
scheduled follow-up gated on "before anyone adds another inbound address".

## Integration

- `docs/operators/README.md` — row added to "Also in this directory".
- `scripts/verify-operator-docs.sh` — page added to `DOCS` (link check + leak scan, 10 → 11
  pages), and `infra/terraform/modules/email/v1.0.0/ses.tf` added to the referenced-paths list so
  the doc's central citation is self-verifying.
- **Deliberately not added to `scripts/sync-wiki.py`'s `PAGE_MAP`** — this is a one-time task
  doc, not a standing manual page. That is safe here rather than a repeat of the `../ops/` 404
  problem from 260826-i2c: `rewrite_links()` falls through to an absolute `blob` URL for unmapped
  targets (`sync-wiki.py:94`), and the repo is public. Verified concretely with
  `sync-wiki.py --dry-run` — the built `Operator-Manual.md` line 54 carries a working
  `github.com/.../blob/main/docs/operators/ses-active-rule-set-fix.md` link.

## Verification

- `KV=kv/bin/kv bash scripts/verify-operator-docs.sh` → **exit 0**, zero `FAIL`, zero `SKIP`,
  "checked 11 pages", all six sections pass.
- **Negative-tested the new `DOCS` entry** (the failure mode 260826-ojd had to repair): planted a
  broken relative link in the page → `FAIL: docs/operators/ses-active-rule-set-fix.md ->
  ./no-such-file-xyz.md` and **exit 1**; reverted, `git status --porcelain` clean.
- Leak scan is safe on the new page by construction: it greps `\b[0-9]{10}\b` and the doc has no
  10-digit runs (account `052251888500` is 12 digits and is explicitly excluded at
  `verify-operator-docs.sh:177` regardless).
- Every file:line citation added to §9 was re-grepped against the tree, not carried over from the
  original draft.

## Still open (not this task)

- **The fix itself is unapplied.** `aws_ses_active_receipt_rule_set.main` is still in state
  pointing at `kmv-email`; the next `terraform apply` of the `email` unit still reverts the
  account pointer and breaks klanker-maker's inbound mail. §7's "do not apply the `email` unit
  before the change lands" is live advice right now.
- Option 3 is documented, not scheduled.
- Nothing pushed — both commits are local on `main`.
- AWS SSO token was expired for this session, so no live assertion was made against SES; §6's
  verify command remains unrun since the original hand-application.
