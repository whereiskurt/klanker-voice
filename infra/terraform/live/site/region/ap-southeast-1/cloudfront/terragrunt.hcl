# Regional CloudFront asset bucket (the private S3 origin CloudFront serves the
# SPA from). Region comes from region.hcl; only us-east-1 is live today.
# ca-central-1 / ap-southeast-1 have identical units that stay skip-excluded
# (site.skip_regions) — the pre-wired mock buckets for future multi-region.

include "skip" {
  path   = "${find_in_parent_folders("region")}/skip.hcl"
  expose = true
}

locals {
  site_vars = read_terragrunt_config(find_in_parent_folders("site.hcl"))
}

# Exclude if CloudFront is disabled OR if this region is skipped (Terragrunt 0.96+)
exclude {
  if      = !local.site_vars.locals.cloudfront.enabled || include.skip.locals.should_skip
  actions = ["all"]
}

include "module" {
  path   = "${find_in_parent_folders("modules")}/cloudfront-assets/config.hcl"
  expose = true
}

include "providers" {
  path = "${find_in_parent_folders("providers")}/regional.hcl"
}

terraform {
  source = "${include.module.locals.module_path}/v1.0.0"
}

inputs = merge(
  include.module.locals.merged_inputs,
  {}
)
