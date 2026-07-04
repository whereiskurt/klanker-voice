# Walking Skeleton — klanker-voice

**Phase:** 1
**Generated:** 2026-07-04

## Capability Proven End-to-End

A developer speaks into the mic — on the localhost web page or in the terminal — and K, the KlankerMaker concierge, greets first, answers in its own ElevenLabs voice within the latency budget, remembers the conversation, and can be interrupted mid-speech, using only three provider API keys (Deepgram, Anthropic, ElevenLabs).

The "deployment" for this phase is deliberately local: the documented full-stack run commands are `make -C apps/voice env` then `uv run python bot.py -t webrtc` (web, prod transport path) or `uv run python console.py` (terminal iteration loop). The deployed Fargate service in Phase 4 runs this same code and consumes `pipeline.toml` + `prompts/concierge.md` unchanged.

## Architectural Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Pipeline framework | pipecat-ai pinned `~=1.5.0` (Settings objects, PipelineWorker, aggregator-owned VAD) | 1.5.0 absorbed turn detection, latency metrics, eval harness, and interruption truncation; the pin insulates tuned behavior from upstream churn (PITFALLS #11) |
| Runtime & packaging | Python 3.12 + uv, `apps/voice/` in the monorepo, committed `uv.lock` | Matches pipecat base-image default and ARCHITECTURE.md repo layout; lockfile is the supply-chain control |
| Stage vendors | Deepgram (Nova-3 / Flux) → Claude Haiku 4.5 → ElevenLabs Flash v2.5 (WS, word timestamps) | Design-spec commitments; every stage streams; three keys total (PIPE-07) |
| Configuration | Checked-in `pipeline.toml` parsed by stdlib tomllib into frozen dataclasses; `KLANKER_PIPELINE_CONFIG` env override for A/B arm configs | D-09: stage selection/knobs/persona path/voice in config, zero secret fields by construction; prod artifact for Phase 4 |
| Secrets | SSM `/kmv/bootstrap/*` → gitignored `.env` (0600) via `make env` (klanker-application profile, us-east-1) | D-10: SSM is the single source of truth from day one; nothing plaintext in the repo |
| Run modes | pipecat runner `-t webrtc` (SmallWebRTC + bundled prebuilt UI) and thin `console.py` (LocalAudioTransport) sharing one `build_pipeline(cfg, transport)` | D-08: web = verification surface (prod transport path), terminal = iteration surface; the runner has no local-audio route so a separate thin entrypoint is the intended shape |
| Turn detection | Three config-selectable arms: Nova-3+VAD timeout, Nova-3+SmartTurn v3 (ONNX, bundled), Flux external EOT — explicit strategies always; Flux arm never sets local strategies | PIPE-04 A/B is config, not integration work; explicit arms avoid the silent-SmartTurn-default trap |
| Persona | Versioned markdown `apps/voice/prompts/concierge.md` loaded as LLMContext system message; greeting instruction lives in the prompt | PIPE-06 / D-01..D-07; copy iterates without code changes; greet-first via LLMRunFrame on connect |
| Measurement | `UserBotLatencyObserver` → LatencyReportObserver → JSON artifact (schema v1, stage names `vad_stop, stt_final, llm_ttft, tts_first_audio, voice_to_voice`) + rich table, p50/p95, informational verdicts | D-11/D-13; the JSON schema is the contract the Phase 5 HUD and CI gate consume — stage names frozen here |
| Scenario testing | `pipecat eval` YAML scenarios with local kokoro user-audio, moonshine transcription, Anthropic judge factory | Named barge-in/memory/greeting verification from deterministic recorded audio without a fourth vendor key |
| Verdict record | `docs/TUNING.md` — endpointing winner, SmartTurn verdict, eager-EOT decision, chosen voice, final knobs, measured tables | D-12: later phases inherit winning values from one authoritative record |

## Stack Touched in Phase 1

- [ ] Project scaffold (uv project, pinned deps, pytest config, gitignore/secret hygiene) — plan 01
- [ ] Routing — runner HTTP surface serving the prebuilt localhost page + `/api/offer` SmallWebRTC signaling — plan 02
- [ ] Data layer — N/A this phase by design (no database; DynamoDB arrives in Phase 2). The persisted state analogs are `pipeline.toml`, `prompts/concierge.md`, and harness JSON artifacts — real reads and writes exercised every run
- [ ] UI — bundled pipecat-ai-prebuilt page: mic interaction wired through the real WebRTC transport to the pipeline — plan 02
- [ ] Local full-stack run — `make -C apps/voice env` + `uv run python bot.py -t webrtc` / `uv run python console.py` documented and smoke-tested — plans 01-02

## Out of Scope (Deferred to Later Slices)

- Cloud deployment, Dockerfile, Fargate, public IP / ICE delta (Phase 4)
- Auth, OIDC, access codes, quotas, kill-switch (Phases 3-4)
- Custom browser client, captions, orb, HUD, countdown timer (Phase 5 — the prebuilt page is a verification surface, not the product client)
- CI latency regression gate — thresholds stay informational this phase (D-13; gate is Phase 5 conference-freeze work)
- TURN fallback, tool calling, RAG, cross-session memory (v2 requirement list)

## Subsequent Slice Plan

Each later phase adds a vertical slice on top of this skeleton without renegotiating its decisions:

- Phase 2: AWS foundation (terragrunt site "kmv", SES clock) — parallel, independent of this skeleton
- Phase 3: auth service + JWT tier claims — the token contract the voice deploy blocks on
- Phase 4: deploy this exact pipeline (same `pipeline.toml`, same persona) to Fargate with quota enforcement; re-verify latency and barge-in deployed
- Phase 5: real browser client + latency HUD consuming the harness JSON schema v1 stage names; thresholds become a CI gate
