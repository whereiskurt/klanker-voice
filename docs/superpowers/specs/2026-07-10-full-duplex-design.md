# Full-duplex concept + variant endpoints — design

**Date:** 2026-07-10
**Status:** implemented-pending-live-tuning
**Branch:** `claude/full-duplex-concept-6z5ylo`
**Scope:** Introduce a "full-duplex" conversational feel (listen *and* talk) to the
concierge, shipped as a **second front-door variant** (`/voice2`) that runs alongside
the untouched live experience (`/voice1`). One deployed server, per-page pipeline
selection. Adds Deepgram Flux (server-side turn detection) and a `DuplexController`
that (a) tells backchannels apart from real barge-ins and (b) emits the bot's own
"mm-hm" listening cues.

## What "full duplex" means here (and doesn't)

The shipped cascade is **half-duplex-with-barge-in**: media flows both ways at all
times (WebRTC + VAD-during-TTS), but the *conversation* is strictly turn-based — one
speaker holds the floor, and *any* sound over the bot fires an interruption that yields
it. A cascade (STT → LLM → TTS) can never be *literally* simultaneous. So "full duplex"
in this project is precisely defined as three things a good listener does:

1. **Backchannels don't steal the floor.** "yeah", "mm-hm", "right", "got it" said over
   the bot mean *keep going* — they must not cut it off. Today they do.
2. **Real interruptions still cut in fast.** "wait—", "no, actually", a fresh question
   must barge in with minimal added latency.
3. **The bot backchannels too.** It drops its own short "mm-hm" while you talk, so it
   sounds like it's actively listening rather than waiting for a hard end-of-turn.

Literal both-mouths-at-once speech-to-speech (OpenAI Realtime / Gemini Live / Nova
Sonic) is explicitly **out of scope** — CLAUDE.md rejects it (vendor lock-in, abandons
the HF-cascade fidelity that is the point of the project, and it would throw away the
router/knowledge/quota/persona apparatus).

## Non-goals

- No change to `/voice1` (the live conference demo). It has no `[duplex]` table and no
  `?variant=`, so its pipeline is byte-for-byte the shipped cascade. This is the safety
  property that makes deploying the experiment low-risk.
- No new Fargate service, no new subdomain, no new terraform. One server, variant
  routing — fits the ~$120–165/mo budget guardrail.
- No per-variant auth or quota. Quota is a **site-wide budget** guardrail and stays
  sourced from the default config; variants only steer the *pipeline*.
- No literal speech-to-speech model (see above).

---

## Architecture

### Variant endpoints (one server, many front doors)

A **variant** is nothing more than "which pipeline config this session runs". The
browser page (`/voice1`, `/voice2`) posts its SDP offer to
`POST /api/offer?variant=<name>`; the server maps the name — through a fixed in-code
allowlist — to the config the session loads.

- **`klanker_voice.variants`** (new): `_VARIANT_CONFIGS = {"voice1": None, "voice2":
  "configs/voice2.toml"}`. `None` means "the default `pipeline.toml`". The variant name
  is attacker-controlled (public query string), so it is used **only** as an allowlist
  key, never as a filesystem path — unknown/malformed → `DEFAULT_VARIANT`. No
  path-traversal surface; every resolved path is anchored under `APP_ROOT`.
- **`server.py`**: `offer()` reads and normalizes `?variant=`, threads it through
  `_negotiate_webrtc → _start_and_run_tracked_session → _run_session`, which loads
  `load_config / load_knowledge_config / load_duplex_config` from the variant's path.
  `load_quota_config` stays on the default (global budget).
- **Client** (`transport/variant.ts`): derives the variant from the first path segment
  and `buildConnectParams` appends `?variant=` only for non-default variants — so the
  `/voice1` request is the exact bare `/api/offer` it always was. The SPA already
  deep-links (server 404 → index.html), so `/voice2` renders the same app and simply
  connects to the duplex pipeline. ICE PATCHes carry the query harmlessly (resolved by
  `pc_id`).

Adding a third variant later is one registry line + one `configs/<name>.toml`.

### DuplexController (the full-duplex behavior)

A `FrameProcessor` inserted **between STT and the knowledge router** — the same slot the
router uses, chosen because that is where the source-queued `InterruptionFrame` can be
intercepted *before* it reaches the aggregator/LLM/TTS. Omitted entirely when
`duplex.enabled` is false, so `voice1` is unchanged.

**How barge-in interception works.** On user-speech-start the worker queues an
`InterruptionFrame` *downstream* through the whole pipeline (`pipeline/worker.py`). The
base `FrameProcessor.process_frame` handles it locally (flush/metrics) but does **not**
auto-forward it — propagation is via `push_frame`. So the controller can **hold** it:

- **Hold-and-release.** While the bot is speaking, a barge-in interruption is withheld
  (not pushed) for up to `interruption_hold_ms`. When the first transcript arrives it's
  classified:
  - **backchannel** → drop the held interruption (bot keeps talking) *and* swallow the
    backchannel's finalized transcript so it never becomes a user turn.
  - **anything else** → release the interruption immediately (normal barge-in).
  - **no transcript in the window** → release (fail-safe: never swallow a real barge-in).
- **Classifier** (`classify_user_speech`, pure/unit-tested): backchannel iff the
  utterance is ≤ `max_backchannel_words` words and *every* word is in the curated
  lexicon (`DEFAULT_BACKCHANNEL_WORDS`). "wait"/"no"/"stop" are deliberately **not** in
  the lexicon — they must barge in.
- **Bot backchannel emitter** (optional, `backchannel_emitter`): on a user pause, push a
  short phrase straight to TTS via `TTSSpeakFrame(append_to_context=False)` (the
  `speak_goodbye` pattern — never enters the LLM context), rate-limited by
  `emitter_min_gap_seconds`, never while the bot is already speaking.

**Deepgram Flux** (`voice2.toml` `[stt]`) pairs with this: its server-side end-of-turn
model shaves the endpointing beat and is far less likely to treat a short "mm-hm" as a
full turn, partially offsetting the hold-window latency. `factories.py` already has the
Flux arm and forbids mixing it with local turn strategies.

### The one real tradeoff

Holding a barge-in adds up to `interruption_hold_ms` (or time-to-first-partial,
whichever is shorter) of latency to *genuine* interruptions, in exchange for not being
cut off by a "yeah". The hold window, the lexicon, and the emitter cadence are
**live-tuning surfaces**. The 07-08 spec flagged exactly this class of change
("filler … talk-over/barge-in risk") as a deferred non-goal requiring on-device audible
verification — that judgment still holds; `voice2` is where we test it without touching
the live demo.

---

## Files touched

**Python (voice service)**
- `src/klanker_voice/config.py` — `DuplexConfig` + `load_duplex_config` (optional
  `[duplex]` table; absent → disabled, so every pre-existing fixture is unaffected).
  Lexicon/phrase defaults live here. (Naming care: the field is `backchannel_words`, not
  `..._tokens` — `_CREDENTIAL_FIELD_RE` rejects any `_token(s)` field.)
- `src/klanker_voice/variants.py` — variant→config allowlist + safe resolution (new).
- `src/klanker_voice/duplex.py` — `classify_user_speech` + `DuplexController` (new).
- `src/klanker_voice/pipeline.py` — `build_pipeline(duplex_cfg=…)` inserts the
  controller between STT and the router only when enabled.
- `server.py` — normalize `?variant=`; thread it to the per-session config load;
  quota stays global.
- `configs/voice2.toml` — full pipeline clone: Flux STT + `[duplex] enabled/emitter on`
  (new). Persona/knowledge/voice identical to `voice1` so it's a clean A/B.

**Client**
- `client/src/transport/variant.ts` — path→variant (new).
- `client/src/transport/voiceSession.ts` — `buildConnectParams` appends the variant.

## Tests

- `tests/test_duplex.py` — classifier table + controller behaviors via pipecat
  `run_test`: backchannel suppressed (interruption dropped + transcript swallowed), real
  interruption released, held-before-decision, bot-silent passthrough, bot-stop drops a
  pending hold, disabled = transparent, emitter emit/rate-limit/off.
- `tests/test_duplex_config.py` — optional-table default, parse, custom lists, validation
  errors.
- `tests/test_variants.py` — default/None config, voice2 under APP_ROOT, unknown +
  path-traversal fall back to default.
- `tests/test_server.py` — `?variant=` normalized and passed to negotiation (known /
  unknown / absent).
- `client/src/transport/variant.test.ts` — path mapping + `buildConnectParams` endpoint
  (voice1 bare, voice2 query, bearer preserved).

**Status at commit:** Python `268 passed, 54 skipped`; client `149 passed`, `tsc
--noEmit` clean. The controller's frame *logic* is unit-proven; the hold-window value,
lexicon, and emitter cadence still need live/audible tuning on `/voice2` (see tradeoff).

## Rollout / deploy

`voice2` is additive and off by default everywhere except its own page, so it can ship
behind the live demo safely. Deploy path is CI-driven and human-gated (`infra/CI.md`):
`apps/voice/**` → `build-voice.yml` → ECR → `deploy.yml` rolls ECS. No terraform change
is required (no new infra). After deploy, tune on `/voice2`:

1. `interruption_hold_ms` — lowest value that still catches "yeah" without a laggy
   barge-in (start 250; try 150–350).
2. Lexicon — add/remove cues heard in real sessions.
3. Emitter cadence (`emitter_min_gap_seconds`) / whether the emitter stays on by default
   — this is the highest talk-over risk; easy to flip off in `voice2.toml` alone.

## GSD note

GSD commands aren't available in the web/remote environment (only
`kv-refresh-knowledge`), so this spec is the planning artifact and the change was
implemented directly on the feature branch. Follow-up tuning should route through
`/gsd-quick` per lever when run locally.
