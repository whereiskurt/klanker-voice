"""Phase 10 (offline media adapter): PCMU codec + framing unit tests (D-02, D-10).

Pure-function, table-driven tests against hand-computed G.711 mu-law known
vectors -- no async harness needed for the codec (mirrors
``test_pronunciation_filter.py`` / ``test_tts_energy.py`` style). The RTP
parser/packetizer + offline session tests (D-03/D-04) are appended in the
same module by Task 3.
"""

import struct

from klanker_voice.telephony.media import PcmFramer, ulaw_decode, ulaw_encode

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
