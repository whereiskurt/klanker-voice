"""``klanker_voice.telephony.ari`` -- raw-``aiohttp`` ARI client (D-06).

**Decision (D-06, research-decided, 11-RESEARCH.md R1): raw ``aiohttp``, no
third-party ARI library.** Every candidate evaluated was rejected:

- ``ari-py`` -- synchronous (``requests``-based); would need a thread
  executor to coexist with pipecat's asyncio loop.
- ``asyncari`` -- wraps ``anyio`` (dual asyncio/trio); its own top-level
  ``anyio.run(main)`` entry-point pattern complicates embedding inside a
  process whose loop is already owned by pipecat's ``WorkerRunner``.
- ``aioari`` -- asyncio-native but wraps ``aioswagger11``, whose last PyPI
  release was 2018-04-13 (stale dependency chain three levels deep).
- ``panoramisk`` -- this is the Asterisk **Manager Interface (AMI)**, a
  different TCP protocol entirely (no REST, no Stasis, no externalMedia) --
  cannot do what D-01/D-02 need.

The ARI surface this phase needs is exactly six REST calls (answer, create
externalMedia channel, create mixing bridge, addChannel, hangup, destroy
bridge) plus one long-lived events WebSocket. ``aiohttp.ClientSession``
already does both jobs natively (``session.request(...)`` for REST,
``session.ws_connect(...)`` for the events stream) -- a ~150-line hand-rolled
wrapper has zero third-party surface beyond what's already vetted/pinned in
this repo (``aiohttp`` is already a transitive dependency via
``pipecat-ai[...]``).

Credentials (§13 logging rule): the ARI username/password are held only
inside the ``aiohttp.BasicAuth`` object passed to ``ClientSession``'s
constructor. They are NEVER logged, and NEVER embedded in an :class:`AriError`
message -- only the HTTP status and request path are ever included.
"""

from __future__ import annotations

from collections.abc import Awaitable, Callable
from typing import Any

import aiohttp
from loguru import logger

#: An async event handler: takes the parsed ARI event dict, returns nothing.
EventHandler = Callable[[dict[str, Any]], Awaitable[None]]


class AriError(Exception):
    """Raised on a non-2xx ARI REST response.

    Carries only the HTTP status and the request path -- NEVER the
    password/Basic-Auth header (T-11-04-01, §13 logging rule).
    """

    def __init__(self, status: int, path: str) -> None:
        self.status = status
        self.path = path
        super().__init__(f"ARI request to {path} failed with status {status}")


class AriClient:
    """Thin, hand-rolled ARI REST + events-WebSocket client over ``aiohttp``
    (D-06) -- no third-party ARI library (see module docstring for R1's
    library-by-library rejection).

    Covers exactly the six REST calls Phase 11 needs (:meth:`answer`,
    :meth:`create_external_media`, :meth:`create_bridge`, :meth:`add_channel`,
    :meth:`hangup`, :meth:`destroy_bridge`) plus the one long-lived events
    WebSocket (:meth:`run`, dispatching ``StasisStart``/
    ``ChannelDtmfReceived``/``ChannelDestroyed`` to handlers registered via
    :meth:`on`).

    Auth is HTTP Basic, sourced from the caller (``ASTERISK_ARI_USERNAME``/
    ``ASTERISK_ARI_PASSWORD`` env, resolved by the controller -- this module
    never reads env itself). Credentials are never logged (§13).
    """

    def __init__(self, base_url: str, username: str, password: str, app_name: str) -> None:
        self._base_url = base_url.rstrip("/")
        self._username = username
        self._password = password
        self._app_name = app_name
        self._session: aiohttp.ClientSession | None = None
        self._handlers: dict[str, EventHandler] = {}

    async def connect(self) -> None:
        """Create the underlying ``aiohttp.ClientSession`` with HTTP Basic
        auth. Idempotent -- a second call while an open session already
        exists is a no-op (reuses it)."""
        if self._session is not None and not self._session.closed:
            return
        auth = aiohttp.BasicAuth(self._username, self._password)
        self._session = aiohttp.ClientSession(auth=auth)

    async def close(self) -> None:
        """Close the underlying session. Safe to call more than once."""
        if self._session is not None and not self._session.closed:
            await self._session.close()

    def _require_session(self) -> aiohttp.ClientSession:
        if self._session is None:
            raise RuntimeError("AriClient.connect() must be awaited before use")
        return self._session

    async def _request(self, method: str, path: str, **params: Any) -> dict[str, Any]:
        """Issue one REST call against ``{base_url}{path}``, raising
        :class:`AriError` on a non-2xx response. Most ARI REST endpoints used
        here return either a JSON body (``externalMedia``/``bridges``
        creation) or an empty ``204 No Content`` (answer/addChannel/hangup/
        destroy) -- both are handled uniformly."""
        session = self._require_session()
        url = f"{self._base_url}{path}"
        async with session.request(method, url, params=params) as resp:
            if resp.status // 100 != 2:
                raise AriError(resp.status, path)
            text = await resp.text()
            if not text:
                return {}
            return await resp.json()

    # --- REST surface (Task 1) --------------------------------------------

    async def answer(self, channel_id: str) -> None:
        """``POST /ari/channels/{id}/answer``."""
        await self._request("POST", f"/ari/channels/{channel_id}/answer")

    async def create_external_media(
        self,
        app: str | None = None,
        external_host: str = "",
        fmt: str = "ulaw",
        channel_id: str | None = None,
    ) -> str:
        """``POST /ari/channels/externalMedia``.

        Passes exactly the only-supported External-Media values (R2):
        ``encapsulation=rtp``, ``transport=udp``, ``connection_type=client``,
        ``direction=both`` -- these are not configurable knobs, Asterisk does
        not currently support any other value for them.

        Returns the new external-media channel id (for the ``ActiveCall``
        registry).
        """
        params: dict[str, Any] = {
            "app": app or self._app_name,
            "external_host": external_host,
            "format": fmt,
            "encapsulation": "rtp",
            "transport": "udp",
            "connection_type": "client",
            "direction": "both",
        }
        if channel_id is not None:
            params["channelId"] = channel_id
        data = await self._request("POST", "/ari/channels/externalMedia", **params)
        return data["id"]

    async def create_bridge(self, bridge_type: str = "mixing") -> str:
        """``POST /ari/bridges``. Returns the new bridge id."""
        data = await self._request("POST", "/ari/bridges", type=bridge_type)
        return data["id"]

    async def add_channel(self, bridge_id: str, channel_id: str) -> None:
        """``POST /ari/bridges/{bridge_id}/addChannel``."""
        await self._request("POST", f"/ari/bridges/{bridge_id}/addChannel", channel=channel_id)

    async def hangup(self, channel_id: str) -> None:
        """``DELETE /ari/channels/{id}``."""
        await self._request("DELETE", f"/ari/channels/{channel_id}")

    async def destroy_bridge(self, bridge_id: str) -> None:
        """``DELETE /ari/bridges/{id}``."""
        await self._request("DELETE", f"/ari/bridges/{bridge_id}")

    # --- Events WebSocket dispatch (Task 2) --------------------------------

    def on(self, event_type: str, handler: EventHandler) -> None:
        """Register ``handler`` to be awaited for every event whose
        ``event["type"]`` equals ``event_type``. Registering again for the
        same ``event_type`` replaces the previous handler."""
        self._handlers[event_type] = handler

    async def run(self) -> None:
        """Connect to ``GET /ari/events?app={app_name}&subscribeAll=true``
        and dispatch every JSON text frame to its registered handler by
        ``event["type"]``.

        - An event type with no registered handler is ignored (debug-logged),
          never crashes the loop.
        - A handler raising an exception is caught and logged; the loop
          continues -- one bad event never kills call control
          (T-11-04-02).
        - A WS close/error frame causes ``run()`` to return cleanly (no
          infinite tight-loop) -- reconnection policy stays with the caller
          for this phase (R6).

        DTMF accumulation is deliberately NOT handled here (Landmine 5): ARI
        delivers ``ChannelDtmfReceived`` as one event per digit, and the
        controller (Plan 05) is the layer that accumulates digits across its
        own gate window -- this client only ferries the parsed event dict.
        """
        session = self._require_session()
        url = f"{self._base_url}/ari/events"
        params = {"app": self._app_name, "subscribeAll": "true"}
        async with session.ws_connect(url, params=params) as ws:
            async for msg in ws:
                if msg.type == aiohttp.WSMsgType.TEXT:
                    await self._dispatch(msg)
                elif msg.type in (
                    aiohttp.WSMsgType.CLOSE,
                    aiohttp.WSMsgType.CLOSED,
                    aiohttp.WSMsgType.CLOSING,
                    aiohttp.WSMsgType.ERROR,
                ):
                    break
                # any other frame type (BINARY/PING/PONG/...) is ignored --
                # ARI only ever sends JSON text frames for events.

    async def _dispatch(self, msg: aiohttp.WSMessage) -> None:
        try:
            event = msg.json()
        except (ValueError, TypeError):
            logger.debug("ari: dropped malformed event frame")
            return
        event_type = event.get("type") if isinstance(event, dict) else None
        handler = self._handlers.get(event_type) if event_type else None
        if handler is None:
            logger.debug(f"ari: no handler registered for event type {event_type!r}")
            return
        try:
            await handler(event)
        except Exception:  # noqa: BLE001 - one bad handler must not kill call control (T-11-04-02)
            logger.exception(f"ari: handler for {event_type!r} raised")
