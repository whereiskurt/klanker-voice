"""Report math + schema stability tests (PIPE-05, D-11, D-13).

The JSON schema is a stability contract: Phase 5's HUD and CI gate consume
these exact keys. If a test here fails after a refactor, the refactor broke
the contract — fix the code, not the test.
"""

import io
import json

import pytest

from klanker_voice.harness.report import (
    SCHEMA_VERSION,
    STAGE_NAMES,
    Report,
    TurnRecord,
    build_anchors,
    build_comparison_table,
    percentile,
)

NOVA3_ANCHORS = build_anchors("deepgram-nova3", "smart_turn_v3")
FLUX_ANCHORS = build_anchors("deepgram-flux", None)

CONFIG = {
    "arm": "deepgram-nova3+smart_turn_v3",
    "config_path": "pipeline.toml",
    "stt_provider": "deepgram-nova3",
    "stt_model": "nova-3-general",
    "turn_strategy": "smart_turn_v3",
    "llm_model": "claude-haiku-4-5",
    "tts_model": "eleven_flash_v2_5",
}


def make_report(v2v_values, *, vad_values=None, anchors=NOVA3_ANCHORS) -> Report:
    report = Report(config=CONFIG, anchors=anchors, generated_at="2026-07-05T00:00:00Z")
    for i, v2v in enumerate(v2v_values):
        record = TurnRecord(voice_to_voice_ms=v2v)
        if vad_values is not None:
            record.vad_stop_ms = vad_values[i]
        report.add_turn(record)
    return report


# ---------------------------------------------------------------------------
# Percentile math (fixed synthetic set, known values)
# ---------------------------------------------------------------------------


class TestPercentileMath:
    def test_known_p50_p95_ten_values(self):
        # 100..1000: p50 = 550 (midpoint), p95 = 955 (linear interpolation)
        values = [float(v) for v in range(100, 1001, 100)]
        assert percentile(values, 50) == pytest.approx(550.0)
        assert percentile(values, 95) == pytest.approx(955.0)

    def test_single_value(self):
        assert percentile([123.0], 50) == 123.0
        assert percentile([123.0], 95) == 123.0

    def test_order_independent(self):
        assert percentile([300.0, 100.0, 200.0], 50) == 200.0

    def test_empty_raises(self):
        with pytest.raises(ValueError):
            percentile([], 50)

    def test_summary_uses_percentiles(self):
        report = make_report([float(v) for v in range(100, 1001, 100)])
        v2v = report.summary()["voice_to_voice"]
        assert v2v == {"p50_ms": 550.0, "p95_ms": 955.0, "n": 10}


# ---------------------------------------------------------------------------
# JSON schema stability (the Phase 5 contract)
# ---------------------------------------------------------------------------


class TestSchemaStability:
    def test_exact_five_stage_names(self):
        data = make_report([500.0]).to_dict()
        assert tuple(data["summary"].keys()) == STAGE_NAMES
        assert STAGE_NAMES == (
            "vad_stop",
            "stt_final",
            "llm_ttft",
            "tts_first_audio",
            "voice_to_voice",
        )

    def test_top_level_contract_keys(self):
        data = make_report([500.0]).to_dict()
        assert data["schema_version"] == SCHEMA_VERSION == 1
        assert data["config"]["arm"] == CONFIG["arm"]
        assert data["generated_at"] == "2026-07-05T00:00:00Z"
        for key in ("anchors", "turns", "summary", "first_bot_speech_ms"):
            assert key in data

    def test_anchors_present_for_all_stages(self):
        data = make_report([500.0]).to_dict()
        assert set(data["anchors"].keys()) == set(STAGE_NAMES)

    def test_anchors_must_cover_all_stages(self):
        with pytest.raises(ValueError):
            Report(config=CONFIG, anchors={"voice_to_voice": "only one"})

    def test_populated_stage_shape(self):
        data = make_report([500.0, 700.0], vad_values=[400.0, 600.0]).to_dict()
        stats = data["summary"]["vad_stop"]
        assert set(stats.keys()) == {"p50_ms", "p95_ms", "n"}
        assert stats["n"] == 2

    def test_json_serializable(self):
        json.dumps(make_report([500.0]).to_dict())

    def test_round_trip_from_dict(self):
        original = make_report([500.0, 900.0], vad_values=[400.0, 800.0])
        original.first_bot_speech_ms = 1234.5
        restored = Report.from_dict(json.loads(json.dumps(original.to_dict())))
        assert restored.summary() == original.summary()
        assert restored.first_bot_speech_ms == 1234.5
        assert restored.config == original.config

    def test_from_dict_rejects_wrong_schema_version(self):
        data = make_report([500.0]).to_dict()
        data["schema_version"] = 99
        with pytest.raises(ValueError, match="schema_version"):
            Report.from_dict(data)

    def test_from_dict_rejects_missing_keys(self):
        data = make_report([500.0]).to_dict()
        del data["anchors"]
        with pytest.raises(ValueError, match="anchors"):
            Report.from_dict(data)


# ---------------------------------------------------------------------------
# Null-stage handling (Flux arm: vad_stop is null, never missing)
# ---------------------------------------------------------------------------


class TestNullStageHandling:
    def test_unpopulated_stage_is_null_not_missing(self):
        # No vad_stop samples at all (the Flux case): the key must be present
        # with a null value — silently omitting it would break consumers.
        data = make_report([500.0, 600.0], anchors=FLUX_ANCHORS).to_dict()
        assert "vad_stop" in data["summary"]
        assert data["summary"]["vad_stop"] is None

    def test_null_survives_json_round_trip(self):
        raw = json.dumps(make_report([500.0], anchors=FLUX_ANCHORS).to_dict())
        assert json.loads(raw)["summary"]["vad_stop"] is None

    def test_flux_anchor_explains_the_null(self):
        assert "Flux" in FLUX_ANCHORS["vad_stop"]
        assert "NULL" in FLUX_ANCHORS["vad_stop"]

    def test_empty_report_all_stages_null(self):
        data = make_report([]).to_dict()
        assert all(data["summary"][stage] is None for stage in STAGE_NAMES)


# ---------------------------------------------------------------------------
# D-13: threshold verdicts are informational — rendering NEVER exits
# ---------------------------------------------------------------------------


class TestInformationalVerdicts:
    def _render(self, report: Report) -> str:
        from rich.console import Console

        buf = io.StringIO()
        report.render(console=Console(file=buf, width=120))
        return buf.getvalue()

    def test_over_both_thresholds_renders_without_raising(self):
        # p50 and p95 both far beyond the 800ms/1200ms marks: must complete
        # without raising and without sys.exit (D-13).
        report = make_report([2000.0, 2500.0, 3000.0])
        output = self._render(report)  # SystemExit would propagate and fail here
        assert "voice_to_voice" in output

    def test_over_thresholds_gets_warning_mark(self):
        assert make_report([2000.0, 2500.0]).verdict_mark() == "⚠"

    def test_under_thresholds_gets_check_mark(self):
        assert make_report([500.0, 600.0, 700.0]).verdict_mark() == "✓"

    def test_empty_report_renders_and_warns(self):
        report = make_report([])
        assert report.verdict_mark() == "⚠"
        self._render(report)

    def test_render_returns_none_no_exit_code_signal(self):
        # The render path must not smuggle an exit signal back to callers.
        from rich.console import Console

        assert make_report([9999.0]).render(console=Console(file=io.StringIO())) is None


# ---------------------------------------------------------------------------
# Comparison table (the A/B diff instrument for plan 01-04)
# ---------------------------------------------------------------------------


class TestComparisonTable:
    def test_two_way_compare_renders(self):
        from rich.console import Console

        a = make_report([500.0, 600.0])
        b = make_report([2000.0, 2600.0])  # exceeds thresholds: still exit-free
        table = build_comparison_table([("arm-a", a), ("arm-b", b)])
        buf = io.StringIO()
        Console(file=buf, width=160).print(table)
        output = buf.getvalue()
        assert "voice_to_voice" in output

    def test_compare_requires_two(self):
        with pytest.raises(ValueError):
            build_comparison_table([("only", make_report([500.0]))])
