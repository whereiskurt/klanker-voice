# Include skip check for regional resources
include "skip" {
  path   = "${find_in_parent_folders("region")}/skip.hcl"
  expose = true
}

# Read site config to check if ecs_services is enabled
locals {
  site_vars = read_terragrunt_config(find_in_parent_folders("site.hcl"))
}

# Exclude if ecs_services is disabled OR if region should be skipped (Terragrunt 0.96+)
exclude {
  if      = !local.site_vars.locals.ecs_services.enabled || include.skip.locals.should_skip
  actions = ["all"]
}

dependency "ecs_task" {
  config_path = "../ecs-task"

  mock_outputs = {
    task_definition_arns = {
      "auth"  = "arn:aws:ecs:us-east-1:123456789012:task-definition/auth-use1-example-site:1"
      "voice" = "arn:aws:ecs:us-east-1:123456789012:task-definition/voice-use1-example-site:1"
    }
  }
  mock_outputs_allowed_terraform_commands = ["init", "validate", "plan", "destroy"]
}

dependency "ecs_cluster" {
  config_path = "../ecs-cluster"

  mock_outputs = {
    clusters = {
      "app" = {
        cluster_id     = "arn:aws:ecs:us-east-1:123456789012:cluster/app-use1-example-site"
        cluster_name   = "app-use1-example-site"
        cluster_arn    = "arn:aws:ecs:us-east-1:123456789012:cluster/app-use1-example-site"
        namespace_id   = "ns-mock"
        namespace_name = "app-use1-example-site.local"
      }
    }
  }
  mock_outputs_allowed_terraform_commands = ["init", "validate", "plan", "destroy"]
}

dependency "certs" {
  config_path = "../certs"

  mock_outputs = {
    cert_map = {
      "voice.klankermaker.ai" = {
        arn                       = "arn:aws:acm:us-east-1:123456789012:certificate/mock-voice-cert"
        domain_name               = "voice.klankermaker.ai"
        subject_alternative_names = ["*.voice.klankermaker.ai"]
        validation_method         = "DNS"
      }
    }
  }
  mock_outputs_allowed_terraform_commands = ["init", "validate", "plan", "destroy"]
}

dependency "network" {
  config_path = "../network"

  mock_outputs = {
    vpc_id                           = "vpc-mock123"
    private_subnet_ids               = ["subnet-private1", "subnet-private2"]
    public_subnet_ids                = ["subnet-public1", "subnet-public2"]
    security_group_ids               = ["sg-mock123"]
    telephony_edge_security_group_id = "sg-mock-telephony-edge"
    alb_arn                          = "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/mock-alb/abc123"
    alb_listener_arn                 = "arn:aws:elasticloadbalancing:us-east-1:123456789012:listener/app/mock-alb/abc123/def456"
    nlb_arn                          = "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/net/mock-nlb/abc123"
  }
  mock_outputs_allowed_terraform_commands = ["init", "validate", "plan", "destroy"]
}

include "module" {
  path   = "${find_in_parent_folders("modules")}/ecs-service/config.hcl"
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
  {
    # Task definitions from ecs-task module
    task_definitions = dependency.ecs_task.outputs.task_definition_arns

    # Cluster information from ecs-cluster module
    clusters = dependency.ecs_cluster.outputs.clusters

    # Network resources from network module
    vpc_id             = dependency.network.outputs.vpc_id
    private_subnet_ids = dependency.network.outputs.private_subnet_ids
    public_subnet_ids  = dependency.network.outputs.public_subnet_ids
    security_group_ids = dependency.network.outputs.security_group_ids
    alb_arn            = try(dependency.network.outputs.alb_arn, "")
    alb_listener_arn   = try(dependency.network.outputs.alb_listener_arn, "")
    nlb_arn            = try(dependency.network.outputs.nlb_arn, "")

    # Phase 12 (D-01, T-12-07-01): telephony-edge gets its OWN POP-locked
    # security group instead of the shared security_group_ids list above
    # (which includes webrtc_udp, 0.0.0.0/0 on UDP 20000-20100 — attaching
    # that here would defeat the entire POP-lock). See
    # network/v1.0.0's telephony_edge_security_group_id output and
    # ecs-service/v1.0.0's security_group_overrides variable.
    security_group_overrides = {
      "telephony-edge" = [dependency.network.outputs.telephony_edge_security_group_id]
    }

    # Default certificate for NLB TLS listeners — no NLB/MQTT for this site;
    # try() falls back to "" when no matching cert exists (safe no-op)
    nlb_default_certificate_arn = try(dependency.certs.outputs.cert_map["mqtt.${local.site_vars.locals.dns.zonename}"].arn, "")
  }
)
