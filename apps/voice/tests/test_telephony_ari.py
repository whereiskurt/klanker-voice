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
