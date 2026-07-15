# kv studio — operator console design

**Status:** Approved for planning (2026-07-15)
**Author:** Kurt + Claude (brainstormed)
**Visual reference:** [`2026-07-15-kv-studio-mockup.html`](./2026-07-15-kv-studio-mockup.html) — clickable, fake-data prototype of every screen described here.

---

## 1. Problem

A single operator idea — *"a call from **this number** using **this passphrase** gets **this much time** and **this knowledge**, in **this persona**"* — is today split across three disconnected surfaces, each edited a different way:

| Operator concept | Where it lives today | Edited via |
|---|---|---|
| **WHO** — inbound DID + caller-id → identity | DynamoDB `AccessCode.phone` + `/tel` mint | `kv code phone` |
| **SECRET** — spoken passphrase / DTMF PIN | SSM SecureString + `gate_mode` in `telephony.toml` | hand-edit SSM + TOML |
| **spoken words** — e.g. "greenhouse" | `knowledge/router/topic-map.yaml` keywords | hand-edit YAML |
| **TIME** — session / period / concurrency | DynamoDB `Tier` row | `kv tier define` |
| **KNOWLEDGE scope** — which packs | `knowledge/manifest.yaml` + packs (runtime router selects) | edit YAML + `make knowledge` |
| **PERSONA** | `prompts/concierge.md` | hand-edit |

There is no single place to express or review a whole route, and no way to snapshot a working configuration, diff it, or roll it back. The operator (Kurt) does this by hand across DynamoDB, SSM, and repo files.

## 2. Goal

A **local operator console** that presents these surfaces as one model — **routing rules** — edits them safely, and captures a whole configuration as a **versioned, git-committed SOP** that can be deployed with one action.

**Non-goal for v1 (deferred to v2):** binding a knowledge scope and persona *to a route/code* enforced in the live pipeline. Today an access code carries **tier only**; the router selects knowledge at runtime from keywords. v1 surfaces and edits that existing behavior faithfully. The mockup marks the future capability with **"new binding"** badges so the target is visible, but v1 does not change the running speech-to-speech runtime or the token mint.

## 3. Locked decisions

- **Runtime:** extend the existing `kv` Go CLI with a `kv studio` command that serves the console on `localhost` (default `:7420`). One binary. It reuses `kv/internal/app/electro` key templates, `aws-sdk-go-v2`, and cobra. No new service, no new AWS deploy target.
- **v1 scope:** unify and edit the surfaces that **already exist**. See §6 in/out list.
- **Interface:** a git-backed **routing-rules table** as the spine (first-match-wins order), a right-side **rule editor** drawer, plus **DID manager**, **Knowledge library**, **Keys & secrets**, and **Save & deploy** areas — exactly as in the mockup.

## 4. Architecture

```
┌──────────────────────────── operator laptop ────────────────────────────┐
│  kv studio  (single Go binary, `kv studio` subcommand)                   │
│                                                                          │
│   ┌───────────────┐     embedded (go:embed) static web console          │
│   │  local web UI │◀───────────────────────────────────────────────┐    │
│   │ (served HTML/ │     JSON over localhost REST                    │    │
│   │  JS, no CDN)  │────────────────────────────────────────────────┘    │
│   └───────────────┘                    │                                 │
│                                        ▼                                 │
│   ┌──────────────────────────────────────────────────────────────────┐  │
│   │  studio server (Go)                                               │  │
│   │   • reads/writes DynamoDB via kv/internal/app/electro key tmpls   │──┼──▶ DynamoDB (codes, tiers, phone maps)
│   │   • reads/writes repo YAML  (manifest.yaml, topic-map.yaml)       │──┼──▶ local git worktree
│   │   • reads/writes telephony.toml gate knobs                        │──┼──▶ local git worktree
│   │   • reads SSM param refs (names only; values via reveal/rotate)   │──┼──▶ SSM SecureString / SOPS
│   │   • SOP snapshot → git commit;  Deploy → apply + push             │  │
│   └──────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────────┘
```

The console is a thin GUI; **all authority lives in the Go server**, which already exists in part (kv's code CRUD). The web UI never touches AWS or git directly — it calls the local server.

### Components (each independently testable)

1. **`kv studio` command** (`kv/internal/app/cmd/studio.go`) — starts the HTTP server, opens the browser, handles `--port`, `--no-open`, `--profile`/`--region` (mirrors existing kv global flags).
2. **Embedded web console** (`kv/internal/app/studio/web/`) — static assets via `go:embed`; self-contained (no external CDN, matching the mockup). The mockup HTML is the design target for this UI.
3. **Studio server / router** (`kv/internal/app/studio/server.go`) — REST endpoints (§5), JSON in/out, maps to the four data adapters.
4. **DynamoDB adapter** — reuses `electro/keys.go` templates for codes, tiers, phone (byPhone GSI). Read = list/scan; write = PutItem with byte-identical keys (as `kv code`/`kv tier` already do).
5. **Repo-file adapter** — typed read/write of `knowledge/manifest.yaml`, `knowledge/router/topic-map.yaml`, and the `[telephony]` gate block of the selected pipeline TOML. Preserves comments/ordering where practical.
6. **Secret adapter** — lists SSM param **names** + SOPS refs; `reveal` fetches a value on demand (never cached to disk, never written to a SOP); `rotate` writes a new SecureString value.
7. **SOP engine** — serializes the current unified view to a single `sops/<name>.yaml` snapshot in the repo, computes the pending changeset (diff vs live), commits ("Save as SOP"), and applies + pushes ("Deploy"). Deploy sequence mirrors the mockup: validate → write DynamoDB → commit YAML/TOML → refresh knowledge (only if a pack source changed).

## 5. Data flow & the unified "Rule" model

A **Rule** is a view object the server assembles from the three stores; it is not a new table. Fields (v1):

- `who`: `{ type: known|any|block, numbers: []e164 }` → DynamoDB `AccessCode.phone` / byPhone GSI (+ a blocklist entry for `block`).
- `secret`: `{ mode: passphrase|dtmf|none, ref }` → SSM param name + `telephony.toml` `gate_mode`. Value is a **reference**, resolved through the Secret adapter.
- `unlocks`: `[{ phrase, add: []packId }]` → `topic-map.yaml` keyword entries.
- `grant`: `{ minutes, periodMin, concurrency, tierId }` → DynamoDB `Tier` row.
- `knowledge`: `[]packId` → **v1: read-only display** of the manifest packs the router can reach; editing the allow-list per route is v2. (The editor shows the chips; v1 persists the manifest/tour, not a per-code binding.)
- `persona`: display-only in v1 (single concierge persona).

**DIDs** are managed separately (they are the *owned inbound* numbers, distinct from a caller-id match): list/search/add/edit, each with region, status, and a per-DID **default rule + opening greeting**.

## 6. Scope

**In (v1):**
- Routing-rules table over live DynamoDB (list, add, edit, reorder-precedence, delete; block a number).
- Rule editor drawer: WHO / SECRET / GRANT / (read-only KNOWLEDGE + PERSONA) with a live "compiles-to" panel naming each backing store.
- DID manager: search / add / edit inbound DIDs incl. per-DID default rule + greeting.
- Knowledge library: view packs (kind, sources, tokens, "used by N rules", talkable/hidden), add a source, trigger rebuild (`refresh_knowledge.py`).
- Keys & secrets: list refs, **reveal/hide**, rotate (SSM SecureString / SOPS).
- Save as SOP (git commit) + Deploy (apply DynamoDB + push YAML/TOML), with a pre-deploy changeset diff.
- `import from live` to seed the console from current DynamoDB + repo state.

**Out (v2+):**
- Per-route/per-code **knowledge scope binding** enforced in the voice runtime.
- Per-route **persona** selection enforced in the runtime.
- Any change to the token mint, gate runtime, or speech pipeline.
- Multi-operator/RBAC; remote hosting (studio is local-only by design).

## 7. Security

- **Local-only:** binds to `127.0.0.1`; no external listener. Uses the operator's own AWS credentials/profile (same as `kv`).
- **Secrets never enter a SOP or git.** SOP snapshots store only param **names/refs**. The `config.py` credential-field regex that already refuses secrets in TOML is the model.
- **Reveal is deliberate and ephemeral** — fetched on click, shown in the browser, never persisted by the server.
- **Deploy is gated** by an explicit changeset review; destructive rule changes (e.g. block) are shown before apply.

## 8. Error handling

- AWS/credential failure → the console shows a clear banner ("can't reach DynamoDB in <region> with profile <p>"), never a blank screen; the mockup's copy conventions apply.
- Git dirty / conflict on Save-as-SOP → surface the conflict, refuse to auto-force.
- YAML/TOML write validated (schema + parse round-trip) before commit; refuse to write malformed config.
- Deploy is transactional per-surface where possible; partial failure reports which surface succeeded and leaves a resumable changeset.

## 9. Testing

- **Go unit tests** for each adapter against a local DynamoDB (or mock) and temp git repo fixtures — the SOP round-trip (assemble → snapshot → diff → apply) is the highest-value test.
- **Golden-file tests** for YAML/TOML writers (comment/order preservation).
- **Server endpoint tests** (table-driven) for the REST surface.
- **Manual UAT** against the mockup: every screen and interaction in the prototype has a real counterpart.

## 10. Milestones (for GSD planning)

1. `kv studio` command + embedded static server + `import from live` (read-only console showing real DynamoDB/YAML).
2. Rule editor writes (DynamoDB codes/tiers/phone) + DID manager.
3. Knowledge library (YAML read/write + rebuild trigger) + Keys reveal/rotate.
4. SOP engine: snapshot, changeset diff, Save (commit) + Deploy (apply/push).
5. Hardening: error banners, validation, tests, docs.

v2 (separate milestone): knowledge/persona **bound per route**, enforced in the runtime.
