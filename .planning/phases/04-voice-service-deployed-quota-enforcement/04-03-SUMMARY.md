---
phase: 04-voice-service-deployed-quota-enforcement
plan: 03
subsystem: infra (deploy/WebRTC) + testing
tags: [webrtc, pion, aiortc, go, cobra, pytest, ice, rtp, smoke-test]

# Dependency graph
requires:
  - phase: 04-01
    provides: "apps/voice server.py /api/offer entrypoint, auth.py smoke/service credential (KMV_SMOKE_SERVICE_TOKEN), webrtc.py public-IP+STUN candidate gathering"
  - phase: 04-02
    provides: "voice ECS task/service infra (public IP, narrowed UDP SG 20000-20100) that Task 3 will deploy onto"
provides:
  - "kv smoke Go command (offer -> ICE connected -> RTP-flow assertion via pion/webrtc) — builds, vets, and its help/registration are verified; NOT yet run against a deployed endpoint"
  - "apps/voice/tests/test_smoke.py in-process transport-sanity test: real aiortc SDP negotiation against /api/offer with a stubbed bypass_accounting identity, no deploy/network"
affects: ["04-03 Task 3 (deploy checkpoint, orchestrator-driven)", "KV-05", "INFR-03"]

# Tech tracking
tech-stack:
  added: ["github.com/pion/webrtc/v4 v4.2.16 (kv's Go WebRTC client stack)"]
  patterns:
    - "kv subcommands report PASS/FAIL via tabwriter/--json output + non-zero exit on FAIL — smoke.go follows the code.go/tier.go CLI shape"
    - "Test isolates the real aiortc offer/answer negotiation from the full Pipecat pipeline by stubbing server._run_session (not server._negotiate_webrtc) — keeps the actual negotiation code path live while avoiding provider-credential dependencies in CI"

key-files:
  created: ["kv/internal/app/cmd/smoke.go"]
  modified: ["kv/internal/app/cmd/root.go", "kv/go.mod", "kv/go.sum", "apps/voice/tests/test_smoke.py"]

key-decisions:
  - "kv smoke uses pion/webrtc/v4 v4.2.16 (latest stable, verified on pkg.go.dev) as the Go WebRTC client stack — matches the plan's threat-register disposition (T-04-SC: standard, actively-maintained library, Go module checksum-pinned)"
  - "Task 2's transport-sanity test stubs server._run_session (the per-session Pipecat pipeline: Deepgram STT + Anthropic LLM + ElevenLabs TTS) rather than server._negotiate_webrtc — this keeps the real aiortc SDP offer/answer negotiation exercised (the actual seam the test targets) while avoiding a hang/failure from missing provider credentials in the test environment (see Deviations)"

patterns-established:
  - "kv smoke: pion PeerConnection (recvonly audio transceiver) -> CreateOffer -> GatheringCompletePromise -> POST /api/offer with Authorization: Bearer <KMV_SMOKE_SERVICE_TOKEN> -> SetRemoteDescription(answer) -> wait ICEConnectionState via channel -> OnTrack RTP-packet counter over a bounded window -> PASS iff RTPPackets > 0"

requirements-completed: []
# KV-05 and INFR-03 are NOT marked complete by this SUMMARY: both require Task 3's
# deployed ICE/RTP proof against the live public-IP Fargate task, which is explicitly
# out of scope for this executor run (deferred to the orchestrator per plan gate="blocking").

coverage:
  - id: D1
    description: "kv smoke Go command: synthetic pion/webrtc offer -> POST /api/offer -> ICE-connected wait (bounded 15s) -> RTP-frame count over a 5s window -> PASS/FAIL report (candidate types, final ICE state, RTP count), credential never printed"
    requirement: KV-05
    verification:
      - kind: unit
        ref: "cd kv && go build ./... && go vet ./... (SMOKE-BUILT marker)"
        status: pass
      - kind: unit
        ref: "cd kv && go test ./... (all existing kv tests still pass)"
        status: pass
      - kind: other
        ref: "kv smoke --help renders; grep confirms pion/webrtc in go.mod and NewSmokeCmd in root.go (REGISTERED marker); kv smoke with no KMV_SMOKE_SERVICE_TOKEN fails fast with a clear error"
        status: pass
    human_judgment: true
    rationale: "The command's actual offer -> ICE-connected -> RTP-flow behavior against a real public-IP Fargate task is unverified until Task 3's deploy checkpoint runs it against the live https://voice.klankermaker.ai endpoint — this plan run only proves the command builds correctly, is wired into the CLI, and its request/response shape matches server.py's contract."
  - id: D2
    description: "In-process transport-sanity test: /api/offer negotiates a real aiortc SDP answer (type=answer, non-empty sdp) for a stubbed bypass_accounting identity, exercising the real offer-handling code path with no deploy, no real ICE connect, and no real media flow"
    requirement: KV-05
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_smoke.py::test_offer_negotiates_real_sdp_answer_for_stubbed_identity"
        status: pass
      - kind: unit
        ref: "apps/voice full suite: uv run pytest -q (91 passed)"
        status: pass
    human_judgment: false

# Metrics
duration: ~50min
completed: 2026-07-05
status: blocked
---

# Phase 4 Plan 03 (Tasks 1-2 of 3): kv smoke command + in-process transport-sanity test

**pion/webrtc-based `kv smoke` CLI (offer -> ICE-connected -> RTP-flow assertion) plus an aiortc in-process pytest pre-flight for `/api/offer` — both green; the deployed proof itself (Task 3) is deferred to the orchestrator with live AWS.**

## Performance

- **Duration:** ~50 min
- **Tasks:** 2 of 3 completed (Task 3 is a `checkpoint:human-verify` deploy gate, explicitly out of scope for this run)
- **Files modified:** 5 (kv/go.mod, kv/go.sum, kv/internal/app/cmd/smoke.go, kv/internal/app/cmd/root.go, apps/voice/tests/test_smoke.py)

## Accomplishments

- **Task 1 — `kv smoke` Go command** (KV-05, D-15): builds a real pion/webrtc `PeerConnection` with a recvonly audio transceiver, creates a genuine SDP offer, POSTs it to `<endpoint>/api/offer` with the smoke/service credential in the `Authorization: Bearer` header (matching `server.py`'s `SmallWebRTCRequest` shape exactly), sets the returned answer as the remote description, waits (bounded 15s) for `ICEConnectionState` connected/completed, and counts inbound RTP packets over a 5s window via `OnTrack`. Reports PASS/FAIL with candidate types (host/srflx), final ICE state, and RTP packet count — as a tabwriter table or `--json`. The credential is read once from `KMV_SMOKE_SERVICE_TOKEN` and never printed or logged. Registered in `root.go` alongside `code`/`tier`.
- **Task 2 — CI transport-sanity test** (`apps/voice/tests/test_smoke.py`): a new test drives a *real* aiortc offer/answer negotiation in-process against the FastAPI `/api/offer` route (via `TestClient`), with `validate_access_token` stubbed to return a `bypass_accounting` `SessionIdentity` matching the `service:smoke` / `no-access` shape from `auth.py`. Asserts the response is `{"type": "answer", "sdp": "<non-empty>"}`. This is the fast pre-flight the plan calls for: if the offer handler can't negotiate an answer locally, there's no point attempting the live deploy in Task 3.
- Verified both changes don't regress existing suites: `kv`'s full Go test suite (`go test ./...`) and the full `apps/voice` pytest suite (91 tests) both pass unchanged.

## Task Commits

Each task was committed atomically:

1. **Task 1: kv smoke — synthetic offer -> ICE connected -> RTP-flow assertion (pion, Go)** - `7766e92` (feat)
2. **Task 2: CI transport-sanity test for /api/offer (no deploy)** - `927315f` (test)

**Task 3 (deploy + deployed ICE/RTP smoke run, INFR-03 proof) is a `checkpoint:human-verify` gate requiring live AWS credentials, a real terragrunt apply, and a container build/push through the Phase-2 CI/OIDC path. It is explicitly deferred to the orchestrator, which has live AWS access. No commit for Task 3 exists yet.**

## Files Created/Modified

- `kv/internal/app/cmd/smoke.go` (new) - `NewSmokeCmd`, `runSmoke`, `waitForICEConnected`, `postOffer`, `printSmokeResult` — the full offer->ICE->RTP smoke client
- `kv/internal/app/cmd/root.go` - registers `NewSmokeCmd` in `NewRootCmd`
- `kv/go.mod` / `kv/go.sum` - add `github.com/pion/webrtc/v4` v4.2.16 (+ transitive pion/ice, pion/dtls, pion/sctp, pion/srtp, pion/turn, etc.)
- `apps/voice/tests/test_smoke.py` - adds `_build_synthetic_offer` helper (real aiortc offer, no network) and `test_offer_negotiates_real_sdp_answer_for_stubbed_identity`

## Decisions Made

- **pion/webrtc/v4 pinned at v4.2.16** (latest stable on pkg.go.dev as of 2026-07-05, verified via `go list -m -versions`) rather than a beta — this is the production smoke client, not a prototype.
- **Task 2 stubs `server._run_session`, not `server._negotiate_webrtc`.** The plan's read-first files and `test_server.py`'s own doc comment establish the precedent of stubbing `_negotiate_webrtc` to avoid real WebRTC negotiation in unit tests — but that precedent was set for testing the *auth/start_gate* seam, not the *negotiation* seam. Task 2's whole purpose is to exercise the real offer->answer negotiation, so `_negotiate_webrtc` must run unstubbed. Discovered during implementation: `_negotiate_webrtc`'s connection callback fires `asyncio.create_task(_run_session(connection))` — a fire-and-forget task that builds the *entire* production Pipecat pipeline (Deepgram STT + Anthropic LLM + ElevenLabs TTS clients), which hangs/blocks in a test environment with no real provider credentials or network. Stubbing only `_run_session` (the background pipeline runner) isolates exactly the offer/answer negotiation seam this test targets, without reaching into unrelated provider wiring. See Deviations below.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Stubbed `server._run_session` in the Task 2 transport-sanity test to avoid a real-provider-credential hang**
- **Found during:** Task 2 (writing the in-process `/api/offer` transport-sanity test)
- **Issue:** Driving a real, unstubbed `/api/offer` call (needed to exercise genuine aiortc SDP negotiation) triggers `server._negotiate_webrtc`'s connection callback, which does `asyncio.create_task(_run_session(connection))`. `_run_session` builds the full production pipeline (`build_pipeline` -> Deepgram/Anthropic/ElevenLabs service construction) as a background task. In the test environment (no real API keys, no network egress expected for a "no AWS" pre-flight test), this background task hung indefinitely — confirmed via an isolated repro script that the hang occurs specifically in pipeline/provider construction, not in the aiortc offer/answer negotiation itself (which completes in <20ms).
- **Fix:** `monkeypatch.setattr(server, "_run_session", AsyncMock())` before posting the offer. This leaves the real negotiation path (`SmallWebRTCRequestHandler.handle_web_request` -> `SmallWebRTCConnection.initialize` -> real aiortc `setRemoteDescription`/`createAnswer`/`setLocalDescription`) fully exercised, while the fire-and-forget session-runner task becomes a no-op instead of constructing real provider clients.
- **Files modified:** `apps/voice/tests/test_smoke.py`
- **Verification:** Isolated repro script confirmed the hang (killed after 90s+ CPU-idle wait) and confirmed the fix (response returned in 0.01s with a valid SDP answer) before the change was applied to the actual test file; `uv run pytest tests/test_smoke.py -x -q` passes (3 passed) and the full `apps/voice` suite passes (91 passed).
- **Committed in:** `927315f` (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (1 blocking)
**Impact on plan:** Necessary to make the transport-sanity test actually exercise real aiortc negotiation without depending on live provider credentials/network in CI. No scope creep — the stub targets exactly the background pipeline construction the plan's "without requiring real media to flow" language already excludes from this test's scope.

## Issues Encountered

None beyond the deviation above (which was resolved within Task 2's own scope).

## User Setup Required

None for Tasks 1-2. Task 3 (deferred) requires:
- The ElevenLabs API key SOPS entry populated (flagged in STATE.md as a Phase-4 blocker)
- A refreshed AWS SSO session (`aws sso login --profile klanker-terraform`) — STATE.md notes the session expired during 04-02 and must be refreshed before any `terragrunt apply`
- `KMV_SMOKE_SERVICE_TOKEN` sourced from SSM `/kmv/secrets/use1/voice/smoke_token` to actually run `kv smoke` against the deployed endpoint

## Next Phase Readiness

- **Ready for Task 3:** `kv smoke` is built, vetted, and wired into the CLI; the transport-sanity test proves `/api/offer`'s negotiation seam works in isolation. Both give the orchestrator's Task 3 deploy checkpoint a green pre-flight before it applies the 04-02 infra and deploys the 04-01 container.
- **Not ready / still open:** KV-05 and INFR-03 remain unverified in the "deployed, real ICE/RTP" sense — only Task 3, run by the orchestrator with live AWS credentials, can close them. `requirements-completed` is deliberately left empty in this SUMMARY's frontmatter; do not mark KV-05/INFR-03 complete in REQUIREMENTS.md until Task 3 reports `kv smoke` PASS against `https://voice.klankermaker.ai`.
- **This plan (04-03) is NOT complete.** Do not advance the plan counter or update ROADMAP.md plan-progress for 04-03 until Task 3 finishes.

---
*Phase: 04-voice-service-deployed-quota-enforcement*
*Tasks 1-2 completed: 2026-07-05 (Task 3 deferred to orchestrator)*
