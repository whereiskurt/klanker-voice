---
phase: 04-voice-service-deployed-quota-enforcement
plan: 01
subsystem: infra
tags: [fastapi, pyjwt, jwks, smallwebrtc, aiortc, docker, uv]

requires:
  - phase: 03-auth-service-access-codes plan 03
    provides: "Pinned Phase-4 JWT contract (issuer/jwks_uri/audience/RS256/namespaced tier_id+group claims, 3600s TTL) — server.py/auth.py validate against it byte-for-byte"
provides:
  - "server.py: a production FastAPI/uvicorn entrypoint (POST /api/offer, GET /health) — the self-hosted Fargate deploy target the pipecat dev runner doesn't provide"
  - "auth.py: validate_access_token() offline RS256 JWT validation (PyJWKClient) + recognize_service_credential() smoke/service-credential bypass (D-15)"
  - "webrtc.py: gather_public_candidates()/build_ice_servers()/inject_public_host_candidate() — self-advertised public-IP host candidate + STUN srflx backup (D-12)"
  - "A single named start_gate(identity) seam in server.py (default allow) — 04-04 replaces its body with real quota enforcement, no restructure needed"
  - "apps/voice/Dockerfile — python:3.12-slim image, live-verified (docker build + a running container answering /health and /api/offer)"
affects: [04-02-deploy-infra, 04-03-ice-smoke-test, 04-04-quota-enforcement, 04-05-idle-teardown]

tech-stack:
  added: ["pyjwt[crypto]~=2.13.0", "boto3>=1.42"]
  patterns:
    - "Offline JWT validation via a cached PyJWKClient (lru_cache on a module-level accessor), monkeypatched directly in tests with an in-memory fake JWKS — no network in the unit suite"
    - "Every fallible external lookup (ECS task metadata, EC2 DescribeNetworkInterfaces) degrades to None/STUN-only rather than raising — gather_public_candidates() is a pure best-effort composer"
    - "server.py isolates aiortc/ICE negotiation behind a private async seam (_negotiate_webrtc) so unit tests exercise the auth -> start_gate flow by monkeypatching that seam, never touching real WebRTC"
    - "Docker CMD calls uvicorn directly from the synced .venv (via PATH), not `uv run uvicorn ...` — uv run re-syncs default (dev) dependency groups on every invocation"

key-files:
  created:
    - apps/voice/src/klanker_voice/auth.py
    - apps/voice/src/klanker_voice/webrtc.py
    - apps/voice/server.py
    - apps/voice/Dockerfile
    - apps/voice/.dockerignore
    - apps/voice/tests/test_auth.py
    - apps/voice/tests/test_webrtc.py
    - apps/voice/tests/test_server.py
  modified:
    - apps/voice/pyproject.toml

key-decisions:
  - "ENI public-IP lookup keys off the ECS task-metadata MAC address (ec2:DescribeNetworkInterfaces Filters=[mac-address]), not an ENI-id field — Fargate task metadata v4 doesn't expose the ENI id directly, only each container Network's MACAddress, which DescribeNetworkInterfaces accepts as a filter"
  - "Host-candidate self-advertisement is SDP-answer text munging (inject_public_host_candidate): duplicate each aiortc-gathered 'typ host' candidate line with the public IP substituted, relying on Fargate's 1:1 NAT mapping the private ENI IP straight through — no aiortc API exists to inject an arbitrary local ICE candidate directly"
  - "_negotiate_webrtc() is a deliberate seam in server.py so test_server.py can prove the auth -> start_gate ordering without negotiating real media, per the plan's explicit instruction"
  - "pythonpath = [\".\"] added to pyproject.toml's [tool.pytest.ini_options] so tests can `import server` (a top-level app script, not part of the installed klanker_voice package)"

requirements-completed: [INFR-03]

coverage:
  - id: D1
    description: "validate_access_token() does full offline RS256 verification (JWKS signing key, issuer, audience=voice, exp) and rejects forged/expired/wrong-aud/wrong-iss/unknown-key tokens with AuthError"
    requirement: INFR-03
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_auth.py (10 tests: valid, missing-tier-default, expired, wrong-aud, wrong-iss, unknown-kid, service-credential, constant-time-no-match, unset-never-matches, missing-token) — all pass"
        status: pass
    human_judgment: false
  - id: D2
    description: "recognize_service_credential() constant-time-matches KMV_SMOKE_SERVICE_TOKEN and short-circuits validate_access_token to bypass_accounting=True without a JWKS round-trip (D-15)"
    requirement: INFR-03
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_auth.py::test_service_credential_bypasses_jwks_and_sets_bypass_accounting — pass"
        status: pass
    human_judgment: false
  - id: D3
    description: "gather_public_candidates()/build_ice_servers() resolve a self-advertised public IP (ECS metadata -> ENI MAC -> EC2 DescribeNetworkInterfaces) with a STUN ICE server list, degrading to STUN-only on any failure, with no network call at import time"
    requirement: INFR-03
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_webrtc.py (13 tests: EC2-response parse, STUN-only degrade on missing env/no-mac/no-interfaces/no-public-ip/EC2-error, KMV_STUN_URL override, SDP host-candidate injection) — all pass"
        status: pass
    human_judgment: false
  - id: D4
    description: "A production FastAPI entrypoint serves GET /health (no auth) and POST /api/offer (validate token -> start_gate hook, default allow -> SmallWebRTC answer, injecting ICE servers + public host candidate)"
    requirement: INFR-03
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_server.py (7 tests: /health 200, /api/offer 401 on invalid token, start_gate reached + 200 on valid identity, 403 on start_gate rejection, bearer-token extraction) — all pass"
        status: pass
    human_judgment: false
  - id: D5
    description: "The service builds into a python:3.12-slim container (aiortc/audio system libs + uv-installed locked deps) and actually runs: GET /health returns 200, POST /api/offer without a token returns 401"
    requirement: INFR-03
    verification:
      - kind: manual_procedural
        ref: "docker build -t kmv-voice:dev apps/voice (succeeds); docker run + curl http://localhost:17860/health -> 200 {\"status\":\"ok\"}; curl -X POST .../api/offer -> 401 {\"error\":\"unauthorized\"} — run live in this session, container removed afterward"
        status: pass
    human_judgment: false

duration: 10min
completed: 2026-07-05
status: complete
---

# Phase 4 Plan 01: Voice Service WebRTC Entrypoint + Auth + Deploy Image Summary

**A production FastAPI/uvicorn entrypoint (`server.py`) replaces the pipecat dev runner for self-hosted Fargate: `/api/offer` validates the Phase-3 RS256 JWT offline before any transport exists, self-advertises the task's public IP as an ICE host candidate with STUN backup, and the whole thing builds into a live-verified `python:3.12-slim` container.**

## Performance

- **Duration:** 10 min (task-commit span, a8f53bc→9c8568b)
- **Started:** 2026-07-05T19:04:43-04:00
- **Completed:** 2026-07-05T19:13:49-04:00
- **Tasks:** 3
- **Files modified:** 9 (8 new, 1 modified)

## Accomplishments

- `auth.py`: `validate_access_token()` performs fully offline RS256 verification via a cached `PyJWKClient` against the pinned Phase-3 contract (issuer, `aud=https://voice.klankermaker.ai`, exp), reading the namespaced `tier_id`/`group` claims with a `"no-access"` default; a recognized `KMV_SMOKE_SERVICE_TOKEN` short-circuits to `bypass_accounting=True` without a JWKS round-trip (D-15); `AuthError` never carries token/claim material (T-04-02)
- `webrtc.py`: `gather_public_candidates()` composes a self-discovered public IP (ECS task-metadata MAC address → `ec2:DescribeNetworkInterfaces`) with a STUN ICE server list (D-12); every failure path (local dev, malformed metadata, no ENI, EC2 error) degrades to `None`/STUN-only rather than raising; `inject_public_host_candidate()` munges an SDP answer to duplicate aiortc's private-IP host candidate with the public IP (Fargate's 1:1 NAT makes that address directly reachable)
- `server.py`: FastAPI app with `GET /health` (unauthenticated) and `POST /api/offer`, which validates the bearer token, then runs the injectable `start_gate(identity)` hook (default allow — 04-04 fills the body), then negotiates the SmallWebRTC connection via `SmallWebRTCRequestHandler`, injecting ICE servers and the public host candidate; a module-level `SESSIONS` dict (`pc_id -> SessionIdentity`) is the seam 04-04/04-05 attach lifecycle state to
- `Dockerfile`: `python:3.12-slim` + `libopus0`/`libvpx` (resolved dynamically via `apt-cache` — the CLAUDE.md-documented `libvpx7` doesn't exist on the current Debian trixie base, it's `libvpx9`) + `ffmpeg`, `uv`-installed locked deps in a cached layer, `uvicorn server:app` on `0.0.0.0:7860`
- Live-verified: `docker build` succeeds, and a running container answers `GET /health` with 200 and rejects an unauthenticated `POST /api/offer` with 401 — not just a build-succeeds check
- 90/90 tests pass (83 prior + 30 net-new across `test_auth.py`, `test_webrtc.py`, `test_server.py`)

## Task Commits

Each task was committed atomically:

1. **Task 1: Offline JWT validation + service-credential recognition (auth.py)** - `a8f53bc` (feat)
2. **Task 2: Public-IP + STUN ICE candidate gathering (webrtc.py)** - `98c6ce6` (feat)
3. **Task 3: Production /api/offer + /health FastAPI entrypoint and Dockerfile** - `9c8568b` (feat)

_This plan runs on the main working tree (sequential executor, no worktree) — the metadata commit below carries SUMMARY.md/STATE.md/ROADMAP.md/REQUIREMENTS.md._

## Files Created/Modified

- `apps/voice/src/klanker_voice/auth.py` - `validate_access_token`, `SessionIdentity`, `AuthError`, `recognize_service_credential`, cached `_jwk_client()`
- `apps/voice/src/klanker_voice/webrtc.py` - `gather_public_candidates`, `build_ice_servers`, `_read_task_eni_public_ip`, `inject_public_host_candidate`, `PublicCandidates`
- `apps/voice/server.py` - FastAPI `app`, `/health`, `/api/offer`, `start_gate`, `SESSIONS`, `_negotiate_webrtc`, `_run_session`
- `apps/voice/Dockerfile` - production image definition
- `apps/voice/.dockerignore` - excludes `.venv/`, `.env`, `artifacts/`, `tests/`, caches
- `apps/voice/pyproject.toml` - added `pyjwt[crypto]~=2.13.0`, `boto3>=1.42`; added `pythonpath = ["."]` to pytest config
- `apps/voice/tests/test_auth.py` - 10 tests, offline fake-JWKS fixtures
- `apps/voice/tests/test_webrtc.py` - 13 tests, fake EC2 client + metadata stubs
- `apps/voice/tests/test_server.py` - 7 tests, FastAPI `TestClient` + monkeypatched seams

## Decisions Made

See frontmatter `key-decisions`. Highlights:
- The ENI public-IP lookup uses the task-metadata MAC address (not an ENI id, which Fargate task metadata v4 doesn't expose) filtered against `ec2:DescribeNetworkInterfaces`.
- Host-candidate self-advertisement is implemented as SDP-answer text munging (`inject_public_host_candidate`) rather than an aiortc-native local-candidate injection API, because no such API exists — this mirrors the ESP32 SDP-munging pattern already present in pipecat's own runner utils, just adding a candidate instead of filtering one.
- `server.py` isolates real aiortc/ICE work behind `_negotiate_webrtc()` specifically so the unit suite can prove the auth→start_gate ordering (the plan's explicit instruction) without needing a live browser peer.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] `libvpx7` does not exist on the current `python:3.12-slim` base image**
- **Found during:** Task 3, first `docker build -t kmv-voice:dev apps/voice` attempt
- **Issue:** `python:3.12-slim`'s current tag resolves to Debian trixie, whose libvpx shared-library package is `libvpx9`, not the `libvpx7` named in CLAUDE.md's container note (written against an older Debian base). `apt-get install libvpx7` failed with `E: Unable to locate package`.
- **Fix:** Resolve the libvpx package name dynamically at build time via `apt-cache search '^libvpx[0-9]+$' | sort -V | tail -1` instead of hardcoding a version, so the Dockerfile survives the next Debian bump too.
- **Files modified:** `apps/voice/Dockerfile`
- **Verification:** `docker build` succeeds; confirmed the resolved package is `libvpx9` in this environment.
- **Committed in:** `9c8568b` (Task 3 commit)

**2. [Rule 1 - Bug] Container CMD used `uv run uvicorn ...`, which re-syncs the `dev` dependency group (pyaudio) at every startup**
- **Found during:** Task 3, first live container smoke test (post-build)
- **Issue:** `uv run` re-resolves and syncs against the lockfile's default groups before running the given command — even though the image was built with `uv sync --no-dev`. That pulled in `pipecat-ai[local]`'s `pyaudio`, which has no wheel in this image and needs `gcc`/portaudio headers not installed, so the container crashed on startup trying to compile it from source.
- **Fix:** Changed `CMD` to invoke `uvicorn` directly (already on `PATH` via the `.venv/bin` `ENV`), bypassing `uv run`'s re-sync entirely.
- **Files modified:** `apps/voice/Dockerfile`
- **Verification:** Rebuilt the image; ran a container; `GET /health` returned `200 {"status":"ok"}` and `POST /api/offer` (no token) returned `401 {"error":"unauthorized"}`. Container and image removed after verification.
- **Committed in:** `9c8568b` (Task 3 commit)

---

**Total deviations:** 2 auto-fixed (both Rule 1 bugs, both caught by actually running `docker build` + a live container rather than stopping at "Dockerfile exists")
**Impact on plan:** Both fixes were required for the Dockerfile to produce a working image at all — no scope creep, no architectural change.

## Issues Encountered

- PyJWT was not yet installed when writing `auth.py` — added `pyjwt[crypto]~=2.13.0` (CLAUDE.md-pinned) to `pyproject.toml` and ran `uv sync`, which also pulled in `boto3` (also required by `webrtc.py`) in the same pass.
- `pytest`'s default rootdir-based `sys.path` insertion (`tests/` only, not `apps/voice/`) would have made `import server` fail in `test_server.py` since `server.py` is a top-level app script outside the installed `klanker_voice` package. Added `pythonpath = ["."]` to `[tool.pytest.ini_options]` in `pyproject.toml` (Task 1, since it touches the same file Task 1 was already editing for the PyJWT/boto3 dependency addition).

## User Setup Required

None - no external service configuration required. (The Phase-3 JWKS secret is already live in SSM per 03-03-SUMMARY.md; this plan's code reads it via `KMV_OIDC_JWKS_URI`/env defaults, no new secret needed for the unit-tested code path. `KMV_SMOKE_SERVICE_TOKEN` provisioning for the live KV-05 smoke test is 04-03's concern.)

## Next Phase Readiness

- **04-02 (deploy infra):** unblocked — `server.py`/`Dockerfile` are the concrete artifacts the ECS task definition and ALB health-check config point at (`GET /health` on 7860, `uvicorn server:app` entrypoint).
- **04-03 (deployed ICE smoke test):** unblocked for the code side — `gather_public_candidates()`/`inject_public_host_candidate()` are unit-tested against synthetic ECS metadata/EC2 responses and a hand-built SDP fixture, but the *actual* Fargate task-metadata shape, the real 1:1 NAT behavior, and live ICE negotiation are unverified until a task is actually deployed. This plan's Known Gap below is exactly what 04-03 exists to prove.
- **04-04 (quota enforcement):** unblocked — `start_gate(identity)` is a single named, unit-tested seam (default allow) ready for a body swap to real concurrency/daily-floor/kill-switch logic; `SessionIdentity.bypass_accounting` is already threaded through from the smoke/service-credential path (D-15) for 04-04 to honor explicitly.
- **04-05 (idle teardown):** unblocked — the module-level `SESSIONS` dict keyed by `pc_id` is in place as the lifecycle-state attachment point.

## Known Gaps

- **Live ECS task-metadata shape is unverified.** `_extract_eni_mac()` assumes each container's `Networks[].MACAddress` field is present, based on AWS's documented task-metadata v4 schema and the common "MAC → DescribeNetworkInterfaces(mac-address filter)" self-discovery pattern — but this has only been exercised against a hand-built sample document in `test_webrtc.py`, never against a real running Fargate task's metadata endpoint. 04-03's deployed ICE smoke test is the first environment where this can be confirmed live.
- **`inject_public_host_candidate`'s SDP munging is a best-effort implementation**, not validated against a real browser ICE agent — it duplicates `typ host` candidate lines with a swapped IP and a suffixed foundation, which is a reasonable low-risk transformation but its actual interop (does the browser try it, does aiortc's own local ICE state stay consistent) is unverified until 04-03's live media test.
- **`apps/voice/src/klanker_voice/__init__.py`** was listed in the plan's `files_modified` but was not touched — it remains empty; nothing in this plan's exports required package-level re-exporting through `__init__.py`.

## Threat Flags

None beyond the plan's own threat model (T-04-01, T-04-02, T-04-05, T-04-03, T-04-SC — all addressed as designed):
- T-04-01 (forged/expired/wrong-audience JWT): mitigated — `validate_access_token` does full offline RS256 verification before any transport is created; `/api/offer` returns 401 on `AuthError`.
- T-04-02 (token/claim leakage into logs): mitigated — `AuthError` carries only an exception-class-name reason; `/api/offer`'s 401 body is a fixed `{"error": "unauthorized"}` string; no token value appears in any log statement.
- T-04-05 (smoke-credential guessing/reuse): mitigated — `recognize_service_credential` is a constant-time compare against an env-sourced value; the bypass sets only `bypass_accounting` and still traverses the full `/api/offer` transport path.
- T-04-03 (over-broad IAM for the EC2 lookup): accepted per plan — `ec2:DescribeNetworkInterfaces` is the only permission this code needs; the actual IAM policy grant is 04-02's concern.
- T-04-SC (pip install of PyJWT/boto3): mitigated — both are on the CLAUDE.md audited/pinned stack, no unverified package.

---
*Phase: 04-voice-service-deployed-quota-enforcement*
*Completed: 2026-07-05*

## Self-Check: PASSED

- Created files verified present: `apps/voice/src/klanker_voice/auth.py`, `apps/voice/src/klanker_voice/webrtc.py`, `apps/voice/server.py`, `apps/voice/Dockerfile`, `apps/voice/.dockerignore`, `apps/voice/tests/test_auth.py`, `apps/voice/tests/test_webrtc.py`, `apps/voice/tests/test_server.py`, this SUMMARY.md
- Commits verified present in `git log --oneline --all`: `a8f53bc` (Task 1), `98c6ce6` (Task 2), `9c8568b` (Task 3)
- `uv run pytest tests/` (full suite): 90/90 pass
- `docker build -t kmv-voice:dev apps/voice`: succeeds; a running container answered `GET /health` (200) and `POST /api/offer` without a token (401) — container/image removed after verification
