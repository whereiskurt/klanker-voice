# Phase 11: VoIP.ms Telephony — Local Asterisk Edge — Research

**Researched:** 2026-07-11
**Domain:** Asterisk ARI/Stasis call control, External Media RTP, pipecat 1.5.0 gate-pipeline design, local SIP dev harness
**Confidence:** MEDIUM-HIGH (ARI mechanics and pipecat internals verified against installed package/official docs; local Docker-networking specifics and exact CI-automatable scope are MEDIUM — flagged as Open Questions)

## Summary

**R1 recommendation: no new dependency — build the ARI client on raw `aiohttp` (already pinned `3.14.1`), using `aiohttp.ClientSession` for both the REST calls and the events WebSocket (`ClientSession.ws_connect()`).** The only two Python ARI libraries the *official* Asterisk docs list are `ari-py` (synchronous, Digium-authored, wraps the legacy `swagger-py` code generator — wrong concurrency model for a process sharing pipecat's asyncio loop) and `asyncari` (async, but built on `anyio`/`trio` + `httpx` + `asyncwebsockets` + `asyncswagger11` — a chain of niche, thinly-maintained packages whose own top-level entry point (`anyio.run`/`trio.run`) does not compose cleanly with an already-running `asyncio` loop owned by pipecat's `WorkerRunner`). `panoramisk` is **AMI** (the legacy TCP Manager protocol), not ARI — wrong protocol entirely, ruled out. The ARI REST surface Phase 11 actually needs is six calls (answer, create externalMedia channel, create bridge, add channels to bridge, hangup/destroy — DTMF arrives as an *event*, not a call) plus one long-lived events WebSocket — genuinely small enough that a ~150-line hand-rolled `AriClient` wrapper over `aiohttp` is less total surface area, and less risk, than inheriting `asyncari`'s dependency chain. This also matches CLAUDE.md's "minimal, justified pins" discipline and reuses two packages already proven in this codebase.

**Three biggest implementation risks (details in each Rx section and Landmines):**
1. **The gate (D-05) cannot be built as a second, separately-run pipeline reusing the same `TelephonyTransport`.** Pipecat's `BaseInputTransport.stop()`/`cancel()`/`cleanup()` — all invoked when a `Pipeline`'s owning `WorkerRunner` receives `EndFrame`/`CancelFrame` — call straight through to `TelephonyInputTransport._teardown()`, which **closes the RTP media session** (verified in the installed `transport.py`, this phase's own Phase-10 code). Ending a "gate-only" `PipelineWorker` to hand off to a second "full" `PipelineWorker` over the *same* `TelephonyTransport`/`RtpMediaSession` would tear down the live UDP socket mid-call. R5 below gives the concrete alternative: **one persistent `Pipeline`/`PipelineWorker` for the whole call, with a `GateProcessor` inline** (same architectural slot as the existing `KnowledgeRouterProcessor`) that swallows frames until unlock.
2. **macOS Docker Desktop networking for SIP/RTP.** `network_mode: host` only works reliably on Linux (Docker Desktop 4.34+ added an opt-in, still-labeled-experimental host-networking mode for Mac/Windows). The dependable approach is explicit `ports:` publishing plus `host.docker.internal` for the Klanker-side RTP listener — detailed in R4/Landmines.
3. **ARI External Media's `connection_type=client` means Asterisk always initiates the UDP flow toward `external_host`** — Klanker's socket must be a passive UDP listener bound *before* the externalMedia channel is created, and must learn Asterisk's actual send-from `(ip, port)` from the first received datagram (symmetric RTP) rather than assume a fixed peer address, exactly mirroring the reasoning already encoded in Phase 10's jitter-tolerant depacketizer.

---

## R1 — ARI client library pin

### Candidates evaluated

| Library | Latest PyPI release | Repo activity | Protocol | asyncio-native? | Notes |
|---|---|---|---|---|---|
| `ari-py` (github.com/asterisk/ari-py) | legacy, Digium-maintained | low | ARI (REST+WS) | **No** — synchronous, built on `requests` + `swagger-py`/`swaggerpy` | `[CITED: docs.asterisk.org ARI-Libraries]` lists this as the "official" one, but it is blocking I/O — would need a thread executor to coexist with pipecat's loop, adding complexity for no benefit. |
| `asyncari` (PyPI, `M-o-a-T/asyncari`) | **0.20.6**, uploaded 2025-05-25 `[VERIFIED: PyPI registry]` | 42 stars, 27 forks, 5 open issues, last push 2025-06-19 `[VERIFIED: GitHub API]` | ARI (REST+WS) | Built on **`anyio`** (dual asyncio/trio backend) + `httpx` + `asyncwebsockets` + `asyncswagger11` `[VERIFIED: PyPI registry — requires_dist]` | Formerly "AsyncARI"; docs.asterisk.org's ARI-Libraries page lists it as the async option `[CITED: docs.asterisk.org]`. Its own dependency chain (`asyncwebsockets`, `asyncswagger11`) is itself thin/low-download-count. Typical `asyncari` usage patterns start from `anyio.run(main)`/`trio.run(main)` at the top level (per the 2019 Asterisk-community "async ARI frontend" announcement thread `[CITED: community.asterisk.org/t/python-new-async-ari-frontend/80193]`) — embedding that inside a process whose event loop is already owned by pipecat's `WorkerRunner`/`asyncio.run` requires using `anyio`'s asyncio-compatible entry points carefully; this is a real but avoidable integration cost. |
| `aioari` (PyPI, `M-o-a-T/aioari`) | 0.10.2, uploaded **2020-11-15** `[VERIFIED: PyPI registry]` | 29 stars, repo last pushed 2024-04-13 (no new PyPI release since 2020) `[VERIFIED: GitHub API]` | ARI (REST+WS) | asyncio-native (predecessor to `asyncari`), but wraps `aioswagger11`, whose own last PyPI release was **2018-04-13** `[VERIFIED: PyPI registry]` | Async clone of `ari-py`; still wraps the Swagger-generated API shape (D-06's explicit "wraps Swagger/ari-py legacy" flag). Stale dependency chain three levels deep. |
| `panoramisk` (PyPI) | 1.4, uploaded 2021-08-05 `[VERIFIED: PyPI registry]` | 160 stars, last pushed 2024-03-15 `[VERIFIED: GitHub API]` | **AMI**, not ARI `[VERIFIED: project README — "manager TCP/IP API"]` | asyncio-native | Ruled out: this is the Asterisk **Manager Interface** (AMI) — a completely different TCP protocol (actions/events, no REST, no Stasis, no externalMedia). Resolves the CONTEXT.md "note: AMI vs ARI — clarify" flag: `panoramisk` cannot do what D-01/D-02 need. |
| Raw `aiohttp` (3.14.1, already pinned) + `websockets` (16.0, already pinned) | current | actively maintained, huge install base | N/A — build ARI calls directly | Native asyncio, zero new deps | **Recommended.** |

### Recommendation: raw `aiohttp`, no new dependency

The ARI surface this phase needs is exactly:
- `POST /ari/channels/{id}/answer`
- `POST /ari/channels/externalMedia` (create the external-media channel)
- `POST /ari/bridges` (create a `mixing` bridge)
- `POST /ari/bridges/{id}/addChannel` (attach caller channel + external-media channel)
- `DELETE /ari/channels/{id}` (hangup)
- `GET /ari/events?app=<name>&api_key=<user>:<pass>&subscribeAll=true` — the one long-lived WebSocket, delivering `StasisStart`, `ChannelDtmfReceived`, `ChannelDestroyed`, etc. as JSON text frames.

`aiohttp.ClientSession` already does both jobs: `session.post(...)`/`session.delete(...)` for the REST calls, and `session.ws_connect(...)` for the events stream (aiohttp's own WS client, no need to pull in the separate `websockets` package for this — `websockets` stays installed only because pipecat's ElevenLabs service already depends on it transitively). A minimal `AriClient` wrapper (Basic-Auth `aiohttp.BasicAuth`, one `asyncio.Task` reading `ws.receive_json()` in a loop, dispatched by `event["type"]`) is ~100–150 lines and has zero third-party surface beyond what's already vetted in this repo. This also sidesteps `asyncari`'s `anyio`/`trio` compatibility question entirely — the controller runs on plain `asyncio`, same as every other module in `apps/voice`.

**Exact pin line:** none — `aiohttp>=3.14` is already declared transitively (via `pipecat-ai[...]`) and directly usable; if the planner wants an explicit direct dependency (rather than relying on the transitive pin) for clarity, add to `pyproject.toml`:
```toml
dependencies = [
    "pipecat-ai[anthropic,deepgram,runner,soundfile,webrtc]~=1.5.0",
    "pyjwt[crypto]~=2.13.0",
    "boto3>=1.42",
    "aiohttp>=3.14,<4",  # ARI REST + events-WebSocket client (D-06) -- already a transitive pin
]
```

If, during implementation, the raw-`aiohttp` approach proves materially harder than expected (e.g. ARI's WS reconnect/heartbeat semantics turn out gnarlier than anticipated), the fallback path is `asyncari` pinned to `asyncari~=0.20.6` — but per D-06's stated bias ("if raw aiohttp+websockets is competitive, favor it"), start with raw `aiohttp` and only reach for `asyncari` if a concrete blocker appears.

---

## R2 — Asterisk External Media → socket-backed `RtpMediaSession`

### External Media channel creation (verified against docs.asterisk.org)

Endpoint: `POST /ari/channels/externalMedia` `[CITED: docs.asterisk.org/Development/.../External-Media-and-ARI/]`

| Param | Value for Phase 11 | Notes |
|---|---|---|
| `app` | the Stasis app name (e.g. `klanker`) | required |
| `external_host` | `"<klanker-rtp-bind-ip>:<port>"` (e.g. `host.docker.internal:40000` in the compose dev harness) | required — this is where Asterisk will **send** RTP |
| `format` | `"ulaw"` | required — matches the Phase 10 PCMU codec |
| `encapsulation` | `"rtp"` (default) | **currently the only supported value** `[CITED: docs.asterisk.org]` |
| `transport` | `"udp"` (default) | **currently the only supported value** |
| `connection_type` | `"client"` (default) | **currently the only supported value** — means *Asterisk* initiates the connection to `external_host`, i.e. Asterisk is always the active UDP sender; Klanker is always the passive listener. `"server"` is documented but not actually usable today. |
| `direction` | `"both"` (default) | **currently the only supported value** — bidirectional RTP over the one socket |
| `channelId` | optional, generate one if you want a predictable ID for the `ActiveCall` registry | |

### Who binds / initiates — the exact handshake

Because `connection_type` only supports `"client"`, **Asterisk always dials out to `external_host:port`.** Concretely:

1. Klanker's socket-backed `RtpMediaSession` **must bind and start listening on its local UDP port BEFORE** the controller calls `POST /channels/externalMedia` with that port baked into `external_host` — otherwise the first RTP packets Asterisk sends arrive at a closed port and are silently dropped (UDP has no handshake/retry).
2. The channel returned by `externalMedia` is then `addChannel`'d into the same mixing bridge as the caller's SIP channel — Asterisk's bridging engine starts forwarding audio in both directions the moment both channels are in the bridge.
3. **Symmetric RTP / first-packet source learning is required, not optional.** Even in a fully local dev setup, the *actual* source `(ip, port)` Asterisk sends from is not guaranteed to be a value you can predict ahead of time (Asterisk itself picks the ephemeral RTP-engine port per `rtp.conf`'s configured range, and in the docker-compose harness the container's internal bridge-network IP is what will appear as the packet's source, not `127.0.0.1`). The socket-backed `RtpMediaSession.write_packet()` must send to **whatever `(ip, port)` the most recently-received datagram came from** (learned via `asyncio.DatagramProtocol.datagram_received(data, addr)`), not a value baked in at construction time. This exactly mirrors the reasoning Phase 10's `RtpDepacketizer` already documents for reordering/loss tolerance — the socket layer adds the "which peer" question on top.

### Recommended asyncio UDP approach

Use `loop.create_datagram_endpoint()` with a small `asyncio.DatagramProtocol` subclass:

```python
class _AsteriskRtpProtocol(asyncio.DatagramProtocol):
    def __init__(self) -> None:
        self._queue: asyncio.Queue[bytes] = asyncio.Queue()
        self._peer: tuple[str, int] | None = None
        self._transport: asyncio.DatagramTransport | None = None

    def connection_made(self, transport: asyncio.DatagramTransport) -> None:
        self._transport = transport

    def datagram_received(self, data: bytes, addr: tuple[str, int]) -> None:
        self._peer = addr  # symmetric-RTP source learning -- updated on every packet
        self._queue.put_nowait(data)

    def error_received(self, exc: Exception) -> None:
        pass  # never raise out of the protocol callback (T-10-style hostile-input posture)
```

`SocketRtpMediaSession` (satisfying the exact `types.RtpMediaSession` Protocol) wraps this: `read_packet()` awaits `self._queue.get()` (returning `None` once a `close()`-triggered sentinel or the transport itself closes — mirroring `OfflineRtpMediaSession`'s end-of-stream contract exactly), `write_packet()` calls `self._transport.sendto(packet, self._peer)` (a no-op / logged warning if `self._peer` is still `None` — no datagram received yet), `close()` calls `self._transport.close()` and is idempotent (matches the Protocol's "safe to call more than once" contract already implemented in `OfflineRtpMediaSession`).

`create_datagram_endpoint(..., local_addr=(bind_host, bind_port))` is the standard, dependency-free asyncio primitive for this — no new library needed (matches the "Don't Hand-Roll" instinct in reverse: this genuinely is a ~40-line stdlib job, not a place to reach for a heavier RTP library).

### Payload type — read from the wire, not hardcoded

Because `format=ulaw` maps to RFC 3551's **static** payload type assignment (PCMU = 0 — no dynamic PT negotiation happens for External Media; there is no SDP offer/answer in this internal UnicastRTP-channel mechanism), the payload type Asterisk sends is deterministically 0 for `ulaw`. The existing Phase 10 `parse_rtp()` already extracts `payload_type` per-datagram from the wire (never hardcoded on the read side), and `TelephonyTransportParams.payload_type` (default `0`, overridable) already governs what Klanker stamps on **outbound** packets — Phase 11 needs no change to either; just confirm at integration-test time that Asterisk's observed inbound `payload_type` byte is in fact `0` and that `TelephonyTransportParams.payload_type` is left at its default (do not hardcode a literal `0` anywhere new — reuse the existing field, per D-03).

---

## R3 — Asterisk configs for a narrow inbound-only Stasis dialplan

**Recommended Asterisk version: 22 (current LTS, `22.10.1` as of 2026-06-25) `[VERIFIED: asterisk.org downloads page / GitHub releases]`.** There is no Digium/Sangoma-official Docker image; the community `andrius/asterisk` image (actively maintained, semantic tags `latest`/`stable`/`22`, version-pinned tags like `22.10.1_debian-trixie`) is the most current, best-documented option for the docker-compose harness `[CITED: hub.docker.com/r/andrius/asterisk]`. Certified Asterisk (`22.8-cert3`) is the lower-churn alternative if the planner prefers a slower-moving base, but community-image availability favors the vanilla 22.x line for local dev.

### `http.conf` (required — ARI rides Asterisk's built-in HTTP server)

```ini
[general]
enabled=yes
bindaddr=127.0.0.1   ; or 0.0.0.0 inside the compose network -- NEVER a public interface (§18/§25.C)
bindport=8088
```

### `ari.conf` (authenticated, private-only)

```ini
[general]
enabled=yes
pretty=yes
allowed_origins=       ; leave empty -- no browser CORS access needed, ARI is server-to-server only

[klanker]
type=user
password=${ARI_PASSWORD}   ; sourced from ASTERISK_ARI_PASSWORD (env, D-09) -- never in this file in plaintext for anything beyond local dev
password_format=plain
read_only=no
```

Security posture (§18/§25.C, D-01/D-02): `bindaddr=127.0.0.1` (or a private compose-network address) plus a strong password is what makes ARI "private-network only, authenticated" — there is no separate ARI-specific network ACL directive; the enforcement is entirely "don't bind/expose the HTTP port publicly" plus the security-group rule that will land in Phase 14.

### `pjsip.conf` (one local softphone endpoint + transport)

```ini
[transport-udp]
type=transport
protocol=udp
bind=0.0.0.0:5060

[softphone](!)   ; template
type=endpoint
context=from-klanker-inbound   ; narrow inbound-only context (D-01)
disallow=all
allow=ulaw
direct_media=no

[softphone-aor](!)
type=aor
max_contacts=1

[softphone-auth](!)
type=auth
auth_type=userpass
password=${SOFTPHONE_SIP_PASSWORD}
username=softphone

[dev-softphone](softphone)
aors=dev-softphone
auth=dev-softphone-auth

[dev-softphone](softphone-aor)

[dev-softphone-auth](softphone-auth)
```

(Skeleton only — the planner/implementer should adapt template names/inheritance to house style; the load-bearing facts are: `disallow=all` + `allow=ulaw` to match the Phase 10 PCMU codec exactly, and `context=from-klanker-inbound` pointing at the narrow dialplan below.)

### `extensions.conf` (inbound-only Stasis dialplan — no other extensions/features reachable)

```ini
[from-klanker-inbound]
exten => _X.,1,NoOp(Klanker inbound call)
 same => n,Answer()
 same => n,Stasis(klanker)
 same => n,Hangup()

; NO [outbound] context anywhere in this file (§25.A) -- no dial contexts,
; no other extensions in [from-klanker-inbound], nothing else the endpoint
; can reach.
```

This is the literal minimal shape docs.asterisk.org's own dialplan examples use for Stasis handoff `[CITED: community.asterisk.org dialplan examples / asterisk.org "Stasis Improvements" blog]` — `Answer()` then immediately `Stasis(<app-name>)`; the `Hangup()` line only ever executes if the Stasis app itself returns control to the dialplan (which a well-behaved controller never triggers — the controller hangs up the channel itself via ARI, not by falling out of Stasis).

**Version-sensitive note:** External Media + the externalMedia REST endpoint have been stable in ARI since Asterisk 15+; nothing about the syntax above is version-sensitive within the 18–23 range currently shipping. The one thing to double check against whichever Docker image tag is pinned is that `res_ari_channels`, `res_ari_bridges`, and `res_stasis` are all compiled into that image's module set (some slim third-party images strip modules) — a `docker exec asterisk 'core show application Stasis'`-style smoke check belongs in the harness README.

---

## R4 — Local docker-compose Asterisk harness + SIP test client

### macOS Docker Desktop networking

`network_mode: host` **only works reliably on Linux**; Docker Desktop 4.34+ (2024) added an opt-in, still-labeled-experimental host-networking mode for macOS/Windows `[VERIFIED: github.com/docker/roadmap issue #238 discussion, docker/for-mac issue #7261]`. Do not depend on it being enabled in every dev's environment. Recommended, portable approach for the compose harness:

1. **Publish exact ports, not host networking:**
   ```yaml
   services:
     asterisk:
       image: andrius/asterisk:22
       ports:
         - "5060:5060/udp"        # SIP signaling
         - "8088:8088/tcp"        # ARI (bind only to the compose-internal/loopback address in ari.conf/http.conf)
         - "10000-10020/udp:10000-10020/udp"  # rtp.conf port range (narrow it for dev -- default range is much larger)
   ```
2. **Run the Klanker telephony controller (D-08 entrypoint) directly on the host macOS**, not inside a container, for local dev — this sidesteps the Mac Docker networking question entirely for the *Klanker* side, and lets the controller's `RtpMediaSession` bind a normal host UDP port that the softphone's own traffic never has to cross a NAT/port-publish boundary to reach... except Asterisk (in its container) still needs to reach that host port. Docker Desktop provides `host.docker.internal` as a working DNS name **from inside the container back to the host** on both Mac and Windows (this is the one direction Docker Desktop has always supported) — set `external_host=host.docker.internal:<port>` in the `externalMedia` call (R2) and it resolves correctly.
3. **`pjsip.conf` NAT/address fields matter even for pure-localhost dev.** If `pjsip.conf`'s transport doesn't declare `external_media_address`/`external_signaling_address` matching an address the SIP softphone (on the host) can actually reach, Asterisk's SDP will advertise its internal container-bridge IP (e.g. `172.x.x.x`), which is **not** directly reachable from the macOS host even though the container's published ports are. Set:
   ```ini
   [transport-udp]
   type=transport
   protocol=udp
   bind=0.0.0.0:5060
   external_media_address=127.0.0.1
   external_signaling_address=127.0.0.1
   local_net=127.0.0.1/32
   ```
   when the softphone runs on the same host as the Docker Desktop VM (the common case) — the port-publish mapping (`5060:5060/udp`, and the rtp.conf range) then makes `127.0.0.1:<port>` genuinely reachable.

### CI-friendly automated SIP test client: SIPp

`SIPp` (github.com/SIPp/sipp) is the right tool for the **required, deterministic D-07 CI artifact** ("SIP client → Asterisk → fake Klanker media transport"):
- Has an official Dockerfile in-repo (`docker build --target=bin`) `[CITED: github.com/SIPp/sipp]` — easy to pin/vendor in the compose harness or a CI job.
- Scenario files are XML — fully scriptable: `INVITE` → wait `200 OK` → `ACK` → `exec play_pcap_audio="fixtures/passphrase-or-pin.pcap"` (a short prerecorded RTP+audio capture, e.g. containing the 4-word passphrase or DTMF PIN tones) → hold N seconds → `BYE` `[CITED: sipp.readthedocs.io/en/latest/media.html — pcap audio replay feature]`.
- Deterministic assertions: SIPp's own exit code + call-count stats give pass/fail on the SIP transaction layer; the *behavioral* assertions (gate unlocked, greeting-not-clipped ordering, hangup released all resources) are checked on the **Klanker side** — against the "fake Klanker media transport" the D-07 decision already specifies, i.e. assert on the `ActiveCall` registry / `CallSession.close()` / lifecycle release calls exactly the way `test_call_runtime.py`'s existing `FakeTransport` pattern already asserts on `CallSession` behavior (see Code to read before planning — `tests/test_call_runtime.py`).

`pjsua`/`baresip` are real interactive softphones (mic capture, actual audio devices, real user-facing UX) — better suited to the **manual, human-run §19-C exit-criterion proof** (a person on a real softphone hearing greeting/conversing/interrupting/hanging up) than to a scripted, headless CI assertion. Recommend: **SIPp for the CI-automated fake-media integration test; a documented manual softphone run (baresip or any SIP softphone/OS-standard client, e.g. Linphone) for the real-pipeline §19-C proof** — this matches D-07's own split exactly.

### Realistic CI-automatable scope vs. manual

CI-automatable (deterministic, no live API keys):
- SIP INVITE → Answer → Stasis → externalMedia + bridge creation → **fake** Klanker media transport (no real Deepgram/Anthropic/ElevenLabs) receiving RTP and asserting decode correctness.
- The full §16/§17 lifecycle matrix: ARI hangup → `release()`; simulated worker failure → hangup; quota-denied → no bridge left; simultaneous hangup+timeout → release-once.
- The §24 gate's DTMF-PIN and passphrase-match logic, run against synthetic/fake STT output (not live Deepgram) — this is unit-testable without any Asterisk/SIPp involvement at all (pure function: transcript tokens → bool).

NOT realistically CI-automatable without real provider keys (stays manual, per D-07):
- The literal greeting-not-clipped audio quality judgment (a human ear, or at minimum a recorded-audio energy/silence analysis, is needed — "not clipped" is a perceptual claim).
- Live Deepgram transcription of a live spoken passphrase through the full SIPp pcap → real STT round trip (would require either live API keys in CI, which D-07/§16 explicitly excludes, or a recorded-fixture STT mock — which then isn't really testing STT behavior, just the gate's token-matching logic, already covered above).

---

## R5 — The §24 silent answer-gate mechanics

### The architectural constraint that decides this (see Summary risk #1)

Verified against the installed pipecat 1.5.0 source (`pipecat/transports/base_input.py`, `base_output.py`) and this repo's own `telephony/transport.py`: `BaseInputTransport.stop(EndFrame)` / `.cancel(CancelFrame)` / `.cleanup()` are the three hooks a `Pipeline`'s frame-flow (or `WorkerRunner.cancel()`) invokes to end a pipeline, and in `TelephonyInputTransport`/`TelephonyOutputTransport` these all route to `_teardown()`, which calls `self._media.close()` — closing the RTP UDP transport. **Running a "gate-only" pipeline to completion and then starting a second, "full" pipeline over the same `TelephonyTransport` instance would close the live call's media socket the moment the gate pipeline ends.** This rules out the "two sequential `build_pipeline()` calls, same transport" design entirely — it is not merely suboptimal, it is broken by the existing, verbatim-reused Phase 10 transport contract.

### Recommended design: one persistent pipeline, a `GateProcessor` in the graph

Build **one** `Pipeline`/`PipelineWorker`/`CallSession` for the entire call (via the existing `create_call_session`/`build_pipeline` seam, unchanged in shape), with a new `GateProcessor` (a plain `FrameProcessor` subclass, occupying the exact same architectural slot pattern as the existing `KnowledgeRouterProcessor` — see `knowledge/router.py`, itself inserted "between the STT service and the `LLMContextAggregatorPair`") placed **immediately after `stt`, before `KnowledgeRouterProcessor`/the duplex controller/the user aggregator**:

```text
transport.input() -> stt -> GateProcessor -> [duplex?] -> router -> user_aggregator -> llm -> tts -> transport.output()
```

While gated (`self._unlocked is False`):
- **Swallow, don't forward,** every `TranscriptionFrame`/`InterimTranscriptionFrame`/`UserStartedSpeakingFrame`/`UserStoppedSpeakingFrame` it receives — `process_frame` simply does not call `push_frame()` for these types while locked (this is the literal redaction boundary D-05e requires: the pre-unlock transcript never reaches `router`/`user_aggregator`/`llm`, because it is never pushed past this processor at all — not "dropped later," never forwarded in the first place).
- Run the **passphrase matcher** on each `TranscriptionFrame.text` it intercepts: lower-case, tokenize, set-membership against `TELEPHONY_PASSPHRASE_WORDS` — order-independent, accumulated across multiple transcription frames within the gate window (the caller's 4 words may span more than one STT-finalized utterance).
- **DTMF PIN is not a pipecat frame path here** — ARI surfaces `ChannelDtmfReceived` as a WebSocket **event** at the controller layer (outside the pipeline entirely), so the controller compares digits to `TELEPHONY_ACCESS_PIN` directly and calls a method on the `GateProcessor` (or pushes a synthetic unlock signal into the pipeline, e.g. via `worker.queue_frames([...])`) to flip it to unlocked — D-05b's "handled at the Asterisk/controller layer, never in the LLM" is satisfied structurally, since the PIN digits never touch pipecat's frame graph at all. (If the planner instead wants a single code path for both factors, pipecat *does* have `InputDTMFFrame`/`DTMFFrame` (verified present in `frames/frames.py`) for transports that decode DTMF from the audio path — but ARI already decodes DTMF and delivers it as a discrete event, so routing it through the audio-frame pipeline would be redundant; the controller-side comparison is simpler and matches D-05b's own wording.)
- On unlock (either factor): call `greet_now(worker, context)` (unchanged, exactly as today) to kick the LLM, and set `self._unlocked = True` so all subsequent frames (post-unlock speech) flow through untouched — this satisfies D-05c exactly ("grant tier ... THEN run the normal path — `greet_now()` → LLM → TTS. The greeting fires HERE").
- **Fail-closed timeout:** a `gate_window_seconds` timer (started when the `GateProcessor` is constructed/pipeline starts) that, on expiry with no unlock, triggers the deterministic goodbye path — reuse `pipeline.speak_goodbye(worker, copy)` (`TTSSpeakFrame`, bypasses the LLM entirely, already proven in `call_runtime.py`'s own wind-down path) followed by the controller hanging up the ARI channel (`DELETE /channels/{id}`) and `CallSession.close()`.

### Logging / redaction discipline (D-05e)

The `GateProcessor` must log only `unlocked{method: "dtmf"|"passphrase", call_id}` — never the transcript, never which words matched, never a partial-match count (no "3 of 4" oracle). Because the pre-unlock `TranscriptionFrame`s are never pushed downstream, they also never reach any transcript-ledger/logging hook that might exist further down the pipeline (satisfying "not written to the ledger or logs verbatim" structurally, not just by convention).

### Whether this technically satisfies "the expensive turn loop is built only after a pass" (D-05d)

Under this design, `build_llm`/`build_tts`/the Anthropic and ElevenLabs SDK client objects **are constructed** at pipeline-build time (before the gate passes), because it's one `Pipeline`. Constructing an SDK client is a cheap, no-network-call operation (confirmed by reading `factories.py`'s existing `build_llm`/`build_tts` — they build client objects, they don't make requests). The *actual* expense — API calls per conversational turn — genuinely never happens until `greet_now()` fires, because nothing upstream of the `GateProcessor` ever reaches `llm`/`tts` while locked. This is a defensible reading of D-05d's intent ("the LLM/TTS never *engage*") even though it is not the more literal "gate-scoped `build_pipeline` variant" phrasing in the CONTEXT.md discretion note — flagged explicitly as an Open Question below since CONTEXT.md leaves this exact tradeoff to the planner.

### SOXR resampler warmup (carried forward from Phase 10)

Phase 10's `TelephonyInputTransport`/`TelephonyOutputTransport` each own one stateful `SOXRStreamAudioResampler` (`clear_after_secs=None`). The Phase 10 finding — first small chunks through a freshly-constructed stream resampler can return **0 bytes** while it fills its internal history buffer — applies identically here: (1) **greeting-not-clipped (§12/D-04):** since the greeting now fires on *unlock*, not on answer, the output resampler will have already processed whatever gate-window audio existed (if any TTS/tone was played during the gate — none is, per D-05 "stays silent") — in the "either" mode with no gate-audio ever sent, the OUTPUT resampler's warmup gap could still land on the very first greeting frame; and (2) **gate STT** — the INPUT resampler's warmup gap could swallow the first ~tens of ms of the caller's very first spoken word, which (for the passphrase factor) risks losing part of word 1. Recommendation: verify via the D-07 integration test whether the gate window (default 10s, effectively several STT-finalized utterances) has enough margin to absorb this; if the first-utterance loss proves to matter in practice, consider priming the input resampler with a short silence buffer at gate-start (mirrors the existing "~100–250ms readiness margin only if tests show clipping" instinct in D-04) rather than any structural pipeline change.

---

## R6 — Standalone telephony entrypoint + lifecycle teardown

### Entrypoint shape

Recommend `python -m klanker_voice.telephony.controller` (a `__main__.py` inside the `telephony/` package, or a `if __name__ == "__main__":` block in `controller.py` itself) over a top-level `telephony_server.py` — this keeps the whole telephony surface (config, controller, ARI client, socket session) inside the `telephony/` package the same way `webrtc.py` keeps WebRTC-specific code isolated at the top of `klanker_voice`, and matches D-08's own phrasing ("e.g. `python -m klanker_voice.telephony.controller`"). It constructs one `AriClient`, connects the events WebSocket, and dispatches `StasisStart`/`ChannelDtmfReceived`/`ChannelDestroyed` to `AsteriskCallController` methods — no FastAPI, no HTTP server of its own (ARI's HTTP server lives inside Asterisk; the Klanker side is a WebSocket client + REST client only).

### ARI `ChannelDestroyed` → `CallSession.close()` → `lifecycle.release()` wiring

`CallSession.close(reason)` is already the single idempotent path (`call_runtime.py`, verbatim, unchanged) — it just calls `lifecycle.release()`, whose own `_stopped` guard (verified in `session.py`) makes repeated/racing calls a no-op. The controller's `on_channel_destroyed(event)` handler:
1. Looks up the `ActiveCall` by `event.channel.id` (the original SIP channel ID, per D-02's registry key).
2. Under the `ActiveCall`'s own lock (see below), if not already closed: calls `await active_call.call_session.close("ari channel destroyed")`, then tears down the bridge (`DELETE /bridges/{id}`) and external-media channel (`DELETE /channels/{external_media_channel_id}`) and the socket-backed `RtpMediaSession.close()`, then removes the registry entry.

### Hard session-timeout must also hang up the SIP channel

`SessionLifecycle.on_stop` (wired in `create_call_session`) already calls `speak_goodbye()` then, after `goodbye_grace_seconds`, `runner.cancel(...)`. For telephony, `runner.cancel()` ending the pipeline is **necessary but not sufficient** — it stops the Klanker-side worker, but the SIP channel itself is still up in Asterisk until something calls `DELETE /channels/{sip_channel_id}`. The controller must additionally wire `lifecycle.on_released` (already an available hook, verified in `session.py`'s `SessionLifecycle` dataclass field — currently used by `create_call_session` for `runner.cancel`) to **also** call the ARI hangup for the original SIP channel, e.g.:
```python
async def _on_released() -> None:
    await runner.cancel("session wind-down complete")
    await ari_client.hangup(active_call.sip_channel_id)  # ARI DELETE /channels/{id}
lifecycle.on_released = _on_released
```
This guarantees a hard timeout (§17 "do not leave a silent open call burning PSTN charges") always reaches the SIP channel, not just the Python-side pipeline.

### Quota-denied leaves no bridge

Because the §24 gate's tier-grant (D-05a) happens **after** `StasisStart` (the caller is already answered and in the gate before any tier/quota check happens — there is no pre-answer quota check possible for PSTN the way `/api/offer`'s `start_gate()` runs before any transport exists), "quota denied" for telephony means: the gate passes (PIN/passphrase correct) but `quota.start_gate(...)` then rejects (e.g. `ERROR_CONCURRENCY_LIMIT` because `max_concurrent_calls=1` is already occupied). In that case the controller must: **not** call `create_call_session` (so no `CallSession`/pipeline/bridge-attached-worker is ever built for the rejected caller), instead play the deterministic goodbye directly on the ARI channel or via a lightweight "gate-only" TTS-free path, then `DELETE` both the external-media channel and the bridge that were already created for the gate itself, then hang up. This is the one place a bridge legitimately gets created *before* the tier/quota decision (since the gate itself needs the media path to run STT) — the controller's `on_stasis_start` must track this "gate bridge, no CallSession yet" state distinctly in the `ActiveCall` registry so a quota rejection still tears down that bridge (never orphaned).

### Idempotency/locking on `ActiveCall` for simultaneous hangup+timeout

Mirror the existing `SessionLifecycle._stopped` boolean-guard pattern (a synchronous check-and-set with no `await` in between, verified in `session.py`'s `release()`): add a `closed: bool` field (already specified in D-02's `ActiveCall` shape) plus an `asyncio.Lock` (or the same synchronous-guard pattern, since `_stopped`-style guards avoid the overhead/complexity of a real lock when the check-and-set has no intervening `await`) to `ActiveCall`. Both the ARI `ChannelDestroyed` handler and a hard-timeout-triggered hangup path funnel through one `_close_active_call(active_call, reason)` helper that checks-and-sets `active_call.closed` before doing any teardown work — this is the same idempotency shape `CallSession.close()`/`SessionLifecycle.release()` already use one layer down, just replicated one layer up for the Asterisk-side resources (bridge/external-channel/RTP-socket) that `CallSession.close()` itself doesn't know about.

---

## Recommended Pins / New Dependencies

**No new dependency required for R1.** `aiohttp>=3.14` (already transitively pinned via `pipecat-ai[...]`) covers the ARI REST client + events WebSocket. If the planner wants it declared as a direct dependency for clarity/documentation purposes:

```toml
dependencies = [
    "pipecat-ai[anthropic,deepgram,runner,soundfile,webrtc]~=1.5.0",
    "pyjwt[crypto]~=2.13.0",
    "boto3>=1.42",
    "aiohttp>=3.14,<4",  # ARI REST + events-WebSocket client (D-06 research-decided pin: raw aiohttp, no ARI-specific library)
]
```

No changes needed to the `[dependency-groups] dev` extras for this phase's unit/lifecycle tests (they use fakes, per the existing `tests/conftest.py`/`test_call_runtime.py` pattern). For the D-07 integration harness, `docker-compose` itself and `SIPp` are **external tooling**, not Python dependencies — they belong in `apps/voice/asterisk/docker-compose.yml` + a `Makefile`/`README.md` target, not in `pyproject.toml`.

**Fallback pin (only if raw `aiohttp` proves insufficient during implementation):**
```toml
"asyncari~=0.20.6",  # fallback ARI client -- anyio-based; verify anyio/asyncio-loop compatibility before adopting
```

---

## Implementation Risks & Landmines

1. **Reusing `TelephonyTransport` across two sequential pipelines closes the RTP socket mid-call** (see R5/Summary risk #1). This is the single most important finding in this document — it eliminates an entire design branch (gate-pipeline-then-full-pipeline) that would otherwise look like the "obviously simplest" approach.
2. **macOS Docker Desktop has no dependable `network_mode: host`.** Plan for explicit `ports:` publishing + `host.docker.internal` for the Klanker-side RTP listener, and `external_media_address`/`external_signaling_address`/`local_net` set to `127.0.0.1` in `pjsip.conf`'s transport, or SIP will one-way-audio/silently fail even on localhost.
3. **`connection_type=client` is the only supported External Media mode** — Asterisk is always the active UDP sender. The socket-backed `RtpMediaSession` must bind and be *listening* before the `externalMedia` channel is created, and must learn the peer address from the first received datagram (symmetric RTP), never assume a fixed peer.
4. **SOXR stream-resampler warmup (Phase 10 finding, carried forward).** First small chunks through a freshly-constructed `SOXRStreamAudioResampler` can return 0 bytes while it fills history. This intersects with the gate: the very first spoken word during the passphrase gate risks partial loss at the input-resampler warmup boundary; and the greeting (which now fires on *unlock*, not answer) is the first thing through the output resampler in the no-gate-tone case. Both need verification against the D-07 integration test, not assumed away.
5. **DTMF timing:** ARI delivers `ChannelDtmfReceived` as one event per digit, not a debounced string — the controller must accumulate digits across the `gate_window_seconds` window itself (comparing the accumulated buffer, or comparing against `TELEPHONY_ACCESS_PIN` after each digit for an early-exit match) rather than expecting one event containing the whole PIN.
6. **Redaction-boundary correctness is a structural property, not a filter.** The recommended `GateProcessor` design achieves D-05e's "never forwarded" requirement by literally never calling `push_frame()` for locked-state transcription frames — this is stronger and simpler to verify (a unit test can assert zero calls to a downstream fake during the locked window) than a "receive it everywhere then scrub it" approach, which would require auditing every downstream consumer (ledger, logs, LLM context) for scrub-compliance instead of one processor.
7. **Quota-denied-after-gate-pass leaves a bridge that the standard `create_call_session` failure path never sees** (see R6) — because the gate itself requires a live bridge before any tier/quota decision is possible for a PSTN caller (unlike the WebRTC path, where `start_gate()` runs before any transport exists), the controller has a genuinely new failure state (`gate bridge exists, no CallSession yet, quota rejected`) that must be explicitly tested (§17), not assumed to be covered by existing `CallSession.close()` idempotency.
8. **`config.py`'s existing credential-field-name regex (`_CREDENTIAL_FIELD_RE`) does not currently match `pin` or `passphrase`.** Verified by reading the pattern directly (`config.py` lines 36-39): it matches `key(s)`, `secret(s)`, `token(s)`, `password`, `credential(s)`, `bearer`, `auth`, `apikey` — but **not** `pin` or `passphrase`/`words`. D-09 explicitly requires extending this rejection to cover the new §24 secret-looking fields (`TELEPHONY_ACCESS_PIN`, `TELEPHONY_PASSPHRASE_WORDS`) — the regex must be widened (e.g. add `pin|passphrase`) or these could silently be accepted as `pipeline.toml` tunables, defeating the "secrets never in TOML" guarantee this module otherwise enforces.

---

## Open Questions for the Planner

1. **Gate-pipeline architecture: single persistent pipeline with a `GateProcessor` (this document's recommendation) vs. a genuinely separate, minimal "STT+DTMF-only" `build_pipeline` variant.** This document shows the two-sequential-pipelines-over-one-transport approach is unsafe (closes the RTP socket). A third alternative not fully explored here: construct TWO separate `TelephonyTransport` instances over the SAME underlying `RtpMediaSession` (transport is cheap to construct; only the media session/socket is the expensive, stateful resource) — i.e. build a throwaway gate-only `Pipeline`/`PipelineWorker` around `TelephonyTransport#1` wrapping the live `RtpMediaSession`, and when the gate ends *without* calling `EndFrame` on that worker (instead directly cancelling the worker task without letting `CancelFrame`/`EndFrame` propagate to the transport's `stop()`/`cancel()`), swap to a second `TelephonyTransport#2` wrapping the *same* `RtpMediaSession` for the full pipeline. This is more literally aligned with CONTEXT.md's "gate-scoped `build_pipeline` variant" phrasing but requires verifying that a `PipelineWorker`/`WorkerRunner` can be torn down (task-cancelled) WITHOUT its transports' `stop()`/`cleanup()` firing — this needs a spike against the installed pipecat 1.5.0 `WorkerRunner`/`PipelineTask` cancellation semantics before committing to it. Recommend the planner spike this specific question early (it's the single highest-uncertainty design point in the whole phase) and fall back to the `GateProcessor`-in-one-pipeline design in this document if the transport-teardown-avoidance spike doesn't pan out cleanly.
2. **Which `[telephony]` config key carries "which tier to grant on unlock" (D-05a).** D-09's locked key list (`enabled`, `provider`, `edge`, `codec`, `sample_rate`, `packet_ms`, `max_concurrent_calls`, `answer_timeout_seconds`, `hangup_on_pipeline_error`, `require_gate`, `gate_mode`, `gate_window_seconds`) does not include a tier-target key. Recommend adding one additional non-secret key, e.g. `unlock_tier_id = "kph-tier"` (or per-`gate_mode` if DTMF and passphrase should ever grant different tiers) — this is additive to D-09, not a contradiction of it, but the planner should confirm this is in-scope for "the `[telephony]` loader lands now" rather than deferred.
3. **Exact shape of the minimal `CallIdentity`/`SessionIdentity` bridge for a PSTN caller.** `quota.start_gate()` takes a `SessionIdentity(sub, tier_id, group, bypass_accounting)` (from `auth.py`), not `call_runtime.CallIdentity` (from `call_runtime.py`) — these are two distinct dataclasses today. The controller will need to construct a `SessionIdentity` (e.g. `sub=f"tel:{normalized_caller_id or call_id}"`, `tier_id=<resolved from unlock method>`) to call the existing `quota.start_gate()` seam unchanged, then separately construct a `CallIdentity` for `create_call_session`. Confirm this dual-construction is acceptable or whether a small adapter function belongs in `telephony/config.py`/`controller.py`.
4. **How far to push the CI-automatable tier for the §16 integration test** — this document recommends SIPp + a fake media transport for the deterministic tier, and a manual softphone run for the real-pipeline §19-C proof, per D-07's own explicit split. If, once built, the fake-media path proves cheap to extend toward more of the real pipeline (e.g. a recorded-fixture STT response instead of a fully fake transport), that's explicitly planner's discretion per D-07 — not a blocker for the required floor.
5. **Whether the `GateProcessor`'s fail-closed timer should be a plain `asyncio.sleep()`-based task (mirroring `SessionLifecycle._service_timer`'s existing pattern exactly) or should reuse/extend `SessionLifecycle` itself** (e.g. a `gate_deadline` concept alongside the existing `session_max_seconds` deadline). This document assumes a separate, simple timer scoped to the `GateProcessor`/controller (since `SessionLifecycle` doesn't exist yet at gate time — it's constructed inside `create_call_session`, which per D-05d only happens after unlock) — confirm this sequencing (gate timer exists and runs BEFORE `SessionLifecycle` is even constructed) is consistent with the planner's chosen architecture from Open Question 1.

## RESEARCH COMPLETE

Researched Phase 11's six open questions against the locked CONTEXT.md decisions (D-01 through D-09), the installed pipecat 1.5.0 source, official Asterisk ARI/External-Media documentation, and current (2026) PyPI/GitHub state for the four candidate ARI client libraries. Headline finding: raw `aiohttp` (already pinned) is the recommended ARI client — no new dependency — because the ARI REST surface this phase needs is small and every alternative library (`ari-py` sync, `asyncari` anyio/trio-based, `aioari` wrapping a stack last released in 2018-2020, `panoramisk` being AMI not ARI at all) carries either a wrong concurrency model or a stale/niche dependency chain. The single most consequential architectural finding is that Phase 10's `TelephonyTransport` ties its lifecycle (via `stop()`/`cancel()`/`cleanup()`) directly to closing the live RTP socket, which rules out a "gate pipeline, then full pipeline" sequential-build design for the §24 answer-gate and instead favors one persistent pipeline with an inline `GateProcessor` (flagged as the top Open Question for the planner to spike/confirm). Concrete Asterisk config skeletons (`http.conf`/`ari.conf`/`pjsip.conf`/`extensions.conf`), the exact External Media handshake (Asterisk-initiates, Klanker-listens, symmetric-RTP source-learning), a macOS Docker Desktop networking workaround, and a SIPp-based CI-automatable integration-test recommendation are all documented above with citations.
