"""Socket-backed ``RtpMediaSession`` (D-03) -- the live-Asterisk analog of
Phase 10's in-memory ``media.OfflineRtpMediaSession``.

Drops into the exact ``types.RtpMediaSession`` Protocol without touching
either the codec (``media.py``) or ``TelephonyTransport`` (``transport.py``)
-- this module is the telephony analog of ``webrtc.py`` isolation: a
transport-specific media module, not a branch in shared code.

Because Asterisk's ``externalMedia`` channel only supports
``connection_type=client`` (R2, docs.asterisk.org), Asterisk always
INITIATES the UDP connection to Klanker -- Klanker must already be bound and
listening on its local port BEFORE the controller creates the externalMedia
channel, or the first RTP datagrams arrive at a closed port and are silently
dropped (UDP has no handshake/retry). :meth:`SocketRtpMediaSession.open`
binds via ``asyncio.loop.create_datagram_endpoint`` before returning.

The peer ``(ip, port)`` Asterisk actually sends from is not reliably
predictable ahead of time (Asterisk's RTP engine picks an ephemeral port per
``rtp.conf``, and in a docker-compose harness the container's bridge-network
IP -- not ``127.0.0.1`` -- is what appears as the packet source). Symmetric
RTP / first-packet source learning (R2) is therefore required, not optional:
:meth:`write_packet` always sends to whichever ``(ip, port)`` the most
recently received datagram came from, NEVER a value fixed at construction.

Payload type is never hardcoded here -- Phase 10's ``parse_rtp``/
``RtpPacketizer`` already read/write it per :class:`types.TelephonyTransportParams`
(D-03); this module only ferries raw bytes.
"""

from __future__ import annotations

import asyncio

from loguru import logger


class _AsteriskRtpProtocol(asyncio.DatagramProtocol):
    """``asyncio.DatagramProtocol`` that queues inbound RTP datagrams and
    learns the sender's ``(ip, port)`` via symmetric-RTP source learning,
    updated on every packet (R2).

    Hostile-input posture (T-11-03-01): ``datagram_received``/
    ``error_received`` never raise out of the callback -- any unexpected
    failure is swallowed and logged at debug, never propagated into the
    event loop.
    """

    def __init__(self) -> None:
        self.queue: asyncio.Queue[bytes | None] = asyncio.Queue()
        self.peer: tuple[str, int] | None = None
        self.transport: asyncio.DatagramTransport | None = None

    def connection_made(self, transport: asyncio.BaseTransport) -> None:
        self.transport = transport  # type: ignore[assignment]

    def datagram_received(self, data: bytes, addr: tuple[str, int]) -> None:
        try:
            self.peer = addr  # symmetric-RTP source learning -- every packet
            self.queue.put_nowait(data)
        except Exception:  # pragma: no cover - defensive, never raise (T-11-03-01)
            logger.debug(f"rtp_socket: swallowed exception in datagram_received: addr={addr}")

    def error_received(self, exc: Exception) -> None:
        # asyncio calls this for OS-level datagram errors (e.g. an ICMP
        # port-unreachable bounce) -- never let it propagate (T-11-03-01).
        logger.debug(f"rtp_socket: swallowed error_received: {exc}")

    def connection_lost(self, exc: Exception | None) -> None:
        # Unblock any read_packet() already awaiting the queue when the
        # transport closes -- mirrors OfflineRtpMediaSession's end-of-stream
        # (None) contract.
        self.queue.put_nowait(None)


class SocketRtpMediaSession:
    """Socket-backed ``types.RtpMediaSession`` (D-03): binds a UDP endpoint
    first, learns Asterisk's peer address from the first inbound datagram,
    and exchanges raw RTP bytes behind ``read_packet``/``write_packet``/
    ``close`` -- the exact shape ``OfflineRtpMediaSession`` (Phase 10)
    already satisfies.
    """

    def __init__(self, protocol: _AsteriskRtpProtocol, transport: asyncio.DatagramTransport) -> None:
        self._protocol = protocol
        self._transport = transport
        self._closed = False

    @classmethod
    async def open(cls, bind_host: str, bind_port: int = 0) -> SocketRtpMediaSession:
        """Bind + start listening BEFORE the controller creates the
        externalMedia channel (R2) -- Asterisk always dials out
        (``connection_type=client``), so Klanker must already be listening.

        ``bind_port=0`` lets the OS assign an ephemeral port; read it back
        via :attr:`bound_port` to advertise in ``external_host``.
        """
        loop = asyncio.get_running_loop()
        protocol = _AsteriskRtpProtocol()
        transport, _ = await loop.create_datagram_endpoint(
            lambda: protocol,
            local_addr=(bind_host, bind_port),
        )
        return cls(protocol, transport)

    @property
    def bound_port(self) -> int:
        """The actually-bound local UDP port -- the controller advertises
        this in ``externalMedia``'s ``external_host`` (R2)."""
        sockname = self._transport.get_extra_info("sockname")
        return sockname[1]

    async def read_packet(self) -> bytes | None:
        """Return the next raw RTP datagram, or ``None`` at end-of-stream
        (mirrors ``OfflineRtpMediaSession``'s contract). Returns ``None``
        immediately once :meth:`close` has been called, without waiting on
        the queue."""
        if self._closed:
            return None
        return await self._protocol.queue.get()

    async def write_packet(self, packet: bytes) -> None:
        """Send ``packet`` to the most-recently-learned peer (symmetric
        RTP). A no-op (logged at debug) if no datagram has been received
        yet -- the peer is not yet known -- never raises."""
        peer = self._protocol.peer
        if peer is None:
            logger.debug("rtp_socket: write_packet before any inbound datagram, dropping")
            return
        self._transport.sendto(packet, peer)

    async def close(self) -> None:
        """Close the underlying ``DatagramTransport``. Safe to call more
        than once (matches the Protocol's "safe to call more than once"
        contract, already implemented identically by
        ``OfflineRtpMediaSession``)."""
        if self._closed:
            return
        self._closed = True
        self._transport.close()
