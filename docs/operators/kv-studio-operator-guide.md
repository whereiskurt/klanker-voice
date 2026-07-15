# kv studio — operator console guide

`kv studio` is a local, loopback-only web console embedded in the `kv` Go
CLI. It presents the operator's real, live voice-routing configuration —
routing rules (access codes → tiers), inbound DIDs, knowledge packs, and the
telephony gate secrets — as one unified view, lets you edit it safely, and
captures a whole configuration as a versioned, git-committed **SOP**
(standard operating procedure snapshot) that can be reviewed and deployed
with one action.

It reads and writes the exact same stores `kv code`/`kv tier`/`kv voipms`/
`kv knowledge` already do — the same DynamoDB table, the same repo config
files, the same SSM parameters — using the same `--profile`/`--region`
credential path. There is no separate service, no new AWS deploy target,
and no alternate data model: `kv studio` is a GUI over the CLI's existing
authority, not a new source of truth.

**Design reference:**
[`2026-07-15-kv-studio-operator-console-design.md`](../superpowers/specs/2026-07-15-kv-studio-operator-console-design.md)
and its clickable mockup,
[`2026-07-15-kv-studio-mockup.html`](../superpowers/specs/2026-07-15-kv-studio-mockup.html)
— the UAT reference every screen in this build is checked against. Where
this guide and the design spec disagree on what Deploy does (the spec's
early architecture diagram says "apply + push"), **this guide and the
shipped code are authoritative** — Deploy never pushes to a remote (see
"What Deploy does and does NOT do" below).

## Prerequisites

- The `kv` CLI, built or installed from this repo (`cd kv && go build -o bin/kv ./cmd/kv`,
  or `go install ./cmd/kv` to put it on your `$GOBIN`/`$PATH`).
- A checkout of this repo — `kv studio` reads and writes repo-relative
  config files (`apps/voice/knowledge/manifest.yaml`,
  `apps/voice/knowledge/router/topic-map.yaml`,
  `apps/voice/configs/telephony.toml`,
  `apps/voice/configs/studio/dids.yaml`,
  `apps/voice/configs/studio/rule-order.yaml`, and SOP snapshots under
  `apps/voice/configs/studio/sops/`) relative to the repo root it is run
  from.
- The same AWS credentials/profile you already use for `kv code`/`kv tier`
  (read/write access to the DynamoDB access-code/tier table) and, for the
  Keys & secrets tab, permission to `GetParameter`/`DescribeParameters`/
  `PutParameter` on the three allow-listed SSM SecureString params under
  `/kmv/secrets/use1/telephony/*`.
- For the DIDs tab's live VoIP.ms list and DID-routing action: the same
  VoIP.ms API credentials `kv voipms` resolves (env or SSM). If they are
  unavailable, the DID tab degrades gracefully — see "Security model" below.

## Launch

From the `kv` module directory, or against a built/installed binary:

```bash
# from kv/, without building a binary first
go run ./cmd/kv studio

# or, using a built/installed binary
kv studio
```

Flags:

| Flag | Default | Purpose |
|---|---|---|
| `--port` | `7420` | TCP port to bind on `127.0.0.1`. |
| `--no-open` | `false` | Do not automatically open the browser. |

`kv studio` binds **`127.0.0.1` only — never `0.0.0.0`, and there is no
`--host` flag to change that.** The console is a local operator tool for
the machine it runs on; it is never intended to be reachable from any other
host. On startup it prints the URL it is serving (e.g.
`kv studio serving 127.0.0.1:7420`) and opens your default browser to it
unless `--no-open` is set. Press Ctrl-C to stop the server.

## The five tabs

### 1. Rules (routing rules)

A first-match-wins table of access codes, each pointing at a tier (time
budget) and, optionally, a caller-id phone match. What it shows: every
`AccessCode` row (code, tier, phone mapping if any, gate mode) joined
against `Tier` (session/period/concurrency limits) and the shared
telephony gate config. What an operator edits: create a new rule (code +
tier id, optionally a phone number and the shared gate mode), re-point an
existing rule at a different tier id, block a code (routes it to the
zero-limit `no-access` tier without deleting it), delete a rule, and
reorder the rules table (authoring order only — first-match-wins is
resolved server-side; reordering the table does not itself change routing
priority beyond documentation/legibility).

**Not editable for an existing rule in this build:** the phone number on
an *existing* rule (WHO) has no edit endpoint — only new rules take a
phone at creation time — and a rule's session/period/concurrency minutes
are read-only (joined from the Tier row); only the tier id a rule points
at is writable. See "Known limitations" below.

### 2. DIDs

Your owned inbound phone numbers. What it shows: a merged view of the live
VoIP.ms inbound DID list (when VoIP.ms credentials are available) plus
local metadata (label, region, default rule, greeting text) from
`apps/voice/configs/studio/dids.yaml`. What an operator edits: search/add
an already-owned DID (this routes an existing number to the PBX
subaccount via the same primitive `kv voipms route-did` uses — it never
provisions a *new* number), and edit an existing DID's label, region,
default rule, and opening greeting text.

### 3. Knowledge

The topic-pack library the concierge's retrieval router draws from. What
it shows: every pack in `apps/voice/knowledge/manifest.yaml` — its kind,
its source list, a cheap token-size estimate, and how many rules
reference it. What an operator edits: append a new source (repo path or
URL) to an existing pack's manifest entry, and trigger a rebuild — the
exact same subprocess `kv knowledge refresh` shells
(`uv run python scripts/refresh_knowledge.py` from `apps/voice/`). A
rebuild report shows the changed files and a `git diff --stat` summary
for human review.

### 4. Keys & secrets

The telephony access-gate secrets, by reference. What it shows: the three
allow-listed SSM SecureString parameter names under
`/kmv/secrets/use1/telephony/*` (the DTMF access PIN, the spoken-passphrase
word set, and the telephony endpoint auth token) plus a static, read-only
list of the three provider API keys (ElevenLabs, Deepgram, Anthropic) —
SOPS-managed and never reachable from this console at all. What an
operator edits: reveal a value (fetches it from SSM on click, shows it in
the browser, never persists it — hide it or re-render the page and it is
gone), and rotate a value (writes a new SecureString, preserving the
existing KMS key id if one was set).

### 5. Save & deploy

The SOP snapshot/review/apply workflow — see the next section.

## The SOP flow: Save-as-SOP → changeset → Deploy

**Save-as-SOP.** Assembles the current live configuration (every rule,
tier, DID, knowledge pack, and gate/order setting — secret *references*
only, never a secret value) into a single named snapshot,
`apps/voice/configs/studio/sops/<name>.yaml`, and commits **only that one
file** to the local git worktree with a strictly-scoped `git add --
<path>` (never `git add -A`/`.`/`-a` — an unrelated dirty file elsewhere in
your working tree is never swept into the commit).

**Changeset.** Re-reads a named SOP and diffs it against a freshly
assembled live view, per surface (rule / tier / DID / unlock / knowledge /
gate / order), reporting what would be added, changed, or removed. This is
always computed fresh on every request — never cached — so it always
reflects any edit made through the tabs above since the SOP was last
saved.

**Deploy.** Re-reads the named SOP, **validates it first** (refusing the
entire action on any validation error — no apply, no commit, no rebuild
is attempted if validation fails), computes the same changeset, then
applies it in this fixed order: idempotent DynamoDB writes (rule/tier
rows), the gate and rule-order repo-file surfaces (whichever changed),
a single scoped local git commit of every config file that actually
changed, and finally — only if a knowledge pack's source list changed —
the same knowledge-rebuild trigger the Knowledge tab uses. A partial
failure reports exactly which step failed and leaves everything before it
already applied; because every step is idempotent by changeset, re-running
Deploy after fixing the failure is always safe.

### What Deploy does and does NOT do

**Deploy DOES:**
- Apply DynamoDB writes idempotently (create-if-missing / update-in-place
  for rules and tiers).
- Write the gate (`telephony.toml`) and rule-order
  (`apps/voice/configs/studio/rule-order.yaml`) files when the SOP changes
  them.
- Commit the changed config files **locally**, scoped to the exact paths
  that changed (never a directory glob, never `git add -A`).
- Refresh knowledge — trigger the same subprocess the Knowledge tab's
  rebuild button uses — **only if** the SOP's changeset shows a new pack
  or a changed source list for an existing pack.

**Deploy does NOT:**
- Push to any git remote, or open a PR. Every commit Deploy or Save-as-SOP
  makes lands on your current local branch only — pushing/PRing is a
  separate, explicit human step you take afterward.
- Auto-commit regenerated knowledge packs. The rebuild step only *runs*
  the refresh subprocess and reports a read-only `git diff --stat` — it
  never runs `git add`/`git commit` on the generated pack output. A human
  must review that diff and commit it themselves (see "Security model"
  below, D-09).
- Delete any live data absent from the SOP. Apply is additive and
  update-only — a rule/tier/DID present live but missing from the SOP is
  left untouched, never removed. There is no delete-on-deploy behavior
  anywhere in this flow.

## Security model

- **127.0.0.1 only.** `kv studio` binds exclusively to loopback — there is
  no `--host` flag, so it is structurally impossible to steer the listener
  onto a non-loopback interface. It is never intended to be exposed beyond
  the operator's own machine.
- **Secret reveal is allow-listed and ephemeral.** Only the three telephony
  gate secrets (`/kmv/secrets/use1/telephony/access_pin`,
  `/kmv/secrets/use1/telephony/passphrase_words`,
  `/kmv/secrets/use1/telephony/endpoint_auth_token`) can ever be revealed
  or rotated through this console — a request naming any other SSM
  parameter (including the auth app's JWT/OIDC/ALTCHA signing secrets,
  which live under the same `/kmv/secrets/use1/*` prefix) is rejected
  before any AWS call is made. A revealed value is returned once in the
  HTTP response and shown in a transient DOM node in the browser — it is
  never written to disk, a log line, or any server-side cache, and it
  disappears when you hide it or reload the page.
- **No secret value ever enters a SOP.** SOP snapshots store only secret
  *references* (parameter names / gate mode), the same discipline the
  voice pipeline's own config loader already enforces for `.toml` files.
- **The three provider API keys are SOPS-managed and display-only.** The
  ElevenLabs, Deepgram, and Anthropic keys never appear in `ConfigView` at
  all and have no reveal/rotate action anywhere in this console — they are
  rotated the same way they always have been, outside `kv studio`.
- **Knowledge rebuild never auto-commits (D-09).** The rebuild trigger runs
  the refresh subprocess and surfaces a read-only `git status`/`git diff
  --stat` summary; it never stages or commits the regenerated pack files.
  A human must review the diff and commit it deliberately.
- **Deploy does not push to a remote and never deletes live data absent
  from the SOP** — see "What Deploy does and does NOT do" above.
- **SOP names are path-traversal-guarded.** A SOP name arriving over the
  HTTP API is rejected if it contains a path separator or is `.`/`..`,
  before it is ever joined into a filename under
  `apps/voice/configs/studio/sops/`.

## Known limitations / deferred

- **WHO (phone) editing for an existing rule is not wired.** A phone
  number can only be set when a rule is first created; there is no
  console action to change or clear the phone mapping on an existing
  rule. The underlying primitive (`SetPhoneMapping`) exists and is used
  by rule creation and by SOP apply — it is simply not exposed as an edit
  action in this build. **Workaround:** delete the rule and recreate it
  with the corrected phone number.
- **GRANT tier-limits editing is not wired.** A rule's session/period/
  concurrency minutes are read-only in the console (joined from the Tier
  row it points at); the only writable GRANT field is which tier id a
  rule points at. The underlying primitive (`UpdateTierLimits`) exists and
  is used by SOP apply — it is not exposed as a direct edit action in this
  build. **Workaround:** reassign the rule to a different, already-defined
  tier (`kv tier list` to see what exists), or use `kv tier` directly to
  adjust a tier's limits outside the console.
- **UI visual UAT against the mockup is still pending.** The design
  mockup (`2026-07-15-kv-studio-mockup.html`) is the intended visual/UX
  target; a full manual pass confirming the shipped console matches it
  screen-for-screen has not yet been completed.
- **Provider-key rotation stays SOPS-managed, not a console action** — see
  "Security model" above. This is a deliberate scope boundary, not a gap:
  the design spec explicitly keeps those three keys out of the console's
  reveal/rotate surface.
- **Per-route knowledge scope and persona binding are out of scope for
  this build (v1).** Every rule reaches every knowledge pack, and there is
  a single fixed persona; the Knowledge/Persona fields shown in a rule's
  detail are read-only. Binding a specific knowledge scope or persona to
  an individual route, enforced in the live voice runtime, is deferred to
  a future milestone (see the design spec §6, "Out (v2+)").

---

*Phase: 19-hardening-adapter-test-suite (Plan 19-02, SC-19-4)*
