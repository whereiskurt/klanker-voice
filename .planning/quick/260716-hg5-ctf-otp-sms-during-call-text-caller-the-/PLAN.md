---
quick_id: 260716-hg5
slug: ctf-otp-sms-during-call
date: 2026-07-16
status: in-progress
---

# Quick Task: CTF OTP SMS-during-call ("check your phone" punchline)

**Spec:** `docs/superpowers/specs/2026-07-16-ctf-otp-sms-during-call-design.md`

Text the caller a written copy of the OTP mid-call, from an SMS-enabled VoIP.ms DID
via `sendSMS`, with runtime auto-fallback over an ordered DID pool. The spoken gag keeps
its full tease; a new "…check your phone" closing beat is the punchline payoff. Opt-in
(`sms_dids`), NA-only, fire-early / fire-and-forget / never-raise.

## Tasks (atomic commits)

1. **config.py** — add `AnnouncementEntry.sms_dids: tuple[str, ...] = ()`, parsed/normalized
   from the TOML array (digits-only, junk stripped, empties dropped); doc it; empty ⇒ off.
   Key passes `_reject_credential_fields`.
2. **controller.py — send primitives + helpers** — new constants
   (`SMS_SEND_TIMEOUT_SECONDS`, `VOIPMS_SMS_API_URL`, `VOIPMS_SMS_USER_ENV`,
   `VOIPMS_SMS_PASS_ENV`, `ANNOUNCEMENT_SMS_BODY_TEMPLATE`, `ANNOUNCEMENT_SMS_PUNCHLINE_COPY`);
   `_sms_dst_from_caller()` (reuse `_normalize_e164`, strip `+`, NA-only else `""`);
   `async _send_sms()` (one `sendSMS`, bounded timeout, True only on 200+status=success,
   never raises, never logs code/body/dst/creds); `async _send_sms_pool()` (ordered,
   first-success-wins).
3. **controller.py — script branch** — `_build_announcement_script(template, code, sms_eligible)`:
   eligible ⇒ punchline copy; else ⇒ today's exact `ANNOUNCEMENT_BYE_COPY`. Accel tease
   unchanged, single utterance, no markup.
4. **controller.py — hook + field** — `ActiveCall.sms_task` field; in `_gate_announcement`
   compute eligibility, `create_task(_send_sms_pool(...))` fire-early (ref held), build/speak
   the branched script, unchanged bounded grace + single teardown.
5. **telephony.toml** — `sms_dids = ["6134805878"]` on the announcement entry.
6. **infra telephony-edge service.hcl** — add `VOIPMS_API_USERNAME` / `VOIPMS_API_PASSWORD`
   secrets entries (SSM `/kmv/secrets/use1/voipms/api_username|api_password`).
7. **tests** — parse/normalize; `_send_sms` success/failure/timeout/never-raise;
   `_send_sms_pool` order + first-success + all-fail + empty; eligibility; script branch
   (ineligible byte-identical to today, no markup); `_gate_announcement` schedules exactly
   one send on eligible / none on ineligible, teardown unaffected by a failing send;
   log-discipline (no code/body/dst in logs).

## Done when
- `pytest` telephony suite green (incl. new tests).
- Ineligible/legacy path proven byte-identical to current behavior.
- No secret/DID-cred in TOML or logs.
