# Global CloudFront distribution for voice.<zone> (full-front: S3 SPA origin +
# ALB origin for /api/*,/health). us-east-1-pinned (global provider) because a
# CloudFront distribution + its ACM cert are global/us-east-1 resources.
#
# Multi-region pattern (Kurt's defcon.run.34 shape): the use1 asset bucket is a
# real dependency; ca-central-1 / ap-southeast-1 are pre-wired as mock buckets
# (skip-excluded regional units) so a region can be lit up later by removing it
# from site.skip_regions + adding it to cloudfront.regions — no restructuring.
# Only the primary (use1) region is wired as an actual origin today (flat
# routing); the cac1/apse1 entries ride along inert via try().

locals {
  site_vars = read_terragrunt_config(find_in_parent_folders("site.hcl"))
  _zone     = local.site_vars.locals.dns.zonename
  _subs     = local.site_vars.locals.dns.subdomains
  _cf_doms  = local.site_vars.locals.cloudfront.domains
}

# Exclude if cloudfront is disabled (Terragrunt 0.96+)
exclude {
  if      = !local.site_vars.locals.cloudfront.enabled
  actions = ["all"]
}

# --- Regional asset buckets (S3 origins) ---

dependency "use1_cloudfront" {
  config_path = "../../region/us-east-1/cloudfront"

  mock_outputs = {
    bucket_ids = {
      voice = "mock-cf-assets-voice-use1"
    }
    bucket_arns = {
      voice = "arn:aws:s3:::mock-cf-assets-voice-use1"
    }
    bucket_regional_domain_names = {
      voice = "mock-cf-assets-voice-use1.s3.us-east-1.amazonaws.com"
    }
    region_label = "use1"
  }
  mock_outputs_allowed_terraform_commands = ["init", "validate", "plan", "destroy"]
}

dependency "cac1_cloudfront" {
  config_path = "../../region/ca-central-1/cloudfront"

  mock_outputs = {
    bucket_ids = {
      voice = "mock-cf-assets-voice-cac1"
    }
    bucket_arns = {
      voice = "arn:aws:s3:::mock-cf-assets-voice-cac1"
    }
    bucket_regional_domain_names = {
      voice = "mock-cf-assets-voice-cac1.s3.ca-central-1.amazonaws.com"
    }
    region_label = "cac1"
  }
  mock_outputs_allowed_terraform_commands = ["init", "validate", "plan", "destroy", "apply"]
  mock_outputs_merge_strategy_with_state  = "shallow"
}

dependency "apse1_cloudfront" {
  config_path = "../../region/ap-southeast-1/cloudfront"

  mock_outputs = {
    bucket_ids = {
      voice = "mock-cf-assets-voice-apse1"
    }
    bucket_arns = {
      voice = "arn:aws:s3:::mock-cf-assets-voice-apse1"
    }
    bucket_regional_domain_names = {
      voice = "mock-cf-assets-voice-apse1.s3.ap-southeast-1.amazonaws.com"
    }
    region_label = "apse1"
  }
  mock_outputs_allowed_terraform_commands = ["init", "validate", "plan", "destroy", "apply"]
  mock_outputs_merge_strategy_with_state  = "shallow"
}

# --- Primary-region ALB (the /api/*,/health origin) ---

dependency "use1_network" {
  config_path = "../../region/us-east-1/network"

  mock_outputs = {
    alb_dns_name = "mock-alb-use1.us-east-1.elb.amazonaws.com"
    alb_zone_id  = "Z35SXDOTRQ7X7K"
  }
  mock_outputs_allowed_terraform_commands = ["init", "validate", "plan", "destroy"]
}

# --- Route53 zones (for the A-alias) ---

dependency "site" {
  config_path = "../.."

  mock_outputs = {
    zone_map = merge(
      {
        (local._zone) = {
          zone_id = "Z1234567890ABC"
          name    = local._zone
        }
      },
      {
        for i, sub in local._subs :
        "${sub}.${local._zone}" => {
          zone_id = format("Z1234567890AB%s", upper(substr("defghijklmnop", i, 1)))
          name    = "${sub}.${local._zone}"
        }
      }
    )
  }
  mock_outputs_allowed_terraform_commands = ["init", "validate", "plan", "destroy"]
}

# --- us-east-1 ACM cert (CloudFront requires the cert in us-east-1) ---

dependency "use1_certs" {
  config_path = "../../region/us-east-1/certs"

  mock_outputs = {
    cert_map = {
      for dom in local._cf_doms :
      "${dom}.${local._zone}" => {
        arn = "arn:aws:acm:us-east-1:123456789012:certificate/mock-cert-${dom}-id"
      }
    }
  }
  mock_outputs_allowed_terraform_commands = ["init", "validate", "plan", "destroy"]
}

include "module" {
  path   = "${find_in_parent_folders("modules")}/cloudfront/config.hcl"
  expose = true
}

include "providers" {
  path = "${find_in_parent_folders("providers")}/global.hcl"
}

terraform {
  source = "${include.module.locals.module_path}/v1.0.0"
}

inputs = merge(
  local.site_vars.locals,
  {
    # Per-domain regional origins. Flat routing consumes only the primary
    # (use1) region: its ALB (from network) + its S3 asset bucket. cac1/apse1
    # carry the mock bucket outputs with empty ALB fields (no regional ALB
    # today) so they stay inert until lit up.
    regional_origins_by_domain = {
      for domain in local.site_vars.locals.cloudfront.domains : domain => {
        use1 = {
          alb_dns_name                   = try(dependency.use1_network.outputs.alb_dns_name, "")
          alb_zone_id                    = try(dependency.use1_network.outputs.alb_zone_id, "")
          s3_bucket_id                   = dependency.use1_cloudfront.outputs.bucket_ids[domain]
          s3_bucket_arn                  = dependency.use1_cloudfront.outputs.bucket_arns[domain]
          s3_bucket_regional_domain_name = dependency.use1_cloudfront.outputs.bucket_regional_domain_names[domain]
        }
        cac1 = {
          alb_dns_name                   = ""
          alb_zone_id                    = ""
          s3_bucket_id                   = try(dependency.cac1_cloudfront.outputs.bucket_ids[domain], "")
          s3_bucket_arn                  = try(dependency.cac1_cloudfront.outputs.bucket_arns[domain], "")
          s3_bucket_regional_domain_name = try(dependency.cac1_cloudfront.outputs.bucket_regional_domain_names[domain], "")
        }
        apse1 = {
          alb_dns_name                   = ""
          alb_zone_id                    = ""
          s3_bucket_id                   = try(dependency.apse1_cloudfront.outputs.bucket_ids[domain], "")
          s3_bucket_arn                  = try(dependency.apse1_cloudfront.outputs.bucket_arns[domain], "")
          s3_bucket_regional_domain_name = try(dependency.apse1_cloudfront.outputs.bucket_regional_domain_names[domain], "")
        }
      }
    }

    # Route53 zone map for the A-alias
    zone_map = dependency.site.outputs.zone_map

    # ACM cert map from us-east-1 certs (CloudFront requires cert in us-east-1)
    cert_map = dependency.use1_certs.outputs.cert_map

    # WAF disabled for this site
    waf_web_acl_arns = {}

    tags = {
      Environment = local.site_vars.locals.site.label
      ManagedBy   = "Terragrunt"
      Purpose     = "CloudFront"
    }
  }
)
