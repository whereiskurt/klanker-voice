"""p50/p95 aggregation, stable JSON schema v1, rich table, informational verdicts.

Schema contract (D-11; consumed by Phase 5 HUD/CI — stage names are frozen):

.. code-block:: json

    {
      "schema_version": 1,
      "generated_at": "2026-07-05T01:23:45Z",
      "config": { "arm": "...", "config_path": "...", "stt_provider": "...", ... },
      "anchors": { "vad_stop": "...", "stt_final": "...", ... },
      "first_bot_speech_ms": 1234.5,
      "turns": [ { "vad_stop_ms": ..., ..., "voice_to_voice_ms": ... } ],
      "summary": {
        "vad_stop": { "p50_ms": ..., "p95_ms": ..., "n": ... },
        "stt_final": null,
        ...
      }
    }

Rules baked in here:

* The five stage names are EXACTLY ``vad_stop``, ``stt_final``, ``llm_ttft``,
  ``tts_first_audio``, ``voice_to_voice`` — always present in ``summary``.
* A stage an arm cannot populate (e.g. ``vad_stop`` under Flux, which owns turn
  detection server-side) serializes as ``null``, never silently omitted, with
  the ``anchors`` entry explaining why (RESEARCH Pitfall 7, Open Question 1).
* Verdicts are informational ✓/⚠ marks against the ~800ms target / 1.2s
  ceiling (D-13). Rendering a warning NEVER causes a nonzero exit; turning
  this into a CI gate is explicitly Phase 5 work.
* All numbers here come from the in-pipeline observer clock
  (UserBotLatencyObserver). Eval-scenario ``within_ms`` budgets measure the
  harness clock — the two must never be mixed in one table (Pitfall 7).
"""

from __future__ import annotations

import json
import math
from dataclasses import dataclass
from pathlib import Path

SCHEMA_VERSION = 1

#: Stable stage names — the D-11 per-stage breakdown. Order is render order.
STAGE_NAMES: tuple[str, ...] = (
    "vad_stop",
    "stt_final",
    "llm_ttft",
    "tts_first_audio",
    "voice_to_voice",
)

#: Informational thresholds (D-13): ~800ms target p50, 1.2s ceiling p95.
TARGET_P50_MS = 800.0
CEILING_P95_MS = 1200.0

_CHECK_MARK = "✓"
_WARN_MARK = "⚠"


def percentile(values: list[float], pct: float) -> float:
    """Linear-interpolation percentile (numpy 'linear' method).

    Args:
        values: Non-empty list of samples (order irrelevant).
        pct: Percentile in [0, 100].

    Raises:
        ValueError: on an empty sample set.
    """
    if not values:
        raise ValueError("percentile() of empty sample set")
    vs = sorted(float(v) for v in values)
    if len(vs) == 1:
        return vs[0]
    k = (len(vs) - 1) * (pct / 100.0)
    lo = math.floor(k)
    hi = math.ceil(k)
    if lo == hi:
        return vs[int(k)]
    return vs[lo] + (vs[hi] - vs[lo]) * (k - lo)


def build_anchors(stt_provider: str, turn_strategy: str | None) -> dict[str, str]:
    """Per-arm documentation of what each stage measurement is anchored to.

    Required by Pitfall 7 (every number's anchor is documented in the JSON)
    and Open Question 1 (the vad_stop anchor collapses under Flux — the
    anchors entry explains the null instead of the stage silently vanishing).

    Args:
        stt_provider: ``deepgram-nova3`` | ``deepgram-flux``.
        turn_strategy: local turn strategy name, or ``None`` for the Flux arm
            (Flux installs ExternalUserTurnStrategies — no local strategy).
    """
    flux = stt_provider == "deepgram-flux"
    if flux:
        vad_stop = (
            "NULL under Flux: Deepgram Flux owns turn detection server-side "
            "(ExternalUserTurnStrategies); no local VAD runs, so "
            "VADUserStoppedSpeakingFrame never fires and there is no local "
            "VAD-stop anchor to measure from (RESEARCH Open Question 1)."
        )
    else:
        vad_stop = (
            "actual user silence (VADUserStoppedSpeakingFrame timestamp minus "
            "vad stop_secs) -> turn release (UserStoppedSpeakingFrame). "
            "Includes the VAD silence window, STT finalization wait, and any "
            f"turn-analyzer wait (strategy: {turn_strategy})."
        )
    return {
        "vad_stop": vad_stop,
        "stt_final": (
            "STT service TTFB reported by pipecat metrics during the "
            "user-stop -> bot-start cycle (time to first transcription "
            "byte). OFTEN NULL: streaming STT reports TTFB at the first "
            "partial, while the user is still speaking — outside the "
            "measured cycle. The STT finalization wait is already included "
            "in vad_stop (user_turn_secs)."
        ),
        "llm_ttft": (
            "LLM service TTFB reported by pipecat metrics during the cycle "
            "(request start -> first streamed token)."
        ),
        "tts_first_audio": (
            "TTS service TTFB reported by pipecat metrics during the cycle "
            "(synthesis request -> first audio byte)."
        ),
        "voice_to_voice": (
            "UserBotLatencyObserver on_latency_measured: actual user silence "
            "(VAD stop_secs-adjusted) -> BotStartedSpeakingFrame. Observer "
            "clock — never comparable with eval-scenario within_ms budgets, "
            "which anchor on the harness's send (Pitfall 7)."
        ),
    }


@dataclass
class TurnRecord:
    """Raw per-turn stage measurements in milliseconds (None = not observed)."""

    vad_stop_ms: float | None = None
    stt_final_ms: float | None = None
    llm_ttft_ms: float | None = None
    tts_first_audio_ms: float | None = None
    voice_to_voice_ms: float | None = None

    def stage_value(self, stage: str) -> float | None:
        """Value for one of the five stable stage names, or None."""
        return getattr(self, f"{stage}_ms")

    def to_dict(self) -> dict:
        return {f"{stage}_ms": self.stage_value(stage) for stage in STAGE_NAMES}

    @classmethod
    def from_dict(cls, data: dict) -> "TurnRecord":
        return cls(**{f"{stage}_ms": data.get(f"{stage}_ms") for stage in STAGE_NAMES})


class Report:
    """Aggregates TurnRecords into the schema-v1 JSON artifact + rich table."""

    def __init__(
        self,
        config: dict,
        anchors: dict[str, str],
        *,
        generated_at: str | None = None,
    ):
        """Create an empty report.

        Args:
            config: Run metadata (arm, config path, providers, models, ...).
                Never carries env values or key material (T-1-07).
            anchors: Per-stage anchor documentation (see :func:`build_anchors`).
            generated_at: ISO-8601 UTC timestamp; defaults to now on serialize.
        """
        missing = [s for s in STAGE_NAMES if s not in anchors]
        if missing:
            raise ValueError(f"anchors missing stage entries: {missing}")
        self.config = dict(config)
        self.anchors = dict(anchors)
        self.generated_at = generated_at
        self.first_bot_speech_ms: float | None = None
        self.turns: list[TurnRecord] = []

    def add_turn(self, record: TurnRecord) -> None:
        """Append one measured user->bot turn."""
        self.turns.append(record)

    def summary(self) -> dict:
        """p50/p95/n per stage. Stages with no samples are None (never omitted)."""
        out: dict[str, dict | None] = {}
        for stage in STAGE_NAMES:
            values = [v for t in self.turns if (v := t.stage_value(stage)) is not None]
            if not values:
                out[stage] = None
                continue
            out[stage] = {
                "p50_ms": round(percentile(values, 50), 1),
                "p95_ms": round(percentile(values, 95), 1),
                "n": len(values),
            }
        return out

    def verdict_mark(self) -> str:
        """Informational ✓/⚠ against the D-13 thresholds. NEVER exits.

        ✓ when voice_to_voice p50 <= 800ms AND p95 <= 1200ms; ⚠ otherwise
        (including when voice_to_voice has no samples yet).
        """
        v2v = self.summary()["voice_to_voice"]
        if v2v is None:
            return _WARN_MARK
        ok = v2v["p50_ms"] <= TARGET_P50_MS and v2v["p95_ms"] <= CEILING_P95_MS
        return _CHECK_MARK if ok else _WARN_MARK

    # ------------------------------------------------------------------
    # Serialization (the stability contract)
    # ------------------------------------------------------------------

    def to_dict(self) -> dict:
        import datetime

        generated_at = self.generated_at or datetime.datetime.now(
            datetime.timezone.utc
        ).strftime("%Y-%m-%dT%H:%M:%SZ")
        return {
            "schema_version": SCHEMA_VERSION,
            "generated_at": generated_at,
            "config": dict(self.config),
            "anchors": dict(self.anchors),
            "first_bot_speech_ms": self.first_bot_speech_ms,
            "turns": [t.to_dict() for t in self.turns],
            "summary": self.summary(),
        }

    def write(self, path: Path | str) -> Path:
        """Serialize the artifact JSON to ``path`` (parents created)."""
        path = Path(path)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(self.to_dict(), indent=2) + "\n", encoding="utf-8")
        return path

    @classmethod
    def from_dict(cls, data: dict) -> "Report":
        """Rehydrate a report from an artifact dict (for the CLI).

        Raises:
            ValueError: on a missing/unsupported schema_version or missing
                required keys — genuine schema errors are surfaced, never
                masked (they are the CLI's only nonzero-exit path).
        """
        version = data.get("schema_version")
        if version != SCHEMA_VERSION:
            raise ValueError(
                f"unsupported harness schema_version {version!r} (expected {SCHEMA_VERSION})"
            )
        for key in ("config", "anchors", "turns", "summary"):
            if key not in data:
                raise ValueError(f"harness artifact missing required key {key!r}")
        report = cls(
            config=data["config"],
            anchors=data["anchors"],
            generated_at=data.get("generated_at"),
        )
        report.first_bot_speech_ms = data.get("first_bot_speech_ms")
        for turn in data["turns"]:
            report.add_turn(TurnRecord.from_dict(turn))
        return report

    @classmethod
    def load(cls, path: Path | str) -> "Report":
        """Load an artifact JSON from disk (for ``report``/``compare``)."""
        raw = Path(path).read_text(encoding="utf-8")
        return cls.from_dict(json.loads(raw))

    # ------------------------------------------------------------------
    # Rendering (rich imported lazily: the JSON path has no rich dependency)
    # ------------------------------------------------------------------

    def build_table(self):
        """Rich table: one row per stage + informational verdict column (D-13)."""
        from rich.table import Table

        summary = self.summary()
        table = Table(
            title=f"Latency report — arm: {self.config.get('arm', '?')}",
            caption=(
                f"verdict is informational only (D-13): {_CHECK_MARK} when "
                f"voice_to_voice p50<={TARGET_P50_MS:.0f}ms and "
                f"p95<={CEILING_P95_MS:.0f}ms; never a nonzero exit"
            ),
        )
        table.add_column("stage")
        table.add_column("p50 (ms)", justify="right")
        table.add_column("p95 (ms)", justify="right")
        table.add_column("n", justify="right")
        table.add_column("verdict", justify="center")

        for stage in STAGE_NAMES:
            stats = summary[stage]
            if stats is None:
                table.add_row(stage, "—", "—", "0", "")
                continue
            verdict = self.verdict_mark() if stage == "voice_to_voice" else ""
            table.add_row(
                stage,
                f"{stats['p50_ms']:.1f}",
                f"{stats['p95_ms']:.1f}",
                str(stats["n"]),
                verdict,
            )
        return table

    def render(self, console=None) -> None:
        """Print the table. Informational only — never raises on thresholds,
        never calls sys.exit (D-13)."""
        from rich.console import Console

        (console or Console()).print(self.build_table())


def build_comparison_table(reports: list[tuple[str, Report]]):
    """Side-by-side per-stage p50/p95 diff table — the D-11 A/B instrument.

    Args:
        reports: (label, report) pairs; two or more.

    With exactly two reports, delta columns (second minus first, ms) are added
    per stage so TUNING.md verdict tables can be read straight off the render.
    """
    from rich.table import Table

    if len(reports) < 2:
        raise ValueError("compare needs at least two artifacts")

    table = Table(
        title="Latency comparison (per-stage p50/p95)",
        caption=(
            "observer-clock numbers only; verdicts informational (D-13). "
            "Δ = second − first (negative is faster)."
        ),
    )
    table.add_column("stage")
    for label, _ in reports:
        table.add_column(f"{label}\np50 / p95 (n)", justify="right")
    two_way = len(reports) == 2
    if two_way:
        table.add_column("Δ p50", justify="right")
        table.add_column("Δ p95", justify="right")

    summaries = [(label, report.summary()) for label, report in reports]
    for stage in STAGE_NAMES:
        row = [stage]
        for _, summary in summaries:
            stats = summary[stage]
            row.append(
                "—" if stats is None else f"{stats['p50_ms']:.1f} / {stats['p95_ms']:.1f} ({stats['n']})"
            )
        if two_way:
            first, second = summaries[0][1][stage], summaries[1][1][stage]
            if first is None or second is None:
                row.extend(["—", "—"])
            else:
                row.append(f"{second['p50_ms'] - first['p50_ms']:+.1f}")
                row.append(f"{second['p95_ms'] - first['p95_ms']:+.1f}")
        table.add_row(*row)
    return table
