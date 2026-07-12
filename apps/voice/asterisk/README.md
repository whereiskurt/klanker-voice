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

_Placeholder -- filled in by Plan 07._ Once the standalone telephony
controller and the §24 silent answer-gate exist, this section will document
the manual proof: point a SIP softphone (Linphone, baresip, or any
standard client) at `sip:softphone@127.0.0.1:5060` using
`SOFTPHONE_SIP_PASSWORD` from `.env`, place a call, and confirm the agent
answers through the gate, greets without clipping, converses, can be
interrupted (barge-in), and hangs up cleanly.
