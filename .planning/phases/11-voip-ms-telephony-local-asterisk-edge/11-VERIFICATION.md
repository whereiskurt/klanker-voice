---
phase: 11-voip-ms-telephony-local-asterisk-edge
verified: 2026-07-12T12:50:01Z
status: human_needed
score: 3/4 truths verified (criteria 1-3), 1 explicit human-verification item (criterion 4)
behavior_unverified: 0
overrides_applied: 0
human_verification:
  - test: "Run the §19-C manual softphone proof: apps/voice/asterisk/README.md → 'Manual §19-C softphone proof' (8-step recipe) — bring up the docker-compose Asterisk harness, run `python -m klanker_voice.telephony.controller` against configs/telephony.toml with real ARI/PIN/passphrase env, register a SIP softphone (e.g. Linphone/baresip) against dev-softphone, place a call."
    expected: "The call reaches Stasis, stays silent through the §24 gate, unlocks via DTMF PIN or spoken 4-word passphrase, hears the greeting not clipped, converses with the agent, can interrupt (barge-in) mid-response, and hangs up cleanly with no leaked Asterisk resources (bridge/external-media channel/RTP socket all torn down)."
    why_human: "Requires a live Docker daemon, real Asterisk process, real SIP softphone, real Deepgram/Anthropic/ElevenLabs API round-trips, and a human ear to judge 'not clipped' / conversational feel / barge-in responsiveness — none of which a static/fake-media test can observe. This is the literal §19-C exit criterion and was explicitly NOT run in this execution sandbox (no Docker daemon available); 11-07-SUMMARY.md and STATE.md both record it honestly as an outstanding item rather than fabricating a pass."
---

# Phase 11: VoIP.ms Telephony — Local Asterisk Edge Verification Report

**Phase Goal:** A local SIP softphone call holds a full conversation with the agent through Asterisk — Asterisk configs (PJSIP/ARI/dialplan), an ARI/Stasis call controller that creates external-media channels + mixing bridges, and the call registry, wiring hangup to `lifecycle.release()`. (Spec Phase C, §7 / §13 / §19-C, plus the silent answer-gate §24 verified outside the LLM.)

**Verified:** 2026-07-12T12:50:01Z
**Status:** human_needed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth (ROADMAP success criterion) | Status | Evidence |
|---|---|---|---|
| 1 | Asterisk configs (`pjsip.conf`, `ari.conf`, `extensions.conf`) exist with a narrow inbound-only Stasis dialplan; ARI is authenticated and private-only | ✓ VERIFIED | `apps/voice/asterisk/extensions.conf` declares exactly one context `[from-klanker-inbound]` → `Answer()` → `Stasis(klanker)`, no `Dial(` anywhere. `pjsip.conf` endpoint template sets `context=from-klanker-inbound`, `disallow=all`/`allow=ulaw` only. `ari.conf` declares a `[klanker]` `type=user` with a password (sourced from env), `allowed_origins=` empty. `http.conf` `bindaddr=127.0.0.1`. `apps/voice/tests/test_asterisk_configs.py` (10 tests) mechanically enforces all of these invariants via text-assertion lint against the committed files — ran independently, 10/10 pass. |
| 2 | An ARI controller allocates a media session, creates the external-media channel + bridge, constructs a Klanker `CallSession`, and keys an `ActiveCall` registry by channel ID | ✓ VERIFIED | `apps/voice/src/klanker_voice/telephony/controller.py::on_stasis_start` binds `SocketRtpMediaSession` first (R2 bind-before-create ordering, asserted by `order.index("media_open") < order.index("create_external_media")`), calls `ari.create_external_media` + `ari.create_bridge` + two `ari.add_channel`, constructs `CallSession` via `create_call_session(channel="pstn")`, and registers `ActiveCall` in `self.calls` keyed by `sip_channel_id` with all §13 fields populated. `test_stasis_start_allocates_and_registers` and `test_unexpected_context_no_allocation` (unexpected-context calls get zero allocation + immediate hangup) — ran independently, both pass. |
| 3 | On `ChannelDestroyed`/hangup the controller closes the `CallSession`, releases lifecycle exactly once, and tears down bridge + external channel + RTP socket (no leaked resources) | ✓ VERIFIED (behavioral) | `_close_active_call` is the single funnel point (`on_channel_destroyed`, hard-timeout `on_released`, quota-denied path all route through it), guarded by `active_call.lock` + `active_call.closed` check-and-set. `CallSession.close()` → `lifecycle.release()`, whose own `_stopped` guard (`session.py`) makes repeated/racing calls a no-op. Behavioral tests (run independently, standalone, all pass): `test_channel_destroyed_closes_exactly_once` (a second `ChannelDestroyed` for the same channel is a no-op, not a re-teardown), `test_simultaneous_close_calls_release_exactly_once` (two racing `asyncio.gather`'d closes for the SAME `ActiveCall` tear down exactly once), `test_hard_timeout_hangs_up_sip_channel` (a hard-timeout `lifecycle.release()` ALSO ARI-hangs-up the original SIP channel, not just the media leg), `test_quota_denied_leaves_no_bridge` (a post-gate quota rejection tears down the already-allocated bridge/external-media/socket with no `CallSession` ever constructed). All four assert the full teardown surface: `destroy_bridge`, `hangup(ext-media)`, `media_session.closed`, and an empty `controller.calls` registry afterward. |
| 4 | A local SIP softphone reaches the Stasis app, hears the greeting (not clipped), converses, interrupts the agent, and hangs up cleanly (spec §19-C exit criterion) | ⚠️ NOT RUN — human-verification required | This is the literal §19-C live-softphone perceptual proof. `11-07-SUMMARY.md` and `.planning/STATE.md` both explicitly and honestly record this as an outstanding item — the human decided to defer it because this execution sandbox has no running Docker daemon (real Asterisk, a real softphone, and a human ear are all required and none are available here). It was **not fabricated as a pass**. See the Human Verification section below. |

**Score:** 3/4 truths verified structurally + behaviorally (criteria 1-3). Criterion 4 is a genuine, honestly-disclosed human-verification gap, not a code gap — see Human Verification below. No overrides applied (none needed or requested).

### Required Artifacts

| Artifact | Expected | Status | Details |
|---|---|---|---|
| `apps/voice/asterisk/extensions.conf` | Narrow inbound-only Stasis dialplan | ✓ VERIFIED | 18 lines, single context, no `Dial()`; lint-tested |
| `apps/voice/asterisk/pjsip.conf` | ulaw-only, `context=from-klanker-inbound` | ✓ VERIFIED | `disallow=all`/`allow=ulaw` exactly; lint-tested |
| `apps/voice/asterisk/ari.conf` | Authenticated, no CORS origins | ✓ VERIFIED | `[klanker]` `type=user` + password; `allowed_origins=` empty |
| `apps/voice/asterisk/http.conf` | Private/loopback ARI bind | ✓ VERIFIED | `bindaddr=127.0.0.1` |
| `apps/voice/asterisk/rtp.conf` | Narrow RTP range | ✓ VERIFIED | `10000-10020` (≤100 span), matches docker-compose published range |
| `apps/voice/asterisk/docker-compose.yml` | Portable dev harness | ✓ VERIFIED (with 1 non-blocking TODO) | Publishes SIP/ARI/RTP ports, `host.docker.internal` mapping; a `TODO(human, before first real --profile integration run)` on the SIPp image tag is a warning-level marker, explicitly documented as non-blocking for CI (`docker compose config`-only check) |
| `apps/voice/src/klanker_voice/telephony/config.py` | `TelephonyConfig` + `load_telephony_config()` | ✓ VERIFIED | All §14/§24 keys present incl. `unlock_tier_id`; reuses `config._resolve_config_path`/`_load_toml_data` (shared credential gate) |
| `apps/voice/src/klanker_voice/telephony/rtp_socket.py` | Socket-backed `RtpMediaSession` | ✓ VERIFIED | Satisfies `types.RtpMediaSession` Protocol; bind-first + symmetric-RTP source learning; `media.py`/`transport.py` byte-unchanged (confirmed no diff to those files in phase commits beyond what Phase 10 already shipped) |
| `apps/voice/src/klanker_voice/telephony/ari.py` | `AriClient` (raw aiohttp, D-06) | ✓ VERIFIED | 6 REST calls + events-WS dispatch loop; no third-party ARI library added; credentials never logged (only status+path in `AriError`) |
| `apps/voice/src/klanker_voice/telephony/controller.py` | `AsteriskCallController` + `ActiveCall` registry | ✓ VERIFIED | 725 lines; allocation, teardown, DTMF, and the §24 gate wiring all present and tested |
| `apps/voice/src/klanker_voice/telephony/gate.py` | `GateProcessor` (§24 silent answer-gate) | ✓ VERIFIED | Inline pipeline processor; swallows pre-unlock transcription/speaking frames (structural redaction boundary); order-independent passphrase matcher; fail-closed timer |
| `apps/voice/src/klanker_voice/telephony/__main__.py` | Standalone entrypoint (D-08) | ✓ VERIFIED | No FastAPI/HTTP-server import; loads config + telephony config, constructs `AriClient`+`AsteriskCallController`, `ari.connect()` → `ari.run()` |
| `apps/voice/tests/test_asterisk_configs.py` | Config-invariant lint | ✓ VERIFIED | 10 tests, all pass (ran independently) |
| `apps/voice/tests/test_telephony_lifecycle.py` | §16 lifecycle unit-test matrix | ✓ VERIFIED | 13 tests (allocation, teardown, hard-timeout, quota-denied, gate variants), all pass |
| `apps/voice/tests/test_telephony_integration.py` | Deterministic fake-media CI integration test (D-07) | ✓ VERIFIED | Drives the whole controller-level call lifecycle as one scenario (no real Asterisk/SIPp/Deepgram/Anthropic/ElevenLabs); the one Asterisk/SIPp-dependent case (`test_docker_compose_sipp_profile_is_valid`) only shells out to `docker compose config` and self-skips when `docker` isn't on `PATH` |

### Key Link Verification

| From | To | Via | Status | Details |
|---|---|---|---|---|
| `extensions.conf [from-klanker-inbound]` | `Stasis(klanker)` | `Answer()` → `Stasis(klanker)` | ✓ WIRED | Confirmed by file read + `test_extensions_conf_reaches_stasis` |
| `pjsip.conf` endpoint | `extensions.conf` context | `context=from-klanker-inbound` | ✓ WIRED | Confirmed by file read + `test_pjsip_conf_endpoint_context_is_inbound_only` |
| `controller.on_stasis_start` | `SocketRtpMediaSession.open` | `media_session_opener` seam, called BEFORE `create_external_media` | ✓ WIRED | R2 bind-first ordering asserted in `test_stasis_start_allocates_and_registers` |
| `controller.on_stasis_start` | `create_call_session(channel="pstn")` | direct call, `TelephonyTransport` wraps the socket session | ✓ WIRED | Confirmed by source read + test assertions on `active_call.call_session` |
| `controller.on_channel_destroyed` / hard-timeout / quota-denied | `_close_active_call` | single funnel, `active_call.lock` guard | ✓ WIRED | 4 independent tests exercise all 3 entry paths converging on one idempotent teardown |
| `CallSession.close()` | `SessionLifecycle.release()` | `lifecycle.release()`, `_stopped` guard | ✓ WIRED | `call_runtime.py:112-116` + `session.py:247-262`; behaviorally confirmed by `active_call.call_session.lifecycle._stopped is True` assertion |
| `GateProcessor` (locked) | LLM/ledger/logs | NEVER `push_frame`s pre-unlock `TranscriptionFrame`/speaking frames | ✓ WIRED | `gate.py:241-273`; behaviorally confirmed by `test_gated_stasis_start_stays_locked_no_quota_no_greet` (no `greet_now`/`quota.start_gate` call before unlock) |
| `controller.on_channel_dtmf_received` | `GateProcessor.unlock("dtmf")` | PIN comparison at controller layer, never through the pipeline | ✓ WIRED | `test_gated_dtmf_unlock_never_touches_pipeline` never calls `gate.process_frame`, only `on_channel_dtmf_received` |
| `telephony/__main__.py` | `webrtc.py` / `server.py` | (absence of import) | ✓ VERIFIED ABSENT | `grep` confirms no import either direction; `git log`/`git diff` confirm zero changes to `server.py`/`webrtc.py` across all Phase 11 commits (111071c..HEAD) |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|---|---|---|---|
| Full telephony test suite (config lint, ARI client, RTP socket, lifecycle, gate, integration) | `uv run pytest tests/test_asterisk_configs.py tests/test_telephony_config.py tests/test_telephony_ari.py tests/test_telephony_rtp_socket.py tests/test_telephony_lifecycle.py tests/test_telephony_gate.py tests/test_telephony_integration.py -q` | `76 passed` | ✓ PASS |
| Full project test suite (regression check) | `uv run pytest -q` (apps/voice) | `403 passed, 53 skipped, 0 failed` | ✓ PASS — matches phase-provided context, independently reproduced |
| Exactly-once teardown invariant (criterion 3), single named tests | `uv run pytest tests/test_telephony_lifecycle.py::test_simultaneous_close_calls_release_exactly_once tests/test_telephony_lifecycle.py::test_hard_timeout_hangs_up_sip_channel tests/test_telephony_lifecycle.py::test_quota_denied_leaves_no_bridge -v` | `3 passed` | ✓ PASS |
| Credential-field regex widening (D-09) | file read of `config.py:49-52` | `_CREDENTIAL_FIELD_RE` includes `pin`, `passphrase`, `pass_?word`, whole-word `words` | ✓ PASS |

### Requirements Coverage

No REQUIREMENTS.md entries map to Phase 11 (confirmed: `grep -n "Phase 11"` and D-xx patterns in `.planning/REQUIREMENTS.md` return no hits). This is consistent with ROADMAP.md's own phase declaration: `**Requirements**: none (coverage driven by success criteria 1-4 + CONTEXT decisions D-01..D-09)`. No orphaned requirements. Coverage was instead assessed directly against the 4 ROADMAP success criteria (above) and the 9 CONTEXT.md decisions D-01..D-09, all of which are traceable to specific artifacts/tests as documented in each plan's `must_haves` frontmatter and confirmed against source in this report.

### Anti-Patterns Found

No blocker-tier debt markers (`TBD`/`FIXME`/`XXX`) in any file touched by Phase 11. One warning-tier `TODO` in `apps/voice/asterisk/docker-compose.yml` (confirming the current SIPp release tag before the first real `--profile integration` run) — explicitly documented in the same comment block as non-blocking for CI (the CI-facing check only runs `docker compose config`, never a build/pull). Not a gap.

No stub patterns (`return null`/empty-object handlers/console.log-only implementations) found in `telephony/*.py`. "placeholder" hits in `controller.py`/`gate.py` are legitimate design terms for the zeroed `bypass_accounting=True` `GateResult` used during the gate window (a documented, tested design choice — not missing functionality).

**Security-hardening findings (advisory, from 11-REVIEW.md — NOT treated as phase-goal-blocking gaps, per explicit task instruction):**

| Finding | File | Severity | Note |
|---|---|---|---|
| CR-01: Gated flow allocates a full live-STT pipeline per call before `quota.start_gate`/`max_concurrent_calls` is checked — a caller who stays silent through the gate window gets an unbounded, unmetered STT allocation | `telephony/controller.py:274-369, 459-557` | Critical (advisory) | Confirmed present by reading `_finish_stasis_start_gated` and the passing `test_gated_stasis_start_stays_locked_no_quota_no_greet` assertion `start_gate_calls == []`. Real security-hardening work, correctly routed to `/gsd-secure-phase`, not a correctness gap in this phase's stated goal (allocate on StasisStart, teardown on hangup) |
| CR-02: Symmetric-RTP peer learning re-learns on every packet with no source-IP validation; `rtp_bind_host` defaults to `0.0.0.0` | `telephony/rtp_socket.py:56-61`, `telephony/controller.py:226` | Critical (advisory) | Confirmed present by reading `datagram_received`. Same disposition as CR-01 |

Both CR-01/CR-02 are logged here for visibility per the task's instruction, and are exactly the two findings the phase context flagged in advance — confirmed independently by direct source read, not just trusted from REVIEW.md.

### Human Verification Required

### 1. §19-C manual softphone proof (ROADMAP success criterion 4)

**Test:** Follow the 8-step recipe in `apps/voice/asterisk/README.md` → "Manual §19-C softphone proof": bring up `docker compose up` for the Asterisk harness, resolve the documented ARI-loopback-vs-published-port prerequisite (11-02's own flagged caveat), run `python -m klanker_voice.telephony.controller` with real `ASTERISK_ARI_*`/`TELEPHONY_ACCESS_PIN`/`TELEPHONY_PASSPHRASE_WORDS` env against `configs/telephony.toml`, register a SIP softphone (e.g. Linphone/baresip) as `dev-softphone`, and place a call.

**Expected:** The call reaches the Stasis app; the agent stays silent until the caller enters the DTMF PIN or speaks the 4-word passphrase; on unlock the greeting plays without being clipped; the caller can hold a natural conversation and interrupt (barge-in) the agent mid-response; hanging up tears down cleanly with no leaked bridge/external-media channel/RTP socket (verifiable via `asterisk -rx "core show channels"` / `bridge show all` returning empty afterward, and the harness log showing `_close_active_call`).

**Why human:** This is the literal §19-C perceptual exit criterion — it requires a live Docker daemon, a real running Asterisk process, a real SIP softphone, live Deepgram/Anthropic/ElevenLabs API round-trips, and a human ear to judge "not clipped" and conversational/barge-in feel. None of these are available or safely automatable in this execution sandbox. The phase author explicitly chose to defer this live run rather than fabricate a pass — it is recorded honestly in `11-07-SUMMARY.md` and `.planning/STATE.md` as an outstanding item, not claimed complete.

### Gaps Summary

No code-level gaps against the phase goal. Success criteria 1-3 (Asterisk configs, ARI controller allocation, exactly-once idempotent teardown) are all structurally present, correctly wired, and behaviorally proven by passing tests that were independently re-run for this verification (76 telephony-scoped tests + a full-suite regression run of 403 passed/53 skipped/0 failed). Success criterion 4 (the live softphone perceptual proof) is not a code gap — it is a genuine, honestly-disclosed human-verification item that requires infrastructure (Docker daemon, real SIP client, live provider credentials, a human ear) unavailable in this environment. Two Critical-severity security-hardening findings from `11-REVIEW.md` (unbounded pre-quota STT allocation; unvalidated symmetric-RTP source) were independently confirmed present in the source but are advisory hardening work for `/gsd-secure-phase`, not phase-goal-blocking gaps, per the explicit scope of this phase's stated goal (allocate/teardown wiring, not DEF-CON-hardened abuse-resistance — that is Phase 14/§25's cloud-enforcement scope).

---

_Verified: 2026-07-12T12:50:01Z_
_Verifier: Claude (gsd-verifier)_
