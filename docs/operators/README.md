# Operator manual

Everything needed to run klanker-voice: the `kv` CLI, the phone numbers behind
it, the access it grants, and the infrastructure underneath.

Written for the operator who built this, walked away for a few weeks, and needs
an answer in under a minute — not a tour.

---

## Start here

**"What phone numbers do I have?"**

```bash
kv telephony list
```

**"What passcodes exist, and what do they grant?"**

```bash
kv code list && kv tier list
```

**"Something is broken and people are watching."** →
[Incident runbook](incident-runbook.md)

---

## The pages

| Page | Answers |
|---|---|
| [`kv` CLI reference](kv-cli-reference.md) | every command, every flag, what it touches, what it needs |
| [Phone number inventory](phone-number-inventory.md) | what numbers exist, where each one hides, which source is authoritative |
| [Access codes & tiers](access-codes-and-tiers.md) | who can get in, the four ways in, and what they get |
| [Phone games](phone-games-runbook.md) | how the CTF lines work; adding, changing and retiring one |
| [PBX lifecycle](pbx-lifecycle.md) | standing the phone side up from nothing, and taking it down |
| [Infrastructure](infrastructure.md) | what runs in AWS right now, and how to check it's healthy |
| [Incident runbook](incident-runbook.md) | the brake, the ceilings, and a diagnostic tree per symptom |

### Also in this directory

| Page | Covers |
|---|---|
| [kv studio operator guide](kv-studio-operator-guide.md) | the local web console, its SOP snapshots and deploy flow |
| [VoIP.ms provisioning runbook](voipms-provisioning-runbook.md) | the portal-only carrier security steps, in §25.F order |
| [Phase 12 seed data](phase12-seed-data.md) | the original telephony seed values |

---

## By task

**Running an event**

1. [Pre-event checklist](incident-runbook.md#before-an-event) — six commands and a phone call
2. [Issue an event code](access-codes-and-tiers.md#issue-a-code-for-an-event) — with an expiry, always
3. [Watch it live](phone-games-runbook.md#watching-a-game-live) — outcomes, callers, new arrivals
4. [Clean up afterwards](incident-runbook.md#after-an-event) — expire what you handed out

**Adding a phone line**

1. [Order and route it](pbx-lifecycle.md#2-order-and-route-a-number)
2. [Tag it](phone-number-inventory.md#adding-a-number) so the edge can tell which number was dialled
3. [Wire a game onto it](phone-games-runbook.md#adding-a-game) — seed SSM *before* touching `service.hcl`
4. [Verify by calling it](phone-games-runbook.md#6-deploy-and-verify)

**Controlling cost**

1. [Check today's spend](kv-cli-reference.md#kv-usage-today)
2. [Tighten a tier](access-codes-and-tiers.md#change-what-everyone-gets-right-now) — no deploy, next session
3. [Find the source](incident-runbook.md#the-bill-is-spiking)
4. [Pull the brake](incident-runbook.md#the-brake)

**Turning it off**

1. [Kill-switch](incident-runbook.md#the-brake) — refuses new sessions, changes nothing else
2. [Scale the phone side to zero](pbx-lifecycle.md#taking-the-whole-phone-side-down) — stops calls arriving
3. [Full decommission](pbx-lifecycle.md#deprovisioning) — including what *not* to delete

---

## Conventions

**Live captures.** Every terminal image was captured against the real
`klanker-application` account and rendered from a checked-in transcript. To
re-capture after a CLI change, edit the `.session` file and run:

```bash
python3 scripts/render-terminal-svg.py            # all
python3 scripts/render-terminal-svg.py telephony-list   # one
```

**Redaction.** These pages are mirrored to a public wiki. Access-code values,
DTMF game codes, spoken passphrases, the gate PIN, bypass tokens, VoIP.ms
credentials and raw caller numbers are redacted in every capture — in the
transcripts, so the redaction is reviewable in `git diff`, and in the studio
screenshots. DIDs, SSM parameter *names*, tier quotas and call statistics are
public and appear verbatim.

**Destructive commands** are called out where they appear. Three are worth
memorising as irreversible or outage-causing:

| Command | Why |
|---|---|
| `kv voipms cancel-did <did> --yes` | the number is released permanently; anyone can take it |
| `kv tier define <existing-tier>` | full replace — omitted flags revert to defaults |
| removing an SSM parameter still referenced by `service.hcl` | ECS refuses to launch the task; the service goes down |

---

## Related

- [Architecture overview](../architecture/overview.md) — how the pieces fit
- [Deployment guide](../guides/deployment.md) — how it gets built and shipped
- [Telephony data flow](../dataflows/telephony-voipms.md) — the call path in detail
- [Auth & quotas data flow](../dataflows/auth-quota.md) — token to metered session
- [Configuration guide](../guides/configuration.md) — the pipeline TOML surface
