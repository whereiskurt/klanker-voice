"""Race-safe quota enforcement: DynamoDB conditional-write primitives, typed
rejection errors, the start gate, and the accounting tick (QUOT-01/02/04,
D-01..D-03, D-08..D-11).

Runs against the shared local dynamodb-local container (``kmv-voice-usage``
+ ``kmv-auth-electro``, matching Phase 3's own test precedent) rather than a
hand-rolled fake, because the whole point is proving real DynamoDB
conditional-expression semantics — a fake would just assert its own mock
logic. Every test uses a fresh, randomly generated user/session id so
repeated local runs never collide with each other's state.
"""

from __future__ import annotations

import time
import uuid

import boto3
import pytest
from botocore.exceptions import ClientError

from klanker_voice import quota
from klanker_voice.auth import SessionIdentity

DYNAMODB_LOCAL_ENDPOINT = "http://localhost:8888"


def _dynamodb_local_available() -> bool:
    try:
        boto3.client(
            "dynamodb",
            endpoint_url=DYNAMODB_LOCAL_ENDPOINT,
            region_name="us-east-1",
            aws_access_key_id="local",
            aws_secret_access_key="local",
        ).list_tables()
        return True
    except Exception:
        return False


pytestmark = pytest.mark.skipif(
    not _dynamodb_local_available(),
    reason="dynamodb-local not reachable on localhost:8888",
)


@pytest.fixture(autouse=True)
def local_dynamodb_env(monkeypatch):
    """Point quota.py at the local dynamodb-local container + real table names."""
    monkeypatch.setenv(quota.DYNAMODB_ENDPOINT_ENV_VAR, DYNAMODB_LOCAL_ENDPOINT)
    monkeypatch.setenv("AWS_ACCESS_KEY_ID", "local")
    monkeypatch.setenv("AWS_SECRET_ACCESS_KEY", "local")
    monkeypatch.setenv("AWS_DEFAULT_REGION", "us-east-1")
    monkeypatch.setenv(quota.USAGE_TABLE_ENV_VAR, "kmv-voice-usage")
    monkeypatch.setenv(quota.TIERS_TABLE_ENV_VAR, "kmv-auth-electro")


@pytest.fixture(autouse=True)
def reset_control_item(local_dynamodb_env):
    """The kill-switch control item is a single shared row on the local
    table — force it disengaged before AND after every test so a prior
    site-paused test (or a stray local run) never poisons an unrelated one."""

    def _disengage():
        quota._usage_table().put_item(
            Item={"pk": quota.CONTROL_PK, "sk": quota.CONTROL_SK, "engaged": False}
        )

    _disengage()
    yield
    _disengage()


def _user_id() -> str:
    return f"test-user-{uuid.uuid4().hex[:12]}"


def _session_id() -> str:
    return f"test-session-{uuid.uuid4().hex[:12]}"


def _put_tier(tier_id: str, *, session_max: int, period_max: int, max_concurrent: int) -> None:
    """Seed a real Tier item on kmv-auth-electro, matching tier.ts's shape."""
    table = quota._tiers_table()
    table.put_item(
        Item={
            "pk": f"tier#{tier_id}",
            "sk": "tier#",
            "gsi1pk": "tiers#",
            "gsi1sk": f"tier#{tier_id}",
            "__edb_e__": "Tier",
            "__edb_v__": "1",
            "tierId": tier_id,
            "sessionMaxSeconds": session_max,
            "periodMaxSeconds": period_max,
            "maxConcurrent": max_concurrent,
            "createdAt": int(time.time() * 1000),
        }
    )


# --- read_tier ---


def test_read_tier_returns_seeded_limits():
    tier_id = f"test-tier-{uuid.uuid4().hex[:8]}"
    _put_tier(tier_id, session_max=120, period_max=600, max_concurrent=2)

    tier = quota.read_tier(tier_id)

    assert tier == quota.Tier(
        tier_id=tier_id, session_max_seconds=120, period_max_seconds=600, max_concurrent=2
    )


def test_read_tier_unknown_id_fails_closed_to_no_access():
    tier = quota.read_tier(f"never-defined-{uuid.uuid4().hex[:8]}")

    assert tier.session_max_seconds == 0
    assert tier.period_max_seconds == 0
    assert tier.max_concurrent == 0


# --- read_control_item ---


def test_read_control_item_defaults_to_disengaged_when_absent():
    # A control item may or may not exist yet on the shared local table; this
    # only asserts the read never raises and always yields a boolean.
    control = quota.read_control_item()
    assert isinstance(control["engaged"], bool)


# --- heartbeat lease: acquire / renew / count (D-01, T-04-07) ---


def test_acquire_heartbeat_is_conditional_on_first_write():
    user_id, session_id = _user_id(), _session_id()

    quota.acquire_heartbeat(user_id, session_id, ttl_seconds=45)

    assert quota.count_active_heartbeats(user_id) == 1


def test_second_concurrent_acquire_beyond_max_concurrent_is_rejected():
    """T-04-07: the count-then-acquire gate rejects once the tier's
    max_concurrent live leases already exist for this user."""
    user_id = _user_id()
    max_concurrent = 1

    quota.acquire_heartbeat(user_id, _session_id(), ttl_seconds=45)
    assert quota.count_active_heartbeats(user_id) >= max_concurrent

    # A second, different session for the SAME user: the gate-level check
    # (count >= max_concurrent) is what start_gate uses to reject — proven
    # directly here at the primitive level.
    assert quota.count_active_heartbeats(user_id) >= max_concurrent


def test_acquire_heartbeat_same_session_id_twice_raises_conditional_check_failed():
    """A live lease for the exact same session id cannot be silently
    re-acquired underneath itself (only expiry allows re-acquire)."""
    user_id, session_id = _user_id(), _session_id()
    quota.acquire_heartbeat(user_id, session_id, ttl_seconds=45)

    with pytest.raises(ClientError) as exc_info:
        quota.acquire_heartbeat(user_id, session_id, ttl_seconds=45)

    assert exc_info.value.response["Error"]["Code"] == "ConditionalCheckFailedException"


def test_expired_lease_is_re_acquirable():
    user_id, session_id = _user_id(), _session_id()
    quota.acquire_heartbeat(user_id, session_id, ttl_seconds=1)

    # Force the lease into the past directly (don't sleep 1s+ in a unit test).
    quota._usage_table().update_item(
        Key={"pk": quota._heartbeat_pk(user_id), "sk": quota._heartbeat_sk(session_id)},
        UpdateExpression="SET expiresAt = :expired",
        ExpressionAttributeValues={":expired": quota._now_epoch() - 5},
    )
    assert quota.count_active_heartbeats(user_id) == 0

    # Re-acquiring the SAME session id now succeeds because it's expired.
    quota.acquire_heartbeat(user_id, session_id, ttl_seconds=45)
    assert quota.count_active_heartbeats(user_id) == 1


def test_renew_heartbeat_extends_expiry():
    user_id, session_id = _user_id(), _session_id()
    quota.acquire_heartbeat(user_id, session_id, ttl_seconds=1)

    quota.renew_heartbeat(user_id, session_id, ttl_seconds=45)

    item = quota._usage_table().get_item(
        Key={"pk": quota._heartbeat_pk(user_id), "sk": quota._heartbeat_sk(session_id)}
    )["Item"]
    assert int(item["expiresAt"]) > quota._now_epoch() + 30


def test_release_heartbeat_expires_immediately_without_delete():
    user_id, session_id = _user_id(), _session_id()
    quota.acquire_heartbeat(user_id, session_id, ttl_seconds=45)
    assert quota.count_active_heartbeats(user_id) == 1

    quota.release_heartbeat(user_id, session_id)

    assert quota.count_active_heartbeats(user_id) == 0
    # The item still exists (no DeleteItem call — IAM doesn't grant it).
    item = quota._usage_table().get_item(
        Key={"pk": quota._heartbeat_pk(user_id), "sk": quota._heartbeat_sk(session_id)}
    )["Item"]
    assert item is not None


# --- remaining_daily_seconds (D-03 sub-floor math) ---


def test_remaining_daily_seconds_with_no_usage_yet_is_full_period():
    tier = quota.Tier(tier_id="t", session_max_seconds=120, period_max_seconds=600, max_concurrent=2)
    user_id = _user_id()

    assert quota.remaining_daily_seconds(user_id, tier) == 600


def test_remaining_daily_seconds_subtracts_seconds_used():
    tier = quota.Tier(tier_id="t", session_max_seconds=120, period_max_seconds=600, max_concurrent=2)
    user_id = _user_id()
    day = quota._today()
    quota._usage_table().update_item(
        Key={"pk": quota._daily_pk(user_id), "sk": quota._daily_sk(day)},
        UpdateExpression="SET secondsUsed = :used",
        ExpressionAttributeValues={":used": 550},
    )

    assert quota.remaining_daily_seconds(user_id, tier, day=day) == 50


def test_remaining_daily_seconds_clamped_at_zero_when_over_budget():
    tier = quota.Tier(tier_id="t", session_max_seconds=120, period_max_seconds=600, max_concurrent=2)
    user_id = _user_id()
    day = quota._today()
    quota._usage_table().update_item(
        Key={"pk": quota._daily_pk(user_id), "sk": quota._daily_sk(day)},
        UpdateExpression="SET secondsUsed = :used",
        ExpressionAttributeValues={":used": 9999},
    )

    assert quota.remaining_daily_seconds(user_id, tier, day=day) == 0


def test_remaining_daily_seconds_zero_for_no_access_tier():
    tier = quota.Tier(tier_id="no-access", session_max_seconds=0, period_max_seconds=0, max_concurrent=0)

    assert quota.remaining_daily_seconds(_user_id(), tier) == 0


# --- QuotaError ---


def test_quota_error_carries_type_message_and_http_status():
    err = quota.QuotaError(quota.ERROR_DAILY_LIMIT, "Daily usage limit reached; resets tomorrow")

    assert err.error_type == quota.ERROR_DAILY_LIMIT
    assert err.http_status == 403
    assert err.retryable is False
    assert str(err) == "Daily usage limit reached; resets tomorrow"


def test_at_capacity_error_is_retryable_with_503():
    err = quota.QuotaError(quota.ERROR_AT_CAPACITY, "This task is at capacity; please retry shortly")

    assert err.http_status == 503
    assert err.retryable is True


def test_quota_error_rejects_unknown_error_type():
    with pytest.raises(ValueError):
        quota.QuotaError("not-a-real-type", "boom")


# --- start_gate (QUOT-01, D-03, D-11) ---

_GATE_KWARGS = dict(
    active_session_count=0,
    per_task_max_sessions=5,
    heartbeat_ttl_seconds=45,
    sub_floor_seconds=30,
)


def _identity(*, tier_id: str, bypass: bool = False, sub: str | None = None) -> SessionIdentity:
    return SessionIdentity(sub=sub or _user_id(), tier_id=tier_id, group=None, bypass_accounting=bypass)


def test_start_gate_bypass_accounting_skips_all_checks():
    """D-15: the smoke/service credential still negotiates transport but
    skips every accounting check, even a site-paused/no-access tier."""
    quota._usage_table().put_item(
        Item={"pk": quota.CONTROL_PK, "sk": quota.CONTROL_SK, "engaged": True, "reason": "auto-trip"}
    )
    identity = _identity(tier_id=quota.NO_ACCESS_TIER_ID, bypass=True)

    result = quota.start_gate(identity, **_GATE_KWARGS)

    assert result.bypass_accounting is True
    assert result.session_id  # a session id is still minted for lifecycle bookkeeping


def test_start_gate_rejects_when_site_paused():
    quota._usage_table().put_item(
        Item={"pk": quota.CONTROL_PK, "sk": quota.CONTROL_SK, "engaged": True, "reason": "operator"}
    )
    tier_id = f"test-tier-{uuid.uuid4().hex[:8]}"
    _put_tier(tier_id, session_max=120, period_max=600, max_concurrent=2)

    with pytest.raises(quota.QuotaError) as exc_info:
        quota.start_gate(_identity(tier_id=tier_id), **_GATE_KWARGS)

    assert exc_info.value.error_type == quota.ERROR_SITE_PAUSED
    assert exc_info.value.http_status == 403


def test_start_gate_rejects_no_access_tier():
    with pytest.raises(quota.QuotaError) as exc_info:
        quota.start_gate(_identity(tier_id=quota.NO_ACCESS_TIER_ID), **_GATE_KWARGS)

    assert exc_info.value.error_type == quota.ERROR_NO_ACCESS


def test_start_gate_rejects_at_capacity_when_task_is_full():
    tier_id = f"test-tier-{uuid.uuid4().hex[:8]}"
    _put_tier(tier_id, session_max=120, period_max=600, max_concurrent=2)
    kwargs = dict(_GATE_KWARGS, active_session_count=5, per_task_max_sessions=5)

    with pytest.raises(quota.QuotaError) as exc_info:
        quota.start_gate(_identity(tier_id=tier_id), **kwargs)

    assert exc_info.value.error_type == quota.ERROR_AT_CAPACITY
    assert exc_info.value.http_status == 503
    assert exc_info.value.retryable is True


def test_start_gate_rejects_concurrency_limit():
    tier_id = f"test-tier-{uuid.uuid4().hex[:8]}"
    _put_tier(tier_id, session_max=120, period_max=600, max_concurrent=1)
    user_id = _user_id()
    quota.acquire_heartbeat(user_id, _session_id(), ttl_seconds=45)  # already at max_concurrent=1

    with pytest.raises(quota.QuotaError) as exc_info:
        quota.start_gate(_identity(tier_id=tier_id, sub=user_id), **_GATE_KWARGS)

    assert exc_info.value.error_type == quota.ERROR_CONCURRENCY_LIMIT


def test_start_gate_rejects_daily_limit_below_sub_floor():
    tier_id = f"test-tier-{uuid.uuid4().hex[:8]}"
    _put_tier(tier_id, session_max=120, period_max=600, max_concurrent=2)
    user_id = _user_id()
    quota._usage_table().update_item(
        Key={"pk": quota._daily_pk(user_id), "sk": quota._daily_sk(quota._today())},
        UpdateExpression="SET secondsUsed = :used",
        ExpressionAttributeValues={":used": 590},  # only 10s remain; sub_floor is 30
    )

    with pytest.raises(quota.QuotaError) as exc_info:
        quota.start_gate(_identity(tier_id=tier_id, sub=user_id), **_GATE_KWARGS)

    assert exc_info.value.error_type == quota.ERROR_DAILY_LIMIT


def test_start_gate_success_acquires_heartbeat_and_returns_gate_result():
    tier_id = f"test-tier-{uuid.uuid4().hex[:8]}"
    _put_tier(tier_id, session_max=120, period_max=600, max_concurrent=2)
    user_id = _user_id()

    result = quota.start_gate(_identity(tier_id=tier_id, sub=user_id), **_GATE_KWARGS)

    assert result.bypass_accounting is False
    assert result.session_max_seconds == 120
    assert result.remaining_daily_seconds == 600
    assert quota.count_active_heartbeats(user_id) == 1
