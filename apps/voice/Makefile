# apps/voice — developer targets

.PHONY: env greetings

## env: fetch the three provider API keys from SSM and write .env (D-10)
env:
	bash scripts/bootstrap_env.sh

## greetings: Render the KPH greeting clips from the configured pipeline.toml voice
greetings:
	.venv/bin/python scripts/render_greetings.py
