<!-- generated-by: gsd-doc-writer -->
# Data Flow: Browser → WebRTC → Pipeline

This page traces exactly what happens in the browser tab at **voice.klankermaker.ai**,
from page load to a live, full-duplex conversation with the KPH concierge — every
network call, every gesture-gated browser API, and every server-side gate it passes
through before audio flows. It is the browser-side companion to
[docs/dataflows/auth-quota.md](auth-quota.md) (identity/quota internals) and
[docs/dataflows/conversation-loop.md](conversation-loop.md) (what happens inside the
pipeline once media is flowing). For the wider system picture see
[docs/architecture/overview.md](../architecture/overview.md); for a list of the
trickier engineering fixes touched on below, see
[docs/techniques/highlights.md](../techniques/highlights.md).

Source of truth for everything in this doc: `apps/voice/client/src/` (the Vite/React
SPA) and `apps/voice/server.py` + `apps/voice/src/klanker_voice/{webrtc,rtvi,session,auth,quota}.py`
(the FastAPI signaling entrypoint), plus the CloudFront/ALB/Fargate topology in
`infra/terraform/live/site/`.

## End-to-end sequence

```mermaid
sequenceDiagram
    participant U as User
    participant B as Browser SPA (client)
    participant A as auth.klankermaker.ai
    participant CF as CloudFront
    participant S as server.py (/api/offer)
    participant P as Pipecat pipeline

    U->>B: Load voice.klankermaker.ai (served by CloudFront -> S3)
    B->>B: App mount: attemptSilentSso() (returning user only, one-shot per load)
    alt returning user with a live issuer session
        B->>A: top-level redirect, prompt=none (never an iframe -- iOS ITP)
        A-->>B: 302 /callback?code=...&state=...
        B->>A: POST /token (authorization_code + PKCE verifier, no client secret)
        A-->>B: access_token (in-memory only)
    else not returning, or silent SSO returned login_required
        B->>B: resolveScreen() -> "land" (LandBounce holding/nudge copy)
        U->>B: tap "Sign in"
        B->>A: full-page redirect /auth (code + PKCE S256)
        A-->>B: 302 /callback?code=...&state=...
        B->>A: POST /token
        A-->>B: access_token
    end
    U->>B: tap "Start conversation" (the ONE user gesture)
    B->>B: requestMic() -> getUserMedia({audio:true})
    B->>B: unlockAudioPlayback() + primeGreeting() (arm greeting clip, volume 0)
    B->>CF: POST /api/offer  (Authorization: Bearer <token>, SDP offer)
    CF->>S: forwarded to ALB origin (/api/* cache-disabled behavior)
    S->>S: validate_access_token() -- RS256 / JWKS / iss / aud / exp
    S->>S: quota.start_gate() -- bypass? site-paused? no-access? capacity? concurrency? daily?
    S->>P: SmallWebRTCTransport negotiate (SDP answer, ICE servers, host candidates)
    P-->>S: SDP answer + session_max_seconds + variant_label + server_version
    S-->>CF-->>B: 200 answer JSON (client's fetch interceptor peeks at it)
    B->>P: ICE gathering + DTLS/SRTP handshake, direct UDP to task's public IP
    Note over B,P: media never traverses CloudFront/ALB -- signaling only does
    P-->>B: RTVI onBotReady -> CONNECTED
    B->>B: Ceremony finishes -> Live screen mounts
    B->>B: playRandomGreeting() resumes the primed clip (mic muted for its duration)
    B-->>U: instant greeting audio (masks WebRTC connect latency)
    P-->>B: live bot audio + RTVI transcript/speaking/level events
    Note over U,P: full-duplex conversation continues (mic in, TTS out, barge-in)
```

## 1. Landing and authentication

`apps/voice/client/src/App.tsx` never renders a real landing page to an unauthenticated
visitor. On mount it runs a one-shot land sequence:

1. `auth.attemptSilentSso()` (`apps/voice/client/src/auth/useAuth.ts`) is a no-op unless
   the browser is a **returning user** (`isReturningUser()`,
   `apps/voice/client/src/auth/returningStore.ts`), is not already authenticated, and
   has not already tried silent SSO this page load (`wasSilentTried()`). If eligible, it
   stashes a fresh PKCE verifier/state pair in `sessionStorage` (keys
   `kmv_pkce_verifier` / `kmv_pkce_state` — see `apps/voice/client/src/auth/useAuth.ts:16-17`)
   and does a **top-level** navigation (`navigate()`, `apps/voice/client/src/auth/navigate.ts`)
   to the issuer's `/auth` endpoint with `prompt=none`. It is deliberately never an
   iframe, because iOS Safari's Intelligent Tracking Prevention blocks first-party
   cookies inside a third-party iframe context.
2. If the browser is still here afterwards and still unauthenticated (silent SSO was a
   no-op, or the issuer answered with no live session), `decideLandAction()`
   (`apps/voice/client/src/flow/landDecision.ts`) decides between a holding screen and a
   forced interactive redirect. The interactive path is a one-shot guard
   (`markInteractiveTried()` / `wasInteractiveTried()`) so a still-unauthenticated
   second arrival gets a manual "Sign in" nudge instead of an auto-bounce loop.

Both the silent and interactive paths route through `buildAuthorizeUrl()`
(`apps/voice/client/src/auth/oidcClient.ts`), which builds an authorization-code + PKCE
(S256) request with `scope=voice` and `resource=<audience>` pinned so the issued token's
`aud` claim matches what the server later checks. There is no client secret — this is a
public PKCE client (`apps/voice/client/src/config/oidc.ts`, values injected at build time
via `VITE_OIDC_*` env vars).

`apps/voice/client/src/screens/Callback.tsx` handles the return trip at `/callback`. It
supports two distinct paths:

- **Normal PKCE code exchange**: reads `code`/`state` from the query string, validates
  `state` against the stashed value, calls `exchangeCode()`
  (`apps/voice/client/src/auth/oidcClient.ts`) — a `POST` to the token endpoint with
  `grant_type=authorization_code`, the code, the verifier, `redirect_uri`, and
  `client_id` (no secret) — and stores the resulting access token **in memory only**
  (`apps/voice/client/src/auth/tokenStore.ts`).
- **Bypass `/join` auto-login** (see the 2026-07-10-bypass-join-login-design spec): if
  the URL **fragment** carries `access_token` (minted by the auth app's `/join/<token>`
  route for demo/conference codes), `Callback.tsx` ingests it directly and fully
  short-circuits the PKCE round trip — no `code`/`state`/verifier exchange happens at
  all. Using the fragment (never the query string) keeps the bearer token out of
  server access logs and `Referer` headers. Full identity/quota mechanics for this path
  are covered in [docs/dataflows/auth-quota.md](auth-quota.md).

A `login_required` / `interaction_required` error on the callback (silent SSO found no
live issuer session) clears the returning-user breadcrumb and routes back to
`ready`/`land` rather than surfacing an error — this is the expected "session expired"
case, not a failure.

## 2. The one gesture: mic, audio unlock, and greeting priming

Nothing capture- or playback-related happens before the user taps "Start conversation."
`useVoiceSession().start()` (`apps/voice/client/src/transport/useVoiceSession.ts`) does,
in order, inside that single gesture:

1. `requestMic()` (`apps/voice/client/src/media/getMic.ts`) — feature-detects
   `navigator.mediaDevices.getUserMedia` first, then requests `{ audio: true }`.
   Failures are classified into three honest, distinct states — `"unsupported"`,
   `"denied"`, `"no-device"` — never merged into one generic error. The probe stream
   is stopped immediately after permission is proven (`mic.stream.getTracks()...stop()`)
   because `PipecatClient` (`enableMic: true`) does its own `getUserMedia` inside
   `connect()`; leaving the probe stream open produced two concurrent captures and a
   silent mic track, confirmed via `chrome://webrtc-internals`.
2. `unlockAudioPlayback()` (`apps/voice/client/src/greeting/greetingPlayer.ts`) — plays
   and immediately pauses a 1-sample silent `data:` URI `Audio` element, which "blesses"
   later out-of-gesture `.play()` calls under WebKit/Safari's autoplay policy.
3. `primeGreeting()` — arms the **real** greeting `Audio` element (picked at random from
   `client/public/greetings/greetings.manifest.json`, rendered ahead of time by
   `apps/voice/scripts/render_greetings.py` from the configured ElevenLabs voice) at
   `volume = 0` and plays-then-pauses it under the same gesture. Volume 0 (not `muted`)
   keeps it an unmuted play so WebKit still blesses the element, while guaranteeing it
   makes no sound during priming — a later resume at full volume is what the user
   actually hears.

Only after these three steps does `beginConnect()` fire the actual `/api/offer` POST.

## 3. Signaling: `POST /api/offer`

`createVoiceSession()` (`apps/voice/client/src/transport/voiceSession.ts`) builds a
`@pipecat-ai/client-js` `PipecatClient` wired to a `@pipecat-ai/small-webrtc-transport`
`SmallWebRTCTransport`, pointed at the same-origin `/api/offer` endpoint
(`buildConnectParams()`). The bearer token is attached as an `Authorization: Bearer …`
header — never a query string, never logged — because `server.py`'s token check reads
that header **before** the vendor client's own `request_data.access_token` fallback is
normalized into shape. If the page path is `/voice2` (the full-duplex default variant),
the request also carries `?variant=voice2`; `/voice1` sends the bare, byte-identical
endpoint it always used. The variant is re-validated server-side against a fixed
allowlist (`klanker_voice.variants`) — the client's choice is a UX convenience, never a
trust boundary.

On the server, `apps/voice/server.py`'s `offer()` handler runs the enforcement chain
described in `apps/voice/server.py:1-20`:

```
validate_access_token()  ->  start_gate(identity)  ->  WebRTC transport
```

1. `_extract_bearer_token()` pulls the token from the `Authorization` header (or the
   fallback body field).
2. `validate_access_token()` (`apps/voice/src/klanker_voice/auth.py`) — fully offline
   RS256 verification: a recognized smoke/service credential
   (`KMV_SMOKE_SERVICE_TOKEN`, constant-time compared) short-circuits with
   `bypass_accounting=True`; otherwise the token's signing key is resolved via
   `PyJWKClient` against the JWKS at `https://auth.klankermaker.ai/use1/api/oidc/jwks`,
   then `iss`/`aud`/`exp` are checked and the namespaced
   `https://klankermaker.ai/tier_id` / `https://klankermaker.ai/group` claims are read.
   Any failure returns `401 {"error": "unauthorized"}` — no WebRTC transport is ever
   created for a bad token.
3. `start_gate()` (`apps/voice/server.py` → `apps/voice/src/klanker_voice/quota.py`) —
   the race-safe DynamoDB quota gate (bypass → site-paused → no-access → at-capacity →
   concurrency-limit → daily-limit → acquire heartbeat lease). A rejection returns a
   typed `403`/`429`/`503` JSON body (`{error, message}`) the client maps to a specific
   gate screen. Full mechanics are in
   [docs/dataflows/auth-quota.md](auth-quota.md).
4. `_negotiate_webrtc()` builds a per-session pipeline (variant-selected config,
   optional ambience `SoundfileMixer`), constructs a `SmallWebRTCTransport`, and hands
   the offer to pipecat's `SmallWebRTCRequestHandler.handle_web_request()`, which
   returns an SDP answer. `apps/voice/src/klanker_voice/webrtc.py`'s
   `gather_public_candidates()` / `inject_public_host_candidate()` then self-advertise
   the Fargate task's public IP as an extra `typ host` ICE candidate (see §4), and a
   Google public STUN server (`stun:stun.l.google.com:19302`, overridable via
   `KMV_STUN_URL`) is offered as a belt-and-suspenders `srflx` candidate. No TURN
   server is configured.
5. The answer is enriched with three additive, backward-compatible fields the vendor
   client ignores unless read explicitly: `session_max_seconds` (the tier's countdown
   cap — the JWT itself only carries `tier_id`, not the numeric limit), `variant_label`
   (e.g. `"KPH(v1)"` / `"KPH(v2)"`, for the live UI tag), and `server_version` /
   `server_built_at` (the running ECS image's git SHA, for the on-screen build stamp —
   see `apps/voice/client/src/version/serverVersionStore.ts`).

A separate `PATCH /api/offer` route (`ice_candidate()`) accepts trickled ICE candidates
against an already-negotiated `pc_id` as the browser's ICE gathering continues — this
route was missing from the original self-hosted entrypoint (the pipecat dev runner
provides it, this production entrypoint originally didn't), which silently dropped every
trickled candidate and caused sessions to connect then drop shortly after. No token
re-check happens on the PATCH: it can only add candidates to an existing connection
resolved by its opaque `pc_id`, never create a session or bypass the POST's gate.

### The client-side answer-inspection workaround

`SmallWebRTCTransport` 1.10.5's `negotiate()` catches **any** error from the
`/api/offer` POST — including a 401/403/429 JSON error body — and silently schedules its
own internal reconnection instead of rejecting; `PipecatClient.connect()`'s promise
therefore never settles on an auth/quota reject, and there's no public callback for the
raw HTTP response. `voiceSession.ts`'s `connect()` works around this with a short-lived
`window.fetch` interceptor scoped to exactly one `connect()` call: it inspects (never
mutates) the `/api/offer` response, and on a non-2xx immediately dispatches a typed
`OFFER_REJECTED` event with the real status and error body, then proactively calls
`client.disconnect()` so the vendor transport stops silently retrying against a gate
that will keep saying no. A residual limitation is documented in the source: an
already-scheduled vendor-internal reconnection attempt can still fire once in the
background after the UI has surfaced "rejected" — a few extra cheap, no-media gate
checks server-side within ~6s, not a spend or security concern since `start_gate`
remains authoritative either way.

## 4. WebRTC media path

Per `apps/voice/src/klanker_voice/webrtc.py` and the CloudFront design in
`docs/superpowers/specs/2026-07-07-cloudfront-static-assets-design.md`, only signaling
(`/api/offer`, `/health`) rides through CloudFront → the ALB → the ECS task. **Media
never traverses CloudFront or the ALB.** Each voice Fargate task runs in a public subnet
with `assign_public_ip = true` (`infra/terraform/live/site/services/voice/service.hcl`);
Fargate's 1:1 NAT means the task's private ENI IP is directly reachable from the
internet via its public IP, so `gather_public_candidates()` reads the task's own ECS
task-metadata (`ECS_CONTAINER_METADATA_URI_V4`), resolves its ENI's public IP via
`ec2:DescribeNetworkInterfaces`, and `inject_public_host_candidate()` duplicates every
`typ host` line in the SDP answer with that public IP substituted in. Any failure in
this chain (no ECS metadata in local dev, a malformed document, an EC2 API error)
degrades to STUN-only rather than raising. The task's ephemeral UDP port range is pinned
to `20000–20100` via a container `system_controls` `net.ipv4.ip_local_port_range`
setting, matching the security group that opens exactly that window.

The browser negotiates ICE against the candidates in the SDP answer, performs a
DTLS/SRTP handshake directly with the task's public IP/UDP port, and from that point on
audio flows peer-to-peer between the browser and the Fargate task — no relay, no TURN.

## 5. Bot readiness, greeting, and going live

`voiceSession.ts` wires `PipecatClient` callbacks directly into the connection-state
reducer (`apps/voice/client/src/transport/connectionState.ts`):

- `onTrackStarted` attaches **only** the bot's incoming remote audio track (never the
  local mic track, which the same callback also fires for) to a headless `<audio>`
  element and calls `.play()` — this is the actual bot-audio playback path; without it
  the pipeline runs end-to-end (mic → STT → LLM → TTS, transcripts/HUD all update) but
  the user hears nothing.
- `onBotReady` dispatches `CONNECTED`.
- `onDisconnected` / `onTransportStateChanged("error")` / `onError` dispatch
  `DISCONNECTED` / `TRANSPORT_ERROR`.

`resolveScreen()` (`apps/voice/client/src/flow/resolveScreen.ts`) only advances to the
`"live"` screen once the outcome is `"connected"` **and** the `Ceremony` intro script has
finished **and** a client instance exists — the orb intro plays first, then `Live.tsx`
mounts. `Live.tsx`'s mount effect is where `playRandomGreeting()` actually runs: it
**resumes** the already-primed `Audio` element (never constructs a fresh one — a fresh,
out-of-gesture element would be blocked by Safari's autoplay policy), restoring its
volume to 1. For the duration of the greeting the mic is explicitly disabled
(`client.enableMic(false)`) and restored when the clip ends or after an 8-second safety
timeout — a plain `HTMLAudioElement` plays outside WebRTC's echo canceller, so on a
phone with the speaker near the mic, KPH would otherwise hear and transcribe its own
greeting and reply to itself (a real mobile bug fixed this way, see
[docs/techniques/highlights.md](../techniques/highlights.md)).

From here the conversation is genuinely full-duplex: `useOrbBinding` drives the orb's
idle/listening/speaking animation and amplitude off RTVI audio-level events (both
`bot_audio_level_enabled` and `user_audio_level_enabled` are explicitly turned on in
`apps/voice/src/klanker_voice/rtvi.py`, since they default off upstream), `Transcript.tsx`
renders live user/bot transcript turns off `RTVIEvent.UserTranscript` /
`RTVIEvent.BotTranscript`, `MicMuteButton.tsx` toggles `client.enableMic()`,
`Countdown.tsx` renders the `session_max_seconds` cap from the offer answer, and
`ComposeBar.tsx` provides a typed-text fallback into the same transcript. Any ambience
bed configured for the active topic (`greenhouse.ambience_*` in the pipeline TOML) is
mixed server-side into the outgoing track by a `SoundfileMixer` attached at
`TransportParams(audio_out_mixer=...)` — this is entirely server-side; the browser never
plays a second audio source for it.

## 6. Ending a session

`endChat()` (`apps/voice/client/src/transport/useVoiceSession.ts`) is the user-initiated
"End chat" path: it tears down the transport (`sessionRef.current.disconnect()`), builds
a clean `SessionSummary`, and resets to idle — `App.tsx` then renders the `SessionEnd`
screen. Server-side, `session.py`'s `SessionLifecycle.release()` is the single idempotent
teardown every path funnels through (explicit end, D-02 wall-clock session cap, D-06
idle-teardown layers, or an abrupt disconnect): it cancels every pending timer, decrements
the task's active-session count, releases the DynamoDB heartbeat lease, and reconciles ECS
scale-in protection.

A worth-noting fix lives in `apps/voice/server.py`'s `_wire_connection_teardown()`: on an
**abrupt** disconnect (tab close, reload, ICE failure) the raw `SmallWebRTCConnection`
fires a `closed` event, but the higher-level transport's own
`on_client_disconnected` handler — registered only after a slow, AWS-bound
`lifecycle.start()` await — could miss it entirely, stranding the heartbeat lease at its
full TTL and walling out a reconnecting user with a `concurrency-limit` 403. The fix
releases the slot **immediately** on the connection's terminal `closed` event, while
leaving the separate 12-second reconnect grace (`session.py`'s
`on_transport_disconnected()` / `on_transport_reconnected()`) reserved for genuinely
transient ICE blips that a same-session reconnect can still recover from.

## 7. Static asset delivery

The SPA itself (`apps/voice/client/`, Vite-built) is **not** served by `server.py` in
production. Per the CloudFront design (`docs/superpowers/specs/2026-07-07-cloudfront-static-assets-design.md`),
`voice.klankermaker.ai` is a single CloudFront distribution with two ordered origins:

| Path pattern | Origin | Notes |
|---|---|---|
| `/api/*`, `/health` | ALB → ECS task | Caching disabled; forwards `Authorization` |
| `/*` (default) | S3 asset bucket (private, OAC) | Hashed assets long-cached; `index.html` no-cache |

This exists specifically so a rolling deploy never serves a stale `index.html`
referencing an asset hash the currently-routed task doesn't have (the "black screen"
failure mode this design fixes) — old and new content-hashed bundles simply coexist in
S3. `server.py`'s own `StaticFiles(html=True)` SPA mount and 404→`index.html` fallback
(`_mount_client_spa()`) remain in the FastAPI app as a local-dev / defense-in-depth
fallback, not the production serving path. Either way, `/api/offer` is called as a
relative same-origin path, so this split required no client code change.

## 8. Failure and edge-case handling

- **Autoplay / iOS audio unlock.** Every playback attempt in this flow (the silent
  unlock element, the primed greeting, the resumed greeting, the bot's remote audio
  track) is deliberately anchored to the single "Start conversation" gesture or chained
  off an already-blessed element; a blocked `.play()` is always caught and reported
  through `reportGreetingFailure()`/`console.warn` rather than left to throw
  unhandled.
- **Distinct mic failure states.** `"unsupported"` / `"denied"` / `"no-device"` each
  render a distinct `MicError` screen copy rather than one generic error
  (`apps/voice/client/src/media/getMic.ts`).
- **Rejected vs. failed, kept distinct.** `connectionState.ts` treats a server
  `OFFER_REJECTED` (auth/quota gate refusal — no media ever started) as categorically
  different from a `TRANSPORT_ERROR` (a post-gate ICE/network failure). A rejection
  routes straight to `GateCard` (never retried automatically); a transport failure
  feeds the bounded retry controller.
- **Bounded auto-retry.** `retryPolicy.ts`'s `createRetryController()` retries a
  transport/ICE failure up to 3 times on a fixed backoff schedule (`500ms, 1000ms,
  2000ms`), surfacing "Reconnecting… (attempt n of 3)" via `retryStatus`, then reports
  `"exhausted"` — never an infinite spinner. The exhausted state renders
  `UdpBlockedWall.tsx` ("This network blocks the audio channel… try cellular or a
  personal hotspot") with a manual "Try again" that resets the schedule and retries
  immediately.
- **Post-connect drop vs. pre-connect failure.** `useVoiceSession.ts` tracks whether
  the current attempt ever reached `"connected"` (`wasConnectedRef`); a
  `DISCONNECTED`/`TRANSPORT_ERROR` **after** that point is routed to a `SessionSummary`
  ("ended" screen) instead of being misrouted into the pre-connect retry/wall flow.
- **Reconnect vs. terminal teardown, server-side.** Covered in §6 above — a transient
  ICE blip gets a 12s reconnect grace; a terminal connection close releases the quota
  slot immediately.
- **SPA deep links.** CloudFront maps S3 403/404 → `/index.html` (HTTP 200) so a
  hard-refresh on `/callback` (or any other client route) doesn't 404; `server.py`'s own
  404 handler mirrors this behavior as a fallback path.

## Related reading

- [docs/dataflows/auth-quota.md](auth-quota.md) — the OIDC token contract, tier/quota
  model, and DynamoDB enforcement in full.
- [docs/dataflows/conversation-loop.md](conversation-loop.md) — what happens once media
  is flowing: VAD → STT → LLM → TTS inside the pipeline.
- [docs/techniques/highlights.md](../techniques/highlights.md) — deeper writeups of the
  trickier fixes touched on here (mobile self-echo, the concurrency slot leak, the
  vendor-transport reject workaround).
- [docs/architecture/overview.md](../architecture/overview.md) — the system-level map
  this page is one deep-dive branch of.
