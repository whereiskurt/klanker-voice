# Phase 11: VoIP.ms Telephony — Local Asterisk Edge - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-11
**Phase:** 11-voip-ms-telephony-local-asterisk-edge
**Areas discussed:** Gate scope (§24), §23 identity boundary, Gate mode, ARI client, Local harness / exit proof, Controller process boundary

---

## Gate scope — how much of the §24 answer-gate lands in Phase 11

| Option | Description | Selected |
|--------|-------------|----------|
| Gate mechanism only, now | Quiet-gate mechanism (STT+DTMF, output/LLM/TTS suppressed until unlock) against a local PIN/passphrase; defer §23 tier-mapping to Phase 12 | |
| Full §24 gate, now | Implement §24 completely incl. tier granting on unlock; pulls §23 identity forward | ✓ |
| No gate — pure Asterisk edge | Configs + controller + bridges + hangup + softphone only; move all of §24 to Phase 12 | |

**User's choice:** Full §24 gate, now.
**Notes:** Refined immediately by the §23-boundary follow-up below — "full gate" means the gate mechanism + PIN/passphrase→tier grant, NOT the full §23 caller-ID resolution.

---

## §23 identity boundary — how much of §23 comes forward with the gate

| Option | Description | Selected |
|--------|-------------|----------|
| PIN/passphrase→tier only; minimal identity seam | Gate + "which PIN/phrase → which tier" grant via a minimal CallIdentity seam; defer caller-ID→code→tier baseline + real DID to Phase 12 | ✓ |
| Full §23 identity now | Pull the entire caller-ID → access-code → tier mint path into Phase 11 with a stubbed caller-ID | |
| Let planner scope the seam | Capture intent, let planner draw the §23 line | |

**User's choice:** PIN/passphrase→tier only; minimal identity seam.
**Notes:** Keeps Phase 11 fully testable on a local softphone (no real caller-ID/DID exists until Phase 12). → CONTEXT D-05a.

---

## Gate mode — factor coverage + default

| Option | Description | Selected |
|--------|-------------|----------|
| 'either' — both factors, either unlocks | Implement + test BOTH DTMF PIN and order-independent 4-word passphrase; default gate_mode='either' | ✓ |
| Passphrase-first, PIN as fast-follow | Build passphrase fully, scaffold DTMF-PIN as secondary | |
| Let planner decide | Capture 'both, either unlocks' as target; planner sequences | |

**User's choice:** 'either' — both factors, either unlocks.
**Notes:** Matches §24's primary/recommended design; both paths get test coverage. → CONTEXT D-05b.

---

## ARI client library

| Option | Description | Selected |
|--------|-------------|----------|
| Research it in planning | Phase-researcher evaluates asyncari / aioari / raw aiohttp+websockets and pins one with rationale | ✓ |
| Raw aiohttp + websockets | No third-party ARI lib; own the ARI REST + events WebSocket plumbing directly | |
| asyncari (async ARI lib) | Use a maintained async ARI wrapper if 2026 health checks out | |

**User's choice:** Research it in planning.
**Notes:** CLAUDE.md requires explicit pins; 2026 maintenance status matters. Raw aiohttp+websockets is the no-new-dependency fallback (both already in the stack). → CONTEXT D-06.

---

## Local harness + §19-C exit-criterion proof

| Option | Description | Selected |
|--------|-------------|----------|
| docker-compose + fake-media integration test | docker-compose Asterisk + scripted SIP client; automated SIP→Asterisk→fake-media test in CI, plus a documented manual real-pipeline softphone run | ✓ |
| docker-compose + documented manual softphone | docker-compose harness + README, but the §19-C proof is manual only (no Asterisk-in-CI test) | |
| Let research recommend | Researcher assesses Asterisk-in-CI feasibility and recommends | |

**User's choice:** docker-compose + fake-media integration test.
**Notes:** The deterministic fake-media integration test is the required CI artifact; the real-pipeline §19-C proof (test creds) stays a documented manual softphone run. → CONTEXT D-07.

---

## Controller process boundary

| Option | Description | Selected |
|--------|-------------|----------|
| Standalone telephony entrypoint | New separate process (`python -m klanker_voice.telephony.controller` / telephony_server.py) alongside Asterisk; WebRTC server.py untouched | ✓ |
| Inside existing voice service | Wire the controller into the FastAPI browser process | |
| Let planner decide | Capture the isolation constraint, let planner choose shape | |

**User's choice:** Standalone telephony entrypoint.
**Notes:** Mirrors the eventual §15 telephony-edge deploy isolation; don't touch webrtc.py/browser server.py. → CONTEXT D-08.

---

## Claude's Discretion

- Exact module/class/function names; whether the socket-backed `RtpMediaSession` lives in `media.py` or a new `telephony/rtp_socket.py`.
- Exact standalone entrypoint filename/shape (module `__main__` vs `telephony_server.py`).
- Where the STT-only gate pipeline is assembled (gate-scoped `build_pipeline` variant vs suppression flag), subject to secrets-never-reach-LLM and LLM/TTS-only-after-unlock.
- Exact ARI External Media parameters (transport/format/direction/connection mode), confirmed against the pinned ARI client + Asterisk version.
- Which §16 lifecycle assertions become automated CI vs manual, above the required fake-media integration-test floor.
- Where the architecture/coupling note lives (SUMMARY + module docstrings).

## Deferred Ideas

- **Phase 12 (spec D):** VoIP.ms `klanker-pbx` subaccount + public DID, §11/§23 caller-ID→access-code→tier mint path, cellular-network test.
- **Phase 13 (spec E):** physical payphone via its own `payphone-ata` subaccount.
- **Phase 14 (spec F):** Terraform/Terragrunt isolated `telephony-edge`, SSM secret provisioning, alarms/dashboards, edge hardening (SG/TLS-SRTP/fail2ban), load test, runbook.
- Pre-rendered PSTN greeting clip (§12); application-level AEC (§10) — only after a measured problem.
- **Operator provisioning prefs captured this session (Phase 12/14):** DID must end in **5878**, anywhere in **Ontario**; run the Asterisk/telephony-edge server near **us-east-1** "for now." POP-vs-region latency tension to resolve in Phase 14. VoIP.ms account creation / 2FA / balance funding / paid DID order are **human/portal actions — not autonomous.** (Saved to project memory.)
- **Private transcription ledger — S3 batch + Athena** (todo, reviewed, not folded): separate feature; Phase 11 only enforces the "pre-unlock transcript never written to the ledger" constraint (D-05e).
