"""``AsteriskCallController`` -- the ARI/Stasis call-control seam (Phase 11,
D-02/D-04/R6).

**Process boundary (D-08, mirrors ``webrtc.py`` isolation).** This module
runs inside a **standalone telephony entrypoint** (a later plan, D-08) --
its own local process, run alongside the docker-compose Asterisk instance.
It is never imported by, and never imports, ``webrtc.py`` or the browser
``server.py``. It reuses ``factories.py`` / ``pipeline.py`` /
``call_runtime.py`` **in-process** (shared *code*), exactly the way
``server.py`` does for the browser path -- but the *process* is separate,
so a telephony bug can never take down the browser voice service and vice
versa.

**Responsibilities (D-02, spec Sec13).** ``AsteriskCallController`` consumes
ARI events and owns the ``calls: dict[str, ActiveCall]`` registry, keyed by
the *original* Asterisk SIP channel ID:

- :meth:`on_stasis_start` -- accepts only the expected inbound
  context/app, normalizes ANI/DID, binds the socket-backed
  :class:`~klanker_voice.telephony.rtp_socket.SocketRtpMediaSession`
  **before** creating Asterisk's External Media channel (R2 bind-first
  ordering -- ``connection_type=client`` means Asterisk always dials out),
  creates the External Media channel + a mixing bridge, attaches both
  channels, evaluates the quota gate, constructs a Klanker ``CallSession``
  via :func:`~klanker_voice.call_runtime.create_call_session`
  (``channel="pstn"``), and runs the worker as a tracked background task
  (Sec12 greeting-readiness ordering -- the pipeline is not started until
  the media path is fully wired).
- :meth:`on_channel_destroyed` / a hard session timeout -- both funnel
  through the single idempotent :meth:`_close_active_call`:
  ``CallSession.close()`` -> ``lifecycle.release()`` exactly once, then
  bridge/external-media-channel/RTP-socket teardown, then the registry
  entry is removed. No leaked resources (ROADMAP criterion 3, R6,
  T-11-05-01).
- A hard session timeout *also* ARI-hangs-up the original SIP channel
  (``lifecycle.on_released`` composed with ``ari.hangup(sip_channel_id)``,
  R6/Sec17/T-11-05-02) -- a wind-down that only cancels the Klanker-side
  pipeline would leave the caller's line silently open, still burning PSTN
  minutes.
- A quota-denied caller (the gate passed, but ``quota.start_gate`` then
  rejects -- e.g. ``ERROR_CONCURRENCY_LIMIT`` at ``max_concurrent_calls=1``)
  never gets a ``CallSession`` constructed; the bridge/external-media
  channel/socket already allocated for the gate are torn down and the SIP
  channel is hung up (R6 "quota-denied leaves no bridge", T-11-05-03).

**§24 gate note.** This plan lands the plumbing + the exactly-once teardown
guarantee; ``quota.start_gate`` is invoked here at an *interim* placement
(right after the bridge/channels are wired, using
``telephony_cfg.unlock_tier_id`` as the granted tier) so the lifecycle
tests in this plan can exercise the full allocate -> teardown path. Plan 06
moves the actual tier grant to the real §24 unlock boundary (DTMF PIN /
spoken passphrase) without changing this module's teardown contract.

**Logging discipline (§13).** Structured logs always carry the call ID.
This module NEVER logs SIP passwords, ARI auth headers, or full
PIN/passphrase values -- :class:`~klanker_voice.telephony.ari.AriError`
already enforces this one layer down (never embeds credentials), and no
code here introduces a new place credentials could leak.
"""

from __future__ import annotations

import asyncio
import time
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field
from typing import Any

from loguru import logger

from klanker_voice import quota
from klanker_voice.auth import SessionIdentity
from klanker_voice.call_runtime import CallIdentity, CallSession, create_call_session
from klanker_voice.config import DuplexConfig, KnowledgeConfig, PipelineConfig, QuotaConfig
from klanker_voice.telephony.ari import AriClient, AriError
from klanker_voice.telephony.config import TelephonyConfig
from klanker_voice.telephony.rtp_socket import SocketRtpMediaSession
from klanker_voice.telephony.transport import TelephonyTransport
from klanker_voice.telephony.types import RtpMediaSession, TelephonyTransportParams

#: Signature of the callable that opens a bound, listening RTP media session.
#: Defaults to :meth:`SocketRtpMediaSession.open`; a test can inject a fake
#: opener so the §16 lifecycle matrix never binds a real UDP socket.
MediaSessionOpener = Callable[[str, int], Awaitable[RtpMediaSession]]

#: The one Stasis app name every Phase-11 Asterisk config (``extensions.conf``
#: / ``ari.conf``) is wired to (11-02-SUMMARY.md).
DEFAULT_APP_NAME = "klanker"

#: The one inbound dialplan context every StasisStart must have come through
#: (``apps/voice/asterisk/extensions.conf``, 11-02-SUMMARY.md, T-11-02-01) --
#: anything else is an unexpected/hostile entry and is rejected, never
#: allocated a bridge (D-02).
DEFAULT_EXPECTED_CONTEXT = "from-klanker-inbound"


@dataclass
class ActiveCall:
    """One live (or gate-pending) PSTN call, registered by
    :meth:`AsteriskCallController.on_stasis_start` and torn down exactly
    once by :meth:`AsteriskCallController._close_active_call` (§13 field
    shape, D-02).

    ``call_session`` is ``None`` only for the brief window between the
    bridge/external-media allocation and a successful ``quota.start_gate``
    call -- if the gate rejects, this ``ActiveCall`` is never registered at
    all (R6 "quota-denied leaves no bridge"), so every entry that actually
    reaches ``self.calls`` has a real ``call_session``.
    """

    sip_channel_id: str
    external_media_channel_id: str
    bridge_id: str
    media_session: RtpMediaSession
    call_session: CallSession
    caller_id: str
    did: str
    created_at: float
    closed: bool = False
    lock: asyncio.Lock = field(default_factory=asyncio.Lock)


def _normalize_token(raw: Any) -> str:
    """Best-effort string normalization for caller-supplied ARI event
    fields (ANI/DID) -- never trusted for control, only for identity/logging
    (§13). Never raises on odd input (missing/None/non-string)."""
    if raw is None:
        return ""
    return str(raw).strip()


class AsteriskCallController:
    """Consumes ARI events and owns the ``calls`` registry (D-02, Sec13).

    Constructed once per process by the (later) standalone telephony
    entrypoint (D-08) and wired to an already-``connect()``ed
    :class:`~klanker_voice.telephony.ari.AriClient` via :meth:`register`
    (or by registering :meth:`on_stasis_start` / :meth:`on_channel_destroyed`
    directly with ``ari.on(...)``).
    """

    def __init__(
        self,
        ari: AriClient,
        cfg: PipelineConfig,
        knowledge_cfg: KnowledgeConfig,
        quota_cfg: QuotaConfig,
        telephony_cfg: TelephonyConfig,
        *,
        app_name: str = DEFAULT_APP_NAME,
        expected_context: str = DEFAULT_EXPECTED_CONTEXT,
        rtp_bind_host: str = "0.0.0.0",
        rtp_advertise_host: str = "127.0.0.1",
        media_session_opener: MediaSessionOpener = SocketRtpMediaSession.open,
    ) -> None:
        self._ari = ari
        self._cfg = cfg
        self._knowledge_cfg = knowledge_cfg
        self._quota_cfg = quota_cfg
        self._telephony_cfg = telephony_cfg
        self._app_name = app_name
        self._expected_context = expected_context
        self._rtp_bind_host = rtp_bind_host
        self._rtp_advertise_host = rtp_advertise_host
        self._open_media_session = media_session_opener

        #: D-02's registry, keyed by the original Asterisk SIP channel ID.
        self.calls: dict[str, ActiveCall] = {}

        #: Strong references to each call's tracked background
        #: ``call_session.run()`` task (mirrors ``server.py``'s
        #: ``SESSION_TASKS`` pattern -- ``asyncio.create_task`` only holds a
        #: *weak* reference, so without retaining it here a still-running
        #: call's task could be garbage-collected mid-call).
        self._tasks: dict[str, asyncio.Task] = {}

    def register(self) -> None:
        """Wire :meth:`on_stasis_start` / :meth:`on_channel_destroyed` onto
        the ``AriClient``'s event dispatch (convenience for the standalone
        entrypoint, D-08) -- tests may instead call the handlers directly."""
        self._ari.on("StasisStart", self.on_stasis_start)
        self._ari.on("ChannelDestroyed", self.on_channel_destroyed)

    # --- StasisStart: allocate + construct (Task 1) ------------------------

    async def on_stasis_start(self, event: dict[str, Any]) -> None:
        """Handle one ARI ``StasisStart`` event (D-02/D-04, Sec12/Sec13).

        Accepts only the expected inbound context/app; anything else is
        hung up immediately with no allocation. On the happy path: answer
        -> bind the socket media session FIRST (R2) -> create the External
        Media channel + mixing bridge -> attach both channels -> evaluate
        the quota gate -> construct + register the ``CallSession`` -> run
        the worker as a tracked background task.
        """
        channel = event.get("channel", {}) or {}
        sip_channel_id = _normalize_token(channel.get("id"))
        application = event.get("application", "")
        dialplan = channel.get("dialplan", {}) or {}
        context = dialplan.get("context", "")

        if application != self._app_name or context != self._expected_context:
            logger.warning(
                f"on_stasis_start: unexpected app={application!r} context={context!r} "
                f"channel={sip_channel_id!r}; hanging up, no allocation"
            )
            if sip_channel_id:
                await self._safe_ari(self._ari.hangup(sip_channel_id), "hangup (unexpected context)")
            return

        caller_id = _normalize_token((channel.get("caller") or {}).get("number"))
        did = _normalize_token(dialplan.get("exten"))

        logger.info(f"on_stasis_start: channel={sip_channel_id} caller={caller_id} did={did}")

        await self._ari.answer(sip_channel_id)

        # R2: Klanker must already be bound and listening BEFORE Asterisk's
        # externalMedia channel is created -- connection_type=client means
        # Asterisk always dials OUT to us; a not-yet-bound port silently
        # drops the first datagrams (UDP has no handshake/retry).
        media = await self._open_media_session(self._rtp_bind_host, 0)
        bound_port = media.bound_port

        bridge_id: str | None = None
        external_media_channel_id: str | None = None
        try:
            external_media_channel_id = await self._ari.create_external_media(
                app=self._app_name,
                external_host=f"{self._rtp_advertise_host}:{bound_port}",
                fmt="ulaw",
            )
            bridge_id = await self._ari.create_bridge("mixing")
            await self._ari.add_channel(bridge_id, sip_channel_id)
            await self._ari.add_channel(bridge_id, external_media_channel_id)

            # Interim placement (this plan): grant telephony_cfg.unlock_tier_id
            # directly. Plan 06 moves this to the real §24 unlock boundary
            # (DTMF PIN / spoken passphrase) without touching the teardown
            # contract below.
            gate_identity = SessionIdentity(
                sub=f"tel:{caller_id or sip_channel_id}",
                tier_id=self._telephony_cfg.unlock_tier_id,
                group=None,
                bypass_accounting=False,
            )
            gate_result = quota.start_gate(
                gate_identity,
                active_session_count=len(self.calls),
                per_task_max_sessions=self._telephony_cfg.max_concurrent_calls,
                heartbeat_ttl_seconds=self._quota_cfg.heartbeat_ttl,
                sub_floor_seconds=self._quota_cfg.sub_floor_seconds,
            )
        except quota.QuotaError as exc:
            # R6 "quota-denied leaves no bridge": the gate's own bridge +
            # external-media channel + socket are torn down; NO CallSession
            # is ever constructed for this caller.
            logger.warning(
                f"on_stasis_start: quota denied ({exc.error_type}) channel={sip_channel_id}"
            )
            await self._teardown_gate_resources(
                bridge_id, external_media_channel_id, media, sip_channel_id
            )
            return
        except Exception:
            logger.exception(
                f"on_stasis_start: failed to establish media/bridge for channel={sip_channel_id}"
            )
            await self._teardown_gate_resources(
                bridge_id, external_media_channel_id, media, sip_channel_id
            )
            return

        transport_params = TelephonyTransportParams(
            clock_rate=self._telephony_cfg.sample_rate,
            packet_time_ms=self._telephony_cfg.packet_ms,
            samples_per_packet=self._telephony_cfg.sample_rate
            * self._telephony_cfg.packet_ms
            // 1000,
        )
        transport = TelephonyTransport(call_id=sip_channel_id, media=media, params=transport_params)
        identity = CallIdentity(
            subject=f"tel:{caller_id or sip_channel_id}", authenticated=True, auth_method="pstn"
        )

        call_session = await create_call_session(
            transport=transport,
            identity=identity,
            gate_result=gate_result,
            cfg=self._cfg,
            knowledge_cfg=self._knowledge_cfg,
            duplex_cfg=DuplexConfig(),
            quota_cfg=self._quota_cfg,
            channel="pstn",
            metadata={"call_id": sip_channel_id, "did": did},
        )

        active_call = ActiveCall(
            sip_channel_id=sip_channel_id,
            external_media_channel_id=external_media_channel_id,
            bridge_id=bridge_id,
            media_session=media,
            call_session=call_session,
            caller_id=caller_id,
            did=did,
            created_at=time.time(),
        )
        self.calls[sip_channel_id] = active_call

        # R6: a hard session timeout (SessionLifecycle's own D-02 wall-clock
        # cutoff) must ALSO reach the SIP channel -- runner.cancel() alone
        # only ends the Klanker-side pipeline, leaving the PSTN line open
        # (T-11-05-02). Compose the default on_released (runner.cancel, set
        # by create_call_session) with the ARI hangup, then route through
        # the single idempotent teardown so bridge/external/socket/registry
        # are cleaned up too.
        async def _on_released() -> None:
            await call_session.runner.cancel("session wind-down complete")
            await self._safe_ari(
                self._ari.hangup(sip_channel_id), "hangup sip channel (hard timeout)"
            )
            await self._close_active_call(active_call, "hard timeout release")

        call_session.lifecycle.on_released = _on_released

        task = asyncio.create_task(call_session.run())
        self._tasks[sip_channel_id] = task
        task.add_done_callback(lambda _t, cid=sip_channel_id: self._tasks.pop(cid, None))

    async def _teardown_gate_resources(
        self,
        bridge_id: str | None,
        external_media_channel_id: str | None,
        media_session: RtpMediaSession,
        sip_channel_id: str,
    ) -> None:
        """Tear down the gate-only bridge/external-media channel/socket
        allocated before a quota rejection (or an unexpected allocation
        failure) -- no ``ActiveCall`` was ever registered for these, so this
        is NOT routed through :meth:`_close_active_call` (R6). A played
        goodbye is deferred to Plan 06's real §24 gate (no TTS-capable
        pipeline exists yet at this point in the flow); this plan hangs up
        directly so no PSTN charge is ever left silently open (§17)."""
        if bridge_id is not None:
            await self._safe_ari(self._ari.destroy_bridge(bridge_id), "destroy_bridge (gate)")
        if external_media_channel_id is not None:
            await self._safe_ari(
                self._ari.hangup(external_media_channel_id), "hangup external_media (gate)"
            )
        await media_session.close()
        await self._safe_ari(self._ari.hangup(sip_channel_id), "hangup sip channel (gate)")

    # --- ChannelDestroyed + the single idempotent teardown (Task 2) --------

    async def on_channel_destroyed(self, event: dict[str, Any]) -> None:
        """Handle one ARI ``ChannelDestroyed`` event: look up the
        ``ActiveCall`` by the original SIP channel ID and route through the
        one idempotent teardown. An unknown channel id is logged and
        ignored -- never fatal (mirrors :class:`~klanker_voice.telephony.
        ari.AriClient`'s own "never crash the dispatch loop" posture)."""
        channel_id = _normalize_token((event.get("channel", {}) or {}).get("id"))
        active_call = self.calls.get(channel_id)
        if active_call is None:
            logger.warning(f"on_channel_destroyed: unknown channel={channel_id!r}")
            return
        await self._close_active_call(active_call, "ari channel destroyed")

    async def _close_active_call(self, active_call: ActiveCall, reason: str) -> None:
        """The single idempotent teardown every close trigger funnels
        through (D-02/R6, T-11-05-01): ``ChannelDestroyed``, a hard-timeout
        release, and any future caller. Mirrors ``SessionLifecycle._stopped``
        one layer up -- a synchronous check-and-set under ``active_call.lock``
        so racing callers (simultaneous hangup + timeout) still tear down
        exactly once."""
        async with active_call.lock:
            if active_call.closed:
                return
            active_call.closed = True

        logger.info(f"_close_active_call: channel={active_call.sip_channel_id} reason={reason!r}")

        await active_call.call_session.close(reason)
        await self._safe_ari(self._ari.destroy_bridge(active_call.bridge_id), "destroy_bridge")
        await self._safe_ari(
            self._ari.hangup(active_call.external_media_channel_id), "hangup external_media"
        )
        await active_call.media_session.close()
        self.calls.pop(active_call.sip_channel_id, None)
        self._tasks.pop(active_call.sip_channel_id, None)

    async def _safe_ari(self, coro: Any, description: str) -> None:
        """Await ``coro`` (an in-flight ARI REST call), swallowing
        :class:`~klanker_voice.telephony.ari.AriError` so one already-gone
        Asterisk-side resource (e.g. Asterisk itself already tore the bridge
        down when the last channel left it) never aborts the rest of a
        teardown sequence (no leaked resources, T-11-05-01)."""
        try:
            await coro
        except AriError as exc:
            logger.warning(f"{description} failed (status={exc.status}); continuing teardown")
