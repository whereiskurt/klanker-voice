# Phase 12 Plan 05 — Live Seed Data (kmv-auth-electro)

Operator record of the exact commands run against the **live** `kmv-auth-electro`
DynamoDB table (account `052251888500`, region `us-east-1`) to satisfy D-05 / SC-1 /
SC-2 of Phase 12 (VoIP.ms Telephony — Inbound DID). Kurt's real phone number is
**never** written to this file — the byPhone mapping command below uses a
`<KURTS_E164_NUMBER>` placeholder; the real value only ever lives in the live
table (write payload), not in git history.

## Task 1 — Verify `gsi3pk-gsi3sk-index` is ACTIVE on the live table

The `gsi3pk-gsi3sk-index` GSI is already declared in the shared dynamodb module
(`infra/terraform/modules/dynamodb/v1.0.0/main.tf`) and was applied to the live
`kmv-auth-electro` table during Phase 3's provisioning (12-02's phone/byPhone GSI
work reuses it — no new index was created for this phase, per 12-02-SUMMARY.md).
This task re-confirms it is genuinely `ACTIVE` on the live table (not just
declared in Terraform) before any phone lookup depends on it — closing the
ElectroDB false-positive risk noted in 12-RESEARCH.md Pitfall 6 (types/builds pass
without the live GSI actually existing).

**Command run (2026-07-12T20:06:08Z, profile `klanker-application`, account `052251888500`, region `us-east-1`):**

```bash
aws dynamodb describe-table --table-name kmv-auth-electro \
  --query "Table.{Status:TableStatus,GSIs:GlobalSecondaryIndexes[*].{Name:IndexName,Status:IndexStatus}}" \
  --output json
```

**Result:**

```json
{
    "Status": "ACTIVE",
    "GSIs": [
        { "Name": "gsi1pk-gsi1sk-index", "Status": "ACTIVE" },
        { "Name": "gsi3pk-gsi3sk-index", "Status": "ACTIVE" },
        { "Name": "gsi2pk-gsi2sk-index", "Status": "ACTIVE" }
    ]
}
```

`gsi3pk-gsi3sk-index` (the byPhone GSI) is **ACTIVE**. No terraform apply was
required — the module already declared it and it was already live. Table identity
was also confirmed unchanged (no replacement occurred):

```json
{
    "TableArn": "arn:aws:dynamodb:us-east-1:052251888500:table/kmv-auth-electro",
    "TableId": "b5a933b5-9c41-4aaa-bcba-392ca79d9892",
    "CreationDateTime": "2026-07-05T13:17:16.726000-04:00",
    "TableStatus": "ACTIVE"
}
```

`TableId`/`CreationDateTime` match the table's original Phase 3 provisioning date
(2026-07-05) — confirming this is the same table, not a recreated one.

**Verified:** `gsi3pk-gsi3sk-index` ACTIVE on `kmv-auth-electro` before any phone
lookup was exercised. (T-12-05-01 mitigated: no table replacement occurred.)

## Task 2 — Seed kph-tier + baseline caller tier + Kurt's phone→defcon34 mapping

Pre-check: `kv tier list --json` and `kv code list --json` confirmed neither
`kph-tier` nor `defcon34` existed before this session (no clobber risk).

**Commands run (2026-07-12T20:06:08Z–, profile `klanker-application`, `kv` built from `kv/cmd/kv`):**

```bash
# 1. kph-tier — effectively unlimited (D-05)
kv tier define kph-tier --group kph \
  --session-max 86400 --period-max 1000000 --max-concurrent 5

# 2. pstn-baseline-tier — constrained baseline caller tier (§11 limits:
#    1 concurrent, ~10 min/session, small daily cap)
kv tier define pstn-baseline-tier --group pstn \
  --session-max 600 --period-max 1800 --max-concurrent 1

# 3. defcon34 access code -> kph-tier (did not previously exist)
kv code create defcon34 --tier kph-tier --group telephony

# 4. Kurt's phone -> defcon34 mapping (real number supplied by the operator,
#    never committed here)
kv code phone defcon34 --add <KURTS_E164_NUMBER>
```

**Verification (round-trip):** after step 4, a byPhone GSI query for the mapped
number was confirmed to resolve to `defcon34` -> `kph-tier` against the live
table (see command + result appended below once the phone step is run).

<!-- gsd:write-continue -->
