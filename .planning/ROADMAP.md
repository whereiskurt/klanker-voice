# Roadmap: klanker-voice

## Overview

klanker-voice ships in five phases that follow the dependency chain of the work itself. Phase 1 proves the core value on a laptop — a tuned Pipecat pipeline hitting ≤1.2s voice-to-voice with real barge-in, gated by a latency harness that becomes the regression instrument for everything after. Phase 2 stands up the reuse-heavy AWS skeleton in parallel (the SES production-access clock must start immediately). Phase 3 ports run.auth and lands the JWT/tier-claims token contract the voice deploy blocks on, with the first `kv` commands arriving alongside the code/tier tables. Phase 4 deploys the voice service to Fargate — the first real ICE/UDP test — with race-safe quota enforcement, session teardown, and the operator loop. Phase 5 delivers the public browser experience at voice.klankermaker.ai and verifies it on real devices and hostile networks, conference-ready.

## Phases

**Phase Numbering:**

- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

- [x] **Phase 1: Local Pipeline & Latency Harness** - Tuned cascaded pipeline runs locally with three API keys; harness proves ≤1.2s and real barge-in
- [x] **Phase 2: Infra Skeleton** - Terragrunt site "kmv" provisions the AWS foundation; SES production-access request goes out week one
- [x] **Phase 3: Auth Service & Access Codes** - run.auth port issues JWT access tokens with tier claims; access-code→tier flow and first `kv` commands (completed 2026-07-05)
- [ ] **Phase 4: Voice Service Deployed & Quota Enforcement** - Quota-gated sessions run on public-IP Fargate with verified ICE/UDP media and the full operator loop
- [ ] **Phase 5: Browser Client & Conference Readiness** - Public sign-in → mic → conversation experience with captions, orb, timer, and HUD, verified on real devices and networks

## Phase Details

### Phase 1: Local Pipeline & Latency Harness

**Goal**: A developer can hold a slick, measured, interruption-safe conversation with the KlankerMaker concierge on a laptop using only three provider API keys
**Mode:** mvp
**Depends on**: Nothing (first phase; runs parallel with Phase 2)
**Requirements**: PIPE-01, PIPE-02, PIPE-03, PIPE-04, PIPE-05, PIPE-06, PIPE-07
**Success Criteria** (what must be TRUE):

  1. Developer can run the full bot locally with only the three provider API keys and hold a natural spoken conversation
  2. Latency harness reports per-stage and voice-to-voice milliseconds from recorded audio, and measured voice-to-voice latency is ≤1.2s (tuned toward ~800ms typical)
     — AMENDED 2026-07-05 (user decision, 01-04 re-escalation): after a two-round measured A/B, ~1402ms p50 is ACCEPTED as the Phase-1 number (cascaded hosted-API floor; barge-in slick; harness context heavier than fresh prod sessions). ≤1.2s (~800ms aspiration) is a committed Phase 6 goal — see docs/TUNING.md § RE-ESCALATION

  3. User can interrupt the agent mid-speech: playback stops promptly and conversation context truncates to words actually spoken, verified by named barge-in test scenarios
  4. Agent remembers the full conversation within a session and speaks as the KlankerMaker concierge via a versioned markdown system prompt
  5. STT/LLM/TTS stages swap via config, and the endpointing A/B (Deepgram Flux vs Nova-3+VAD; SmartTurn) has measured verdicts recorded

**Plans:** 5/5 plans executed

Plans:

- [x] 01-01-PLAN.md — Toolchain, SSM→.env key bootstrap, and legitimacy-gated pipecat 1.5.0 install
- [x] 01-02-PLAN.md — Walking skeleton: config/factories/pipeline, persona v1, both run modes, greet-first
- [x] 01-03-PLAN.md — Latency harness (JSON + p50/p95 table) and named eval scenarios (barge-in, memory, greeting)
- [x] 01-04-PLAN.md — Three-arm endpointing A/B matrix with measured verdicts in docs/TUNING.md
- [x] 01-05-PLAN.md — 3-voice audition (user picks by ear), final config, conversational-feel sign-off

### Phase 2: Infra Skeleton

**Goal**: The AWS foundation exists — DNS/TLS, DynamoDB, secrets, container plumbing, and CI deploy path — and the multi-day SES production-access review is underway
**Mode:** mvp
**Depends on**: Nothing (runs parallel with Phase 1)
**Requirements**: INFR-01, INFR-02, INFR-04, INFR-05, INFR-07
**Success Criteria** (what must be TRUE):

  1. Terragrunt site "kmv" provisions network, certs, ecs-cluster, ecr, dynamodb, secrets, email, github-oidc, ecs-task, and ecs-service modules from the defcon.run.34 layout
  2. voice.klankermaker.ai and auth.klankermaker.ai resolve with valid TLS via cross-account DNS
  3. SES production-access request is submitted with SPF/DKIM/DMARC configured for klankermaker.ai
  4. Provider API keys flow SOPS → SSM SecureString → container secrets with no plaintext secrets in the repo
  5. GitHub Actions deploys via OIDC roles with no long-lived AWS keys

**Plans:** 6/7 plans executed

Plans:

- [x] 02-01-PLAN.md — Toolchain pins, zone/SES probes, state backend bootstrap (D-05/D-06), public repo push
- [x] 02-02-PLAN.md — Clone dc34 tree: 11 verbatim modules + kmv rewrites (site.hcl delta, stubs, DMARC unit, WebRTC SG) + validate/plan gates
- [x] 02-03-PLAN.md — Single-region SOPS KMS key + bootstrap-param migration into .secrets.sops.json
- [x] 02-04-PLAN.md — Apply site → certs → network (cross-account DNS, ACM ISSUED, VPC/ALB/SGs)
- [x] 02-05-PLAN.md — Apply ecs-cluster/ecr/dynamodb/secrets/email/dmarc; retire /kmv/bootstrap/*
- [x] 02-06-PLAN.md — Apply github-oidc, repo variables + gated environments, delegate-role human checkpoint
- [x] 02-07-PLAN.md — CI workflows (plan/apply/build/deploy/gitleaks) + end-to-end OIDC proof run

### Phase 3: Auth Service & Access Codes

**Goal**: A user can sign in via magic link with an access code and receive a tier-claimed JWT that downstream services validate offline; operators manage codes and tiers via `kv`
**Mode:** mvp
**Depends on**: Phase 2
**Requirements**: AUTH-01, AUTH-02, AUTH-03, AUTH-04, AUTH-05, KV-01, KV-02
**Success Criteria** (what must be TRUE):

  1. User can sign in via magic-link email, with an interstitial confirm-click page so corporate link-scanners don't consume tokens
  2. Auth issues JWT access tokens with tier/group claims that a relying service validates offline via the JWKS endpoint
  3. User may enter any access code (or none) at login: known codes map to tiers, unknown/blank yields a no-access tier with guidance
  4. Operator-defined codes carry expiry and max-redemption limits, and the login form is protected by Altcha captcha
  5. Operator can create, list, and expire access codes and define/list tiers via `kv`

**Plans**: 4/4 plans complete

Plans:

- [x] 03-01-PLAN.md — Port run.auth app; trim to single-region Email-only voice-client; magic-link login green with interstitial confirm page + Altcha; two DynamoDB tables live (AUTH-01, AUTH-05)
- [x] 03-02-PLAN.md — access_codes/tier/login_intent/code_redemption entities; login-time code→tier resolution + email→token bridge; unique-user redemption counting (AUTH-03, AUTH-04)
- [x] 03-03-PLAN.md — Enable Resource Indicators; RS256 JWT access token + tier_id/group claims; persistent JWKS in SSM; pin Phase-4 contract (AUTH-02)
- [x] 03-04-PLAN.md — `kv` CLI code + tier CRUD; ElectroDB key-compat round-trip; seed demo/kphdemo123 tiers and codes (KV-01, KV-02)

**Waves:** 1 → {03-01}; 2 → {03-02}; 3 → {03-03, 03-04 parallel}
**UI hint**: yes

### Phase 4: Voice Service Deployed & Quota Enforcement

**Goal**: Quota-gated voice sessions run end-to-end on deployed Fargate tasks with real browser↔task UDP media, race-safe usage enforcement, and the full operator loop
**Mode:** mvp
**Depends on**: Phase 1, Phase 2, Phase 3
**Requirements**: INFR-03, INFR-06, QUOT-01, QUOT-02, QUOT-03, QUOT-04, QUOT-05, KV-03, KV-04, KV-05
**Success Criteria** (what must be TRUE):

  1. Deployed ICE smoke test passes: an offer against the live service reaches ICE `connected` with RTP flowing to a public-IP Fargate task, runnable via `kv`
  2. Session start is blocked when tier session-length, daily, or concurrency limits are exceeded, and usage ticks via race-safe conditional writes hard-stop the session at its cap
  3. Agent speaks a time warning ~30s before cutoff and a graceful goodbye at zero, including on mid-session daily-quota exhaustion
  4. Site-wide kill-switch gates new sessions, and abandoned sessions are torn down via layered idle detection with a server-side wall-clock outer bound
  5. Voice service autoscales 1→4 tasks with scale-in protection during active sessions, and operator can view today's usage and flip the kill-switch via `kv`

**Plans**: TBD

### Phase 5: Browser Client & Conference Readiness

**Goal**: A member of the public at voice.klankermaker.ai signs in, taps the mic, and holds a slick conversation with full session UX — proven on real phones and hostile networks
**Mode:** mvp
**Depends on**: Phase 4
**Requirements**: CLNT-01, CLNT-02, CLNT-03, CLNT-04, CLNT-05, CLNT-06, CLNT-07, CLNT-08
**Success Criteria** (what must be TRUE):

  1. User signs in via OIDC redirect to auth.klankermaker.ai before the mic is available, then grants mic through a gesture-gated flow with distinct error states (denied / no device / unsupported browser)
  2. User sees a connection state machine with clear ICE-failure/UDP-blocked messaging and auto-retry, verified on a real iPhone and a restricted conference-style network
  3. User sees live captions for both sides, a state-aware orb (listening / thinking / speaking), and a visible session countdown timer
  4. User can toggle a latency HUD showing per-stage pipeline latency
  5. User gets a clean session end and one-click reconnect that re-checks quota before reconnecting

**Plans**: TBD
**UI hint**: yes

### Phase 6: Latency v2 (deferred — scoped 2026-07-05)

**Goal**: Close the gap from the accepted ~1402ms p50 local baseline to ≤1.2s voice-to-voice (aspiration ~800ms), measured on the deployed service
**Mode:** mvp
**Depends on**: Phase 1 (pipeline), Phase 4 (deployed measurement baseline); executes after Phase 5 unless schedule slack allows earlier
**Origin**: 01-04 re-escalation decision (user: "accept + scope later phase"); levers recorded in docs/TUNING.md § RE-ESCALATION
**Success Criteria** (what must be TRUE):

  1. PIPE-08 ack-masking implemented: an immediate acknowledgment masks the LLM+TTS wall, measured perceptual gap ≤~600ms from user stop to first agent audio
  2. Lighter/faster LLM turn A/B measured via the config-swappable LLM service (deployed, fresh-session context)
  3. (Optional) Flux double-endpointing experiment: deliberate Pitfall-3 acceptance measured end-to-end; adopted only if server EOT beats SmartTurn v3
  4. Deployed voice-to-voice p50/p95 re-measured from us-east-1 against the 1402ms local baseline and recorded in docs/TUNING.md

**Plans**: TBD

### Phase 7: KPH Knowledge Base

**Scoped**: 2026-07-05
**Goal**: KPH answers with deep, current knowledge of Kurt's world — klanker-maker, defcon.run, meshtk, and selected repos/scripts — without breaking the voice-latency budget
**Mode:** mvp
**Depends on**: Phase 1 (pipeline); complements Phase 6 (ack-masking masks retrieval latency)
**Origin**: User at Phase-1 sign-off: "massive RAG or something really smart that can steer... all of the knowledge of my repos, and some scripts and stuff I'd train it on"
**Success Criteria** (what must be TRUE):

  1. A curated, versioned knowledge pack (repo/project digests) ships in the system prompt; once ≥4096 tokens, Anthropic prompt caching engages (verified via cache_read_input_tokens > 0) and TTFT stays within the accepted latency budget
  2. A retrieval path answers depth questions ("the long version") from full repo content, with latency masked acceptably in conversation
  3. Knowledge refresh is a script run, not a manual edit — regenerating digests from the live repos
  4. KPH answers a benchmark set of Kurt/repo questions correctly, verified by eval scenarios

**Plans**: TBD

## Progress

**Execution Order:**
Phases execute in numeric order: 1 → 2 → 3 → 4 → 5 (Phases 1 and 2 have no interdependency and may run in parallel)

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Local Pipeline & Latency Harness | 5/5 | ✅ Complete (verified 5/5, amended) | 2026-07-05 |
| 2. Infra Skeleton | 7/7 | ✅ Complete (verified 5/5) | 2026-07-05 |
| 3. Auth Service & Access Codes | 4/4 | Complete   | 2026-07-05 |
| 4. Voice Service Deployed & Quota Enforcement | 0/TBD | Not started | - |
| 5. Browser Client & Conference Readiness | 0/TBD | Not started | - |
| 6. Latency v2 (deferred) | 0/TBD | Not started | - |
| 7. KPH Knowledge Base | 0/TBD | Not started | - |
