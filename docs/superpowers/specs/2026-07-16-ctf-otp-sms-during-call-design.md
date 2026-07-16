# CTF OTP — SMS-During-Call ("Check Your Phone" Punchline) — Design

**Date:** 2026-07-16
**Status:** Approved (brainstorm) — ready for implementation
**Scope repo:** klanker-voice (this repo).
**Builds on:** `2026-07-15-ctf-phone-otp-announcement-did-design.md` (the DTMF-triggered
OTP readout — Revision 2, live in prod).

## Summary

Enrich the live CTF phone-OTP gag so that, in addition to *reading* the OTP aloud,
the agent **texts the caller a written copy of the code mid-call**. The text is the
**punchline payoff**: the spoken gag keeps its full tease ("Did you get that? … No?"
→ accelerating passes), and then the voice lands a new closing beat —
*"…just kidding — check your phone. Good luck, hack the planet!"* — by which time the
SMS has already arrived.

The text goes to the **caller's own phone number (ANI)**, which the voice edge already
has in hand (`ActiveCall.caller_id`). It is sent from one of our own
**SMS-enabled VoIP.ms DIDs** via the VoIP.ms `sendSMS` REST method, with a
**runtime auto-fallback** across an ordered pool of candidate sending DIDs so a DID that
is not SMS-capable is skipped automatically.

Everything here is **opt-in and additive**: an announcement entry with no `sms_dids`
behaves byte-for-byte as it does today (spoken readout only, existing sign-off).

## Why this shape (decisions locked in brainstorm)

| Decision | Choice | Rationale |
|---|---|---|
| Text's role vs. the voice gag | **Punchline payoff** | Keep the full "hard to catch" tease; the text is the relief. Not a silent backup, not a replacement for the readout. |
| Send path | **VoIP.ms `sendSMS`** | Same vendor as the call; REST API already used by `kv` (`voip.ms/api/v1/rest.php`, JSON status envelope). No new vendor/account. |
| Sender vs. dialed DID | **Decoupled** | The dialed DID is invisible at the edge (all calls arrive `did=557010_klanker-pbx`), so the "from" number is simply a configured SMS-capable DID we own — never "the number they called." |
| Fallback | **Runtime auto-fallback over an ordered DID pool** | Try each `sms_dids` entry in order until one send succeeds; self-heals if a DID's SMS capability changes in the portal. All fire-and-forget, so it never hangs the line. |
| Body | **Code + flavor + expiry** | `CTF proof code: NNNNNN — expires in ~2 min. Relay it fast. Hack the planet!` — context + urgency (the TOTP rolls every 120 s). |
| Send timing | **Fire-early, fire-and-forget** | Kick off the send the moment we have the OTP (before the slow readout), so the text lands before the "check your phone" beat. Bounded HTTP timeout; never awaited on the teardown path; never raises. |

## Flow

```
caller dials any DID ─▶ §24 gate ─▶ caller presses DTMF announcement code
                                             │
                              [voice edge / controller.py :: _gate_announcement]
                              cancel_for_takeover (gate stays closed) →
                              GET current TOTP from auth /ctf/otp →
                              sms_eligible? (sms_dids set AND caller ANI is NA-valid)
                                   │yes                         │no
                     create_task(_send_sms_pool(...))     (skip; no text)
                        (fire-early, bounded, never-raise)      │
                                   └───────────┬────────────────┘
                              build spoken script (branch on sms_eligible):
                                 eligible → slow×2 → "Did you get that? No?" →
                                            accel passes → "…check your phone.
                                            Good luck, hack the planet!"
                                 else     → today's exact copy ("Good luck!
                                            Hack the planet!")
                              speak over RTP → bounded grace → HANG UP
```

## Components

### 1. Config — `AnnouncementEntry.sms_dids`

Add one field to `apps/voice/src/klanker_voice/telephony/config.py`:

```python
sms_dids: tuple[str, ...] = ()   # ordered pool of SMS-capable sending DIDs; empty ⇒ SMS off
```

- Parsed from a TOML array of digit strings (`["6134805878", ...]`), normalized to a
  tuple of digits-only strings; non-digit junk stripped, empties dropped.
- **Empty ⇒ feature off**, behavior identical to today.
- A DID is a **public phone number, not a credential** — safe in plaintext TOML. The
  key `sms_dids` contains no credential token, so it passes
  `config._reject_credential_fields` (which rejects keys matching
  `password/passwd/secret/token/key/api_key`, and only scans keys, not values).
- **Credentials are never in TOML.** The VoIP.ms API username/password are read at call
  time from the environment under fixed names `VOIPMS_API_USERNAME` /
  `VOIPMS_API_PASSWORD` (task-def `secrets` → SSM), mirroring how the OTP bearer is read
  from `entry.otp_env_var`.

### 2. Send primitive — `_send_sms` + ordered fallback

Two module-level async functions (module-level so tests monkeypatch them, exactly like
`_fetch_ctf_otp` / `_fetch_tel_token`):

```python
async def _send_sms(url, api_user, api_pass, from_did, dst, message) -> bool
async def _send_sms_pool(url, api_user, api_pass, dids, dst, message) -> bool
```

- `_send_sms` issues one `method=sendSMS` GET (`did=<from_did>`, `dst=<10/11-digit dest>`,
  `message=<body>`, `api_username`/`api_password`) with a **bounded timeout**
  (`SMS_SEND_TIMEOUT_SECONDS ≈ 4 s`). Returns `True` only on HTTP 200 **and**
  `status == "success"` in the JSON envelope; `False` for every other outcome. **Never
  raises** (any transport/parse/status failure ⇒ `False`).
- `_send_sms_pool` iterates `dids` **in order**, returning `True` on the first success and
  stopping (exactly one text delivered); returns `False` if the pool is empty or every
  attempt fails.
- **Logging discipline:** log only channel-id + an ok/fail boolean and (on fail) which
  attempt index failed. **Never** log the OTP, the message body, the destination number,
  the `from_did` beyond a count, the URL, or any credential. Credentials cross only the
  query string of the outbound request, never a log line (mirrors `kv`'s posture).
- `dst` formatting: derive from `ActiveCall.caller_id` via a small helper that reuses
  `_normalize_e164` and strips the leading `+` (VoIP.ms accepts `1NXXNXXXXXX`).

### 3. Eligibility + spoken-script branch

```python
dst = _sms_dst_from_caller(active_call.caller_id)
api_user = os.environ.get("VOIPMS_API_USERNAME", "")
api_pass = os.environ.get("VOIPMS_API_PASSWORD", "")
sms_eligible = bool(entry.sms_dids) and bool(dst) and bool(api_user) and bool(api_pass)
```

(Eligibility also requires the API creds to be present — known before we
speak — so the "check your phone" punchline is never promised on a
deployment where the creds were not wired. A later network/DID failure is
still possible and accepted, but a guaranteed-broken promise is not.)

- `_sms_dst_from_caller` returns a VoIP.ms-ready NA destination string, or `""` for a
  withheld / non-North-American / malformed ANI (reusing `_normalize_e164`'s NA rules).
- `_build_announcement_script(template, code, sms_eligible)` gains the flag:
  - **eligible** → closing beat = `ANNOUNCEMENT_SMS_PUNCHLINE_COPY`
    (`"just kidding — check your phone. Good luck, hack the planet!"`).
  - **not eligible** → **today's exact** `ANNOUNCEMENT_BYE_COPY`
    (`"Good luck! Hack the planet!"`) — no "check your phone" promise we can't keep for a
    caller we can't text.
- The accelerating-digits tease is unchanged in both branches. The whole line remains a
  **single** `speak_goodbye` utterance (no multi-utterance sequencing), all plain
  punctuation, **no angle-bracket markup** (the streaming ElevenLabs path reads markup
  aloud — a hard rule carried over from the prior spec).

### 4. Hook — `_gate_announcement`

After `code` is fetched and before building/speaking the line:

```python
sms_eligible = bool(entry.sms_dids) and dst != ""
if sms_eligible:
    task = asyncio.create_task(_send_sms_pool(...))
    active_call.sms_task = task        # hold a reference so it isn't GC'd
line = _build_announcement_script(entry.line_template, code, sms_eligible)
await speak_goodbye(worker, line)
...existing bounded grace + single idempotent teardown...
```

- The send is **never awaited** in the critical/teardown path; it completes well within the
  existing grace sleep. The reference on `ActiveCall` prevents premature GC; nothing else
  reads it.
- Because speech is queued as one utterance up front, the script cannot branch on the
  send's *result* (which lands mid-playback). Consequence, accepted for a CTF gag: if the
  send later fails, a caller we believed textable still hears the code read aloud twice —
  they simply don't get the text. Non-textable callers never hear the "check your phone"
  line at all.

### 5. New tunable module constants (controller.py)

- `SMS_SEND_TIMEOUT_SECONDS = 4.0`
- `VOIPMS_SMS_API_URL = "https://voip.ms/api/v1/rest.php"`
- `VOIPMS_SMS_USER_ENV = "VOIPMS_API_USERNAME"`, `VOIPMS_SMS_PASS_ENV = "VOIPMS_API_PASSWORD"`
- `ANNOUNCEMENT_SMS_BODY_TEMPLATE = "CTF proof code: {code} — expires in ~2 min. Relay it fast. Hack the planet!"`
  (the SMS body uses the **plain** code, not the digit-spaced readout form)
- `ANNOUNCEMENT_SMS_PUNCHLINE_COPY = "just kidding — check your phone. Good luck, hack the planet!"`

## Config & infra wiring

### `apps/voice/configs/telephony.toml`

Add to the existing `[[telephony.announcement]]` table:

```toml
sms_dids = ["6134805878"]   # ordered pool of SMS-enabled sending DIDs (verified 2026-07-16); add more for fallback depth
```

`6134805878` (Belleville, ON / 613-480-KURT) is the **only** DID with
`sms_enabled = 1` as of 2026-07-16 (verified live via `getDIDsInfo`), so the feature is
functional immediately with a single-entry pool. Enabling SMS on `3474803715` /
`7254043234` / `9862763234` (all report `sms_available = 1`) and appending them here
gives real fallback depth.

### `infra/terraform/live/site/services/telephony-edge/service.hcl`

Add two `secrets` entries to the container definition (the task role **already** grants
`ssm:GetParameter` on `/kmv/secrets/use1/voipms/*`, and the parameters already exist —
no IAM change, no new secret):

```hcl
{ name = "VOIPMS_API_USERNAME", valueFrom = ".../parameter/kmv/secrets/use1/voipms/api_username" }
{ name = "VOIPMS_API_PASSWORD", valueFrom = ".../parameter/kmv/secrets/use1/voipms/api_password" }
```

Apply the telephony-edge ecs-task terragrunt unit, then deploy the telephony-edge image.

## Security & abuse

- **The caller can only ever text their own ANI.** `dst` is derived solely from the
  inbound caller-ID — the feature cannot be weaponized to text a third party.
- **The call is the rate limiter.** One send per triggered call; the call tears down after
  the readout. Cost ≈ $0.01 per trigger (VoIP.ms outbound SMS).
- **North-American only.** VoIP.ms SMS is NA-only; a non-NA / withheld caller silently
  gets no text **and** the original spoken sign-off (no broken promise).
- **No secret ever logged or placed in TOML/git.** API creds are SSM→env; the OTP, body,
  and destination are never logged.

## Operator prerequisites & recommended cleanups (not code)

1. **Task-def creds (required):** apply the telephony-edge unit so `VOIPMS_API_USERNAME` /
   `VOIPMS_API_PASSWORD` reach the container. Without them, `_send_sms_pool` fails closed
   (no text; readout still works).
2. **SMS-enabled sender (satisfied):** `6134805878` already has SMS on. For fallback depth,
   enable SMS on the other DIDs in the portal and add them to `sms_dids`.
3. **Rotate the VoIP.ms API password.** Carried over as an open item from the Phase-12
   notes. Anyone with it can `sendSMS`/place calls from our DIDs (mitigated today by
   VoIP.ms API IP-allowlisting, but rotate regardless — and the send path should use the
   rotated value).
4. **Unrelated spam-echo cleanup (optional):** `6134805878` currently has
   `sms_forward_enabled = 1`, `sms_forward = 5197101515` — inbound spam to the DID is
   forwarded to the operator's cell (this is the source of the "spam from my own number"
   text, **not** a klanker-voice bug). Disable that forward rule in the portal to stop the
   echoes. Note: if a CTF player *replies* to the OTP text, that reply is inbound to the
   sending DID and, while the forward rule is on, would also echo to the operator's cell.

## Testing

Unit tests (pytest, mirroring the existing telephony suite; monkeypatch the module-level
`_send_sms`/`_send_sms_pool`/`_fetch_ctf_otp` seams — no network, no real ARI):

- `sms_dids` parse/normalize: array → tuple of digits; junk stripped; empty ⇒ `()`.
- `_send_sms`: 200+`status=success` ⇒ `True`; non-200 / non-success / timeout / malformed
  ⇒ `False`; never raises.
- `_send_sms_pool`: first-success-wins and stops; all-fail ⇒ `False`; empty pool ⇒ `False`;
  order preserved.
- Eligibility: NA caller + non-empty pool ⇒ eligible; withheld/non-NA or empty pool ⇒ not.
- `_build_announcement_script`: eligible ⇒ contains the check-your-phone punchline;
  not-eligible ⇒ byte-identical to today's output. No angle-bracket markup in either.
- `_gate_announcement`: eligible path schedules exactly one send and speaks the punchline
  variant; ineligible path schedules none and speaks the legacy variant; a send that
  raises/fails never affects teardown (still exactly one `_close_active_call`).
- Log-discipline assertion: OTP, body, and `dst` never appear in emitted log records.

## Revision 2026-07-16 — send via the auth relay (VoIP.ms API IP allowlist)

Live debugging after ship found the direct-from-telephony-edge send **rejected**
by VoIP.ms: its REST API is **IP-allowlisted**, and the telephony-edge Fargate
task egresses from an **ephemeral** public IP that cannot be whitelisted (the
task needs a public IP for SIP/RTP, so it can't sit behind the NAT). The `auth`
service runs on a private subnet and egresses from the **stable NAT EIP**, which
is whitelistable.

So the send path changed: telephony-edge no longer calls VoIP.ms directly.
Instead it **POSTs the built SMS to a new internal auth route** `POST
/use1/ctf/sms` (`{to, message, dids}`, optional shared bearer reusing
`CTF_OTP_AUTH_TOKEN`, uniform-404 no-oracle — mirrors `/ctf/otp`). Auth relays to
VoIP.ms `sendSMS` (with the ordered-pool auto-fallback now living in
`apps/auth/webapp/src/lib/voipms-sms.ts`) from its stable IP and returns
`{sent:true}` / 404. VoIP.ms API creds moved to the **auth** task-def;
`AnnouncementEntry` gained `sms_relay_url`; eligibility = `sms_dids` + `sms_relay_url`
+ NA caller. A separate delivery bug was also fixed: the SMS body must be **7-bit
GSM-7** (an em-dash forced UCS-2, which this VoIP.ms→NA-mobile route silently
drops even though `sendSMS` returns success).

**Operator prerequisite:** whitelist the NAT EIP **`3.217.188.133`** in the
VoIP.ms portal (Main Menu → SOAP/REST API → allowed IPs). Until then the relay
still gets `ip_not_enabled` (now logged by both sides).

## Out of scope

- MMS / media (QR image of the code) — SMS text only.
- Any inbound-SMS handling or reply parsing.
- The meshtk verify/award side (separate repo, tracked in the prior spec).
