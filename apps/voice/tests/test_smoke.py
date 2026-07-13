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
import types
from unittest.mock import AsyncMock, MagicMock


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

    ``server.create_call_session`` (09-01: the transport-neutral seam that
    now owns pipeline construction — Deepgram STT + Anthropic LLM +
    ElevenLabs TTS — previously built inside the fire-and-forget
    ``_run_session`` task) is stubbed to a no-op returning a fake
    ``CallSession``: constructing the real pipeline needs real provider
    credentials this in-process test never has, and (09-01 timing shift)
    pipeline construction now happens synchronously inside the connection
    callback rather than in a background task. Stubbing it isolates exactly
    the seam this test targets (offer -> aiortc negotiation -> answer)
    without reaching into unrelated provider wiring (see 04-03-SUMMARY.md
    deviations).
    """
    from fastapi.testclient import TestClient

    import server
    from klanker_voice import session
    from klanker_voice.auth import NO_ACCESS_TIER_ID, SMOKE_SERVICE_SUB, SessionIdentity

    bypass_identity = SessionIdentity(
        sub=SMOKE_SERVICE_SUB, tier_id=NO_ACCESS_TIER_ID, group=None, bypass_accounting=True
    )
    monkeypatch.setattr(server, "validate_access_token", lambda token: bypass_identity)
    # No real STUN network call during the in-process answer negotiation.
    monkeypatch.setattr(server._webrtc_handler, "_ice_servers", [])
    # No real Pipecat pipeline/provider construction for this transport-sanity
    # check: create_call_session is stubbed to return a fake CallSession whose
    # lifecycle is never actually started/stopped (run() is a no-op), so no
    # CloudWatch/DynamoDB call is ever attempted either.
    fake_call_session = types.SimpleNamespace(lifecycle=object(), run=AsyncMock())
    monkeypatch.setattr(server, "create_call_session", AsyncMock(return_value=fake_call_session))
    # 04-04: SessionLifecycle.start()/stop() (CloudWatch metric emission) now
    # fire for every negotiated session, including bypass ones. This dev
    # environment carries real AWS credentials — never let a transport-only
    # sanity test make a live CloudWatch call against the real account.
    monkeypatch.setattr(session, "boto3", MagicMock())
    monkeypatch.setattr(session, "_task_metadata_ids", lambda: ("", ""))

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
