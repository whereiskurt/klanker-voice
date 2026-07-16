---
quick_id: 260716-hg5
slug: ctf-otp-sms-during-call
date: 2026-07-16
status: complete
branch: worktree-otpnumber
---

# Summary: CTF OTP SMS-during-call ("check your phone" punchline)

**Spec:** `docs/superpowers/specs/2026-07-16-ctf-otp-sms-during-call-design.md`

Text the caller a written copy of the CTF OTP mid-call (to their own ANI) via
VoIP.ms `sendSMS`, fired early so it lands before a new "…just kidding — check
your phone" spoken punchline. Opt-in, NA-only, fire-and-forget, never-raise.

## What shipped (code, tests green)

- **`telephony/config.py`** — `AnnouncementEntry.sms_dids: tuple[str,...] = ()`
  (ordered pool, empty ⇒ off). `_parse_sms_dids` normalizes to digits-only,
  drops empties, preserves order (the auto-fallback order). Key passes the
  credential-field gate; DID digits (public) only ever in TOML.
- **`telephony/controller.py`** — `_sms_dst_from_caller` (reuses `_normalize_e164`,
  NA-only 10-digit dst, `""` ⇒ ineligible); `_send_sms` (one `sendSMS`, bounded
  4s timeout, `True` only on HTTP 200 + `status=="success"`, never raises, never
  logs code/body/dst/creds); `_send_sms_pool` (ordered, first-success-wins);
  `_build_announcement_script(sms_eligible)` branches the closing beat; hook in
  `_gate_announcement` fires the send early via `create_task`, ref parked on the
  new `ActiveCall.sms_task`, never awaited on teardown. New tunable constants
  (`SMS_SEND_TIMEOUT_SECONDS`, `VOIPMS_SMS_API_URL`, `VOIPMS_SMS_USER_ENV`/
  `_PASS_ENV`, `ANNOUNCEMENT_SMS_BODY_TEMPLATE`, `ANNOUNCEMENT_SMS_PUNCHLINE_COPY`).
  Eligibility also requires API creds present (never promise an unattemptable text).
- **`configs/telephony.toml`** — `sms_dids = ["6134805878"]` on the announcement entry.
- **`infra/.../telephony-edge/service.hcl`** — `VOIPMS_API_USERNAME`/`VOIPMS_API_PASSWORD`
  task-def secrets from SSM `/kmv/secrets/use1/voipms/api_{username,password}`
  (IAM already allows `voipms/*`; params already exist).
- **Tests** — new `test_telephony_sms.py` (23) + `test_telephony_config.py` (4).
  **203 telephony tests green.** Ineligible/legacy path proven byte-identical.

## Live investigation (answered the operator's security question)

Live `getDIDsInfo` (2026-07-16, via `kv`-resolved creds): all 4 DIDs
`sms_available=1`, but only **`6134805878`** has `sms_enabled=1`. That DID also
has `sms_forward_enabled=1`, `sms_forward=5197101515` — so the "spam text from my
own number" was inbound spam to the DID **forwarded** to the operator cell by a
portal rule, **not** a klanker-voice bug and not the (then-unbuilt) SMS feature.

## Shipped — LIVE

**PR #61 MERGED + telephony-edge deployed + creds live 2026-07-16.** Gitleaks +
Terragrunt Plan passed on the PR. The post-merge `deploy.yml` (Build: Telephony
Edge, triggered by the `apps/voice/**` change) APPLIED the ecs task-def terragrunt
unit with the merged `service.hcl` AS PART OF DEPLOY, using the resolved image —
so the VoIP.ms API cred secrets landed with **no separate `terragrunt-apply`
dispatch** and no image-revert. Verified live (via `--profile klanker-application`):
- task-def **rev 28** carries `VOIPMS_API_USERNAME`/`VOIPMS_API_PASSWORD`;
- image `7fcf8cc` = main HEAD (has the code + `sms_dids=["6134805878"]`);
- service PRIMARY / rollout COMPLETED / 1-1 running;
- `6134805878` `sms_enabled=1 sms_forward_enabled=0` — forwarding disabled (spam
  echo fixed), SMS still enabled for sending.

Password rotation deferred per operator. Live end-to-end phone test pending.

## Commits (branch `worktree-otpnumber`)

- `b56f857` docs(spec)
- `53fe14a` feat(config): sms_dids
- `d0d9ecd` feat(controller): SMS send + toml
- `739232e` test: coverage
- `e93b3c9` infra(telephony-edge): API creds

## Remaining to ship (ops — human/operator gated)

1. **Apply** the telephony-edge ecs-task terragrunt unit (adds the 2 secrets),
   then **redeploy** telephony-edge (expect the deploy-concurrency gotcha if
   auth+voice+telephony collide — re-run `deploy.yml -f service=telephony-edge`).
2. **PR** the branch (filter `.planning/` via gsd-pr-branch).
3. **Optional operator:** enable SMS on 347/725/986 + append to `sms_dids` for
   fallback depth; disable the `6134805878` `sms_forward` rule to stop spam echoes;
   **rotate the leaked VoIP.ms API password** (standing Phase-12 item; the send
   path should use the rotated value).

## Live verification (pending human)

Call a DID, press the announcement code from an NA cell → hear the OTP readout +
"check your phone" punchline AND receive the SMS with the code. (Requires step 1.)
