# Phase 11: VoIP.ms Telephony — Local Asterisk Edge - Context

**Gathered:** 2026-07-11
**Status:** Ready for planning
**Source:** Derived from the authoritative telephony spec (`docs/superpowers/specs/2026-07-11-voipms-telephony-integration.md` §7, §12, §13, §14, §16, §17, §18, §19-C, §24, §25) — same "plan from spec" spine used for Phases 9 and 10, but this is a genuine `/gsd-discuss-phase` pass because Phase C is the first telephony phase to leave the hermetic-offline world and hit real infrastructure (Asterisk, ARI, live UDP sockets, a local dev harness). Five gray areas the spec leaves open were decided interactively (D-05..D-09 below). Grounded against the live code: the Phase 9 `call_runtime.py` seam (`create_call_session(*, transport, identity, cfg, channel, metadata)`), the Phase 10 `telephony/` package (`RtpMediaSession` Protocol = `read_packet`/`write_packet`/`close`, `TelephonyTransport`, PCMU codec, RTP parser/packetizer, `TelephonyTransportParams`).

<domain>
## Phase Boundary

**This phase delivers (spec Phase C — local Asterisk edge):**
A **local SIP softphone call holds a full conversation with the existing Klanker agent through Asterisk** — with the silent §24 answer-gate in front of it. Concretely, this phase adds:

1. **Asterisk configs** (`apps/voice/asterisk/`): `pjsip.conf`, `ari.conf`, `extensions.conf` (+ `README.md`) — a narrow, **inbound-only** Stasis dialplan where an inbound call reaches ONLY the Stasis app (no other extensions/features/dial contexts); ARI authenticated and private-network-only.
2. **ARI/Stasis controller** (`telephony/controller.py`): consumes ARI events; on `StasisStart` accepts only the expected inbound context, normalizes ANI/DID, allocates a media session, creates the **External Media** channel + a **mixing bridge**, attaches both channels, constructs a Klanker `CallSession` via the Phase 9 `create_call_session(...)` seam, and starts the worker. On `ChannelDestroyed`/hangup it closes the `CallSession` (single idempotent close → `lifecycle.release()` exactly once), tears down bridge + external channel + RTP socket, and removes the registry entry — no leaked resources.
3. **`ActiveCall` registry** keyed by original Asterisk channel ID (fields per §13: SIP channel ID, external-media channel ID, bridge ID, RTP media session, Klanker `CallSession`, caller ID, DID, creation timestamp, closed flag/lock).
4. **Socket-backed `RtpMediaSession`**: a UDP/RTP implementation that satisfies the Phase 10 `RtpMediaSession` Protocol (`read_packet`/`write_packet`/`close`) and binds to the Asterisk external-media address — dropped in **without touching the Phase 10 codec or `TelephonyTransport`** (the whole point of the Phase 10 seam).
5. **The silent §24 answer-gate** (see D-05): on answer the agent stays dark (no greeting/LLM/TTS) until the caller proves access via DTMF PIN **or** a spoken order-independent 4-word passphrase; unlock grants tier and runs the normal `greet_now()` → LLM → TTS path; fail-closed static goodbye + hangup on timeout.
6. **`[telephony]` config loader** (`config.py` / `telephony/config.py`, deferred from Phase 10 D-09) for the §14 non-secret keys + the §24 gate keys, with SSM/env secret names (§14).
7. A **standalone telephony entrypoint** (see D-08) + a **local docker-compose Asterisk dev harness** (see D-07).

**Exit criterion (spec §19-C):** a local SIP softphone reaches the Stasis app, **hears the greeting (not clipped), converses, interrupts the agent, and hangs up cleanly** — through the silent gate.

**Why now:** Phase 9 extracted the transport-neutral runtime; Phase 10 proved a `TelephonyTransport` runs 8 kHz μ-law through the real pipeline offline. Phase 11 makes it a real (local) call by adding the Asterisk edge and the socket-backed media session, plus the §24 security gate that is the primary DoS/abuse control (§25.D) — all **before** any VoIP.ms subaccount, public DID, or cloud infra (Phases 12–14).

**Explicitly OUT of scope for this phase (spec §19-C / §19-D / §15 / §23):**
- NO VoIP.ms subaccount, NO public DID, NO Asterisk↔VoIP.ms registration/trunking, NO cellular-network test (Phase 12/D).
- NO caller-ID → access-code → baseline-tier resolution (the §11/§23 mint path) — only the §24 PIN/passphrase → tier grant lands here via a minimal identity seam (D-05a). Real DID + caller-ID identity is Phase 12.
- NO physical payphone / ATA (Phase 13/E).
- NO cloud infrastructure — NO Terraform/Terragrunt, NO isolated `telephony-edge` AWS deploy, NO SSM provisioning, NO alarms/dashboards, NO fail2ban/security-group/TLS-SRTP edge hardening (Phase 14/F). Secrets in Phase 11 are read from local env for the dev harness; the SSM wiring is Phase 14. Security *posture* (inbound-only, narrow dialplan, ARI private-only, fail-closed) is enforced in configs/code now; the *cloud enforcement* is Phase 14.
- NO new STT/LLM/TTS provider construction — `factories.py` remains the single source (§22); `build_pipeline(cfg, transport)` is reused verbatim.
- NO second VAD/endpointing system — the existing Deepgram turn strategy stays authoritative (do not undo the Flux double-endpointing guard).
- NO application echo cancellation / AEC (spec §10 — only after measuring a real problem).
- NO outbound calling anywhere — no outbound dialplan context, ever (§25.A).
</domain>

<decisions>
## Implementation Decisions

Spec-locked decisions (D-01..D-04) carry the §7/§13/§14 design forward unchanged; D-05..D-09 are the five gray areas decided in this discussion.

### Media & call-control mechanism (LOCKED by spec §7 / §13)
- **D-01 — Asterisk mechanism (§7).** PJSIP for SIP, **ARI/Stasis** for call control, **ARI External Media** for audio exchange. Call sequence: SIP INVITE → PJSIP endpoint → dialplan answers → channel enters Stasis → controller creates External Media channel → caller + external-media channel join a mixing bridge → Klanker sends/receives RTP → hangup closes the Klanker session. **Not** direct SIP in the app (§7 "Why not direct SIP" — Asterisk owns registration/auth/SDP/NAT/RTP/DTMF/codec/hostile-traffic concerns; Klanker owns conversational media + agent behavior).
- **D-02 — Controller responsibilities + `ActiveCall` (§13).** `AsteriskCallController` with `on_stasis_start` / `on_channel_destroyed` handlers and a `calls: dict[str, ActiveCall]` registry keyed by original Asterisk channel ID. `ActiveCall` fields exactly per §13 (above). Structured logs carry the call ID; **never** log SIP passwords, auth headers, or full PINs/passphrases.
- **D-03 — Socket-backed `RtpMediaSession` drops into the Phase 10 seam (§8 / Phase 10 D-04).** Implement the UDP/RTP `RtpMediaSession` behind the existing Phase 10 Protocol (`async read_packet() -> bytes | None`, `async write_packet(packet)`, `async close()`) so the codec (`media.py`) and `TelephonyTransport` (`transport.py`) are **untouched**. Bind/advertise the external-media address per the ARI External Media contract; read the RTP **payload type from the negotiated/external-media format**, not hardcoded (Phase 10 D-03). This is the telephony analog of the `webrtc.py` isolation pattern — a transport-specific module, not branches in shared code.
- **D-04 — Greeting readiness ordering (§12).** For a telephone call: answer channel → establish external media → start the Klanker worker → emit `on_client_connected` **once** → greet. Do **not** greet before the bridge + media receiver are ready (first words clip). Add ~100–250 ms readiness margin **only if tests show clipping** — no arbitrary multi-second sleep. Preserve the canonical `greet_now()` path (persona/greeting consistency); pre-rendered PSTN greeting clip is a future improvement, not this phase. **Note:** with the §24 gate (D-05) the greeting fires **on unlock**, not on answer.

### The §24 silent answer-gate (gray area — DECIDED)
- **D-05 — Full §24 gate lands in Phase 11, with a *minimal* identity seam.** On answer the agent **stays silent** — no greeting, no LLM turn, no TTS — until the caller proves access. The pipeline is built only far enough to run **STT (+ receive DTMF)** during the gate; OUTPUT is suppressed (no `greet_now()`, no LLM, no TTS). An optional single neutral tone on answer is allowed only if a fully dead line proves confusing in testing.
  - **D-05a — §23 boundary (gray area — DECIDED: "PIN/passphrase→tier only; minimal identity seam").** Phase 11 implements the §24 gate **and** the "which PIN/phrase → which tier" grant on unlock (e.g. correct passphrase → `kph-tier`), realized through a **minimal `CallIdentity` seam** — the abstraction Phase 9 deliberately left as a thin placeholder (Phase 9 D-01). It does **NOT** pull forward the §11/§23 caller-ID → access-code → baseline-tier resolution or the real DID/mint path — those stay Phase 12 (untestable on a local softphone with no real caller-ID anyway). Keep the identity abstraction minimal; do not build the mint-path resolver here.
  - **D-05b — Gate mode (gray area — DECIDED: "'either' — both factors, either unlocks").** Implement **both** factors with default `gate_mode = "either"`, and give **both** test coverage:
    - **Factor 1 — DTMF PIN**, within the window: caller enters a keypad PIN; Asterisk surfaces digits via ARI DTMF events; the **controller** compares to `TELEPHONY_ACCESS_PIN` — handled at the Asterisk/controller layer, **never in the LLM**.
    - **Factor 2 — spoken 4-word passphrase**, all 4 within the window: STT runs during the gate; a matcher checks whether **all 4 secret words** (`TELEPHONY_PASSPHRASE_WORDS`) are present in the normalized transcript — **order-independent set-membership on lower-cased tokens**, done **outside the LLM**; words may be buried across a couple of natural sentences. This is the primary/recommended mode.
  - **D-05c — On unlock:** grant the caller's tier (via the D-05a seam), redact/drop the pre-unlock transcript, THEN run the normal path — `greet_now()` → LLM → TTS. The greeting fires **here** (consistent with §12 "don't greet before ready").
  - **D-05d — Fail-closed (§17 / §18 / §25):** if neither factor lands within `gate_window_seconds` (default 10 s, configurable), play a short **static** goodbye/unavailable message and **hang up** — never leave a silent open call burning PSTN charges. STT runs during the gate; the LLM/TTS never engage until unlock, so the expensive turn loop is built **only after a pass**.
  - **D-05e — Never-recognized / never-echoed guarantees (HARD requirements, §24 / §18):**
    - The 4 words live ONLY in env/secret storage (SSM in prod, local env for this phase's harness). **Never** placed in the LLM context (before OR after unlock) — never in greeting/persona/system prompt, never in any provider request.
    - **Redact before anywhere:** the pre-unlock transcript contains the secret words → it is NOT forwarded to the LLM and NOT written to the transcript ledger or logs verbatim. Drop the pre-unlock transcript entirely (or scrub the 4 words) before it leaves the gate. Post-unlock conversation flows normally.
    - The matcher never logs which words matched, the words themselves, or the raw unlock utterance. Log only `unlocked{method:"passphrase"|"dtmf", call_id}`. Never surface a per-word "3 of 4 matched" oracle to the caller (silent until all 4 land).
  - **D-05f — Distinct from the `greenhouse` router keyword.** The §24 gate is a SEPARATE security/auth layer verified outside the LLM (decides ACCESS/tier); the existing `greenhouse` keyword is a persona/topic unlock inside the knowledge router (decides PERSONA). They may share vocabulary and a phrase MAY do both, but the mechanisms stay distinct.

### ARI client library (gray area — DECIDED: "Research it in planning")
- **D-06 — ARI client is a research-decided pin.** The spec says only "a small controller that consumes ARI events" and pins no library; CLAUDE.md requires explicit, justified pins. The **phase-researcher evaluates current (2026) options** — `asyncari`, `aioari`, and raw `aiohttp` + `websockets` against the ARI REST + events WebSocket — for asyncio-loop compatibility with pipecat 1.5.0, maintenance health, and hostile-input robustness, then pins one **with rationale** in RESEARCH.md/PLAN.md. `websockets` and `aiohttp` are already in the stack (raw is a viable no-new-dependency fallback). Add the chosen pin to `apps/voice/pyproject.toml` (voice extras).

### Local dev harness + exit-criterion proof (gray area — DECIDED: "docker-compose + fake-media integration test")
- **D-07 — docker-compose Asterisk + automated fake-media integration test, plus a documented manual real-pipeline softphone run.**
  - Ship a **docker-compose Asterisk** service (using the D-01..D-02 configs) + a scripted SIP test client under `apps/voice/asterisk/` (with `README.md`).
  - **Automated integration test (CI-runnable):** drive `SIP client → Asterisk → fake Klanker media transport` (§16 integration tier) — asserting the §16/§17 lifecycle + gate behaviors deterministically against a **fake** media/pipeline (no real Deepgram/ElevenLabs, no live keys in CI).
  - **Documented manual proof:** the §19-C exit criterion (real pipeline, test credentials — greeting not clipped, converse, interrupt, clean hangup, through the gate) is run **manually via a local softphone** and documented in the harness README + the phase SUMMARY. The researcher confirms how far Asterisk-in-CI is practical vs. what stays manual; if a fuller CI path proves cheap, it may extend the automated tier (planner discretion), but the deterministic fake-media integration test is the required CI artifact.
  - Preserve the Phase 10 hermetic offline tests; keep the full existing suite green.

### Controller process boundary (gray area — DECIDED: "Standalone telephony entrypoint")
- **D-08 — Standalone telephony entrypoint; do not touch `webrtc.py`/the browser `server.py`.** The ARI controller runs as **its own local process** — a new entrypoint (e.g. `python -m klanker_voice.telephony.controller`, or an `apps/voice/telephony_server.py`) — run alongside the docker-compose Asterisk. This mirrors the eventual §15 `telephony-edge` deploy isolation and keeps the WebRTC path untouched. It reuses `factories.py` / `build_pipeline` / `call_runtime.py` in-process within the telephony entrypoint (shared **code**, separate **process** from the browser voice service). Do not wire the controller into the FastAPI browser `server.py`.

### `[telephony]` config + secrets (LOCKED by spec §14 / §24)
- **D-09 — `[telephony]` loader lands now (Phase 10 D-09 deferral resolved).** Add the non-secret `[telephony]` block loader to `config.py` (or `telephony/config.py`): `enabled`, `provider = "voipms"`, `edge = "asterisk-ari"`, `codec = "pcmu"`, `sample_rate = 8000`, `packet_ms = 20`, `max_concurrent_calls = 1`, `answer_timeout_seconds = 15`, `hangup_on_pipeline_error = true`, plus the §24 gate keys `require_gate = true`, `gate_mode = "either"`, `gate_window_seconds = 10`. Transport/media/gate **behavior only** — never provider credentials or parallel STT/LLM/TTS settings (§22.3).
  - **Secrets** (env for this phase's harness; SSM is Phase 14): `ASTERISK_ARI_URL`, `ASTERISK_ARI_USERNAME`, `ASTERISK_ARI_PASSWORD`, `TELEPHONY_ACCESS_PIN`, `TELEPHONY_PASSPHRASE_WORDS`. (`VOIPMS_SIP_*` are Phase 12 — not needed for a local softphone.) The SIP password is consumed by Asterisk, **not** passed into the Klanker Python process. Extend `config.py`'s credential-name rejection to cover the new secret-looking fields so they can never be surfaced as tunables.

### Claude's Discretion
- Exact module/class/function names; whether the socket-backed `RtpMediaSession` lives in `media.py` or a new `telephony/rtp_socket.py`.
- Exact entrypoint filename/shape for D-08 (module `__main__` vs `telephony_server.py`).
- Where the gate STT-only pipeline is assembled (a gate-scoped `build_pipeline` variant vs a suppression flag on the existing worker) — subject to D-05e (secrets never reach the LLM) and D-05d (LLM/TTS built only after unlock).
- Exact ARI External Media parameters (transport/format/direction/connection mode) — confirm against the pinned ARI client + installed Asterisk version.
- Which §16 lifecycle assertions become automated CI tests vs manual, within D-07's required-artifact floor.
- Whether the architecture/coupling note lives in the SUMMARY + module docstrings (mirror Phase 9 D-08 / Phase 10).

### Reviewed Todos (not folded)
- **`2026-07-06-private-transcription-ledger-s3-batch-athena.md` — "Private transcription ledger — S3 batch + Athena"** (matched on keywords `private`/`klanker`/`session`, score 0.6): NOT folded. It's a separate transcript-ledger feature, not Asterisk-edge work. It touches Phase 11 only via D-05e's rule that the **pre-unlock transcript must never reach the ledger/logs** — that constraint is captured in D-05e, but building the ledger itself is out of scope here.
</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Authoritative spec
- `docs/superpowers/specs/2026-07-11-voipms-telephony-integration.md` —
  - §5 (proposed repo layout: `asterisk/` configs + `telephony/controller.py` + `telephony/config.py`; test files `test_telephony_config.py` / `test_telephony_lifecycle.py`; keep Asterisk process separate from `apps/voice`),
  - §7 (media boundary: PJSIP + ARI/Stasis + External Media; call sequence; "why not direct SIP"),
  - §12 (greeting ordering — answer → external media → worker → `on_client_connected` → greet; no clipping; canonical `greet_now()`),
  - §13 (`AsteriskCallController` responsibilities + `ActiveCall` shape + logging rules),
  - §14 (`[telephony]` non-secret config keys + SSM/env secret names; SIP password consumed by Asterisk, not Klanker),
  - §16 (test matrix: Lifecycle unit tests + integration tier "SIP → Asterisk → fake Klanker media transport" then "→ real pipeline test creds"),
  - §17 (failure behavior: unavailable-before-answer, pipeline-fails-mid-call, Asterisk-loses-media timeout, quota-denied → static message + hang up, no open PSTN charges),
  - §18 (security requirements — narrow dialplan, ARI private/authenticated, gate outside the LLM),
  - §19-C (Phase C definition + exit criterion "local SIP call has a full conversation"),
  - §24 (the silent answer-gate — quiet-gate mechanics, DTMF + 4-word passphrase, unlock→tier→greet, fail-closed, never-recognized/never-echoed guarantees),
  - §25 (hostile/DEF CON hardening — inbound-only/no outbound context, concurrency=1, gate-as-DoS-control, no caller-driven tool use; the *cloud* enforcement pieces are Phase 14).

### Code to read before planning/implementing (verified present)
- `apps/voice/src/klanker_voice/call_runtime.py` — Phase 9 seam: `create_call_session(*, transport, identity, cfg, channel, metadata)` / `CallSession` with the **one idempotent close path**; telephony's `channel="pstn"` caller. Hangup must funnel through this single close.
- `apps/voice/src/klanker_voice/telephony/types.py` — Phase 10 `RtpMediaSession` **Protocol** (`read_packet`/`write_packet`/`close`) + `TelephonyTransportParams` (clock 8000 / ptime 20 / 160 samples / payload_type overridable). The socket-backed session (D-03) implements this Protocol.
- `apps/voice/src/klanker_voice/telephony/transport.py` — Phase 10 `TelephonyTransport(BaseTransport)` (input/output processors, resamplers, `flush_output_audio()`, fire-once connect/disconnect) — **reused unchanged**.
- `apps/voice/src/klanker_voice/telephony/media.py` — Phase 10 PCMU codec + RTP parser/packetizer — **reused unchanged** (payload type read from format, not hardcoded).
- `apps/voice/src/klanker_voice/pipeline.py` — `build_pipeline(cfg, transport, ...)` (graph starts at `transport.input()`, ends at `transport.output()`); the gate's STT-only assembly must respect this seam.
- `apps/voice/src/klanker_voice/session.py` — `SessionLifecycle` / `TeardownObserver` (single source of teardown/quota truth; ARI hangup → `release()` exactly once; quota-denied leaves no bridge).
- `apps/voice/src/klanker_voice/webrtc.py` — the transport-specific-module isolation pattern to mirror (do NOT branch shared code).
- `apps/voice/server.py` — how `SmallWebRTCTransport` + params + greeting/lifecycle are constructed by the caller; the analog the telephony entrypoint follows — but the telephony controller is a **separate process** (D-08), not wired into this FastAPI app.
- `apps/voice/src/klanker_voice/factories.py` — provider construction; MUST remain the single source (telephony builds NO providers).
- `apps/voice/src/klanker_voice/config.py` — `PipelineConfig` + the credential-name rejection to extend (D-09).
- Knowledge router / `greenhouse` handling (the persona-unlock path) — to keep the §24 gate mechanism **distinct** from the router keyword (D-05f).
- `apps/voice/tests/conftest.py`, `apps/voice/tests/test_call_runtime.py`, `apps/voice/tests/test_session.py`, and the Phase 10 `test_telephony_transport.py` / `test_telephony_media.py` — existing fakes + the `FakeTransport`/fake-media patterns to reuse for the §16 fake-media integration test (D-07).

### Phase artifacts (direct dependencies)
- `.planning/phases/09-voip-ms-telephony-call-runtime-extraction/09-CONTEXT.md` + `09-01-SUMMARY.md` — the runtime seam, its couplings, and the minimal-`CallIdentity` note the D-05a seam extends.
- `.planning/phases/10-voip-ms-telephony-offline-media-adapter/10-CONTEXT.md` + `10-01-SUMMARY.md` / `10-02-SUMMARY.md` — the media/transport/`RtpMediaSession` seam the socket session (D-03) plugs into, and the interruption-flush wiring the barge-in exit test exercises.

### Project instructions
- `.claude/CLAUDE.md` — stack pins (pipecat ~=1.5.0, Python 3.12; new ARI pin per D-06), naming ("klanker-voice", never "voiceai"), GSD workflow enforcement, budget/security constraints (public entry wired to metered APIs must be quota-gated).
</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- **Phase 9 `create_call_session(...)` / `CallSession`** — the telephony controller constructs a session with `channel="pstn"` and a transport built from `TelephonyTransport` + the new socket `RtpMediaSession`; the single idempotent `close()` is where ARI hangup lands.
- **Phase 10 `telephony/` package** — codec, RTP, `TelephonyTransport`, `TelephonyTransportParams`, and the `RtpMediaSession` Protocol are all reused **verbatim**; Phase 11's only media addition is the socket-backed session behind the same Protocol.
- **`SessionLifecycle` / `TeardownObserver`** — reused for quota gate (concurrency=1), hard-stop timer, and the exactly-once release the §16 lifecycle tests assert against ARI hangup / worker failure / simultaneous hangup+timeout.
- **`factories.py` + `build_pipeline`** — the STT-during-gate assembly reuses these; no provider is duplicated.
- **Existing test fakes** (`FakeTransport`, conftest stubs) — the basis for the §16 fake-media integration test (D-07).

### Established Patterns
- **Transport-specific module isolation** (`webrtc.py`) — Phase 11 adds `telephony/controller.py` + the socket session as isolated modules; no branching of shared code.
- **One idempotent close path** (Phase 9 D-05) — ARI `ChannelDestroyed` + hard timeout + worker failure all funnel to `CallSession.close()` → `release()` once.
- **Fire-once connect/disconnect events** (Phase 10) — greeting registration hangs off `on_client_connected` (now fired **on unlock**, D-05c).
- **Secrets never surfaced as tunables** — `config.py` credential-name rejection pattern extended to the new §14/§24 secret fields (D-09).

### Integration Points
- **Asterisk ARI (events WebSocket + REST)** ↔ the standalone controller process (D-06, D-08).
- **Asterisk External Media (UDP/RTP)** ↔ the socket-backed `RtpMediaSession` (D-03).
- **The §24 gate** ↔ STT output (passphrase matcher) + ARI DTMF events (PIN) + the D-05a identity/tier seam + the redaction boundary before LLM/ledger/logs.
- **`pipeline.toml [telephony]`** ↔ `config.py` loader (D-09), consumed by the controller/entrypoint (D-08).
</code_context>

<specifics>
## Specific Ideas

Suggested build order (Phase C is real-infra but still local — get the gate + teardown right before the live round-trip):
1. Read the code list above — especially the Phase 9 close seam, the Phase 10 `RtpMediaSession` Protocol, and `webrtc.py` isolation.
2. `telephony/config.py` (or extend `config.py`): the `[telephony]` loader (D-09) + credential-name rejection for the new secrets.
3. Asterisk configs (`asterisk/pjsip.conf` / `ari.conf` / `extensions.conf` + `README.md`): inbound-only Stasis dialplan, ARI private/authenticated (D-01) + docker-compose harness (D-07).
4. Socket-backed `RtpMediaSession` (D-03) behind the Phase 10 Protocol.
5. Pin + wire the ARI client (D-06, research-decided); `telephony/controller.py` — `on_stasis_start` / `on_channel_destroyed`, external-media channel + mixing bridge, `ActiveCall` registry (D-02), hangup → single close → `release()` once (§16 lifecycle).
6. The §24 gate (D-05): STT-only gate pipeline with output suppressed; DTMF-PIN (controller/ARI) + 4-word passphrase (STT matcher, order-free, outside LLM); redaction boundary; unlock → tier grant (minimal `CallIdentity` seam) → `greet_now()` → LLM → TTS; fail-closed goodbye+hangup.
7. Standalone telephony entrypoint (D-08); do not touch `webrtc.py`/browser `server.py`.
8. Tests: §16 Lifecycle unit tests + the fake-media integration test (`SIP → Asterisk → fake Klanker media transport`) as the CI artifact; keep the Phase 10 offline suite + full existing suite green.
9. **Manual §19-C proof:** local softphone → real pipeline (test creds) → greeting-not-clipped, converse, interrupt, clean hangup, through the gate — documented in the harness README + SUMMARY.
10. Architecture/coupling note (mirror Phase 9 D-08 / Phase 10).
11. **Stop after Phase C** — no VoIP.ms subaccount/DID, no §23 caller-ID resolution, no cloud infra.

Constraint reminders the planner must honor: inbound-only / no outbound context ever (§25.A); concurrency = 1; gate verified **outside** the LLM; the 4 passphrase words + PIN never enter LLM context and the pre-unlock transcript never reaches the LLM/ledger/logs (D-05e).
</specifics>

<deferred>
## Deferred Ideas

Later roadmap phases — NOT here:
- **Phase 12 (spec D):** dedicated `klanker-pbx` VoIP.ms subaccount + public DID, Asterisk↔VoIP.ms registration/routing, the §11/§23 caller-ID → access-code → baseline-tier resolution (the mint path), cellular-network test, provider security restrictions.
- **Phase 13 (spec E):** physical payphone via its own `payphone-ata` subaccount; ATA gain/DTMF/echo tuning.
- **Phase 14 (spec F):** Terraform/Terragrunt isolated `telephony-edge` deploy, SSM secret provisioning, alarms/dashboards (`ActivePstnCalls`, gate-fail rate, ANY-outbound, balance drop), failure routing, security-group/TLS-SRTP/fail2ban edge hardening, load/concurrency test, operations runbook.
- **Pre-rendered PSTN greeting clip** (§12) — after the canonical `greet_now()` path is proven on PSTN.
- **Application-level echo cancellation / AEC** (§10) — only after measuring a real problem.
- **Private transcription ledger — S3 batch + Athena** (`2026-07-06-private-transcription-ledger-...`): separate feature; Phase 11 only enforces the "pre-unlock transcript never written to the ledger" constraint (D-05e), not the ledger build.

### Reviewed Todos (not folded)
- **Private transcription ledger — S3 batch + Athena** — reviewed (keyword match, score 0.6), not folded: out of scope for the Asterisk edge; its only Phase-11 touchpoint (pre-unlock redaction) is already captured in D-05e.

</deferred>

---

*Phase: 11-voip-ms-telephony-local-asterisk-edge*
*Context gathered: 2026-07-11 — `/gsd-discuss-phase` (5 gray areas decided) atop the telephony spec §7/§12/§13/§14/§16/§17/§18/§19-C/§24/§25, grounded against the Phase 9 runtime seam + Phase 10 telephony package*
