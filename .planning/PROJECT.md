# klanker-voice

## What This Is

A conference-ready, public speech-to-speech voice agent at **voice.klankermaker.ai** — a
browser page where you tap a mic button and hold a natural, low-latency conversation with
the "KlankerMaker concierge" (an agent that knows Kurt, the klanker platform, defcon.run,
and his repos). Built as a cascaded HF-style pipeline (VAD → STT → LLM → TTS) with
best-in-class hosted APIs, gated by a new magic-link/OIDC identity service at
**auth.klankermaker.ai** with an access-code → tier → quota system, and operated via a
`kv` Go CLI (sibling to klanker-maker's `km`).

**Authoritative design spec:** `docs/superpowers/specs/2026-07-04-klanker-voice-design.md`
(interview-validated and approved 2026-07-04). Naming note: the term "voiceai" is avoided
entirely for copyright reasons — the project is **klanker-voice**.

## Core Value

The conversation must feel *slick*: ≤1.2s voice-to-voice latency with natural barge-in and
ElevenLabs-quality speech — a demo that makes people say "whoa" in the first ten seconds.

## Requirements

### Validated

(None yet — ship to validate)

### Active

- [ ] Browser client at voice.klankermaker.ai: mic button → live conversation with captions, waveform, session timer
- [ ] Pipecat cascaded pipeline: Silero VAD → Deepgram Nova-3 streaming STT → Claude Haiku 4.5 → ElevenLabs Flash v2.5, all stages streaming, barge-in support
- [ ] SmallWebRTC transport: signaling via ALB, media UDP direct browser↔Fargate task (zero per-minute transport cost)
- [ ] auth.klankermaker.ai: magic-link email (SES) + OIDC provider, ported from defcon.run.34 run.auth
- [ ] Access codes: any code accepted at login; known codes map to tiers (e.g. demo→2min, kphdemo123→30min); unknown → no-access tier
- [ ] Quota system: DynamoDB tiers/usage tables, per-session and per-day caps, concurrency limits, site-wide daily budget kill-switch, spoken wind-down
- [ ] Terragrunt infra skeleton (site "kmk"): cloned defcon.run.34 layout — network, certs, ecs-cluster, ecr, dynamodb, secrets, email, github-oidc, ecs-task, ecs-service
- [ ] `kv` Go CLI: operator tooling for klanker-voice (access-code CRUD, quota/usage inspection, session visibility, deploy/smoke-test helpers)
- [ ] KlankerMaker concierge persona via versioned markdown system prompt
- [ ] Local-first pipeline development: bot runs on a laptop with three API keys for fast conversational-feel tuning

### Out of Scope

- RAG/knowledge retrieval — fat system prompt is sufficient for v1
- Agent tool-calling — round-trip pauses undercut the slick feel; revisit post-v1
- TURN fallback for UDP-blocked networks — documented limitation in v1
- Multi-region / CloudFront in front of voice — single us-east-1 region is enough
- Self-hosted/local models — hosted APIs beat local quality/latency per dollar
- Registering klankervoice.ai — possible future; v1 uses voice.klankermaker.ai
- The name "voiceai" anywhere — copyright concerns

## Context

- **Reuse-heavy project:** terragrunt skeleton, versioned modules, SOPS→SSM secrets, and
  the run.auth app all port from `/Users/khundeck/working/defcon.run.34` (proven at DEF CON).
- **AWS:** app resources in klanker-application (052251888500); DNS zone klankermaker.ai
  (Z036807010CWM2JH60RKQ) in klanker-management account (481723467561); state via
  klanker-terraform. `TF_VAR_profile_prefix=klanker-`.
- **Only genuinely new components:** the Pipecat voice service, the WebRTC public-IP/UDP
  infra delta, the access-code/quota layer, and the `kv` CLI.
- **klanker tooling family:** klanker-maker ships `km`; klanker-voice ships `kv` (Go),
  giving a consistent two-CLI operator experience.
- **Three subproject tracks:** 1) infra skeleton, 2) auth service, 3) voice service —
  with local pipeline tuning running in parallel from day one.

## Constraints

- **Budget**: ~$120–165/mo conference-ready (ElevenLabs Pro $99 + Fargate/ALB/usage); ~$85/mo off-season — quotas and kill-switch bound API burn
- **Latency**: ≤1.2s voice-to-voice; every pipeline stage must stream
- **Tech stack**: Pipecat (Python) for the pipeline; Next.js for auth (run.auth port); Go for `kv`; terraform/terragrunt matching defcon.run.34 conventions
- **Security**: public mic wired to metered APIs — every session must be quota-gated via OIDC token claims
- **Naming**: "klanker-voice" everywhere; never "voiceai" (copyright)

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Cascaded pipeline w/ hosted APIs (not realtime-API vendor, not self-hosted) | HF-concepts fidelity + best quality/latency per dollar | — Pending |
| Pipecat SmallWebRTC direct-to-task (no SFU/TURN vendor) | $0/min transport, echo cancellation, barge-in for free | — Pending |
| Deepgram Nova-3 / Claude Haiku 4.5 / ElevenLabs Flash v2.5 | Streaming-first vendors; TTS quality is the differentiator | — Pending |
| Port run.auth instead of new auth | Proven magic-link/OIDC/DynamoDB base | — Pending |
| Access codes map to tiers; any code accepted at login | Frictionless demo handout with quota control | — Pending |
| `kv` Go CLI for operations | Consistent klanker tooling family alongside `km` | — Pending |
| Project name klanker-voice | Avoid "voiceai" copyright issues | ✓ Good |

## Evolution

This document evolves at phase transitions and milestone boundaries.

**After each phase transition** (via `/gsd-transition`):
1. Requirements invalidated? → Move to Out of Scope with reason
2. Requirements validated? → Move to Validated with phase reference
3. New requirements emerged? → Add to Active
4. Decisions to log? → Add to Key Decisions
5. "What This Is" still accurate? → Update if drifted

**After each milestone** (via `/gsd-complete-milestone`):
1. Full review of all sections
2. Core Value check — still the right priority?
3. Audit Out of Scope — reasons still valid?
4. Update Context with current state

---
*Last updated: 2026-07-04 after initialization*
