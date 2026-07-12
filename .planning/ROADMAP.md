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

**Gap-closure (05.2-VERIFICATION.md, commit cfc7013):** phase verification found one reproducible, non-live bug against Success Criterion #2 — App.tsx's `handleAuthenticated` unconditionally re-marked the returning-user breadcrumb, undoing Callback.tsx's `clearReturningUser()` on the `login_required` safe-degrade path. Fixed via TDD (regression test proven RED then GREEN); 6/6 success criteria now verified at the code/behavior level. Live iPhone/Safari verification pass remains deferred, unchanged in scope.

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

**Reconciliation (RE-PLANNED 2026-07-07 for Amendments 3–5):** Built as **router + per-topic curated packs + a stable Kurt-STYLE cached prefix + a bounded LOCAL retrieval path** (CONTEXT D-10/D-11/D-13 ⋈ DESIGN-NOTES Amendments 1–5). **Amendment 3 reopens D-10/D-11** to add keyless in-process retrieval — **SQLite FTS5 + BM25**, topic-scoped, top-k chunks injected into the uncached system[1] block, ack-masked (no 4th vendor, PIPE-07). Criterion 2's "retrieval path... from full repo content" is now literal (bounded local BM25 over the full corpus), not just a pre-baked pack. **Amendment 4:** the style layer is REAL — Kurt's 14 transcribed clips (~82 min, km-heavy) + a humor/personality deck ground a distilled Kurt-STYLE layer in the cached prefix (with a public-mic PG-13 guardrail), and the transcripts + km diagram-as-text feed the facts packs + retrieval corpus. **Amendment 5:** grill-with-docs is **DROPPED** — corpus prep is direct per-source indexing (km docs+diagram+transcripts; defcon.run.34 `infra/terraform/{live,modules,providers}` + `apps/` as code; meshtk README + Go source), with retrieval-quality mitigation (per-topic scoping, curated-pack framing, docs-over-source ranking). The refresh pipeline is re-runnable + manifest-driven so the GROWING corpus (incoming defcon audio, a Google Docs talk) folds in via one manifest edit, no re-plan. The do-not-say scrubber is DEMOTED to a thin advisory lint (flag-not-block, corpus is all-public). Cross-system synthesis and vector/semantic RAG remain OUT for launch. The four pre-Amendment-3 plans were regenerated into five (Amendments 4 & 5 applied 2026-07-07).

**Plans**: 5/5 plans complete

Plans:
**Wave 1**

- [x] 07-01-PLAN.md — km walking slice: keyword router + ack + two-block cached prompt (stable prefix + swappable deep pack) + km curated content + REAL transcript-distilled Kurt-STYLE layer (+ humor deck + PG-13 guardrail) + advisory do-not-say lint + live cache proof (PIPE-10, PIPE-06, PIPE-07)

**Wave 2** *(parallel; both depend on 07-01)*

- [x] 07-02-PLAN.md — Local retrieval subsystem: SQLite FTS5/BM25 chunking+index+query (keyless), deep-turn topic-scoped injection into uncached system[1], km depth walking slice + latency guard (PIPE-10, PIPE-07)
- [x] 07-03-PLAN.md — defcon.run.34 + meshtk curated deep packs (facts also harvested from the humor deck), multi-topic discrimination + overlap guard, cross-topic cache warmth, per-topic evals (PIPE-10)

**Wave 3** *(parallel; both depend on 07-01 + 07-02 + 07-03)*

- [x] 07-04-PLAN.md — Refresh workflow (`make knowledge` / `kv knowledge refresh`): promote transcribe/normalize scripts + distill curated packs + style pass + DIRECT per-source code indexing (grill-with-docs DROPPED, Amendment 5) + FTS5 chunk/index build + advisory-lint flagging (manifest-only, skip-missing, offline, re-runnable/growing) (PIPE-10, PIPE-07)
- [x] 07-05-PLAN.md — Adaptive steering + time-aware pacing + honest unknowns + do-not-say spoken boundary + public-mic PG-13 crude-humor guard eval + benchmark eval set incl. retrieval DEPTH/coverage + router accuracy (PIPE-10, PIPE-06)

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
| 7. KPH Knowledge Base | 5/5 | Complete   | 2026-07-07 |

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

---

## Milestone v1.1: Telephony (VoIP.ms / Payphone)

**Started:** 2026-07-11
**Authoritative spec:** `docs/superpowers/specs/2026-07-11-voipms-telephony-integration.md` (interview-validated; addenda §23–§25 for phone→code→tier identity, the silent call-answer gate, and DEF CON hardening)
**Goal:** A caller on the public telephone network — or the physical payphone through an ATA — can hold a natural conversation with the existing Klanker Voice agent, by adding an Asterisk SIP/PSTN edge and a Klanker-side media transport, reusing the existing Pipecat cascade (STT → LLM → TTS), quota gates, and session lifecycle unchanged. Inbound-only in v1.1; fail-closed at every layer; financial blast radius capped.

**Phase ↔ spec mapping** (spec §19 implementation phases A–F):

| Roadmap phase | Spec phase | Delivers |
|---------------|-----------|----------|
| Phase 9  | A | Transport-neutral shared call runtime (refactor, no telephony code) |
| Phase 10 | B | Offline media adapter — PCMU codec, RTP, `TelephonyTransport` |
| Phase 11 | C | Local Asterisk — ARI controller, bridges, external media, softphone call |
| Phase 12 | D | VoIP.ms inbound DID reaches the agent from the cellular network |
| Phase 13 | E | Physical payphone via its own ATA subaccount |
| Phase 14 | F | Production hardening — Terraform, SSM, alarms, failure routing, runbook |

**Execution order:** Phases 9 → 10 → 11 → 12 → 13 → 14 (strict dependency chain; each spec exit criterion gates the next).

### Phase 9: VoIP.ms Telephony — Call Runtime Extraction

**Goal:** Extract a transport-neutral shared call runtime (`apps/voice/src/klanker_voice/call_runtime.py`) from `server.py` so both the existing WebRTC path and future telephony construct, run, and idempotently close one live voice session through the same seam — a behavior-preserving refactor with the browser voice path unchanged. (Spec Phase A, §6 / §19-A / §21.)
**Requirements**: none (telephony milestone has no REQ-IDs yet — coverage driven by the 6 success criteria + CONTEXT D-01..D-08)
**Depends on:** Phase 5 (the deployed WebRTC voice path being refactored)
**Plans:** 1/1 plans complete
**Success Criteria** (what must be TRUE):

  1. `call_runtime.py` exposes a narrow API to construct, run, and idempotently close a session around an arbitrary Pipecat `BaseTransport` (the spec §6 `CallSession` / `create_call_session(*, transport, identity, cfg, channel, metadata)` shape), owning quota-gate → ambience → `build_pipeline(cfg, transport)` → observers → `SessionLifecycle` → callbacks → greeting → one idempotent close path
  2. The WebRTC `/api/offer` path is converted to use the shared runtime; quota start-gate, `SessionLifecycle`, observers, greeting (`greet_now`/`greet_first`), warning + goodbye callbacks, reconnect grace, RTVI, and ambience-mixer behavior are all preserved
  3. Browser voice works exactly as before — every existing lifecycle / quota / greeting / connection-teardown test still passes (spec §19-A exit criterion)
  4. `close()` is idempotent and lifecycle `release()` fires exactly once on worker or transport termination (single release path, §6.10) — the WebRTC-specific reconnect-race teardown (`webrtc.py`) is preserved, not generalized into the shared runtime
  5. No SIP / Asterisk / RTP / codec / infrastructure code is introduced (Phase A boundary, §21.6), and no STT/LLM/TTS provider construction is duplicated (§22.2 — `factories.py` remains the single source)
  6. Focused tests prove transport-neutral construction, idempotent close, and release-on-worker/transport-termination; a short architecture note documents the extracted seam and any existing coupling that resisted clean extraction (§21.7 / §21.9)

Plans:

- [x] 09-01-PLAN.md — Extract transport-neutral `call_runtime.py` (CallSession/create_call_session), convert the WebRTC `/api/offer` path to it, focused tests + architecture note (behavior-preserving refactor)

**Waves:** 1 → {09-01}

### Phase 10: VoIP.ms Telephony — Offline Media Adapter

**Goal:** Recorded telephone audio traverses the real Klanker pipeline without SIP — add a PCMU (G.711 μ-law) codec, an RTP parser/packetizer, and the Pipecat-compatible `TelephonyTransport` (input/output processors, stateful 8 kHz↔pipeline-rate resampling, interruption flush). (Spec Phase B, §7–§10 / §19-B.)
**Requirements**: TBD
**Depends on:** Phase 9
**Plans:** 2/2 plans complete
**Wave 1**

- [x] 10-01-PLAN.md — PCMU μ-law codec + RFC 3550 RTP parser/packetizer + offline in-memory RtpMediaSession + known-vector tests (Wave 1)

**Wave 2** *(blocked on Wave 1 completion)*

- [x] 10-02-PLAN.md — TelephonyTransport (input/output processors, per-direction 8 kHz boundary resampler, interruption flush, fire-once events) + §19-B offline pipeline-traversal proof (Wave 2)

**Success Criteria** (what must be TRUE):

  1. PCMU decode/encode pass known vectors; RTP sequence/timestamp/SSRC handling tolerates reordering, duplicates, and a single missing 20 ms packet (silence insertion acceptable)
  2. `TelephonyTransport` emits correct Pipecat `InputAudioRawFrame`s from RTP and PCMU RTP from `OutputAudioRawFrame`s, with a stateful streaming resampler at the 8 kHz boundary (no per-frame drift)
  3. Interruption flushes the output queue (≤20–60 ms application buffer); `stop()` is idempotent and the disconnect event fires exactly once
  4. Recorded telephone audio (WAV → synthetic RTP) traverses the real `build_pipeline` end-to-end **offline** — RTP → `InputAudioRawFrame` in, `OutputAudioRawFrame` → captured PCMU RTP out — with no Asterisk and no live SIP (spec §19-B exit criterion). This is a hermetic path-traversal proof (no real provider APIs / network); the live Deepgram→ElevenLabs round-trip over telephone audio is deferred to a Phase 11 live eval (per CONTEXT D-08/D-10 — Phase B builds no providers).

### Phase 11: VoIP.ms Telephony — Local Asterisk Edge

**Goal:** A local SIP softphone call holds a full conversation with the agent through Asterisk — add the Asterisk configs (PJSIP/ARI/dialplan), an ARI/Stasis call controller that creates external-media channels + mixing bridges, and the call registry, wiring hangup to `lifecycle.release()`. (Spec Phase C, §7 / §13 / §19-C, plus the silent answer-gate §24 verified outside the LLM.)
**Requirements**: none (coverage driven by success criteria 1-4 + CONTEXT decisions D-01..D-09)
**Depends on:** Phase 10
**Plans:** 7/7 plans complete

Plans:
**Wave 1**

- [x] 11-01-PLAN.md — [telephony] config loader + credential-regex widening (D-09)
- [x] 11-02-PLAN.md — Asterisk configs (inbound-only Stasis, private ARI) + docker-compose harness (D-01, D-07)
- [x] 11-03-PLAN.md — socket-backed RtpMediaSession behind the Phase-10 Protocol (D-03)

**Wave 2** *(blocked on Wave 1 completion)*

- [x] 11-04-PLAN.md — raw-aiohttp ARI client (REST + events WS) (D-06)

**Wave 3** *(blocked on Wave 2 completion)*

- [x] 11-05-PLAN.md — AsteriskCallController + ActiveCall registry + idempotent teardown + §16 lifecycle tests (D-02)

**Wave 4** *(blocked on Wave 3 completion)*

- [x] 11-06-PLAN.md — the silent §24 answer-gate: GateProcessor + passphrase/PIN + redaction boundary + fail-closed (D-05)

**Wave 5** *(blocked on Wave 4 completion)*

- [x] 11-07-PLAN.md — standalone telephony entrypoint + SIPp fake-media CI test + manual §19-C proof (D-08, D-07)

**Success Criteria** (what must be TRUE):

  1. Asterisk configs (`pjsip.conf`, `ari.conf`, `extensions.conf`) exist with a narrow inbound-only Stasis dialplan; ARI is authenticated and private-only
  2. An ARI controller allocates a media session, creates the external-media channel + bridge, constructs a Klanker `CallSession`, and keys an `ActiveCall` registry by channel ID
  3. On `ChannelDestroyed`/hangup the controller closes the `CallSession`, releases lifecycle exactly once, and tears down bridge + external channel + RTP socket (no leaked resources)
  4. A local SIP softphone reaches the Stasis app, hears the greeting (not clipped), converses, interrupts the agent, and hangs up cleanly (spec §19-C exit criterion)

### Phase 12: VoIP.ms Telephony — Inbound DID

**Goal:** A public VoIP.ms DID reliably reaches the agent from the cellular network — provision a dedicated `klanker-pbx` subaccount, register Asterisk, route the DID, apply the phone→code→tier identity (§23) + silent answer-gate (§24), and the security restrictions. (Spec Phase D, §4 / §11 / §23–§25 / §19-D.)
**Requirements**: none (telephony milestone has no REQ-IDs — coverage driven by the 4 success criteria + CONTEXT D-01..D-06)
**Depends on:** Phase 11
**Plans:** 6/8 plans executed
**Success Criteria** (what must be TRUE):

  1. A dedicated `klanker-pbx` VoIP.ms subaccount (strong unique SIP password, IP-restricted, outbound disabled) is registered and the DID routes to it; secrets live only in SSM
  2. A caller ID maps to an existing access code → tier via the existing mint path (§23); caller-ID alone grants at most the default minimal tier, and the silent DTMF-PIN / 4-word passphrase gate (§24, verified outside the LLM) is the only path to a high/`kph-tier` grant
  3. Quota is enforced by the existing `SessionLifecycle` (1 concurrent, short max duration, small daily cap); a call from a mobile phone completes a multi-turn conversation and hangs up cleanly from either side (spec §19-D exit criterion)
  4. Fail-closed behavior holds: a scanner/unknown caller who fails the gate burns no STT/LLM/TTS quota and gets a static goodbye + hangup; Klanker unavailable → static unavailable message, never a silent open call

Plans:
**Wave 1**

- [x] 12-01-PLAN.md — `kv voipms` API automation + operator provisioning runbook (§25.F order) (D-03) [Wave 1]
- [x] 12-02-PLAN.md — Auth §23 mint path: E.164 normalization + sparse byPhone GSI + resolvePhoneToCode + private no-oracle /tel route + tests (D-02) [Wave 1]
- [x] 12-03-PLAN.md — `kv code phone` + electro gsi3 byPhone key writers + normalization-parity tests (D-05) [Wave 1]
- [x] 12-04-PLAN.md — Asterisk VoIP.ms registration trunk (inbound-only, ulaw-only) + render extension + config-lint tests (D-01) [Wave 1]

**Wave 2** *(blocked on Wave 1 completion)*

- [x] 12-05-PLAN.md — [BLOCKING] provision byPhone GSI live on kmv-auth-electro + seed kph-tier/baseline tier/Kurt→defcon34 (D-05) [Wave 2]
- [x] 12-06-PLAN.md — Controller wiring: caller-ID→/tel mint→entitled tier composed with the unchanged §24 gate + fail-closed (D-02/D-05/D-04) [Wave 2]

**Wave 3** *(blocked on Wave 2 completion)*

- [ ] 12-07-PLAN.md — Minimal secure telephony-edge deploy: POP-locked SG + SSM valueFrom + Dockerfile (D-01/D-04) [Wave 3]

**Wave 4** *(blocked on Wave 3 completion)*

- [ ] 12-08-PLAN.md — Manual §19-D cellular proof (D-06) [Wave 4]

**Waves:** 1 → {12-01, 12-02, 12-03, 12-04} → 2 → {12-05, 12-06} → 3 → {12-07} → 4 → {12-08}

### Phase 13: VoIP.ms Telephony — Physical Payphone

**Goal:** The physical payphone handset converses naturally with the agent — register the ATA on its own `payphone-ata` subaccount, verify the VoIP.ms echo test, call the Klanker DID, and tune ATA gain/DTMF only if measured necessary. (Spec Phase E, §4 / §19-E; no Klanker code specific to the ATA.)
**Requirements**: TBD
**Depends on:** Phase 12
**Plans:** 0 plans
**Success Criteria** (what must be TRUE):

  1. The ATA is registered on its own `payphone-ata` subaccount (isolated credential, incapable of expensive destinations) and passes the VoIP.ms echo test independently
  2. The payphone calls the Klanker DID like any other telephone and holds a natural two-way conversation (spec §19-E exit criterion)
  3. Any gain/DTMF tuning needed is documented; no Klanker-side code change is required specifically for the ATA (§22.8)

### Phase 14: VoIP.ms Telephony — Production Hardening

**Goal:** The telephony edge is production-ready and DEF-CON-hostile-safe — Terraform/Terragrunt for an isolated `telephony-edge` deploy, SSM secrets, alarms + dashboards, failure routing, load/concurrency test, and an operations runbook. (Spec Phase F, §15 / §17 / §18 / §25 / §19-F / §20.)
**Requirements**: TBD
**Depends on:** Phase 13
**Plans:** 0 plans
**Success Criteria** (what must be TRUE):

  1. An isolated `telephony-edge` service is deployed via Terraform/Terragrunt (separately deployable from `voice`/`auth`); SIP/RTP ingress is restricted to VoIP.ms POP ranges and ARI is never internet-exposed
  2. Telephony metrics/alarms exist (`ActivePstnCalls`, setup latency, packet loss/jitter, hangup reason, gate-fail rate, ANY outbound attempt, balance drop) without caller-ID cardinality
  3. Failure routing is verified: pipeline mid-call error, lost Klanker media, registration failure, and quota-denied all fail closed without leaving open PSTN charges; outbound calling is disabled everywhere
  4. An operations runbook covers registration, DID routing kill-switch, credential rotation, and one-way-audio debugging; the spec §20 Definition of Done checklist is satisfied
