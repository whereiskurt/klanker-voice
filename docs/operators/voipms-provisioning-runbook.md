# VoIP.ms provisioning runbook (Phase 12, §25.F blank-account order)

A human operator's step-by-step recipe for taking a **blank VoIP.ms
account** to a securely-provisioned `klanker-pbx` subaccount with exactly
one inbound DID routed to it — in the exact order the telephony spec's
§25.F requires. Steps 1–4 and 8 are **portal-only** (2FA, restrictions,
alerts, API whitelist) and cannot and should not be scripted; steps 5–7 use
the `kv voipms` command family (D-03) built in this same plan.

**Outbound calling is never enabled anywhere in this runbook.** VoIP.ms
outbound stays disabled on the subaccount forever (§25.A) — there is no
step here, now or later, that turns it on.

## Prerequisites

- A VoIP.ms account (sign up at https://voip.ms if you don't have one).
- Access to write SSM SecureString parameters in the `klanker-application`
  AWS account (052251888500), region `us-east-1`.
- The `kv` CLI built from this repo (`cd kv && go build ./...`).
- A password manager to generate strong, unique passwords — you will create
  at least three (portal password, API password, subaccount SIP password),
  and none of them may match each other or any other credential you use.

---

## The §25.F order

### 1. Enable portal 2FA + set a strong portal password

1. Log in to the VoIP.ms member portal (https://voip.ms/m/login.php).
2. Set a strong, unique portal password (password manager generated,
   ≥20 characters, never reused elsewhere).
3. Under **Account Settings → Two Factor Authentication**, enable 2FA
   (TOTP app preferred over SMS).
4. Confirm you can log out and back in with 2FA before continuing.

### 2. Lock international / premium destinations

1. Under **Account Settings → International Restrictions** (or the
   equivalent "Calling Restrictions" panel), lock/disable **all**
   international and premium-rate destination classes.
2. This account will never dial outbound calls (§25.A) — locking
   destinations is defense-in-depth in case that assumption is ever
   violated by a misconfiguration.

### 3. Set balance low, auto-recharge OFF, spend/low-balance alerts ON

1. Under **Billing → Balance Manager**, keep the account balance
   **low** — only enough for a DID's monthly fee plus a small inbound-minute
   buffer (inbound calls are billed per-minute even though outbound is
   disabled).
2. Turn **auto-recharge OFF**. A DEF CON audience with a public DID must
   never be able to silently run up an unbounded bill — a drained balance
   should fail closed (calls stop routing), not auto-refill.
3. Under **Account Settings → Notifications**, enable **low-balance** and
   **spend** alert emails, set to a threshold well above zero so you have
   time to react.

### 4. Enable the API + set a strong `api_password` + whitelist the setup IP

1. Under **Account Settings → API**, enable API access.
2. Set a strong `api_password` — **must be a different value from the
   portal password** set in step 1 (the API auth model is fully separate
   from portal login).
3. Under the same API panel, **whitelist your current setup IP** (the
   machine/network you're running `kv voipms` from right now). VoIP.ms
   rejects API calls from IPs outside this whitelist.
4. Note the `api_username` shown on this page (usually your VoIP.ms account
   email or a dedicated API username) — you'll need both `api_username` and
   `api_password` for the `kv voipms` steps below.

### 5. Create the `klanker-pbx` subaccount via `kv voipms create-subaccount`

1. Generate a strong, unique SIP password for the subaccount (different
   from both the portal password and the API password).
2. Export the API credentials from step 4 into your shell (temporarily —
   these move to SSM in the "Secrets → SSM" section below and should not
   linger in shell history longer than this session):

   ```bash
   export VOIPMS_API_USERNAME="<api_username from step 4>"
   export VOIPMS_API_PASSWORD="<api_password from step 4>"
   ```

3. Create the subaccount, outbound-disabled and IP-restricted to the
   telephony edge's egress IP (or your current setup IP if the edge isn't
   deployed yet — tighten this later, see step 8):

   ```bash
   kv voipms create-subaccount \
     --username klanker-pbx \
     --password '<the strong SIP password you generated>' \
     --allowed-ip <telephony-edge egress IP>
   ```

4. **Confirm in the portal** (Accounts → Sub Accounts) that the new
   `klanker-pbx` subaccount shows international/outbound routing disabled.
   The `kv voipms` method and parameter names were verified against the
   VoIP.ms API method registry on 2026-07-12 (`createSubAccount` with
   `username`, `lock_international`, `enable_ip_restriction` +
   `ip_restriction`), but **this manual portal confirmation is still
   required** — it's the check that catches a wrong parameter *value*
   before any DID is routed.

   Note on per-call caps: the VoIP.ms API has **no per-call max-duration
   method** (`kv voipms set-caps` fails loudly and says so). The per-call
   bound is enforced by the Asterisk/controller call timer; account burn
   is bounded by the balance protections in step 3.

### 6. Order ONE DID (Toronto POP, per-minute, CNAM off)

1. In the portal, under **DID Numbers → Order DID (US/Canada)**, search for
   available numbers on a **Toronto** POP (see the POP list below — pick one
   POP and remember it, you'll register Asterisk to the *same* POP).
2. Choose **per-minute** billing (not a flat-rate plan) — this keeps a
   low-traffic demo DID cheap and bounds cost exposure.
3. Turn **CNAM (Caller ID Name) lookup OFF** for this DID — it's an extra
   per-call fee this project doesn't need.
4. Complete the order. Note the DID number — this becomes `VOIPMS_DID`.
5. Order exactly **one** DID. This project does not need, and should not
   provision, more than one public inbound number.

### 7. Route the DID to `klanker-pbx` via `kv voipms route-did`

```bash
kv voipms route-did <the DID number from step 6> --subaccount klanker-pbx
```

Confirm in the portal (**DID Numbers → Manage DID**) that routing now shows
`klanker-pbx`. The underlying VoIP.ms method name (`setDIDRouting`, params
`did` + `routing`) was verified on 2026-07-12 — but still confirm the
routing change actually took effect in the portal rather than trusting a
non-error exit code alone.

**Registration POP must equal the DID's POP.** If you registered (or plan
to register) the Asterisk edge to `toronto.voip.ms` (POP 1), the DID
ordered in step 6 must also be provisioned on Toronto POP 1 — not a
different Toronto POP number, and not a different city. VoIP.ms delivers
inbound calls over the *registered* leg; a POP mismatch between
registration and DID means calls never arrive.

### 8. Re-lock the API IP whitelist

1. Once steps 5–7 are done and the `telephony-edge` deploy's real egress IP
   is known, return to **Account Settings → API** and update the IP
   whitelist to that IP **only** — remove the temporary setup-IP entry
   added in step 4.
2. Confirm `kv voipms balance` (or any other `kv voipms` subcommand) now
   fails when run from a machine *outside* the whitelist, and succeeds from
   inside it (e.g. from the deployed edge, or via an SSM-tunnelled session
   from the allowed IP).
3. Set a recurring reminder (6 months) to re-verify this whitelist is still
   correct — IPs can be reassigned if the edge is ever redeployed.

---

## Secrets → SSM

**Every value below lives ONLY in SSM SecureString — never in git, never in
`pipeline.toml`/`configs/telephony.toml`, never in logs, never left set in
a long-running shell.** Use `aws ssm put-parameter --type SecureString` for
each, in the `klanker-application` account (052251888500), `us-east-1`:

| Secret | SSM parameter path | Source |
|---|---|---|
| `VOIPMS_SIP_USERNAME` | `/kmv/secrets/use1/voipms/sip_username` | The `klanker-pbx` subaccount username created in step 5 |
| `VOIPMS_SIP_PASSWORD` | `/kmv/secrets/use1/voipms/sip_password` | The strong SIP password generated in step 5 |
| `VOIPMS_API_USERNAME` | `/kmv/secrets/use1/voipms/api_username` | The API username from step 4 (used by `kv voipms`) |
| `VOIPMS_API_PASSWORD` | `/kmv/secrets/use1/voipms/api_password` | The API password set in step 4 |
| `VOIPMS_DID` | `/kmv/secrets/use1/voipms/did` | The DID number ordered in step 6 |
| The telephony `/tel` endpoint auth token | `/kmv/secrets/use1/telephony/endpoint_auth_token` | A freshly generated shared bearer token (not a VoIP.ms value) authenticating the Asterisk controller's calls to the private caller-ID-mint endpoint (D-02) |

**Added by 12-07 (the deployed telephony-edge's `task.containers[].secrets[]`
`valueFrom` wiring pulls all eleven of the rows in this table — the four
below were introduced by 11-06's §24 gate and 12-06's ARI wiring but were
never added to this table until now):**

| Secret | SSM parameter path | Source |
|---|---|---|
| `ASTERISK_ARI_USERNAME` | `/kmv/secrets/use1/asterisk/ari_username` | The ARI username configured in `ari.conf`'s rendered `[klanker]` user (any value; must match `ASTERISK_ARI_PASSWORD` below and what the controller connects with) |
| `ASTERISK_ARI_PASSWORD` | `/kmv/secrets/use1/asterisk/ari_password` | A freshly generated strong ARI password — consumed by BOTH Asterisk (`ari.conf`) and the standalone controller (ARI REST/WebSocket auth) |
| `TELEPHONY_ACCESS_PIN` | `/kmv/secrets/use1/telephony/access_pin` | The §24 silent answer-gate DTMF PIN (D-05) — consumed by the controller only, never written to any `.conf` file |
| `TELEPHONY_PASSPHRASE_WORDS` | `/kmv/secrets/use1/telephony/passphrase_words` | The §24 silent answer-gate spoken passphrase word set (D-05) — consumed by the controller only |

Example (repeat per row, substituting the real value and parameter path):

```bash
aws ssm put-parameter \
  --name "/kmv/secrets/use1/voipms/sip_password" \
  --type SecureString \
  --value '<the real secret value>' \
  --region us-east-1
```

**Reminder:** the SIP password (`VOIPMS_SIP_PASSWORD`) is consumed by
**Asterisk** (rendered into its gitignored config at container start, the
same pattern the Phase-11 local harness uses for the softphone password),
**not** passed into the Klanker Python voice process. The API credentials
(`VOIPMS_API_USERNAME`/`VOIPMS_API_PASSWORD`) are consumed only by whatever
runs `kv voipms` (an operator's shell, or a future CI/deploy step) — they
are never needed by the running Asterisk edge or the voice service at
runtime.

---

## Toronto POP IP list (SG allow-list source)

The deployed `telephony-edge` security group locks inbound SIP (UDP 5060)
and RTP (UDP 20000–20100) to these VoIP.ms Toronto POP IPs only — never
`0.0.0.0/0`:

| POP id | Hostname | IP |
|---|---|---|
| 45 | `toronto1.voip.ms` (= `toronto.voip.ms`) | `208.100.60.50` |
| 99 | `toronto2.voip.ms` | `208.100.60.51` |
| 98 | `toronto3.voip.ms` | `208.100.60.52` |
| 92 | `toronto4.voip.ms` | `208.100.60.53` |
| 12 | `toronto5.voip.ms` | `208.100.60.54` |
| 38 | `toronto6.voip.ms` | `208.100.60.55` |
| 61 | `toronto7.voip.ms` | `208.100.60.56` |
| 62 | `toronto8.voip.ms` | `208.100.60.57` |
| 63 | `toronto9.voip.ms` | `208.100.60.58` |
| 6 | `toronto10.voip.ms` | `208.100.60.59` |

**Re-verification note:** these IPs were pulled LIVE from the API's
`getServersInfo` on 2026-07-12 during provisioning (replacing a stale
wiki-sourced list — the wiki's `158.85.70.x`/`184.75.21x.x` addresses no
longer appear in the server registry). To re-verify:
`kv voipms` + `getServersInfo`, or the wiki Servers page cross-checked
against a live `host toronto.voip.ms`. VoIP.ms infrastructure can
change. **Re-verify this list against
[wiki.voip.ms/article/Servers](https://wiki.voip.ms/article/Servers) every
6 months**, and whenever inbound calls start failing after a period of
working correctly (a POP IP change is a likely cause). Update both this
table and the Terraform security-group CIDR list together.

---

## Verification checklist

Before considering the account "provisioned," confirm all of the following:

- [ ] Portal 2FA is enabled and required at every login
- [ ] Portal password and API password are different, strong, unique values
- [ ] International/premium destinations are locked
- [ ] Auto-recharge is OFF; low-balance and spend alerts are configured
- [ ] `klanker-pbx` subaccount exists, outbound disabled, IP-restricted
- [ ] Exactly one DID is ordered: Toronto POP, per-minute, CNAM off
- [ ] The DID is routed to `klanker-pbx` (confirmed in the portal)
- [ ] The DID's POP matches the POP Asterisk registers to
- [ ] The API IP whitelist is locked to the real deployed edge IP (not a
      temporary setup IP)
- [ ] All six secrets above are in SSM SecureString, and NOT set in any
      shell profile, `.env` committed to git, or `pipeline.toml`/
      `configs/telephony.toml`
- [ ] No outbound-dialing capability was enabled anywhere in this process

---

*Phase: 12-voip-ms-telephony-inbound-did*
*Written: 2026-07-12 (Plan 12-01, D-03/D-04/SC-1)*
