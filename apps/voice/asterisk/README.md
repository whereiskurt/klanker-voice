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
| `docker-compose.yml` | The dev harness: a single pinned-version `asterisk` service. |
| `.env.example` | Placeholders for the six telephony secrets (D-09) -- copy to `.env`, never commit real values. |

## Bring-up

```bash
cd apps/voice/asterisk
cp .env.example .env   # fill in local-dev-only values; .env is gitignored
docker compose config  # validates the compose file
docker compose up
```

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

**Known limitation (flagged, not required by this plan):** Asterisk's own
`.conf` parser does not perform `${VAR}` shell-style substitution -- unlike
docker-compose's own `environment:` block (which *does* interpolate host
env vars, used above for the container's own env), the two `.conf` files
above need their `${...}` placeholders replaced with real values before
`docker compose up` if you need a genuinely authenticated ARI session (e.g.
`sed -i '' "s/\${ASTERISK_ARI_PASSWORD}/$(grep ASTERISK_ARI_PASSWORD .env | cut -d= -f2)/" ari.conf`
against a local, gitignored copy -- never edit and commit the tracked
file with a real value). This plan's own verification (config lint +
`docker compose config` + the module smoke check above) does not require a
live ARI HTTP round-trip. Wiring a real substitution step (entrypoint
`envsubst`, or a small local render script) is a discretionary follow-up
for whichever later plan first needs the controller to actually
authenticate against ARI over the network.

**Also flagged:** `http.conf`'s ARI HTTP server binds to the *container's
own* `127.0.0.1` (private/loopback, per D-01/§18/§25.C) -- `docker-compose.yml`
still publishes `8088:8088` for forward compatibility, but a host-run
process cannot reach it through that published port while bindaddr stays
loopback-scoped inside the container (this is expected Docker behavior:
port publishing forwards to a container's routable interface, not its own
loopback). The standalone telephony controller (D-08) that will actually
need this connection is a later plan's work; when it lands it will either
(a) run inside the same compose network, or (b) this file's `bindaddr` will
move to the container's internal `0.0.0.0` paired with a host-loopback-scoped
compose publish (`127.0.0.1:8088:8088`) to preserve the same private-only
guarantee end-to-end. Not required by this plan's scope.

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

### Recipe

1. **Fill in secrets.**
   ```bash
   cd apps/voice/asterisk
   cp .env.example .env   # if not already done -- gitignored, local-dev only
   ```
   Fill in `ASTERISK_ARI_PASSWORD`, `SOFTPHONE_SIP_PASSWORD`,
   `TELEPHONY_ACCESS_PIN`, and `TELEPHONY_PASSPHRASE_WORDS` (four
   space-separated words) in `.env`. Separately, make sure the voice
   app's own `.env` (`make -C apps/voice env`) has real Deepgram/
   Anthropic/ElevenLabs API keys -- this is the one proof that actually
   spends provider API calls.

   Also apply the manual `${VAR}` substitution workaround documented
   above (`ari.conf`/`pjsip.conf` don't shell-substitute) against a
   local, gitignored copy of those two files before bringing the stack
   up, e.g.:
   ```bash
   sed -i '' "s/\${ASTERISK_ARI_PASSWORD}/$(grep ASTERISK_ARI_PASSWORD .env | cut -d= -f2)/" ari.conf
   sed -i '' "s/\${SOFTPHONE_SIP_PASSWORD}/$(grep SOFTPHONE_SIP_PASSWORD .env | cut -d= -f2)/" pjsip.conf
   ```
   Never commit either file with a real value substituted in.

2. **Bring up Asterisk.**
   ```bash
   docker compose up
   ```
   Confirm the module smoke check (above) still passes:
   `docker exec klanker-asterisk-dev asterisk -rx 'core show application Stasis'`.

3. **Resolve the ARI-reachability prerequisite (flagged by 11-02, not
   yet fixed as of this plan).** `http.conf`'s ARI HTTP server is bound
   to the *container's own* `127.0.0.1` -- a host-run
   `klanker_voice.telephony.controller` process cannot reach it through
   the published `8088:8088` port (Docker forwards published ports to a
   container's routable interface, not its own loopback). Pick one:
   - **(a) Run the controller inside the compose network** -- easiest:
     add a `klanker-telephony` service to `docker-compose.yml` (or
     `docker compose run` a one-off container built from the voice
     app's own image) sharing the asterisk service's network namespace
     (`network_mode: "service:asterisk"`, same pattern the `sipp`
     service already uses) so `ASTERISK_ARI_URL=http://127.0.0.1:8088`
     resolves correctly from inside that container.
   - **(b) Adjust the ARI bind** -- move `http.conf`'s `bindaddr` to the
     container's internal `0.0.0.0` and pair it with a
     host-loopback-scoped compose publish (`127.0.0.1:8088:8088`) so a
     host-run controller process can reach it directly while the
     private-only guarantee (D-01/§18/§25.C) is preserved end-to-end
     (still never a public interface). This is the fix a future plan
     should make permanent if the host-run pattern becomes the norm.

   Either way, this prerequisite must be resolved before step 4 below
   can make a real ARI connection -- if it isn't, `ari.connect()` will
   fail to reach Asterisk and this whole proof cannot proceed.

4. **Run the standalone telephony controller** (from wherever you
   resolved step 3 -- host or inside the compose network):
   ```bash
   cd apps/voice
   KLANKER_PIPELINE_CONFIG=configs/telephony.toml \
   ASTERISK_ARI_URL=http://127.0.0.1:8088 \
   ASTERISK_ARI_USERNAME=klanker \
   ASTERISK_ARI_PASSWORD=... \
   TELEPHONY_ACCESS_PIN=... \
   TELEPHONY_PASSPHRASE_WORDS='w1 w2 w3 w4' \
   uv run python -m klanker_voice.telephony.controller
   ```
   (`python -m klanker_voice.telephony` is the equivalent alternate
   entrypoint -- see `telephony/__main__.py`'s own docstring; both are
   wired to the same `main()`.) The voice app's own provider keys
   (Deepgram/Anthropic/ElevenLabs) must already be in `apps/voice/.env`
   -- `load_dotenv(override=True)` picks them up alongside the
   telephony-specific env vars above.

5. **Register a SIP softphone.** Point Linphone, baresip, or any
   standard SIP client at `sip:dev-softphone@127.0.0.1:5060` using
   `SOFTPHONE_SIP_PASSWORD` from `.env`. Place a call to the inbound
   extension.

6. **Confirm the gated happy path:**
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

7. **Confirm the fail-closed path.** Place a second call and stay
   silent past `gate_window_seconds` (see `configs/telephony.toml`/
   `[telephony]`). Confirm: a static goodbye plays, then a clean
   hangup, with no `CallSession` ever built (matches
   `test_telephony_integration.py`'s fail-closed assertion, now judged
   by ear over a real SIP leg instead of fake media).

8. **Confirm no resource leaks.** After each call above, check the
   controller's logs for the single idempotent teardown log line (no
   double-close, no dangling bridge/`ActiveCall` registry entry). A
   leaked `externalMedia` channel or mixing bridge would show up as a
   stale Asterisk-side resource on a subsequent
   `docker exec ... asterisk -rx 'core show channels'`.

### Status

This proof has **not yet been run** as of Phase 11 Plan 07 -- the
execution sandbox that authored this recipe has no running Docker
daemon, and the human decision was to defer the live run rather than
fabricate a pass. It is tracked as an outstanding human-verify item in
`.planning/STATE.md` (mirrors how prior voice-pipeline phases tracked
pending live evals, e.g. Phase 5's consolidated live-verification pass
and Phase 7's live-audio benchmark run) until a human with a working
Docker daemon and a SIP softphone actually completes steps 1-8 above.
