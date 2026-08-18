# Context — quick 260818-opm: kv operator manual

## The ask (operator, verbatim intent)

> "I've kinda lost track of how to list out all the phone numbers. Show all the
> passcodes that I let you in. I wanna be able to provision and deprovision the
> PBX, and there's a ton of infrastructure that stands up kv. I need a manual
> with some screenshots. Think about it like me — someone who just ran a CTF and
> set up all these phone numbers and different challenges to them and the back
> ends that connect it."

The operator is the author of this system and still could not answer, from
memory, four questions it should answer instantly:

1. What phone numbers do I own, and which is authoritative?
2. What access codes exist and what do they grant?
3. How do I stand a PBX up — and tear one down?
4. What actually runs this thing in AWS?

That is a **documentation** failure, not a tooling failure: `kv` already answers
(1) and (2) in one command each. The manual's first job is to make the existing
answers findable.

## Decisions (answered by the operator upfront)

| # | Question | Decision |
|---|---|---|
| D-01 | Live AWS output, or reconstructed from source? | **Live.** Operator ran `aws sso login`; every example is captured from the real `klanker-application` account (052251888500 / us-east-1). |
| D-02 | Public wiki-safe, or operator-private? | **Public-safe.** DIDs and SSM parameter *names* appear (already public in `configs/telephony.toml`); access-code values, DTMF game codes, passphrases, gate PIN, bypass tokens, VoIP.ms credentials and raw caller numbers are redacted to placeholders. |
| D-03 | Screenshot format? | **Terminal-styled SVG** for CLI captures (renders inline on GitHub *and* the wiki, diffable as text, regenerable) + **real PNG screenshots** of the `kv studio` web console via Chrome headless. |
| D-04 | Scope? | **Full operator manual** — kv CLI reference, plus task runbooks (number inventory, codes, CTF games, PBX lifecycle, infrastructure, incidents). |

## Derived constraints

- **D-05 (no secret leakage).** `docs/` is mirrored to a public wiki by
  `scripts/sync-wiki.py`. No captured artifact may carry a secret value. Access
  codes are redacted even though they are low-sensitivity, because they are the
  literal credential a stranger would type. Caller numbers are PII and are
  redacted in both SVG captures and PNG screenshots.
- **D-06 (regenerable, not hand-drawn).** Every SVG capture is generated from a
  checked-in `.session` transcript by a checked-in renderer, so a future
  operator can re-capture after a CLI change instead of hand-editing XML.
- **D-07 (truth over tidiness).** Where the tooling has a real gap — a
  command that does not exist, an answer split across three stores, a
  credential path that fails from a laptop — the manual says so plainly rather
  than documenting the happy path only.

## Facts established during research (live, 2026-08-18)

- 37 commands across 9 top-level groups: `code`, `tier`, `usage`, `killswitch`,
  `knowledge`, `smoke`, `studio`, `telephony`, `voipms`.
- **`kv telephony list` is the answer to "list all the phone numbers"** — it
  already merges VoIP.ms `getDIDsInfo`, the caller-ID mint mappings, gate
  secrets status, gate config, and the phone-games table into one report. The
  operator did not know this.
- **`kv code list` is the answer to "show all the passcodes"** — but it does
  NOT surface bypass `/join` links or caller-ID phone mappings (they are not on
  `AccessCodeRecord`). Those live in `kv telephony list` and `kv studio`.
- Two DynamoDB tables, addressed by two *different* global flags:
  `--table` (`kmv-auth-electro`: codes, tiers) and `--usage-table`
  (`kmv-voice-usage`: usage, kill-switch). Getting this wrong is silent.
- Three ECS services on one cluster `app-use1-kmv`: `voice-use1`,
  `telephony-edge-use1`, `auth-use1` — all Fargate, `desired=1`.
- 5 live DIDs; the 613 number in older notes is gone (cancelled for the
  toll-free slot).
- 5 phone games across 4 DIDs, keyed by **resolved code VALUE**, not by DID —
  so two games can share one line, and two games must never share a code.

## Non-goals

- No changes to `kv` source. Gaps are documented and recommended, not coded.
- No changes to live configuration. All AWS/VoIP.ms calls made during research
  were read-only.
