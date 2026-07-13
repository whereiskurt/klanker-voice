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
#    never committed here — <ADMIN_PHONE_E164> is a placeholder)
kv code phone defcon34 --add <ADMIN_PHONE_E164>
```

All four commands were run successfully against the live table on 2026-07-12
(profile `klanker-application`). Post-write state confirmed via `kv tier list`
and `kv code list`:

| Item | Live values |
|------|-------------|
| `kph-tier` | `sessionMaxSeconds=86400`, `periodMaxSeconds=1000000`, `maxConcurrent=5`, `group=kph` |
| `pstn-baseline-tier` | `sessionMaxSeconds=600`, `periodMaxSeconds=1800`, `maxConcurrent=1`, `group=pstn` |
| `defcon34` | access code, `tierId=kph-tier`, `group=telephony` (created fresh — did not previously exist, no clobber) |

**Round-trip verification (byPhone GSI, run against the live table):**

```bash
aws dynamodb query --table-name kmv-auth-electro \
  --index-name gsi3pk-gsi3sk-index \
  --key-condition-expression "gsi3pk = :pk AND gsi3sk = :sk" \
  --expression-attribute-values '{":pk":{"S":"phone#<ADMIN_PHONE_E164>"},":sk":{"S":"phone#"}}' \
  --query "Items[*].{code:code.S,tierId:tierId.S,phoneEnabled:phoneEnabled.BOOL}" --output json
```

Result (real number redacted):

```json
[ { "code": "defcon34", "tierId": "kph-tier", "phoneEnabled": true } ]
```

The mapped number resolves through the **live** `gsi3pk-gsi3sk-index` GSI to
`defcon34` -> `kph-tier` with `phoneEnabled=true` — the exact lookup the 12-02
`resolvePhoneToCode()` / `GET /tel/<e164>` mint path performs. The plan-level
tier verify (`aws dynamodb get-item` on `tier#kph-tier`) also passed.

## Admin phone number in SSM — operator-only, bot-unreadable

The admin phone number is additionally stored in ONE SSM SecureString parameter
so operators can retrieve it without it ever living in git:

| Parameter | Type | Scope |
|-----------|------|-------|
| `/kmv/operators/use1/admin_phone` | SecureString (Version 1) | **Operator-only** |

```bash
# Created via (real value redacted):
aws ssm put-parameter --name "/kmv/operators/use1/admin_phone" \
  --type SecureString --value "<ADMIN_PHONE_E164>" \
  --description "Operator-only: admin phone E.164. NEVER add to any container valueFrom or task-role SSM grant (incl. 12-07 telephony edge)." \
  --tags Key=Scope,Value=operator-only Key=Phase,Value=12-05
```

### Why this path, and the access-isolation proof (checked 2026-07-12)

The path `/kmv/operators/use1/` is deliberately **disjoint** from
`/kmv/secrets/use1/*` — the prefix every container secret in this project is
wired from (`infra/terraform/live/site/services/{auth,voice}/service.hcl`
`secrets[].valueFrom` entries all point under `/kmv/secrets/use1/`).

Access check performed against BOTH the IaC and the live account:

1. **Dedicated task roles (what running bot code uses):**
   `auth-use1-kmv-task-role` and `voice-use1-kmv-task-role` — the task roles of
   the ONLY two ACTIVE task definitions (`auth-use1-kmv`, `voice-use1-kmv`) —
   carry **zero `ssm:*` actions** (their `task_role_iam_statements` in
   service.hcl grant only DynamoDB/SES/ECS/EC2/CloudWatch). The running
   voice/auth/telephony bot code **cannot read any SSM parameter at all**,
   including this one. Verified live via `aws iam get-role-policy` and
   `aws ecs describe-task-definition`.
2. **Execution roles:** the ecs-task module
   (`infra/terraform/modules/ecs-task/v1.0.0/main.tf`, `ssm_access` policy)
   grants `ssm:GetParameter*` on `parameter/*` to each task's **execution
   role** — but the execution role is only exercised by the ECS agent to
   inject `secrets[].valueFrom` entries at container start; it is not
   assumable by code inside the container. The guard here is therefore:
   **this parameter must never appear in any container's `valueFrom` list.**
3. **Shared cluster task role (hazard, currently unused):**
   `ecs-task-role-app-use1-kmv-6e913c73` (ecs-cluster module) grants a
   wide-open `ssm:*` on `Resource=*`. **No active task definition uses it**
   today — but any future task created WITHOUT dedicated
   `task_role_policy_statements` falls back to it and could read this
   parameter.

### Hard constraints (apply to all future work, especially 12-07)

- **NEVER** add `/kmv/operators/use1/admin_phone` (or anything under
  `/kmv/operators/`) to any container's `secrets[].valueFrom` list.
- **NEVER** grant any task role `ssm:*`/`ssm:GetParameter*` on
  `/kmv/operators/*`.
- The upcoming **telephony-edge service (12-07)** MUST use a dedicated
  least-privilege task role (non-empty `task_role_policy_statements`, like
  auth/voice) — never the shared cluster role — and its SSM/secret grants must
  stay under `/kmv/secrets/use1/*`, never touching `/kmv/operators/*`.
- The real phone number exists in exactly two places: the live
  `kmv-auth-electro` item (`defcon34`'s `phone` attribute / byPhone GSI keys)
  and this SSM parameter. It is never committed to git — this doc and all
  history use the `<ADMIN_PHONE_E164>` placeholder.
