"""Production FastAPI entrypoint for the deployed klanker-voice service (INFR-03).

Serves POST ``/api/offer`` (SmallWebRTC signaling) and GET ``/health`` on port
7860 — the self-hosted Fargate deploy target the pipecat dev runner
(``pipecat.runner.run`` / ``bot.py``) doesn't provide: no configurable health
check, no injectable start-gate, no candidate control. This module is the
enforcement seam every session rides through::

    validate_access_token()  ->  start_gate(identity)  ->  WebRTC transport

Both steps happen before any :class:`SmallWebRTCConnection` is created
(T-04-01): a forged/expired/wrong-audience/wrong-issuer/unknown-key token
never reaches a transport. ``start_gate`` calls ``quota.start_gate`` (04-04):
real race-safe quota enforcement (concurrency slot, daily floor, kill-switch
read, per-task cap) — see :mod:`klanker_voice.quota`.

Run with::

    uv run uvicorn server:app --host 0.0.0.0 --port 7860
"""

from __future__ import annotations

import asyncio
from dataclasses import dataclass
from typing import Any

from dotenv import load_dotenv
from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse
from loguru import logger

from pipecat.processors.frameworks.rtvi import RTVIObserver
from pipecat.transports.base_transport import TransportParams
from pipecat.transports.smallwebrtc.connection import SmallWebRTCConnection
from pipecat.transports.smallwebrtc.request_handler import (
    SmallWebRTCRequest,
    SmallWebRTCRequestHandler,
)
from pipecat.transports.smallwebrtc.transport import SmallWebRTCTransport
from pipecat.workers.runner import WorkerRunner

from klanker_voice import quota, session
from klanker_voice.auth import AuthError, SessionIdentity, validate_access_token
from klanker_voice.config import load_config, load_quota_config
from klanker_voice.observers import LatencyReportObserver
from klanker_voice.pipeline import (
    build_pipeline,
    build_worker,
    inject_warning_instruction,
    register_greet_first,
    speak_goodbye,
)
from klanker_voice.rtvi import build_rtvi_observer_params, build_rtvi_processor
from klanker_voice.session import SessionLifecycle, TeardownObserver
from klanker_voice.webrtc import (
    build_ice_servers,
    gather_public_candidates,
    inject_public_host_candidate,
)

load_dotenv(override=True)

app = FastAPI(title="klanker-voice")


@dataclass
class SessionRecord:
    """Lifecycle state attached to a negotiated session, keyed by pc_id."""

    identity: SessionIdentity
    gate_result: quota.GateResult
    lifecycle: SessionLifecycle


#: Session registry: pc_id -> SessionRecord. A single, well-known seam that
#: 04-04 (quota enforcement) and 04-05 (idle teardown) attach lifecycle state
#: to — deliberately a plain module-level dict so those plans are additive,
#: not a restructure.
SESSIONS: dict[str, SessionRecord] = {}

#: One SmallWebRTCRequestHandler for the process lifetime, ICE-server-configured
#: at import (mirrors the pipecat dev runner's own module-level handler
#: pattern). Building it only reads KMV_STUN_URL and constructs an in-memory
#: IceServer object — no network I/O at import time.
_webrtc_handler = SmallWebRTCRequestHandler(ice_servers=build_ice_servers())

_WEBRTC_TRANSPORT_PARAMS = TransportParams(audio_in_enabled=True, audio_out_enabled=True)


def start_gate(identity: SessionIdentity) -> quota.GateResult:
    """Start-gate hook: race-safe quota enforcement (04-04, QUOT-01, D-11).

    Delegates to :func:`klanker_voice.quota.start_gate`, which raises a
    typed :class:`klanker_voice.quota.QuotaError` on rejection (bypass,
    site-paused, no-access, at-capacity, concurrency-limit, daily-limit —
    see that module for the enforcement order). The per-task cap reads
    ``session.active_session_count()`` — :mod:`klanker_voice.session` owns
    the single source of truth for this task's live session count (INFR-06).
    """
    quota_cfg = load_quota_config()
    return quota.start_gate(
        identity,
        active_session_count=session.active_session_count(),
        per_task_max_sessions=quota_cfg.per_task_max_sessions,
        heartbeat_ttl_seconds=quota_cfg.heartbeat_ttl,
        sub_floor_seconds=quota_cfg.sub_floor_seconds,
    )


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


async def _run_session(connection: SmallWebRTCConnection, lifecycle: SessionLifecycle) -> None:
    """Build and run the pipeline over an established SmallWebRTC
    connection, wiring the D-04/D-05 spoken wind-down and D-06/D-07 idle-
    teardown layers onto the real worker/transport/context (04-05) —
    :class:`SessionLifecycle` deliberately never holds these references
    itself (module docstring), so this is the one seam that closes the loop
    between its callback hooks and the live pipeline.
    """
    transport = SmallWebRTCTransport(
        params=_WEBRTC_TRANSPORT_PARAMS,
        webrtc_connection=connection,
    )
    cfg = load_config()
    quota_cfg = load_quota_config()
    rtvi = build_rtvi_processor()
    built = build_pipeline(cfg, transport, rtvi=rtvi)
    worker = build_worker(
        built.pipeline,
        observers=[
            LatencyReportObserver(cfg),
            RTVIObserver(rtvi, params=build_rtvi_observer_params()),
            TeardownObserver(lifecycle),
        ],
    )
    register_greet_first(transport, worker, built.context)
    runner = WorkerRunner(handle_sigint=False)
    await runner.add_workers(worker)

    async def _on_warning() -> None:
        # D-04 natural warning: a high-priority LLM-context instruction, not
        # spoken verbatim by code.
        await inject_warning_instruction(worker, built.context, quota_cfg.warning_copy)

    async def _on_stop() -> None:
        # D-04/D-05: the deterministic goodbye bypasses the LLM, gets up to
        # goodbye_grace_seconds to finish, then a hard close. WorkerRunner
        # .cancel is pipecat's own documented hangup call ("typically on
        # transport disconnect") and is idempotent, so a racing idle-
        # teardown layer calling it again is a harmless no-op.
        await speak_goodbye(worker, quota_cfg.goodbye_copy)
        await asyncio.sleep(quota_cfg.goodbye_grace_seconds)
        await runner.cancel("session wind-down complete")

    lifecycle.on_warning = _on_warning
    lifecycle.on_stop = _on_stop
    # D-06 layer 3 fallback / belt-and-suspenders: every idle-teardown layer
    # (transport disconnect + reconnect grace, silence watchdog, pipeline
    # stall) releases the DB/metric bookkeeping via SessionLifecycle.release()
    # on its own; on_released is what actually ends *this* running pipeline
    # once that happens, so an abandoned session never keeps burning STT/LLM/
    # TTS spend after its slot has already been freed.
    lifecycle.on_released = runner.cancel

    @transport.event_handler("on_client_disconnected")
    async def _on_client_disconnected(transport, client):  # noqa: ANN001 — pipecat handler shape
        await lifecycle.on_transport_disconnected()

    @transport.event_handler("on_client_connected")
    async def _on_client_reconnected(transport, client):  # noqa: ANN001 — pipecat handler shape
        # Fires both for the very first connect (register_greet_first's own
        # handler greets there) and again if ICE reconnects after a drop —
        # cancelling a pending reconnect-grace teardown is a no-op the rest
        # of the time (D-07).
        await lifecycle.on_transport_reconnected()

    await runner.run()


async def _start_and_run_tracked_session(
    connection: SmallWebRTCConnection, lifecycle: SessionLifecycle
) -> None:
    """Start the session lifecycle (metric + scale-in protection + service
    timer/tick, QUOT-02/INFR-06), run the pipeline, then always stop the
    lifecycle on exit (success, error, or cancellation) — this is what
    releases the heartbeat lease and the active-session slot.

    Started as its own fire-and-forget task (not awaited inline in the
    connection callback) so a slow AWS call in ``lifecycle.start()`` never
    delays the SDP answer.
    """
    await lifecycle.start()
    try:
        await _run_session(connection, lifecycle)
    finally:
        await lifecycle.stop()


async def _negotiate_webrtc(
    body: dict[str, Any], identity: SessionIdentity, gate_result: quota.GateResult
) -> Any:
    """Create/renegotiate the SmallWebRTC connection and return the SDP answer.

    Isolated from :func:`offer` so unit tests can stub this seam and exercise
    the auth/start_gate flow without doing real aiortc/ICE negotiation
    (SDP offers require a live browser peer to be meaningful). The session's
    active-session slot is only claimed once the connection actually
    negotiates (inside ``SessionLifecycle.start()``, called from the
    fire-and-forget task below) — a session that fails to negotiate never
    leaks a phantom slot.
    """
    webrtc_request = SmallWebRTCRequest.from_dict(dict(body))
    quota_cfg = load_quota_config()

    async def _connection_callback(connection: SmallWebRTCConnection) -> None:
        lifecycle = SessionLifecycle(
            user_id=identity.sub,
            session_id=gate_result.session_id,
            tier=gate_result.tier,
            quota_config=quota_cfg,
            bypass_accounting=gate_result.bypass_accounting,
        )
        SESSIONS[connection.pc_id] = SessionRecord(
            identity=identity, gate_result=gate_result, lifecycle=lifecycle
        )
        asyncio.create_task(_start_and_run_tracked_session(connection, lifecycle))

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
        gate_result = start_gate(identity)
    except quota.QuotaError as exc:
        logger.info(f"/api/offer start_gate rejected sub={identity.sub}: {exc.error_type}")
        return JSONResponse(
            status_code=exc.http_status, content={"error": exc.error_type, "message": exc.message}
        )
    except Exception as exc:  # defensive: any other start_gate failure still rejects, never 500s
        logger.info(f"/api/offer start_gate rejected sub={identity.sub}: {exc}")
        return JSONResponse(status_code=403, content={"error": "rejected", "detail": str(exc)})

    return await _negotiate_webrtc(body, identity, gate_result)
