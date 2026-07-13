# Phase 15: Private transcription ledger — S3 batch + Athena + admin conversation view - Context

**Gathered:** 2026-07-12
**Status:** Ready for planning
**Source:** PRD Express Path (.planning/todos/pending/2026-07-06-private-transcription-ledger-s3-batch-athena.md)

<domain>
## Phase Boundary

Every conversation turn — the user's final STT text AND the concierge's spoken replies —
lands in a private, append-only S3 ledger, and the operator can read any session back as
a threaded chat (session-grouped, turn-ordered, alternating user/assistant bubbles) via
Athena and the existing gated /admin report in the auth app. Today NOTHING is persisted:
turns stream through the Pipecat pipeline and are discarded; only quota bookkeeping exists.

In scope: pipeline tap + buffered batch writer in the voice service, private S3 bucket +
Athena table + task-role IAM in terraform, "sessions may be recorded" client notice, and
the /admin conversation-view report. Telephony sessions (Phase 12 `call_runtime.py`) flow
through the same agent and MUST also be captured.

Out of scope: audio recording (text only), analytics dashboards beyond the conversation
view + basic ad-hoc Athena queries, retention automation beyond a simple lifecycle rule.

</domain>

<decisions>
## Implementation Decisions

### Record shape (LOCKED — user decisions 2026-07-06)
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

### Storage & format (LOCKED)
- Newline-JSON ("json is fine, no scaling concerns" — ≤25 users). NOT Parquet.
- Private S3 bucket: SSE, all public access blocked, date-partitioned keys
  (`s3://<bucket>/ledger/dt=YYYY-MM-DD/…`)
- Athena external table over the bucket for ad-hoc queries ("all phrases by email",
  "per-day volume", "by code_hash")
- Voice service buffers and batches: flush every ~2–5 min OR on session end OR at N
  records — no per-utterance S3 PUTs
- Quota stays in the `kmv-voice-usage` DynamoDB table; transcripts live ONLY in S3.
  No co-mingling — different access patterns.

### Primary UX / acceptance bar (LOCKED)
- "I want to easily see every back and forth like a convo" — the report is NOT a flat
  event log. The Athena query / admin report MUST group by `session_id`, order by
  `turn_seq` (fallback `ts`), and render alternating user/assistant chat bubbles.
- Surface as a report in the existing gated /admin area of the auth app (Phase 05.1).

### Privacy posture (LOCKED — user ruling 2026-07-06)
- "There are no expectations of privacy, so it's all good." Public demo.
- Pair with a visible "sessions may be recorded" notice in the client — the notice is
  what establishes the no-expectation-of-privacy posture. Ship it in this phase.
- This REVERSES Phase 05.1's "operational-only, no transcripts" deferral.

### Infra conventions (LOCKED — project standards)
- Terraform/terragrunt matching existing `infra/terraform/live/site/services/voice/`
  conventions; SOPS→SSM for any secrets (e.g. the code-hash salt)
- Voice task role gets least-privilege `s3:PutObject` scoped to the ledger prefix

### Post-research resolutions (LOCKED — user decisions 2026-07-12)
- **/admin bootstrap:** Phase 05.1 was never executed; /admin does not exist. THIS phase
  ships a minimal `ADMIN_EMAILS`-gated /admin shell in the auth app, with the transcript
  conversation view as its first report. Phase 05.1 later grows it (users/usage reports).
- **Token claims:** auth app adds namespaced email + access-code claims to the access
  token via `extraTokenClaims` (profile already fetched there). Voice service computes
  `code_hash` as HMAC-SHA256 with an SSM-stored salt — uniform across magic-link,
  bypass (`anon:<code>:<uuid>` subs), and PSTN paths.
- **PSTN identity:** separate `caller_id` column carrying the E.164 number (and DID);
  `email` stays null for phone sessions. No `tel:` overloading of the email field.

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

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Source todo (the PRD for this phase)
- `.planning/todos/pending/2026-07-06-private-transcription-ledger-s3-batch-athena.md` — full record shape, backend sketch, resolved decisions

### Voice service tap points
- `apps/voice/src/klanker_voice/observers.py` — existing pipeline observers (where turn taps likely hook)
- `apps/voice/src/klanker_voice/session.py` — session lifecycle (flush-on-end hook)
- `apps/voice/src/klanker_voice/pipeline.py` — pipeline assembly (frame flow, where STT/LLM/TTS frames pass)
- `apps/voice/src/klanker_voice/duplex.py` — voice2 full-duplex variant (must be tapped too; it is the prod default)
- `apps/voice/src/klanker_voice/auth.py` — JWT claims (email, tier, code) available per session
- `apps/voice/src/klanker_voice/call_runtime.py` — Phase 12 telephony runtime (PSTN sessions must also land in the ledger)
- `apps/voice/src/klanker_voice/quota.py` — existing DynamoDB usage pattern (what NOT to co-mingle with; also the existing aioboto3/boto usage pattern to follow)

### Infra
- `infra/terraform/live/site/services/voice/` — voice service terraform (task role, SSM wiring — add S3/Athena here per conventions)
- `infra/terraform/live/site/services/auth/` — auth app terraform (admin report may need Athena read access)

### Admin report
- `apps/auth/webapp/` — Next.js auth app hosting the gated /admin area (Phase 05.1) where the conversation view lives

### Design spec
- `docs/superpowers/specs/2026-07-04-klanker-voice-design.md` — authoritative system design (naming, budget, security posture)

</canonical_refs>

<specifics>
## Specific Ideas

- Report drill-in: sessions list → click a session → threaded chat transcript with
  alternating bubbles (user right/assistant left or similar) — modeled on how the user
  described it: "easily see every back and forth like a convo"
- Athena example queries worth shipping in docs/runbook: all phrases by email, per-day
  volume, phrases by code_hash
- Batch object naming should sort chronologically within a partition
- `kv` CLI is the operator tool family — a `kv transcripts` subcommand is a nice-to-have,
  NOT required for this phase (the /admin view is the acceptance bar)

</specifics>

<deferred>
## Deferred Ideas

- Audio recording (ledger is text-only)
- Parquet/compaction, Glue crawlers, partition projection tuning — overkill at ≤25 users
- Retention/deletion tooling beyond a simple S3 lifecycle rule
- `kv transcripts` CLI subcommand (nice-to-have; /admin report is the acceptance bar)
- Full-text search over transcripts

</deferred>

---

*Phase: 15-private-transcription-ledger-s3-batch-athena-admin-conversat*
*Context gathered: 2026-07-12 via PRD Express Path*
