"""Phase 10 (offline media adapter): PCMU codec + framing unit tests (D-02, D-10).

Pure-function, table-driven tests against hand-computed G.711 mu-law known
vectors -- no async harness needed for the codec (mirrors
``test_pronunciation_filter.py`` / ``test_tts_energy.py`` style). The RTP
parser/packetizer + offline session tests (D-03/D-04) are appended in the
same module by Task 3.
"""

import struct

from klanker_voice.telephony.media import (
    OfflineRtpMediaSession,
    PcmFramer,
    RtpDepacketizer,
    RtpPacket,
    RtpPacketizer,
    build_rtp,
    parse_rtp,
    ulaw_decode,
    ulaw_encode,
)
from klanker_voice.telephony.types import TelephonyTransportParams

# --- helpers -----------------------------------------------------------


def _pcm(*samples: int) -> bytes:
    """Pack signed 16-bit little-endian PCM samples into bytes."""
    return struct.pack(f"<{len(samples)}h", *samples)


def _unpack_pcm(data: bytes) -> tuple[int, ...]:
    return struct.unpack(f"<{len(data) // 2}h", data)


# --- decode: hand-computed known vectors (G.711 mu-law, standard formula) --


def test_decode_0xff_is_zero():
    # mu-law 0xFF -> "positive zero": inverted=0x00, sign=0, exponent=0,
    # mantissa=0 -> magnitude=((0<<3)+132)-132=0.
    assert _unpack_pcm(ulaw_decode(bytes([0xFF]))) == (0,)


def test_decode_0x7f_is_zero():
    # mu-law 0x7F -> "negative zero": inverted=0x80, sign=0x80, exponent=0,
    # mantissa=0 -> magnitude=0, sign flips it to -0 == 0.
    assert _unpack_pcm(ulaw_decode(bytes([0x7F]))) == (0,)


def test_decode_0x00_is_largest_magnitude_negative():
    # mu-law 0x00 -> inverted=0xFF, sign=0x80, exponent=7, mantissa=15 ->
    # magnitude=((15<<3)+132)<<7 - 132 = 32124, negated.
    assert _unpack_pcm(ulaw_decode(bytes([0x00]))) == (-32124,)


def test_decode_0x80_is_largest_magnitude_positive():
    # mu-law 0x80 -> inverted=0x7F, sign=0, exponent=7, mantissa=15 ->
    # magnitude=32124, positive.
    assert _unpack_pcm(ulaw_decode(bytes([0x80]))) == (32124,)


def test_decode_is_total_over_all_256_codes():
    # Every byte 0x00..0xFF must decode without raising (T-10-02).
    all_codes = bytes(range(256))
    pcm = ulaw_decode(all_codes)
    assert len(pcm) == 256 * 2
    # Sanity: every decoded sample fits in signed 16-bit range.
    for sample in _unpack_pcm(pcm):
        assert -32768 <= sample <= 32767


# --- encode: hand-computed known vectors --------------------------------


def test_encode_zero_pcm_is_0xff():
    assert ulaw_encode(_pcm(0)) == bytes([0xFF])


def test_encode_large_positive_sample_is_in_0x80_range():
    # int16 max (32767) clips to the encoder's CLIP ceiling (32635) and lands
    # in the top mu-law code (exponent=7, mantissa=15): 0x80.
    encoded = ulaw_encode(_pcm(32767))
    assert encoded == bytes([0x80])
    assert 0x80 <= encoded[0] <= 0x8F


def test_encode_large_negative_sample_is_0x00():
    # int16 min (-32768) clips to -CLIP and lands at exponent=7, mantissa=15,
    # sign set -> byte 0x00.
    assert ulaw_encode(_pcm(-32768)) == bytes([0x00])


def test_encode_decode_roundtrip_idempotent_for_255_of_256_codes():
    # decode(code) -> encode(decoded) returns the original byte for every
    # code EXCEPT the one well-known G.711 dual-zero-representation
    # collision: both 0x7F ("negative zero") and 0xFF ("positive zero")
    # decode to PCM 0, and re-encoding 0 always yields the canonical 0xFF.
    # This is a documented property of the standard, not a codec bug.
    mismatches = []
    for code in range(256):
        pcm = ulaw_decode(bytes([code]))
        back = ulaw_encode(pcm)[0]
        if back != code:
            mismatches.append(code)
    assert mismatches == [0x7F], (
        f"expected only the 0x7F dual-zero exception, got {mismatches!r}"
    )


# --- clipping: saturate at int16 bounds, no overflow/wrap ----------------


def test_clip_saturates_at_positive_boundary():
    # Every magnitude at/above the encoder's clip ceiling (32635) saturates
    # to the same top code -- no wraparound.
    boundary = ulaw_encode(_pcm(32635))
    above = ulaw_encode(_pcm(32767))
    assert boundary == above == bytes([0x80])


def test_clip_saturates_at_negative_boundary():
    boundary = ulaw_encode(_pcm(-32635))
    below = ulaw_encode(_pcm(-32768))
    assert boundary == below == bytes([0x00])


# --- silence --------------------------------------------------------------


def test_silence_pcm_zeros_encode_to_repeated_0xff():
    silence_pcm = _pcm(0, 0, 0, 0)
    assert ulaw_encode(silence_pcm) == bytes([0xFF, 0xFF, 0xFF, 0xFF])


def test_silence_ulaw_0xff_decodes_to_pcm_zeros():
    assert _unpack_pcm(ulaw_decode(bytes([0xFF, 0xFF, 0xFF]))) == (0, 0, 0)


# --- framing: 160-sample/20ms whole-frame splitting + incomplete buffer --


def test_framer_splits_whole_frames_and_buffers_incomplete_tail():
    framer = PcmFramer(samples_per_packet=160)
    # One full frame (320 bytes) plus 100 extra bytes (an incomplete tail).
    full_frame = _pcm(*range(160))
    tail = bytes(100)
    frames = framer.push(full_frame + tail)
    assert frames == [full_frame]
    assert framer.pending == 100

    # Completing the tail with the remaining 220 bytes yields exactly the
    # second whole frame, with nothing left buffered.
    rest = bytes(220)
    frames2 = framer.push(rest)
    assert len(frames2) == 1
    assert len(frames2[0]) == 320
    assert framer.pending == 0


def test_framer_emits_multiple_whole_frames_from_one_chunk():
    framer = PcmFramer(samples_per_packet=160)
    three_frames = _pcm(*range(160)) + _pcm(*range(160)) + _pcm(*range(160))
    frames = framer.push(three_frames)
    assert len(frames) == 3
    assert framer.pending == 0


def test_framer_buffers_when_chunk_smaller_than_one_frame():
    framer = PcmFramer(samples_per_packet=160)
    frames = framer.push(bytes(10))
    assert frames == []
    assert framer.pending == 10


# ===========================================================================
# RFC 3550 RTP parser/packetizer + offline RtpMediaSession (D-03, D-04)
# ===========================================================================

# --- parse_rtp: well-formed + malformed-input guard (T-10-01) -------------


def test_parse_well_formed_header_exposes_all_fields():
    datagram = build_rtp(
        payload=b"abcd", sequence=42, timestamp=8000, ssrc=0xDEADBEEF, payload_type=0, marker=True
    )
    packet = parse_rtp(datagram)
    assert packet == RtpPacket(
        payload_type=0, sequence=42, timestamp=8000, ssrc=0xDEADBEEF, marker=True, payload=b"abcd"
    )


def test_parse_rejects_truncated_header_returns_none():
    assert parse_rtp(b"") is None
    assert parse_rtp(b"short") is None
    assert parse_rtp(bytes(11)) is None  # one byte short of the 12-byte header


def test_parse_rejects_wrong_version_returns_none():
    # Version bits (top 2 bits of byte 0) set to 1 (RTP requires 2).
    bad = bytes([0x40]) + bytes(11)
    assert parse_rtp(bad) is None


# --- build_rtp: byte-exact, parse . build == identity ---------------------


def test_build_rtp_is_byte_exact_12_byte_header_plus_payload():
    datagram = build_rtp(
        payload=b"\x01\x02\x03", sequence=1, timestamp=160, ssrc=1234, payload_type=0
    )
    assert len(datagram) == 12 + 3
    assert datagram[0] == 0x80  # V=2,P=0,X=0,CC=0
    assert datagram[1] == 0x00  # marker=0, PT=0
    assert datagram[2:4] == (1).to_bytes(2, "big")
    assert datagram[4:8] == (160).to_bytes(4, "big")
    assert datagram[8:12] == (1234).to_bytes(4, "big")
    assert datagram[12:] == b"\x01\x02\x03"


def test_parse_build_roundtrip_is_identity():
    original = RtpPacket(
        payload_type=8, sequence=999, timestamp=160000, ssrc=0x12345678, marker=False,
        payload=b"payload-bytes",
    )
    datagram = build_rtp(
        payload=original.payload, sequence=original.sequence, timestamp=original.timestamp,
        ssrc=original.ssrc, payload_type=original.payload_type, marker=original.marker,
    )
    assert parse_rtp(datagram) == original


# --- RtpPacketizer: seq+=1, ts+=samples_per_packet, stable SSRC, params.payload_type --


def test_packetizer_increments_sequence_and_timestamp_using_params():
    params = TelephonyTransportParams(payload_type=8)  # never-hardcoded-0 check
    packetizer = RtpPacketizer(ssrc=0xAABBCCDD, params=params, initial_sequence=10, initial_timestamp=1600)
    pkt1 = parse_rtp(packetizer.packetize(b"\xff" * 160))
    pkt2 = parse_rtp(packetizer.packetize(b"\xff" * 160))
    pkt3 = parse_rtp(packetizer.packetize(b"\xff" * 160))

    assert [p.sequence for p in (pkt1, pkt2, pkt3)] == [10, 11, 12]
    assert [p.timestamp for p in (pkt1, pkt2, pkt3)] == [1600, 1760, 1920]
    assert {p.ssrc for p in (pkt1, pkt2, pkt3)} == {0xAABBCCDD}  # stable SSRC
    assert all(p.payload_type == 8 for p in (pkt1, pkt2, pkt3))  # from params, not hardcoded 0


def test_packetizer_wraps_sequence_at_16_bits():
    params = TelephonyTransportParams()
    packetizer = RtpPacketizer(ssrc=1, params=params, initial_sequence=65535, initial_timestamp=0)
    pkt1 = parse_rtp(packetizer.packetize(b"\xff" * 160))
    pkt2 = parse_rtp(packetizer.packetize(b"\xff" * 160))
    assert pkt1.sequence == 65535
    assert pkt2.sequence == 0  # wrapped, no error


def test_packetizer_wraps_timestamp_at_32_bits():
    params = TelephonyTransportParams()
    packetizer = RtpPacketizer(
        ssrc=1, params=params, initial_sequence=0, initial_timestamp=2**32 - 100
    )
    pkt1 = parse_rtp(packetizer.packetize(b"\xff" * 160))
    pkt2 = parse_rtp(packetizer.packetize(b"\xff" * 160))
    assert pkt1.timestamp == 2**32 - 100
    assert pkt2.timestamp == (2**32 - 100 + 160) % 2**32  # wrapped past 2**32, no error


# --- RtpDepacketizer: dup/reorder/one-missing-silence, bounded (T-10-03) --


def _pkt(seq: int, ts: int = 0, payload: bytes = b"\xaa" * 160) -> RtpPacket:
    return RtpPacket(payload_type=0, sequence=seq, timestamp=ts, ssrc=1, marker=False, payload=payload)


def test_depacketizer_deduplicates_duplicate_packet():
    depacketizer = RtpDepacketizer(params=TelephonyTransportParams())
    out1 = depacketizer.process(_pkt(1))
    out2 = depacketizer.process(_pkt(1))  # exact duplicate
    assert out1 == [b"\xaa" * 160]
    assert out2 == []  # dropped, not re-emitted


def test_depacketizer_tolerates_minor_reordering_without_crashing():
    depacketizer = RtpDepacketizer(params=TelephonyTransportParams())
    depacketizer.process(_pkt(5))
    # seq 4 arrives AFTER seq 5 (one swapped pair) -- must not raise.
    out = depacketizer.process(_pkt(4))
    assert out == [b"\xaa" * 160]
    # seq 6 continues normally afterward -- must not raise.
    out2 = depacketizer.process(_pkt(6))
    assert out2 == [b"\xaa" * 160]


def test_depacketizer_inserts_one_silence_frame_for_single_missing_packet():
    depacketizer = RtpDepacketizer(params=TelephonyTransportParams())
    depacketizer.process(_pkt(1))
    # seq 2 never arrives (lost); seq 3 arrives next -- exactly one gap.
    out = depacketizer.process(_pkt(3))
    assert out == [b"\xff" * 160, b"\xaa" * 160]  # one inserted silence frame, then the payload


def test_depacketizer_startup_timestamp_discontinuity_does_not_crash():
    depacketizer = RtpDepacketizer(params=TelephonyTransportParams())
    # First packet arrives with an arbitrary (non-zero, discontinuous) start timestamp.
    out = depacketizer.process(_pkt(1, ts=123456789))
    assert out == [b"\xaa" * 160]


# --- OfflineRtpMediaSession: in-memory read deque + write capture list ----


async def test_offline_session_reads_preloaded_packets_in_order_then_none():
    session = OfflineRtpMediaSession([b"pkt1", b"pkt2"])
    assert await session.read_packet() == b"pkt1"
    assert await session.read_packet() == b"pkt2"
    assert await session.read_packet() is None  # end-of-stream


async def test_offline_session_write_packet_appends_to_captured_list():
    session = OfflineRtpMediaSession([])
    await session.write_packet(b"outbound1")
    await session.write_packet(b"outbound2")
    assert session.written == [b"outbound1", b"outbound2"]


async def test_offline_session_close_is_safe_and_idempotent():
    session = OfflineRtpMediaSession([])
    await session.close()
    await session.close()  # safe to call twice


async def test_malformed_datagram_guard_and_session_smoke():
    assert parse_rtp(b"") is None
    assert parse_rtp(b"abc") is None
    session = OfflineRtpMediaSession([b"pkt1", b"pkt2"])
    assert await session.read_packet() == b"pkt1"
