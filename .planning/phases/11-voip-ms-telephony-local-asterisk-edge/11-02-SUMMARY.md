---
phase: 11-voip-ms-telephony-local-asterisk-edge
plan: 02
subsystem: infra
tags: [asterisk, ari, pjsip, docker-compose, sip, rtp, telephony]

# Dependency graph
requires:
  - phase: 11-voip-ms-telephony-local-asterisk-edge (plan 01)
    provides: "[telephony] config loader + credential-name rejection (D-09), TelephonyConfig"
provides:
  - "apps/voice/asterisk/ config set (http.conf, ari.conf, pjsip.conf, extensions.conf, rtp.conf)"
  - "docker-compose.yml portable macOS-safe Asterisk dev harness (pinned image, explicit ports, host.docker.internal)"
  - "apps/voice/tests/test_asterisk_configs.py structural invariant lint (9 tests) proving D-01/§25 posture"
affects: [11-03, 11-04, 11-05, 11-06, 11-07]

# Tech tracking
tech-stack:
  added: ["andrius/asterisk:22.10.1_debian-trixie (docker image, pinned)"]
  patterns:
    - "Text-only config-invariant lint tests (no live Asterisk process) as the structural enforcement mechanism for a security posture (mirrors the credential-field-rejection regex pattern from Phase 10/11-01)"

key-files:
  created:
    - apps/voice/asterisk/http.conf
    - apps/voice/asterisk/ari.conf
    - apps/voice/asterisk/pjsip.conf
    - apps/voice/asterisk/extensions.conf
    - apps/voice/asterisk/rtp.conf
    - apps/voice/asterisk/docker-compose.yml
    - apps/voice/asterisk/.env.example
    - apps/voice/asterisk/README.md
    - apps/voice/tests/test_asterisk_configs.py
  modified: []

key-decisions:
  - "http.conf/ari.conf bindaddr stays 127.0.0.1 (container-loopback-scoped, literal private/loopback value) per the plan's own Task 1 text and D-01's truth ('bound to a private/loopback interface, never a public one') -- docker-compose still publishes 8088:8088 for forward compatibility, but a host-run process cannot reach ARI through that published port while bindaddr stays loopback-scoped (expected Docker behavior: port publishing forwards to a container's routable interface, not its own loopback). Flagged in README as a discretionary follow-up for whichever later plan (D-08's standalone controller) first needs a live ARI HTTP round-trip -- not required by this plan's own verification (config lint + docker compose config + the docker-exec-based module smoke check, none of which need ARI network reachability)."
  - "ari.conf/pjsip.conf reference \${ASTERISK_ARI_PASSWORD}/\${SOFTPHONE_SIP_PASSWORD} as env-var-name placeholders (matching the research skeleton exactly), but Asterisk's own .conf parser does not perform shell-style \${VAR} substitution -- flagged explicitly in the README as a known limitation with a documented manual-substitution workaround, rather than building an envsubst/entrypoint rendering pipeline this plan's own acceptance criteria did not require."
  - "RTP range narrowed to 10000-10020 (21 ports) matching a single-call dev harness (max_concurrent_calls=1), well under Asterisk's much wider default range."

requirements-completed: [D-01, D-07]

coverage:
  - id: D1
    description: "Inbound-only Stasis dialplan: exactly one context, zero Dial() application calls, hands off to Stasis(klanker) (T-11-02-01, §25.A)"
    requirement: "D-01"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_asterisk_configs.py::TestExtensionsConfInboundOnly (3 tests)"
        status: pass
    human_judgment: false
  - id: D2
    description: "Private, authenticated ARI: http.conf bindaddr is loopback-only, ari.conf declares an authenticated type=user with empty allowed_origins (T-11-02-02, §18/§25.C)"
    requirement: "D-01"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_asterisk_configs.py::TestPrivateAuthenticatedAri (3 tests)"
        status: pass
    human_judgment: false
  - id: D3
    description: "ulaw-only PJSIP endpoint (disallow=all/allow=ulaw), matching the Phase 10 PCMU codec exactly, context locked to the inbound-only dialplan"
    requirement: "D-01"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_asterisk_configs.py::TestPjsipConfUlawOnly (2 tests)"
        status: pass
    human_judgment: false
  - id: D4
    description: "Portable macOS-Docker-Desktop-safe docker-compose harness: pinned image tag (never latest, T-11-02-SC), explicit port publishing (SIP/ARI/narrow RTP range), host.docker.internal reachability"
    requirement: "D-07"
    verification:
      - kind: other
        ref: "docker compose config (from apps/voice/asterisk/) -> COMPOSE_OK"
        status: pass
    human_judgment: false
  - id: D5
    description: "README bring-up docs + module smoke check + secret-sourcing documentation + placeholder §19-C manual softphone proof section for Plan 07"
    verification: []
    human_judgment: true
    rationale: "Documentation quality/completeness and the actual live docker-compose-up + module-smoke-check run are not machine-verifiable here (no live docker daemon in this execution sandbox); a human or a later plan's live run confirms the harness actually starts and the Stasis application is present."

# Metrics
duration: 20min
completed: 2026-07-12
status: complete
---

# Phase 11 Plan 02: Local Asterisk Edge Config + Docker Harness Summary

**Five hand-authored Asterisk configs (inbound-only Stasis dialplan, private authenticated ARI, ulaw-only PJSIP endpoint) plus a pinned-image, macOS-safe docker-compose dev harness and a 9-test structural invariant lint proving the D-01/§25 security posture mechanically.**

## Performance

- **Duration:** ~20 min
- **Started:** 2026-07-11T23:57:00-04:00 (approx, first file write)
- **Completed:** 2026-07-12T00:02:19-04:00
- **Tasks:** 3
- **Files modified:** 9

## Accomplishments
- `apps/voice/asterisk/{http,ari,pjsip,extensions,rtp}.conf` — a narrow, single-context inbound-only Stasis dialplan (`[from-klanker-inbound]` → `Answer()` → `Stasis(klanker)` → `Hangup()`), authenticated ARI (`type=user`, empty `allowed_origins`) bound to `127.0.0.1`, and a ulaw-only PJSIP endpoint (`disallow=all`/`allow=ulaw`) matching the Phase 10 PCMU codec byte-for-byte.
- `docker-compose.yml` + `.env.example` + `README.md` — a portable, pinned-image (`andrius/asterisk:22.10.1_debian-trixie`) dev harness that publishes exactly the ports needed (5060/udp SIP, 8088/tcp ARI, 10000-10020/udp RTP), mounts the five configs read-only, and documents the `docker exec ... asterisk -rx 'core show application Stasis'` module smoke check plus secret sourcing (six env placeholders, none real). `docker compose config` validates.
- `apps/voice/tests/test_asterisk_configs.py` — 9 pure text-assertion tests (no live Asterisk process) enforcing the inbound-only / ulaw-only / private-authenticated-ARI / narrow-RTP-range invariants; empirically proved to genuinely bite by temporarily adding a `Dial()` line and an `allow=g722` line, confirming both failures, then reverting (no mutation committed).

## Task Commits

Each task was committed atomically:

1. **Task 1: Author the Asterisk config set (http/ari/pjsip/extensions/rtp)** - `0f0d711` (feat)
2. **Task 2: docker-compose harness + .env.example + README bring-up** - `49beb8d` (feat)
3. **Task 3: Structural config-invariant lint test** - `cc6930a` (test)

**Plan metadata:** (this commit)

## Files Created/Modified
- `apps/voice/asterisk/http.conf` - ARI HTTP server, `bindaddr=127.0.0.1`, `bindport=8088`
- `apps/voice/asterisk/ari.conf` - authenticated ARI user `klanker`, empty `allowed_origins`
- `apps/voice/asterisk/pjsip.conf` - `transport-udp` + one ulaw-only `dev-softphone` endpoint, `context=from-klanker-inbound`
- `apps/voice/asterisk/extensions.conf` - single `[from-klanker-inbound]` context, no `Dial()`, no other context
- `apps/voice/asterisk/rtp.conf` - `rtpstart=10000`/`rtpend=10020`
- `apps/voice/asterisk/docker-compose.yml` - pinned-image Asterisk service, explicit ports, read-only config mounts, `host.docker.internal` via `extra_hosts`
- `apps/voice/asterisk/.env.example` - six telephony secret placeholders (D-09), no real values
- `apps/voice/asterisk/README.md` - bring-up steps, module smoke check, secret sourcing, two flagged known limitations, placeholder §19-C section for Plan 07
- `apps/voice/tests/test_asterisk_configs.py` - 9 structural invariant tests

## Decisions Made
- **ARI bindaddr kept literal loopback (127.0.0.1), not the compose-network `0.0.0.0` alternative the research skeleton also offered.** This matches the plan's own Task 1 action text and the strict "private/loopback value" wording in both the must-have truths and Task 3's acceptance criteria. The tradeoff (a host-run controller can't yet reach ARI through the published `8088:8088` port because Docker's port-publish mechanism forwards to a container's routable interface, not its own loopback) is explicitly documented in the README as a discretionary follow-up — this plan's own verification never needs a live ARI HTTP round-trip (the module smoke check runs via `docker exec`, not the network).
- **`${VAR}`-style placeholders in `ari.conf`/`pjsip.conf` are documentation-only** (Asterisk's `.conf` parser doesn't shell-substitute); the README documents a manual local-substitution workaround rather than building an `envsubst`/entrypoint rendering pipeline, since no acceptance criterion in this plan required a live authenticated ARI session.
- **RTP range narrowed to 10000-10020** (21 ports) — matches `max_concurrent_calls=1` and the docker-compose published range exactly.

## Deviations from Plan

None — plan executed exactly as written. All three tasks' acceptance criteria were met without needing any Rule 1-4 auto-fixes; the two items above are documented discretionary choices within stated ambiguity (the plan's own research explicitly offered the `127.0.0.1` vs `0.0.0.0` bindaddr choice), not deviations from a specified behavior.

## Issues Encountered
- Docker daemon was not running in the execution sandbox (`docker info` failed to connect to the Docker API), so `docker compose up` and the module smoke check could not be exercised live in this session. `docker compose config` (the plan's own gating verification command) does not require a running daemon and passed (`COMPOSE_OK`). The live bring-up + smoke check is documented in the README for a human or a later plan's live run.

## User Setup Required
None - no external service configuration required for this plan's own scope (config authoring + compose harness + lint test). A human wanting to actually run `docker compose up` needs Docker Desktop running locally; `.env.example` documents the six placeholder secret names to copy into a local, gitignored `.env`.

## Next Phase Readiness
- The Asterisk edge config set + compose harness are ready for the next waves: the socket-backed `RtpMediaSession` (D-03), the ARI client pin + `telephony/controller.py` (D-06/D-02), and the §24 silent answer-gate (D-05) all build on top of this harness and its ARI user/PJSIP endpoint.
- Flagged, not blocking: the ARI-loopback-vs-published-port limitation should be resolved (bindaddr moved to the container's `0.0.0.0` paired with a host-loopback-scoped compose publish `127.0.0.1:8088:8088`) by whichever later plan first needs the standalone controller to authenticate against ARI over the network — most likely 11-05/11-06 per the ROADMAP's wave structure, or Plan 07 when it fills in the manual §19-C softphone proof section.
- Full test suite remains green: 348 passed, 53 skipped (pre-existing skips, unrelated to this plan).

---
*Phase: 11-voip-ms-telephony-local-asterisk-edge*
*Completed: 2026-07-12*

## Self-Check: PASSED

All 9 created files found on disk; all 3 task commits (0f0d711, 49beb8d, cc6930a) found in git log.
