"""(kind, provider) -> builder registry for pipeline services (PIPE-04).

All construction goes through pipecat 1.5.0 ``Settings`` objects — bare
constructor kwargs are deprecated and removed in 2.0. API keys are read from
the environment at build time only; never log constructor args or dump
Settings objects (T-1-03).

Turn-strategy matrix (RESEARCH Pattern 4):

- arm ``vad_timeout``:   Silero VAD + EXPLICIT SpeechTimeoutUserTurnStopStrategy.
  Explicit because the 1.5.0 default stop strategy is SmartTurn v3 — omitting
  strategies would silently measure the wrong arm (Pitfall 2).
- arm ``smart_turn_v3``: Silero VAD + explicit TurnAnalyzerUserTurnStopStrategy
  (matches the default, set anyway so the config is self-documenting).
- Flux arm (stt.provider = deepgram-flux): NEVER set ``user_turn_strategies``
  and never set ``vad_analyzer`` — Flux auto-installs ExternalUserTurnStrategies
  via its service metadata; passing our own silently re-enables local turn
  detection: double endpointing, double interruptions (Pitfall 3). Enforced in
  code below so misconfiguration is impossible.
"""

from __future__ import annotations

import os
from typing import Callable

from pipecat.audio.turn.smart_turn.local_smart_turn_v3 import LocalSmartTurnAnalyzerV3
from pipecat.audio.vad.silero import SileroVADAnalyzer
from pipecat.audio.vad.vad_analyzer import VADParams
from pipecat.processors.aggregators.llm_response_universal import LLMUserAggregatorParams
from pipecat.services.anthropic.llm import AnthropicLLMService
from pipecat.services.deepgram.flux.stt import DeepgramFluxSTTService
from pipecat.services.deepgram.stt import DeepgramSTTService
from pipecat.services.elevenlabs.tts import ElevenLabsTTSService
from pipecat.turns.user_stop.speech_timeout_user_turn_stop_strategy import (
    SpeechTimeoutUserTurnStopStrategy,
)
from pipecat.turns.user_stop.turn_analyzer_user_turn_stop_strategy import (
    TurnAnalyzerUserTurnStopStrategy,
)
from pipecat.turns.user_turn_strategies import UserTurnStrategies

from klanker_voice.config import PipelineConfig
from klanker_voice.pronunciation import PronunciationTextFilter

FLUX_PROVIDER = "deepgram-flux"

#: Interim ElevenLabs voice used while pipeline.toml's voice_id is "" (the
#: D-02 three-voice audition in plan 01-05 lands the real one). The ElevenLabs
#: WS API REJECTS a null voice_id outright (1008 policy violation, "A voice
#: with voice_id None does not exist") — there is no server-side default, so
#: an explicit premade voice is required for TTS to speak at all. Verified
#: live against this account's key (premade "Rachel", available on every
#: ElevenLabs account).
INTERIM_ELEVENLABS_VOICE_ID = "21m00Tcm4TlvDq8ikWAM"


def _require_env(name: str) -> str:
    value = os.environ.get(name)
    if not value:
        raise RuntimeError(
            f"{name} is not set. Run `make -C apps/voice env` to write .env from SSM."
        )
    return value


# ---------------------------------------------------------------------------
# Service builders — one per (kind, provider)
# ---------------------------------------------------------------------------


def _build_stt_deepgram_nova3(cfg: PipelineConfig) -> DeepgramSTTService:
    return DeepgramSTTService(
        api_key=_require_env("DEEPGRAM_API_KEY"),
        settings=DeepgramSTTService.Settings(model=cfg.stt.model),
    )


def _build_stt_deepgram_flux(cfg: PipelineConfig) -> DeepgramFluxSTTService:
    settings_kwargs: dict = {
        "model": cfg.stt.model or "flux-general-en",
        "eot_threshold": cfg.stt.flux.eot_threshold,
    }
    # eager EOT is a spend-for-latency lever (Pitfall 4) — only pass when nonzero.
    if cfg.stt.flux.eager_eot_threshold:
        settings_kwargs["eager_eot_threshold"] = cfg.stt.flux.eager_eot_threshold
    return DeepgramFluxSTTService(
        api_key=_require_env("DEEPGRAM_API_KEY"),
        settings=DeepgramFluxSTTService.Settings(**settings_kwargs),
    )


def _build_llm_anthropic(cfg: PipelineConfig) -> AnthropicLLMService:
    # The 1.5.0 source default is a Sonnet model — the config override
    # (claude-haiku-4-5) is mandatory, so always pass the model through.
    return AnthropicLLMService(
        api_key=_require_env("ANTHROPIC_API_KEY"),
        settings=AnthropicLLMService.Settings(model=cfg.llm.model),
    )


def _build_tts_elevenlabs(cfg: PipelineConfig) -> ElevenLabsTTSService:
    return ElevenLabsTTSService(
        api_key=_require_env("ELEVENLABS_API_KEY"),
        settings=ElevenLabsTTSService.Settings(
            model=cfg.tts.model,
            # voice_id is "" until the D-02 audition lands one in config;
            # None is NOT accepted by the ElevenLabs WS API (no default
            # voice), so fall back to the documented interim premade voice.
            voice=cfg.tts.voice_id or INTERIM_ELEVENLABS_VOICE_ID,
            speed=cfg.tts.speed,
            # 07.1 tunable energy (must match render_greetings.py so the
            # pre-rendered welcome clip and the live voice share one character).
            stability=cfg.tts.stability,
            similarity_boost=cfg.tts.similarity_boost,
            style=cfg.tts.style,
        ),
        # 07.1: respell klanker proper nouns for TTS only. pipecat applies
        # text_filters after aggregation / before synthesis, so captions
        # (built from the upstream LLMTextFrame) keep the natural spelling.
        text_filters=[PronunciationTextFilter()],
    )


#: Registry: (kind, provider) -> builder. Provider strings match config.py's
#: allowlists; adding a provider means one builder + one registry entry.
BUILDERS: dict[tuple[str, str], Callable] = {
    ("stt", "deepgram-nova3"): _build_stt_deepgram_nova3,
    ("stt", FLUX_PROVIDER): _build_stt_deepgram_flux,
    ("llm", "anthropic"): _build_llm_anthropic,
    ("tts", "elevenlabs"): _build_tts_elevenlabs,
}


def _build(kind: str, provider: str, cfg: PipelineConfig):
    try:
        builder = BUILDERS[(kind, provider)]
    except KeyError:
        raise ValueError(f"no builder registered for ({kind!r}, {provider!r})") from None
    return builder(cfg)


def build_stt(cfg: PipelineConfig):
    """Build the configured STT service."""
    return _build("stt", cfg.stt.provider, cfg)


def build_llm(cfg: PipelineConfig):
    """Build the configured LLM service."""
    return _build("llm", cfg.llm.provider, cfg)


def build_tts(cfg: PipelineConfig):
    """Build the configured TTS service."""
    return _build("tts", cfg.tts.provider, cfg)


# ---------------------------------------------------------------------------
# Turn-strategy matrix (RESEARCH Pattern 4)
# ---------------------------------------------------------------------------


def build_user_aggregator_params(
    cfg: PipelineConfig,
    *,
    user_turn_strategies: UserTurnStrategies | None = None,
) -> LLMUserAggregatorParams:
    """Build the arm-appropriate ``LLMUserAggregatorParams`` from config.

    Args:
        cfg: Parsed pipeline config.
        user_turn_strategies: Explicit strategy override for advanced callers.
            Forbidden when the STT provider is Deepgram Flux — Flux installs
            ``ExternalUserTurnStrategies`` itself, and a caller-supplied value
            would silently re-enable local turn detection (Pitfall 3).

    Raises:
        ValueError: If the config combines deepgram-flux with an explicit
            turn strategy (misconfiguration must be impossible, not a runtime
            surprise).
    """
    if cfg.stt.provider == FLUX_PROVIDER:
        if user_turn_strategies is not None:
            raise ValueError(
                "stt.provider=deepgram-flux forbids explicit user_turn_strategies: "
                "Flux installs ExternalUserTurnStrategies server-side; passing local "
                "strategies re-enables double endpointing and double interruptions."
            )
        # No vad_analyzer, no user_turn_strategies: Flux's service metadata
        # installs ExternalUserTurnStrategies on the aggregator at startup.
        return LLMUserAggregatorParams()

    if user_turn_strategies is not None:
        return LLMUserAggregatorParams(
            vad_analyzer=_build_vad(cfg),
            user_turn_strategies=user_turn_strategies,
        )

    if cfg.turn.strategy == "vad_timeout":
        # EXPLICIT strategies: the 1.5.0 default is SmartTurn v3; relying on the
        # default here would make the "plain VAD" A/B arm measure the wrong thing.
        strategies = UserTurnStrategies(
            stop=[
                SpeechTimeoutUserTurnStopStrategy(
                    user_speech_timeout=cfg.turn.user_speech_timeout
                )
            ]
        )
    elif cfg.turn.strategy == "smart_turn_v3":
        # Matches the 1.5.0 default, but set explicitly so config is self-documenting.
        strategies = UserTurnStrategies(
            stop=[TurnAnalyzerUserTurnStopStrategy(turn_analyzer=LocalSmartTurnAnalyzerV3())]
        )
    else:  # pragma: no cover — config.py allowlist makes this unreachable
        raise ValueError(f"unknown turn.strategy {cfg.turn.strategy!r}")

    return LLMUserAggregatorParams(
        vad_analyzer=_build_vad(cfg),
        user_turn_strategies=strategies,
    )


def _build_vad(cfg: PipelineConfig) -> SileroVADAnalyzer:
    return SileroVADAnalyzer(params=VADParams(stop_secs=cfg.turn.vad_stop_secs))
