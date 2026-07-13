"""PCMU (G.711 mu-law) codec + RFC 3550 RTP + offline media seam (D-02/D-03/D-04).

Explicit mu-law implementation (recommended per D-02) rather than stdlib
``audioop``: deterministic, hand-vector-testable, and version-proof --
``audioop`` is deprecated and REMOVED in Python 3.13, whereas this module has
no such 3.13 coupling (the project pins Python 3.12, spec CLAUDE.md).

``ulaw_decode``/``ulaw_encode`` are PURE ``bytes -> bytes`` transforms with
NO resampling inside them -- resampling is a separate, stateful, streaming
step composed later at the transport boundary (D-06; Plan 02).

This module also implements the RFC 3550 RTP parser/packetizer (no in-tree
or pipecat analog exists for raw RTP -- PATTERNS "No Analog Found") and an
offline, in-memory ``RtpMediaSession`` (D-04) that satisfies the
``types.RtpMediaSession`` Protocol with NO socket and NO external-media
call-signaling address. Phase 11/C swaps in a socket-backed implementation
without touching this codec or the ``TelephonyTransport`` (Plan 02).
"""

from __future__ import annotations

import struct
from collections import deque
from dataclasses import dataclass

from klanker_voice.telephony.types import TelephonyTransportParams

# --- PCMU (G.711 mu-law) codec -------------------------------------------

#: Encoding bias added to the sample magnitude before quantization (standard
#: G.711 mu-law constant).
_ULAW_BIAS = 0x84

#: Maximum sample magnitude before clipping -- leaves headroom under the
#: signed 16-bit ceiling (32767) once ``_ULAW_BIAS`` is added, matching the
#: standard mu-law compander's segment structure (8 segments of 16 steps).
_ULAW_CLIP = 32635


def _ulaw_encode_sample(sample: int) -> int:
    """Encode one signed 16-bit PCM sample to a mu-law byte (G.711)."""
    if sample < 0:
        sign = 0x80
        magnitude = -sample
    else:
        sign = 0x00
        magnitude = sample
    if magnitude > _ULAW_CLIP:
        magnitude = _ULAW_CLIP  # saturate -- never overflow/wrap
    biased = magnitude + _ULAW_BIAS
    exponent = min(7, max(0, biased.bit_length() - 8))
    mantissa = (biased >> (exponent + 3)) & 0x0F
    return ~(sign | (exponent << 4) | mantissa) & 0xFF


def _ulaw_decode_sample(ulaw_byte: int) -> int:
    """Decode one mu-law byte to a signed 16-bit PCM sample (G.711).

    Total over the full ``0x00``..``0xFF`` domain -- no error is possible
    for any byte value (T-10-02).
    """
    inverted = ~ulaw_byte & 0xFF
    sign = inverted & 0x80
    exponent = (inverted >> 4) & 0x07
    mantissa = inverted & 0x0F
    magnitude = (((mantissa << 3) + _ULAW_BIAS) << exponent) - _ULAW_BIAS
    return -magnitude if sign else magnitude


def ulaw_encode(pcm: bytes) -> bytes:
    """Signed 16-bit little-endian PCM -> mu-law bytes. Pure, no resampling."""
    if len(pcm) % 2 != 0:
        raise ValueError("pcm byte length must be a multiple of 2 (16-bit samples)")
    sample_count = len(pcm) // 2
    if sample_count == 0:
        return b""
    samples = struct.unpack(f"<{sample_count}h", pcm)
    return bytes(_ulaw_encode_sample(s) for s in samples)


def ulaw_decode(ulaw: bytes) -> bytes:
    """Mu-law bytes -> signed 16-bit little-endian PCM. Pure, no resampling."""
    if not ulaw:
        return b""
    samples = [_ulaw_decode_sample(b) for b in ulaw]
    return struct.pack(f"<{len(samples)}h", *samples)


# --- 160-sample / 20 ms framing -------------------------------------------


class PcmFramer:
    """Splits an arbitrary-length 16-bit PCM byte stream into whole frames.

    Constructed with ``samples_per_packet`` (default 160 -- the 8 kHz/20 ms
    default from :class:`~klanker_voice.telephony.types.TelephonyTransportParams`).
    An incomplete trailing frame is BUFFERED (never emitted, never dropped)
    until a later chunk completes it (D-02).
    """

    def __init__(self, samples_per_packet: int = 160) -> None:
        self._frame_bytes = samples_per_packet * 2  # 16-bit samples
        self._buffer = bytearray()

    def push(self, pcm_chunk: bytes) -> list[bytes]:
        """Feed a PCM chunk; return zero or more whole frames.

        Each returned frame is exactly ``samples_per_packet`` samples
        (``_frame_bytes`` bytes) long. Any remainder shorter than one frame
        stays buffered internally (see :attr:`pending`).
        """
        self._buffer.extend(pcm_chunk)
        frames: list[bytes] = []
        while len(self._buffer) >= self._frame_bytes:
            frames.append(bytes(self._buffer[: self._frame_bytes]))
            del self._buffer[: self._frame_bytes]
        return frames

    @property
    def pending(self) -> int:
        """Bytes currently buffered as an incomplete trailing frame."""
        return len(self._buffer)


# --- RFC 3550 RTP header parse/build (D-03) -------------------------------

#: RTP version this adapter speaks (RFC 3550 Sec5.1). Any other value in a
#: received datagram is rejected (T-10-01).
_RTP_VERSION = 2

#: Fixed RTP header length with no CSRC list / extension (RFC 3550 Sec5.1).
_RTP_HEADER_LEN = 12


@dataclass
class RtpPacket:
    """One parsed RTP datagram's header fields + payload (RFC 3550 Sec5.1)."""

    payload_type: int
    sequence: int
    timestamp: int
    ssrc: int
    marker: bool
    payload: bytes


def parse_rtp(datagram: bytes) -> RtpPacket | None:
    """Parse a 12-byte RTP header + payload. Never raises (T-10-01).

    Returns ``None`` -- rather than raising -- for a datagram shorter than
    the fixed 12-byte header or one whose version field isn't 2. This is the
    one untrusted-input guard this offline phase lands ahead of Phase 11's
    live socket feed.
    """
    if len(datagram) < _RTP_HEADER_LEN:
        return None
    version = (datagram[0] >> 6) & 0x03
    if version != _RTP_VERSION:
        return None
    marker = bool(datagram[1] & 0x80)
    payload_type = datagram[1] & 0x7F
    sequence, timestamp, ssrc = struct.unpack(">HII", datagram[2:12])
    payload = datagram[12:]
    return RtpPacket(
        payload_type=payload_type,
        sequence=sequence,
        timestamp=timestamp,
        ssrc=ssrc,
        marker=marker,
        payload=payload,
    )


def build_rtp(
    *,
    payload: bytes,
    sequence: int,
    timestamp: int,
    ssrc: int,
    payload_type: int,
    marker: bool = False,
) -> bytes:
    """Build a byte-exact 12-byte RTP header + payload (RFC 3550 Sec5.1).

    ``sequence``/``timestamp`` are masked to their wire widths (16/32 bits)
    so callers get correct wraparound instead of a struct-pack error.
    """
    sequence &= 0xFFFF
    timestamp &= 0xFFFFFFFF
    ssrc &= 0xFFFFFFFF
    payload_type &= 0x7F
    byte0 = _RTP_VERSION << 6  # P=0, X=0, CC=0
    byte1 = (0x80 if marker else 0x00) | payload_type
    header = struct.pack(">BBHII", byte0, byte1, sequence, timestamp, ssrc)
    return header + payload


# --- Stateful packetizer / depacketizer (D-03) ----------------------------


class RtpPacketizer:
    """Emits successive RTP datagrams for a single call's outbound stream.

    Increments ``sequence`` by 1 and ``timestamp`` by
    ``params.samples_per_packet`` per call to :meth:`packetize`, keeps
    ``ssrc`` stable, and reads ``payload_type`` from ``params`` -- NEVER a
    hardcoded literal (spec Sec9/D-03). Wraps sequence (16-bit) and
    timestamp (32-bit) on overflow via :func:`build_rtp`'s masking.
    """

    def __init__(
        self,
        *,
        ssrc: int,
        params: TelephonyTransportParams,
        initial_sequence: int = 0,
        initial_timestamp: int = 0,
    ) -> None:
        self._ssrc = ssrc & 0xFFFFFFFF
        self._params = params
        self._sequence = initial_sequence & 0xFFFF
        self._timestamp = initial_timestamp & 0xFFFFFFFF

    def packetize(self, ulaw_payload: bytes) -> bytes:
        """Build the next RTP datagram carrying ``ulaw_payload``."""
        packet = build_rtp(
            payload=ulaw_payload,
            sequence=self._sequence,
            timestamp=self._timestamp,
            ssrc=self._ssrc,
            payload_type=self._params.payload_type,
        )
        self._sequence = (self._sequence + 1) & 0xFFFF
        self._timestamp = (self._timestamp + self._params.samples_per_packet) & 0xFFFFFFFF
        return packet


#: Bound on the depacketizer's recent-sequence dedup window (T-10-03: no
#: unbounded buffering). Comfortably larger than any realistic jitter burst
#: for 20 ms packets.
_DEPACKETIZER_SEEN_WINDOW = 64


class RtpDepacketizer:
    """Bounded dup/reorder/one-missing-packet tolerance (D-03, T-10-03).

    Fed one already-parsed :class:`RtpPacket` at a time via :meth:`process`,
    which returns zero or more mu-law payload byte-strings to hand
    downstream: normally exactly one (the packet's own payload); zero for an
    exact-duplicate sequence number (dropped); or two when exactly one
    packet was missed between the previous and current sequence -- one
    synthetic silence frame (the mu-law silence byte repeated) followed by
    the current payload, so the downstream audio clock stays aligned
    (silence insertion for a single missing 20 ms packet is acceptable for
    the MVP, spec Sec9/D-03).

    Never raises: reordering, larger gaps, and a discontinuous startup
    timestamp are all tolerated without crashing. The recent-sequence
    dedup window is BOUNDED (:data:`_DEPACKETIZER_SEEN_WINDOW`) -- no
    unbounded buffering, no busy-loop (T-10-03).
    """

    def __init__(self, *, params: TelephonyTransportParams, silence_byte: int = 0xFF) -> None:
        self._params = params
        self._silence_byte = silence_byte
        # The HIGHEST sequence number seen so far (not merely the last one
        # *processed*) -- gap detection must be relative to this, or a
        # reordered/late packet would look like a forward gap on the very
        # next in-order packet (see the minor-reordering test).
        self._highest_sequence: int | None = None
        self._seen: deque[int] = deque(maxlen=_DEPACKETIZER_SEEN_WINDOW)
        self._seen_set: set[int] = set()

    def process(self, packet: RtpPacket) -> list[bytes]:
        seq = packet.sequence
        if seq in self._seen_set:
            return []  # exact duplicate -- drop, do not re-emit
        self._seen.append(seq)
        self._seen_set = set(self._seen)  # bounded by deque's maxlen (T-10-03)

        out: list[bytes] = []
        if self._highest_sequence is None:
            self._highest_sequence = seq
        else:
            # Forward distance from the highest sequence seen, treating the
            # 16-bit space as a half-circle (RFC 1982-style serial
            # arithmetic): a forward_gap in (0, 0x7FFF] is a genuine advance;
            # anything larger means ``seq`` is actually a late/reordered
            # packet that arrived behind the highest one already seen.
            forward_gap = (seq - self._highest_sequence) & 0xFFFF
            if 0 < forward_gap <= 0x7FFF:
                if forward_gap == 2:
                    # Exactly one missing 20 ms packet -> insert one silence frame.
                    frame_len = len(packet.payload) or self._params.samples_per_packet
                    out.append(bytes([self._silence_byte]) * frame_len)
                # forward_gap == 1: normal progression, no insertion.
                # forward_gap > 2 (multi-loss): tolerate silently, no crash --
                # never insert more than the single silence frame above.
                self._highest_sequence = seq
            # else: a reordered/late packet (behind the highest seen) --
            # emit its payload but don't touch gap tracking.
        out.append(packet.payload)
        return out


# --- Offline in-memory RtpMediaSession (D-04) ------------------------------


class OfflineRtpMediaSession:
    """In-memory ``RtpMediaSession`` (Protocol in ``types.py``) -- NO socket.

    The read side is pre-loaded with synthetic/WAV-derived RTP datagrams and
    popped in order by :meth:`read_packet`, yielding ``None`` once exhausted
    (end-of-stream). The write side appends every packetized RTP datagram to
    :attr:`written`, a plain list tests assert on. Phase 11/C substitutes a
    socket-backed implementation that satisfies this exact shape without
    touching the codec or the transport (D-04).
    """

    def __init__(self, incoming: list[bytes] | None = None) -> None:
        self._read_queue: deque[bytes] = deque(incoming or [])
        self.written: list[bytes] = []
        self._closed = False

    async def read_packet(self) -> bytes | None:
        if not self._read_queue:
            return None
        return self._read_queue.popleft()

    async def write_packet(self, packet: bytes) -> None:
        self.written.append(packet)

    async def close(self) -> None:
        """Release held resources. Safe to call more than once."""
        self._closed = True
