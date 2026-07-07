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
- [x] **Phase 4: Voice Service Deployed & Quota Enforcement** - Quota-gated sessions run on public-IP Fargate with verified ICE/UDP media and the full operator loop (completed 2026-07-06)
- [x] **Phase 5: Browser Client & Conference Readiness** - Public sign-in → mic → conversation experience with captions, orb, timer, and HUD, verified on real devices and networks (completed 2026-07-06)

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
     — NOTE 2026-07-05 (03-VERIFICATION): resolve-to-no-access logic complete + tested; the "with guidance" UI clause (D-07) DEFERRED to Phase 5 client UX by user decision (users are redirected to the voice client, which is where they hit the "need a code" moment — CLNT-01/08). Live-table seed (demo/kphdemo123 + tiers) applied to kmv-auth-electro this session.

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

**Plans**: 6/6 plans complete

Plans:
**Wave 1**

- [x] 04-01-PLAN.md — Production /api/offer + /health FastAPI entrypoint, offline JWT validation, public-IP+STUN ICE candidates, Dockerfile (INFR-03 code)
- [x] 04-02-PLAN.md — Infra delta: narrow+attach UDP SG (20000-20100), enable public-IP task/service, usage table, task IAM, session-count autoscaling 1→4 (INFR-03, INFR-06)

**Wave 2** *(blocked on Wave 1 completion)*

- [x] 04-03-PLAN.md — `kv smoke` synthetic offer→ICE→RTP + deploy + deployed ICE smoke proof (KV-05, INFR-03)

**Wave 3** *(blocked on Wave 2 completion)*

- [x] 04-04-PLAN.md — Race-safe quota: usage model, start-gate (typed rejects+sub-floor+per-task cap), heartbeat lease, service timer, 15s tick+rollup+auto-trip, hard-stop, ActiveSessions metric+scale-in protection (QUOT-01, QUOT-02, QUOT-04, INFR-06)

**Wave 4** *(blocked on Wave 3 completion)*

- [x] 04-05-PLAN.md — Spoken wind-down (natural warning + deterministic goodbye) + three-layer idle teardown + reconnect grace (QUOT-03, QUOT-05)
- [x] 04-06-PLAN.md — Operator loop: `kv usage` + `kv killswitch` + autoscale verification (QUOT-04, KV-03, KV-04, INFR-06)

**Waves:** 1 → {04-01, 04-02}; 2 → {04-03}; 3 → {04-04}; 4 → {04-05, 04-06 parallel}

### Phase 5: Browser Client & Conference Readiness

**Goal**: A member of the public at voice.klankermaker.ai signs in, taps the mic, and holds a slick conversation with full session UX — proven on real phones and hostile networks
**Mode:** mvp
**Depends on**: Phase 4
**Requirements**: CLNT-01, CLNT-02, CLNT-03, CLNT-04, CLNT-05, CLNT-06, CLNT-07, CLNT-08
**Success Criteria** (what must be TRUE):

  1. User signs in via OIDC redirect to auth.klankermaker.ai before the mic is available, then grants mic through a gesture-gated flow with distinct error states (denied / no device / unsupported browser)
     — includes the no-access-tier guidance surfaced in-client (moved from Phase 3 / D-07, 2026-07-05): a no-access user is told they cannot start a session and how to get a code

  2. User sees a connection state machine with clear ICE-failure/UDP-blocked messaging and auto-retry, verified on a real iPhone and a restricted conference-style network
  3. User sees live captions for both sides, a state-aware orb (listening / thinking / speaking), and a visible session countdown timer
  4. User can toggle a latency HUD showing per-stage pipeline latency
  5. User gets a clean session end and one-click reconnect that re-checks quota before reconnecting

**Plans**: 7/7 plans complete
**UI hint**: yes

Plans:
**Wave 1** *(parallel: server + client scaffold, no file overlap)*

- [x] 05-01-PLAN.md — Server RTVI wiring (transcripts + bot/user speaking + audio levels) + composed per-turn latency emission + StaticFiles SPA mount (CLNT-03, CLNT-04, CLNT-06 server half)
- [x] 05-02-PLAN.md — Vite+TS+React scaffold + UI-SPEC design tokens + hero orb (WebGL2 shader + particle ring, winner A) with 2D fallback + attract landing + D-03 multi-stage Docker (CLNT-04)

**Wave 2** *(depends on 05-02)*

- [x] 05-03-PLAN.md — authorization-code + PKCE sign-in (in-memory token) + callback route + no-access exclusive gate (CLNT-08)

**Wave 3** *(depends on 05-01, 05-02, 05-03)*

- [x] 05-04-PLAN.md — Gesture-gated mic + distinct errors + SmallWebRTC connect (Bearer) + connection state machine + live orb reactivity + subtitle captions (CLNT-01, CLNT-02, CLNT-03, CLNT-04)

**Wave 4** *(depends on 05-04)*

- [x] 05-05-PLAN.md — Escalating session countdown + toggleable latency HUD from kmv-latency RTVI messages (CLNT-05, CLNT-06)

**Wave 5** *(depends on 05-05)*

- [x] 05-06-PLAN.md — Bounded retry → honest UDP-blocked wall + typed gate-rejection copy + clean end + quota-rechecked reconnect (CLNT-02, CLNT-07)

**Wave 6** *(depends on 05-06)*

- [x] 05-07-PLAN.md — Mobile/iOS layout + a11y baseline + real-iPhone & restricted-network conference verification (CLNT-01…08)

**Waves:** 1 → {05-01, 05-02}; 2 → {05-03}; 3 → {05-04}; 4 → {05-05}; 5 → {05-06}; 6 → {05-07}

### Phase 05.2: Slick Start — single-tap silent SSO + instant pre-rendered greeting (INSERTED)

**Goal:** A returning user taps the mic once and immediately hears a warm, randomly-chosen KPH greeting while the WebRTC stream connects underneath — the session start feels like one slick tap, no visible sign-in bounce on the tap, and KPH never greets twice.
**Mode:** mvp
**Depends on**: Phase 5 (browser client)
**Design spec**: docs/superpowers/specs/2026-07-06-slick-start-design.md
**Implementation plan**: docs/superpowers/plans/2026-07-06-slick-start.md
**Requirements**: CLNT-08 (auth), CLNT-01/02 (session start), PIPE-02 (greet-first), D-04/D-05 (auth/attract)
**Success Criteria** (what must be TRUE):

  1. A returning user (previously interactively signed in on this device) loads voice.klankermaker.ai, one silent top-level `prompt=none` SSO bounce completes during load, and their **first tap** goes straight to mic + connect — no sign-in redirect on the tap
  2. A signed-out user loads to Attract instantly and does no silent attempt; a `login_required` from the silent bounce clears the breadcrumb and lands signed-out with no error UI
  3. The instant a returning user taps, a random pre-rendered KPH greeting clip plays (unlocking iOS audio on the same gesture), masking the connect gap; the Live handoff waits until BOTH the clip has ended AND the transport is connected (no greeting/STT overlap)
  4. Greeting clips are rendered from the `voice_id` configured in pipeline.toml (MP3, `eleven_flash_v2_5`); a CI drift guard fails if the clips were rendered from a different voice than currently shipped
  5. Server `greet_first` is disabled on the WebRTC path (client owns the opener) so KPH does not greet twice; the console/local path is unaffected
  6. No access token is ever persisted — Workstream A adds only a boolean `localStorage` breadcrumb + a `sessionStorage` per-load loop guard

**Task 0 (prompt=none feasibility gate): VERIFIED GREEN 2026-07-06** — live issuer returns `303 → /callback?error=login_required` for `prompt=none` with no session (no login-page render); Workstream A is cleared to build.

**Plans:** 4/4 plans complete

**Waves:** 1 → {05.2-01, 05.2-02, 05.2-04}; 2 → {05.2-03}

Plans:

- [x] 05.2-01-PLAN.md — Workstream A: single-tap silent SSO (breadcrumb, prompt=none, attemptSilentSso, login_required branch) [CLNT-08]
- [x] 05.2-02-PLAN.md — Workstream B: greeting source + render script + drift guard (key-gated render checkpoint) [CLNT-01, CLNT-02]
- [x] 05.2-03-PLAN.md — Workstream B: greeting player + deferred Live handoff (+ phase full-suite gate) [CLNT-01, CLNT-02]
- [x] 05.2-04-PLAN.md — Workstream B: server greet_first toggle + persona opening-move tweak [PIPE-02]

### Phase 05.1: Operator Admin Panel (INSERTED)

**Goal**: The operator (KPH) can invite his dad + a few close friends (≤25 users) and glance at usage — a gated `/admin` section in the existing auth app showing users, sessions, minutes, quota trips, and code create/list/expire + kill-switch, so the first-look audience can be onboarded and watched without touching the CLI
**Mode:** mvp
**Depends on**: Phase 5 (voice client must exist to have sessions to observe)
**Design spec**: docs/superpowers/specs/2026-07-06-admin-panel-design.md
**Success Criteria** (what must be TRUE):

  1. An `ADMIN_EMAILS`-allowlisted operator logs in via magic link (code-free) and reaches `/admin`; a non-allowlisted session gets 404 (route existence not advertised)
  2. Operator sees a users list (email, tier, group, first/last seen, total sessions, total minutes) and per-user session detail (start, duration, quota trips, force-stop) — operational data only, NO transcripts (deferred to Phase 7)
  3. Operator can create and expire access codes from the panel, producing DynamoDB writes byte-compatible with `kv code create/expire`
  4. Operator can view and toggle the kill-switch from the panel

**Plans**: TBD

Plans:

- [ ] TBD (run /gsd-plan-phase 05.1 to break down)

### Phase 6: Latency v2 (deferred — scoped 2026-07-05)

**Goal**: Close the gap from the accepted ~1402ms p50 local baseline to ≤1.2s voice-to-voice (aspiration ~800ms), measured on the deployed service
**Mode:** mvp
**Depends on**: Phase 1 (pipeline), Phase 4 (deployed measurement baseline); executes after Phase 7 (2026-07-05 reorder — Phase 7 runs before Phase 6)
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
**Depends on**: Phase 1 (pipeline); complements Phase 6 (ack-masking masks retrieval latency). **Execution order: runs BEFORE Phase 6** (2026-07-05 user reorder).
**Origin**: User at Phase-1 sign-off: "massive RAG or something really smart that can steer... all of the knowledge of my repos, and some scripts and stuff I'd train it on"
**Success Criteria** (what must be TRUE):

  1. A curated, versioned knowledge pack (repo/project digests) ships in the system prompt; once ≥4096 tokens, Anthropic prompt caching engages (verified via cache_read_input_tokens > 0) and TTFT stays within the accepted latency budget
  2. A retrieval path answers depth questions ("the long version") from full repo content, with latency masked acceptably in conversation
  3. Knowledge refresh is a script run, not a manual edit — regenerating digests from the live repos
  4. KPH answers a benchmark set of Kurt/repo questions correctly, verified by eval scenarios

**Reconciliation (RE-PLANNED 2026-07-06 for Amendment 3):** Built as **router + per-topic curated packs + a stable Kurt-STYLE cached prefix + a bounded LOCAL retrieval path** (CONTEXT D-10/D-11/D-13 ⋈ DESIGN-NOTES Amendments 1+2+3). **Amendment 3 reopens D-10/D-11** to add keyless in-process retrieval — **SQLite FTS5 + BM25**, topic-scoped, top-k chunks injected into the uncached system[1] block, ack-masked (no 4th vendor, PIPE-07). Criterion 2's "retrieval path... from full repo content" is now literal (bounded local BM25 over the full corpus), not just a pre-baked pack. Corpus prep is per-source (km docs+diagram-as-text indexed directly; defcon.run.34/meshtk get an offline grill-with-docs doc-gen pass then index). The do-not-say scrubber is DEMOTED to a thin advisory lint (flag-not-block, corpus is all-public). Cross-system synthesis and vector/semantic RAG remain OUT for launch. The four pre-Amendment-3 plans were regenerated into five.

**Plans**: 5 plans

Plans:
**Wave 1**

- [ ] 07-01-PLAN.md — Foundation on ONE topic (km): keyword router + ack + two-block cached prompt (stable prefix + swappable deep pack) + km curated content + advisory do-not-say lint + live cache proof (PIPE-10, PIPE-06, PIPE-07)

**Wave 2** *(parallel; both depend on 07-01)*

- [ ] 07-02-PLAN.md — Local retrieval subsystem: SQLite FTS5/BM25 chunking+index+query (keyless), deep-turn topic-scoped injection into uncached system[1], km depth walking slice + latency guard (PIPE-10, PIPE-07)
- [ ] 07-03-PLAN.md — defcon.run.34 + meshtk curated deep packs, multi-topic discrimination + overlap guard, cross-topic cache warmth, per-topic evals (PIPE-10)

**Wave 3** *(parallel; both depend on 07-01 + 07-02 + 07-03)*

- [ ] 07-04-PLAN.md — Refresh workflow (`make knowledge` / `kv knowledge refresh`): distill curated packs + swappable grill-with-docs doc-gen + FTS5 chunk/index build + advisory-lint flagging (manifest-only, public-refusal, skip-missing, offline) (PIPE-10, PIPE-07)
- [ ] 07-05-PLAN.md — Adaptive steering + time-aware pacing + honest unknowns + do-not-say spoken boundary + benchmark eval set incl. retrieval DEPTH/coverage + router accuracy (PIPE-10, PIPE-06)

**Waves:** 1 → {07-01}; 2 → {07-02, 07-03 parallel}; 3 → {07-04, 07-05 parallel}

## Progress

**Execution Order:**
Phases execute in order: 1 → 2 → 3 → 4 → 5 → **7** → **6** (Phases 1 and 2 have no interdependency and may run in parallel).
**Reorder (2026-07-05, user decision):** after Phase 5, run **Phase 7 (KPH Knowledge Base) before Phase 6 (Latency v2)** — Phase 7 is content/knowledge work depending only on Phase 1, and is the higher priority; Phase 6 latency tuning follows. Phase numbers are unchanged — only the execution order.

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Local Pipeline & Latency Harness | 5/5 | ✅ Complete (verified 5/5, amended) | 2026-07-05 |
| 2. Infra Skeleton | 7/7 | ✅ Complete (verified 5/5) | 2026-07-05 |
| 3. Auth Service & Access Codes | 4/4 | ✅ Complete (verified 4/5; #3 guidance→Phase 5, seed done) | 2026-07-05 |
| 4. Voice Service Deployed & Quota Enforcement | 6/6 | Complete   | 2026-07-06 |
| 5. Browser Client & Conference Readiness | 7/7 | Complete   | 2026-07-06 |
| 6. Latency v2 (deferred) | 0/TBD | Not started | - |
| 7. KPH Knowledge Base | 0/5 | Planned (5 plans, 3 waves — re-planned for Amendment 3) | - |

### Phase 8: Documentation & Architecture

**Goal**: A newcomer (or future-KPH) can understand and operate klanker-voice from docs alone — a polished system architecture diagram plus written documentation (README, architecture narrative, deploy/runbook) covering the full AWS -> Pipecat pipeline (VAD -> STT -> LLM -> TTS) -> auth/OIDC -> DynamoDB topology, the operator loop (`kv`, `/admin`), and the quota/kill-switch model
**Mode:** mvp
**Depends on**: Phase 7 (documents the whole finished system, including the concierge knowledge base)
**Design assets**: docs/superpowers/specs/2026-07-04-klanker-voice-design.md (authoritative); starter architecture diagram exported 2026-07-06 (Excalidraw)
**Success Criteria** (what must be TRUE):

  1. A system architecture diagram exists (committed source, e.g. `.excalidraw` + exported PNG/SVG) showing client -> auth/OIDC/JWT -> Fargate voice service -> Pipecat pipeline -> hosted APIs, plus the AWS data/platform layer
  2. A top-level README orients a newcomer: what the project is, the app layout (`apps/voice`, `apps/auth`, `kv`, `infra`), and how to run each locally
  3. An architecture narrative explains the request/session lifecycle, the tier/quota/kill-switch model, and the auth token contract
  4. A deploy/runbook covers deploying the voice + auth services, rotating secrets, issuing codes, and using the operator surfaces (`kv`, `/admin`)

**Plans**: TBD

Plans:

- [ ] TBD (run /gsd-plan-phase 8 to break down)
