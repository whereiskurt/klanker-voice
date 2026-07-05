---
phase: 2
slug: infra-skeleton
status: ready
nyquist_compliant: true
wave_0_complete: false
created: 2026-07-04
updated: 2026-07-04
---

# Phase 2 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.
> IaC phase: validation is terragrunt/terraform-native plus AWS CLI post-apply smokes — no unit-test framework applies.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | terragrunt/terraform native validation (no unit-test framework — IaC phase) |
| **Config file** | none — commands run per-unit in `infra/terraform/live/site` with `infra/.envrc` loaded |
| **Quick run command** | `terragrunt hcl fmt --check && terragrunt validate` (per changed unit) |
| **Full suite command** | `terragrunt run --all validate` from `live/site`; `terragrunt run --all plan` as the deep gate |
| **Estimated runtime** | fmt/validate ~seconds per unit; `run --all plan` ~1–3 min (backend + provider auth) |

---

## Sampling Rate

- **After every task commit:** `terragrunt hcl fmt --check` + `terragrunt validate` on touched units + forbidden-string grep (`! grep -riq "kmk\|voiceai" infra/`)
- **After every plan wave:** `terragrunt run --all validate`; `terragrunt run --all plan` once state/backend exist (Plan 02 Task 3 onward)
- **Before `/gsd-verify-work`:** full plan clean + all post-apply smoke commands below green
- **Max feedback latency:** seconds for fmt/validate; minutes for run --all plan (acceptable for IaC — no faster signal exists)

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| 2-01-01 | 01 | 1 | INFR-01 | — | read-only probes before any write | smoke | toolchain version checks + AGENTS.md + ZONE-AUDIT grep (plan verify) | ✅ CLI-native | ⬜ pending |
| 2-01-02 | 01 | 1 | INFR-01 | T-2-02 | state bucket encrypted/versioned/public-blocked | smoke | s3api get-bucket-versioning / get-public-access-block / dynamodb describe-table chain | ✅ | ⬜ pending |
| 2-01-03 | 01 | 1 | INFR-01 | T-2-01 | no secret material tracked in public repo | smoke | git check-ignore + gh repo view visibility=PUBLIC | ✅ | ⬜ pending |
| 2-02-01 | 02 | 2 | INFR-01 | — | verbatim modules (no drift from proven source) | integration | diff -r loop over 11 modules + exclusion checks | ✅ | ⬜ pending |
| 2-02-02 | 02 | 2 | INFR-01 | T-2-04, T-2-06 | plaintext fallback gitignored; apex-MX trap avoided in config | unit | git check-ignore + site.hcl greps + dmarc/main.tf p=quarantine + zero file() in stubs | ✅ | ⬜ pending |
| 2-02-03 | 02 | 2 | INFR-01 | — | forbidden strings zero; full tree plans clean | integration | forbidden-string greps + `terragrunt hcl fmt --check`; manual-in-task: `run --all validate` + `run --all plan` | ✅ | ⬜ pending |
| 2-03-01 | 03 | 3 | INFR-05 | T-2-08 | single-region KMS key; key id persisted before oidc apply | smoke | kms describe-key MultiRegion=False + .sops.yaml/.envrc/gh-variable greps | ✅ | ⬜ pending |
| 2-03-02 | 03 | 3 | INFR-05 | T-2-07, T-2-09 | encrypted round-trip; no plaintext residue; bootstrap params preserved | integration | sops -d \| jq keys == six objects + length checks + `test ! -f .secrets.json` | ✅ | ⬜ pending |
| 2-04-01 | 04 | 4 | INFR-02 | T-2-10, T-2-11 | cross-account writes limited to expected NS records | smoke (post-apply) | route53 list-hosted-zones (app) + list-resource-record-sets NS (mgmt) | ✅ | ⬜ pending |
| 2-04-02 | 04 | 4 | INFR-02 | — | valid TLS material (ISSUED = Phase 2 gate; no service answers yet) | smoke (post-apply) | acm list-certificates ISSUED contains auth. + voice. | ✅ | ⬜ pending |
| 2-04-03 | 04 | 4 | INFR-01 | T-2-12 | wide-UDP SG exists but attached to nothing | smoke (post-apply) | describe-security-groups webrtc filter udp + ALB active | ✅ | ⬜ pending |
| 2-05-01 | 05 | 5 | INFR-01 | — | cluster/repos exist before any image push | smoke (post-apply) | ecs describe-clusters ACTIVE + ecr describe-repositories both repos | ✅ | ⬜ pending |
| 2-05-02 | 05 | 5 | INFR-05 | T-2-15, T-2-16 | SecureStrings decryptable; bootstrap deleted only after round-trip | smoke (post-apply) | ssm get-parameter length checks + bootstrap param absent (deepgram) | ✅ | ⬜ pending |
| 2-05-03 | 05 | 5 | INFR-04 | T-2-13, T-2-14 | identity verified; org DMARC quarantine; zero apex MX | smoke (post-apply) | ses get-identity-verification-attributes Success + dig _dmarc p=quarantine + empty apex-MX query | ✅ | ⬜ pending |
| 2-06-01 | 06 | 6 | INFR-07 | T-2-17, T-2-20 | roles restricted (env/branch); real KMS ARN baked; gated environment | smoke (post-apply) | iam list-roles >=4 kmv-github-* + trust JSON valid + gh env protection_rules >0 | ✅ | ⬜ pending |
| 2-06-02 | 06 | 6 | INFR-07 | T-2-18 | delegate trust requires external_id kmv | checkpoint (human-action) | — (see Manual-Only table) | — | ⬜ pending |
| 2-07-01 | 07 | 7 | INFR-07 | T-2-21 | plan=readonly role; apply=gated env; no static keys | unit | YAML parse + role/env greps + negative credential grep | ✅ | ⬜ pending |
| 2-07-02 | 07 | 7 | INFR-07 | T-2-22 | path filters exact; gitleaks present | unit | YAML parse of 6 workflows + path/role greps | ✅ | ⬜ pending |
| 2-07-03 | 07 | 7 | INFR-07 | T-2-21 | OIDC assumption proven in a real run | e2e | gh run list/view --log grep kmv-github-readonly | ✅ | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `scripts/bootstrap-state.sh` — prerequisite for any terragrunt backend contact (created + run in Plan 01 Task 2, wave 1)
- [ ] `infra/.envrc` — prerequisite for terragrunt env resolution (Plan 01 Task 2)
- [ ] Forbidden-string grep gate — one-liner run per task commit from Plan 02 on (covers D-01/naming constraint)

*No test-framework install needed — validation is CLI-native (aws, dig, terragrunt, sops, jq, gh all present per research Environment Availability).*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| kmv-github-delegate role exists in 481723467561 with external_id trust + Route53-scoped policy | INFR-07 (CI cross-account DNS) | No in-scope profile has IAM read/write in the mgmt account; creation and inspection require the user's admin access | User creates per Plan 06 Task 2 checkpoint; downstream proof is automated: a terragrunt-plan CI run goes fully green on mgmt-provider units (Plan 07 Task 3); until then partial-red is the documented expected state (research Pitfall 8) |
| Browser TLS handshake on voice./auth. hostnames | INFR-02 (full) | No service answers on the hostnames until Phase 3/4; ACM ISSUED status is the automatable Phase 2 gate | Deferred to Phase 3 (auth) / Phase 4 (voice): `curl -sv https://auth.klankermaker.ai` expects certificate-verify-ok once an ALB listener target exists |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies (checkpoint task 2-06-02 verified via Manual-Only row + downstream automated CI proof)
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references (backend script + .envrc land in wave 1 before first terragrunt contact)
- [x] No watch-mode flags
- [x] Feedback latency acceptable (seconds for static gates; post-apply smokes bounded by documented poll loops)
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** approved 2026-07-04 (planner) — pending executor confirmation at first wave
