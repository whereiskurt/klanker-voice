---
phase: 1
slug: local-pipeline-latency-harness
status: ready
nyquist_compliant: true
wave_0_complete: false
created: 2026-07-04
updated: 2026-07-04
---

# Phase 1 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.
> Populated from 01-RESEARCH.md "Validation Architecture" + the task/verify map across plans 01-01..01-05.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | pytest + pytest-asyncio (greenfield — installed by plan 01-01 Task 3, dev dependency group) |
| **Config file** | `apps/voice/pyproject.toml` `[tool.pytest.ini_options]` (created in plan 01-01 Task 3) |
| **Quick run command** | `cd apps/voice && uv run pytest tests/ -x -q` |
| **Full suite command** | `cd apps/voice && uv run pytest tests/ -q` then all five eval scenarios via `uv run pipecat eval run scenarios/*.yaml --bot-url ws://localhost:7860` (requires `uv run python bot.py -t eval` running) |
| **Estimated runtime** | unit suite <30s; full eval suite ~3–5 min (live vendor APIs) |

---

## Sampling Rate

- **After every task commit:** `cd apps/voice && uv run pytest tests/ -x -q` (<30s — unit tests only)
- **After every plan wave:** unit suite + at least `scenarios/greeting.yaml` against a running `bot.py -t eval`
- **Before `/gsd-verify-work`:** full unit suite green + all five eval scenarios green + one harness JSON artifact per A/B arm recorded in `docs/TUNING.md`
- **Max feedback latency:** <30s (unit sampling)

---

## Per-Task Verification Map

Commands abbreviated; the authoritative `<automated>` command lives in each plan's task. All commands run from `apps/voice/` unless noted.

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| 1-01-01 | 01 | 1 | PIPE-07 | T-1-01 / T-1-02 | `.env` gitignored + mode 600; script never echoes secrets | smoke (script + git check-ignore + stat + key-line count) | `test -x scripts/bootstrap_env.sh` + `git check-ignore -q apps/voice/.env` + mode/count checks | ✅ self-contained | ⬜ pending |
| 1-01-02 | 01 | 1 | PIPE-07 | T-1-SC | only the audited package set may be installed | checkpoint:human-verify (blocking-human legitimacy gate) | — (never auto-approved) | — | ⬜ pending |
| 1-01-03 | 01 | 1 | PIPE-07 | T-1-SC | uv.lock committed; exact audited set only | smoke (import of seven pinned 1.5.0 surfaces + version check) | `uv run python -c "import pipecat, ...; print(pipecat.__version__)"` matching 1.5.x | ✅ self-contained | ⬜ pending |
| 1-02-01 | 02 | 2 | PIPE-04, PIPE-06 | T-1-04 | config rejects credential-looking fields, bad providers, out-of-range knobs | unit | `uv run pytest tests/test_config.py -x -q` | ❌ W0 (test written in-task) | ⬜ pending |
| 1-02-02 | 02 | 2 | PIPE-04 | — | flux + explicit turn strategy raises ValueError | unit | `uv run pytest tests/test_factories.py -x -q` | ❌ W0 (test written in-task) | ⬜ pending |
| 1-02-03 | 02 | 2 | PIPE-03, PIPE-06, PIPE-07 | T-1-05 | N/A (localhost-only accepted) | smoke (bounded poll of localhost:7860) + human-check | poll-until-ready loop, then process-group kill | ✅ self-contained | ⬜ pending |
| 1-03-01 | 03 | 3 | PIPE-05 | — | N/A | unit (percentile math, schema stability, null-stage, D-13 no-exit rule) | `uv run pytest tests/test_report.py -x -q` | ❌ W0 (test written in-task) | ⬜ pending |
| 1-03-02 | 03 | 3 | PIPE-05, PIPE-07 | — | judge stays on the three keys (no 4th vendor) | smoke (CLI --help + judge_factory import) | `uv run python -m klanker_voice.harness --help` + import check | ✅ self-contained | ⬜ pending |
| 1-03-03 | 03 | 3 | PIPE-02, PIPE-03, PIPE-06 | T-1-07 | artifacts/ gitignored; no env values in JSON | eval scenarios (5 named, recorded audio through real input path) + human-check | `uv run pipecat eval run scenarios/*.yaml --bot-url ws://localhost:7860 --record-dir artifacts/eval-recordings` | ❌ W0 (scenarios written in-task) | ⬜ pending |
| 1-04-01 | 04 | 4 | PIPE-04 | T-1-09 | committed artifacts carry config metadata only, never env values | harness A/B (3 arms, identical scenario suite) | `uv run python -m klanker_voice.harness compare` over docs/tuning/arm-{a,b,c}.json | ✅ self-contained | ⬜ pending |
| 1-04-02 | 04 | 4 | PIPE-01, PIPE-04 | — | N/A | doc greps + config tests + scenario re-run + p95 escalation rule | `grep` TUNING.md content + `pytest tests/test_config.py` + greeting/bargein_mid eval re-run | ✅ self-contained | ⬜ pending |
| 1-05-01 | 05 | 5 | PIPE-06 | T-1-11 | API key never printed or persisted to manifest | smoke (3 renders + manifest exist) | `uv run python scripts/audition.py` + file-count/manifest checks | ✅ self-contained | ⬜ pending |
| 1-05-02 | 05 | 5 | PIPE-06 | — | — | checkpoint:decision (D-02 voice pick by ear) | — | — | ⬜ pending |
| 1-05-03 | 05 | 5 | PIPE-01, PIPE-06 | — | N/A | config assert + unit + live greeting eval with chosen voice | `load_config` voice_id assert + `pytest tests/test_config.py` + greeting.yaml eval | ✅ self-contained | ⬜ pending |
| 1-05-04 | 05 | 5 | PIPE-01..PIPE-07 | — | — | checkpoint:human-verify (phase gate: conversational feel) | — | — | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

Greenfield project — the test infrastructure is itself a Phase 1 deliverable, folded into the plans (each ❌ W0 entry above is created in the same task that its `<automated>` verify runs, so no task ever depends on a test file created later):

- [ ] Framework install: `uv add --group dev pytest pytest-asyncio` — plan 01-01 Task 3
- [ ] `apps/voice/pyproject.toml` `[tool.pytest.ini_options]` (testpaths, asyncio_mode) — plan 01-01 Task 3
- [ ] `apps/voice/tests/conftest.py` — shared fixtures (sample pipeline.toml in tmp_path, invalid-variant mutator) — plan 01-02 Task 1
- [ ] `apps/voice/tests/test_config.py` — covers PIPE-04 (TOML parse/validation, secret-rejection) — plan 01-02 Task 1
- [ ] `apps/voice/tests/test_factories.py` — covers PIPE-04 (arm construction incl. Flux no-strategies rule) — plan 01-02 Task 2
- [ ] `apps/voice/tests/test_report.py` — covers PIPE-05 (p50/p95 math, JSON schema stability, D-13 no-exit rule) — plan 01-03 Task 1
- [ ] `apps/voice/scenarios/*.yaml` (greeting, memory, bargein_early/mid/monologue) — covers PIPE-02/03/06 — plan 01-03 Task 3

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Conversational feel, greet-first timing, voice output in both run modes | PIPE-07, D-04, D-08 | Needs human ears; no automated check hears audio | Plan 01-02 Task 3 human-check (webrtc page + console.py with headphones) |
| Barge-in stop-latency feel (audio abruptness at interruption) | PIPE-02 | Judge checks text coherence; audio abruptness needs ears | Plan 01-03 Task 3 human-check (play recorded bargein_mid session from artifacts/eval-recordings) |
| Voice audition winner | PIPE-06, D-02 | Locked user-in-the-loop decision: picked by ear | Plan 01-05 Task 2 checkpoint:decision (three renders + manifest) |
| Final conversational-feel sign-off | PIPE-01..07 | The phase's "slick" criterion is a human judgment | Plan 01-05 Task 4 checkpoint:human-verify (six-step walkthrough) |

---

## Validation Sign-Off

- [x] All auto tasks have `<automated>` verify (checkpoint tasks are human by design)
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references (in-task creation, verified in the same task's automated command)
- [x] No watch-mode flags
- [x] Feedback latency <30s for unit sampling
- [x] `nyquist_compliant: true` set in frontmatter
- [x] Escalation guard: if the winning configuration's measured voice-to-voice p95 exceeds 1.2s, plan 01-04 Task 2 pauses for an explicit user tuning/scope decision before the phase can close (D-13 preserved — no nonzero exits, no CI gate)

**Approval:** approved 2026-07-04
