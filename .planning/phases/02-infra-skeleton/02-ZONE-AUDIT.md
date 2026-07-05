# Phase 2 Zone / SES / Identity Audit

**Probed:** 2026-07-05 (all probes read-only, live SSO session)
**Purpose:** Resolve research Open Question 1 (apex mail → DMARC route), validate
assumption A1 (HostedZoneAdmin can list/read the mgmt zone), and confirm D-12
(SES production access). Plans 04/05 read this file before touching DNS.

## 1. Profile Identities (sts get-caller-identity)

| Profile | Account | Assumed Role | Expected | Match |
|---------|---------|--------------|----------|-------|
| klanker-application | 052251888500 | AWSReservedSSO_AdministratorAccess_024532ccbde75573 | 052251888500 (app) | YES |
| klanker-management | 481723467561 | AWSReservedSSO_HostedZoneAdmin_5462beec97e1c2f0 | 481723467561 (mgmt) | YES |
| klanker-terraform | 052251888500 | AWSReservedSSO_AdministratorAccess_024532ccbde75573 | 052251888500 (state account) | YES |

All three resolve through the SSO session as whereiskurt@gmail.com. The
terraform profile lands in the application account with AdministratorAccess —
sufficient for the state-backend bootstrap (D-05).

## 2. Assumption A1 — HostedZoneAdmin zone listing

`aws route53 list-hosted-zones --profile klanker-management` **succeeded**
(11 zones returned, including `klankermaker.ai.` as `Z036807010CWM2JH60RKQ`).
A1 holds: the HostedZoneAdmin permission set can list zones; record reads on the
target zone also succeeded (section 3). Write permission
(ChangeResourceRecordSets) remains untested until the first apply — read path is
proven.

## 3. klankermaker.ai zone record summary (Z036807010CWM2JH60RKQ)

Record names/types only (3 record sets total):

| Name | Type | Note |
|------|------|------|
| klankermaker.ai. | NS | apex NS (registrar-managed) |
| klankermaker.ai. | SOA | apex SOA |
| sandboxes.klankermaker.ai. | NS | existing delegation to the klanker sandboxes platform |

**Apex mail verdict:** the apex has **NO MX record and no mail-related records
of any kind** (no TXT/SPF, no _dmarc, no DKIM). Research Open Question 1 is
resolved as expected: no apex mail exists.

**DMARC route decision:** **Route 2 — standalone `_dmarc.klankermaker.ai` TXT
via the inline unit** (`region/us-east-1/dmarc/`, bib-secrets pattern), with
`make_site_domain = false` in site.hcl. Route 1 (`make_site_domain = true`)
is rejected: it would hardcode an apex receive MX; even though nothing uses
apex mail today, there is no reason to take over apex inbound for one TXT
record (research Pitfall 6).

**Delegation collision check:** NO existing NS records for
`auth.klankermaker.ai` or `voice.klankermaker.ai` — the site module's NS
delegations will not collide. The only existing delegation is
`sandboxes.klankermaker.ai` (unrelated, must not be touched).

## 4. SES account status (052251888500, us-east-1) — D-12

`aws sesv2 get-account --profile klanker-application --region us-east-1`:

| Field | Value |
|-------|-------|
| ProductionAccessEnabled | **true** (D-12 CONFIRMED — out of sandbox) |
| SendingEnabled | true |
| EnforcementStatus | HEALTHY |
| Max24HourSend | 50,000 |
| MaxSendRate | 14/sec |
| SentLast24Hours | 0 |
| MailType | TRANSACTIONAL |
| ReviewDetails.Status | GRANTED (case 177459104700751) |

No prod-access request needed; INFR-04 reduces to identity + DKIM + DMARC
records via the email module, exactly as D-12 states.

## 5. Toolchain state at audit time

| Tool | Version | CI pin | Status |
|------|---------|--------|--------|
| terraform | 1.14.3 (via tfenv, upgraded from 1.8.2) | 1.14.3 | MATCH |
| terragrunt | 0.99.1 | 0.97.1 (>= 0.96 required) | OK |
| sops | 3.11.0 | 3.11.0 | MATCH |
| direnv | installed via brew this session | — | hook appended to ~/.zshrc (`eval "$(direnv hook zsh)"`); already-open shells need a restart or `source infra/.envrc` manually |
