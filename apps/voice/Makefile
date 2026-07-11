# apps/voice — developer targets

.PHONY: env greetings ambience knowledge say voice1-local voice2-local

## env: fetch the three provider API keys from SSM and write .env (D-10)
env:
	bash scripts/bootstrap_env.sh

## greetings: Render the KPH greeting clips from the configured pipeline.toml voice
greetings:
	.venv/bin/python scripts/render_greetings.py

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

## voice1-local: run the shipped half-duplex voice1 pipeline locally (mic UI at http://localhost:7860)
voice1-local:
	uv run python bot.py -t webrtc

## voice2-local: run the full-duplex voice2 pipeline locally (Flux + duplex emitter + greenhouse ambience) at http://localhost:7860
voice2-local:
	KLANKER_PIPELINE_CONFIG=configs/voice2.toml uv run python bot.py -t webrtc
