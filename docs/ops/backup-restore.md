# kv backup / kv restore runbook (Phase 16, §4)

`kv backup` writes one verified, self-contained artifact of everything that
exists only in AWS — the three DynamoDB tables and the full S3 transcript
ledger. `kv restore` is the other half of that contract: it reads the
artifact back into a live stack. Both commands are built for the **destroy**
case, not just a pause (D-02) — a backup that only had to survive a
scale-to-zero would quietly fail on the one day it actually matters.

This runbook covers the artifact layout, the required rebuild ordering, why
destinations are resolved live instead of trusted from the archive, which
rows are filtered on restore and why, and the dry-run workflow.

## The artifact

`kv backup` writes `kmv-backup-<ISO8601>.zip` to `./backups` (override with
`--out`) with this layout:

```
manifest.json                    # D-05: git SHA, AWS account id, region, kv
                                  # version, resolved table/bucket names,
                                  # row/object/byte counts, a SHA-256 per file
dynamodb/auth-electro.jsonl       # kmv-auth-electro, one JSON line per item
dynamodb/auth-authjs.jsonl        # kmv-auth-authjs,  one JSON line per item
dynamodb/voice-usage.jsonl        # kmv-voice-usage,  one JSON line per item
ledger/<key>                      # every S3 ledger object, key preserved
external/voipms-dids.json         # DID inventory (advisory; degrades on failure)
external/nat-eip.txt              # the NAT EIP at backup time
external/ssm-params.json          # /kmv/ parameter inventory — SecureString
                                   # values are NEVER captured (D-14)
```

**The zip is unencrypted by design.** It contains personal data — full
conversation transcripts and user email addresses — and it may be the only
remaining copy of that data once an account is destroyed. `kv backup`
deliberately does **not** encrypt it with the account's own
secrets-encryption key: that key would be gone at exactly the moment the
backup matters (the day after `kv destroy`). Store the finished zip
somewhere safe and treat it as sensitive. A future encryption scheme must
use a key held **outside** this AWS account (age or GPG) — see the design
spec's §10 open items.

`kv backup` verifies its own output by default: it re-opens the finished
zip and re-checks every SHA-256 digest and row count against `manifest.json`
before reporting success (`--no-verify` opts out). "Backup succeeded" means
the artifact was read back, not merely written.

## The required rebuild ordering (D-13)

`kv restore` assumes the target DynamoDB tables and the S3 ledger bucket
**already exist** — it creates no infrastructure. Rebuilding a destroyed
stack from a backup is always three steps, in this order:

1. **config (git)** — the terraform/terragrunt config for the site is
   already in this repo; nothing to do here except make sure you're on the
   right ref.
2. **`terragrunt apply`** — create the DynamoDB tables, the S3 ledger bucket,
   and everything else the site needs. Run this before `kv restore`, not
   after — a missing table or bucket is a hard error at restore time, naming
   the resource and the fact that `terragrunt apply` is the step that
   creates it.
3. **`kv restore <zip>`** — restore the DynamoDB rows and the ledger objects
   into the freshly-applied stack.

`kv restore` never runs `terragrunt apply` itself and never creates a table
or a bucket on your behalf. If a destination is missing, fix step 2 and
re-run step 3 — restore is idempotent and safe to re-run (see below).

## Destinations are resolved live, never from the manifest (D-10)

Every write destination — the three table names and the ledger bucket name —
is resolved from **current terraform outputs** at restore time
(`ResolveLiveTargets`), never from `manifest.json`. This matters concretely:
the S3 ledger bucket module names its bucket with a `random_id` suffix, so a
bucket created by `terragrunt apply` after a `kv destroy` gets a **new**
name that does not match whatever `manifest.json` recorded when the backup
was originally written. If restore trusted the manifest's recorded bucket
name, it would either fail outright (bucket doesn't exist) or, worse,
silently write into some other bucket that happens to share the old name.

`kv restore`'s printed report shows both: the manifest's recorded names
(labelled **PROVENANCE** — audit-only, where the backup came from) and the
live-resolved names (labelled **DESTINATIONS** — where this restore actually
wrote). If the two diverge, you'll see it, not just have to trust it.

## Rows filtered by default (D-11, `--skip-ephemeral`)

`kv restore` drops several row classes by default — restoring them would
either be pointless (they'll be regenerated on first use) or actively
harmful:

- **Concurrency leases** (`kmv-voice-usage`, `UsageHeartbeat` items) — the
  live quota gate's active-session slot. Restoring a stale **concurrency
  lease** would wedge the quota gate for a real user waiting on a session
  slot that DynamoDB's TTL would otherwise have already reclaimed — a bug
  this project has already debugged once in production.
- **OIDC session and interaction state** (`kmv-auth-electro`, oidc-provider's
  `Session`/`Interaction` models) — short-lived login-flow state that must
  never outlive the login it was minted for.
- **Login intents** (`kmv-auth-electro`, `LoginIntent` items) — the
  ~15-minute email→tier bridge used between "enter an access code" and
  "click the magic link."
- **next-auth sessions and verification tokens** (`kmv-auth-authjs`) —
  session cookies and single-use magic-link tokens.
- **Any item whose TTL attribute has already expired** — independent of key
  shape, on every table.

Access codes, tiers, auth profiles, code redemptions, users, and accounts
are never filtered — they are durable records, not ephemeral state, even if
they happen to carry their own (unrelated) expiry field.

Pass `--skip-ephemeral=false` to restore everything, including the rows
above — useful for a full forensic restore, but not the default for
rebuilding a live stack.

## Idempotent, resumable writes (D-12)

Restore writes are batched (`BatchWriteItem`, 25 items per call) with
`UnprocessedItems` retried and exponential-backoff retry on throttling. A
full-item `PutRequest` is inherently idempotent, so re-running `kv restore`
over a partially-completed restore converges — it never duplicates a row and
never errors on a row that's already there.

## Dry run

`kv restore <zip> --dry-run` reports exactly what would happen — per-table
write counts, per-table skipped-row counts broken out by reason (e.g. how
many concurrency leases were dropped), and the ledger object count — without
issuing a single `PutItem`, `BatchWriteItem`, or `PutObject` call. Run this
first after any `terragrunt apply` you're not 100% sure about.

```
kv restore ./backups/kmv-backup-20260820T030000Z.zip --dry-run
```

## Selective restore

`--tables` and `--ledger` restore only that half of the artifact. With
neither flag given, a bare `kv restore <zip>` restores both — the common
case for a full rebuild.
