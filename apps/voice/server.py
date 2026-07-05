"""Production FastAPI entrypoint for the deployed klanker-voice service (INFR-03).

Serves POST ``/api/offer`` (SmallWebRTC signaling) and GET ``/health`` on port
7860 — the self-hosted Fargate deploy target the pipecat dev runner
(``pipecat.runner.run`` / ``bot.py``) doesn't provide: no configurable health
check, no injectable start-gate, no candidate control. This module is the
enforcement seam every session rides through::

    validate_access_token()  ->  start_gate(identity)  ->  WebRTC transport

Both steps happen before any :class:`SmallWebRTCConnection` is created
(T-04-01): a forged/expired/wrong-audience/wrong-issuer/unknown-key token
never reaches a transport. ``start_gate`` defaults to allow; 04-04 replaces
its body with real quota enforcement (concurrency slot, daily floor,
kill-switch read) — the function stays a single well-named seam so that plan
is a body swap, not a restructure.

Run with::

    uv run uvicorn server:app --host 0.0.0.0 --port 7860
"""

from __future__ import annotations

import asyncio
from typing import Any

from dotenv import load_dotenv
from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse
from loguru import logger

from pipecat.transports.base_transport import TransportParams
from pipecat.transports.smallwebrtc.connection import SmallWebRTCConnection
from pipecat.transports.smallwebrtc.request_handler import (
    SmallWebRTCRequest,
    SmallWebRTCRequestHandler,
)
from pipecat.transports.smallwebrtc.transport import SmallWebRTCTransport
from pipecat.workers.runner import WorkerRunner

from klanker_voice.auth import AuthError, SessionIdentity, validate_access_token
from klanker_voice.config import load_config
from klanker_voice.observers import LatencyReportObserver
from klanker_voice.pipeline import build_pipeline, build_worker, register_greet_first
from klanker_voice.webrtc import (
    build_ice_servers,
    gather_public_candidates,
    inject_public_host_candidate,
)

load_dotenv(override=True)

app = FastAPI(title="klanker-voice")

#: Session registry: pc_id -> SessionIdentity. A single, well-known seam that
#: 04-04 (quota enforcement) and 04-05 (idle teardown) attach lifecycle state
#: to — deliberately a plain module-level dict so those plans are additive,
#: not a restructure.
SESSIONS: dict[str, SessionIdentity] = {}

#: One SmallWebRTCRequestHandler for the process lifetime, ICE-server-configured
#: at import (mirrors the pipecat dev runner's own module-level handler
#: pattern). Building it only reads KMV_STUN_URL and constructs an in-memory
#: IceServer object — no network I/O at import time.
_webrtc_handler = SmallWebRTCRequestHandler(ice_servers=build_ice_servers())

_WEBRTC_TRANSPORT_PARAMS = TransportParams(audio_in_enabled=True, audio_out_enabled=True)


def start_gate(identity: SessionIdentity) -> None:
    """Start-gate hook — default allow.

    04-04 replaces this body with real quota enforcement (no-access reject,
    concurrency-slot acquisition, daily-floor check, kill-switch read) and
    raises a typed rejection error (D-11) instead of returning. The default
    implementation here never rejects a session.
    """
    return None


def _extract_bearer_token(request: Request, body: dict[str, Any]) -> str | None:
    """Pull the bearer credential from the Authorization header, or the
    pipecat client's ``request_data.access_token`` field as a fallback."""
    auth_header = request.headers.get("authorization", "")
    if auth_header.lower().startswith("bearer "):
        return auth_header[7:].strip()
    request_data = body.get("request_data")
    if isinstance(request_data, dict):
        token = request_data.get("access_token")
        if isinstance(token, str):
            return token
    return None


@app.get("/health")
async def health() -> dict[str, str]:
    """Unauthenticated ALB health check (this is the target group health_check_path)."""
    return {"status": "ok"}


async def _run_session(connection: SmallWebRTCConnection) -> None:
    """Run the Phase-1 pipeline over an established SmallWebRTC connection."""
    transport = SmallWebRTCTransport(
        params=_WEBRTC_TRANSPORT_PARAMS,
        webrtc_connection=connection,
    )
    cfg = load_config()
    built = build_pipeline(cfg, transport)
    worker = build_worker(built.pipeline, observers=[LatencyReportObserver(cfg)])
    register_greet_first(transport, worker, built.context)
    runner = WorkerRunner(handle_sigint=False)
    await runner.add_workers(worker)
    await runner.run()


async def _negotiate_webrtc(body: dict[str, Any], identity: SessionIdentity) -> Any:
    """Create/renegotiate the SmallWebRTC connection and return the SDP answer.

    Isolated from :func:`offer` so unit tests can stub this seam and exercise
    the auth/start_gate flow without doing real aiortc/ICE negotiation
    (SDP offers require a live browser peer to be meaningful).
    """
    webrtc_request = SmallWebRTCRequest.from_dict(dict(body))

    async def _connection_callback(connection: SmallWebRTCConnection) -> None:
        SESSIONS[connection.pc_id] = identity
        asyncio.create_task(_run_session(connection))

    answer = await _webrtc_handler.handle_web_request(webrtc_request, _connection_callback)

    public = gather_public_candidates()
    if answer and public.public_ip:
        answer["sdp"] = inject_public_host_candidate(answer["sdp"], public.public_ip)
    return answer


@app.post("/api/offer")
async def offer(request: Request) -> Any:
    """SmallWebRTC signaling: validate token -> start_gate -> transport (T-04-01)."""
    try:
        body = await request.json()
    except Exception:
        body = {}
    if not isinstance(body, dict):
        body = {}

    token = _extract_bearer_token(request, body)
    try:
        identity = validate_access_token(token or "")
    except AuthError as exc:
        logger.info(f"/api/offer rejected: {exc}")
        return JSONResponse(status_code=401, content={"error": "unauthorized"})

    try:
        start_gate(identity)
    except Exception as exc:  # 04-04 raises typed QuotaError subclasses here (D-11)
        logger.info(f"/api/offer start_gate rejected sub={identity.sub}: {exc}")
        return JSONResponse(status_code=403, content={"error": "rejected", "detail": str(exc)})

    return await _negotiate_webrtc(body, identity)
