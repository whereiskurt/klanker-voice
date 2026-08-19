# Phone number inventory

*"How do I list out all the phone numbers?"*

## The short answer

```bash
kv telephony list
```

![kv telephony list](../assets/terminal/telephony-list.svg)

That one command is the whole inventory. It reaches into VoIP.ms, DynamoDB, SSM
and the repo config in a single pass and prints what each one thinks is true.
Nothing else needs to be consulted for a routine "what do I own" check.

If you want to click instead of read, `kv studio` renders the same data, and the
DID manager (`http://127.0.0.1:7420/#dids`) is the nicest view of it:

![kv studio inbound DIDs](../assets/studio/studio-dids.png)

---

## The live inventory

As captured on 2026-08-18. All five numbers route to the shared
`557010_klanker-pbx` subaccount on POP 45 (`toronto1.voip.ms`).

| Number | Dial as | Where | Tag | Role |
|---|---|---|---|---|
| `3474803715` | +1 347-480-3715 | New York, NY | *(none)* | plain concierge line — no game, no prefix tag |
| `7254043234` | +1 725-404-3234 | Las Vegas, NV | `KVD3234` | game 1 — DTMF-only OTP |
| `7254043283` | +1 725-404-3283 | Las Vegas, NV | `KVD3283` | game 2 — DTMF-only OTP |
| `7254048283` | +1 725-404-8283 | Las Vegas, NV | `KVD8283` | game 3 — DTMF **or** spoken trigger |
| `8559164636` | +1 855-916-INFO | Toll-free US/CAN | `KVD1800` | games 4 **and** 5 — OTP, plus the RICK playback on a second code |

Two things this table implies that are easy to forget:

- **One line can host more than one game.** `8559164636` runs two, because the
  announcement registry is keyed by the *code value* a caller enters, not by the
  number they dialled.
- **An untagged number is not a broken number.** `3474803715` has no caller-ID
  prefix, which means the edge cannot tell which DID was dialled — so it falls
  through to the plain concierge. That is its job.

The VoIP.ms account has a **5-DID cap**. Every number above occupies a slot; a
sixth requires cancelling one of these first. That constraint is why the old
613 number no longer exists — it was released to make room for the toll-free
line.

---

## Everywhere a number can hide

`kv telephony list` covers the routine case. When something is behaving
strangely — a number that rings nowhere, a game that fires on the wrong line —
the reason is almost always that these places disagree. Here is all of them.

| # | Place | What it holds | Authoritative for |
|---|---|---|---|
| 1 | **VoIP.ms account** (`getDIDsInfo`) | the numbers you actually own and pay for | **ownership and routing — the source of truth** |
| 2 | `configs/telephony.toml` → `[telephony.cid_prefix_dids]` | tag → DID map | which tag the edge resolves to which number |
| 3 | `configs/telephony.toml` → `otp_only_dids` | DIDs where the concierge PIN/passphrase is suppressed | game-only lines |
| 4 | `configs/telephony.toml` → `[[telephony.announcement]].dids` | which line each game answers on | game scoping |
| 5 | `configs/telephony.toml` → `sms_reply_dids` / `sms_dids` | which number a mid-call SMS is sent *from* | outbound SMS identity |
| 6 | **DynamoDB** `kmv-auth-electro` (sparse `gsi3`) | *caller* numbers mapped to access codes | caller-ID auto-identification |
| 7 | **SSM** `/kmv/secrets/use1/voipms/did` | one DID, from original provisioning | nothing current — see below |
| 8 | `configs/studio/dids.yaml` | per-DID label / default rule / greeting | metadata only, never provisioning |
| 9 | **CloudWatch** telephony-edge logs | numbers that were actually dialled | what really happened |

### Two of these are traps

**#6 is a different kind of number entirely.** The caller-ID mint mappings are
keyed by the number of the person *calling in*, not by a number you own. A row
there means "when this person phones, recognise them and give them this tier."
It looks like a DID in a listing and is not one.

**#7 is stale by design.** `/kmv/secrets/use1/voipms/did` was written during the
original single-DID provisioning and has not tracked reality since the second
number was added. Do not treat it as inventory. It survives only because
`docs/operators/voipms-provisioning-runbook.md` still describes the blank-account
path that creates it.

**#8 creates nothing.** Adding a row to `dids.yaml` does not purchase, route or
enable a number — it attaches a label and a default rule to a DID that already
exists at VoIP.ms. The file header says so in as many words; it is worth
believing, because the file looks exactly like a provisioning list.

---

## Reconciling

Run this when a number misbehaves, before an event, and after any provisioning
change. It takes about a minute.

### 1. What do I own?

```bash
kv telephony list
```

The *Inbound DIDs* section is ground truth. If a number you expect is missing,
it is not on the account — it was cancelled, or the order never completed.

If that section says `not configured` or `error — VoIP.ms rejected the call:
ip_not_enabled`, you have a credential or allow-list problem, **not** an
inventory problem. The rest of the report is still valid. See
[the CLI reference](kv-cli-reference.md#voipms).

### 2. Does the config agree?

Every DID in `[telephony.cid_prefix_dids]` must exist in step 1's list, and every
tagged DID must carry that exact tag at VoIP.ms. Check the carrier side per
number:

```bash
kv voipms set-cid-prefix 7254043234 KVD3234   # idempotent: re-asserts + verifies
```

Re-running `set-cid-prefix` with the tag it should already have is a safe way to
confirm it: the command reads the DID back after writing and fails loudly if the
prefix did not land or if routing changed.

> **The `cnam` trap.** If a DID has CNAM lookup enabled, VoIP.ms overwrites the
> caller-ID name and your prefix never arrives — per-DID resolution silently
> stops working and every call on that line falls through to the untagged
> concierge. `kv voipms set-cid-prefix` forces `cnam=0` for exactly this reason.
> If you ever set a prefix through the web portal instead, check CNAM yourself.

### 3. Do calls arrive where you think?

```bash
kv telephony calls --since 168h --view numbers
```

![kv telephony calls, numbers view](../assets/terminal/telephony-calls.svg)

This is the reality check the config cannot give you. If a tagged line shows all
its traffic under `concierge (untagged DIDs)`, its prefix is not landing —
go back to step 2.

The two "unknown" buckets are deliberately kept apart:

- `unknown (pre-resolution)` — calls from before per-DID resolution existed. The
  dialled number is genuinely unrecoverable for these.
- `concierge (untagged DIDs)` — modern calls on a DID with no prefix tag. For
  `3474803715` this is correct and expected.

### 4. Is anyone being auto-identified?

The *Caller-ID mint mappings* section of step 1. Each row is a person who skips
the gate entirely. Audit it after an event; a stale mapping is a standing
free pass.

```bash
kv code phone <code> --remove
```

---

## Adding a number

Summarised here; the full procedure with the carrier-side prerequisites is in
[PBX lifecycle](pbx-lifecycle.md), and the game wiring is in
[phone games](phone-games-runbook.md).

```bash
kv voipms balance                                    # can you afford it?
kv voipms search-dids --state NV                     # pick a rate center
kv voipms search-dids --state NV --ratecenter RENO   # pick a number
kv voipms order-did 7755021688                       # spends money
kv voipms set-cid-prefix 7755021688 KVDRENO          # only if it needs a tag
kv telephony list                                    # confirm it appears
```

Then, if it is a game line, add the `[telephony.cid_prefix_dids]` entry and the
`[[telephony.announcement]]` block, seed its SSM parameters, wire `service.hcl`,
and deploy — in that order. Seeding before wiring is not a style preference:
**ECS refuses to launch a task whose `valueFrom` parameter does not exist**, so
wiring first takes the telephony edge down.

## Removing a number

```bash
kv telephony calls --since 720h --did 7755021688     # is anyone still using it?
# remove its cid_prefix_dids entry, its announcement block, and its
# service.hcl secrets rows; deploy; confirm the line is inert
kv voipms cancel-did 7755021688 --yes                # IRREVERSIBLE
```

Cancel last. The number is gone permanently the moment that command succeeds —
you cannot reclaim it, and anyone can take it. Everything else in the list is
reversible; this is not.

---

## Related

- [`kv` CLI reference](kv-cli-reference.md) — every flag on every command above
- [Access codes & tiers](access-codes-and-tiers.md) — the other inventory
- [Phone games](phone-games-runbook.md) — what those tags are actually for
- [PBX lifecycle](pbx-lifecycle.md) — standing the carrier side up and down
- [VoIP.ms provisioning runbook](voipms-provisioning-runbook.md) — the portal-only security steps
