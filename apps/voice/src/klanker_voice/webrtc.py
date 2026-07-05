"""Public-IP + STUN ICE candidate gathering for the deployed Fargate task (D-12).

The deploy target puts each voice task in a public subnet with
``assign_public_ip=ENABLED`` — no ALB or TURN in the WebRTC *media* path, only
``/api/offer`` signaling rides the ALB. The task's ENI only knows its private
IP; Fargate's 1:1 NAT means that private IP is directly reachable from the
internet via the task's public IP, so the task self-advertises its own public
IP as a host ICE candidate rather than relying on a third party to discover
it. A public STUN server is gathered in parallel as a belt-and-suspenders
srflx candidate. No TURN (CLAUDE.md: "revisit only if mandatory").

Every lookup here degrades to ``None``/STUN-only on any failure — local dev
(no ECS task-metadata endpoint), a malformed metadata document, an unattached
ENI, or a transient EC2 API error must never raise out of
:func:`gather_public_candidates`. This module makes no network call at
import time.
"""

from __future__ import annotations

import json
import os
import urllib.request
from dataclasses import dataclass, field
from urllib.error import URLError

import boto3
from loguru import logger
from pipecat.transports.smallwebrtc.connection import IceServer

#: Set automatically by the ECS/Fargate agent inside every task; absent in
#: local/dev environments, which is how we detect "not running on ECS".
METADATA_URI_ENV_VAR = "ECS_CONTAINER_METADATA_URI_V4"

#: Public STUN URL used as the srflx backup candidate (D-12 belt-and-suspenders).
STUN_URL_ENV_VAR = "KMV_STUN_URL"
DEFAULT_STUN_URL = "stun:stun.l.google.com:19302"

_METADATA_FETCH_TIMEOUT_SECS = 2.0


@dataclass(frozen=True)
class PublicCandidates:
    """The result of one candidate-gathering pass.

    Attributes:
        public_ip: This task's self-discovered public IP, or ``None`` when
            unavailable (local dev, metadata read failure, EC2 API error) —
            callers degrade to STUN-only in that case.
        ice_servers: The ICE server list (currently: STUN) to hand to the
            WebRTC peer connection.
    """

    public_ip: str | None
    ice_servers: list[IceServer] = field(default_factory=list)


def _stun_url() -> str:
    return os.environ.get(STUN_URL_ENV_VAR, DEFAULT_STUN_URL)


def build_ice_servers() -> list[IceServer]:
    """Return the ICE server list (one STUN entry, sourced from KMV_STUN_URL)."""
    return [IceServer(urls=[_stun_url()])]


def _fetch_task_metadata() -> dict | None:
    """GET the ECS task-metadata v4 ``/task`` document, or None if unavailable."""
    base = os.environ.get(METADATA_URI_ENV_VAR)
    if not base:
        return None
    try:
        with urllib.request.urlopen(
            f"{base}/task", timeout=_METADATA_FETCH_TIMEOUT_SECS
        ) as resp:
            return json.loads(resp.read())
    except (URLError, TimeoutError, ValueError, OSError) as exc:
        logger.warning(f"ECS task-metadata fetch failed, degrading to STUN-only: {exc}")
        return None


def _extract_eni_mac(task_metadata: dict) -> str | None:
    """Pull the awsvpc ENI's MAC address out of a task-metadata document."""
    for container in task_metadata.get("Containers", []):
        for network in container.get("Networks", []):
            mac = network.get("MACAddress")
            if mac:
                return mac
    return None


def _read_task_eni_public_ip() -> str | None:
    """Resolve this task's self-advertised public IP (D-12), or None.

    Reads the ECS task-metadata v4 endpoint for the awsvpc ENI's MAC address,
    then resolves the ENI's ``Association.PublicIp`` via
    ``ec2:DescribeNetworkInterfaces`` filtered by that MAC (read-only; T-04-03
    accepts the narrow IAM surface this needs — no ENI id is exposed directly
    in task metadata, but the MAC is, and DescribeNetworkInterfaces accepts a
    ``mac-address`` filter). Any failure along the way returns ``None`` rather
    than raising, so the caller always has the STUN fallback.
    """
    task_metadata = _fetch_task_metadata()
    if task_metadata is None:
        return None

    mac = _extract_eni_mac(task_metadata)
    if not mac:
        logger.warning("ECS task-metadata had no ENI MAC address; degrading to STUN-only")
        return None

    try:
        ec2 = boto3.client("ec2")
        response = ec2.describe_network_interfaces(
            Filters=[{"Name": "mac-address", "Values": [mac]}]
        )
        interfaces = response.get("NetworkInterfaces", [])
        if not interfaces:
            logger.warning(f"No ENI found for MAC {mac}; degrading to STUN-only")
            return None
        association = interfaces[0].get("Association") or {}
        public_ip = association.get("PublicIp")
        if not public_ip:
            logger.warning(
                f"ENI for MAC {mac} has no public IP association; degrading to STUN-only"
            )
            return None
        return public_ip
    except Exception as exc:  # boto3/EC2 errors, credentials, throttling, etc.
        logger.warning(f"EC2 DescribeNetworkInterfaces failed, degrading to STUN-only: {exc}")
        return None


def gather_public_candidates() -> PublicCandidates:
    """Compose the self-advertised public IP (or None) with the STUN ICE servers.

    Never raises — a missing/failed public-IP lookup degrades to STUN-only,
    matching the local-dev and belt-and-suspenders design (D-12).
    """
    return PublicCandidates(public_ip=_read_task_eni_public_ip(), ice_servers=build_ice_servers())


def inject_public_host_candidate(sdp: str, public_ip: str) -> str:
    """Self-advertise ``public_ip`` as an additional host ICE candidate (D-12).

    aiortc only gathers a host candidate for the ENI's private IP; Fargate's
    1:1 NAT means that same address is reachable from the internet via the
    task's public IP, so this duplicates each ``typ host`` candidate line
    with the IP field swapped for ``public_ip`` and a distinct foundation, so
    the browser tries both. A no-op (returns ``sdp`` unchanged) if no host
    candidate line is present.
    """
    output_lines: list[str] = []
    extra_lines: list[str] = []
    for line in sdp.splitlines():
        output_lines.append(line)
        if line.startswith("a=candidate") and (" typ host" in line):
            parts = line.split()
            if len(parts) > 4:
                foundation, _, rest = parts[0].partition(":")
                parts[0] = f"{foundation}:{rest}pub"
                parts[4] = public_ip
                extra_lines.append(" ".join(parts))
    output_lines.extend(extra_lines)
    trailing = "\r\n" if sdp.endswith(("\r\n", "\n")) else ""
    return "\r\n".join(output_lines) + trailing
