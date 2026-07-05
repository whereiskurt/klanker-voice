"""Unit tests for the factory registry and turn-strategy matrix (PIPE-04).

Services are constructed with dummy api_key values from env-stubbed fixtures —
no network calls are made in unit tests.
"""

from __future__ import annotations

from pathlib import Path

import pytest

from pipecat.audio.vad.silero import SileroVADAnalyzer
from pipecat.turns.user_stop.speech_timeout_user_turn_stop_strategy import (
    SpeechTimeoutUserTurnStopStrategy,
)
from pipecat.turns.user_stop.turn_analyzer_user_turn_stop_strategy import (
    TurnAnalyzerUserTurnStopStrategy,
)
from pipecat.turns.user_turn_strategies import UserTurnStrategies

from klanker_voice.config import (
    FluxConfig,
    LlmConfig,
    PersonaConfig,
    PipelineConfig,
    SttConfig,
    TtsConfig,
    TurnConfig,
)
from klanker_voice.factories import (
    build_llm,
    build_stt,
    build_tts,
    build_user_aggregator_params,
)


def _cfg(
    *,
    stt_provider: str = "deepgram-nova3",
    stt_model: str = "nova-3-general",
    turn_strategy: str = "smart_turn_v3",
    voice_id: str = "test-voice-id",
    speed: float = 1.1,
) -> PipelineConfig:
    return PipelineConfig(
        stt=SttConfig(
            provider=stt_provider,
            model=stt_model,
            flux=FluxConfig(eot_threshold=0.7, eager_eot_threshold=0.0),
        ),
        turn=TurnConfig(strategy=turn_strategy, vad_stop_secs=0.2, user_speech_timeout=0.6),
        llm=LlmConfig(provider="anthropic", model="claude-haiku-4-5"),
        tts=TtsConfig(provider="elevenlabs", model="eleven_flash_v2_5", voice_id=voice_id, speed=speed),
        persona=PersonaConfig(prompt_path=Path(__file__)),  # any existing file
    )


class TestTurnStrategyMatrix:
    def test_vad_timeout_arm_sets_explicit_speech_timeout_and_no_turn_analyzer(self):
        params = build_user_aggregator_params(_cfg(turn_strategy="vad_timeout"))
        assert isinstance(params.vad_analyzer, SileroVADAnalyzer)
        stops = params.user_turn_strategies.stop
        assert any(isinstance(s, SpeechTimeoutUserTurnStopStrategy) for s in stops)
        assert not any(isinstance(s, TurnAnalyzerUserTurnStopStrategy) for s in stops)

    def test_smart_turn_v3_arm_sets_explicit_turn_analyzer_strategy(self):
        params = build_user_aggregator_params(_cfg(turn_strategy="smart_turn_v3"))
        assert isinstance(params.vad_analyzer, SileroVADAnalyzer)
        stops = params.user_turn_strategies.stop
        assert any(isinstance(s, TurnAnalyzerUserTurnStopStrategy) for s in stops)

    def test_flux_arm_sets_no_strategies_and_no_vad(self):
        params = build_user_aggregator_params(
            _cfg(stt_provider="deepgram-flux", stt_model="flux-general-en")
        )
        # Flux installs ExternalUserTurnStrategies itself; both must be unset here.
        assert params.user_turn_strategies is None
        assert params.vad_analyzer is None

    def test_flux_plus_explicit_strategy_raises(self):
        cfg = _cfg(stt_provider="deepgram-flux", stt_model="flux-general-en")
        with pytest.raises(ValueError, match="deepgram-flux forbids"):
            build_user_aggregator_params(
                cfg, user_turn_strategies=UserTurnStrategies()
            )


class TestServiceBuilders:
    def test_stt_nova3_builder_passes_model_through(self, stub_provider_keys):
        stt = build_stt(_cfg())
        assert stt._settings.model == "nova-3-general"

    def test_stt_flux_builder_passes_flux_knobs_through(self, stub_provider_keys):
        stt = build_stt(_cfg(stt_provider="deepgram-flux", stt_model="flux-general-en"))
        assert stt._settings.model == "flux-general-en"
        assert stt._settings.eot_threshold == 0.7
        # eager_eot_threshold == 0.0 means disabled: never passed to Settings.
        assert not stt._settings.eager_eot_threshold

    def test_llm_builder_passes_haiku_model_through(self, stub_provider_keys):
        llm = build_llm(_cfg())
        assert llm._settings.model == "claude-haiku-4-5"

    def test_tts_builder_passes_speed_and_voice_through(self, stub_provider_keys):
        tts = build_tts(_cfg(voice_id="voice-abc", speed=1.1))
        assert tts._settings.speed == 1.1
        assert tts._settings.voice == "voice-abc"

    def test_missing_api_key_fails_loudly(self, monkeypatch):
        monkeypatch.delenv("ANTHROPIC_API_KEY", raising=False)
        with pytest.raises(RuntimeError, match="ANTHROPIC_API_KEY"):
            build_llm(_cfg())
