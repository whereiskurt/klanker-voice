"""``SocketRtpMediaSession`` (D-03) unit tests -- the socket-backed
``types.RtpMediaSession`` that drops into the Phase 10 seam.

Real localhost UDP loopback throughout (not mocked) -- this module IS the
socket boundary, so exercising the actual ``asyncio`` datagram-endpoint
machinery (bind-before-listen, symmetric-RTP source learning, idempotent
close, hostile-input tolerance) is the only genuine proof of D-03/R2/T-11-03-01.
No Asterisk, no SIP -- a second raw UDP socket on 127.0.0.1 stands in for
Asterisk's external-media sender/receiver.
"""

from __future__ import annotations

import asyncio
import socket

from klanker_voice.telephony.rtp_socket import SocketRtpMediaSession
from klanker_voice.telephony.transport import TelephonyTransport
from klanker_voice.telephony.types import TelephonyTransportParams


def _probe_socket() -> socket.socket:
    """A plain, non-blocking UDP socket standing in for Asterisk's
    external-media sender/receiver -- not the module under test."""
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    sock.setblocking(False)
    sock.bind(("127.0.0.1", 0))
    return sock


# --- Task 1: bind-first UDP session + symmetric-RTP source learning -------


async def test_round_trip_over_loopback_learns_peer_and_echoes():
    """Bind-first (R2), then a probe datagram teaches the session its peer;
    write_packet sends back to that learned (ip, port)."""
    session = await SocketRtpMediaSession.open("127.0.0.1", 0)
    probe = _probe_socket()
    loop = asyncio.get_running_loop()
    try:
        sent = b"\x80\x00\x00\x01" + b"\x00" * 8 + b"payload"
        probe.sendto(sent, ("127.0.0.1", session.bound_port))

        received = await asyncio.wait_for(session.read_packet(), timeout=2.0)
        assert received == sent

        reply = b"reply-from-klanker"
        await session.write_packet(reply)
        data, _addr = await asyncio.wait_for(loop.sock_recvfrom(probe, 4096), timeout=2.0)
        assert data == reply
    finally:
        await session.close()
        probe.close()


async def test_write_before_any_inbound_datagram_is_a_noop(monkeypatch):
    """Peer is unknown until the first inbound datagram -- write_packet must
    not raise and must send nothing (acceptance criteria)."""
    session = await SocketRtpMediaSession.open("127.0.0.1", 0)
    calls: list[object] = []
    monkeypatch.setattr(session._transport, "sendto", lambda *a, **kw: calls.append(a))
    try:
        await session.write_packet(b"nobody-is-listening-yet")
        assert calls == []
    finally:
        await session.close()


async def test_close_is_idempotent_and_read_after_close_returns_none():
    """close() twice does not raise; read_packet() after close returns the
    end-of-stream sentinel (mirrors OfflineRtpMediaSession's contract)."""
    session = await SocketRtpMediaSession.open("127.0.0.1", 0)
    await session.close()
    await session.close()  # idempotent -- must not raise

    result = await asyncio.wait_for(session.read_packet(), timeout=1.0)
    assert result is None


async def test_short_malformed_datagram_does_not_crash_the_protocol():
    """A too-short/garbage datagram (T-11-03-01) must not raise out of
    ``datagram_received`` and must not wedge the protocol -- a subsequent
    valid datagram is still delivered."""
    session = await SocketRtpMediaSession.open("127.0.0.1", 0)
    probe = _probe_socket()
    try:
        probe.sendto(b"\x00", ("127.0.0.1", session.bound_port))
        garbage = await asyncio.wait_for(session.read_packet(), timeout=2.0)
        assert garbage == b"\x00"

        probe.sendto(b"a-normal-looking-packet", ("127.0.0.1", session.bound_port))
        next_pkt = await asyncio.wait_for(session.read_packet(), timeout=2.0)
        assert next_pkt == b"a-normal-looking-packet"
    finally:
        await session.close()
        probe.close()


# --- Task 2: Protocol-conformance + TelephonyTransport-integration --------


async def test_socket_rtp_media_session_satisfies_telephony_transport_seam():
    """Structural D-03 proof: a real ``TelephonyTransport`` (Phase 10,
    unchanged) accepts a ``SocketRtpMediaSession`` as its ``media`` with no
    codec/transport change. Construction only -- no pipeline run (that is
    Plan 07's integration test)."""
    session = await SocketRtpMediaSession.open("127.0.0.1", 0)
    try:
        assert hasattr(session, "read_packet")
        assert hasattr(session, "write_packet")
        assert hasattr(session, "close")

        params = TelephonyTransportParams()
        transport = TelephonyTransport(call_id="phone-seam-test", media=session, params=params)
        # Constructing input()/output() around this media session must not
        # raise -- proves the seam without running the pipeline.
        assert transport.input() is not None
        assert transport.output() is not None
    finally:
        await session.close()
