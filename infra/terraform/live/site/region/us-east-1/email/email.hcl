locals {
  # Read parent configuration files to access site and region values
  site_vars   = read_terragrunt_config(find_in_parent_folders("site.hcl"))
  region_vars = read_terragrunt_config(find_in_parent_folders("region.hcl"))

  # Extract values for easier reference
  dns_zonename = local.site_vars.locals.dns.zonename
  region_label = local.region_vars.locals.region.label

  # No region-level SMTP users beyond the site-level auth.<zone> user
  smtp_iam_users = []

  # Minimal forwarding: only when a destination address is configured
  fwd_rules = get_env("TF_VAR_FWD_EMAIL_TO_ADDRESS", "") != "" ? [
    {
      match   = "auth.${local.dns_zonename}"
      send_to = get_env("TF_VAR_FWD_EMAIL_TO_ADDRESS", "")
    },
  ] : []

  # Path to email forwarder Lambda source code
  forwarder_lambda_source_path = "${get_repo_root()}/infra/terraform/live/site/region/us-east-1/email/lambdas/email-forwarder"
}
