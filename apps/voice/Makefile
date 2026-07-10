# apps/voice — developer targets

.PHONY: env greetings ambience knowledge

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
