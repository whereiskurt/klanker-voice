# Architecture Patterns

**Domain:** Browser speech-to-speech voice agent (Pipecat) on AWS ECS Fargate with direct WebRTC, OIDC auth, DynamoDB quotas
**Researched:** 2026-07-04
**Overall confidence:** MEDIUM (AWS mechanics and OIDC/DynamoDB patterns cross-verified across official docs + community sources; Pipecat/aiortc internals verified directly in source but single-sourced — seam-classified LOW)

## Recommended Architecture

```
                 ┌─────────────────────────────────────────────────────┐
                 │                     Browser                         │
                 │  Pipecat JS client · mic · captions · timer         │
                 └───────┬───────────────────┬─────────────────────────┘
        HTTPS (login,    │                   │  UDP (WebRTC media, Opus)
        OIDC redirect,   │                   │  DIRECT to task public IP
        POST /api/offer) │                   │  — never touches the ALB
                         ▼                   ▼
                 ┌───────────────┐   ┌──────────────────────────────────┐
                 │      ALB      │   │  ECS Fargate task (public IP)    │
                 │ host routing: │   │  apps/voice: FastAPI + Pipecat   │
                 │  auth.* ──────┼──▶│  ┌────────────────────────────┐  │
                 │  voice.* ─────┼──▶│  │ N in-process sessions:     │  │
                 └───────────────┘   │  │ SmallWebRTC ↔ VAD → STT →  │  │
                         │           │  │ LLM → TTS pipeline each    │  │
                         ▼           │  └────────────────────────────┘  │
                 ┌───────────────┐   │  JWT validator (JWKS cache)      │
                 │ apps/auth     │   │  Quota gate + 15s tick writer    │
                 │ Next.js:      │   │  Task-protection manager         │
                 │ magic link,   │   └───────┬──────────┬───────────────┘
                 │ oidc-provider,│           │          │
                 │ access codes  │           │          ▼
                 └──────┬────────┘           │   Deepgram / Anthropic /
                        │                    │   ElevenLabs (streaming APIs)
                        ▼                    ▼
                 ┌─────────────────────────────────────┐      ┌──────────┐
                 │ DynamoDB: auth tables, access_codes,│◀─────│ cli/kv   │
                 │ tiers, usage (+ GLOBAL kill-switch) │      │ (Go, ops)│
                 └─────────────────────────────────────┘      └──────────┘
```

**Core shape:** two web services behind one ALB (host-based routing), with WebRTC media deliberately *bypassing* the ALB — UDP flows browser↔task public IP. The voice service is **multi-session per task** (one FastAPI process runs ~5 concurrent Pipecat pipelines), not bot-per-task. Auth is consulted **zero times per session** at runtime: tier/quota identity travels inside the signed access token.

### Component Boundaries

| Component | Responsibility | Communicates With |
|-----------|---------------|-------------------|
| Browser client (served by voice service) | Mic capture, OIDC redirect flow, SDP offer POST, playback, captions/timer UI, reconnect UX | ALB (HTTPS), voice task (UDP media), auth (redirect) |
| ALB | TLS termination, host routing (`auth.*` / `voice.*`), health checks | Browser, auth tasks, voice tasks (HTTP only) |
| `apps/auth` (Next.js) | Magic-link login (SES), access-code capture, code→tier resolution, OIDC issuer (`oidc-provider`), mints tokens with tier claims, publishes JWKS | Browser, SES, DynamoDB (auth + `access_codes` tables) |
| `apps/voice` (Python/FastAPI + Pipecat) | `POST /api/offer` signaling, JWT validation (offline via JWKS), quota gate, session lifecycle, 15s usage ticks, spoken wind-down, ECS task protection toggling | Browser (HTTP + UDP), auth (JWKS fetch only, cached), DynamoDB (`tiers`, `usage`), ECS agent endpoint, Deepgram/Anthropic/ElevenLabs |
| DynamoDB | Source of truth for users, access codes, tiers, usage counters, concurrency markers, global kill-switch | auth (read/write), voice (read tiers, conditional-write usage), kv (read/write) |
| `cli/kv` (Go) | Operator plane: access-code CRUD, quota/usage inspection, session visibility, deploy/smoke helpers | DynamoDB, ECS/CloudWatch APIs, service health endpoints |
| `infra/terraform` | Terragrunt tree (site `kmk`): network, certs, ecs-cluster, ecr, dynamodb, secrets, email, github-oidc, ecs-task, ecs-service | AWS (3 accounts: application, management/DNS, terraform/state) |

**Boundary rules worth enforcing:**
- Voice never calls auth's application APIs at runtime — only the public JWKS/discovery endpoints, cached. Tier limits ride in token claims; quota *state* lives in DynamoDB.
- Auth never touches the `usage` table's tick path — voice owns increments; auth (and kv) own tier/code definitions.
- kv talks to AWS APIs and DynamoDB directly (operator credentials), not through the services.

### Data Flow

**1. Login (once per user):**
Browser → `auth.klankermaker.ai` login form (email + optional access code, Altcha) → magic link via SES → callback resolves code against `access_codes` (unknown/blank → `no-access` tier) → OIDC authorization-code+PKCE back to `voice.klankermaker.ai` → browser holds access token with `tier`, `groups` claims.

**2. Session start (per session):**
Browser `POST /api/offer` (SDP + Bearer token) → ALB → any voice task →
(a) validate JWT offline: signature via cached JWKS, `iss=auth.klankermaker.ai`, `aud=voice`, `exp`;
(b) quota gate against DynamoDB: global kill-switch item → per-day `usage` read → conditional increment of concurrency marker (`active < max_concurrent`); typed error rejections;
(c) create `SmallWebRTCConnection(ice_servers=[STUN])`, set remote offer, return SDP answer whose srflx candidate is the task's public IP (learned via STUN through the ENI's 1:1 NAT);
(d) set ECS task protection if this is the task's first active session.
Media then flows UDP browser↔task directly; the session is inherently sticky to the answering task (the peer connection lives in that process).

**3. In-session (every 15s):**
Voice task issues one `UpdateItem`: `ADD seconds_used :15` with `ConditionExpression seconds_used < :period_cap` (plus a parallel tick on the GLOBAL budget item). Condition failure ⇒ quota exhausted mid-session ⇒ trigger spoken wind-down instead of hard cut. Local session timer handles `session_max_seconds − 30s` warning and goodbye.

**4. Session end:**
Decrement concurrency marker, final usage flush, drop task protection when the task's active-session count hits zero (or let the protection TTL expire as backstop).

**5. Ops:**
`kv` reads/writes `access_codes`/`tiers`, inspects `usage`, lists protected tasks/sessions via ECS APIs, flips the kill-switch item.

## Patterns to Follow

### Pattern 1: Server-side STUN for public-IP advertisement (not manual candidate injection)
**What:** Give `SmallWebRTCConnection` a STUN server (`stun:stun.l.google.com:19302` or self-chosen). aiortc gathers a server-reflexive (srflx) candidate that *is* the Fargate task's public IP, because the ENI public IP is 1:1 static NAT with port preservation.
**When:** Always, in the deployed service. This is the mechanism that makes "advertise the task's own public IP" work.
**Why this instead of the spec's "IP from ECS task metadata" idea:** Verified in source — `SmallWebRTCConnection` exposes **only** `ice_servers` and a timeout; there is no host-IP override, candidate-injection, or address-rewriting hook, and aioice has no API to add external host addresses (confidence: LOW-tagged single source, but read directly from `pipecat.transports.smallwebrtc.connection` and `aioice/ice.py`). STUN achieves the same result with zero custom code. Keep ECS-metadata IP discovery for *logging/ops only* — and note Fargate task metadata v4 does **not** include the public IP; you'd need `ecs:DescribeTasks` → ENI id → `ec2:DescribeNetworkInterfaces` (`Association.PublicIp`) with those IAM permissions on the task role (MEDIUM).
```python
webrtc_connection = SmallWebRTCConnection(
    ice_servers=[IceServer(urls=["stun:stun.l.google.com:19302"])]
)
```

### Pattern 2: Multi-session FastAPI process per task (not bot-per-task)
**What:** One long-running FastAPI app per Fargate task; each accepted `/api/offer` spawns an in-process Pipecat `PipelineTask`. ~5 sessions per 1 vCPU/2 GB task per the spec's sizing.
**When:** This scale (15–20 concurrent, conference envelope).
**Why:** Pipecat's documented alternatives are bot-runner-spawns-a-VM-per-session (Pipecat Cloud / Fly machines) — Fargate task cold-start (~30–60s image pull + boot) would destroy the "tap mic, talk" demo feel. Multi-session per process is exactly the SmallWebRTC runner model. Caveat verified in docs: aiortc SCTP chunk sizing (`PIPECAT_SCTP_MAX_CHUNK_SIZE`) is process-global — all sessions in a task share it.

### Pattern 3: ECS task scale-in protection tied to active-session count
**What:** From inside the task, `PUT $ECS_AGENT_URI/task-protection/v1/state` with `{"ProtectionEnabled": true, "ExpiresInMinutes": <session_max/60 + margin>}` when active sessions go 0→1; disable (or let expire) at 1→0. Task role needs `ecs:UpdateTaskProtection`.
**When:** Any autoscaled service holding live WebRTC sessions — scale-in otherwise kills mid-conversation sessions.
**Why:** This is AWS's canonical mechanism for exactly this (long-lived WebSocket/game sessions). Confidence MEDIUM (AWS docs + blog cross-verified). Set expiry to tier `session_max_seconds` + a few minutes, not the 2h default — protected tasks also block deployments, so short expiries keep deploys unstuck.
```python
# on first session start
requests.put(f"{os.environ['ECS_AGENT_URI']}/task-protection/v1/state",
             json={"ProtectionEnabled": True, "ExpiresInMinutes": 35})
```

### Pattern 4: Offline JWT validation with PyJWKClient — but force JWT access tokens in oidc-provider first
**What:** Voice validates `Authorization: Bearer` with PyJWT + `PyJWKClient` (JWKS cached, `kid`-resolved), asserting `iss`, `aud`, `exp`, then reads `tier` claims.
**Critical prerequisite (MEDIUM, cross-verified):** `node oidc-provider` issues **opaque** access tokens by default. To make them locally verifiable JWTs you must enable the Resource Indicators (RFC 8707) feature with `accessTokenFormat: 'jwt'` and register the voice API as the resource/audience — and put tier claims into the access token via the resource-server claims hook. This is an *auth-service* build task that gates the voice validator; decide it in the auth phase, not the voice phase. (Fallback if skipped: token introspection endpoint — adds an auth round-trip per session start, which the spec explicitly wants to avoid.)

### Pattern 5: Single-round-trip quota tick — ADD with ConditionExpression
**What:** `UpdateItem` combining the atomic counter and the cap check: `ADD seconds_used :tick` + `ConditionExpression: seconds_used < :period_cap`. `ConditionalCheckFailedException` ⇒ quota exhausted ⇒ spoken wind-down. Concurrency: conditional increment of `active_sessions < max_concurrent` at session start, decrement at end, with a TTL'd per-session marker (or kv sweeper) to recover leaks from task death. Usage keyed `user_id × yyyy-mm-dd`; kill-switch is the same pattern on a singleton `GLOBAL × yyyy-mm-dd` item.
**When:** All metering writes.
**Why:** Atomic, contention-free at this volume, no read-modify-write races; conditions cost nothing extra. (MEDIUM — AWS docs + multiple independent writeups.)

### Pattern 6: Monorepo with native per-language tooling, no meta-build-system
**What:**
```
klanker-voice/
├── apps/
│   ├── voice/          # Python: pyproject.toml (uv), src/, prompts/concierge.md, Dockerfile
│   └── auth/           # Next.js: package.json, Dockerfile (ported run.auth)
├── cli/
│   └── kv/             # Go: go.mod, cmd/kv/, internal/
├── infra/
│   └── terraform/      # terragrunt: providers/, live/site/…, modules/<name>/vX.Y.Z, services/*.hcl
├── docs/               # specs, shared contracts (claims shape, table schemas)
└── .github/workflows/  # per-app path-filtered CI; GitHub OIDC deploy roles
```
**When:** Polyglot repos of this size (3 deployables, 1 CLI).
**Why:** Each toolchain stays idiomatic (uv/npm/go); CI uses path filters per app; matches the defcon.run.34 layout the infra is cloned from, so `services/<name>/service.hcl` maps 1:1 to `apps/<name>`. Nx/Bazel-style orchestration buys nothing at 3 apps. Shared contracts (token claims, DynamoDB table names, tier schema) live in `docs/` and are duplicated as constants per app — acceptable at this scale, note it as a drift risk.

## Anti-Patterns to Avoid

### Anti-Pattern 1: Routing WebRTC media through the ALB (or an NLB)
**What:** Trying to keep all traffic behind the load balancer.
**Why bad:** ALB is HTTP-only; media is UDP. An NLB adds cost and breaks the per-task addressing model ICE needs. The whole cost story ($0/min transport) depends on direct UDP.
**Instead:** ALB carries only HTTPS signaling/static; SG on tasks allows UDP ingress for media. ALB idle timeout (default 60s, max 4000s) is irrelevant to media and fine for a sub-second `POST /api/offer` — only raise it if you later add WebSocket signaling or SSE.

### Anti-Pattern 2: Assuming you can bound aiortc to UDP 20000–20100 via configuration
**What:** The spec's "SG with bounded UDP port range" assumes the media server can be told which ports to use.
**Why bad:** Verified in aioice source: sockets bind `local_addr=(address, 0)` — OS ephemeral ports, no port-range parameter anywhere in aiortc/aioice's public API (aiortc issue #487 remains open). A 100-port SG rule with unbounded binding = intermittent, maddening connection failures.
**Instead (pick one, in order of preference):**
1. **Widen the SG rule** to UDP 1024–65535 (or the container's ephemeral range) inbound on the task SG. Exposure is modest: only aiortc listens on those ports, the task is single-purpose, and this is the zero-code path. Ship v1 this way.
2. Constrain the task's network namespace via ECS `systemControls` sysctl `net.ipv4.ip_local_port_range` and match the SG — verify Fargate supports this sysctl during the infra phase (flagged as phase research; not confirmed in this pass).
3. Small monkey-patch of aioice's socket binding to draw from a bounded range — workable but you own it across Pipecat/aiortc upgrades.

### Anti-Pattern 3: Bot-per-Fargate-task session spawning
**What:** Copying the Pipecat Cloud / Fly "machine per session" pattern onto ECS `RunTask`.
**Why bad:** Fargate cold start is tens of seconds; users get dead air after tapping the mic. Also multiplies cost and IAM/metadata plumbing.
**Instead:** Multi-session process per task (Pattern 2); scale tasks, not sessions.

### Anti-Pattern 4: Per-request auth-service calls for token checks
**What:** Voice calling auth's introspection or userinfo per offer/tick.
**Why bad:** Adds latency and an availability coupling the design explicitly avoids; ticks every 15s across sessions would hammer it.
**Instead:** JWT access tokens with tier claims, validated offline (Pattern 4). The only voice→auth dependency is the cached JWKS document.

### Anti-Pattern 5: Autoscaling without session-awareness
**What:** Plain CPU target-tracking with no protection.
**Why bad:** Scale-in kills live conversations mid-sentence; deploys do the same.
**Instead:** Task protection (Pattern 3) + scale on a custom `ActiveSessions`-per-task CloudWatch metric the voice service publishes (CPU is a poor proxy — pipelines are mostly I/O-wait on hosted APIs). Scale-out threshold ~4 sessions/task avg; min 1, max 4 tasks.

## Suggested Build Order

Dependencies discovered in research sharpen the spec's three-subproject order:

1. **Local Pipecat pipeline (parallel track, day one)** — no infra dependency; needs only three API keys. De-risks the core value (latency/barge-in feel). Include the STUN-configured `SmallWebRTCConnection` from the start so local ≈ prod transport.
2. **Infra skeleton** — network/certs/ecr/dynamodb/secrets/email/github-oidc/ecs-cluster. The WebRTC delta (public-IP tasks + UDP SG rule) is the only novel module work; resolve the port-range decision (Anti-Pattern 2) here.
3. **Auth service** — depends on infra (DNS, SES, DynamoDB, ecs-service). **Must land the JWT-access-token / resource-indicator decision and tier-claims shape here** (Pattern 4 prerequisite) — this is the contract the voice deploy blocks on. Ship `access_codes`/`tiers` tables with it.
4. **Voice service deployed** — depends on infra + auth's token contract. Adds JWT validation, quota gate, ticks, task protection, `/api/offer`. First end-to-end UDP test through the real SG happens here; budget time for ICE debugging.
5. **kv CLI** — depends only on DynamoDB schemas and AWS APIs; build incrementally alongside 3–4 (code CRUD as soon as `access_codes` exists; session visibility after 4).

**Phases likely needing deeper research when reached:** Fargate `systemControls` sysctl support for port ranges (infra phase); `oidc-provider` resource-indicator + custom-claims configuration specifics (auth phase); Pipecat interruption/wind-down orchestration details (voice phase).

## Scalability Considerations

| Concern | At 5 concurrent | At 20 concurrent (conference) | At 100+ concurrent |
|---------|-----------------|-------------------------------|--------------------|
| Voice tasks | 1 task, no scaling events | 1→4 tasks, ActiveSessions metric, task protection mandatory | Rethink: NLB/TURN or Pipecat Cloud; SG/IP-per-task model still works but ops burden grows |
| Signaling | Single ALB, defaults fine | Same; ALB is nowhere near limits | Same |
| DynamoDB ticks | Trivial (on-demand) | ~80 writes/min — trivial | Still trivial; hot partition only matters on the GLOBAL kill-switch item (~400+ writes/min before any concern) |
| JWKS fetch | Cached in-process | Same | Same |
| UDP-blocked clients | Documented failure | Same (accepted v1 risk) | TURN fallback becomes table stakes |

## Sources

- Pipecat SmallWebRTC transport docs — https://docs.pipecat.ai/server/services/transport/small-webrtc (webfetch; seam-tagged LOW)
- Pipecat `SmallWebRTCConnection` source — https://reference-server.pipecat.ai/en/latest/_modules/pipecat/transports/smallwebrtc/connection.html (constructor surface verified; LOW)
- Pipecat runner guide + `runner/utils.py` (SDP munging behavior) — https://docs.pipecat.ai/server/utilities/runner/guide, https://github.com/pipecat-ai/pipecat/blob/main/src/pipecat/runner/utils.py (LOW)
- Pipecat cross-network issue — https://github.com/pipecat-ai/pipecat/issues/2426 (LOW)
- aioice source (no port-range API) — https://raw.githubusercontent.com/aiortc/aioice/main/src/aioice/ice.py; aiortc port-limit issue https://github.com/aiortc/aiortc/issues/487; aioice API docs https://aioice.readthedocs.io/en/latest/api.html (LOW, verified in source)
- Fargate task metadata v4 — https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task-metadata-endpoint-v4-fargate.html; public-IP discovery recipe https://cloudtelcohub.com/posts/ip-public-aws-ecs-fargate/ (MEDIUM, cross-verified)
- ECS task scale-in protection — https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task-scale-in-protection.html, https://aws.amazon.com/blogs/containers/announcing-amazon-ecs-task-scale-in-protection/, https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task-scale-in-protection-endpoint.html (MEDIUM)
- ALB idle timeout — https://websocket.org/guides/infrastructure/aws/alb/ + AWS ELB docs (MEDIUM)
- node-oidc-provider JWT/opaque access tokens & resource indicators — https://github.com/panva/node-oidc-provider/blob/main/docs/README.md (MEDIUM)
- DynamoDB counters/conditional writes — https://aws.amazon.com/blogs/database/implement-resource-counters-with-amazon-dynamodb/ and independent writeups (MEDIUM)
- Pipecat deployment pattern (bot runner, per-session VM tradeoffs) — https://docs.pipecat.ai/deployment/pattern (MEDIUM)
