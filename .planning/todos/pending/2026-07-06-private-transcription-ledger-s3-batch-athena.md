---
created: 2026-07-06T21:53:40.622Z
title: Private transcription ledger — S3 batch + Athena
area: voice
files:
  - apps/voice/src/klanker_voice/session.py
  - apps/voice/src/klanker_voice/observers.py
  - apps/voice/src/klanker_voice/auth.py
  - infra/terraform/live/site/services/voice/
  - apps/auth/webapp/ (admin report)
---

## Problem

The operator wants a private, append-only ledger of every user utterance for review
and analytics. Today **no transcript is persisted anywhere** — STT/LLM/TTS turns stream
through the Pipecat pipeline and are discarded; only quota bookkeeping is stored. This
was explicitly deferred earlier ("operational-only; transcripts later") but is now a
green-lit requirement.

**Privacy ruling (user, 2026-07-06):** "There are no expectations of privacy, so it's
all good." Public demo at voice.klankermaker.ai; pair with a visible "sessions may be
recorded" notice in the client, which is what establishes the no-expectation-of-privacy
posture. Not a blocker — recorded here as the decision.

## Solution

Capture-and-land pipeline (TBD on exact placement — see Open Questions):

**Record shape (one row per user utterance):**
- `transcript` — the phrase the user said (final STT text)
- `email` — the authenticated user's email (from the JWT / session)
- `ts` — timestamp / time of day (UTC epoch + local)
- `code_hash` — hash of the access code used (e.g. SHA-256, NOT the raw code)
- (candidates) `session_id`, `tier_id`, maybe the agent's reply text

**Backend style (as sketched by user):**
1. Voice service emits each utterance record as it's transcribed (hook near the STT
   observer / SessionLifecycle in `observers.py` / `session.py`).
2. Buffer + batch every ~2-5 min (or on session end / N records), write newline-JSON
   (or Parquet) objects to an **S3 bucket**, partitioned by date
   (`s3://<bucket>/ledger/dt=YYYY-MM-DD/…`).
3. **Athena** external table over the bucket ties it together for ad-hoc queries
   ("all phrases by email", "per-day volume", "by code_hash").
4. Optional: surface as an **admin portal report** (ties into Phase 05.1 `/admin`).

**Infra to add:** private S3 bucket (SSE, no public access, lifecycle/retention),
Athena table + workgroup, voice task-role IAM `s3:PutObject` to the ledger prefix.
Follows the existing SOPS→SSM / least-privilege task-role conventions.

**Hashing note:** `code_hash` should be a stable salted hash so the same code groups
together across rows without storing the plaintext code.

## Open Questions

1. **Placement** — does this belong in Phase 7 (KPH Knowledge Base, which already has a
   recorded-transcript design in 07-DESIGN-NOTES.md), a **new dedicated phase**, or an
   extension of Phase 05.1's admin report? Leaning: its own phase or fold into Phase 7,
   with the admin-report view as a slice of Phase 05.1. Decide at planning time.
2. **Format** — newline-JSON (simplest) vs Parquet (cheaper Athena scans at scale). For
   ≤25 users, JSON is fine; note the upgrade path.
3. **Reply text** — store the agent's spoken reply alongside the user utterance, or user
   phrases only? User asked for "every phrase the user asked" — default to user-only,
   confirm if the concierge's replies are wanted too.
4. Relationship to the existing `kmv-voice-usage` DynamoDB table — keep quota there,
   transcripts in S3 (different access pattern), don't co-mingle.
