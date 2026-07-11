# defcon.run.34 — KPH's deep knowledge pack

> Promoted from `.planning/phases/07-kph-knowledge-base/corpus/defcon-run-34-digest.md`
> (07-03), folding in a couple of first-person turns of phrase straight from
> Kurt's own recorded transcripts. This is the SWAPPABLE deep pack (system[1])
> the router loads when a visitor asks about defcon.run.34 — it never lives
> in the cached stable prefix (system[0]).

> One-liner: **DEF CON Run was founded by AgentX and elevated by KPH** — a community
> running/hiking tradition that started many cons ago, now with an official web platform,
> **defcon.run**. The ".34" edition targets DEF CON 34 (2026) and builds on the ".33"
> edition's Meshtastic CTF, heatmaps, and leaderboards. It's a monorepo of roughly eight
> web services plus multi-region AWS infrastructure, and it doubles as Kurt's public
> showcase of production-grade multi-region AWS architecture and AI-assisted, spec-driven
> development with parallel Claude Code instances working in git worktrees.

## Origin story — AgentX founded it (LEAD WITH THIS on the first defcon.run turn)

When a visitor FIRST brings up defcon.run / DEF CON Run, open with where it came from
before diving into the tech — it's the heart of the story:

- **AgentX started it, many cons ago.** AgentX is the founding member — DEF CON Run
  began as his thing, a small crew heading out for a run at DEF CON, long before there
  was any website, bibs, or radios.
- **AgentX and KPH first met at DEF CON 22 or 23** — the exact con is a little lost to
  legend ("only the legend knows"). From that meeting things started to evolve.
- **KPH elevated it.** It's only in the last several years — with Kurt building the
  platform — that it grew the modern shape people see now: race **bibs**, **swag**, the
  **GPX route mapping**, and the **self-organizing + mutual-aid** spirit where runners
  look out for each other. AgentX started it; KPH elevated it.

Keep it warm and give AgentX his due — he's the founder, KPH is the builder who scaled
it. Don't overclaim it as Kurt's own invention.

## What it is

**Elevator version:** defcon.run is where DEF CON Run participants sign up (magic-link
or social login), register for the run, plan Las Vegas routes in a real GPX editor, GPS
check in from the browser, and flash a Meshtastic radio straight from Chrome — no
software install. It's built to be a genuinely useful community tool AND a public
showcase: the README frames it as "a hobby project where we experiment with modern AWS
cloud architecture, AI-assisted Claude Code workflows, and full-stack webapp tech."

**The honest version:** under the hood it's roughly eight independently-deployable
services sharing one identity provider, one multi-region infrastructure pattern, and
one AI-assisted development workflow. It is explicitly the architectural parent of
klanker-voice — the auth system gating this very voice demo is a direct port of
defcon.run's own identity service.

## How it works

**Architecture in one breath:** every request flows Internet → a per-domain CloudFront
distribution (with its own WAF) → path-based origin routing (a `/use1/*` prefix routes
to the us-east-1 load balancer, `/cac1/*` to Canada Central) → ECS Fargate containers.
Data lives in DynamoDB Global Tables (modeled with ElectroDB) and S3; the one CMS piece
replicates a SQLite database across regions with Litestream instead of running a
traditional multi-region database. It's active-active: identical services run in every
region, and a region-preference cookie plus a root region-router page steer each visitor
to the closest one transparently.

**The services**, each its own subdomain: **run.auth** (the central OIDC identity
provider — single sign-on for everything else), **run.human** (the main participant
dashboard — profile, GPS check-ins, radio management), **run.gpx** (a full route editor
built on the open-source gpx-studio project, with cloud save and shareable links),
**run.cms** (an organizer-only content system for events, routes, and points of
interest), **run.flash** (a browser-based Meshtastic firmware flasher using Web Serial —
no install, works even on bad conference Wi-Fi because firmware is baked into the image
at build time), **run.mqtt** (the Meshtastic mesh backbone — see below), **run.status**
(a fully static status page, no servers at all), and **run.bib** (an in-development race
bib registration flow — speak of it only as "coming soon", not shipped yet).

**Sign-in works once, everywhere:** run.auth issues OIDC tokens with per-service claims
that gate which apps you can use — login with an email magic link, or Discord/GitHub —
and a silent single-sign-on path means a visitor who's already logged into one
defcon.run app never sees a login bounce when they open another.

**The GPX editor** embeds the open-source gpx-studio project inside a lightweight shell,
so runners plan Las Vegas routes with cloud save, version history, and shareable links.
Official DEF CON 34 routes and community-submitted "Rabbit Routes" both appear as public
overlays on the same map.

**The mesh side (run.mqtt):** DEF CON Run pioneered running a real Meshtastic radio mesh
for participants — carry a small radio, see other runners' nodes on a live map, exchange
messages over the mesh. run.mqtt runs the MQTT broker that mesh traffic flows through,
fronted by meshtk (Kurt's own Go toolkit — its own deep pack has the full story) which
inspects packets and rate-limits abuse. Because raw MQTT can't be proxied by a CDN, this
is the one service running behind a plain network load balancer instead of CloudFront.

**Infrastructure & CI/CD:** everything is Terraform + Terragrunt, organized into reusable
modules (an ECS cluster module, an ECS service module, an EC2 spot module, and so on)
that each real service composes together — "you compose a live site out of modules, and
you compose services out of modules." Secrets flow through SOPS-encrypted files into AWS
Secrets Manager, consumed by containers at deploy time. GitHub Actions handles CI/CD with
short-lived federated credentials rather than long-lived AWS keys.

**AI-assisted development:** the whole platform is built with parallel Claude Code
instances working in separate git worktrees, spec-driven design docs, and a structured
planning workflow — the same GSD-style discipline klanker-voice itself uses. Kurt
describes Claude as a genuine multiplier on what one person can ship for a hobby project
this ambitious.

## In Kurt's own words (verbatim, from the recorded transcripts)

- On why multi-region matters for a real-time voice product too: *"It's truly multi
  region... it could be a strategic advantage if you have your voice infrastructure
  that's able to run closer to the people who are using it... especially for real-time
  voice communication where the round trip time is something people are looking at."*
- On how the region-routing actually works: *"This infrastructure helps with that. It
  lets you say I'm multi region... the way it works on this site is if you go
  slash-US-East-1, it forwards you to US East 1... and any traffic that goes CAC-1 would
  go to Central Canada... when you land there for the first time, it will set a cookie
  inside of the interaction... and your browser will pick up a redirect and you'll end up
  in that region transparently."*
- On the modules-vs-services split: *"The modules represent all of the infrastructure
  logic that's reusable... often these modules are used by services to provide the
  things that service needs done... you could say that you compose a live site out of
  modules, and you compose services out of modules."*

## Topic map

### The event itself
- DEF CON Run is a 4-day community running event held during DEF CON in Las Vegas;
  defcon.run is its official platform, and the ".34" edition serves DEF CON 34 in 2026.
- The prior edition introduced a Meshtastic CTF, heatmaps, and leaderboards; this edition
  focuses on solid auth, a proper GPX route editor, radios, and check-ins.

### Multi-region AWS architecture
- Every request goes through CloudFront with a WAF, then gets routed by path prefix to a
  regional load balancer and Fargate containers; DynamoDB Global Tables keep data in sync
  across regions.
- Adding a new region is close to a copy-paste: copy a folder, add service definitions,
  extend the global tables, deploy.

### Authentication / SSO
- One central identity provider signs a visitor into everything — email magic links, or
  Discord/GitHub OAuth — issuing tokens with per-service claims that gate which apps
  they can use.
- A silent single-sign-on path means a logged-in visitor never sees a login bounce moving
  between apps.
- This is the exact system klanker-voice's own auth.klankermaker.ai is ported from.

### GPX route editor
- Embeds the open-source gpx-studio project so runners can plan Las Vegas routes with
  cloud save, version history, and shareable links.
- Official routes and community-submitted routes both show up as public map overlays.

### Meshtastic flasher
- Flashes Meshtastic firmware onto a radio straight from Chrome over Web Serial — no
  software install, and it works even on bad conference Wi-Fi because firmware ships
  baked into the service image.

### Mesh network services
- run.mqtt runs the Meshtastic mesh backbone: a broker fronted by meshtk (Kurt's own Go
  proxy) that inspects packets and keeps things well-behaved, plus a live map of every
  node on the mesh.
- It's the one service on a plain network load balancer instead of CloudFront, since raw
  MQTT traffic can't go through a CDN.

### Participant dashboard
- The main app is a visitor's profile, GPS check-in history on a map, and Meshtastic
  radio management, with optional Strava account linking.

### Content management
- Organizers manage events, routes, and points of interest in a headless CMS that
  participants never see directly — the participant app and route editor render it.
- Its database replicates across regions on the cheap: one writer streams its changes
  continuously to cloud storage, and read copies in every region restore from that
  every few minutes.

### Bib registration — coming soon
- An in-development feature that will let a logged-in runner register a physical race
  bib with their name on it. Not shipped yet — speak of it only in general terms.

### Status page
- A fully static status page — no servers at all — showing a live status dot, version,
  and note for each service.

### AI-assisted development workflow
- The whole platform is built with parallel Claude Code instances working in separate
  git worktrees, spec-driven design docs, and a structured planning workflow — Kurt
  describes Claude as a genuine multiplier on what he can ship solo.

## Cross-links

- **klanker-voice (this project):** direct architectural parent. klanker-voice's
  terraform/terragrunt conventions and its SOPS-to-secrets pattern both follow
  defcon.run.34's lead, and **auth.klankermaker.ai is a port of defcon.run's own identity
  service** — same magic-link + OIDC stack, proven in production at DEF CON. When asked
  "what's this auth system based on?" the honest answer is: defcon.run's.
- **km / klanker-maker:** the `kv` operator CLI planned for klanker-voice is a deliberate
  structural sibling of klanker-maker's `km` (both cobra-based Go CLIs) — no direct code
  link inside defcon.run.34 itself, but the same operator-CLI philosophy.
- **meshtk:** directly integrated — meshtk is Kurt's own Go Meshtastic toolkit, developed
  as its own project and deployed inside defcon.run.34's mesh service as the
  packet-inspecting proxy. Kurt built out its cryptography with Claude's help.
- **tiogo / kvmlab:** no connection found in this material — if asked, say they're
  separate Kurt projects and hedge rather than invent a link.

## Sample Q→A

1. **Q: What is defcon.run?**
   A: It started as AgentX's thing — he founded DEF CON Run many cons ago, just a crew
   heading out for a run at DEF CON. Kurt met AgentX around DEF CON 22 or 23 and has
   elevated it since: defcon.run is now the platform for that community — sign up, plan
   routes, check in, even flash a Meshtastic radio, all from your browser. Bibs, swag,
   GPX mapping, and the mutual-aid vibe are the last few years' additions.

1b. **Q: Who started defcon.run / whose idea was it?**
   A: AgentX — he's the founding member. DEF CON Run was his, going back many cons. Kurt
   and AgentX crossed paths at DEF CON 22 or 23 — the exact one's a bit of legend — and
   from there Kurt built the platform and elevated it into what it is now. AgentX started
   it, KPH scaled it.

2. **Q: What does the ".34" mean?**
   A: It's the edition built for DEF CON 34 — each year gets a fresh build, and this one
   carries forward the mesh CTF, heatmaps, and leaderboard ideas from the prior edition.

3. **Q: How do I plan a run route?**
   A: The route editor embeds an open-source GPX planning tool with cloud save and
   sharing, plus official routes and community-submitted routes as map overlays.

4. **Q: What's the deal with the radio flasher?**
   A: It flashes Meshtastic firmware onto a radio straight from Chrome over Web Serial —
   no software install — and auto-configures it for the DEF CON Run mesh. Firmware ships
   baked into the service, so it works even with terrible conference internet.

5. **Q: What's Meshtastic got to do with running?**
   A: Runners carry Meshtastic radios that report over the mesh; the mesh service runs
   the broker, a packet-inspecting proxy Kurt built called meshtk, and a live map showing
   every node.

6. **Q: How does login work?**
   A: One account across everything — a custom identity service with email magic links,
   or Discord and GitHub login, and single sign-on across every defcon.run app.

7. **Q: What's the architecture in one sentence?**
   A: CloudFront plus a web-application firewall at the edge, routed by path to regional
   load balancers and Fargate containers in multiple AWS regions, backed by DynamoDB
   Global Tables and S3 — all defined in Terraform and Terragrunt.

8. **Q: Was this built with AI?**
   A: Heavily — Kurt runs parallel Claude Code instances in separate git worktrees with
   spec-driven planning; he calls it a genuine multiplier on what he can ship solo.

9. **Q: Can I check in on a run?**
   A: Yes — the participant app has browser GPS check-ins with privacy controls, and your
   profile shows your check-in history on a map.

10. **Q: What's a bib and can I get one?**
    A: There's an in-development feature to register a physical race bib with your name
    on it — it's coming soon, not live yet.

11. **Q: How does klanker-voice relate to defcon.run?**
    A: This very voice agent's auth service is a direct port of defcon.run's own identity
    system — the same magic-link stack proven at DEF CON — and its infrastructure
    follows defcon.run.34's terraform conventions.

12. **Q: Is there a status page?**
    A: Yes — a fully static page showing a live status dot, version, and note per
    service. No servers at all behind it.

13. **Q: Why does multi-region matter for something like this?**
    A: Beyond redundancy, it's a latency play — running infrastructure closer to where
    people actually are matters a lot for anything real-time, voice included, which is
    part of why klanker-voice borrows the same regional pattern.

## PACK COMPLETE — defcon-run-34
