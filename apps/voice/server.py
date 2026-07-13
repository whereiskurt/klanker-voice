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
from pathlib import Path
from typing import Any

from dotenv import load_dotenv
from fastapi import FastAPI, Request
from fastapi.responses import FileResponse, JSONResponse
from fastapi.staticfiles import StaticFiles
from loguru import logger

from pipecat.transports.base_transport import TransportParams
from pipecat.transports.smallwebrtc.connection import SmallWebRTCConnection
from pipecat.transports.smallwebrtc.request_handler import (
    SmallWebRTCPatchRequest,
    SmallWebRTCRequest,
    SmallWebRTCRequestHandler,
)
from pipecat.transports.smallwebrtc.transport import SmallWebRTCTransport

from klanker_voice import quota, session, variants
from klanker_voice import version as server_version
from klanker_voice.auth import AuthError, SessionIdentity, validate_access_token
from klanker_voice.call_runtime import CallIdentity, create_call_session
from klanker_voice.config import (
    load_config,
    load_duplex_config,
    load_knowledge_config,
    load_quota_config,
)
from klanker_voice.pipeline import build_ambience_mixer
from klanker_voice.session import SessionLifecycle
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

#: Strong references to the fire-and-forget session tasks, keyed by pc_id.
#: ``asyncio.create_task`` only keeps a *weak* reference to the task it
#: returns, so without retaining it here a still-pending session task can be
#: garbage-collected mid-run — silently dropping its ``finally: release()``
#: and leaking the heartbeat lease (voice-concurrency-slot-leak BUG 1
#: hardening). Cleared by the task's own done-callback.
SESSION_TASKS: dict[str, asyncio.Task] = {}

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


def _wire_connection_teardown(
    connection: SmallWebRTCConnection, lifecycle: SessionLifecycle
) -> None:
    """Release the slot IMMEDIATELY on the raw connection's ``closed`` event,
    at connection-creation time — the abrupt-path release trigger that the
    transport's ``on_client_disconnected`` handler misses
    (voice-concurrency-slot-leak BUG 1 + rev :13 refinement).

    On an abrupt client vanish (tab-close / RELOAD / ICE-close) aiortc discards
    the peer and :class:`SmallWebRTCConnection` fires its ``closed`` event, but
    the higher-level transport's ``on_client_disconnected`` — registered only
    inside :func:`_run_session`, *after* the awaited (slow, AWS-bound)
    ``lifecycle.start()`` — may never see it. Production evidence: the
    ``request_handler`` logs "Discarding peer connection" yet nothing reaches
    :meth:`SessionLifecycle.release`, so the heartbeat lease lingered at its
    full 45s TTL and walled reconnects with ``concurrency-limit`` 403.

    **Why immediate release, not the reconnect grace.** The connection
    ``closed`` event is *terminal*: pipecat fires it only once aiortc has
    actually ``pc.close()``d the peer — a graceful ``disconnect()``, an ICE
    ``failed`` self-close (``_handle_new_connection_state``), or the connecting
    timeout. A *transient* ICE blip fires the separate ``disconnected`` event
    (routed to ``_handle_peer_disconnected``, which does **not** reach
    ``on_client_disconnected``); a ``restart_pc`` renegotiation removes the old
    pc's listeners before closing, so it never fires ``closed`` either. And
    after a ``closed`` the ``request_handler`` pops the pc from its map while a
    returning client gets a brand-new ``session_id`` from ``start_gate`` — so a
    *same-session* reconnect is impossible. Routing a terminal close through the
    12s D-07 reconnect grace (rev :13) therefore waits for a reconnect that can
    never come: two reloads inside the grace stacked two draining slots and
    walled the third at ``max_concurrent=2`` for ~30s. Calling ``release()``
    directly frees the slot at once.

    The 12s reconnect grace stays reserved for the TRANSIENT path — the
    transport's ``on_client_disconnected`` -> ``on_transport_disconnected`` —
    which is left unchanged (see :func:`_run_session`); a legitimate mid-session
    ICE recovery still cancels that grace via ``on_transport_reconnected``.
    ``release()`` is the single idempotent teardown, so this is safe alongside
    the transport handler: its ``_stopped`` guard makes any double-fire
    (``closed`` + a racing ``on_client_disconnected``) a no-op, and it cancels
    any pending reconnect-grace task, so immediate release always wins.
    """

    @connection.event_handler("closed")
    async def _on_connection_closed(connection):  # noqa: ANN001 — pipecat handler shape
        await lifecycle.release()


async def _negotiate_webrtc(
    body: dict[str, Any],
    identity: SessionIdentity,
    gate_result: quota.GateResult,
    variant: str = variants.DEFAULT_VARIANT,
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
    quota_cfg = load_quota_config()  # global budget guardrail — never per-variant

    async def _connection_callback(connection: SmallWebRTCConnection) -> None:
        # variant-aware config load (full-duplex, 2026-07-10): ``voice1``
        # (default) is today's ``pipeline.toml``; ``voice2`` is
        # ``configs/voice2.toml`` (Flux STT + duplex controller). Only steers
        # the *pipeline* — auth, transport, teardown, and the site-wide
        # ``quota`` budget are variant-independent (quota_cfg above).
        config_path = variants.variant_config_path(variant)  # None -> default pipeline.toml
        cfg = load_config(config_path)
        knowledge_cfg = load_knowledge_config(config_path)
        duplex_cfg = load_duplex_config(config_path)

        # WebRTC-specific (D-03): the ambience mixer + TransportParams must be
        # attached at transport construction time, so they're built here, not
        # inside the shared runtime. Greenhouse coffee-shop bed (260710): a
        # per-session SoundfileMixer on the output transport, OFF until the
        # router enables it while greenhouse is active. Pin
        # audio_out_sample_rate to the WAV's rate (the mixer won't resample).
        # No mixer -> the shared default params (byte-identical to before).
        mixer = build_ambience_mixer(cfg)
        transport_params = (
            TransportParams(
                audio_in_enabled=True,
                audio_out_enabled=True,
                audio_out_sample_rate=cfg.greenhouse.ambience_sample_rate,
                audio_out_mixer=mixer,
            )
            if mixer is not None
            else _WEBRTC_TRANSPORT_PARAMS
        )
        # WebRTC-specific (D-03): transport construction stays here; the
        # shared runtime accepts an already-built BaseTransport.
        transport = SmallWebRTCTransport(
            params=transport_params,
            webrtc_connection=connection,
        )

        call_session = await create_call_session(
            transport=transport,
            identity=CallIdentity(subject=identity.sub, authenticated=True),
            gate_result=gate_result,
            cfg=cfg,
            knowledge_cfg=knowledge_cfg,
            duplex_cfg=duplex_cfg,
            quota_cfg=quota_cfg,
            channel="webrtc",
            metadata={"pc_id": connection.pc_id},
        )
        SESSIONS[connection.pc_id] = SessionRecord(
            identity=identity, gate_result=gate_result, lifecycle=call_session.lifecycle
        )
        # Wire the abrupt-close release trigger BEFORE spawning the run task,
        # so a client that vanishes during the slow ``lifecycle.start()``
        # window still reaches release() (voice-concurrency-slot-leak BUG 1).
        _wire_connection_teardown(connection, call_session.lifecycle)
        pc_id = connection.pc_id
        task = asyncio.create_task(call_session.run())
        # Retain a strong ref (see SESSION_TASKS) and pop both registries once
        # the session task ends, however it ends.
        SESSION_TASKS[pc_id] = task

        def _on_session_task_done(_task: asyncio.Task) -> None:
            SESSION_TASKS.pop(pc_id, None)
            SESSIONS.pop(pc_id, None)

        task.add_done_callback(_on_session_task_done)

    answer = await _webrtc_handler.handle_web_request(webrtc_request, _connection_callback)

    public = gather_public_candidates()
    if answer and public.public_ip:
        answer["sdp"] = inject_public_host_candidate(answer["sdp"], public.public_ip)
    if answer:
        # CLNT-05/D-10: the client countdown has no other source for the tier
        # session cap (the JWT only carries tier_id, not the numeric seconds
        # -- see 03-03-SUMMARY's claim contract) other than this connect-flow
        # response, per the plan's own key_link ("tier session_max_seconds
        # (token claim / offer) + session start -> useCountdown"). Additive
        # key -- SmallWebRTCTransport only reads sdp/type/pc_id and ignores
        # unknown fields, so this is backward compatible with the vendor
        # client's own answer parsing (T-05-05-T: display-only, the server's
        # own service timer remains the authoritative hard-stop).
        answer["session_max_seconds"] = gate_result.session_max_seconds
        # Display-only per-variant label (subtle live-UI tag, mirrors the
        # session_max_seconds plumbing above): a lightweight, deliberate extra
        # TOML parse -- ``_connection_callback`` (above) loads its own copy of
        # ``cfg`` for the pipeline, but that happens inside the request
        # handler's callback and isn't threaded back out here, so this reads
        # the config a second time purely for the label.
        label_cfg = load_config(variants.variant_config_path(variant))
        answer["variant_label"] = label_cfg.label
        # Server/pipeline build stamp (VERSION concept V2): the short git SHA of
        # the running ECS image, so the UI can show `pipe:<sha>` next to its own
        # `ui:<sha>` and confirm the pipeline (not just the CloudFront assets)
        # rolled to this commit. Additive key, same ignore-unknown contract.
        answer["server_version"] = server_version.APP_VERSION
        answer["server_built_at"] = server_version.APP_BUILT_AT
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

    # Variant selection (full-duplex, 2026-07-10): the browser page (/voice1,
    # /voice2) posts ?variant=<name>. It's attacker-controllable, so it's
    # normalized against a fixed allowlist (unknown -> DEFAULT_VARIANT) and only
    # ever used as a registry key, never a path (see klanker_voice.variants).
    variant = variants.normalize_variant(request.query_params.get("variant"))

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

    return await _negotiate_webrtc(body, identity, gate_result, variant)


@app.patch("/api/offer")
async def ice_candidate(request: SmallWebRTCPatchRequest) -> Any:
    """Trickle-ICE candidate updates for an already-negotiated connection.

    The pipecat SmallWebRTC client POSTs the initial offer (token-gated +
    quota-gated by :func:`offer` above), then PATCHes trickled ICE candidates
    to the SAME ``pc_id`` as the media path comes up and whenever it
    renegotiates/ICE-restarts mid-session. The pipecat dev runner provides
    this route (``run.py``) but this self-hosted entrypoint had only the POST
    half — so every candidate PATCH 405'd, the server never received the
    browser's trickled candidates, and the session dropped shortly after
    connecting (worked locally only because the dev runner has it).

    No token re-check here (mirrors the runner): a PATCH only adds ICE
    candidates to an EXISTING connection resolved by its opaque ``pc_id`` —
    it can neither create a session nor bypass the POST's auth/quota gate.
    """
    await _webrtc_handler.handle_patch_request(request)
    return {"status": "success"}


#: The built client (D-01/02/03): Vite+React SPA source lives at
#: apps/voice/client/, built to dist/, COPY'd into the Docker image by the
#: (future) multi-stage build. Resolved relative to this file, never CWD, so
#: the mount is correct regardless of where the process is launched from
#: (T-05-01-I).
CLIENT_DIST_DIR = Path(__file__).resolve().parent / "client" / "dist"


def _mount_client_spa(app: FastAPI, dist_dir: Path) -> None:
    """Mount the built SPA with deep-link fallback (D-01/02/03, CLNT-08).

    Registered AFTER ``/health`` and ``/api/offer`` (module load order) so
    those routes always win over the mount — Starlette matches routes in
    registration order, and a root-mounted ``StaticFiles`` prefix-matches
    every path, so anything declared first still takes priority.

    Mechanism: ``StaticFiles(html=True)`` serves ``GET /`` (index.html) and
    hashed asset paths directly. A 404 from inside that mount (e.g. the OIDC
    callback route, or any other client-side deep link) is caught by the
    404 exception handler below and re-served as ``index.html`` so the SPA's
    own router receives it — EXCEPT under ``/api``, which 404s normally
    (T-05-01-I: the fallback must never mask a real API 404).

    Tolerant of a missing ``dist_dir`` (local dev / unit tests, before the
    client has been built): skips the mount entirely and logs once, rather
    than raising at import time.
    """
    if not dist_dir.is_dir():
        logger.warning(f"client dist directory not found at {dist_dir}; skipping SPA mount")
        return

    index_path = dist_dir / "index.html"
    app.mount("/", StaticFiles(directory=str(dist_dir), html=True), name="client-spa")

    @app.exception_handler(404)
    async def _spa_fallback(request: Request, exc: Exception) -> Any:
        if request.url.path.startswith("/api"):
            return JSONResponse(status_code=404, content={"error": "not_found"})
        return FileResponse(index_path)


_mount_client_spa(app, CLIENT_DIST_DIR)
