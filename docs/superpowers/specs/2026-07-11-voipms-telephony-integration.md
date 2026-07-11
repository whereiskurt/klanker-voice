# Klanker Voice: VoIP.ms / Payphone Telephony Integration Plan

**Repository:** `whereiskurt/klanker-voice`  
**Audience:** implementation agent working directly in the repository  
**Status:** proposed implementation plan  
**Primary goal:** allow a caller on the public telephone network—or the physical payphone connected through an ATA—to talk to the existing Klanker Voice agent.

---

## 1. Executive summary

Do **not** build a separate telephone-specific AI pipeline.

`klanker-voice` already has a transport-neutral Pipecat cascade:

```text
transport.input()
  -> STT
  -> optional duplex controller
  -> knowledge router
  -> user context aggregator
  -> Anthropic LLM
  -> ElevenLabs TTS
  -> transport.output()
  -> assistant context aggregator
```

The telephony project should add a new audio transport at the edge and continue using the existing:

- Deepgram STT selection
- Smart Turn / Flux turn detection
- `DuplexController`
- Anthropic Haiku configuration
- ElevenLabs TTS and pronunciation filtering
- knowledge routing and retrieval
- greeting behavior
- quota gates and session lifecycle
- metrics, teardown observers, and ECS scale-in protection

### Recommended first production architecture

```text
Caller / payphone
      |
    PSTN
      |
  VoIP.ms DID
      |
  SIP trunk or SIP URI
      |
 Asterisk edge service
      |
 ARI External Media: RTP/PCM
      |
 Telephony transport adapter
      |
 Existing Klanker Pipecat pipeline
```

Use **Asterisk as the SIP/PSTN boundary** and add a Klanker-side media adapter. This is lower risk than embedding a full SIP registrar, transaction layer, SDP negotiator, RTP NAT traversal implementation, and DTMF stack directly into the Python voice service.

The physical payphone remains independent:

```text
Payphone -> ATA -> VoIP.ms subaccount
```

It can call the Klanker DID like any other telephone. Incoming PSTN callers can also reach the same agent.

---

## 2. Existing codebase findings

The primary runtime is under:

```text
apps/voice/
```

The Python package is:

```text
apps/voice/src/klanker_voice/
```

Important existing seams:

### `pipeline.py`

`build_pipeline(cfg, transport, ...)` already accepts a Pipecat `BaseTransport`.

This is the most important integration seam. The current processor graph begins with `transport.input()` and ends with `transport.output()`. Telephony should preserve this contract.

The pipeline also already supports:

- optional RTVI processing
- optional full-duplex handling through `DuplexController`
- a per-session `RetrievalIndex`
- knowledge topic routing before aggregation
- immediate greeting through `greet_now()`
- deterministic TTS goodbye through `TTSSpeakFrame`

### `factories.py`

All AI service creation is centralized:

- Deepgram Nova-3
- Deepgram Flux
- Anthropic
- ElevenLabs
- VAD and user-turn strategies

Do not duplicate provider construction in telephony code. A phone call should use the same `PipelineConfig` and factory functions as WebRTC.

### `pipeline.toml`

This is explicitly the single stage-selection surface.

Current relevant defaults observed in the repository include:

- Deepgram Nova-3
- Smart Turn v3
- Anthropic Claude Haiku 4.5
- ElevenLabs Flash v2.5
- configured voice, speed, and voice settings

Add a `[telephony]` section only for transport/media behavior. Do not add provider credentials or parallel STT/LLM/TTS settings.

### `session.py`

`SessionLifecycle` already owns:

- session duration
- accounting ticks
- concurrency bookkeeping
- active-session CloudWatch metrics
- ECS task scale-in protection
- reconnect and silence teardown
- warning and hard-stop callbacks
- one idempotent release path

A telephone call must create and release this lifecycle exactly as a browser session does.

For telephony, a SIP hangup or Asterisk channel destruction is terminal. It should normally release immediately rather than using a browser-style reconnect grace period.

### `server.py`

This is the current deployed service entry point and owns live connection setup.

Avoid putting SIP transaction logic directly into this already-large file. Add a telephony module and expose a narrow call-session function from shared runtime code.

### `webrtc.py`

WebRTC-specific connection and ICE configuration is isolated. Follow the same pattern by introducing a telephony-specific module rather than adding branches throughout WebRTC code.

---

## 3. Scope

### Phase 1 scope

Implement inbound calls from a VoIP.ms DID to the Klanker agent.

Acceptance flow:

```text
1. Call the DID.
2. Asterisk answers.
3. Klanker creates a normal quota-controlled session.
4. The configured greeting is heard.
5. Caller audio reaches Deepgram.
6. Klanker responses return through ElevenLabs.
7. Caller can interrupt the agent.
8. Hanging up releases all resources promptly.
9. Existing web sessions continue to work unchanged.
```

### Phase 1.5 scope

Allow the ATA-connected payphone to call the same DID.

No Klanker code should be required specifically for the ATA. It is simply a SIP endpoint registered to VoIP.ms.

### Later scope

- outbound calls initiated by Klanker
- DTMF menus or actions
- transfer to Kurt's mobile
- simultaneous ring / fallback routing
- voicemail or call recording
- multiple DIDs mapped to personas
- SMS entry point
- direct SIP endpoint without Asterisk

---

## 4. VoIP.ms configuration

Create a dedicated VoIP.ms subaccount for the PBX/agent. Do not reuse the ATA's credentials.

Suggested logical accounts:

```text
main account
├── subaccount: payphone-ata
└── subaccount: klanker-pbx
```

### PBX subaccount

Set the device type to the equivalent of:

```text
Asterisk / IP PBX / gateway / VoIP switch
```

Use a dedicated SIP password.

Select one VoIP.ms POP and use the same POP for:

- the PBX registration/trunk
- the DID routing

For Ontario, choose based on measured latency and reliability rather than naming alone.

### DID routing

Route the DID to the `klanker-pbx` SIP subaccount.

An alternative is routing the DID to an external SIP URI exposed by the Asterisk edge. Start with registration-based trunking unless infrastructure constraints strongly favor URI routing.

### Codecs

For the first implementation, prefer:

```text
PCMU / G.711 μ-law, 8 kHz, mono
```

Reasons:

- universal PSTN compatibility
- minimal transcoding surprises
- Asterisk support
- predictable 20 ms RTP packetization

G.722 can be added for SIP-to-SIP HD audio, but ordinary PSTN calls will remain narrowband.

### Security controls

Apply:

- dedicated subaccount
- strong generated credential
- IP restriction when the service has stable egress
- international dialing disabled unless explicitly needed
- account and per-call spending limits
- maximum call duration
- TLS/SRTP when supported end-to-end
- no SIP credentials in Git or `pipeline.toml`

Keep the ATA account incapable of expensive destinations where possible.

---

## 5. Proposed repository changes

Suggested files:

```text
apps/voice/
├── asterisk/
│   ├── extensions.conf
│   ├── pjsip.conf
│   ├── ari.conf
│   └── README.md
├── src/klanker_voice/
│   ├── call_runtime.py
│   └── telephony/
│       ├── __init__.py
│       ├── config.py
│       ├── controller.py
│       ├── media.py
│       ├── transport.py
│       └── types.py
├── tests/
│   ├── test_telephony_config.py
│   ├── test_telephony_media.py
│   ├── test_telephony_lifecycle.py
│   └── test_telephony_transport.py
└── pipeline.toml
```

Infrastructure additions should follow the repository's current Terragrunt layout, probably as a distinct service:

```text
infra/terraform/live/site/.../services/telephony-edge/
```

Keep the Asterisk process separate from `apps/voice` unless there is a compelling operational reason to co-locate them.

---

## 6. Extract a shared call runtime

Before adding telephony, extract the reusable session setup currently embedded in `server.py`.

Target API:

```python
@dataclass
class CallSession:
    session_id: str
    worker: PipelineWorker
    lifecycle: SessionLifecycle

    async def run(self) -> None: ...
    async def close(self, reason: str) -> None: ...


async def create_call_session(
    *,
    transport: BaseTransport,
    identity: CallIdentity,
    cfg: PipelineConfig,
    channel: Literal["webrtc", "pstn"],
    metadata: dict[str, str],
) -> CallSession:
    ...
```

Responsibilities:

1. authenticate or resolve the caller identity
2. call the quota start gate
3. build ambience mixer when compatible
4. build the existing pipeline
5. create observers
6. create `SessionLifecycle`
7. wire warning and stop callbacks
8. wire transport disconnect handling
9. register or invoke greeting
10. return a session object with one idempotent close path

Both WebRTC and telephony should use this function.

Do not attempt a large rewrite of all of `server.py`. Extract only the path needed to prevent duplicated lifecycle and pipeline wiring.

---

## 7. Telephony media boundary

### Recommended Asterisk mechanism

Use:

- PJSIP for VoIP.ms
- ARI/Stasis for call control
- ARI External Media for audio exchange

High-level call sequence:

```text
VoIP.ms sends SIP INVITE
  -> Asterisk PJSIP endpoint/trunk
  -> dialplan answers
  -> channel enters Stasis app
  -> controller creates External Media channel
  -> caller and external-media channel join a mixing bridge
  -> Klanker receives and sends RTP
  -> channel hangup closes Klanker session
```

### Why not direct SIP in Phase 1?

Direct SIP would require the application to correctly own:

- SIP registration refresh
- digest authentication
- INVITE/re-INVITE/ACK/BYE transaction state
- SDP offer/answer
- RTP port allocation
- NAT and public-address advertisement
- symmetric RTP
- DTMF negotiation
- codec negotiation and transcoding
- retransmission timers
- provider-specific quirks
- malformed and hostile internet traffic

Asterisk already handles these concerns well. Klanker should own conversational media and agent behavior.

---

## 8. `TelephonyTransport`

Implement a Pipecat-compatible transport.

Conceptual API:

```python
class TelephonyTransport(BaseTransport):
    def __init__(
        self,
        *,
        call_id: str,
        media: RtpMediaSession,
        params: TelephonyTransportParams,
    ) -> None:
        ...

    def input(self) -> FrameProcessor:
        ...

    def output(self) -> FrameProcessor:
        ...

    async def start(self) -> None:
        ...

    async def stop(self) -> None:
        ...
```

### Input path

```text
RTP PCMU payload
  -> sequence/jitter handling
  -> μ-law decode
  -> signed 16-bit PCM
  -> optional resample 8 kHz -> pipeline input rate
  -> Pipecat InputAudioRawFrame
```

### Output path

```text
Pipecat OutputAudioRawFrame
  -> resample pipeline rate -> 8 kHz
  -> signed PCM -> μ-law encode
  -> 20 ms framing
  -> RTP packetization
  -> Asterisk external-media address
```

### Transport events

Expose telephony equivalents of existing connection events:

```text
on_client_connected
on_client_disconnected
```

For a new answered call, emit connected once the media path is ready. This allows existing greeting registration to work.

On SIP/ARI hangup:

```text
transport stop
-> cancel worker
-> lifecycle.release()
-> remove call registry entry
```

Make every step idempotent.

---

## 9. Audio details

### Narrowband reality

PSTN audio is normally approximately:

```text
8 kHz
mono
G.711
telephone-band speech
```

Do not send 44.1 or 48 kHz audio to the SIP side and assume Asterisk will always make good choices.

### Deepgram input

Confirm the Pipecat Deepgram service receives accurate sample-rate metadata after decoding. The input frame's sample rate must match the actual PCM.

Deepgram can process 8 kHz telephone audio, but test recognition quality with:

- payphone handset
- background room noise
- Canadian and US telephone paths
- speakerphone
- clipped or quiet callers

### ElevenLabs output

The existing ElevenLabs service may generate at a higher sample rate. Downsample once, at the telephony transport boundary.

Use a stateful streaming resampler. Avoid independently resampling each tiny frame because that can create boundary artifacts and clock drift.

### Packetization

Start with:

```text
codec: PCMU
clock: 8000 Hz
packet time: 20 ms
samples per packet: 160
payload type: negotiated, commonly 0 for PCMU
```

Do not assume RTP payload type without reading the negotiated/external-media format.

### Jitter and loss

Asterisk should absorb the public SIP/RTP edge. The Klanker external-media path is under our control, but the adapter should still tolerate:

- minor reordering
- duplicate packets
- a missing packet
- timestamp discontinuity at startup

For an MVP, silence insertion for a missing 20 ms packet is acceptable.

---

## 10. Turn-taking and barge-in

Preserve the current user-turn strategy selection.

The telephone transport must not add a second VAD or endpointing system. The repository explicitly prevents double endpointing for Deepgram Flux; telephony must not undo that.

### Interruption behavior

When caller speech begins while TTS is playing:

1. existing Pipecat interruption frames should stop downstream speech
2. queued outbound audio in the Klanker adapter must be flushed
3. Asterisk/media buffers should be kept shallow
4. RTP should resume with live response audio after the next turn

Add a transport method such as:

```python
async def flush_output_audio(self) -> None:
    ...
```

Wire it to the relevant interruption frame or processor event.

Target buffering:

```text
20-60 ms application output queue
```

Large output buffers make phone agents feel uninterruptible.

### Echo

A normal handset provides acoustic isolation. Speakerphones and poorly configured ATAs may produce echo.

Do not enable application echo cancellation first. Let the endpoint/ATA/Asterisk path handle it. Add AEC only after measuring a real problem.

---

## 11. Identity, authentication, and quota policy

A PSTN caller does not have the existing browser JWT.

Introduce a `CallIdentity` abstraction:

```python
@dataclass(frozen=True)
class CallIdentity:
    subject: str
    caller_id: str | None
    did: str | None
    authenticated: bool
    auth_method: str
```

### MVP identity

For a private prototype:

```text
subject = "pstn:<normalized-caller-id>"
authenticated = false
```

Do not trust caller ID as strong authentication.

### Better private access

Offer one of:

- allowlisted caller IDs plus a PIN
- DTMF PIN on answer
- short spoken passphrase verified outside the LLM
- call-back verification
- dedicated unlisted DID with strict quota

Never pass authentication secrets into the conversational LLM context.

### Quota mapping

Add a telephone tier or map calls to an existing constrained tier.

Recommended prototype limits:

```text
maximum concurrent PSTN calls: 1
maximum call duration: 10 minutes
daily PSTN minutes: small explicit cap
outbound calling: disabled
```

Use the existing `SessionLifecycle`; do not implement an independent telephone timer.

---

## 12. Greeting behavior

The existing WebRTC path uses a connection event to invoke the greet-first behavior.

For telephone calls:

1. answer the channel
2. establish external media
3. start the Klanker worker
4. emit `on_client_connected`
5. greet

Do not greet before the bridge and media receiver are ready, or the first words will be clipped.

Add roughly 100-250 ms of readiness margin only if tests show clipping. Do not use an arbitrary multi-second sleep.

A future improvement is using the repository's pre-rendered greeting clip on PSTN, but first preserve the canonical `greet_now()` path so persona and greeting behavior remain consistent.

---

## 13. Asterisk controller

Create a small controller service or module that consumes ARI events.

Conceptual responsibilities:

```python
class AsteriskCallController:
    async def on_stasis_start(self, event: StasisStart) -> None:
        # accept only expected inbound context
        # normalize ANI and DID
        # allocate media session
        # create external-media channel
        # create bridge
        # attach both channels
        # create Klanker CallSession
        # start the worker

    async def on_channel_destroyed(self, event: ChannelDestroyed) -> None:
        # close CallSession
        # close media sockets
        # delete bridge/external channel
```

Maintain:

```python
calls: dict[str, ActiveCall]
```

Key by the original Asterisk channel ID or an explicit generated call ID.

An `ActiveCall` should contain:

- original SIP channel ID
- external media channel ID
- bridge ID
- RTP media session
- Klanker `CallSession`
- caller ID
- DID
- creation timestamp
- closed flag / lock

Use structured logs with call ID but never log SIP passwords, auth headers, or full PINs.

---

## 14. Configuration

Add a non-secret section to `pipeline.toml`:

```toml
[telephony]
enabled = false
provider = "voipms"
edge = "asterisk-ari"

codec = "pcmu"
sample_rate = 8000
packet_ms = 20

max_concurrent_calls = 1
answer_timeout_seconds = 15
hangup_on_pipeline_error = true
require_pin = false
```

Secrets stay in environment/SSM:

```text
ASTERISK_ARI_URL
ASTERISK_ARI_USERNAME
ASTERISK_ARI_PASSWORD
VOIPMS_SIP_USERNAME
VOIPMS_SIP_PASSWORD
VOIPMS_SIP_HOST
TELEPHONY_ACCESS_PIN
```

Extend `config.py` credential-name rejection to cover new secret-looking fields if necessary.

The SIP password should be consumed by the Asterisk service, not passed into the Klanker Python process.

---

## 15. Infrastructure

### Network

Asterisk needs:

- SIP signaling access to the selected VoIP.ms POP
- RTP access according to configured port range
- access from Klanker/controller to ARI
- a stable externally reachable address if receiving direct SIP URI traffic

For registration-based trunking, provider traffic still requires correct NAT and RTP advertisement.

Avoid placing SIP behind an HTTP ALB.

Viable AWS patterns:

```text
Asterisk on ECS/EC2 with public IP or NLB UDP/TCP listeners
```

For a first version, a small EC2 instance or ECS task with explicit host networking may be simpler than forcing stateful SIP and a broad RTP range through an awkward load-balancer design.

### Security groups

Restrict SIP/RTP ingress to documented VoIP.ms POP ranges when operationally practical.

ARI must be private-only and authenticated.

Do not expose ARI to the internet.

### Deployment isolation

Keep Asterisk and the AI voice task separately deployable:

```text
telephony-edge
voice
auth/webapp
```

A voice deployment should not force the SIP registrar to disconnect if this can be avoided.

### Observability

Add metrics:

```text
ActivePstnCalls
PstnCallDurationSeconds
PstnCallSetupLatencyMs
PstnInboundPacketsLost
PstnInputJitterMs
PstnAudioQueueDepthMs
PstnHangupReason
```

Reuse existing per-stage Pipecat latency observers.

Add dimensions conservatively to avoid CloudWatch cardinality explosion. Do not use caller ID as a metric dimension.

---

## 16. Testing strategy

### Unit tests

#### Codec

- PCMU decode known vectors
- PCMU encode known vectors
- 160-sample packet framing
- incomplete-frame buffering
- clipping behavior
- silence behavior

#### RTP

- sequence increment
- timestamp increment by 160
- SSRC stability
- duplicate packet handling
- one missing packet
- wraparound

#### Transport

- RTP input emits correct Pipecat audio frame
- Pipecat output emits PCMU RTP
- interruption flushes output
- stop is idempotent
- disconnect event fires once

#### Lifecycle

- ARI hangup invokes `release()`
- worker failure hangs up the call
- quota rejection does not leave an Asterisk bridge
- simultaneous hangup and timeout release once
- active call slot is returned

### Integration tests

Use a local Asterisk container and SIP test client.

Test:

```text
SIP client -> Asterisk -> fake Klanker media transport
```

Then:

```text
SIP client -> Asterisk -> real pipeline using test credentials
```

Assertions:

- caller hears greeting
- prerecorded caller WAV is transcribed
- response audio returns
- hangup cleans all resources
- two calls obey configured concurrency
- long silence triggers teardown
- interruption stops bot audio promptly

### End-to-end VoIP.ms tests

1. route DID to PBX
2. call from mobile
3. call from payphone ATA
4. run VoIP.ms echo test independently to validate ATA
5. verify two-way audio
6. verify caller-ID normalization
7. verify hangup from each side
8. verify fail-closed behavior when Klanker is unavailable

---

## 17. Failure behavior

Define explicit outcomes.

### Klanker unavailable before answer

Asterisk should:

```text
play a short static unavailable message
hang up
```

Optionally route to Kurt's cell later.

### Pipeline fails mid-call

Use existing fatal-error teardown observer, then:

```text
stop generated audio
optionally play static apology
hang up
release lifecycle
```

Do not leave a silent open call consuming PSTN charges.

### Asterisk loses Klanker media

End the call after a short bounded timeout.

### VoIP.ms registration fails

Emit an alarm and keep the web voice service unaffected.

### Quota denied

Do not construct the expensive STT/LLM/TTS pipeline. Play a static message and hang up.

---

## 18. Security requirements

Treat public SIP as hostile input.

Required:

- no default or anonymous ARI access
- ARI private network only
- narrow dialplan context
- inbound calls cannot reach arbitrary extensions
- outbound dialing disabled in Phase 1
- strict call duration and concurrency caps
- SIP passwords from SSM/secrets only
- no SIP auth material in logs
- validate ARI event fields
- normalize telephone numbers before storage
- rate-limit calls per source where possible
- prevent caller-controlled DID/persona path traversal
- do not let DTMF or caller speech execute tools without existing authorization
- call recording disabled by default
- disclose recording if it is later enabled

The LLM must never decide whether a caller is authenticated.

---

## 19. Implementation phases

### Phase A — refactor without behavior change

- add `call_runtime.py`
- move reusable pipeline/lifecycle setup out of `server.py`
- keep WebRTC behavior passing all tests
- add channel metadata to logs

**Exit criterion:** browser voice works exactly as before.

### Phase B — offline media adapter

- add PCMU codec
- add RTP parser/packetizer
- add telephony transport processors
- test with WAV and synthetic RTP
- implement interruption flushing

**Exit criterion:** recorded telephone audio can traverse the real Klanker pipeline without SIP.

### Phase C — local Asterisk

- add Asterisk configs
- add ARI controller
- create/destroy bridges and external media
- connect a local SIP softphone
- exercise greeting, conversation, interruption, hangup

**Exit criterion:** local SIP call has a full conversation.

### Phase D — VoIP.ms inbound DID

- create dedicated subaccount
- register Asterisk
- route DID
- apply security restrictions
- test from cellular network

**Exit criterion:** public DID reliably reaches Klanker.

### Phase E — physical payphone

- register ATA on its own subaccount
- verify echo test
- call Klanker DID
- tune ATA gain and DTMF only if needed

**Exit criterion:** payphone handset can converse naturally with Klanker.

### Phase F — production hardening

- Terraform/Terragrunt
- SSM secrets
- alarms and dashboards
- rolling-deploy behavior
- failure routing
- load/concurrency test
- runbook

---

## 20. Definition of done

- [ ] No STT/LLM/TTS provider logic duplicated
- [ ] Existing WebRTC path remains operational
- [ ] One inbound PSTN call can complete a multi-turn conversation
- [ ] Greeting is not clipped
- [ ] Caller interruption stops TTS quickly
- [ ] SIP hangup releases worker, quota heartbeat, call slot, bridge, and RTP socket
- [ ] Hard session timeout also hangs up SIP
- [ ] Fatal pipeline errors do not leave open calls
- [ ] VoIP.ms and Asterisk credentials are outside Git
- [ ] Outbound calling is disabled
- [ ] Unit, local SIP, and public DID tests are documented
- [ ] Operations runbook explains registration, routing, and one-way-audio debugging

---

## 21. Initial implementation prompt for the dev agent

Implement **Phase A only** first.

1. Read:
   - `AGENTS.md`
   - `.claude/CLAUDE.md`
   - `apps/voice/server.py`
   - `apps/voice/bot.py`
   - `apps/voice/console.py`
   - `apps/voice/src/klanker_voice/pipeline.py`
   - `apps/voice/src/klanker_voice/session.py`
   - `apps/voice/src/klanker_voice/webrtc.py`
   - existing tests around lifecycle, quota, greeting, and connection teardown

2. Identify the smallest reusable unit that builds and owns one live voice session independently of WebRTC request handling.

3. Add `apps/voice/src/klanker_voice/call_runtime.py` with a narrow API for constructing, running, and idempotently closing a session around an arbitrary Pipecat `BaseTransport`.

4. Convert the existing WebRTC server path to use it.

5. Preserve:
   - quota gate behavior
   - `SessionLifecycle`
   - observers
   - greeting behavior
   - warning and goodbye callbacks
   - reconnect behavior
   - RTVI behavior
   - ambience mixer behavior
   - all existing metrics and teardown guarantees

6. Do not add SIP, Asterisk, RTP, codecs, or infrastructure in this phase.

7. Add focused tests proving:
   - transport-neutral construction
   - WebRTC path behavior is unchanged
   - close is idempotent
   - lifecycle release occurs on worker/transport termination

8. Run the repository's documented formatting, type-checking, and test commands.

9. Produce:
   - code changes
   - tests
   - a short architecture note describing the extracted seam
   - explicit notes about assumptions or existing coupling that prevented a clean extraction

Stop after Phase A and report the exact files changed and test results.

---

## 22. Notes and decisions to preserve

1. **Asterisk is an edge adapter, not the agent runtime.**
2. **The existing `build_pipeline(cfg, transport)` contract is the center of the design.**
3. **Telephone audio conversion belongs in the transport boundary.**
4. **Existing Deepgram turn strategy rules remain authoritative.**
5. **Existing session lifecycle remains the single source of truth for teardown and quota.**
6. **Caller ID is metadata, not authentication.**
7. **Phase 1 is inbound-only and must not become an open calling relay.**
8. **The ATA/payphone needs no special integration with Klanker beyond being able to call the DID.**
