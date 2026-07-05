# AGENTS.md

This file anchors the terragrunt repo-root lookup: the root
`infra/terraform/live/site/terragrunt.hcl` computes
`repo_root = dirname(find_in_parent_folders("AGENTS.md"))`.

Do not move, rename, or delete this file — every terragrunt run under
`infra/terraform/live/site` fails to parse without it.

Project instructions for AI agents live in `./.claude/CLAUDE.md`.
