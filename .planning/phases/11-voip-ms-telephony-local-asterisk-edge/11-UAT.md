---
status: testing
phase: 11-voip-ms-telephony-local-asterisk-edge
source: [11-VERIFICATION.md]
started: 2026-07-12T12:50:01Z
updated: 2026-07-12T12:50:01Z
---

## Current Test

number: 1
name: §19-C live softphone proof — full gated conversation through the local Asterisk edge
expected: |
  A local SIP softphone call reaches the Stasis app, stays SILENT through the §24 gate,
  unlocks via DTMF PIN or a spoken order-independent 4-word passphrase, hears the greeting
  (NOT clipped), holds a multi-turn conversation, can be interrupted (barge-in) mid-response,
  and hangs up cleanly with NO leaked Asterisk resources (bridge + external-media channel +
  RTP socket all torn down). Fail-closed: a call that stays silent past the gate window gets
  a static goodbye + clean hangup, no open call.
awaiting: user response

## Tests

### 1. §19-C live softphone proof — full gated conversation through the local Asterisk edge
expected: |
  Follow `apps/voice/asterisk/README.md` → "Manual §19-C softphone proof" (8-step recipe):
  1. `cp apps/voice/asterisk/.env.example apps/voice/asterisk/.env`, fill ARI + SIP + PIN/passphrase
     values; ensure provider keys are in the voice `.env` (`make -C apps/voice env`).
  2. `cd apps/voice/asterisk && docker compose up`; confirm the Stasis app is registered
     (`docker exec klanker-asterisk-dev asterisk -rx 'core show application Stasis'`).
  3. `cd apps/voice && KLANKER_PIPELINE_CONFIG=configs/telephony.toml ASTERISK_ARI_URL=... \
     ASTERISK_ARI_USERNAME=klanker ASTERISK_ARI_PASSWORD=... TELEPHONY_ACCESS_PIN=... \
     TELEPHONY_PASSPHRASE_WORDS='w1 w2 w3 w4' uv run python -m klanker_voice.telephony.controller`.
     (Resolve the ARI loopback-vs-published-port note in the README first — run the controller
     inside the compose network or adjust the ARI bind.)
  4. Register a SIP softphone (Linphone/baresip) as `dev-softphone` and call the inbound extension.
  5. Confirm: line is SILENT on answer; unlock via the 4 passphrase words OR the DTMF PIN;
     agent GREETS (not clipped), holds a multi-turn conversation, can be INTERRUPTED (barge-in),
     hangs up cleanly.
  6. Confirm fail-closed: a second call, stay silent past `gate_window_seconds` → static
     goodbye + clean hangup, no open call.
  7. Confirm no leaks: controller logs an exactly-once close; no bridge/registry entry survives.
result: [pending]

## Summary

total: 1
passed: 0
issues: 0
pending: 1
skipped: 0
blocked: 0

## Gaps
