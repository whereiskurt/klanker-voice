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

**Record shape (one row per turn — BOTH sides of the conversation):**
- `role` — "user" or "assistant" (the concierge). **Store both sides** (user decision
  2026-07-06: "both sides of the text convo").
- `text` — the utterance text (final STT for the user turn; spoken reply for assistant)
- `email` — the authenticated user's email (from the JWT / session)
- `ts` — timestamp / time of day (UTC epoch + local)
- `session_id` — groups one conversation together
- `turn_seq` — monotonic per-session turn index so the back-and-forth reconstructs in
  the exact order it happened (don't rely on `ts` alone — turns can share a second)
- `code_hash` — salted hash of the access code used (NOT the raw code)
- (candidate) `tier_id`

**Primary UX (user, 2026-07-06):** "I want to easily see every back and forth like a
convo." The point isn't a flat event log — it's to read each session as a threaded
chat transcript. So the Athena query / admin report MUST group by `session_id` and
order by `turn_seq` (fallback `ts`), rendering alternating user/assistant bubbles.
This is the acceptance bar for the report view.

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

## Resolved (user, 2026-07-06)

1. **Both sides stored** — record user turns AND the concierge's replies (see `role`).
2. **Format: newline-JSON** — "json is fine, no scaling concerns." No Parquet; ≤25 users.
3. **Conversation view is the goal** — the report reads like a chat, grouped by session,
   ordered by turn (see Primary UX above). This is the acceptance bar.
4. **Placement leaning approved** — its own phase OR fold into Phase 7 (recorded-transcript
   design already sketched), with the admin-report view as a slice of Phase 05.1. Final
   pick still made at scheduling time, but the leaning is endorsed.

## Open Questions

- Relationship to the existing `kmv-voice-usage` DynamoDB table — keep quota there,
  transcripts in S3 (different access pattern), don't co-mingle. (Design detail for
  planning, not a blocker.)
- Where in the pipeline to tap both turns: user text from the STT/transcription frame,
  assistant text from the LLM/TTS output frame — both carry `session_id`. Confirm the
  exact Pipecat frames/observers at implementation time (`observers.py`).
