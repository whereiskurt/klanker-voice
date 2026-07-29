# RICK phone-game clips (quick task 260729-rck)

Local mirror directory for the RICK code's playback clips on the
855-916-INFO toll-free line. The telephony playback loop
(`controller._gate_audio_announcement`) shuffles every `*.wav` HERE over
the call, capped at the entry's `max_play_seconds` (see
`configs/telephony.toml`, Game 5).

**Audio is never committed to this public repository** (`.gitignore`
blocks `*.wav` here) — clips may be material the operator can play over a
phone line but not publish. The deployed task mirrors them at boot from
PRIVATE S3 (`telephony/audio_sync.py`):

    s3://$KMV_LEDGER_BUCKET/media/telephony/rick/*.wav

Operator flow:

1. Drop sources (mp3/wav/m4a — anything ffmpeg reads) in `../rick-src/`
   (also gitignored).
2. `make -C apps/voice rick-audio` → normalizes into here as **8kHz mono
   s16 wav** (the only format the loop accepts; anything else is skipped
   with a warning at call time).
3. Upload: `aws s3 cp assets/telephony/rick/ s3://$KMV_LEDGER_BUCKET/media/telephony/rick/ --recursive --exclude "*" --include "*.wav"`
4. Restart the telephony-edge task — clips are data, not code; no image
   rebuild needed.

An empty directory is deploy-safe: the RICK code answers and immediately
hangs up until clips exist.
