# Local Asterisk edge (Phase 11, D-01/D-07)

A portable, macOS-Docker-Desktop-safe local Asterisk dev harness: a narrow
inbound-only Stasis dialplan, authenticated private-only ARI, and a
ulaw-only PJSIP softphone endpoint. This is real infra (spec Phase C) but
still entirely local -- no VoIP.ms subaccount, no public DID, no cloud
infra (all Phase 12-14).

## Files

| File | Purpose |
|------|---------|
| `http.conf` | Asterisk's built-in HTTP server (ARI rides this). Bound to `127.0.0.1` -- never a public interface (§18/§25.C). |
| `ari.conf` | Authenticated ARI user `klanker`; `allowed_origins` empty (server-to-server only, no browser CORS). |
| `pjsip.conf` | One `udp` transport + one softphone endpoint, ulaw-only (`disallow=all` / `allow=ulaw`), `context=from-klanker-inbound`. |
| `extensions.conf` | The single `[from-klanker-inbound]` context: `Answer()` -> `Stasis(klanker)` -> `Hangup()`. No outbound context, no `Dial()`, ever (§25.A). |
| `rtp.conf` | Narrow `10000-10020` RTP port range matching what `docker-compose.yml` publishes. |
| `docker-compose.yml` | The dev harness: `asterisk-config-render` (renders secrets), a pinned-version `asterisk` service, and `klanker-telephony` (the standalone controller, shared netns). |
| `render_configs.py` | Renders `ari.conf`/`pjsip.conf`'s `${VAR}` placeholders with real secrets from `.env` into gitignored `.rendered/` -- run by the `asterisk-config-render` service. |
| `.gitignore` | Ignores `.rendered/` (real secrets, never commit). |
| `.env.example` | Placeholders for the six telephony secrets (D-09) -- copy to `.env`, never commit real values. |

## Bring-up

```bash
cd apps/voice/asterisk
cp .env.example .env   # fill in local-dev-only values; .env is gitignored
docker compose config  # validates the compose file
docker compose up      # config-render -> Asterisk -> the Klanker telephony
                        # controller, all wired together in one command
```

A plain `docker compose up` now brings up the FULL wired stack: the
`asterisk-config-render` sidecar substitutes real secrets from `.env` into
gitignored `.rendered/{ari,pjsip}.conf`, `asterisk` starts against those
rendered configs, and `klanker-telephony` (the standalone controller)
connects to it over private ARI -- no extra manual step. Run
`docker compose up asterisk` instead if you only want the edge without the
controller (e.g. to run the controller yourself on the host for debugging).

## Module smoke check

Confirms the pinned image actually ships `res_ari`/`res_stasis` compiled in
(proves T-11-02-SC's tampering mitigation didn't silently regress across an
image-tag bump):

```bash
docker exec klanker-asterisk-dev asterisk -rx 'core show application Stasis'
```

A working image prints the `Stasis` application's usage/synopsis text. If
it instead prints "No such application", the image build is missing
`res_stasis`/`res_ari` and the pinned tag needs to change.

This smoke check talks to the Asterisk CLI over its Unix control socket
inside the container (`docker exec`) -- it does **not** require the ARI
HTTP port to be reachable from the host, so it works regardless of the
known limitation below.

## Secrets

All six secret names (`ASTERISK_ARI_URL`, `ASTERISK_ARI_USERNAME`,
`ASTERISK_ARI_PASSWORD`, `SOFTPHONE_SIP_PASSWORD`, `TELEPHONY_ACCESS_PIN`,
`TELEPHONY_PASSPHRASE_WORDS`) are sourced from `.env` (local dev) --
never committed. `ari.conf` and `pjsip.conf` reference
`${ASTERISK_ARI_PASSWORD}` / `${SOFTPHONE_SIP_PASSWORD}` as the *name* of
the env var the value comes from, matching D-09's "secrets never
hardcoded" posture. Production secret provisioning (SSM) is Phase 14.

**RESOLVED (was: `${VAR}` substitution is documentation-only).** Asterisk's
own `.conf` parser does not perform `${VAR}` shell-style substitution --
unlike docker-compose's own `environment:` block (which *does* interpolate
host env vars, used above for the render sidecar's own env). This is now
solved by the `asterisk-config-render` compose service: it runs
`render_configs.py`, which substitutes real secrets from `.env` into
gitignored `.rendered/ari.conf` and `.rendered/pjsip.conf` at container
start (`string.Template.safe_substitute`, so passwords with shell-special
characters render correctly). The `asterisk` service bind-mounts those
rendered copies instead of the tracked templates; the tracked `ari.conf`/
`pjsip.conf` under this directory keep their `${...}` placeholders
unchanged forever, so `test_asterisk_configs.py`'s lint stays green and no
real secret is ever committed.

**RESOLVED (was: ARI loopback-vs-published-port mismatch).** `http.conf`'s
ARI HTTP server binds to the *container's own* `127.0.0.1` (private/
loopback, per D-01/§18/§25.C -- this bindaddr is unchanged). The
`klanker-telephony` service now solves the reachability problem by sharing
Asterisk's network namespace (`network_mode: "service:asterisk"`), so
`http://127.0.0.1:8088` resolves correctly *inside* that shared namespace.
As a direct consequence, ARI is no longer published on the host at all --
`docker-compose.yml`'s old `8088:8088` publish (which forwarded to a
non-listening loopback and reached nothing) has been removed entirely, a
strict tightening of the private-only posture, not a relaxation.

## Manual §19-C softphone proof

The standalone telephony controller (`klanker_voice.telephony`, D-08) and
the §24 silent answer-gate (D-05) both exist now. This section is the
real recipe for the one exit criterion that genuinely cannot be
automated: a human's ear judging whether the greeting is clipped, whether
barge-in feels natural, and whether the call behaves cleanly end-to-end
over a real SIP leg with the real Deepgram/Anthropic/ElevenLabs pipeline
behind it.

### CI-vs-manual boundary (read this first)

`apps/voice/tests/test_telephony_integration.py` is the CI-required,
deterministic artifact (D-07). It drives the controller against a
**fake** `AriClient` and a **fake** RTP media session (`FakeAriClient`/
`FakeRtpMediaSession`, reused from `test_telephony_lifecycle.py`) with
synthetic transcription frames -- it needs no live Docker, no live
Asterisk, and no live provider keys, and it runs green in a fully clean
env (`env -i`). It proves the §16/§17 lifecycle and the §24 gate logic
mechanically: passphrase/DTMF unlock -> `CallSession` + greet; fail-closed
timeout -> goodbye + hangup with no `CallSession` ever built;
`ChannelDestroyed` -> exactly-once teardown, empty registry.

**What CI does NOT and cannot prove:** that the greeting audio is not
clipped, that barge-in actually interrupts TTS playback the way a human
expects, that a real SIP softphone's RTP negotiates and plays back
correctly through Asterisk's `externalMedia` bridge, or that a live
Deepgram/Anthropic/ElevenLabs round trip behaves correctly over the
socket-backed media session. Those are perceptual/integration judgments
that require a human ear and a live stack -- there is no fake-media
substitute for them. This manual proof is that judgment, and it is
tracked as an outstanding human-verify item (see `.planning/STATE.md`)
until someone actually runs it -- never claimed as CI-covered.

The SIPp scenario (`sipp/gate-pass.xml` + the `docker-compose.yml` `sipp`
service, `profiles: [integration]`) is a semi-automated middle tier: it
drives a real SIP call (INVITE/200/ACK/pcap-audio/BYE) against a real
Asterisk container, but still can't judge audio quality or barge-in feel
-- see `sipp/fixtures/README.md` for the pcap-recording recipe. Useful for
regression-testing the SIP/RTP wiring itself; not a substitute for this
manual proof either.

### Runtime requirements & gotchas (validated live 2026-07-12)

The one-command stack was proven end-to-end against a real Linphone softphone.
Four environment facts were essential to get **caller audio** flowing through
Docker Desktop's UDP NAT -- without them the call connects and the gate arms,
but no audio reaches the agent and it fail-closes after `gate_window_seconds`:

1. **`TELEPHONY_MEDIA_ADDRESS` must be your host LAN IP** (`ipconfig getifaddr en0`),
   set in `.env`. Asterisk advertises this in its SDP `c=` line; it refuses to
   advertise loopback as "external" and otherwise falls back to the container's
   internal bridge IP (e.g. `172.20.0.2`), which the host cannot reach -- caller
   RTP is then black-holed. `rtp_symmetric` on the endpoint handles the return leg.
2. **ICE is disabled** (`rtp.conf: icesupport=no`) -- ICE candidate negotiation
   deadlocks through Docker Desktop's NAT.
3. **NAT tolerance is on** (`pjsip.conf` endpoint: `force_rport`/`rewrite_contact`/
   `rtp_symmetric`) -- the softphone reaches Asterisk through Docker's userland NAT
   from a rewritten source, so Asterisk must reply to the actual packet source.
4. **If the source IP looks wrong** (e.g. a public/CDN address in `pjsip show
   contacts`, or signaling 401-loops), **restart Docker Desktop** -- the gvisor
   network stack can cache a stale host-address mapping that makes UDP return
   traffic unroutable. A plain host probe to `127.0.0.1:5060` should show source
   `192.168.65.1` once healthy.

**Softphone auth caveat:** Linphone (desktop 6.x) does **not** retry an INVITE
after a `401` challenge, so a call to an auth-required endpoint silently fails.
For the local proof, the endpoint's `auth=` can be dropped (identity is still
pinned by `username`/`ip`, and the §24 gate is the real access control); a
softphone that retries INVITE auth (or `gate_mode` alone) needs no change.

### Recipe

1. **Fill in secrets.**
   ```bash
   cd apps/voice/asterisk
   cp .env.example .env   # if not already done -- gitignored, local-dev only
   ```
   Fill in `ASTERISK_ARI_PASSWORD`, `SOFTPHONE_SIP_PASSWORD`,
   `TELEPHONY_ACCESS_PIN`, `TELEPHONY_PASSPHRASE_WORDS` (four
   space-separated words), and `TELEPHONY_MEDIA_ADDRESS` (your host LAN IP)
   in `.env`. Separately, make sure the voice
   app's own `.env` (`make -C apps/voice env`) has real Deepgram/
   Anthropic/ElevenLabs API keys -- this is the one proof that actually
   spends provider API calls. No further manual step is needed: the
   `asterisk-config-render` service substitutes the real ARI/SIP
   passwords into gitignored rendered configs automatically at container
   start (see "Secrets" above).

2. **Bring up the full stack.**
   ```bash
   docker compose up
   ```
   One command brings up `asterisk-config-render` -> `asterisk` ->
   `klanker-telephony`, already wired together over private ARI (the
   controller reaches `http://127.0.0.1:8088` by sharing Asterisk's own
   network namespace -- see "Known limitation" notes above, now
   resolved). Confirm the module smoke check (above) still passes:
   `docker exec klanker-asterisk-dev asterisk -rx 'core show application Stasis'`.
   Watch the `klanker-telephony-dev` container's logs for the
   `telephony controller starting: ...` line -- that confirms it
   connected to ARI successfully.

3. **Register a SIP softphone.** Point Linphone, baresip, or any
   standard SIP client at `sip:dev-softphone@127.0.0.1:5060` (endpoint
   `dev-softphone`, username `softphone`) using `SOFTPHONE_SIP_PASSWORD`
   from `.env`. Place a call to any 2+ digit number (e.g. `1000`) -- the
   `_X.` inbound pattern in `extensions.conf` routes any such number to
   `Stasis(klanker)`.

4. **Confirm the gated happy path:**
   - The line answers **silently** -- no greeting, no LLM, no TTS yet
     (§24 gate locked, D-05e: no transcription frame reaches the LLM
     while locked).
   - Speak the 4 passphrase words in any order (or key the DTMF PIN
     instead).
   - The agent then **greets** -- confirm the greeting is **not
     clipped** (the one judgment CI cannot make).
   - Hold a short multi-turn conversation.
   - **Interrupt** the agent mid-response (barge-in) and confirm it
     actually stops speaking and listens.
   - **Hang up** from the softphone and confirm a clean disconnect (no
     hung channel, no error in the controller logs).

5. **Confirm the fail-closed path.** Place a second call and stay
   silent past `gate_window_seconds` (see `configs/telephony.toml`/
   `[telephony]`). Confirm: a static goodbye plays, then a clean
   hangup, with no `CallSession` ever built (matches
   `test_telephony_integration.py`'s fail-closed assertion, now judged
   by ear over a real SIP leg instead of fake media).

6. **Confirm no resource leaks.** After each call above, check the
   `klanker-telephony-dev` container's logs for the single idempotent
   teardown log line (no double-close, no dangling bridge/`ActiveCall`
   registry entry). A leaked `externalMedia` channel or mixing bridge
   would show up as a stale Asterisk-side resource on a subsequent
   `docker exec klanker-asterisk-dev asterisk -rx 'core show channels'`.

### Status

This proof has **not yet been run live** -- the execution sandbox that
authored this recipe has no running Docker daemon, and the human decision
was to defer the live run rather than fabricate a pass. The recipe itself
is now one-command (`docker compose up` brings up the fully wired stack,
no manual ARI-reachability workaround or `${VAR}` substitution step
remains). It is tracked as an outstanding human-verify item in
`.planning/STATE.md` (mirrors how prior voice-pipeline phases tracked
pending live evals, e.g. Phase 5's consolidated live-verification pass
and Phase 7's live-audio benchmark run) until a human with a working
Docker daemon and a SIP softphone actually completes steps 1-6 above.
