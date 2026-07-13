# Phase 15: Private transcription ledger — S3 batch + Athena + admin conversation view - Research

**Researched:** 2026-07-12
**Domain:** Pipecat pipeline transcript capture → buffered S3 newline-JSON ledger → Athena ad-hoc + Next.js admin conversation view
**Confidence:** HIGH (repo evidence, file:line-cited; web claims cross-checked against installed pipecat 1.5.0 source)

## Summary

The single cleanest tap for both sides of every conversation is the pair of **context-aggregator
events** pipecat 1.5.0 already exposes on the `LLMContextAggregatorPair` this repo builds in
`build_pipeline()`: `on_user_turn_message_added` (finalized user STT text, exactly what entered the
LLM context) and `on_assistant_turn_stopped` (the assistant's aggregated reply text plus an
`interrupted` flag). Because the assistant aggregator sits **after** `transport.output()` in this
repo's pipeline, its aggregation is built from TTS text frames that only flow downstream as their
audio is actually written to the transport — i.e., it approximates *what was actually spoken*, not
what the LLM generated, and barge-in truncation is handled for free. Wiring the tap inside
`create_call_session()` (call_runtime.py) covers **all three entry paths in one seam**: browser
WebRTC voice1, the voice2 full-duplex prod default, and the Phase-12 telephony call runtime — all
three construct sessions exclusively through that function.

Two significant discoveries change the phase's shape. First: **the gated `/admin` area does not
exist.** Phase 05.1 was inserted into the roadmap and its design spec approved, but its phase
directory is empty (plans "TBD") and `apps/auth/webapp/src/app/` has no admin routes at all. The
CONTEXT.md premise "surface as a report in the existing gated /admin area" is false today — this
phase must either be sequenced after 05.1 or bootstrap a minimal `ADMIN_EMAILS`-gated `/admin`
shell itself. Second: **neither `email` nor the access code is available at tap time** for
magic-link users. The access token carries only `sub` (opaque Auth.js user id), `tier_id`, and
`group`; email exists only on the AuthProfile item in DynamoDB, and the redeemed code exists only
on LoginIntent/CodeRedemption items. The bypass `/join` and PSTN `/tel` paths *do* embed the raw
code in the token's `sub` (`anon:<code>:<uuid>`). Recommendation: add namespaced `email` (and
ideally `code`) claims in the auth app's `extraTokenClaims` — the profile is already fetched right
there — so every record can carry email-or-caller-identity + a salted `code_hash` computed once, in
the voice service, from an SSM-injected salt.

For storage, follow the repo's existing AWS pattern exactly: **sync boto3 called via
`asyncio.to_thread`** (no new Python dependency), a per-session buffered writer flushed on a ~2min
timer / 50 records / session release, with the final flush riding the existing single idempotent
teardown funnel (`SessionLifecycle.release` → `CallSession.run`'s `finally`). Terraform additions
follow the established terragrunt module/unit conventions; the admin report should read the
newline-JSON objects **directly from S3** (trivial at ≤25 users) while Athena ships as the
operator's ad-hoc query surface with **partition projection** (no MSCK repair, no Glue crawler).

**Primary recommendation:** Tap `on_user_turn_message_added` + `on_assistant_turn_stopped` inside
`create_call_session()`; buffer per session; flush via boto3-in-`to_thread`; add email/code claims
in auth; render the admin conversation view from direct S3 reads; resolve the missing-/admin
question before planning waves.

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

#### Record shape (LOCKED — user decisions 2026-07-06)
- One row per turn, BOTH sides of the conversation ("both sides of the text convo")
- `role` — "user" or "assistant"
- `text` — final STT text for user turns; the spoken reply for assistant turns
- `email` — authenticated user's email from the JWT/session (for PSTN sessions with no
  email, use the caller identity the telephony edge has — e.g. caller number/DID; exact
  field naming is Claude's discretion, but phone sessions must not be dropped)
- `ts` — timestamp (UTC epoch; local time derivable at query time)
- `session_id` — groups one conversation
- `turn_seq` — monotonic per-session turn index; ordering MUST NOT rely on `ts` alone
  (turns can share a second)
- `code_hash` — stable salted hash of the access code (NEVER the raw code); same code
  groups together across rows
- Candidate field: `tier_id` (include if cheap)

#### Storage & format (LOCKED)
- Newline-JSON ("json is fine, no scaling concerns" — ≤25 users). NOT Parquet.
- Private S3 bucket: SSE, all public access blocked, date-partitioned keys
  (`s3://<bucket>/ledger/dt=YYYY-MM-DD/…`)
- Athena external table over the bucket for ad-hoc queries ("all phrases by email",
  "per-day volume", "by code_hash")
- Voice service buffers and batches: flush every ~2–5 min OR on session end OR at N
  records — no per-utterance S3 PUTs
- Quota stays in the `kmv-voice-usage` DynamoDB table; transcripts live ONLY in S3.
  No co-mingling — different access patterns.

#### Primary UX / acceptance bar (LOCKED)
- "I want to easily see every back and forth like a convo" — the report is NOT a flat
  event log. The Athena query / admin report MUST group by `session_id`, order by
  `turn_seq` (fallback `ts`), and render alternating user/assistant chat bubbles.
- Surface as a report in the existing gated /admin area of the auth app (Phase 05.1).

#### Privacy posture (LOCKED — user ruling 2026-07-06)
- "There are no expectations of privacy, so it's all good." Public demo.
- Pair with a visible "sessions may be recorded" notice in the client — the notice is
  what establishes the no-expectation-of-privacy posture. Ship it in this phase.
- This REVERSES Phase 05.1's "operational-only, no transcripts" deferral.

#### Infra conventions (LOCKED — project standards)
- Terraform/terragrunt matching existing `infra/terraform/live/site/services/voice/`
  conventions; SOPS→SSM for any secrets (e.g. the code-hash salt)
- Voice task role gets least-privilege `s3:PutObject` scoped to the ledger prefix

### Claude's Discretion
- Exact Pipecat frames/observers to tap (user text from the STT/transcription frame,
  assistant text from the LLM/TTS output frame — both carry session context; confirm
  actual frame types in `observers.py`/`pipeline.py`/`duplex.py` at planning time)
- Buffer implementation (in-process asyncio task vs. flush hooks on SessionLifecycle),
  exact flush thresholds within the ~2–5 min guidance, S3 key naming beyond the dt=
  partition scheme
- PSTN identity field shape for telephony sessions (no JWT email exists there)
- Athena DDL details (external table vs. Glue crawler — prefer the simplest static DDL),
  workgroup/output-location placement
- Admin report implementation details (server component + Athena query vs. pre-baked
  query results; pagination; session list → detail drill-in)
- Salt storage/rotation mechanics for `code_hash`
- Where the "sessions may be recorded" notice renders in the client UI

### Deferred Ideas (OUT OF SCOPE)
- Audio recording (ledger is text-only)
- Parquet/compaction, Glue crawlers, partition projection tuning — overkill at ≤25 users
- Retention/deletion tooling beyond a simple S3 lifecycle rule
- `kv transcripts` CLI subcommand (nice-to-have; /admin report is the acceptance bar)
- Full-text search over transcripts
</user_constraints>

## Project Constraints (from CLAUDE.md)

- **Naming:** "klanker-voice" everywhere; NEVER "voiceai" (copyright).
- **Stack pins:** pipecat-ai ~=1.5.0 (Python 3.12); Next.js on the auth app (installed: 16.1.6 —
  do not bump majors during this phase); ElectroDB for DynamoDB modeling in the auth app; Go 1.26
  + cobra for `kv` (not needed this phase — `kv transcripts` is deferred).
- **Infra:** terraform/terragrunt matching defcon.run.34 conventions; SOPS→SSM SecureString for
  secrets, containers consume via `valueFrom`.
- **Security:** public mic wired to metered APIs — every session quota-gated via OIDC token claims
  (the ledger must not weaken any of the existing gates; it is write-only from the voice service).
- **Budget:** ~$120–165/mo ceiling — the ledger's S3/Athena footprint at ≤25 users is cents/month;
  no new always-on infrastructure.
- **GSD workflow enforcement:** file changes go through GSD commands.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Turn capture (user + assistant text) | Voice service (Python/pipecat) | — | Only the pipeline sees finalized turns; aggregator events fire in-process |
| Session identity (email/caller/code) | Auth service (token claims) | Voice service (claim read + hash) | Auth owns claims; voice is the only place all 3 entry paths converge |
| `code_hash` salting | Voice service | SSM (salt storage) | One hashing implementation covering webrtc/anon/pstn; salt never in code |
| Buffering + S3 batch writes | Voice service | — | Buffer must live where turns are produced; flush on session lifecycle |
| Ledger bucket, IAM, Athena DDL | Terraform (infra) | — | Matches existing terragrunt module/unit conventions |
| Ad-hoc queries | Athena (operator console) | — | LOCKED requirement; not in any request path |
| Conversation-view report | Auth app `/admin` (Next.js server) | S3 (direct read) | Gated operator UI; reads ledger objects directly at this scale |
| "Sessions may be recorded" notice | Voice client (React SPA) | — | The client is where users see it; establishes the privacy posture |

## Standard Stack

### Core

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| pipecat-ai | 1.5.0 (installed) | Aggregator turn events (`on_user_turn_message_added`, `on_assistant_turn_stopped`) | Already the pipeline framework; events verified in installed source `[VERIFIED: .venv pipecat 1.5.0 source]` |
| boto3 | already a dependency (session.py/quota.py) | S3 `put_object` for batch flush | Repo's established AWS pattern is sync boto3 via `asyncio.to_thread` — zero new deps `[VERIFIED: apps/voice/src/klanker_voice/session.py:14-17, quota.py:42]` |
| @aws-sdk/client-s3 | ^3.x (match existing ^3.893.0 line) | Admin report reads ledger objects | Same publisher/monorepo as the already-installed `@aws-sdk/client-dynamodb` `[VERIFIED: apps/auth/webapp/package.json:20-25]` |

### Supporting

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| Athena (no SDK in request path) | n/a | Operator ad-hoc queries via console/CLI | LOCKED requirement; table+workgroup declared in terraform, queried out-of-band |
| @aws-sdk/client-athena | ^3.x | ONLY if the planner chooses Athena-in-request-path for the report | Not recommended (see Architecture Patterns); listed for completeness |
| hmac / hashlib (stdlib) | Python 3.12 | `code_hash = HMAC-SHA256(salt, code)` | Never hand-roll hashing beyond stdlib primitives |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Aggregator events | Custom `BaseObserver` on `TranscriptionFrame`/`TTSTextFrame` | Re-implements turn aggregation + interruption truncation the aggregators already do; rejected |
| Aggregator events | RTVI observer bot-transcription messages | RTVI aggregates `LLMTextFrame` (LLM-*generated* text, sentence-chunked) — wrong "actually spoken" semantics `[VERIFIED: pipecat rtvi/observer.py:757-771]` |
| boto3-in-to_thread | aioboto3/aiobotocore | New dependency; repo has zero async-AWS usage; flushes are infrequent (~2min) so thread offload is fine |
| Direct S3 read in /admin | Athena StartQueryExecution poll from Next.js | Seconds of latency per page view, results-bucket + extra IAM plumbing; overkill at ≤25 users |
| Static Athena DDL (terraform `aws_glue_catalog_table`) | Glue crawler | Crawler is explicitly deferred by CONTEXT; static DDL with partition projection needs no repair jobs |

**Installation:**
```bash
# Voice service: NO new Python packages (boto3 already present via pipecat/aws usage)
# Auth webapp:
cd apps/auth/webapp && npm install @aws-sdk/client-s3
```

**Version verification:** `npm view @aws-sdk/client-s3 version` → 3.x line, published continuously
(checked 2026-07-12: latest publish 2026-07-10, 27.7M weekly downloads). Python side verified: no
new package required — `boto3` imports already exist at `session.py:32` and `quota.py:42`.

## Package Legitimacy Audit

| Package | Registry | Age | Downloads | Source Repo | Verdict | Disposition |
|---------|----------|-----|-----------|-------------|---------|-------------|
| @aws-sdk/client-s3 | npm | mature (v3 monorepo, years old) | 27.7M/wk | github.com/aws/aws-sdk-js-v3 | [OK]* | Approved |
| @aws-sdk/client-athena | npm | mature (v3 monorepo) | 950k/wk | github.com/aws/aws-sdk-js-v3 | [OK]* | Approved only if Athena-in-request-path is chosen |

\* The `gsd-tools query package-legitimacy check` seam returned `SUS: too-new` for both — a false
positive: AWS publishes the entire SDK monorepo near-daily, so the *latest version* is always
days old. Both packages share the exact publisher/repo/scope with `@aws-sdk/client-dynamodb`
already pinned in `apps/auth/webapp/package.json:20`. Signals (repoUrl `aws/aws-sdk-js-v3`, no
postinstall, no deprecation, 27.7M and 950k weekly downloads) support legitimacy. Planner should
still pin the same `^3.x` range as the existing AWS SDK deps rather than a floating latest.

**Packages removed due to [SLOP] verdict:** none
**Packages flagged as suspicious [SUS]:** none (tool false-positive documented above)

## Architecture Patterns

### System Architecture Diagram

```
  Browser (voice1/voice2 SPA)     PSTN caller (Phase 12)
        │  mic + OIDC token             │  SIP → Asterisk → ARI
        ▼                               ▼
  server.py /api/offer          telephony/controller.py
  (validate token → start_gate) (caller-ID → /tel mint → gate)
        │                               │
        └───────────┬───────────────────┘
                    ▼
        call_runtime.create_call_session()   ◄── ONE tap seam (all 3 paths)
                    │
                    ▼
   Pipeline: input → [rtvi] → stt → [gate] → [duplex] → router
             → user_aggregator ──► on_user_turn_message_added ──┐
             → llm → tts → transport.output()                   │
             → assistant_aggregator ─► on_assistant_turn_stopped┤
                                                                ▼
                                              LedgerWriter (per session)
                                              buffer + turn_seq counter
                                        flush: ~120s timer | 50 records | release
                                                                │ boto3 put_object
                                                                ▼   (asyncio.to_thread)
                              s3://<ledger-bucket>/ledger/dt=YYYY-MM-DD/*.jsonl
                                     │                          │
                     Athena external table            auth app /admin report
                     (partition projection,           (ListObjectsV2 + GetObject,
                      operator ad-hoc SQL)             group session_id, sort turn_seq,
                                                       threaded chat bubbles)
```

### Recommended Project Structure

```
apps/voice/src/klanker_voice/
├── ledger.py                # NEW: LedgerRecord, LedgerWriter (buffer/flush/hash), code_hash()
├── call_runtime.py          # MODIFIED: construct LedgerWriter, wire both aggregator events,
│                            #           final flush in CallSession.run finally / close
├── auth.py                  # MODIFIED: read optional email/code claims into SessionIdentity
├── server.py                # MODIFIED: FastAPI lifespan → drain/flush writers on SIGTERM
apps/auth/webapp/src/app/admin/          # NEW (or from Phase 05.1): gated route group
├── layout.tsx               # ADMIN_EMAILS gate (404 for non-admins)
├── transcripts/page.tsx     # session list (by day)
└── transcripts/[sessionId]/page.tsx     # threaded chat view
infra/terraform/
├── modules/ledger/v1.0.0/   # NEW: S3 bucket + SSE + PAB + lifecycle, Athena workgroup,
│                            #      aws_glue_catalog_database + aws_glue_catalog_table (projection)
└── live/site/region/us-east-1/ledger/terragrunt.hcl   # NEW unit, standard include pattern
```

### Pattern 1: The tap — aggregator turn events (THE crux answer)

**What:** Register event handlers on the two aggregator halves `build_pipeline()` already
constructs (`pipeline.py:136-140` builds `LLMContextAggregatorPair`; `BuiltPipeline` exposes
`user_aggregator`/`assistant_aggregator` at `pipeline.py:56-57`).

**Evidence for the events (installed pipecat 1.5.0,
`pipecat/processors/aggregators/llm_response_universal.py`):**
- `on_user_turn_message_added` registered at line 655, fired at line 846 with
  `UserTurnMessageAddedMessage(content, timestamp, user_id)` — docstring (lines 304-323):
  "Fired when a user message is written to the LLM context … `content` is always populated."
  This is the finalized user STT turn, post-duplex-suppression, post-gate. `[VERIFIED]`
- `on_assistant_turn_stopped` registered at line 1438, fired at line 2069 inside
  `_trigger_assistant_turn_stopped` (lines 2056-2075) with
  `AssistantTurnStoppedMessage(content, interrupted, timestamp)` (lines 326-343). `[VERIFIED]`
- Registration API is the same `add_event_handler(name, coro)` the repo already uses on
  observers (`observers.py:170-172`). `[VERIFIED]`

**Why this covers ALL THREE entry paths:** every live session — WebRTC voice1
(`server.py:242-252`), voice2 full-duplex (same `_negotiate_webrtc` path, variant only swaps
config at `server.py:212-215`), and telephony (`telephony/controller.py:106` import;
`CallSession` built in `_finish_stasis_start*`) — is constructed exclusively through
`create_call_session()` (`call_runtime.py:131-244`), which calls `build_pipeline()` and holds
the `built` handle with both aggregators. Wiring the tap there requires **zero changes at the
three call sites**. `[VERIFIED: call_runtime.py:183-191, server.py:242, controller.py:106]`

**"Actually spoken" semantics:** the assistant aggregator is placed AFTER `transport.output()`
(`pipeline.py:174-183`), and both `SmallWebRTCTransport` and `TelephonyOutputTransport`
(`telephony/transport.py:106,225`) extend `BaseOutputTransport`, whose audio task only pushes
frames downstream after writing their audio to the wire (`pipecat/transports/base_output.py:
844-871`) and drains interruptible frames on an `InterruptionFrame` (`base_output.py:548-564`).
TTS text that was never played therefore never reaches the aggregator — the ledgered assistant
`text` is the truncated, actually-spoken reply, and `AssistantTurnStoppedMessage.interrupted`
tells you a barge-in happened. Recommend persisting `interrupted` as an optional field. `[VERIFIED]`

**Example (shape only — planner refines):**
```python
# call_runtime.py, inside create_call_session(), after build_pipeline():
writer = LedgerWriter(
    session_id=gate_result.session_id,
    identity=identity,                 # email/caller_id/code inputs — see Pattern 3
    tier_id=gate_result.tier.tier_id,
    enabled=not gate_result.bypass_accounting,   # never ledger smoke sessions
)

@built.user_aggregator.event_handler("on_user_turn_message_added")
async def _ledger_user(_agg, message):           # UserTurnMessageAddedMessage
    await writer.append(role="user", text=message.content)

@built.assistant_aggregator.event_handler("on_assistant_turn_stopped")
async def _ledger_assistant(_agg, message):      # AssistantTurnStoppedMessage
    if message.content:
        await writer.append(role="assistant", text=message.content,
                            interrupted=message.interrupted)
```

**What is deliberately NOT captured (all by existing design, document in the runbook):**
- Telephony pre-unlock speech: `GateProcessor` never forwards `TranscriptionFrame`s while locked
  (`telephony/gate.py:147-151, 248-273`) — the §24/D-05e redaction boundary holds for the ledger
  automatically. `[VERIFIED]`
- voice2 backchannels ("mm-hm"): `DuplexController` swallows the backchannel's final transcript
  before the aggregator (`duplex.py:159-163`) — correct, they are not turns. `[VERIFIED]`
- Router acks, deterministic goodbye, bot backchannel emitter: all `TTSSpeakFrame(...,
  append_to_context=False)` (`knowledge/router.py:386`, `pipeline.py:300`, `duplex.py:257`), and
  the assistant aggregator skips non-`append_to_context` text (`llm_response_universal.py:
  1940-1942`). The ledger records the *conversation*, not every emitted sound. `[VERIFIED]`

### Pattern 2: turn_seq — writer-owned monotonic counter

Do NOT source `turn_seq` from `TurnTrackingObserver` (its `on_turn_started/on_turn_ended`
counts user-initiated turns only — `pipecat/observers/turn_tracking_observer.py:69-70,181-193`)
and do NOT derive it from `ts`. The `LedgerWriter` increments an in-memory per-session integer
on every `append()` — trivially monotonic, shared by both roles, survives nothing it doesn't
need to survive (a session is one process, one object). `[VERIFIED: observer source read]`

### Pattern 3: Identity plumbing (what exists at tap time, per path)

| Path | `sub` at tap | email | raw code | tier_id | caller identity |
|------|-------------|-------|----------|---------|----------------|
| WebRTC magic-link | Auth.js user id (opaque) | ❌ not in token | ❌ not in token, not on AuthProfile | ✅ claim | n/a |
| WebRTC bypass /join | `anon:<code>:<uuid>` | ❌ (anonymous) | ✅ parseable from sub | ✅ claim | n/a |
| PSTN /tel mint | gate identity is `tel:<caller_id>`; the validated mint token's sub is `anon:<code>:<uuid>` | ❌ | ✅ in mint-token sub (currently discarded) | ✅ | ✅ `CallIdentity.caller_id`/`did` |

Evidence: access token claims are ONLY `tier_id`/`group` (`apps/auth/webapp/src/config/oidc.ts:
388-395`, "Deliberately emits ONLY the two namespaced tier_id/group claims"); email lives on
AuthProfile (`entities/auth-profile.ts:52`), the redeemed code on LoginIntent (keyed by email,
`entities/login-intent.ts:16,63-64`) and CodeRedemption (`(code, userId)`,
`entities/code-redemption.ts:20`); AuthProfile stores `activeTierId`/`activeGroup` but NOT the
code (`auth-profile.ts:99-103`); anon sub format `anon:<code>:<uuid>`
(`lib/bypass-token.ts:92`); `/tel` mints via the same `mintAnonToken`
(`app/tel/[e164]/route.ts:69-70`); the telephony controller validates that token but keeps only
`tier_id` (`telephony/controller.py:568-572`) and gates as `sub=f"tel:{caller_id...}"`
(`controller.py:742`); `CallIdentity` already carries `caller_id`/`did`/`tier_id`
(`call_runtime.py:81-87`). `[VERIFIED]`

**Recommended plumbing (smallest change set that satisfies the LOCKED record shape):**
1. **Auth app:** in `extraTokenClaims` (oidc.ts:388) add
   `[config.oidc.claimNames.email]: profile?.email ?? null` — the profile is already fetched on
   that exact line. For the code: stamp `activeCode` onto AuthProfile in the login-intent bridge
   (it already stamps `activeTierId`/`activeGroup` from the same LoginIntent that carries `code` —
   `config/login-intent-bridge.ts`), then emit a namespaced `code` claim the same way.
   `mintAnonToken` needs no email (anonymous); its sub already carries the code.
2. **Voice `auth.py`:** read both optional claims into `SessionIdentity` (additive fields,
   defaulted `None` — same pattern as Phase 12's additive `CallIdentity` fields).
3. **Voice `ledger.py`:** ONE `resolve_code()` helper — claim if present, else parse
   `anon:<code>:<uuid>` subs, else (PSTN) the mint-token sub (extend
   `_mint_tier_from_caller_id` to return `identity.sub` alongside `tier_id` — a 2-line change).
   ONE `code_hash()` = `hmac.new(salt, code.encode(), sha256).hexdigest()` with salt from env
   `KMV_LEDGER_SALT` (SSM-injected). PSTN `email` field: use `caller_id` (E.164) or
   `tel:<caller_id>`; record `did` as an optional extra field.
4. `tier_id` is free (`gate_result.tier.tier_id`) — include it (the CONTEXT "if cheap" bar is met).

Fallback if the auth-app claim changes are descoped: record `sub` instead of email for magic-link
users and let the admin report join sub→email via `getAuthProfile` (auth app already has
DynamoDB access) — but then `email` is a display-time join, not a ledger field, and `code_hash`
is null for magic-link sessions. Flag to the user if chosen. `[ASSUMED — design tradeoff]`

### Pattern 4: Buffered batch writer (flush design)

**Repo AWS precedent:** every AWS call runs sync-boto3 inside `asyncio.to_thread`
(`session.py:14-17` module docstring; `session.py:175,179,268-272`; `quota.py` is plain boto3
called from `to_thread` by session.py). No aioboto3 anywhere. Follow it. `[VERIFIED]`

**Design:**
- `LedgerWriter` per session, created in `create_call_session()`. Buffer = `list[dict]`,
  `turn_seq` counter, `asyncio.Lock` around append/flush.
- Flush triggers: (a) a per-writer `asyncio.Task` timer every **120s** (inside the 2–5min
  guidance; same `asyncio.create_task` pattern as `SessionLifecycle._tick_loop`,
  `session.py:203-205`); (b) buffer length ≥ **50**; (c) **final flush on session end**.
- Final-flush hook: `CallSession.run()`'s `finally` already brackets every session
  (`call_runtime.py:110-121` — "always … released"); add `await writer.close()` there, after
  `lifecycle.stop()`. `SessionLifecycle.release()` is the single idempotent funnel every
  teardown layer routes through (`session.py:247-274`), and `run()`'s `finally` executes even on
  cancellation — the same guarantee the heartbeat release relies on.
- Flush = one `put_object` per flush:
  `ledger/dt=<UTC date>/<HHMMSSZ>-<session_id>-<batch_seq>.jsonl` — sorts chronologically within
  a partition (CONTEXT "Specific Ideas"). Body = `"\n".join(json.dumps(r) for r in batch)`.
- Error posture: mirror `LatencyReportObserver._write_artifact` (`observers.py:278-283`) — log
  and continue, never let ledger I/O take down a live conversation. Keep the failed batch in the
  buffer for the next flush attempt (bounded: drop-oldest above ~500 records).

**Fargate SIGTERM survivability:** the container runs uvicorn as PID 1 in exec form
(`apps/voice/Dockerfile:116`), so SIGTERM → uvicorn graceful shutdown → FastAPI lifespan
shutdown hooks. `server.py` currently has **no lifespan/shutdown handler** `[VERIFIED: grep]`,
and the ecs-task module sets no `stopTimeout` → ECS default 30s before SIGKILL. Add a FastAPI
lifespan (or `app.add_event_handler("shutdown", ...)`) that cancels live sessions'
runners (which drives each `run()` `finally` → final flush) and awaits a module-level
`flush_all(timeout=~10s)`. The telephony entrypoint (`telephony/__main__.py`) needs the
equivalent in its `finally` (line 119). Also note ECS scale-in protection
(`session.py:420-434`) already shields tasks with active sessions from autoscale scale-in —
SIGTERM mid-session is mostly deploys, which is exactly when the lifespan flush matters.

### Pattern 5: Terraform shape (matching existing conventions)

**How infra is declared today:** data-only `services/<name>/service.hcl` stubs hold ECR/table/
IAM/task/service locals read by `site.hcl` (`services/voice/service.hcl:1-5`); regional
terragrunt units live at `live/site/region/us-east-1/<unit>/terragrunt.hcl` including a
versioned module via `modules/<name>/config.hcl` (`region/us-east-1/secrets/terragrunt.hcl`);
secrets flow SOPS (`live/site/.secrets.sops.json`) → secrets module → SSM SecureString at
`/kmv/secrets/use1/<name>/<key>` (`SECRETS.md:7-16`) → container `secrets.valueFrom`
(`services/voice/service.hcl:132-149`). `[VERIFIED]`

**Minimal additions:**
1. `modules/ledger/v1.0.0/` — `aws_s3_bucket` (SSE-S3/AES256 is sufficient; KMS optional),
   `aws_s3_bucket_public_access_block` (all four true),
   `aws_s3_bucket_lifecycle_configuration` (simple expiration, e.g. 365d — CONTEXT allows one
   rule), `aws_athena_workgroup` (results to a `athena-results/` prefix in the same bucket,
   `enforce_workgroup_configuration=true`), `aws_glue_catalog_database` +
   `aws_glue_catalog_table` carrying the static DDL with **partition projection** (see below).
   Static terraform DDL > bootstrap script: it lives in state, applies idempotently, and the
   repo has no SQL-bootstrap precedent. `[VERIFIED: module conventions; DDL choice is recommendation]`
2. New unit `live/site/region/us-east-1/ledger/` following the secrets unit's include pattern.
3. `services/voice/service.hcl` — task_role statement:
   `{sid="LedgerPutOnly", actions=["s3:PutObject"], resources=["arn:aws:s3:::<bucket>/ledger/*"]}`
   plus container secret `KMV_LEDGER_SALT` ←
   `/kmv/secrets/use1/ledger/code_hash_salt`, plus a plain env var for the bucket name.
4. `services/auth/service.hcl` — task_role statements for the report:
   `s3:ListBucket` (bucket, prefix-conditioned to `ledger/*`) + `s3:GetObject`
   (`.../ledger/*`); Athena/Glue grants only if the planner puts Athena in the request path.
5. `.secrets.sops.json` + template: add the `ledger.code_hash_salt` entry (one random 32-byte
   value; rotation = write new value + accept that historical code_hash groupings break —
   document, don't build tooling).

**Athena DDL (partition projection — no MSCK, no crawler):**
```sql
CREATE EXTERNAL TABLE ledger (
  role       string,
  text       string,
  email      string,
  ts         bigint,
  session_id string,
  turn_seq   int,
  code_hash  string,
  tier_id    string,
  channel    string,
  interrupted boolean
)
PARTITIONED BY (dt string)
ROW FORMAT SERDE 'org.openx.data.jsonserde.JsonSerDe'
WITH SERDEPROPERTIES ('ignore.malformed.json'='true')
LOCATION 's3://<bucket>/ledger/'
TBLPROPERTIES (
  'projection.enabled'='true',
  'projection.dt.type'='date',
  'projection.dt.format'='yyyy-MM-dd',
  'projection.dt.range'='2026-07-01,NOW',
  'projection.dt.interval'='1',
  'projection.dt.interval.unit'='DAYS',
  'storage.location.template'='s3://<bucket>/ledger/dt=${dt}/'
);
-- [CITED: docs.aws.amazon.com/athena/latest/ug/partitions.html — projection.dt.* properties]
```
In terraform this is expressed as `aws_glue_catalog_table` columns + `parameters` (the same
TBLPROPERTIES map). The Hive `org.apache.hive.hcatalog.data.JsonSerDe` also works; OpenX is the
common choice for tolerant parsing. `[CITED: AWS Athena docs]`

**Conversation query worth shipping in the runbook:**
```sql
SELECT session_id, turn_seq, role, text, from_unixtime(ts) AS t
FROM ledger WHERE dt BETWEEN date_format(current_date - interval '7' day, '%Y-%m-%d')
                        AND date_format(current_date, '%Y-%m-%d')
ORDER BY session_id, turn_seq;
```

**SCP gotcha (from Phase 12-07, STATE.md):** the org SCP `DenyInfraAndStorage` (p-cvd490xt)
denied SG/IAM creation to CI principals — only `ecr` applied via CI; IAM/SG changes needed a
local operator-SSO `terragrunt apply`. The new bucket + IAM statements will very likely hit the
same wall ("Storage" is in the SCP's name). Plan for a local operator apply step, with CI plans
as the review artifact. `[VERIFIED: STATE.md Phase 12-07 deploy record; SCP scope inference ASSUMED]`

### Pattern 6: Admin conversation view — direct S3 read, Athena stays ad-hoc

**Critical finding:** `/admin` does not exist. `apps/auth/webapp/src/app/` contains only
`tel/`, `join/`, `(authlogin)/login`, and `api/` routes `[VERIFIED: directory listing]`; the
Phase 05.1 phase directory is empty and its ROADMAP plans are "TBD" `[VERIFIED:
.planning/phases/05.1-*/ empty; ROADMAP.md:220-237]`. The approved admin design
(`docs/superpowers/specs/2026-07-06-admin-panel-design.md`: gated route group, `ADMIN_EMAILS`
allowlist, non-admins get 404, code-free magic-link for admins) is a spec, not code. **This
phase must either declare a dependency on Phase 05.1 or include a minimal-shell bootstrap plan**
(gate + layout only — no users/codes/kill-switch panels, which remain 05.1's scope).

**Report implementation recommendation:** Next.js App Router **server components** reading S3
directly with `@aws-sdk/client-s3` (task-role credentials — the same default-chain pattern the
existing ElectroDB client uses):
- *Session list page:* `ListObjectsV2` over `ledger/dt=<day>/` for the selected day(s); object
  keys already encode time + session_id, so a list is derivable without reading bodies (read
  bodies only for first/last-line preview if desired). Day picker = partition picker.
- *Session detail page:* `ListObjectsV2` with a `<session_id>`-filtered scan of that day's keys
  (or key naming `dt=…/<HHMMSSZ>-<session_id>-<n>.jsonl` makes suffix filtering trivial),
  `GetObject` each, parse lines, filter `session_id`, sort by `turn_seq`, render alternating
  bubbles (user right / assistant left per CONTEXT).
- Latency: a handful of small objects per session — tens of ms. No pagination complexity at
  ≤25 users; add a simple day-window cap.
- Athena is NOT in the request path: it exists as the LOCKED ad-hoc surface (console/CLI +
  runbook queries). S3 Select is not an option (deprecated for new use) — and is unnecessary
  here anyway. `[ASSUMED: S3 Select deprecation status — stated as given in phase constraints]`

Auth task role today has DynamoDB + SES only (`services/auth/service.hcl:55-85`) — the S3 read
grants in Pattern 5 are required. `[VERIFIED]`

### Pattern 7: "Sessions may be recorded" notice

Client screens live at `apps/voice/client/src/screens/` (`ReadyToStart.tsx`, `LandBounce.tsx`,
`Ceremony.tsx`, `Live.tsx`) `[VERIFIED: listing]`. Recommended placement (Claude's discretion per
CONTEXT): small persistent print on the pre-connect screen(s) (`ReadyToStart`/`LandBounce`) —
the tap gesture then constitutes informed continuation — plus optionally a one-line footer on
`Live`. PSTN callers get no visual notice; if desired, one sentence in the telephony unlock
greeting covers it (discretionary; the LOCKED requirement is the client notice only).

### Anti-Patterns to Avoid

- **Per-utterance S3 PUTs** — explicitly locked out; buffer and batch.
- **Custom frame-level transcript reassembly** (observer on TranscriptionFrame/TTSTextFrame) —
  duplicates aggregator logic and gets interruption truncation wrong.
- **Writing transcripts to `kmv-voice-usage`** — locked out ("no co-mingling").
- **Glue crawler / MSCK REPAIR** — partition projection makes both unnecessary; crawlers are
  explicitly deferred.
- **Raw access code anywhere in a record or log** — only the salted hash; also never log record
  text at INFO level in the voice service (transcripts in CloudWatch would be a second,
  unmanaged ledger).
- **Ledgering bypass/smoke sessions** — `service:smoke` (`auth.py:50`) sessions would pollute
  the ledger; skip when `gate_result.bypass_accounting` is true. (Telephony's placeholder
  pre-unlock lifecycle is also bypass; the writer should be enabled/upgraded at unlock alongside
  `upgrade_from_bypass` — `session.py:207-245`.)

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Turn finalization + barge-in truncation | Frame-level text reassembly | Aggregator events (`on_user_turn_message_added`, `on_assistant_turn_stopped`) | Aggregators already handle interim/final, interruptions, function-call noise `[VERIFIED]` |
| "What was actually spoken" | TTS word-timestamp tracking | Assistant aggregator after `transport.output()` (already this repo's topology) | Output transport playout-gates TTS text frames downstream `[VERIFIED: base_output.py:844-871]` |
| Async S3 client | aioboto3 integration | sync boto3 + `asyncio.to_thread` | Repo-wide precedent (session.py/quota.py); flushes are rare |
| Partition management | MSCK repair cron / crawler | Athena partition projection TBLPROPERTIES | Zero maintenance; AWS-documented pattern `[CITED: AWS docs]` |
| Salted hashing | custom scheme | `hmac.new(salt, code, sha256)` stdlib | Standard keyed hash; constant-time, no deps |
| Admin S3 querying | Athena poll loop in Next.js | ListObjectsV2 + GetObject | ≤25 users; seconds→ms; less IAM |

**Key insight:** pipecat 1.5.0 already computes exactly the two artifacts this phase needs (the
context-committed user turn and the spoken-truncated assistant turn); the whole voice-service
side of this phase is ~1 new module + event wiring, not pipeline surgery.

## Common Pitfalls

### Pitfall 1: `on_assistant_turn_stopped` double-fire / missing final fire
**What goes wrong:** upstream issue reports the event firing twice per spoken turn in some TTS
text-frame configurations ([pipecat#3762](https://github.com/pipecat-ai/pipecat/issues/3762))
and turn events not firing on task cancellation ([pipecat#3564](https://github.com/pipecat-ai/pipecat/issues/3564)).
**Why:** multiple flush paths inside the aggregator (BotStoppedSpeaking vs. response-end).
**How to avoid:** (a) dedupe in the handler — skip an append whose (role, text, timestamp)
equals the previous assistant append; (b) at `writer.close()`, optionally reconcile against
`built.context` messages (`CallSession.context` is already exposed — `call_runtime.py:108`) and
append any trailing assistant message the events missed. Write a test that fires the handler
twice with identical payloads and asserts one record.
**Warning signs:** duplicate consecutive assistant bubbles in the admin view.

### Pitfall 2: The pre-rendered greeting never appears in the ledger
**What goes wrong:** every deployed variant ships `greet_first = false`
(`pipeline.toml:45`, `configs/voice2.toml:51`, `configs/telephony.toml:52` `[VERIFIED]`); the
WebRTC opener is a client-side mp3 (`client/src/greeting/greetingPlayer.ts`) that never touches
the pipeline — so browser transcripts start with the user's first turn. The server also doesn't
know *which* of the 3 clips played.
**How to avoid:** decide explicitly (discretion): simplest is to skip it and document that
`turn_seq=1` is the user's first utterance; alternatively write a synthetic
`role=assistant, turn_seq=0, text="[pre-rendered greeting]", source="greeting-clip"` record at
session start for webrtc channels. Telephony's unlock greeting IS ledgered normally (it flows
LLM→TTS→aggregator via `greet_now`).
**Warning signs:** operator confusion — "why does every chat start with the user?"

### Pitfall 3: Losing the last turns on teardown/SIGTERM
**What goes wrong:** buffered records vanish when the task is killed mid-session (deploy,
scale-in, crash).
**How to avoid:** final flush in `CallSession.run()`'s `finally` (runs on cancellation too);
FastAPI lifespan shutdown draining all writers with a bounded timeout (< ECS's 30s default);
same `finally`-flush in `telephony/__main__.py`. Accept that SIGKILL loses ≤120s of buffer —
that's the design point of the flush interval.
**Warning signs:** sessions in the admin view that end abruptly vs. what the operator heard.

### Pitfall 4: Identity fields silently null
**What goes wrong:** shipping the writer before the auth-claim changes means every magic-link
row has `email=null, code_hash=null` — and backfilling is impossible (the token is gone).
**How to avoid:** sequence the auth-app claim additions (email + code claims,
`activeCode` stamping) BEFORE or WITH the voice tap plan; assert in tests that a webrtc-path
record built from a claims-bearing token carries both fields.
**Warning signs:** "all phrases by email" Athena query returns nothing.

### Pitfall 5: turn ordering by timestamp
**What goes wrong:** user/assistant turns share a second; `AssistantTurnStoppedMessage.timestamp`
is the turn *start* (ISO8601), not commit time — sorting by it interleaves wrongly.
**How to avoid:** LOCKED decision already covers this: `turn_seq` from the writer counter is the
sort key everywhere (Athena examples, admin view); `ts` is display-only.

### Pitfall 6: Athena schema drift vs. writer JSON
**What goes wrong:** field renamed in `ledger.py` but not in the Glue table → silently null
columns (OpenX SerDe ignores unknown/missing keys).
**How to avoid:** one JSON-schema-ish constant in `ledger.py` (field names tuple) + a unit test
that asserts the terraform DDL column list matches (read the .tf or duplicate the list and lint
in CI); `ignore.malformed.json='true'` so one bad line never breaks a whole day's queries.

### Pitfall 7: Timezone mismatches between partitions and records
**What goes wrong:** `dt=` computed in local time while `ts` is UTC → turns near midnight land
in the "wrong" partition and per-day queries drift.
**How to avoid:** everything UTC — `dt` from `datetime.now(timezone.utc)` (the exact
`quota._today()` pattern, `quota.py:173-174`); `ts` = epoch seconds (int). Local time is a
query-time `from_unixtime(ts) AT TIME ZONE ...` concern only.

### Pitfall 8: CI cannot apply the new infra
**What goes wrong:** `terragrunt apply` from CI fails on bucket/IAM creation (org SCP
`DenyInfraAndStorage`, proven in Phase 12-07).
**How to avoid:** plan the deploy step as local operator-SSO apply with CI plan as review, same
as 12-07's network/ecs units.

## Code Examples

### Ledger record (newline-JSON line, one per turn)
```json
{"role":"user","text":"tell me about the klanker platform","email":"dad@example.com","ts":1752350400,"session_id":"7f3c…","turn_seq":3,"code_hash":"a1b2…","tier_id":"kph-tier","channel":"webrtc","interrupted":false}
```
PSTN row: `"email":"tel:+16135551234"` (or a separate `caller_id` field + null email — naming is
discretionary; do not drop the session), plus optional `"did":"+13474803715"`.

### code_hash (voice service, stdlib only)
```python
import hashlib, hmac, os

def code_hash(code: str) -> str | None:
    salt = os.environ.get("KMV_LEDGER_SALT", "")
    if not (salt and code):
        return None
    return hmac.new(salt.encode(), code.strip().lower().encode(), hashlib.sha256).hexdigest()
    # lower/strip mirrors AccessCode's write-time normalization (access-code.ts normalizeCode)
```

### Flush (boto3 via to_thread — repo pattern)
```python
def _put(self, key: str, body: bytes) -> None:      # runs in a thread
    boto3.client("s3").put_object(Bucket=self._bucket, Key=key, Body=body,
                                  ContentType="application/x-ndjson")

async def flush(self) -> None:
    async with self._lock:
        batch, self._buffer = self._buffer, []
    if not batch:
        return
    key = (f"ledger/dt={_utc_date()}/"
           f"{_utc_hms()}Z-{self._session_id}-{self._batch_seq:04d}.jsonl")
    self._batch_seq += 1
    body = ("\n".join(json.dumps(r, ensure_ascii=False) for r in batch) + "\n").encode()
    try:
        await asyncio.to_thread(self._put, key, body)
    except Exception as e:                    # observers.py:278-283 posture
        logger.error(f"ledger flush failed ({len(batch)} records kept): {e}")
        async with self._lock:
            self._buffer[:0] = batch          # retry next flush, bounded elsewhere
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| pipecat `TranscriptProcessor` (pre-1.x) | Aggregator turn events on `LLMContextAggregatorPair` (llm_response_universal) | pipecat 1.x "universal" context line | No extra processor in the pipeline; events carry finalized turn text |
| MSCK REPAIR / Glue crawler for partitions | Athena partition projection TBLPROPERTIES | GA years ago; standard for date partitions | Zero partition maintenance |
| S3 Select for JSON filtering | Plain GetObject + app-side parse (or Athena) | S3 Select deprecated for new use | Not a design option here `[ASSUMED]` |

**Deprecated/outdated:** none affecting the pinned stack.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | S3 Select is deprecated/unavailable for new use | Pattern 6 | None — recommendation doesn't use it either way |
| A2 | Org SCP `DenyInfraAndStorage` also blocks CI S3-bucket creation (name-based inference; proven only for SG/IAM) | Pattern 5 / Pitfall 8 | CI apply might actually succeed — plan the local-apply fallback either way |
| A3 | Adding an email claim to the access token is acceptable to the user (PII in a token audienced to the voice service, private demo) | Pattern 3 | If rejected, fall back to sub-recorded + report-time join (email becomes display-only) |
| A4 | Emitting the (raw) code as an access-token claim is acceptable (it already appears in anon-token subs) | Pattern 3 | If rejected, hash at the auth side or accept null code_hash for magic-link users |
| A5 | 365-day lifecycle expiration is an acceptable "simple lifecycle rule" default | Pattern 5 | Trivial to change; confirm at planning |
| A6 | Skipping bypass/smoke sessions from the ledger matches operator intent | Anti-patterns | If operator wants smoke visibility, flip the writer's `enabled` gate |
| A7 | Pre-rendered greeting handling (skip vs. synthetic row) is operator-neutral | Pitfall 2 | Cosmetic; confirm preferred rendering at planning |

## Open Questions

1. **Phase 05.1 dependency — the `/admin` area does not exist.**
   - What we know: ROADMAP lists 05.1 as inserted/unplanned; the phase dir is empty; no admin
     routes exist in the auth app; the admin design spec is approved
     (`docs/superpowers/specs/2026-07-06-admin-panel-design.md`).
   - What's unclear: whether the user wants 05.1 executed first, or a minimal gated shell
     bootstrapped inside Phase 15 (gate + transcripts report only).
   - Recommendation: bootstrap the minimal `ADMIN_EMAILS`-gated shell in this phase per the
     approved spec's gating design (magic-link login already works; the gate is a layout check +
     404), leaving users/codes/kill-switch panels to 05.1. Surface this to the user before
     planning.
2. **Email/code claims in the access token (A3/A4).**
   - What we know: neither is in the token today; both are one-file changes in the auth app;
     the anon paths already leak the code into `sub` by design.
   - Recommendation: add both namespaced claims; hash code in the voice service only.
3. **PSTN field naming** (`email` = `tel:+E164` vs. separate `caller_id` column + null email) —
   pure discretion; pick one at planning and encode it in the Athena DDL.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| aws CLI | operator apply / Athena ad-hoc | ✓ | 2.32.25 | — |
| terraform | infra plans | ✓ | 1.14.3 | — |
| terragrunt | infra plans | ✓ | 0.99.1 | — |
| sops | salt secret entry | ✓ | 3.11.0 | — |
| node (ambient) | auth webapp / client tests | ✓ (with caveat) | v22.1.0 | `nvm use 23` — client tests need ≥22.12 (known repo gotcha, STATE.md) |
| Python venv (uv) | voice tests | ✓ | 3.12 (.venv present) | — |
| dynamodb-local :8888 | quota tests (skip-if-unreachable) | not probed | — | tests self-skip |

**Missing dependencies with no fallback:** none.
**Missing dependencies with fallback:** node ambient version (use nvm 23 for client tests).

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework (voice) | pytest ≥9.1.1 + pytest-asyncio ≥1.4.0 (`apps/voice/pyproject.toml:15-16`) |
| Framework (auth webapp) | vitest (`npm test` → `vitest run`, `package.json:10`) |
| Config file | `apps/voice/pyproject.toml [tool.pytest.ini_options]`; webapp vitest config present |
| Quick run command | `cd apps/voice && uv run pytest tests/test_ledger.py -x` (Wave 0 file) |
| Full suite command | `cd apps/voice && uv run pytest` (currently 421 passed / 53 skipped) and `cd apps/auth/webapp && npm test` (59/59) |

### Phase Requirements → Test Map
(No formal REQ IDs assigned yet — ROADMAP Phase 15 requirements are "TBD"; behaviors from the
LOCKED decisions.)

| Req | Behavior | Test Type | Automated Command | File Exists? |
|-----|----------|-----------|-------------------|-------------|
| Both roles recorded, turn_seq monotonic | writer appends user+assistant, seq increments | unit | `uv run pytest tests/test_ledger.py -x` | ❌ Wave 0 |
| Tap wiring on all paths | create_call_session registers both handlers; bypass sessions skipped | unit (fake aggregators) | `uv run pytest tests/test_call_runtime.py -x` | ✅ file exists — extend |
| Flush triggers (timer / N / close) + SIGTERM drain | flush fires; close flushes; put_object failure keeps buffer | unit (monkeypatched boto3, `fake_aws`-style — `tests/test_session.py:89-95` precedent) | `uv run pytest tests/test_ledger.py -x` | ❌ Wave 0 |
| code_hash: salted, never raw, anon-sub parse, PSTN sub | hash stability + null-salt behavior | unit | `uv run pytest tests/test_ledger.py -x` | ❌ Wave 0 |
| Email/code claims | extraTokenClaims emits claims; auth.py reads them | vitest + pytest | `npm test` / `uv run pytest tests/test_auth.py -x` | ✅ both exist — extend |
| Admin gate + threaded view | non-admin 404; session grouped, turn-ordered render | vitest (route/unit) | `cd apps/auth/webapp && npm test` | ❌ Wave 0 |
| Recording notice | notice renders on pre-connect screen | vitest (client) | `cd apps/voice/client && npm test` | ❌ Wave 0 |
| Bucket private + IAM scoped + projection DDL | terraform plan review | manual/checkpoint | `terragrunt plan` (operator) | manual-only — SCP blocks CI apply; plan output is the artifact |

### Sampling Rate
- **Per task commit:** the touched module's test file with `-x`
- **Per wave merge:** full voice suite + auth webapp suite
- **Phase gate:** all three suites green + a live end-to-end: one browser session + (if DID
  available) one PSTN call, then verify rows in S3 and the admin view renders the thread

### Wave 0 Gaps
- [ ] `apps/voice/tests/test_ledger.py` — writer/flush/hash/skip-bypass
- [ ] `apps/auth/webapp/src/app/admin/**/__tests__` — gate + view tests
- [ ] client notice test (extend existing screen test patterns)

## Security Domain

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | yes | Existing OIDC/JWT gate unchanged; admin gate = session email ∈ `ADMIN_EMAILS` (approved 05.1 design) |
| V3 Session Management | yes | Existing next-auth session for /admin; no new session machinery |
| V4 Access Control | yes | Least-privilege task roles: voice = `s3:PutObject` on `ledger/*` ONLY (no read/list — write-only ledger); auth = read-only on `ledger/*`; non-admin → 404 not 403 (no route disclosure) |
| V5 Input Validation | yes | Transcript text is untrusted user speech — JSON-encode (never string-interpolate into SQL/HTML); React escapes by default; Athena queries parameterized/static |
| V6 Cryptography | yes | HMAC-SHA256 stdlib for code_hash; SSE on the bucket; salt in SSM SecureString, never in code/TOML (`config.py:49-53` credential-field regex applies — salt is env-only anyway) |
| V8 Data Protection | yes | Private bucket (PAB all-true), transcripts = personal data → visible recording notice ships in-phase; no transcript text in logs |

### Known Threat Patterns for this stack

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Transcript exfil via bucket misconfig | Information Disclosure | PAB + SSE + no public policy + write-only voice role |
| Prompt-injected utterance rendered in admin ("stored XSS via speech") | Tampering/Elevation | React auto-escaping; never `dangerouslySetInnerHTML` for transcript text |
| Raw access code leakage | Information Disclosure | Only salted HMAC persisted; never log code; anon `sub` values must NOT be written verbatim into records (they contain the code) — store hashed code + a redacted subject |
| Ledger poisoning / spoofed rows | Tampering | Only the voice task role can PutObject; auth role is read-only; no delete grants anywhere (append-only posture) |
| CloudWatch as accidental second ledger | Information Disclosure | Log record COUNTS and keys, never `text` values |

## Sources

### Primary (HIGH confidence — repo + installed source, tool-verified)
- `apps/voice/src/klanker_voice/{call_runtime,pipeline,server,session,quota,auth,duplex,observers,rtvi}.py` — all file:line cites above
- `apps/voice/src/klanker_voice/telephony/{controller,gate,transport}.py`
- installed `pipecat-ai 1.5.0` source: `processors/aggregators/llm_response_universal.py`,
  `transports/base_output.py`, `processors/frameworks/rtvi/observer.py`,
  `observers/turn_tracking_observer.py`
- `apps/auth/webapp/src/{config/oidc.ts,lib/bypass-token.ts,entities/*.ts,app/tel/[e164]/route.ts}`
- `infra/terraform/live/site/{services/voice/service.hcl,services/auth/service.hcl,SECRETS.md}`,
  `region/us-east-1/*`, `modules/*`
- `.planning/{ROADMAP.md,STATE.md}`, Phase 05.1 phase dir (empty), 15-CONTEXT.md, source PRD todo

### Secondary (MEDIUM confidence — web, cross-checked against installed source)
- [Pipecat Turn Events docs](https://docs.pipecat.ai/api-reference/server/utilities/turn-management/turn-events)
- [pipecat#3762 — on_assistant_turn_stopped double-fire](https://github.com/pipecat-ai/pipecat/issues/3762)
- [pipecat#3564 — turn events on task cancellation](https://github.com/pipecat-ai/pipecat/issues/3564)
- [Athena partitions + partition projection](https://docs.aws.amazon.com/athena/latest/ug/partitions.html)
- [Athena partition projection JSON example](https://docs.aws.amazon.com/athena/latest/ug/create-cloudfront-table-partition-json.html)

### Tertiary (LOW confidence)
- S3 Select deprecation status (not load-bearing — see A1)

## Metadata

**Confidence breakdown:**
- Tap points / frame semantics: HIGH — read directly from installed 1.5.0 source + repo pipeline topology
- Identity plumbing: HIGH on what exists; the email/code-claim *recommendation* is a design choice needing user sign-off (A3/A4)
- Batch writer / shutdown: HIGH — repo precedents cited; SIGTERM path reasoned from Dockerfile + ECS defaults
- Terraform shape: HIGH on conventions; MEDIUM on SCP behavior for S3 (A2)
- Admin view: HIGH on the missing-/admin finding; recommendation (direct S3 read) is well-supported at this scale
- Pitfalls: HIGH — each anchored to code or an upstream issue

**Research date:** 2026-07-12
**Valid until:** ~2026-08-12 (stable pinned stack; re-verify pipecat event behavior if the pin moves off 1.5.0)
