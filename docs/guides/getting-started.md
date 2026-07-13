<!-- generated-by: gsd-doc-writer -->
# Getting Started

This is the real, working sequence for going from a fresh clone to a live voice conversation
with the KlankerMaker concierge on your own machine. It also covers the browser client, the
auth service, the telephony edge, and the `kv` operator CLI, and is explicit about which steps
need AWS/SSM access versus which work with nothing but your own provider API keys.

The fastest path to "talk to it" is **Section 1 alone** — the voice pipeline has its own
bundled dev UI and does not require the browser client or the auth service to run.

For the full configuration reference (every environment variable and `pipeline.toml` field),
see [`docs/guides/configuration.md`](configuration.md). For how the pieces fit together, see
[`docs/architecture/overview.md`](../architecture/overview.md). For day-to-day dev workflow
(lint, tests, branch conventions), see `docs/guides/development.md`.

## Prerequisites

| Tool | Version | Needed for |
|---|---|---|
| Python | `>=3.12` (repo is pinned to `3.12` via `apps/voice/.python-version`) | Voice service |
| [uv](https://github.com/astral-sh/uv) | any recent release (the container image pins `0.11.26`) | Voice service dependency management |
| Node.js | 22 LTS | Browser client, auth service |
| Go | `1.26` (per `kv/go.mod`) | `kv` operator CLI |
| git | any recent release | Cloning the repo |
| AWS CLI + credentials for the `klanker-application` profile | — | **Optional.** Only required if you want `make env` to pull real provider keys from SSM, or if you want to run the auth service locally (its secrets are always SSM-backed). See [Working without AWS access](#working-without-aws-access-below). |
| Docker + Docker Compose | — | **Optional.** Only required for the local Asterisk telephony edge (Section 4). |

On macOS, the voice service's dev dependency group pulls in `pipecat-ai[local]` for laptop-mic
testing via `console.py`, which needs PortAudio's headers to build the `pyaudio` wheel:

```bash
brew install portaudio
```

If you skip this, `uv sync` will still succeed for the browser/WebRTC dev path (`make
voice1-local` / `make voice2-local`) — you'll only hit a build failure if you try to run
`console.py` without PortAudio installed.

## Clone the repository

```bash
git clone https://github.com/whereiskurt/klanker-voice.git
cd klanker-voice
```

## 1. Voice pipeline (the fastest path to a working conversation)

Everything here happens in `apps/voice/`.

### Install dependencies

```bash
cd apps/voice
uv sync
```

This creates `.venv/` and installs the locked dependency tree from `uv.lock`, including the
`dev` dependency group (`pipecat-ai[evals,local]`, `pytest`, `pytest-asyncio`).

### Get provider API keys

The pipeline needs three real, paid provider keys — Deepgram (STT), Anthropic (LLM), and
ElevenLabs (TTS) — written to `apps/voice/.env`. There are two ways to get there:

**With AWS/SSM access** (the `klanker-application` profile in `us-east-1`):

```bash
make env
```

This runs `scripts/bootstrap_env.sh`, which fetches the three `SecureString` parameters under
`/kmv/secrets/use1/{deepgram,anthropic,elevenlabs}/api_key`, refuses to run if shell `xtrace` is
on (it would leak secrets to the terminal), and writes `apps/voice/.env` atomically with mode
`600`. It never leaves a partial `.env` behind if any fetch fails.

<a id="working-without-aws-access-below"></a>
**Without AWS access**, hand-write `apps/voice/.env` yourself with your own personal keys from
each provider, using the exact same variable names `bootstrap_env.sh` would have written:

```
DEEPGRAM_API_KEY=...
ANTHROPIC_API_KEY=...
ELEVENLABS_API_KEY=...
```

Either way, these three keys are the only secrets the local pipeline needs — `pipeline.toml`
itself is not allowed to carry credential-shaped values (the loader rejects any field named like
`api_key`, `secret`, `token`, etc.), so there's nothing else to configure here. See
[Environment variables (voice service)](configuration.md#environment-variables-voice-service) in
the configuration guide for the full list, including the optional OIDC/quota variables that only
matter once you're running against a real auth service.

### Run it

```bash
make voice1-local
```

This runs `uv run python bot.py -t webrtc`, which serves Pipecat's bundled dev UI at
**http://localhost:7860** over the same `SmallWebRTCTransport` used in production — open that
URL, allow mic access, and talk to it. This path does **not** go through the OIDC/quota gate
(that enforcement lives in `server.py`, the production entrypoint — see
[Auth service](#3-auth-service-optional---the-full-login-gated-path) below if you want to
exercise that path too).

Two other run modes, same config/pipeline code, no duplicated logic:

```bash
make voice2-local     # full-duplex variant (Deepgram Flux + backchannel emitter), same URL
uv run python console.py   # laptop mic/speaker via the terminal, no browser at all
```

`console.py` is the fastest prompt-iteration loop (no browser round trip), but **wear
headphones** — without them, your laptop speakers feed the bot's own audio back into the mic and
it interrupts itself in a loop.

To try one of the endpointing A/B arms or the full-duplex `voice2` variant instead of the
default `pipeline.toml`, set `KLANKER_PIPELINE_CONFIG`:

```bash
KLANKER_PIPELINE_CONFIG=configs/voice2.toml uv run python bot.py -t webrtc
```

See [Config variants](configuration.md#config-variants) for what each of `configs/arm-a.toml`,
`arm-b.toml`, `arm-c.toml`, `voice2.toml`, and `telephony.toml` changes.

None of the above modes are "free" — they all call the same real, metered Deepgram/Anthropic/
ElevenLabs APIs as production. The eval scenarios under `apps/voice/scenarios/` (run via `uv run
python bot.py -t eval`) synthesize the *user's* side of the conversation locally and transcribe
the bot's replies locally for automated judging, but the bot's own STT/LLM/TTS calls in that
mode are still real API calls against your configured keys.

## 2. Browser client (optional — the real SPA, not the bundled dev UI)

`apps/voice/client/` is the bespoke Vite + React SPA that's actually served in production
(`voice.klankermaker.ai`), as opposed to Pipecat's bundled dev UI used above. Run it if you want
to work on the client UI itself or exercise the real login flow end-to-end.

```bash
cd apps/voice/client
npm install
npm run dev
```

Unlike `make voice1-local`, this SPA is **not** self-contained: `src/config/oidc.ts` reads four
`VITE_OIDC_*` environment variables at startup and fails loudly if any are unset, because the
client always negotiates a real OIDC login before it will open a WebRTC session. Copy
`apps/voice/client/.env.example` to `.env.local` and fill in values that point at a running auth
service (Section 3) before `npm run dev` will produce a usable page. See
[Client (`apps/voice/client`)](configuration.md#client-appsvoiceclient) in the configuration
guide for what each `VITE_OIDC_*` variable means.

## 3. Auth service (optional — the full login-gated path)

`apps/auth/webapp/` is a Next.js app (magic-link email + an embedded `oidc-provider` issuer)
ported from `run.auth`. Its local environment is always SSM-backed, so **this step needs AWS
access regardless of whether you point its DynamoDB tables at a local instance**:

```bash
cd apps/auth/webapp
npm install
./from-aws-to-env.sh    # resolves ARN-shaped lines in from-aws.tmpl via `aws ssm get-parameter`
                         # into .env.local; everything else is copied through unchanged
npm run dev              # next dev, defaults to http://localhost:3002 per NEXTAUTH_URL
```

See [Auth service (`apps/auth/webapp`)](configuration.md#auth-service-appsauthwebapp) for the
full variable breakdown, including which values are plain and which are SSM-resolved.

## 4. Telephony edge (optional — PSTN path)

`apps/voice/asterisk/` is a portable local Asterisk dev harness (ARI + PJSIP, ulaw-only) that
lets you exercise the phone-call ingress path without a real VoIP.ms DID. It's entirely local —
no cloud infra, no VoIP.ms subaccount required:

```bash
cd apps/voice/asterisk
cp .env.example .env   # fill in local-dev-only values; .env is gitignored
docker compose config  # validates the compose file
docker compose up      # config-render -> Asterisk -> the klanker telephony controller, wired together
```

See `apps/voice/asterisk/README.md` for the module smoke check and known limitations, and
[`docs/dataflows/telephony-voipms.md`](../dataflows/telephony-voipms.md) for the full PSTN call
flow.

## 5. `kv` operator CLI

```bash
cd kv
go build -o kv ./cmd/kv
./kv --help
```

`kv` talks to DynamoDB (access codes, tiers, usage) and AWS directly — most subcommands need the
same AWS access as the voice/auth services. See
[`kv` CLI (`kv/`)](configuration.md#kv-cli-kv) for its flags and environment-variable fallbacks.

## Running the test suite

```bash
cd apps/voice
uv run pytest
```

`pyproject.toml` scopes `testpaths` to `tests/` and sets `asyncio_mode = "auto"`, so no extra
flags are needed for the async pipeline tests.

## Common setup issues

- **`uv sync` fails building `pyaudio`.** You're missing PortAudio (macOS: `brew install
  portaudio`; see [Prerequisites](#prerequisites)). This only blocks `console.py` — the browser
  dev path (`make voice1-local`) doesn't need it.
- **`make env` fails with an SSM `AccessDenied` or a missing profile.** You don't have (or
  haven't authenticated) the `klanker-application` AWS profile. Either run `aws sso login
  --profile klanker-application` and retry, or hand-write `apps/voice/.env` with your own
  provider keys — see [Get provider API keys](#get-provider-api-keys).
- **The browser client build/dev server throws on startup about a missing `VITE_OIDC_*`
  variable.** The SPA under `apps/voice/client/` always requires a real OIDC issuer; either point
  it at a locally running auth service (Section 3) or use `make voice1-local`'s bundled dev UI
  instead, which needs none of this.
- **Changing `pipeline.toml`'s `voice_id` and the greeting audio sounds stale.** The pre-rendered
  greeting clips are generated ahead of time (`make greetings`) and must be re-rendered and
  recommitted after any TTS-character change — CI's greeting-voice-drift test will otherwise
  fail. See [`[tts]`](configuration.md#tts--elevenlabs-voice-character) in the configuration
  guide.

## Next steps

- [`docs/guides/configuration.md`](configuration.md) — every `pipeline.toml` field and
  environment variable across all five deployable units.
- `docs/guides/development.md` — build/lint/test commands and branch conventions.
- [`docs/architecture/overview.md`](../architecture/overview.md) — how the voice service, auth
  service, telephony edge, and `kv` CLI fit together.
- [`docs/dataflows/browser-webrtc.md`](../dataflows/browser-webrtc.md) — the browser mic → WebRTC
  → pipeline path in detail.
- [`docs/dataflows/auth-quota.md`](../dataflows/auth-quota.md) — the magic-link/OIDC and
  access-code → tier → quota flow.
