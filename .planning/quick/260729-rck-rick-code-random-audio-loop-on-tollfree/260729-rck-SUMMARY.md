---
phase: quick-260729-rck
plan: 01
subsystem: telephony
tags: [toml, pipecat, audio, s3, terraform, ecs-secrets, ffmpeg]
requires:
  - phase: quick-260728-tfn
    provides: the 855-916-INFO toll-free game DID (KVD1800), live + seeded
provides:
  - A second armed code on 855-916-INFO -- 7425 (RICK) -- playing a continuous shuffle of private clips, capped at 180s
  - The audio-playback announcement action type (AnnouncementEntry.audio_dir + max_play_seconds, mutually exclusive with otp_url/line_template)
  - pickup_cue.play_audio_clip (generic barge-in-safe clip injection) + controller._gate_audio_announcement + _load_audio_clips
  - Boot-time private-S3 clip sync (telephony/audio_sync.py; s3://$KMV_LEDGER_BUCKET/media/telephony/ -> assets/telephony/; task-role TelephonyMediaRead/List)
  - make rick-audio normalization target + gitignored rick/rick-src asset dirs
  - SSM announcement_code_rick=7425 seeded + readback-verified; rickrollfull.wav (212s, 8kHz mono s16) uploaded to the private media prefix
affects: [telephony-edge, kv-cli, pr-85]
key-decisions:
  - "Clips NEVER enter the public repo (copyright): .gitignore blocks them; they live under the PRIVATE ledger bucket's media/telephony/ prefix and audio_sync.py mirrors them at boot, reusing KMV_LEDGER_BUCKET (no new terragrunt dependency). Clip updates are upload + task restart -- no image rebuild."
  - "Playback reuses the pickup-cue OutputAudioRawFrame seam (pre-rendered, interruptible, never labeled TTS) -- zero billed API calls, D-05d trivially preserved"
  - "Continuous shuffle, no immediate repeats with 2+ clips, hard 180s cap (toll-free spend bound); each code is its own call (operator-confirmed)"
  - "Runtime accepts 16-bit mono wav only; make rick-audio (ffmpeg) normalizes any source format; everything unreadable degrades with a warning, empty dir = immediate hangup (deploy-safe)"
  - "Dispatch-site outcome telemetry untouched: RICK calls record outcome=announcement_code via the existing _record_outcome"
requirements-completed: [QUICK-260729-RCK]
coverage:
  - id: D1
    description: "audio_dir/max_play_seconds parse + mutual exclusion + bounds; OTP entries byte-identical"
    verification:
      - {kind: unit, ref: "tests/test_telephony_config.py (audio-dir suite; 71 passed)", status: pass}
    human_judgment: false
  - id: D2
    description: "shuffle playback until cap then single teardown; empty dir silent teardown; never OTP-fetch/speak_goodbye/quota/greet; loader skips unplayable files"
    verification:
      - {kind: unit, ref: "tests/test_telephony_controller.py (audio announcement suite; telephony suites 302 passed)", status: pass}
    human_judgment: false
  - id: D3
    description: "S3 sync mirrors wav keys traversal-guarded, silent no-op without bucket env, never raises"
    verification:
      - {kind: unit, ref: "tests/test_telephony_audio_sync.py (3 passed)", status: pass}
    human_judgment: false
  - id: D4
    description: "Shipped-config guards (py+go) expect 5 entries incl. the RICK shape; kv suites green"
    verification:
      - {kind: unit, ref: "go test ./internal/app/studio/... ./internal/app/cmd/... (ok)", status: pass}
    human_judgment: false
  - id: D5
    description: "Live call: dial 855-916-INFO, enter 7425, hear the shuffle (after PR #85 merge + deploy)"
    verification: []
    human_judgment: true
    rationale: "Requires the merged deploy and a real dial-in."
duration: ~40min
completed: 2026-07-29
status: complete
---

# Quick Task 260729-rck: RICK code — random audio loop on the toll-free line

855-916-INFO now has two personalities behind one gate: **5678 (LOST)** the
OTP gag, **7425 (RICK)** a continuous shuffle of private clips (first clip:
the full rickroll, 212s source, capped at 180s of play).

## Task Commits

1. `audio_dir`/`max_play_seconds` config action type — (feat)
2. `_gate_audio_announcement` shuffle loop + `play_audio_clip` seam — (feat)
3. RICK toml entry + assets pipeline + `CTF_ANNOUNCEMENT_CODE_RICK` wiring + 5-entry shipped guards — (feat)
4. Private-S3 boot sync + gitignored audio + `TelephonyMediaRead/List` IAM — (feat)

## Live steps DONE (2026-07-29)

- ✅ `announcement_code_rick=7425` seeded + readback-verified (distinct from 5678/333266/1337/696969)
- ✅ `rickrollfull.mp3` normalized (8kHz mono s16, 212s) and uploaded to `s3://kmv-ledger-use1-adba57e4419be01f/media/telephony/rick/rickrollfull.wav` — NOT committed to the repo (public; copyright)

## Remaining

Same merge+deploy choreography as PR #85 (nothing new): terragrunt apply
picks up the RICK secret + media IAM, redeploy telephony-edge (re-pin
image), then live-verify both codes on one line each in its own call.
