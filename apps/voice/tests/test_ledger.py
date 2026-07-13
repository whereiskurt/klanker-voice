"""LedgerWriter contract: buffered batch S3 writer, code_hash, parse_code_from_sub
(LEDG-01/LEDG-05).

No real AWS: ``ledger.boto3`` is patched with a recording fake ``s3`` client (mirrors
``test_session.py``'s ``fake_aws`` fixture shape). Env isolation via autouse
``monkeypatch.setenv`` for ``KMV_LEDGER_BUCKET``/``KMV_LEDGER_SALT``.
"""

from __future__ import annotations

import json

import pytest

from klanker_voice import ledger

BUCKET = "kmv-ledger-test"
SALT = "test-salt-value"


class _FakeS3Client:
    """Records every ``put_object`` call; raises on demand."""

    def __init__(self):
        self.calls: list[dict] = []
        self._raise_next = False

    def raise_next(self) -> None:
        self._raise_next = True

    def put_object(self, **kwargs):
        if self._raise_next:
            self._raise_next = False
            raise RuntimeError("simulated S3 failure")
        self.calls.append(kwargs)
        return {}


@pytest.fixture(autouse=True)
def ledger_env(monkeypatch):
    monkeypatch.setenv(ledger.LEDGER_BUCKET_ENV_VAR, BUCKET)
    monkeypatch.setenv(ledger.LEDGER_SALT_ENV_VAR, SALT)


@pytest.fixture
def fake_s3(monkeypatch):
    """Patch ``ledger.boto3.client`` with a recording fake S3 client."""
    client = _FakeS3Client()

    def _client(name, *args, **kwargs):
        assert name == "s3"
        return client

    monkeypatch.setattr(ledger.boto3, "client", _client)
    return client


def _writer(**overrides) -> ledger.LedgerWriter:
    kwargs = dict(
        session_id="sess-1",
        email="user@example.com",
        code="ABC123",
        caller_id=None,
        did=None,
        tier_id="kph-tier",
        channel="webrtc",
        enabled=True,
    )
    kwargs.update(overrides)
    return ledger.LedgerWriter(**kwargs)


# --- canonical field set -----------------------------------------------------


def test_ledger_fields_is_the_pinned_canonical_tuple():
    assert ledger.LEDGER_FIELDS == (
        "role",
        "text",
        "email",
        "caller_id",
        "did",
        "ts",
        "session_id",
        "turn_seq",
        "code_hash",
        "tier_id",
        "channel",
        "interrupted",
    )


# --- turn_seq monotonicity -------------------------------------------------


async def test_turn_seq_monotonic_across_roles(fake_s3):
    writer = _writer()

    await writer.append(role="user", text="hello there")
    await writer.append(role="assistant", text="hi, how can I help?")

    assert writer._buffer[0]["turn_seq"] == 1
    assert writer._buffer[1]["turn_seq"] == 2


# --- flush key/body shape ---------------------------------------------------


async def test_flush_writes_one_put_object_with_expected_key_and_body(fake_s3):
    writer = _writer(session_id="sess-flush")
    await writer.append(role="user", text="turn one")
    await writer.append(role="assistant", text="turn two")

    await writer.flush()

    assert len(fake_s3.calls) == 1
    call = fake_s3.calls[0]
    assert call["Bucket"] == BUCKET
    key = call["Key"]
    assert key.startswith("ledger/dt=")
    assert "-sess-flush-" in key
    assert key.endswith(".jsonl")
    # ledger/dt=YYYY-MM-DD/HHMMSSZ-<session_id>-<NNNN>.jsonl
    import re

    assert re.match(
        r"^ledger/dt=\d{4}-\d{2}-\d{2}/\d{6}Z-sess-flush-\d{4}\.jsonl$", key
    ), key

    body = call["Body"].decode("utf-8")
    lines = body.strip("\n").split("\n")
    assert len(lines) == 2
    records = [json.loads(line) for line in lines]
    assert records[0]["role"] == "user"
    assert records[0]["text"] == "turn one"
    assert records[1]["role"] == "assistant"
    assert records[1]["text"] == "turn two"


# --- put failure keeps the batch buffered -----------------------------------


async def test_put_failure_keeps_batch_buffered_for_retry(fake_s3):
    writer = _writer(session_id="sess-retry")
    await writer.append(role="user", text="will retry")

    fake_s3.raise_next()
    await writer.flush()  # swallows the exception, re-buffers

    assert len(fake_s3.calls) == 0
    assert len(writer._buffer) == 1

    # Second flush attempt succeeds and delivers the same record.
    await writer.flush()

    assert len(fake_s3.calls) == 1
    body = fake_s3.calls[0]["Body"].decode("utf-8")
    assert json.loads(body.strip("\n"))["text"] == "will retry"


# --- 50-record auto-flush, close flush, idempotent close --------------------


async def test_fifty_records_triggers_flush_and_close_is_idempotent(fake_s3):
    writer = _writer(session_id="sess-batch")

    for i in range(50):
        await writer.append(role="user", text=f"utterance {i}")

    # 50 records should have triggered an automatic flush.
    assert len(fake_s3.calls) == 1
    assert len(writer._buffer) == 0

    await writer.append(role="assistant", text="one more after the flush")
    await writer.close()

    assert len(fake_s3.calls) == 2
    assert len(writer._buffer) == 0

    # A second close() must be a no-op — no additional flush.
    await writer.close()
    assert len(fake_s3.calls) == 2


# --- double-fire dedupe (Pitfall 1) -----------------------------------------


async def test_identical_consecutive_assistant_appends_collapse_to_one(fake_s3):
    writer = _writer(session_id="sess-dedupe")

    await writer.append(role="assistant", text="same reply")
    await writer.append(role="assistant", text="same reply")

    assert len(writer._buffer) == 1


# --- code_hash -----------------------------------------------------------


def test_code_hash_stable_and_normalizes_strip_lower():
    h1 = ledger.code_hash("ABC")
    h2 = ledger.code_hash(" abc ")

    assert h1 is not None
    assert h1 == h2


def test_code_hash_returns_none_when_salt_unset(monkeypatch):
    monkeypatch.delenv(ledger.LEDGER_SALT_ENV_VAR, raising=False)

    assert ledger.code_hash("ABC") is None


def test_code_hash_returns_none_when_code_empty():
    assert ledger.code_hash("") is None
    assert ledger.code_hash(None) is None


async def test_record_never_contains_raw_code(fake_s3):
    writer = _writer(session_id="sess-hash", code="SUPERSECRETCODE")
    await writer.append(role="user", text="hi")

    record = writer._buffer[0]
    assert "SUPERSECRETCODE" not in json.dumps(record)
    assert record["code_hash"] == ledger.code_hash("SUPERSECRETCODE")


# --- disabled writer no-op --------------------------------------------------


async def test_disabled_writer_appends_and_flushes_nothing(fake_s3):
    writer = _writer(session_id="sess-disabled", enabled=False)

    await writer.append(role="user", text="should not be recorded")
    await writer.flush()
    await writer.close()

    assert writer._buffer == []
    assert len(fake_s3.calls) == 0


# --- parse_code_from_sub -----------------------------------------------------


def test_parse_code_from_sub_extracts_code():
    assert ledger.parse_code_from_sub("anon:defcon34:uuid-1") == "defcon34"


def test_parse_code_from_sub_returns_none_for_opaque_sub():
    assert ledger.parse_code_from_sub("some-opaque-authjs-id") is None


async def test_anon_sub_never_written_verbatim_into_record(fake_s3):
    code = ledger.parse_code_from_sub("anon:defcon34:uuid-1")
    writer = _writer(session_id="sess-anon", code=code)
    await writer.append(role="user", text="hi")

    record = writer._buffer[0]
    assert "anon:" not in json.dumps(record)
