---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: milestone
status: executing
stopped_at: Phase 05 Plan 04 executed (code-complete; live checkpoint deferred to post-05-04 deployed validation)
last_updated: "2026-07-06T06:01:10.140Z"
last_activity: 2026-07-06
last_activity_desc: "Phase 05 Plan 04 executed: gesture-gated mic + SmallWebRTC connect (with a vendor-library rejection-detection fix) + live orb/caption RTVI wiring (code-complete; live checkpoint deferred, 05-04-SUMMARY.md)"
progress:
  total_phases: 8
  completed_phases: 4
  total_plans: 33
  completed_plans: 26
  percent: 50
current_phase: 5
current_phase_name: Browser Client & Conference Readiness
---

# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-07-04)

**Core value:** The conversation must feel slick — ≤1.2s voice-to-voice latency with natural barge-in and ElevenLabs-quality speech.
**Current focus:** Phase 05 — browser-client-conference-readiness

## Current Position

Phase 1 (Local Pipeline & Latency Harness): ✅ COMPLETE — 5/5 plans, verification PASSED 5/5 (amended latency criterion; 01-VERIFICATION.md)
Phase 2 (Infra Skeleton): ✅ COMPLETE — 7/7 plans merged, verification PASSED 5/5 (02-VERIFICATION.md; TLS handshake deferred to Phase 4 by design)
Phase 3 (Auth Service & Access Codes): ✅ COMPLETE — 4/4 plans merged, verification 4/5 (03-VERIFICATION.md). run.auth ported to apps/auth/webapp; access-code→tier + login→token bridge; OIDC RS256 JWT tokens; kv CLI. JWKS signing key live in SSM; kmv-auth-electro seeded (demo/kphdemo123 + tiers). Criterion-3 no-access GUIDANCE deferred to Phase 5 (logic done); deployed E2E is a Phase-4 verification item.
Phase 4 (Voice Service Deployed & Quota Enforcement): IN PROGRESS — Plan 04/6 done. 04-02 (deploy infra): voice ECS task/service enabled + wired, webrtc_udp SG narrowed to 20000-20100/udp, kmv-voice-usage DynamoDB table, least-privilege task-role IAM, session-count autoscale min1/max4. 04-03 (deployed ICE smoke, INFR-03/KV-05 VERIFIED LIVE): `kv smoke` against `https://voice.klankermaker.ai` reports PASS — ICE connected, host+srflx candidates, 244 RTP packets — real UDP media flowing on the deployed public-IP Fargate task. 04-04 (race-safe quota enforcement, QUOT-01/QUOT-02/QUOT-04/INFR-06 done, see 04-04-SUMMARY.md): `usage.ts` (4 ElectroDB entities) + `quota.py` (typed 5-way start-gate reject, atomic-enough heartbeat lease, 15s tick with rollup+auto-trip) + `session.py` (`SessionLifecycle`: service timer hard-stop, ActiveSessions CloudWatch metric, ECS scale-in protection) — 32 new tests, 134/134 total pass. Known Gap: the deployed voice task role's IAM does not yet grant cross-table read on `kmv-auth-electro` (the tiers table) — real `/api/offer` calls would fail closed at `read_tier()` until that IAM statement is added; must fix before live-traffic verification. Plans 05-06 (idle teardown + spoken wind-down, kv operator loop) remain.
Phase 5 (Browser Client & Conference Readiness): IN PROGRESS — Plan 04/7 done (05-01, 05-02, 05-03, 05-04-SUMMARY.md). 05-01: server-side RTVI wiring (RTVIProcessor after transport.input(), RTVIObserver w/ audio-level flags; LatencyReportObserver emits one kmv-latency RTVIServerMessageFrame/turn for the HUD; server.py StaticFiles mount for client/dist w/ 404 deep-link fallback). 05-02 (CLNT-04, 05-02-SUMMARY.md): bespoke Vite+TS+React SPA at apps/voice/client/ — UI-SPEC design system encoded as CSS tokens (src/styles/tokens.css); hero orb <OrbCanvas state amplitude /> = WebGL2 shader plasma + orbiting particle ring (sketch 001 winner A) with mandatory 2D/reduced-motion OrbFallback; orbState.ts (ORB_STATE_VISUALS + smoothAmplitude EMA) is the single state->color/motion source 05-04 live wiring reads; D-07 attract landing (Attract.tsx, onTapToTalk seam for 05-03); D-03 multi-stage Dockerfile (node:22-slim client-build -> COPY dist into python:3.12-slim /app/client/dist). Two @pipecat-ai transport deps exact-pinned (1.12.0/1.10.5) + committed lockfile. npm build green, tsc clean, orbState.test.ts 9/9. Both plan checkpoints orchestrator-cleared (npm legitimacy precleared; attract "whoa" approved — authoritative visual sign-off deferred to the 05-04 deployed-AWS checkpoint). Local build needs node>=22.12 (vite8/rolldown floor; used node v23.6.0; image node:22-slim is above it). 05-03 (CLNT-08, 05-03-SUMMARY.md): PKCE (RFC 7636, Web-Crypto-only) + public OIDC client (buildAuthorizeUrl/exchangeCode, verified against the RFC's own known-answer vector); in-memory-only tokenStore (no localStorage/cookie, grep-gated) is the Bearer source 05-04 reads; Callback.tsx (state-validated code exchange) + NoAccessGate.tsx (D-13 verbatim exclusive/invite-only copy). Found + fixed two real deploy blockers: (1) the 'voice' OIDC client registration (apps/auth/webapp/src/config/oidc.ts) was still the pre-D-01/D-02 confidential-client shape (client_secret_post + Auth.js callback URIs) — corrected to a public PKCE client (token_endpoint_auth_method: none) with the SPA's own /callback redirect_uri; full auth webapp suite still 33/33; (2) build-voice.yml passes no --build-arg, so Dockerfile now bakes public VITE_OIDC_* as ARG defaults (no secret exists) so the deployed bundle isn't built with them undefined. Checkpoint (live sign-in round-trip against deployed auth+client) deferred to the orchestrator's post-05-04 deployed-AWS validation, not self-approved. 05-04 (CLNT-01/02/03/04, 05-04-SUMMARY.md): gesture-gated getMic.ts (denied/no-device/unsupported, verbatim MicError.tsx copy); voiceSession.ts (PipecatClient + SmallWebRTCTransport -> POST /api/offer, Bearer as an Authorization header — NOT request_data, since small-webrtc-transport 1.10.5 nests it back as camelCase requestData which server.py's pre-gate check doesn't read); connectionState.ts pure reducer (idle/requesting-mic/connecting/connected/rejected/failed, rejected distinct from connected+failed); found + fixed a real vendor-library gap (Rule 2): SmallWebRTCTransport's negotiate() swallows the offer POST's HTTP error and never rejects connect() on a 401/403/429 — added a window.fetch interceptor scoped to one connect() call to detect + surface it (residual known limitation: a possible one-shot background retry before disconnect() lands, flagged for 05-06). useOrbBinding.ts derives live OrbState + EMA-smoothed uAmplitude from RTVI events into the existing OrbCanvas; captionReducer.ts + Captions.tsx render last-exchange-only subtitle captions; Live.tsx + App.tsx route to the live stage only once connected. 40/40 tests pass, tsc/build clean. Checkpoint (live end-to-end conversation) deferred to the orchestrator's post-05-04 deployed-AWS validation — also blocked on the still-open Phase-4 IAM gap (voice task role lacks cross-table read on kmv-auth-electro), not self-approved here.
Phase 6 (Latency v2) + Phase 7 (KPH Knowledge Base): scoped, deferred (Phase 7 has a router/recorded-transcript design evolution captured in 07-DESIGN-NOTES.md)
Status: Executing Phase 05 (4/7 plans done)
Last activity: 2026-07-06 — Phase 05 Plan 04 executed: gesture-gated mic + SmallWebRTC connect (with a vendor-library rejection-detection fix) + live orb/caption RTVI wiring (code-complete; live checkpoint deferred, 05-04-SUMMARY.md)

### Phase 4 handoff (the auth contract Phase 4 consumes)

- JWT ACCESS token contract (pinned in 03-03-SUMMARY): issuer https://auth.klankermaker.ai/use1/api/oidc, jwks .../use1/api/oidc/jwks, aud https://voice.klankermaker.ai, RS256, scope voice, TTL 3600s, claims https://klankermaker.ai/tier_id (string, "no-access" default) + https://klankermaker.ai/group (string|null). Voice service uses PyJWT+PyJWKClient to validate offline — implemented in 04-01 (apps/voice/src/klanker_voice/auth.py).
- Live: DynamoDB kmv-auth-authjs + kmv-auth-electro (ACTIVE, seeded); SSM /kmv/secrets/use1/oidc/jwks (RS256 JWK Set, kid kmv-oidc-m-zCTIi5).
- Phase 4 will also: re-measure deployed voice-to-voice p50/p95 vs the ~1402ms local baseline (us-east-1 proximity expected to improve it), and build the usage table + quota enforcement (QUOT-01..05) against the tiers this phase defined.
- 04-01 done: production entrypoint (server.py), offline JWT validation (auth.py), public-IP+STUN ICE gathering (webrtc.py), Dockerfile — all unit-tested (30 new tests, 90/90 total) and the Docker image live-verified (build + running container). Known gap: real ECS task-metadata shape and live ICE/SDP-munging interop are unverified until 04-03's deployed smoke test.

Progress: [████████░░] 79%

## Performance Metrics

**Velocity:**

- Total plans completed: 0
- Average duration: -
- Total execution time: 0 hours

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| - | - | - | - |

**Recent Trend:**

- Last 5 plans: -
- Trend: -

*Updated after each plan completion*
| Phase 02 P01 | 5 min | 3 tasks | 5 files |
| Phase 04 P01 | 10min | 3 tasks | 9 files |
| Phase 04 P02 | 35min | 3 tasks | 8 files |
| Phase 04 P04 | ~55min | 3 tasks | 10 files |
| Phase 05 P01 | 25min | 3 tasks | 6 files |
| Phase 05 P02 | 35min | 3 tasks | 23 files |
| Phase 05 P03 | 45min | 5 tasks | 17 files |
| Phase 05 P04 | 45min | 3 tasks | 16 files |

## Accumulated Context

### Decisions

Decisions are logged in PROJECT.md Key Decisions table.
Recent decisions affecting current work:

- Roadmap: 5-phase structure per research — local pipeline first (de-risks core value, zero infra dependency), infra parallel (SES clock), auth before voice (JWT/tier-claims contract is voice's hard dependency), client/hardening last
- Roadmap: `kv` CLI has no standalone phase — grows incrementally in Phase 3 (code/tier CRUD) and Phase 4 (usage, kill-switch, smoke test)
- Roadmap: INFR-03 (deployed ICE smoke test) and INFR-06 (autoscaling) live in Phase 4, not Phase 2 — only verifiable once the voice service is deployed
- [Phase 02]: SGUID locked at 6e913c73 — state bucket/lock table tf-kmv-use1-6e913c73 live in 052251888500 — bootstrap-state.sh is the single source (Pitfall 3); same value must land in site.hcl random_suffix default (Plan 02) and gh repo var SGUID (Plan 06)
- [Phase 02]: Apex DMARC via route 2: standalone _dmarc inline unit, make_site_domain=false — zone audit found zero apex mail records and no auth./voice. NS delegation collisions; route 1 would hijack apex inbound MX (Pitfall 6)
- [Phase 02]: kmv-github-delegate CORRECTION — the 02-06 "user-created" role never existed; 02-07 executor created it via sudo-management (which HAS admin in 481723467561, contra 02-06's assumption) + added route53:ListTagsForResource (5th action, needed by aws_route53_zone data source). INFR-07 proof run green. 02-06-SUMMARY/02-USER-SETUP reconciled
- [Phase 01]: Endpointing A/B FINAL — winner Nova-3 + SmartTurn v3 + persona v2, v2v p50 ~1402ms / p95 ~2080ms ACCEPTED (user decision after two measured rounds); Flux LOSES on pipecat 1.5.0 (hard-coded 0.5s ExternalUserTurnStopStrategy hold); eager EOT rejected; prompt caching ruled out at this prompt size (Haiku 4096-token cache minimum vs ~600-token persona); ≤1.2s committed to Phase 6 (ack-masking headline, lighter LLM A/B, optional Flux double-endpointing)
- [Phase 04]: [Phase 04-01]: ENI public-IP lookup keys off the ECS task-metadata MAC address (ec2:DescribeNetworkInterfaces mac-address filter), not an ENI-id field — task metadata v4 doesn't expose the ENI id directly
- [Phase 04]: [Phase 04-01]: Dockerfile resolves the libvpx package name dynamically via apt-cache (libvpx9 on the current Debian trixie base, not the libvpx7 CLAUDE.md documented against an older base); CMD invokes uvicorn directly instead of uv run uvicorn (uv run re-syncs the dev group's pyaudio and breaks the container)
- [Phase 04-02]: Extended the ecs-task module (dedicated per-task IAM role + container systemControls) beyond this plan's declared file scope - no existing mechanism in the codebase could express least-privilege task-role IAM or kernel sysctls; the ecs-cluster module's shared task role is wide-open (dynamodb:*/cloudwatch:*/ssm:*/s3:*/secretsmanager:* on Resource=*)
- [Phase 04-04]: Concurrency-limit enforcement is a consistent Query + conditional write, not a cross-item DynamoDB transaction (the deployed task role's IAM grants no TransactWriteItems) — an atomic per-user counter alternative was rejected because it can't self-heal on a crashed task without a reaper, which would violate D-01's "no reaper" requirement. `release_heartbeat` never calls DeleteItem (not IAM-granted) — it sets `expiresAt` into the past; TTL is the real backstop either way.
- [Phase 04-04]: KNOWN GAP — the deployed voice task role's IAM (`voice/service.hcl`'s `UsageTableCrud` statement) only grants access to `kmv-voice-usage`; `quota.read_tier()` needs `dynamodb:GetItem` on `kmv-auth-electro` (the Phase-3 tiers table) too. A real deployed `/api/offer` call will get `AccessDeniedException` on every non-bypass session today. Add a cross-table read statement to `voice/service.hcl` and re-apply before any live-traffic verification of the quota gate.
- [Phase 05-01]: kmv-latency emission is pushed from the last-observed downstream FrameProcessor (cached in on_push_frame), not a constructor-injected RTVIProcessor reference — keeps the change within observers.py + tests only (no server.py change), since any live processor's push notifies the same pipeline-wide observer set
- [Phase 05-01]: SPA deep-link fallback (client-side callback route) implemented via a global @app.exception_handler(404), not a second catch-all route — a root-mounted StaticFiles("/") prefix-matches every path in Starlette's router, so a later catch-all route would never be reached
- [Phase 05-02]: Inter self-hosted by vendoring the Google-Fonts latin-subset variable woff2 as a static asset (not @fontsource/inter) — UI-SPEC mandates self-hosting, not a package; keeps the npm supply-chain surface minimal (threat T-05-02-SC)
- [Phase 05-02]: Client hero orb is a faithful React port of sketch 001 Variant A (WebGL2 plasma shader + orbiting particle ring); orbState.ts ORB_STATE_VISUALS + smoothAmplitude() EMA is the single state->color/motion source 05-04 live RTVI wiring reads; feature-detection (webgl2 + prefers-reduced-motion) swaps to the calm 2D OrbFallback
- [Phase 05-02]: D-03 multi-stage Dockerfile: node:22-slim client-build stage runs npm ci && npm run build, COPYs dist into /app/client/dist (the 05-01 StaticFiles mount path); client artifacts gitignored+dockerignored (no committed build output). Local build needs node>=22.12 (vite8/rolldown floor) — used node v23.6.0 locally; image node:22-slim is above the floor
- [Phase 05]: [Phase 05-03]: Corrected the 'voice' OIDC client from a stale confidential-client shape (client_secret_post + Auth.js callback URIs) to a public PKCE client (token_endpoint_auth_method: none) with the SPA's own /callback redirect_uri — D-01/D-02 pivoted the voice app away from the Next.js/next-auth assumption this client registration predated
- [Phase 05]: [Phase 05-03]: Baked public VITE_OIDC_* config into the Dockerfile client-build stage as ARG defaults (no secret exists for this PKCE public client) since build-voice.yml passes no --build-arg values
- [Phase Phase 05-04]: Bearer token sent as an Authorization header only (not request_data.access_token) -- small-webrtc-transport 1.10.5 nests requestData back camelCase, not the snake_case request_data server.py's pre-gate token check reads
- [Phase Phase 05-04]: Added a window.fetch interceptor around SmallWebRTCTransport connect() to detect and surface 401/403/429 offer rejections -- the vendor client silently swallows the offer POST's HTTP error and never rejects connect() otherwise

### Pending Todos

- [captured 2026-07-05] Post-conference: swap KPH to an ElevenLabs voice clone trained on Kurt (IVC now / PVC better; Pro plan covers both). Architecture-ready: one-line voice_id change in pipeline.toml [tts]; verify eleven_flash_v2_5 is supported by the clone; re-run the 01-05 audition renderer to A/B clone vs incumbent by ear before switching.
- [captured 2026-07-05, user at Phase-1 sign-off] KPH knowledge base: "massive RAG or something really smart that can steer... all of the knowledge of my repos, and some scripts and stuff I'd train it on." Recommended shape for the voice-latency constraint (two-tier): (1) curated repo/project digests as a large cached system-prompt knowledge pack — once ≥4096 tokens, Haiku prompt caching ENGAGES (0.1× cost, fast cached prefill), turning the 01-04 caching dead-end into a win at knowledge-pack scale; pre-warm at session start; (2) a retrieval tool for depth questions, latency masked by Phase-6 ack-masking (natural synergy). Fine-tuning is the wrong tool; curated context + retrieval is. Candidate Phase 7 or fold into Phase 6 planning discussion.

### Blockers/Concerns

- [Phase 2]: SES production access is a multi-day manual review — request must go out at Phase 2 start (week one)
- [Phase 3]: oidc-provider v9 JWT access tokens with tier claims (Resource Indicators) not yet prototyped — spike early; it is the contract Phase 4 blocks on
- [Phase 4]: STUN srflx behind Fargate 1:1 NAT is source-verified but not live-tested — deployed ICE smoke test is the first Phase 4 deliverable
- [Phase 4]: Confirm the ElevenLabs API key SOPS entry is populated before the voice deploy (flagged by 02-07)
- [Phase 4]: Re-measure deployed voice-to-voice p50/p95 against the 1402ms local baseline as part of the ICE smoke test — expectation (user + analysis 2026-07-05): us-east-1 proximity to Deepgram/Anthropic/ElevenLabs endpoints + fresh-session context (~600 vs ~3000 tokens) should improve on local numbers; new browser↔task WebRTC leg adds ~20-50ms each way
- [Phase 4]: REQUIREMENTS.md's INFR-03 checkbox was auto-marked `[x]` after 04-01 (per the plan's own `requirements:` frontmatter and the standard per-plan mark-complete step) — but INFR-03's text explicitly requires "verified by a deployed ICE smoke test," which is 04-03's job. 04-01 only delivers the code half (auth + candidate gathering + entrypoint, all unit-tested against synthetic fixtures, no live Fargate task yet). Treat INFR-03 as genuinely done only once 04-03's deployed smoke test passes, not from this checkbox alone.
- [Phase 4-02]: AWS SSO session (Developer, profiles klanker-terraform/klanker-application/klanker-management) expired (InvalidGrantException on refresh) - could not run terragrunt run-all validate/plan against live state for the voice deploy infra. Module-level terraform validate -backend=false passed for all touched modules as a substitute. Before 04-03 applies: run 'aws sso login --profile klanker-terraform' interactively, then re-run terragrunt run --all validate + a targeted terragrunt plan on the ecs-task/ecs-service units.
- [Phase 4-04]: BLOCKER for live quota enforcement — the deployed voice task role's IAM does not grant `dynamodb:GetItem` on `kmv-auth-electro` (Phase-3 tiers table); `quota.read_tier()` will get `AccessDeniedException` in production until `voice/service.hcl`'s task role gets a cross-table read statement added and re-applied. All 04-04 tests pass locally against dynamodb-local, where this constraint doesn't exist — this is a real-deployment-only gap. Fix before relying on the quota gate against live traffic.
- [Phase 5-03]: REQUIREMENTS.md's CLNT-08 checkbox is auto-marked [x] per the standard per-plan step, but the plan's own checkpoint (live PKCE sign-in round-trip against deployed auth.klankermaker.ai + a deployed client, no-access gate, refresh re-auth) has NOT been exercised — deferred by explicit orchestrator instruction to the post-05-04 deployed-AWS validation pass. Treat CLNT-08 as genuinely done only once that live check passes, not from this checkbox alone (same pattern as Phase 4's INFR-03). Also: apps/auth must be redeployed alongside voice for the corrected 'voice' OIDC client (public PKCE, /callback redirect_uri) to take effect against the live auth.klankermaker.ai.
- [Phase 5-04]: REQUIREMENTS.md's CLNT-01/CLNT-03/CLNT-04 checkboxes are auto-marked [x] per the standard per-plan step, but the plan's own checkpoint (live end-to-end conversation: reactive orb, dual captions, distinct mic errors against the deployed stack) has NOT been exercised -- deferred by explicit orchestrator instruction to the post-05-04 deployed-AWS validation pass, which is also blocked on the still-open Phase-4 IAM gap (voice task role lacks cross-table read on kmv-auth-electro). Treat these as genuinely done only once that live check passes (same pattern as Phase 4's INFR-03 and Phase 5-03's CLNT-08). CLNT-02 intentionally left Pending -- this plan only delivers its happy path; the requirement's own text bundles auto-retry/UDP-blocked messaging, explicitly 05-06's job.

### Roadmap Evolution

- Phase 05.1 inserted after Phase 5: Operator Admin Panel — gated /admin in auth app (operational-only; transcripts deferred to Phase 7) (URGENT)

## Deferred Items

Items acknowledged and carried forward from previous milestone close:

| Category | Item | Status | Deferred At |
|----------|------|--------|-------------|
| *(none)* | | | |

## Session Continuity

**Resume file:** .planning/phases/05-browser-client-conference-readiness/05-05-PLAN.md

Last session: 2026-07-06T06:00:08.918Z
Stopped at: Phase 05 Plan 04 executed (code-complete; live checkpoint deferred to post-05-04 deployed validation)
Resume: before executing 04-05-PLAN.md (idle teardown + spoken wind-down), be aware 04-04's `SessionLifecycle.on_warning`/`on_stop`/`on_daily_exhausted` are the named hook points already wired and tested — 04-05 fills their bodies (LLM-context injection at -30s, deterministic goodbye TTS at 0, then the actual transport teardown) rather than restructuring the timer. Also: close the Known Gap flagged above (voice task role IAM needs cross-table read on `kmv-auth-electro`) before any live-traffic verification of the quota gate — locally everything passes against dynamodb-local, but a real deployed `/api/offer` call will fail closed at `read_tier()` today.
Also live/done this session: 04-03 Task 3 (deploy checkpoint) completed by the orchestrator — `kv smoke` PASS against the live `https://voice.klankermaker.ai` (ICE connected, host+srflx, 244 RTP packets; INFR-03/KV-05 verified live); 04-04 executed (usage.ts + quota.py + session.py — 3 tasks, 10 files, 32 new tests, 134/134 total pass; caught and fixed a test-isolation gap that had leaked 3 stray items into the real `kmv-voice-usage` table, cleaned up before commit).
Note: git guard is a harmless `rm -f` wrapper in ~/.zshrc (footgun-prevention) — avoid `rm -f`/`-r` in non-interactive shells (use plain `rm`), git itself is fine.
