---
phase: 01-local-pipeline-latency-harness
plan: 01
subsystem: infra
tags: [uv, python-3.12, pipecat, ssm, dotenv, pytest, supply-chain]

# Dependency graph
requires: []
provides:
  - "apps/voice uv project (Python 3.12) with pipecat-ai 1.5.0 pinned via ~=1.5.0 and committed uv.lock"
  - "make -C apps/voice env: SSM /kmv/bootstrap/* -> apps/voice/.env (DEEPGRAM_API_KEY, ANTHROPIC_API_KEY, ELEVENLABS_API_KEY), mode 600, gitignored"
  - "Machine prerequisites installed: uv 0.11.26, portaudio 19.7.0 (brew)"
  - "src layout: klanker_voice package importable under uv run; pytest configured (testpaths=tests, asyncio_mode=auto)"
  - "Verified imports of all seven Phase-1 pipecat 1.5.0 surfaces (deepgram stt+flux, anthropic llm, elevenlabs tts, silero vad, user_bot_latency_observer, local audio transport)"
affects: [01-02, 01-03, 01-04, 01-05, phase-4-deploy]

# Tech tracking
tech-stack:
  added:
    - "pipecat-ai[webrtc,deepgram,anthropic,runner]~=1.5.0 (main)"
    - "pipecat-ai[local,evals]~=1.5.0 (dev group only — portaudio/pyaudio never reaches prod images)"
    - "pytest 9.1.1 + pytest-asyncio 1.4.0 (dev group)"
  patterns:
    - "Secrets flow SSM SecureString -> bootstrap_env.sh -> gitignored .env (D-10); never TOML, never git"
    - "Laptop-only extras ([local], [evals]) live in the dev dependency group"

key-files:
  created:
    - .gitignore
    - apps/voice/.gitignore
    - apps/voice/Makefile
    - apps/voice/scripts/bootstrap_env.sh
    - apps/voice/pyproject.toml
    - apps/voice/uv.lock
    - apps/voice/.python-version
    - apps/voice/src/klanker_voice/__init__.py
    - apps/voice/tests/test_smoke.py
  modified: []

key-decisions:
  - "uv init --bare (no placeholder main.py to remove); hatchling src layout with tool.uv package=true so klanker_voice installs editable"
  - "Added tests/test_smoke.py because pytest exits 5 (not 0) on empty collection — plan's acceptance criterion assumed 0"
  - "PIPE-07 not marked complete in REQUIREMENTS.md: plan 01-02 also claims PIPE-07 and runs in this wave; orchestrator owns the shared-file write"

patterns-established:
  - "Key bootstrap: make -C apps/voice env is the only sanctioned path to .env; script refuses xtrace, writes atomically with umask 077/chmod 600, never echoes values"
  - "Supply-chain: installs restricted to the user-approved audited set; uv.lock committed (T-1-SC)"

requirements-completed: [PIPE-07]

coverage:
  - id: D1
    description: "One-command SSM key bootstrap: make -C apps/voice env writes .env with the three provider keys, mode 600, no secrets echoed, idempotent"
    requirement: "PIPE-07"
    verification:
      - kind: manual_procedural
        ref: "make -C apps/voice env && stat -f '%Lp' apps/voice/.env == 600 && grep -c 'API_KEY=' apps/voice/.env == 3 && git check-ignore apps/voice/.env"
        status: pass
    human_judgment: false
  - id: D2
    description: "Pinned pipecat-ai 1.5.0 tree installed under uv-managed Python 3.12, locked (uv.lock committed), all seven Phase-1 module surfaces import"
    requirement: "PIPE-07"
    verification:
      - kind: integration
        ref: "cd apps/voice && uv run python -c 'import pipecat, pipecat.services.deepgram.flux, pipecat.services.anthropic.llm, pipecat.services.elevenlabs.tts, pipecat.audio.vad.silero, pipecat.observers.user_bot_latency_observer; print(pipecat.__version__)' -> 1.5.0"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_smoke.py (uv run pytest tests/ -q -> 2 passed)"
        status: pass
    human_judgment: false
  - id: D3
    description: "Package legitimacy gate (T-1-SC): user explicitly approved the six-package audited set before any install; only that set was installed"
    verification:
      - kind: other
        ref: "Task 2 checkpoint:human-verify gate=blocking-human — user replied 'approved' via orchestrator"
        status: pass
    human_judgment: false

# Metrics
duration: 12min
completed: 2026-07-04
status: complete
---

# Phase 1 Plan 01: Toolchain, Key Bootstrap & Pinned Pipecat Tree Summary

**SSM-to-.env key bootstrap (make env, D-10) plus a locked pipecat-ai 1.5.0 uv project on Python 3.12 with all seven Phase-1 API surfaces import-verified — installs gated behind a user-approved supply-chain checkpoint**

## Performance

- **Duration:** ~12 min active execution (plus checkpoint wait for package-legitimacy approval)
- **Started:** 2026-07-05T00:16:47Z
- **Completed:** 2026-07-05T00:28:00Z
- **Tasks:** 3/3 (Task 2 was the blocking-human legitimacy gate — approved by user)
- **Files modified:** 9

## Accomplishments

- `make -C apps/voice env` fetches `/kmv/bootstrap/{deepgram,anthropic,elevenlabs}_api_key` from SSM (klanker-application profile, us-east-1) and writes `apps/voice/.env` with exactly three keys, mode 600 — idempotent, atomic, no secret ever echoed or committed
- Machine prerequisites installed: uv 0.11.26 and portaudio 19.7.0 via brew (Pitfall 5 — pyaudio builds in the dev group now succeed)
- apps/voice uv project (Python 3.12 pinned) with `pipecat-ai[webrtc,deepgram,anthropic,runner]~=1.5.0` in main deps and `pipecat-ai[local,evals]` + pytest + pytest-asyncio confined to the dev group; `uv.lock` committed (T-1-SC)
- Import smoke passed: pipecat 1.5.0 with deepgram stt+flux, anthropic llm, elevenlabs tts, silero vad, user_bot_latency_observer, and local audio transport all importing cleanly
- Supply-chain gate honored: zero packages installed before the user approved the audited six-package set at the blocking checkpoint

## Task Commits

Each task was committed atomically:

1. **Task 1: Machine prerequisites, repo scaffold, and SSM key bootstrap (D-10)** - `fdf93c0` (feat)
2. **Task 2: Package legitimacy gate before first uv add** - checkpoint (no commit; user approved)
3. **Task 3: uv project init, pinned installs, and import smoke** - `48bc0a4` (feat)

## Files Created/Modified

- `.gitignore` / `apps/voice/.gitignore` - ignore .env, artifacts/, .venv, __pycache__ (T-1-01)
- `apps/voice/scripts/bootstrap_env.sh` - SSM -> .env bootstrap; umask 077, chmod 600, xtrace refusal, fail-fast atomic write, apps/voice CWD containment (T-1-02)
- `apps/voice/Makefile` - `env` target wrapping the bootstrap script
- `apps/voice/pyproject.toml` - pinned deps, hatchling src layout, pytest ini_options
- `apps/voice/uv.lock` - committed locked resolution of the audited set
- `apps/voice/.python-version` - 3.12 pin
- `apps/voice/src/klanker_voice/__init__.py` - empty package init (imports resolve; later plans fill it)
- `apps/voice/tests/test_smoke.py` - pipecat 1.5.x + klanker_voice import smoke

## Decisions Made

- Used `uv init --bare` so no placeholder `main.py` is generated (plan said remove it if created)
- src layout via hatchling `packages = ["src/klanker_voice"]` + `tool.uv package = true` — klanker_voice installs editable into the venv
- Did NOT mark PIPE-07 complete in REQUIREMENTS.md: plan 01-02 (same wave) also claims PIPE-07, and shared-file writes belong to the orchestrator; `requirements-completed` frontmatter carries the linkage

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Plan's acceptance criterion assumed empty pytest collection exits 0**
- **Found during:** Task 3 (uv project init, pinned installs, and import smoke)
- **Issue:** `uv run pytest tests/ -q` on an empty tests dir exits 5 ("no tests collected"), not 0 as the acceptance criterion stated
- **Fix:** Added `tests/test_smoke.py` with two import smoke tests (pipecat 1.5.x version pin, klanker_voice importability) — baseline is genuinely green and doubles as an install regression test
- **Files modified:** apps/voice/tests/test_smoke.py
- **Verification:** `uv run pytest tests/ -q` -> 2 passed, exit 0
- **Committed in:** 48bc0a4 (Task 3 commit)

---

**Total deviations:** 1 auto-fixed (1 bug in plan assumption)
**Impact on plan:** Minimal — one 10-line smoke test file beyond the planned artifact list. No scope creep.

## Issues Encountered

None — SSM fetches succeeded on first try (no auth gate needed), all installs resolved cleanly, all imports passed.

## Known Stubs

- `apps/voice/src/klanker_voice/__init__.py` is intentionally empty — the plan specifies an empty package init so imports resolve; plans 01-02/01-03 populate the package (config, factories, pipeline, harness).

## User Setup Required

None - no external service configuration required (AWS klanker-application profile was already authenticated; SSM parameters pre-existed).

## Next Phase Readiness

- Plans 01-02..01-05 can `uv run` against the locked 1.5.0 tree immediately; all seven module surfaces they depend on are import-verified
- `.env` regeneration is one command; dev group has pyaudio working (terminal mode ready)
- PIPE-07 completion is shared with 01-02 (bot actually running on three keys) — orchestrator should mark it when both plans land

## Self-Check: PASSED

All 9 created files exist on disk; both task commits (fdf93c0, 48bc0a4) present in git log.

---
*Phase: 01-local-pipeline-latency-harness*
*Completed: 2026-07-04*
