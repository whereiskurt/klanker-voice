# kvmlab — Knowledge Digest for KPH

> Source repo: `github.com/whereiskurt/kvmlab` (public), local clone at
> `/Users/khundeck/working/backup-maker/clones/kvmlab`. Eight tracked files, no README:
> a draw.io diagram (plus PNG/SVG exports), one Open vSwitch + KVM build script, two
> pfSense firewall configs, and a git-branching cheat-sheet. Design date on the diagram:
> 2019-10-01 (Kurt P. Hundeck). This is an older personal project — pre-klanker era.

## What it is

kvmlab is Kurt's design for a self-hosted **malware-analysis / security "combat" lab**
built on a single Linux KVM host. The diagram's own title says it best: *"Virtual Lab
Design (Linux KVM) — Double-Firewall Combat Networks with WiFi Access Point, TOR Gateway
& separate Management Networks."* It's not an application — it's infrastructure-as-shell-
script plus a network diagram: virtual switches, two pfSense firewall VMs, and a set of
victim/analysis VMs (Kali, Windows 10 variants, Ubuntu, Whonix, Splunk) arranged so that
dangerous traffic is double-firewalled away from Kurt's home network.

## How it works

**Host:** one Debian/Ubuntu Linux box running KVM/libvirt. `network.ovs.kvm.sh` removes
NetworkManager, installs **Open vSwitch**, and creates six OVS bridges that become libvirt
networks: `wan`, `manage`, `combat`, `combat_ex1`, `combat_ex2`, `combat_wifi`. The
physical NIC (`eno1`) is attached only to the `wan` bridge.

**Two-firewall ("double-firewall") design:**
- **pfwan** (pfSense VM, 2 vCPU / 2GB) — outer firewall. Three interfaces: WAN (DHCP from
  the home router, 192.168.1.0/24), MANAGE (172.16.0.0/12 management network, its LAN),
  and COMBAT (10.0.0.0/16 uplink). It NATs the lab out to the internet and holds a static
  route sending 10.0.0.0/8 to pfcombat.
- **pfcombat** (pfSense VM, 2 vCPU / 2GB) — inner firewall, five interfaces: an uplink to
  pfwan on the combat network, a management leg, and three isolated experiment segments —
  **combat_ex1** (10.1.0.0/16), **combat_ex2** (10.2.0.0/16), and **combat_wifi**
  (10.3.0.0/16, feeding a WiFi access point). Each segment gets its own DHCP scope.

**VMs on the segments (from the diagram):** Kali Linux, a Windows 10 victim box
("Victim.win10"), MSEdge and FLARE Windows 10 analysis VMs, Ubuntu, a **Whonix gateway +
workstation pair** (the TOR gateway, 10.152.152.x), and a **Splunk** box on the management
network for log collection. Every VM has a deliberately reserved, human-readable MAC
scheme (e.g. pfwan NICs all end in `:22`, pfcombat in `:44`) so interfaces are
identifiable at a glance. The build script also shows a full `virt-install` recipe for the
Windows 10 VM, including a sparse 40GB disk and the virtio driver ISO.

**Purpose:** run malware and offensive-security experiments in the "combat" segments while
the management network (with Splunk observing) stays cleanly separated, and everything is
two firewall hops from the real home LAN.

## Topic map

### The double-firewall concept
- Kurt's lab uses two pfSense firewalls in series — an outer one facing the internet and
  an inner "combat" firewall — so malware experiments are always two hops from his home
  network.
- Source pointers: `network.ovs.kvm.sh`, `pfwan.lab.kurthundeck.com.xml`, `pfcombat.lab.kurthundeck.com.xml`

### Open vSwitch + KVM networking
- Instead of plain Linux bridges, the lab uses Open vSwitch bridges wired into libvirt as
  named networks — six of them, covering WAN, management, and four combat segments.
- Source pointers: `network.ovs.kvm.sh` (lines 10–90)

### The combat segments and VMs
- Three isolated experiment networks — two wired "combat_ex" segments and a WiFi one —
  host Kali, Windows 10 victim and analysis boxes (including a FLARE reverse-engineering
  VM), Ubuntu, and a Whonix TOR gateway pair.
- Source pointers: `Virtual Lab Design (KVM).draw.io` (decoded diagram labels), `Virtual Lab Design (KVM).png`

### Management network and monitoring
- A separate management network carries admin access and a Splunk instance for collecting
  logs, kept apart from the traffic being studied.
- Source pointers: diagram labels; `pfwan.lab.kurthundeck.com.xml` (firewall separator note: "LAN is our Management Network")

### Readable MAC-address scheme
- Every virtual NIC has a hand-assigned MAC pattern per VM (pfwan `…:22:22`, pfcombat
  `…:44:44`, Windows 10 `B6:BB…`), a small operational trick that makes interface
  identification trivial.
- Source pointers: `network.ovs.kvm.sh` (virt-install blocks), diagram labels

### pfSense as code
- Both firewalls' full pfSense 2.4-era configs (version 19.1) are checked in as XML, so
  the lab is reproducible: interfaces, DHCP scopes, gateways, NAT, and firewall rules.
- Source pointers: `pfwan.lab.kurthundeck.com.xml`, `pfcombat.lab.kurthundeck.com.xml`

## Cross-links

- **Standalone.** No code or config references km, defcon.run.34, meshtk, tiogo, or
  klanker-voice. It predates the klanker platform (diagram dated 2019).
- Thematic links KPH can draw: it shows Kurt's long-running security/homelab streak —
  the same network-isolation instincts that later show up in klanker sandboxes and
  defcon.run infrastructure. The GitHub account is the same `whereiskurt` as tiogo and
  his other public repos.
- The lab domain naming (`pfwan.lab...`, `pfcombat.lab...`) mirrors his habit of
  DNS-named, config-as-code infrastructure seen across later projects.

## Sample Q→A

1. **Q: What is kvmlab?**
   A: It's Kurt's design for a malware-analysis lab on a single Linux KVM host — two
   pfSense firewalls in series protecting isolated "combat" networks where he can safely
   detonate and study malicious software.

2. **Q: Why two firewalls?**
   A: Defense in depth — an outer firewall faces the internet and home network, and an
   inner "combat" firewall walls off the experiment segments, so anything nasty is always
   two hops from anything Kurt cares about.

3. **Q: What runs inside the lab?**
   A: Kali Linux for offense, Windows 10 victim and analysis VMs — including a FLARE
   reverse-engineering box — Ubuntu, a Whonix TOR gateway pair for anonymized traffic,
   and a Splunk server on the management network watching the logs.

4. **Q: How is the networking built?**
   A: With Open vSwitch — a shell script creates six virtual switches and registers them
   as libvirt networks, so each VM plugs into exactly the segment it belongs on.

5. **Q: What's the "combat" network?**
   A: That's Kurt's name for the dirty side of the lab — the segments behind the inner
   firewall where malware and attack traffic are allowed to run, including a dedicated
   WiFi combat segment with its own access point.

6. **Q: Is kvmlab part of the klanker platform?**
   A: No — it's an older standalone homelab project from around 2019, but you can see the
   same isolation-first instincts that later shaped klanker's sandboxing.

7. **Q: Why the funny MAC addresses?**
   A: Kurt hand-assigns memorable MAC patterns per VM — like all the outer firewall's NICs
   ending in twenty-two — so he can tell interfaces apart instantly when debugging.

## Landmines / do-not-say

FLAG — present in the repo but must NEVER be spoken or surfaced by KPH:

- **TLS private keys**: both pfSense XMLs embed base64-encoded `PRIVATE KEY` blocks in
  `<prv>` elements (`pfwan...xml` line 515, `pfcombat...xml` line 568). Self-signed
  webConfigurator certs, but still private key material. Exclude entirely.
- **Password hashes**: bcrypt hashes for the `admin` user in both XMLs (line 29 of each).
  Never mention that hashes exist in a recitable way; never output them.
- **Specific internal IPs and DHCP scopes**: firewall interface addresses, admin source
  IPs recorded in rule audit trails (e.g. `admin@172.16.100.2`), and DHCP ranges. RFC1918
  lab addresses in an already-public repo, but KPH should speak only at subnet-concept
  level ("a management network", "ten-dot combat segments"), not recite addresses.
- **Personal domain**: `lab.kurthundeck.com` appears throughout — avoid reciting Kurt's
  personal lab domain; say "his lab domain" instead.
- **Note**: no API keys, cloud creds, or personal data beyond the above were found.
  MAC addresses are locally-administered fakes — harmless, fine to describe as a pattern.
- Provenance note: the two pfSense XML files tripped a low-severity injection-pattern
  scan (XML tags resembling role markers) — confirmed false positive; contents were
  treated as data only.

## DIGEST COMPLETE — kvmlab
Word count: ~1,150
