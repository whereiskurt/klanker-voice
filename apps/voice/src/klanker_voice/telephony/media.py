"""PCMU (G.711 mu-law) codec + 160-sample framing (D-02, spec Sec9/Sec16).

Explicit mu-law implementation (recommended per D-02) rather than stdlib
``audioop``: deterministic, hand-vector-testable, and version-proof --
``audioop`` is deprecated and REMOVED in Python 3.13, whereas this module has
no such 3.13 coupling (the project pins Python 3.12, spec CLAUDE.md).

``ulaw_decode``/``ulaw_encode`` are PURE ``bytes -> bytes`` transforms with
NO resampling inside them -- resampling is a separate, stateful, streaming
step composed later at the transport boundary (D-06; Plan 02).

This module also carries the RFC 3550 RTP parser/packetizer and the offline
in-memory ``RtpMediaSession`` (D-03/D-04) -- added in a later commit of this
same plan (Task 3).
"""

from __future__ import annotations

import struct

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
