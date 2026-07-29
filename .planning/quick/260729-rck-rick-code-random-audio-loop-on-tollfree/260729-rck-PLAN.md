---
phase: quick-260729-rck
plan: 01
subsystem: telephony
tags: [toml, pipecat, audio, terraform, ecs-secrets, ffmpeg]
requires:
  - phase: quick-260728-tfn
    provides: the 855-916-INFO toll-free game DID (KVD1800), live + seeded
provides:
  - A second armed code on 855-916-INFO -- 7425 (RICK) -- that plays a continuous shuffle of operator-supplied audio clips over the call, capped at max_play_seconds then teardown
  - A new audio-playback announcement action type (AnnouncementEntry.audio_dir + max_play_seconds, mutually exclusive with otp_url/line_template)
  - assets/telephony/rick/ clip folder + `make rick-audio` ffmpeg normalization target (rick-src/ -> 8kHz mono 16-bit wav)
  - service.hcl CTF_ANNOUNCEMENT_CODE_RICK valueFrom + SSM seed announcement_code_rick=7425
affects: [telephony-edge, kv-cli, pr-85]
key-decisions:
  - "Playback reuses the pickup-cue seam: OutputAudioRawFrame batches queued via worker.queue_frames, bracketed by Bot(Started|Stopped)SpeakingFrame -- pre-rendered outbound-only audio, no TTS/LLM/quota (D-05d cost invariant preserved)"
  - "Continuous shuffle until max_play_seconds (default 180) then goodbye-less teardown; each code is its own call (operator-confirmed defaults)"
  - "Runtime loads .wav only (stdlib wave, never raises, empty dir degrades to immediate teardown); mp3 conversion happens at build time via `make rick-audio` (ffmpeg), keeping runtime dependency-free"
  - "Clips are tracked repo assets baked by the existing Dockerfile COPY . . -- NOTE for operator: the repo is public, commit only clips you can publish"
  - "Outcome telemetry unchanged: dispatch-site _record_outcome('announcement_code') covers the RICK entry automatically"
tasks:
  - id: 1
    name: "config: audio_dir + max_play_seconds fields, action-type validation"
    files: [apps/voice/src/klanker_voice/telephony/config.py, apps/voice/tests/test_telephony_config.py]
    verify: "cd apps/voice && uv run pytest tests/test_telephony_config.py -q"
  - id: 2
    name: "controller: _load_audio_clips + _gate_audio_announcement shuffle loop + dispatch branch"
    files: [apps/voice/src/klanker_voice/telephony/controller.py, apps/voice/tests/test_telephony_controller.py]
    verify: "cd apps/voice && uv run pytest tests/test_telephony_controller.py tests/test_telephony_gate.py tests/test_telephony_lifecycle.py tests/test_telephony_sms.py -q"
  - id: 3
    name: "RICK toml entry + assets folder + make target + service.hcl wiring + shipped-config test updates (py+go)"
    files: [apps/voice/configs/telephony.toml, apps/voice/assets/telephony/rick/README.md, apps/voice/Makefile, infra/terraform/live/site/services/telephony-edge/service.hcl, apps/voice/tests/test_telephony_config.py, kv/internal/app/studio/repofile_adapter_test.go]
    verify: "cd apps/voice && uv run pytest tests/ -q -k telephony; cd ../../kv && go test ./internal/app/studio/... ./internal/app/cmd/..."
  - id: 4
    name: "live: seed announcement_code_rick=7425 (readback), update PR #85 body"
    files: []
    verify: "aws ssm get-parameter readback == 7425"
---

# Quick Task 260729-rck: RICK code — random audio loop on the toll-free line

855-916-INFO gets a second personality: dial 5678 (LOST) for the OTP gag,
dial 7425 (RICK) for a continuous shuffle of whatever clips the operator
drops in `assets/telephony/rick/`, capped at 180s.
