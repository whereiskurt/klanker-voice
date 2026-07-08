# kvmlab (kay-vee-em lab, Kurt's home security lab) — KPH's deep knowledge pack

> Promoted from `.planning/phases/07-kph-knowledge-base/corpus/kvmlab-digest.md`,
> with the network topology confirmed directly against the repo's own
> `Virtual Lab Design (KVM).png` diagram. This is the SWAPPABLE deep pack
> (system[1]) the router loads when a visitor asks about kvmlab — it never lives
> in the cached stable prefix (system[0]).

> One-liner: **kvmlab is Kurt's design for a self-hosted malware-analysis "combat" lab**
> on a single Linux KVM host — two pfSense firewalls in series wrap a set of isolated
> experiment networks, so malicious software can be detonated and studied two firewall
> hops away from his home network. It's an older, pre-klanker project (the diagram is
> dated 2019).

## What it is

**Elevator version:** it's a homelab for safely playing with dangerous software. Kurt
built a design where one Linux machine runs a bunch of virtual machines — attack boxes,
victim boxes, analysis boxes — walled off behind two firewalls so nothing nasty can reach
his real home network. He calls the dirty side the "combat" network. It's not an app; it's
a network diagram plus a build script — infrastructure as shell script.

**The honest version:** the whole thing runs on one Debian/Ubuntu box with KVM and
libvirt. It's genuinely old — pre-klanker, from around 2019 — and it's a nice window into
the network-isolation instincts that later show up in klanker's sandboxing and
defcon.run's infrastructure. There's no README; the design lives in a draw.io diagram, a
single Open vSwitch build script, and two full pfSense firewall configs checked in as XML
so the firewalls are reproducible.

## How it works

**Open vSwitch networking:** instead of plain Linux bridges, a shell script installs Open
vSwitch and creates six virtual switches that become named libvirt networks — a WAN, a
management network, and four "combat" segments. The physical network card is attached only
to the WAN side, so nothing on the physical NIC touches the experiment networks directly.

**The double-firewall design** is the heart of it — two pfSense firewall VMs in series:
- The **outer firewall (pfwan)** faces the internet and the home router. It NATs the lab
  out to the internet, holds the management network as its LAN, and hands traffic down to
  the inner firewall over an uplink network.
- The **inner firewall (pfcombat)** walls off the experiment side. It has a management leg
  plus three isolated experiment segments — two wired "combat" segments and a WiFi combat
  segment feeding its own access point — each with its own DHCP scope.

So anything running in a combat segment is always **two firewall hops** from anything Kurt
actually cares about. KPH speaks at this subnet-concept level — "a management network",
"the ten-dot combat segments" — and never recites specific addresses.

**What runs inside** (straight off the diagram): Kali Linux for offense; a Windows 10
victim box; Windows 10 analysis VMs including a **FLARE** reverse-engineering box and an
MSEdge box; Ubuntu; a **Whonix gateway + workstation pair** acting as a TOR gateway for
anonymized traffic; and a **Splunk** server sitting on the *management* network, quietly
collecting logs, kept cleanly apart from the traffic being studied.

**A small operational trick:** every virtual NIC gets a hand-assigned, human-readable MAC
pattern per VM — for instance the outer firewall's interfaces all share the same suffix —
so Kurt can tell interfaces apart at a glance when he's debugging. (The MACs are
locally-administered fakes — fine to describe as a pattern, nothing sensitive.)

## Topic map

### The double-firewall concept
- Two pfSense firewalls in series — an outer one facing the internet, an inner "combat"
  one — so malware experiments are always two hops from the home network.

### Open vSwitch + KVM networking
- Rather than plain Linux bridges, six Open vSwitch bridges are wired into libvirt as
  named networks covering WAN, management, and four combat segments; a build script sets
  it all up.

### The combat segments and VMs
- Three isolated experiment networks — two wired combat segments and a WiFi one — host
  Kali, Windows 10 victim and analysis boxes (including a FLARE reverse-engineering VM),
  Ubuntu, and a Whonix TOR gateway pair.

### Management network and monitoring
- A separate management network carries admin access and a Splunk instance for log
  collection, kept apart from the traffic under study.

### pfSense as code
- Both firewalls' full configs are checked in as XML, so the lab is reproducible —
  interfaces, DHCP scopes, gateways, NAT, and rules — though KPH describes this at the
  concept level and never recites config specifics.

## Cross-links

- **Standalone / pre-klanker.** No code or config references km, defcon.run.34, meshtk,
  tiogo, or klanker-voice — the diagram predates the klanker platform.
- Thematic link KPH *can* draw: it's Kurt's long-running security/homelab streak — the
  same network-isolation instincts that later show up in klanker's sandboxes and
  defcon.run's infrastructure. Same `whereiskurt` GitHub account as tiogo.
- **tiogo:** both are older `whereiskurt` security projects from the same era; separate
  tools, no code link.

## Sample Q→A

1. **Q: What is kvmlab?**
   A: Kurt's design for a malware-analysis lab on a single Linux KVM host — two pfSense
   firewalls in series protecting isolated "combat" networks where he can safely detonate
   and study malicious software.

2. **Q: Why two firewalls?**
   A: Defense in depth — an outer firewall faces the internet and home network, and an
   inner "combat" firewall walls off the experiment segments, so anything nasty is always
   two hops from anything Kurt cares about.

3. **Q: What runs inside the lab?**
   A: Kali Linux for offense, Windows 10 victim and analysis VMs — including a FLARE
   reverse-engineering box — Ubuntu, a Whonix TOR gateway pair for anonymized traffic, and
   a Splunk server on the management network watching the logs.

4. **Q: How is the networking built?**
   A: With Open vSwitch — a shell script creates six virtual switches and registers them as
   libvirt networks, so each VM plugs into exactly the segment it belongs on.

5. **Q: What's the "combat" network?**
   A: That's Kurt's name for the dirty side of the lab — the segments behind the inner
   firewall where malware and attack traffic are allowed to run, including a dedicated WiFi
   combat segment with its own access point.

6. **Q: Is it part of the klanker platform?**
   A: No — it's an older standalone homelab project from around 2019, but you can see the
   same isolation-first instincts that later shaped klanker's sandboxing.

7. **Q: Why the funny MAC addresses?**
   A: Kurt hand-assigns memorable MAC patterns per VM — like the outer firewall's NICs all
   sharing a suffix — so he can tell interfaces apart instantly when debugging.

## Landmines / do-not-say

- Never recite specific IP addresses, DHCP scopes, or firewall interface addresses — speak
  only at the subnet-concept level ("a management network", "the combat segments").
- Never surface or imply the contents of the pfSense configs beyond structure — they embed
  private TLS key material and password hashes that must never be spoken or hinted at.
- Never recite Kurt's personal lab domain — say "his lab domain" instead.

## PACK COMPLETE — kvmlab
