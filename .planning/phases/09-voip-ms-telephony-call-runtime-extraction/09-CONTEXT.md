# Phase 9: VoIP.ms Telephony — Call Runtime Extraction - Context

**Gathered:** 2026-07-11
**Status:** Ready for planning
**Source:** Derived from the authoritative telephony spec (`docs/superpowers/specs/2026-07-11-voipms-telephony-integration.md` §6, §19-A, §21, §22) — user chose "plan from spec" over discuss-phase; the spec is interview-validated and already carries the §2 codebase findings.

<domain>
## Phase Boundary

**This phase delivers (spec Phase A — refactor without behavior change):**
A transport-neutral shared call runtime, `apps/voice/src/klanker_voice/call_runtime.py`, that constructs, runs, and idempotently closes ONE live voice session around an arbitrary Pipecat `BaseTransport`. The existing WebRTC `/api/offer` path is converted to use it. This is a **behavior-preserving refactor** — the browser voice experience is byte-for-byte unchanged.

**Why now:** The whole telephony milestone (Phases 10–14) hinges on reusing the existing pipeline/lifecycle/quota wiring with a different transport at the edge. Today that wiring is embedded inside `server.py`'s WebRTC request handling. Extracting the reusable seam FIRST (with zero behavior change and full test coverage) de-risks every later phase and prevents duplicated lifecycle/pipeline code.

**Explicitly OUT of scope for this phase (spec §21.6):**
- NO SIP, Asterisk, ARI, RTP, PCMU/codec, jitter, or media-adapter code
- NO infrastructure (Terraform/Terragrunt, telephony-edge service)
- NO `pipeline.toml` `[telephony]` section, no telephony config/secrets
- NO new provider construction — `factories.py` remains the single source of STT/LLM/TTS/VAD creation (spec §22.2)
- NO large rewrite of `server.py` — extract ONLY the path needed to prevent duplicated lifecycle + pipeline wiring (spec §6 final note)

**Exit criterion (spec §19-A):** browser voice works exactly as before.
</domain>

<decisions>
## Implementation Decisions (LOCKED by spec §6 / §21 / §22)

- **D-01 — Target API shape (spec §6)**`call_runtime.py` exposes a narrow, transport-neutral API. Target shape from the spec:

```python
@dataclass
class CallSession:
    session_id: str
    worker: PipelineWorker
    lifecycle: SessionLifecycle
    async def run(self) -> None: ...
    async def close(self, reason: str) -> None: ...   # ONE idempotent close path

async def create_call_session(
    *, transport: BaseTransport, identity: CallIdentity, cfg: PipelineConfig,
    channel: Literal["webrtc", "pstn"], metadata: dict[str, str],
) -> CallSession: ...
```

The exact names/dataclass fields are the planner's to reconcile against what actually exists in the code (e.g. the current worker/identity types). `CallIdentity` may be introduced as a thin abstraction now OR deferred — but if introduced, keep it minimal (Phase 12/§11/§23 fills in phone→code→tier). The `channel` value is `"webrtc"` for this phase; `"pstn"` is reserved for later.

- **D-02 — What to extract (grounded in the current `server.py`, 506 lines)**The reusable, transport-neutral core to move into `call_runtime.py`:
- `_run_session(...)` (server.py ~L149) — builds ambience mixer, pipeline via `build_pipeline`, RTVI observer, teardown observers; wires warning/stop callbacks and the `on_client_connected`/`on_client_disconnected` greeting + teardown handlers.
- `_start_and_run_tracked_session(...)` (server.py ~L259) — the run/track wrapper.
- The `SessionLifecycle` construction + callback wiring currently inside `_negotiate_webrtc` → `_connection_callback` (server.py ~L347).

- **D-03 — What STAYS WebRTC-specific (do NOT generalize into the shared runtime)**- `_negotiate_webrtc` / `_connection_callback` (aiortc/ICE/`SmallWebRTCConnection` signaling).
- `_wire_connection_teardown(connection, lifecycle)` (server.py ~L280) — the reconnect-race teardown that maps `SmallWebRTCConnection` `closed`/`on_client_disconnected` to `lifecycle.release()`. Its race-handling comments (server.py ~L294–L324: heartbeat-lease lingering, `restart_pc` renegotiation, brand-new `session_id` on reconnect) are WebRTC-transport-specific. Preserve them EXACTLY where they are; the shared runtime must not absorb this logic. (Spec §5 `webrtc.py` note: introduce transport-specific modules rather than branching shared code.)
- `_extract_bearer_token`, `start_gate` HTTP wrapper, `offer`/`ice_candidate` FastAPI routes, SPA mount.

- **D-04 — Behavior that MUST be preserved verbatim (spec §21.5)**quota start-gate behavior · `SessionLifecycle` (service-timer hard-stop, ActiveSessions CloudWatch metric, ECS scale-in protection, accounting ticks) · observers (RTVI, LatencyReport, Teardown) · greeting behavior (`greet_now()` / `greet_first` guard from Phase 05.2) · warning + goodbye (`TTSSpeakFrame`) callbacks · reconnect grace behavior · RTVI processing · ambience-mixer behavior (`build_ambience_mixer`) · all existing metrics and teardown guarantees.

- **D-05 — Idempotent single close path (spec §6.10, §8)**`CallSession.close()` is idempotent — calling it twice, or a close racing a lifecycle hard-stop / worker failure / transport disconnect, releases the lifecycle exactly ONCE. This mirrors `SessionLifecycle`'s existing "one idempotent release path" guarantee; the runtime wraps it so both WebRTC teardown and (later) SIP hangup funnel through the same close.

- **D-06 — Telephony reconnect semantics noted, not implemented (spec §6.8, §8, §11)**For telephony a hangup is terminal (no browser-style reconnect grace). This phase does NOT implement that, but the extracted API must not bake the WebRTC reconnect-grace assumption into the shared core such that a future terminal-close transport can't opt out. Keep reconnect handling in the WebRTC wiring (D-03), not the runtime.

- **D-07 — Tests (spec §21.7)**Add focused tests proving: (a) transport-neutral construction of a `CallSession` around a fake/stub `BaseTransport` without WebRTC request handling; (b) `close()` is idempotent; (c) lifecycle `release()` occurs on worker termination AND on transport termination; (d) the existing WebRTC path behavior is unchanged (existing lifecycle/quota/greeting/teardown suites still green). Prefer reusing existing test fakes (`test_session.py` and the quota/greeting suites already stub much of this).

- **D-08 — Deliverable note (spec §21.9)**Produce a short architecture note describing the extracted seam and explicit notes about any existing coupling that prevented a clean extraction (e.g. anything in `_run_session` that turned out to be WebRTC-aware).

### Claude's Discretion
- Exact module/function/dataclass names and whether `CallIdentity` is introduced now vs. deferred to Phase 12.
- How `worker`/`PipelineWorker` is represented (match existing types).
- Whether the architecture note lives in the PLAN summary, a `docs/` note, or a module docstring.
- Test file layout under `apps/voice/tests/` (match existing conventions).
</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Authoritative spec
- `docs/superpowers/specs/2026-07-11-voipms-telephony-integration.md` — §6 (extract a shared call runtime — target API + 10 responsibilities), §19-A (Phase A definition + exit criterion), §21 (initial implementation prompt: read-list, preserve-list, test contract, stop-after-A), §22 (notes/decisions to preserve — `build_pipeline(cfg, transport)` is the center of the design).

### Code to read before planning (spec §21.1 read-list — verified present)
- `apps/voice/server.py` (506 lines — the extraction source: `start_gate` L109, `_run_session` L149, `_start_and_run_tracked_session` L259, `_wire_connection_teardown` L280, `_negotiate_webrtc` L328, `_connection_callback` L347, `offer` L408)
- `apps/voice/bot.py` (74 lines — pipeline/duplex config entry)
- `apps/voice/console.py` (52 lines — non-WebRTC session entry; a second existing caller of the pipeline worth checking as a reuse target)
- `apps/voice/src/klanker_voice/pipeline.py` (`build_pipeline(cfg, transport, ...)` — the transport-neutral seam that already accepts a `BaseTransport`)
- `apps/voice/src/klanker_voice/session.py` (`SessionLifecycle`, `TeardownObserver` — the single source of teardown/quota truth)
- `apps/voice/src/klanker_voice/webrtc.py` (166 lines — the pattern to follow: transport-specific module, NOT branched shared code)
- `apps/voice/src/klanker_voice/factories.py` (provider construction — must remain the single source, §22.2)
- Existing tests around lifecycle/quota/greeting/teardown: `apps/voice/tests/` (esp. `test_session.py`) — the regression net for "browser voice unchanged"

### Project instructions
- `.claude/CLAUDE.md` — stack pins (pipecat ~=1.5.0, Python 3.12), naming ("klanker-voice", never "voiceai"), and the GSD workflow enforcement.
</canonical_refs>

<specifics>
## Specific Ideas

Spec §21 is effectively the executable brief for this phase — follow it in order:
1. Read the §21.1 read-list (above).
2. Identify the smallest reusable unit that builds and owns one live voice session independently of WebRTC request handling.
3. Add `call_runtime.py` with the §6 narrow API around an arbitrary `BaseTransport`.
4. Convert the WebRTC server path to use it.
5. Preserve the §21.5 list verbatim (= D-04).
6. Add no SIP/Asterisk/RTP/codec/infra (= D-02 exclusions).
7. Add the §21.7 focused tests (= D-07).
8. Run the repo's documented format/type-check/test commands (`apps/voice/Makefile`, `pyproject.toml`).
9. Produce code + tests + the §21.9 architecture note + coupling notes.
10. **Stop after Phase A** and report exact files changed + test results.
</specifics>

<deferred>
## Deferred Ideas

Everything after spec Phase A — implemented in later roadmap phases, NOT here:
- **Phase 10 (spec B):** PCMU codec, RTP parser/packetizer, `TelephonyTransport`, resampling, interruption flush.
- **Phase 11 (spec C):** Asterisk configs, ARI/Stasis controller, external media + bridges, softphone call.
- **Phase 12 (spec D):** VoIP.ms subaccount + DID, phone→code→tier identity (§23), silent call-answer gate (§24).
- **Phase 13 (spec E):** physical payphone via its own ATA subaccount.
- **Phase 14 (spec F):** Terraform/Terragrunt telephony-edge, SSM secrets, alarms, failure routing, runbook.
- `CallIdentity`'s real phone→code→tier resolution (§11/§23) — only a minimal placeholder here at most.
</deferred>

---

*Phase: 09-voip-ms-telephony-call-runtime-extraction*
*Context gathered: 2026-07-11 — derived from telephony spec §6/§19-A/§21/§22 (user chose plan-from-spec)*
