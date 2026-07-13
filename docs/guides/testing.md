<!-- generated-by: gsd-doc-writer -->
# Testing Guide

klanker-voice has four independent test surfaces — one per deployable
service — plus a fifth, higher-level surface that isn't a pass/fail test
suite at all: an LLM-judged conversation eval harness, and a documented set
of things that can only be verified by a human ear on a live call. This
guide covers all five.

| Surface | Location | Runner |
|---|---|---|
| Voice service (Python) | `apps/voice/tests/` | `pytest` |
| Browser client (TypeScript) | `apps/voice/client/src/**/*.test.*` | `vitest` |
| Auth service (TypeScript) | `apps/auth/webapp/src/**/__tests__/` | `vitest` |
| `kv` CLI (Go) | `kv/internal/app/cmd/`, `kv/internal/app/electro/` | `go test` |
| Conversation quality | `apps/voice/scenarios/*.yaml` | `klanker_voice.harness` (LLM-judged, informational) |

For local setup and build/lint commands, see
[`docs/guides/development.md`](development.md). For the endpointing/latency
tuning results the harness produced, see [`docs/TUNING.md`](../TUNING.md).

---

## Voice service — `pytest`

### Setup

```bash
cd apps/voice
uv sync
```

### Running

```bash
uv run pytest                          # full suite
uv run pytest tests/test_quota.py      # one file
uv run pytest tests/test_quota.py -k acquire_heartbeat   # one test/pattern
uv run pytest -x                       # stop on first failure
```

Config lives in `apps/voice/pyproject.toml`:

```toml
[tool.pytest.ini_options]
testpaths = ["tests"]
asyncio_mode = "auto"
pythonpath = ["."]
```

`asyncio_mode = "auto"` means async `def test_...()` functions run without
an explicit `@pytest.mark.asyncio` decorator — most of this suite is async,
since the pipeline itself is. `tests/conftest.py` supplies the shared
`MINIMAL_TOML` fixture body (a minimal valid `pipeline.toml`) and an
autouse fixture that resets a module-global scale-in-protection cache
between tests so ECS-protection assertions stay isolated.

The suite is hermetic: no live Deepgram/Anthropic/ElevenLabs calls, no live
DynamoDB, no live Asterisk. Provider services and AWS clients are stubbed
or faked at the seams (`FakeAriClient`, `FakeRtpMediaSession`, mocked
`dynamodb` conditional writes, RSA keypairs minted locally for JWT tests).
It runs green in a clean environment with no secrets set.

### What's covered, by subsystem

**Pipeline core & config** — `test_config.py`, `test_factories.py`,
`test_duplex_config.py`, `test_duplex.py`, `test_variants.py`,
`test_greet_first_config.py`. Config-file loading and validation
(`klanker_voice.config`), the `(kind, provider) -> builder` factory
registry and turn-strategy matrix, the optional `[duplex]` full-duplex
table, and `PersonaConfig.greet_first`.

**Auth & quota** — `test_auth.py` (offline RS256 JWT validation against a
locally generated RSA keypair — no live JWKS fetch), `test_quota.py`
(race-safe DynamoDB conditional-write primitives, the start gate,
accounting-tick math), `test_slot_leak.py` (the heartbeat lease must be
released on every session-teardown path, including an abrupt client
vanish), `test_session.py` (`SessionLifecycle`: the 15s tick,
persist/renew/rollup/auto-trip, hard-stop, ECS scale-in protection),
`test_teardown.py` (idle-teardown layers atop the wall-clock bound and
reconnect grace), `test_winddown.py` (spoken wind-down warning + goodbye
grace on mid-session quota exhaustion).

**Call runtime & transports** — `test_call_runtime.py` (the transport-neutral
`create_call_session` constructor both the browser and PSTN paths call),
`test_server.py` / `test_server_static.py` (the `/health` and `/api/offer`
FastAPI endpoints — SDP offers only, no real WebRTC/ICE negotiation),
`test_webrtc.py` (public-IP + STUN ICE candidate gathering, no network at
import time), `test_rtvi.py` (RTVI processor wiring), `test_smoke.py`
(pinned pipecat version check, package importability, and an in-process
real `aiortc` SDP offer/answer negotiation with no network required).

**Knowledge & retrieval** — `test_knowledge_pack.py`, `test_knowledge_router.py`
(keyword classification, confidence floor, Haiku fallback), `test_knowledge_retrieval.py`
(local, keyless SQLite FTS5/BM25 retrieval), `test_knowledge_pacing.py`
(time-aware pacing of the retrieval block against remaining session time),
`test_knowledge_refresh.py` (`scripts/refresh_knowledge.py`),
`test_greenhouse_hidden.py` (the hidden `greenhouse` recruiting-mode topic
must stay unlisted and only unlock via keyword).

**Voice quality (regression, not perceptual)** — `test_pronunciation_filter.py`
(the TTS text-normalization filter), `test_tts_energy.py`
(stability/similarity_boost/style/speed voice-setting plumbing),
`test_regreet_suppression.py` (the double-greeting echo is suppressed by
the persona system prompt alone), `test_greeting_voice_drift.py` — this one
is a CI tripwire: it fails if the rendered greeting clips were made from a
different ElevenLabs voice than the one `pipeline.toml` currently ships,
i.e. it catches someone swapping the TTS voice without re-running `make -C
apps/voice greetings`.

**Telephony** (`apps/voice/src/klanker_voice/telephony/`) —
`test_asterisk_configs.py` (structural invariant lint on the Asterisk edge
`.conf` files — never validates real secrets, just shape), `test_telephony_config.py`,
`test_telephony_ari.py` (the raw `aiohttp` ARI REST + events-WebSocket
client), `test_telephony_media.py` (PCMU/G.711 mu-law codec and framing,
pure-function/table-driven against hand-computed known values),
`test_telephony_rtp_socket.py` (the socket-backed RTP media session),
`test_telephony_transport.py` (`TelephonyTransport` unit tests plus an
offline pipeline-traversal proof), `test_telephony_gate.py` (the §24
silent DTMF/passphrase answer-gate — hermetic, no real Asterisk/ARI/STT/LLM/TTS),
`test_telephony_lifecycle.py` (the call lifecycle state matrix),
`test_telephony_controller.py` (caller-ID mint + fail-closed behavior), and
`test_telephony_integration.py` — the CI-required deterministic artifact:
it drives `AsteriskCallController` against a **fake** `AriClient` and
**fake** RTP media session with synthetic transcription frames, proving the
lifecycle and gate logic mechanically end-to-end with no live Docker, no
live Asterisk, and no provider keys.

**Observability** — `test_observers.py` (`LatencyReportObserver`
frame-path tests, the per-stage latency anchor the harness/`docs/TUNING.md`
tables are built from), `test_report.py` (the JSON schema the latency
report emits is a stability contract — it's consumed by both the HUD and,
per its own docstring, a CI gate).

---

## Browser client — `vitest`

```bash
cd apps/voice/client
npm install
npm test          # runs `vitest run` — single pass, no watch mode
```

Setup lives in `apps/voice/client/vitest.setup.ts`: it wires
`@testing-library/jest-dom`'s matchers and runs `cleanup()` after every
test via `@testing-library/react`. The devDependency stack is Vitest +
`happy-dom` + Testing Library (`@testing-library/react`, `/dom`,
`/jest-dom`).

---

## Auth service — `vitest`

```bash
cd apps/auth/webapp
npm install
npm test          # runs `vitest run`, config in vitest.config.ts
```

Tests live alongside the code they cover, under `__tests__/` directories:
config/session-bridge logic (`src/config/__tests__/` —
`load-existing-grant`, `login-intent-bridge`, `oidc-resource-token`),
shared helpers (`src/lib/__tests__/` — `bypass-token`,
`phone-normalization`), ElectroDB entity behavior (`src/entities/__tests__/`
— `access-code-resolution`, `code-redemption`, `phone-resolution`,
`tier-and-login-intent`), and route handlers (`src/app/tel/__tests__/tel-route.test.ts`,
`src/app/api/login/__tests__/{login-altcha,login-access-code}.test.ts`,
`src/app/(authlogin)/login/confirm/__tests__/confirm-no-consume.test.ts`).

---

## `kv` CLI — `go test`

```bash
cd kv
go test ./...
```

Test files: `internal/app/cmd/roundtrip_test.go`, `bypass_test.go`,
`code_test.go`, `voipms_test.go`, `usage_killswitch_test.go`,
`internal/app/electro/keys_test.go`. Several tests cross-check the Go
implementation against its TypeScript counterpart in the auth webapp — for
example `code_test.go`'s `TestNormalizeE164` asserts the Go phone
normalizer reproduces the same canonical output as
`apps/auth/webapp/src/lib/phone-normalization.ts`, documenting one known,
deliberate divergence (blank/no-digit input) rather than silently drifting.

---

## Conversation-quality eval harness

`apps/voice/scenarios/*.yaml` define scripted, multi-turn conversations —
greeting behavior, three barge-in variants (`bargein_early.yaml`,
`bargein_mid.yaml`, `bargein_monologue.yaml`), knowledge/retrieval accuracy
per topic (`kph_knowledge_defconrun.yaml`, `kph_knowledge_km.yaml`,
`kph_knowledge_meshtk.yaml`, `kph_retrieval_depth.yaml`,
`kph_retrieval_km.yaml`, `kph_router_accuracy.yaml`), tone/guardrail checks
(`kph_crude_humor_guard.yaml`, `kph_cache_verify.yaml`, `kph_tour_mode.yaml`,
`kph_unknowns.yaml`), and session memory (`memory.yaml`). Each scenario
scripts a synthetic user (audio via a TTS voice, e.g. `kokoro`), waits for
pipeline events (`tts_response`, `llm_started`, `user_transcription`), and
hands the bot's response to an LLM judge (`klanker_voice.harness.judge`)
against a natural-language rubric.

Run a scenario against the real pipeline:

```bash
cd apps/voice
uv run python bot.py -t eval    # eval-harness transport target
```

This is the same target `docs/TUNING.md`'s A/B arm comparisons were
measured against (`bot.py -t eval` over the five-scenario suite). It's also
what `apps/voice/console.py` exercises for terminal mic/speaker
iteration (`uv run python console.py` — same `load_config` +
`build_pipeline` + greet-first path as `bot.py`, zero pipeline logic
duplicated; wear headphones, since laptop speakers will feed bot audio back
into the mic and trigger self-interruption).

Re-render or diff past runs from their JSON artifacts, with no live run
required:

```bash
uv run python -m klanker_voice.harness report artifacts/harness/run.json
uv run python -m klanker_voice.harness compare arm-a.json arm-b.json arm-c.json
```

`report` re-renders the per-stage p50/p95 latency table for one artifact;
`compare` renders the side-by-side per-stage diff table used to build the
arm-comparison tables in `docs/TUNING.md`. Both subcommands **always exit
0** on threshold results — verdicts are informational only. A nonzero exit
is reserved for genuine I/O or schema errors (missing file, invalid JSON,
wrong `schema_version`), never a threshold miss. Don't wire either
subcommand into a CI gate expecting a pass/fail exit code; treat the tables
as a reviewed-by-a-human artifact, the way `docs/TUNING.md` does.

---

## What's only verified by ear

Some behavior in this project is explicitly documented as unverifiable by
any automated test — the repo is direct about the boundary rather than
pretending a fake substitutes for it. The clearest example is the
telephony edge's **§19-C manual softphone proof**
(`apps/voice/asterisk/README.md`):

- `test_telephony_integration.py` is the CI-required, deterministic
  artifact — it drives the controller against a fake ARI client and fake
  RTP media with synthetic transcription frames, and it proves the
  lifecycle and gate logic mechanically. It needs no live Docker, no live
  Asterisk, and no live provider keys.
- What it **cannot** prove: that the greeting audio isn't clipped, that
  barge-in actually interrupts TTS playback the way a human expects, that
  a real SIP softphone's RTP negotiates and plays back correctly through
  Asterisk's external-media bridge, or that a live Deepgram/Anthropic/ElevenLabs
  round trip behaves correctly over the socket-backed media session.

The README's recipe brings up the full local Asterisk stack (`docker
compose up` in `apps/voice/asterisk/`), registers a real SIP softphone
(Linphone, baresip, etc.), and walks through the gated happy path by ear:
confirm the line answers silently, speak the passphrase or key the DTMF
PIN, confirm the greeting is not clipped, hold a short conversation,
interrupt mid-response and confirm it actually stops speaking, then hang
up and confirm a clean disconnect. A SIPp scenario
(`apps/voice/asterisk/sipp/gate-pass.xml`, `docker compose --profile
integration run --rm sipp`) is a semi-automated middle tier that drives a
real SIP call against a real Asterisk container to regression-test SIP/RTP
wiring — but it, too, cannot judge audio quality or barge-in feel, and
isn't a substitute for the manual proof.

The same "verified by ear" posture applies more informally to overall
voice-to-voice latency feel and TTS voice quality on the browser path —
`docs/TUNING.md`'s endpointing verdicts are explicitly **informational
only**: no run, report, or CLI exits nonzero on a latency threshold, and
the tuning document records at least one case where the shipped TTS voice
was picked "by ear" rather than by any automated metric.

---

## Continuous integration

There is currently **no CI workflow that runs `pytest`, `vitest`, or `go
test`** — test suites are a local/developer-run gate, not an automated
merge gate yet. The workflows under `.github/workflows/` cover a different
set of concerns:

- `build-voice.yml`, `build-auth.yml` — build and push Docker images on
  pushes to `main` touching `apps/voice/**` / `apps/auth/**`, then deploy
  via `deploy.yml`. Neither runs a test step before building.
- `gitleaks-scan.yml` — secret scanning on every PR and push to `main`.
- `terragrunt-plan.yml` / `terragrunt-apply.yml` — Terraform plan on PRs
  and pushes to `main` (read-only, no gate); apply is
  `workflow_dispatch`/`workflow_call`-only, gated behind human reviewer
  approval in the `terraform-apply` environment. See `infra/CI.md`.

Run the relevant suite locally before opening a PR: `uv run pytest`
(voice), `npm test` (client and auth webapp), `go test ./...` (`kv`).
