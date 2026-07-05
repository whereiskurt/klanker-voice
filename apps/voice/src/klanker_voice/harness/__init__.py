"""Latency harness (plan 01-03): report schema v1, CLI, and judge factory.

The JSON report schema produced here is a stability contract — the Phase 5
HUD and the eventual CI gate consume it. Stage names never change:
``vad_stop``, ``stt_final``, ``llm_ttft``, ``tts_first_audio``,
``voice_to_voice``.

The judge factory lives in :mod:`klanker_voice.harness.judge` and is
referenced by scenario YAMLs via the dotted path
``klanker_voice.harness.judge.judge_factory`` (kept out of this package
root so ``python -m klanker_voice.harness`` never imports LLM services).
"""

from klanker_voice.harness.report import (
    CEILING_P95_MS,
    SCHEMA_VERSION,
    STAGE_NAMES,
    TARGET_P50_MS,
    Report,
    TurnRecord,
    build_anchors,
    percentile,
)

__all__ = [
    "CEILING_P95_MS",
    "SCHEMA_VERSION",
    "STAGE_NAMES",
    "TARGET_P50_MS",
    "Report",
    "TurnRecord",
    "build_anchors",
    "percentile",
]
