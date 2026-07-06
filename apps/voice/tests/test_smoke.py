"""Install smoke: the pinned pipecat 1.5.x tree and the klanker_voice package import.

Also the KV-05 transport-sanity pre-flight (04-03 Task 2): a real, in-process
aiortc offer/answer negotiation against ``/api/offer`` with a stubbed
identity, proving the offer handler can produce a well-formed SDP answer
before the deploy checkpoint (Task 3) is attempted against the live service.
No AWS/network is required — the deployed ICE/RTP proof itself is 04-03's
`kv smoke` (Task 1) run against the real endpoint.
"""

from __future__ import annotations

import asyncio
from unittest.mock import AsyncMock


def test_pipecat_version_is_pinned_line():
    import pipecat

    assert pipecat.__version__.startswith("1.5.")


def test_klanker_voice_package_importable():
    import klanker_voice  # noqa: F401


async def _build_synthetic_offer() -> tuple[str, str]:
    """Build a real aiortc SDP offer (recvonly audio), no network required.

    This is the same aiortc stack the deployed `kv smoke` (Task 1) drives —
    just without any actual ICE/RTP negotiation over the wire, since there is
    no live peer in-process.
    """
    from aiortc import RTCPeerConnection

    pc = RTCPeerConnection()
    try:
        pc.addTransceiver("audio", direction="recvonly")
        offer = await pc.createOffer()
        await pc.setLocalDescription(offer)
        local = pc.localDescription
        return local.sdp, local.type
    finally:
        await pc.close()


def test_offer_negotiates_real_sdp_answer_for_stubbed_identity(monkeypatch):
    """/api/offer must return a well-formed SDP answer for a valid (stubbed,
    bypass_accounting) identity, driving the real offer-handling code path
    in-process — no deploy, no real ICE connect, no real media flow.

    ``server._run_session`` (the per-session Pipecat pipeline: Deepgram STT +
    Anthropic LLM + ElevenLabs TTS) is stubbed to a no-op: it's a fire-and-
    forget background task the real handler kicks off after the SDP answer
    is already computed, and constructing it needs real provider credentials
    this in-process test never has. Stubbing it isolates exactly the seam
    this test targets (offer -> aiortc negotiation -> answer) without
    reaching into unrelated provider wiring (see 04-03-SUMMARY.md deviations).
    """
    from fastapi.testclient import TestClient

    import server
    from klanker_voice.auth import NO_ACCESS_TIER_ID, SMOKE_SERVICE_SUB, SessionIdentity

    bypass_identity = SessionIdentity(
        sub=SMOKE_SERVICE_SUB, tier_id=NO_ACCESS_TIER_ID, group=None, bypass_accounting=True
    )
    monkeypatch.setattr(server, "validate_access_token", lambda token: bypass_identity)
    # No real STUN network call during the in-process answer negotiation.
    monkeypatch.setattr(server._webrtc_handler, "_ice_servers", [])
    # No real Pipecat pipeline/provider construction for this transport-sanity check.
    monkeypatch.setattr(server, "_run_session", AsyncMock())

    client = TestClient(server.app)
    offer_sdp, offer_type = asyncio.run(_build_synthetic_offer())

    response = client.post(
        "/api/offer",
        headers={"Authorization": "Bearer smoke-credential-placeholder"},
        json={"sdp": offer_sdp, "type": offer_type},
    )

    assert response.status_code == 200
    body = response.json()
    assert body["type"] == "answer"
    assert isinstance(body["sdp"], str) and body["sdp"].strip() != ""
