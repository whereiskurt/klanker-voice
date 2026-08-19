---
phase: quick-260818-opm
plan: 01
status: complete
subsystem: docs
tags: [operator-docs, kv-cli, telephony, runbooks, svg-capture, wiki]

requires:
  - phase: quick-260806-cm9
    provides: "`kv telephony calls` — the last of the 37 commands the reference documents"
provides:
  - "docs/operators/ — an eight-page operator manual (index + 7 pages) covering the kv CLI, phone numbers, access codes, phone games, PBX lifecycle, infrastructure and incidents"
  - "scripts/render-terminal-svg.py — .session transcript -> dark terminal-window SVG, so CLI captures render on GitHub and the wiki, diff as text, and regenerate after a CLI change"
  - "scripts/verify-operator-docs.sh — mechanical verification of the manual's claims (links, images, flags, defaults, config values, secret/PII leakage)"
  - "docs/operators/.public-numbers — reviewable allowlist of phone numbers the manual may print verbatim"
  - "sync-wiki.py image rewriting to raw.githubusercontent.com (bug fix — images were resolving to blob URLs)"
affects: [docs, wiki, operator-tooling]

tech-stack:
  added: []
  patterns:
    - "redaction at transcript level rather than in a rendered binary, so what was hidden is reviewable in git diff"
    - "default-deny PII check with a reviewable allowlist, instead of a deny-list the public script would itself have to contain"
    - "docs verification as a runnable script, so drift fails a check rather than confusing an operator"

key-files:
  created:
    - docs/operators/README.md
    - docs/operators/kv-cli-reference.md
    - docs/operators/phone-number-inventory.md
    - docs/operators/access-codes-and-tiers.md
    - docs/operators/phone-games-runbook.md
    - docs/operators/pbx-lifecycle.md
    - docs/operators/infrastructure.md
    - docs/operators/incident-runbook.md
    - docs/operators/.public-numbers
    - scripts/render-terminal-svg.py
    - scripts/verify-operator-docs.sh
    - docs/assets/terminal/ (8 .session + 8 .svg)
    - docs/assets/studio/ (6 .png)
  modified:
    - scripts/sync-wiki.py
    - docs/wiki/_Sidebar.md
    - docs/wiki/Home.md
    - README.md
---

# Summary

An operator manual for `kv` and the infrastructure it drives, written because
the person who built this system could no longer answer, from memory, what
phone numbers he owned or what access codes existed.

## What was actually wrong

Mostly not the tooling. `kv telephony list` already merges VoIP.ms, DynamoDB,
SSM and the repo config into one report — it *is* the phone-number inventory,
and the operator did not know. `kv code list` already lists every access code.
The failure was that neither was findable, and nothing said which of the nine
places a phone number can appear is authoritative.

So the manual leads with the single command that answers each question, and only
then explains the machinery.

## Delivered

**Eight pages, ~13,000 words** under `docs/operators/`, plus the three existing
operator docs folded into the index:

| Page | Answers |
|---|---|
| `README.md` | start here; by-task paths; conventions |
| `kv-cli-reference.md` | all 37 commands, real flags and defaults, backing store, credentials |
| `phone-number-inventory.md` | the nine places a number hides; which is authoritative; reconciliation |
| `access-codes-and-tiers.md` | the four ways in; what each grants; audit and cleanup |
| `phone-games-runbook.md` | the five places one CTF game lives; add/change/retire; the traps |
| `pbx-lifecycle.md` | provision *and* deprovision, in dependency order |
| `infrastructure.md` | the live AWS inventory and a five-step health ladder |
| `incident-runbook.md` | the brake, the ceilings, a diagnostic tree per symptom |

**14 captures**, all from the live `klanker-application` account: 8 terminal
SVGs rendered from checked-in transcripts, and 6 PNG screenshots of `kv studio`
driven through Chrome headless against the operator's real configuration.

## Notable findings, recorded in the manual

- **The two-table trap.** `--table` (`kmv-auth-electro`) and `--usage-table`
  (`kmv-voice-usage`) select different tables; the wrong one returns empty
  rather than erroring.
- **`kv code list` hides bypass and phone state** — not on the record it reads,
  not even with `--json`. Three commands are needed to see a code's full
  configuration.
- **`maxRedemptions` is deliberately unenforced** on the bypass `/join` and
  caller-ID mint paths (verified in `access-code.ts`). A capped code still
  admits unlimited people through its bypass link.
- **A missing `pstn-public-tier` row is a silent site-wide phone outage** —
  referenced by name from `telephony.toml`, and an absent tier is no-access.
- **The announcement registry is keyed by resolved code VALUE, not DID**, so two
  games sharing a code value means the later one silently wins for both lines.
- **Four gaps named honestly:** no `kv voipms list-dids`, no code/tier delete,
  game codes unreadable through `kv`, and `code list`'s hidden attributes.

## Bugs found and fixed while writing

1. **`sync-wiki.py` broke every image on the wiki.** Its link rewriter treated
   `![alt](path)` as an ordinary link and rewrote it to a `github.com/blob` URL,
   which serves an HTML viewer rather than bytes. Images now resolve to
   `raw.githubusercontent.com`. This would have made every capture in this
   manual a broken image on the public wiki.

2. **The runbooks contained a real orderable phone number.** `7755021688` came
   from the live stock search and was used as the example DID in three
   provisioning blocks — copy-pasting them would have spent money on a random
   Reno number. Replaced with `<NEW-DID>` placeholders; the search *capture*
   keeps the real rows, since that is what the command printed.

3. **The first draft of the verifier leaked what it was protecting.** It
   hardcoded the access-code values and caller numbers it checked for, in a
   script destined for a public repo. Rewritten to derive its deny-list: a
   structural rule (every 10-digit number must be a public DID or a 555
   documentation number) plus a live check against `kv code list --json`.

## Verification

`scripts/verify-operator-docs.sh` passes clean: every relative link and image
resolves, every transcript has a rendered SVG, all 43 documented flags exist in
`--help`, all 11 quoted defaults match, every gate/ceiling value matches
`telephony.toml`, every referenced repo path exists, and no secret or caller
number appears in any page that syncs public.

Negative-tested — a real caller number injected into a page is caught, while 555
documentation numbers are correctly ignored.

`kv smoke` was run against production during capture and returned `PASS` with
243 RTP packets, so the live stack is verified healthy as of this work.

## Not done

- **Not pushed, not PR'd.** The branch is `docs/pause-backup-teardown-spec` in
  the `docs` worktree, awaiting operator review.
- **The wiki has not been synced.** `scripts/sync-wiki.py --dry-run` was
  verified; the real push is deliberately left to the operator.
- **No `kv` source changes.** The four gaps are documented and recommended, not
  coded — the ask was documentation.
- **AWS-side inventory is a point-in-time capture** (2026-08-18). Task-definition
  revisions in `infrastructure.md` will drift with every deploy; the verifier
  does not check them, and says so.
