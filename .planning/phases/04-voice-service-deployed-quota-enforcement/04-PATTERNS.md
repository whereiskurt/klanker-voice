# Phase 4: Voice Service Deployed & Quota Enforcement - Pattern Map

**Mapped:** 2026-07-05
**Files analyzed:** 11 new/modified files
**Analogs found:** 8 / 11 (72% coverage)

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `apps/voice/bot.py` | controller | request-response | `apps/voice/bot.py` (existing) | exact |
| `apps/voice/quota.py` | service | CRUD + request-response | `kv/internal/app/cmd/tier.go` | role-match |
| `apps/voice/pyproject.toml` | config | N/A | `apps/voice/pyproject.toml` (existing) | exact |
| `apps/voice/pipeline.toml` | config | N/A | `apps/voice/pipeline.toml` (existing) | exact |
| `apps/voice/tests/test_smoke.py` | test | request-response | `apps/voice/tests/test_smoke.py` (existing) | exact |
| `apps/auth/webapp/src/entities/usage.ts` | model | CRUD | `apps/auth/webapp/src/entities/tier.ts` | role-match |
| `kv/internal/app/cmd/usage.go` | CLI command | CRUD + request-response | `kv/internal/app/cmd/tier.go` | exact |
| `kv/internal/app/cmd/killswitch.go` | CLI command | CRUD + request-response | `kv/internal/app/cmd/code.go` | role-match |
| `kv/internal/app/cmd/smoke.go` | CLI command | request-response | `kv/internal/app/cmd/code.go` | role-match |
| `infra/.../services/voice/service.hcl` | config (infrastructure) | N/A | `infra/.../services/auth/service.hcl` | exact |
| `infra/.../network/securitygroups.tf` | infrastructure | N/A | `infra/terraform/modules/network/v1.0.0/securitygroups.tf` | exact |

---

## Pattern Assignments

### `apps/voice/bot.py` (controller, request-response)

**Analog:** `apps/voice/bot.py` (lines 1-54)

**Current structure** (lines 1-54):
```python
"""klanker-voice runner entrypoint (D-08 web verification surface)."""

from dotenv import load_dotenv
from pipecat.runner.types import RunnerArguments
from klanker_voice.config import load_config
from klanker_voice.observers import LatencyReportObserver
from klanker_voice.pipeline import build_pipeline, build_worker, register_greet_first

load_dotenv(override=True)

async def bot(runner_args: RunnerArguments):
    """Per-session bot entry point, discovered and invoked by the pipecat runner."""
    transport = await create_transport(runner_args, transport_params)
    cfg = load_config()  # KLANKER_PIPELINE_CONFIG-aware
    built = build_pipeline(cfg, transport)
    worker = build_worker(built.pipeline, observers=[LatencyReportObserver(cfg)])
    register_greet_first(transport, worker, built.context)
    runner = WorkerRunner(handle_sigint=runner_args.handle_sigint)
    await runner.add_workers(worker)
    await runner.run()
```

**Modifications needed:** Add quota start-gate check, service-timer callback registration, and CloudWatch metric emission to the `bot()` function. The pattern is to:
1. Extract the JWT token from transport/request context at `/api/offer` time
2. Validate token via PyJWT/PyJWKClient (offline JWKS validation)
3. Query DynamoDB for tier limits and current usage
4. Enforce start-gate and acquire heartbeat slot
5. Register in-memory timers for session wall-clock and 15s usage tick
6. Emit session-count metric to CloudWatch

---

### `apps/voice/quota.py` (service, CRUD + request-response) — NEW

**Analog:** `kv/internal/app/cmd/tier.go` (CRUD pattern, lines 18-44)

**DynamoDB operations pattern** (from tier.go lines 31-44):
```go
// DefineTier writes (creates or replaces) a Tier item via PutItem
func DefineTier(ctx context.Context, client *dynamodb.Client, table, tierID, group string, sessionMaxSecs, periodMaxSecs, maxConcurrent int64) error {
	if err := validateCodeCharset(tierID); err != nil {
		return fmt.Errorf("invalid tier id: %w", err)
	}
	item := electro.NewTierItem(tierID, group, sessionMaxSecs, periodMaxSecs, maxConcurrent)
	_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(table),
		Item:      item.Marshal(),
	})
	if err != nil {
		return fmt.Errorf("put tier %q: %w", tierID, err)
	}
	return nil
}
```

**Error handling & validation pattern** (from config.py lines 99-128):
```python
# Recursively reject credential fields in config
def _reject_credential_fields(data: object, path: str = "") -> None:
    if isinstance(data, dict):
        for key, value in data.items():
            key_path = f"{path}.{key}" if path else key
            if _CREDENTIAL_FIELD_RE.search(str(key)):
                raise ConfigError(
                    f"pipeline.toml field '{key_path}' looks like credential material. "
                    "Secrets never live in TOML — API keys come from .env via env vars."
                )
            _reject_credential_fields(value, key_path)

def _require_range(name: str, value: float, lo: float, hi: float) -> float:
    value = float(value)
    if not (lo <= value <= hi):
        raise ConfigError(f"{name} must be within {lo}-{hi}, got {value}")
    return value
```

**Expected structure in `apps/voice/quota.py`:**
- Session heartbeat acquisition (conditional write to DynamoDB usage table)
- 15s tick for usage persistence and heartbeat renewal (conditional write pattern from CLAUDE.md D-02, D-03)
- Service-timer callbacks for warning-at-30s and goodbye-at-0
- Idle teardown layer checks (transport disconnect, VAD timeout, pipeline error)
- Kill-switch read at `/api/offer` start-gate
- Auto-trip logic when daily usage crosses configured ceiling
- Typed rejection errors (no-access, concurrency-limit, daily-limit, site-paused)

---

### `apps/auth/webapp/src/entities/usage.ts` (model, CRUD) — NEW

**Analog:** `apps/auth/webapp/src/entities/tier.ts` (lines 1-69)

**ElectroDB entity pattern** (tier.ts lines 1-69):
```typescript
import { Entity } from "electrodb";
import { electroClient, ELECTRO_TABLE } from "./client";

/**
 * Tier Entity — Session/period/concurrency limits.
 * Fields: tier_id, session_max_seconds, period_max_seconds, max_concurrent.
 * FINAL key templates: primary pk: "tier#${tierId}" sk: "tier#"
 *                     gsi1 pk: "tiers#" sk: "tier#${tierId}"
 */
export const Tier = new Entity(
  {
    model: {
      entity: "Tier",
      version: "1",
      service: "kmv",
    },
    attributes: {
      tierId: { type: "string", required: true, set: (val) => String(val ?? "").trim().toLowerCase() },
      group: { type: "string" },
      sessionMaxSeconds: { type: "number" },
      periodMaxSeconds: { type: "number" },
      maxConcurrent: { type: "number" },
      createdAt: { type: "number", default: () => Date.now(), readOnly: true },
    },
    indexes: {
      primary: {
        pk: { field: "pk", composite: ["tierId"], template: "tier#${tierId}" },
        sk: { field: "sk", composite: [], template: "tier#" },
      },
      all: {
        index: "gsi1pk-gsi1sk-index",
        pk: { field: "gsi1pk", composite: [], template: "tiers#" },
        sk: { field: "gsi1sk", composite: ["tierId"], template: "tier#${tierId}" },
      },
    },
  },
  { client: electroClient, table: ELECTRO_TABLE }
);
```

**Usage entity key templates** (per design spec §6 and CONTEXT.md D-01/D-02):
- Primary: `user_id × yyyy-mm-dd` / `seconds_used` (daily per-user usage)
- Heartbeat lease: `user_id × session_id` with ~45s TTL for concurrency slot
- Global rollup: single item keyed to today's date for site-wide daily total (sessions, seconds, est. cost)
- Control/kill-switch: single item for start-gate read (flipped by `kv killswitch`)

---

### `kv/internal/app/cmd/usage.go` (CLI command, CRUD + request-response) — NEW

**Analog:** `kv/internal/app/cmd/tier.go` (lines 18-152)

**Command structure pattern** (tier.go lines 79-136):
```go
// NewTierCmd builds the "kv tier" parent command with define/list subcommands
func NewTierCmd(cfg *Config) *cobra.Command {
	tierCmd := &cobra.Command{
		Use:   "tier",
		Short: "Manage tiers (session/period/concurrency limits)",
	}

	var (
		group         string
		sessionMax    int64
		periodMax     int64
		maxConcurrent int64
	)

	define := &cobra.Command{
		Use:   "define <tierId>",
		Short: "Define (create or replace) a tier",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			client, err := cfg.DynamoClient(c.Context())
			if err != nil {
				return err
			}
			if err := DefineTier(c.Context(), client, cfg.Table, args[0], group, sessionMax, periodMax, maxConcurrent); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "defined tier %q (session-max=%ds period-max=%ds max-concurrent=%d)\n",
				electro.NormalizeTierID(args[0]), sessionMax, periodMax, maxConcurrent)
			return nil
		},
	}
	define.Flags().StringVar(&group, "group", "", "optional group label")
	define.Flags().Int64Var(&sessionMax, "session-max", 0, "max seconds per session (required)")
	tierCmd.AddCommand(define)

	list := &cobra.Command{
		Use:   "list",
		Short: "List tiers",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			client, err := cfg.DynamoClient(c.Context())
			if err != nil {
				return err
			}
			records, err := ListTiers(c.Context(), client, cfg.Table)
			if err != nil {
				return err
			}
			return printTiers(c, records, asJSON)
		},
	}
	tierCmd.AddCommand(list)
	return tierCmd
}
```

**`kv usage` subcommands expected:**
- `kv usage today [--user-id=X]` — read today's usage item or global rollup
- `kv usage history <user-id> <date-range>` — query daily usage history per user
- Output format: tabwriter (text) or JSON via `--json` flag

---

### `kv/internal/app/cmd/killswitch.go` (CLI command, CRUD + request-response) — NEW

**Analog:** `kv/internal/app/cmd/code.go` (lines 1-100, UpdateItem pattern for soft-expire)

**UpdateItem pattern with conditional write** (code.go lines 105-126):
```go
// ExpireAccessCode soft-expires a code by setting expiresAt to now via UpdateItem
func ExpireAccessCode(ctx context.Context, client *dynamodb.Client, table, code string) error {
	if err := validateCodeCharset(code); err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(table),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: electro.AccessCodePK(code)},
			"sk": &types.AttributeValueMemberS{Value: electro.AccessCodeSK()},
		},
		UpdateExpression: aws.String("SET expiresAt = :now"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":now": &types.AttributeValueMemberN{Value: strconv.FormatInt(now, 10)},
		},
		ConditionExpression: aws.String("attribute_exists(pk)"),
	})
	if err != nil {
		return fmt.Errorf("expire code %q: %w", code, err)
	}
	return nil
}
```

**`kv killswitch` subcommands expected:**
- `kv killswitch status` — read current kill-switch state (on/off) and auto-trip ceiling config
- `kv killswitch on` — manually engage kill-switch (operator override)
- `kv killswitch off` — manually disengage kill-switch
- Conditional write: only allow flip if existing state differs (idempotent)

---

### `kv/internal/app/cmd/smoke.go` (CLI command, request-response) — NEW

**Analog:** `kv/internal/app/cmd/tier.go` (lines 79-85, command structure)

**Expected pattern:** Full WebRTC offer→ICE→RTP flow
- Construct a synthetic WebRTC SDP offer (use pipecat client-js SDK or aiortc)
- POST to `/api/offer` with smoke-test service credential (bypasses quota accounting)
- Negotiate ICE to `connected` state (verify public IP candidate + STUN srflx both present)
- Confirm RTP frames flow to the public-IP task (parse received RTP packets)
- Graceful teardown and session cleanup
- Output: success/failure + media flow confirmation

---

### `infra/.../services/voice/service.hcl` (config, infrastructure) — MODIFIED

**Analog:** `infra/terraform/live/site/services/auth/service.hcl` (lines 1-116)

**Table definition pattern** (auth/service.hcl lines 25-48):
```hcl
  dynamodb = {
    tables = [
      {
        table_name = "kmv-auth-authjs"
        table_type = "nextauth"
        replica_regions = [
          {
            label = "use1"
            full  = "us-east-1"
          }
        ]
      },
      {
        table_name = "kmv-auth-electro"
        table_type = "electro"
        replica_regions = [
          {
            label = "use1"
            full  = "us-east-1"
          }
        ]
      }
    ]
  }
```

**Voice service modifications:**
- Add `usage` table to `dynamodb.tables` array in `infra/terraform/live/site/services/voice/service.hcl`
- Table schema: `electro` type (inherits gsi1, gsi2, gsi3 indexes for multi-access-pattern queries)
- Table name: `kmv-voice-electro` or similar (follows naming convention)
- TTL enabled on expiration column for heartbeat lease auto-cleanup

**ECS service modifications** (voice/service.hcl lines 59-91):
```hcl
  service = {
    name          = "voice"
    regions       = ["us-east-1"]
    cluster_name  = "app"
    task_family   = "voice"
    desired_count = 1

    # WebRTC groundwork: media flows UDP direct browser<->task, so the task
    # runs in public subnets with a public IP (signaling stays on the ALB).
    assign_public_ip = true
    
    autoscaling = {
      enabled      = false
      min_capacity = 1
      max_capacity = 4  # D-13: scale 1→4 on session-count metric
    }
  }
```

---

### `infra/terraform/modules/network/v1.0.0/securitygroups.tf` (infrastructure, MODIFIED)

**Analog:** `infra/terraform/modules/network/v1.0.0/securitygroups.tf` (lines 1-56, security group pattern)

**SG ingress rule pattern** (securitygroups.tf lines 11-22):
```hcl
  ingress = [
    {
      description      = "HTTPS port to VPC"
      from_port        = 443
      to_port          = 443
      protocol         = "tcp"
      cidr_blocks      = []
      ipv6_cidr_blocks = []
      self             = true
      prefix_list_ids  = [data.aws_ec2_managed_prefix_list.cloudfront.id]
      security_groups  = []
    },
    // ... more rules
  ]
```

**New UDP ingress rule for WebRTC media** (D-12):
- Add ingress rule: UDP port range 20000–20100 from 0.0.0.0/0 (public, on voice tasks)
- This allows browser→task direct RTP media flow
- Pair with `assign_public_ip = true` in ECS service definition

**Application autoscaling policy** (new, D-13):
- Register voice ECS service as Application Auto Scaling target
- Metric: custom CloudWatch metric `active_sessions` per task
- Target tracking: maintain 1–2 sessions per task on average
- Scale out: add task when avg sessions/task > target
- Scale in protection: task blocks scale-in while `active_sessions >= 1`

---

### `apps/voice/tests/test_smoke.py` (test, request-response) — MODIFIED

**Analog:** `apps/voice/tests/test_smoke.py` (current lines 1-12)

**Current structure** (lines 1-12):
```python
"""Install smoke: the pinned pipecat 1.5.x tree and the klanker_voice package import."""

def test_pipecat_version_is_pinned_line():
    import pipecat
    assert pipecat.__version__.startswith("1.5.")

def test_klanker_voice_package_importable():
    import klanker_voice  # noqa: F401
```

**Extension for KV-05 smoke test:**
- Add `test_webrtc_offer_iceflow()` — synthetic SDP offer negotiation with `/api/offer`
- Verify ICE candidates include public IP + STUN srflx pair
- Confirm RTP frames reach the public-IP task (packet inspection)
- Use service credential that bypasses quota (smoke-test IAM policy)
- Output: detailed report on media flow success/failure

---

## Shared Patterns

### Token Validation at `/api/offer`
**Apply to:** `apps/voice/bot.py`, `apps/voice/quota.py`

**PyJWT/PyJWKClient pattern** (from CLAUDE.md technology stack):
```python
from jwt import PyJWKClient
import jwt

# Load JWKS from auth.klankermaker.ai/.well-known/openid-configuration
signing_key = PyJWKClient(
    uri=f"https://auth.klankermaker.ai/api/oidc/jwks",
    cache_keys=True,
    max_cached_keys=10
).get_signing_key(token_header["kid"])

# Decode + validate token claims (offline, no round-trip)
decoded = jwt.decode(
    token,
    signing_key.key,
    algorithms=["RS256"],
    audience="voice.*",  # Audience check for voice resource
    issuer="https://auth.klankermaker.ai",  # Issuer check
    options={"verify_exp": True}
)
# decoded contains: tier_id, group, sub (user ID), exp, iat
```

### DynamoDB Conditional Write Pattern
**Apply to:** Heartbeat acquisition, usage tick, kill-switch flip, slot release in `apps/voice/quota.py`

**Pattern** (from kv code.go, CONTEXT.md D-01/D-03):
```python
# Using boto3 (AWS SDK for Python)
response = dynamodb_client.update_item(
    TableName="kmv-voice-usage",
    Key={
        "pk": {"S": f"session#{user_id}#{session_id}"},
        "sk": {"S": f"heartbeat#{int(time.time())}"}
    },
    UpdateExpression="SET #ttl = :ttl, #active = :active",
    ExpressionAttributeNames={"#ttl": "ttl", "#active": "active"},
    ExpressionAttributeValues={
        ":ttl": {"N": str(int(time.time()) + 45)},  # 45s TTL
        ":active": {"BOOL": True}
    },
    ConditionExpression="attribute_not_exists(pk) OR #ttl < :now",  # Acquire or renew
    ReturnValues="ALL_NEW"
)
```

### Config-Driven Thresholds
**Apply to:** All quota knobs (D-15 discretion list) in `apps/voice/pipeline.toml`

**Pattern** (from config.py lines 137-236):
```toml
[quota]
heartbeat_renew_interval = 15  # seconds
heartbeat_ttl = 45             # seconds
sub_floor_seconds = 30         # min daily remaining to start new session
user_silence_timeout = 60      # seconds before idle teardown
reconnect_grace_seconds = 15   # brief blip reconnect window
goodbye_grace_seconds = 5      # cap on goodbye TTS playback + close
per_task_max_sessions = 5      # hard cap before retryable reject
auto_trip_ceiling_seconds = 3600  # daily site-wide budget in seconds
auto_trip_ceiling_dollars = 150   # est. daily cost ceiling
```

### CloutWatch Metric Emission
**Apply to:** Session lifecycle in `apps/voice/quota.py`

**Pattern** (boto3 cloudwatch client):
```python
cloudwatch = boto3.client('cloudwatch')
cloudwatch.put_metric_data(
    Namespace='klanker-voice/ecs',
    MetricData=[
        {
            'MetricName': 'ActiveSessions',
            'Value': current_session_count,
            'Unit': 'Count',
            'Timestamp': datetime.utcnow(),
            'Dimensions': [
                {'Name': 'TaskId', 'Value': task_id},
                {'Name': 'Service', 'Value': 'voice'},
            ]
        }
    ]
)
```

### Typed Rejection Errors
**Apply to:** `/api/offer` start-gate in `apps/voice/quota.py`

**Pattern** (from CONTEXT.md D-11):
```python
class QuotaError(Exception):
    """Base quota rejection error."""
    def __init__(self, error_type: str, message: str, http_status: int = 403):
        self.error_type = error_type  # no-access | concurrency-limit | daily-limit | site-paused
        self.message = message
        self.http_status = http_status

# At /api/offer start-gate:
if tier.session_max_seconds == 0:
    raise QuotaError("no-access", "Your tier does not permit voice sessions")
if active_heartbeats >= tier.max_concurrent:
    raise QuotaError("concurrency-limit", "You have reached your concurrent session limit")
if remaining_daily_seconds < config.sub_floor_seconds:
    raise QuotaError("daily-limit", "Daily usage limit reached; reset tomorrow")
if kill_switch.engaged:
    raise QuotaError("site-paused", "Voice service is temporarily paused by the operator")

# Return to browser as JSON:
{"error": error.error_type, "message": error.message}
```

---

## No Analog Found

Files requiring custom implementation (refer to design spec §6, CONTEXT.md decision sections, and CLAUDE.md for guidance):

| File | Role | Data Flow | Reason |
|------|------|-----------|--------|
| `apps/voice/quota.py` | service | CRUD + request-response | New service layer; no exact analog in voice app. Patterns borrowed from kv Go CRUD (tier.go) and config validation (config.py). |
| `apps/auth/webapp/src/entities/usage.ts` | model | CRUD | New ElectroDB entity; borrowed pattern from tier.ts but with new key schema (user_id × date × session_id). |
| `kv/internal/app/cmd/smoke.go` | CLI command | request-response | New smoke test CLI; no analog. Pattern from tier.go command structure + synthetic WebRTC offer logic from pipecat-ai client-js. |

---

## Metadata

**Analog search scope:** 
- `apps/voice/` — Python voice service
- `apps/auth/webapp/src/entities/` — ElectroDB entities
- `kv/internal/app/cmd/` — Go CLI commands
- `infra/terraform/` — Infrastructure templates (DynamoDB, ECS, network)

**Files scanned:** ~50 files across Python, Go, HCL, and TypeScript
**Pattern extraction date:** 2026-07-05

---

## Integration Checklist for Planner

- [ ] Verify JWT token validation (PyJWT/PyJWKClient) is added at `/api/offer` entrypoint
- [ ] Confirm DynamoDB usage table is added to voice service.hcl with `electro` schema type
- [ ] Check that heartbeat lease uses conditional writes with ~45s TTL (D-01)
- [ ] Verify 15s tick logic includes usage persistence + rollup update (D-02, D-10)
- [ ] Ensure kill-switch read on every `/api/offer` call (D-08)
- [ ] Confirm all four typed rejection errors are distinct (no-access, concurrency-limit, daily-limit, site-paused) (D-11)
- [ ] Verify ECS service has `assign_public_ip = true` and UDP SG rule for 20000–20100 (D-12)
- [ ] Check that Application Auto Scaling policy targets active_sessions metric (D-13)
- [ ] Confirm `kv usage`, `kv killswitch`, `kv smoke` commands are wired into NewRootCmd()
- [ ] Verify smoke test includes ICE candidate verification + RTP frame inspection (D-15)
