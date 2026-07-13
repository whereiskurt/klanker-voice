---
phase: 11-voip-ms-telephony-local-asterisk-edge
plan: 07
subsystem: infra
tags: [telephony, asterisk, ari, sipp, docker-compose, entrypoint, integration-test]

# Dependency graph
requires:
  - phase: 11-voip-ms-telephony-local-asterisk-edge (plan 02)
    provides: "apps/voice/asterisk/ config set + docker-compose.yml dev harness"
  - phase: 11-voip-ms-telephony-local-asterisk-edge (plan 05)
    provides: "AsteriskCallController + ActiveCall registry, §16/§17 lifecycle"
  - phase: 11-voip-ms-telephony-local-asterisk-edge (plan 06)
    provides: "GateProcessor + gated controller wiring, §24 silent answer-gate (D-05)"
provides:
  - "apps/voice/src/klanker_voice/telephony/__main__.py — standalone ARI controller entrypoint, no FastAPI/browser coupling (D-08)"
  - "apps/voice/configs/telephony.toml — harness config, [telephony] enabled = true"
  - "apps/voice/tests/test_telephony_integration.py — deterministic fake-media CI integration test (D-07)"
  - "apps/voice/asterisk/sipp/gate-pass.xml + fixtures/README.md — SIPp scenario for the semi-automated local run"
  - "apps/voice/asterisk/docker-compose.yml sipp service (profiles: [integration])"
  - "apps/voice/asterisk/README.md — full manual §19-C softphone proof recipe + honest CI-vs-manual boundary"
affects: [12, 13, 14]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Standalone-process entrypoint pattern: telephony/__main__.py mirrors server.py's config-load shape (load_dotenv + load_config + feature configs) but never imports FastAPI/webrtc — process isolation enforced structurally, not just documented (D-08)"
    - "Deterministic fake-media CI tier + a separate, explicitly non-CI, human-perceptual manual proof — the same honest split established by 11-02's docker-daemon-unavailable disclosure and D-07's own text"

key-files:
  created:
    - apps/voice/src/klanker_voice/telephony/__main__.py
    - apps/voice/configs/telephony.toml
    - apps/voice/tests/test_telephony_integration.py
    - apps/voice/asterisk/sipp/gate-pass.xml
    - apps/voice/asterisk/sipp/fixtures/README.md
  modified:
    - apps/voice/src/klanker_voice/telephony/controller.py
    - apps/voice/asterisk/docker-compose.yml
    - apps/voice/asterisk/README.md

key-decisions:
  - "Both D-08-equivalent run commands wired to the same main(): python -m klanker_voice.telephony (this module) and python -m klanker_voice.telephony.controller (controller.py's own __main__ guard importing this module's main()) — avoids picking a single literal reading of D-08's phrasing where both are defensible; README documents the .controller form as canonical."
  - "§19-C live-softphone proof deliberately DEFERRED, not fabricated. The human explicitly decided to defer the live run (no Docker daemon in this execution sandbox); the README recipe (8 steps) and this SUMMARY both record it as an outstanding human-verify item rather than claiming a pass that never happened."
  - "The two known live-stack prerequisites 11-02 flagged (ARI loopback-vs-published-port mismatch; .conf ${VAR} placeholders are documentation-only, not shell-substituted) are carried forward into the manual recipe as explicit step-3 blockers with two documented resolution options, not silently assumed away."

patterns-established:
  - "Manual perceptual proof sections in harness READMEs must explicitly state what the adjacent CI test does and does NOT cover — prevents a false 'CI covers X' claim propagating into later phase verification."

requirements-completed: [D-08, D-07]

coverage:
  - id: D1
    description: "Standalone telephony entrypoint runs the ARI controller as its own process (loads cfg + telephony_cfg, constructs AriClient + AsteriskCallController, connects events WS, dispatches events) with zero FastAPI/browser-server coupling (D-08)"
    requirement: "D-08"
    verification:
      - kind: unit
        ref: "KLANKER_PIPELINE_CONFIG=configs/telephony.toml uv run python -c \"import klanker_voice.telephony.__main__ as m; from klanker_voice.telephony.config import load_telephony_config; assert load_telephony_config().enabled is True\" -> entrypoint import + harness config OK"
        status: pass
      - kind: other
        ref: "grep -rn 'fastapi\\|server import\\|webrtc' apps/voice/src/klanker_voice/telephony/__main__.py -> no matches"
        status: pass
      - kind: other
        ref: "git diff --stat apps/voice/server.py apps/voice/src/klanker_voice/webrtc.py -> empty"
        status: pass
    human_judgment: false
  - id: D2
    description: "Deterministic CI integration test drives SIP INVITE -> Asterisk -> fake Klanker media transport and asserts the §16/§17 lifecycle + §24 gate behaviors without live provider keys (D-07)"
    requirement: "D-07"
    verification:
      - kind: integration
        ref: "apps/voice/tests/test_telephony_integration.py -> 4/4 pass (env -i clean env, no leaked provider keys)"
        status: pass
      - kind: unit
        ref: "full project suite -> 403 passed, 53 skipped, 0 failed"
        status: pass
    human_judgment: false
  - id: D3
    description: "The §19-C manual softphone proof (real pipeline, greeting-not-clipped, converse, interrupt, clean hangup, through the gate, fail-closed, no leaks) is documented in the harness README with an honest CI-vs-manual boundary and surfaced as an outstanding human checkpoint"
    verification: []
    human_judgment: true
    rationale: "This is the one perceptual exit criterion (§19-C) that cannot be judged by any automated fake-media test — it requires a live Docker/Asterisk/softphone stack, real provider API calls, and a human ear judging clipping/barge-in feel. The human explicitly decided to defer the live run rather than fabricate a pass; recorded as an outstanding human-verify item in STATE.md, not claimed complete here."

# Metrics
duration: ~35min (across two agent sessions, checkpoint pause between)
completed: 2026-07-12
status: complete
---

# Phase 11 Plan 07: Standalone Telephony Entrypoint + Live SIP Integration Summary

**Standalone `python -m klanker_voice.telephony.controller` process entrypoint (zero FastAPI coupling, D-08) plus a deterministic fake-media CI integration test (D-07) and a fully documented — but not yet human-run — manual §19-C softphone proof recipe.**

## Performance

- **Duration:** ~35 min total (Tasks 1-2 in the prior session, Task 4 + SUMMARY in this continuation)
- **Started:** 2026-07-12 (prior session)
- **Completed:** 2026-07-12
- **Tasks:** 4 (3 auto tasks + 1 checkpoint)
- **Files modified:** 8

## Accomplishments
- `telephony/__main__.py` — the standalone ARI controller entrypoint: `load_dotenv(override=True)` → `load_config()` + `load_knowledge_config()` + `load_quota_config()` + `load_telephony_config()` → guard on `[telephony].enabled` → env-only `ASTERISK_ARI_*` credentials (raises `ConfigError` if user/pass unset, never logged) → construct `AriClient` + `AsteriskCallController` → register handlers → connect + run the events WebSocket. `controller.py` gained a matching `if __name__ == "__main__"` guard so both `python -m klanker_voice.telephony` and `python -m klanker_voice.telephony.controller` work identically. `configs/telephony.toml` is the harness config (`[telephony] enabled = true`, all base pipeline tables cloned from `pipeline.toml`).
- `tests/test_telephony_integration.py` — the CI-required deterministic fake-media artifact: reuses `test_telephony_lifecycle.py`'s `FakeAriClient`/`FakeRtpMediaSession`/`_build_controller` to drive one coherent end-to-end scenario per test (StasisStart → passphrase or DTMF gate-unlock → real `quota.start_gate` grant + `greet_now` → `ChannelDestroyed` → the single idempotent teardown) plus a fail-closed scenario. Runs green with zero provider keys (`env -i` clean env).
- `asterisk/sipp/gate-pass.xml` (SIPp INVITE→200→ACK→pcap-audio→BYE scenario) + `sipp/fixtures/README.md` (pcap recording recipe — fixtures deliberately NOT committed, D-09) + `docker-compose.yml` gains a `sipp` service under `profiles: [integration]` (never runs on a plain `up`), built from the official SIPp source with an explicitly flagged unverified-in-sandbox version pin.
- `asterisk/README.md`'s "Manual §19-C softphone proof" section: an 8-step recipe (fill secrets incl. the `${VAR}` substitution workaround → `docker compose up` → resolve the 11-02-flagged ARI-loopback-vs-published-port prerequisite via one of two documented options → run the controller with real ARI/PIN/passphrase env → register a SIP softphone → confirm silent-answer/unlock/greeting-not-clipped/converse/barge-in/clean-hangup → confirm fail-closed → confirm no leaks), plus an explicit "CI-vs-manual boundary" section stating exactly what the fake-media test proves and does not prove.

## Task Commits

Each task was committed atomically:

1. **Task 1: Standalone telephony entrypoint + harness config (D-08)** - `2dd1efa` (feat)
2. **Task 2: SIPp scenario + docker-compose sipp service + deterministic fake-media integration test (D-07)** - `057b4a3` (test)
3. **[checkpoint] Partial-progress record (paused at Task 3 checkpoint)** - `cc0ed2b` (docs)
4. **Task 4: Manual §19-C softphone proof recipe + CI-vs-manual boundary** - `0f54206` (docs)

**Plan metadata:** (this commit)

_Note: Task 3 is the plan's `checkpoint:human-verify` gate — see "Deviations from Plan" below for how it was resolved (deferred, not fabricated)._

## Files Created/Modified
- `apps/voice/src/klanker_voice/telephony/__main__.py` - standalone ARI controller entrypoint, no FastAPI/browser-server import
- `apps/voice/src/klanker_voice/telephony/controller.py` - added `if __name__ == "__main__"` guard importing `__main__.main()`
- `apps/voice/configs/telephony.toml` - harness config, `[telephony] enabled = true`
- `apps/voice/tests/test_telephony_integration.py` - 4 deterministic fake-media integration tests
- `apps/voice/asterisk/sipp/gate-pass.xml` - SIPp UAC scenario (INVITE/200/ACK/pcap-audio/BYE)
- `apps/voice/asterisk/sipp/fixtures/README.md` - pcap fixture recording recipe (fixtures not committed)
- `apps/voice/asterisk/docker-compose.yml` - added `sipp` service (`profiles: [integration]`)
- `apps/voice/asterisk/README.md` - full manual §19-C softphone proof recipe + CI-vs-manual boundary section

## Decisions Made
- Both `python -m klanker_voice.telephony` and `python -m klanker_voice.telephony.controller` are wired to the same `main()` — D-08's own phrasing supports either literal reading, so both work rather than picking one and leaving the other broken; README documents `.controller` as canonical.
- §19-C live-softphone proof is recorded as **deferred**, not fabricated. The human explicitly decided to defer the live run given no Docker daemon in this execution sandbox — the recipe is fully documented and ready to run, but has not actually been executed. Tracked as an outstanding human-verify item in `.planning/STATE.md`, mirroring how Phase 5 and Phase 7 tracked their own deferred consolidated live-verification passes.
- The two known live-stack prerequisites 11-02 flagged (ARI HTTP bindaddr is container-loopback-only so a host-run controller can't reach it through the published port; `.conf` `${VAR}` placeholders don't shell-substitute) are carried into the recipe as an explicit numbered step with two concrete resolution options (run the controller inside the compose network, or move the bindaddr to `0.0.0.0` paired with a host-loopback-scoped publish) rather than silently assumed resolved.

## Deviations from Plan

### Auto-fixed Issues

None beyond the one already recorded in the prior session's Task 2 (see below) — no new Rule 1-4 auto-fixes were needed to complete Task 4 or write this SUMMARY.

**1. [Rule 1 - Bug] `SessionLifecycle.release()` uncovered real-heartbeat-release gap in the first test combining a real gate unlock with teardown**
- **Found during:** Task 2 (deterministic fake-media integration test)
- **Issue:** The happy-path/DTMF integration tests are the first to combine a REAL gate unlock (`bypass_accounting=False`) with a teardown in the same test — `SessionLifecycle.release()` then calls the real `quota.release_heartbeat()`, uncovered by the existing `fake_aws` fixture (boto3 CloudWatch/ECS only, not this heartbeat path).
- **Fix:** Added a `_stub_release_heartbeat` monkeypatch in the new test module.
- **Files modified:** `apps/voice/tests/test_telephony_integration.py`
- **Verification:** Full project suite 403 passed/53 skipped/0 failed.
- **Committed in:** `057b4a3` (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (1 bug), carried forward from the prior session's Task 2. No new deviations in this continuation session.
**Impact on plan:** Necessary for test hermeticity. No scope creep.

### Checkpoint resolution (Task 3)

Task 3 (`checkpoint:human-verify`, gate `blocking`) asked a human to run a real SIP softphone call end-to-end and confirm the §19-C perceptual criteria. **The human decided to defer this live run** rather than have it fabricated: the execution sandbox has no running Docker daemon (`docker info` fails to connect; the `docker`/compose CLI ARE present and were used for config-validation-only checks in Plan 02 and Task 2). Task 4 documents the exact recipe a human with a working Docker daemon and a SIP softphone needs to follow to close this out. This is recorded as an **outstanding human-verify item**, not a completed verification — see "Known Stubs / Outstanding Items" below and `.planning/STATE.md`.

## Issues Encountered
None in this continuation session beyond the checkpoint deferral above (which is a decision, not an issue).

## Known Stubs / Outstanding Items

- **§19-C live softphone proof — NOT YET RUN.** `apps/voice/asterisk/README.md`'s "Manual §19-C softphone proof" section documents the full 8-step recipe, but no human has executed it against a live stack yet (this session's sandbox has no Docker daemon). This is the one exit criterion this plan cannot self-certify. Tracked in `.planning/STATE.md` as an outstanding human-needed item for Phase 11, and should be surfaced again at any future phase-verification or ship step for Phase 11.
- **ARI loopback-vs-published-port mismatch — NOT YET FIXED**, only documented with two resolution options (11-02's original flag, carried forward). Whoever runs the §19-C proof must apply one of the two workarounds first (README step 3).

## User Setup Required

**External services require manual configuration for the §19-C proof.** No `{phase}-USER-SETUP.md` was generated separately for this plan (11-02 already covers the base harness `.env`/Docker setup); the incremental requirement for THIS plan is:
- A running Docker daemon + `docker compose up` in `apps/voice/asterisk/`
- A SIP softphone (Linphone, baresip, or equivalent) registered against the `dev-softphone` PJSIP endpoint
- Real Deepgram/Anthropic/ElevenLabs API keys in `apps/voice/.env` (this proof spends real provider API calls)
- One of the two ARI-reachability workarounds applied (README step 3)

Full steps: `apps/voice/asterisk/README.md` → "Manual §19-C softphone proof".

## Next Phase Readiness
- The standalone telephony entrypoint, the deterministic CI integration test, and the full manual-proof documentation are all in place and committed. Phase 11's code-level deliverables (D-01 through D-09, §24 gate, D-08 entrypoint, D-07 CI test) are complete.
- **Blocking for a genuine "Phase 11 fully verified" claim:** the §19-C live softphone proof itself (this plan's Task 3/checkpoint) remains outstanding — a human with a working Docker daemon needs to run the documented recipe before Phase 11 can be considered perceptually verified end-to-end, not just structurally/deterministically verified.
- `webrtc.py`/`server.py` confirmed byte-unchanged (`git diff --stat` empty) — the browser WebRTC path was never touched by this plan, matching D-08's process-isolation requirement.
- Full project test suite: 403 passed, 53 skipped, 0 failed (unchanged from the prior session — Task 4 was documentation-only).

---
*Phase: 11-voip-ms-telephony-local-asterisk-edge*
*Completed: 2026-07-12*

## Self-Check: PASSED

All created/modified files found on disk; all 4 commits (2dd1efa, 057b4a3, cc0ed2b, 0f54206) found in git log.
