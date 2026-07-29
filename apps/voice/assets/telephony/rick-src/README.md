# RICK clip sources (quick task 260729-rck)

Drop raw audio here (mp3/wav/m4a — anything ffmpeg reads), then run
`make -C apps/voice rick-audio` to normalize into `../rick/` as 8kHz mono
s16 wavs (the only format the telephony playback loop accepts).

Raw sources in this directory are NOT baked into the image and NOT played —
only the normalized `../rick/*.wav` files are. This repository is public —
commit only audio you have the right to publish (or keep sources local and
commit only the normalized clips you can).
