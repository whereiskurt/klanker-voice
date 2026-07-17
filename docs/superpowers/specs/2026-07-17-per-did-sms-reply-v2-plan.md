# Per-DID OTP SMS reply — v2 execution plan (post-/clear)

**Date:** 2026-07-17
**Goal:** the CTF OTP-during-call text must come **FROM the number the caller dialed**, not from a shared sender (currently 613).
**Status:** planning — execute in a fresh session.

## Current live state (working — do not regress)
- All 5 DIDs route to `account:557010_klanker-pbx`; calls answer; press **333266** → OTP read-out → SMS **from 613** (the `sms_dids` pool) → *"Just kidding. Check your phone. Hack the planet!"* + pause.
- `main` = PR #67; the per-DID scaffolding is deployed but **INERT** (no DID delivers a distinct exten, so `dialed_did` is always `<none>` → falls back to the 613 pool). Behavior is functionally clean.
- **Sender is independent of inbound:** the SMS is sent via the VoIP.ms API with `did=<sender>` — any SMS-enabled DID can be the sender, chosen at send time. **The ONLY hard problem is identifying which DID the caller dialed, at the edge.**

## What has FAILED (do not repeat)
1. **Approach A — read the SIP `To:` header** (#65): on the shared sub-account VoIP.ms puts the **sub-account name** (`557010_klanker-pbx`) in both the Request-URI AND `To:` — never the DID. Live-proven. DEAD.
2. **Option A — per-DID VoIP.ms sub-accounts** (#67): gave each Vegas DID its own sub-account + registration. Registration went `yes` (byte-identical to klanker-pbx), routing set correctly (`account:557010_vegas3283`, FULL username) — yet **VoIP.ms fast-busies the call; 0 calls reach Asterisk.** The one difference: all sub-accounts register from the **same Fargate IP**. Strong conclusion: **VoIP.ms delivers inbound to only ONE sub-account per source IP.** Caused two live outages while testing. DEAD unless VoIP.ms says otherwise (Step 3).

### Hard-won gotchas
- `kv voipms route-did <did> --subaccount X` builds `routing=account:X` with **NO prefix** — you MUST pass the **FULL** username `557010_<label>` (short label = VoIP.ms can't resolve = busy). Rollback = `kv voipms route-did <did> --subaccount 557010_klanker-pbx`.
- **NEVER experiment on a live DID without a rollback ready.** Test one DID at a time; keep the others on klanker-pbx. Each failed attempt = an outage on that number.

## The plan (cheapest-first)

### Step 1 — DIAGNOSTIC (cheap, do FIRST): is the DID in ANY other SIP header?
We only ever checked `To:`. VoIP.ms often carries the dialed DID in **another** header even on account routing. On the WORKING klanker-pbx trunk (no re-routing, no outage risk), capture the full INVITE header set and log it on a live call:
- In `extensions.conf`, before `Stasis`, `Set()` channel vars from: `${PJSIP_HEADER(read,P-Called-Party-ID)}`, `${PJSIP_HEADER(read,Diversion)}`, `${PJSIP_HEADER(read,Remote-Party-ID)}`, `${PJSIP_HEADER(read,Contact)}`, `${PJSIP_HEADER(read,X-*)}` (and any others), plus `${CALLERID(dnid)}`.
- Read them via ARI `get_channel_var` (already added in #67 — reuse it) and **log all at INFO** on one live call to a klanker-pbx DID.
- If the dialed DID appears in ANY header → **trivial fix**: point `_dialed_did_from_*` at that header. Reuse the existing `dialed_did` + `_select_sms_send_dids` + `sms_reply_dids` machinery from #65/#67 verbatim. NO sub-accounts, NO SIP-URI routing, NO outage risk (klanker-pbx routing unchanged). **This is the ideal outcome — try it first.**

### Step 2 — if the DID is in NO header: SIP-URI routing to a STATIC inbound IP
Give telephony-edge a **stable inbound SIP endpoint** and route each DID to a SIP URI that carries the DID:
- Provision a static inbound IP (NLB with an EIP, or an EIP on the task path) fronting the Fargate SIP (UDP 5060) + RTP range. SG locked to `voipms_toronto_pop_cidrs` (already defined in telephony-sg.hcl) — this preserves the POP-lock security posture.
- Add a `type=identify` for the VoIP.ms POP IPs (already present as `voipms-identify`) so inbound is IP-authenticated (no registration needed for inbound; keep the outbound registration or drop it).
- In VoIP.ms, route each DID to **`sip:{DID}@<static-IP>`** (SIP URI routing) instead of `account:...`. The INVITE's Request-URI user is then the **DID itself** → `dialplan.exten` = the DID → controller reads the real dialed DID directly (simpler than a map — exten IS the DID).
- Trade-off: this opens a public inbound SIP port (today's registration model deliberately has none). The SG POP-lock + pjsip identify mitigate. This is the standard production PBX posture for receiving DIDs and is the robust long-term fix.
- Effort: moderate infra (NLB/EIP + SG + pjsip identify + per-DID VoIP.ms routing). Test on ONE DID first.

### Step 3 — PARALLEL (operator-owned): VoIP.ms support ticket
Ask VoIP.ms: *"Can one PBX (single public IP) register multiple sub-accounts and receive inbound per-DID differentiated by sub-account?"* If **yes** (with some setting) → the #67 Option-A scaffolding is already built; just fix whatever setting and re-route with the FULL username. If **no** → confirms Step 2 is required.

## Recommendation
Do **Step 1** immediately (cheap, zero outage risk, possibly a complete fix). File **Step 3** in parallel. Only if Step 1 finds nothing and Step 3 says no → do **Step 2** (SIP-URI + static IP).

## Cleanup decision (after a solution is picked)
The #67 vegas scaffolding (pjsip `[voipms-registration-vegas*]`, `subaccount_did_map`, `[telephony.subaccount_dids]`, 4 SSM `sip_*_vegas*` params, the `vegas3234`/`vegas3283` VoIP.ms sub-accounts, `service.hcl` vegas secrets, entrypoint scrub) is INERT:
- If **Step 1 or 2** wins → REMOVE all of it (Option A is abandoned). A cleanup branch was drafted then discarded this session; redo it cleanly.
- If **Step 3** unblocks Option A → KEEP it and just fix the VoIP.ms setting.

## Reference facts for the fresh session
- AWS: `--profile klanker-application` (account 052251888500), region us-east-1. VoIP.ms API reachable from Kurt's egress (IP-allowlisted).
- ECS: cluster `app-use1-kmv`, service `telephony-edge-use1`, task-def family `telephony-edge-use1-kmv`, log group `/ecs/telephony-edge-telephony-edge-use1-kmv`. Deploy = merge `apps/voice/**` to main → `build-telephony-edge.yml`.
- VoIP.ms registration check: `getRegistrationStatus&account=557010_klanker-pbx` (creds from SSM `/kmv/secrets/use1/voipms/api_{username,password}`). Registration completes ~1-2 min after task start.
- Unlock code = **333266** (SSM `/kmv/secrets/use1/ctf/announcement_code`, changed from 990011 this session).
- The `dialed_did` / `get_channel_var` / `_select_sms_send_dids` / `sms_reply_dids` code from #65/#67 is present on main and reusable for Step 1.
- Related memory: `ctf-per-did-sms-reply.md`, `ctf-otp-sms-during-call-idea.md`, `ctf-phone-otp-announcement-did.md`. Prior design doc: `2026-07-16-ctf-per-did-sms-reply-design.md`.
- SEPARATE workstream (do not conflate): the CTF **verifier** (meshtk seed copy) — see `ctf-phone-otp-announcement-did.md`. The base32 seed was copied into DC34's `.secrets.sops.json` (`mqtt.ctf-otp-url`, UNCOMMITTED in that repo's working tree).
