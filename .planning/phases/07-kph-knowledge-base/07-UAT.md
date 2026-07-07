---
status: testing
phase: 07-kph-knowledge-base
source: [07-VERIFICATION.md]
started: 2026-07-07T19:35:00Z
updated: 2026-07-07T19:35:00Z
---

## Current Test

number: 1
name: Live mobile conversation — km/defcon/meshtk answers in Kurt's voice with barge-in
expected: |
  On a real phone against voice.klankermaker.ai: single-tap SSO + instant greeting,
  then ask about km (deep answer surfaces long-tail detail), defcon.run.34, and meshtk;
  KPH sounds like Kurt (transcript-distilled style), stays PG-13 to a neutral opener,
  and barge-in interrupts cleanly. Talk twice in the same topic to feel the cached prefill.
awaiting: user response

## Tests

### 1. Live mobile conversation (the headline morning test)
expected: Single-tap SSO + instant greeting; km/defcon/meshtk answered correctly and with depth; Kurt's voice + humor; PG-13 to a neutral opener; clean barge-in; second in-topic turn feels fast (cache warm).
result: [pending]

### 2. Full live-audio benchmark eval set (ROADMAP criteria 2 & 4 + persona guardrail)
expected: |
  `uv sync --group dev` then `uv run python bot.py -t eval` +
  `uv run pipecat eval run scenarios/kph_*.yaml scenarios/memory.yaml --bot-url ws://localhost:7860`.
  High pass rate across km/defcon/meshtk correctness, retrieval depth/coverage, router accuracy,
  honest-unknowns, tour-mode, and the crude-humor guard; record the router-accuracy number.
result: [pending]

### 3. Live knowledge refresh (ROADMAP criterion 3, D-09)
expected: |
  `make -C apps/voice knowledge` (or `kv knowledge refresh`) for real with ANTHROPIC_API_KEY set
  and the local km/defcon.run.34/meshtk checkouts present; review the `knowledge/` git diff (D-09),
  triage any advisory-lint flags, and commit if clean.
result: [pending]

## Summary

total: 3
passed: 0
issues: 0
pending: 3
skipped: 0
blocked: 0

## Gaps
