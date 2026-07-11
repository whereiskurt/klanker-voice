# Phase 10: VoIP.ms Telephony — Offline Media Adapter - Pattern Map

**Mapped:** 2026-07-11
**Files analyzed:** 4 new source modules + 2–3 new test modules
**Analogs found:** 6 / 6 (100%) — all in-tree or in the pinned pipecat 1.5.0 install

---

## File Classification

| New File | Role | Data Flow | Closest Analog | Match Quality |
|----------|------|-----------|----------------|---------------|
| `telephony/__init__.py` | package barrel | n/a | `src/klanker_voice/knowledge/__init__.py` (barrel style) | role-match |
| `telephony/types.py` | config/dataclass + interface | transform (params) | `config.py` `@dataclass(frozen=True)` blocks + `pipecat.transports.base_transport.TransportParams` | exact (two analogs) |
| `telephony/media.py` | utility (codec) + transform (RTP) + I/O seam | streaming / batch | `pipecat.audio.utils.ulaw_to_pcm`/`pcm_to_ulaw` + `pipecat/serializers/twilio.py` | role-match (μ-law); no RTP analog (see No-Analog) |
| `telephony/transport.py` | transport | streaming / event-driven | `pipecat/transports/smallwebrtc/transport.py` (`SmallWebRTCTransport` + In/Out) | exact (structural) |
| `tests/test_telephony_media.py` | test | unit | `tests/test_tts_energy.py` / `tests/test_pronunciation_filter.py` (pure-function unit) | role-match |
| `tests/test_telephony_transport.py` | test | unit + integration | `tests/test_call_runtime.py` (`FakeTransport(BaseTransport)`, `_build_call_session`) | exact |
| `tests/test_telephony_config.py` *(if D-09)* | test | unit | `tests/test_config.py` / `tests/test_duplex_config.py` | role-match |

---

## Pattern Assignments

### `telephony/types.py` (config dataclass + `RtpMediaSession` interface)

**Analog A — frozen dataclass with defaults:** `apps/voice/src/klanker_voice/config.py` L78–124

```python
@dataclass(frozen=True)
class TtsConfig:
    provider: str
    model: str
    voice_id: str
    speed: float
    # trailing defaults keep direct constructors (tests, other callers) working
    stability: float = 0.4
    similarity_boost: float = 0.85
    style: float = 0.1
```

**Assignment — `TelephonyTransportParams` (D-09 defaults, spec §9 Packetization):** mirror this frozen-dataclass-with-trailing-defaults style. Do NOT subclass pipecat's pydantic `TransportParams` for these telephony knobs (that class is the audio-enable flags container the transport passes to its In/Out processors — see transport analog). Keep telephony clock/ptime/payload-type as a plain project dataclass, analogous to `DuplexConfig`/`GreenhouseConfig`:

```python
@dataclass(frozen=True)
class TelephonyTransportParams:
    clock_rate: int = 8000          # spec §9: PCMU 8 kHz clock
    packet_time_ms: int = 20        # spec §9: 20 ms packets
    samples_per_packet: int = 160   # spec §9: 8000 * 0.020
    payload_type: int = 0           # spec §9/D-03: PCMU default 0 but OVERRIDABLE — never hardcode downstream
```

**Analog B — the pydantic `TransportParams` the transport actually needs:** `pipecat/transports/base_transport.py` L25–90. This is the object handed to `BaseInputTransport`/`BaseOutputTransport`; the transport constructs it internally (see below). Relevant fields:

```python
audio_out_enabled: bool = False
audio_out_sample_rate: int | None = None
audio_in_enabled: bool = False
audio_in_sample_rate: int | None = None
```

**`RtpMediaSession` interface (D-04, spec §7/§8):** use a `typing.Protocol` (Claude's discretion allows ABC; Protocol matches the repo's structural-typing lean and keeps the offline impl decoupled). Shape it as an async read/write seam so Phase 11/C can drop a socket-backed impl with no codec/transport change:

```python
from typing import Protocol

class RtpMediaSession(Protocol):
    """Bidirectional RTP byte seam. Offline impl (Phase B) is in-memory;
    Phase 11/C swaps in a UDP/external-media impl WITHOUT touching codec or transport."""
    async def read_packet(self) -> bytes | None: ...   # one RTP datagram, or None on end
    async def write_packet(self, packet: bytes) -> None: ...
    async def close(self) -> None: ...
```

---

### `telephony/media.py` (PCMU codec + RTP parse/build + offline `RtpMediaSession`)

**Analog A — μ-law codec (D-02):** `pipecat/audio/utils.py` L170–211

```python
async def ulaw_to_pcm(ulaw_bytes, in_rate, out_rate, resampler):
    in_pcm_bytes = audioop.ulaw2lin(ulaw_bytes, 2)          # μ-law -> signed 16-bit PCM
    out_pcm_bytes = await resampler.resample(in_pcm_bytes, in_rate, out_rate)
    return out_pcm_bytes

async def pcm_to_ulaw(pcm_bytes, in_rate, out_rate, resampler):
    in_pcm_bytes = await resampler.resample(pcm_bytes, in_rate, out_rate)
    out_ulaw_bytes = audioop.lin2ulaw(in_pcm_bytes, 2)       # signed 16-bit PCM -> μ-law
    return out_ulaw_bytes
```

**Assignment (D-02):** pipecat itself uses stdlib `audioop.ulaw2lin`/`lin2ulaw`. Per D-02 the **recommended** path is an *explicit* μ-law table implementation (deterministic, testable against known vectors, and 3.13-proof since `audioop` is removed there). If the planner instead reuses `audioop`, it is permissible on the 3.12 pin but MUST be flagged as a 3.12-only coupling in the SUMMARY/module docstring (mirror Phase 9 D-08 coupling note). **Key structural takeaway from this analog:** the codec is a pure `bytes -> bytes` transform and resampling is a SEPARATE step composed around it — keep μ-law encode/decode free of any resampling so the codec unit tests hit exact known vectors.

**Analog B — 8 kHz μ-law framing + interruption "clear" seam:** `pipecat/serializers/twilio.py` (a telephony serializer; the closest in-tree μ-law↔pipecat-frame bridge, though it is WebSocket-framed, NOT RTP — the planner supplies RTP itself). Structural excerpts:

```python
# construction: one STATEFUL stream resampler per direction (mirror for D-06)
self._input_resampler = create_stream_resampler(clear_after_secs=...)   # -> SOXRStreamAudioResampler
self._output_resampler = create_stream_resampler(clear_after_secs=...)

# inbound: μ-law payload -> PCM -> InputAudioRawFrame WITH the real PCM sample rate (spec §9 Deepgram)
deserialized = await ulaw_to_pcm(payload, self._twilio_sample_rate, self._sample_rate, self._input_resampler)
audio_frame = InputAudioRawFrame(audio=deserialized, num_channels=1, sample_rate=self._sample_rate)

# interruption -> emit a transport-side "clear"/flush (spec §10 / D-07 analog)
elif isinstance(frame, InterruptionFrame):
    return json.dumps({"event": "clear", "streamSid": self._stream_sid})

# outbound: PCM at frame rate -> 8 kHz μ-law
elif isinstance(frame, AudioRawFrame):
    serialized = await pcm_to_ulaw(frame.audio, frame.sample_rate, self._twilio_sample_rate, self._output_resampler)
```

**Offline `RtpMediaSession` impl (D-04):** no analog exists in-tree (see No-Analog). Build an in-memory impl: `read_packet()` pops from a pre-loaded deque of synthetic/WAV-derived RTP datagrams; `write_packet()` appends to a captured list the tests assert on. NO socket. Shape it to satisfy the `types.py` Protocol exactly.

---

### `telephony/transport.py` (`TelephonyTransport(BaseTransport)`)

**Primary analog (structural, exact):** `pipecat/transports/smallwebrtc/transport.py` — the three-class shape `SmallWebRTCInputTransport(BaseInputTransport)` / `SmallWebRTCOutputTransport(BaseOutputTransport)` / `SmallWebRTCTransport(BaseTransport)`. This is the "transport-specific module, not branches in shared code" pattern (spec §2/§22, CONTEXT D-04/D-05). D-05 grants discretion to build on the `BaseInputTransport`/`BaseOutputTransport` primitives — this analog shows exactly how, and is strongly recommended over hand-rolled `FrameProcessor`s because the base classes already handle VAD/turn wiring, audio-task lifecycle, and interruption plumbing (spec §10: do NOT add a second VAD).

**Outer transport class + event registration + lazy input()/output()** — `smallwebrtc/transport.py` L919–995:

```python
class SmallWebRTCTransport(BaseTransport):
    def __init__(self, webrtc_connection, params, input_name=None, output_name=None):
        super().__init__(input_name=input_name, output_name=output_name)
        self._params = params
        self._client = SmallWebRTCClient(webrtc_connection, self._callbacks)
        self._input = None
        self._output = None
        # Only these registered event names may be wired by callers:
        self._register_event_handler("on_client_connected")
        self._register_event_handler("on_client_disconnected")

    def input(self) -> SmallWebRTCInputTransport:
        if not self._input:
            self._input = SmallWebRTCInputTransport(self._client, self._params, name=self._input_name)
        return self._input

    def output(self) -> SmallWebRTCOutputTransport:
        if not self._output:
            self._output = SmallWebRTCOutputTransport(self._client, self._params, name=self._input_name)
        return self._output
```

**Assignment for `TelephonyTransport.__init__` (spec §8 ctor):** signature is `__init__(self, *, call_id, media: RtpMediaSession, params: TelephonyTransportParams)`. Internally construct a pydantic `TransportParams(audio_in_enabled=True, audio_out_enabled=True, audio_in_sample_rate=<pipeline rate>, audio_out_sample_rate=<pipeline rate>)` to hand to the In/Out processors — the two `params` objects are distinct (telephony clock/codec knobs vs the pipecat audio-enable container). Register exactly `on_client_connected` / `on_client_disconnected` (spec §8 events; D-05 fire-once).

**Input transport — background receive task pushing audio frames** — `smallwebrtc/transport.py` L574–688:

```python
class SmallWebRTCInputTransport(BaseInputTransport):
    def __init__(self, client, params, **kwargs):
        super().__init__(params, **kwargs)          # BaseInputTransport.__init__(params, **kwargs)
        self._receive_audio_task = None

    async def start(self, frame: StartFrame):
        await super().start(frame)
        if self._initialized: return
        self._initialized = True
        await self.set_transport_ready(frame)        # <-- fire connect-ready here (D-05: on_client_connected once)
        if not self._receive_audio_task and self._params.audio_in_enabled:
            self._receive_audio_task = self.create_task(self._receive_audio())

    async def _receive_audio(self):
        async for audio_frame in self._client.read_audio_frame():
            if audio_frame:
                await self.push_audio_frame(audio_frame)   # BaseInputTransport.push_audio_frame(InputAudioRawFrame)

    async def stop(self, frame: EndFrame):
        await super().stop(frame)
        await self._teardown()                        # idempotent (also called from cancel/cleanup)
```

**Assignment (input path, spec §8 / D-05):** the telephony `_receive_audio` loop pulls RTP via `media.read_packet()` → RTP parse (seq/ts/jitter) → μ-law decode → PCM → stateful resample 8 kHz→pipeline-rate → construct `InputAudioRawFrame(audio=..., num_channels=1, sample_rate=<pipeline rate>)` (sample_rate MUST equal the resampled PCM rate — spec §9 Deepgram) → `await self.push_audio_frame(frame)`. Wire `_teardown()` from `stop`/`cancel`/`cleanup` idempotently exactly as the analog does.

**Output transport — `write_audio_frame` override is the whole sink contract** — `smallwebrtc/transport.py` L811–905 + `base_output.py` L241–250:

```python
class SmallWebRTCOutputTransport(BaseOutputTransport):
    def __init__(self, client, params, **kwargs):
        super().__init__(params, **kwargs)

    async def write_audio_frame(self, frame: OutputAudioRawFrame) -> bool:
        return await self._client.write_audio_frame(frame)   # <-- the ONE method a sink overrides

    async def start(self, frame): ...   # super().start + set_transport_ready
    async def stop(self, frame): ...    # super().stop + idempotent _teardown
```

`base_output.py` L241–250 shows the default no-op contract you override:

```python
async def write_audio_frame(self, frame: OutputAudioRawFrame) -> bool:
    return False
```

**Assignment (output path, spec §8 / D-06):** override `write_audio_frame(frame)`: stateful resample pipeline-rate→8 kHz → μ-law encode → 20 ms/160-sample framing → RTP packetize (seq++, ts += 160, stable SSRC, 16-bit wrap) → `await media.write_packet(rtp)`. NOTE `base_output`'s `MediaSender.handle_audio_frame` (L577–604) already resamples to `self._sample_rate` and re-chunks to `audio_chunk_size` before `write_audio_frame` is called — so set `audio_out_sample_rate=8000` on the pydantic params and you receive already-8 kHz chunks, OR keep it at pipeline rate and do the down-resample yourself in `write_audio_frame`. D-06 says resample ONCE at the boundary with one stateful streaming resampler per direction — pick one location and document it.

**Interruption / `flush_output_audio` (D-07, spec §10):** `base_output.py` L548–575 `MediaSender.handle_interruptions(self, _: InterruptionFrame)` is the built-in hook the base class already calls on an `InterruptionFrame`:

```python
async def handle_interruptions(self, _: InterruptionFrame):
    await self._cancel_clock_task()
    await self._cancel_video_task()
    if self._audio_queue.has_uninterruptible or self._mixer:
        self._audio_queue.reset()          # drain queued interruptible audio
    else:
        await self._cancel_audio_task()
        self._create_audio_task()
    ...
    await self._bot_stopped_speaking()
    # NOTE the base comment explicitly calls out "telephony serializers that
    # clear the playout buffer on interruptions" — that is exactly D-07.
```

**Assignment:** add `async def flush_output_audio(self) -> None` on `TelephonyTransport` (spec §10 signature) that clears the adapter's queued outbound RTP/μ-law bytes, and wire it to the interruption path — either by overriding the output processor's interruption handling to also flush the media session, or by having `write_audio_frame`/a small custom queue honor a flush flag. Keep the application output queue shallow (target 20–60 ms; spec §10). Do NOT add a second VAD — rely on the existing `InterruptionFrame` the pipeline already emits (Deepgram turn strategy stays authoritative, spec §10 / D-07).

**Frame types to import** — `pipecat/frames/frames.py`:

```python
InputAudioRawFrame  (L1295)  # SystemFrame + AudioRawFrame; ctor kwargs: audio, sample_rate, num_channels
OutputAudioRawFrame (L197)   # DataFrame + AudioRawFrame; .audio, .sample_rate, .num_channels, .num_frames (auto)
InterruptionFrame   (L1019)  # SystemFrame; empty payload — the barge-in signal
StartFrame / EndFrame        # passed to start()/stop() overrides
```

---

### `tests/test_telephony_transport.py` (transport unit + §19-B integration)

**Primary analog (exact):** `apps/voice/tests/test_call_runtime.py` — the `FakeTransport(BaseTransport)` stub, the `fake_aws` recording client, and `_build_call_session` are the reusable scaffolding for the D-10 offline pipeline-traversal proof.

**`FakeTransport(BaseTransport)` minimal-stub pattern** — `test_call_runtime.py` L67–82:

```python
class FakeTransport(BaseTransport):
    def __init__(self) -> None:
        super().__init__()
        self._register_event_handler("on_client_connected")
        self._register_event_handler("on_client_disconnected")
    def input(self) -> FrameProcessor:
        return FrameProcessor(name="fake-transport-input")
    def output(self) -> FrameProcessor:
        return FrameProcessor(name="fake-transport-output")
```

**§19-B exit-criterion integration harness** — `test_call_runtime.py` L116–152: build a real `CallSession` around a transport with `bypass_accounting=True` + `fake_aws`, then assert construction. For Phase 10 the transport is a **real `TelephonyTransport`** fed by an **offline `RtpMediaSession`** (WAV→PCMU or synthetic RTP):

```python
async def _build_call_session(make_config_file, *, transport):
    cfg = load_config(make_config_file())
    knowledge_cfg = load_knowledge_config()
    return await create_call_session(
        transport=transport, identity=CallIdentity(subject="tester", authenticated=True),
        gate_result=_gate_result(),  # bypass_accounting=True -> no real DynamoDB
        cfg=cfg, knowledge_cfg=knowledge_cfg, duplex_cfg=DuplexConfig(),
        quota_cfg=_quota_config(), channel="pstn", metadata={})   # <-- channel="pstn" here
```

**Recording-AWS fixture to reuse verbatim** — `test_call_runtime.py` L32–64 (`_FakeAwsClient` + `fake_aws` + `reset_active_count`). These plus `conftest.py`'s `make_config_file` / `stub_provider_keys` are the full offline-pipeline test rig — no live providers, no AWS, no network.

**Event-fires-once assertion pattern** — `test_call_runtime.py` L149–152 & L201–210: drive `transport._event_handlers["on_client_disconnected"].handlers` directly and count `put_metric_data` calls to prove single-release. For D-10 transport tests, similarly drive the media session and assert: (a) `on_client_connected` fired exactly once when media ready; (b) `on_client_disconnected` fired exactly once on terminal close; (c) `stop()` idempotent (call twice, assert one teardown).

---

### `tests/test_telephony_media.py` (codec + RTP unit)

**Analog:** `apps/voice/tests/test_pronunciation_filter.py` / `test_tts_energy.py` — pure-function unit tests with table-driven known vectors (no async harness needed for the codec; RTP parse/build is also pure). Structure: one test per D-10 codec case (decode vectors, encode vectors, 160-sample framing, incomplete-frame buffering, clipping, silence) and one per RTP case (seq increment, ts += 160, SSRC stable, duplicate handling, one-missing-packet silence insertion, 16-bit wraparound). Assert exact bytes against hand-computed μ-law/RTP vectors.

---

### `tests/test_telephony_config.py` (if D-09 config added)

**Analog:** `apps/voice/tests/test_config.py` / `test_duplex_config.py` — load a TOML `[telephony]` table, assert parsed defaults (clock 8000 / ptime 20 / samples 160 / payload_type 0) and overridability. Only add if D-09 loader is built this phase (optional; may defer to Phase 11).

---

## Shared Patterns

### 1. Transport-specific module, never branch shared code (spec §2/§22, D-04/D-05)
**Source:** `webrtc.py` (isolated WebRTC helper) + `SmallWebRTCTransport` (self-contained transport) + `call_runtime.create_call_session` accepting an arbitrary `BaseTransport`.
**Apply to:** all of `telephony/`. The shared runtime (`call_runtime.py`, `pipeline.py`, `factories.py`) is touched ZERO times; telephony is a new leaf module that produces a `BaseTransport`, exactly as `server.py` produces a `SmallWebRTCTransport`. Phase 9's SUMMARY (Next Phase Readiness) confirms: "only a new transport-specific caller analogous to `server.py`'s `_connection_callback`."

### 2. Stateful streaming resampler, one per direction (D-06, spec §9)
**Source:** `pipecat/audio/utils.py` L40–49 `create_stream_resampler(**kwargs) -> SOXRStreamAudioResampler`; `soxr_stream_resampler.py` L28–117.
```python
from pipecat.audio.utils import create_stream_resampler
self._input_resampler = create_stream_resampler(clear_after_secs=None)   # None recommended for telephony gaps
self._output_resampler = create_stream_resampler(clear_after_secs=None)
resampled = await self._input_resampler.resample(pcm_bytes, 8000, pipeline_rate)  # async, mono int16 only
```
**Gotcha (soxr_stream L90–94):** a single `SOXRStreamAudioResampler` instance raises if reused with *different* rate pairs — hence one instance per direction, each with a fixed rate pair. `clear_after_secs=None` avoids stale-history clears across telephony's irregular gaps (documented in the class).

### 3. `BaseInputTransport`/`BaseOutputTransport` subclass contract
**Source:** `smallwebrtc/transport.py` In/Out classes + `base_input.py` L195 `push_audio_frame`, `base_output.py` L241 `write_audio_frame`.
**Apply to:** telephony In/Out processors. Input side: override `start`/`stop`/`cancel`/`cleanup`, run a receive task that calls `self.push_audio_frame(InputAudioRawFrame(...))`. Output side: override `write_audio_frame(frame) -> bool` as the single sink; call `self.set_transport_ready(frame)` in `start`. Idempotent `_teardown()` shared by `stop`/`cancel`/`cleanup`.

### 4. Fire-once connect/disconnect events mapped to lifecycle (D-05/D-08)
**Source:** `SmallWebRTCTransport._register_event_handler(...)` + `_call_event_handler(...)` (L969–1029) and `call_runtime`'s transport-agnostic wiring (Phase 9 PATTERNS §3):
```python
@transport.event_handler("on_client_disconnected")
async def _on_client_disconnected(transport, client):
    await lifecycle.on_transport_disconnected()
```
**Apply to:** `TelephonyTransport` registers the same two event names so `create_call_session`'s existing wiring works UNCHANGED. Telephony close is terminal (D-08): `on_client_disconnected` maps to `lifecycle.on_transport_disconnected`, no browser-style reconnect grace (that stays WebRTC — Phase 9 D-03/D-06).

### 5. Offline test rig: fake AWS + stub keys + tmp config, no network (D-10)
**Source:** `test_call_runtime.py` L32–64 (`_FakeAwsClient`/`fake_aws`/`reset_active_count`) + `conftest.py` `make_config_file`/`stub_provider_keys`.
**Apply to:** every telephony test. `gate_result(bypass_accounting=True)` skips real DynamoDB; `fake_aws` records CloudWatch/ECS; `stub_provider_keys` lets `factories.py` construct clients without live creds. This is precisely the rig that makes the §19-B "audio traverses the real pipeline without SIP" proof runnable offline.

### 6. Frozen-dataclass config with trailing defaults (D-09)
**Source:** `config.py` `TtsConfig`/`GreenhouseConfig`/`DuplexConfig` (all `@dataclass(frozen=True)`, trailing-default fields for back-compat).
**Apply to:** `TelephonyTransportParams`. Keep it a plain project dataclass (not pydantic) — telephony behavior only, NEVER provider credentials/STT/LLM/TTS settings (spec §22.3 / D-09).

---

## No Analog Found

| Item | Role | Reason | Plan |
|------|------|--------|------|
| RTP RFC 3550 parser/packetizer | transform | pipecat's telephony serializers are WebSocket-framed (Twilio/Telnyx/Plivo) — none parse raw RTP headers; aiortc's RTP is buried in its media stack and not a reusable primitive here | Implement from RFC 3550 per D-03: 12-byte header (V/P/X/CC/M/PT, seq, timestamp, SSRC). Unit-test seq/ts/ssrc/wrap directly (spec §16 RTP). Read payload_type from `TelephonyTransportParams`, never hardcode. |
| Offline in-memory `RtpMediaSession` | I/O seam | New Phase-B abstraction; the socket-backed impl is deliberately deferred to Phase 11/C | In-memory deque (read) + capture list (write) implementing the `types.py` Protocol; no socket. Shaped so Phase 11 swaps a UDP impl with zero codec/transport change (D-04). |
| Explicit μ-law tables (if chosen over `audioop`) | utility | pipecat uses stdlib `audioop`; D-02 recommends explicit tables for 3.13-proofing | Implement G.711 μ-law encode/decode tables; validate against known vectors (spec §16 Codec). If `audioop` used instead, flag 3.12-only coupling in SUMMARY (Phase 9 D-08 style). |

---

## Metadata

**Analog search scope:**
- `apps/voice/server.py` (transport construction call site, L205–274)
- `apps/voice/src/klanker_voice/{webrtc,call_runtime,pipeline,config}.py`
- `apps/voice/tests/{conftest,test_call_runtime}.py`
- pipecat 1.5.0 install: `transports/base_transport.py`, `transports/base_input.py`, `transports/base_output.py`, `transports/smallwebrtc/transport.py`, `audio/resamplers/soxr_stream_resampler.py`, `audio/utils.py`, `serializers/twilio.py`, `frames/frames.py`

**Files scanned:** 15+ (in-tree + pinned pipecat)
**Analogs identified:** 6 exact/high-quality (5 structural + 1 codec), 3 documented no-analog gaps
**Pattern extraction date:** 2026-07-11

---

## Critical Notes

- **Two distinct `params` objects.** `TelephonyTransportParams` (project dataclass, telephony clock/codec knobs, spec §9) is NOT pipecat's pydantic `TransportParams` (audio-enable container handed to `BaseInputTransport`/`BaseOutputTransport`). The transport holds both; do not conflate them.
- **`sample_rate` metadata is load-bearing (spec §9 Deepgram).** The `InputAudioRawFrame` sample_rate MUST equal the resampled PCM rate, not 8000. Deepgram reads this field.
- **Resample once, at the boundary (D-06).** One `SOXRStreamAudioResampler` per direction, fixed rate pair, `clear_after_secs=None`. Reusing an instance with a different rate pair raises (soxr_stream L90–94).
- **Do not add a second VAD (spec §10 / D-07).** Reuse the pipeline's existing `InterruptionFrame`; the base output transport already has `handle_interruptions` that clears queued audio — `flush_output_audio` extends that to the media session, it does not replace turn detection.
- **Zero shared-runtime edits.** `call_runtime.py`/`pipeline.py`/`factories.py` are reused verbatim; telephony only adds a new leaf package + tests. `channel="pstn"` is the only new value threaded into the existing `create_call_session` signature.
