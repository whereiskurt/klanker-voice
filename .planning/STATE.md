---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: milestone
status: executing
stopped_at: Phase 4 Plan 02 executed (voice deploy infra)
last_updated: "2026-07-05T23:35:35Z"
last_activity: 2026-07-05
last_activity_desc: "Phase 4 Plan 02 executed: voice ECS task/service enabled+wired, webrtc_udp SG narrowed to 20000-20100, kmv-voice-usage table + least-privilege task IAM, session-count autoscale (1->4) — module-level terraform validate clean; live terragrunt plan blocked by expired AWS SSO session"
progress:
  total_phases: 7
  completed_phases: 3
  total_plans: 22
  completed_plans: 18
  percent: 82
current_phase: 4
current_phase_name: Voice Service Deployed & Quota Enforcement
---

# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-07-04)

**Core value:** The conversation must feel slick — ≤1.2s voice-to-voice latency with natural barge-in and ElevenLabs-quality speech.
**Current focus:** Phase 4 — Voice Service Deployed & Quota Enforcement

## Current Position

Phase 1 (Local Pipeline & Latency Harness): ✅ COMPLETE — 5/5 plans, verification PASSED 5/5 (amended latency criterion; 01-VERIFICATION.md)
Phase 2 (Infra Skeleton): ✅ COMPLETE — 7/7 plans merged, verification PASSED 5/5 (02-VERIFICATION.md; TLS handshake deferred to Phase 4 by design)
Phase 3 (Auth Service & Access Codes): ✅ COMPLETE — 4/4 plans merged, verification 4/5 (03-VERIFICATION.md). run.auth ported to apps/auth/webapp; access-code→tier + login→token bridge; OIDC RS256 JWT tokens; kv CLI. JWKS signing key live in SSM; kmv-auth-electro seeded (demo/kphdemo123 + tiers). Criterion-3 no-access GUIDANCE deferred to Phase 5 (logic done); deployed E2E is a Phase-4 verification item.
Phase 4 (Voice Service Deployed & Quota Enforcement): IN PROGRESS — Plan 02/6 done (04-02-SUMMARY.md): voice ECS task/service enabled + wired from voice/service.hcl locals (public-IP, site.hcl ecs_tasks/ecs_services flipped on); webrtc_udp SG narrowed to 20000-20100/udp; kmv-voice-usage DynamoDB table (electro, TTL expiresAt); dedicated least-privilege task-role IAM (new ecs-task module capability); ecs-service module extended for custom-metric TargetTrackingScaling; voice autoscaling min1/max4 on ActiveSessions. Module-level `terraform validate -backend=false` clean on all 3 touched modules; live `terragrunt run-all validate`/`plan` BLOCKED by an expired AWS SSO session (Developer sso_session, InvalidGrantException) — must be refreshed (`aws sso login`) before 04-03 applies. Plans 03-06 (deploy image + ICE smoke test, quota enforcement, idle teardown, kv operator loop) remain.
Phase 6 (Latency v2) + Phase 7 (KPH Knowledge Base): scoped, deferred (Phase 7 has a router/recorded-transcript design evolution captured in 07-DESIGN-NOTES.md)
Status: Phases 1+2+3 complete; Phase 4 in progress (2/6 plans)
Last activity: 2026-07-05 — Phase 4 Plan 02 executed: voice deploy infra (SG narrow, task/service enable+wire, usage table, least-privilege IAM, session-count autoscale)

### Phase 4 handoff (the auth contract Phase 4 consumes)

- JWT ACCESS token contract (pinned in 03-03-SUMMARY): issuer https://auth.klankermaker.ai/use1/api/oidc, jwks .../use1/api/oidc/jwks, aud https://voice.klankermaker.ai, RS256, scope voice, TTL 3600s, claims https://klankermaker.ai/tier_id (string, "no-access" default) + https://klankermaker.ai/group (string|null). Voice service uses PyJWT+PyJWKClient to validate offline — implemented in 04-01 (apps/voice/src/klanker_voice/auth.py).
- Live: DynamoDB kmv-auth-authjs + kmv-auth-electro (ACTIVE, seeded); SSM /kmv/secrets/use1/oidc/jwks (RS256 JWK Set, kid kmv-oidc-m-zCTIi5).
- Phase 4 will also: re-measure deployed voice-to-voice p50/p95 vs the ~1402ms local baseline (us-east-1 proximity expected to improve it), and build the usage table + quota enforcement (QUOT-01..05) against the tiers this phase defined.
- 04-01 done: production entrypoint (server.py), offline JWT validation (auth.py), public-IP+STUN ICE gathering (webrtc.py), Dockerfile — all unit-tested (30 new tests, 90/90 total) and the Docker image live-verified (build + running container). Known gap: real ECS task-metadata shape and live ICE/SDP-munging interop are unverified until 04-03's deployed smoke test.

Progress: [████████░░] 82%

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

## Deferred Items

Items acknowledged and carried forward from previous milestone close:

| Category | Item | Status | Deferred At |
|----------|------|--------|-------------|
| *(none)* | | | |

## Session Continuity

**Resume file:** .planning/phases/04-voice-service-deployed-quota-enforcement/04-03-PLAN.md

Last session: 2026-07-05T23:35:35Z
Stopped at: Phase 4 Plan 02 executed (voice deploy infra: SG narrow, task/service enable+wire, usage table, least-privilege IAM, session-count autoscale)
Resume: before executing 04-03-PLAN.md (deploy the 04-01 container image + ICE smoke test), first refresh the AWS SSO session (`aws sso login --profile klanker-terraform`) and confirm `terragrunt run --all validate` + a targeted `terragrunt plan` on the ecs-task/ecs-service units are clean — 04-02 could not complete this live check in-session (expired SSO refresh token). Then continue through Plan 06 (kv operator loop), in order per 04-PATTERNS.md wave sequencing.
Also live/done this session: 04-01 executed (server.py, auth.py, webrtc.py, Dockerfile — 3 tasks, 9 files, 90/90 tests pass, Docker image live-verified via build+run); 04-02 executed (network SG narrow + IAM/sysctl module extensions + voice service.hcl wiring — 4 commits, 8 files, module-level terraform validate clean); earlier this session: KPHv1 (Kurt's voice clone, voice_id 6zcBdCPOI1TDYCTSsqUv) swapped into apps/voice/pipeline.toml (flash_v2_5-supported); OIDC JWKS signing key created in SSM /kmv/secrets/use1/oidc/jwks; kmv-auth-electro seeded with demo/kphdemo123 + tiers; Phase 7 router/recorded-transcript design evolution captured in 07-DESIGN-NOTES.md.
Note: git guard is a harmless `rm -f` wrapper in ~/.zshrc (footgun-prevention) — avoid `rm -f`/`-r` in non-interactive shells (use plain `rm`), git itself is fine.
