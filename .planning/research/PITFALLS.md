# Domain Pitfalls

**Domain:** Low-latency speech-to-speech voice agent (Pipecat + SmallWebRTC on AWS Fargate, public quota-gated demo)
**Researched:** 2026-07-04
**Overall confidence:** MEDIUM (web findings cross-verified against official Pipecat/Deepgram/AWS docs and primary GitHub issues)

Phase references map to the three subproject tracks in PROJECT.md / the design spec:
**P1** = infra skeleton, **P2** = auth.klankermaker.ai, **P3** = voice service + client, **P0** = local-first pipeline tuning track.

---

## Critical Pitfalls

Mistakes that cause rewrites, dead demos, or runaway bills.

### Pitfall 1: The 1.2s budget dies in turn-taking and framework defaults, not in vendor TTFBs

**What goes wrong:** Teams pick fast vendors (Deepgram ~300ms, Haiku TTFT ~400ms, Flash TTS ~150ms), sum to ~1.0s on paper, then measure 2–3s in practice. The gap is almost never the vendors — it's endpointing silence windows and pipeline overhead stacking *serially* on top of vendor TTFBs.
**Why it happens:** Three specific traps:
1. **Pipecat's LLM response aggregator** shipped an `aggregation_timeout=1.0` default (v0.0.57+) that adds up to a full second before text reaches TTS. This one parameter has caused more "why is my bot slow" reports than any other single cause. Community fix: `0.3`.
2. **Endpointing double-counting:** Silero VAD `stop_secs` (~0.2–0.5s) *plus* Deepgram's own endpointing/`utterance_end_ms` silence window can stack if both are treated as the turn-end signal. That's 0.5–1.5s of pure silence-waiting before the LLM is even called.
3. **Cross-region round-trips:** every stage is a network hop (browser→Fargate→Deepgram→Anthropic→ElevenLabs→browser). Production measurements show ~570ms of vendor TTFB inside a ~1,050ms total — nearly half the wall time is pipeline overhead and network, and that half grows if the task isn't in us-east-1 close to the vendors.
**Consequences:** The demo feels laggy; the core value ("whoa in ten seconds") fails. Fixing it late means re-tuning every stage under time pressure.
**Prevention:**
- Build the **latency harness first** (scripted WAV → per-stage timestamps → voice-to-voice ms), before persona or infra work. Instrument per-stage TTFB (Pipecat exposes metrics frames) so you always know *where* the milliseconds are.
- Explicitly set `aggregation_timeout` (~0.3s) and audit every aggregator/buffer default when pinning the Pipecat version.
- Pick **one** owner of turn-end: either Silero VAD `stop_secs` or Deepgram endpointing drives the turn, with the other subordinate. Tune the winner to ~200–300ms for demo snappiness, accepting occasional early endpoints.
- Sentence-chunk LLM output to TTS: first sentence goes to ElevenLabs the moment it completes, never wait for the full response.
- Keep Fargate in us-east-1 (already planned) — all three vendors have low RTT from there.
**Detection (warning signs):** Voice-to-voice > 1.5s with all "fast" vendors; per-stage metrics showing large gaps *between* stage completions rather than inside stages; responses that feel like they start exactly ~1s after the LLM clearly finished thinking.
**Phase:** P0 (harness + tuning is the whole point of the local-first track); re-verify in P3 against the deployed task.

### Pitfall 2: WebRTC works on the laptop, fails on Fargate — private-IP host candidates

**What goes wrong:** SmallWebRTC/aiortc gathers host candidates from the task's network interfaces. On Fargate (awsvpc mode) the ENI holds only the **private VPC IP**; the public IP is a NAT mapping the task can't see on any interface. The SDP answer advertises `10.x.x.x`/`172.31.x.x`, the browser can't reach it, ICE times out, and media never flows — even though signaling over the ALB succeeded, so everything *looks* connected until silence.
**Why it happens:** aiortc automatically selects all interface addresses as candidates and has no built-in "advertise this external IP" story like coturn's `external-ip`. Local dev never exposes this because laptop and browser share a LAN. "Worked locally, dead in cloud" is the single most-reported aiortc deployment failure.
**Consequences:** Zero media in production; days of ICE debugging during the deploy crunch.
**Prevention:**
- At container startup, fetch the task public IP from **ECS task metadata endpoint v4** (`${ECS_CONTAINER_METADATA_URI_V4}/task`) and inject it into the transport's advertised candidates (SmallWebRTC exposes ICE/host-IP configuration; verify the exact knob against the pinned Pipecat version — this is the "public-IP delta" the spec already calls out, treat it as a first-class deliverable with its own smoke test).
- Also configure a STUN server (`stun:stun.l.google.com:19302` costs nothing) as backup discovery — but note aiortc srflx behavior behind NAT has its own quirks; the metadata-injection path is primary.
- Security group must allow the bounded UDP range (e.g. 20000–20100) inbound from 0.0.0.0/0, and aiortc must be constrained to that same range — mismatch = intermittent ICE failure that only some sessions hit.
- Write an automated **cloud smoke test**: headless browser (or `kv` helper) posts an offer to the deployed service and asserts ICE reaches `connected` and audio RTP flows, not just that `/api/offer` returns 200.
**Detection:** ICE state stuck in `checking` then `failed`; SDP answer containing only RFC1918 addresses; sessions that connect signaling but produce no audio.
**Phase:** P1 (SG/public-IP infra) + P3 (metadata injection + smoke test). Flag P3 for deeper research on the exact SmallWebRTC ICE configuration API at build time.

### Pitfall 3: Barge-in that half-works — TTS keeps talking or the context corrupts

**What goes wrong:** Two distinct bugs that both destroy the "natural conversation" feel:
1. **TTS not cancelling:** If the user interrupts while TTS audio is still being synthesized/in flight (before Pipecat emits `BotStartedSpeakingFrame`), the interruption guard sees "bot not speaking," skips cancellation, and the ElevenLabs WebSocket keeps streaming — the bot talks *over* the user after they interrupted (pipecat-ai/pipecat #3986). Client-side buffered audio also keeps playing if only the server flushes.
2. **Context corruption:** After an interruption, the assistant message stored in LLM context must be truncated to *what was actually spoken aloud*. If the full generated response is stored, the bot "remembers saying" things the user never heard; if nothing is stored, it repeats itself. Either way the conversation goes subtly insane a few turns after the first barge-in.
**Why it happens:** Interruption is a race across four components (VAD, LLM stream, TTS WebSocket, client audio buffer) and Pipecat's handling has version-specific bugs and defaults. Word-level truncation depends on TTS word timestamps being wired through.
**Consequences:** The flagship "natural barge-in" feature demos badly; worst case the bot argues with itself.
**Prevention:**
- Make barge-in a named test scenario in the latency harness and UAT: interrupt during (a) first 200ms of bot speech, (b) mid-sentence, (c) long monologue. Assert bot audio stops < ~300ms and the next bot turn is coherent with what was heard.
- Pin the Pipecat version after verifying its interruption behavior; read the changelog/issues (#3986, #3985, #2460) for the pinned version before upgrading. Never upgrade Pipecat the week of the conference.
- Verify assistant-context truncation on interruption works with the ElevenLabs service (word-timestamp support) in the pinned version; log the stored assistant message after each interruption during tuning.
- Ensure interruption clears **client-side** playout buffers too (Pipecat JS SDK handles this with WebRTC transport — verify, don't assume).
**Detection:** Bot audio continues > 500ms after user starts speaking; bot references content it never finished saying; bot repeats a previously interrupted sentence verbatim.
**Phase:** P0 (tunable locally with a laptop mic from day one) — this is precisely why the local-first track exists.

### Pitfall 4: Abandoned sessions keep burning money — idle detection is not reliable

**What goes wrong:** User closes the laptop lid / walks out of Wi-Fi range / backgrounds mobile Safari. The WebRTC peer dies silently (no clean disconnect), and the bot pipeline keeps running: Deepgram WebSocket open (billed per minute), the session pinned in the concurrency table, the Fargate task held hot. Pipecat's own idle detection has documented reliability holes here: `on_idle_timeout` not firing when the peer is already dead (#3140), pipeline randomly not cancelled on disconnect (#3179), `EndFrame`/`CancelFrame` hanging when the client is gone (#2249-class bugs).
**Why it happens:** ICE disconnection detection is slow and sometimes never resolves to a terminal state; framework cleanup paths assume a live transport to flush frames through.
**Consequences:** Metered API burn with nobody listening; concurrency slots leak so legitimate users get "too many sessions"; the daily budget kill-switch trips on ghost usage.
**Prevention (belt-and-suspenders, all four):**
1. Pipecat `PipelineIdleDetection` with `cancel_on_idle_timeout=True` and a short timeout (60–120s, not the 5min default).
2. Handle transport disconnect events (`on_client_disconnected` / ICE state change) → hard-cancel the pipeline task with an asyncio timeout around cleanup so a hung `CancelFrame` can't wedge it.
3. **Server-side absolute wall clock:** an independent asyncio timer per session enforcing `tier.session_max_seconds` + small grace, killing the session regardless of framework state. This is the quota enforcer anyway — make it the outermost layer, not a Pipecat processor.
4. Concurrency markers in DynamoDB carry a TTL / heartbeat timestamp; the 15s usage tick doubles as heartbeat, and stale markers (> 60s) are ignored/reaped so crashed tasks can't leak slots forever.
**Detection:** DynamoDB usage rows still ticking with no active WebRTC peer; Deepgram/ElevenLabs dashboard minutes >> quota-recorded minutes; `kv` session list showing sessions older than the max tier length.
**Phase:** P3 (session lifecycle), with the DynamoDB TTL design in P2's quota schema.

### Pitfall 5: Magic links consumed by email scanners; SES sandbox blocks the launch

**What goes wrong:** Two independent auth-killers for a fresh domain:
1. **Link prefetch:** Corporate mail scanners and Outlook SafeLinks GET every URL in incoming mail. next-auth's default email flow consumes the single-use verification token on that GET, so the human clicks a dead link ("Verification failed"). This is next-auth #4965/#1840 — it hits *exactly* the audience of a conference demo (people on corporate email).
2. **SES friction:** New SES accounts are sandboxed (200 msgs/day, verified recipients only), and production access is a manual review that takes days and can be denied on first attempt. A brand-new `klankermaker.ai` sender with imperfect SPF/DKIM/DMARC alignment lands in spam even after production access.
**Why it happens:** The run.auth port from defcon.run brings the code but not the domain reputation or the SES account state; defcon.run's mail already worked, so it's easy to forget these are per-domain/per-account battles.
**Consequences:** Nobody can log in at the conference; the entire quota gate is fronted by a broken front door.
**Prevention:**
- **Request SES production access in week 1 of P1** (the `email` module), not when auth ships. Verify the domain, set up DKIM (SES easy-DKIM), SPF, and a DMARC record (`p=none` initially) before the first send. Send steady low-volume test traffic to Gmail/Outlook/corporate addresses during development to build reputation.
- Change the magic-link flow to an **interstitial confirmation page**: the emailed link lands on a page with a "Sign in" button; the button's POST consumes the token. Scanners GET, humans click. (Alternative: allow N uses within the expiry window — weaker, but a one-line mitigation.)
- Test the full flow from an Outlook/Microsoft 365 mailbox with SafeLinks on — Gmail-only testing hides this bug completely.
- Keep magic-link expiry generous (15–30min); conference Wi-Fi + phone mail clients are slow.
**Detection:** "Token already used" errors in auth logs seconds after send; verification tokens consumed by user-agents that aren't browsers; SES reputation dashboard complaints/bounce upticks; test sends landing in spam.
**Phase:** P1 (SES module + DNS records + production-access request) and P2 (interstitial flow in the run.auth port).

### Pitfall 6: Quota enforcement with read-then-write races — free minutes and leaked slots

**What goes wrong:** Naive flow: read `usage.seconds_used`, compare to tier cap, then start session / increment. Two concurrent session starts (same access code shared with a room of people, or one user double-clicking) both pass the check; concurrency limits and the site-wide kill-switch are similarly bypassed. Separately, atomic `ADD` counters alone can't enforce a cap — they happily increment past it.
**Why it happens:** DynamoDB has no transactions across the natural read→decide→write flow unless you push the decision into a `ConditionExpression`.
**Consequences:** The public-mic-wired-to-metered-APIs threat model in the spec is defeated by a refresh-spam script; budget kill-switch fires late.
**Prevention:**
- Enforce every limit **in the write**: `UpdateExpression: ADD seconds_used :tick` with `ConditionExpression: seconds_used < :cap` (and same pattern for the global daily counter). `ConditionalCheckFailedException` = quota exhausted; use `ReturnValuesOnConditionCheckFailure` to get the current value without a second read.
- Concurrency: acquire a slot with a conditional write (`attribute_not_exists(slot)` or `active_count < :max_concurrent` guarded increment) at session start, release on end, and — per Pitfall 4 — expire stale slots via heartbeat timestamp so crashes don't permanently consume them.
- Accept that the 15s tick means up to ~15s of overage per session past the cap; that's fine — don't over-engineer, the conditional write on the *tick* catches mid-session daily-cap exhaustion (triggering the spoken wind-down).
- Unit-test the race explicitly: two concurrent starts against a 1-concurrent tier must yield exactly one success.
**Detection:** Usage rows exceeding tier caps; concurrent session counts above `max_concurrent` in `kv session list`; kill-switch daily total overshooting its cap by more than one tick interval × concurrency.
**Phase:** P2 (schema + conditional-write patterns), enforced by P3 at session start/tick.

---

## Moderate Pitfalls

### Pitfall 7: Echo → bot interrupts itself (speakerphone/conference-floor mode)

**What goes wrong:** On speakerphone (which is how everyone will demo it at a conference booth), bot audio re-enters the mic. If AEC isn't fully effective, Silero VAD detects "user speech," triggers barge-in, the bot cancels itself mid-sentence, hears the tail of its own audio again, and loops — or the STT transcribes the bot's own words and the LLM answers itself.
**Why it happens:** Browser AEC3 is good but (a) needs 2–5 seconds to adapt after the stream starts, (b) only works if bot audio is played through a path the browser can use as an AEC reference (the WebRTC remote track → `<audio>` element path — fine here, but broken if anyone "improves" playback via raw WebAudio), and (c) degrades with loud speakers + hot mics in noisy rooms.
**Prevention:** Keep `echoCancellation: true` (plus `noiseSuppression`, `autoGainControl`) in getUserMedia constraints and play bot audio via the standard WebRTC sink. Delay/gate mic transmission ~1–2s after connect while AEC adapts (a "connecting" chime covers it). Tune VAD confidence threshold with speakerphone testing, not headphones. Consider requiring slightly longer/louder speech to trigger interruption (VAD `start_secs`/confidence) as the anti-self-trigger knob. Add "phone on speaker at full volume in a noisy room" to the UAT checklist.
**Detection:** Transcripts containing the bot's own phrases; barge-ins with no human speaking; interruption storms in logs (multiple interruptions per second).
**Phase:** P0 tuning (easy to reproduce locally with laptop speakers) + P3 UAT.

### Pitfall 8: iOS Safari — the mic button works but no audio ever plays

**What goes wrong:** On iPhone Safari the session connects, captions even appear, but the bot is silent; or the waveform never moves; or the session dies when the user switches apps. Causes: AudioContext created outside the user gesture (stays `suspended`), `play()` on the remote audio element not gesture-triggered, missing `playsinline`, AudioContext suspension on backgrounding (WebKit bug 237878), audio routed to the earpiece instead of loudspeaker.
**Prevention:** Make the mic-button tap do everything in the gesture handler synchronously: create/resume AudioContext **before** awaiting `getUserMedia`, call `audioEl.play()` in the same handler, set `playsinline`. Handle `visibilitychange` by treating backgrounding as a disconnect with the existing one-click reconnect UX rather than pretending the session survives. Test on a real iPhone early — the Pipecat JS SDK handles some of this, but verify against the pinned SDK version, and iOS Safari is untestable from desktop devtools.
**Detection:** iOS-only reports of silence; `AudioContext.state === 'suspended'` telemetry; sessions from iOS user-agents with connect success but zero outbound audio played.
**Phase:** P3 (frontend). Flag for phone-in-hand testing in every UAT pass.

### Pitfall 9: ALB defaults sabotage signaling and long sessions

**What goes wrong:** ALB's default 60s idle timeout kills any long-lived signaling/control connection (WebSocket data channel fallback, SSE status streams, even slow SDP negotiation on bad conference Wi-Fi). Media is direct UDP and unaffected, but if the client keeps *any* control channel through the ALB (session events, captions transport fallback), it drops at exactly 60s of quiet and the client misreads it as a session failure.
**Prevention:** In the P1 `ecs-service`/ALB module: raise idle timeout to ≥ the max tier session length + margin (e.g. 2400s), and add application-level heartbeats (15–30s ping) on any ALB-traversing persistent connection anyway. Confirm health-check path/port don't route to the UDP media range. Also confirm target group is `ip` type (awsvpc requirement).
**Detection:** Disconnects clustering at exactly 60s (or whatever the timeout is) of control-channel silence; 1006 WebSocket close codes.
**Phase:** P1 (infra defaults) + P3 (heartbeats).

### Pitfall 10: The UDP-blocked-network error is indistinguishable from "the demo is broken"

**What goes wrong:** v1 deliberately has no TURN. On hotel/corporate Wi-Fi, ICE fails after a long timeout (15–30s of spinner), and without explicit handling the user sees a hang — which at a conference reads as "their product doesn't work," not "this network blocks UDP."
**Prevention:** Detect ICE failure fast (watch `iceConnectionState`, cap the wait at ~8–10s) and show a *specific* message: "This network blocks the audio connection (UDP) — try a phone hotspot." Add a pre-flight or diagnostics affordance if cheap. Ensure booth demos run on a hotspot/known-good network as an operational rule. Keep the TURN fallback (coturn sidecar / Cloudflare TURN) noted as the fast-follow, and structure the transport config so adding `iceServers` later is a config change, not a refactor.
**Detection:** Sessions with signaling success but ICE `failed`; connect-failure rate spiking on particular networks/venues.
**Phase:** P3 (client error UX). Accepted limitation per spec — the pitfall is only the *presentation* of the failure.

### Pitfall 11: Pipecat version drift breaks tuned behavior

**What goes wrong:** Pipecat moves fast and has shipped regressions directly relevant to this stack: choppy/robotic SmallWebRTC audio in a specific release, changed aggregator defaults (the 1.0s timeout), interruption-behavior changes, service API renames. An innocent `pip upgrade` before the conference un-tunes weeks of P0 work.
**Prevention:** Pin exact versions (pipecat-ai + transport extras + JS SDK together; they co-version). Record the pinned version and every non-default parameter in a `pipeline-config` doc. Upgrades go through the latency harness + barge-in scenarios as a regression gate. Freeze versions ≥2 weeks before conference use.
**Detection:** Any behavior change after a dependency bump; harness numbers shifting without config changes.
**Phase:** P0 establishes the pin + harness; all later phases respect it.

### Pitfall 12: TTS credit burn from verbosity and interrupted speech

**What goes wrong:** ElevenLabs bills per character *synthesized*, not per character *heard*. A chatty persona (fat concierge prompt → long answers) plus barge-in means paying for full paragraphs the user cut off after one sentence. Teams routinely blow through plan credits in month 2–3; Pro's ~500 min sounds like a lot until a booth runs 8 hours/day.
**Prevention:** Persona prompt hard-constrains response length ("1–3 short sentences unless asked to elaborate") — this is also a latency win. Sentence-chunked TTS limits interrupted waste to roughly the in-flight sentence, and on interruption the ElevenLabs WebSocket must be closed/flushed immediately (ties to Pitfall 3). Set ElevenLabs usage alerts at 50/75/90% of plan; have `kv` surface cumulative TTS characters vs plan so burn rate is visible daily. The site-wide kill-switch already bounds worst case — make sure its cap is derived from the ElevenLabs plan, not picked independently.
**Detection:** ElevenLabs character usage growing faster than quota-recorded session-seconds implies; average response length creeping up in transcripts.
**Phase:** P0 (persona length constraints, chunking) + P3 (usage telemetry in `kv`).

---

## Minor Pitfalls

### Pitfall 13: Scale-in kills live conversations

**What goes wrong:** Autoscaling (1→4 tasks) scales *in* after the rush and ECS terminates a task mid-conversation; sessions are sticky to the task that answered the offer, so those users get dropped.
**Prevention:** Use ECS task scale-in protection (task sets protection while it has active sessions) or generous deregistration/stop timeouts + client one-click reconnect (already spec'd). At conference scale (1–4 tasks), simplest is protect-while-busy plus draining before deploys.
**Phase:** P1/P3.

### Pitfall 14: Deploys during the conference drop everyone

**What goes wrong:** Rolling deploy replaces tasks; every live session dies because sessions are task-sticky with no handoff.
**Prevention:** Treat deploys as maintenance events; deploy off-hours; rely on the reconnect UX. Don't attempt session migration — out of scope complexity.
**Phase:** P3 (ops runbook / `kv` deploy helper messaging).

### Pitfall 15: Local/laptop dev masks half these bugs

**What goes wrong:** The local-first track uses laptop mic + localhost transport — no NAT, no AEC stress, no iOS, no ALB, perfect network. Teams conclude "the pipeline is done" when only the *conversational* layer is done.
**Prevention:** Maintain an explicit "only reproducible deployed/on-device" checklist (Pitfalls 2, 7, 8, 9, 10, 13) and schedule a deployed end-to-end milestone well before the conference, with phone + speakerphone + hotel-Wi-Fi-style testing.
**Phase:** Roadmap-level — build the P3 verification plan around this list.

### Pitfall 16: OIDC token validation done per-15s-tick or with un-cached JWKS

**What goes wrong:** The voice service validates the access token at `/api/offer`. Doing full JWKS fetches per request (or per tick) adds latency and a hard dependency on auth uptime mid-session; conversely, never re-checking means revoked/expired tiers ride out long sessions.
**Prevention:** Validate once at session start with cached JWKS (refresh on `kid` miss); after that, quota ticks are DynamoDB-only (claims already carry the tier — the spec's design is correct, just don't add auth round-trips back in). Token expiry mid-session is fine: the session wall-clock, not the token, bounds the session.
**Phase:** P3.

---

## Phase-Specific Warnings (roadmap summary)

| Phase | Likely Pitfall | Mitigation to bake into the plan |
|-------|----------------|----------------------------------|
| P0 local pipeline | #1 latency stacking, #3 barge-in bugs, #7 echo, #11 version drift, #12 verbosity | Latency harness + barge-in scenarios *first*; pin Pipecat; set `aggregation_timeout`; single endpointing owner; short-answer persona |
| P1 infra skeleton | #2 UDP SG/public-IP delta, #5 SES production access, #9 ALB timeout | Request SES prod access week 1; SPF/DKIM/DMARC before first send; ALB idle ≥ session max; UDP SG range == aiortc port range |
| P2 auth service | #5 scanner-consumed links, #6 quota races | Interstitial confirm-click magic-link page; all limits as DynamoDB ConditionExpressions; race unit tests; slot TTL/heartbeat schema |
| P3 voice service | #2 ICE misadvertisement, #4 abandoned sessions, #8 iOS Safari, #10 UDP-block UX, #16 JWKS | Task-metadata public-IP injection + deployed ICE smoke test; 4-layer session teardown; gesture-scoped audio unlock; fast specific ICE-fail error |

**Research flags for later phases:** P3 needs build-time verification of (a) the exact SmallWebRTC ICE/host-IP configuration API and (b) interruption + word-timestamp context-truncation behavior in the pinned Pipecat release — both are version-sensitive and this file's specifics may age.

## Sources

All findings MEDIUM confidence (web search cross-verified against official docs / primary GitHub issues) unless noted.

- Pipecat production issues & latency tuning: [Pipecat voice agent in production guide](https://luonghongthuan.com/en/blog/pipecat-voice-agent-production-scalable-guide/), [Measuring voice-to-voice latency with Pipecat](https://www.fullstackml.dev/p/15-where-does-the-time-go-measuring), [Pipecat speech input & turn detection docs](https://docs.pipecat.ai/pipecat/learn/speech-input)
- Latency budgets: [Smallest.ai latency budget](https://smallest.ai/blog/designing-voice-assistants-stt-llm-tts-tools-and-latency-budget), [LiveKit voice agent architecture](https://livekit.com/blog/voice-agent-architecture-stt-llm-tts-pipelines-explained), [LiveKit turn detection](https://livekit.com/blog/turn-detection-voice-agents-vad-endpointing-model-based-detection)
- Barge-in bugs (primary sources): [pipecat #3986 interruption guard](https://github.com/pipecat-ai/pipecat/issues/3986), [pipecat #3985 graceful release](https://github.com/pipecat-ai/pipecat/issues/3985), [pipecat #2460 websocket barge-in](https://github.com/pipecat-ai/pipecat/issues/2460)
- Idle/abandoned sessions (primary): [pipecat #3140](https://github.com/pipecat-ai/pipecat/issues/3140), [pipecat #3179](https://github.com/pipecat-ai/pipecat/issues/3179), [Pipeline idle detection docs](https://docs.pipecat.ai/server/pipeline/pipeline-idle-detection)
- WebRTC on AWS / aiortc NAT: [aiortc NAT discussion #763](https://github.com/aiortc/aiortc/discussions/763), [aioice candidate selection #2](https://github.com/aiortc/aioice/issues/2), [ECS Fargate task networking](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/fargate-task-networking.html), [WebSocket→WebRTC migration lessons](https://dev.to/aws-builders/switching-my-ai-voice-agent-from-websocket-to-webrtc-what-broke-and-what-i-learned-3dkn), [SmallWebRTCTransport docs](https://docs.pipecat.ai/api-reference/server/services/transport/small-webrtc)
- ALB timeouts: [ALB WebSocket guide](https://websocket.org/guides/infrastructure/aws/alb/), [WebSocket on ALB+ECS](https://techholding.co/blog/aws-websocket-alb-ecs)
- Deepgram endpointing: [Endpointing & interim results docs](https://developers.deepgram.com/docs/understand-endpointing-interim-results), [UtteranceEnd docs](https://developers.deepgram.com/docs/utterance-end), [deepgram discussion #980](https://github.com/orgs/deepgram/discussions/980)
- DynamoDB quotas: [AWS blog: conditional write errors under concurrency](https://aws.amazon.com/blogs/database/handle-conditional-write-errors-in-high-concurrency-scenarios-with-amazon-dynamodb/), [DynamoDB optimistic locking best practices](https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/BestPractices_ImplementingVersionControl.html)
- Echo/AEC: [WebRTC audio pipeline end-to-end](https://www.forasoft.com/learn/audio-for-video/articles-audio/webrtc-audio-pipeline-end-to-end), [Web Audio echo fix (AEC adaptation)](https://dev.to/hamedhajiloo/how-i-fixed-a-web-audio-echo-problem-with-a-5-second-delay-384h)
- iOS Safari: [webrtcHacks autoplay restrictions](https://webrtchacks.com/autoplay-restrictions-and-webrtc/), [WebKit bug 237878 backgrounded AudioContext](https://bugs.webkit.org/show_bug.cgi?id=237878), [webrtc/samples #1186 loudspeaker routing](https://github.com/webrtc/samples/issues/1186)
- SES/magic links (primary): [next-auth #4965 scanner-consumed links](https://github.com/nextauthjs/next-auth/issues/4965), [next-auth #1840 SafeLinks](https://github.com/nextauthjs/next-auth/issues/1840), [SES production access](https://docs.aws.amazon.com/ses/latest/dg/request-production-access.html), [SES DMARC compliance](https://docs.aws.amazon.com/ses/latest/dg/send-email-authentication-dmarc.html)
- ElevenLabs cost: [ElevenLabs pricing breakdown](https://www.cekura.ai/blogs/elevenlabs-pricing), [ElevenLabs agents billing](https://help.elevenlabs.io/hc/en-us/articles/29298065878929-How-much-does-ElevenLabs-Agents-formerly-Conversational-AI-cost)
