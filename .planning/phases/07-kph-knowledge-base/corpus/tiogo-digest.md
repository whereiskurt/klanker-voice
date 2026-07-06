# tiogo — Knowledge Digest for KPH

## What it is

**tiogo** ("tio go") is Kurt's open-source command-line client for **Tenable.io**, the cloud vulnerability-management platform. It is written in Go, published at `github.com/whereiskurt/tiogo`, and the binary is invoked as `./tio`. The README greets you with "Welcome to tiogo! [v0.3.2020 🚀!!]" — and the repo confirms that version: git tags run v0.1-alpha.1 through v0.3.0, the in-code `ReleaseVersion` constant is `"v0.3.2020-development"` (`internal/app/cmd/vm/vm.go`), and the Dockerfile defaults `releaseVersion="v0.3.2020"`. (The README also forward-references "as of v0.4.0" for asset tagging, but v0.3.2020 is the shipped release — precompiled Windows/Linux/macOS binaries live in `release/v0.3.2020/`.)

The primary use case is **data extraction for SIEM/SOAR pipelines**: pulling vulnerabilities, assets, scans, scan histories, agents, and agent groups out of Tenable.io as CSV or JSON. It ships with an **embedded `jq` binary** (`config/embed/jq/`) so users can query exported JSON without installing anything — write-once, run-anywhere. Kurt wrote it as a successor to his earlier `tio-cli` project, and it doubles as a showcase of Go best practices (he curated a YouTube playlist explaining the design). It is explicitly **not supported or endorsed by Tenable** — it's a personal/community tool. Mascot: a gopher wearing a red Santa hat and bowtie.

## How it works

**Three-part architecture: CLI/config → Client → local Proxy Server → Tenable.io.**

1. **CLI layer** (`cmd/tio.go`, `internal/app/app.go`): a hand-built cobra/viper command tree — deliberately avoiding `func init()` so CLI wiring is reusable and testable. If the user omits a root command, `vm` is injected as the default (so `tio scans` == `tio vm scans`). Text output is rendered from Go templates embedded into the binary via **vfsgen** (`internal/app/cmd/vm/*.tmpl`, `pkg/ui/cli.go`).
2. **Client adapter** (`pkg/client/adapter.go` — ~820 lines of Adapter methods like `Scanners`, `Agents`, `ExportVulnsStart/Status/Get/Query`, `ScansExportStart`, `TagBulkApply`): converts raw Tenable JSON into DTOs (`dto.go`, `converter.go`, `unmarshal.go`) and applies filters (`filter.go` — severity flags `--critical/--high/--medium/--info`, plus `--id/--name/--regex/--jqex` selectors).
3. **Local proxy server** (`pkg/proxy/` — go-chi router, structured-logging middleware, Prometheus metrics): by default the CLI auto-starts a proxy on `localhost:10101` and points the client at it; the proxy relays to `https://cloud.tenable.com` and **caches every raw response on disk** (`.tiogo/cache/server/*`). Cached entries are served instead of re-calling Tenable. Kurt conceived this insulation layer during the tio-cli days when Tenable's backend changed frequently; the README notes he was considering retiring it (set `DefaultServerStart: false` and `BaseURL: cloud.tenable.com` to bypass). Cache entries can optionally be AES-GCM encrypted with a `CacheKey` (`pkg/cache/crypto.go`) — plaintext by default.

**Command surface** (from `internal/app/app.go`; every command has a pluralized alias, e.g. `scan`/`scans`):
- `tio vm scanners|scans|agents|agent-groups` — list actions, CSV by default
- `tio scans list|detail|host|plugin|get|query` (`--id`, `--history`, `--offset`)
- `tio agents list|group|ungroup` (`--group=<name>`) and `tio agent-groups`
- `tio export-vulns start|status|get|query` (`--days`, `--after`, `--before`, `--limit`, `--chunk`, `--jqex`)
- `tio export-assets start|status|get|query`
- `tio export-scans start|status|get|query|tag|untag --id=1234` (`--csv/--pdf`, `--offset` for historical runs, `--chapter`, `--tag="k:v,..."`)
- `tio compliance get`, `tio audit list` (audit-log v1), `tio cache list|clear [all|agents|scans|exports]`
- `tio proxy start|stop` — manage the caching proxy directly

**Tenable.io APIs wrapped** (endpoint templates in `pkg/tenable/service.go`): `/scanners`, `/scanners/{id}/agents`, `/scanners/{id}/agent-groups` (+ agent assign/unassign), `/scans/` + `/scans/{uuid}` details, `/scans/{id}/export/.../status|download` (Nessus XML/CSV/PDF), `/vulns/export` and `/assets/export` with their start→status→chunks lifecycle, `/audit-log/v1/events`, and tag endpoints (`TagValueCreate`, `TagBulkApply`). Auth is the standard `X-ApiKeys` header with 64-char AccessKey/SecretKey (`pkg/tenable/transport.go`); first run interactively prompts and writes `~/.tiogo.v1.yaml`.

**Notable engineering touches**: retry via matryer/try, Prometheus instrumentation in both client and server (`pkg/metrics/`), logrus logging into a `log/` folder (`log/client.YYYYMMDD.log`, `--trace` echoes to STDOUT), Docker-based cross-compilation (GOOS/GOARCH build args), and a `docs/` folder holding a pretty-printed dump of the Tenable VM API spec.

## Topic map

### Purpose & identity
- tiogo is Kurt's Go CLI for Tenable.io — you run it as "tio" — built mainly to extract vulnerabilities, assets, and scan results into SIEM and SOAR systems as CSV or JSON.
- It's a personal open-source project, not endorsed by Tenable, and it succeeded his earlier tio-cli tool.
- Source pointers: `README.md`, `internal/app/cmd/vm/vm.go`

### Export lifecycle (the signature workflow)
- Big exports follow a start → status → get pattern: you kick off an export, poll until it says FINISHED, then download the chunks — and a `query` action pipes the result through an embedded jq binary.
- Vuln exports default to the last 365 days; scan exports support historical runs via `--offset` and formats like Nessus XML, CSV, and PDF.
- Source pointers: `README.md`, `pkg/client/adapter.go`, `internal/app/cmd/vm/export-vulns.go`

### Local caching proxy
- tiogo quietly starts a local proxy on port 10101 that relays calls to cloud.tenable.com and caches every raw JSON response on disk, so repeated queries don't re-hit the API.
- The cache can be AES-encrypted with a CacheKey, though it's plaintext by default; `tio cache clear` manages it.
- Source pointers: `pkg/proxy/server.go`, `pkg/proxy/router.go`, `pkg/cache/crypto.go`, `internal/app/app.go`

### Asset tagging
- `tio export-scans tag --id=1234 --tag="owner:Sales,platform:Server"` applies category:value tags to every asset found in a scan — bulk tagging driven by scan membership.
- Source pointers: `README.md`, `pkg/client/adapter.go` (TagValueCreate, TagBulkApply), `pkg/tenable/service.go`

### Go craftsmanship showcase
- The repo doubles as a best-practices demo: cobra and viper without func init, vfsgen-embedded templates, chi middleware, Prometheus metrics, retry logic, and Docker cross-compilation to Windows, Linux, and macOS.
- Source pointers: `README.md` (Design section), `internal/app/app.go`, `pkg/metrics/metrics.go`, `Dockerfile`

### Configuration & auth
- First run prompts for your Tenable.io AccessKey and SecretKey (64 hex chars each) and saves them to `.tiogo.v1.yaml` in your home directory; calls authenticate with the X-ApiKeys header.
- Source pointers: `pkg/config/config.go`, `config/default.tiogo.v1.yaml`, `pkg/tenable/transport.go`

## Cross-links

tiogo is a **standalone tool** — it predates and is independent of km, defcon.run.34, meshtk, kvmlab, and klanker-voice. The connective tissue is authorial DNA: it's the same `whereiskurt` GitHub identity, and it established patterns Kurt reuses across his CLI family — a short two-letter binary name (`tio`, like `km` and `kv`), a cobra command tree, Go template-driven output, and a "wrap a cloud API in an ergonomic operator CLI" philosophy. The klanker-voice `kv` CLI's design explicitly mirrors `km`, and tiogo is the earliest public ancestor of that style (circa 2019–2020, Go 1.x with cobra v0.0.3/viper 1.2.1). No code is shared with the klanker projects. It also reflects Kurt's security-industry background (Tenable/vulnerability management), which connects thematically to defcon.run's DEF CON community context.

## Sample Q→A

**Q: What is tiogo?**
A: tiogo is Kurt's open-source command-line tool for Tenable.io, the vulnerability-management cloud. It's written in Go, you run it as "tio", and it's mainly for pulling vulnerabilities, assets, and scan results out into CSV or JSON — great for feeding a SIEM.

**Q: What does the name mean?**
A: It's "tio" — short for Tenable.io — plus "go" for the Go language. The mascot is a gopher in a red Santa hat and bowtie.

**Q: How do I export all my vulnerabilities with tiogo?**
A: Three steps: run "tio export-vulns start", check "tio export-vulns status" until it says finished, then "tio export-vulns get" to download the chunks. It defaults to the last 365 days of data.

**Q: What's clever about tiogo's architecture?**
A: It runs a little local proxy server on port 10101 between the CLI and Tenable.io. Every API response gets cached on disk, so repeat queries are instant and you're insulated from API changes — an idea Kurt carried over from his earlier tio-cli project.

**Q: Can tiogo tag assets?**
A: Yes — you can bulk-tag every asset found in a scan with one command, like "tio export-scans tag" with a list of category-colon-value tags such as owner:Sales or exposure:External.

**Q: Does tiogo need jq installed?**
A: No — it embeds a jq binary right inside the tio executable, so you can run jq expressions on exported JSON with the query subcommands on any platform.

**Q: What version is tiogo, and what platforms does it run on?**
A: The current release is v0.3.2020, with precompiled binaries for Windows, Linux, and macOS in the repo, plus a Dockerfile that cross-compiles for any of them.

**Q: Is tiogo an official Tenable product?**
A: No — Kurt is clear that it's his personal project, not supported or endorsed by Tenable in any way.

**Q: What parts of the Tenable API does it cover?**
A: The Vulnerability Management API: scanners, scans and scan histories, agents and agent groups, bulk vuln and asset exports, scan exports in Nessus, CSV, or PDF format, the audit log, and asset tagging. Web-app scanning and container APIs were sketched as future "ws" and "container" commands but never built.

**Q: Why did Kurt write tiogo?**
A: Practically, to extract Tenable.io data into SIEM and SOAR systems. But it's also his Go best-practices showcase — cobra and viper wired without init functions, embedded templates, Prometheus metrics, chi middleware — and he curated a YouTube playlist explaining the design choices.

**Q: How does tiogo authenticate?**
A: With Tenable.io API keys. The first run prompts for your access key and secret key, saves them to a dot-tiogo YAML file in your home folder, and sends them in the X-ApiKeys header on every call.

**Q: How does tiogo relate to the klanker tools?**
A: It's standalone — no shared code — but it's the earliest public ancestor of Kurt's CLI style: a tiny two-letter binary, cobra command tree, and template-driven output, the same pattern the km and kv CLIs follow.

## Landmines / do-not-say

- **API keys**: The README shows a 64-hex-character AccessKey/SecretKey pair in its onboarding walkthrough — these are **obviously fabricated examples** (repeating patterns), not real credentials. KPH should never recite any key material aloud, real or example; just describe the auth flow.
- **User config**: Real credentials live in `~/.tiogo.v1.yaml` on a user's machine — not in the repo. Never speculate about or reference the contents of anyone's config file.
- **Cache contents**: The `.tiogo/cache/` folder holds raw Tenable.io responses — real vulnerability and asset data for whoever runs it. The repo clone contains no cache data, but KPH must never discuss any specific organization's scan results, vulnerabilities, hosts, or asset inventories.
- **Security framing**: tiogo touches vulnerability data by design. KPH should describe capabilities, never give the impression it can access, or has accessed, anyone's Tenable.io tenant.
- **Do not call it a Tenable product** — the disclaimer matters to Kurt.
- No secrets, tokens, or customer data were found in the repository itself; the one hex-string grep hit is the README's dummy example.

## DIGEST COMPLETE — tiogo

Word count: ~1,590 words.
