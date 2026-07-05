# Apex DMARC inline unit (tiny live-tree Terraform unit, no versioned module).
# The email module only writes per-identity DMARC records; the org-domain
# policy for the management zone lives here so the apex never grows an SES
# receive MX (make_site_domain stays false in site.hcl).
include "skip" {
  path   = "${find_in_parent_folders("region")}/skip.hcl"
  expose = true
}

exclude {
  if      = include.skip.locals.should_skip
  actions = ["all"]
}

include "providers" {
  path = "${find_in_parent_folders("providers")}/regional.hcl"
}

locals {
  site_vars = read_terragrunt_config(find_in_parent_folders("site.hcl"))
}

terraform {
  source = "."
}

inputs = {
  dns = local.site_vars.locals.dns
}
