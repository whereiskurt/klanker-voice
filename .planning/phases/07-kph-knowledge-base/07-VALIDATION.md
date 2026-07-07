---
phase: 07
slug: kph-knowledge-base
status: draft
nyquist_compliant: true
wave_0_complete: false
created: 2026-07-07
---

# Phase 07 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution. Regenerated
> 2026-07-07 after the Amendment 3–5 re-plan (5 plans / 3 waves). Reflects the LOCAL
> retrieval path (Amendment 3) and direct code indexing (Amendment 5) — supersedes the
> stale `07-RESEARCH.md § Open Questions` (Q1 "no retrieval", Q3 "pacing in 07-04").

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | pytest 9.1.1 + pytest-asyncio 1.4.0 (`apps/voice/pyproject.toml`), plus `pipecat-ai[evals]` scenario harness (`apps/voice/scenarios/*.yaml` + `klanker_voice.harness.judge.judge_factory`); `kv` Go build for the CLI dispatcher |
| **Config file** | `apps/voice/pyproject.toml` `[tool.pytest.ini_options]` (`testpaths = ["tests"]`); scenario YAMLs in `apps/voice/scenarios/` |
| **Quick run command** | `cd apps/voice && pytest tests/ -x -q` |
| **Full suite command** | `cd apps/voice && pytest tests/ -q` + full `scenarios/kph_*.yaml` set + `cd kv && go build ./...` |
| **Estimated runtime** | ~30s unit; scenario set adds LLM-judge latency |

---

## Sampling Rate

- **After every task commit:** `cd apps/voice && pytest tests/test_knowledge_*.py -x -q` (fast unit checks)
- **After every plan wave:** Full `pytest tests/ -q` + the new knowledge scenario set
- **Before `/gsd-verify-work`:** Full suite + full scenario set green; `cache_read_input_tokens > 0` verified; `cd kv && go build ./...` green
- **Max feedback latency:** ~30s (unit)

---

## Per-Task Verification Map

| Plan | Wave | Requirement | Secure Behavior (STRIDE ref) | Test Type | Automated Command | File Exists | Status |
|------|------|-------------|------------------------------|-----------|-------------------|-------------|--------|
| 07-01 (router + two-block cache) | 1 | PIPE-10, PIPE-06, PIPE-07 | Advisory do-not-say lint flags (not blocks); public-mic PG-13 guardrail must_have | unit | `pytest tests/test_knowledge_router.py -x -q` | ❌ W0 | ⬜ pending |
| 07-01 (pack + style layer) | 1 | PIPE-10, PIPE-06 | `system[0]` byte-identity (cache prefix stable, ≥4096 tok) | unit | `pytest tests/test_knowledge_pack.py -x -q` | ❌ W0 | ⬜ pending |
| 07-02 (FTS5/BM25 retrieval — DEPTH) | 2 | PIPE-10, PIPE-07 | Retrieved chunks inject into UNCACHED `system[1]` only | unit | `pytest tests/test_knowledge_retrieval.py -x -q` | ❌ W0 | ⬜ pending |
| 07-03 (defcon/meshtk packs, multi-topic) | 2 | PIPE-10 | Multi-topic discrimination + overlap guard; append-only vs 07-01 schema | unit | `pytest tests/test_knowledge_router_multitopic.py -x -q` | ❌ W0 | ⬜ pending |
| 07-04 (refresh workflow) | 3 | PIPE-10, PIPE-07 | Manifest-only sources (no auto-discovery); offline; skip-on-missing | unit | `pytest tests/test_knowledge_refresh.py -x -q` | ❌ W0 | ⬜ pending |
| 07-04 (kv dispatcher) | 3 | PIPE-10 | `kv knowledge refresh` is a thin dispatcher over `refresh_knowledge.py` | build | `cd kv && go build ./... && grep -Rq NewKnowledgeCmd internal/app/cmd/root.go` | ❌ W0 | ⬜ pending |
| 07-05 (pacing + steering) | 3 | PIPE-10, PIPE-06 | Pacing PREPENDS to `system[1]`, never `system[0]` (byte-identity test) | unit | `pytest tests/test_knowledge_pacing.py -x -q` | ❌ W0 | ⬜ pending |
| 07-05 (benchmark evals) | 3 | PIPE-10 | Retrieval DEPTH/coverage; router accuracy; crude-humor guard (neutral opener stays PG-13); honest-unknowns (D-12) | scenario (LLM-judge) | `python -c "import yaml,glob; [yaml.safe_load(open(f)) for f in glob.glob('scenarios/kph_*.yaml')]"` then run harness | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

**Cache-verification scenario (ROADMAP criterion 1):** a `scenarios/kph_cache_verify.yaml` asserts `cache_read_input_tokens > 0` on the second in-topic turn (owned by 07-01/07-05 scenario set).

---

## Wave 0 Requirements

- [ ] `tests/test_knowledge_router.py` — router topic-selection + low-confidence fallback (PIPE-10) — 07-01 RED-gates on this
- [ ] `tests/test_knowledge_pack.py` — pack assembly + `system[0]` byte-identity / ≥4096-tok cache prefix — 07-01 RED-gates on this
- [ ] `tests/test_knowledge_retrieval.py` — FTS5/BM25 chunk+index+query, `system[1]`-only injection (07-02)
- [ ] `tests/test_knowledge_router_multitopic.py` — multi-topic discrimination + overlap guard (07-03)
- [ ] `tests/test_knowledge_refresh.py` — manifest-driven, skip-on-missing, offline refresh — 07-04 RED-gates on this
- [ ] `tests/test_knowledge_pacing.py` — time-aware pacing into `system[1]`, `system[0]` byte-identity (07-05)
- [ ] `scenarios/kph_*.yaml` — benchmark set incl. `kph_crude_humor_guard.yaml`, `kph_retrieval_depth.yaml`, `kph_cache_verify.yaml`, per-topic knowledge scenarios (07-05)

*Existing infrastructure (pytest + pipecat evals harness, 168+ tests) covers the framework; the above are the new stubs.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Refresh git-diff human review (D-09) | PIPE-10 | Regenerated digests must be human-inspected before commit — the review gate is a human judgment, not an assertion | Run `make -C apps/voice knowledge`; inspect the resulting git diff + advisory-lint flags before committing |
| Live latency feel (TTFT within budget with the larger pack) | PIPE-10 | Voice-to-voice "slick feel" under real speech is perceptual; automated TTFT numbers are necessary but not sufficient | Drive a live session, confirm the ack masks the deep-turn retrieval/prompt cost |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies (07-01: 6 · 07-02: 4 · 07-03: 4 · 07-04: 6 · 07-05: 4)
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references (6 test modules + scenario set)
- [x] No watch-mode flags
- [x] Feedback latency < 30s (unit)
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** approved 2026-07-07
