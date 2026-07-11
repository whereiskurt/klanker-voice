"""klanker-voice telephony: the offline media adapter (spec Phase B).

New leaf package -- touches NO shared-runtime file (``call_runtime.py``,
``pipeline.py``, ``factories.py``, ``server.py``, ``webrtc.py``). This phase
(Phase 10 / spec Phase B) produces ONLY the deterministic, offline media
layer: a PCMU (G.711 mu-law) codec, an RFC 3550 RTP parser/packetizer, the
``TelephonyTransportParams`` config dataclass, and an in-memory
``RtpMediaSession`` implementation -- no SIP, no sockets, no Asterisk. The
``TelephonyTransport`` (pipecat ``BaseTransport``) that composes these
pieces is Plan 02.
"""

from __future__ import annotations

from klanker_voice.telephony.types import RtpMediaSession, TelephonyTransportParams

__all__ = [
    "TelephonyTransportParams",
    "RtpMediaSession",
]
