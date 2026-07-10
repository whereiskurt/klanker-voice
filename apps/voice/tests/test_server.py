"""Production entrypoint: /health (no auth) and /api/offer (auth -> start_gate -> transport).

Real WebRTC/ICE negotiation is never exercised here — SDP offers only mean
anything with a live browser peer, and that's what 04-03's deployed smoke
test proves. These tests stub ``server._negotiate_webrtc`` to isolate the
auth/start_gate seam (T-04-01), per the plan's explicit instruction.
"""

from __future__ import annotations

from unittest.mock import AsyncMock

import pytest
from fastapi.testclient import TestClient

import server
from klanker_voice import variants
from klanker_voice.auth import AuthError, SessionIdentity
from klanker_voice.webrtc import PublicCandidates

client = TestClient(server.app)

VALID_IDENTITY = SessionIdentity(
    sub="user-123", tier_id="premium", group="friends-and-family", bypass_accounting=False
)


def test_health_returns_200_with_no_auth():
    response = client.get("/health")

    assert response.status_code == 200
    assert response.json() == {"status": "ok"}


def test_offer_without_valid_token_returns_401(monkeypatch):
    def _raise(token):
        raise AuthError("missing credential")

    monkeypatch.setattr(server, "validate_access_token", _raise)

    response = client.post("/api/offer", json={"sdp": "v=0...", "type": "offer"})

    assert response.status_code == 401
    assert response.json() == {"error": "unauthorized"}


def test_offer_with_valid_identity_reaches_start_gate(monkeypatch):
    monkeypatch.setattr(server, "validate_access_token", lambda token: VALID_IDENTITY)

    start_gate_calls: list[SessionIdentity] = []

    def _recording_start_gate(identity: SessionIdentity) -> None:
        start_gate_calls.append(identity)

    monkeypatch.setattr(server, "start_gate", _recording_start_gate)

    negotiate_mock = AsyncMock(return_value={"sdp": "v=0...", "type": "answer", "pc_id": "pc-1"})
    monkeypatch.setattr(server, "_negotiate_webrtc", negotiate_mock)

    response = client.post(
        "/api/offer",
        headers={"Authorization": "Bearer some-valid-jwt"},
        json={"sdp": "v=0...", "type": "offer"},
    )

    assert response.status_code == 200
    assert response.json() == {"sdp": "v=0...", "type": "answer", "pc_id": "pc-1"}
    assert start_gate_calls == [VALID_IDENTITY]
    negotiate_mock.assert_awaited_once()


def test_offer_rejected_by_start_gate_returns_403(monkeypatch):
    monkeypatch.setattr(server, "validate_access_token", lambda token: VALID_IDENTITY)

    def _reject(identity: SessionIdentity) -> None:
        raise RuntimeError("daily-limit")

    monkeypatch.setattr(server, "start_gate", _reject)

    negotiate_mock = AsyncMock()
    monkeypatch.setattr(server, "_negotiate_webrtc", negotiate_mock)

    response = client.post(
        "/api/offer",
        headers={"Authorization": "Bearer some-valid-jwt"},
        json={"sdp": "v=0...", "type": "offer"},
    )

    assert response.status_code == 403
    assert response.json()["error"] == "rejected"
    negotiate_mock.assert_not_awaited()


def _offer_capturing_variant(monkeypatch):
    """Stub auth + start_gate + _negotiate_webrtc; return the AsyncMock so the
    caller can read which variant reached _negotiate_webrtc."""
    monkeypatch.setattr(server, "validate_access_token", lambda token: VALID_IDENTITY)
    monkeypatch.setattr(server, "start_gate", lambda identity: "gate-ok")
    negotiate_mock = AsyncMock(return_value={"sdp": "v=0...", "type": "answer", "pc_id": "pc-1"})
    monkeypatch.setattr(server, "_negotiate_webrtc", negotiate_mock)
    return negotiate_mock


def test_offer_passes_known_variant_to_negotiation(monkeypatch):
    negotiate_mock = _offer_capturing_variant(monkeypatch)
    response = client.post(
        "/api/offer?variant=voice2",
        headers={"Authorization": "Bearer some-valid-jwt"},
        json={"sdp": "v=0...", "type": "offer"},
    )
    assert response.status_code == 200
    # _negotiate_webrtc(body, identity, gate_result, variant) — variant is arg 3.
    assert negotiate_mock.await_args.args[3] == "voice2"


def test_offer_unknown_variant_falls_back_to_default(monkeypatch):
    negotiate_mock = _offer_capturing_variant(monkeypatch)
    response = client.post(
        "/api/offer?variant=not-a-real-variant",
        headers={"Authorization": "Bearer some-valid-jwt"},
        json={"sdp": "v=0...", "type": "offer"},
    )
    assert response.status_code == 200
    assert negotiate_mock.await_args.args[3] == variants.DEFAULT_VARIANT


def test_offer_no_variant_defaults_to_voice2(monkeypatch):
    negotiate_mock = _offer_capturing_variant(monkeypatch)
    response = client.post(
        "/api/offer",
        headers={"Authorization": "Bearer some-valid-jwt"},
        json={"sdp": "v=0...", "type": "offer"},
    )
    assert response.status_code == 200
    assert negotiate_mock.await_args.args[3] == "voice2"


async def test_negotiate_webrtc_sets_variant_label(monkeypatch):
    """_negotiate_webrtc resolves answer["variant_label"] the same way it
    resolves session_max_seconds — a lightweight, deliberate extra TOML read
    keyed off the requested variant."""
    handler_mock = AsyncMock(
        return_value={"sdp": "v=0...", "type": "answer", "pc_id": "pc-1"}
    )
    monkeypatch.setattr(server._webrtc_handler, "handle_web_request", handler_mock)
    monkeypatch.setattr(
        server, "gather_public_candidates", lambda: PublicCandidates(public_ip=None)
    )
    gate_result = server.quota.GateResult(
        session_id="sess-1",
        tier=server.quota.Tier(
            tier_id="t", session_max_seconds=120, period_max_seconds=600, max_concurrent=2
        ),
        session_max_seconds=120,
        remaining_daily_seconds=600,
        bypass_accounting=False,
    )

    answer_v1 = await server._negotiate_webrtc(
        {"sdp": "v=0...", "type": "offer"}, VALID_IDENTITY, gate_result, "voice1"
    )
    assert answer_v1["variant_label"] == "KPH(v1)"

    answer_v2 = await server._negotiate_webrtc(
        {"sdp": "v=0...", "type": "offer"}, VALID_IDENTITY, gate_result, "voice2"
    )
    assert answer_v2["variant_label"] == "KPH(v2)"


def test_extract_bearer_token_prefers_authorization_header():
    request = _fake_request(headers={"authorization": "Bearer header-token"})

    assert server._extract_bearer_token(request, {}) == "header-token"


def test_extract_bearer_token_falls_back_to_request_data():
    request = _fake_request(headers={})

    token = server._extract_bearer_token(
        request, {"request_data": {"access_token": "body-token"}}
    )

    assert token == "body-token"


def test_extract_bearer_token_returns_none_when_absent():
    request = _fake_request(headers={})

    assert server._extract_bearer_token(request, {}) is None


class _FakeRequest:
    def __init__(self, headers: dict[str, str]):
        self.headers = headers


def _fake_request(headers: dict[str, str]) -> _FakeRequest:
    return _FakeRequest(headers)
