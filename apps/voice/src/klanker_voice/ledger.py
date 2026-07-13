"""Buffered batch S3 ledger writer (LEDG-01/LEDG-02/LEDG-05): every conversation
turn — both the user's final STT text and the assistant's actually-spoken
reply — is written as one newline-JSON record to a private S3 bucket, batched
on a ~120s timer, a 50-record buffer, or session close — never a per-utterance
PUT (LEDG-02 storage rule).

Mirrors the repo's established AWS pattern (``quota.py``/``session.py``): sync
boto3 called via ``asyncio.to_thread`` — no async-AWS SDK, no second AWS
client library. The writer touches ONLY S3; it must never import a NoSQL
usage-table resource or reference the quota service's own table by name
(LEDG-05 — quota bookkeeping and transcripts never co-mingle, different
access patterns; see quota.py for that table).

``code_hash`` is a salted HMAC-SHA256 of the normalized access code, computed
ONCE at writer construction — the raw code and any ``anon:<code>:<uuid>``
subject string are never persisted in a record and never logged
(T-15-02-01). Flush failures are logged with a record COUNT and the S3 key
only, never ``text`` (T-15-02-02) — the same "never let side-effect I/O take
down a live conversation" posture as ``observers.py``'s artifact writer.

Wiring the tap into ``create_call_session`` and the SIGTERM shutdown drain
(:func:`flush_all`) is Plan 15-03's concern; this module is additive and
inert until a caller constructs a :class:`LedgerWriter`.
"""

from __future__ import annotations

import asyncio
import hashlib
import hmac
import json
import os
import time
from dataclasses import dataclass, field
from datetime import datetime, timezone

import boto3
from loguru import logger

#: Ledger bucket + salt env vars — mirrors quota.py's env-var-name constant
#: pattern (USAGE_TABLE_ENV_VAR / TIERS_TABLE_ENV_VAR).
LEDGER_BUCKET_ENV_VAR = "KMV_LEDGER_BUCKET"
DEFAULT_LEDGER_BUCKET = ""
LEDGER_SALT_ENV_VAR = "KMV_LEDGER_SALT"

#: Canonical field set for one ledger record, in insertion order — MUST equal
#: the Athena/Glue DDL column list Plan 15-04 declares (schema-drift guard,
#: RESEARCH Pitfall 6). Never reorder without updating both sides.
LEDGER_FIELDS = (
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

#: Buffer flush thresholds (LOCKED: ~2-5min guidance; N-record cap).
_FLUSH_INTERVAL_SECONDS = 120.0
_FLUSH_RECORD_THRESHOLD = 50
#: Bounded re-buffer cap on repeated flush failure (RESEARCH Pattern 4) — the
#: buffer drops its oldest records above this size rather than growing
#: unbounded across a sustained S3 outage.
_MAX_BUFFERED_RECORDS = 500


def _bucket() -> str:
    return os.environ.get(LEDGER_BUCKET_ENV_VAR, DEFAULT_LEDGER_BUCKET)


def _utc_date() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%d")


def _utc_hms() -> str:
    return datetime.now(timezone.utc).strftime("%H%M%S")


def _now_epoch() -> int:
    return int(time.time())


def code_hash(code: str | None) -> str | None:
    """Salted HMAC-SHA256 of the normalized access code.

    Normalization (strip + lower) mirrors the auth app's own AccessCode
    write-time ``normalizeCode``, so the same code always groups to the same
    hash regardless of how it was entered. Returns ``None`` when either the
    ``KMV_LEDGER_SALT`` env value or ``code`` is empty/unset — never raises,
    and the raw code is never returned or logged.
    """
    salt = os.environ.get(LEDGER_SALT_ENV_VAR, "")
    if not salt or not code:
        return None
    normalized = code.strip().lower()
    if not normalized:
        return None
    return hmac.new(salt.encode(), normalized.encode(), hashlib.sha256).hexdigest()


def parse_code_from_sub(sub: str | None) -> str | None:
    """Extract the code from an ``anon:<code>:<uuid>`` bypass/PSTN-mint
    subject. Returns ``None`` for any other subject shape (opaque Auth.js
    ids, the smoke-service sub, empty/missing) — never raises.
    """
    if not sub:
        return None
    parts = sub.split(":")
    if len(parts) == 3 and parts[0] == "anon":
        return parts[1] or None
    return None


#: Every live LedgerWriter registers itself here so server.py's SIGTERM
#: lifespan drain (Plan 15-03) can flush_all() before the task dies.
_ACTIVE_WRITERS: set["LedgerWriter"] = set()


@dataclass(eq=False)
class LedgerWriter:
    """Per-session buffered batch writer — one instance per call, constructed
    in ``create_call_session()`` (wired in Plan 15-03).

    ``code_hash`` is computed once, at construction, from ``code`` — the raw
    code is never stored on the instance after construction and never
    appears in a record. A writer constructed with ``enabled=False`` (the
    bypass/smoke seam, T-anti-pattern "ledgering bypass sessions") buffers
    and flushes nothing.

    Instances are compared/hashed by identity (``eq=False``) so they can
    live in the module-level :data:`_ACTIVE_WRITERS` registry.
    """

    session_id: str
    email: str | None = None
    code: str | None = None
    caller_id: str | None = None
    did: str | None = None
    tier_id: str | None = None
    channel: str = "webrtc"
    enabled: bool = True

    _buffer: list[dict] = field(default_factory=list, init=False, repr=False)
    _turn_seq: int = field(default=0, init=False, repr=False)
    _batch_seq: int = field(default=0, init=False, repr=False)
    _lock: asyncio.Lock = field(default_factory=asyncio.Lock, init=False, repr=False)
    _closed: bool = field(default=False, init=False, repr=False)
    _flush_task: "asyncio.Task | None" = field(default=None, init=False, repr=False)
    _code_hash: str | None = field(default=None, init=False, repr=False)

    def __post_init__(self) -> None:
        # Computed once, here — self.code is never read again after this.
        self._code_hash = code_hash(self.code)
        _ACTIVE_WRITERS.add(self)

    async def append(self, *, role: str, text: str, interrupted: bool = False) -> None:
        """Buffer one turn. No-op when ``enabled`` is False or already
        closed. Skips a record identical to the immediately previous append
        (same role, text, and second — Pitfall 1's
        ``on_assistant_turn_stopped`` double-fire dedupe). Kicks the ~120s
        flush timer on the first buffered append; auto-flushes once the
        buffer reaches :data:`_FLUSH_RECORD_THRESHOLD`.
        """
        if not self.enabled or self._closed:
            return
        now = _now_epoch()
        if self._buffer:
            prev = self._buffer[-1]
            if prev["role"] == role and prev["text"] == text and prev["ts"] == now:
                return
        self._turn_seq += 1
        record = {
            "role": role,
            "text": text,
            "email": self.email,
            "caller_id": self.caller_id,
            "did": self.did,
            "ts": now,
            "session_id": self.session_id,
            "turn_seq": self._turn_seq,
            "code_hash": self._code_hash,
            "tier_id": self.tier_id,
            "channel": self.channel,
            "interrupted": interrupted,
        }
        self._buffer.append(record)
        if self._flush_task is None:
            self._flush_task = asyncio.create_task(self._flush_loop())
        if len(self._buffer) >= _FLUSH_RECORD_THRESHOLD:
            await self.flush()

    async def _flush_loop(self) -> None:
        try:
            while True:
                await asyncio.sleep(_FLUSH_INTERVAL_SECONDS)
                await self.flush()
        except asyncio.CancelledError:
            raise

    async def flush(self) -> None:
        """Swap the buffer and PUT it as one newline-JSON object.

        On any failure, log a record COUNT and the S3 key only (never
        ``text``, never the raw code) and re-buffer the batch for the next
        attempt — ledger I/O must never take down a live conversation. The
        re-buffered total is capped at :data:`_MAX_BUFFERED_RECORDS`,
        dropping the oldest records above that during a sustained outage.
        """
        async with self._lock:
            batch, self._buffer = self._buffer, []
        if not batch:
            return
        key = (
            f"ledger/dt={_utc_date()}/"
            f"{_utc_hms()}Z-{self.session_id}-{self._batch_seq:04d}.jsonl"
        )
        body = ("\n".join(json.dumps(r, ensure_ascii=False) for r in batch) + "\n").encode()
        try:
            await asyncio.to_thread(self._put, key, body)
            self._batch_seq += 1
        except Exception as exc:  # never let ledger I/O take down a live conversation
            logger.error(f"ledger flush failed (count={len(batch)}, key={key}): {exc}")
            async with self._lock:
                self._buffer[:0] = batch
                overflow = len(self._buffer) - _MAX_BUFFERED_RECORDS
                if overflow > 0:
                    del self._buffer[:overflow]
                    logger.error(f"ledger buffer overflow: dropped {overflow} oldest records")

    def _put(self, key: str, body: bytes) -> None:
        boto3.client("s3").put_object(
            Bucket=_bucket(), Key=key, Body=body, ContentType="application/x-ndjson"
        )

    async def close(self) -> None:
        """Idempotent teardown: cancel the flush timer, final flush, and
        unregister from :data:`_ACTIVE_WRITERS`. Safe to call more than
        once — every call after the first is a no-op."""
        if self._closed:
            return
        self._closed = True
        if self._flush_task is not None:
            self._flush_task.cancel()
        await self.flush()
        _ACTIVE_WRITERS.discard(self)


async def flush_all(timeout: float = 10.0) -> None:
    """Shutdown drain (Plan 15-03's SIGTERM lifespan hook): close every
    active writer, bounded by ``timeout`` so a slow/hung flush never
    exceeds ECS's default 30s SIGTERM->SIGKILL window."""
    writers = list(_ACTIVE_WRITERS)
    if not writers:
        return
    try:
        await asyncio.wait_for(
            asyncio.gather(*(w.close() for w in writers), return_exceptions=True),
            timeout=timeout,
        )
    except asyncio.TimeoutError:
        logger.error(f"ledger flush_all timed out after {timeout}s ({len(writers)} writers)")
