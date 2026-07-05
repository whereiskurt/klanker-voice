# Data-only service stub for the voice service (Pipecat speech-to-speech).
# site.hcl reads this at parse time for every unit, so this file must stay
# pure data: no filesystem reads, no VERSION lookups — image tags are
# hardcoded strings until Phase 4 wires the real task/service definitions.
locals {
  # ECR repositories for this service (created now so Phase 4 CI can push)
  ecr_repositories = [
    {
      name                 = "voice-app"
      regions              = ["us-east-1"]
      image_tag_mutability = "IMMUTABLE"
      lifecycle_policy = {
        max_image_count = 10
        expire_days     = 30
      }
    }
  ]

  # DynamoDB tables for the voice service (Phase 4 adds tiers/usage tables)
  dynamodb = {
    tables = []
  }

  # Placeholder task definition — unused while site.hcl ecs_tasks.enabled = false.
  # Phase 4 fills real containers; image tag stays a hardcoded string.
  task = {
    name         = "voice"
    regions      = ["us-east-1"]
    cluster_name = "app"
    task_cpu     = 1024
    task_memory  = 2048

    containers = [
      {
        name      = "voice-app"
        image     = "voice-app:0.0.0"
        cpu       = 1024
        memory    = 2048
        essential = true

        environment = [
          {
            name  = "VOICE_PUBLIC_URL"
            value = "https://voice.{{SITE_DOMAIN}}"
          }
        ]

        port_mappings = [
          {
            container_port = 7860
            host_port      = 7860
          }
        ]
      }
    ]
  }

  # Placeholder service definition — unused while site.hcl ecs_services.enabled = false.
  service = {
    name          = "voice"
    regions       = ["us-east-1"]
    cluster_name  = "app"
    task_family   = "voice"
    desired_count = 1

    # WebRTC groundwork: media flows UDP direct browser<->task, so the task
    # runs in public subnets with a public IP (signaling stays on the ALB).
    assign_public_ip = true

    load_balancers = [
      {
        type                  = "alb"
        container_name        = "voice-app"
        container_port        = 7860
        target_group_protocol = "HTTP"
        health_check_path     = "/health"

        listener = {
          port         = 443
          protocol     = "HTTPS"
          host_headers = ["voice.{{SITE_DOMAIN}}"]
        }
      }
    ]

    autoscaling = {
      enabled      = false
      min_capacity = 1
      max_capacity = 2
    }
  }
}
