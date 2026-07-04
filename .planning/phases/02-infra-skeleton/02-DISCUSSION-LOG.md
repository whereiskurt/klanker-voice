# Phase 2: Infra Skeleton - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-04
**Phase:** 2-Infra Skeleton
**Areas discussed:** Module sourcing, State backend bootstrap, GitHub repo & CI flow, SES & email identity

---

## Site label (raised by user during area selection)

**User's choice:** Rename `kmk` → **`kmv`** (klanker-maker-voice); never use "kmk" anywhere. Applied repo-wide and committed (`5e256fb`).

---

## Module sourcing

| Option | Description | Selected |
|--------|-------------|----------|
| Copy verbatim, prune later | Proven modules as-is, naming changes only | ✓ |
| Fork and trim on import | Strip during copy | |
| Git-reference dc34 modules | Source from other repo | |

**User's choice:** Copy verbatim, prune later. Also: keep multi-region machinery (skip_regions etc.), single-region SOPS KMS key as the one simplification.

---

## State backend bootstrap

| Option | Description | Selected |
|--------|-------------|----------|
| Bootstrap script | Idempotent scripts/bootstrap-state.sh, tf-kmv prefix | ✓ |
| Manual once | Console/CLI + README | |
| TF bootstrap stack | Self-managing state stack | |

**User's choice:** Bootstrap script; non-secret env in checked-in `infra/.envrc` (direnv).

---

## GitHub repo & CI flow

| Option | Description | Selected |
|--------|-------------|----------|
| Public whereiskurt/klanker-voice | Portfolio-friendly, SOPS keeps secrets | ✓ |
| Path-filtered push to main | Apps auto-deploy; infra plan-only + manual apply gate | ✓ |

**User's choice:** Public repo, path-filtered CI with human-gated infra applies.

---

## SES & email identity

| Option | Description | Selected |
|--------|-------------|----------|
| sign-in@auth.klankermaker.ai | Purpose-named sender on auth subdomain | ✓ |
| p=quarantine DMARC | Deliverability sweet spot for new domain | ✓ |
| SES prod-access day one | (Asked) | n/a |

**User's choice:** sign-in@ sender, p=quarantine. **Key fact:** the account already has SES production access and increased quota — no sandbox exit needed; INFR-04 reduces to identity/DKIM/DMARC records.

## Claude's Discretion

- VPC/subnet sizing, cert SANs, ECR retention (dc34 defaults)
- GitHub Actions workflow structure (within path-filter + plan-gate rules)
- WebRTC SG/public-IP knob placement (verified in Phase 4)

## Deferred Ideas

None — discussion stayed within phase scope.
