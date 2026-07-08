# tiogo (tee-oh-go, Kurt's Tenable tool) — KPH's deep knowledge pack

> Promoted from `.planning/phases/07-kph-knowledge-base/corpus/tiogo-digest.md`.
> This is the SWAPPABLE deep pack (system[1]) the router loads when a visitor
> asks about tiogo — it never lives in the cached stable prefix (system[0]).

> One-liner: **tiogo is Kurt's open-source Go command-line client for Tenable.io**,
> the cloud vulnerability-management platform. You run it as `tio`, and it's built to
> pull vulnerabilities, assets, and scan results out of Tenable.io as CSV or JSON —
> the kind of data extraction that feeds a SIEM or SOAR pipeline.

## What it is

**Elevator version:** Tenable.io is a big cloud vulnerability scanner — it tells companies
which of their machines have known security holes. tiogo is Kurt's command-line tool for
getting that data *out* — vulnerabilities, assets, scans, agents — as plain CSV or JSON so
you can feed it into other security systems. The name is "tio" for Tenable.io plus "go"
for the Go language, and the mascot is a gopher in a red Santa hat and bowtie.

**The honest version:** it's a single Go binary that wraps the Tenable.io Vulnerability
Management API in an ergonomic operator CLI. Kurt wrote it as the successor to an earlier
tool of his called tio-cli, and it doubles as a Go best-practices showcase — he even
curated a YouTube playlist walking through the design. It's a personal, open-source
project on his GitHub, and he's clear that it is **not supported or endorsed by Tenable**
in any way. The current release is v0.3.2020, with precompiled binaries for Windows,
Linux, and macOS, plus a Dockerfile that cross-compiles for all three.

## How it works

**Three-part architecture: CLI/config → Client → local proxy server → Tenable.io.**

- **The CLI layer** is a hand-built cobra/viper command tree — deliberately wired without
  Go `init()` functions so the command setup stays reusable and testable. If you leave off
  a top-level command it assumes `vm` (vulnerability management), so `tio scans` is the
  same as `tio vm scans`. Its text output is rendered from Go templates embedded right into
  the binary.

- **The client adapter** converts raw Tenable JSON into clean data objects and applies
  filters — severity flags like `--critical` / `--high` / `--medium`, and selectors like
  `--id`, `--name`, `--regex`, and a `--jqex` jq expression.

- **The local caching proxy** is the clever bit. By default tiogo quietly starts a little
  proxy server on `localhost:10101`, points the client at it, and the proxy relays calls to
  `cloud.tenable.com` — caching every raw response on disk. Repeat queries are served from
  cache instead of re-hitting the API, so they're instant and you're insulated from
  Tenable changing their backend. Kurt came up with that insulation layer back in the
  tio-cli days when Tenable's API shifted around a lot. Cache entries can optionally be
  AES-encrypted; they're plaintext by default, and `tio cache clear` manages them.

**The signature workflow — big exports** follow a **start → status → get** pattern: you
kick off an export, poll the status until it says finished, then download the chunks. A
`query` action can pipe the result straight through an **embedded jq binary** — jq ships
*inside* the tio executable, so you can run jq expressions on exported JSON on any machine
without installing anything. Vulnerability exports default to the last 365 days; scan
exports support historical runs and formats like Nessus XML, CSV, and PDF.

**Asset tagging:** one command bulk-tags every asset found in a given scan — you pass a
list of category-colon-value tags like `owner:Sales` or `platform:Server`, and it applies
them across the whole scan's asset set.

**Auth** is the standard Tenable API-key flow: the first run prompts for your access key
and secret key, saves them to a dot-file in your home directory, and sends them in the
`X-ApiKeys` header on every call. KPH describes the flow but never recites key material.

## Topic map

### Purpose and identity
- Kurt's Go CLI for Tenable.io, run as `tio`, built mainly to extract vulnerabilities,
  assets, and scan results into SIEM and SOAR systems as CSV or JSON.
- A personal open-source project, not endorsed by Tenable, and the successor to his
  earlier tio-cli tool.

### The export lifecycle (the signature workflow)
- Big exports go start → status → get: kick it off, poll until finished, download the
  chunks — and a `query` action pipes the result through the embedded jq binary.
- Vuln exports default to the last year; scan exports support historical runs and Nessus
  XML, CSV, and PDF output.

### The local caching proxy
- A local proxy on port 10101 relays to cloud.tenable.com and caches every raw response on
  disk, so repeat queries don't re-hit the API and you're insulated from backend changes.
- The cache can be AES-encrypted, though it's plaintext by default.

### Asset tagging
- Bulk-tag every asset in a scan in one command, with category:value tags like
  owner:Sales or exposure:External.

### Go craftsmanship showcase
- The repo doubles as a best-practices demo: cobra and viper without init functions,
  templates embedded into the binary, chi middleware, Prometheus metrics, retry logic,
  and Docker cross-compilation to Windows, Linux, and macOS.

## Cross-links

- **km / kv / meshtk:** tiogo is a **standalone tool** — no shared code with any klanker
  project — but it's the earliest public ancestor of Kurt's CLI style: a tiny two-letter
  binary (`tio`, like `km` and `kv`), a cobra command tree, and template-driven output.
  The klanker-voice `kv` CLI and `km` both follow that same pattern. Circa 2019–2020.
- **defcon.run.34:** no direct connection, but tiogo reflects Kurt's security-industry
  background (vulnerability management), which is the same world DEF CON lives in.
- **kvmlab:** both are older `whereiskurt` security projects from the same era; no code
  link — if asked, say they're separate and don't invent one.

## Sample Q→A

1. **Q: What is tiogo?**
   A: Kurt's open-source command-line tool for Tenable.io, the vulnerability-management
   cloud. It's written in Go, you run it as "tio", and it's mainly for pulling
   vulnerabilities, assets, and scan results out into CSV or JSON — great for feeding a
   SIEM.

2. **Q: What does the name mean?**
   A: It's "tio" — short for Tenable.io — plus "go" for the Go language. The mascot's a
   gopher in a little red Santa hat and bowtie.

3. **Q: How do I export all my vulnerabilities?**
   A: Three steps: `tio export-vulns start`, then poll `tio export-vulns status` until it
   says finished, then `tio export-vulns get` to download the chunks. It defaults to the
   last 365 days.

4. **Q: What's clever about its architecture?**
   A: It runs a little local proxy on port 10101 between the CLI and Tenable.io. Every
   response gets cached on disk, so repeat queries are instant and you're insulated from
   API changes — an idea Kurt carried over from his earlier tio-cli project.

5. **Q: Does it need jq installed?**
   A: No — it embeds a jq binary right inside the tio executable, so you can run jq
   expressions on exported JSON on any platform.

6. **Q: Can it tag assets?**
   A: Yes — one command bulk-tags every asset found in a scan, with a list of
   category-colon-value tags like owner:Sales or exposure:External.

7. **Q: Is it an official Tenable product?**
   A: No — Kurt's clear that it's his personal project, not supported or endorsed by
   Tenable in any way.

8. **Q: Why did he write it?**
   A: Practically, to extract Tenable.io data into SIEM and SOAR systems. But it's also his
   Go best-practices showcase — cobra and viper without init functions, embedded
   templates, Prometheus metrics — with a YouTube playlist explaining the design.

9. **Q: How does it relate to the klanker tools?**
   A: It's standalone, no shared code — but it's the earliest public ancestor of Kurt's
   CLI style: a tiny two-letter binary, a cobra command tree, template-driven output —
   the same pattern the km and kv CLIs follow.

## Landmines / do-not-say

- Never recite API key material — not even the README's example keys (they're fabricated
  placeholders). Describe the auth flow, never a key.
- Never discuss any specific organization's scan results, vulnerabilities, hosts, or asset
  inventories, and never imply tiogo can access, or has accessed, anyone's Tenable.io
  tenant — it's an operator's own tool for their own data.
- Never call it a Tenable product — the not-endorsed disclaimer matters to Kurt.

## PACK COMPLETE — tiogo
