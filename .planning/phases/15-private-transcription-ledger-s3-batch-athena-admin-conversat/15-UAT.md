---
status: testing
phase: 15-private-transcription-ledger-s3-batch-athena-admin-conversat
source: [15-VERIFICATION.md]
started: 2026-07-13T13:45:00Z
updated: 2026-07-13T13:45:00Z
---

## Current Test

number: 1
name: Live end-to-end capture + threaded /admin read
expected: |
  After the phase-15 voice/auth images deploy, a real voice.klankermaker.ai
  session's turns (user STT + assistant replies) appear under one session_id in
  /use1/admin/transcripts, ordered by turn_seq, alternating user/assistant
  bubbles, with a non-null code_hash (or caller_id/did for PSTN) and no raw
  access code visible.
awaiting: user response

## Tests

### 1. Live end-to-end capture + threaded /admin read
expected: After merging phase 15 to main (CI builds + deploys new voice/auth container images), hold a real voice.klankermaker.ai session, then open https://auth.klankermaker.ai/use1/admin/transcripts as an ADMIN_EMAILS operator (whereiskurt@gmail.com) and confirm that session appears as a threaded, turn-ordered, alternating-bubble conversation grouped under one session_id, with a non-null code_hash (or caller_id/did for PSTN) and no raw access code visible.
result: [pending]

### 2. Recording-notice + transcript-view visual legibility
expected: The "Sessions may be recorded for quality and demo purposes." notice is legible on mobile + desktop; the threaded conversation view is readable and correctly distinguishes user vs. assistant turns.
result: [pending]

## Summary

total: 2
passed: 0
issues: 0
pending: 2
skipped: 0
blocked: 0

## Gaps

_None recorded yet. Both pending items are gated on the phase-15 application-image deploy (infra is already live), not on code defects._
