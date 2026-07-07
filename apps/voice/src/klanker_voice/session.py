"""Per-session lifecycle: the precise in-memory service timer (D-02
hard-stop), the 15s durability/accounting tick, ActiveSessions CloudWatch
metric emission, ECS task-scale-in protection (QUOT-02, D-02, D-10, D-13,
INFR-06), the D-04/D-05 spoken wind-down hooks, and the D-06/D-07 layered
idle-teardown (transport disconnect + reconnect grace, user-silence
watchdog, pipeline stall/error) — all funneled through a single idempotent
:meth:`SessionLifecycle.release` (QUOT-03, QUOT-05).

``on_warning``/``on_stop`` are callback hooks server.py builds once the real
pipeline (worker/context/transport) exists for a session — this module never
imports server.py or holds a transport reference itself, keeping the
coupling one-directional (server.py -> session.py).

Every AWS call in this module (CloudWatch, ECS, and — via
:mod:`klanker_voice.quota` — DynamoDB) runs off the asyncio event loop via
``asyncio.to_thread``, so a slow API call never blocks the other concurrent
sessions this task is also running.
"""

from __future__ import annotations

import asyncio
import json
import os
import time
import urllib.request
from dataclasses import dataclass, field
from typing import Awaitable, Callable
from urllib.error import URLError

import boto3
from loguru import logger

from pipecat.frames.frames import ErrorFrame, UserStartedSpeakingFrame
from pipecat.observers.base_observer import BaseObserver, FramePushed
from pipecat.processors.frame_processor import FrameDirection

from klanker_voice import quota
from klanker_voice.config import QuotaConfig

#: CloudWatch namespace/metric matching the deployed autoscaling policy's
#: custom_metric_target (infra/.../services/voice/service.hcl).
METRIC_NAMESPACE = "klanker-voice/ecs"
METRIC_NAME = "ActiveSessions"

#: ECS task-metadata v4 endpoint — set automatically inside every Fargate
#: task; absent in local/dev (same env var webrtc.py already keys off).
METADATA_URI_ENV_VAR = "ECS_CONTAINER_METADATA_URI_V4"
_METADATA_FETCH_TIMEOUT_SECS = 2.0

Callback = Callable[[], Awaitable[None]]

_active_session_count = 0


def active_session_count() -> int:
    """This task's current active-session count (D-14 per-task cap read)."""
    return _active_session_count


def _increment() -> int:
    global _active_session_count
    _active_session_count += 1
    return _active_session_count


def _decrement() -> int:
    global _active_session_count
    _active_session_count = max(0, _active_session_count - 1)
    return _active_session_count


async def _default_hard_stop() -> None:
    """Fallback warning/stop callback — 04-05 replaces this with the spoken
    wind-down. A no-op here still satisfies QUOT-02: the service timer fires
    regardless of what the callback does, and SessionLifecycle's caller
    (server.py) is expected to tear the transport down once ``on_stop``
    returns (04-05's concern; this plan proves the *timer* fires on time)."""
    return None


def _task_metadata_ids() -> tuple[str, str]:
    """Return ``(cluster, task_id)`` from ECS task metadata, or ``("", "")``
    outside ECS (local dev/test) — never raises."""
    base = os.environ.get(METADATA_URI_ENV_VAR)
    if not base:
        return "", ""
    try:
        with urllib.request.urlopen(f"{base}/task", timeout=_METADATA_FETCH_TIMEOUT_SECS) as resp:
            doc = json.loads(resp.read())
        arn = str(doc.get("TaskARN", ""))
        cluster = str(doc.get("Cluster", ""))
        task_id = arn.rsplit("/", 1)[-1] if arn else ""
        return cluster, task_id
    except (URLError, TimeoutError, ValueError, OSError) as exc:
        logger.warning(f"ECS task-metadata fetch failed (session.py): {exc}")
        return "", ""


@dataclass
class SessionLifecycle:
    """Owns one session's service timer, accounting tick, metric emission,
    and scale-in protection. Constructed after a successful
    ``quota.start_gate`` call; ``start()``/``stop()`` bracket the session."""

    user_id: str
    session_id: str
    tier: quota.Tier
    quota_config: QuotaConfig
    bypass_accounting: bool = False
    on_warning: Callback = field(default=_default_hard_stop)
    on_stop: Callback = field(default=_default_hard_stop)
    on_daily_exhausted: Callback | None = None
    #: Optional hook server.py sets to the real pipeline's hard-close (e.g.
    #: ``WorkerRunner.cancel``) — SessionLifecycle itself never holds a
    #: worker/transport reference (see module docstring), but every D-06
    #: idle-teardown layer needs to actually end the running pipeline, not
    #: just release the DB/metric bookkeeping. Called once, at the end of
    #: :meth:`release`, guarded by the same idempotency check.
    on_released: Callback | None = None
    clock: Callable[[], float] = field(default=time.time)
    #: Override for tests only — the real rollup/daily items always use
    #: today's date; a synthetic day isolates a test's auto-trip state from
    #: the shared local table's real "today" rollup item.
    day: str | None = None

    _tick_task: asyncio.Task | None = field(default=None, init=False, repr=False)
    _timer_task: asyncio.Task | None = field(default=None, init=False, repr=False)
    _watchdog_task: asyncio.Task | None = field(default=None, init=False, repr=False)
    _reconnect_task: asyncio.Task | None = field(default=None, init=False, repr=False)
    _last_tick_at: float = field(default=0.0, init=False, repr=False)
    _is_first_tick: bool = field(default=True, init=False, repr=False)
    _stopped: bool = field(default=False, init=False, repr=False)
    _wind_down_fired: bool = field(default=False, init=False, repr=False)
    #: 07-05 (D-06): stamped once, at construction time (via __post_init__),
    #: off the SAME `clock` this session's D-02 service timer already uses --
    #: the single source `remaining_seconds()` reads, never a second timer.
    _started_at: float = field(default=0.0, init=False, repr=False)

    def __post_init__(self) -> None:
        self._started_at = self.clock()

    def remaining_seconds(self) -> float | None:
        """D-06 time-aware pacing (07-05): seconds left before the D-02 hard
        stop, computed from this session's OWN existing clock/tier state --
        a synchronous read at call time, never a new timer or thread.

        Returns ``None`` for a bypass session (D-15: no real tier/session_max
        bound, never subject to the wall-clock cutoff) -- callers must treat
        ``None`` as "no pacing signal available", not "no time left".
        """
        if self.bypass_accounting:
            return None
        elapsed = self.clock() - self._started_at
        return max(0.0, self.tier.session_max_seconds - elapsed)

    async def start(self) -> None:
        """Increment the active-session count, emit the metric, acquire
        scale-in protection if this is the task's first session, and start
        the service timer + (unless bypassing) the accounting tick loop and
        the D-06 user-silence watchdog."""
        self._last_tick_at = self.clock()
        was_idle = active_session_count() == 0
        _increment()
        await asyncio.to_thread(self._emit_metric)
        if was_idle:
            await asyncio.to_thread(self._set_scale_in_protection, True)

        if not self.bypass_accounting:
            # A bypass (smoke/service credential) session has no real tier
            # bound (D-15 skips accounting entirely) — running the service
            # timer against a session_max of 0 would hard-stop it instantly.
            # Metric emission + scale-in protection above still apply: it's
            # still occupying task capacity. The idle-teardown layers below
            # are likewise skipped for the same reason a smoke session is
            # never subject to the wall-clock cutoff.
            self._tick_task = asyncio.create_task(self._tick_loop())
            self._timer_task = asyncio.create_task(self._service_timer())
            self._watchdog_task = asyncio.create_task(self._silence_watchdog())

    async def release(self) -> None:
        """The single idempotent teardown path every layer funnels through
        (D-02 wall-clock cutoff, and the D-06 three idle layers): cancels
        every pending timer/tick/watchdog task, decrements the active-session
        count, releases the heartbeat lease, emits the metric, and clears
        scale-in protection once no sessions remain.

        Safe to call concurrently or repeatedly — the guard check-and-set
        below is synchronous (no ``await`` in between), so only the first
        of any number of racing callers actually does anything; every other
        caller returns immediately. The heartbeat lease TTL is the backstop
        if this is somehow never called at all (a crashed task).
        """
        if self._stopped:
            return
        self._stopped = True
        for task in (self._tick_task, self._timer_task, self._watchdog_task, self._reconnect_task):
            if task is not None:
                task.cancel()
        is_last = _decrement() == 0
        if not self.bypass_accounting:
            await asyncio.to_thread(quota.release_heartbeat, self.user_id, self.session_id)
        await asyncio.to_thread(self._emit_metric)
        if is_last:
            await asyncio.to_thread(self._set_scale_in_protection, False)
        if self.on_released is not None:
            await self.on_released()

    async def stop(self) -> None:
        """Back-compat name for :meth:`release` — server.py's per-connection
        ``finally`` block calls this once the pipeline run ends, for any
        reason (normal wind-down hard-close, an idle-teardown layer ending
        the worker run, or an unhandled error)."""
        await self.release()

    async def _fire_wind_down(self) -> None:
        """Invoke ``on_stop`` exactly once, no matter which trigger reaches
        it first — the D-02 wall-clock cutoff and D-04's mid-session
        daily/period-exhaustion hook both route here so the identical spoken
        wind-down sequence never double-fires (e.g. exhaustion detected on
        one 15s tick moments before the service timer's own cutoff)."""
        if self._wind_down_fired:
            return
        self._wind_down_fired = True
        await self.on_stop()

    async def _service_timer(self) -> None:
        """D-02: the precise in-memory stop clock — warning at
        ``session_max - winddown_warning_seconds`` (D-04), stop at
        ``session_max``, independent of the 15s tick's own timing."""
        session_max = self.tier.session_max_seconds
        warning_at = max(0.0, session_max - self.quota_config.winddown_warning_seconds)
        try:
            await asyncio.sleep(warning_at)
            await self.on_warning()
            await asyncio.sleep(max(0.0, session_max - warning_at))
            await self._fire_wind_down()
        except asyncio.CancelledError:
            raise

    # --- D-06/D-07: layered idle teardown atop the D-02 wall-clock bound ---

    async def _silence_watchdog(self) -> None:
        """D-06 layer 2: no user speech for ``user_silence_timeout`` seconds
        -> release(). Started at session start and reset by every
        :meth:`on_user_speech` call."""
        try:
            await asyncio.sleep(self.quota_config.user_silence_timeout)
            await self.release()
        except asyncio.CancelledError:
            raise

    async def on_user_speech(self) -> None:
        """Reset the D-06 layer-2 silence watchdog — call on every real
        user-speech event (e.g. ``UserStartedSpeakingFrame``)."""
        if self._stopped:
            return
        if self._watchdog_task is not None:
            self._watchdog_task.cancel()
        self._watchdog_task = asyncio.create_task(self._silence_watchdog())

    async def _reconnect_grace(self) -> None:
        """D-07: give a dropped transport ``reconnect_grace_seconds`` to
        reconnect into this same session before tearing it down."""
        try:
            await asyncio.sleep(self.quota_config.reconnect_grace_seconds)
            await self.release()
        except asyncio.CancelledError:
            raise

    async def on_transport_disconnected(self) -> None:
        """D-06 layer 1 / D-07: start the reconnect-grace timer. Cancelled
        by :meth:`on_transport_reconnected` if the same session reconnects
        in time; otherwise :meth:`release` fires once the grace elapses."""
        if self._stopped:
            return
        if self._reconnect_task is not None:
            self._reconnect_task.cancel()
        self._reconnect_task = asyncio.create_task(self._reconnect_grace())

    async def on_transport_reconnected(self) -> None:
        """Cancel a pending reconnect-grace teardown — the client
        reconnected into this same session within the grace window."""
        if self._reconnect_task is not None:
            self._reconnect_task.cancel()
            self._reconnect_task = None

    async def on_pipeline_stall(self) -> None:
        """D-06 layer 3: a bot-speaking stall / unrecoverable STT-LLM-TTS
        pipeline error -> release() immediately (no grace — the pipeline
        itself is unhealthy)."""
        await self.release()

    async def _tick_loop(self) -> None:
        interval = self.quota_config.heartbeat_renew_interval
        try:
            while True:
                await asyncio.sleep(interval)
                await self._tick()
        except asyncio.CancelledError:
            raise

    async def _tick(self) -> None:
        now = self.clock()
        delta = max(0, int(now - self._last_tick_at))
        self._last_tick_at = now
        _, task_id = await asyncio.to_thread(_task_metadata_ids)

        result: quota.TickResult = await asyncio.to_thread(
            quota.record_tick,
            user_id=self.user_id,
            session_id=self.session_id,
            tier=self.tier,
            delta_seconds=delta,
            task_id=task_id,
            heartbeat_ttl_seconds=self.quota_config.heartbeat_ttl,
            est_cost_per_second=self.quota_config.est_cost_per_second,
            auto_trip_ceiling_seconds=self.quota_config.auto_trip_ceiling_seconds,
            auto_trip_ceiling_dollars=self.quota_config.auto_trip_ceiling_dollars,
            is_first_tick=self._is_first_tick,
            day=self.day,
        )
        self._is_first_tick = False

        if result.site_paused:
            logger.warning("Site-wide auto-trip ceiling crossed; kill-switch engaged")
        if result.daily_exhausted:
            logger.info(f"Mid-session daily exhaustion for user={self.user_id}; invoking wind-down")
            if self.on_daily_exhausted is not None:
                await self.on_daily_exhausted()
            else:
                # D-04: identical wind-down as the D-02 service-timer cutoff,
                # guarded so it never fires twice if both triggers race.
                await self._fire_wind_down()

    def _emit_metric(self) -> None:
        try:
            cloudwatch = boto3.client("cloudwatch")
            cloudwatch.put_metric_data(
                Namespace=METRIC_NAMESPACE,
                MetricData=[
                    {
                        "MetricName": METRIC_NAME,
                        "Value": float(active_session_count()),
                        "Unit": "Count",
                        "Dimensions": [{"Name": "Service", "Value": "voice"}],
                    }
                ],
            )
        except Exception as exc:  # never let metric publish failure break a session
            logger.warning(f"ActiveSessions metric publish failed: {exc}")

    def _set_scale_in_protection(self, enabled: bool) -> None:
        cluster, task_id = _task_metadata_ids()
        if not cluster or not task_id:
            logger.debug("No ECS task metadata (local dev); skipping scale-in protection call")
            return
        try:
            ecs = boto3.client("ecs")
            ecs.update_task_protection(cluster=cluster, tasks=[task_id], protectionEnabled=enabled)
        except Exception as exc:  # never let a protection-API hiccup break a session
            logger.warning(f"ECS task-scale-in-protection update failed: {exc}")


class TeardownObserver(BaseObserver):
    """D-06 layers 2 + 3, wired onto real pipecat frames: a
    ``UserStartedSpeakingFrame`` resets the silence watchdog (layer 2), and a
    fatal ``ErrorFrame`` (an unrecoverable STT/LLM/TTS pipeline error, or a
    bot-speaking stall surfaced the same way) tears the session down
    immediately (layer 3). Attach via
    ``build_worker(pipeline, observers=[TeardownObserver(lifecycle), ...])``
    — the same non-intrusive observer seam :class:`~klanker_voice.observers.
    LatencyReportObserver` already uses, so this needs no changes to the
    pipeline's processor graph.
    """

    def __init__(self, lifecycle: "SessionLifecycle", **kwargs):
        super().__init__(**kwargs)
        self._lifecycle = lifecycle

    async def on_push_frame(self, data: FramePushed) -> None:
        frame = data.frame
        if isinstance(frame, UserStartedSpeakingFrame) and data.direction == FrameDirection.DOWNSTREAM:
            await self._lifecycle.on_user_speech()
        elif isinstance(frame, ErrorFrame) and frame.fatal:
            await self._lifecycle.on_pipeline_stall()
