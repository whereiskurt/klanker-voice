"""Telephony media/transport knobs + the offline<->socket swap seam (D-01/D-09).

``TelephonyTransportParams`` is a plain, frozen project dataclass carrying
telephony/media *behavior* ONLY -- clock rate, packet timing, payload type.
It is NOT pipecat's pydantic ``TransportParams`` (the audio-enable-flags
container handed to ``BaseInputTransport``/``BaseOutputTransport`` -- see
Phase 10 PATTERNS "Critical Notes: two distinct params objects"). It NEVER
carries vendor credentials or STT/LLM/TTS settings (D-09, spec Sec22.3) --
those stay in ``config.py``'s ``PipelineConfig``.

``RtpMediaSession`` is the bidirectional RTP byte seam ``TelephonyTransport``
(Plan 02) will consume. Phase 10 (this phase) provides only an in-memory,
offline implementation (``media.OfflineRtpMediaSession``) fed synthetic/WAV-
derived RTP; Phase 11/C swaps in a socket-backed implementation without
touching the codec (``media.py``) or the transport.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Protocol


@dataclass(frozen=True)
class TelephonyTransportParams:
    """Telephony/media knobs only -- never vendor credentials or
    STT/LLM/TTS settings (D-09, spec Sec22.3).

    Attributes:
        clock_rate: PCMU's 8 kHz RTP clock (spec Sec9).
        packet_time_ms: RTP packetization interval -- 20 ms per packet.
        samples_per_packet: ``clock_rate * packet_time_ms / 1000`` = 160 for
            the default 8 kHz/20 ms pair. Kept as an explicit field (not
            derived) so the RTP packetizer/depacketizer and the PCM framer
            share one literal source of truth.
        payload_type: RTP payload type. 0 is the common PCMU default but is
            OVERRIDABLE (spec Sec9/D-03) -- downstream code MUST read this
            field and must never hardcode a literal 0.
    """

    clock_rate: int = 8000
    packet_time_ms: int = 20
    samples_per_packet: int = 160
    payload_type: int = 0


class RtpMediaSession(Protocol):
    """Bidirectional RTP byte seam consumed by ``TelephonyTransport`` (Plan 02).

    Phase 10 (this phase) ships only an offline, in-memory implementation
    (``media.OfflineRtpMediaSession``): the read side is fed
    synthetic/WAV-derived RTP datagrams, the write side captures packetized
    RTP for test assertions -- NO UDP socket, NO Asterisk external-media
    address. Phase 11/C drops in a socket-backed implementation that
    satisfies this exact Protocol WITHOUT touching the codec or the
    transport (D-04).
    """

    async def read_packet(self) -> bytes | None:
        """Return the next raw RTP datagram, or ``None`` at end-of-stream."""
        ...

    async def write_packet(self, packet: bytes) -> None:
        """Send one raw RTP datagram."""
        ...

    async def close(self) -> None:
        """Release any held resources. Safe to call more than once."""
        ...
