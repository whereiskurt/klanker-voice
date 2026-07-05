"""Anthropic judge factory for eval scenarios (keeps the three-key constraint).

Scenario YAMLs reference this via the ``judge.eval.factory`` hook::

    judge:
      modality: audio
      transcription: { service: moonshine }
      eval: { factory: "klanker_voice.harness.judge.judge_factory" }

``EvalJudge.from_config`` imports the dotted path and calls ``factory(config)``;
the returned service only needs ``run_inference()`` — verified present on
``AnthropicLLMService`` in the pinned 1.5.0 source. Using Anthropic here means
no Ollama install and no fourth vendor key (PIPE-07, RESEARCH Alternatives).
"""

from __future__ import annotations

import os
from pathlib import Path

from dotenv import load_dotenv

from pipecat.services.anthropic.llm import AnthropicLLMService

#: Judge model: fastest/cheapest Anthropic tier — verdicts are one-shot JSON.
DEFAULT_JUDGE_MODEL = "claude-haiku-4-5"

#: apps/voice/.env — written by `make -C apps/voice env`. Loaded here because
#: `pipecat eval run` is a separate process that never touches bot.py's
#: load_dotenv().
_APP_ENV_FILE = Path(__file__).resolve().parents[3] / ".env"


def judge_factory(config: dict | None = None) -> AnthropicLLMService:
    """Build the eval-judge LLM service from the scenario's ``judge.eval`` block.

    Args:
        config: The ``judge.eval:`` mapping from the scenario YAML (the
            harness passes it through). Honors an optional ``model`` override;
            everything else uses defaults.

    Returns:
        An ``AnthropicLLMService`` ready for out-of-pipeline ``run_inference``.

    Raises:
        RuntimeError: when ANTHROPIC_API_KEY is missing from the environment.
    """
    if _APP_ENV_FILE.is_file():
        load_dotenv(_APP_ENV_FILE, override=False)
    api_key = os.environ.get("ANTHROPIC_API_KEY")
    if not api_key:
        raise RuntimeError(
            "ANTHROPIC_API_KEY is not set. Run `make -C apps/voice env` to write "
            ".env from SSM before running eval scenarios."
        )
    model = str((config or {}).get("model") or DEFAULT_JUDGE_MODEL)
    return AnthropicLLMService(
        api_key=api_key,
        settings=AnthropicLLMService.Settings(model=model),
    )
