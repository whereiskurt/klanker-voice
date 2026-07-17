# CTF per-DID SMS reply — design

**Date:** 2026-07-16
**Status:** approved (operator KPH, Approach A), implemented
**Quick task:** 260716-hg5 follow-up
**Builds on:** `2026-07-16-ctf-otp-sms-during-call-design.md` (the SMS-during-call punchline)

## Problem

The CTF OTP-during-call feature texts the caller a written copy of the OTP as
the "check your phone" punchline. Today every caller is texted from a single
**shared pool** (`sms_dids = ["6134805878"]`), so **all** OTP texts appear to
come from the 613 number regardless of which DID the caller dialed. The
operator wants **each DID to reply from its own number**: a call to
`725-404-3234` should text back from `725-404-3234`, a call to `725-404-3283`
from `725-404-3283`. 613 must be **removed as the universal sender** and
**reserved/unburned** for a separate future challenge.

## The blocker (and the chosen fix)

The announcement trigger is DID-agnostic (any DID + DTMF `990011`), and on the
shared VoIP.ms sub-account the ARI `StasisStart` event's `dialplan.exten` is the
**sub-account name** (`557010_klanker-pbx`), **never** the dialed number — the
same invisibility that killed Rev-1 DID matching. To reply from the dialed DID
we must first make the dialed DID **visible at the edge**.

**Chosen approach (Approach A — "pass the phone number through"):** VoIP.ms
carries the dialed DID in the SIP **`To:` header** even on a shared sub-account.
The Asterisk dialplan stashes that header into a channel variable
(`KLANKER_SIP_TO`) before `Stasis()`; the controller reads it via ARI
`getChannelVar` at `StasisStart` and parses the real dialed DID. No new
sub-accounts, no new SIP registrations — works for all DIDs at once.

The one empirical unknown — *does VoIP.ms actually populate `To:` with the DID
on this sub-account?* — is **verified by the same live call that exercises the
feature**: the raw `To:` header and parsed `dialed_did` are logged at INFO
(both are public routing info, no secret), and a **safe fallback** keeps the
feature alive if the header does not carry the DID.

## Design

### Data flow

```
VoIP.ms INVITE (To: <sip:17254043283@toronto.voip.ms>)
  → Asterisk dialplan: Set(KLANKER_SIP_TO=${PJSIP_HEADER(read,To)}) before Stasis()
  → controller.on_stasis_start: sip_to = ari.get_channel_var(chan, "KLANKER_SIP_TO")
                                dialed_did = _dialed_did_from_sip_to(sip_to)  # "7254043283"
  → ActiveCall.dialed_did
  → _gate_announcement: send_dids = _select_sms_send_dids(entry, dialed_did)
  → _send_sms_via_relay(..., dids=send_dids)  → auth /ctf/sms relay → VoIP.ms sendSMS
```

### Sender selection (`_select_sms_send_dids`)

Per-DID mode is ON whenever `entry.sms_reply_dids` (the enrolled set) is
non-empty:

| dialed DID state | result |
|---|---|
| resolved **and** enrolled | `(dialed_did,)` — text **from the dialed DID** |
| resolved but **not** enrolled (e.g. 613) | `()` — **no text** (reserves the DID) |
| unresolved (`To:` parse miss) | `entry.sms_dids` — **legacy pool fallback** |

When `sms_reply_dids` is empty, returns `entry.sms_dids` unconditionally —
byte-identical to the pre-per-DID pool behavior.

This precisely satisfies both goals: enrolled Vegas DIDs reply from themselves;
613 (resolved but unenrolled) sends nothing and stays reserved; a parse miss
(the broken-mechanism case only) is no worse than today's pool behavior.

### Config (`AnnouncementEntry`)

New `sms_reply_dids: tuple[str, ...] = ()` — the enrolled DIDs allowed to
reply-from-self, normalized digits-only (same rule as `sms_dids`). A DID is a
public number, never a credential (passes `_reject_credential_fields`).

Shipped `configs/telephony.toml`:
```toml
sms_dids = ["6134805878"]                        # parse-miss fallback only
sms_reply_dids = ["7254043234", "7254043283"]    # both Vegas DIDs, reply-from-self
```

### DID parsing (`_dialed_did_from_sip_to`)

Pulls the user-part of the first `sip:` URI in the `To:` header, normalizes
through the same NA rules (`_normalize_e164`) as every other number, returns the
bare 10-digit NANP form (matching `sms_reply_dids` after normalization). Returns
`""` for anything not confidently a 10-digit NANP DID — including the
sub-account name `557010_klanker-pbx` (no 10-digit run → unresolved → fallback).
Never raises.

### Security / logging

- The dialplan `Set()` is a **read** of an inbound header into a channel var —
  it opens no outbound path and is not a `Dial()`/feature-code, so the
  T-11-02-01 inbound-only posture is unchanged (asserted by
  `test_asterisk_configs.py`).
- `sip_to` / `dialed_did` are logged at INFO — public routing info, no secret.
  The OTP/body/creds logging discipline is unchanged.
- `${PJSIP_HEADER(read,To)}` is Asterisk dialplan syntax; `extensions.conf` is
  bind-mounted verbatim (only `ari.conf`/`pjsip.conf` are `${VAR}`-rendered), so
  it is never env-substituted at container start.

## Verification plan

1. Ship (merge → `build-telephony-edge.yml` rebuilds the image incl. the
   dialplan + config).
2. Call **725-404-3234** and **725-404-3283**, DTMF `990011` from an NA cell.
3. Confirm: the OTP text arrives **from the dialed number**; CloudWatch
   `on_stasis_start` line shows the raw `To:` header + the parsed `dialed_did`.
4. If the `To:` header does **not** carry the DID (dialed_did `<none>` in logs):
   the caller still gets a text from the 613 fallback (no regression), and we
   pivot to per-DID sub-accounts (Option B in the handoff) as a follow-up.

## Follow-up (post-verification)

Once per-DID resolution is proven live, a trivial change empties `sms_dids`
(drops the 613 fallback entirely) so 613 is never used as a sender.

## Out of scope

MMS/QR, inbound-SMS/reply handling, per-DID sub-accounts (only needed if the
`To:` header approach fails verification).
