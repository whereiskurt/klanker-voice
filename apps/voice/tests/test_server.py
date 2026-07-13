"""Production entrypoint: /health (no auth) and /api/offer (auth -> start_gate -> transport).

Real WebRTC/ICE negotiation is never exercised here — SDP offers only mean
anything with a live browser peer, and that's what 04-03's deployed smoke
test proves. These tests stub ``server._negotiate_webrtc`` to isolate the
auth/start_gate seam (T-04-01), per the plan's explicit instruction.
"""

from __future__ import annotations

import asyncio
import time
from unittest.mock import AsyncMock

import pytest
from fastapi.testclient import TestClient

import server
from klanker_voice import ledger, variants
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


# --- Phase 15 (LEDG-01/LEDG-02): identity threading + shutdown drain ------


async def test_negotiate_webrtc_threads_email_and_code_into_call_identity(monkeypatch):
    """The WebRTC CallIdentity build now carries email/code from the
    validated SessionIdentity through to create_call_session (LEDG-01)."""
    captured: dict = {}

    class _FakeCallSession:
        def __init__(self) -> None:
            self.lifecycle = object()

        async def run(self) -> None:
            return None

    async def _fake_create_call_session(**kwargs):
        captured.update(kwargs)
        return _FakeCallSession()

    monkeypatch.setattr(server, "create_call_session", _fake_create_call_session)
    monkeypatch.setattr(server, "build_ambience_mixer", lambda cfg: None)
    # Bypass the real aiortc-backed transport entirely -- create_call_session
    # is mocked out above and never inspects it; only `identity` matters here.
    monkeypatch.setattr(server, "SmallWebRTCTransport", lambda **kwargs: object())
    monkeypatch.setattr(server, "_wire_connection_teardown", lambda connection, lifecycle: None)

    class _FakeConnection:
        pc_id = "pc-ledger-identity-test"

    async def _invoke_callback(webrtc_request, connection_callback):
        await connection_callback(_FakeConnection())
        return {"sdp": "v=0...", "type": "answer", "pc_id": "pc-ledger-identity-test"}

    monkeypatch.setattr(server._webrtc_handler, "handle_web_request", _invoke_callback)
    monkeypatch.setattr(
        server, "gather_public_candidates", lambda: PublicCandidates(public_ip=None)
    )

    identity_with_claims = SessionIdentity(
        sub="user-abc",
        tier_id="premium",
        group=None,
        bypass_accounting=False,
        email="dad@example.com",
        code="kphdemo123",
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

    try:
        await server._negotiate_webrtc(
            {"sdp": "v=0...", "type": "offer"}, identity_with_claims, gate_result, "voice1"
        )
        call_identity = captured["identity"]
        assert call_identity.email == "dad@example.com"
        assert call_identity.code == "kphdemo123"
        assert call_identity.caller_id is None  # webrtc never carries caller_id/did
        assert call_identity.did is None
    finally:
        server.SESSIONS.pop("pc-ledger-identity-test", None)
        server.SESSION_TASKS.pop("pc-ledger-identity-test", None)


def test_shutdown_drain_calls_flush_all_with_configured_timeout(monkeypatch):
    """The FastAPI lifespan's shutdown half awaits ledger.flush_all with the
    module's bounded timeout (LEDG-02)."""
    flush_calls: list[float] = []

    async def _fake_flush_all(timeout: float = 10.0) -> None:
        flush_calls.append(timeout)

    monkeypatch.setattr(server.ledger, "flush_all", _fake_flush_all)

    with TestClient(server.app):
        pass  # entering/exiting the context runs the real lifespan startup/shutdown

    assert flush_calls == [server.LEDGER_DRAIN_TIMEOUT_SECONDS]


def test_shutdown_drain_is_bounded_when_a_writer_hangs(monkeypatch):
    """A genuinely hanging writer.close() must not block shutdown past the
    configured bound -- exercises the REAL ledger.flush_all (its own
    asyncio.wait_for is what enforces the bound, Plan 15-02), proving the
    server-level wiring round-trips correctly."""
    monkeypatch.setattr(server, "LEDGER_DRAIN_TIMEOUT_SECONDS", 0.05)
    writer = ledger.LedgerWriter(session_id="hang-test-session")

    async def _hang_close() -> None:
        await asyncio.sleep(3600)

    monkeypatch.setattr(writer, "close", _hang_close)
    try:
        start = time.monotonic()
        with TestClient(server.app):
            pass
        elapsed = time.monotonic() - start
        assert elapsed < 2.0  # well under the real 10s default; bounded by the 0.05s patch
    finally:
        ledger._ACTIVE_WRITERS.discard(writer)


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
