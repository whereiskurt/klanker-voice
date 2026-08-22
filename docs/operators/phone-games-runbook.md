# Phone games runbook

The CTF lines: call a number, enter a code, get something back. This page is how
they actually work, how to add one, how to change one, and how to retire one —
plus every trap that has already cost a live debugging session.

> A "game code" is **not** an access code. Access codes live in DynamoDB and
> grant voice sessions ([see that page](access-codes-and-tiers.md)). Game codes
> live only in SSM, are entered as DTMF *during* a call, and grant no session at
> all — the caller hears a script and the line hangs up. No mint, no quota, no
> concierge.

---

## What's live

Five games across four numbers, as of 2026-08-18.

| Game | Number | Tag | Trigger | Does |
|---|---|---|---|---|
| 3234 | 725-404-3234 | `KVD3234` | DTMF only | reads a live TOTP aloud, texts a claim link |
| 3283 | 725-404-3283 | `KVD3283` | DTMF only | same, different seed |
| UCTF | 725-404-8283 | `KVD8283` | DTMF **or** spoken | same, different seed |
| 1800 | 855-916-INFO | `KVD1800` | DTMF **or** spoken | same; SMS sent from the 725 pool |
| RICK | 855-916-INFO | `KVD1800` | DTMF only | plays a shuffled audio clip, hard-capped, then hangs up |

Note that **1800 and RICK share a line**. That works because the registry is
keyed by the code a caller enters, not the number they dialled.

```bash
kv telephony list      # the "Phone games" section
```

---

## How a call becomes a game

```
caller dials a DID
        │
        ▼
VoIP.ms prepends the DID's caller-ID name prefix   ("KVD3234" + their CNAM)
        │
        ▼
Asterisk answers, hands the call to the controller via ARI
        │
        ▼
edge reads ${CALLERID(name)}, matches the prefix against
[telephony.cid_prefix_dids]  →  now it knows which number was dialled
        │
        ▼
pickup cue plays (ring + "hey", ~4.3s).  The gate window starts AFTER this.
        │
        ▼
caller has gate_window_seconds (12) to present a factor
        │
        ├── enters a game's DTMF code   ─┐
        ├── says a game's spoken words  ─┤──▶ that game's script runs, then hangup
        ├── enters the concierge PIN     │    (suppressed entirely on OTP-only DIDs)
        ├── says the concierge passphrase┘
        │
        └── nothing lands in time ──▶ fail-closed: rickroll clip, then hangup
```

Two design facts drive everything else on this page:

**The dialled DID is invisible at the edge without the prefix.** VoIP.ms routes
every number to the one shared `557010_klanker-pbx` subaccount, so the SIP
`To:` header carries the subaccount, not the number. The caller-ID name prefix
is the only channel that survives. A DID with no prefix — or with CNAM enabled,
which overwrites the name — resolves to nothing and falls through to the plain
concierge.

**The armed-trigger registry is keyed by the resolved code VALUE.** Not by DID.
The controller builds `{code_value: entry}` from the environment at boot, then
filters by DID per call. Which gives the single most dangerous constraint here:

> ### Two games must never resolve to the same code value.
>
> If two SSM parameters hold the same string, the later entry silently wins for
> **both** DIDs — the earlier game becomes unreachable, with no error at boot,
> no error at call time, and nothing in the logs. It just stops working.
>
> Check before you seed. There is no tooling that catches this.

---

## The five places one game lives

Adding a game means touching all five, in this order. Skipping the order is how
you take the telephony edge down.

| # | Place | Holds |
|---|---|---|
| 1 | **VoIP.ms** | the number itself, routed to `klanker-pbx`, prefix tag set, `cnam=0` |
| 2 | `configs/telephony.toml` → `[telephony.cid_prefix_dids]` | tag → DID |
| 3 | `configs/telephony.toml` → `[[telephony.announcement]]` | the game block: DIDs, script, env var *names* |
| 4 | **SSM** `/kmv/secrets/use1/ctf/*` | the code *value*, the OTP seed, optional spoken words |
| 5 | `infra/.../services/telephony-edge/service.hcl` | the `valueFrom` rows that inject #4 into the task |

Note the split between #3 and #4: the TOML carries only env var **names**, never
a value. That is what lets the config live in a public repo.

---

## Adding a game

### 1. Get the number and tag it

```bash
kv voipms balance
kv voipms search-dids --state NV --ratecenter RENO
kv voipms order-did <NEW-DID>                        # spends money
kv voipms set-cid-prefix <NEW-DID> KVDRENO           # cnam forced 0, readback verified
kv telephony list                                    # confirm it appears
```

The account caps at **5 DIDs**. If you're at the cap, something has to be
cancelled first — and cancelling is irreversible.

### 2. Pick the code, and check it's unique

Choose a DTMF code. Verify no existing game already uses that value:

```bash
for p in 3234 3283 uctf 1800 rick; do
  printf '%s: ' "$p"
  aws ssm get-parameter --name "/kmv/secrets/use1/ctf/announcement_code_$p" \
    --with-decryption --query 'Parameter.Value' --output text
done
```

Compare against your candidate. This is the only defence against the collision
described above.

### 3. Seed SSM — **before** touching `service.hcl`

```bash
aws ssm put-parameter --type SecureString \
  --name /kmv/secrets/use1/ctf/announcement_code_reno --value '<your code>'

# OTP games also need a base32 TOTP seed:
aws ssm put-parameter --type SecureString \
  --name /kmv/secrets/use1/ctf/otp_secret_reno --value '<base32 seed>'

# Optional spoken trigger. Seed the literal __unset__ sentinel to wire it
# now and arm it later:
aws ssm put-parameter --type SecureString \
  --name /kmv/secrets/use1/ctf/announcement_words_reno --value '__unset__'
```

> **Seed before you wire.** ECS refuses to launch a task whose `valueFrom`
> parameter does not exist. Adding the `service.hcl` row first and deploying
> takes the telephony edge — every phone line — down until you seed the
> parameter. This has bitten before; it is why the TOML comments shout about it.

### 4. Add the TOML entries

In `apps/voice/configs/telephony.toml`:

```toml
[telephony.cid_prefix_dids]
"KVDRENO" = "<NEW-DID>"

[[telephony.announcement]]
dids           = ["<NEW-DID>"]
otp_url        = "https://auth.klankermaker.ai/use1/ctf/otp?g=reno"
otp_env_var    = "CTF_OTP_AUTH_TOKEN"
code_env_var   = "CTF_ANNOUNCEMENT_CODE_RENO"     # NAME only, never a value
words_env_var  = "CTF_ANNOUNCEMENT_WORDS_RENO"    # optional
line_template  = "Hey! Let me get that one time password for you. Ready? . ... {code}. That's {code_fast}."
sms_dids       = []
sms_reply_dids = ["<NEW-DID>"]
sms_relay_url  = "https://auth.klankermaker.ai/use1/ctf/sms"
sms_claim_url_template = "https://q.defcon.run/creno?v={code}"
```

And, if this line should suppress the concierge PIN and passphrase:

```toml
otp_only_dids = [..., "<NEW-DID>"]
```

`line_template` **must keep a `{code}` substitution** — the config loader rejects
the whole file at boot without one, which means the edge won't start.

### 5. Wire the task definition

In `infra/terraform/live/site/services/telephony-edge/service.hcl`, add to
`secrets`:

```hcl
{ name = "CTF_ANNOUNCEMENT_CODE_RENO",
  valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/ctf/announcement_code_reno" },
{ name = "CTF_ANNOUNCEMENT_WORDS_RENO",
  valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/ctf/announcement_words_reno" },
```

An OTP game also needs its seed wired into the **auth** service
(`services/auth/service.hcl`), because the auth app is what mints the TOTP:

```hcl
{ name = "CTF_OTP_SECRET_RENO",
  valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/ctf/otp_secret_reno" },
```

The auth `/ctf/otp` route resolves `?g=<game>` against a **static allowlist** of
env vars. A new game needs a code change there too — it is not purely
configuration.

### 6. Deploy and verify

Both services, then call the number:

```bash
kv telephony list                        # the game appears in the games table
kv telephony stats --since 1h --did <NEW-DID>
```

**Verification is a phone call.** Nothing short of dialling proves the chain —
prefix set at the carrier, prefix arriving in `CALLERID(name)`, tag matching the
TOML, code resolving from SSM, script playing. Each link fails silently.

---

## The traps

Everything below has actually happened.

### `cnam=1` silently kills per-DID resolution

With CNAM lookup on, VoIP.ms overwrites the caller-ID *name* — so your prefix
never arrives, the DID doesn't resolve, and every call on that line falls
through to the plain concierge. No error. Live-proven on DID 3283.

`kv voipms set-cid-prefix` forces `cnam=0` on every write for this reason. If
you ever set a prefix through the web portal, check CNAM yourself.

### `setDIDInfo` is full-replace

VoIP.ms's DID-update API resets every field you don't send. Setting a prefix
through the raw API without re-sending the full snapshot wipes routing, POP,
dial time, failover targets. `kv voipms set-cid-prefix` snapshots first and
re-sends twelve fields, then reads back and refuses to report success unless
routing survived. Use the command, not the API.

### Duplicate code values

Covered above. Two games with the same code value: the later one wins for both
DIDs, silently.

### Wiring before seeding

Covered above. ECS won't launch the task; every phone line goes down.

### The gate window is tight for speech

`gate_window_seconds = 12`, measured from the end of the ~4.3s pickup cue. That
is comfortable for DTMF and tight for a spoken trigger, because Deepgram needs
~2.8s to finalise an utterance on top of the caller's speaking time.

The history is instructive: the window was 8s, and telemetry showed that even
the operator — who knows the code — was landing at a median of 6.0s with a max
of 12.1s, with 3 of 24 successful unlocks already exceeding 8s. Six external
callers timed out at exactly 8.1s having entered partial codes of 2, 4, 5, 5, 6
and 9 digits: the fingerprint of a window closing mid-code.

The fix was two-part — widen to 12s **and** start the timer after the cue rather
than at pipeline start. That took a caller's real dialling budget from ~4s to
~10s. Immediately afterwards, the first-ever external solve happened: one caller
swept all four games in six minutes.

If spoken triggers start failing, `gate_window_seconds` is the knob.
`gate_cue_lead_max_seconds` (8.0) caps how far the cue can push the deadline, so
the gate always fails closed within `window + cue_lead` no matter what the cue
does.

### `CODE STATUS: not set` is normal

`kv telephony list`'s games table reflects **your shell's** environment. The
deployed values live in SSM and reach the task through `valueFrom`. `not set`
locally means nothing is wrong. The command says so in its own footer, and it
still catches people.

### The `__unset__` sentinel

A spoken trigger is skipped — no registry row — in four states: no
`words_env_var` on the entry, the env var absent, a value that is empty or
whitespace-only, or a value that equals `__unset__` exactly (after strip and
lowercase; a real phrase merely *containing* the word is unaffected).

This is what lets a game ship with its numeric factor live and its spoken factor
inert, arming later by replacing one SSM value with no deploy. The two factors
arm independently — a disabled spoken factor never disturbs the numeric one.

### Toll-free can't send SMS until verified

US carriers block SMS from an unverified toll-free number. Game 1800's claim
text is therefore sent from the 725 pool: its `sms_reply_dids` is empty, which
makes the sender fall back to the ordered `sms_dids` pool and try each until one
succeeds. Once toll-free SMS verification clears, drop the pool and set
`sms_reply_dids = ["8559164636"]` to text from the dialled number itself.

### SMS goes through auth, not from the edge

The VoIP.ms API is IP-allow-listed, and the telephony edge's Fargate egress IP
is ephemeral. So the edge POSTs to the auth app's `/ctf/sms` relay, and auth —
which egresses from the stable, allow-listed NAT EIP — makes the VoIP.ms call.
An `sms_relay_url` that is empty means no SMS is sent, silently.

### RICK clips are never committed

The playback clips live in private S3 (`s3://$KMV_LEDGER_BUCKET/media/telephony/rick/`)
and are mirrored into the container at boot. An empty clip folder degrades to an
immediate hangup, which is deploy-safe but indistinguishable from a broken game.
`make -C apps/voice rick-audio` normalises; upload per the README in
`assets/telephony/rick/`.

`audio_dir` and `otp_url`/`line_template` are **mutually exclusive** — the loader
enforces it. A playback entry is a different kind of thing from an OTP entry.

---

## Changing a game

**The code value** — rotate the SSM parameter. No deploy; the edge reads it at
boot, so restart the service (or wait for the next deployment) for it to take.
Check uniqueness first.

**The script** — edit `line_template` in the TOML and deploy. Keep `{code}`.

**Which line it answers on** — edit `dids` in its announcement block, and make
sure that DID has a prefix tag in `[telephony.cid_prefix_dids]`. Deploy.

**Arming a spoken trigger** — replace the `__unset__` sentinel in SSM with real
whitespace-separated words. Matching is an order-independent token subset, so
any utterance *containing* the words unlocks ("I'm lost", "Help, I'm lost!").
Restart to pick it up.

---

## Retiring a game

```bash
kv telephony calls --since 720h --did 7254043234     # is anyone still playing?
```

Then, in order:

1. Remove its `[[telephony.announcement]]` block from the TOML.
2. Remove its `service.hcl` `valueFrom` rows (both services, if it had an OTP seed).
3. Deploy.
4. Confirm the line is inert by calling it.
5. Optionally `kv voipms clear-cid-prefix <did>` to drop it back to the plain concierge.
6. Optionally delete its SSM parameters.
7. Only if you also want the number gone: `kv voipms cancel-did <did> --yes` —
   **irreversible**.

Removing the TOML block before removing the `service.hcl` rows is harmless (an
unused env var is fine). The reverse — removing the SSM parameter while
`service.hcl` still references it — takes the edge down.

---

## Watching a game live

```bash
kv telephony stats --since 2h                     # anonymous: how far do people get?
kv telephony calls --since 2h --view new          # who just showed up?
kv telephony calls --since 2h --view calls        # the blow-by-blow
```

![kv telephony stats](../assets/terminal/telephony-stats.svg)

Read the outcome mix. A healthy game line shows `announcement_code` alongside
some `gate_timeout` and `early_hangup` — people dial, hesitate, hang up, come
back. A line showing *only* `gate_timeout` is not a hard puzzle, it is a broken
one: either the code isn't reaching the caller, or the window is too tight, or
the prefix isn't resolving.

`DIGITS` in the calls view is a count, never the digits themselves. A run of
partial counts (2, 4, 5...) with `gate_timeout` is the signature of a window
closing mid-code.

---

## Related

- [`kv` CLI reference](kv-cli-reference.md) — every command used here
- [Phone number inventory](phone-number-inventory.md) — where the numbers live
- [PBX lifecycle](pbx-lifecycle.md) — the carrier and edge underneath all of this
- [Telephony data flow](../dataflows/telephony-voipms.md) — the call path in detail
- [Incident runbook](incident-runbook.md) — when a line stops answering
