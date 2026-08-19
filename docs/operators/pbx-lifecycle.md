# PBX lifecycle — provision and deprovision

Standing the phone side up from nothing, and taking it back down cleanly.

The [VoIP.ms provisioning runbook](voipms-provisioning-runbook.md) covers the
§25.F blank-account security order in detail — portal 2FA, destination locks,
balance alerts, the API allow-list — and this page does not repeat it. What this
page adds is the **whole** lifecycle: the AWS side as well as the carrier side,
in dependency order, and the teardown direction, which nothing else documents.

---

## What "the PBX" actually is

Not a box. Three cooperating pieces:

| Piece | Where | What it does |
|---|---|---|
| **VoIP.ms account** | the carrier | owns the numbers; terminates PSTN; delivers calls over a registered SIP leg |
| **`klanker-pbx` subaccount** | the carrier | the single SIP identity every DID routes to; outbound permanently disabled |
| **`telephony-edge` task** | ECS Fargate, `app-use1-kmv` | Asterisk (PJSIP + ARI) plus the Python controller, one container, one task |

Every DID routes to the one subaccount, and the edge registers as that
subaccount to one POP. That is why the dialled number is invisible at the edge
without a caller-ID prefix tag — see
[phone games](phone-games-runbook.md#how-a-call-becomes-a-game).

There is **no load balancer**. ARI is private-network-only; the task takes a
public IP purely so the outbound registration trunk works, and a POP-locked
security group is what makes that safe.

---

## Provisioning, in dependency order

Each step depends on the ones above it. Doing them out of order is where the
outages come from.

### 0. Carrier security first

Follow [the §25.F runbook](voipms-provisioning-runbook.md) steps 1–4 before
anything else: portal 2FA, international/premium destination locks, low balance
with **auto-recharge off** plus spend alerts, and API access enabled with a
strong `api_password` and your current IP allow-listed.

Auto-recharge off is not thrift. A public DID wired to metered APIs must fail
closed on a drained balance, not silently refill.

### 1. Create the subaccount

```bash
export VOIPMS_API_USERNAME='<from the portal API panel>'
export VOIPMS_API_PASSWORD='<from the portal API panel>'

kv voipms create-subaccount \
  --username klanker-pbx \
  --password '<a strong unique SIP password>' \
  --allowed-ip '<the edge egress IP, or your setup IP for now>'
```

Created outbound-locked (`lock_international=1`), ulaw-only, device type
Asterisk/IP-PBX, and IP-restricted when `--allowed-ip` is given.

**Confirm in the portal.** The parameter *names* are verified against the live
API; only the portal shows you that a parameter *value* did what you meant. This
is the check that catches a mistake before any DID is routed to it.

Three passwords are in play and **none may match another**: the portal password,
the API password, and this SIP password.

### 2. Order and route a number

```bash
kv voipms balance
kv voipms search-dids --state NV                     # rate centers
kv voipms search-dids --state NV --ratecenter RENO   # numbers + pricing
kv voipms order-did 7755021688                       # spends money
```

Defaults are the live-proven ones: routed to `account:557010_klanker-pbx`, POP
45, 60s dial time, `cnam=0`, per-minute billing.

For a number you already own:

```bash
kv voipms route-did 7755021688 --subaccount klanker-pbx
```

> **The POP must match the registration POP.** VoIP.ms delivers inbound calls
> over the *registered* leg. A DID on a different POP than the one Asterisk
> registers to simply never rings, and nothing errors at order time. POP 45 is
> `toronto1.voip.ms`, and it is what everything here uses.

The account caps at **5 DIDs**. At the cap, ordering fails; you must cancel one
first, and cancelling is irreversible.

### 3. Put the secrets in SSM

Every value below is SSM SecureString only — never git, never a TOML, never left
set in a long-running shell.

| Env var | SSM parameter | Source |
|---|---|---|
| `VOIPMS_SIP_USERNAME` | `/kmv/secrets/use1/voipms/sip_username` | the subaccount username from step 1 |
| `VOIPMS_SIP_PASSWORD` | `/kmv/secrets/use1/voipms/sip_password` | the SIP password from step 1 |
| `VOIPMS_API_USERNAME` | `/kmv/secrets/use1/voipms/api_username` | the API username |
| `VOIPMS_API_PASSWORD` | `/kmv/secrets/use1/voipms/api_password` | the API password |
| `ASTERISK_ARI_USERNAME` | `/kmv/secrets/use1/asterisk/ari_username` | any value; must match what the controller uses |
| `ASTERISK_ARI_PASSWORD` | `/kmv/secrets/use1/asterisk/ari_password` | a fresh strong password |
| `TELEPHONY_ENDPOINT_AUTH_TOKEN` | `/kmv/secrets/use1/telephony/endpoint_auth_token` | a fresh bearer for the caller-ID mint endpoint |
| `TELEPHONY_ACCESS_PIN` | `/kmv/secrets/use1/telephony/access_pin` | the gate DTMF PIN |
| `TELEPHONY_PASSPHRASE_WORDS` | `/kmv/secrets/use1/telephony/passphrase_words` | the gate spoken passphrase |

```bash
aws ssm put-parameter --type SecureString \
  --name /kmv/secrets/use1/voipms/sip_password \
  --value '<the real value>' --region us-east-1
```

Note the split in who consumes what: the **SIP** password is rendered into
Asterisk's gitignored config at container start. The **API** credentials are used
by whatever runs `kv voipms` — an operator shell, or `kv studio` — and by the
auth app for the SMS relay. The running Asterisk edge never needs them.

`/kmv/secrets/use1/voipms/did` also exists from the original single-DID
provisioning. **It has been stale since the second number was added.** Nothing
reads it as inventory; don't start.

### 4. Lock the security group to the POPs

The edge's security group allows inbound SIP (UDP 5060) and RTP only from the
ten VoIP.ms Toronto POP addresses — never `0.0.0.0/0`:

```
208.100.60.50/32   toronto1   POP 45   (the registration target)
208.100.60.51/32   toronto2   POP 99
208.100.60.52/32   toronto3   POP 98
208.100.60.53/32   toronto4   POP 92
208.100.60.54/32   toronto5   POP 12
208.100.60.55/32   toronto6   POP 38
208.100.60.56/32   toronto7   POP 61
208.100.60.57/32   toronto8   POP 62
208.100.60.58/32   toronto9   POP 63
208.100.60.59/32   toronto10  POP 6
```

Declared in `infra/.../region/us-east-1/network/telephony-sg.hcl` and attached
to the service via `security_group_overrides`.

> **The edge is deliberately not in the shared security-group list** that voice
> and auth use. That list includes `webrtc_udp`, which is `0.0.0.0/0` on UDP
> 20000–20100 — attaching it here would defeat the entire POP lock.

These IPs were pulled live from `getServersInfo` during provisioning, replacing
a stale wiki-sourced list. **Re-verify every 6 months**, and immediately whenever
inbound calls start failing after a period of working — a POP IP change is a
prime suspect. Update the Terraform list and the runbook table together.

### 5. Deploy the edge

The edge image is `kmv-telephony-edge` in ECR, built from
`apps/voice/asterisk/Dockerfile` (Asterisk + the controller in one container).
Deploy via the normal path — see [infrastructure](infrastructure.md).

Its task role is deliberately narrow: SSM read plus KMS decrypt scoped to exactly
the three secret prefixes it consumes. Never the shared cluster role, and
**never `/kmv/operators/*`**, which holds an operator-only parameter no task role
may read.

### 6. Re-lock the API allow-list

Once the deployed edge's real egress IP is known, return to the portal's API
panel and narrow the allow-list to that IP, removing the temporary setup entry.

> Doing this will stop `kv voipms` working from your laptop, and
> `kv telephony list` will report `ip_not_enabled` where the DID table should be.
> That is the trade: either your workstation is allow-listed and you can run
> `kv voipms` locally, or it is locked to the edge and you cannot. Decide
> deliberately rather than discovering it mid-event. Set a 6-month reminder to
> re-verify the entry — IPs get reassigned on redeploy.

### 7. Verify by calling

```bash
kv telephony list                          # DIDs present, routed, tagged
kv telephony stats --since 1h              # your test call shows up
kv telephony calls --since 1h --view calls # with the outcome you expect
```

Dial the number. Nothing short of a real call proves registration, POP match,
security group, prefix resolution and the gate all at once.

---

## Deprovisioning

Reverse order, and one direction matters more than the other: **anything that
removes an SSM parameter a running task references will stop that task from
launching.** Take the reference away first, always.

### Retiring one number

```bash
kv telephony calls --since 720h --did 7755021688     # confirm it's cold
```

1. Remove its `[[telephony.announcement]]` block(s) and its
   `[telephony.cid_prefix_dids]` entry from `configs/telephony.toml`.
2. Remove its `valueFrom` rows from `service.hcl` (both telephony-edge and, if
   it had an OTP seed, auth).
3. Deploy both services.
4. Confirm the line is inert — call it.
5. `kv voipms clear-cid-prefix 7755021688` (optional; drops it to plain concierge).
6. Delete its SSM parameters (optional).
7. `kv voipms cancel-did 7755021688 --yes` — **irreversible**, and only if you
   truly want the number gone.

Stopping at step 5 leaves a working concierge line. Stopping at step 4 leaves a
number you own that answers with the default behaviour. Only step 7 is
permanent.

### Taking the whole phone side down

To pause without destroying anything:

```bash
aws ecs update-service --cluster app-use1-kmv \
  --service telephony-edge-use1 --desired-count 0
```

Inbound calls stop being answered; everything else — numbers, subaccount,
secrets, config — stays exactly as it is. Set `--desired-count 1` to bring it
back. This is the right move for "not during the conference" or a cost pause.

> Terraform owns `desired_count`. A scale-to-zero done this way will be undone by
> the next `terragrunt apply` of the ECS service unit. For anything longer than a
> few days, change it in the unit and apply, rather than fighting the next deploy.

To decommission for real:

1. Scale the service to zero, confirm calls stop.
2. Retire each DID (above), cancelling last.
3. Delete the `klanker-pbx` subaccount in the portal.
4. Delete the `/kmv/secrets/use1/{voipms,asterisk,telephony}/*` parameters.
5. Remove the telephony-edge service, task and security-group units.
6. Drain the VoIP.ms balance to near zero and leave auto-recharge off.

**Do not delete** on the way out:

- `kmv-auth-electro` or `kmv-voice-usage` — the browser side needs both.
- The `pstn-public-tier` row — harmless if orphaned, and a silent outage if the
  phone side ever comes back without it.
- The ledger S3 bucket — it holds transcript history and has no `force_destroy`,
  so Terraform will refuse to remove it while objects remain. That refusal is a
  feature.
- `/kmv/operators/*` — operator parameters, unrelated to the PBX.

---

## Rotating credentials

**SIP password.** Change it in the portal, update
`/kmv/secrets/use1/voipms/sip_password`, redeploy the edge. There is a
registration gap between the portal change and the redeploy during which inbound
calls fail — do it in a maintenance window, not during an event.

**API password.** Change it in the portal, update
`/kmv/secrets/use1/voipms/api_password`. No redeploy needed for `kv voipms`
(it reads SSM at invocation), but the auth service caches it at task start, so
redeploy auth if the SMS relay matters.

**ARI password.** Both Asterisk and the controller read it, and they are in the
same container — update `/kmv/secrets/use1/asterisk/ari_password` and redeploy.
No call-path gap beyond the deployment itself.

**Gate PIN / passphrase.** Rotate from `kv studio` → Keys & secrets, or with
`aws ssm put-parameter --overwrite`. The edge reads them at boot, so restart to
pick up the change.

---

## Related

- [VoIP.ms provisioning runbook](voipms-provisioning-runbook.md) — the portal-only security order
- [Phone number inventory](phone-number-inventory.md) — what you currently own
- [Phone games](phone-games-runbook.md) — what the tagged lines do
- [Infrastructure](infrastructure.md) — the AWS side in full
- [`kv` CLI reference](kv-cli-reference.md) — every command used here
