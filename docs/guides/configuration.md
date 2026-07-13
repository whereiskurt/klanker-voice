<!-- generated-by: gsd-doc-writer -->
# Configuration Guide

klanker-voice is split into five deployable units — the voice service, its browser client, the
auth service, the telephony edge, and the `kv` operator CLI — plus Terraform infrastructure that
wires secrets into all of them. This guide documents every configuration knob, where it lives,
its default, and its effect. It does not print secret values, phone numbers, or PINs.

For the latency-tuning trade-offs behind the `[stt]`/`[turn]` knobs specifically, see
[docs/TUNING.md](../TUNING.md).

## Voice service (`apps/voice`)

### `pipeline.toml` — the stage-selection surface

`apps/voice/pipeline.toml` is the single source of truth for which providers/models the pipeline
uses and how each stage behaves. It is parsed and validated by
`apps/voice/src/klanker_voice/config.py`, which loads the file via `tomllib`, applies range/type
checks, and — critically — **rejects any field whose name looks like credential material**
(`_CREDENTIAL_FIELD_RE` matches `api_key`, `key`, `secret`, `token`, `password`, `credential`,
`bearer`, `auth`, `pin`, `passphrase`, and the standalone field name `words`). Secrets can never
live in this file; they come exclusively from environment variables (see
[Secrets: SOPS → SSM → env](#secrets-sops--ssm--env) below).

The active config path resolves in this order: an explicit path argument → the
`KLANKER_PIPELINE_CONFIG` environment variable → `apps/voice/pipeline.toml`. This is how the A/B
arm configs, `configs/voice2.toml`, and `configs/telephony.toml` (all full clones of the same
schema) get selected without a code change.

#### `[stt]` — speech-to-text

| Field | Default | Effect |
|---|---|---|
| `provider` | `deepgram-nova3` | `deepgram-nova3` or `deepgram-flux`. Flux does server-side end-of-turn detection and causes the `[turn]` table to be ignored (`ExternalUserTurnStrategies` is installed instead). |
| `model` | `nova-3-general` | Deepgram model id. |
| `stt.flux.eot_threshold` | `0.7` | Only read when `provider = "deepgram-flux"`. End-of-turn confidence, range 0.5–0.9. |
| `stt.flux.eager_eot_threshold` | `0.0` | `0` disables eager EOT; nonzero trades LLM spend for lower latency. Range 0.0–0.9. |

#### `[turn]` — local turn detection (ignored when STT provider is Flux)

| Field | Default | Effect |
|---|---|---|
| `strategy` | `smart_turn_v3` | `vad_timeout` or `smart_turn_v3`. |
| `vad_stop_secs` | `0.2` | Silence duration before VAD marks user speech stopped. Must be > 0 and < 5.0s. |
| `user_speech_timeout` | `0.6` | Timeout strategy knob. Must be > 0 and < 5.0s. |

#### `[llm]`

| Field | Default | Effect |
|---|---|---|
| `provider` | `anthropic` | Only `anthropic` is allowed. |
| `model` | `claude-haiku-4-5` | Mandatory override — pipecat 1.5.0 defaults to a Sonnet-tier model otherwise. |

#### `[tts]` — ElevenLabs voice character

| Field | Default | Effect |
|---|---|---|
| `provider` | `elevenlabs` | Only `elevenlabs` is allowed. |
| `model` | `eleven_flash_v2_5` | ElevenLabs streaming model id. |
| `voice_id` | (current: "Burt Fundeck") | ElevenLabs voice id. **Changing this requires re-running `make -C apps/voice greetings`** and committing the new clips — CI's greeting-voice-drift test (`tests/test_greeting_voice_drift.py`) fails otherwise. |
| `speed` | `1.02` | ElevenLabs WS range 0.7–1.2. |
| `stability` | `0.45` | Range 0.0–1.0. Lower = more expressive/variable; higher = steadier. |
| `similarity_boost` | `0.85` | Range 0.0–1.0. Fidelity to the source voice. |
| `style` | `0.3` | Range 0.0–1.0. Low = calm; high = over-the-top. |

`speed`/`stability`/`similarity_boost`/`style` apply to both the live voice and the pre-rendered
greeting — `render_greetings.py` reads the same values, so retuning here means re-running
`make greetings` to keep the welcome clip matched.

#### `[persona]`

| Field | Default | Effect |
|---|---|---|
| `prompt_path` | `prompts/concierge.md` | Path to the system prompt, resolved relative to the config file's own directory. Must exist at load time. |
| `greet_first` | `false` | When `false`, the client plays a pre-rendered greeting on tap (slick-start) and the server must not greet again. |

#### `[knowledge]` — Phase 7 router + retrieval

| Field | Default | Effect |
|---|---|---|
| `manifest` | `knowledge/manifest.yaml` | Curated N-topic manifest + tour priority order. |
| `topic_map` | `knowledge/router/topic-map.yaml` | Router keywords/aliases + confidence floor. |
| `packs_dir` | `knowledge/topics` | Per-topic deep packs (swappable). |
| `style_path` | `knowledge/style/kurt-voice.md` | The stable/cached style-layer prefix. |
| `cache_floor` | `4096` | Claude Haiku 4.5's minimum cacheable system-prefix length. |
| `index_dir` | `knowledge/index` | Per-topic committed BM25/FTS5 chunk files (`{topic}/*.jsonl`). Required to exist when `retrieval_enabled` is true. |
| `retrieval_enabled` | `true` | `false` disables local retrieval entirely — the router never queries. |
| `retrieval_top_k` | `4` | Top-k chunks injected into the system prompt per deep turn. |
| `retrieval_budget` | `1500` | Approximate token cap on injected chunk text. |

All paths are resolved relative to the config file's directory; every referenced file/directory is
existence-checked at load time and raises `ConfigError` if missing.

#### `[quota]` — session limits and wind-down

| Field | Default | Effect |
|---|---|---|
| `heartbeat_renew_interval` | `15`s | Interval between concurrency-lease renewals + accounting ticks. |
| `heartbeat_ttl` | `45`s | A crashed task's lease self-expires after this (must exceed `heartbeat_renew_interval`). |
| `sub_floor_seconds` | `30`s | Blocks a new session if remaining daily time is below this. |
| `per_task_max_sessions` | `5` | Per-task soft concurrency cap; over cap returns a retryable rejection. |
| `auto_trip_ceiling_seconds` | `7200`s (2h) | Site-wide daily aggregate seconds ceiling that auto-engages the kill-switch. |
| `auto_trip_ceiling_dollars` | `40` | Site-wide daily estimated-cost ceiling (coarse blended estimate). |
| `est_cost_per_second` | `0.005` | Coarse blended Deepgram+Haiku+ElevenLabs cost estimate per second (~$18/hr). |
| `winddown_warning_seconds` | `30`s | Lead time before the session max at which the spoken warning is injected. |
| `goodbye_grace_seconds` | `5`s | Cap on letting the deterministic goodbye TTS finish before hard-close. |
| `user_silence_timeout` | `50`s | No user speech for this long triggers idle teardown. Range 10–300s. |
| `reconnect_grace_seconds` | `12`s | Transport-disconnect grace before teardown. Range 1–120s. |
| `warning_copy` | (see file) | The spoken wind-down warning text, injected as a high-priority LLM instruction. |
| `goodbye_copy` | (see file) | The spoken goodbye text, sent straight to TTS bypassing the LLM. |

#### `[greenhouse]` — ambient audio bed

| Field | Default | Effect |
|---|---|---|
| `ambience_enabled` | `true` | Mixes an ambient bed under the voice while the greenhouse (recruiting) mode is active. |
| `ambience_sound` | `coffee-shop` | Loads `assets/ambience/{ambience_sound}.wav` (must be mono, matching `ambience_sample_rate` — the mixer does not resample). |
| `ambience_volume` | `0.06` | Range 0.0–1.0. |
| `ambience_sample_rate` | `24000` | Must match the WAV file and output transport. |

#### `[duplex]` — optional, full-duplex variant only

Absent from `pipeline.toml` (the shipped `voice1` variant is half-duplex by default); set only in
`configs/voice2.toml`.

| Field | Default | Effect |
|---|---|---|
| `enabled` | `false` | Master switch — `false` means no `DuplexController` is inserted. |
| `backchannel_emitter` | `false` | When true, the concierge emits its own short "mm-hm" listening cues while the visitor talks. |
| `max_backchannel_words` | `3` | An utterance longer than this is never treated as a backchannel. |
| `interruption_hold_ms` | `250` | How long a barge-in is held while the controller waits for the first transcript to decide backchannel vs. real interruption. Range 0–2000ms. |
| `emitter_min_talk_seconds` | `8.0` | Visitor must be talking continuously this long before any bot backchannel fires. Range 0–60s. |
| `emitter_min_gap_seconds` | `6.0` | Minimum spacing between emitted bot backchannels. Range 0–60s. |
| `backchannel_words` | curated lexicon | TOML array of strings overriding the default listening-cue lexicon. |
| `emitter_phrases` | `["mm-hm.", "mm."]` | TOML array of strings overriding the bot's own backchannel phrases. |

#### `[telephony]` — media + answer-gate knobs only (no credentials)

| Field | Default | Effect |
|---|---|---|
| `enabled` | `false` | Opt-in; the WebRTC path is byte-unaffected while false. |
| `provider` | `voipms` | Upstream SIP trunk provider. |
| `edge` | `asterisk-ari` | Local call-control edge. |
| `codec` | `pcmu` | μ-law RTP codec. |
| `sample_rate` | `8000` | PCMU clock rate, Hz. |
| `packet_ms` | `20` | RTP packetization interval. |
| `max_concurrent_calls` | `1` | Soft cap on simultaneous ARI calls this controller accepts. |
| `answer_timeout_seconds` | `15` | Bridge/external-media readiness deadline. |
| `hangup_on_pipeline_error` | `true` | Tears the call down on an unhandled pipeline error rather than leaving a silent open line. |
| `require_gate` | `true` | Master switch for the silent answer-gate. `false` is a test/dev-only escape hatch. |
| `gate_mode` | `either` | `dtmf`, `passphrase`, or `either` (both factors accepted, either unlocks). |
| `gate_window_seconds` | `10` | Caller's time budget to unlock before a fail-closed goodbye + hangup. |
| `unlock_tier_id` | `kph-tier` | Fallback tier granted on gate unlock when the caller-ID mint is unconfigured. |
| `tel_mint_url` | `""` (empty) | Base URL of the internal caller-ID mint endpoint. Empty means the mint integration is off and every call falls back to `unlock_tier_id`. |
| `tel_mint_env_var` | `TELEPHONY_ENDPOINT_AUTH_TOKEN` | Name of the env var holding the mint call's bearer token (the token value itself is never in TOML). |

The secrets this table's comments reference — `ASTERISK_ARI_URL`, `ASTERISK_ARI_USERNAME`,
`ASTERISK_ARI_PASSWORD`, `TELEPHONY_ACCESS_PIN`, `TELEPHONY_PASSPHRASE_WORDS` — live in env/SSM
only, never TOML.

### Config variants

| File | Differs from `pipeline.toml` by |
|---|---|
| `configs/voice2.toml` | `[stt]` uses `deepgram-flux`; `[duplex]` is enabled (full-duplex, backchannel emitter on). Served at `/voice2` via `server.py`'s variant routing. Everything else (LLM, TTS voice, persona, knowledge) is intentionally identical to `voice1` for a clean interactivity A/B. |
| `configs/telephony.toml` | Full clone with `[telephony].enabled = true` (the only behavioral difference) and relative paths adjusted (`../`) for its location under `configs/`. Selected via `KLANKER_PIPELINE_CONFIG=configs/telephony.toml`. |
| `configs/arm-a.toml`, `configs/arm-b.toml`, `configs/arm-c.toml` | Endpointing A/B arms differing only in `[stt]`/`[turn]` — see [docs/TUNING.md](../TUNING.md) for what each arm measures and the recorded verdicts. |

### Environment variables (voice service)

Read directly from `os.environ` by `apps/voice/src/klanker_voice/*.py` (never from `pipeline.toml`):

| Variable | Required | Default | Source file | Purpose |
|---|---|---|---|---|
| `DEEPGRAM_API_KEY` | Yes | — | provider SDKs (via `.env`) | Deepgram STT API key. |
| `ANTHROPIC_API_KEY` | Yes | — | provider SDKs (via `.env`) | Claude API key. |
| `ELEVENLABS_API_KEY` | Yes | — | provider SDKs (via `.env`) | ElevenLabs TTS API key. |
| `KLANKER_PIPELINE_CONFIG` | No | `apps/voice/pipeline.toml` | `config.py` | Overrides which pipeline TOML file loads (A/B arms, variants). |
| `KMV_OIDC_ISSUER` | No | `https://auth.klankermaker.ai/use1/api/oidc` | `auth.py` | OIDC issuer used to validate incoming access tokens. |
| `KMV_OIDC_JWKS_URI` | No | `https://auth.klankermaker.ai/use1/api/oidc/jwks` | `auth.py` | JWKS endpoint (fetched once per process, cached by `PyJWKClient`). |
| `KMV_VOICE_AUDIENCE` | No | `https://voice.klankermaker.ai` | `auth.py` | Expected `aud` claim on validated tokens. |
| `KMV_SMOKE_SERVICE_TOKEN` | No | — | `auth.py` | A dedicated smoke/service credential that, on match, bypasses the JWKS lookup entirely (used by CI smoke checks). |
| `KMV_USAGE_TABLE` | No | `kmv-voice-usage` | `quota.py` | DynamoDB table for the concurrency-lease/usage ledger. |
| `KMV_TIERS_TABLE` | No | `kmv-auth-electro` | `quota.py` | DynamoDB table read for tier limits (thin-token design — limits are read here, not from the JWT). |
| `KMV_DYNAMODB_ENDPOINT` | No | (unset = AWS) | `quota.py` | Local/dev/test only; points boto3 at dynamodb-local. Must stay unset in production. |
| `KMV_STUN_URL` | No | `stun:stun.l.google.com:19302` | `webrtc.py` | STUN server for WebRTC ICE. |
| `ECS_CONTAINER_METADATA_URI_V4` | No | (ECS-injected) | `webrtc.py`, `session.py` | Standard ECS task metadata endpoint, used for public-IP discovery / task self-identification. |

### Local dev workflow

`apps/voice/Makefile` targets (all run from `apps/voice/`):

| Target | Effect |
|---|---|
| `make env` | Runs `scripts/bootstrap_env.sh` — fetches the three provider API keys from SSM and writes `.env` (mode 600). See [Secrets](#secrets-sops--ssm--env). |
| `make greetings` | Renders the pre-rendered KPH greeting clips from the configured `pipeline.toml` voice. Re-run after any `voice_id`/TTS-character change. |
| `make ambience` | Renders the greenhouse coffee-shop ambient bed via ElevenLabs sound generation (the API key needs the `sound_generation` permission). |
| `make knowledge` | Regenerates curated knowledge packs + retrieval indexes from the corpus. |
| `make say TEXT="..."` | Speaks a text block through the configured KPH voice — a deterministic voice-output smoke test. |
| `make voice1-local` | Runs the shipped half-duplex `voice1` pipeline locally at `http://localhost:7860`. |
| `make voice2-local` | Runs the full-duplex `voice2` pipeline locally (`KLANKER_PIPELINE_CONFIG=configs/voice2.toml`), same URL. |

`bootstrap_env.sh` reads three SSM SecureString parameters under
`/kmv/secrets/use1/{deepgram,anthropic,elevenlabs}/api_key` using the `klanker-application` AWS
profile in `us-east-1`, refuses to run with shell `xtrace` enabled, and writes `apps/voice/.env`
atomically with mode 600 — no partial `.env` is ever left behind on a fetch failure.

## Client (`apps/voice/client`)

A Vite/React app served as static files by the voice FastAPI app (`server.py`'s
`CLIENT_DIST_DIR` mount at `apps/voice/client/dist`). Build-time environment variables (read via
`import.meta.env`, declared in `src/vite-env.d.ts`):

| Variable | Required | Purpose |
|---|---|---|
| `VITE_OIDC_ISSUER` | Yes | OIDC issuer the client's PKCE flow trusts. |
| `VITE_OIDC_CLIENT_ID` | Yes | OIDC client id registered with the auth service. |
| `VITE_OIDC_AUDIENCE` | Yes | Expected token audience — must match the voice service's `KMV_VOICE_AUDIENCE`. |
| `VITE_OIDC_REDIRECT_URI` | Yes | OAuth redirect URI the auth service must have registered. |
| `VITE_APP_VERSION` | No (defaults to `"dev"`) | Build version stamp shown in the UI. |
| `VITE_APP_BUILT_AT` | No (defaults to `""`) | Build timestamp stamp shown in the UI. |

The four `VITE_OIDC_*` variables are read via a `requireEnv()` helper in `src/config/oidc.ts` and
fail loudly at startup if unset — see `apps/voice/client/.env.example` for the local-dev template
(not reproduced here since it is a `.env*`-pattern file).

## Auth service (`apps/auth/webapp`)

A ported Next.js app (magic-link + embedded OIDC issuer). Local/staging environment values are
materialized by `apps/auth/webapp/from-aws-to-env.sh`, which reads `from-aws.tmpl` line by line: any
line shaped `KEY=arn:aws:ssm:...` is resolved live via `aws ssm get-parameter --with-decryption`
into `.env.local`; every other line is copied through unchanged.

Key plain (non-secret) values from `from-aws.tmpl`:

| Variable | Value | Purpose |
|---|---|---|
| `HOSTNAME` | `0.0.0.0` | Required so the ALB health check can reach the container. |
| `NEXTAUTH_URL` | `http://localhost:3002` (local) / `https://auth.{domain}/use1` (prod) | next-auth's base URL. |
| `SITE_DOMAIN` | `klankermaker.ai` | Root domain for cookie scoping and derived URLs. |
| `AUTH_PUBLIC_URL` | `https://auth.klankermaker.ai/use1` | Pinned base that the issuer, `routePrefix`, JWKS URI, and the voice client's redirect URIs all resolve from — kept as one value so they can never disagree. |
| `AUTH_DYNAMODB_ENDPOINT` / `AUTH_ELECTRO_ENDPOINT` | `http://localhost:8000` (local only) | Points at dynamodb-local for a green local check without touching live tables. |
| `AUTH_DYNAMODB_DBNAME` | `kmv-auth-authjs` | next-auth's own adapter table. |
| `AUTH_ELECTRO_DBNAME` | `kmv-auth-electro` | The shared ElectroDB single-table (OIDC adapter, tiers, access codes). |
| `OIDC_VOICE_CLIENT_ID` / `OIDC_VOICE_SECRET` | dev placeholders locally; `voice` / `unused-public-pkce-client` in prod | The voice app's registered OIDC client — a public PKCE client, so the "secret" is not actually secret. |

SSM-backed (ARN-resolved) values from `from-aws.tmpl`: `AUTH_SES_ACCESS_KEY_ID`,
`AUTH_SES_SECRET_ACCESS_KEY`, `AUTH_JWT_SECRET`, `OIDC_COOKIE_KEYS`, `ALTCHA_HMAC_KEY`, and
`OIDC_JWKS` (a full JWK Set — persistent, not auto-generated per process, so JWKS validation stays
stable across restarts and across a multi-task Fargate fleet).

`apps/auth/webapp/next.config.ts` reads three additional build/runtime variables:

| Variable | Default | Purpose |
|---|---|---|
| `WEBAPP_ORIGIN` | `auth.klankermaker.ai` | Origin used to build `assetPrefix` in production. |
| `WEBAPP_PREFIX` | `use1/assets` | Asset path prefix. |
| `REGION_SHORT` | `use1` | Mounts the app at `/${REGION_SHORT}` (`basePath`) in production; exposed to the client as `NEXT_PUBLIC_REGION_SHORT`. |

In production (`NODE_ENV=production`), the app also sets `output: 'standalone'` and a `basePath`
derived from `REGION_SHORT`.

## Telephony edge (`apps/voice/asterisk` + `klanker_voice.telephony`)

The telephony edge is one Fargate container running Asterisk + ARI + the standalone Python
controller (`klanker_voice.telephony.controller` / `telephony.__main__`), configured by the
`[telephony]` table documented above plus these environment variables:

| Variable | Required | Default | Purpose |
|---|---|---|---|
| `ASTERISK_ARI_URL` | No | `http://127.0.0.1:8088` | ARI is bound loopback-only inside the container — never exposed on the task ENI. |
| `ASTERISK_ARI_USERNAME` | Yes | — | ARI basic-auth username. |
| `ASTERISK_ARI_PASSWORD` | Yes | — | ARI basic-auth password. |
| `TELEPHONY_ACCESS_PIN` | Yes (when `gate_mode` includes `dtmf`) | — | The §24 answer-gate's numeric unlock code. |
| `TELEPHONY_PASSPHRASE_WORDS` | Yes (when `gate_mode` includes `passphrase`) | — | The §24 answer-gate's spoken multi-word phrase. |
| `TELEPHONY_ENDPOINT_AUTH_TOKEN` | Only if `tel_mint_url` is configured | — | Bearer token for the caller-ID mint call to the auth service's `/tel` route. |
| `VOIPMS_SIP_USERNAME` / `VOIPMS_SIP_PASSWORD` | Yes | — | Upstream SIP trunk credentials, rendered into `pjsip.conf` by the container entrypoint and then scrubbed from the environment before the Python controller starts. |
| `DEEPGRAM_API_KEY`, `ANTHROPIC_API_KEY`, `ELEVENLABS_API_KEY` | Yes | — | Same pipeline provider keys the voice service uses — the telephony controller builds the identical STT/LLM/TTS cascade after gate unlock. |

`KLANKER_PIPELINE_CONFIG` is set to `configs/telephony.toml` for this container so the `[telephony]`
table's `enabled = true` takes effect.

## `kv` CLI (`kv/`)

A Cobra-based Go CLI. Config is a mix of persistent flags and environment-variable fallbacks
(`kv/internal/app/cmd/root.go`):

| Flag | Env fallback | Default | Purpose |
|---|---|---|---|
| `--table` | `AUTH_ELECTRO_DBNAME` | `kmv-auth-electro` | DynamoDB table for code/tier commands. |
| `--usage-table` | `KMV_USAGE_TABLE` | `kmv-voice-usage` | DynamoDB table for `kv usage`/`kv killswitch` (must match the voice service's own `KMV_USAGE_TABLE`). |
| `--endpoint-url` | `AWS_ENDPOINT_URL_DYNAMODB` | (unset = AWS) | DynamoDB endpoint override for dynamodb-local dev/testing. |
| `--region` | `AWS_REGION` | (ambient AWS config) | AWS region. |
| `--log-level` | — | `info` | Log verbosity (`debug`, `info`, `warn`, `error`). |

Additional env-only inputs used by specific subcommands:

| Variable | Used by | Purpose |
|---|---|---|
| `KV_AUTH_ORIGIN` | `kv code` | Origin used when composing bypass/join URLs. |
| `REGION_SHORT` | `kv code` | Region path segment used in the same URL composition. |
| `KMV_SMOKE_SERVICE_TOKEN` | `kv smoke` | The smoke/service credential (same variable the voice service's `auth.py` checks). |
| `VOIPMS_API_USERNAME` / `VOIPMS_API_PASSWORD` | `kv voipms` | VoIP.ms account API credentials — read via `os.Getenv` only; a repo test (`voipms_test.go`) asserts no literal credential is ever assigned in `voipms.go`. |

## Infrastructure (`infra/terraform`)

### Secrets: SOPS → SSM → env

Every provider API key and application secret follows the same path:

1. **Author** the secret locally in `infra/terraform/live/site/.secrets.sops.json` (SOPS-encrypted,
   safe to commit) or the gitignored plaintext `.secrets.json` fallback. The shape must match
   `site.hcl`'s `secrets.definitions` — see `infra/terraform/live/site/SECRETS.md` for the exact
   key names (`deepgram`, `anthropic`, `elevenlabs`, `jwt`, `oidc`, `altcha`) and the
   `sops encrypt` / `sops edit` workflow.
2. **Provision** — `site.hcl` decrypts on the fly (`sops --decrypt`) at every plan/apply and feeds
   the `secrets` Terraform module, which writes one SSM `SecureString` parameter per
   secret/key pair at `/kmv/secrets/use1/<name>/<key>` (`infra/terraform/modules/secrets/v1.0.0/ssm.tf`).
3. **Consume** — each ECS task definition's `containers[].secrets` list maps a container
   environment variable name to the parameter's `valueFrom` ARN
   (`infra/terraform/modules/ecs-task/v1.0.0/main.tf`). The container's execution role is granted
   `ssm:GetParameters` + `kms:Decrypt` scoped only to the prefixes it needs — ECS injects the
   decrypted value at container start; nothing plaintext ever touches the image, the task
   definition source, or CI logs.

Per-service secret sets (names only — see the service's `service.hcl` under
`infra/terraform/live/site/services/{voice,auth,telephony-edge}/` for the full parameter paths):

| Service | Secrets injected via `valueFrom` |
|---|---|
| `voice` | `DEEPGRAM_API_KEY`, `ANTHROPIC_API_KEY`, `ELEVENLABS_API_KEY`, `KMV_SMOKE_SERVICE_TOKEN` |
| `auth` | `AUTH_JWT_SECRET`, `OIDC_COOKIE_KEYS`, `OIDC_JWKS`, `ALTCHA_HMAC_KEY` |
| `telephony-edge` | `VOIPMS_SIP_USERNAME`, `VOIPMS_SIP_PASSWORD`, `ASTERISK_ARI_USERNAME`, `ASTERISK_ARI_PASSWORD`, `TELEPHONY_ENDPOINT_AUTH_TOKEN`, `TELEPHONY_ACCESS_PIN`, `TELEPHONY_PASSPHRASE_WORDS`, plus the same `DEEPGRAM_API_KEY`/`ANTHROPIC_API_KEY`/`ELEVENLABS_API_KEY` the voice service uses (the telephony controller runs the identical pipeline after gate unlock). |

The telephony-edge task role is deliberately scoped to only
`/kmv/secrets/use1/{voipms,asterisk,telephony}/*` — it is never granted `/kmv/operators/*`, which
holds an operator-only parameter no bot task role may read (see
`docs/operators/phase12-seed-data.md`).

### Non-secret task environment (Terraform-defined)

Each service's `service.hcl` also sets plain (non-secret) container environment variables directly
in Terraform — for example the auth service's `NODE_ENV`, `AUTH_PUBLIC_URL`, `AUTH_ELECTRO_DBNAME`,
and `SITE_DOMAIN`, or the voice service's `VOICE_PUBLIC_URL`. These are visible in the task
definition and are not secrets.

<!-- VERIFY: exact AWS account id and full SSM parameter ARNs are committed in the terraform live
config and are not fabricated here, but confirm they still match the live account before quoting
them externally. -->

### Image tags and deploy

Each service's container `image` field is env-driven at plan time
(`TF_VAR_VOICE_IMAGE_TAG`, `TF_VAR_AUTH_IMAGE_TAG`, `TF_VAR_TELEPHONY_EDGE_IMAGE_TAG`), so the
CI/OIDC deploy workflow can apply the immutable `${github.sha}` image it just built, while a local
`terragrunt apply` falls back to the last known-good pinned tag baked into `service.hcl`.

## Related docs

- [docs/TUNING.md](../TUNING.md) — the `[stt]`/`[turn]` endpointing arms and their measured latency
  trade-offs.
