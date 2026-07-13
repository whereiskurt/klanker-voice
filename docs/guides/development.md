<!-- generated-by: gsd-doc-writer -->
# Development Guide

How the klanker-voice repo is organized, and how to work on each part of it. For first-time
environment setup (secrets, provider keys, running the pipeline locally for the first time),
see [`docs/guides/getting-started.md`](getting-started.md). For `pipeline.toml`, environment
variables, and A/B arm configs, see [`docs/guides/configuration.md`](configuration.md). For
running the test suite and the eval-scenario harness, see
[`docs/guides/testing.md`](testing.md).

---

## Repo layout

The repo is a small monorepo with four deployable pieces plus shared infra/docs:

```
apps/
  voice/            the pipeline service (bot.py, server.py, console.py)
    client/          Vite + React browser SPA
    asterisk/         local/cloud Asterisk dev edge (ARI, PJSIP, docker-compose)
    knowledge/         manifest, router, per-topic packs, style layer, BM25 index
    prompts/           concierge system prompt
    scenarios/         eval-harness YAML scenarios (barge-in, greeting, knowledge, retrieval)
    scripts/           dev/ops scripts (env bootstrap, greetings, ambience, knowledge refresh)
    src/klanker_voice/ Python package: pipeline, telephony, knowledge, harness
    configs/           A/B arm configs + telephony/voice2 variant configs
    tests/             pytest suite
  auth/
    webapp/           Next.js OIDC identity service (magic-link, access-code -> tier -> quota)
kv/                   Go CLI: access-code CRUD, usage/session visibility, deploy helpers
infra/
  terraform/          Terraform + Terragrunt: modules/ + live/site/ (region, global, services)
docs/                 architecture, dataflows, guides, techniques, operator runbooks, design specs
.planning/            GSD phase-planning artifacts (see "How this repo is planned" below)
```

The four things that actually deploy independently are: the voice service (`apps/voice`,
which also serves the built browser client as static files), the auth webapp
(`apps/auth/webapp`), the Asterisk telephony edge (`apps/voice/asterisk`, built from the
voice service's telephony controller), and the `kv` operator CLI (`kv/`, run locally, not
deployed).

---

## Voice service — `apps/voice/`

Python 3.12, managed with [`uv`](https://docs.astral.sh/uv/), built on
[Pipecat](https://github.com/pipecat-ai/pipecat) `~=1.5.0`. See
`apps/voice/pyproject.toml` for the exact dependency set.

### Where things live

- `bot.py` — pipeline entry point; builds and runs the Pipecat pipeline for local dev
  (`-t webrtc`) and reads `pipeline.toml` / `KLANKER_PIPELINE_CONFIG` for stage selection.
- `server.py` — the FastAPI app: `/api/offer` SmallWebRTC signaling, health checks, and
  serving the built client (`client/dist/`) as static files in deployed environments.
- `console.py` — a terminal-only pipeline runner for quick manual smoke-testing without a
  browser.
- `src/klanker_voice/` — the installable package (`klanker-voice`, see
  `[tool.hatch.build.targets.wheel] packages = ["src/klanker_voice"]`):
  - `pipeline.py`, `factories.py`, `config.py` — pipeline assembly and stage-provider
    factories driven by `pipeline.toml`.
  - `auth.py`, `quota.py`, `session.py` — OIDC token validation (JWKS) and the
    concurrency-lease/quota engine; see
    [`docs/dataflows/auth-quota.md`](../dataflows/auth-quota.md).
  - `duplex.py`, `rtvi.py`, `webrtc.py`, `observers.py` — full-duplex/backchannel handling,
    RTVI client events, and the SmallWebRTC transport wiring; see
    [`docs/dataflows/conversation-loop.md`](../dataflows/conversation-loop.md) and
    [`docs/dataflows/browser-webrtc.md`](../dataflows/browser-webrtc.md).
  - `knowledge/` — `router.py` (keyword/alias topic routing), `retrieval.py` (BM25/FTS5
    chunk retrieval), `prompt_assembly.py`, `lint.py` (advisory content lint); see
    [`docs/dataflows/knowledge-retrieval.md`](../dataflows/knowledge-retrieval.md).
  - `telephony/` — `ari.py`, `controller.py`, `gate.py`, `media.py`, `rtp_socket.py`,
    `transport.py` — the Asterisk ARI client and RTP media bridge; see
    [`docs/dataflows/telephony-voipms.md`](../dataflows/telephony-voipms.md).
  - `harness/` — `judge.py` (LLM-judge eval), `report.py` (latency table rendering),
    `__main__.py` (the `report`/`compare` CLI) — see "Eval harness" below.
  - `variants.py`, `pronunciation.py`, `version.py` — voice1/voice2 variant routing,
    pronunciation normalization, and the build-version stamp.
- `configs/` — alternate `pipeline.toml`-schema configs: `arm-a.toml` / `arm-b.toml` /
  `arm-c.toml` (A/B latency-tuning arms) and `voice2.toml` / `telephony.toml` (product
  variants). Selected via `KLANKER_PIPELINE_CONFIG`; see "Voice variants" below and
  [`docs/guides/configuration.md`](configuration.md#config-variants).
- `knowledge/` — the curated corpus: `manifest.yaml` (source list), `topics/` (generated
  per-topic packs), `style/` (Kurt's style layer), `index/` (BM25/FTS5 chunk files),
  `router/topic-map.yaml`, `transcripts/`, `corpus/`, `diagrams/`. See "Knowledge refresh
  workflow" below.
- `scenarios/` — YAML eval scenarios (`greeting.yaml`, `bargein_*.yaml`, `kph_*.yaml`,
  `memory.yaml`) consumed by the eval harness.
- `scripts/` — `bootstrap_env.sh` (SSM → `.env`), `render_greetings.py`, `render_ambience.py`,
  `refresh_knowledge.py`, `say.py`, `audition.py`.
- `prompts/concierge.md` — the KPH system prompt.
- `asterisk/` — the local/cloud Asterisk dev edge (`docker-compose.yml`, `ari.conf`,
  `pjsip.conf`, `extensions.conf`, `Dockerfile`); see
  [`docs/dataflows/telephony-voipms.md`](../dataflows/telephony-voipms.md).
- `tests/` — the pytest suite (41 test modules as of this writing), plus `conftest.py` for
  shared fixtures.

### Build / run commands

`apps/voice/Makefile` is the canonical set of developer targets:

| Command | Description |
|---|---|
| `make env` | Fetch the three provider API keys from SSM and write `apps/voice/.env` (see `scripts/bootstrap_env.sh`) |
| `make voice1-local` | Run the shipped half-duplex `voice1` pipeline locally at `http://localhost:7860` |
| `make voice2-local` | Run the full-duplex `voice2` pipeline (`KLANKER_PIPELINE_CONFIG=configs/voice2.toml`) locally at `http://localhost:7860` |
| `make greetings` | Render the pre-recorded KPH greeting clips from the configured `pipeline.toml` voice — **see the warning below before running this** |
| `make ambience` | Render the greenhouse coffee-shop ambient bed (requires an ElevenLabs key with `sound_generation` permission) |
| `make knowledge` | Regenerate curated knowledge packs + retrieval indexes from the corpus (`scripts/refresh_knowledge.py`) — see "Knowledge refresh workflow" below |
| `make say TEXT="..."` | Speak a text block through the configured KPH voice — a deterministic voice-output smoke test |

Outside the Makefile, `uv sync` installs dependencies (including the `dev` group —
`pytest`, `pytest-asyncio`, and Pipecat's `evals`/`local` extras) and `uv run python -m
klanker_voice.harness ...` runs the eval-harness CLI (see below).

### Code style

There is no linter or formatter configured for the voice service (no `ruff`, `black`, or
`mypy` config in `apps/voice/`) and no lint step in CI (`.github/workflows/build-voice.yml`
builds and deploys the Docker image; it does not run `pytest` or a linter). Match the
existing code's style — module-level docstrings explaining the "why" of non-obvious design
choices (see `scripts/refresh_knowledge.py` for the fullest example of this pattern), typed
function signatures, and `from __future__ import annotations` in newer modules.

### Voice variants — `pipeline.toml` vs `configs/voice2.toml`

The voice service ships two product variants of the same pipeline, selected via the
`KLANKER_PIPELINE_CONFIG` environment variable and routed per-request via
`klanker_voice/variants.py` and `server.py`'s `/api/offer?variant=voice2`:

- **`pipeline.toml`** (voice1, the default) — half-duplex, Deepgram Nova-3 STT with local
  VAD/`smart_turn_v3` turn detection.
- **`configs/voice2.toml`** (voice2, full-duplex) — differs from `pipeline.toml` in exactly
  two ways, per the file's own header comment: (1) `[stt]` uses Deepgram Flux for
  server-side end-of-turn detection instead of Nova-3 + local VAD, and (2) a `[duplex]`
  table turns on the `DuplexController` for backchannel-aware barge-in. Everything else
  (LLM, TTS voice, persona, knowledge) is intentionally identical so the two variants are a
  clean A/B on interactivity alone.

`configs/telephony.toml` is the variant served over the Asterisk/PSTN path, and
`configs/arm-a.toml` / `arm-b.toml` / `arm-c.toml` are latency-tuning A/B arms used by the
harness (see [`docs/TUNING.md`](../TUNING.md) for verdicts and
[`docs/guides/configuration.md`](configuration.md) for the full config reference).

All of these reuse the exact same TOML schema as `pipeline.toml` — never fork the schema
per-variant.

### The eval harness — `src/klanker_voice/harness/`

The harness (`python -m klanker_voice.harness`, or `apps/voice/scenarios/*.yaml` driven)
runs scripted conversation scenarios against a live pipeline and judges the result:

- `judge.py` — `judge_factory`, the Anthropic LLM-judge used by scenario `eval:` blocks to
  score whether a bot response meets a natural-language expectation (see any
  `scenarios/*.yaml` `judge:` block).
- `report.py` — `Report` (loads a harness JSON artifact) and `build_comparison_table`; turns
  raw per-turn latency data into the per-stage p50/p95 tables used for A/B verdicts.
- `__main__.py` — the `report` / `compare` CLI:
  ```bash
  uv run python -m klanker_voice.harness report artifacts/harness/a.json
  uv run python -m klanker_voice.harness compare a.json b.json
  ```
  `report` re-renders the per-stage latency table for one or more past runs; `compare`
  renders a side-by-side per-stage diff across two or more artifacts. Per the module's own
  docstring, threshold verdicts (the check/warn marks in the table) never affect the exit
  code — both subcommands exit 0 on a successful load. Exit code 1 is reserved for genuine
  I/O or schema errors (missing file, invalid JSON, wrong `schema_version`).

Scenarios live in `apps/voice/scenarios/*.yaml` (`greeting.yaml`, `bargein_early.yaml`,
`bargein_mid.yaml`, `bargein_monologue.yaml`, `kph_knowledge_*.yaml`,
`kph_retrieval_*.yaml`, `kph_router_accuracy.yaml`, `kph_tour_mode.yaml`,
`kph_unknowns.yaml`, `kph_crude_humor_guard.yaml`, `kph_cache_verify.yaml`, `memory.yaml`).
Each scenario defines a synthetic user (audio synthesized locally via `kokoro`, no extra API
key needed), a judge (transcription via `moonshine`, evaluation via the Anthropic judge
factory), and a sequence of `turns` with `expect:` blocks (an `event` + optional `within_ms`
budget and/or natural-language `eval:` description). See
[`docs/guides/testing.md`](testing.md) for how to actually run scenarios end-to-end.

### Knowledge refresh workflow

`make -C apps/voice knowledge` (`scripts/refresh_knowledge.py`) regenerates KPH's knowledge
from `knowledge/manifest.yaml`: it distills curated per-topic packs and Kurt's style layer,
chunks the corpus into the BM25/FTS5 retrieval index (`knowledge/index/{topic}/*.jsonl`),
and runs an advisory content lint over every generated output. It is a deliberate, offline
script — never run during a live session — and it **never auto-commits**. Output lands as an
ordinary git diff in the tracked `apps/voice/knowledge/` tree, which a human reviews before
committing (`--dry-run` / `--out-dir` write to a scratch directory instead, for a first look
that doesn't touch the tracked tree). This human git-diff review gate is a hard project rule
— see the [`kv-refresh-knowledge` skill](../../.claude/skills) for the full guided workflow,
and [`docs/dataflows/knowledge-retrieval.md`](../dataflows/knowledge-retrieval.md) for how
the router and retrieval index are consumed at runtime.

### Greeting audio — do not regenerate casually

**Warning:** the first-connect greeting the browser client plays the instant you tap the mic
is not a live TTS call — it's a single hand-picked, hand-spliced audio take (see the
`feat(voice): new first-connect greeting — hand-picked spliced take` commit). `make
greetings` / `scripts/render_greetings.py` re-renders `client/public/greetings/*.mp3` from
whatever text and voice settings are currently in `pipeline.toml`, which **overwrites that
hand-picked clip** with a fresh, un-curated TTS render. Only run `make greetings` when you
deliberately intend to replace the greeting (e.g. after a genuine `voice_id` change) — and
if you do, expect to re-audition and re-splice a new take by hand afterward, not just accept
the raw render. `pipeline.toml`'s own `[tts]` comment reinforces this: any `voice_id` change
requires re-running `make greetings` and committing the new clips, because
`tests/test_greeting_voice_drift.py` fails CI if the committed clips drift from the
configured voice — so a deliberate voice change and a casual re-render are the two very
different situations this warning is drawing a line between.

---

## Browser client — `apps/voice/client/`

Vite 8 + TypeScript + React 19, served as static files by the voice service's FastAPI app
(`server.py`) in deployed environments; runs standalone via Vite's dev server locally.

```
apps/voice/client/package.json  scripts: dev, build, preview, test
```

| Command | Description |
|---|---|
| `npm run dev` | Vite dev server (talks to a locally-running voice service for `/api/offer`) |
| `npm run build` | `tsc --noEmit` (typecheck) then `vite build` — output goes to `client/dist/`, which `server.py` serves in deployed environments and which `build-voice.yml` copies to S3/CloudFront |
| `npm run preview` | Preview a production build locally |
| `npm test` | `vitest run` — the client's unit/component test suite (`src/**/*.test.ts(x)`, e.g. `App.flow.test.tsx`, `transport/useVoiceSession.greeting.test.ts`, `greeting/greetingPlayer.test.ts`) |

No ESLint or Prettier config is present in `apps/voice/client/` — match existing TypeScript
style. See [`docs/dataflows/browser-webrtc.md`](../dataflows/browser-webrtc.md) for how the
client negotiates a SmallWebRTC session and consumes RTVI events.

---

## Auth service — `apps/auth/webapp/`

Next.js 16 (App Router), `next-auth` v5 beta, an embedded `oidc-provider` issuer, and
ElectroDB/DynamoDB for access-code/tier/usage entities — a deliberate port of the `run.auth`
service from `defcon.run`. See `apps/auth/webapp/package.json` for exact dependency
versions.

| Command | Description |
|---|---|
| `npm run dev` | `next dev` |
| `npm run build` | `next build` |
| `npm start` | `next start` |
| `npm run lint` | `eslint` — config at `apps/auth/webapp/eslint.config.mjs` |
| `npm test` | `vitest run` |

See [`docs/dataflows/auth-quota.md`](../dataflows/auth-quota.md) for magic-link/OIDC and the
access-code → tier → quota flow, and
[`docs/guides/configuration.md`](configuration.md#auth-service-appsauthwebapp) for
environment variables.

---

## Telephony edge — `apps/voice/asterisk/`, `src/klanker_voice/telephony/`

The PSTN front door: real DIDs provisioned through VoIP.ms terminate on an Asterisk instance
(ARI + PJSIP, ulaw-only), which hands each call to `klanker_voice.telephony`'s ARI
controller over a `Stasis()` dialplan handoff. The controller bridges 20ms-paced RTP audio
into the same pipeline the browser client uses.

- `apps/voice/asterisk/` — `docker-compose.yml`, `ari.conf`, `pjsip.conf`,
  `extensions.conf`, `http.conf`, `rtp.conf`, `Dockerfile`, `entrypoint.sh`,
  `render_configs.py`, and a `sipp/` load-test harness. `docker-compose.yml` is the local
  dev edge — bring it up to test the telephony path without a real DID.
- `src/klanker_voice/telephony/` — `ari.py` (ARI REST/WebSocket client), `controller.py`
  (call orchestration), `gate.py` (DTMF PIN answer-gate), `media.py` / `rtp_socket.py`
  (RTP media bridge, the 20ms send clock), `transport.py`, `types.py`, `config.py`, and a
  `__main__.py` entry point.

The controller is its own standalone process — no FastAPI, no HTTP server of its own, and it
never imports (or is imported by) `server.py`. Run it alongside the local Asterisk
docker-compose stack:

```bash
cd apps/voice
KLANKER_PIPELINE_CONFIG=configs/telephony.toml \
ASTERISK_ARI_URL=http://127.0.0.1:8088 \
ASTERISK_ARI_USERNAME=klanker \
ASTERISK_ARI_PASSWORD=... \
TELEPHONY_ACCESS_PIN=... \
TELEPHONY_PASSPHRASE_WORDS='w1 w2 w3 w4' \
uv run python -m klanker_voice.telephony.controller
```

(`python -m klanker_voice.telephony` is an equivalent entry point to the same controller;
`asterisk/README.md` documents `klanker_voice.telephony.controller` as the canonical one.)
ARI/Asterisk secrets are read from the environment only — never from `pipeline.toml` /
`configs/telephony.toml`. See
[`docs/dataflows/telephony-voipms.md`](../dataflows/telephony-voipms.md) for the full
inbound-call sequence and the RTP-pacing fix history, and
`docs/operators/voipms-provisioning-runbook.md` for provisioning a real DID.

---

## Operator CLI — `kv/`

Go 1.26, `spf13/cobra` v1.10.2 for the command tree (module `github.com/whereiskurt/klanker-voice/kv`),
`aws-sdk-go-v2` for AWS access — structurally a sibling of klanker-maker's `km`.

```
kv/
  cmd/kv/main.go              entry point -> internal/app/cmd.Execute()
  internal/app/cmd/           subcommands: code.go, tier.go, usage.go, killswitch.go,
                               knowledge.go, smoke.go, voipms.go, root.go (+ *_test.go)
  internal/app/electro/       ElectroDB-equivalent DynamoDB entity modeling in Go
```

Build and test with the standard Go toolchain from `kv/`:

```bash
go build ./...
go test ./...
```

See [`docs/guides/configuration.md`](configuration.md#kv-cli-kv) for the environment
variables `kv` reads, and `kv --help` (or any subcommand's `--help`) for the current command
tree — it's the authoritative source since the CLI evolves independently of this doc.

---

## Infrastructure — `infra/terraform/`

Terraform + Terragrunt, deliberately reused from the `defcon.run.34` conventions rather than
designed from scratch.

```
infra/terraform/
  modules/     ecs-cluster/, ecs-service/, ecs-task/, network/, dynamodb/, ecr/, certs/,
               cloudfront/, cloudfront-assets/, secrets/, site/, email/, github-oidc/
  live/site/   the environment-specific Terragrunt tree (region/global/service wiring)
  providers/   provider configuration
```

Secrets flow SOPS → SSM SecureString → container `valueFrom` (see `.sops.yaml`); CI deploys
via GitHub Actions with OIDC-to-AWS (no long-lived CI credentials) — see
`.github/workflows/build-voice.yml`, `build-auth.yml`, `deploy.yml`,
`terragrunt-plan.yml`, `terragrunt-apply.yml`, and `gitleaks-scan.yml`. Full details in
[`docs/guides/configuration.md`](configuration.md#infrastructure-infraterraform) and
[`docs/guides/deployment.md`](deployment.md).

---

## How this repo is planned

Day-to-day feature work in this repo goes through a structured planning workflow rooted in
`.planning/` (GSD-style phase plans — `.planning/ROADMAP.md`, `.planning/phases/*/`,
`.planning/STATE.md`) and, for larger design decisions, a dated design-spec convention under
`docs/superpowers/specs/` (e.g. `docs/superpowers/specs/2026-07-04-klanker-voice-design.md`,
the authoritative project design spec) and `docs/superpowers/plans/`. If you're proposing a
substantial change, check whether a relevant spec already exists there before writing code —
see [`.github/CONTRIBUTING.md`](../../.github/CONTRIBUTING.md), which asks contributors to
open an issue first for anything beyond a small fix, precisely because this architecture is
opinionated and most of the reasoning behind it is already written down in one of those two
places.

---

## Branch and PR conventions

Recent history (`git log`) shows a consistent, lightly-scoped conventional-commit style:
`<type>(<scope>): <description>`, e.g. `fix(12-07): pace outbound RTP on a real-time 20ms
clock`, `feat(voice): new first-connect greeting`, `docs(12-08): resolve
telephony-outbound-garble debug session`. The `<scope>` is frequently a phase/plan
identifier from `.planning/` (e.g. `12-07`) rather than a component name — that's an
artifact of the GSD phase-planning workflow, not a rule you need to invent a phase number
for on a small PR; a component scope (`voice`, `auth`, `telephony`) is equally fine.

Feature branches are typically named `gsd/phase-{phase}-{slug}` for planned phase work (see
`.planning/config.json`'s `phase_branch_template`), merged to `main` via pull request. For
contribution mechanics, sign-off requirements (DCO), and the PR review process, see
[`.github/CONTRIBUTING.md`](../../.github/CONTRIBUTING.md) — all contributions require a
`Signed-off-by:` trailer (`git commit -s`), and pipeline changes are ultimately judged by
ear on the live service, not just by passing tests.

---

## Related docs

- [`docs/guides/getting-started.md`](getting-started.md) — first-time local setup
- [`docs/guides/configuration.md`](configuration.md) — `pipeline.toml`, env vars, A/B arms, per-service config reference
- [`docs/guides/testing.md`](testing.md) — pytest + eval-scenario harness, how to run and interpret both
- [`docs/architecture/overview.md`](../architecture/overview.md) — system architecture
- [`docs/dataflows/`](../dataflows/) — per-path data flow docs (browser WebRTC, telephony, conversation loop, auth/quota, knowledge retrieval)
- [`.github/CONTRIBUTING.md`](../../.github/CONTRIBUTING.md) — contribution process, DCO, and review expectations
