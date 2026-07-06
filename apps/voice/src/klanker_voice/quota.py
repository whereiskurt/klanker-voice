"""Race-safe quota enforcement: DynamoDB conditional-write primitives and
typed rejection errors (QUOT-01/02, D-01..D-03, D-08).

The concurrency slot is a heartbeat lease (D-01): one item per active
session, keyed by user, renewed on every 15s tick, with an ``expiresAt`` TTL
so a crashed task's slot self-expires with no reaper process. This module
never calls ``DeleteItem`` or any transaction API — the deployed task role
(``infra/.../services/voice/service.hcl``) grants only ``GetItem``,
``PutItem``, ``UpdateItem``, and ``Query`` on ``kmv-voice-usage`` (least
privilege, T-04-13); a "release" is therefore an immediate-expiry
``UpdateItem``, and TTL is the actual backstop either way.

Key templates here MUST match ``apps/auth/webapp/src/entities/usage.ts``
byte-for-byte (the same kv<->webapp compat discipline established in Phase 3).

Concurrency-limit enforcement is a consistent read (count active leases)
immediately followed by a conditional write for the specific session's lease
item. With no ``TransactWriteItems`` permission available, this is not a
single atomic operation across "count" and "acquire" — but the write itself
is conditional (idempotent per session id, not a blind ``PutItem``), and the
race window is a single-digit-millisecond DynamoDB round trip against a
handful of concurrent sessions (design spec: ~5/task, autoscale 1->4). An
atomic per-user counter item was considered and rejected: it cannot
self-heal on a crashed task without a reaper, which would violate D-01's
explicit "no reaper process" requirement.

:func:`start_gate` is the ``/api/offer`` enforcement seam; :func:`record_tick`
is the 15s durability/accounting/rollup tick, called by
:class:`klanker_voice.session.SessionLifecycle`.
"""

from __future__ import annotations

import os
import time
import uuid
from dataclasses import dataclass
from datetime import datetime, timezone
from decimal import Decimal
from typing import TYPE_CHECKING, Any

import boto3
from boto3.dynamodb.conditions import Key
from botocore.exceptions import ClientError

if TYPE_CHECKING:
    from klanker_voice.auth import SessionIdentity

#: Voice service's own usage table (Phase 4). Least-privilege task-role IAM
#: grants only GetItem/PutItem/UpdateItem/Query on this table.
USAGE_TABLE_ENV_VAR = "KMV_USAGE_TABLE"
DEFAULT_USAGE_TABLE = "kmv-voice-usage"

#: Phase-3 tiers table (thin-token architecture: voice reads limits here,
#: not from the JWT, so editing a tier never requires re-issuing tokens).
#: NOTE (Known Gap, see 04-04-SUMMARY.md): the deployed voice task role's IAM
#: does not currently grant cross-table read access to kmv-auth-electro —
#: this is a follow-up infra change, not something this code-only plan can
#: fix (out of its files_modified scope).
TIERS_TABLE_ENV_VAR = "KMV_TIERS_TABLE"
DEFAULT_TIERS_TABLE = "kmv-auth-electro"

#: Local/dev/test only — points boto3 at dynamodb-local. Unset in production.
DYNAMODB_ENDPOINT_ENV_VAR = "KMV_DYNAMODB_ENDPOINT"

#: Matches auth.py's own default-tier constant (session_max=0 => no-access).
NO_ACCESS_TIER_ID = "no-access"

# --- typed rejection errors (D-11) ---

ERROR_NO_ACCESS = "no-access"
ERROR_CONCURRENCY_LIMIT = "concurrency-limit"
ERROR_DAILY_LIMIT = "daily-limit"
ERROR_SITE_PAUSED = "site-paused"
ERROR_AT_CAPACITY = "at-capacity"  # retryable (D-14)

_RETRYABLE_ERRORS = frozenset({ERROR_AT_CAPACITY})

_HTTP_STATUS_BY_ERROR = {
    ERROR_NO_ACCESS: 403,
    ERROR_CONCURRENCY_LIMIT: 403,
    ERROR_DAILY_LIMIT: 403,
    ERROR_SITE_PAUSED: 403,
    ERROR_AT_CAPACITY: 503,
}


class QuotaError(Exception):
    """A typed start-gate rejection (D-11): the Phase-5 client maps
    ``error_type`` to its own friendly page. Never carries token/claim
    material — ``message`` is a fixed, non-sensitive string per error type."""

    def __init__(self, error_type: str, message: str):
        if error_type not in _HTTP_STATUS_BY_ERROR:
            raise ValueError(f"unknown QuotaError error_type: {error_type!r}")
        self.error_type = error_type
        self.message = message
        self.http_status = _HTTP_STATUS_BY_ERROR[error_type]
        self.retryable = error_type in _RETRYABLE_ERRORS
        super().__init__(message)


@dataclass(frozen=True)
class Tier:
    """Session/period/concurrency limits, read from the Phase-3 tiers table."""

    tier_id: str
    session_max_seconds: int
    period_max_seconds: int
    max_concurrent: int


@dataclass(frozen=True)
class GateResult:
    """The outcome of a successful :func:`start_gate` call.

    ``tier`` is the resolved Tier (already read during the gate check) so
    callers building a :class:`klanker_voice.session.SessionLifecycle` never
    need a second ``read_tier`` round trip. For a bypass session, ``tier``
    is a zeroed placeholder — never consulted, since ``bypass_accounting``
    sessions skip the service timer and tick entirely.
    """

    session_id: str
    tier: Tier
    session_max_seconds: int
    remaining_daily_seconds: int
    bypass_accounting: bool


@dataclass(frozen=True)
class TickResult:
    """The outcome of a :func:`record_tick` call — hooks for session.py."""

    daily_exhausted: bool
    site_paused: bool


# --- table/env resolution ---


def _usage_table_name() -> str:
    return os.environ.get(USAGE_TABLE_ENV_VAR, DEFAULT_USAGE_TABLE)


def _tiers_table_name() -> str:
    return os.environ.get(TIERS_TABLE_ENV_VAR, DEFAULT_TIERS_TABLE)


def _dynamodb_resource():
    """A fresh boto3 DynamoDB resource.

    Deliberately not cached (unlike ``auth._jwk_client``): calls here are
    infrequent (session start + one per 15s tick), and tests swap
    ``KMV_DYNAMODB_ENDPOINT``/credentials per run — caching would leak a
    stale endpoint across tests.
    """
    kwargs: dict[str, Any] = {}
    endpoint = os.environ.get(DYNAMODB_ENDPOINT_ENV_VAR)
    if endpoint:
        kwargs["endpoint_url"] = endpoint
    return boto3.resource("dynamodb", **kwargs)


def _usage_table():
    return _dynamodb_resource().Table(_usage_table_name())


def _tiers_table():
    return _dynamodb_resource().Table(_tiers_table_name())


def _today() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%d")


def _now_epoch() -> int:
    return int(time.time())


# --- key templates (byte-compat with apps/auth/webapp/src/entities/usage.ts) ---


def _heartbeat_pk(user_id: str) -> str:
    return f"session#{user_id}"


def _heartbeat_sk(session_id: str) -> str:
    return f"heartbeat#{session_id}"


def _daily_pk(user_id: str) -> str:
    return f"user#{user_id}"


def _daily_sk(day: str) -> str:
    return f"day#{day}"


ROLLUP_PK = "rollup#"


def _rollup_sk(day: str) -> str:
    return f"day#{day}"


CONTROL_PK = "control#"
CONTROL_SK = "killswitch#"


# --- tier + control reads ---


def read_tier(tier_id: str) -> Tier:
    """Read tier limits from the Phase-3 tiers table (thin-token architecture,
    03-summary D-01): the JWT carries only ``tier_id``, this table is the
    single source of truth for the actual limits.

    An unknown/tampered tier id fails closed to the same shape as the
    ``no-access`` tier (``session_max_seconds=0``), not an exception — the
    caller's ``session_max_seconds <= 0`` check turns that into the
    ``no-access`` typed reject uniformly.
    """
    response = _tiers_table().get_item(Key={"pk": f"tier#{tier_id}", "sk": "tier#"})
    item = response.get("Item")
    if item is None:
        return Tier(tier_id=tier_id, session_max_seconds=0, period_max_seconds=0, max_concurrent=0)
    return Tier(
        tier_id=tier_id,
        session_max_seconds=int(item.get("sessionMaxSeconds", 0)),
        period_max_seconds=int(item.get("periodMaxSeconds", 0)),
        max_concurrent=int(item.get("maxConcurrent", 0)),
    )


def read_control_item() -> dict[str, Any]:
    """Read the kill-switch control item (D-08); defaults to disengaged when
    the item has never been written (fresh table / never tripped)."""
    response = _usage_table().get_item(Key={"pk": CONTROL_PK, "sk": CONTROL_SK})
    item = response.get("Item") or {}
    return {"engaged": bool(item.get("engaged", False)), "reason": item.get("reason")}


# --- heartbeat lease (D-01) ---


def count_active_heartbeats(user_id: str, *, now: int | None = None) -> int:
    """Count this user's non-expired heartbeat leases (their live concurrency).

    A strongly-consistent Query (``ConsistentRead=True``) so a just-acquired
    lease is always visible to the very next count check on the same task.
    """
    now = _now_epoch() if now is None else now
    response = _usage_table().query(
        KeyConditionExpression=Key("pk").eq(_heartbeat_pk(user_id)),
        ConsistentRead=True,
    )
    return sum(1 for item in response.get("Items", []) if int(item.get("expiresAt", 0)) > now)


def acquire_heartbeat(user_id: str, session_id: str, *, ttl_seconds: float, task_id: str = "") -> None:
    """Atomically create-or-renew this session's heartbeat lease (D-01).

    Conditional write: succeeds if the item doesn't exist yet, OR the
    existing lease has already expired (self-healing re-acquire of a
    crashed/stale session's slot). Raises
    ``botocore.exceptions.ClientError`` (``ConditionalCheckFailedException``)
    if a *live* lease already exists for this exact session id (a duplicate
    concurrent acquire attempt for the same session).
    """
    now = _now_epoch()
    _usage_table().put_item(
        Item={
            "pk": _heartbeat_pk(user_id),
            "sk": _heartbeat_sk(session_id),
            "expiresAt": now + int(ttl_seconds),
            "taskId": task_id,
            "acquiredAt": now,
        },
        ConditionExpression="attribute_not_exists(pk) OR expiresAt < :now",
        ExpressionAttributeValues={":now": now},
    )


def renew_heartbeat(user_id: str, session_id: str, *, ttl_seconds: float) -> None:
    """Renew an already-acquired lease's TTL (called every 15s tick)."""
    now = _now_epoch()
    _usage_table().update_item(
        Key={"pk": _heartbeat_pk(user_id), "sk": _heartbeat_sk(session_id)},
        UpdateExpression="SET expiresAt = :expires",
        ExpressionAttributeValues={":expires": now + int(ttl_seconds)},
    )


def release_heartbeat(user_id: str, session_id: str) -> None:
    """Best-effort clean release at graceful teardown.

    Sets ``expiresAt`` into the past (immediate logical expiry) rather than
    calling ``DeleteItem`` — the task role's IAM does not grant delete, and
    TTL cleanup is the backstop either way.
    """
    _usage_table().update_item(
        Key={"pk": _heartbeat_pk(user_id), "sk": _heartbeat_sk(session_id)},
        UpdateExpression="SET expiresAt = :expired",
        ExpressionAttributeValues={":expired": _now_epoch() - 1},
    )


# --- daily usage + remaining-time math (D-03 sub-floor) ---


def _read_daily_seconds_used(user_id: str, day: str) -> int:
    response = _usage_table().get_item(Key={"pk": _daily_pk(user_id), "sk": _daily_sk(day)})
    item = response.get("Item") or {}
    return int(item.get("secondsUsed", 0))


def remaining_daily_seconds(user_id: str, tier: Tier, *, day: str | None = None) -> int:
    """Remaining seconds in the tier's daily/period budget, clamped >= 0.

    A tier with ``period_max_seconds <= 0`` (no-access) always has zero
    remaining — callers should already have rejected on ``session_max`` in
    that case; this is a defensive floor, not the primary no-access gate.
    """
    if tier.period_max_seconds <= 0:
        return 0
    day = day or _today()
    used = _read_daily_seconds_used(user_id, day)
    return max(0, tier.period_max_seconds - used)


# --- rollup + auto-trip (D-09/D-10) ---


def _auto_trip(*, reason: str) -> bool:
    """Conditionally engage the kill-switch control item.

    Idempotent: a conditional write that only succeeds if the switch isn't
    already engaged. Returns True iff this call actually flipped it.
    """
    try:
        _usage_table().update_item(
            Key={"pk": CONTROL_PK, "sk": CONTROL_SK},
            UpdateExpression="SET engaged = :true, reason = :reason, updatedAt = :now",
            ConditionExpression="attribute_not_exists(engaged) OR engaged = :false",
            ExpressionAttributeValues={
                ":true": True,
                ":false": False,
                ":reason": reason,
                ":now": _now_epoch(),
            },
        )
        return True
    except ClientError as exc:
        if exc.response["Error"]["Code"] == "ConditionalCheckFailedException":
            return False
        raise


def record_tick(
    *,
    user_id: str,
    session_id: str,
    tier: Tier,
    delta_seconds: int,
    task_id: str,
    heartbeat_ttl_seconds: float,
    est_cost_per_second: float,
    auto_trip_ceiling_seconds: float,
    auto_trip_ceiling_dollars: float,
    is_first_tick: bool,
    day: str | None = None,
) -> TickResult:
    """The durability + accounting tick (D-02): renews the heartbeat,
    conditionally persists ``seconds_used`` to the daily item, adds the
    delta to the global rollup (D-10), and auto-trips the kill-switch (D-09)
    if the site-wide ceiling is crossed.

    ``day`` defaults to today (UTC) and exists as a parameter mainly so
    tests can isolate rollup state per run without polluting the shared
    "today" rollup item across repeated local test executions.
    """
    day = day or _today()
    renew_heartbeat(user_id, session_id, ttl_seconds=heartbeat_ttl_seconds)

    if delta_seconds > 0:
        daily_response = _usage_table().update_item(
            Key={"pk": _daily_pk(user_id), "sk": _daily_sk(day)},
            UpdateExpression=(
                "SET secondsUsed = if_not_exists(secondsUsed, :zero) + :delta, updatedAt = :now"
            ),
            ExpressionAttributeValues={":delta": delta_seconds, ":zero": 0, ":now": _now_epoch()},
            ReturnValues="UPDATED_NEW",
        )
        seconds_used_today = int(daily_response["Attributes"]["secondsUsed"])

        rollup_expr = (
            "SET totalSeconds = if_not_exists(totalSeconds, :zero) + :delta, "
            "estCost = if_not_exists(estCost, :zerof) + :cost, updatedAt = :now"
        )
        rollup_values: dict[str, Any] = {
            ":delta": delta_seconds,
            ":zero": 0,
            ":zerof": Decimal("0"),
            ":cost": Decimal(str(delta_seconds * est_cost_per_second)),
            ":now": _now_epoch(),
        }
        if is_first_tick:
            rollup_expr += ", sessionCount = if_not_exists(sessionCount, :zero) + :one"
            rollup_values[":one"] = 1
        rollup_response = _usage_table().update_item(
            Key={"pk": ROLLUP_PK, "sk": _rollup_sk(day)},
            UpdateExpression=rollup_expr,
            ExpressionAttributeValues=rollup_values,
            ReturnValues="UPDATED_NEW",
        )
        total_seconds_today = int(rollup_response["Attributes"]["totalSeconds"])
        est_cost_today = float(rollup_response["Attributes"]["estCost"])
    else:
        # Sub-interval tick (e.g. a very short test interval or the very
        # first tick firing before a full period elapsed): still renew the
        # lease (above), skip accounting entirely for a zero delta.
        seconds_used_today = _read_daily_seconds_used(user_id, day)
        rollup_item = _usage_table().get_item(Key={"pk": ROLLUP_PK, "sk": _rollup_sk(day)}).get("Item") or {}
        total_seconds_today = int(rollup_item.get("totalSeconds", 0))
        est_cost_today = float(rollup_item.get("estCost", 0))

    site_paused = False
    if total_seconds_today >= auto_trip_ceiling_seconds or est_cost_today >= auto_trip_ceiling_dollars:
        site_paused = _auto_trip(reason="auto-trip")

    remaining = max(0, tier.period_max_seconds - seconds_used_today) if tier.period_max_seconds > 0 else 0
    daily_exhausted = tier.period_max_seconds > 0 and remaining <= 0

    return TickResult(daily_exhausted=daily_exhausted, site_paused=site_paused)


# --- start gate (QUOT-01, D-03, D-11) ---


def start_gate(
    identity: "SessionIdentity",
    *,
    active_session_count: int,
    per_task_max_sessions: int,
    heartbeat_ttl_seconds: float,
    sub_floor_seconds: float,
    task_id: str = "",
) -> GateResult:
    """The ``/api/offer`` enforcement seam.

    Order: bypass short-circuit (D-15) -> site-paused (D-08) -> no-access ->
    at-capacity (retryable, D-14, per-task) -> concurrency-limit (D-01) ->
    daily-limit (D-03 sub-floor) -> acquire the heartbeat lease.

    A fresh ``session_id`` is minted here (not the WebRTC ``pc_id``, which
    doesn't exist until after this gate passes and the connection is
    negotiated) — the caller threads it through to the eventual
    ``SessionLifecycle``/heartbeat-tick calls for this session.

    Raises:
        QuotaError: one of the five typed rejections (D-11).
    """
    session_id = str(uuid.uuid4())

    if identity.bypass_accounting:
        placeholder_tier = Tier(
            tier_id=NO_ACCESS_TIER_ID, session_max_seconds=0, period_max_seconds=0, max_concurrent=0
        )
        return GateResult(
            session_id=session_id,
            tier=placeholder_tier,
            session_max_seconds=0,
            remaining_daily_seconds=0,
            bypass_accounting=True,
        )

    control = read_control_item()
    if control["engaged"]:
        raise QuotaError(ERROR_SITE_PAUSED, "Voice service is temporarily paused by the operator")

    tier = read_tier(identity.tier_id)
    if tier.session_max_seconds <= 0:
        raise QuotaError(ERROR_NO_ACCESS, "Your tier does not permit voice sessions")

    if active_session_count >= per_task_max_sessions:
        raise QuotaError(ERROR_AT_CAPACITY, "This task is at capacity; please retry shortly")

    if count_active_heartbeats(identity.sub) >= tier.max_concurrent:
        raise QuotaError(ERROR_CONCURRENCY_LIMIT, "You have reached your concurrent session limit")

    remaining = remaining_daily_seconds(identity.sub, tier)
    if remaining < sub_floor_seconds:
        raise QuotaError(ERROR_DAILY_LIMIT, "Daily usage limit reached; resets tomorrow")

    acquire_heartbeat(identity.sub, session_id, ttl_seconds=heartbeat_ttl_seconds, task_id=task_id)

    return GateResult(
        session_id=session_id,
        tier=tier,
        session_max_seconds=tier.session_max_seconds,
        remaining_daily_seconds=remaining,
        bypass_accounting=False,
    )
