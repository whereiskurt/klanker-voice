# Phase 2: Infra Skeleton - Context

**Gathered:** 2026-07-04
**Status:** Ready for planning

<domain>
## Phase Boundary

The AWS foundation exists: terragrunt site **kmv** provisions network, certs,
ecs-cluster, ecr, dynamodb, secrets, email, github-oidc, ecs-task, and ecs-service
modules from the defcon.run.34 layout; voice.klankermaker.ai and
auth.klankermaker.ai resolve with valid TLS; secrets flow SOPS → SSM; CI deploys
via GitHub OIDC. (INFR-01, INFR-02, INFR-04, INFR-05, INFR-07.) The WebRTC
UDP/public-IP groundwork lands here structurally, but its deployed verification
(INFR-03) is Phase 4.

</domain>

<decisions>
## Implementation Decisions

### Naming
- **D-01:** Site label is **`kmv`** (klanker-maker-voice). **Never use "kmk" anywhere.** State prefix `tf-kmv`, SSM namespace `/kmv/...`, bootstrap params `/kmv/bootstrap/*`.

### Module sourcing
- **D-02:** **Copy defcon.run.34 modules verbatim** into `infra/terraform/modules/` (config.hcl + v1.0.0 pattern preserved), changing only naming/config inputs. Prune dead functionality opportunistically in later phases — not during import.
- **D-03:** **Keep the multi-region machinery** (skip_regions, region.hcl derivation, providers/regional.hcl aliases) as-is; us-east-1 is the only active region via config. One simplification allowed: **single-region SOPS KMS key** instead of dc34's multi-region CMK.
- **D-04:** Modules in scope: network, certs, ecs-cluster, ecr, dynamodb, secrets, email, github-oidc, ecs-task, ecs-service, site. Explicitly NOT copied: cloudtrail, waffaw, mqtt, ec2spot, s3-uploads*, cloudfront*, bib/gpx modules.

### State backend & environment
- **D-05:** State backend created by a checked-in **idempotent `scripts/bootstrap-state.sh`** (aws cli, `klanker-terraform` profile): versioned+encrypted S3 bucket and DynamoDB lock table with `tf-kmv` prefix; prints the `TG_BUCKET_USE1`/`TG_TABLE_USE1` exports. Run once, repeatable.
- **D-06:** Non-secret env (bucket/table names, `TF_VAR_APPLICATION_ACCOUNT_ID=052251888500`, `TF_VAR_MANAGEMENT_ACCOUNT_ID=481723467561`, `TF_VAR_profile_prefix=klanker-`) lives in a **checked-in `infra/.envrc`** (direnv). CI mirrors the same values in workflow env. No secrets in .envrc.

### GitHub repo & CI
- **D-07:** Repo is **public: github.com/whereiskurt/klanker-voice**. SOPS keeps secrets safe; quotas assume a public URL anyway.
- **D-08:** CI is **path-filtered push-to-main**: `apps/voice/**` → build/deploy voice, `apps/auth/**` → build/deploy auth, `infra/**` → terragrunt **plan only** with manual apply approval (GitHub environment gate). Infra applies are always human-gated.
- **D-09:** GitHub OIDC roles cloned from the dc34 github-oidc module (readonly/deploy/release pattern), scoped to whereiskurt/klanker-voice.

### SES & email identity
- **D-10:** Magic-link sender is **`sign-in@auth.klankermaker.ai`** — DKIM/SPF scoped under klankermaker.ai, sender domain matches the site the user just visited.
- **D-11:** DMARC for klankermaker.ai: **`p=quarantine`** with SPF+DKIM alignment.
- **D-12:** **SES production access already exists** — the klanker-application account is out of the SES sandbox with an increased quota. INFR-04 reduces to: SES domain identity + DKIM + DMARC records via the email module (cross-account DNS into the management zone). No prod-access request, no external review clock.

### Claude's Discretion
- VPC/subnet sizing, cert SAN layout, ECR retention policies — follow dc34 defaults unless something conflicts with kmv needs.
- Exact GitHub Actions workflow structure (as long as path filtering + infra plan-gate hold).
- Where the WebRTC SG/public-IP knobs land in the network/ecs-service module inputs (Phase 4 verifies; Phase 2 should not block on the aiortc port-range question — wide UDP range per research).

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Source layout to clone
- `/Users/khundeck/working/defcon.run.34/infra/terraform/` — the layout being copied: `providers/{global,regional}.hcl` (backend + provider generation), `live/site/{terragrunt.hcl,site.hcl}`, `live/site/region/us-east-1/*`, `live/site/services/*/service.hcl`, `modules/*/{config.hcl,v1.0.0}`
- `/Users/khundeck/working/defcon.run.34/infra/terraform/live/site/SECRETS.md` and `.secrets.sops.json.template` — SOPS secrets flow being reproduced

### Design & requirements
- `docs/superpowers/specs/2026-07-04-klanker-voice-design.md` — accounts, DNS zone (klankermaker.ai / Z036807010CWM2JH60RKQ in 481723467561), profiles, subdomains
- `.planning/REQUIREMENTS.md` — INFR-01/02/04/05/07 are this phase's contract

### Research
- `.planning/research/ARCHITECTURE.md` — monorepo layout, WebRTC infra notes (STUN srflx approach, wide UDP SG range, Fargate sysctl open question)
- `.planning/research/PITFALLS.md` — UDP/SG range must match what aiortc actually binds; SES/magic-link deliverability notes
- `.planning/research/STACK.md` — pinned tool versions

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- Entire `defcon.run.34/infra/terraform` tree — proven terragrunt skeleton, copied verbatim per D-02.
- dc34 GitHub Actions workflows (referenced for the OIDC deploy pattern).

### Established Patterns
- Versioned modules (`config.hcl` + `v1.0.0`), `{{PLACEHOLDER}}` substitution in config.hcl, service.hcl data files aggregated by site.hcl, env-var-driven backend config, CI=true profile dropping.

### Integration Points
- `/kmv/bootstrap/*` SSM params (user-managed) → migrated into `.secrets.sops.json` once the SOPS KMS key exists; bootstrap params deleted after.
- Phase 3 (auth) and Phase 4 (voice) consume: cluster, ALB/certs, DynamoDB tables, ECR repos, SSM secret paths, OIDC deploy roles.

</code_context>

<specifics>
## Specific Ideas

- The user manages three bootstrap keys at `/kmv/bootstrap/{deepgram,anthropic,elevenlabs}_api_key` (Deepgram done, ElevenLabs in progress); Phase 2 moves them into the SOPS file encrypted with the new KMS key.
- "kmv" naming is a hard constraint from the user — grep for `kmk` should return zero in any deliverable.

</specifics>

<deferred>
## Deferred Ideas

None — discussion stayed within phase scope.

</deferred>

---

*Phase: 2-Infra Skeleton*
*Context gathered: 2026-07-04*
