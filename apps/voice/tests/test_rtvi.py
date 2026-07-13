"""RTVI wiring tests (05-01, CLNT-03/04/06, D-09).

Task 1: RTVIProcessor placement in build_pipeline + RTVIObserverParams +
the worker built by klanker_voice.call_runtime.create_call_session (09-01:
the transport-neutral seam extracted from server.py's former
``_run_session``) carries an RTVIObserver.

Task 2: LatencyReportObserver emits one composed ``kmv-latency``
RTVIServerMessageFrame per finalized turn — the HUD's live data source.

No live client-js/browser connection is exercised here (that's manual,
per the plan's non-gating verification note) — these are frame-path and
pipeline-shape unit tests, matching the existing observer test style
(test_observers.py's ``_feed``/``_settle`` pattern).
"""

from __future__ import annotations

import asyncio
import tempfile
from pathlib import Path
from unittest.mock import AsyncMock

from pipecat.observers.base_observer import FramePushed
from pipecat.processors.frame_processor import FrameDirection, FrameProcessor
from pipecat.processors.frameworks.rtvi import (
    RTVIObserver,
    RTVIObserverParams,
    RTVIProcessor,
    RTVIServerMessageFrame,
)

from klanker_voice.config import (
    FluxConfig,
    LlmConfig,
    PersonaConfig,
    PipelineConfig,
    SttConfig,
    TtsConfig,
    TurnConfig,
)
from klanker_voice.observers import KMV_LATENCY_MESSAGE_TYPE, LatencyReportObserver
from klanker_voice.pipeline import build_pipeline
from klanker_voice.rtvi import build_rtvi_observer_params, build_rtvi_processor


def _cfg() -> PipelineConfig:
    return PipelineConfig(
        stt=SttConfig(
            provider="deepgram-nova3",
            model="nova-3-general",
            flux=FluxConfig(eot_threshold=0.7, eager_eot_threshold=0.0),
        ),
        turn=TurnConfig(strategy="smart_turn_v3", vad_stop_secs=0.2, user_speech_timeout=0.6),
        llm=LlmConfig(provider="anthropic", model="claude-haiku-4-5"),
        tts=TtsConfig(provider="elevenlabs", model="eleven_flash_v2_5", voice_id="", speed=1.1),
        persona=PersonaConfig(prompt_path=Path(__file__)),  # any existing file
    )


class _FakeTransport:
    """Minimal transport double: .input()/.output() FrameProcessors plus a
    no-op event_handler decorator (create_call_session registers
    on_client_disconnected/on_client_connected handlers on the transport)."""

    def input(self) -> FrameProcessor:
        return FrameProcessor(name="fake-transport-input")

    def output(self) -> FrameProcessor:
        return FrameProcessor(name="fake-transport-output")

    def event_handler(self, _name: str):
        def _decorator(fn):
            return fn

        return _decorator


class TestTask1RTVIPlacement:
    def test_build_pipeline_places_rtvi_processor_right_after_transport_input(
        self, stub_provider_keys
    ):
        rtvi = build_rtvi_processor()

        built = build_pipeline(_cfg(), _FakeTransport(), rtvi=rtvi)

        processors = list(built.pipeline.processors)
        input_index = next(
            i for i, p in enumerate(processors) if p.name == "fake-transport-input"
        )
        assert processors[input_index + 1] is rtvi
        assert built.rtvi is rtvi

    def test_build_pipeline_without_rtvi_is_unaffected(self, stub_provider_keys):
        built = build_pipeline(_cfg(), _FakeTransport())

        assert built.rtvi is None
        assert not any(isinstance(p, RTVIProcessor) for p in built.pipeline.processors)

    def test_build_rtvi_observer_params_enables_audio_levels(self):
        params = build_rtvi_observer_params()

        assert isinstance(params, RTVIObserverParams)
        assert params.bot_audio_level_enabled is True
        assert params.user_audio_level_enabled is True
        # Defaults already cover these (read_first note) — assert they still hold.
        assert params.bot_speaking_enabled is True
        assert params.user_transcription_enabled is True
        assert params.metrics_enabled is True

    def test_create_call_session_worker_observers_include_rtvi_observer(
        self, stub_provider_keys
    ):
        """klanker_voice.call_runtime.create_call_session (09-01: the
        transport-neutral seam extracted from server.py's former
        ``_run_session``) builds a worker whose observers list carries an
        RTVIObserver alongside the pre-existing latency/teardown observers.

        Heavy runtime bits (real WebRTC transport, ``CallSession.run()``)
        are never exercised — ``create_call_session`` only *constructs* the
        session, it doesn't run the pipeline — so the real ``PipelineWorker``
        can be built directly against the fake transport with no stubbing.
        """
        import asyncio as _asyncio

        from klanker_voice import call_runtime, quota
        from klanker_voice.config import load_duplex_config, load_knowledge_config, load_quota_config
        from klanker_voice.session import TeardownObserver

        gate_result = quota.GateResult(
            session_id="s1",
            tier=quota.Tier(
                tier_id="demo", session_max_seconds=120, period_max_seconds=600, max_concurrent=2
            ),
            session_max_seconds=120,
            remaining_daily_seconds=600,
            bypass_accounting=True,
        )

        call_session = _asyncio.run(
            call_runtime.create_call_session(
                transport=_FakeTransport(),
                identity=call_runtime.CallIdentity(subject="u1", authenticated=True),
                gate_result=gate_result,
                cfg=_cfg(),
                knowledge_cfg=load_knowledge_config(),
                duplex_cfg=load_duplex_config(),
                quota_cfg=load_quota_config(),
                channel="webrtc",
                metadata={},
            )
        )

        # PipelineWorker wraps the observers list passed to build_worker() in
        # its private WorkerObserver proxy — inspect the real thing rather
        # than stubbing build_worker (a fake worker lacks the `.name`/
        # `.attach()` real WorkerRunner.add_workers() requires).
        observers = call_session.worker._observer._observers
        assert any(isinstance(o, RTVIObserver) for o in observers)
        assert any(isinstance(o, LatencyReportObserver) for o in observers)
        assert any(isinstance(o, TeardownObserver) for o in observers)


class TestPacingFnWiring:
    """07-05 / 07-VERIFICATION gap closure (D-06 time-aware pacing).

    The router's ``remaining_seconds_fn`` must be sourced from the live
    ``SessionLifecycle`` in production. Two seams, both required or the pacing
    feature (built + unit-tested in 07-05's test_knowledge_pacing.py) is dead
    code in the deployed bot:
      1. ``build_pipeline`` forwards the fn to ``KnowledgeRouterProcessor``.
      2. ``klanker_voice.call_runtime.create_call_session`` (09-01: the
         transport-neutral seam extracted from server.py's former
         ``_run_session``) passes ``lifecycle.remaining_seconds`` into
         ``build_pipeline``.
    The dev/eval path (bot.py) supplies nothing → stays ``None`` (no session
    cap there), so no regression for existing callers.
    """

    def test_build_pipeline_forwards_remaining_seconds_fn_to_router(self, stub_provider_keys):
        from klanker_voice.knowledge.router import KnowledgeRouterProcessor

        def _fn():
            return 42.0

        built = build_pipeline(_cfg(), _FakeTransport(), remaining_seconds_fn=_fn)

        router = next(
            p for p in built.pipeline.processors if isinstance(p, KnowledgeRouterProcessor)
        )
        assert router._remaining_seconds_fn is _fn

    def test_build_pipeline_default_remaining_seconds_fn_is_none(self, stub_provider_keys):
        from klanker_voice.knowledge.router import KnowledgeRouterProcessor

        built = build_pipeline(_cfg(), _FakeTransport())

        router = next(
            p for p in built.pipeline.processors if isinstance(p, KnowledgeRouterProcessor)
        )
        assert router._remaining_seconds_fn is None

    def test_create_call_session_sources_remaining_seconds_from_lifecycle(
        self, monkeypatch, stub_provider_keys
    ):
        """``klanker_voice.call_runtime.create_call_session`` must hand
        ``lifecycle.remaining_seconds`` to ``build_pipeline`` so the deployed
        router paces to the real session clock. ``build_pipeline`` (as
        imported into ``call_runtime``'s namespace) is wrapped to capture its
        kwarg while still running for real."""
        import asyncio as _asyncio

        from klanker_voice import call_runtime, quota
        from klanker_voice.config import load_duplex_config, load_knowledge_config, load_quota_config

        captured: dict = {}

        def _capturing_build_pipeline(cfg, transport, **kwargs):
            captured["remaining_seconds_fn"] = kwargs.get("remaining_seconds_fn")
            return build_pipeline(cfg, transport, **kwargs)

        monkeypatch.setattr(call_runtime, "build_pipeline", _capturing_build_pipeline)

        gate_result = quota.GateResult(
            session_id="s1",
            tier=quota.Tier(
                tier_id="demo", session_max_seconds=120, period_max_seconds=600, max_concurrent=2
            ),
            session_max_seconds=120,
            remaining_daily_seconds=600,
            bypass_accounting=True,
        )

        call_session = _asyncio.run(
            call_runtime.create_call_session(
                transport=_FakeTransport(),
                identity=call_runtime.CallIdentity(subject="u1", authenticated=True),
                gate_result=gate_result,
                cfg=_cfg(),
                knowledge_cfg=load_knowledge_config(),
                duplex_cfg=load_duplex_config(),
                quota_cfg=load_quota_config(),
                channel="webrtc",
                metadata={},
            )
        )

        fn = captured["remaining_seconds_fn"]
        assert fn is not None
        # Bound methods are re-created per attribute access, so compare identity
        # via __self__ (the exact lifecycle instance) rather than `is`.
        assert getattr(fn, "__self__", None) is call_session.lifecycle


# ---------------------------------------------------------------------------
# Task 2: composed per-turn kmv-latency emission
# ---------------------------------------------------------------------------


def _observer(cfg: PipelineConfig) -> LatencyReportObserver:
    return LatencyReportObserver(
        cfg, config_path="configs/test.toml", artifacts_dir=Path(tempfile.mkdtemp())
    )


def _push(frame, destination=None) -> FramePushed:
    return FramePushed(
        source=None,
        destination=destination,
        frame=frame,
        direction=FrameDirection.DOWNSTREAM,
        timestamp=0,
    )


async def _feed(obs: LatencyReportObserver, frame, *, destination=None) -> None:
    await obs.on_push_frame(_push(frame, destination=destination))


async def _settle() -> None:
    await asyncio.sleep(0.05)


async def _drive_one_turn(obs: LatencyReportObserver, *, destination) -> None:
    """Drive a full user->bot turn through the observer (mirrors
    test_observers.py's non-Flux nova-3 fixture). Every frame carries
    ``destination`` (a live processor stand-in) so the observer has a
    downstream processor cached by the time the turn finalizes and pushes
    the kmv-latency frame (see observers.py)."""
    from pipecat.frames.frames import (
        BotStartedSpeakingFrame,
        ClientConnectedFrame,
        MetricsFrame,
        UserStartedSpeakingFrame,
        VADUserStoppedSpeakingFrame,
    )
    from pipecat.metrics.metrics import TTFBMetricsData

    await _feed(obs, ClientConnectedFrame(), destination=destination)
    await _feed(obs, BotStartedSpeakingFrame(), destination=destination)  # greeting
    await _settle()

    await _feed(obs, UserStartedSpeakingFrame(), destination=destination)
    await _feed(obs, VADUserStoppedSpeakingFrame(stop_secs=0.2), destination=destination)
    await _feed(
        obs,
        MetricsFrame(data=[TTFBMetricsData(processor="AnthropicLLMService#0", value=0.30, model="h")]),
        destination=destination,
    )
    await _feed(
        obs,
        MetricsFrame(data=[TTFBMetricsData(processor="ElevenLabsTTSService#0", value=0.12, model="e")]),
        destination=destination,
    )
    await _feed(obs, BotStartedSpeakingFrame(), destination=destination)
    await _settle()


class TestTask2KmvLatencyEmission:
    def test_one_turn_pushes_exactly_one_kmv_latency_frame(self):
        fake_downstream = AsyncMock()
        obs = _observer(_cfg())

        asyncio.run(_drive_one_turn(obs, destination=fake_downstream))

        assert len(obs.report.turns) == 1
        fake_downstream.push_frame.assert_awaited_once()
        (pushed_frame,), _ = fake_downstream.push_frame.call_args
        assert isinstance(pushed_frame, RTVIServerMessageFrame)
        payload = pushed_frame.data
        assert payload["type"] == KMV_LATENCY_MESSAGE_TYPE
        data = payload["data"]
        assert set(data.keys()) == {
            "stt_ms",
            "llm_ttft_ms",
            "tts_first_audio_ms",
            "voice_to_voice_ms",
            "v2v_p50_ms",
        }
        assert data["llm_ttft_ms"] == 300.0
        assert data["tts_first_audio_ms"] == 120.0
        assert data["voice_to_voice_ms"] is not None

    def test_missing_stage_serializes_as_none_not_an_exception(self):
        """stt_final_ms is routinely None (see anchors doc: streaming STT
        reports TTFB outside the measured cycle) — must serialize as null,
        never raise."""
        fake_downstream = AsyncMock()
        obs = _observer(_cfg())

        asyncio.run(_drive_one_turn(obs, destination=fake_downstream))

        (pushed_frame,), _ = fake_downstream.push_frame.call_args
        assert pushed_frame.data["data"]["stt_ms"] is None

    def test_running_p50_matches_report_summary(self):
        fake_downstream = AsyncMock()
        obs = _observer(_cfg())

        asyncio.run(_drive_one_turn(obs, destination=fake_downstream))
        asyncio.run(_drive_one_turn(obs, destination=fake_downstream))

        assert len(obs.report.turns) == 2
        expected_p50 = obs.report.summary()["voice_to_voice"]["p50_ms"]
        (pushed_frame,), _ = fake_downstream.push_frame.call_args
        assert pushed_frame.data["data"]["v2v_p50_ms"] == expected_p50

    def test_no_downstream_processor_seen_is_a_no_op(self):
        """A turn with no destination ever observed (destination=None
        throughout, as harness/console synthetic feeds may do) must skip
        emission, not crash — the harness artifact path stays unaffected."""
        obs = _observer(_cfg())

        asyncio.run(_drive_one_turn(obs, destination=None))

        assert len(obs.report.turns) == 1  # harness artifact path is unaffected

    def test_artifact_and_p50_table_unaffected_by_emission(self):
        """Additive emission only — the JSON artifact/report state is
        unaffected by whether a downstream processor was observed.
        voice_to_voice_ms is excluded from the comparison: it's a real
        wall-clock measurement (see observers.py), so two separate
        asyncio.run() calls naturally differ by sub-millisecond jitter — the
        point of this test is that emitting the RTVI frame never mutates the
        recorded stage values."""
        obs_with_downstream = _observer(_cfg())
        obs_without_downstream = _observer(_cfg())

        asyncio.run(_drive_one_turn(obs_with_downstream, destination=AsyncMock()))
        asyncio.run(_drive_one_turn(obs_without_downstream, destination=None))

        turn_with = obs_with_downstream.report.turns[0]
        turn_without = obs_without_downstream.report.turns[0]
        assert turn_with.vad_stop_ms == turn_without.vad_stop_ms
        assert turn_with.llm_ttft_ms == turn_without.llm_ttft_ms
        assert turn_with.tts_first_audio_ms == turn_without.tts_first_audio_ms
        assert turn_with.stt_final_ms == turn_without.stt_final_ms
