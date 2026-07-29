# RICK clip sources (quick task 260729-rck)

Drop raw audio here (mp3/wav/m4a — anything ffmpeg reads), then run
`make -C apps/voice rick-audio` to normalize into `../rick/` as 8kHz mono
s16 wavs (the only format the telephony playback loop accepts).

Everything in this directory except this README is **gitignored** — audio
never enters this public repository. Deployed clips travel via PRIVATE S3
instead: see `../rick/README.md` for the upload + boot-sync flow.
