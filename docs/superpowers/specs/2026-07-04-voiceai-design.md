# VoiceAI (klankermaker.ai) — Design Spec

**Date:** 2026-07-04
**Status:** Approved (interview complete; ready for planning)
**Owner:** Kurt Hundeck (whereiskurt@gmail.com)

## 1. Summary

A public, conference-ready voice AI demo at **voice.klankermaker.ai**: a browser-based
speech-to-speech conversational agent ("KlankerMaker concierge") built as a cascaded
pipeline in the style of huggingface/speech-to-speech, backed by best-in-class hosted
APIs, transported over direct WebRTC, and deployed on the proven defcon.run.34
terragrunt/ECS infrastructure patterns. Access is gated by a new identity service at
**auth.klankermaker.ai** (magic-link email + OIDC) with an access-code → tier → quota
system.

Goals, in priority order:

1. **Slick and conversational** — voice-to-voice latency ≤ 1.2s, natural barge-in,
   ElevenLabs-quality voice.
2. **Low cost per minute, scalable** — ~$0.05–0.12 per conversation-minute in metered
   API costs, zero per-minute transport vendor, horizontal Fargate scaling.
3. **Reuse** — terragrunt skeleton, modules, and run.auth ported from defcon.run.34.

## 2. Decomposition & build order

Three subprojects, each its own GSD phase cycle:

| # | Subproject | What it is | Basis |
|---|-----------|------------|-------|
| 1 | **Infra skeleton** | `infra/terraform` terragrunt tree for the `kmk` site | Cloned defcon.run.34 layout + modules |
| 2 | **auth.klankermaker.ai** | Magic-link + OIDC identity service with access codes & quotas | Port of `apps/run.auth` |
| 3 | **voice.klankermaker.ai** | Pipecat voice pipeline service + browser client | New (the only genuinely new component) |

**Parallel track:** the Pipecat bot is built and tuned **locally first** (needs only
three API keys and a laptop mic) so the conversational feel iterates fast while infra
and auth land. The deployed container runs the same code.

## 3. Accounts, DNS, and profiles

- **Application resources:** AWS account `052251888500` via profile `klanker-application`.
- **DNS:** `klankermaker.ai` hosted zone `Z036807010CWM2JH60RKQ` in account
  `481723467561` via profile `klanker-management` (HostedZoneAdmin) — same
  cross-account DNS pattern as defcon.run.
- **State:** profile `klanker-terraform`; S3 backend + lock table named via
  `TG_BUCKET_<REGION_LABEL>` / `TG_TABLE_<REGION_LABEL>` env vars, prefix `tf-kmk`.
- **Profile wiring:** `TF_VAR_profile_prefix=klanker-` (the providers layer appends
  `application` / `management` / `terraform`). CI drops profiles and uses GitHub OIDC
  assume-role, as in defcon.run.34.
- **Site config:** `site.label = "kmk"`, `dns.zonename = "klankermaker.ai"`,
  `subdomains = ["auth", "voice"]`. Primary region `us-east-1`.

## 4. Infra skeleton (subproject 1)

Copied from `defcon.run.34/infra/terraform`, trimmed to what this project needs:

- `providers/global.hcl` + `providers/regional.hcl` — backend + provider generation,
  unchanged except naming.
- `live/site/terragrunt.hcl`, `site.hcl`, `region/skip.hcl`, `region/us-east-1/…`.
- Modules (versioned `modules/<name>/config.hcl` + `v1.0.0`): `network`, `certs`,
  `ecs-cluster`, `ecr`, `dynamodb`, `secrets`, `email` (SES for klankermaker.ai),
  `github-oidc`, `ecs-task`, `ecs-service`, `site`.
- Services defined as `services/<name>/service.hcl` data files aggregated by
  `site.hcl`: `auth` and `voice`.
- Secrets: `.secrets.sops.json` (SOPS) → SSM SecureString at
  `/kmk/secrets/<region>/<name>/<key>`; containers consume via `valueFrom`.
  Secret set: Deepgram API key, Anthropic API key, ElevenLabs API key, auth
  session/OIDC signing keys, SES config as needed.
- Explicitly **not** copied: cloudtrail, waffaw, mqtt, ec2spot, s3-uploads*,
  cloudfront-assets, bib/gpx modules. Add later only if needed.

**New infra consideration (WebRTC):** the `network`/`ecs-service` modules must support
(a) Fargate tasks with public IPs, and (b) a security-group ingress rule for a bounded
UDP port range (e.g. 20000–20100) for WebRTC media. This is the only infra delta from
the defcon.run.34 patterns.

## 5. Voice service (subproject 3, the new component)

### Pipeline

Pipecat (Python, open source) implementing the cascaded HF-style pipeline, every stage
streaming:

```
mic (browser, WebRTC/Opus)
  → SileroVAD (on-task, turn detection)
  → Deepgram Nova-3 streaming STT (partials ~300ms, built-in endpointing)
  → Claude Haiku 4.5 (claude-haiku-4-5), streaming, sentence-chunked to TTS
  → ElevenLabs Flash v2.5 WebSocket TTS (first audio ~75–150ms)
  → speaker (browser)
```

- **Latency budget (voice-to-voice ≤ 1.2s):** VAD endpoint ~200ms + STT final ~300ms
  + LLM TTFT ~400ms + TTS first-audio ~150ms + network ~100ms.
- **Barge-in:** user speech during agent playback cancels TTS and the in-flight LLM
  turn (Pipecat interruption handling).
- **Vendor swappability:** STT/LLM/TTS providers are config-driven (Pipecat plugin
  per stage) so stages can be A/B'd (e.g. Haiku vs Sonnet) without code changes.

### Transport & scaling

- **SmallWebRTC (Pipecat):** browser POSTs an SDP offer to `/api/offer` over HTTPS
  through the ALB; whichever Fargate task receives it answers with ICE candidates
  advertising the task's **own public IP** (from ECS task metadata). Media then flows
  UDP directly browser↔task. No SFU, no TURN vendor, no per-minute transport cost.
- Sessions are naturally sticky to the task that answered the offer; horizontal
  scaling is trivial (any task can take the next offer).
- **Sizing:** 1 vCPU / 2 GB tasks, ~5 concurrent sessions each; autoscaling 1→4 tasks
  covers 15–20 concurrent sessions (conference envelope).
- **Fallback risk noted:** networks that block UDP (hotel/corp Wi-Fi) will fail to
  connect media. v1 accepts this with a clear client-side error; a TURN fallback
  (e.g. coturn sidecar or Cloudflare TURN) is a listed future enhancement.

### Frontend

Single-page client served by the voice service (Pipecat JS client SDK):

- OIDC login redirect to auth.klankermaker.ai → returns with tokens.
- One prominent mic button; connected state shows live captions, a simple
  waveform/level indicator, and a visible session countdown timer.
- Auto-retry with status messaging on connect failure.

### Persona

"KlankerMaker concierge": knows Kurt, the klanker platform, defcon.run, and repo
highlights. Implemented as a versioned markdown system prompt
(`apps/voice/prompts/concierge.md`) — fat prompt now, RAG deferred.

### Session lifecycle

1. Client presents OIDC access token to `/api/offer`.
2. Service validates token (issuer = auth.klankermaker.ai), reads tier claims,
   checks quota (see §6). Reject with typed error if exhausted/over-concurrent.
3. Session runs; the service increments the user's usage record every 15s tick.
4. At tier `session_max_seconds − 30s`, the agent **speaks** a time warning; at
   zero it says goodbye and closes cleanly. Same spoken-wind-down on daily-quota
   exhaustion mid-session.

## 6. Auth service & quota model (subproject 2)

### Port of run.auth

Keep: Next.js, next-auth v5 magic-link email (nodemailer → SES), DynamoDB adapter
(ElectroDB), embedded `oidc-provider` (issuer), Altcha captcha on the login form.
Strip: defcon-specific pages/deps (leaflet, gpx, strapi, qr/pdf).
Add: access-code capture at login, tier claims in tokens, quota tables.

`voice` is registered as an OIDC client (authorization code + PKCE).

### Access codes

- Login form has an **optional access-code field**. Any value (or none) is accepted —
  login always succeeds via magic link.
- A DynamoDB `access_codes` table maps *known* codes → tier + group claims. Examples:
  `demo` → 2-minute demo tier; `kphdemo123` → 30-minute tier. Unknown/blank code →
  `no-access` tier (authenticated, but cannot start voice sessions; the UI explains
  how to get a code).
- Codes carry: tier id, expiry date, max redemptions, redemption count.
- Tier/groups are embedded as claims in the ID/access token so the voice service
  needs no auth-service round-trip per session start.

### Quota model

DynamoDB tables (ElectroDB single-table or separate; planner's choice):

- `tiers`: `tier_id`, `session_max_seconds`, `period_max_seconds` (per day),
  `max_concurrent`.
- `usage`: keyed `user_id × yyyy-mm-dd`, `seconds_used` (incremented by voice
  service ticks), conditional-write concurrency marker for `max_concurrent`.
- Site-wide **daily budget kill-switch**: a global daily seconds cap checked at
  session start; when tripped, new sessions get a friendly "demo is resting" page.

## 7. Cost envelope (conference-ready)

| Item | $/month |
|------|---------|
| ElevenLabs Pro (~500 speech-min) | 99 |
| Fargate (autoscale 1–4 × 1vCPU/2GB) | ~25 |
| ALB | ~20 |
| Deepgram + Anthropic usage | ~10–15 |
| Route53, SSM, DynamoDB, misc | ~5 |
| **Total** | **~$155–165** |

Off-season: drop to ElevenLabs Creator ($22) and min-capacity 1 task → ~$85/mo.
Marginal cost ≈ $0.05–0.12 per conversation-minute. Provider spend alerts +
the site-wide kill-switch bound worst-case burn.

## 8. Failure handling

- **Provider error mid-session** (STT/LLM/TTS): agent speaks a brief apology and the
  session ends cleanly — never dead air. No cross-vendor fallback in v1.
- **WebRTC media failure** (UDP blocked, ICE fail): client shows a clear error +
  retry; documented limitation until TURN fallback ships.
- **Quota/budget exhaustion:** spoken wind-down in-session; friendly gate page at
  session start.
- **Task death mid-session:** client detects disconnection, offers one-click
  reconnect (new session, quota-checked).

## 9. Testing

- **Unit:** access-code resolution, tier claims, quota check/increment/concurrency
  (auth + voice service logic).
- **Latency harness:** scripted WAV → pipeline → audio-out, asserting voice-to-voice
  ms budget; runs locally and against staging.
- **Manual UAT checklist:** barge-in feel, time-warning behavior, magic-link flow,
  bad-code flow, UDP-blocked error path.
- **Load:** 10 concurrent synthetic sessions before any conference use.

## 10. Out of scope (v1)

- RAG/knowledge retrieval (fat prompt only)
- Agent tool-calling
- TURN fallback for UDP-blocked networks
- Multi-region; CloudFront in front of voice
- Self-hosted/local models (the HF repo's local inference path)

## 11. Key decisions log

| Decision | Choice | Why |
|----------|--------|-----|
| Pipeline architecture | Cascaded, hosted APIs | HF-concepts fidelity + best quality/latency per dollar |
| Transport | Pipecat SmallWebRTC, direct to task | $0/min transport, echo cancellation, barge-in |
| STT | Deepgram Nova-3 streaming | ~300ms partials, endpointing, $0.0077/min |
| LLM | Claude Haiku 4.5 (config-swappable) | TTFT ~300–400ms, cheap per turn |
| TTS | ElevenLabs Flash v2.5 | Best voice quality at ~75–150ms first-audio |
| Auth | Ported run.auth + access codes | Proven magic-link/OIDC base; tier gating |
| Scale target | Conference-ready (~$120–165/mo) | 15–20 concurrent sessions, ElevenLabs Pro |
