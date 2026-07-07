# meshtk (mesh T K, the meshtastic toolkit) — KPH's deep knowledge pack

> Promoted from `.planning/phases/07-kph-knowledge-base/corpus/meshtk-digest.md`
> (07-03), folding in a few first-person turns of phrase straight from Kurt's
> own recorded transcripts. This is the SWAPPABLE deep pack (system[1]) the
> router loads when a visitor asks about meshtk — it never lives in the
> cached stable prefix (system[0]).

> One-liner: **meshtk is Kurt's Go toolkit for virtual Meshtastic nodes** — mesh-radio
> nodes that exist purely in software, with no physical radio or serial hardware,
> speaking the real Meshtastic protocol over MQTT. It shipped and ran live at DEF CON,
> in support of defcon.run's own Meshtastic mesh.

## What it is

**Elevator version:** Meshtastic is a real off-grid mesh-radio protocol — people carry
small radios that relay messages to each other without any cell network. meshtk lets you
put a node on that mesh entirely in software: no radio, no serial hardware, just a Go
binary that speaks the same protocol over MQTT. Kurt calls it "a toolkit for working
with virtual Meshtastic nodes."

**The honest version:** it does three things well. First, it can announce a virtual node
onto the mesh — broadcasting the same info packets a real radio would, so it shows up on
public mesh maps and can hold encrypted conversations. Second, it can simulate whole
fleets of these virtual nodes at once — wandering around, chatting, emitting telemetry.
Third, it runs as a security proxy in front of a real MQTT broker, inspecting every
packet and applying rules before anything reaches the broker. It's a single static Go
binary with a Cobra command-line interface — the same tooling style as `km`.

## How it works

**The protocol layer:** Meshtastic radios exchange encrypted packets over MQTT. To make
sense of any of it, meshtk has to unwrap the MQTT transport, then decode the Meshtastic
"envelope" inside it, then decrypt the actual payload — because everything on the wire is
encrypted. There are two layers of Meshtastic encryption: a pre-shared channel key that
any two radios can agree on ahead of time (like a walkie-talkie frequency with a lock),
and a public/private-key layer on top for genuinely private one-to-one messages. Every
radio generates its own key pair the first time it's set up, and happily shares its
public half with anyone who asks.

**Virtual node announcement:** meshtk can connect to an MQTT broker and broadcast the
same info/position packets a real radio would, at a regular interval — that's how a
software-only node ends up visible on a public mesh map alongside real hardware.

**Fleet simulation ("ghosts"):** meshtk can spin up whole fleets of simulated nodes that
follow a route, chat, and emit telemetry with realistic jitter, ramping up and down
gracefully. The simulated nodes are named after hacker-history legends as a nice tribute
— a fun detail to mention, without reciting exact node data.

**The security proxy:** Meshtastic's own broker software (Mosquitto) has two real gaps
behind a load balancer: it can't see a client's real IP address, and it has no idea
what's inside an encrypted Meshtastic payload. meshtk sits in front and solves both — it
decrypts and inspects every packet, then applies a small rule set (allow, block, kill the
connection, slow it down, or rewrite it) before the packet ever reaches the broker. A fun
example rule: profanity gets swapped for emoji on the public channel.

**Per-visitor credentials:** the newest version added a proper credential cache, so every
connection attempt gets checked against real defcon.run registration data with a fast
in-memory lookup, rather than everyone sharing one generic set of credentials the way a
lot of ad-hoc mesh deployments historically have. Bad connections get rejected before the
underlying broker ever sees them; the cache is resilient to a backing database hiccup —
it keeps serving from cache and reports itself as "degraded" rather than falling over.

**The origin story:** meshtk exists because Kurt wanted interactive bots reachable over
the mesh — post a message to a public channel, and a bot listening on the other end takes
action, protected by a one-time-password unlock so only someone holding the secret could
trigger it. Getting the private-message cryptography working correctly (the elliptic-curve
key exchange piece) was a genuinely hard bug — Kurt describes pointing Claude Code at the
real Meshtastic firmware repo, the official mobile app source, and his own meshtk code
side by side, and Claude spotted a small implementation detail meshtk's code was missing
that the reference implementations had right. That fix is what got private messaging
working end to end.

## In Kurt's own words (verbatim, from the recorded transcripts)

- On what meshtk actually is: *"It's a toolkit for working with virtual Meshtastic
  nodes... it's a proxy that is able to accept MQTT traffic which is what Meshtastic
  uses over the wire... and inside of that MQTT packet is the Meshtastic envelope... and
  inside of that envelope is the Meshtastic payload."*
- On why you have to decrypt to understand anything: *"It's really only once you start
  to look at the Meshtastic payload do you start to understand what's happening on the
  network... because over the wire it's using encryption... two types of encryption. One
  is their channel encryption... even over that pre-shared key network, maybe you wanna
  have a private conversation with somebody — that's where the PKI level comes in."*
- On the moment Claude cracked the crypto bug: *"I got the firmware repo... I had the
  Android app and the iOS app — those are public projects... and then I had my meshtk,
  and I said, why is it basically that my meshtk cannot send messages or cannot
  communicate? And it figured out, looking at those three code bases against my code
  base, that I was missing an implementation... a small detail in the way the
  cryptography worked... Claude is ultimately what helped me implement the final PKI...
  that was like a super moment for me."*
- On the one-time-password bot game he built on top: *"We had a cool feature — one-time
  passwords for these bots... the first message you have to send to these other nodes is
  the one-time password. If you don't send it, this thing is not replying... you had to
  run around, find the one-time password, send it to the bot, and then the bot was yours
  to chat with."*

## Topic map

### Virtual nodes and mesh maps
- meshtk creates Meshtastic nodes that exist only in software — no radio needed — by
  publishing the same info and position broadcasts a real radio would send, so they
  appear on public mesh maps alongside real hardware.

### The ghost fleet
- Simulated nodes named after hacker-history legends wander a set of embedded routes as
  part of defcon.run, each roaming, chatting, and emitting telemetry with realistic
  jitter, ramping up and down gracefully — a fun tribute detail, not something to recite
  exact data for.

### The security proxy
- meshtk fronts the real MQTT broker, recovers real client IP addresses behind a load
  balancer, decrypts Meshtastic payloads with known channel keys, and applies ordered
  rules: allow, block, kill, slow down, or rewrite.
- Example rules: block anything that fails to decrypt, always allow the basic info
  broadcasts, and swap profanity for emoji on the public channel.

### Per-visitor credentials
- Every connection attempt is checked against real defcon.run registration data, cached
  in memory for fast lookups; valid connections proceed, invalid ones are rejected before
  the broker ever sees them.
- If the backing database has a hiccup mid-event, the cache keeps serving and the health
  check reports "degraded" instead of the service falling over — graceful degradation,
  not a hard failure.

### Encryption and PKI
- meshtk handles both flavors of Meshtastic encryption: a pre-shared channel key for
  general mesh chat, and public/private-key exchange for genuinely private one-to-one
  messages — so a virtual node can hold an encrypted conversation just like real
  hardware.

### Interactive bots and one-time passwords
- The original motivation: bots that listen on the mesh and act on messages, protected
  by a one-time-password unlock so only someone holding the secret can trigger them —
  the basis of a DEF CON mesh scavenger-hunt-style game Kurt built.

### Deployment
- Runs on standard cloud compute behind a load balancer, config is a simple YAML file,
  and it's built with the same Go/Cobra tooling philosophy as `km`.

## Cross-links

- **defcon.run.34:** meshtk is defcon.run's mesh backbone tooling — it's deployed
  directly inside defcon.run.34's mesh service as the packet-inspecting proxy, and its
  credential cache checks against real defcon.run registration data. The ghost fleet and
  route nodes exist specifically for defcon.run events.
- **km / klanker platform:** same tooling DNA — Go plus a Cobra command-line interface,
  single static binary. meshtk predates `kv` but is a stylistic sibling.
- **klanker-voice:** this digest feeds KPH directly; the `kv` CLI planned for
  klanker-voice follows the same Cobra pattern meshtk and `km` both use. No code
  dependency between them.
- **tiogo / kvmlab:** no connection found in this material — if asked, say they're
  separate Kurt projects and hedge rather than invent a link.

## Sample Q→A

1. **Q: What is meshtk?**
   A: It's Kurt's Go toolkit for virtual Meshtastic nodes — software-only mesh radios
   that live on MQTT. It can announce fake nodes onto mesh maps, simulate whole fleets,
   and run a security proxy in front of a real MQTT broker. It ran live at DEF CON
   supporting defcon.run.

2. **Q: What's a virtual Meshtastic node?**
   A: A node with no radio hardware at all — meshtk publishes the same info and position
   packets a real radio would, over MQTT, so it shows up on public mesh maps and can even
   hold encrypted conversations.

3. **Q: What are the ghosts?**
   A: Simulated nodes named as a tribute to hacker-history legends that roam DEF CON
   along preset routes as part of defcon.run — each one wandering, chatting, and emitting
   telemetry on its own.

4. **Q: Why does DEF CON's mesh need a proxy at all?**
   A: Two reasons: the real broker can't see client IPs behind a load balancer, and it
   can't read Meshtastic's encrypted payloads. meshtk sits in front, decrypts every
   packet, and applies allow/block/kill/slow/rewrite rules before anything reaches the
   broker.

5. **Q: How does it keep bad clients out?**
   A: Every connection attempt is checked against real defcon.run registration data,
   cached in memory for fast lookups. Valid clients are let through; invalid ones are
   rejected before the broker ever sees them.

6. **Q: Can it rewrite messages?**
   A: Yes — rules can modify packets in flight. A fun example: on the public channel it
   swaps profanity for emoji. Family-friendly mesh, enforced at the proxy.

7. **Q: What language is it written in?**
   A: Go, compiled to a single static binary with a Cobra command-line interface — the
   same tooling style Kurt uses for `km`.

8. **Q: What happens if the credential database goes down mid-conference?**
   A: A resilience layer kicks in — cached credentials keep working, and the health
   check reports "degraded" instead of the service dying outright. Graceful degradation,
   not a hard failure.

9. **Q: What's the one-time-password thing about?**
   A: The original idea was internet-reachable bots on the mesh — post to a public
   channel with the right one-time password and a bot listening on MQTT takes action.
   Kurt built a DEF CON scavenger-hunt game on top of exactly that mechanic.

10. **Q: Does it work with encryption?**
    A: Fully — it handles both pre-shared channel encryption and public/private-key
    exchange for private one-to-one messages, so a virtual node can send and receive
    encrypted direct messages just like real hardware.

11. **Q: How did Claude help build it?**
    A: The hardest bug was getting the private-message cryptography exactly right. Kurt
    pointed Claude Code at the real Meshtastic firmware and mobile-app source alongside
    his own code, and Claude spotted the small implementation detail meshtk's code was
    missing — that's what got private messaging working end to end.

12. **Q: Is meshtk open source?**
    A: Yes, on GitHub under Kurt's account — a genuine work in progress that shipped for
    real at DEF CON.

## PACK COMPLETE — meshtk
