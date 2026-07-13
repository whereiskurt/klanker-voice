"""klanker-voice telephony: the offline media adapter (spec Phase B).

New leaf package -- touches NO shared-runtime file (``call_runtime.py``,
``pipeline.py``, ``factories.py``, ``server.py``, ``webrtc.py``). Phase 10
(spec Phase B) produces the deterministic, offline media + transport layer:
a PCMU (G.711 mu-law) codec, an RFC 3550 RTP parser/packetizer, the
``TelephonyTransportParams`` config dataclass, an in-memory
``RtpMediaSession`` implementation, and ``TelephonyTransport`` (a pipecat
``BaseTransport``) that composes them -- no SIP, no sockets, no Asterisk.
"""

from __future__ import annotations

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
from klanker_voice.telephony.transport import (
    TelephonyInputTransport,
    TelephonyOutputTransport,
    TelephonyTransport,
)
from klanker_voice.telephony.types import RtpMediaSession, TelephonyTransportParams

__all__ = [
    "TelephonyTransportParams",
    "RtpMediaSession",
    "ulaw_decode",
    "ulaw_encode",
    "PcmFramer",
    "RtpPacket",
    "parse_rtp",
    "build_rtp",
    "RtpPacketizer",
    "RtpDepacketizer",
    "OfflineRtpMediaSession",
    "TelephonyTransport",
    "TelephonyInputTransport",
    "TelephonyOutputTransport",
]
