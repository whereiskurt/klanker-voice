# Infrastructure — what actually runs this

An operator's inventory of the live AWS surface, and how to check each layer is
healthy.

This page answers *what exists and is it up*. For *how it gets built and
deployed* — the CI pipeline, image builds, SOPS→SSM flow, IAM role design,
rollback — see [the deployment guide](../guides/deployment.md), which this page
deliberately does not duplicate.

> Inventory captured live on 2026-08-18 from account `052251888500`, region
> `us-east-1`.

---

## The whole thing, at a glance

```
                    voice.klankermaker.ai            auth.klankermaker.ai
                             │                                │
                    CloudFront E2A080D9TKBKHA                  │
                     (static assets, S3 OAC)                   │
                             │                                │
                             └──────────┬─────────────────────┘
                                        ▼
                       ALB  alb-use1-klankermaker-ai
                                        │
        ┌───────────────────────────────┼───────────────────────────────┐
        ▼                               ▼                               │
   voice-use1                      auth-use1                            │
   (public IP: yes)                (public IP: no)                      │
        │                               │                               │
        │                               │        telephony-edge-use1 ───┘
        │                               │        (public IP: yes, no ALB)
        │                               │                    │
        │                               │              VoIP.ms POP 45
        │                               │              (SIP/RTP, POP-locked SG)
        ▼                               ▼
   kmv-voice-usage             kmv-auth-electro
                               kmv-auth-authjs
```

All three services run on **one Fargate cluster, `app-use1-kmv`**, one task each.

---

## Compute

| Service | Task definition | Desired | Public IP | Behind ALB |
|---|---|---|---|---|
| `voice-use1` | `voice-use1-kmv:84` | 1 | yes | yes |
| `auth-use1` | `auth-use1-kmv:20` | 1 | no | yes |
| `telephony-edge-use1` | `telephony-edge-use1-kmv:58` | 1 | yes | **no** |

Revisions are as of the capture — they climb with every deploy.

`telephony-edge` is the odd one and deliberately so: no load balancer (ARI is
private-network-only), a public IP purely so the outbound VoIP.ms registration
trunk works, and its own POP-locked security group instead of the shared list.
It is sized at 2 vCPU / 4 GB.

> **`max_concurrent_calls = 4` in the telephony config permits four simultaneous
> calls at the software layer only.** The single task is still sized for one.
> Truthful four-call capacity needs a vCPU/memory bump and a real load test —
> that work is deferred. The knob permits; it does not deliver.

```bash
aws ecs describe-services --cluster app-use1-kmv \
  --services voice-use1 auth-use1 telephony-edge-use1 \
  --query 'services[].{name:serviceName,desired:desiredCount,running:runningCount,state:deployments[0].rolloutState}'
```

---

## Data

### DynamoDB

| Table | Holds | Reached by |
|---|---|---|
| `kmv-auth-electro` | access codes, tiers, bypass tokens, caller-ID phone mappings | `kv --table` |
| `kmv-voice-usage` | per-user daily usage, site rollup, kill-switch control item | `kv --usage-table` |
| `kmv-auth-authjs` | next-auth sessions and magic-link state | the auth app only |
| `tf-kmv-use1-6e913c73` | Terraform state locks | terragrunt only |

The first two are the [two-table trap](kv-cli-reference.md#the-two-table-trap).

### S3

| Bucket | Holds |
|---|---|
| `kmv-cf-assets-voice-use1-…` | the built browser client, served through CloudFront |
| `kmv-ledger-use1-…` | the private transcript ledger, plus telephony media (the RICK clips) |
| `logs-alb-use1-kmv-…` | ALB access logs |
| `ses-inbox-kmv-use1-…` | inbound SES mail |
| `tf-kmv-use1-6e913c73` | Terraform state |

> The ledger bucket has **no `force_destroy`**. Terraform will refuse to delete
> it while objects remain, and that refusal is intentional — it holds transcript
> history.

### ECR

`kmv-voice-app`, `kmv-auth-app`, `kmv-telephony-edge`. Tags are immutable, with a
lifecycle policy keeping 10 images / 30 days.

### CloudFront

One distribution, `E2A080D9TKBKHA`, aliased to `voice.klankermaker.ai`, fronting
the S3 asset bucket via OAC.

---

## Secrets

Everything lives under `/kmv/` in SSM Parameter Store, `us-east-1`, and reaches
containers as `valueFrom` references in the task definition. **No secret value
is ever in git**, in a TOML, or in an image.

| Prefix | Holds | Consumed by |
|---|---|---|
| `/kmv/secrets/use1/{deepgram,anthropic,elevenlabs}/api_key` | the three provider keys | voice, telephony-edge |
| `/kmv/secrets/use1/voipms/*` | SIP + API credentials, and one stale `did` | telephony-edge, auth, `kv voipms` |
| `/kmv/secrets/use1/asterisk/ari_*` | ARI credentials | telephony-edge |
| `/kmv/secrets/use1/telephony/*` | gate PIN, passphrase, mint endpoint bearer | telephony-edge |
| `/kmv/secrets/use1/ctf/*` | game codes, spoken triggers, TOTP seeds, relay bearer | telephony-edge, auth |
| `/kmv/secrets/use1/{jwt,oidc,altcha}/*` | signing keys, cookie keys, captcha HMAC | auth |
| `/kmv/secrets/use1/voice/smoke_token` | the `kv smoke` service credential | voice |
| `/kmv/secrets/use1/ledger/code_hash_salt` | transcript-ledger hashing salt | voice, telephony-edge |
| `/kmv/operators/use1/*` | operator-only values | **nothing** — no task role may read this |
| `/kmv/{ses,dynamodb,cloudfront,ledger}/*` | non-secret infrastructure outputs | terragrunt, apps |

Two rules worth stating plainly:

- **`/kmv/operators/*` is off-limits to every task role.** The telephony-edge
  role in particular is scoped to exactly the three secret prefixes it consumes,
  and this prefix is explicitly excluded.
- **ECS refuses to launch a task whose `valueFrom` parameter does not exist.**
  So a parameter must be seeded *before* its `service.hcl` row is applied.
  Getting this backwards takes the service down. This is the single most common
  self-inflicted outage in this project's history.

```bash
aws ssm describe-parameters \
  --parameter-filters "Key=Name,Option=BeginsWith,Values=/kmv/secrets/use1/ctf" \
  --query 'Parameters[].Name' --output text
```

---

## Terragrunt units

Under `infra/terraform/live/site/`:

| Unit | Creates |
|---|---|
| `global/github-oidc` | the CI OIDC roles |
| `global/cloudfront` | global CloudFront config |
| `region/us-east-1/network` | VPC, subnets, security groups (incl. the POP-locked telephony SG) |
| `region/us-east-1/certs` | ACM certificates |
| `region/us-east-1/ecr` | the three image repositories |
| `region/us-east-1/dynamodb` | the three application tables |
| `region/us-east-1/secrets` | SSM parameters from SOPS |
| `region/us-east-1/ecs-cluster` | `app-use1-kmv` |
| `region/us-east-1/ecs-task` | all three task definitions |
| `region/us-east-1/ecs-service` | all three services |
| `region/us-east-1/ledger` | the transcript ledger bucket |
| `region/us-east-1/email`, `dmarc` | SES and DNS records |
| `region/us-east-1/cloudfront` | the asset distribution |

Service *configuration* lives in `live/site/services/{auth,voice,telephony-edge}/service.hcl`
— pure data files that `site.hcl` reads at parse time. Three units share one
task/service definition pair, which is why the deploy workflow resolves each
service's currently-running image tag before applying: without that, deploying
one service would roll the other two back to whatever tag the state last knew.

`ap-southeast-1` and `ca-central-1` are declared but **skipped** (`skip_regions`).

---

## Deploy path

Summarised; the detail is in [the deployment guide](../guides/deployment.md).

| Change under | Workflow | Effect |
|---|---|---|
| `apps/voice/**` | `build-voice.yml` + `build-telephony-edge.yml` | build → ECR → `deploy.yml` rolls ECS |
| `apps/auth/**` | `build-auth.yml` | build → ECR → `deploy.yml` rolls ECS |
| `infra/**` | `terragrunt-plan.yml` on PR/push | **plan only**, read-only role, no gate |
| `infra/**` | `terragrunt-apply.yml` | `workflow_dispatch` only, requires human approval |

**Applies never run automatically.** Plans run on every PR and push with a
read-only role; applies are manual and gated behind an environment reviewer.

Tool pins: terragrunt 0.97.1, terraform 1.14.3, sops 3.11.0. Keep local versions
aligned — `infra/.envrc` mirrors the non-secret env contract CI uses.

> **Known deploy quirk:** the `deploy-ecs-main` concurrency group cancels
> displaced pending deploys. Two deploys queued close together means the first
> pending one is dropped, not queued behind. Watch for it when pushing twice in
> quick succession.

---

## Health checks

Fastest to slowest, cheapest to most expensive. Run the first two before any
event.

### 1. Does the pipeline carry audio?

```bash
export KMV_SMOKE_SERVICE_TOKEN=$(aws ssm get-parameter \
  --name /kmv/secrets/use1/voice/smoke_token --with-decryption \
  --query Parameter.Value --output text)
kv smoke
```

![kv smoke](../assets/terminal/smoke.svg)

`RTP-PACKETS` non-zero is the assertion. ICE reaching `connected` only proves a
path was negotiated; packets prove the pipeline is speaking. Exits non-zero on
FAIL, so it works in a gate.

### 2. Is the brake off, and is spend sane?

```bash
kv killswitch status
kv usage today
kv voipms balance
```

![kv usage and killswitch](../assets/terminal/usage-killswitch.svg)

A drained VoIP.ms balance stops inbound calls routing — which fails closed and
looks exactly like "the phone number is broken".

### 3. Is the phone side configured as expected?

```bash
kv telephony list
```

Five sections, five independent sources; each degrades on its own without
blinding the others.

### 4. Are the tasks actually running?

```bash
aws ecs describe-services --cluster app-use1-kmv \
  --services voice-use1 auth-use1 telephony-edge-use1 \
  --query 'services[].{n:serviceName,d:desiredCount,r:runningCount,s:deployments[0].rolloutState}'
```

`runningCount` below `desiredCount` with `rolloutState: IN_PROGRESS` is a deploy
in flight. The same with `FAILED` usually means a task cannot start — and the
most common cause is a `valueFrom` pointing at an SSM parameter that does not
exist.

### 5. What do the logs say?

| Service | Log group |
|---|---|
| voice | `/ecs/voice-app-voice-use1-kmv` |
| auth | `/ecs/auth-app-auth-use1-kmv` |
| telephony-edge | `/ecs/telephony-edge-telephony-edge-use1-kmv` |

```bash
aws logs tail /ecs/telephony-edge-telephony-edge-use1-kmv --since 30m --follow
```

> **CloudWatch `@timestamp` is ingestion time, not event time.** The edge
> batches, so several genuinely distinct calls can share one timestamp. `kv
> telephony calls` derives real event time from the ARI channel id's epoch
> prefix for exactly this reason — don't hand-roll a Logs Insights query that
> sorts on `@timestamp` and trust the ordering.

---

## Costs

Roughly $120–165/month conference-ready, ~$85/month off-season. The bounded
pieces: Fargate tasks, the ALB, CloudFront, DynamoDB on-demand, plus per-use
Deepgram/Anthropic/ElevenLabs and VoIP.ms per-minute inbound.

The unbounded piece is a public microphone wired to metered APIs. Four things
bound it, and they are all covered in [the incident runbook](incident-runbook.md):

1. **Tier quotas** — session, period, concurrency, per identity.
2. **The auto-trip ceilings** — `auto_trip_ceiling_seconds` (7200) and
   `auto_trip_ceiling_dollars` (40) engage the kill-switch by themselves.
3. **`kv killswitch on`** — the manual brake, effective on the next session.
4. **VoIP.ms balance with auto-recharge off** — the phone side fails closed.

---

## Related

- [Deployment guide](../guides/deployment.md) — how it gets built and shipped
- [Incident runbook](incident-runbook.md) — when one of these layers is unhappy
- [PBX lifecycle](pbx-lifecycle.md) — the telephony edge in detail
- [`kv` CLI reference](kv-cli-reference.md) — every command used here
- [Architecture overview](../architecture/overview.md) — how the pieces fit conceptually
