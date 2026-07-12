# Phase 11: VoIP.ms Telephony — Local Asterisk Edge - Pattern Map

**Mapped:** 2026-07-11
**Files analyzed:** 11 new/modified files
**Analogs found:** 9/11 (2 files — Asterisk configs and tests — have partial analogs within the project codebase; see details below)

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `apps/voice/src/klanker_voice/telephony/controller.py` | controller | request-response (ARI events) | `apps/voice/server.py` lines 147–239 + `apps/voice/src/klanker_voice/webrtc.py` | role-match (transport-specific entry + registry) |
| Socket-backed `RtpMediaSession` (UDP/RTP, satisfies Phase 10 Protocol) | service | streaming (UDP socket) | `apps/voice/src/klanker_voice/telephony/transport.py` lines 1–80 + `telephony/types.py` lines 39–61 | exact (implements same Protocol) |
| Inline `GateProcessor` (pipecat FrameProcessor for §24 gate) | processor | request-response (transcription → unlock) | `apps/voice/src/klanker_voice/knowledge/router.py` lines 1–200 (KnowledgeRouterProcessor structure) | role-match (FrameProcessor subclass, pipeline insertion) |
| `CallIdentity` / identity seam | model | request-response (tier resolution) | `apps/voice/src/klanker_voice/call_runtime.py` lines 60–73 (CallIdentity dataclass) | exact (reuse or extend) |
| `[telephony]` config loader | utility (config) | CRUD (load/validate TOML) | `apps/voice/src/klanker_voice/config.py` lines 261–323 (_reject_credential_fields + _load_toml_data) | exact-pattern (credential rejection regex + loader) |
| Standalone telephony entrypoint | controller (main) | request-response (ARI client lifecycle) | `apps/voice/server.py` lines 1–72 (FastAPI app + module imports + initialization) | role-match (config load + service/client construction) |
| `apps/voice/asterisk/` configs (pjsip.conf, ari.conf, extensions.conf, docker-compose.yml) | config | configuration (PJSIP/ARI/Stasis setup) | N/A (no existing Asterisk config in codebase) | no-analog |
| `apps/voice/tests/test_telephony_lifecycle.py` | test | CRUD (lifecycle assertions) | `apps/voice/tests/test_call_runtime.py` lines 1–120 (FakeTransport, fake_aws, lifecycle tests) | exact-pattern (reuse FakeTransport pattern) |
| `apps/voice/tests/test_telephony_config.py` | test | CRUD (config load + credential rejection) | `apps/voice/tests/test_config.py` (credential rejection tests) | exact-pattern (reuse test structure) |
| Docker-compose SIP integration test | test (integration) | streaming (SIP INVITE → RTP → fake media) | `apps/voice/tests/conftest.py` (fake media infrastructure) | partial-match (reuse fake-media patterns) |
| Telephony-specific config extensions (`TelephonyConfig` / `[telephony]` table fields) | model (config section) | CRUD (parse + validate) | `apps/voice/src/klanker_voice/config.py` lines 234–256 (QuotaConfig dataclass + loader) | exact-pattern (frozen dataclass + section loader) |

---

## Pattern Assignments

### `apps/voice/src/klanker_voice/telephony/controller.py` (controller, request-response)

**Analog:** `apps/voice/server.py` + `apps/voice/src/klanker_voice/webrtc.py`

**Imports pattern** (`server.py` lines 22–68):
```python
from loguru import logger
from pipecat.transports.base_transport import BaseTransport
from pipecat.workers.runner import WorkerRunner

from klanker_voice import quota, session
from klanker_voice.auth import SessionIdentity, validate_access_token
from klanker_voice.config import load_config, load_knowledge_config, load_quota_config
from klanker_voice.pipeline import build_pipeline, build_worker
from klanker_voice.session import SessionLifecycle, TeardownObserver
```

**Transport + registry construction pattern** (`server.py` lines 166–199):
```python
# Phase 11 adapts this: instead of SmallWebRTCTransport, construct TelephonyTransport;
# instead of a per-connection lifecycle, maintain an ActiveCall registry keyed by
# Asterisk channel ID; lifecycle setup remains verbatim.

transport = SmallWebRTCTransport(params=_WEBRTC_TRANSPORT_PARAMS, webrtc_connection=connection)
config_path = variants.variant_config_path(variant)
cfg = load_config(config_path)
knowledge_cfg = load_knowledge_config(config_path)
duplex_cfg = load_duplex_config(config_path)
quota_cfg = load_quota_config()

built = build_pipeline(cfg, transport, rtvi=rtvi, knowledge_cfg=knowledge_cfg, 
                       duplex_cfg=duplex_cfg, remaining_seconds_fn=lifecycle.remaining_seconds)
worker = build_worker(built.pipeline, observers=[LatencyReportObserver(cfg), TeardownObserver(lifecycle)])
```

**Idempotent close pattern** (`server.py` lines 262–299):
```python
# Phase 11 on_channel_destroyed / hard-timeout handler mirrors this:
async def _on_client_disconnected(transport, client):
    await lifecycle.on_transport_disconnected()

# And the on_released hook (called by lifecycle.release()) cancels the runner:
lifecycle.on_released = runner.cancel
```

**Controller responsibilities in Phase 11 (adapted):**
- Consume ARI `StasisStart` → create external-media channel + mixing bridge + socket-backed `RtpMediaSession`
- Allocate and construct a `CallSession` via `create_call_session(transport=TelephonyTransport(...), identity=..., gate_result=...)`
- On `ChannelDestroyed` / hard timeout: call `await active_call.call_session.close("reason")` once (idempotent), tear down bridge/external-channel/socket, remove registry
- DTMF PIN → controller layer (not LLM), passphrase → `GateProcessor` (pipeline layer, post-STT)

---

### Socket-backed `RtpMediaSession` (service, streaming)

**Analog:** `apps/voice/src/klanker_voice/telephony/types.py` (Protocol) + `apps/voice/src/klanker_voice/telephony/transport.py` (usage pattern) + `apps/voice/src/klanker_voice/telephony/media.py` (codec/RTP wire format)

**RtpMediaSession Protocol** (`types.py` lines 39–61):
```python
class RtpMediaSession(Protocol):
    """Bidirectional RTP byte seam consumed by TelephonyTransport."""
    
    async def read_packet(self) -> bytes | None:
        """Return the next raw RTP datagram, or None at end-of-stream."""
        ...
    
    async def write_packet(self, packet: bytes) -> None:
        """Send one raw RTP datagram."""
        ...
    
    async def close(self) -> None:
        """Release any held resources. Safe to call more than once."""
        ...
```

**UDP/asyncio implementation pattern** (recommended in RESEARCH.md R2):
```python
# Phase 11 implements a socket-backed version using asyncio.DatagramProtocol
class _AsteriskRtpProtocol(asyncio.DatagramProtocol):
    def __init__(self) -> None:
        self._queue: asyncio.Queue[bytes] = asyncio.Queue()
        self._peer: tuple[str, int] | None = None
        self._transport: asyncio.DatagramTransport | None = None
    
    def connection_made(self, transport: asyncio.DatagramTransport) -> None:
        self._transport = transport
    
    def datagram_received(self, data: bytes, addr: tuple[str, int]) -> None:
        self._peer = addr  # symmetric-RTP source learning
        self._queue.put_nowait(data)
    
    def error_received(self, exc: Exception) -> None:
        pass  # never raise; follow T-10 hostile-input posture
```

**Transport usage** (`transport.py` lines 220–250, showing TelephonyInputTransport constructor):
```python
# Phase 11 uses: transport = TelephonyTransport(media=socket_session, ...)
class TelephonyInputTransport(BaseInputTransport):
    def __init__(self, media: RtpMediaSession, params: TelephonyTransportParams, 
                 pipecat_params: TransportParams, *, on_ready, on_media_end, **kwargs) -> None:
        super().__init__(pipecat_params, **kwargs)
        self._media = media  # Phase 11's socket session dropped in here
        self._telephony_params = params
        self._on_ready = on_ready
        self._on_media_end = on_media_end
```

**Codec / RTP wire format** (reused from Phase 10 `media.py` verbatim — Phase 11 adds no codec changes):
```python
# Phase 11 inherits: PCMU (μ-law) codec, 8 kHz clock, 20 ms packetization, 160 samples/packet, PT=0
# See apps/voice/src/klanker_voice/telephony/media.py parse_rtp(), ulaw_encode(), ulaw_decode()
```

---

### Inline `GateProcessor` (processor, request-response)

**Analog:** `apps/voice/src/klanker_voice/knowledge/router.py` (KnowledgeRouterProcessor) + `apps/voice/src/klanker_voice/pipeline.py` lines 141–162 (processor insertion pattern)

**FrameProcessor subclass structure** (`knowledge/router.py` lines 122–200):
```python
from pipecat.frames.frames import Frame, TranscriptionFrame, TTSSpeakFrame
from pipecat.processors.frame_processor import FrameDirection, FrameProcessor

class KnowledgeRouterProcessor(FrameProcessor):
    """Sits between STT and user_aggregator in build_pipeline (D-01)."""
    
    def __init__(self, cfg: PipelineConfig, knowledge_cfg: KnowledgeConfig, 
                 llm: ..., initial_topic: str, retrieval_index: ..., 
                 remaining_seconds_fn: ...) -> None:
        super().__init__(name="knowledge-router")
        self._cfg = cfg
        # ... init fields ...
    
    async def process_frame(self, frame: Frame, direction: FrameDirection) -> None:
        if isinstance(frame, TranscriptionFrame):
            # Process transcription, possibly emit TTSSpeakFrame
            await self._process_transcription(frame)
        await super().process_frame(frame, direction)
```

**Phase 11 GateProcessor structure (adapted):**
```python
# Sits between STT and router/user_aggregator; swallows frames while locked;
# runs passphrase matcher on TranscriptionFrame.text (order-free 4-word set membership);
# DTMF PIN handled at controller layer (ARI ChannelDtmfReceived → unlock directly);
# on unlock: call greet_now(worker, context) and set self._unlocked = True;
# on gate_window timeout: call speak_goodbye(worker, goodbye_copy) then hangup.

class GateProcessor(FrameProcessor):
    def __init__(self, passphrase_words: list[str], gate_window_seconds: float) -> None:
        super().__init__(name="answer-gate")
        self._unlocked = False
        self._passphrase_words = set(w.lower() for w in passphrase_words)
        self._gate_window_seconds = gate_window_seconds
        self._accumulated_words: set[str] = set()
        self._gate_timer_task: asyncio.Task | None = None
    
    async def process_frame(self, frame: Frame, direction: FrameDirection) -> None:
        if isinstance(frame, TranscriptionFrame) and not self._unlocked:
            # Redaction boundary: never forward pre-unlock transcription downstream
            tokens = self._tokenize(frame.text.lower())
            self._accumulated_words.update(tokens)
            if self._passphrase_words.issubset(self._accumulated_words):
                await self._unlock()
            return  # NEVER call push_frame — pre-unlock transcript never reaches LLM/ledger
        
        # Unlocked: forward everything normally
        await super().process_frame(frame, direction)
    
    async def _unlock(self) -> None:
        self._unlocked = True
        logger.info(f"Gate unlocked: passphrase method, call_id={self._call_id}")
        # Caller (controller) wires this; greet_now fires here
        await self._on_unlock()
```

**Pipeline insertion** (`pipeline.py` lines 141–162):
```python
# Phase 11 inserts GateProcessor right after STT, before router:
processors = [transport.input()]
if rtvi is not None:
    processors.append(rtvi)
processors.append(stt)
if gate_processor is not None:
    processors.append(gate_processor)  # Phase 11: inserted here, between STT and router
processors.append(router)
processors.extend([user_aggregator, llm, tts, transport.output(), assistant_aggregator])

pipeline = Pipeline(processors)
```

---

### `CallIdentity` / identity seam for tier grant (model, request-response)

**Analog:** `apps/voice/src/klanker_voice/call_runtime.py` lines 60–73

**Current definition** (`call_runtime.py`):
```python
@dataclass(frozen=True)
class CallIdentity:
    """Minimal, transport-neutral caller identity.
    
    Deliberately thin: only ``subject`` is used by this phase (threaded into
    :class:`~klanker_voice.session.SessionLifecycle` as ``user_id``).
    Phase 12 (spec §11/§23) adds real phone -> code -> tier resolution for
    telephony callers; do NOT anticipate that here.
    """
    
    subject: str
    authenticated: bool = False
    auth_method: str = "webrtc-oidc"
```

**Phase 11 adaptation (minimal seam, D-05a):**
```python
# Phase 11 constructs CallIdentity for PSTN caller, e.g.:
# identity = CallIdentity(
#     subject=f"tel:{normalized_caller_id or call_id}",
#     authenticated=False,  # will be set True after unlock
#     auth_method="pstn-pin-passphrase"
# )
#
# The actual tier grant happens at the gate-unlock boundary:
# - DTMF PIN → controller layer checks against TELEPHONY_ACCESS_PIN, calls unlock seam
# - Passphrase → GateProcessor checks tokens, calls unlock seam
# - unlock seam: construct SessionIdentity (via auth.py's domain) for quota.start_gate()
#   THEN set identity.authenticated=True, hand to create_call_session()
#
# Do NOT pull forward the §11/§23 caller-ID → access-code → baseline-tier resolver;
# that stays Phase 12 (untestable on local softphone). The D-05a "minimal seam" is
# just the thin call-identity abstraction this module already defines.
```

**Usage in create_call_session** (`call_runtime.py` lines 129–150):
```python
async def create_call_session(*, transport: BaseTransport, identity: CallIdentity, 
                              gate_result: quota.GateResult, ...) -> CallSession:
    logger.info(f"create_call_session: channel={channel} session_id={gate_result.session_id} "
                f"user_id={identity.subject}")
    
    lifecycle = SessionLifecycle(
        user_id=identity.subject,  # Phase 11: "tel:..." or call_id
        session_id=gate_result.session_id,
        tier=gate_result.tier,
        quota_config=quota_cfg,
        bypass_accounting=gate_result.bypass_accounting,
    )
```

---

### `[telephony]` config loader (utility, CRUD)

**Analog:** `apps/voice/src/klanker_voice/config.py` lines 261–323 (credential rejection) + lines 234–256 (QuotaConfig pattern)

**Credential rejection pattern** (`config.py` lines 36–40, 261–276):
```python
_CREDENTIAL_FIELD_RE = re.compile(
    r"(?:^|_)(?:api_?key|key|keys|secret|secrets|token|tokens|password|passwd|"
    r"credential|credentials|bearer|auth)(?:_|$)|apikey",
    re.IGNORECASE,
)

def _reject_credential_fields(data: object, path: str = "") -> None:
    """Recursively reject any field whose name suggests credential material."""
    if isinstance(data, dict):
        for key, value in data.items():
            key_path = f"{path}.{key}" if path else key
            if _CREDENTIAL_FIELD_RE.search(str(key)):
                raise ConfigError(
                    f"pipeline.toml field '{key_path}' looks like credential material. "
                    "Secrets never live in TOML (D-09) — API keys come from .env "
                    "via `make -C apps/voice env`."
                )
            _reject_credential_fields(value, key_path)
```

**Phase 11 extension (D-09): extend regex to match `pin` and `passphrase`:**
```python
# In config.py, line 36-40, update _CREDENTIAL_FIELD_RE to:
_CREDENTIAL_FIELD_RE = re.compile(
    r"(?:^|_)(?:api_?key|key|keys|secret|secrets|token|tokens|password|passwd|"
    r"credential|credentials|bearer|auth|pin|passphrase|pass_?word|words)(?:_|$)|apikey",
    re.IGNORECASE,
)
# This ensures TELEPHONY_ACCESS_PIN, TELEPHONY_PASSPHRASE_WORDS, etc. cannot be
# silently accepted as TOML tunables.
```

**Config dataclass pattern** (`config.py` lines 234–256, QuotaConfig):
```python
@dataclass(frozen=True)
class QuotaConfig:
    """Race-safe quota enforcement + wind-down/teardown knobs (QUOT-01/02/03/05)."""
    
    heartbeat_renew_interval: float  # seconds between ticks
    heartbeat_ttl: float  # seconds
    sub_floor_seconds: float
    per_task_max_sessions: int
    auto_trip_ceiling_seconds: float
    # ... etc ...
```

**Phase 11 TelephonyConfig** (add to `config.py` or `telephony/config.py`):
```python
@dataclass(frozen=True)
class TelephonyConfig:
    """Telephony [telephony] table (Phase 11, D-09): media + gate knobs only.
    
    Non-secret behavior config — secrets (ARI credentials, PIN, passphrase words)
    sourced from env/SSM, never TOML.
    """
    
    enabled: bool = False
    provider: str = "voipms"
    edge: str = "asterisk-ari"
    codec: str = "pcmu"
    sample_rate: int = 8000
    packet_ms: int = 20
    max_concurrent_calls: int = 1
    answer_timeout_seconds: int = 15
    hangup_on_pipeline_error: bool = True
    # §24 gate
    require_gate: bool = True
    gate_mode: str = "either"  # "dtmf", "passphrase", or "either"
    gate_window_seconds: int = 10
    unlock_tier_id: str = "kph-tier"  # (Open Question #2 from RESEARCH)
```

**Config loader pattern** (`config.py` lines 327–336):
```python
def load_config(path: Path | str | None = None) -> PipelineConfig:
    """Parse and validate a pipeline TOML file into a PipelineConfig."""
    path = _resolve_config_path(path)
    data = _load_toml_data(path)  # Already runs _reject_credential_fields(data)
    # ... validation ...
    return PipelineConfig(...)

# Phase 11 adds:
def load_telephony_config(path: Path | str | None = None) -> TelephonyConfig:
    """Parse and validate the [telephony] section of pipeline.toml."""
    path = _resolve_config_path(path)
    data = _load_toml_data(path)  # Reuses same _reject_credential_fields gate
    
    telephony_table = data.get("telephony", {})
    if not isinstance(telephony_table, dict):
        raise ConfigError("[telephony] table is missing or not a dict")
    
    # Parse and validate each field
    return TelephonyConfig(
        enabled=telephony_table.get("enabled", False),
        provider=telephony_table.get("provider", "voipms"),
        edge=telephony_table.get("edge", "asterisk-ari"),
        codec=telephony_table.get("codec", "pcmu"),
        sample_rate=int(telephony_table.get("sample_rate", 8000)),
        packet_ms=int(telephony_table.get("packet_ms", 20)),
        max_concurrent_calls=int(telephony_table.get("max_concurrent_calls", 1)),
        answer_timeout_seconds=int(telephony_table.get("answer_timeout_seconds", 15)),
        hangup_on_pipeline_error=bool(telephony_table.get("hangup_on_pipeline_error", True)),
        require_gate=bool(telephony_table.get("require_gate", True)),
        gate_mode=telephony_table.get("gate_mode", "either"),
        gate_window_seconds=int(telephony_table.get("gate_window_seconds", 10)),
        unlock_tier_id=telephony_table.get("unlock_tier_id", "kph-tier"),
    )
```

---

### Standalone telephony entrypoint (`python -m klanker_voice.telephony.controller`) (controller, request-response)

**Analog:** `apps/voice/server.py` (module structure + config loading) + `apps/voice/src/klanker_voice/harness/__main__.py` (console entrypoint pattern)

**Server.py module structure** (`server.py` lines 1–72):
```python
"""Production FastAPI entrypoint for the deployed klanker-voice service (INFR-03).

Serves POST ``/api/offer`` (SmallWebRTC signaling) and GET ``/health`` on port 7860 — 
the self-hosted Fargate deploy target...
"""

from __future__ import annotations

from dotenv import load_dotenv
from fastapi import FastAPI
from loguru import logger

from klanker_voice import quota, session, variants
from klanker_voice.auth import AuthError, SessionIdentity, validate_access_token
from klanker_voice.config import load_config, load_duplex_config, load_knowledge_config, load_quota_config

load_dotenv(override=True)

app = FastAPI(title="klanker-voice")
```

**Phase 11 telephony entrypoint** (new `telephony/controller.py` or `python -m klanker_voice.telephony`):
```python
# apps/voice/src/klanker_voice/telephony/__main__.py
"""Telephony ARI/Stasis controller entrypoint (Phase C, D-08).

Standalone process: connects to Asterisk ARI (REST + events WebSocket),
maintains ActiveCall registry, dispatches StasisStart/ChannelDtmfReceived/
ChannelDestroyed events to AsteriskCallController. Runs alongside docker-compose
Asterisk and the browser server.py (separate processes, shared factories/pipeline).

Run with::

    python -m klanker_voice.telephony.controller
"""

from __future__ import annotations

import asyncio
import os
from dotenv import load_dotenv
from loguru import logger

from klanker_voice import quota
from klanker_voice.config import load_config, load_knowledge_config, load_quota_config
from klanker_voice.telephony.config import load_telephony_config
from klanker_voice.telephony.controller import AsteriskCallController
from klanker_voice.telephony.ari import AriClient

load_dotenv(override=True)

async def main() -> None:
    """Initialize and run the ARI controller."""
    cfg = load_config()
    knowledge_cfg = load_knowledge_config()
    quota_cfg = load_quota_config()
    telephony_cfg = load_telephony_config()
    
    if not telephony_cfg.enabled:
        logger.error("[telephony] section disabled in pipeline.toml")
        return
    
    ari_url = os.environ.get("ASTERISK_ARI_URL", "http://localhost:8088")
    ari_user = os.environ.get("ASTERISK_ARI_USERNAME", "klanker")
    ari_pass = os.environ.get("ASTERISK_ARI_PASSWORD")
    
    ari_client = AriClient(base_url=ari_url, username=ari_user, password=ari_pass)
    controller = AsteriskCallController(ari_client, cfg, knowledge_cfg, quota_cfg, telephony_cfg)
    
    await ari_client.connect()
    await controller.run()

if __name__ == "__main__":
    asyncio.run(main())
```

---

### Asterisk configs (N/A — no existing code analog, see RESEARCH.md R3–R4)

**Files to create** (documented in RESEARCH.md with concrete TOML/INI skeletons):
- `apps/voice/asterisk/http.conf` — ARI's HTTP server binding (D-01)
- `apps/voice/asterisk/ari.conf` — authenticated ARI user (D-01)
- `apps/voice/asterisk/pjsip.conf` — SIP transport + softphone endpoint (D-01)
- `apps/voice/asterisk/extensions.conf` — inbound-only Stasis dialplan (D-01)
- `apps/voice/asterisk/docker-compose.yml` — local harness (D-07)
- `apps/voice/asterisk/README.md` — setup + CI automation steps (D-07)

**See RESEARCH.md R3 (Asterisk configs) and R4 (docker-compose + SIPp) for exact skeletal content and macOS Docker Desktop networking workarounds.**

---

### `apps/voice/tests/test_telephony_lifecycle.py` (test, CRUD)

**Analog:** `apps/voice/tests/test_call_runtime.py` (FakeTransport pattern + lifecycle assertions)

**FakeTransport pattern** (`test_call_runtime.py` lines 47–63):
```python
from pipecat.processors.frame_processor import FrameProcessor
from pipecat.transports.base_transport import BaseTransport

class FakeTransport(BaseTransport):
    """A minimal, deliberately NOT-WebRTC BaseTransport stub — proves
    create_call_session works against ANY transport, not just SmallWebRTCTransport."""
    
    def __init__(self) -> None:
        super().__init__()
        self._register_event_handler("on_client_connected")
        self._register_event_handler("on_client_disconnected")
    
    def input(self) -> FrameProcessor:
        return FrameProcessor(name="fake-transport-input")
    
    def output(self) -> FrameProcessor:
        return FrameProcessor(name="fake-transport-output")
```

**Fake AWS client pattern** (`test_call_runtime.py` lines 34–46):
```python
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

**Phase 11 test structure:**
```python
# apps/voice/tests/test_telephony_lifecycle.py
"""Lifecycle assertions for Phase 11 telephony (ARI → CallSession → close, D-05/D-16/D-17)."""

import asyncio
import pytest
from klanker_voice import quota, session
from klanker_voice.call_runtime import CallIdentity, create_call_session
from klanker_voice.config import load_config, load_knowledge_config, load_quota_config
from klanker_voice.telephony.types import RtpMediaSession, TelephonyTransportParams
from klanker_voice.telephony.transport import TelephonyTransport

# Reuse FakeTransport pattern from test_call_runtime.py
class FakeRtpMediaSession:
    """Fake RtpMediaSession for integration test (no real UDP socket)."""
    
    def __init__(self):
        self.packets_written: list[bytes] = []
        self.packets_to_read: list[bytes] = []
        self._read_index = 0
    
    async def read_packet(self) -> bytes | None:
        if self._read_index >= len(self.packets_to_read):
            return None  # end-of-stream
        pkt = self.packets_to_read[self._read_index]
        self._read_index += 1
        return pkt
    
    async def write_packet(self, packet: bytes) -> None:
        self.packets_written.append(packet)
    
    async def close(self) -> None:
        pass

@pytest.fixture(autouse=True)
def reset_active_count():
    """Reset the global active-session count between tests."""
    session._active_session_count = 0
    yield
    session._active_session_count = 0

@pytest.mark.asyncio
async def test_asterisk_channel_destroyed_closes_call():
    """§16/D-16: ARI ChannelDestroyed → CallSession.close() → lifecycle.release() exactly once."""
    # Create fake media + transport + session
    media = FakeRtpMediaSession()
    transport = TelephonyTransport(
        media=media,
        params=TelephonyTransportParams(),
        pipecat_params=TransportParams(audio_in_enabled=True, audio_out_enabled=True),
        on_ready=lambda: None,
        on_media_end=lambda: None,
    )
    
    cfg = load_config()
    knowledge_cfg = load_knowledge_config()
    quota_cfg = load_quota_config()
    
    gate_result = quota.GateResult(
        session_id="test-call-123",
        tier="kph-tier",
        bypass_accounting=True,
    )
    
    identity = CallIdentity(subject="tel:+15551234567")
    
    session_obj = await create_call_session(
        transport=transport,
        identity=identity,
        gate_result=gate_result,
        cfg=cfg,
        knowledge_cfg=knowledge_cfg,
        duplex_cfg=None,
        quota_cfg=quota_cfg,
        channel="pstn",
        metadata={"call_id": "test-123"},
    )
    
    # Verify once constructed
    assert session_obj.lifecycle._stopped == False
    
    # Simulate ARI ChannelDestroyed → controller calls close()
    await session_obj.close("ari channel destroyed")
    
    # Verify idempotent close: second call is a no-op
    await session_obj.close("ari channel destroyed")
    assert session_obj.lifecycle._stopped == True
```

---

### `apps/voice/tests/test_telephony_config.py` (test, CRUD)

**Analog:** `apps/voice/tests/test_config.py` (credential rejection tests)

**Credential rejection test pattern:**
```python
# Phase 11 test_telephony_config.py
"""Credential rejection + TelephonyConfig parsing (D-09)."""

import pytest
from klanker_voice.config import ConfigError, load_telephony_config

def test_telephony_config_rejects_pin_field():
    """D-09: pin/passphrase/words fields must be rejected from TOML (§24 secrets)."""
    # This test would create a temporary TOML with a [telephony] section
    # containing a field like access_pin or passphrase_words, then verify
    # ConfigError is raised before the field is even parsed.
    
    bad_toml = """
    [telephony]
    enabled = true
    access_pin = "1234"  # Bad: looks like credential
    """
    
    # Write to temp file, attempt load_telephony_config(...), expect ConfigError
    with pytest.raises(ConfigError, match="looks like credential material"):
        load_telephony_config(path=temp_file)

def test_telephony_config_loads_valid_table():
    """Valid [telephony] table parses without error."""
    valid_toml = """
    [telephony]
    enabled = true
    provider = "voipms"
    edge = "asterisk-ari"
    codec = "pcmu"
    gate_mode = "either"
    gate_window_seconds = 10
    unlock_tier_id = "kph-tier"
    """
    
    cfg = load_telephony_config(path=temp_file)
    assert cfg.enabled == True
    assert cfg.provider == "voipms"
    assert cfg.gate_mode == "either"
    assert cfg.gate_window_seconds == 10
```

---

## Shared Patterns

### Authentication / Authorization (D-01, D-02: ARI private + authenticated)

**Source:** `apps/voice/server.py` lines 127–139 (bearer token extraction + auth validation)

**Apply to:** Telephony controller's ARI client initialization

```python
# Phase 11 ARI client setup:
ari_user = os.environ.get("ASTERISK_ARI_USERNAME")
ari_pass = os.environ.get("ASTERISK_ARI_PASSWORD")

if not ari_user or not ari_pass:
    raise ConfigError("ASTERISK_ARI_USERNAME and ASTERISK_ARI_PASSWORD must be set")

# Pass to AriClient (basic HTTP auth); ARI REST and events WebSocket both use these creds
# (spec §14, RESEARCH R1: no new library, raw aiohttp + Basic-Auth)
```

### Error Handling & Logging

**Source:** `apps/voice/server.py` + `apps/voice/src/klanker_voice/call_runtime.py` (logger.info + typed exceptions)

**Apply to:** Telephony controller's ARI event handlers and lifecycle transitions

```python
# Pattern: logger.info() at key state transitions, typed exceptions for validation
from loguru import logger
from klanker_voice.config import ConfigError

logger.info(f"StasisStart: channel_id={channel_id}, caller={caller_id}, did={did}")

if not ari_user:
    raise ConfigError("ASTERISK_ARI_USERNAME not set")
```

### Idempotent Close / Release

**Source:** `apps/voice/src/klanker_voice/session.py` + `apps/voice/src/klanker_voice/call_runtime.py` lines 95–98

**Apply to:** Telephony controller's `on_channel_destroyed` and timeout handlers (§16 D-02 requirement: exactly-once close, no leaks)

```python
# controller.py on_channel_destroyed handler
async def on_channel_destroyed(self, event):
    call_id = event.channel.id
    if call_id not in self._active_calls:
        logger.warning(f"ChannelDestroyed for unknown call {call_id}")
        return
    
    active_call = self._active_calls[call_id]
    
    # Reuse the single, idempotent close path (D-05 from Phase 9)
    await active_call.call_session.close("ari channel destroyed")
    
    # Clean up registry (after close, which is idempotent)
    del self._active_calls[call_id]
    
    # Tear down bridge / external-media channel / socket
    await self._ari_client.hangup(active_call.sip_channel_id)
    # ... tear down bridge, external media channel, RTP socket ...
```

### Config Loading (pipeline.toml → frozen dataclasses)

**Source:** `apps/voice/src/klanker_voice/config.py` lines 327–336 (load_config pattern)

**Apply to:** Telephony config loader (load_telephony_config) and environment/SSM secret fetching

```python
# apps/voice/src/klanker_voice/telephony/config.py
from klanker_voice.config import load_telephony_config

# All non-secret [telephony] behavior keys come from TOML (via load_telephony_config);
# all secrets come from env/SSM: ASTERISK_ARI_URL, ASTERISK_ARI_USERNAME, 
# ASTERISK_ARI_PASSWORD, TELEPHONY_ACCESS_PIN, TELEPHONY_PASSPHRASE_WORDS.

telephony_cfg = load_telephony_config()  # Loaded, validated, frozen
ari_password = os.environ.get("ASTERISK_ARI_PASSWORD")  # From env, never TOML
```

---

## No Analog Found

Files / aspects with no close match in the existing codebase (planner should use RESEARCH.md patterns):

| File / Aspect | Reason |
|---|---|
| Asterisk PJSIP/ARI/Stasis configs | No existing Asterisk configuration in the codebase; use RESEARCH.md R3 skeletal examples + official Asterisk docs |
| ARI client (raw aiohttp wrapper) | No existing ARI client library in codebase; use RESEARCH.md R1 recommendation: hand-rolled ~100–150 line wrapper over aiohttp 3.14 (already pinned) |
| SIPp scenario + docker-compose Asterisk harness | No existing SIP testing infrastructure; use RESEARCH.md R4 pattern (SIPp XML scenarios + docker-compose skeleton with explicit port publishing) |
| GateProcessor's gate-timer/fail-closed timeout | Similar to SessionLifecycle's service timer (session.py) but scoped to gate window only; recommend one-off asyncio.sleep() task OR extend SessionLifecycle with gate_deadline concept (Open Question #5 in RESEARCH.md) |

---

## Metadata

**Analog search scope:** 
- `apps/voice/src/klanker_voice/` (pipeline, config, auth, session, WebRTC transport pattern)
- `apps/voice/src/klanker_voice/telephony/` from Phase 10 (types.py Protocol, transport.py, media.py)
- `apps/voice/src/klanker_voice/knowledge/` (KnowledgeRouterProcessor as FrameProcessor pattern)
- `apps/voice/tests/` (FakeTransport, fake AWS stubs, lifecycle tests)
- `apps/voice/server.py` (config loading, lifecycle wiring, transport construction)

**Files scanned:** 23 source + test files

**Pattern extraction date:** 2026-07-11

---

## PATTERN MAPPING COMPLETE

**Phase:** 11 - VoIP.ms Telephony — Local Asterisk Edge  
**Files classified:** 11  
**Analogs found:** 9/11 (exact-match or role-match patterns identified)

### Coverage

- **Files with exact analog:** 4 (CallIdentity, RtpMediaSession Protocol, config credential rejection, FakeTransport test pattern)
- **Files with role-match analog:** 5 (controller structure, GateProcessor, telephony config loader, telephony entrypoint, lifecycle tests)
- **Files with no analog:** 2 (Asterisk configs, SIPp/docker-compose harness — design documented in RESEARCH.md, patterns to follow per official Asterisk docs)

### Key Patterns Identified

- **Transport-specific module isolation:** Phase 11 follows the same `webrtc.py` pattern — all telephony code (controller, media session, transport) lives isolated in `telephony/` package; shared code (`call_runtime.py`, `pipeline.py`, `factories.py`, `session.py`) is byte-unchanged. The seam: `create_call_session(transport=TelephonyTransport(...), channel="pstn")`.

- **One idempotent close path:** All teardown (ARI hangup, bridge/media destruction, socket close, lifecycle release) funnels through `CallSession.close()` → `lifecycle.release()`, exactly once, reusing Phase 9's proven pattern. Registry guards and asyncio-lock coordination (Active Call locking) prevent double-close bugs.

- **Redaction boundary via processor design:** The `GateProcessor` implements D-05e's "never forwarded" guarantee structurally — by never calling `push_frame()` on pre-unlock transcription frames, the pre-unlock transcript never reaches the LLM/ledger/logs without explicit scrubbing logic downstream. This mirrors the existing `KnowledgeRouterProcessor` pattern of selective frame filtering.

- **Config credential rejection:** The existing `_CREDENTIAL_FIELD_RE` regex in `config.py` must be extended (D-09) to match `pin` and `passphrase` fields, so that `TELEPHONY_ACCESS_PIN` and `TELEPHONY_PASSPHRASE_WORDS` cannot be silently accepted in TOML — all §24 secrets come from env/SSM only.

- **Fake media for testing:** Phase 11's CI-automatable integration test reuses the existing `FakeTransport` pattern from `test_call_runtime.py` — the fake media/RTP session is injected at the same point `TelephonyTransport` would be, so `create_call_session` lifecycle assertions run against both real (Phase 11 manual proof) and synthetic (CI deterministic) paths identically.

### Ready for Planning

Pattern mapping complete. Planner can now reference these analogs to:
- Construct `AsteriskCallController` with concrete `ActiveCall` registry + ARI event dispatch (following `server.py`'s transport-specific entry pattern)
- Implement socket-backed `RtpMediaSession` satisfying Phase 10's Protocol (no changes to codec/transport)
- Insert `GateProcessor` into the pipeline between STT and router (following `KnowledgeRouterProcessor` structure)
- Extend `config.py`'s credential rejection + add `TelephonyConfig` dataclass loader (following existing `QuotaConfig` pattern)
- Write `test_telephony_lifecycle.py` reusing `FakeTransport` and fake AWS stubs
- Build `python -m klanker_voice.telephony.controller` entrypoint mirroring `server.py`'s config/factory construction

All Asterisk configs and SIPp integration harness follow RESEARCH.md R3–R4 (official Asterisk docs + Docker Desktop networking guidance).
