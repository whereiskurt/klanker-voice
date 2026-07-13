# Requirements: klanker-voice

**Defined:** 2026-07-04
**Core Value:** The conversation must feel slick — ≤1.2s voice-to-voice (tuning toward ~800ms typical), natural barge-in, ElevenLabs-quality speech.

## v1 Requirements

### Voice Pipeline

- [ ] **PIPE-01**: User can hold a spoken conversation with the agent at ≤1.2s voice-to-voice latency (tuned toward ~800ms typical)
- [x] **PIPE-02**: User can interrupt the agent mid-speech; playback stops promptly and conversation context is truncated to words actually spoken
- [ ] **PIPE-03**: Agent remembers the full conversation within a session (in-context history)
- [ ] **PIPE-04**: STT/LLM/TTS stages are config-swappable; endpointing A/B (Deepgram Flux vs Nova-3+VAD; SmartTurn) run during tuning with measured verdicts
- [ ] **PIPE-05**: Latency harness measures per-stage and voice-to-voice ms from recorded audio, locally and against staging
- [x] **PIPE-06**: Agent speaks as the KlankerMaker concierge (knows Kurt, klanker platform, defcon.run, repos) via a versioned markdown system prompt
- [x] **PIPE-07**: Developer can run the full bot locally with only the three provider API keys
- [x] **PIPE-10**: RAG/knowledge retrieval (promoted from v2-deferred in Phase 7: router + curated per-topic packs + local keyless SQLite FTS5/BM25 retrieval, Amendment 1/3)

### Browser Client

- [x] **CLNT-01**: User grants mic via a gesture-gated flow with distinct error states (denied / no device / unsupported browser)
- [x] **CLNT-02**: User sees a connection state machine with clear messaging for ICE failure / UDP-blocked networks, with auto-retry
- [x] **CLNT-03**: User sees live captions for both sides of the conversation
- [x] **CLNT-04**: User sees a state-aware orb visualization (listening / thinking / speaking)
- [x] **CLNT-05**: User sees a visible session countdown timer
- [x] **CLNT-06**: User can toggle a latency HUD showing per-stage pipeline latency (also serves as the tuning instrument)
- [x] **CLNT-07**: User gets a clean session end and one-click reconnect that re-checks quota before reconnecting
- [x] **CLNT-08**: User signs in via OIDC redirect to auth.klankermaker.ai before the mic is available

### Auth & Access Codes

- [ ] **AUTH-01**: User can sign in via magic-link email (SES), with an interstitial confirm-click page so corporate link-scanners don't consume tokens
- [ ] **AUTH-02**: Auth issues JWT access tokens with tier/group claims that the voice service validates offline via JWKS (oidc-provider Resource Indicators)
- [ ] **AUTH-03**: User may enter any access code (or none) at login; known codes map to tiers, unknown/blank yields a no-access tier with guidance
- [ ] **AUTH-04**: Operator-defined codes carry expiry and max-redemption limits
- [ ] **AUTH-05**: Login form is protected by Altcha captcha

### Quota Enforcement

- [x] **QUOT-01**: Session start is blocked when tier session-length, daily, or concurrency limits are exceeded (DynamoDB conditional writes — race-safe)
- [x] **QUOT-02**: Usage increments via conditional-write ticks during the session; the session hard-stops at the cap
- [ ] **QUOT-03**: Agent speaks a time warning ~30s before cutoff and says a graceful goodbye at zero (also on daily-quota exhaustion mid-session)
- [x] **QUOT-04**: Site-wide daily budget kill-switch gates new sessions with a friendly page
- [ ] **QUOT-05**: Abandoned sessions are torn down via layered idle detection with a server-side absolute session wall-clock as the outer bound

### Infrastructure

- [x] **INFR-01**: Terragrunt skeleton (site "kmv") provisions network, certs, ecs-cluster, ecr, dynamodb, secrets, email, github-oidc, ecs-task, ecs-service from the defcon.run.34 layout
- [ ] **INFR-02**: voice.klankermaker.ai and auth.klankermaker.ai resolve with valid TLS via cross-account DNS (management zone)
- [x] **INFR-03**: WebRTC delta works deployed: public-IP Fargate tasks, wide UDP SG range, STUN-advertised srflx candidates — verified by a deployed ICE smoke test
- [ ] **INFR-04**: SES production access and DKIM for klankermaker.ai (request started week 1)
- [ ] **INFR-05**: Provider API keys flow SOPS → SSM SecureString → container secrets
- [x] **INFR-06**: Voice service autoscales 1→4 tasks with scale-in protection while sessions are active
- [x] **INFR-07**: GitHub Actions deploys via OIDC roles (no long-lived AWS keys)

### kv CLI (Go)

- [ ] **KV-01**: Operator can create, list, and expire access codes with tier mapping via `kv`
- [ ] **KV-02**: Operator can define and list tiers (session/daily/concurrency limits) via `kv`
- [x] **KV-03**: Operator can view today's usage per user and site-wide via `kv`
- [x] **KV-04**: Operator can flip the site-wide kill-switch via `kv`
- [x] **KV-05**: Operator can run a deployed smoke test (offer + ICE reachability) via `kv`

### Private Transcription Ledger

- [x] **LEDG-01**: Access token carries namespaced email + code claims (magic-link) so the voice service can build a complete ledger record from the validated token alone
- [x] **LEDG-05**: The transcript ledger writer touches only S3 — never DynamoDB — so transcripts never co-mingle with quota bookkeeping

## v2 Requirements (Deferred)

- **KV-06**: Live session inspection (`kv sessions`) — defer until a multi-user event is scheduled
- **PIPE-08**: Instant-acknowledgment latency masking — only if tuned typical latency lands >700ms
- **CLNT-09**: TURN fallback for UDP-blocked networks — when measured failure rate justifies it
- **PIPE-09**: Agent tool-calling — only with a latency-safe spoken-filler pattern

## Out of Scope

| Item | Reason |
|------|--------|
| The name "voiceai" anywhere | Copyright concerns — project is klanker-voice |
| Audio recording storage | Privacy posture; transcripts only |
| Cross-session memory, voice picker | Needs a real user base and privacy design |
| Multi-region, CloudFront-fronted voice | Single us-east-1 region suffices for a demo |
| Self-hosted/local models | Hosted APIs beat local on quality/latency per dollar |
| Registering klankervoice.ai | Possible future; v1 uses voice.klankermaker.ai |
| Emotion display / avatar | Anti-feature per research; polish budget goes to latency |

## Traceability

Coverage: 38/38 v1 requirements mapped (PIPE-10 promoted from v2-deferred in Phase 7).

| Requirement | Phase | Status |
|-------------|-------|--------|
| PIPE-01 | Phase 1 | Pending |
| PIPE-02 | Phase 1 | Complete |
| PIPE-03 | Phase 1 | Pending |
| PIPE-04 | Phase 1 | Pending |
| PIPE-05 | Phase 1 | Pending |
| PIPE-06 | Phase 1 | Complete |
| PIPE-07 | Phase 1 | Complete |
| PIPE-10 | Phase 7 | Complete |
| CLNT-01 | Phase 5 | Complete |
| CLNT-02 | Phase 5 | Complete |
| CLNT-03 | Phase 5 | Complete |
| CLNT-04 | Phase 5 | Complete |
| CLNT-05 | Phase 5 | Complete |
| CLNT-06 | Phase 5 | Complete |
| CLNT-07 | Phase 5 | Complete |
| CLNT-08 | Phase 5 | Complete |
| AUTH-01 | Phase 3 | Pending |
| AUTH-02 | Phase 3 | Pending |
| AUTH-03 | Phase 3 | Pending |
| AUTH-04 | Phase 3 | Pending |
| AUTH-05 | Phase 3 | Pending |
| QUOT-01 | Phase 4 | Complete |
| QUOT-02 | Phase 4 | Complete |
| QUOT-03 | Phase 4 | Code+tests; live audio → Phase 5 |
| QUOT-04 | Phase 4 | Complete |
| QUOT-05 | Phase 4 | Code+tests; live teardown → Phase 5 |
| INFR-01 | Phase 2 | Complete |
| INFR-02 | Phase 2 | Pending |
| INFR-03 | Phase 4 | Complete |
| INFR-04 | Phase 2 | Pending |
| INFR-05 | Phase 2 | Pending |
| INFR-06 | Phase 4 | Complete |
| INFR-07 | Phase 2 | Complete |
| KV-01 | Phase 3 | Pending |
| KV-02 | Phase 3 | Pending |
| KV-03 | Phase 4 | Complete |
| KV-04 | Phase 4 | Complete |
| KV-05 | Phase 4 | Complete |
| LEDG-01 | Phase 15 | Complete |
| LEDG-05 | Phase 15 | Complete |
