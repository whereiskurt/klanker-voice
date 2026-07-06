# meshtk — Knowledge Digest for KPH

## What it is

meshtk is Kurt's Go toolkit for **virtual Meshtastic nodes** — mesh-radio nodes that exist purely in software, with no radio or serial hardware, speaking the Meshtastic protocol over **MQTT + protobufs**. The repo self-describes as "meshtk — A Meshtastic virtual node Toolkit" and its README opens with "TODO: Update this now that we're live at DEFCON 33!" — it shipped and ran live at DEF CON 33 in support of **defcon.run**.

It does three big things:

1. **Virtual node announcement** — put a software-only node on the mesh: broadcast NodeInfo, position, and map reports so it shows up on public mesh maps, and read/respond to encrypted channel messages and PKI private messages.
2. **Fleet simulation** — spin up whole fleets of simulated nodes ("ghosts") that move along GPX tracks, chat, emit telemetry, and ramp up/down gracefully.
3. **Security proxy in front of an MQTT broker** — a Meshtastic-aware reverse proxy that sits in front of Mosquitto, decrypts and inspects every packet, and applies allow/block/kill/slow/rewrite rules. The v1.0 milestone added a **DynamoDB-backed credential cache** so every MQTT CONNECT is validated against defcon.run credentials with sub-millisecond in-memory lookups.

The stated motivation: interactive Meshtastic bots reachable over MQTT — someone posts to a public channel with a one-time password, and a bot on the internet takes action. It is a single static Go binary, Cobra/Viper CLI, ~29K LOC at v1.0.

## How it works

**Architecture** (from `.planning/codebase/ARCHITECTURE.md` and the code):

- `cmd/meshtk.go` — thin main; all logic lives under `internal/`.
- **Command layer** (`internal/app/app.go`, `cmdargs.go`) — Cobra command tree via a fluent CommandBuilder, with env-var mapping (`MESHTK_*` overrides).
- **App layer** — one package per mode: `internal/app/nodeinfo/`, `internal/app/fleet/`, `internal/app/server/`.
- **MQTT/protocol layer** (`internal/mqtt/`) — wraps Eclipse Paho; handles Meshtastic ServiceEnvelope protobufs, channel-PSK AES encryption/decryption, PKI (ECDH + AES-256) private messages, and a JSON **NodeDB** (nodeID → node metadata) persisted to disk.
- **Credential cache** (`internal/credcache/`, `internal/admin/`) — v1.0 addition: DynamoDB lookup + Otter v2 in-memory cache + singleflight dedup + circuit breaker + negative caching, plus an HTTP admin API.
- **Protobufs** (`protos/meshtastic/`) — `makeprotos.sh` clones the upstream meshtastic/protobufs repo, patches, and generates Go code (committed in `generated/`).

**Main commands:**

- `meshtk nodeinfo announce` — connect to an MQTT broker, broadcast NodeInfo/Position/MapReport at an interval, track responding nodes into the NodeDB. This is how a virtual node appears on public mesh maps.
- `meshtk fleet simulate` — run N fleets of virtual nodes with a lifecycle (ramp-up → steady → ramp-down), per-node behaviours (`small`, `friendly` tags), GPS drift and GPX-track movement, optional ChatBot responses (some gated by TOTP one-time passwords that can "unlock chat mode").
- `meshtk server proxy` — TCP listener (default :1883) that speaks proxy protocol (real client IPs behind an AWS NLB), parses each MQTT packet, decodes the Meshtastic envelope, decrypts channel payloads with known keys, then runs a **rule-based PacketDecider**: Allow / Block / Kill / Slow, plus rewrites (e.g., cap HopLimit at 3, profanity-to-emoji rewriting on the public channel). Since v1.0 it also intercepts CONNECT: validates username/password against the credential cache, swaps in generic Mosquitto creds on success, and rejects with CONNACK 0x05 before the broker ever sees a bad client. Blocked-packet logs rotate to S3.
- `meshtk server protobuf` — gRPC listener (default :50051) for remote packet inspection.

**Meshtastic concepts it touches:** MQTT topic scheme (`msh/US/2/e/LongFast`, map topics), channels mapped to topics with per-channel PSK AES keys (`AQ==` default key), port numbers (NODEINFO_APP, POSITION_APP, TEXT_MESSAGE_APP, MAP_REPORT_APP, telemetry), node IDs (uint32 / `!hex` form), hop limits, node roles (CLIENT, ROUTER, TRACKER), hardware models, PKI public/private keypairs per node, and MapReport metadata (firmware, region, modem preset).

**Why a proxy at all:** Mosquitto doesn't support proxy protocol (so behind an NLB it can't see real IPs) and doesn't "speak Meshtastic" (can't see into encrypted channel payloads). meshtk fills both gaps in one front-end instead of writing a broker plugin (`internal/app/server/README.md`).

## Topic map

### Virtual nodes and mesh maps
- meshtk creates Meshtastic nodes that exist only in software — no radio needed — by publishing the same NodeInfo, position, and map-report protobufs a real radio would send over MQTT, so they appear on public mesh maps like meshmap.
- Source pointers: `README.md`, `internal/app/nodeinfo/cmd.go`, `internal/mqtt/publish.go`, `internal/mqtt/node.go`

### The DEF CON ghost fleet
- The "ghosts" are simulated nodes named after hacker legends — hopper, turing, mudge, ladyada, gibson, goldstein, sharp, condor, dt, and friends — that wander DEF CON on embedded GPX tracks as part of defcon.run.
- Each ghost has its own node database file and a GPX route; fleets ramp up, roam, chat, emit telemetry with jitter, and ramp down gracefully.
- Source pointers: `nodes.ghost.*.json`, `internal/embedded/gpx/ghosts/*.gpx`, `internal/app/fleet/simulate.go`, `internal/app/fleet/behaviours.go`

### Routes and the Las Vegas mesh
- The `nodes.route.*` files (east, west, north, south, tribute, sign, lvcc_rebar, lvcc_indoor, lvcc_dds…) are node sets tied to defcon.run running routes and Las Vegas Convention Center locations.
- Source pointers: `nodes.route.*.json`, `internal/embedded/gpx/`

### The Meshtastic-aware security proxy
- meshtk fronts a Mosquitto broker, gets real client IPs via proxy protocol, decrypts Meshtastic payloads with known channel keys, and applies ordered rules: allow, block, kill the connection, slow (rate-limit), or rewrite the packet.
- Example rules: block anything that fails to decrypt, always allow NodeInfo/Position/Text, cap hop limit at 3, and rewrite profanity to emoji on the public channel.
- Source pointers: `internal/app/server/README.md`, `internal/app/server/rules.go`, `internal/app/server/decider.go`, `internal/app/server/proxy.go`, `internal/app/server/inspect.go`

### Credential cache (v1.0 milestone)
- Every MQTT CONNECT is validated against defcon.run credentials stored in DynamoDB, cached in-memory with Otter v2 for sub-millisecond lookups (900s TTL default); valid clients are forwarded with swapped generic broker creds, invalid ones get CONNACK 0x05 before touching Mosquitto.
- Resilience is built in: singleflight dedups concurrent cache-miss fetches, a circuit breaker keeps cache hits working through DynamoDB outages, and 60s negative caching blunts brute-force cost spikes.
- An internal HTTP admin API offers evict, refresh, stats, list, flush, and a health endpoint (always HTTP 200 with a healthy/degraded status field so ECS never kills the task mid-outage).
- Source pointers: `.planning/PROJECT.md`, `internal/credcache/` (auth.go, cache.go, store.go), `internal/admin/server.go`, `internal/app/server/authenticator.go`

### Encryption and PKI
- meshtk handles both Meshtastic channel encryption (PSK AES, e.g. the default `AQ==` key) and PKI private messages (ECDH key exchange + AES-256) — it can send and receive encrypted DMs as a virtual node.
- For PKI decryption of incoming messages it fetches peer public keys from the defcon.run map API at `https://mqtt.defcon.run/map/nodes.json` (a hardcoded dependency, flagged as tech debt).
- Source pointers: `internal/mqtt/crypto.go`, `.planning/codebase/CONCERNS.md`

### Interactive bots and OTP
- The original motivation: bots that listen on MQTT and act on channel messages — with TOTP one-time-password protection so only someone holding the shared secret can trigger actions or unlock chat mode.
- Source pointers: `README.md` (Motivation), `pkg/otp/totp.go`, `pkg/config/config.go` (ChatBot struct: RequiresOTP, UnlocksChatMode)

### Deployment and ops
- Runs on AWS ECS behind a Network Load Balancer (hence proxy protocol), uses the standard AWS credential chain, and archives blocked-packet logs to S3 with rotation; config is YAML (`meshtk.yaml`) merged from home dir, cwd, or `-c` flag, with `MESHTK_*` env overrides.
- Source pointers: `.planning/codebase/INTEGRATIONS.md`, `pkg/network/s3mover.go`, `pkg/config/config.go`

## Cross-links

- **defcon.run.34**: meshtk is defcon.run's mesh backbone tooling — the proxy validates credentials against the **defcon.run DynamoDB schema** (credential CRUD lives in defcon.run, out of meshtk's scope), the PKI key fetch hits `https://mqtt.defcon.run/map/nodes.json`, the DEF CON config targets `mqtt.defcon.org`, and the ghost fleet + LVCC route nodes exist for defcon.run events. Infrastructure conventions (ECS/NLB, S3, credential chain) mirror the defcon.run terraform world.
- **km / klanker platform**: same tooling DNA — Go + Cobra/Viper single-binary CLIs. meshtk predates `kv` but is a sibling in style; notably the string "kph" appears as a passthrough MQTT username in the proxy (Kurt's handle threading through his projects).
- **klanker-voice**: this digest feeds KPH; the `kv` CLI planned for klanker-voice follows the same cobra pattern meshtk and km use. No code dependency.
- **tiogo / kvmlab**: no references found in the meshtk repo; any relationship is stylistic (Kurt's Go CLI lineage), not technical. Say "unsure" if pressed.

## Sample Q→A

1. **Q: What is meshtk?**
   A: It's Kurt's Go toolkit for virtual Meshtastic nodes — software-only mesh radios that live on MQTT. It can announce fake nodes onto mesh maps, simulate whole fleets, and run a security proxy in front of an MQTT broker. It ran live at DEF CON 33 supporting defcon.run.

2. **Q: What's a virtual Meshtastic node?**
   A: A node with no radio hardware at all — meshtk publishes the same NodeInfo, position, and map-report packets a real radio would, over MQTT, so the node shows up on public mesh maps and can even hold encrypted conversations.

3. **Q: What are the ghosts?**
   A: Simulated nodes named after hacker legends — Grace Hopper, Turing, Mudge, Ladyada, Gibson, and more — that roam DEF CON along GPS tracks as part of defcon.run. Each ghost follows its own embedded GPX route and can chat back.

4. **Q: Why does DEF CON's mesh need a proxy?**
   A: Two reasons: Mosquitto can't see real client IPs behind an AWS load balancer, and it can't read Meshtastic's encrypted payloads. meshtk sits in front, decrypts every packet, and applies allow, block, kill, slow, or rewrite rules before anything reaches the broker.

5. **Q: How does it keep bad clients out?**
   A: Every MQTT connect is checked against defcon.run credentials in DynamoDB, cached in memory for sub-millisecond lookups. Valid clients get forwarded with swapped generic credentials; invalid ones are rejected before the broker ever sees them.

6. **Q: Can it rewrite messages?**
   A: Yes — rules can modify packets in flight. For example it caps hop limits at three, and on the public channel it swaps profanity for emoji. Family-friendly mesh, enforced at the proxy.

7. **Q: What language is it written in?**
   A: Go — about twenty-nine thousand lines, compiled to a single static binary with a Cobra CLI. Kurt jokes in the README he'll get ChatGPT to rewrite it in Rust later.

8. **Q: What happens if DynamoDB goes down mid-conference?**
   A: A circuit breaker kicks in — cached credentials keep working, the health endpoint reports "degraded" instead of dying, and ECS leaves the task alone. Graceful degradation, not cascading failure.

9. **Q: What's the OTP thing about?**
   A: The original idea was internet bots you could command from the mesh — post to a public channel with a time-based one-time password, and a bot listening on MQTT takes action. Only someone holding the shared secret can unlock it.

10. **Q: Does it work with encryption?**
    A: Fully — it decrypts channel messages with pre-shared AES keys and handles PKI private messages using elliptic-curve key exchange, so a virtual node can send and receive encrypted DMs like real hardware.

11. **Q: How do the ghosts move?**
    A: Each ghost follows a GPX track embedded in the binary — routes for each ghost plus city tracks like Manhattan and Japan — with simulated GPS drift and jitter so they look like real wandering radios.

12. **Q: Is meshtk open source?**
    A: It's on GitHub under whereiskurt/meshtk, README-tagged "release early, release often" — a work in progress that shipped for real at DEF CON 33.

## Landmines / do-not-say

FLAG — this feeds a PUBLIC agent. Never surface the following, all of which are present in the repo:

- **Private keys in node databases**: every `nodes.ghost.*.json` and `nodes.route.*.json` file contains per-node `privkey` and `pubkey` hex values. Never recite node keys, node IDs (`!hex` / uint32), or their exact latitude/longitude values.
- **PKI private key in config**: `meshtk.defcon.yaml` contains an inline `NodeInfo.PKI.PrivateKey` (and PublicKey). `meshtk.bak.yaml`, `meshtk.liamcottle.yaml`, and `meshtk.sslexample.yaml` may hold similar material — treat all root YAML configs as secret-bearing.
- **MQTT credentials**: configs embed broker credentials (e.g., the defcon config's username/password for `mqtt.defcon.org`, the public `meshdev`/`large4cats` default). Even semi-public ones should not be spoken by the agent.
- **Auth bypass usernames**: hardcoded passthrough usernames ("ghosts", "kph", "ax", "meshmap") skip authentication in `internal/app/server/inspect.go` — do not reveal these or that a bypass exists.
- **USER_CREATION_SEED**: an environment variable used to derive/validate passwords (`inspect.go`); never mention its role or value.
- **Known security bugs** (from `.planning/codebase/CONCERNS.md`): `GenerateKeyPair()` prints private keys to stdout; allowlist enforcement is partially disabled; `math/rand` used for coordinate jitter. Do not advertise exploitable weaknesses of live defcon.run infrastructure.
- **DynamoDB schema/table details** for defcon.run credentials — internal operational detail.
- **Exact node coordinates**: ghost/route files pin precise Las Vegas locations; speak in generalities ("around the convention center"), never raw coordinates.
- Safe to say publicly: the project exists, its architecture, the ghost concept and names (they're hacker-history tributes), and that it ran at DEF CON 33.

## DIGEST COMPLETE — meshtk

Word count: ~1,950 words.
