# Feature Research

**Domain:** Public browser-based conversational voice agent demo (speech-to-speech, quota-gated)
**Researched:** 2026-07-04
**Confidence:** MEDIUM (web-sourced, cross-checked across ElevenLabs, OpenAI, Hume, Sesame, Vapi/Retell materials and Pipecat docs)

## Context

Reference products analyzed: ElevenLabs Conversational AI / ElevenLabs UI component library, OpenAI Advanced Voice Mode (Realtime API), Sesame (Maya), Hume EVI, Vapi and Retell (operator side). The bar for "slick" is set by these products; a demo that misses table stakes below reads as a hackathon project, not a product.

One cross-cutting finding: **the demo is judged in the first 10 seconds by latency and interruption behavior, not by features.** Perceived-latency research is consistent: <300ms turn gap feels human, 300–500ms feels responsive, 500–800ms is noticeable, >800ms feels delayed, >1500ms feels broken. The project's 1.2s budget is a *ceiling*, not a target — tuning should aim for ~800ms typical, with the 1.2s budget reserved for worst-case turns.

## Feature Landscape

### Table Stakes (Users Expect These)

#### Client UX

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| Gesture-gated mic permission flow | Requesting mic on page load gets ~12% approval; after a user gesture ~30%. Every polished demo primes the user ("tap to talk") before the browser prompt | LOW | Request `getUserMedia` only from the mic-button click; use `navigator.permissions.query()` to pre-detect state and skip priming if already granted |
| Distinct mic-error states | Users hit denied / no-device / device-in-use constantly; a generic "error" loses them | LOW | Handle `NotAllowedError` (show browser-specific unblock instructions), `NotFoundError`, `NotReadableError` separately. Error names differ Chrome vs Firefox |
| Explicit connection state machine | ElevenLabs Conversation Bar exposes disconnected/connecting/connected/disconnecting visually; users need to know "is it listening?" | LOW | States: idle → mic-request → connecting (SDP/ICE) → connected → reconnecting → ended/error. Each gets distinct UI |
| Live captions (user + agent) | Every reference product shows a running transcript; it's also the accessibility story and the "is it hearing me right?" trust signal | MEDIUM | Pipecat RTVI events carry STT partials/finals and LLM text to the JS client. Show user partials live (greyed), finalize on endpoint |
| Audio visualization tied to agent state | The ElevenLabs orb and OpenAI's animation set the expectation: the UI must *visibly* distinguish listening / thinking / speaking | MEDIUM | A level-reactive waveform is the floor; a state-aware orb (listening/thinking/speaking) is the standard. Drive from RTVI bot-state events + audio levels |
| Barge-in (interruption) | The #1 "is this real?" test at a conference. OpenAI, Hume, ElevenLabs all stop instantly when you speak | MEDIUM | Pipecat interruption handling cancels TTS + in-flight LLM out of the box; SmallWebRTC gives browser echo cancellation so the agent doesn't interrupt itself. Requires tuning, not building |
| Tuned endpointing | Default VAD `stop_secs` (~0.8s+) makes every turn feel laggy; the single cheapest latency win | MEDIUM | Silero VAD `stop_secs` in the 0.2–0.5s range for demo snappiness; too low causes mid-sentence cutoffs. This is the core local-tuning loop |
| Visible session countdown timer | Quota-gated demos must telegraph limits; a surprise cutoff feels broken, a visible timer feels fair | LOW | Already in spec. Sync from server tier claims + tick; don't trust client clock |
| Mute toggle + mic-active indicator | Privacy table stakes on any always-listening page; every reference widget has it | LOW | Visible "mic live" indicator whenever the track is open |
| Clean session end + one-click reconnect | Task death / provider error must never leave dead air; spec's "spoken apology, clean close" matches industry practice | MEDIUM | Client detects `RTCPeerConnection` state change → shows reconnect CTA (new quota-checked session) |
| UDP-blocked / ICE-failure error path | Hotel/conference Wi-Fi will hit this; a hang with no message is the worst failure mode of the whole demo | MEDIUM | Detect ICE failure timeout → specific "this network blocks the audio path — try a hotspot" message. Documented v1 limitation, but the *message* is table stakes |
| In-session context memory | Sesame reviews specifically call out "remembered details from two minutes earlier" as what made it feel real | LOW | Free in a cascaded pipeline — full conversation history rides in the Claude context. Just don't truncate aggressively |

#### Operator / Admin

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| Access-code CRUD | Codes are the distribution mechanism; operator must mint/expire/revoke without a deploy | LOW | `kv code create/list/revoke` with tier, expiry, max-redemptions, redemption count (matches spec's access_codes table) |
| Tier management | Quota values will be tuned live at the conference ("bump kphdemo to 45 min") | LOW | `kv tier list/set` over the DynamoDB tiers table |
| Usage visibility | Vapi/Retell/ElevenLabs all lead with usage dashboards; the CLI equivalent is per-user/per-day seconds and daily totals | LOW | `kv usage --today`, `kv usage --user <id>`; reads the usage table the voice service already ticks every 15s |
| Site-wide kill switch | Public mic wired to metered APIs demands a one-command stop; spec already requires the daily budget gate | LOW | `kv killswitch on/off` + `kv budget status`. The check lives at session start in the voice service |
| Active session visibility | "Is anyone on right now? Who's eating the quota?" is the first question during a live event | MEDIUM | `kv sessions` listing live sessions (user, tier, elapsed, task). Requires the concurrency markers the quota model already writes |

### Differentiators (Competitive Advantage)

| Feature | Value Proposition | Complexity | Notes |
|---------|-------------------|------------|-------|
| Spoken quota wind-down | No reference demo handles quota exhaustion *in-voice*. Agent saying "we've got about 30 seconds left — any last questions?" turns the demo's biggest constraint into its most charming moment | MEDIUM | Already in spec. Requires injecting a system message into the LLM turn near `session_max_seconds − 30s` and a graceful goodbye at zero |
| Deeply personal persona | ElevenLabs/OpenAI demos are generic assistants. A concierge that *actually knows* Kurt, klanker, and defcon.run gives conference visitors something no vendor demo has: specificity | LOW | Fat versioned markdown prompt (spec). Content quality is the work, not code |
| Latency HUD (debug overlay) | For a technical (DEF CON-adjacent) audience, showing live per-stage ms (VAD/STT/LLM/TTS/network) is itself a "whoa" — it proves the engineering instead of hiding it | MEDIUM | Toggleable overlay fed by Pipecat metrics events. Also doubles as the tuning instrument for the local-first track |
| Smart turn detection (semantic endpointing) | Plain VAD cuts off "so... what I mean is—". Pipecat's SmartTurnAnalyzer classifies complete/incomplete turns from the last 8s of audio, distinguishing backchannels ("uh-huh") from real barge-in — the difference between "good demo" and "Sesame-tier feel" | MEDIUM | Requires Silero VAD enabled; adds ~model-inference latency at each pause. A/B against tuned `stop_secs` alone during local tuning; adopt only if it wins on feel |
| Instant acknowledgment / latency masking | The perceived-latency literature is unanimous: an immediate "mm-hmm" or breath sound while the LLM thinks makes 900ms feel like 300ms | MEDIUM | Options: pre-cached short TTS acks fired on end-of-turn, or prompt the LLM to open with a short discourse marker so first sentence chunks to TTS fast. Tune carefully — overdone fillers feel gimmicky |
| Frictionless code handout | "Any code is accepted, known codes unlock tiers" (spec) beats every signup-wall demo at a conference — hand someone a card with `kphdemo123` and they're talking in 30s | LOW | Differentiating *flow*, not code: magic-link + optional code field. Keep the login → talking path under a minute |
| `kv` as scriptable operator surface | Vendor dashboards are click-ops; a Go CLI matching `km` gives scriptable, SSH-able, demo-day-fast operations and a consistent klanker tooling family | MEDIUM | Differentiator for the *operator* (Kurt), not visitors — which is exactly who the operator features serve |

### Anti-Features (Commonly Requested, Often Problematic)

| Feature | Why Requested | Why Problematic | Alternative |
|---------|---------------|-----------------|-------------|
| Web admin dashboard | Every vendor has one; feels "complete" | Weeks of UI work for a single operator; auth surface on admin functions of a public demo | `kv` CLI against DynamoDB (already decided in spec) |
| Text-input / chat fallback | ElevenLabs widget offers voice+text switching | Dilutes the speech-to-speech story, doubles the client surface, and invites people to *type* at a voice demo | Voice-only; the no-access tier page explains how to get a code instead |
| Emotion detection display (Hume-style) | Hume's 48-expression live meter is genuinely cool | Requires an expression-analysis model the cascaded pipeline doesn't have; big scope for a gimmick outside the core value | State-aware orb + captions deliver the "alive" feel; Claude can react to *content* sentiment via prompt |
| Tool calling / live data | "Ask it to check X" demos well in videos | Round-trip pauses blow the latency budget and the slick feel (already out of scope in spec) | Fat prompt with pre-baked knowledge; revisit post-v1 |
| RAG / knowledge retrieval | "It should know everything in the repos" | Retrieval adds 100–500ms before TTFT and an infra dependency; prompt space is sufficient for concierge scope | Versioned fat system prompt (spec decision) |
| Audio recording storage / playback | Vendor platforms all store call recordings | Privacy liability for a public demo (biometric voice data), storage cost, consent complexity | Store text transcripts/metadata only for session inspection; discard audio at session end |
| Cross-session user memory | "It remembered me from yesterday!" wow factor | Privacy expectations, storage schema, and prompt-injection-of-stale-context problems; in-session memory already delivers the effect | Strong in-session memory; greet returning users by tier, not by history |
| Voice/persona picker | Playground-style voice selection (Hume, ElevenLabs) | Multiplies TTS voice cost testing, dilutes the single-concierge brand, adds UI | One excellent tuned ElevenLabs voice |
| Animated avatar / face | "Put a face on it" | Uncanny valley risk, render cost, and it shifts attention from the voice quality — the actual differentiator | Abstract orb/waveform (industry standard for a reason) |
| TURN fallback in v1 | Conference Wi-Fi blocks UDP sometimes | Adds a relay vendor or coturn sidecar + per-minute cost before the core demo is proven | Clear client-side error + "use a hotspot" message; TURN is the documented first post-v1 enhancement |

## Feature Dependencies

```
Barge-in
    └──requires──> Echo cancellation (SmallWebRTC browser AEC)
    └──requires──> VAD active during agent playback (Pipecat default)

Smart turn detection ──requires──> Silero VAD enabled
Tuned endpointing (stop_secs) ──feeds──> Smart turn fallback timer

Live captions ──require──> RTVI event transport (Pipecat JS client)
Agent-state orb ──requires──> RTVI bot-state events (same transport as captions)
Latency HUD ──requires──> Pipecat metrics events (same transport)

Session countdown timer ──requires──> Tier claims in OIDC token (auth service)
Spoken wind-down ──requires──> Usage tick (15s) + session timer + TTS priority injection
One-click reconnect ──requires──> Quota check at /api/offer (else reconnect = quota bypass)

kv usage/sessions ──require──> usage table ticks + concurrency markers (voice service)
kv killswitch ──requires──> global budget check at session start (voice service)
kv code/tier CRUD ──require──> access_codes + tiers tables (auth service schema)

Instant acknowledgment ──conflicts──> Smart turn detection (both add per-turn latency;
    stacking them can push past the 1.2s ceiling — tune together, not separately)
```

### Dependency Notes

- **Client polish rides one rail:** captions, orb state, and the latency HUD all consume the same Pipecat RTVI event stream. Once the event plumbing exists, all three are incremental UI work — plan them together.
- **Quota features span both services:** the auth service *mints* tier claims; the voice service *enforces* them and writes usage. `kv` only reads/writes DynamoDB — it has no runtime dependency on either service, which is what makes it safe as an emergency tool (kill switch works even if auth is down).
- **Reconnect must re-check quota** or it becomes the abuse vector for the entire quota system.
- **Latency-adding features compete for the same budget:** smart turn (+inference per pause) and acknowledgment sounds (+audio before real response) both trade raw latency for *perceived* quality. Evaluate on feel during local tuning, not in isolation.

## MVP Definition

### Launch With (v1)

- [ ] Gesture-gated mic flow with distinct error states — first impression gate
- [ ] Connection state machine + ICE-failure/UDP-blocked messaging — conference Wi-Fi reality
- [ ] Barge-in with tuned endpointing (`stop_secs` sweep during local tuning) — the "whoa" test
- [ ] Live captions + state-aware visualization (waveform minimum, orb preferred) — trust + polish
- [ ] Session countdown timer + spoken wind-down — quota UX is product UX here
- [ ] In-session context memory (full history in LLM context) — free, high-impact
- [ ] Clean end / one-click quota-checked reconnect — never dead air
- [ ] Access codes → tiers → quotas + site-wide kill switch — can't go public without it
- [ ] `kv`: code CRUD, tier set, usage today, killswitch — minimum operator loop
- [ ] Concierge persona prompt v1 — the content *is* the demo

### Add After Validation (v1.x)

- [ ] Smart turn detection — if local A/B shows feel improvement within latency budget
- [ ] Instant-acknowledgment latency masking — if typical turn latency lands >700ms after tuning
- [ ] Latency HUD overlay — build early as a tuning tool; polish into a visitor-facing toggle for the conference
- [ ] `kv sessions` live view — when first multi-user event is scheduled
- [ ] Orb upgrade (if v1 shipped waveform-only)

### Future Consideration (v2+)

- [ ] TURN fallback (coturn sidecar / Cloudflare TURN) — when UDP-blocked failure rate is measured and matters
- [ ] Tool calling — only with a latency-safe pattern (e.g., spoken filler during call)
- [ ] RAG — only if persona outgrows prompt space
- [ ] Cross-session memory, voice picker — only with a real user base and privacy posture

## Feature Prioritization Matrix

| Feature | User Value | Implementation Cost | Priority |
|---------|------------|---------------------|----------|
| Barge-in + endpointing tuning | HIGH | MEDIUM | P1 |
| Mic permission + error flows | HIGH | LOW | P1 |
| Captions + state visualization | HIGH | MEDIUM | P1 |
| Session timer + spoken wind-down | HIGH | MEDIUM | P1 |
| Quota system + kill switch | HIGH (operator survival) | MEDIUM | P1 |
| `kv` code/tier/usage/killswitch | HIGH (operator) | LOW | P1 |
| Persona prompt quality | HIGH | LOW | P1 |
| One-click reconnect | MEDIUM | LOW | P1 |
| Latency HUD | MEDIUM (HIGH for this audience) | MEDIUM | P2 |
| Smart turn detection | MEDIUM | MEDIUM | P2 |
| Acknowledgment/latency masking | MEDIUM | MEDIUM | P2 |
| `kv sessions` live view | MEDIUM | MEDIUM | P2 |
| Orb (vs plain waveform) | MEDIUM | MEDIUM | P2 |
| TURN fallback | MEDIUM | HIGH | P3 |
| Emotion display, avatar, voice picker | LOW | HIGH | P3 (anti-feature leaning) |

## Competitor Feature Analysis

| Feature | ElevenLabs Conv. AI | OpenAI Adv. Voice | Hume EVI | Sesame (Maya) | Our Approach |
|---------|--------------------|--------------------|----------|---------------|--------------|
| Visualization | 3D orb w/ listening/thinking/speaking states | Minimal animated orb | Waveform + live emotion meters | Minimal UI, voice carries it | State-aware orb/waveform from RTVI events |
| Captions | Transcript viewer component | Live transcript | Timestamped transcript + emotion data | Sparse | Live user partials + agent text |
| Barge-in | Yes, instant | Yes, server/semantic VAD w/ eagerness knobs | Yes, "stops rapidly, resumes with context" | Yes, natural | Pipecat interruption + AEC; tuned locally |
| Turn detection | Managed | Server VAD + semantic VAD (~300ms gap) | Managed | Model-conditioned on conversation context | Silero `stop_secs` tuning → optional smart-turn |
| Latency feel | Fast | ~300–500ms class | Fast | Best-in-class rhythm + prosody | ≤1.2s ceiling, ~800ms target, masking if needed |
| Access gating | API keys / plans | Account | Account + playground | Waitlist-style demo | Magic link + any-code-accepted tiers (lower friction than all of them) |
| Operator visibility | Call history + eval criteria + analytics dashboard | Platform usage pages | Dashboard | n/a | `kv` CLI: codes, tiers, usage, sessions, killswitch |
| Spend control | Plan limits, concurrency tiers | Rate limits | Plan limits | n/a | Per-tier quotas + daily budget kill switch + spoken wind-down (unique) |

## Sources

- ElevenLabs UI component docs (orb, conversation bar, voice button, transcript viewer): ui.elevenlabs.io/docs — MEDIUM
- ElevenLabs Agents platform docs (conversation analysis, call history, analytics dashboard): elevenlabs.io/docs — MEDIUM
- OpenAI voice agents guide / Realtime API VAD & interruption docs and practitioner writeups (Medium: Realtime interruption handling) — MEDIUM
- Sesame "Crossing the uncanny valley of conversational voice" + third-party analyses (FlowHunt, Sidecar, R&D World) — MEDIUM
- Hume EVI product/docs (empathic-voice-interface, dev.hume.ai FAQ & overview) — MEDIUM
- Pipecat docs & GitHub (Smart Turn overview, SmartTurnParams, Silero VAD `stop_secs`, interruption issues #4912/#3844, smart-turn repo) — MEDIUM
- Vapi/Retell platform comparisons (softcery.com 2026 platform roundup, retellai.com comparisons, lindy.ai review) — MEDIUM (vendor-adjacent; cross-checked)
- Voice latency benchmark posts (Hamming AI, Telnyx, Trillet, bitbytes.io, simbavoice) — MEDIUM (consistent across ≥4 independent sources)
- Mic permission UX (MDN getUserMedia, Speechmatics browser-mic post, addpipe getUserMedia 2026 guide, permission-prompt approval-rate research) — MEDIUM
- API abuse prevention / tiered quota patterns (Cloudflare anonymous credentials, TrueFoundry, Solo.io tiered rate limiting) — MEDIUM

---
*Feature research for: public browser-based conversational voice agent demo*
*Researched: 2026-07-04*
