---
phase: 260903-mqd-fix-and-integrate-the-ses-active-receipt
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
  - docs/operators/ses-active-rule-set-fix.md
  - docs/operators/README.md
  - scripts/verify-operator-docs.sh
autonomous: true
requirements: [QUICK-260903-mqd]

must_haves:
  truths:
    - "No command in the doc fails on the installed toolchain: `terragrunt terraform -- version` is gone (terragrunt 0.99.1 rejects it); `terragrunt state list` stays (it IS a supported shortcut)."
    - "The doc states the repo's real Terraform pin (1.14.3) rather than telling the reader to go check, so the `removed`-block prerequisite is settled on the page."
    - "The doc names the two problems the recommended fix does NOT solve: every receipt rule this repo creates still binds to the now-inert kmv-email set, and the hand-made kmv-auth-inbound rule is managed by no Terraform in either repo."
    - "The doc offers a third option (parameterize the rule set name + import the hand-made rule) that closes both, with its real cost stated, and still recommends §5 as the immediate action."
    - "The `aws_ses_receipt_rule.support` state address in the do-not-do list is the real one — nested module plus count index — not the bare resource name."
    - "The doc is reachable from docs/operators/README.md and link-checked by scripts/verify-operator-docs.sh."
    - "scripts/verify-operator-docs.sh still exits 0 on the clean tree with no new FAIL lines."
  artifacts:
    - docs/operators/ses-active-rule-set-fix.md
    - docs/operators/README.md
    - scripts/verify-operator-docs.sh
  key_links:
    - "TF pin verified at .github/workflows/deploy.yml:120, terragrunt-apply.yml:84, terragrunt-plan.yml:81 — all 1.14.3; local terraform is also 1.14.3. `removed` needs >=1.7, so no fallback branch is warranted."
    - "`terragrunt terraform -- version` was RUN and errors: `unknown command: \"terraform\"` on terragrunt 0.99.1. `terragrunt --help` lists `state` as a supported OpenTofu shortcut, so §4's command is fine."
    - "Six rule-attachment references bind to kmv-email: ses.tf:26,57,87,118 (module inputs) + receive.tf:14 + forwarding.tf:134. ses.tf:8 is the activation resource itself."
    - "The forwarding trap is real and reachable: live/site/region/us-east-1/email/email.hcl:16-21 creates a forwarding rule as soon as TF_VAR_FWD_EMAIL_TO_ADDRESS is set."
    - "Only one email unit exists (live/site/region/us-east-1/email); ap-southeast-1 and ca-central-1 have none — so the bare state address in §4 and the single-consumer claim both hold."
    - "sync-wiki.py PAGE_MAP does NOT include this doc, and rewrite_links() falls through to an absolute BLOB link for unmapped targets (scripts/sync-wiki.py:94) — so a README link resolves on the public wiki rather than 404ing. Deliberately NOT added to PAGE_MAP: this is a one-time task doc, not a standing manual page."
    - "The leak scan greps `\\b[0-9]{10}\\b` across DOCS — the doc has no 10-digit runs (account 052251888500 is 12 digits and also explicitly excluded at verify-operator-docs.sh:177), so adding it to DOCS is safe."
---

# Quick Task 260903-mqd: Fix and integrate the SES active-receipt-rule-set operator doc

`docs/operators/ses-active-rule-set-fix.md` landed untracked and unreviewed. Its mechanics are
correct — verified against the repo — but it carries one command that cannot run, states the
Terraform-version prerequisite as an open question when the repo answers it, is imprecise about
one state address, omits the two problems its own recommendation leaves standing, and is wired
into neither the operator-manual index nor the doc verifier.

## Task 1: Correct the doc and add the gap analysis

**Files:** `docs/operators/ses-active-rule-set-fix.md`

**Action:**
1. §5 — replace the "`removed` blocks need Terraform >= 1.7 (`terragrunt terraform -- version`
   to check)" paragraph. State the actual pin (1.14.3, all three workflows) so the prerequisite
   is settled, give `terragrunt run -- version` as the correct incantation, and note that
   §4's `terragrunt state list` is unaffected. Keep the imperative `state rm` path but demote it
   from "for old Terraform" to "if you would rather not add config for a one-time removal".
2. Add a new §9, *What none of these fix — and the option that does*, placed after the §8
   alternative so the three options read in order. Cover: (a) all six rule attachments still
   bind to `kmv-email` with the file:line table and the `TF_VAR_FWD_EMAIL_TO_ADDRESS` trap,
   noting it is latent today because `auth.klankermaker.ai` is the only inbound address;
   (b) `kmv-auth-inbound` is managed by nobody and no plan in either repo will show its loss;
   (c) Option 3 — parameterize the rule set name, `removed` the activation resource, import the
   hand-made rule — with its honest cost (rule-name mismatch, coupling to another system's set,
   `kmv-email` left empty). End with the recommendation: do §5 now, schedule Option 3 before
   anyone adds another inbound address.
3. §7 — correct the `aws_ses_receipt_rule.support` reference to
   `module.ses_root["auth.klankermaker.ai"].aws_ses_receipt_rule.support[0]` and say why a bare
   `state rm` of that name matches nothing.
4. Renumber the trailing "Context worth having" section 9 → 10 and fix the §9 cross-reference
   inside the new section.

**Verify:** `grep -c 'terragrunt terraform --' docs/operators/ses-active-rule-set-fix.md` → 0.
Every file:line citation in the new section re-checked against the tree with grep.

**Done:** The doc contains no command that fails on the installed toolchain, and a reader who
applies §5 knows exactly what remains unfixed.

## Task 2: Wire the doc into the manual and the verifier

**Files:** `docs/operators/README.md`, `scripts/verify-operator-docs.sh`

**Action:**
1. `README.md` — add a row to the "Also in this directory" table.
2. `verify-operator-docs.sh` — add the doc to the `DOCS` array (link-check + leak scan), and add
   `infra/terraform/modules/email/v1.0.0/ses.tf` to the "Referenced repo paths exist" list so the
   doc's central citation is self-verifying.

**Verify:** `KV=kv/bin/kv bash scripts/verify-operator-docs.sh` — exit 0, zero FAIL lines,
"checked 11 pages".

**Done:** The doc is discoverable from the manual index and drift in it fails a check rather
than confusing an operator.
