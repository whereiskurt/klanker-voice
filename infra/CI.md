# CI for infra/

Changes under `infra/**` are path-filtered into CI per D-08:

- **Pull requests and pushes to main** trigger `terragrunt-plan.yml` — a read-only
  `terragrunt run plan --all` using the `kmv-github-readonly` OIDC role
  (environment `terraform-plan`, no gate). No long-lived AWS keys exist in CI.
- **Applies never run automatically.** `terragrunt-apply.yml` is
  `workflow_dispatch`/`workflow_call` only and runs in the `terraform-apply`
  environment, which requires human reviewer approval before the
  `kmv-github-terragrunt` role is ever assumed.
- App image builds/deploys are separate: `apps/voice/**` → `build-voice.yml`,
  `apps/auth/**` → `build-auth.yml`, each pushing to ECR via `kmv-github-release`
  and rolling ECS via `deploy.yml` (`kmv-github-deploy`).

Tool pins in CI: terragrunt 0.97.1, terraform 1.14.3, sops 3.11.0 — keep local
versions aligned (see `infra/.envrc` for the non-secret env contract CI mirrors).
