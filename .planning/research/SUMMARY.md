# Project Research Summary

**Project:** klanker-voice (voice.klankermaker.ai)
**Domain:** Public browser-based speech-to-speech voice agent demo (quota-gated, conference-grade)
**Researched:** 2026-07-04
**Confidence:** MEDIUM

## Executive Summary

This is a low-latency cascaded voice agent (Silero VAD → Deepgram Nova-3 → Claude Haiku 4.5 → ElevenLabs Flash v2.5) built on Pipecat with SmallWebRTC, deployed as multi-session FastAPI processes on ECS Fargate with direct browser↔task UDP media, fronted by a ported run.auth OIDC service and a Go `kv` operator CLI. Every committed stack choice was verified current and correct as of 2026-07-04. Experts in this domain converge on one truth: the demo is judged in the first ten seconds by latency and barge-in behavior, not features. The 1.2s latency budget is a ceiling — target ~800ms typical — and the budget dies in framework defaults and turn-taking (Pipecat's 1.0s `aggregation_timeout`, stacked VAD + Deepgram endpointing), not vendor TTFBs.

The recommended approach starts with a local-first pipeline track on day one: build a latency harness (per-stage ms instrumentation) and named barge-in test scenarios before any persona or infra work, pin pipecat-ai `~=1.5.0` exactly, and tune endpointing with a single turn-end owner. In parallel, stand up the infra skeleton early because SES production access is a manual multi-day review that must be requested in week one. Auth ships next and must land the JWT-access-token contract (oidc-provider issues opaque tokens by default; the Resource Indicators feature with `accessTokenFormat: 'jwt'` plus tier claims is a hard prerequisite the voice deploy blocks on). The voice service deploys last against that contract; `kv` builds incrementally alongside.

The key risks are all deployment-seam risks that local dev completely masks: WebRTC advertising private IPs on Fargate (solved by server-side STUN — verified in source that SmallWebRTC has no host-IP override, so the spec's "inject metadata IP" idea won't work as imagined), aiortc's inability to bind a bounded UDP port range (widen the SG to the ephemeral range for v1), abandoned sessions burning metered API dollars (four-layer teardown: idle detection + disconnect handlers + server-side wall clock + TTL'd concurrency markers), magic links eaten by corporate email scanners (interstitial confirm-click page), and quota races (every limit enforced as a DynamoDB ConditionExpression, never read-then-write).

## Key Findings

### Recommended Stack

All committed choices validated. Two refinements: (1) pipecat-ai 1.5.0 restructured extras — there is no `silero` or `elevenlabs` extra anymore (both in core); correct install is `pipecat-ai[webrtc,deepgram,anthropic,runner]~=1.5.0` on Python 3.12. (2) Deepgram Flux (conversational STT with built-in end-of-turn detection, can save 200–600ms/turn) is the ecosystem's 2026 upgrade path — keep Nova-3 as committed baseline but make the STT stage config-swappable for A/B. Do NOT use `dailyco/pipecat-base` (Pipecat Cloud contract); use `python:3.12-slim` + uv with `libopus0 libvpx7 ffmpeg`.

**Core technologies:**
- pipecat-ai 1.5.0 (pin `~=1.5.0`): pipeline framework — post-1.0 stable, Silero VAD and ElevenLabs WS TTS now core; moves fast, pin and gate upgrades through the latency harness
- @pipecat-ai/client-js 1.12.0 + small-webrtc-transport 1.10.5: browser client — pin both exactly (released independently)
- Deepgram Nova-3 / Claude Haiku 4.5 (`claude-haiku-4-5`) / ElevenLabs Flash v2.5 (`eleven_flash_v2_5`): the STT/LLM/TTS cascade — all current, all the right latency/cost tier
- PyJWT 2.13 + PyJWKClient: offline OIDC token validation in the voice service (not python-jose — stale/CVE history)
- Next.js 16.2.x + next-auth 5.0.0-beta.31 (exact pin) + oidc-provider 9.8.6 + ElectroDB 3.9.1: auth port — match run.auth's majors during the port, upgrade separately; oidc-provider v9 is ESM-only
- Go 1.26 + cobra v1.10.2 (+ optional lipgloss v1): `kv` CLI matching `km` structurally; skip bubbletea, avoid charm v2 betas
- Terraform/Terragrunt at defcon.run.34 pins; SOPS → SSM SecureString for secrets

### Expected Features

The bar is set by ElevenLabs Conversational AI, OpenAI Advanced Voice, Hume EVI, and Sesame. Missing table stakes reads as hackathon, not product.

**Must have (table stakes):**
- Gesture-gated mic flow with distinct error states (denied/no-device/in-use) — first-impression gate
- Explicit connection state machine + fast, specific ICE-failure/UDP-blocked messaging — conference Wi-Fi reality
- Barge-in with tuned endpointing (`stop_secs` 0.2–0.5s sweep) — the number-one "is this real?" test
- Live captions + state-aware visualization (waveform floor, orb standard) — all ride the same RTVI event stream
- Session countdown timer, mute toggle, clean end + one-click quota-checked reconnect
- In-session context memory (free in a cascaded pipeline — full history in Claude context)
- Access codes → tiers → quotas + site-wide kill switch; `kv` code CRUD / tier set / usage / killswitch — the minimum operator loop

**Should have (differentiators):**
- Spoken quota wind-down ("about 30 seconds left…") — no reference demo does this; turns the constraint into charm
- Deeply personal concierge persona (fat versioned prompt) — content quality is the work, not code
- Latency HUD debug overlay — doubles as the tuning instrument; a "whoa" for a DEF CON-adjacent audience
- Frictionless code handout flow (any code accepted, known codes unlock tiers) — under a minute from card to conversation

**Defer (v2+):**
- TURN fallback (documented "use a hotspot" error message is v1) — first post-v1 enhancement
- Smart turn detection and instant-acknowledgment masking — v1.x, only if local A/B wins on feel within budget (they compete for the same latency budget — tune together)
- Anti-features to resist: web admin dashboard, text-chat fallback, tool calling, RAG, audio recording storage, cross-session memory, voice picker, animated avatar

### Architecture Approach

Two web services behind one ALB (host routing `auth.*` / `voice.*`) with WebRTC media deliberately bypassing the ALB — UDP flows directly browser↔Fargate task public IP. The voice service is multi-session per task (~5 in-process Pipecat pipelines per 1 vCPU/2 GB task), never bot-per-task (Fargate cold start would kill the tap-mic-and-talk feel). Auth is consulted zero times per session at runtime: tier claims ride in the signed JWT access token, validated offline via cached JWKS; quota state lives in DynamoDB enforced by single-round-trip conditional writes (`ADD seconds_used :15` with `ConditionExpression seconds_used < :cap`). Monorepo with native per-language tooling (uv/npm/go), no meta-build-system.

**Major components:**
1. `apps/voice` (Python/FastAPI + Pipecat) — `/api/offer` signaling, offline JWT validation, quota gate + 15s ticks + spoken wind-down, session lifecycle, ECS task scale-in protection
2. `apps/auth` (Next.js, run.auth port) — magic link via SES, access-code→tier resolution, embedded OIDC issuer minting JWT access tokens with tier claims
3. Browser client — mic capture, OIDC flow, SDP offer, captions/timer/orb from RTVI events, reconnect UX
4. DynamoDB — users, access_codes, tiers, usage counters, concurrency markers, GLOBAL kill-switch item
5. `cli/kv` (Go) — operator plane hitting DynamoDB/AWS APIs directly (works even if both services are down)
6. `infra/terraform` — defcon.run.34 clone; the only novel module work is public-IP tasks + UDP SG ingress

**Key cross-file reconciliation (architecture wins over pitfalls-file assumptions — verified in source):**
- Public-IP advertisement: use server-side STUN (`SmallWebRTCConnection(ice_servers=[stun])`) — aiortc gathers a srflx candidate that IS the task's public IP via the ENI's 1:1 NAT. There is no host-IP override or candidate-injection hook in SmallWebRTC/aioice; ECS-metadata IP discovery is for logging only (and task metadata v4 doesn't even include the public IP).
- UDP port range: aiortc/aioice cannot be bound to a port range (sockets bind port 0; aiortc #487 open). The spec's bounded 20000–20100 SG rule will cause intermittent ICE failures. Ship v1 with the SG widened to the ephemeral UDP range on the single-purpose task SG; investigate ECS `systemControls` sysctl during the infra phase.

### Critical Pitfalls

1. **Latency dies in defaults, not vendors** — build the latency harness first; set `aggregation_timeout≈0.3` (the 1.0s default is the single most-reported "why is my bot slow" cause); pick ONE turn-end owner (Silero `stop_secs` OR Deepgram endpointing, never both); sentence-chunk LLM→TTS; stay in us-east-1.
2. **Works on laptop, dead on Fargate (private-IP ICE candidates)** — STUN-configured transport from day one so local ≈ prod; automated deployed smoke test asserting ICE reaches `connected` and RTP flows, not just `/api/offer` 200.
3. **Half-working barge-in** — TTS keeps streaming if interrupted before `BotStartedSpeakingFrame` (pipecat #3986); interrupted context must truncate to what was actually spoken or conversations go subtly insane. Named interruption test scenarios in the harness; pin Pipecat; freeze versions ≥2 weeks before the conference.
4. **Abandoned sessions burn money** — Pipecat idle detection has documented reliability holes (#3140, #3179). Four layers: `PipelineIdleDetection` (60–120s), disconnect handlers with asyncio-timeout-wrapped cleanup, an independent server-side wall-clock enforcing `session_max_seconds`, and TTL/heartbeat concurrency markers (the 15s tick doubles as heartbeat).
5. **Magic links eaten by scanners + SES sandbox** — request SES production access in week 1; SPF/DKIM/DMARC before first send; interstitial confirm-click page so scanners' GETs don't consume the token; test from an Outlook/SafeLinks mailbox.
6. **Quota races** — every limit (per-user seconds, concurrency, global budget) enforced in the write via ConditionExpression; unit-test the two-concurrent-starts race explicitly.

Moderate but roadmap-relevant: echo/self-interruption on speakerphone (tune with speakers, not headphones; gate mic ~1–2s post-connect for AEC adaptation), iOS Safari audio unlock (everything in the gesture handler; test on a real iPhone early), ALB 60s idle timeout on any control channel, ElevenLabs credit burn from verbose persona + interrupted synthesis (hard-constrain response length — also a latency win; derive kill-switch cap from the ElevenLabs plan).

## Implications for Roadmap

Based on research, suggested phase structure (refines the spec's three-subproject order into five phases; Phase 1 and Phase 2 start in parallel):

### Phase 1: Local Pipeline + Latency Harness (parallel track, day one)
**Rationale:** Zero infra dependency (three API keys only); de-risks the entire core value — if the conversational feel isn't there, nothing else matters. The harness and pins made here become the regression gate for every later change.
**Delivers:** Latency harness (scripted WAV → per-stage ms → voice-to-voice), tuned pipeline (aggregation_timeout, single endpointing owner, sentence-chunked TTS), named barge-in test scenarios, pinned versions + pipeline-config doc, persona prompt v1 with length constraints, STUN-configured SmallWebRTCConnection so local ≈ prod transport, STT stage config-swappable for a Flux A/B.
**Addresses:** Barge-in, tuned endpointing, in-session memory, persona quality (FEATURES P1s).
**Avoids:** Pitfalls 1, 3, 7, 11, 12.

### Phase 2: Infra Skeleton
**Rationale:** SES production access is a multi-day manual review — the request must go out in week one or auth can't launch. The WebRTC delta (public-IP tasks + UDP SG) is the only novel module work in an otherwise reuse-heavy defcon.run.34 clone.
**Delivers:** network/certs/ecr/dynamodb/secrets/email/github-oidc/ecs-cluster modules; SES prod-access request + SPF/DKIM/DMARC; ALB idle timeout ≥ max session length; the UDP SG port-range decision resolved (default: widened ephemeral range).
**Uses:** Terraform/Terragrunt at defcon.run.34 pins, SOPS→SSM.
**Avoids:** Pitfalls 2 (SG half), 5 (SES half), 9.

### Phase 3: Auth Service (run.auth port)
**Rationale:** Depends on infra (DNS/SES/DynamoDB); gates the voice deploy because it owns the token contract. The JWT-access-token decision (oidc-provider Resource Indicators + `accessTokenFormat: 'jwt'` + tier claims + voice audience) must land HERE — it is the contract voice blocks on.
**Delivers:** Magic-link login with interstitial confirm page, access_codes/tiers tables + code→tier resolution, OIDC issuer emitting JWT access tokens with tier claims, JWKS endpoint; quota schema with conditional-write patterns and slot TTL/heartbeat design; race unit tests. First `kv` commands (code CRUD, tier set) as soon as tables exist.
**Implements:** apps/auth component + DynamoDB schema (ARCHITECTURE).
**Avoids:** Pitfalls 5 (scanner links), 6 (quota races).

### Phase 4: Voice Service Deployed + Client
**Rationale:** Depends on infra + auth's token contract. First real end-to-end UDP test through the SG happens here — budget explicit time for ICE debugging.
**Delivers:** `/api/offer` with offline JWT validation (cached JWKS) + quota gate; 15s conditional-write ticks + spoken wind-down; four-layer session teardown; ECS task protection + ActiveSessions-based scaling; browser client (mic flow, state machine, captions, orb/waveform, timer, reconnect, fast ICE-fail messaging, iOS gesture-scoped audio unlock); deployed ICE/RTP smoke test; `kv sessions` + killswitch + usage telemetry.
**Uses:** pipecat 1.5.0 pipeline from Phase 1, PyJWT/PyJWKClient, client-js + small-webrtc-transport.
**Avoids:** Pitfalls 2, 4, 8, 10, 13, 16.

### Phase 5: Conference Hardening + v1.x Polish
**Rationale:** The "only reproducible deployed/on-device" checklist (Pitfall 15) needs a dedicated deployed milestone well before the event: real iPhone, speakerphone at volume, hotel-Wi-Fi-style network, Outlook mailbox.
**Delivers:** UAT pass on the device/network checklist; version freeze (≥2 weeks out); ops runbook (deploys off-hours, hotspot at booth); ElevenLabs usage alerts + budget cap tied to plan; optional v1.x items that won their A/Bs (smart turn, acknowledgment masking, latency HUD as visitor-facing toggle, orb upgrade).

### Phase Ordering Rationale

- Phase 1 ∥ Phase 2 from day one: the local track needs no AWS; the SES clock needs to start immediately.
- Auth before voice because the JWT/tier-claims contract is voice's hard dependency (Pattern 4 prerequisite) — deciding it late forces either rework or the per-session introspection round-trip the design explicitly rejects.
- `kv` has no phase of its own: it depends only on DynamoDB schemas and AWS APIs, so it grows incrementally inside Phases 3–4 (code CRUD when access_codes exists, session visibility after voice deploys) and works as an emergency tool even when both services are down.
- Client polish (captions, orb, HUD) is grouped in Phase 4 because all three consume the same RTVI event stream — plumb once, build three.

### Research Flags

Phases likely needing deeper research during planning (`/gsd-plan-phase --research-phase`):
- **Phase 2 (infra):** Fargate `systemControls` sysctl support for `net.ipv4.ip_local_port_range` (the only path to a bounded UDP SG) — unconfirmed in this pass.
- **Phase 3 (auth):** oidc-provider v9 Resource Indicators + custom tier-claims configuration specifics, and v8/CJS→v9/ESM implications for the run.auth port.
- **Phase 4 (voice):** version-sensitive, verify at build time against the pinned Pipecat release: (a) exact SmallWebRTC ICE configuration surface (STUN srflx behavior on Fargate), (b) interruption + word-timestamp context-truncation behavior, (c) idle-detection reliability workarounds.

Phases with standard patterns (skip research-phase):
- **Phase 1 (local pipeline):** Pipecat quickstart territory; the work is tuning, not research — the harness answers questions empirically.
- **Phase 5 (hardening):** checklist-driven UAT; all inputs already enumerated in PITFALLS.md.

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | MEDIUM | Version pins drawn directly from registry metadata (most reliable data here); ecosystem judgments (Flux positioning, base-image guidance) cross-checked vendor + independent sources |
| Features | MEDIUM | Cross-checked across ≥5 reference products; latency-perception thresholds consistent across ≥4 independent sources |
| Architecture | MEDIUM overall, LOW on Pipecat/aiortc internals | AWS/OIDC/DynamoDB patterns cross-verified; SmallWebRTC/aioice findings verified directly in source but single-sourced |
| Pitfalls | MEDIUM | Cross-verified against official docs and primary GitHub issues; several are version-sensitive and will age |

**Overall confidence:** MEDIUM

### Gaps to Address

- **STUN srflx on Fargate unproven end-to-end:** the architecture's STUN mechanism is source-verified but not live-tested behind Fargate's 1:1 NAT; PITFALLS notes aiortc srflx quirks behind NAT. Handle: make the deployed ICE smoke test the first Phase 4 deliverable; keep the aioice monkey-patch as the documented fallback.
- **UDP port bounding:** no confirmed way to bound aiortc's ports; v1 default is a widened SG. Handle: Phase 2 research on Fargate sysctls; accept widened range if unsupported.
- **oidc-provider JWT access tokens with tier claims:** pattern identified but exact v9 configuration not prototyped. Handle: spike early in Phase 3; introspection endpoint is the (design-violating) fallback, so de-risk first.
- **run.auth current state unknown:** which next-auth beta, oidc-provider major (v8/CJS vs v9/ESM), and adapter it uses determines port scope. Handle: audit the defcon.run.34 source at Phase 3 start; port first, upgrade second.
- **Pipecat version sensitivity:** interruption, idle detection, and aggregator behavior are all pinned-version-specific. Handle: pipeline-config doc + harness regression gate on any bump; freeze before the conference.

## Sources

### Primary (HIGH confidence)
- Package registries queried 2026-07-04 (PyPI, npm, Go module proxy): all version pins in STACK.md
- Pipecat/aioice source (read directly): SmallWebRTCConnection constructor surface, aioice port binding — single-sourced, seam-tagged LOW despite being primary
- Primary GitHub issues: pipecat #3986/#3985/#3140/#3179/#2460, aiortc #487, next-auth #4965/#1840

### Secondary (MEDIUM confidence)
- Official docs cross-verified: Pipecat (transports, idle detection, deployment patterns), Deepgram (Flux/Nova-3, endpointing), ElevenLabs (Flash v2.5, pricing), AWS (ECS task protection, Fargate metadata v4, DynamoDB conditional writes, SES production access), node-oidc-provider README
- Reference-product analysis: ElevenLabs UI/Agents docs, OpenAI Realtime docs, Hume EVI, Sesame writeups, Vapi/Retell comparisons
- Latency benchmarks: consistent across ≥4 independent sources (Hamming, Telnyx, Trillet, LiveKit, Smallest.ai)

### Tertiary (LOW confidence, needs validation)
- Anthropic model catalog (claude-api skill cache, 2026-06) — re-verify model id at build time
- Fargate sysctl port-range support — unverified, flagged for Phase 2 research

Full detail: STACK.md, FEATURES.md, ARCHITECTURE.md, PITFALLS.md in this directory.

---
*Research completed: 2026-07-04*
*Ready for roadmap: yes*
