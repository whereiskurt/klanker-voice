---
phase: quick-260729-gfr
plan: 01
subsystem: telephony
tags: [toml, pipecat, audio]
requires:
  - phase: quick-260729-rck
    provides: play_audio_clip seam + private-S3 clip sync (rick-roll-sound-effect.wav lands in assets/telephony/rick/ at boot)
provides:
  - "TelephonyConfig.gate_fail_audio: optional path to ONE wav played on gate-window expiry INSTEAD of the spoken 'Sorry, I wasn't able to verify access' goodbye, then hangup"
  - "Scope: ONLY reason='gate window expired' (failed/absent codes); mint-failure and quota-denied fail paths keep the spoken goodbye (those callers didn't fail a code)"
  - "Graceful degrade: unset knob, missing file, or unreadable wav -> byte-identical spoken-goodbye behavior; telemetry outcomes unchanged (recorded before the branch)"
  - "Shipped telephony.toml points gate_fail_audio at the short rickroll clip (S3-synced at boot)"
tasks:
  - id: 1
    name: "config knob + pickup_cue.load_wav_clip cached single-file loader + controller branch + toml + tests"
    files: [apps/voice/src/klanker_voice/telephony/config.py, apps/voice/src/klanker_voice/telephony/pickup_cue.py, apps/voice/src/klanker_voice/telephony/controller.py, apps/voice/configs/telephony.toml, apps/voice/tests/test_telephony_config.py, apps/voice/tests/test_telephony_controller.py]
    verify: "cd apps/voice && uv run pytest tests/ -q -k telephony"
  - id: 2
    name: "PR + merge + ride the same deploy train"
    files: []
    verify: "gh pr checks green; ECS revisions carry the merge SHA"
---

# Quick Task 260729-gfr: Failed gate codes get the short rickroll

Wrong code on any gated line? Instead of "Sorry, I wasn't able to verify
access on this line. Goodbye." — 9 seconds of Rick, then hangup.
