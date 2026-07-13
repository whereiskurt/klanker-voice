# Phase 10: VoIP.ms Telephony — Offline Media Adapter - Context

**Gathered:** 2026-07-11
**Status:** Ready for planning
**Source:** Derived from the authoritative telephony spec (`docs/superpowers/specs/2026-07-11-voipms-telephony-integration.md` §8, §9, §10, §16, §19-B, §22) — "plan from spec" path (the same approach used for Phase 9; the spec is interview-validated and carries the §2 codebase findings). Grounded against the live code: `pipeline.py` `build_pipeline(cfg, transport)`, the Phase 9 `call_runtime.py` seam, and pipecat 1.5.0's audio primitives.

<domain>
## Phase Boundary

**This phase delivers (spec Phase B — offline media adapter):**
A **transport-and-codec layer** that lets recorded/synthetic telephone audio traverse the *real* Klanker pipeline with **no SIP, no Asterisk, and no network sockets**. Concretely: a PCMU (G.711 μ-law) codec, an RTP parser/packetizer, an offline `RtpMediaSession` seam, and a `TelephonyTransport(BaseTransport)` (with input/output frame processors, idempotent start/stop, connect/disconnect events, and interruption flushing) — all reusing the Phase 9 `create_call_session` / `build_pipeline(cfg, transport)` seam that already accepts an arbitrary `BaseTransport`.

**Why now:** Phase 9 extracted a transport-neutral call runtime around an arbitrary `BaseTransport`. Phase 10 proves that seam by building a *telephony* transport and running 8 kHz μ-law audio through the genuine pipeline **offline** — before any Asterisk/SIP/infra complexity lands (Phases 11–14). Getting codec + RTP + resampling + interruption correct in isolation, fully unit-tested against known vectors, de-risks the live-call phases.

**Exit criterion (spec §19-B):** recorded telephone audio can traverse the real Klanker pipeline without SIP.

**Explicitly OUT of scope for this phase (spec §19-B / §7 / §13 / §15):**
- NO Asterisk configs, NO ARI/Stasis controller, NO bridges or external-media channels (Phase 11/C)
- NO SIP, NO UDP sockets, NO live network media, NO binding to an Asterisk external-media address (Phase 11/C)
- NO VoIP.ms subaccount/DID, NO phone→code→tier identity, NO call-answer security gate (Phase 12/D, spec §11/§23/§24)
- NO physical payphone/ATA (Phase 13/E)
- NO infrastructure — no Terraform/Terragrunt, no telephony-edge service, no SSM/alarms (Phase 14/F)
- NO new provider construction — `factories.py` stays the single source of STT/LLM/TTS/VAD (spec §22.4); `build_pipeline` is reused verbatim
- NO second VAD/endpointing system — the existing Deepgram turn strategy stays authoritative (spec §10; no double endpointing)
- NO application echo cancellation / AEC (spec §10 Echo — add only after measuring a real problem, i.e. much later)
</domain>

<decisions>
## Implementation Decisions (LOCKED by spec §8 / §9 / §10 / §16 / §19-B / §22)

- **D-01 — Module layout (spec §5).** New package `apps/voice/src/klanker_voice/telephony/` scoped to the OFFLINE subset of the §5 tree:
  - `types.py` — `TelephonyTransportParams` + `RtpMediaSession` interface + shared dataclasses/enums.
  - `media.py` — PCMU codec (μ-law decode/encode) + RTP parser/packetizer + the offline `RtpMediaSession` implementation.
  - `transport.py` — `TelephonyTransport(BaseTransport)`.
  - `__init__.py` — public exports.
  Do NOT create `controller.py` (Asterisk/ARI — Phase 11) or any `asterisk/` configs this phase. `config.py` is optional (see D-09). The planner may split `media.py` into `codec.py` + `rtp.py` if cleaner — its discretion.

- **D-02 — PCMU codec (spec §9, §16 "Codec" tests).** G.711 μ-law encode/decode between μ-law bytes and signed 16-bit PCM. MUST pass **known vectors** (decode + encode), plus 160-sample/20 ms framing, incomplete-frame buffering, clipping behavior, and silence behavior. **Discretion (recommended):** implement μ-law explicitly (small, deterministic, testable against vectors, and version-proof) rather than stdlib `audioop` — `audioop` is deprecated and **removed in Python 3.13**; the project pins 3.12 (where it still exists), so `audioop` is *permissible* but if used it must be noted as a 3.12-only dependency in the coupling note.

- **D-03 — RTP parser/packetizer (spec §8 input/output, §9 packetization + jitter/loss, §16 "RTP" tests).** RFC 3550 header parse + build. Preserve: sequence number increments, timestamp increments by **160** per 20 ms packet, **stable SSRC**, and 16-bit sequence/timestamp **wraparound**. The RTP **payload type must be read from the negotiated/external-media format, not hardcoded** — 0 is the common PCMU default but is not assumed (spec §9). The adapter must tolerate, without crashing: minor reordering, duplicate packets, a single missing packet (**silence insertion for one missing 20 ms packet is acceptable for the MVP**), and a timestamp discontinuity at startup.

- **D-04 — Offline `RtpMediaSession` seam (spec §7 "Why not direct SIP", §8 ctor `media: RtpMediaSession`).** Define an `RtpMediaSession` interface that `TelephonyTransport` consumes (`__init__(*, call_id, media, params)`), and provide an **offline/in-memory implementation** for Phase B: it is fed synthetic RTP (or WAV-derived PCMU) on the read side and captures packetized RTP on the write side — **NO UDP socket, NO Asterisk external-media address**. The seam must be shaped so Phase 11/C can drop in a socket-backed implementation **without touching the codec or the transport**. This is the telephony analog of the webrtc.py isolation pattern (spec §2/§22: transport-specific module, not branches in shared code).

- **D-05 — `TelephonyTransport(BaseTransport)` (spec §8).** Pipecat-compatible transport exposing `input() -> FrameProcessor`, `output() -> FrameProcessor`, `async start()`, `async stop()`. Preserve the `build_pipeline` contract (graph begins at `transport.input()`, ends at `transport.output()` — spec §2).
  - **Input path (spec §8):** RTP PCMU payload → sequence/jitter handling → μ-law decode → signed-16-bit PCM → resample 8 kHz → pipeline input rate → `InputAudioRawFrame` **with correct sample-rate metadata** (spec §9 Deepgram input: the frame's sample rate MUST match the actual PCM).
  - **Output path (spec §8):** `OutputAudioRawFrame` → resample pipeline rate → 8 kHz → μ-law encode → 20 ms framing → RTP packetization → `RtpMediaSession`.
  - **Events (spec §8):** emit `on_client_connected` **once** when the media path is ready (this lets the existing greeting registration / `register_greet_first` fire); emit `on_client_disconnected` **exactly once** on terminal close. **Discretion:** whether to build on pipecat's `BaseInputTransport`/`BaseOutputTransport` primitives or compose custom `FrameProcessor`s — follow the `SmallWebRTCTransport` pattern.

- **D-06 — Stateful streaming resampler at the 8 kHz boundary (spec §9 ElevenLabs output).** Resample **once, at the transport boundary**, with a **stateful streaming resampler** — one instance per direction per call — NOT a fresh per-frame resample (that causes boundary artifacts + clock drift). **Recommended:** reuse pipecat 1.5.0's `SOXRStreamAudioResampler` (`pipecat.audio.resamplers.soxr_stream_resampler`) rather than hand-rolling; the planner confirms the exact import/class name.

- **D-07 — Interruption flushing (spec §10).** Add `async def flush_output_audio(self) -> None` on the transport and wire it to the relevant pipecat interruption frame/processor event (this pipecat version exposes `InterruptionFrame`). On caller barge-in: (1) existing pipecat interruption frames stop downstream speech; (2) the adapter's queued outbound audio is flushed; (3) the application output queue is kept **shallow — target 20–60 ms**; (4) RTP resumes with live response audio on the next turn. Do **NOT** add a second VAD/endpointing system — the Deepgram turn strategy remains authoritative (spec §10; the repo's Flux double-endpointing guard must not be undone).

- **D-08 — Lifecycle/quota + provider reuse, terminal-close semantics (spec §2, §6.8, §22).** Phase B is **offline**, so it does NOT wire a live `SessionLifecycle` into `server.py` — but the transport's `on_client_disconnected` must map cleanly to the lifecycle hooks the Phase 9 runtime already wires (`lifecycle.on_transport_disconnected`), and telephony close is **terminal** (no browser-style reconnect grace — that stays a WebRTC concern per Phase 9 D-03/D-06). This phase PROVES a `TelephonyTransport` can be constructed and run through `build_pipeline(cfg, transport)` offline; it constructs **no real providers** (factories.py stays the single source, spec §22.4) and adds **no provider settings**.

- **D-09 — `pipeline.toml [telephony]` (spec §14, §22.3).** Introduce a `TelephonyTransportParams` dataclass (in `types.py` or an optional `telephony/config.py`) with sane defaults: clock 8000 Hz, packet time 20 ms, samples/packet 160, payload type default 0 but overridable. Wiring an operator-facing `pipeline.toml [telephony]` **loader** is **OPTIONAL this phase and may be deferred to Phase 11/C** (when a real Asterisk call needs tunable values). If added, it is **transport/media behavior ONLY** — never provider credentials or parallel STT/LLM/TTS settings (spec §22.3). **Discretion:** add now (minimal, defaults-only) vs defer.

- **D-10 — Tests (spec §16 Unit + §19-B exit criterion).** Cover:
  - **Codec:** decode known vectors, encode known vectors, 160-sample framing, incomplete-frame buffering, clipping, silence.
  - **RTP:** sequence increment, timestamp increment by 160, SSRC stability, duplicate-packet handling, one-missing-packet handling, wraparound.
  - **Transport:** RTP input emits the correct Pipecat audio frame (with right sample rate); Pipecat output emits PCMU RTP; interruption flushes output; `stop()` is idempotent; disconnect event fires exactly once.
  - **Phase-B exit proof:** a recorded/synthetic telephone-audio source (WAV → PCMU, or synthetic RTP) traverses the **real** `build_pipeline` offline (no SIP/Asterisk) and produces output audio — the §19-B exit criterion, expressed as an offline integration test. Reuse existing fakes/conftest where possible.
  - Test files (spec §5): `test_telephony_media.py`, `test_telephony_transport.py` (+ `test_telephony_config.py` if D-09 config is added; lifecycle-integration tests that need Asterisk are **Phase 11**, not here).

### Claude's Discretion
- Exact module split (`media.py` monolith vs `codec.py` + `rtp.py`) and class/function names.
- μ-law implementation strategy (explicit tables — recommended/version-proof — vs stdlib `audioop` on 3.12).
- Whether `RtpMediaSession` is a `typing.Protocol` or an ABC.
- Exact resampler class/import (recommend `SOXRStreamAudioResampler`); confirm against installed pipecat 1.5.0.
- Whether `TelephonyTransport` builds on `BaseInputTransport`/`BaseOutputTransport` or composes custom `FrameProcessor`s.
- Whether to add the `pipeline.toml [telephony]` loader now (defaults-only) or defer to Phase 11 (D-09).
- Where the architecture/coupling note lives (SUMMARY + module docstring, mirroring Phase 9 D-08).
</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Authoritative spec
- `docs/superpowers/specs/2026-07-11-voipms-telephony-integration.md` — §8 (`TelephonyTransport` conceptual API + input/output/event paths), §9 (audio details: narrowband, Deepgram sample-rate metadata, ElevenLabs downsample-once, packetization, jitter/loss), §10 (turn-taking/barge-in: interruption flush, 20–60 ms queue, no second VAD, echo), §16 (unit test matrix: codec/RTP/transport), §19-B (Phase B definition + exit criterion), §22 (notes/decisions to preserve — `build_pipeline(cfg, transport)` is the center; conversion belongs at the transport boundary; factories stay single-source).

### Code to read before planning/implementing (verified present)
- `apps/voice/src/klanker_voice/call_runtime.py` (Phase 9 seam — `create_call_session(*, transport, ...)` / `CallSession` around an arbitrary `BaseTransport`; the thing this phase's transport plugs into)
- `apps/voice/src/klanker_voice/pipeline.py` (`build_pipeline(cfg, transport, ...)` — graph starts at `transport.input()`, ends at `transport.output()`; the seam to preserve)
- `apps/voice/src/klanker_voice/webrtc.py` (the transport-specific-module pattern to mirror — do not branch shared code)
- `apps/voice/server.py` (how `SmallWebRTCTransport` + `TransportParams` are constructed by the caller — the analog the telephony caller will follow in Phase 11)
- `apps/voice/src/klanker_voice/factories.py` (provider construction — MUST remain the single source; telephony builds NO providers)
- `apps/voice/src/klanker_voice/config.py` (`PipelineConfig` etc. — for param types)
- `apps/voice/tests/conftest.py`, `apps/voice/tests/test_call_runtime.py`, `apps/voice/tests/test_session.py` (existing fakes + the `FakeTransport(BaseTransport)` stub pattern to reuse/extend)
- pipecat 1.5.0 installed modules: `pipecat.audio.resamplers.soxr_stream_resampler`, `pipecat.transports.base_input` / `base_output`, `pipecat.frames.frames` (`InputAudioRawFrame`, `OutputAudioRawFrame`, `InterruptionFrame`)

### Project instructions
- `.claude/CLAUDE.md` — stack pins (pipecat ~=1.5.0, Python 3.12), naming ("klanker-voice", never "voiceai"), GSD workflow enforcement.
- `.planning/phases/09-voip-ms-telephony-call-runtime-extraction/09-01-SUMMARY.md` — the Phase 9 seam + its documented couplings (what the telephony caller must supply: transport construction + params).
</canonical_refs>

<specifics>
## Specific Ideas

Phase B is a self-contained, offline, heavily-unit-tested media layer. Suggested build order:
1. Read the code list above — especially the `build_pipeline` seam and the Phase 9 `FakeTransport` test stub.
2. `types.py`: `TelephonyTransportParams` (clock/ptime/samples-per-packet/payload-type defaults) + `RtpMediaSession` interface.
3. `media.py`: μ-law codec (vectors), then RTP parse/build (seq/ts/ssrc/wrap), then the offline `RtpMediaSession`.
4. `transport.py`: `TelephonyTransport(BaseTransport)` — input/output processors, the two resamplers (stateful, one per direction), `start()`/`stop()` (idempotent), connect/disconnect events (fire-once), `flush_output_audio()`.
5. Tests per D-10, ending with the §19-B offline "audio traverses the real pipeline without SIP" proof.
6. Run the repo's format/type-check/test commands (`apps/voice/Makefile`, `pyproject.toml`); keep the full existing suite green.
7. Produce code + tests + a short architecture/coupling note (mirror Phase 9 D-08).
8. **Stop after Phase B** — no Asterisk/ARI/SIP/sockets/infra.
</specifics>

<deferred>
## Deferred Ideas

Everything after spec Phase B — later roadmap phases, NOT here:
- **Phase 11 (spec C):** Asterisk configs, ARI/Stasis controller, bridges + external media, socket-backed `RtpMediaSession`, local SIP softphone call.
- **Phase 12 (spec D):** VoIP.ms subaccount + DID, Asterisk registration, DID routing, phone→code→tier identity (§11/§23), call-answer security gate (§24).
- **Phase 13 (spec E):** physical payphone via its own ATA subaccount; ATA gain/DTMF/echo tuning.
- **Phase 14 (spec F):** Terraform/Terragrunt telephony-edge, SSM secrets, alarms/dashboards, rolling-deploy/failure routing, load/concurrency test, runbook.
- Application-level echo cancellation / AEC (spec §10 — only after measuring a real problem).
- The operator-facing `pipeline.toml [telephony]` loader, if D-09 defers it.
</deferred>

---

*Phase: 10-voip-ms-telephony-offline-media-adapter*
*Context gathered: 2026-07-11 — derived from telephony spec §8/§9/§10/§16/§19-B/§22 (plan-from-spec), grounded against live code + pipecat 1.5.0*
