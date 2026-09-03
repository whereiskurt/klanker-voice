# Stop this project from stealing the SES active receipt rule set

**One-time infrastructure fix.** Written 2026-09-03 for someone with no prior context.

**Account:** `052251888500`, region `us-east-1`.

---

## 1. What is wrong

AWS SES allows **exactly one active receipt rule set per account per region**. Only that one
set is evaluated for inbound mail; every other rule set in the account is inert.

The `email` unit here manages `aws_ses_active_receipt_rule_set.main`, which points that
account-wide slot at this project's own rule set (`kmv-email`). A sibling system sharing the
account — `klanker-maker`, resource prefix `km` — puts its inbound rules in a different set,
`sandbox-email-shared`.

While `kmv-email` held the slot:

- mail to `sandboxes.klankermaker.ai` matched no rule and was **silently dropped**
- sending was unaffected, because outbound SES never consults receipt rules

It therefore looked like a broken mail *reader* on the other system for days, when it was
actually a routing problem one layer up. Nothing in either project detected it.

## 2. What has already been done

Applied by hand via the AWS API on 2026-09-03. **No Terraform in either project has been
changed**, so this state is currently undefended:

1. This project's rule was **copied** (not moved) into `sandbox-email-shared` as
   `kmv-auth-inbound`, behaviourally identical:

   | field | value |
   |---|---|
   | Recipients | `auth.klankermaker.ai` |
   | Action | S3 → `ses-inbox-kmv-use1-6e913c73`, prefix `inbox/auth.klankermaker.ai/` |
   | TlsPolicy | `Optional` |
   | ScanEnabled | `true` |

2. The account's active rule set was switched to `sandbox-email-shared`, which now holds:

   ```
   km-operator-inbound   operator-km@sandboxes.klankermaker.ai → km-artifacts-12345/mail/create/km/
   km-sandbox-catchall   sandboxes.klankermaker.ai            → km-artifacts-12345/mail/km/
   kmv-auth-inbound      auth.klankermaker.ai                 → ses-inbox-kmv-use1-6e913c73/inbox/…
   ```

Both systems now receive; verified end to end with a probe that reached S3 in 22 seconds.
The original `auth.klankermaker.ai` rule **still exists inside `kmv-email`, untouched**, so
this project's state remains accurate about it. `kmv-email` is simply no longer the active set.

## 3. Why it still needs a fix

`aws_ses_active_receipt_rule_set.main` is still in this project's state pointing at
`kmv-email`. **The next `terraform apply` of the `email` unit detects that as drift and
switches the account back**, silently breaking the sibling system's inbound mail again.

Making that stop is the whole task.

## 4. Where the code is

`infra/terraform/modules/email/v1.0.0/ses.tf`, lines 7–9:

```hcl
resource "aws_ses_active_receipt_rule_set" "main" {
  rule_set_name = aws_ses_receipt_rule_set.main.rule_set_name
}
```

Consumed by `infra/terraform/live/site/region/us-east-1/email/terragrunt.hcl`:

```hcl
terraform {
  source = "${include.module.locals.module_path}/v1.0.0"
}
```

Because terragrunt points `terraform { source }` straight at the module, **that module is the
root module of the unit** — so the state address is the bare
`aws_ses_active_receipt_rule_set.main`, with no `module.` prefix. Confirm before editing:

```bash
cd infra/terraform/live/site/region/us-east-1/email
terragrunt state list | grep active_receipt
```

## 5. The change

In `infra/terraform/modules/email/v1.0.0/ses.tf`:

1. **Delete** the `resource "aws_ses_active_receipt_rule_set" "main"` block above.
2. **Add** a `removed` block so Terraform forgets it without touching AWS:

```hcl
removed {
  from = aws_ses_active_receipt_rule_set.main

  lifecycle {
    destroy = false
  }
}
```

Both edits are required — a `removed` block and a live `resource` block for the same address
is a configuration error.

> **`lifecycle { destroy = false }` is the load-bearing line.** Without it Terraform destroys
> the resource, and destroying *this* resource type means **deactivating the account's active
> rule set entirely** — every inbound message in the account, for both systems, dropped.

`removed` blocks need Terraform >= 1.7. This repo pins **1.14.3** — `.github/workflows/deploy.yml:120`,
`terragrunt-apply.yml:84`, `terragrunt-plan.yml:81` — so the block is available and there is no
older-Terraform case to handle. If you want to confirm what your own shell has, it is
`terragrunt run -- version`; terragrunt 0.99 no longer forwards a bare `terraform` subcommand.
(`terragrunt state list` in §4 is unaffected — `state` is one of its supported shortcuts.)

The imperative equivalent, if you would rather not add configuration for a one-time removal:

```bash
terragrunt state rm aws_ses_active_receipt_rule_set.main
```

followed by deleting the resource block. Same end state — but it leaves nothing in the code
explaining why the resource went away, which is the reason the `removed` block is preferred.

### Expected plan

```
Plan: 0 to add, 0 to change, 0 to destroy.
  # aws_ses_active_receipt_rule_set.main will no longer be managed by Terraform,
  # but will not be destroyed
```

**If the plan shows anything being destroyed, stop** — `destroy = false` is missing or
misplaced.

### A note on module versioning

Every module in `infra/terraform/modules/` is at `v1.0.0`, and none has ever been bumped, so
there is no established precedent either way. Editing `v1.0.0` in place is the smaller change
and the `email` unit is its only consumer. If you would rather treat versioned module
directories as immutable, create `v1.1.0` with the edit and bump the unit's `source` pin —
functionally identical, just more ceremony. Pick one; the fix itself is the same.

## 6. Verify after applying

```bash
aws ses describe-active-receipt-rule-set --region us-east-1 \
  --query '{Active:Metadata.Name,Rules:Rules[].Name}'
```

Must still report:

```json
{
  "Active": "sandbox-email-shared",
  "Rules": ["km-operator-inbound", "km-sandbox-catchall", "kmv-auth-inbound"]
}
```

Then confirm this project's own mail still lands — new objects should keep appearing under
`s3://ses-inbox-kmv-use1-6e913c73/inbox/auth.klankermaker.ai/`. The destination bucket and key
prefix are unchanged, so nothing downstream of the bucket needs touching.

## 7. Do not do these

- **Do not delete `kmv-email` or the receipt rule inside it.** Both are inert but still in state
  and still accurate; removing them is a destructive change to this project's own configuration
  for no benefit. Note the rule's real state address is
  `module.ses_root["auth.klankermaker.ai"].aws_ses_receipt_rule.support[0]` — it is declared in
  the nested `ses-domain` module and created with `count`, so a bare
  `state rm aws_ses_receipt_rule.support` matches nothing. Unlike the activation resource in §4,
  this one *is* module-prefixed.
- **Do not run `aws ses set-active-receipt-rule-set --rule-set-name kmv-email`** to "restore"
  anything. That is the exact action this task exists to prevent.
- **Do not apply the `email` unit before the change lands.** The apply reverts the pointer
  first; the fix has to go in ahead of it.

## 8. Alternative, if you would rather own the pointer explicitly

Instead of forgetting the resource, keep it and point it at the shared set:

```hcl
resource "aws_ses_active_receipt_rule_set" "main" {
  rule_set_name = "sandbox-email-shared"
}
```

This is also stable — the sibling system deliberately does **not** manage the activation
pointer, so there is no contention in either direction — and it has the advantage of keeping
the desired state declarative rather than relying on nobody touching it.

The trade-offs: it hard-codes a name owned by another system, and it replaces the current
reference to `aws_ses_receipt_rule_set.main.rule_set_name` with a literal, so the resource no
longer follows this project's own rule set. Section 5 is the recommendation; this is a
legitimate alternative if you would rather something actively assert the correct value.

## 9. What none of these fix — and the option that does

Sections 5 and 8 both stop the pointer from flipping back. Neither changes **where this
project's receipt rules live**, and that leaves two problems standing.

### Every rule this repo creates still lands in an inert rule set

All six rule attachments in the module bind to `aws_ses_receipt_rule_set.main` — that is
`kmv-email`, the set that is no longer active:

| resource | file |
|---|---|
| `aws_ses_receipt_rule.support`, via the four `module.ses_*` blocks | `modules/email/v1.0.0/ses.tf:26,57,87,118` → `ses-domain/ses.tf:39` |
| `aws_ses_receipt_rule.forwarding` | `modules/email/v1.0.0/forwarding.tf:134` |
| `aws_ses_receipt_rule.receive` | `modules/email/v1.0.0/receive.tf:14` |

The concrete trap is one environment variable away.
`live/site/region/us-east-1/email/email.hcl:16-21` adds a forwarding rule the moment
`TF_VAR_FWD_EMAIL_TO_ADDRESS` is set. Set it and Terraform creates the rule, reports a clean
apply, and forwards **nothing** — the rule sits in a set SES never evaluates. The same holds for
any new `receive_rules` entry and any new name added to `email.zonenames`.

This is latent today, not broken: `auth.klankermaker.ai` is the only inbound address this
project has (`live/site/site.hcl:59`), and the hand-made copy in `sandbox-email-shared` covers
it. It becomes real the first time someone adds mail handling here — and it will fail silently,
which is exactly how the original incident went undetected for days.

### The rule that is actually working is managed by nobody

`kmv-auth-inbound` was created by hand (§2). It is in this project's Terraform state nowhere,
and in klanker-maker's nowhere. If `sandbox-email-shared` is ever recreated, this project's
inbound mail disappears and **neither repo's plan shows drift** — neither knows the rule exists.
The `km doctor` check in §10 asserts which rule set is *active*; it does not assert that
`kmv-auth-inbound` is *in* it.

### Option 3: put this project's rules in the active set

Make the rule set name an input rather than a hard-wired local resource. In
`modules/email/v1.0.0`:

```hcl
# variables.tf
variable "receipt_rule_set_name" {
  description = "Rule set the receipt rules attach to. Null = this site's own set."
  type        = string
  default     = null
}

# ses.tf
locals {
  receipt_rule_set_name = coalesce(
    var.receipt_rule_set_name,
    aws_ses_receipt_rule_set.main.rule_set_name,
  )
}
```

Replace all six references above with `local.receipt_rule_set_name`, set
`receipt_rule_set_name = "sandbox-email-shared"` in the `email` unit's inputs, apply the §5
`removed` block to the activation resource, and import the hand-made rule so Terraform owns it
again:

```bash
terragrunt import 'module.ses_root["auth.klankermaker.ai"].aws_ses_receipt_rule.support[0]' \
  'sandbox-email-shared:kmv-auth-inbound'
```

Both problems above go away: new rules land where SES will read them, and the working rule is
managed.

**The honest cost.** This is more than a two-line edit:

- The module names the rule from `receipt_rule_config.rule_name`, which is
  `auth.klankermaker.ai` — the hand-made rule is called `kmv-auth-inbound`. The import needs
  either the rule renamed in AWS first or the config adjusted to match the existing name; they
  must agree or the next plan proposes a create-and-destroy.
- It makes this repo write into a rule set another system owns. That coupling is worth being
  deliberate about, and it should be noted on the klanker-maker side too.
- `aws_ses_receipt_rule_set.main` still creates `kmv-email`, which becomes a permanently empty
  set. Harmless, but it should either be removed in the same change or commented as vestigial.

**Recommendation:** do §5 now. It is small, it stops the recurring outage, and it is the urgent
part. Treat Option 3 as the scheduled follow-up — specifically, do it **before** anyone adds
another inbound address or sets `TF_VAR_FWD_EMAIL_TO_ADDRESS` — and record it as a known gap
until then.

## 10. Context worth having

The sibling system now ships a `km doctor` check that fails loudly when the active rule set is
not `sandbox-email-shared`, naming whichever set displaced it. A regression here is therefore
detectable — but only by someone who runs that command, and only after inbound mail has
already been silently dropped for however long. That detection is a backstop, not a substitute
for this fix.
