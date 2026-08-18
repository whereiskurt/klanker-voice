---
phase: quick-260818-opm
plan: 01
type: execute
wave: 1
depends_on: []
autonomous: true
requirements: [QUICK-260818-opm]
files_modified:
  - scripts/render-terminal-svg.py
  - scripts/sync-wiki.py
  - docs/operators/README.md
  - docs/operators/kv-cli-reference.md
  - docs/operators/phone-number-inventory.md
  - docs/operators/access-codes-and-tiers.md
  - docs/operators/phone-games-runbook.md
  - docs/operators/pbx-lifecycle.md
  - docs/operators/infrastructure.md
  - docs/operators/incident-runbook.md
  - docs/assets/terminal/*.session
  - docs/assets/terminal/*.svg
  - docs/assets/studio/*.png
  - docs/wiki/_Sidebar.md
  - docs/wiki/Home.md
  - README.md

must_haves:
  truths:
    - "An operator who has forgotten everything can answer 'what numbers do I own', 'what codes exist and what do they grant', 'how do I add/retire a phone game', 'how do I stand up or tear down the PBX', and 'what runs this in AWS' — each from a single named page, each with a real captured example."
    - "Every one of the 37 kv commands appears in the reference with its real flags, real defaults, what store it reads/writes, and what credentials it needs."
    - "Every terminal capture is generated from a checked-in .session transcript by a checked-in renderer, so it can be regenerated after a CLI change."
    - "No secret value appears anywhere in docs/ — access-code values, DTMF game codes, spoken passphrases, the gate PIN, bypass tokens, VoIP.ms credentials and raw caller numbers are all redacted, in SVG captures and PNG screenshots alike."
    - "Images render on the public wiki, not just in the repo — sync-wiki.py rewrites image targets to raw.githubusercontent.com rather than blob URLs."
    - "Where the tooling genuinely lacks something the operator asked for, the manual states the gap plainly instead of documenting only the happy path."
  artifacts:
    - docs/operators/kv-cli-reference.md
    - docs/operators/phone-number-inventory.md
    - docs/operators/pbx-lifecycle.md
    - docs/operators/infrastructure.md
    - scripts/render-terminal-svg.py
  key_links:
    - "`kv telephony list` merges four sources (VoIP.ms getDIDsInfo, DynamoDB phone mappings, SSM gate secrets, telephony.toml gate config + announcement blocks) — it is the single answer to the operator's 'list all the phone numbers' question and the spine of the inventory page."
    - "`--table` (kmv-auth-electro) vs `--usage-table` (kmv-voice-usage) is a silent-failure trap: code/tier/telephony/studio read the former, usage/killswitch the latter."
    - "The announcement registry is keyed by resolved code VALUE, not DID (configs/telephony.toml, the distinct-code-value constraint) — the single most dangerous fact about adding a phone game."
    - "setDIDInfo is FULL-REPLACE and cnam=1 silently clobbers callerid_prefix; `kv voipms set-cid-prefix` bakes in the snapshot-preserve + forced cnam=0 + readback that makes prefix enrolment safe."
    - "scripts/sync-wiki.py's PAGE_MAP must gain the new operator pages, and its link rewriter must special-case images, or every capture 404s on the wiki."
---

<objective>
Write a complete operator manual for `kv` and the infrastructure it drives, at
`docs/operators/`, built from live captures against the real account.

Purpose: the operator built this system, ran a CTF on it, and can no longer
recall how to enumerate his own phone numbers or access codes. The manual has to
work as a cold-start reference, not a tour.

Output: an operator-manual index plus seven pages, a terminal-SVG capture
pipeline, real `kv studio` screenshots, and wiki/README wiring.
</objective>

<tasks>

## Task 1 — capture pipeline

Write `scripts/render-terminal-svg.py`: reads a `.session` transcript
(`$ command` lines plus output), emits a dark terminal-window SVG under
`docs/assets/terminal/`. Deterministic, no external deps, re-runnable over a
directory.

Commit: `docs(operators): terminal-SVG capture renderer`

## Task 2 — captures

Write the `.session` transcripts from the live output captured during research,
with secrets and caller numbers redacted at transcript level (so the redaction
is reviewable in git, not baked into a binary). Render them. Copy the redacted
`kv studio` PNGs into `docs/assets/studio/`.

Commit: `docs(operators): live kv captures + studio screenshots`

## Task 3 — kv CLI reference

`docs/operators/kv-cli-reference.md`: global flags and the two-table trap,
credential resolution, then all 37 commands grouped by task, each with
synopsis, flags/defaults, backing store, required permissions, and a capture
where one exists. Mark destructive commands explicitly.

Commit: `docs(operators): kv CLI reference`

## Task 4 — the two "I lost track" pages

`phone-number-inventory.md` — every place a number can hide, which is
authoritative, and the reconciliation procedure.
`access-codes-and-tiers.md` — codes, tiers, bypass links, caller-ID mint
mappings, the gate secrets, and which command shows which.

Commit: `docs(operators): phone-number + access-code inventory runbooks`

## Task 5 — lifecycle runbooks

`phone-games-runbook.md` — the end-to-end wiring of a CTF phone game and every
trap already learned the hard way.
`pbx-lifecycle.md` — provision and deprovision, both directions, including what
must not be deleted.

Commit: `docs(operators): phone-game + PBX lifecycle runbooks`

## Task 6 — infrastructure and incidents

`infrastructure.md` — the live AWS inventory, terragrunt unit map, deploy path,
and how to verify each layer.
`incident-runbook.md` — kill-switch, cost control, and a diagnostic decision
tree for the failures that have actually happened.

Commit: `docs(operators): infrastructure + incident runbooks`

## Task 7 — index and wiring

`docs/operators/README.md` index, wiki sidebar/Home entries, README link, and
the `sync-wiki.py` image-rewrite fix.

Commit: `docs(operators): manual index + wiki/README wiring`

## Task 8 — verify

Check every flag and default against `--help`, every path exists, every SSM
path and table name matches source, and run a secret scan over the new docs.

Commit: `docs(operators): verification pass`

</tasks>
