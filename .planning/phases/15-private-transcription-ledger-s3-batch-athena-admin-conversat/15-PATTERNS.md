# Phase 15: Private transcription ledger — Pattern Map

**Mapped:** 2026-07-12
**Files analyzed:** 22 new/modified files
**Analogs found:** 19 / 22 (3 have no codebase analog — see "No Analog Found")

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `apps/voice/src/klanker_voice/ledger.py` (NEW) | service | batch / file-I/O | `quota.py` + `session.py` + `observers.py` | role-match (composite) |
| `apps/voice/src/klanker_voice/call_runtime.py` (MOD) | service wiring | event-driven | itself (`transport.event_handler`, `run()` finally) | exact |
| `apps/voice/src/klanker_voice/auth.py` (MOD) | middleware | request-response | itself (namespaced-claim read → `SessionIdentity`) | exact |
| `apps/voice/src/klanker_voice/server.py` (MOD) | controller | request-response | `telephony/__main__.py` finally + `session.py` release funnel | role-match |
| `apps/voice/src/klanker_voice/telephony/controller.py` (MOD) | service | event-driven | itself (`_mint_tier_from_caller_id`) | exact |
| `apps/voice/src/klanker_voice/telephony/__main__.py` (MOD) | entrypoint | event-driven | itself (`finally: await ari.close()`) | exact |
| `apps/voice/tests/test_ledger.py` (NEW) | test | — | `tests/test_session.py` (`fake_aws` fixture) | exact |
| `apps/voice/tests/test_call_runtime.py` (MOD) | test | — | itself | exact |
| `apps/voice/tests/test_auth.py` (MOD) | test | — | itself | exact |
| `apps/auth/webapp/src/config/index.ts` (MOD) | config | — | itself (`claimNames` block, lines 125-128) | exact |
| `apps/auth/webapp/src/config/oidc.ts` (MOD) | service | request-response | itself (`extraTokenClaims`, lines 388-396) | exact |
| `apps/auth/webapp/src/config/login-intent-bridge.ts` (MOD) | service | CRUD | itself (`setActiveTier` call, line 42) | exact |
| `apps/auth/webapp/src/entities/auth-profile.ts` (MOD) | model | CRUD | itself (`activeTierId` attribute, lines 95-105) | exact |
| `apps/auth/webapp/src/lib/ledger.ts` (NEW) | service | file-I/O read | `entities/client.ts` (AWS client + explicit cred chain) | role-match |
| `apps/auth/webapp/src/app/admin/layout.tsx` (NEW) | layout/guard | request-response | `app/(authlogin)/layout.tsx` + `config/auth.ts` `auth` export | partial |
| `apps/auth/webapp/src/app/admin/transcripts/page.tsx` (NEW) | server component | file-I/O read | none (see No Analog) | — |
| `apps/auth/webapp/src/app/admin/transcripts/[sessionId]/page.tsx` (NEW) | server component | file-I/O read | none (see No Analog) | — |
| webapp admin tests (NEW) | test | — | `app/tel/__tests__/tel-route.test.ts` | role-match |
| `apps/voice/client/src/screens/ReadyToStart.tsx` (MOD) | component | — | itself (`.ready-cta-sub` line) | exact |
| `infra/terraform/modules/ledger/v1.0.0/` + `config.hcl` (NEW) | infra module | — | `modules/cloudfront-assets/v1.0.0/main.tf` + `modules/dynamodb/config.hcl` | role-match |
| `infra/terraform/live/site/region/us-east-1/ledger/terragrunt.hcl` (NEW) | infra unit | — | `region/us-east-1/secrets/terragrunt.hcl` | exact |
| `infra/terraform/live/site/services/{voice,auth}/service.hcl` (MOD) | infra config | — | themselves (IAM statements + `secrets.valueFrom`) | exact |

---

## Pattern Assignments

### `apps/voice/src/klanker_voice/ledger.py` (NEW — service, batch file-I/O)

Composite analog: module shape from `quota.py`, async task/teardown discipline from `session.py`, error posture from `observers.py`.

**Module header + env-var constants pattern** (`quota.py:32-52`) — docstring explaining the design decision, `from __future__ import annotations`, module-level env-var-name constants with defaults:
```python
from __future__ import annotations

import os
import time
import uuid
from dataclasses import dataclass
from datetime import datetime, timezone
from decimal import Decimal
from typing import TYPE_CHECKING, Any

import boto3
from boto3.dynamodb.conditions import Key
from botocore.exceptions import ClientError

if TYPE_CHECKING:
    from klanker_voice.auth import SessionIdentity

#: Voice service's own usage table (Phase 4). Least-privilege task-role IAM
#: grants only GetItem/PutItem/UpdateItem/Query on this table.
USAGE_TABLE_ENV_VAR = "KMV_USAGE_TABLE"
DEFAULT_USAGE_TABLE = "kmv-voice-usage"
```
Ledger equivalents: `LEDGER_BUCKET_ENV_VAR = "KMV_LEDGER_BUCKET"`, `LEDGER_SALT_ENV_VAR = "KMV_LEDGER_SALT"`, resolved via small `_bucket()`-style getters (mirrors `quota.py:_usage_table_name` / `auth.py:71-81` `_issuer()/_audience()`).

**UTC date helpers — copy verbatim** (`quota.py:173-178`; Pitfall 7 in RESEARCH):
```python
def _today() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%d")

def _now_epoch() -> int:
    return int(time.time())
```

**Sync boto3 called via `asyncio.to_thread` — the repo-wide AWS rule** (`session.py:14-18` docstring, applied at `session.py:175,268-272,374-389`):
```python
# session.py module docstring:
# Every AWS call in this module (CloudWatch, ECS, and — via
# :mod:`klanker_voice.quota` — DynamoDB) runs off the asyncio event loop via
# ``asyncio.to_thread``, so a slow API call never blocks the other concurrent
# sessions this task is also running.

# call shape (session.py:268):
await asyncio.to_thread(quota.release_heartbeat, self.user_id, self.session_id)
```
The `LedgerWriter.flush()` S3 `put_object` MUST follow this shape (sync `_put` method run in `to_thread`) — do NOT introduce aioboto3.

**Periodic asyncio timer task pattern** (`session.py:361-368` `_tick_loop`, started at `session.py:203-205`, cancelled in `release()` at `session.py:263-265`):
```python
async def _tick_loop(self) -> None:
    interval = self.quota_config.heartbeat_renew_interval
    try:
        while True:
            await asyncio.sleep(interval)
            await self._tick()
    except asyncio.CancelledError:
        raise
# started:   self._tick_task = asyncio.create_task(self._tick_loop())
# cancelled: for task in (...): task.cancel()
```
The writer's ~120s flush timer copies this exactly (create in an explicit `start()`/first-append, cancel in `close()`).

**Idempotent close guard** (`session.py:247-274` `release()` — synchronous check-and-set, no await between):
```python
if self._stopped:
    return
self._stopped = True
for task in (self._tick_task, ...):
    if task is not None:
        task.cancel()
```
`LedgerWriter.close()` must use the same `_closed` guard so racing teardown layers flush exactly once.

**Log-and-continue error posture** (`observers.py:278-283` `_write_artifact` — never let ledger I/O take down a live conversation):
```python
def _write_artifact(self) -> None:
    try:
        self._report.write(self._artifact_path)
    except OSError as e:
        # Never let artifact I/O take down a live conversation.
        logger.error(f"LatencyReportObserver: failed to write artifact: {e}")
```
Also `session.py:417-418` (`except Exception as exc:  # never let metric publish failure break a session`). Flush failures: log, re-buffer the batch (bounded), continue.

**code_hash** — stdlib HMAC, mirroring `auth.py:95-104`'s constant-time env-credential handling:
```python
# auth.py:101-104 — env-read + guard shape to mirror:
configured = os.environ.get(SMOKE_TOKEN_ENV_VAR, "")
if not configured or not token:
    return False
return hmac.compare_digest(configured, token)
```
Ledger version (RESEARCH Code Examples): `hmac.new(salt.encode(), code.strip().lower().encode(), hashlib.sha256).hexdigest()`, returning `None` when salt or code is missing. Never log the raw code; anon subs (`anon:<code>:<uuid>`) must never be written verbatim into records.

---

### `apps/voice/src/klanker_voice/call_runtime.py` (MOD — the ONE tap seam)

**Analog: itself.** All three entry paths (webrtc voice1, voice2 duplex, PSTN) construct sessions exclusively through `create_call_session()` (`call_runtime.py:131-244`).

**Event-handler registration pattern already used in this exact function** (`call_runtime.py:230-236`):
```python
@transport.event_handler("on_client_disconnected")
async def _on_client_disconnected(transport, client):  # noqa: ANN001 — pipecat handler shape
    await lifecycle.on_transport_disconnected()
```
The ledger tap registers the same way on the aggregator halves `build_pipeline()` returns (`built.user_aggregator` / `built.assistant_aggregator`, `pipeline.py:56-57,136-140`):
```python
@built.user_aggregator.event_handler("on_user_turn_message_added")
async def _ledger_user(_agg, message):
    await writer.append(role="user", text=message.content)

@built.assistant_aggregator.event_handler("on_assistant_turn_stopped")
async def _ledger_assistant(_agg, message):
    if message.content:
        await writer.append(role="assistant", text=message.content,
                            interrupted=message.interrupted)
```

**Final-flush hook — `CallSession.run()`'s existing `finally` bracket** (`call_runtime.py:110-121`):
```python
async def run(self) -> None:
    await self.lifecycle.start()
    try:
        await self.runner.run()
    finally:
        await self.lifecycle.stop()
```
Add `await writer.close()` inside this `finally` (after `lifecycle.stop()`). It runs on success, error, AND cancellation — same guarantee the heartbeat release relies on. `CallSession` dataclass gains a `writer` field (additive, like `context` at line 108).

**Bypass/smoke skip** — construct the writer disabled when `gate_result.bypass_accounting` is true (same flag `SessionLifecycle` receives at `call_runtime.py:174-180`); enable it at telephony unlock alongside `lifecycle.upgrade_from_bypass` (`session.py:207-245` — the writer's enable/identity-upgrade should ride the same seam).

**Additive-defaulted-field convention for `CallIdentity`** (`call_runtime.py:63-87` — Phase 12 precedent):
```python
@dataclass(frozen=True)
class CallIdentity:
    subject: str
    authenticated: bool = False
    auth_method: str = "webrtc-oidc"
    tier_id: str | None = None
    caller_id: str | None = None
    did: str | None = None
```
New identity inputs (email, raw code / mint-sub) follow this exact additive-defaulted style so every existing construction site stays byte-unchanged.

---

### `apps/voice/src/klanker_voice/auth.py` (MOD — read new claims)

**Analog: itself.** Namespaced-claim constants (`auth.py:40-42`) and claim extraction (`auth.py:152-155`):
```python
#: Namespaced claims from the Phase-3 contract (03-03-SUMMARY.md).
TIER_ID_CLAIM = "https://klankermaker.ai/tier_id"
GROUP_CLAIM = "https://klankermaker.ai/group"
# ...
sub = str(claims.get("sub") or "")
tier_id = str(claims.get(TIER_ID_CLAIM) or NO_ACCESS_TIER_ID)
group = claims.get(GROUP_CLAIM)
return SessionIdentity(sub=sub, tier_id=tier_id, group=group, bypass_accounting=False)
```
Add `EMAIL_CLAIM = "https://klankermaker.ai/email"` and `CODE_CLAIM = "https://klankermaker.ai/code"` (names must match the auth app's `config.oidc.claimNames` byte-for-byte — same contract discipline as the pinned tier/group pair). Extend the frozen `SessionIdentity` dataclass (`auth.py:61-68`) with `email: str | None = None`, `code: str | None = None` — additive defaults keep every constructor call unchanged (smoke path at `auth.py:129-134` needs no edits).

---

### `apps/voice/src/klanker_voice/server.py` (MOD — shutdown drain)

**Analog: `session.py` release funnel + `telephony/__main__.py` finally.** `server.py` has no lifespan/shutdown handler today (verified in RESEARCH). The drain follows the `finally`-bracketing convention (`telephony/__main__.py:117-120`):
```python
await ari.connect()
try:
    await ari.run()
finally:
    await ari.close()
```
Add a FastAPI lifespan (or `app.add_event_handler("shutdown", ...)`) that cancels live runners (driving each `CallSession.run()` `finally` → final flush) and awaits a module-level `ledger.flush_all(timeout≈10s)` — bounded well under ECS's default 30s SIGTERM→SIGKILL window. `telephony/__main__.py` gets the equivalent in its existing `finally` (line 119).

---

### `apps/voice/src/klanker_voice/telephony/controller.py` (MOD — return mint-sub for code_hash)

**Analog: itself.** `_mint_tier_from_caller_id` (`controller.py:542-572`) currently validates the mint token and returns only `identity.tier_id`:
```python
try:
    identity = validate_access_token(token)
except AuthError:
    logger.warning("tel mint: minted token failed validation")
    return None
return identity.tier_id
```
Change: return `(identity.tier_id, identity.sub)` (the sub is `anon:<code>:<uuid>` — `lib/bypass-token.ts:92`) so `ledger.resolve_code()` can extract the code for hashing. Preserve the fail-closed contract: every failure path still returns `None`(-tuple), never raises, never logs the token. Thread `caller_id`/`did` from the existing `CallIdentity` (`call_runtime.py:85-86`) into the record's `caller_id`/`did` columns; `email` stays null for PSTN.

---

### `apps/voice/tests/test_ledger.py` (NEW) + test extensions

**Analog: `tests/test_session.py` — the `fake_aws` monkeypatch fixture** (`test_session.py:88-99`):
```python
@pytest.fixture
def fake_aws(monkeypatch):
    """Fake boto3.client(...) for cloudwatch/ecs; records every call."""
    clients: dict[str, _FakeAwsClient] = {}

    def _client(name, *args, **kwargs):
        clients.setdefault(name, _FakeAwsClient())
        return clients[name]

    monkeypatch.setattr(session.boto3, "client", _client)
    monkeypatch.setattr(session, "_task_metadata_ids", lambda: ("test-cluster", "test-task-123"))
    return clients
```
Copy this shape, patching `ledger.boto3` to a recording fake `s3` client. The `_FakeAwsClient.__getattr__` recorder (`test_session.py:60-64`) captures `put_object` kwargs for assertions on key naming (`ledger/dt=.../<HHMMSSZ>-<session_id>-<n>.jsonl`) and body content. Env isolation via autouse `monkeypatch.setenv` fixtures (`test_session.py:67-78`). Tests to include per RESEARCH Validation: monotonic `turn_seq` across both roles, flush triggers (timer/N/close), put failure keeps buffer, double-fire dedupe (Pitfall 1), salt/hash stability + null-salt → null hash, bypass sessions skipped.

Webapp claim tests extend the existing vitest pattern (see admin tests below); `test_auth.py` already monkeypatches `auth._jwk_client` for offline validation (`auth.py:83-92` docstring documents this seam).

---

### `apps/auth/webapp/src/config/index.ts` + `oidc.ts` (MOD — email/code claims)

**Analog: themselves.** Claim-name registry (`config/index.ts:125-128`):
```typescript
claimNames: {
  tierId: "https://klankermaker.ai/tier_id",
  group: "https://klankermaker.ai/group",
},
```
Add `email` / `code` entries here (single source of truth; `bypass-token.ts:82-85` mints from the same registry).

`extraTokenClaims` (`oidc.ts:388-396`) — the profile is ALREADY fetched here; the change is two added lines:
```typescript
extraTokenClaims: async (ctx, token) => {
  if (token.kind !== "AccessToken" || !token.accountId) return {};

  const profile = await getAuthProfile(token.accountId);
  return {
    [config.oidc.claimNames.tierId]: profile?.activeTierId ?? "no-access",
    [config.oidc.claimNames.group]: profile?.activeGroup ?? null,
    // NEW: [config.oidc.claimNames.email]: profile?.email ?? null,
    // NEW: [config.oidc.claimNames.code]: profile?.activeCode ?? null,
  };
},
```

---

### `apps/auth/webapp/src/entities/auth-profile.ts` + `login-intent-bridge.ts` (MOD — stamp activeCode)

**Analog: the existing `activeTierId` bridge, end to end.** Attribute shape (`auth-profile.ts:95-105`):
```typescript
// Access-code -> tier bridge (Phase 3 Plan 02, D-04/D-05/D-07).
// Stamped by the LoginIntent bridge on every login; NOT permanent —
// latest-wins, see class doc above.
activeTierId: {
  type: "string",
  default: "no-access",
},
activeGroup: {
  type: "string",
},
```
Add `activeCode: { type: "string" }` beside them (latest-wins, same D-05 semantics). Stamp helper (`auth-profile.ts:187-195`):
```typescript
export async function setActiveTier(userId, tierId, group) {
  await AuthProfile.patch({ userId })
    .set({ activeTierId: tierId, activeGroup: group ?? undefined })
    .go();
}
```
Extend (or sibling) to also `.set({ activeCode: code ?? undefined })`. Call site — the bridge already holds `intent.code` (`login-intent-bridge.ts:42-58`):
```typescript
await setActiveTier(userId, intent.tierId, intent.group);
// intent.code is right here (used for CodeRedemption.create at line 51) —
// pass it into the same stamp.
```

---

### `apps/auth/webapp/src/lib/ledger.ts` (NEW — S3 read helper for the report)

**Analog: `entities/client.ts` — including its load-bearing credentials gotcha** (`client.ts:1-31`):
```typescript
import { DynamoDB } from "@aws-sdk/client-dynamodb";
import { fromNodeProviderChain } from "@aws-sdk/credential-providers";

// In prod (ECS Fargate) no static keys are supplied — resolve credentials via an
// EXPLICIT provider chain so the ECS task-role (container credentials) provider is
// used. This static import is required: Next.js `output: 'standalone'` bundling drops
// the SDK's default (dynamically-required) provider chain, so without it the task-role
// creds never resolve ("Resolved credential object is not valid"). Locally, explicit
// AUTH_*_ID/SECRET (e.g. "local"/"local" against dynamodb-local) still win.
const dynamodbCreds =
  process.env.AUTH_DYNAMODB_ID && process.env.AUTH_DYNAMODB_SECRET
    ? { credentials: { accessKeyId: ..., secretAccessKey: ... } }
    : { credentials: fromNodeProviderChain() };
```
The new S3 client MUST replicate this: `new S3Client({ region: process.env.AWS_REGION, credentials: fromNodeProviderChain() })` with an env-override escape hatch for local testing, plus an exported env-derived bucket constant mirroring `client.ts:69-70`:
```typescript
export const ELECTRO_TABLE = process.env.AUTH_ELECTRO_DBNAME || "kmv-auth-electro";
// ledger equivalent: export const LEDGER_BUCKET = process.env.LEDGER_BUCKET || "";
```
Install `@aws-sdk/client-s3` pinned to the same `^3.x` line as the existing `@aws-sdk/client-dynamodb` in `package.json`.

---

### `apps/auth/webapp/src/app/admin/layout.tsx` (NEW — ADMIN_EMAILS gate)

**Partial analog: `(authlogin)/layout.tsx` for route-group layout structure** (`app/(authlogin)/layout.tsx:36-78` — full `<html>/<body>` shell, `Providers` + `SessionProvider basePath`, env-driven version metadata). Note its basePath convention (`layout.tsx:12-14`):
```typescript
const isDev = process.env.NODE_ENV !== "production";
const REGION_SHORT = process.env.REGION_SHORT || "use1";
const authBasePath = isDev ? "/api/auth" : `/${REGION_SHORT}/api/auth`;
```
**Server-side session read:** no server component in the app calls it yet, but `config/auth.ts:120` already exports the v5 helper:
```typescript
export const { handlers, signIn, signOut, auth } = NextAuth({
```
Gate shape (per approved spec `docs/superpowers/specs/2026-07-06-admin-panel-design.md`: `ADMIN_EMAILS` allowlist, non-admins get **404** not 403):
```typescript
import { auth } from "@/config/auth";
import { notFound } from "next/navigation";

const admins = (process.env.ADMIN_EMAILS ?? "").split(",").map(e => e.trim().toLowerCase()).filter(Boolean);
const session = await auth();
if (!session?.user?.email || !admins.includes(session.user.email.toLowerCase())) notFound();
```
Scope discipline: gate + layout + transcripts report ONLY — users/codes/kill-switch panels remain Phase 05.1.

---

### `apps/auth/webapp/src/app/admin/transcripts/*.tsx` (NEW — session list + chat view)

No true analog (see below). Consume `lib/ledger.ts`: `ListObjectsV2` over `ledger/dt=<day>/` for the list page (keys encode `<HHMMSSZ>-<session_id>-<n>`, so sessions derive from keys alone); `GetObject` + line-parse + filter `session_id` + **sort by `turn_seq`** (never `ts` — LOCKED) for the detail page; alternating user/assistant bubbles. React auto-escaping only — never `dangerouslySetInnerHTML` for transcript text (stored-XSS-via-speech threat, RESEARCH Security).

**Webapp test analog: `app/tel/__tests__/tel-route.test.ts`** — env-injection before dynamic import (`tel-route.test.ts:15,42-56`):
```typescript
let GET: typeof import("../[e164]/route").GET;

beforeAll(async () => {
  process.env.AUTH_ELECTRO_ENDPOINT = "http://localhost:8888";
  // ... env setup BEFORE the module under test is imported ...
  ({ GET } = await import("../[e164]/route"));
});
```
Admin tests set `ADMIN_EMAILS` + a mocked S3 client the same way (env first, then `await import`).

---

### `apps/voice/client/src/screens/ReadyToStart.tsx` (MOD — recording notice)

**Analog: itself** — the existing sub-line under the CTA (`ReadyToStart.tsx:17-20`):
```tsx
<div className="ready-cta-wrap">
  <button type="button" className="ready-cta" onClick={onStart}>Let's start talking</button>
  <p className="ready-cta-sub">This taps the mic awake and pages KPH. Ready when you are.</p>
</div>
```
Add the "sessions may be recorded" line as a sibling small-print element (new class in `readyToStart.css`, matching the `.ready-cta-sub` styling convention). Same treatment on `LandBounce.tsx` if the planner covers both pre-connect screens. Test extends `ReadyToStart.test.tsx` (existing screen-test pattern; note repo gotcha — client tests need node ≥22.12, `nvm use 23`).

---

### `infra/terraform/modules/ledger/v1.0.0/` (NEW module)

**S3 analog: `modules/cloudfront-assets/v1.0.0/main.tf`** — private SSE bucket + PAB + lifecycle, exactly the resource set needed (`main.tf:47-99`):
```hcl
resource "aws_s3_bucket_server_side_encryption_configuration" "cf_assets_encryption" {
  bucket = aws_s3_bucket.cf_assets[each.key].id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_public_access_block" "cf_assets_public_access_block" {
  bucket = aws_s3_bucket.cf_assets[each.key].id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_lifecycle_configuration" "cf_assets_lifecycle" {
  rule {
    id     = "delete-old-versions"
    status = "Enabled"
    ...
  }
}
```
Bucket naming convention (`main.tf:18-22`): `"${var.site.label}-<purpose>-${var.region.label}-${random_id.rnd.hex}"` with the standard `merge(var.tags, {...})` tag block. Module file split: `main.tf` / `variables.tf` / `outputs.tf` / `ssm.tf` (cloudfront-assets exports bucket id/arn to SSM parameters in `ssm.tf` — do the same so `service.hcl` env vars and operator tooling can resolve the bucket name).

**Module `config.hcl` analog: `modules/dynamodb/config.hcl` (complete file, 14 lines):**
```hcl
locals {
  site_vars     = read_terragrunt_config(find_in_parent_folders("site.hcl"))
  region_vars   = read_terragrunt_config(find_in_parent_folders("region.hcl"))
  dynamodb_vars = read_terragrunt_config("dynamodb.hcl")

  module_path = "${find_in_parent_folders("modules/")}/dynamodb"

  merged_inputs = merge(
    local.site_vars.locals,
    local.region_vars.locals,
    local.dynamodb_vars.locals,
    {}
  )
}
```
Athena/Glue resources (`aws_athena_workgroup`, `aws_glue_catalog_database`, `aws_glue_catalog_table` with the partition-projection `parameters` map) have NO repo analog — take the DDL verbatim from RESEARCH Pattern 5.

### `infra/terraform/live/site/region/us-east-1/ledger/terragrunt.hcl` (NEW unit)

**Exact analog: `region/us-east-1/secrets/terragrunt.hcl` (complete file):**
```hcl
include "skip" {
  path   = "${find_in_parent_folders("region")}/skip.hcl"
  expose = true
}

locals {
  site_vars = read_terragrunt_config(find_in_parent_folders("site.hcl"))
}

exclude {
  if      = !local.site_vars.locals.secrets.enabled || include.skip.locals.should_skip
  actions = ["all"]
}

include "module" {
  path   = "${find_in_parent_folders("modules")}/secrets/config.hcl"
  expose = true
}

include "providers" {
  path = "${find_in_parent_folders("providers")}/regional.hcl"
}

terraform {
  source = "${include.module.locals.module_path}/v1.0.0"
}

inputs = include.module.locals.merged_inputs
```
Copy wholesale, s/secrets/ledger/, with a matching `ledger.enabled` toggle in `site.hcl` (follow how `secrets.enabled` is declared there).

### `services/voice/service.hcl` + `services/auth/service.hcl` (MOD — IAM, secret, env)

**Analog: their own existing statements.** Least-privilege IAM statement shape (`services/voice/service.hcl:45-53`):
```hcl
{
  sid     = "UsageTableCrud"
  actions = ["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:UpdateItem", "dynamodb:Query"]
  resources = [
    "arn:aws:dynamodb:*:*:table/kmv-voice-usage",
    "arn:aws:dynamodb:*:*:table/kmv-voice-usage/index/*"
  ]
},
```
Voice addition: `sid = "LedgerPutOnly"`, `actions = ["s3:PutObject"]`, `resources = ["arn:aws:s3:::<bucket>/ledger/*"]` — write-only, no read/list. Auth addition mirrors the read-only cross-service grant precedent (`services/auth/service.hcl:70-77` `VoiceUsageRead`): `s3:ListBucket` (bucket arn, prefix-conditioned) + `s3:GetObject` (`.../ledger/*`).

Container secret injection (`services/voice/service.hcl:132-149`):
```hcl
secrets = [
  {
    name      = "DEEPGRAM_API_KEY"
    valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/deepgram/api_key"
  },
  ...
]
```
Add `KMV_LEDGER_SALT` ← `.../kmv/secrets/use1/ledger/code_hash_salt` here, plus a plain `environment` entry for the bucket name (pattern at `service.hcl:119-124`). Salt value flows SOPS (`live/site/.secrets.sops.json`) → secrets module → SSM SecureString per `SECRETS.md`. The auth container gets `ADMIN_EMAILS` + `LEDGER_BUCKET` the same way (env or SSM per its existing container block).

Deploy note (RESEARCH Pitfall 8): org SCP `DenyInfraAndStorage` will likely block CI apply of the bucket/IAM — plan a local operator-SSO `terragrunt apply` with the CI plan as review artifact, as in Phase 12-07.

---

## Shared Patterns

### AWS calls from asyncio (voice service)
**Source:** `apps/voice/src/klanker_voice/session.py:14-18` (rule), `:268-272` (shape)
**Apply to:** `ledger.py` flush, any new AWS touchpoint in voice
```python
await asyncio.to_thread(<sync_boto3_fn>, *args)
```

### Never break a live conversation (error posture)
**Source:** `observers.py:278-283`, `session.py:417-418,432-434`
**Apply to:** every ledger write path, metric-style side effects
```python
except Exception as exc:  # never let <side-effect> failure break a session
    logger.warning(f"...: {exc}")
```

### Idempotent teardown funnel
**Source:** `session.py:247-274` (`release()` sync check-and-set), `call_runtime.py:110-121` (`finally` bracket)
**Apply to:** `LedgerWriter.close()`, server lifespan drain, telephony `finally`

### Namespaced claim contract (byte-for-byte across services)
**Source:** `config/index.ts:125-128` ↔ `auth.py:40-42` (pinned pair precedent)
**Apply to:** new email/code claims — declare once in `claimNames`, mirror constants in `auth.py`, cover with tests on both sides (vitest `oidc-resource-token.test.ts` + pytest `test_auth.py` already test the existing pair)

### Explicit AWS credential chain in the webapp
**Source:** `entities/client.ts:8-22`
**Apply to:** the new S3 client — `fromNodeProviderChain()` statically imported (Next standalone bundling drops the default chain)

### Secrets: SOPS → SSM SecureString → container `valueFrom`
**Source:** `services/voice/service.hcl:132-149`, `infra/terraform/live/site/SECRETS.md`
**Apply to:** `KMV_LEDGER_SALT` (path `/kmv/secrets/use1/ledger/code_hash_salt`)

### UTC-everywhere date handling
**Source:** `quota.py:173-178`
**Apply to:** `dt=` partition keys, `ts` epoch field (Pitfall 7)

### No transcript text in logs
**Source:** discipline established at `auth.py:14-16` (never log token bodies) and RESEARCH Security ("CloudWatch as accidental second ledger")
**Apply to:** all ledger code — log counts and keys, never `text` values; never log raw codes or anon subs

## No Analog Found

Files/resources with no close match — planner should use RESEARCH.md patterns directly:

| File | Role | Data Flow | Reason |
|------|------|-----------|--------|
| `app/admin/transcripts/page.tsx` + `[sessionId]/page.tsx` | server component | S3 read | The webapp has zero data-fetching server components (all pages are `useSession` client components); RESEARCH Pattern 6 is the spec |
| `aws_glue_catalog_{database,table}` + `aws_athena_workgroup` in the ledger module | infra | — | No Athena/Glue anywhere in `infra/terraform/modules/`; use the partition-projection DDL from RESEARCH Pattern 5 verbatim |
| Pipecat aggregator event payload handling (`UserTurnMessageAddedMessage` / `AssistantTurnStoppedMessage`) | event contract | event-driven | Not used in repo yet; verified against installed pipecat 1.5.0 source (`llm_response_universal.py:655,846,1438,2069`) — registration API matches `observers.py:170-172` `add_event_handler` |

## Metadata

**Analog search scope:** `apps/voice/src/klanker_voice/` (+ `telephony/`, `tests/`), `apps/voice/client/src/screens/`, `apps/auth/webapp/src/` (`app/`, `config/`, `entities/`, `lib/`, tests), `infra/terraform/` (`modules/`, `live/site/services/`, `live/site/region/us-east-1/`)
**Files read:** 20 (targeted, non-overlapping ranges for files >200 lines)
**Pattern extraction date:** 2026-07-12
