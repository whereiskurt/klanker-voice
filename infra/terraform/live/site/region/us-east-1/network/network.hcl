locals {
  # Read parent configuration files to access site and region values
  site_vars   = read_terragrunt_config(find_in_parent_folders("site.hcl"))
  region_vars = read_terragrunt_config(find_in_parent_folders("region.hcl"))

  # Phase 12 (D-01, 12-07): the eight Toronto VoIP.ms POP CIDRs, kept in
  # their own data-only stub (telephony-sg.hcl) so the SG's ingress list
  # and its operator re-verification runbook live next to each other.
  telephony_sg_vars = read_terragrunt_config("telephony-sg.hcl")

  # Network-specific configuration for us-east-1
  network = {
    vpc = {
      cidr_block              = "10.0.0.0/16"
      enable_dns_hostnames    = true
      enable_dns_support      = true
      availability_zone_count = 2

      # Public subnets: one per AZ
      public_subnets_cidr = [
        "10.0.1.0/24",
        "10.0.2.0/24"
      ]

      # Private subnets: one per AZ
      private_subnets_cidr = [
        "10.0.10.0/24",
        "10.0.20.0/24"
      ]

      tags = {
        Environment = "production"
        ManagedBy   = "terraform"
      }
    }

    nat_gateway = {
      enabled = true
    }

    vpc_flow_logs = {
      enabled                  = false
      traffic_type             = "ALL"
      max_aggregation_interval = 60
      force_destroy            = true
    }

    vpc_endpoints = {
      enabled = false
    }

    # NOTE: the network module's alb.tf exposes no idle-timeout input
    # (checked during the Phase 2 clone). The WebRTC control-channel
    # idle-timeout bump (>= 2400s) is deferred to Phase 4.
    alb = {
      enabled                    = true
      enable_deletion_protection = false
      ssl_policy                 = "ELBSecurityPolicy-TLS13-1-2-2021-06"
      logs_force_destroy         = true
    }

    # No MQTT / NLB workloads for this site
    nlb = {
      enabled                    = false
      enable_deletion_protection = false
      logs_force_destroy         = true
    }

    # Phase 12 (D-01, T-12-07-01): the telephony-edge security group's
    # ingress allow-list. Empty in every OTHER site (all existing regions
    # keep the default `[]`, which means the module creates the SG with
    # zero ingress rules — see securitygroups.tf's own comment) so this is
    # additive/backward-compatible for every already-deployed region.
    telephony_edge_pop_cidrs = local.telephony_sg_vars.locals.voipms_toronto_pop_cidrs
  }
}
