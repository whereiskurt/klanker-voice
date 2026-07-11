# Phase 9: VoIP.ms Telephony — Call Runtime Extraction - Pattern Map

**Mapped:** 2026-07-11  
**Files analyzed:** 3 new/modified files + test patterns  
**Analogs found:** 5 / 5 (100%)

---

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `apps/voice/src/klanker_voice/call_runtime.py` | shared module | session-lifecycle | `apps/voice/server.py` (L149-277) | exact — extraction source |
| `apps/voice/server.py` | HTTP entrypoint | request-response | `apps/voice/server.py` (L328-441) | incremental refactor |
| `apps/voice/tests/test_call_runtime.py` | test | unit + integration | `apps/voice/tests/test_session.py`, `test_server.py` | role-match (existing fakes) |

---

## Pattern Assignments

### `apps/voice/src/klanker_voice/call_runtime.py` (shared module, session-lifecycle)

**Primary analog:** `apps/voice/server.py` lines 149–277 (`_run_session` + `_start_and_run_tracked_session`)

**Secondary analogs:**
- `apps/voice/src/klanker_voice/session.py` (SessionLifecycle pattern)
- `apps/voice/src/klanker_voice/pipeline.py` (build_pipeline + build_worker pattern)
- `apps/voice/console.py` (non-WebRTC session caller example)

---

#### Target API shape (spec §6, D-01)

```python
@dataclass
class CallSession:
    """Single live voice session around an arbitrary BaseTransport."""
    session_id: str
    worker: PipelineWorker
    lifecycle: SessionLifecycle

    async def run(self) -> None:
        """Run the session pipeline to completion (success, error, or cancellation)."""
        ...

    async def close(self, reason: str) -> None:
        """Idempotently close the session (single release path)."""
        ...


async def create_call_session(
    *,
    transport: BaseTransport,
    identity: CallIdentity,
    cfg: PipelineConfig,
    channel: Literal["webrtc", "pstn"],
    metadata: dict[str, str],
) -> CallSession:
    """Construct and return a CallSession, transport-neutral.
    
    Responsibilities (spec §6):
    1. Resolve caller identity
    2. Call the quota start gate
    3. Build ambience mixer (if compatible)
    4. Build the pipeline
    5. Create observers
    6. Create SessionLifecycle
    7. Wire warning and stop callbacks
    8. Wire transport disconnect handling
    9. Register greeting (if applicable)
    10. Return a session object with one idempotent close path
    """
    ...
```

---

#### Imports pattern

**Source:** `apps/voice/server.py` lines 22–70

```python
from __future__ import annotations

import asyncio
from dataclasses import dataclass
from typing import Any, Literal

from loguru import logger

from pipecat.processors.frameworks.rtvi import RTVIObserver
from pipecat.transports.base_transport import BaseTransport, TransportParams
from pipecat.workers.runner import WorkerRunner

from klanker_voice import quota, session, variants
from klanker_voice.config import (
    load_config,
    load_duplex_config,
    load_knowledge_config,
    load_quota_config,
    PipelineConfig,
    DuplexConfig,
)
from klanker_voice.observers import LatencyReportObserver
from klanker_voice.pipeline import (
    build_ambience_mixer,
    build_pipeline,
    build_worker,
    inject_warning_instruction,
    register_greet_first,
    speak_goodbye,
)
from klanker_voice.rtvi import build_rtvi_observer_params, build_rtvi_processor
from klanker_voice.session import SessionLifecycle, TeardownObserver
```

---

#### CallIdentity type (spec D-01, deferrable placeholder)

**Pattern from spec §11/§23 (deferred for now; use minimal placeholder if needed):**

```python
from dataclasses import dataclass

@dataclass(frozen=True)
class CallIdentity:
    """Minimal call identity for transport-neutral construction.
    
    Phase 12 (spec §23) will add phone → code → tier resolution.
    """
    subject: str
    authenticated: bool = False
    auth_method: str = "pstn-caller-id"  # For now; later phases refine
```

---

#### Core session construction pattern

**Source:** `apps/voice/server.py` lines 347–375 (`_connection_callback`)

**Key pattern: Create SessionLifecycle AFTER quota gate, wire callbacks BEFORE task spawn**

```python
# Inside the transport's connection callback or create_call_session:
lifecycle = SessionLifecycle(
    user_id=identity.subject,
    session_id=gate_result.session_id,
    tier=gate_result.tier,
    quota_config=quota_cfg,
    bypass_accounting=gate_result.bypass_accounting,
)

# Register lifecycle in the global session registry BEFORE spawning the run task
# (enables the immediate-release fast-path to find it if connection closes during lifecycle.start())
# In server.py: SESSIONS[connection.pc_id] = SessionRecord(...)
# In call_runtime.py: a similar keying mechanism, or return it from create_call_session

# Store the lifecycle so teardown can find it later
```

---

#### Pipeline and worker construction pattern

**Source:** `apps/voice/server.py` lines 169–217 (`_run_session`)

```python
async def _run_session(
    connection: SmallWebRTCConnection,
    lifecycle: SessionLifecycle,
    variant: str = variants.DEFAULT_VARIANT,
) -> None:
    """Build and run the pipeline over an established connection.
    
    **EXTRACT INTO CALL_RUNTIME.PY AS A SHARED FUNCTION.**
    This is the core: variant-aware config load, ambience mixer, pipeline/worker/observer wiring.
    """
    config_path = variants.variant_config_path(variant)
    cfg = load_config(config_path)
    knowledge_cfg = load_knowledge_config(config_path)
    duplex_cfg = load_duplex_config(config_path)
    quota_cfg = load_quota_config()  # global budget guardrail — never per-variant

    # Ambience mixer (greenhouse, 260710): per-session SoundfileMixer
    mixer = build_ambience_mixer(cfg)
    transport_params = (
        TransportParams(
            audio_in_enabled=True,
            audio_out_enabled=True,
            audio_out_sample_rate=cfg.greenhouse.ambience_sample_rate,
            audio_out_mixer=mixer,
        )
        if mixer is not None
        else TransportParams(audio_in_enabled=True, audio_out_enabled=True)
    )
    
    # Transport is passed in (BaseTransport) — transport-neutral seam
    # For WebRTC: transport = SmallWebRTCTransport(params=transport_params, webrtc_connection=connection)
    # For telephony: transport = TelephonyTransport(params=transport_params, ...)
    
    rtvi = build_rtvi_processor()
    built = build_pipeline(
        cfg,
        transport,
        rtvi=rtvi,
        knowledge_cfg=knowledge_cfg,
        duplex_cfg=duplex_cfg,
        remaining_seconds_fn=lifecycle.remaining_seconds,
    )
    
    worker = build_worker(
        built.pipeline,
        observers=[
            LatencyReportObserver(cfg),
            RTVIObserver(rtvi, params=build_rtvi_observer_params()),
            TeardownObserver(lifecycle),
        ],
    )
    
    # Greeting wiring (transport-agnostic)
    if cfg.persona.greet_first:
        register_greet_first(transport, worker, built.context)
    
    runner = WorkerRunner(handle_sigint=False)
    await runner.add_workers(worker)
    
    # --- Callback wiring (the crux of transport-neutral coupling) ---
    
    async def _on_warning() -> None:
        """D-04 natural warning: LLM-context instruction."""
        await inject_warning_instruction(worker, built.context, quota_cfg.warning_copy)

    async def _on_stop() -> None:
        """D-04/D-05: deterministic goodbye + hard close."""
        await speak_goodbye(worker, quota_cfg.goodbye_copy)
        await asyncio.sleep(quota_cfg.goodbye_grace_seconds)
        await runner.cancel("session wind-down complete")

    lifecycle.on_warning = _on_warning
    lifecycle.on_stop = _on_stop
    # D-06 layer 3 fallback: once release() fires, cancel the runner (end the pipeline)
    lifecycle.on_released = runner.cancel
    
    # --- Transport event wiring (transport-agnostic handler registration) ---
    
    @transport.event_handler("on_client_disconnected")
    async def _on_client_disconnected(transport, client):  # noqa: ANN001
        await lifecycle.on_transport_disconnected()

    @transport.event_handler("on_client_connected")
    async def _on_client_reconnected(transport, client):  # noqa: ANN001
        await lifecycle.on_transport_reconnected()

    await runner.run()
```

---

#### Session lifecycle start/run/stop pattern

**Source:** `apps/voice/server.py` lines 259–277 (`_start_and_run_tracked_session`)

```python
async def _start_and_run_tracked_session(
    connection: SmallWebRTCConnection,
    lifecycle: SessionLifecycle,
    variant: str = variants.DEFAULT_VARIANT,
) -> None:
    """Start the session lifecycle (metric + scale-in protection + service timer),
    run the pipeline, then always stop on exit (success, error, or cancellation).
    
    **EXTRACT INTO CALL_RUNTIME.PY.**
    This is what actually brackets the entire session:
    - lifecycle.start() — increments active session count, emits metric, starts timers
    - _run_session(...) — builds and runs the pipeline
    - lifecycle.stop() — releases the heartbeat lease and slot in the finally block
    """
    await lifecycle.start()
    try:
        await _run_session(connection, lifecycle, variant)
    finally:
        await lifecycle.stop()
```

---

#### Idempotent close pattern

**Source:** `apps/voice/src/klanker_voice/session.py` lines 207–234

```python
async def release(self) -> None:
    """The single idempotent teardown path (QUOT-03, QUOT-05).
    
    Safe to call concurrently or repeatedly — the guard check-and-set below
    is synchronous (no await in between), so only the first caller does
    anything; every other caller returns immediately.
    """
    if self._stopped:
        return
    self._stopped = True
    for task in (self._tick_task, self._timer_task, self._watchdog_task, self._reconnect_task):
        if task is not None:
            task.cancel()
    _decrement()
    if not self.bypass_accounting:
        await asyncio.to_thread(quota.release_heartbeat, self.user_id, self.session_id)
    await asyncio.to_thread(self._emit_metric)
    await asyncio.to_thread(self._reconcile_scale_in_protection)
    if self.on_released is not None:
        await self.on_released()
```

**Pattern for `CallSession.close()`:**

```python
async def close(self, reason: str) -> None:
    """Idempotently close the session — delegates to lifecycle.release()."""
    logger.info(f"Closing session {self.session_id}: {reason}")
    await self.lifecycle.release()
```

---

#### Callback wiring pattern (no transport reference held by lifecycle)

**Source:** `apps/voice/server.py` lines 214–246

**Key principle:** SessionLifecycle never imports server.py or holds a transport reference (one-directional coupling). Callbacks are set by the caller AFTER the lifecycle is constructed but BEFORE it starts.

```python
# SessionLifecycle fields (from session.py lines 122–131):
on_warning: Callback = field(default=_default_hard_stop)
on_stop: Callback = field(default=_default_hard_stop)
on_daily_exhausted: Callback | None = None
on_released: Callback | None = None

# Wiring pattern (server.py L234–242):
lifecycle.on_warning = _on_warning          # natural warning (LLM context instruction)
lifecycle.on_stop = _on_stop                # spoken goodbye + hard close
lifecycle.on_released = runner.cancel       # end the pipeline once release() fires
```

---

#### Transport disconnect/reconnect event pattern (transport-agnostic)

**Source:** `apps/voice/server.py` lines 244–254

**Pattern: Register handlers on the transport object, call lifecycle hooks**

```python
@transport.event_handler("on_client_disconnected")
async def _on_client_disconnected(transport, client):
    await lifecycle.on_transport_disconnected()

@transport.event_handler("on_client_connected")
async def _on_client_reconnected(transport, client):
    await lifecycle.on_transport_reconnected()
```

This pattern works identically for WebRTC and telephony — the transport's event contract is the seam.

---

#### Return type pattern

**Source:** `apps/voice/src/klanker_voice/pipeline.py` lines 43–58 (BuiltPipeline example)

```python
@dataclass
class CallSession:
    """Owned session object returned from create_call_session()."""
    session_id: str
    worker: PipelineWorker
    lifecycle: SessionLifecycle

    async def run(self) -> None:
        """Run the session pipeline to completion."""
        # Delegates to the runner that was constructed internally
        # The runner is held implicitly via worker/lifecycle/callbacks
        # This is what the caller awaits: await session.run()
        ...

    async def close(self, reason: str) -> None:
        """Idempotently close the session."""
        await self.lifecycle.release()
```

---

### `apps/voice/server.py` (HTTP entrypoint, incremental refactor)

**Analog:** `apps/voice/server.py` itself (L328–441, `_negotiate_webrtc` + `offer`)

**Changes required:**

1. Keep WebRTC-specific functions (`_negotiate_webrtc`, `_connection_callback`, `_wire_connection_teardown`, `_negotiate_webrtc`, `_extract_bearer_token`, `/api/offer` and `/api/offer` PATCH routes) **exactly as they are** — they are NOT extracted.

2. Inside `_negotiate_webrtc`'s `_connection_callback`, replace the inline pipeline/worker construction with a call to `create_call_session()`:

**Before (inline construction, to be replaced):**

```python
async def _connection_callback(connection: SmallWebRTCConnection) -> None:
    lifecycle = SessionLifecycle(...)
    SESSIONS[connection.pc_id] = SessionRecord(...)
    _wire_connection_teardown(connection, lifecycle)
    task = asyncio.create_task(_start_and_run_tracked_session(connection, lifecycle, variant))
    SESSION_TASKS[pc_id] = task
    task.add_done_callback(_on_session_task_done)
```

**After (using call_runtime):**

```python
from klanker_voice.call_runtime import create_call_session, CallIdentity

async def _connection_callback(connection: SmallWebRTCConnection) -> None:
    # WebRTC-specific: create the transport
    transport = SmallWebRTCTransport(
        params=_WEBRTC_TRANSPORT_PARAMS,
        webrtc_connection=connection,
    )
    
    # Transport-neutral: create the session
    call_identity = CallIdentity(subject=identity.sub, authenticated=True)
    call_session = await create_call_session(
        transport=transport,
        identity=call_identity,
        cfg=cfg,  # loaded once during start_gate, can be cached
        channel="webrtc",
        metadata={"pc_id": connection.pc_id},
    )
    
    # WebRTC-specific: register and track
    SESSIONS[connection.pc_id] = SessionRecord(
        identity=identity,
        gate_result=gate_result,
        lifecycle=call_session.lifecycle,
    )
    _wire_connection_teardown(connection, call_session.lifecycle)
    
    # Transport-neutral: spawn the session runner task
    pc_id = connection.pc_id
    task = asyncio.create_task(call_session.run())
    SESSION_TASKS[pc_id] = task
    
    def _on_session_task_done(_task):
        SESSION_TASKS.pop(pc_id, None)
        SESSIONS.pop(pc_id, None)
    
    task.add_done_callback(_on_session_task_done)
```

3. **Do NOT move `_wire_connection_teardown()`** — it is WebRTC-specific (aiortc `pc.closed` event, reconnect-grace semantics). It stays in server.py. The spec's note (D-03, §5) is explicit: "Preserve them EXACTLY where they are."

4. The `/api/offer` route itself (L407–441) **stays unchanged** — only the internal `_negotiate_webrtc` → `_connection_callback` flow gains the `create_call_session()` call.

---

#### Variant-aware config loading

**Source:** `apps/voice/server.py` lines 168–172

**Pattern for call_runtime to accept variant:**

```python
# In create_call_session():
async def create_call_session(
    *,
    transport: BaseTransport,
    identity: CallIdentity,
    cfg: PipelineConfig,  # Pre-loaded config (with variant already selected)
    channel: Literal["webrtc", "pstn"],
    metadata: dict[str, str],
) -> CallSession:
    """The cfg is pre-selected by the caller, not loaded inside call_runtime.
    
    This keeps the variant selection at the HTTP layer (server.py)
    and makes call_runtime transport/variant-independent.
    """
    # cfg is already variant-aware (caller passed it)
    # No need to call variants.variant_config_path(...) here
```

---

### `apps/voice/tests/test_call_runtime.py` (unit + integration tests)

**Primary analogs:**
- `apps/voice/tests/test_session.py` (SessionLifecycle unit tests, fake AWS pattern)
- `apps/voice/tests/test_server.py` (auth/start_gate/transport wiring pattern)
- `apps/voice/tests/conftest.py` (shared fixtures: make_config_file, stub_provider_keys, fake_aws)

---

#### Test structure pattern

**Source:** `apps/voice/tests/conftest.py` and `test_session.py`

```python
"""Test plan D-07: transport-neutral CallSession construction, idempotency, 
worker/transport termination.

Uses real asyncio event loop with tiny intervals. AWS calls (CloudWatch, ECS)
are faked via recording stub. Quota calls hit dynamodb-local when relevant."""

import asyncio
from unittest.mock import AsyncMock

import pytest

from pipecat.transports.base_transport import BaseTransport, TransportParams
from klanker_voice.call_runtime import (
    CallSession,
    CallIdentity,
    create_call_session,
)
from klanker_voice.session import SessionLifecycle
from klanker_voice import quota, session


@pytest.fixture
def fake_transport() -> BaseTransport:
    """Stub BaseTransport for testing — no aiortc, no real media."""
    class FakeTransport(BaseTransport):
        def __init__(self):
            super().__init__(params=TransportParams(audio_in_enabled=True, audio_out_enabled=True))
            self._event_handlers = {}
        
        def event_handler(self, event_name):
            def register(handler):
                self._event_handlers.setdefault(event_name, []).append(handler)
                return handler
            return register
        
        def input(self):
            # Return a no-op processor that the pipeline can chain
            from pipecat.processors.frame_processor import FrameProcessor
            return FrameProcessor()
        
        def output(self):
            # Return a no-op processor
            from pipecat.processors.frame_processor import FrameProcessor
            return FrameProcessor()
        
        async def start(self):
            pass
        
        async def stop(self):
            pass
    
    return FakeTransport()


async def test_create_call_session_returns_callable_session(fake_transport, make_config_file, stub_provider_keys):
    """A CallSession is constructed with session_id, worker, lifecycle."""
    cfg = make_config_file()
    identity = CallIdentity(subject="test-user", authenticated=True)
    
    # Mock the quota gate
    gate_result = quota.GateResult(
        session_id="test-sess-1",
        tier=quota.Tier(tier_id="t", session_max_seconds=60, period_max_seconds=600, max_concurrent=1),
        session_max_seconds=60,
        remaining_daily_seconds=600,
        bypass_accounting=False,
    )
    
    # create_call_session must NOT await; it returns a CallSession ready to run
    call_session = await create_call_session(
        transport=fake_transport,
        identity=identity,
        cfg=cfg,
        channel="webrtc",
        metadata={},
    )
    
    assert isinstance(call_session, CallSession)
    assert call_session.session_id == gate_result.session_id
    assert isinstance(call_session.lifecycle, SessionLifecycle)
    assert call_session.worker is not None


async def test_call_session_close_is_idempotent(fake_transport, make_config_file, stub_provider_keys, fake_aws):
    """Calling close() multiple times is safe — release() fires exactly once."""
    call_session = await create_call_session(...)
    
    # Multiple close calls
    await call_session.close("first close")
    await call_session.close("second close")
    
    # Both should be no-ops; the lifecycle's _stopped guard ensures only the first does work
    # Inspect the fake AWS client to verify metric emitted only once
    assert call_session.lifecycle._stopped is True


async def test_lifecycle_release_on_worker_termination(fake_transport, make_config_file, stub_provider_keys, fake_aws):
    """When the worker ends (normal completion, error, or cancellation),
    lifecycle.release() fires (via on_released callback)."""
    call_session = await create_call_session(...)
    
    # The callback wiring is:
    # lifecycle.on_released = runner.cancel
    # So when release() is called, the runner is cancelled
    # For this test, we verify the callback is set and fires
    
    release_called = False
    async def recording_on_released():
        nonlocal release_called
        release_called = True
    
    call_session.lifecycle.on_released = recording_on_released
    await call_session.close("test close")
    
    assert release_called is True


async def test_transport_disconnect_triggers_lifecycle_hooks(fake_transport, make_config_file, stub_provider_keys):
    """When the transport fires on_client_disconnected, lifecycle hooks fire."""
    call_session = await create_call_session(...)
    
    disconnect_called = False
    async def on_disconnect_handler():
        nonlocal disconnect_called
        disconnect_called = True
    
    call_session.lifecycle.on_transport_disconnected = on_disconnect_handler
    
    # Simulate the transport event
    if hasattr(fake_transport, '_event_handlers'):
        handlers = fake_transport._event_handlers.get("on_client_disconnected", [])
        for handler in handlers:
            await handler(fake_transport, None)
    
    assert disconnect_called is True
```

---

#### Existing test fakes to reuse

**From conftest.py:**

```python
# Fixture: stub_provider_keys — sets dummy API keys so factories work without live creds
# Fixture: make_config_file — writes a minimal valid pipeline.toml to tmp_path
# Fixture: _reset_scale_in_protection_state — resets module-global state between tests

# From test_session.py:
class _FakeAwsClient:
    """Records every call made to it; every method just returns None."""
    def __init__(self):
        self.calls: list[tuple[str, dict]] = []
    
    def __getattr__(self, name):
        def _record(**kwargs):
            self.calls.append((name, kwargs))
            return {}
        return _record

@pytest.fixture
def fake_aws(monkeypatch):
    """Fake boto3.client(...) for cloudwatch/ecs; records every call."""
    clients: dict[str, _FakeAwsClient] = {}
    
    def _client(name, *args, **kwargs):
        clients.setdefault(name, _FakeAwsClient())
        return clients[name]
    
    monkeypatch.setattr(session.boto3, "client", _client)
    monkeypatch.setattr(session, "_task_metadata_ids", lambda: ("test-cluster", "test-task-123"))
    return clients
```

---

## Shared Patterns

### 1. SessionLifecycle idempotent release

**Source:** `apps/voice/src/klanker_voice/session.py` lines 207–234

**Apply to:** All code that tears down a session (server.py, call_runtime.py, any telephony controller)

```python
# Pattern: always call release() exactly once per session
# SessionLifecycle ensures:
# - _stopped guard makes multiple calls safe
# - all timer tasks are cancelled
# - heartbeat lease is freed
# - metric is emitted
# - scale-in protection is cleared
# - on_released callback is invoked

# Callers must:
# 1. Create the lifecycle after quota gate
# 2. Set on_released callback BEFORE starting
# 3. Call release() in finally block OR via an event handler
# 4. Never call release() from within on_released (would be a no-op anyway)
```

---

### 2. Callback wiring pattern (no bidirectional coupling)

**Source:** `apps/voice/server.py` lines 234–242 and `session.py` module docstring

**Apply to:** Any code that needs to couple transport events to lifecycle actions

```python
# Pattern: SessionLifecycle has callback fields; callers wire them

# SessionLifecycle owns:
# - on_warning() — called by _service_timer when warning threshold reached
# - on_stop() — called by _service_timer at hard-stop time
# - on_released() — called by release() after all internal cleanup
# - (also: on_transport_disconnected/reconnected — called by transport event handlers)

# Caller (server.py / call_runtime.py) wires these to the actual pipeline:
async def _on_warning():
    await inject_warning_instruction(worker, context, copy)

async def _on_stop():
    await speak_goodbye(worker, copy)
    await asyncio.sleep(grace)
    await runner.cancel()

lifecycle.on_warning = _on_warning
lifecycle.on_stop = _on_stop
lifecycle.on_released = runner.cancel

# Key: lifecycle never imports the caller; coupling is one-directional
```

---

### 3. Transport event handler registration pattern

**Source:** `apps/voice/server.py` lines 244–254 and `pipeline.py` lines 256–258

**Apply to:** Any code that needs to react to transport events

```python
# Pattern: Use transport.event_handler(...) decorator to register async handlers

@transport.event_handler("on_client_disconnected")
async def _on_client_disconnected(transport, client):  # noqa: ANN001
    # The signature is pipecat's standard: (transport, client) are positional args
    await lifecycle.on_transport_disconnected()

@transport.event_handler("on_client_connected")
async def _on_client_reconnected(transport, client):
    await lifecycle.on_transport_reconnected()

# This pattern works identically for any BaseTransport subclass:
# - SmallWebRTCTransport (browser)
# - TelephonyTransport (telephony, Phase B)
# - LocalAudioTransport (console)
# - Any custom transport

# The handler is registered at pipeline-construction time (inside _run_session / build_pipeline)
# AFTER the lifecycle callbacks are wired but BEFORE the runner starts
```

---

### 4. Build pipeline with transport-neutral seam

**Source:** `apps/voice/src/klanker_voice/pipeline.py` lines 65–171

**Apply to:** Any code that constructs a pipeline

```python
# Pattern: build_pipeline accepts an arbitrary BaseTransport

def build_pipeline(
    cfg: PipelineConfig,
    transport: BaseTransport,  # <-- The transport-neutral seam
    *,
    rtvi: RTVIProcessor | None = None,
    knowledge_cfg: KnowledgeConfig | None = None,
    duplex_cfg: DuplexConfig | None = None,
    remaining_seconds_fn: Callable[[], float | None] | None = None,
) -> BuiltPipeline:
    """Assemble the cascade pipeline.
    
    The transport is used as:
    - transport.input() — first processor in the chain (receives audio from transport)
    - transport.output() — last processor in the chain (sends audio to transport)
    """
    processors = [
        transport.input(),
        stt,
        router,
        user_aggregator,
        llm,
        tts,
        transport.output(),
        assistant_aggregator,
    ]
    
    pipeline = Pipeline(processors)
    return BuiltPipeline(pipeline=pipeline, context=context, user_aggregator=user_aggregator, ...)

# Key insight: The pipeline itself is transport-agnostic
# Only transport.input() and transport.output() are transport-specific
# Everything else (STT, LLM, TTS, routing, knowledge) is identical
```

---

### 5. Greet-first + worker construction pattern

**Source:** `apps/voice/server.py` lines 214–216 and `pipeline.py` lines 239–258

**Apply to:** All code that builds a worker

```python
# Pattern 1: Build the worker with observers
worker = build_worker(
    built.pipeline,
    observers=[
        LatencyReportObserver(cfg),
        RTVIObserver(rtvi, params=build_rtvi_observer_params()),
        TeardownObserver(lifecycle),  # <-- Connects pipeline events to lifecycle
    ],
)

# Pattern 2: Register greet-first on transports that support on_client_connected
if cfg.persona.greet_first:
    register_greet_first(transport, worker, built.context)

# Pattern 3: For transports without on_client_connected (e.g., LocalAudioTransport),
# call greet_now() directly after starting the runner:
await greet_now(worker, built.context)

# Key: greet_now() and register_greet_first() both use the same underlying
# mechanism: add a developer kick message + queue an LLMRunFrame
```

---

### 6. Ambience mixer (per-topic, router-controlled)

**Source:** `apps/voice/server.py` lines 178–188 and `pipeline.py` lines 174–207

**Apply to:** All session construction code

```python
# Pattern: Optionally build a mixer (returns None if disabled or no WAV)
mixer = build_ambience_mixer(cfg)

# If mixer exists, use it in TransportParams
transport_params = (
    TransportParams(
        audio_in_enabled=True,
        audio_out_enabled=True,
        audio_out_sample_rate=cfg.greenhouse.ambience_sample_rate,
        audio_out_mixer=mixer,
    )
    if mixer is not None
    else TransportParams(audio_in_enabled=True, audio_out_enabled=True)
)

# The router (inside the pipeline) controls mixing via MixerEnableFrame / MixerUpdateSettingsFrame
# No other code touches the mixer
```

---

### 7. Error handling + cancellation pattern

**Source:** `apps/voice/server.py` lines 273–277 and `session.py` lines 207–234

**Apply to:** All async session code

```python
# Pattern: Always use try/finally to guarantee cleanup

async def _start_and_run_tracked_session(...) -> None:
    await lifecycle.start()
    try:
        await _run_session(...)
    finally:
        await lifecycle.stop()  # Guarantees release() is called

# SessionLifecycle.release() is idempotent and guards against concurrent calls
# so even if release() is called from multiple paths (worker termination,
# transport disconnect, timeout), it fires exactly once
```

---

### 8. AWS API call pattern (off the event loop)

**Source:** `apps/voice/src/klanker_voice/session.py` lines 174–179, 228–232

**Apply to:** All code making AWS calls

```python
# Pattern: Use asyncio.to_thread for blocking AWS calls

async def start(self) -> None:
    # These block on network I/O; run them on a thread pool
    await asyncio.to_thread(self._emit_metric)
    await asyncio.to_thread(self._reconcile_scale_in_protection)
    # Now the main event loop isn't blocked

async def release(self) -> None:
    await asyncio.to_thread(quota.release_heartbeat, self.user_id, self.session_id)
    await asyncio.to_thread(self._emit_metric)
    await asyncio.to_thread(self._reconcile_scale_in_protection)
    # Safe for concurrent sessions; no event-loop blocking
```

---

## No Analog Found

Files/patterns with no close existing match:

| Pattern | Reason | Plan |
|---------|--------|------|
| `CallIdentity` (minimal) | New abstraction (Phase 12 fleshes it out) | Use dataclass placeholder now; expand when spec §23 lands |
| `CallSession` dataclass + API | New public interface | Extract target is spec §6; no existing example in codebase |

---

## Metadata

**Analog search scope:**
- `apps/voice/server.py` (506 lines) — extraction source
- `apps/voice/src/klanker_voice/session.py` (300+ lines) — lifecycle pattern
- `apps/voice/src/klanker_voice/pipeline.py` (280+ lines) — builder pattern
- `apps/voice/src/klanker_voice/webrtc.py` (167 lines) — transport-specific module example
- `apps/voice/console.py` (52 lines) — non-WebRTC caller example
- `apps/voice/tests/` (16+ test files) — testing patterns

**Files scanned:** 20+  
**Analogs identified:** 5 exact/high-quality matches  
**Pattern extraction date:** 2026-07-11

---

## Critical Notes

### What stays WebRTC-specific (D-03, do NOT extract)

- `_negotiate_webrtc()` — SDP offer/answer via SmallWebRTCHandler
- `_connection_callback()` — creates SmallWebRTCConnection & lifecycle; NOW CALLS `create_call_session()`
- `_wire_connection_teardown()` — immediate-release on aiortc `connection.closed` event (reconnect-grace race handling)
- `_extract_bearer_token()` — HTTP header parsing
- `/api/offer` POST and PATCH routes — FastAPI binding
- SmallWebRTCTransport instantiation — transport-specific; passed to `create_call_session()`

### What moves to call_runtime.py (D-02, extract these)

- `_run_session()` — pipeline/worker/observer/callback wiring
- `_start_and_run_tracked_session()` — lifecycle.start/run/stop wrapper
- Session construction logic that doesn't touch WebRTC/HTTP
- CallSession dataclass + create_call_session() function

### Behavior that MUST be preserved (D-04)

Every line of:
- quota start-gate behavior
- SessionLifecycle (service-timer hard-stop, ActiveSessions metric, ECS scale-in, accounting ticks)
- Observers (RTVI, LatencyReport, Teardown)
- Greeting (greet_now, greet_first guard from Phase 05.2)
- Warning + goodbye (TTSSpeakFrame callbacks)
- Reconnect grace (12s window, browser reconnect logic)
- RTVI processing
- Ambience mixer (per-session, router-controlled)
- All metrics and teardown guarantees

**Exit criterion (spec §19-A):** Browser voice works byte-for-byte identically after refactor.

