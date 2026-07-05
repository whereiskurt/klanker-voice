"""LatencyReportObserver: UserBotLatencyObserver events -> JSON artifact + table.

Builds on pipecat 1.5.0's built-in ``UserBotLatencyObserver`` (RESEARCH
Don't Hand-Roll: it already anchors voice-to-voice on VAD events and collects
per-service TTFB breakdowns — no custom frame-timestamp logging here). This
subclass only:

* maps each ``on_latency_breakdown`` payload onto the five stable stage names
  (``vad_stop``, ``stt_final``, ``llm_ttft``, ``tts_first_audio``,
  ``voice_to_voice``) and appends a :class:`~klanker_voice.harness.report.TurnRecord`,
* serializes the schema-v1 JSON artifact to ``apps/voice/artifacts/harness/``
  (incrementally after every turn, so even a hard kill leaves the artifact),
* prints the rich console table at session end (EndFrame/CancelFrame).

Attach via ``build_worker(pipeline, observers=[LatencyReportObserver(cfg)])``
— ``enable_metrics`` is already on from plan 01-02.
"""

from __future__ import annotations

import os
import time
import uuid
from pathlib import Path

from loguru import logger

from pipecat.frames.frames import CancelFrame, EndFrame, UserStoppedSpeakingFrame
from pipecat.observers.base_observer import FramePushed
from pipecat.observers.user_bot_latency_observer import (
    LatencyBreakdown,
    UserBotLatencyObserver,
)
from pipecat.processors.frame_processor import FrameDirection

from klanker_voice.config import (
    APP_ROOT,
    CONFIG_PATH_ENV_VAR,
    DEFAULT_CONFIG_PATH,
    PipelineConfig,
)
from klanker_voice.harness.report import Report, TurnRecord, build_anchors

#: Default artifact directory (gitignored — T-1-07: artifacts never in git).
DEFAULT_ARTIFACTS_DIR = APP_ROOT / "artifacts" / "harness"

FLUX_PROVIDER = "deepgram-flux"


def arm_name(cfg: PipelineConfig) -> str:
    """Human-readable A/B arm label derived from config.

    Flux owns turn detection server-side, so the arm is just the provider;
    local arms are ``<stt-provider>+<turn-strategy>``.
    """
    if cfg.stt.provider == FLUX_PROVIDER:
        return cfg.stt.provider
    return f"{cfg.stt.provider}+{cfg.turn.strategy}"


def _classify_processor(name: str) -> str | None:
    """Map a metrics processor name onto a stage.

    Processor names look like ``ElevenLabsTTSService#0``. Suffix matching is
    deliberate: a naive ``"stt" in name`` check would misfire on
    ``elevenlabsTTSservice`` (the ``bsTTs`` run contains ``stt``).
    """
    base = name.split("#", 1)[0].lower()
    if base.endswith("sttservice"):
        return "stt_final"
    if base.endswith("llmservice"):
        return "llm_ttft"
    if base.endswith("ttsservice"):
        return "tts_first_audio"
    return None


class LatencyReportObserver(UserBotLatencyObserver):
    """Serializes UserBotLatencyObserver measurements into the D-11 report.

    Args:
        cfg: The loaded pipeline config — arm metadata and per-arm anchors are
            recorded per run (Pitfall 7 / Open Question 1).
        config_path: Config file the run was built from; defaults to the same
            resolution ``load_config()`` used (env override or the default).
        artifacts_dir: Where the JSON artifact lands; defaults to
            ``apps/voice/artifacts/harness/``.
    """

    def __init__(
        self,
        cfg: PipelineConfig,
        *,
        config_path: Path | str | None = None,
        artifacts_dir: Path | str | None = None,
        **kwargs,
    ):
        super().__init__(**kwargs)
        if config_path is None:
            config_path = os.environ.get(CONFIG_PATH_ENV_VAR) or DEFAULT_CONFIG_PATH
        self._flux = cfg.stt.provider == FLUX_PROVIDER
        arm = arm_name(cfg)
        turn_strategy = None if cfg.stt.provider == FLUX_PROVIDER else cfg.turn.strategy
        # Config metadata only — provider/model/knob names, never env values
        # or key material (T-1-07).
        self._report = Report(
            config={
                "arm": arm,
                "config_path": str(config_path),
                "stt_provider": cfg.stt.provider,
                "stt_model": cfg.stt.model,
                "turn_strategy": turn_strategy,
                "llm_model": cfg.llm.model,
                "tts_model": cfg.tts.model,
            },
            anchors=build_anchors(cfg.stt.provider, turn_strategy),
        )
        stamp = time.strftime("%Y%m%dT%H%M%SZ", time.gmtime())
        slug = arm.replace("+", "-")
        self._artifact_path = (
            Path(artifacts_dir) if artifacts_dir is not None else DEFAULT_ARTIFACTS_DIR
        ) / f"{stamp}-{slug}-{uuid.uuid4().hex[:6]}.json"
        self._pending_v2v_ms: float | None = None
        self._report_finalized = False

        # Subscribe to the parent's event surface — the built-in anchoring is
        # the measurement; this class only records and serializes.
        self.add_event_handler("on_latency_measured", self._record_latency_measured)
        self.add_event_handler("on_latency_breakdown", self._record_latency_breakdown)
        self.add_event_handler("on_first_bot_speech_latency", self._record_first_bot_speech)

    @property
    def report(self) -> Report:
        """The accumulating report (harness/tests introspection)."""
        return self._report

    @property
    def artifact_path(self) -> Path:
        """Where the JSON artifact is (incrementally) written."""
        return self._artifact_path

    async def on_push_frame(self, data: FramePushed):
        """Track latency (parent) and finalize the report at session end."""
        await super().on_push_frame(data)
        if data.direction != FrameDirection.DOWNSTREAM:
            return
        # Flux-native voice-to-voice anchor. Flux owns endpointing server-side
        # and never emits a VADUserStoppedSpeakingFrame, so the parent's anchor
        # never arms and Arm C records zero turns (RESEARCH Open Question 1).
        # Flux instead broadcasts a plain UserStoppedSpeakingFrame at its
        # EndOfTurn. Seed the parent's user-stop anchor AFTER it has processed
        # that frame — so user_turn_secs stays None (vad_stop stays null: there
        # is no locally observable turn wait, the EOT wait is server-side) while
        # on_latency_measured still fires at the next BotStartedSpeakingFrame.
        # The resulting voice_to_voice is the POST-ENDPOINTING processing
        # latency (LLM + TTS + aggregation); it EXCLUDES Flux's server-side EOT
        # detection wait, so it is comparable to the local arms' (voice_to_voice
        # minus vad_stop), not to their full voice_to_voice (see anchors).
        if (
            self._flux
            and self._user_stopped_time is None
            and isinstance(data.frame, UserStoppedSpeakingFrame)
        ):
            self._user_stopped_time = time.time()
        if isinstance(data.frame, (EndFrame, CancelFrame)):
            self.finalize()

    # ------------------------------------------------------------------
    # Event handlers (called by the parent's _call_event_handler)
    # ------------------------------------------------------------------

    async def _record_latency_measured(self, _observer, latency_secs: float):
        # Emitted just before on_latency_breakdown for the same cycle
        # (see UserBotLatencyObserver._handle_bot_started_speaking).
        self._pending_v2v_ms = latency_secs * 1000.0

    async def _record_latency_breakdown(self, _observer, breakdown: LatencyBreakdown):
        v2v_ms = self._pending_v2v_ms
        self._pending_v2v_ms = None
        if v2v_ms is None:
            # First-bot-speech (greeting) breakdown: anchored on client
            # connect, not on a user-stop — a different clock. Keeping it out
            # of the per-turn stats keeps the summary's anchors honest.
            return

        record = TurnRecord(voice_to_voice_ms=round(v2v_ms, 1))
        if breakdown.user_turn_secs is not None:
            record.vad_stop_ms = round(breakdown.user_turn_secs * 1000.0, 1)
        for ttfb in breakdown.ttfb:
            stage = _classify_processor(ttfb.processor)
            if stage is None:
                continue
            # First entry per stage wins: list order is metric arrival order,
            # and the first TTFB is the one on the speaking-latency path.
            if getattr(record, f"{stage}_ms") is None:
                setattr(record, f"{stage}_ms", round(ttfb.duration_secs * 1000.0, 1))

        self._report.add_turn(record)
        self._write_artifact()

    async def _record_first_bot_speech(self, _observer, latency_secs: float):
        # Connect -> first speech (the D-04 greeting). Different anchor than
        # voice_to_voice, so it lives in its own top-level field.
        self._report.first_bot_speech_ms = round(latency_secs * 1000.0, 1)
        self._write_artifact()

    # ------------------------------------------------------------------
    # Artifact + table
    # ------------------------------------------------------------------

    def _write_artifact(self) -> None:
        try:
            self._report.write(self._artifact_path)
        except OSError as e:
            # Never let artifact I/O take down a live conversation.
            logger.error(f"LatencyReportObserver: failed to write artifact: {e}")

    def finalize(self) -> None:
        """Write the final JSON and print the table once (D-11).

        Called on EndFrame/CancelFrame; safe to call again (no-op). Verdicts
        in the table are informational only — this never raises on a
        threshold and never exits (D-13).
        """
        if self._report_finalized:
            return
        self._report_finalized = True
        self._write_artifact()
        logger.info(f"LatencyReportObserver: harness artifact at {self._artifact_path}")
        try:
            self._report.render()
        except Exception as e:  # rendering is a nicety; the artifact is the record
            logger.error(f"LatencyReportObserver: table render failed: {e}")
