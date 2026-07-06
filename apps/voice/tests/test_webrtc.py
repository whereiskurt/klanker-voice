"""Public-IP + STUN ICE candidate gathering (D-12) — no network at import time."""

from __future__ import annotations

import pytest

from klanker_voice import webrtc

SAMPLE_TASK_METADATA = {
    "Cluster": "arn:aws:ecs:us-east-1:123456789012:cluster/app",
    "TaskARN": "arn:aws:ecs:us-east-1:123456789012:task/app/abc123",
    "Containers": [
        {
            "Name": "voice",
            "Networks": [
                {
                    "NetworkMode": "awsvpc",
                    "IPv4Addresses": ["10.0.1.234"],
                    "AttachmentIndex": 0,
                    "MACAddress": "0e:9e:32:0d:c1:e2",
                }
            ],
        }
    ],
}

SAMPLE_DESCRIBE_NETWORK_INTERFACES_RESPONSE = {
    "NetworkInterfaces": [
        {
            "NetworkInterfaceId": "eni-0123456789abcdef0",
            "MacAddress": "0e:9e:32:0d:c1:e2",
            "Association": {"PublicIp": "203.0.113.42"},
        }
    ]
}


class FakeEc2Client:
    def __init__(self, response: dict, *, raise_exc: Exception | None = None):
        self._response = response
        self._raise_exc = raise_exc
        self.calls: list[dict] = []

    def describe_network_interfaces(self, **kwargs):
        self.calls.append(kwargs)
        if self._raise_exc:
            raise self._raise_exc
        return self._response


def test_build_ice_servers_honors_kmv_stun_url(monkeypatch):
    monkeypatch.setenv(webrtc.STUN_URL_ENV_VAR, "stun:stun.example.com:3478")

    servers = webrtc.build_ice_servers()

    assert len(servers) == 1
    assert servers[0].urls == ["stun:stun.example.com:3478"]


def test_build_ice_servers_default_when_unset(monkeypatch):
    monkeypatch.delenv(webrtc.STUN_URL_ENV_VAR, raising=False)

    servers = webrtc.build_ice_servers()

    assert servers[0].urls == [webrtc.DEFAULT_STUN_URL]


def test_read_task_eni_public_ip_parses_sample_response(monkeypatch):
    monkeypatch.setattr(webrtc, "_fetch_task_metadata", lambda: SAMPLE_TASK_METADATA)
    fake_ec2 = FakeEc2Client(SAMPLE_DESCRIBE_NETWORK_INTERFACES_RESPONSE)
    monkeypatch.setattr(webrtc.boto3, "client", lambda service: fake_ec2)

    public_ip = webrtc._read_task_eni_public_ip()

    assert public_ip == "203.0.113.42"
    assert fake_ec2.calls == [
        {"Filters": [{"Name": "mac-address", "Values": ["0e:9e:32:0d:c1:e2"]}]}
    ]


def test_gather_public_candidates_includes_parsed_public_ip(monkeypatch):
    monkeypatch.setattr(webrtc, "_fetch_task_metadata", lambda: SAMPLE_TASK_METADATA)
    monkeypatch.setattr(
        webrtc.boto3, "client", lambda service: FakeEc2Client(SAMPLE_DESCRIBE_NETWORK_INTERFACES_RESPONSE)
    )

    result = webrtc.gather_public_candidates()

    assert result.public_ip == "203.0.113.42"
    assert len(result.ice_servers) == 1


def test_degrades_to_stun_only_when_metadata_env_var_absent(monkeypatch):
    monkeypatch.delenv(webrtc.METADATA_URI_ENV_VAR, raising=False)

    public_ip = webrtc._read_task_eni_public_ip()
    result = webrtc.gather_public_candidates()

    assert public_ip is None
    assert result.public_ip is None
    assert len(result.ice_servers) == 1  # STUN is still present


def test_degrades_to_none_when_metadata_has_no_mac(monkeypatch):
    monkeypatch.setattr(webrtc, "_fetch_task_metadata", lambda: {"Containers": []})

    assert webrtc._read_task_eni_public_ip() is None


def test_degrades_to_none_when_no_interfaces_found(monkeypatch):
    monkeypatch.setattr(webrtc, "_fetch_task_metadata", lambda: SAMPLE_TASK_METADATA)
    monkeypatch.setattr(
        webrtc.boto3, "client", lambda service: FakeEc2Client({"NetworkInterfaces": []})
    )

    assert webrtc._read_task_eni_public_ip() is None


def test_degrades_to_none_when_no_public_ip_association(monkeypatch):
    monkeypatch.setattr(webrtc, "_fetch_task_metadata", lambda: SAMPLE_TASK_METADATA)
    monkeypatch.setattr(
        webrtc.boto3,
        "client",
        lambda service: FakeEc2Client({"NetworkInterfaces": [{"Association": {}}]}),
    )

    assert webrtc._read_task_eni_public_ip() is None


def test_gather_public_candidates_never_raises_on_ec2_error(monkeypatch):
    monkeypatch.setattr(webrtc, "_fetch_task_metadata", lambda: SAMPLE_TASK_METADATA)
    monkeypatch.setattr(
        webrtc.boto3,
        "client",
        lambda service: FakeEc2Client({}, raise_exc=RuntimeError("boom: throttled")),
    )

    # Must not raise.
    result = webrtc.gather_public_candidates()

    assert result.public_ip is None
    assert len(result.ice_servers) == 1


def test_fetch_task_metadata_returns_none_when_env_absent(monkeypatch):
    monkeypatch.delenv(webrtc.METADATA_URI_ENV_VAR, raising=False)

    assert webrtc._fetch_task_metadata() is None


def test_fetch_task_metadata_degrades_on_http_error(monkeypatch):
    monkeypatch.setenv(webrtc.METADATA_URI_ENV_VAR, "http://169.254.170.2/v4/abc")

    def _raise(*args, **kwargs):
        raise OSError("connection refused")

    monkeypatch.setattr(webrtc.urllib.request, "urlopen", _raise)

    assert webrtc._fetch_task_metadata() is None


def test_inject_public_host_candidate_duplicates_host_line():
    sdp = (
        "v=0\r\n"
        "o=- 1 1 IN IP4 10.0.1.234\r\n"
        "a=candidate:1 1 UDP 2130706431 10.0.1.234 12345 typ host\r\n"
        "a=candidate:2 1 UDP 1694498815 203.0.113.9 12345 typ srflx raddr 10.0.1.234 rport 12345\r\n"
    )

    munged = webrtc.inject_public_host_candidate(sdp, "203.0.113.42")

    lines = munged.splitlines()
    host_lines = [l for l in lines if " typ host" in l]
    assert len(host_lines) == 2
    assert any("203.0.113.42" in l for l in host_lines)
    assert any("10.0.1.234" in l for l in host_lines)
    # The original srflx line is untouched.
    assert sum(1 for l in lines if " typ srflx" in l) == 1


def test_inject_public_host_candidate_is_noop_without_host_candidates():
    sdp = "v=0\r\no=- 1 1 IN IP4 10.0.1.234\r\n"

    assert webrtc.inject_public_host_candidate(sdp, "203.0.113.42") == sdp
