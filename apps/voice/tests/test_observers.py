"""LatencyReportObserver frame-path tests — the Flux-native anchor (plan 01-04
tuning round).

Deepgram Flux owns endpointing server-side and never emits a
``VADUserStoppedSpeakingFrame``, so pipecat's ``UserBotLatencyObserver`` (which
the report observer subclasses) never arms its voice-to-voice anchor and Arm C
records zero turns (RESEARCH Open Question 1). The observer seeds the parent's
user-stop anchor on Flux's plain ``UserStoppedSpeakingFrame`` (its EndOfTurn)
so a measurement fires at the next ``BotStartedSpeakingFrame`` — giving the
post-endpointing processing latency (LLM + TTS + aggregation), with ``vad_stop``
left null because there is no locally observable turn wait.

These tests drive synthetic frames through the observer (no live API).
"""

from __future__ import annotations

import asyncio
import tempfile
from pathlib import Path

from pipecat.frames.frames import (
    BotStartedSpeakingFrame,
    ClientConnectedFrame,
    InterruptionFrame,
    MetricsFrame,
    UserStartedSpeakingFrame,
    UserStoppedSpeakingFrame,
    VADUserStoppedSpeakingFrame,
)
from pipecat.metrics.metrics import TTFBMetricsData
from pipecat.observers.base_observer import FramePushed
from pipecat.processors.frame_processor import FrameDirection

from klanker_voice.config import (
    FluxConfig,
    LlmConfig,
    PersonaConfig,
    PipelineConfig,
    SttConfig,
    TtsConfig,
    TurnConfig,
)
from klanker_voice.observers import LatencyReportObserver


def _cfg(*, stt_provider: str, stt_model: str) -> PipelineConfig:
    return PipelineConfig(
        stt=SttConfig(
            provider=stt_provider,
            model=stt_model,
            flux=FluxConfig(eot_threshold=0.7, eager_eot_threshold=0.0),
        ),
        turn=TurnConfig(strategy="smart_turn_v3", vad_stop_secs=0.2, user_speech_timeout=0.6),
        llm=LlmConfig(provider="anthropic", model="claude-haiku-4-5"),
        tts=TtsConfig(provider="elevenlabs", model="eleven_flash_v2_5", voice_id="", speed=1.1),
        persona=PersonaConfig(prompt_path="prompts/concierge.md"),
    )


def _observer(cfg: PipelineConfig) -> LatencyReportObserver:
    return LatencyReportObserver(
        cfg, config_path="configs/test.toml", artifacts_dir=Path(tempfile.mkdtemp())
    )


def _push(frame) -> FramePushed:
    return FramePushed(
        source=None,
        destination=None,
        frame=frame,
        direction=FrameDirection.DOWNSTREAM,
        timestamp=0,
    )


async def _feed(obs: LatencyReportObserver, frame) -> None:
    await obs.on_push_frame(_push(frame))


async def _settle() -> None:
    # pipecat dispatches event handlers as scheduled tasks — yield so the
    # report's on_latency_* handlers run before assertions.
    await asyncio.sleep(0.05)


class TestFluxNativeAnchor:
    async def test_flux_endofturn_records_a_turn_with_null_vad_stop(self):
        obs = _observer(_cfg(stt_provider="deepgram-flux", stt_model="flux-general-en"))
        # Greeting first so first-bot-speech is consumed (mirrors greet-first).
        await _feed(obs, ClientConnectedFrame())
        await _feed(obs, BotStartedSpeakingFrame())
        await _settle()

        # A real user turn under Flux: StartOfTurn (broadcast interruption +
        # plain UserStartedSpeakingFrame), then EndOfTurn (plain
        # UserStoppedSpeakingFrame), then LLM/TTS TTFB, then bot speaks.
        await _feed(obs, UserStartedSpeakingFrame())
        await _feed(obs, InterruptionFrame())
        await _feed(obs, UserStoppedSpeakingFrame())
        await _feed(
            obs,
            MetricsFrame(
                data=[TTFBMetricsData(processor="DeepgramFluxSTTService#0", value=0.0, model="f")]
            ),
        )
        await asyncio.sleep(0.03)  # processing window (LLM + TTS)
        await _feed(
            obs,
            MetricsFrame(
                data=[TTFBMetricsData(processor="AnthropicLLMService#0", value=0.30, model="h")]
            ),
        )
        await _feed(
            obs,
            MetricsFrame(
                data=[TTFBMetricsData(processor="ElevenLabsTTSService#0", value=0.12, model="e")]
            ),
        )
        await _feed(obs, BotStartedSpeakingFrame())
        await _settle()

        assert len(obs.report.turns) == 1
        turn = obs.report.turns[0]
        # vad_stop stays null — Flux endpointing is server-side, not locally observable.
        assert turn.vad_stop_ms is None
        # voice_to_voice IS measured (EndOfTurn -> bot start), and the per-stage
        # TTFBs that arrived in the processing window are captured.
        assert turn.voice_to_voice_ms is not None and turn.voice_to_voice_ms >= 0
        assert turn.llm_ttft_ms == 300.0
        assert turn.tts_first_audio_ms == 120.0
        summary = obs.report.summary()
        assert summary["vad_stop"] is None
        assert summary["voice_to_voice"] is not None and summary["voice_to_voice"]["n"] == 1

    async def test_non_flux_plain_userstopped_does_not_seed_anchor(self):
        # Guard: under a non-Flux arm the parent anchors on VADUserStoppedSpeakingFrame;
        # a plain UserStoppedSpeakingFrame must NOT arm the measurement (else we'd
        # double-count or measure the wrong window).
        obs = _observer(_cfg(stt_provider="deepgram-nova3", stt_model="nova-3-general"))
        await _feed(obs, ClientConnectedFrame())
        await _feed(obs, BotStartedSpeakingFrame())  # greeting
        await _settle()
        await _feed(obs, UserStartedSpeakingFrame())
        await _feed(obs, UserStoppedSpeakingFrame())  # plain, no VAD frame
        await _feed(obs, BotStartedSpeakingFrame())
        await _settle()
        # No VAD stop frame ever arrived, so no user turn should be recorded.
        assert len(obs.report.turns) == 0
