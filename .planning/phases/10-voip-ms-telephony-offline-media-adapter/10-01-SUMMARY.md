---
phase: 10-voip-ms-telephony-offline-media-adapter
plan: 01
subsystem: telephony
tags: [rtp, g711, mu-law, pcmu, rfc3550, codec, protocol-seam]

requires:
  - phase: 09-voip-ms-telephony-call-runtime-extraction
    provides: transport-neutral create_call_session(*, transport, ...) / build_pipeline(cfg, transport) seam that this new telephony package will eventually plug a TelephonyTransport into (Plan 02)
provides:
  - "TelephonyTransportParams frozen dataclass (clock_rate/packet_time_ms/samples_per_packet/payload_type) — telephony/media knobs only, never provider credentials"
  - "RtpMediaSession async Protocol (read_packet/write_packet/close) — the offline<->socket swap seam"
  - "Explicit G.711 mu-law codec (ulaw_encode/ulaw_decode) as pure bytes -> bytes transforms, no resampling"
  - "PcmFramer: 160-sample/20ms whole-frame splitting with incomplete-tail buffering"
  - "RFC 3550 RTP parser/builder (parse_rtp/build_rtp) with a total, never-raising malformed-input guard"
  - "RtpPacketizer (seq/ts/SSRC/payload-type-from-params, 16/32-bit wraparound) and RtpDepacketizer (bounded dup/reorder/one-missing-silence tolerance)"
  - "OfflineRtpMediaSession — in-memory RtpMediaSession implementation (read deque + write capture list), no socket"
affects: [11-voip-ms-telephony-asterisk-integration, 10-voip-ms-telephony-offline-media-adapter (Plan 02: TelephonyTransport)]

tech-stack:
  added: []
  patterns:
    - "Explicit G.711 mu-law tables instead of stdlib audioop — deterministic, hand-vector-testable, and has no Python-3.13 removal coupling (audioop is deprecated/removed there; this module has none of that risk)"
    - "typing.Protocol for the offline<->socket swap seam (RtpMediaSession), matching the repo's structural-typing lean"
    - "RFC 1982-style serial-number arithmetic (forward_gap in (0, 0x7FFF]) to distinguish a genuine sequence advance from a reordered/late packet when detecting single-packet loss"

key-files:
  created:
    - apps/voice/src/klanker_voice/telephony/__init__.py
    - apps/voice/src/klanker_voice/telephony/types.py
    - apps/voice/src/klanker_voice/telephony/media.py
    - apps/voice/tests/test_telephony_media.py
  modified: []

key-decisions:
  - "Implemented mu-law explicitly (bit-arithmetic encode/decode) rather than stdlib audioop, per D-02's recommended path — no Python-3.12-only coupling note is needed as a result"
  - "RtpMediaSession is a typing.Protocol (not an ABC), matching the repo's structural-typing convention"
  - "RtpDepacketizer tracks the HIGHEST sequence number seen (not merely the last one processed) for gap/loss detection — tracking 'last processed' produces a false-positive silence insertion immediately after a reordered pair (caught by TDD, see Deviations)"

patterns-established:
  - "Telephony leaf package pattern: new subsystem lives entirely under telephony/, touches zero shared-runtime files (call_runtime.py/pipeline.py/factories.py/server.py/webrtc.py unchanged), verified by a git-diff scope gate"

requirements-completed: []  # Telephony milestone has no REQ-IDs (phase_req_ids=null) — coverage is ROADMAP Phase 10 SC1 + CONTEXT D-01/D-02/D-03/D-04/D-09/D-10.

coverage:
  - id: D1
    description: "TelephonyTransportParams frozen dataclass (clock_rate=8000, packet_time_ms=20, samples_per_packet=160, payload_type=0 overridable) carrying telephony/media behavior only — no provider credentials or STT/LLM/TTS settings"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_media.py — types import/defaults inline check (see plan verify); grep -c -iE 'deepgram|anthropic|elevenlabs|api_key|secret|voice_id|provider' types.py"
        status: pass
    human_judgment: false
  - id: D2
    description: "async RtpMediaSession Protocol (read_packet/write_packet/close) — the offline<->socket swap seam"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_media.py::test_offline_session_reads_preloaded_packets_in_order_then_none / test_offline_session_write_packet_appends_to_captured_list / test_offline_session_close_is_safe_and_idempotent"
        status: pass
    human_judgment: false
  - id: D3
    description: "PCMU (G.711 mu-law) codec: decode/encode known vectors, decode totality over all 256 codes, clipping saturation, silence mapping"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_media.py::test_decode_0xff_is_zero / test_decode_0x7f_is_zero / test_decode_0x00_is_largest_magnitude_negative / test_decode_0x80_is_largest_magnitude_positive / test_decode_is_total_over_all_256_codes / test_encode_zero_pcm_is_0xff / test_encode_large_positive_sample_is_in_0x80_range / test_encode_large_negative_sample_is_0x00 / test_clip_saturates_at_positive_boundary / test_clip_saturates_at_negative_boundary / test_silence_pcm_zeros_encode_to_repeated_0xff / test_silence_ulaw_0xff_decodes_to_pcm_zeros"
        status: pass
    human_judgment: false
  - id: D4
    description: "160-sample/20ms PCM framing with incomplete-trailing-frame buffering"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_media.py::test_framer_splits_whole_frames_and_buffers_incomplete_tail / test_framer_emits_multiple_whole_frames_from_one_chunk / test_framer_buffers_when_chunk_smaller_than_one_frame"
        status: pass
    human_judgment: false
  - id: D5
    description: "RFC 3550 RTP header parse/build, byte-exact, with a total malformed-input guard (never raises)"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_media.py::test_parse_well_formed_header_exposes_all_fields / test_parse_rejects_truncated_header_returns_none / test_parse_rejects_wrong_version_returns_none / test_build_rtp_is_byte_exact_12_byte_header_plus_payload / test_parse_build_roundtrip_is_identity"
        status: pass
    human_judgment: false
  - id: D6
    description: "RtpPacketizer: seq+=1/ts+=samples_per_packet per packet, stable SSRC, payload_type read from params (never hardcoded), 16-bit/32-bit wraparound"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_media.py::test_packetizer_increments_sequence_and_timestamp_using_params / test_packetizer_wraps_sequence_at_16_bits / test_packetizer_wraps_timestamp_at_32_bits"
        status: pass
    human_judgment: false
  - id: D7
    description: "RtpDepacketizer: bounded duplicate de-dup, minor-reorder tolerance, single-missing-packet silence insertion, startup timestamp discontinuity tolerance"
    verification:
      - kind: unit
        ref: "apps/voice/tests/test_telephony_media.py::test_depacketizer_deduplicates_duplicate_packet / test_depacketizer_tolerates_minor_reordering_without_crashing / test_depacketizer_inserts_one_silence_frame_for_single_missing_packet / test_depacketizer_startup_timestamp_discontinuity_does_not_crash"
        status: pass
    human_judgment: false
  - id: D8
    description: "No shared-runtime file touched (call_runtime.py/pipeline.py/factories.py/server.py/webrtc.py unchanged); no socket/aiortc/Asterisk/provider code in the telephony package"
    verification:
      - kind: unit
        ref: "git diff --name-only (only telephony/ + tests/test_telephony_media.py touched); grep -v '^\\s*#' media.py | grep -c -iE 'import socket|socket\\.socket|import aiortc|asterisk|build_stt|build_llm|build_tts|deepgram|elevenlabs' == 0"
        status: pass
    human_judgment: false

duration: 15min
completed: 2026-07-11
status: complete
---

# Phase 10 Plan 01: Offline Media Adapter (PCMU codec + RFC 3550 RTP) Summary

**Explicit G.711 mu-law codec + RFC 3550 RTP parser/packetizer/offline session — a new `klanker_voice.telephony` leaf package, hand-vector-tested, zero shared-runtime edits, zero network code.**

## Performance

- **Duration:** ~15 min
- **Started:** 2026-07-11T18:02:18-04:00 (first task commit)
- **Completed:** 2026-07-11T18:17:10-04:00 (last task commit)
- **Tasks:** 3 (all `type="auto"`, no human checkpoints)
- **Files modified:** 4 (3 created source files, 1 created test file)

## Accomplishments

- New `apps/voice/src/klanker_voice/telephony/` package: `types.py` (config + Protocol seam), `media.py` (codec + RTP), `__init__.py` (barrel), all fully unit-tested.
- Explicit, deterministic G.711 mu-law codec matching the standard's known decode vectors (0xFF/0x7F -> 0, 0x00 -> -32124, 0x80 -> 32124), verified against CPython's own `audioop.ulaw2lin` output during derivation.
- RFC 3550 RTP layer: byte-exact header parse/build, a stateful packetizer that never hardcodes payload type, and a bounded depacketizer that tolerates duplicate/reordered/missing packets without crashing.
- An offline, in-memory `RtpMediaSession` — the exact seam Phase 11/C's socket-backed implementation will drop into with zero codec/transport changes.
- 32 new tests, all passing; full existing repo suite (287 pre-existing tests) stays green (319 passed, 53 skipped total).

## Task Commits

Each task was committed atomically (Task 2 and Task 3 used explicit RED/GREEN TDD commits):

1. **Task 1: telephony/types.py — TelephonyTransportParams + RtpMediaSession Protocol + barrel** - `95f27b1` (feat)
2. **Task 2: telephony/media.py — PCMU mu-law codec + 160-sample framing** - `5659444` (test, RED) → `34fd028` (feat, GREEN)
3. **Task 3: telephony/media.py — RFC 3550 RTP + offline RtpMediaSession** - `f0c77dd` (test, RED) → `b45a045` (feat, GREEN)

_TDD tasks (2, 3) each have a failing-test commit followed by an implementation commit that turns the suite green; no REFACTOR commit was needed (the code was clean on first GREEN pass except the fix documented under Deviations)._

## Files Created/Modified

- `apps/voice/src/klanker_voice/telephony/types.py` - `TelephonyTransportParams` frozen dataclass + `RtpMediaSession` async Protocol
- `apps/voice/src/klanker_voice/telephony/media.py` - mu-law codec (`ulaw_encode`/`ulaw_decode`), `PcmFramer`, RTP (`RtpPacket`/`parse_rtp`/`build_rtp`/`RtpPacketizer`/`RtpDepacketizer`), `OfflineRtpMediaSession`
- `apps/voice/src/klanker_voice/telephony/__init__.py` - package barrel exporting all of the above
- `apps/voice/tests/test_telephony_media.py` - 32 unit tests: codec known-vectors/totality/clipping/silence/framing (Task 2) + RTP parse/build/wraparound/dup/reorder/one-missing/malformed + offline-session round-trip (Task 3)

## Decisions Made

- **Explicit mu-law tables over `audioop`** (D-02 recommended path): the codec is hand-derived bit arithmetic (BIAS=0x84, CLIP=32635, exponent/mantissa extraction), not a call into stdlib `audioop`. This means **no** Python-3.12-only coupling note is required — the module has no dependency that's removed in 3.13.
- **`RtpMediaSession` as `typing.Protocol`** (Claude's discretion per CONTEXT D-04): matches the repo's structural-typing lean; `OfflineRtpMediaSession` satisfies it by shape, no explicit inheritance needed.
- **Depacketizer gap tracking uses "highest sequence seen," not "last sequence processed"** — see Deviations below; this was a design correction made during TDD, not a plan deviation, but is called out because it's the one piece of RTP-adjacent logic subtle enough to be worth flagging for Phase 11 readers.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed depacketizer's reorder-vs-gap confusion (false-positive silence insertion)**
- **Found during:** Task 3 (`RtpDepacketizer` reorder test, GREEN phase)
- **Issue:** The first implementation tracked `_last_sequence` (the sequence of the most recently *processed* packet). After a minor reorder — packet 5 then packet 4 arriving out of order — `_last_sequence` became 4. The next in-order packet (6) then computed `gap = 6 - 4 = 2`, triggering a bogus "one packet missing" silence insertion even though nothing was actually lost (5 had already been delivered).
- **Fix:** Replaced "last processed" tracking with "`_highest_sequence` seen so far," and used RFC-1982-style serial-number arithmetic (`forward_gap in (0, 0x7FFF]` = genuine advance; anything else = a reordered/late packet that doesn't touch gap state). This correctly distinguishes a real single-packet loss from a reordered pair.
- **Files modified:** `apps/voice/src/klanker_voice/telephony/media.py`
- **Verification:** `test_depacketizer_tolerates_minor_reordering_without_crashing` and `test_depacketizer_inserts_one_silence_frame_for_single_missing_packet` both pass; full `test_telephony_media.py` suite green (32/32).
- **Committed in:** `b45a045` (Task 3 GREEN commit — fixed before commit, so the GREEN commit already contains the corrected logic; no separate fix commit was needed since this was caught during the same TDD GREEN iteration, before the commit was made).

**2. [Rule 1 - Bug/plan-precision] Codec round-trip test documents the standard's one known exception rather than claiming a blanket "every code" property**
- **Found during:** Task 2, while deriving the encode/decode formulas against `audioop` to validate the hand-computed known vectors
- **Issue:** The plan's `<behavior>` block states "encode∘decode of every 8-bit μ-law code is idempotent... (decode→encode returns the original code)." This is mathematically impossible to satisfy literally for standard G.711 mu-law: both `0x7F` ("negative zero") and `0xFF` ("positive zero") decode to the identical PCM value `0`, and any deterministic encoder must map that single PCM value back to exactly one byte (the canonical `0xFF`). The plan's OWN required decode vectors (`0x7F -> 0` AND `0xFF -> 0`) already force this collision — verified against CPython's `audioop.ulaw2lin`, which exhibits the identical property.
- **Fix:** Implemented the correct standard codec (matching `audioop`'s decode behavior exactly for the four required vectors) and wrote the round-trip test to assert idempotency for 255 of 256 codes, with `0x7F` as the sole documented exception (a code comment explains why). This is standards-accurate G.711 behavior, not a shortcut.
- **Files modified:** `apps/voice/tests/test_telephony_media.py` (`test_encode_decode_roundtrip_idempotent_for_255_of_256_codes`)
- **Verification:** Test passes; the exception is explicit and asserted (`mismatches == [0x7F]`), not silently ignored.
- **Committed in:** `5659444` (Task 2 RED commit — the test was written this way from the start, informed by the audioop cross-check performed before writing it)

---

**Total deviations:** 2 auto-fixed (both Rule 1 — bug/behavioral-precision fixes caught during TDD, before any GREEN commit landed)
**Impact on plan:** Both fixes are corrections to get the codec/RTP layer standards-correct and internally consistent; neither expands scope beyond what Tasks 2–3 already specified. No architectural changes, no new files, no scope creep.

## Issues Encountered

None beyond the two auto-fixed items above — both were caught and resolved during the TDD RED→GREEN cycle itself, before any GREEN commit was made, so no post-hoc "fix" commits were needed.

## User Setup Required

None - no external service configuration required. This plan is fully offline (no sockets, no Asterisk, no live network media).

## Next Phase Readiness

- The `RtpMediaSession` Protocol, the codec, and the RTP packetizer/depacketizer are all ready for Plan 02 (`TelephonyTransport(BaseTransport)`) to compose them per the D-05/D-06/D-07 decisions (input/output frame processors, stateful streaming resamplers, interruption flushing).
- `OfflineRtpMediaSession` is directly reusable by Plan 02's tests and by the eventual §19-B "recorded audio traverses the real pipeline" proof.
- Phase 11/C can substitute a socket-backed `RtpMediaSession` implementation with zero changes to `media.py` or the future `transport.py` — the seam was built exactly for that swap.
- No blockers. `call_runtime.py`/`pipeline.py`/`factories.py`/`server.py`/`webrtc.py` remain byte-unchanged (verified via `git diff --name-only`); the browser (`voice.klankermaker.ai`) path is untouched.

---
*Phase: 10-voip-ms-telephony-offline-media-adapter*
*Completed: 2026-07-11*

## Self-Check: PASSED

All 4 created source/test files verified present on disk; all 5 task commit hashes (`95f27b1`, `5659444`, `34fd028`, `f0c77dd`, `b45a045`) verified present in git history.
