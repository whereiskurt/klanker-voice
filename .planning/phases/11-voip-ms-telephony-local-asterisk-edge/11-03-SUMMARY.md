---
phase: 11-voip-ms-telephony-local-asterisk-edge
plan: 03
subsystem: telephony
tags: [asyncio, udp, rtp, asterisk, external-media, symmetric-rtp]

requires:
  - phase: 10-voip-ms-telephony-offline-media-adapter
    provides: "types.RtpMediaSession Protocol + OfflineRtpMediaSession contract, TelephonyTransport consuming media via the Protocol"
provides:
  - "SocketRtpMediaSession: a socket-backed types.RtpMediaSession implementation over real asyncio UDP"
  - "_AsteriskRtpProtocol: bind-first DatagramProtocol with symmetric-RTP first-packet source learning"
affects: [11-04, 11-05, 11-06, 11-07]

tech-stack:
  added: []
  patterns:
    - "asyncio.DatagramProtocol callbacks never raise (hostile-input posture, T-11-03-01) -- swallow + loguru debug-log"
    - "bind-before-signal: SocketRtpMediaSession.open() binds via create_datagram_endpoint BEFORE the controller creates Asterisk's externalMedia channel (client-only connection_type)"
    - "symmetric RTP: peer (ip,port) is learned per-packet from datagram_received(addr), never fixed at construction"

key-files:
  created:
    - apps/voice/src/klanker_voice/telephony/rtp_socket.py
    - apps/voice/tests/test_telephony_rtp_socket.py
  modified: []

key-decisions:
  - "Task 1 and Task 2 executed together in one TDD pass: the seam-conformance test (TelephonyTransport construction around SocketRtpMediaSession) was written into the same RED test file as Task 1's round-trip/close/malformed-datagram tests, since it required no new production code -- only one feat commit was needed"
  - "close() short-circuits read_packet() via an explicit _closed flag (returns None immediately) in addition to connection_lost's queued None sentinel -- the flag handles calls made after close(), the sentinel unblocks a reader already awaiting the queue when close() happens concurrently"

patterns-established:
  - "SocketRtpMediaSession.bound_port exposes the OS-assigned ephemeral port (bind_port=0) for the controller to advertise in external_host -- read via transport.get_extra_info('sockname')"

requirements-completed: [D-03]

coverage:
  - id: D1
    description: "SocketRtpMediaSession satisfies types.RtpMediaSession (read_packet/write_packet/close) over real asyncio UDP, binding before any peer is known"
    requirement: "D-03"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_rtp_socket.py#test_round_trip_over_loopback_learns_peer_and_echoes"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_rtp_socket.py#test_close_is_idempotent_and_read_after_close_returns_none"
        status: pass
    human_judgment: false
  - id: D2
    description: "write_packet is a safe no-op before any inbound datagram is received (peer unknown)"
    requirement: "D-03"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_rtp_socket.py#test_write_before_any_inbound_datagram_is_a_noop"
        status: pass
    human_judgment: false
  - id: D3
    description: "A short/garbage datagram delivered to datagram_received does not raise and does not wedge the protocol (T-11-03-01)"
    requirement: "D-03"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_rtp_socket.py#test_short_malformed_datagram_does_not_crash_the_protocol"
        status: pass
    human_judgment: false
  - id: D4
    description: "TelephonyTransport (Phase 10, unchanged) accepts SocketRtpMediaSession as its media without any codec/transport change -- proves the seam"
    requirement: "D-03"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_rtp_socket.py#test_socket_rtp_media_session_satisfies_telephony_transport_seam"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_transport.py (full suite, unmodified)"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_telephony_media.py (full suite, unmodified)"
        status: pass
    human_judgment: false

duration: 15min
completed: 2026-07-12
status: complete
---

# Phase 11 Plan 03: Socket-backed RtpMediaSession Summary

**`SocketRtpMediaSession` over real asyncio UDP -- binds first, learns Asterisk's peer via symmetric-RTP first-packet source learning, and satisfies Phase 10's `RtpMediaSession` Protocol byte-for-byte with `media.py`/`transport.py` untouched (D-03).**

## Performance

- **Duration:** 15 min
- **Started:** 2026-07-12T00:04:58-04:00
- **Completed:** 2026-07-12T00:09:XX-04:00
- **Tasks:** 2 (combined into one TDD RED/GREEN cycle -- see Deviations)
- **Files modified:** 2 (both new)

## Accomplishments

- `klanker_voice.telephony.rtp_socket._AsteriskRtpProtocol`: an `asyncio.DatagramProtocol` that queues inbound datagrams into an `asyncio.Queue`, learns the sender's `(ip, port)` on every packet (symmetric RTP, R2), and never raises out of `datagram_received`/`error_received` (T-11-03-01) -- swallowed and logged via `loguru.logger.debug`.
- `SocketRtpMediaSession.open(bind_host, bind_port)`: an async factory that binds via `loop.create_datagram_endpoint` BEFORE returning -- satisfying R2's hard ordering requirement (Asterisk's `externalMedia` only supports `connection_type=client`, so Asterisk always dials out; Klanker must already be listening or the first datagrams are silently dropped).
- `read_packet`/`write_packet`/`close` satisfy `types.RtpMediaSession` verbatim, mirroring `OfflineRtpMediaSession`'s contract: `read_packet()` returns `None` at end-of-stream (immediately post-`close()`, or via a `connection_lost`-pushed sentinel for a reader already blocked on the queue); `write_packet()` is a safe no-op (logged) before any peer is known; `close()` is idempotent.
- `bound_port` property exposes the OS-assigned ephemeral port (`get_extra_info("sockname")`) for the controller to advertise in `externalMedia`'s `external_host`.
- Zero changes to `media.py` or `transport.py` -- verified via `git diff --stat` (empty) -- the Phase 10 codec/transport seam is reused byte-unchanged.
- `TelephonyTransport(media=SocketRtpMediaSession(...), ...)` constructs without error, structurally proving D-03's "drops into the seam" claim.

## Task Commits

Each task was committed atomically (TDD RED -> GREEN):

1. **Task 1 RED: failing tests** - `bfaf246` (test) -- round-trip loopback + peer learning, write-before-inbound no-op, idempotent close, hostile short-datagram tolerance, AND the Task 2 seam-conformance test (see Deviations)
2. **Task 1+2 GREEN: implementation** - `82e1509` (feat) -- `_AsteriskRtpProtocol` + `SocketRtpMediaSession`; all 5 tests pass; Phase 10 suites unaffected (45/45 across `test_telephony_rtp_socket.py` + `test_telephony_transport.py` + `test_telephony_media.py`)

**Plan metadata:** (this commit, following)

## Files Created/Modified

- `apps/voice/src/klanker_voice/telephony/rtp_socket.py` - `_AsteriskRtpProtocol` (bind-first UDP DatagramProtocol, symmetric-RTP source learning) + `SocketRtpMediaSession` (satisfies `types.RtpMediaSession`)
- `apps/voice/tests/test_telephony_rtp_socket.py` - 5 tests: round-trip loopback, write-before-inbound no-op, idempotent close/end-of-stream, hostile short-datagram tolerance, TelephonyTransport seam-conformance construction

## Decisions Made

- Combined Task 1 (implementation) and Task 2 (seam-conformance test) into a single TDD RED/GREEN cycle: the plan's own Task 2 acceptance criteria required no new production code, only an additional test in the same file already being written for Task 1's RED phase. Writing all 5 tests together in one RED commit, then the one implementation in one GREEN commit, avoided a redundant empty "Task 2" commit while still satisfying every acceptance criterion for both tasks.
- `read_packet()` after `close()` returns `None` via two complementary mechanisms: an explicit `_closed` flag (checked synchronously, handles calls made after `close()` returns) plus `connection_lost`'s queued `None` sentinel (unblocks a reader already awaiting the queue when `close()` runs concurrently with a pending `read_packet()`). Neither alone covers both cases.

## Deviations from Plan

### Auto-fixed Issues

None - no bugs/blockers encountered; the seam and Protocol contract matched Phase 10's existing `OfflineRtpMediaSession` exactly as documented, and `create_datagram_endpoint` worked as the research (R2) described.

### Structural Deviation (documented, not a Rule 1-4 fix)

**1. Task 1 + Task 2 merged into one commit pair, not two**
- **Found during:** Task 1's TDD RED phase
- **Rationale:** Task 2's own acceptance criteria ("a `read_packet`/`write_packet`/`close`-shaped duck-type check plus successful `TelephonyTransport(media=session, ...)` construction") required zero new implementation code -- only a test. Since I write TDD RED tests as one cohesive test file before any implementation, the natural TDD flow put Task 2's test in the same RED commit as Task 1's tests, and both were proven GREEN by the same single implementation commit.
- **Files affected:** `apps/voice/tests/test_telephony_rtp_socket.py` (contains both Task 1's and Task 2's tests)
- **Verification:** `cd apps/voice && uv run pytest tests/test_telephony_rtp_socket.py tests/test_telephony_transport.py tests/test_telephony_media.py -q` -> 45 passed (plan's own `<verification>` command, run verbatim)
- **Committed in:** `bfaf246` (RED, both tasks' tests), `82e1509` (GREEN, both tasks' implementation need)

---

**Total deviations:** 1 structural (task-boundary merge, no scope change)
**Impact on plan:** None on scope or correctness -- every acceptance criterion for both Task 1 and Task 2 is met and independently verifiable; only the commit-per-task granularity differs from the plan's literal task list.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required. This module is pure `asyncio` stdlib (no new dependency); it is not yet wired into any controller/entrypoint (that is a later Phase 11 plan, per `11-RESEARCH.md`'s D-08 note).

## Verification

- `cd apps/voice && uv run pytest tests/test_telephony_rtp_socket.py tests/test_telephony_transport.py tests/test_telephony_media.py -q` -- 45 passed.
- `git diff --stat apps/voice/src/klanker_voice/telephony/media.py apps/voice/src/klanker_voice/telephony/transport.py` -- empty (zero changes; seam reused verbatim).
- Full project test suite (`uv run pytest -q` from `apps/voice`): 353 passed, 53 skipped (live-provider/eval-dependent tests, pre-existing and unrelated to this plan), 0 failed.
- `uv run ruff check` on both new files: all checks passed.

## Next Phase Readiness

- `SocketRtpMediaSession` is ready to be wired into a live Asterisk-facing controller (D-08, a later Phase 11 plan) -- `SocketRtpMediaSession.open(bind_host, 0)` then `.bound_port` gives the controller everything it needs to call `POST /ari/channels/externalMedia` with a correct `external_host`.
- No blockers for 11-04 onward. The socket layer is deliberately narrow (no controller/ARI-client wiring here) per this plan's own declared file scope.

---
*Phase: 11-voip-ms-telephony-local-asterisk-edge*
*Completed: 2026-07-12*

## Self-Check: PASSED

- FOUND: apps/voice/src/klanker_voice/telephony/rtp_socket.py
- FOUND: apps/voice/tests/test_telephony_rtp_socket.py
- FOUND: bfaf246 (test commit)
- FOUND: 82e1509 (feat commit)
- FOUND: 33faf95 (docs commit)
