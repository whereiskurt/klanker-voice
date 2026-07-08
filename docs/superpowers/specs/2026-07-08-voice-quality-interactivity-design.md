# Voice quality & interactivity pass — design

**Date:** 2026-07-08
**Status:** approved-pending-review
**Scope:** Four small, independent levers that make the live concierge feel more
interactive and better-spoken, now that the login experience is settled. This is a
polish pass on the shipped Phase-07/07.1 pipeline — no architectural change.

## Goal

The pipeline sounds good; the *interaction* should feel warmer and more human, and
KPH should know more of Kurt's world. Four strategic changes:

1. **Two new deep topics** — `tiogo` and `kvmlab` — wired into the existing router +
   knowledge machinery so KPH can talk about them like it already talks about km,
   defcon.run, and meshtk.
2. **A warmer opening greeting** that names what KPH can talk about and invites the
   visitor to pick.
3. **Naturalized topic-switch ack** — the single fixed "Okay! Let's dig into X." line
   becomes a small rotating set, so the "let me think about it" beat feels human.
4. **Pronunciation fixes** for the terms that currently sound robotic (`km`, `km CLI`,
   `defcon.run.34`).

## Non-goals

- No new pipeline stages, no filler-on-every-turn machinery. The only "thinking" beat
  is the existing switch-ack, made warmer (Lever C). The heavier "filler on deep
  non-switch turns" idea was explicitly deferred (talk-over/barge-in risk).
- tiogo and kvmlab do **not** join the proactive 60-second tour — the tour stays the
  flagship trio (km → defcon.run → meshtk). The two new topics are answerable on
  request only.

---

## Lever A — New deep topics: `tiogo` + `kvmlab`

Both topics reuse the Phase-07 knowledge apparatus exactly as meshtk does. The curated
digests already exist (`.planning/phases/07-kph-knowledge-base/corpus/tiogo-digest.md`,
`kvmlab-digest.md`) and the manifest's "digest promoted directly into the pack" pattern
means the pack builds from the digest even though neither source repo is required
(`skip_if_missing: true` on all repo sources). tiogo is not cloned locally; kvmlab is at
`/Users/khundeck/working/backup-maker/clones/kvmlab`.

### Files touched

1. **`knowledge/router/topic-map.yaml`** — append two topic entries: `id`, `spoken_name`,
   a one-line `hook` (rendered into stable block0 so KPH can always name the topic even
   before its deep pack loads), and weighted keyword lists. Keyword-weighting care:
   - `tiogo`: distinctive = `tiogo`, `tio go`, `tenable`, `tenable io`, `tio-cli`,
     `nessus`, `vuln export` (weight 2–3). Weak-alone = bare `tio`, `export`,
     `vulnerability` (weight 1). Avoid keying on generic `go`/`cli`.
   - `kvmlab`: distinctive = `kvmlab`, `kvm lab`, `combat lab`, `malware lab`,
     `double firewall`, `pfsense`, `open vswitch`, `whonix` (weight 2–3). Weak-alone =
     bare `kvm`, `lab`, `firewall` (weight 1).

2. **`knowledge/manifest.yaml`** — append two topics pointing at their `*-digest.md`
   (the promoted-into-pack source, `kind: docs`) plus `skip_if_missing` repo sources for
   provenance (tiogo → `github.com/whereiskurt/tiogo`; kvmlab → the local clone path +
   its diagram/scripts). Do **not** add either to `tour_priority` (stays
   `[klanker-maker, defcon-run-34, meshtk]`).

3. **Pack build** — run the sanctioned `kv-refresh-knowledge` workflow
   (`make -C apps/voice knowledge`), which applies the do-not-say scrubber and gates on
   the D-09 git-diff human review. Produces `knowledge/topics/tiogo.md` and
   `knowledge/topics/kvmlab.md`. Small packs under the 4096-token cache threshold simply
   aren't separately cached — fine at this size.
   - **kvmlab enrichment:** the diagram PNG
     (`Virtual Lab Design (KVM).png`) was read directly; fold the exact topology into
     the pack — six OVS bridges (`wan`, `manage 172.16.0.0/12`, `combat 10.0.0.0/16`,
     `combat_ex1 10.1.0.0/16`, `combat_ex2 10.2.0.0/16`, `combat_wifi 10.3.0.0/16`);
     pfwan (outer) and pfcombat (inner) pfSense VMs with their `vtnet` NIC/MAC maps;
     combat_ex1 = kali/win10/ubuntu, combat_ex2 = MSEdge/FLARE, combat_wifi = Victim.win10
     behind a WiFi AP; Whonix TOR gateway+workstation at `10.152.152.x`; Splunk on the
     management net. **Correct the digest's claim** that pfcombat MACs end in `:44` — that
     is Splunk; pfcombat's NICs are `56:55 / 62:66 / 66:66 / 76:77 / 86:88`.

4. **`prompts/concierge.md`** — add one line each to "What you know":
   - tiogo — Kurt's open-source Go CLI (`tio`) for Tenable.io; pulls vulnerabilities,
     assets, and scans out as CSV/JSON for SIEM/SOAR, with a local caching proxy.
   - kvmlab — Kurt's pre-klanker double-firewall KVM "combat lab" for malware/offensive-
     security experiments, isolated two firewall hops from his home network.

### Logistics note (implementation)

The corpus digests live in the **main** worktree's `.planning/` tree; this
`voice-improve` worktree has none. Run the pack build from a checkout where the digests
resolve (main worktree), or stage the two digest files where the refresh reads them.
This is an execution detail, not a design change.

### Tests

- `test_knowledge_router` — extend fixtures/expected topic set to include the two new ids;
  assert `tiogo`/`kvmlab` classify above the confidence floor on representative
  utterances and that weak-alone terms (`go`, `lab`) do not over-trigger.
- `test_knowledge_pack` — the two packs load and are non-empty.
- Add scenario files mirroring `kph_knowledge_meshtk.yaml` for the two topics (optional,
  eval-harness coverage).

---

## Lever B — Warmer opening greeting

The three pre-rendered greeting clips are terse ("Hey — I'm KPH. What's on your mind?").
Replace them with the fuller energy while keeping the KPH identity (persona rule: KPH
must always name itself KPH).

### Files touched

1. **`client/public/greetings/greetings.source.json`** — new lines, primary variant:
   > "Hey, how's it going — I'm KPH, Kurt's concierge. I can tell you about a bunch of his
   > GitHub projects and DEF CON run. What do you wanna hear about?"

   Plus one or two shorter variants in the same voice (e.g. "Hey — KPH here, Kurt's
   concierge. Ask me about his projects or DEF CON run — what's got your interest?").

2. **Re-render clips** — `make -C apps/voice greetings` renders from the configured
   Burt Fundeck voice in `pipeline.toml` and rewrites `greetings.manifest.json`. The
   `test_greeting_voice_drift` guard checks source↔manifest consistency and passes after
   re-render.

3. **`prompts/concierge.md` "Opening move"** — small tweak: the greeting now *already*
   offered the topic menu and asked "what do you wanna hear about," so KPH's first real
   turn must answer directly and must not re-list the menu or re-greet. The existing
   `NO_REGREET_KICK_MESSAGE` already suppresses the double-greeting; the persona note just
   needs to reflect that the menu was already offered.

### Tests

- `test_greeting_voice_drift` — passes post-render (source/manifest/voice_id aligned).

---

## Lever C — Naturalize the switch-ack

`router.py` fires one fixed line, `DEFAULT_ACK_TEMPLATE = "Okay! Let's dig into
{spoken_name}."`, on every genuine topic switch. This is the "let me think about it" beat
that masks the pack-swap + BM25 retrieval. Make it a small **round-robin** set (not
random — deterministic for tests), every variant still ending on `{spoken_name}` so the
retrieval stays masked behind the ack:

- "Ooh, {spoken_name} — good one. Let me get into it."
- "Okay, let's dig into {spoken_name}."
- "Right — {spoken_name}. Here's the deal."
- "Love that one. So, {spoken_name}…"

### Files touched

- **`src/klanker_voice/knowledge/router.py`** — replace the single template constant with
  an ordered `_ACK_TEMPLATES` list; the processor holds an index counter and advances it
  per fired ack (round-robin, deterministic). Keep `ack_template` override support for
  tests/config, defaulting to the list.

### Tests

- `test_knowledge_router` — update ack assertions to accept any member of the set and to
  verify round-robin advancement; retrieval-masking behavior unchanged.

---

## Lever D — Pronunciation fixes

`src/klanker_voice/pronunciation.py` `_RULES` is an ordered list (earlier rules win on
overlap); the fix is adding/replacing rules in the right order. TTS-only — captions still
show the raw `km` / `DEF CON` forms.

### Rule changes (order matters — most-specific first)

| Term (input) | Current spoken | New spoken |
|---|---|---|
| `km CLI` / `kmCLI` | "kay em see elle eye" | **"the klanker maker tool"** (new rule, before bare `km`/`CLI`) |
| bare `km` (word-boundary) | "kay em" | **"klanker maker"** |
| `defcon.run.34` | (no rule → "DEFCON-er-one" mangling) | **"deaf con run thirty four"** (new rule, before `defcon.run`) |

Existing rules retained: `def con.run` → "deaf con run", `mesh tk` → "Mesh Tee Kay",
`def con` → "deaf con", `CLI` → "see elle eye" (still catches standalone CLI),
`Guelph` → "Gwelf".

### Files touched

- **`src/klanker_voice/pronunciation.py`** — insert `km CLI` and `defcon.run.34` rules
  ahead of their more-general counterparts; change the bare `km` mapping.

### Tests

- `test_pronunciation_filter` — add cases: "the km CLI" → "…the klanker maker tool",
  standalone "km" → "klanker maker", "defcon.run.34" → "deaf con run thirty four", and a
  regression that `km` still does not fire inside `kmv`/`10km`.

---

## Rollout order

Independent levers; suggested sequence for clean commits and verification:

1. **D (pronunciation)** — pure function + unit test, zero infra. Fastest win.
2. **C (ack)** — router change + unit test.
3. **B (greeting)** — source edit + `make greetings` re-render + drift test.
4. **A (topics)** — topic-map/manifest edits + `make knowledge` pack build (D-09 git-diff
   gate) + concierge lines + router fixtures. Largest, gated on human diff review.

Levers A and B require live/audible verification (does KPH name tiogo/kvmlab correctly;
does the new greeting sound right in Burt Fundeck's voice). C and D are unit-verifiable
plus a spot-check on live audio.

## GSD note

Project convention (CLAUDE.md) routes edits through a GSD command. These are small,
independent changes — a good fit for `/gsd-quick` per lever (or one small phase), with
Lever A's pack build going through the `kv-refresh-knowledge` workflow and its D-09 gate.
