"""``AriClient`` (D-06/D-01) unit tests -- the raw-``aiohttp`` ARI REST +
events-WebSocket client the Plan-05 controller consumes.

Fakes stand in for ``aiohttp.ClientSession``/its request context managers
(Task 1: REST surface) and for its WebSocket response object (Task 2: events
dispatch loop) -- no real network I/O, no real Asterisk. This exercises the
client's own request-shaping, id-extraction, error-typing, and dispatch
logic exactly the way the controller will drive it.
"""

from __future__ import annotations

import json
from typing import Any

import aiohttp
import pytest

from klanker_voice.telephony.ari import AriClient, AriError

# --- Task 1: REST surface fakes --------------------------------------------


class _FakeResponse:
    """Stands in for ``aiohttp``'s request context manager."""

    def __init__(self, status: int = 200, json_body: dict[str, Any] | None = None) -> None:
        self.status = status
        self._json_body = json_body

    async def __aenter__(self) -> "_FakeResponse":
        return self

    async def __aexit__(self, *exc: object) -> bool:
        return False

    async def text(self) -> str:
        return "" if self._json_body is None else json.dumps(self._json_body)

    async def json(self) -> dict[str, Any]:
        return self._json_body or {}


class _FakeRestSession:
    """Stands in for ``aiohttp.ClientSession`` for the REST surface only --
    records every call, returns canned responses off a queue."""

    def __init__(self, responses: list[_FakeResponse]) -> None:
        self._responses = list(responses)
        self.calls: list[dict[str, Any]] = []

    def request(self, method: str, url: str, params: dict[str, Any] | None = None) -> _FakeResponse:
        self.calls.append({"method": method, "url": url, "params": params})
        return self._responses.pop(0)


def _client_with_fake_session(responses: list[_FakeResponse]) -> tuple[AriClient, _FakeRestSession]:
    client = AriClient(
        base_url="http://127.0.0.1:8088",
        username="klanker",
        password="s3cr3t-password",
        app_name="klanker",
    )
    fake = _FakeRestSession(responses)
    client._session = fake  # type: ignore[assignment]
    return client, fake


# --- Task 1: acceptance criteria -------------------------------------------


async def test_answer_posts_to_correct_path():
    client, fake = _client_with_fake_session([_FakeResponse(status=204)])
    await client.answer("chan-1")
    assert len(fake.calls) == 1
    call = fake.calls[0]
    assert call["method"] == "POST"
    assert call["url"] == "http://127.0.0.1:8088/ari/channels/chan-1/answer"


async def test_create_external_media_sends_only_supported_param_values_and_returns_id():
    client, fake = _client_with_fake_session(
        [_FakeResponse(status=200, json_body={"id": "ext-media-1"})]
    )
    channel_id = await client.create_external_media(
        app="klanker", external_host="host.docker.internal:40000", fmt="ulaw"
    )
    assert channel_id == "ext-media-1"
    call = fake.calls[0]
    assert call["method"] == "POST"
    assert call["url"] == "http://127.0.0.1:8088/ari/channels/externalMedia"
    params = call["params"]
    assert params["app"] == "klanker"
    assert params["external_host"] == "host.docker.internal:40000"
    assert params["format"] == "ulaw"
    assert params["encapsulation"] == "rtp"
    assert params["transport"] == "udp"
    assert params["connection_type"] == "client"
    assert params["direction"] == "both"


async def test_create_external_media_defaults_app_to_client_app_name():
    client, fake = _client_with_fake_session(
        [_FakeResponse(status=200, json_body={"id": "ext-media-2"})]
    )
    await client.create_external_media(external_host="127.0.0.1:40001")
    assert fake.calls[0]["params"]["app"] == "klanker"


async def test_create_bridge_posts_mixing_type_and_returns_id():
    client, fake = _client_with_fake_session([_FakeResponse(status=200, json_body={"id": "bridge-1"})])
    bridge_id = await client.create_bridge()
    assert bridge_id == "bridge-1"
    call = fake.calls[0]
    assert call["method"] == "POST"
    assert call["url"] == "http://127.0.0.1:8088/ari/bridges"
    assert call["params"]["type"] == "mixing"


async def test_add_channel_posts_bridge_and_channel_ids():
    client, fake = _client_with_fake_session([_FakeResponse(status=204)])
    await client.add_channel("bridge-1", "ext-media-1")
    call = fake.calls[0]
    assert call["method"] == "POST"
    assert call["url"] == "http://127.0.0.1:8088/ari/bridges/bridge-1/addChannel"
    assert call["params"]["channel"] == "ext-media-1"


async def test_hangup_deletes_channel():
    client, fake = _client_with_fake_session([_FakeResponse(status=204)])
    await client.hangup("chan-1")
    call = fake.calls[0]
    assert call["method"] == "DELETE"
    assert call["url"] == "http://127.0.0.1:8088/ari/channels/chan-1"


async def test_destroy_bridge_deletes_bridge():
    client, fake = _client_with_fake_session([_FakeResponse(status=204)])
    await client.destroy_bridge("bridge-1")
    call = fake.calls[0]
    assert call["method"] == "DELETE"
    assert call["url"] == "http://127.0.0.1:8088/ari/bridges/bridge-1"


async def test_non_2xx_response_raises_ari_error_without_leaking_password():
    client, _fake = _client_with_fake_session([_FakeResponse(status=500)])
    with pytest.raises(AriError) as exc_info:
        await client.hangup("chan-1")
    message = str(exc_info.value)
    assert "s3cr3t-password" not in message
    assert exc_info.value.status == 500
    assert exc_info.value.path == "/ari/channels/chan-1"


async def test_request_before_connect_raises_runtime_error():
    client = AriClient(
        base_url="http://127.0.0.1:8088",
        username="klanker",
        password="s3cr3t-password",
        app_name="klanker",
    )
    with pytest.raises(RuntimeError):
        await client.answer("chan-1")


# --- Task 2: events WebSocket dispatch fakes -------------------------------


class _FakeWSMessage:
    """Stands in for ``aiohttp.WSMessage`` -- ``.type`` is an
    ``aiohttp.WSMsgType`` member, ``.json()`` is synchronous (matches the
    real API)."""

    def __init__(self, msg_type: aiohttp.WSMsgType, data: Any = None) -> None:
        self.type = msg_type
        self._data = data

    def json(self) -> Any:
        return self._data


class _FakeWebSocket:
    """Stands in for the async-context-manager + async-iterable
    ``aiohttp.ClientWebSocketResponse`` that ``session.ws_connect(...)``
    yields."""

    def __init__(self, messages: list[_FakeWSMessage]) -> None:
        self._messages = list(messages)

    async def __aenter__(self) -> "_FakeWebSocket":
        return self

    async def __aexit__(self, *exc: object) -> bool:
        return False

    def __aiter__(self) -> "_FakeWebSocket":
        return self

    async def __anext__(self) -> _FakeWSMessage:
        if not self._messages:
            raise StopAsyncIteration
        return self._messages.pop(0)


class _FakeWsSession:
    """Stands in for ``aiohttp.ClientSession`` for the events-WS surface
    only -- records every ``ws_connect`` call, returns one canned fake WS."""

    def __init__(self, ws: _FakeWebSocket) -> None:
        self._ws = ws
        self.ws_connect_calls: list[dict[str, Any]] = []

    def ws_connect(self, url: str, params: dict[str, Any] | None = None) -> _FakeWebSocket:
        self.ws_connect_calls.append({"url": url, "params": params})
        return self._ws


def _client_with_fake_ws(messages: list[_FakeWSMessage]) -> tuple[AriClient, _FakeWsSession]:
    client = AriClient(
        base_url="http://127.0.0.1:8088",
        username="klanker",
        password="s3cr3t-password",
        app_name="klanker",
    )
    fake = _FakeWsSession(_FakeWebSocket(messages))
    client._session = fake  # type: ignore[assignment]
    return client, fake


# --- Task 2: acceptance criteria -------------------------------------------


async def test_run_connects_to_events_endpoint_with_app_and_subscribe_all():
    client, fake = _client_with_fake_ws([])
    await client.run()
    assert len(fake.ws_connect_calls) == 1
    call = fake.ws_connect_calls[0]
    assert call["url"] == "http://127.0.0.1:8088/ari/events"
    assert call["params"] == {"app": "klanker", "subscribeAll": "true"}


async def test_run_dispatches_registered_handlers_in_order_with_parsed_dicts():
    received: list[dict[str, Any]] = []

    async def on_stasis_start(event: dict[str, Any]) -> None:
        received.append(event)

    async def on_dtmf(event: dict[str, Any]) -> None:
        received.append(event)

    async def on_destroyed(event: dict[str, Any]) -> None:
        received.append(event)

    messages = [
        _FakeWSMessage(aiohttp.WSMsgType.TEXT, {"type": "StasisStart", "channel": {"id": "c1"}}),
        _FakeWSMessage(aiohttp.WSMsgType.TEXT, {"type": "ChannelDtmfReceived", "digit": "1"}),
        _FakeWSMessage(aiohttp.WSMsgType.TEXT, {"type": "ChannelDestroyed", "channel": {"id": "c1"}}),
    ]
    client, _fake = _client_with_fake_ws(messages)
    client.on("StasisStart", on_stasis_start)
    client.on("ChannelDtmfReceived", on_dtmf)
    client.on("ChannelDestroyed", on_destroyed)

    await client.run()

    assert [e["type"] for e in received] == [
        "StasisStart",
        "ChannelDtmfReceived",
        "ChannelDestroyed",
    ]
    assert received[1]["digit"] == "1"


async def test_unregistered_event_type_is_ignored_not_fatal():
    received: list[dict[str, Any]] = []

    async def on_destroyed(event: dict[str, Any]) -> None:
        received.append(event)

    messages = [
        _FakeWSMessage(aiohttp.WSMsgType.TEXT, {"type": "SomeUnhandledEvent"}),
        _FakeWSMessage(aiohttp.WSMsgType.TEXT, {"type": "ChannelDestroyed"}),
    ]
    client, _fake = _client_with_fake_ws(messages)
    client.on("ChannelDestroyed", on_destroyed)

    await client.run()  # must not raise

    assert len(received) == 1
    assert received[0]["type"] == "ChannelDestroyed"


async def test_handler_exception_is_caught_and_loop_continues_to_next_frame():
    received: list[str] = []

    async def failing_handler(event: dict[str, Any]) -> None:
        raise ValueError("boom")

    async def next_handler(event: dict[str, Any]) -> None:
        received.append(event["type"])

    messages = [
        _FakeWSMessage(aiohttp.WSMsgType.TEXT, {"type": "StasisStart"}),
        _FakeWSMessage(aiohttp.WSMsgType.TEXT, {"type": "ChannelDestroyed"}),
    ]
    client, _fake = _client_with_fake_ws(messages)
    client.on("StasisStart", failing_handler)
    client.on("ChannelDestroyed", next_handler)

    await client.run()  # must not raise despite the handler's ValueError

    assert received == ["ChannelDestroyed"]


async def test_ws_close_frame_ends_run_without_raising():
    messages = [
        _FakeWSMessage(aiohttp.WSMsgType.TEXT, {"type": "StasisStart"}),
        _FakeWSMessage(aiohttp.WSMsgType.CLOSE),
        # never reached -- run() must break out at the CLOSE frame above
        _FakeWSMessage(aiohttp.WSMsgType.TEXT, {"type": "ChannelDestroyed"}),
    ]
    received: list[str] = []

    async def handler(event: dict[str, Any]) -> None:
        received.append(event["type"])

    client, _fake = _client_with_fake_ws(messages)
    client.on("StasisStart", handler)
    client.on("ChannelDestroyed", handler)

    await client.run()  # must not raise

    assert received == ["StasisStart"]
