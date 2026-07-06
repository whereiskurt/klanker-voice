# defcon.run.34 — Knowledge Digest for KPH

## What it is

**defcon.run 34** is the official web platform for DEF CON Run — Kurt's annual 4-day community running/hiking event held at DEF CON in Las Vegas. The ".34" edition targets DEF CON 34 (2026) and is the successor to defcon.run.33 (which pioneered the Meshtastic CTF, heatmaps, and leaderboards). It is a monorepo of roughly eight web services plus multi-region AWS infrastructure, and it doubles as Kurt's public showcase of two things: **multi-region AWS webscale architecture** (CloudFront + WAF → regional ALBs → ECS Fargate, DynamoDB Global Tables, Litestream SQLite replication) and **AI-assisted spec-driven development** with parallel Claude Code instances working in git worktrees. The repo README frames it as "a hobby project where we experiment with modern AWS cloud architecture, AI-assisted Claude Code workflows, and full-stack webapp tech."

What participants actually get: sign up with a magic-link/social login, register for the run, plan Las Vegas routes in a real GPX editor, GPS check-in from the browser, flash a Meshtastic radio from Chrome via Web Serial, see live mesh nodes on a map, and (planned v1.5) register a physical race bib with their name on it.

Source of truth: `/Users/khundeck/working/defcon.run.34/README.md`, `.planning/PROJECT.md`, `.claude/architecture.md`.

## How it works

**Architecture (one breath):** All web traffic flows Internet → CloudFront distribution (per domain, each with a WAF WebACL) → path-based origin routing (`/use1/*` → us-east-1 ALB, `/cac1/*` → ca-central-1 ALB) → ECS Fargate tasks. Next.js apps set `basePath` to the region prefix at Docker build time. Data lives in DynamoDB Global Tables (modeled with ElectroDB), S3 (uploads, GPX files, media), and — for the CMS — SQLite replicated via Litestream. Active-active multi-region: identical services in each region; a `preferred-region` cookie plus a root region-router page steer users.

**The services** (each a subdomain of defcon.run):

| Service | Domain | Stack | Role |
|---|---|---|---|
| run.auth | auth.defcon.run | Next.js 16, Auth.js v5, oidc-provider v9 | Central OIDC identity provider — SSO for everything |
| run.human | run.defcon.run | Next.js 16, React 19, HeroUI, Tailwind 4 | Main participant app — profile, check-ins, radio management |
| run.gpx | gpx.defcon.run | Next.js wrapping vendored gpx-studio (SvelteKit) | Full GPX route editor + public route map |
| run.cms | cms.defcon.run | Strapi 5.6 + SQLite + Litestream | Organizer-only headless CMS: Events, Routes, POIs |
| run.flash | flash.defcon.run | Next.js + esptool-js + @meshtastic/core | Browser-based ESP32 Meshtastic firmware flasher (Web Serial) |
| run.mqtt | mqtt.defcon.run | Mosquitto + meshtk (Go) + meshmap, behind an NLB | Meshtastic MQTT broker, packet-inspecting proxy, live node map |
| run.status | status.defcon.run | Pure static S3 + CloudFront | Status page — service dots, versions, marquee ticker |
| run.bib | bib.defcon.run | Next.js (mirrors run.flash layout) | Race-bib registration (v1.5, in development) |

Plus local/ops tooling: **configui** (a localhost-only Go binary that generates HCL, runs terragrunt with live SSE streaming, and edits SOPS secrets) and **waffaw** (a WAF-testing platform using Artillery + Playwright headless Chromium for real browser TLS fingerprints, with S3 as its sole control plane — ~70% built).

**Auth model:** run.auth runs a dual stack — Auth.js v5 for its own sessions plus `oidc-provider` v9 as an embedded OIDC server. Login methods: email OTP via SES (with ALTCHA proof-of-work captcha), Discord OAuth, GitHub OAuth; Strava OAuth is account-linking only. OIDC clients (run.human, run.gpx, run.cms, run.flash, run.bib) use PKCE, authorization_code + refresh_token grants, and a custom `services` scope whose claims gate per-service access (e.g. you need the `gpx` claim to use the GPX editor, `cms` for Strapi admin). A "silent SSO" design (2026-07-03) makes cross-service login invisible via `prompt=none` once you're logged in anywhere on `.defcon.run`.

**CMS replication:** Strapi writes go to a single master in us-east-1; Litestream continuously streams the SQLite WAL to S3; read-only workers in every region restore from S3 every 5 minutes and atomically swap databases. "Simple, cheap, resilient."

**GPX editor:** run.gpx embeds the open-source gpx-studio SvelteKit app, built from source at deploy time and served as static files under `/studio/*`, with a Next.js shell providing Auth.js sessions and `/api/gpx/*` cloud-save routes (S3 files + DynamoDB metadata, 50-version history, public/private share links with nanoid tokens). Public routes on the map come from DynamoDB `GLOBAL` folders — "DEF CON 34 Maps" (admin-published) and "Rabbit Routes" (user-submitted, admin-approved) — with Strapi enriching them (colors, write-ups, cover photos, POIs) and, per the newest approved design, also able to author routes outright.

**mqtt.defcon.run (v1.3):** a 4-container ECS task (mosquitto broker → meshtk proxy → nginx/meshmap, dependency-ordered) exposed via a Network Load Balancer with 4 listeners (1883 TCP, 8883 TLS, 443 TLS, 8443 WSS) and Route53 latency-based routing — no CloudFront, since raw MQTT can't be proxied by it. The meshtk proxy does packet inspection, rate limiting, and S3 logging.

**Infra & CI/CD:** Terraform 1.14 + Terragrunt 0.97, ~20 reusable modules (`infra/terraform/modules/`), live config under `infra/terraform/live/site/` split into global/, region/, and services/. Secrets: SOPS-encrypted JSON → SSM Parameter Store SecureStrings (KMS), consumed by tasks via `valueFrom`. GitHub Actions with OIDC federation (7 IAM roles, zero long-lived AWS credentials); security scanning via gitleaks, checkov, and prowler. `./apps/release-all.sh --pr --parallel` bumps VERSION files, builds/pushes images to every regional ECR, rewrites taskdefs, and triggers deployment.

**AI-assisted development:** The repo is run with the GSD planning workflow (`.planning/` — PROJECT, ROADMAP, milestone archives), git-worktree-based parallel Claude instances (`.claude/worktrees/{admin,release,flash,cms,gpx,bib}`), and superpowers-style design specs in `docs/superpowers/specs/`. Milestones shipped: v1.0 Meshtastic Flasher MVP, v1.1 CMS Content Types, v1.2 User Check-ins, v1.3 Meshtk Integration; active/planned: v1.4 Flash refresh, v1.5 Bib Registration, v1.6 UX refresh, v1.7 GPX routes + Strava sync.

## Topic map

### The event itself
- DEF CON Run is a 4-day community running event held during DEF CON in Las Vegas; defcon.run is its official platform, and the ".34" edition serves DEF CON 34 in 2026.
- The previous edition (DC33) introduced a Meshtastic CTF, heatmaps, and leaderboards; DC34 focuses on solid auth, a proper GPX route editor, radios, and check-ins.
- Source pointers: `README.md` (Motivations section), `.planning/PROJECT.md`

### Multi-region AWS architecture
- Every request goes through CloudFront with a WAF, then gets routed by path prefix — slash-use1 for Virginia, slash-cac1 for Canada — to a regional load balancer and Fargate containers; DynamoDB Global Tables keep data in sync across regions.
- One release script deploys every app to every region; adding a region is copy a folder, add service definitions, extend the global tables, and deploy.
- Source pointers: `README.md`, `infra/README.md`, `.claude/architecture.md`

### Authentication / SSO (run.auth)
- One central identity provider at auth.defcon.run signs you into everything — email magic links via SES, or Discord and GitHub OAuth — and issues OIDC tokens with per-service claims that gate which apps you can use.
- It runs Auth.js for sessions and an embedded oidc-provider OIDC server side by side, with PKCE required and a silent-SSO fast path so a logged-in user never sees a login bounce.
- Source pointers: `apps/run.auth/`, `.claude/architecture.md`, `docs/superpowers/specs/2026-07-03-oidc-silent-sso-design.md`

### GPX route editor (run.gpx)
- gpx.defcon.run embeds the open-source gpx-studio editor inside a Next.js shell, so runners can plan Las Vegas routes in the browser with cloud save, version history, and shareable links.
- Official "DEF CON 34 Maps" and community "Rabbit Routes" appear as public overlays on the map, enriched from the CMS with colors, photos, write-ups, and points of interest.
- Source pointers: `apps/run.gpx/`, `apps/README.md`, `docs/superpowers/specs/2026-07-05-strapi-authored-routes-and-pois-design.md`

### Meshtastic flasher (run.flash)
- flash.defcon.run flashes Meshtastic firmware onto ESP32 radios straight from Chrome using Web Serial — pick your device, connect, flash, and it auto-configures MQTT, channels, and radio settings.
- Firmware binaries are vendored into the Docker image at build time, so the flasher has zero runtime dependency on the internet — it works even if conference Wi-Fi is a disaster.
- Source pointers: `apps/run.flash/README.md`, `.planning/PROJECT.md` (v1.0, v1.4 sections)

### Mesh network services (run.mqtt / meshtk)
- mqtt.defcon.run runs a Mosquitto MQTT broker fronted by meshtk, a Go proxy that inspects Meshtastic packets and rate-limits abuse, plus a live "meshmap" showing every node on the mesh.
- Because MQTT is raw TCP, this is the one service on a Network Load Balancer with latency-based DNS instead of CloudFront — four ports covering plain MQTT, TLS MQTT, HTTPS, and secure WebSockets.
- Source pointers: `apps/run.mqtt/`, `.planning/MILESTONES.md` (v1.3), `.planning/PROJECT.md`

### Participant features (run.human)
- run.defcon.run is the participant dashboard: your profile with a personal QR code, GPS check-ins from the browser with privacy controls and a Leaflet map of your history, Meshtastic radio management, and Strava account linking.
- Check-ins are quota-enforced and privacy-toggleable; the profile map colors markers by how recent each check-in is.
- Source pointers: `apps/run.human/webapp/src/app/`, `.planning/PROJECT.md` (v1.2 requirements)

### Content management (run.cms)
- Organizers manage Events, Routes, and Points of Interest in a Strapi CMS at cms.defcon.run; participants never see Strapi — run.human and run.gpx render the content.
- Its clever trick is database replication on the cheap: one SQLite writer streams its WAL to S3 with Litestream, and read replicas in each region restore and atomically swap every five minutes.
- Source pointers: `apps/run.cms/`, `apps/README.md` (Master-Worker CMS Replication), `.claude/architecture.md`

### Bib registration (run.bib — in development)
- bib.defcon.run (v1.5, being built now) lets a logged-in runner register a physical race bib — their name auto-shrinks to fit the bib, rendered as a live preview styled like a real race bib — with support tiers payable on-site or online.
- One bib per account, written to the runner's identity so it shows up on every login.
- Source pointers: `apps/run.bib/`, `.planning/REQUIREMENTS-v1.5-bib.md`, `docs/superpowers/specs/2026-07-04-bib-admin-and-orderform-design.md`

### Status page (run.status)
- status.defcon.run is a fully static status page — no servers at all, just S3 behind CloudFront — showing a colored dot, version, and note for each service, updated by editing two JSON files and running one script.
- Source pointers: `apps/run.status/README.md`, `infra/terraform/modules/status-site/`

### AI-assisted development workflow
- The whole platform is built with parallel Claude Code instances working in git worktrees, spec-driven design docs, and a milestone/phase planning system — Kurt describes Claude as a "massive multiplier" that let him ship far more than expected.
- Notable: Claude wrote the first heatmap and leaderboard implementations for DC33 and helped finish meshtk's crypto.
- Source pointers: `README.md` (AI-Assisted Development), `.claude/`, `docs/superpowers/specs/`, `.planning/ROADMAP.md`

### Ops tooling (configui, waffaw)
- configui is a local-only Go web UI that generates Terragrunt config from forms, streams terraform runs live, and manages secrets — infrastructure administration without memorizing commands.
- waffaw ("sounds like waffle") is a WAF-testing platform that drives fleets of cloud nodes running real headless Chromium so test traffic has genuine browser TLS fingerprints; S3 is its entire control plane — no Lambda, no API Gateway.
- Source pointers: `apps/local/configui/` (referenced as `apps/configui`), `apps/run.waffaw/DESIGN.md`, `.claude/architecture.md`

## Cross-links

- **klanker-voice** (this project): defcon.run.34 is its direct architectural parent. The voice project's terraform/terragrunt "matches defcon.run.34 conventions" per its own design constraints, its SOPS→SSM secrets pattern is copied from here, and **auth.klankermaker.ai is a port of run.auth** (same Auth.js v5 beta + oidc-provider + ElectroDB + SES magic-link + ALTCHA stack, proven in production at DEF CON). When KPH is asked "what's this auth system based on?" the answer is run.auth from defcon.run.
- **km / klanker-maker**: the `kv` operator CLI planned for klanker-voice is explicitly a structural sibling of klanker-maker's `km` (both cobra-based Go CLIs). No direct code link inside defcon.run.34 itself, but the same operator-CLI philosophy applies.
- **meshtk**: directly integrated. meshtk is Kurt's Go Meshtastic MQTT proxy, maintained in a separate repo (`~/working/meshtk`, cloned from GitHub in CI) and deployed inside defcon.run.34's run.mqtt service as the packet-inspecting, rate-limiting proxy at mqtt.defcon.run (v1.3 milestone). Kurt finished its crypto implementation with Claude's help during the DC33 cycle.
- **tiogo**: no references found in this repo. If KPH is asked, say it's a separate Kurt project (do not claim a connection to defcon.run.34); unsure of details from this corpus.
- **kvmlab**: no references found in this repo; same caveat as tiogo.

## Sample Q→A

1. **Q: What is defcon.run?** A: It's the platform for DEF CON Run, Kurt's annual four-day community running event at DEF CON in Las Vegas — sign up, plan routes, check in on runs, and even flash a Meshtastic radio, all from your browser.
2. **Q: What does the ".34" mean?** A: It's the edition for DEF CON 34 in 2026 — each year gets a fresh repo, and this one builds on lessons from the DEF CON 33 edition, which had the Meshtastic CTF, heatmaps, and leaderboards.
3. **Q: How do I plan a run route?** A: Head to gpx.defcon.run — it embeds the open-source gpx-studio editor with cloud save and sharing, plus official DEF CON 34 routes and community "Rabbit Routes" as map overlays.
4. **Q: What's the deal with the radio flasher?** A: flash.defcon.run flashes Meshtastic firmware onto ESP32 radios straight from Chrome over Web Serial — no software install — and it auto-configures the radio for the DEF CON Run mesh. Firmware is baked into the service, so it works even with terrible conference internet.
5. **Q: What's Meshtastic got to do with running?** A: Runners carry Meshtastic radios that report over the mesh; mqtt.defcon.run runs the MQTT broker, a Go proxy called meshtk that inspects and rate-limits packets, and a live map showing every node.
6. **Q: How does login work?** A: One account across everything — auth.defcon.run is a custom OIDC identity provider with email magic links, or Discord and GitHub login, and single sign-on across all the defcon.run apps.
7. **Q: What's the architecture in one sentence?** A: CloudFront plus WAF at the edge, path-routed to regional load balancers and ECS Fargate containers in multiple AWS regions, with DynamoDB Global Tables and S3 underneath — all defined in Terraform and Terragrunt.
8. **Q: How is the CMS replicated across regions?** A: With Litestream — the Strapi master streams its SQLite write-ahead log to S3 continuously, and read replicas in each region restore and atomically swap the database every five minutes. Cheap and resilient.
9. **Q: Was this built with AI?** A: Heavily — Kurt runs parallel Claude Code instances in git worktrees with spec-driven planning; he calls Claude a massive multiplier and jokes that features get built while he sleeps.
10. **Q: Can I check in on a run?** A: Yes — run.defcon.run has browser GPS check-ins with privacy controls, and your profile shows your check-in history on a map with markers colored by how recent they are.
11. **Q: What's a bib and can I get one?** A: The upcoming bib.defcon.run lets you register a physical race bib with your name on it — the preview auto-shrinks your name to fit, just like a real bib — with optional support tiers.
12. **Q: Is the platform's code public?** A: The DEF CON 33 edition is on GitHub under khundeck, and Kurt shares the multi-region AWS patterns openly — he built it partly so others could learn from "hundreds of hours of AWS magic."
13. **Q: How does klanker-voice relate to defcon.run?** A: This very voice agent's auth service is a port of defcon.run's run.auth — the same magic-link OIDC stack proven at DEF CON — and its infrastructure follows defcon.run.34's terraform conventions.
14. **Q: Is there a status page?** A: Yes — status.defcon.run, a fully static page on S3 showing a live dot, version, and note per service. It's the cheapest possible status page: no servers at all.
15. **Q: What is waffaw?** A: A WAF-testing platform — the name riffs on "WAF," pronounced like waffle — that drives fleets of real headless browsers from many IPs so test traffic has genuine browser fingerprints, used for defensive testing of the platform's own firewall rules.

## Landmines / do-not-say

Content KPH must NOT surface — flagged here by location only:

- **Secrets and secret plumbing**: `.secrets.sops.json`, `env.sops.sh`, `infra/terraform/live/site/SECRETS.md`, SSM parameter path patterns (`/{site}/secrets/{region}/{provider}/{key}`), KMS key details, and any OIDC client secrets, Stripe/PayPal keys, or MQTT service-account credentials. Never recite paths, formats, or values.
- **Internal service-to-service auth**: the `X-Internal-Secret` header mechanism and internal Cloud Map service-discovery URLs (`app-{region}-{site}.local`). Do not describe how services authenticate to each other.
- **Documented security gaps** (in `.claude/architecture.md`): no rate limiting on auth endpoints, `sessionVersion` not yet enforced, `allowDangerousEmailAccountLinking=true`. These are an attacker's shopping list — never mention.
- **Admin/abuse-detection posture**: `docs/superpowers/specs/2026-07-05-admin-activity-reports-design.md` describes the fraud-tripwire strategy ("pre-con, any activity is signal") and what gets monitored. Revealing it tells scanners how to stay quiet.
- **WAF specifics**: deny-by-default WebACL on auth, managed rule sets in use, and everything in `apps/run.waffaw/DESIGN.md` about evading WAF detection (TLS fingerprinting, multi-ENI source IPs). Fine to say waffaw exists; do not explain evasion techniques or rule configurations.
- **Easter eggs — spoilers**: the status page's Konami/5-tap Matrix mode and the `elkentaro` birthday card (`apps/run.status/README.md`), and the backlogged fleet-simulator "ghosts" meshmap easter egg (`.planning/backlog/fleet-simulator-easter-egg.md`). Don't spoil; at most hint that easter eggs exist.
- **Registrant/personal data**: anything in DynamoDB about real users — emails, GPS check-in coordinates, Strava links, bib names, payment status. Never reference individual users or data. Kurt's personal email addresses should not be volunteered.
- **Unreleased/unapproved plans**: v1.5 bib payment details (pricing tiers, provider choices, crypto seam), v1.6/v1.7 roadmaps, and pending design docs are internal until shipped. Speak only about shipped features (v1.0–v1.3 plus flash refresh) as live; describe v1.5 bib in general "coming soon" terms only.
- **AWS account structure**: the management/application/terraform account split, IAM role names, state bucket details, and `aws-nuke` config (`infra/aws-nuke-guelph.yaml.tpl`). Do not describe.

## DIGEST COMPLETE — defcon.run.34

Word count: ~2,450 words.
