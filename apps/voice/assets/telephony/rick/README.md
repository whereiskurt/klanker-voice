# RICK phone-game clips (quick task 260729-rck)

Normalized playback clips for the RICK code on the 855-916-INFO toll-free
line. The telephony playback loop (`controller._gate_audio_announcement`)
shuffles every `*.wav` in THIS directory over the call, capped at the
entry's `max_play_seconds` (see `configs/telephony.toml`, Game 5).

**Do not put raw files here.** Drop sources (mp3/wav/m4a — anything ffmpeg
reads) in `../rick-src/` and run:

    make -C apps/voice rick-audio

which normalizes them into this directory as **8kHz mono s16 wav** — the
only format the loop accepts (anything else is skipped with a warning at
call time). Commit the normalized wavs; the Dockerfile's `COPY . .` bakes
them into the telephony-edge image on the next deploy.

An empty directory is deploy-safe: the RICK code answers and immediately
hangs up until clips land here.

**This repository is public — commit only clips you have the right to
publish.**
