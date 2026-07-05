# Data-only service stub for the auth service (magic-link / OIDC identity).
# site.hcl reads this at parse time for every unit, so this file must stay
# pure data: no filesystem reads, no VERSION lookups — image tags are
# hardcoded strings until Phase 3 wires the real task/service definitions.
locals {
  # ECR repositories for this service (created now so Phase 3 CI can push)
  ecr_repositories = [
    {
      name                 = "auth-app"
      regions              = ["us-east-1"]
      image_tag_mutability = "IMMUTABLE"
      lifecycle_policy = {
        max_image_count = 10
        expire_days     = 30
      }
    }
  ]

  # DynamoDB tables for the auth service (Plan 03-01): two physical tables —
  # @auth/dynamodb-adapter needs its own "nextauth"-schema table (GSI1PK/
  # GSI1SK key convention), separate from the "electro"-schema single table
  # that all ElectroDB entities (oidc-adapter, auth-profile, and Plan 03-02's
  # tiers/access_codes) share. The DEF CON quota table is NOT ported (D-11) —
  # Phase 4 adds a usage table against the design-spec schema.
  dynamodb = {
    tables = [
      {
        table_name = "kmv-auth-authjs"
        table_type = "nextauth"
        replica_regions = [
          {
            label = "use1"
            full  = "us-east-1"
          }
        ]
      },
      {
        table_name = "kmv-auth-electro"
        table_type = "electro"
        replica_regions = [
          {
            label = "use1"
            full  = "us-east-1"
          }
        ]
      }
    ]
  }

  # Placeholder task definition — unused while site.hcl ecs_tasks.enabled = false.
  # Phase 3 fills real containers; image tag stays a hardcoded string.
  task = {
    name         = "auth"
    regions      = ["us-east-1"]
    cluster_name = "app"
    task_cpu     = 256
    task_memory  = 512

    containers = [
      {
        name      = "auth-app"
        image     = "auth-app:0.0.0"
        cpu       = 256
        memory    = 512
        essential = true

        environment = [
          {
            name  = "AUTH_PUBLIC_URL"
            value = "https://auth.{{SITE_DOMAIN}}"
          }
        ]

        port_mappings = [
          {
            container_port = 3000
            host_port      = 3000
          }
        ]
      }
    ]
  }

  # Placeholder service definition — unused while site.hcl ecs_services.enabled = false.
  service = {
    name          = "auth"
    regions       = ["us-east-1"]
    cluster_name  = "app"
    task_family   = "auth"
    desired_count = 1

    assign_public_ip = false

    load_balancers = [
      {
        type                  = "alb"
        container_name        = "auth-app"
        container_port        = 3000
        target_group_protocol = "HTTP"
        health_check_path     = "/api/health"

        listener = {
          port         = 443
          protocol     = "HTTPS"
          host_headers = ["auth.{{SITE_DOMAIN}}", "*.auth.{{SITE_DOMAIN}}"]
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
