<!-- GSD:project-start source:PROJECT.md -->

## Project

**klanker-voice**

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

**Core Value:** The conversation must feel *slick*: ≤1.2s voice-to-voice latency with natural barge-in and
ElevenLabs-quality speech — a demo that makes people say "whoa" in the first ten seconds.

### Constraints

- **Budget**: ~$120–165/mo conference-ready (ElevenLabs Pro $99 + Fargate/ALB/usage); ~$85/mo off-season — quotas and kill-switch bound API burn
- **Latency**: ≤1.2s voice-to-voice; every pipeline stage must stream
- **Tech stack**: Pipecat (Python) for the pipeline; Next.js for auth (run.auth port); Go for `kv`; terraform/terragrunt matching defcon.run.34 conventions
- **Security**: public mic wired to metered APIs — every session must be quota-gated via OIDC token claims
- **Naming**: "klanker-voice" everywhere; never "voiceai" (copyright)

<!-- GSD:project-end -->

<!-- GSD:stack-start source:research/STACK.md -->

## Technology Stack

## Recommended Stack

### Voice service (Python / Pipecat)

| Technology | Version | Purpose | Why |
|------------|---------|---------|-----|
| Python | **3.12** | Runtime | pipecat-ai 1.5.0 requires `>=3.11`; 3.12 matches the official pipecat base image default and has the broadest wheel coverage (onnxruntime, aiortc, numba). 3.13 works (via `audioop-lts`) but buys nothing here. |
| pipecat-ai | **1.5.0** (pin `~=1.5.0`) | Pipeline framework | Current release (2026-07-04). 1.x line is post-1.0 stable API. Silero VAD (`onnxruntime~=1.24.3`) and ElevenLabs WS TTS (`websockets>=13.1`) are now **core** dependencies — no extra needed. |
| pipecat-ai extras | `[webrtc,deepgram,anthropic,runner]` | Provider plugins | `webrtc` → aiortc + opencv; `deepgram` → deepgram-sdk `>=6.1.1,<8`; `anthropic` → anthropic SDK `>=0.49,<1`; `runner` → fastapi + uvicorn + dev runner. Add `local` (pyaudio) **only** in the dev environment for laptop mic testing. |
| aiortc | **1.14.0** (transitively pinned) | WebRTC media (SmallWebRTC) | Latest release (2025-10-13); exactly the floor pipecat 1.5.0 declares (`aiortc>=1.14.0,<2`). Don't pin separately — let pipecat's constraint govern. |
| FastAPI / uvicorn | **0.139.x / 0.50.x** | `/api/offer` signaling endpoint + health checks | Pulled by the `runner`/`websocket` extras; the standard pipecat signaling shape. |
| PyJWT | **2.13.0** | OIDC access-token validation in the voice service | Validate tokens from auth.klankermaker.ai locally (JWKS via `PyJWKClient`, issuer/audience checks) — no per-session auth round-trip, as the design requires. Prefer over `python-jose` (stale maintenance) and `authlib` (overkill for verify-only). |
| Deepgram Nova-3 | model id `nova-3` | Streaming STT | Committed; still current flagship, ~300ms partials, built-in endpointing, $0.0077/min. See Flux note under Alternatives. |
| Claude Haiku 4.5 | model id **`claude-haiku-4-5`** | LLM | Verified current and active (full id `claude-haiku-4-5-20251001`; 200K context, 64K max output, $1/$5 per MTok). Fastest/cheapest Anthropic tier — right latency/cost point for a conversational turn loop. Config-swappable per design. |
| ElevenLabs Flash v2.5 | model id `eleven_flash_v2_5` | Streaming TTS (WebSocket) | Verified current: ~75ms first-audio, and it is ElevenLabs' own default for their Agents Platform. Eleven v3 is more expressive but **not real-time** — wrong tool for this project. No Python SDK needed; pipecat's ElevenLabs WS service uses core `websockets`. |
| Silero VAD | bundled (`SileroVADAnalyzer`) | On-task turn detection | Now in pipecat core (onnxruntime base dep). Zero extra install, CPU-cheap, the de facto standard for cascaded pipelines. |

### Container image

| Choice | Recommendation | Why |
|--------|----------------|-----|
| Base image | **`python:3.12-slim` (bookworm) + `uv` for installs** | Do **not** use `dailyco/pipecat-base` — it targets Pipecat Cloud (expects a `bot.py` + their runtime interface and process-per-session orchestration). Self-hosted Fargate with SmallWebRTC needs your own FastAPI/uvicorn entrypoint. `-slim` needs `apt-get install`: `libopus0`, `libvpx7`, `ffmpeg` (aiortc/audio deps) — budget for that in the Dockerfile. |
| Arch | linux/amd64 (Fargate x86) or arm64 (Graviton, ~20% cheaper) | onnxruntime + aiortc both publish arm64 wheels; Graviton Fargate is a safe cost win — verify once in staging. |

### Frontend client

| Technology | Version | Purpose | Why |
|------------|---------|---------|-----|
| @pipecat-ai/client-js | **1.12.0** | Browser client SDK (RTVI) | Current (2026-06-18). Handles mic capture, transport lifecycle, transcript/bot-speaking events for captions. |
| @pipecat-ai/small-webrtc-transport | **1.10.5** | Browser↔task WebRTC transport | Current (2026-06-19). Pairs with server-side SmallWebRTCTransport; POSTs SDP offer to `/api/offer`. Versions of client-js and the transport package are released independently — pin both exactly. |

### Auth service (Next.js port of run.auth)

| Technology | Version | Purpose | Why |
|------------|---------|---------|-----|
| Node.js | **22 LTS** | Runtime | Next 16 requires `node>=20.9`; oidc-provider v9 is ESM-only and targets 20.19+/22.x. 22 is the active LTS through the project window. |
| Next.js | **16.2.x** (16.2.10 current) | Auth app framework | Current stable line. Since this is a *port* of run.auth, match its major if it's on 15.x and upgrade deliberately — don't mix a Next major bump into the port. If starting the app shell fresh, use 16.2.x. |
| next-auth | **5.0.0-beta.31** (pin exact) | Magic-link email auth | v5 is *still beta* (beta.31, 2026-04-14 — betas ship ~2/year, it's stable-in-practice and the only line that works with App Router). run.auth already runs v5 beta in production at DEF CON — that's the proof. Pin the exact beta; betas can break between releases. Do NOT "upgrade" to latest v4 (4.24.14) — different API, pages-router era. |
| oidc-provider | **9.8.6** | Embedded OIDC issuer | Current (2026-06-26). ESM-only (`type: module`) — if run.auth is on v8/CJS, keep its working version for the port and upgrade as a separate task; v9 in a Next.js custom-route context needs ESM care. |
| ElectroDB | **3.9.1** | DynamoDB single-table modeling | Current; run.auth already uses it. Keep — don't switch to @auth/dynamodb-adapter's table layout since access-codes/tiers/usage tables want ElectroDB entities anyway. |
| @auth/dynamodb-adapter | 2.11.2 (only if run.auth uses it) | next-auth DynamoDB persistence | Port whatever run.auth uses. If it has a custom ElectroDB adapter, keep that. |
| nodemailer + SES | latest | Magic-link delivery | Ported unchanged from run.auth. |
| Altcha | latest | Login captcha | Ported unchanged from run.auth. |

### `kv` CLI (Go)

| Technology | Version | Purpose | Why |
|------------|---------|---------|-----|
| Go | **1.26.x** (1.26.4 current) | Toolchain | Current stable line; set `go 1.26` in go.mod. |
| spf13/cobra | **v1.10.2** | Command tree | The standard; matches `km` so the two CLIs stay structurally identical — this is the deciding factor, not fashion. |
| spf13/viper | v1.21.0 | Config/env (only if `km` uses it) | Mirror `km`'s config approach exactly. |
| aws-sdk-go-v2 | **v1.42.x** + per-service modules (dynamodb, ecs, ssm, cloudwatchlogs) | Access-code CRUD, usage inspection, session visibility, deploy helpers | v2 is the only maintained line. |
| charmbracelet/lipgloss | **v1.1.0** (optional) | Pretty table/status output | Nice-to-have for `kv usage`/`kv sessions` output. Stay on **v1** — the v2 line is still in beta/experimental. |
| charmbracelet/fang | v1.0.0 (optional) | Cobra styling/batteries (styled help, errors, version) | Now 1.0; cheap polish on top of cobra with zero structural change. Adopt only if `km` does or you want the two CLIs restyled together. |

### Infrastructure

| Technology | Version | Purpose | Why |
|------------|---------|---------|-----|
| Terraform + Terragrunt | match defcon.run.34 pins | IaC | Reuse-heavy port; do not bump majors during the port. The only new infra: Fargate tasks with public IPs + SG ingress for a bounded UDP range (e.g. 20000–20100) for WebRTC media. |
| SOPS → SSM SecureString | as in defcon.run.34 | Secrets (Deepgram, Anthropic, ElevenLabs keys; auth signing keys) | Proven pattern, containers consume via `valueFrom`. |

## Installation

# Voice service (Python 3.12, uv)

# dev-only (laptop mic):

# Frontend client

# Auth service (in the ported run.auth app)

# kv CLI

## Alternatives Considered / What NOT to Use

| Category | Recommended | Alternative | Why Not (or: when to reconsider) |
|----------|-------------|-------------|----------------------------------|
| STT | Deepgram Nova-3 (committed) | **Deepgram Flux** | Not "why not" — **flag for A/B**. Flux is Deepgram's 2026 conversational-STT model with model-integrated end-of-turn detection (median EOT <300ms), and the ecosystem now treats Nova-3+VAD → Flux as the voice-agent upgrade path; it can shave 200–600ms off turn latency by replacing VAD/endpointing-based turn detection. Same vendor, same API key. Keep Nova-3 as the committed baseline; make the STT service a config switch and try Flux during local tuning. |
| TTS | ElevenLabs Flash v2.5 | Eleven v3 | v3 is more expressive but **not real-time** (no published latency; positioned for pre-rendered audio). Wrong for a ≤1.2s loop. |
| TTS | ElevenLabs Flash v2.5 | Turbo v2.5 | Higher quality but ~250-300ms first-audio vs ~75ms; Flash is ElevenLabs' own Agents default. |
| LLM | Claude Haiku 4.5 | Sonnet-tier models | Higher TTFT and 3–5× the cost per turn; Haiku is the right conversational tier. Design already keeps this config-swappable. |
| Base image | python:3.12-slim | `dailyco/pipecat-base` | Built for Pipecat Cloud's runtime contract (bot.py entrypoint, their session orchestration). On self-hosted Fargate it fights your FastAPI/SmallWebRTC entrypoint. |
| Transport | SmallWebRTC (committed) | Daily transport / LiveKit | Both are excellent but per-minute/infra cost defeats the $0/min transport goal; SmallWebRTC is explicitly designed for this direct-to-task pattern. Revisit only if TURN fallback becomes mandatory. |
| Voice pipeline | Pipecat cascaded | OpenAI Realtime / Gemini Live / Amazon Nova Sonic speech-to-speech APIs | Single-vendor lock-in, less control over each stage, and it abandons the HF-cascade fidelity that is the point of the project. |
| Python JWT | PyJWT 2.13 | python-jose | Effectively unmaintained; had CVEs (algorithm confusion) historically. PyJWT + PyJWKClient is the maintained standard. |
| Auth | next-auth 5.0.0-beta.31 (exact pin) | next-auth 4.24.14 "stable" | v4 is the legacy pages-router line; the App Router run.auth port is v5-shaped. Also do not float `^5.0.0-beta` — pin the exact beta. |
| OIDC issuer | oidc-provider 9.8.6 | Ory Hydra / Keycloak / Auth0 | External infra or SaaS cost for one client app; run.auth's embedded issuer is proven at DEF CON. |
| Go CLI framework | cobra v1.10.2 | urfave/cli, kong | Nothing wrong with them, but `km` is cobra — consistency across the klanker tooling family wins. |
| Go TUI | none (lipgloss tables at most) | bubbletea | Operator CLI needs scriptable output, not an event loop. Also avoid charm **v2 betas** (bubbletea v2, lipgloss v2) — unstable APIs. |
| VAD | Silero (pipecat core) | WebRTC VAD, Krisp | Silero is now zero-install in pipecat 1.5.0 and is the accuracy standard; Krisp adds licensing cost. |
| Smart turn detection | Silero VAD (v1) | pipecat `local-smart-turn` (smart-turn model) | Pulls torch/transformers (~2GB image bloat) for marginal gain at this scale; if turn-taking feels off in tuning, try Deepgram Flux first — it solves the same problem server-side with no image bloat. |

## Version-Pin Gotchas (things that will bite)

## Sources

- PyPI registry metadata (queried 2026-07-04): pipecat-ai 1.5.0, aiortc 1.14.0, anthropic 0.116.0, deepgram-sdk 7.4.0, pyjwt 2.13.0, fastapi 0.139.0, uvicorn 0.50.0
- npm registry metadata (queried 2026-07-04): @pipecat-ai/client-js 1.12.0, @pipecat-ai/small-webrtc-transport 1.10.5, next 16.2.10, next-auth 4.24.14 / 5.0.0-beta.31, oidc-provider 9.8.6, electrodb 3.9.1, @auth/dynamodb-adapter 2.11.2
- Go module proxy + go.dev/dl (queried 2026-07-04): go1.26.4, cobra v1.10.2, viper v1.21.0, bubbletea v1.3.10, lipgloss v1.1.0, bubbles v1.0.0, fang v1.0.0, aws-sdk-go-v2 v1.42.1
- Anthropic model catalog (claude-api skill, cached 2026-06): claude-haiku-4-5 active, 200K ctx, 64K out, $1/$5 per MTok
- Pipecat docs: SmallWebRTCTransport reference (docs.pipecat.ai), pipecat-cloud-images README (dailyco/pipecat-base scope)
- Deepgram docs: Flux vs Nova-3 comparison, Nova-3→Flux migration guide (developers.deepgram.com)
- ElevenLabs docs + 2026 comparisons: Flash v2.5 ~75ms and Agents Platform default; v3 not real-time

<!-- GSD:stack-end -->

<!-- GSD:conventions-start source:CONVENTIONS.md -->

## Conventions

Conventions not yet established. Will populate as patterns emerge during development.
<!-- GSD:conventions-end -->

<!-- GSD:architecture-start source:ARCHITECTURE.md -->

## Architecture

Architecture not yet mapped. Follow existing patterns found in the codebase.
<!-- GSD:architecture-end -->

<!-- GSD:skills-start source:skills/ -->

## Project Skills

No project skills found. Add skills to any of: `.claude/skills/`, `.agents/skills/`, `.cursor/skills/`, `.github/skills/`, or `.codex/skills/` with a `SKILL.md` index file.
<!-- GSD:skills-end -->

<!-- GSD:workflow-start source:GSD defaults -->

## GSD Workflow Enforcement

Before using Edit, Write, or other file-changing tools, start work through a GSD command so planning artifacts and execution context stay in sync.

Use these entry points:

- `/gsd-quick` for small fixes, doc updates, and ad-hoc tasks
- `/gsd-debug` for investigation and bug fixing
- `/gsd-execute-phase` for planned phase work

Do not make direct repo edits outside a GSD workflow unless the user explicitly asks to bypass it.
<!-- GSD:workflow-end -->

<!-- GSD:profile-start -->

## Developer Profile

> Profile not yet configured. Run `/gsd-profile-user` to generate your developer profile.
> This section is managed by `generate-claude-profile` -- do not edit manually.
<!-- GSD:profile-end -->
