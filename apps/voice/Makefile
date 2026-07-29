# apps/voice — developer targets

.PHONY: env greetings pickup-cue ambience knowledge say rick-audio voice1-local voice2-local

## env: fetch the three provider API keys from SSM and write .env (D-10)
env:
	bash scripts/bootstrap_env.sh

## greetings: Render the KPH greeting clips from the configured pipeline.toml voice
greetings:
	.venv/bin/python scripts/render_greetings.py

## pickup-cue: Render the telephony pickup "hey" clip from the configured pipeline.toml voice.
## Re-run after any pipeline.toml voice_id change (mirrors the `greetings` drift guard, quick task 260713-m9n).
pickup-cue:
	.venv/bin/python scripts/render_pickup_cue.py

## ambience: Render the greenhouse coffee-shop ambient bed (ElevenLabs SFX -> mono 24kHz WAV)
## NOTE: the ElevenLabs API key must have the `sound_generation` permission enabled.
ambience:
	.venv/bin/python scripts/render_ambience.py

## knowledge: regenerate curated packs + retrieval indexes from the corpus (D-07)
knowledge:
	uv run python scripts/refresh_knowledge.py

## say: speak a text block through the configured KPH voice (deterministic voice-output test). Usage: make say TEXT="hello there"
say:
	.venv/bin/python scripts/say.py "$(TEXT)"

## rick-audio: normalize the RICK phone-game clips (quick task 260729-rck).
## Drop source files (mp3/wav/m4a/anything ffmpeg reads) in assets/telephony/rick-src/,
## run this, and commit the resulting assets/telephony/rick/*.wav (8kHz mono s16 --
## the only format the telephony playback loop accepts). Requires ffmpeg on PATH.
## NOTE: this repo is public -- commit only clips you can publish.
rick-audio:
	@mkdir -p assets/telephony/rick
	@found=0; for f in assets/telephony/rick-src/*; do \
		[ -f "$$f" ] || continue; \
		case "$$f" in *.md) continue;; esac; \
		found=1; \
		base=$$(basename "$$f"); out="assets/telephony/rick/$${base%.*}.wav"; \
		echo "normalizing $$base -> $$out"; \
		ffmpeg -hide_banner -loglevel error -y -i "$$f" -ac 1 -ar 8000 -sample_fmt s16 "$$out" || exit 1; \
	done; \
	if [ "$$found" = 0 ]; then echo "no source clips in assets/telephony/rick-src/ -- drop mp3/wavs there first"; fi

## voice1-local: run the shipped half-duplex voice1 pipeline locally (mic UI at http://localhost:7860)
voice1-local:
	uv run python bot.py -t webrtc

## voice2-local: run the full-duplex voice2 pipeline locally (Flux + duplex emitter + greenhouse ambience) at http://localhost:7860
voice2-local:
	KLANKER_PIPELINE_CONFIG=configs/voice2.toml uv run python bot.py -t webrtc
