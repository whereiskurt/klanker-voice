"""RTVI wiring tests (05-01, CLNT-03/04/06, D-09).

Task 1: RTVIProcessor placement in build_pipeline + RTVIObserverParams +
the worker built in server._run_session carries an RTVIObserver.

No live client-js/browser connection is exercised here (that's manual,
per the plan's non-gating verification note) — these are pipeline-shape
unit tests, matching the existing observer test style.
"""

from __future__ import annotations

import asyncio
from pathlib import Path

from pipecat.processors.frame_processor import FrameProcessor
from pipecat.processors.frameworks.rtvi import RTVIObserver, RTVIObserverParams, RTVIProcessor

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
    no-op event_handler decorator (server._run_session registers
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

    def test_run_session_worker_observers_include_rtvi_observer(self, monkeypatch, stub_provider_keys):
        """server._run_session builds a worker whose observers list carries an
        RTVIObserver alongside the pre-existing latency/teardown observers.

        Heavy runtime bits (real WebRTC transport, WorkerRunner.run(), greet
        wiring) are stubbed — this test isolates the worker/observer wiring
        seam, matching test_smoke.py's stubbing precedent for _run_session's
        neighbors.
        """
        import server
        from klanker_voice import quota
        from klanker_voice.session import SessionLifecycle, TeardownObserver

        captured: dict = {}

        class _FakeWorker:
            def __init__(self, pipeline, *, observers=None):
                captured["observers"] = observers or []

        def _fake_build_worker(pipeline, *, observers=None):
            return _FakeWorker(pipeline, observers=observers)

        class _FakeRunner:
            def __init__(self, *args, **kwargs):
                pass

            async def add_workers(self, worker):
                pass

            async def run(self):
                pass

            async def cancel(self, *args, **kwargs):
                pass

        monkeypatch.setattr(server, "SmallWebRTCTransport", lambda **kwargs: _FakeTransport())
        monkeypatch.setattr(server, "build_worker", _fake_build_worker)
        monkeypatch.setattr(server, "WorkerRunner", _FakeRunner)
        monkeypatch.setattr(server, "register_greet_first", lambda *a, **k: None)

        lifecycle = SessionLifecycle(
            user_id="u1",
            session_id="s1",
            tier=quota.Tier(
                tier_id="demo", session_max_seconds=120, period_max_seconds=600, max_concurrent=2
            ),
            quota_config=server.load_quota_config(),
            bypass_accounting=True,
        )

        asyncio.run(server._run_session(connection=object(), lifecycle=lifecycle))

        observers = captured["observers"]
        assert any(isinstance(o, RTVIObserver) for o in observers)
        assert any(isinstance(o, LatencyReportObserver) for o in observers)
        assert any(isinstance(o, TeardownObserver) for o in observers)
