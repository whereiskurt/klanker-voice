# Security Policy

Klanker Voice is a personal project (see [NOTICE.md](../NOTICE.md)). There is
no commercial support, no SLA, and no formal security team. The notes below
describe how to report vulnerabilities and what to expect in return.

## Reporting a Vulnerability

**Please do not open a public GitHub issue for security-sensitive reports.**

Email **whereiskurt@gmail.com** with `[klanker-voice-security]` in the subject
and include:

- A description of the vulnerability
- Steps to reproduce (proof-of-concept code or commands welcome)
- Affected versions, commits, or configurations
- Your assessment of impact and any suggested mitigations
- Whether you'd like to be credited in any eventual release notes

I will acknowledge receipt within **7 days** and aim to provide a more detailed
response within **30 days** indicating next steps and a rough timeline. As a
side project this may slip; please feel free to email a polite nudge if you
haven't heard back.

If you'd like to encrypt the report, ask in your first email and I will
respond with a public key.

## Scope

**In scope:**

- The voice service (`apps/voice/` — pipeline, signaling endpoints, session
  and quota gating, telephony media handling)
- The auth service (`apps/auth/` — magic-link login, OIDC issuer, access-code
  and tier handling, token claims)
- The browser client (`apps/voice/client/`)
- The Asterisk telephony edge configuration (`apps/voice/asterisk/`)
- The `kv` operator CLI (`kv/`)
- Terraform / Terragrunt modules in `infra/`

**Out of scope:**

- Vulnerabilities in third-party services Klanker Voice integrates with
  (AWS, Anthropic, Deepgram, ElevenLabs, VoIP.ms). Please report those to the
  respective vendors.
- Issues that require operator misconfiguration to exploit. The threat model
  assumes the operator (the human running `kv` and the AWS account) is
  trusted; anonymous browser users and PSTN callers are not.
- Toll-fraud or abuse scenarios against forks that have removed the quota,
  concurrency, or access-code gating.
- Bugs in unmerged branches.

Reports that matter most here: anything that lets an unauthenticated or
under-quota caller burn metered API spend, bypass access-code gating, hijack
another session, or extract secrets from the containers.

## Disclosure

This is a personal project; I will work with reporters in good faith on
coordinated disclosure but cannot commit to a fixed embargo or guarantee any
particular timeline. As a rough guide:

- Critical issues with active exploit potential: I'll prioritize a fix before
  public disclosure where possible.
- Lower-severity issues: I may publish the fix and a brief advisory together.

If you intend to publish independently after a reasonable period (e.g., 90
days from acknowledgement), please tell me up front so I can plan around it.

## No Bug Bounty

There is no monetary bug bounty. Reports are accepted out of goodwill, and
credit is given in release notes if the reporter would like to be named.

## Supported Versions

Only the `main` branch and the most recent tagged release receive security
updates. Older versions are unsupported.

## See Also

- [LICENSE](../LICENSE) — the warranty disclaimer that applies to this
  software.
- [NOTICE.md](../NOTICE.md) — personal-project status, no employer
  affiliation.
- [CONTRIBUTING.md](CONTRIBUTING.md) — DCO and contributor warranty terms.
