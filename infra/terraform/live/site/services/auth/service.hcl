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

  # Least-privilege auth task-role IAM (Phase 5 deploy): full CRUD on the two
  # auth tables (@auth/dynamodb-adapter nextauth table + ElectroDB electro
  # single-table that oidc-adapter/tiers/access_codes share) and read on the
  # voice usage table. SES email is sent via SMTP credentials (secrets below),
  # NOT the task role, so no ses:* is granted here.
  task_role_iam_statements = [
    {
      sid = "AuthTablesCrud"
      actions = [
        "dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:UpdateItem",
        "dynamodb:DeleteItem", "dynamodb:Query", "dynamodb:Scan",
        "dynamodb:BatchGetItem", "dynamodb:BatchWriteItem", "dynamodb:ConditionCheckItem"
      ]
      resources = [
        "arn:aws:dynamodb:*:*:table/kmv-auth-authjs",
        "arn:aws:dynamodb:*:*:table/kmv-auth-authjs/index/*",
        "arn:aws:dynamodb:*:*:table/kmv-auth-electro",
        "arn:aws:dynamodb:*:*:table/kmv-auth-electro/index/*"
      ]
    },
    {
      sid       = "VoiceUsageRead"
      actions   = ["dynamodb:GetItem", "dynamodb:Query"]
      resources = [
        "arn:aws:dynamodb:*:*:table/kmv-voice-usage",
        "arn:aws:dynamodb:*:*:table/kmv-voice-usage/index/*"
      ]
    }
  ]

  # Real task definition (Phase 5 deploy). Image tag is env-driven (deploy.yml
  # sets TF_VAR_AUTH_IMAGE_TAG to the just-built :sha), same pattern as voice.
  task = {
    name         = "auth"
    regions      = ["us-east-1"]
    cluster_name = "app"
    task_cpu     = 512
    task_memory  = 1024

    task_role_policy_statements = local.task_role_iam_statements

    containers = [
      {
        name      = "auth-app"
        image     = "auth-app:${get_env("TF_VAR_AUTH_IMAGE_TAG", "a75939496904e4ed02098a991a9781dd45f799f9")}"
        cpu       = 512
        memory    = 1024
        essential = true

        # Next.js standalone (`node server.js`) binds PORT on HOSTNAME — 0.0.0.0
        # is required for the ALB health check to reach the container. Issuer
        # resolves from AUTH_PUBLIC_URL as https://auth.<domain>/use1/api/oidc,
        # which MUST equal the SPA's baked VITE_OIDC_ISSUER and the voice
        # server's expected issuer. DynamoDB/SES use the task role + SMTP secrets
        # (no static AUTH_*_ID/SECRET/ENDPOINT -> SDK default chain = task role).
        environment = [
          { name = "NODE_ENV", value = "production" },
          { name = "HOSTNAME", value = "0.0.0.0" },
          { name = "PORT", value = "3000" },
          { name = "AWS_REGION", value = "us-east-1" },
          { name = "REGION_SHORT", value = "use1" },
          { name = "SITE_DOMAIN", value = "{{SITE_DOMAIN}}" },
          { name = "AUTH_PUBLIC_URL", value = "https://auth.{{SITE_DOMAIN}}/use1" },
          { name = "NEXTAUTH_URL", value = "https://auth.{{SITE_DOMAIN}}/use1" },
          { name = "AUTH_COOKIE_DOMAIN", value = "auth.{{SITE_DOMAIN}}" },
          { name = "AUTH_DYNAMODB_DBNAME", value = "kmv-auth-authjs" },
          { name = "AUTH_ELECTRO_DBNAME", value = "kmv-auth-electro" },
          { name = "AUTH_VOICE_USAGE_DBNAME", value = "kmv-voice-usage" },
          { name = "AUTH_SES_REGION", value = "us-east-1" },
          { name = "AUTH_SES_SMTP_FROM", value = "no-reply@auth.{{SITE_DOMAIN}}" },
          { name = "OIDC_VOICE_CLIENT_ID", value = "voice" },
          { name = "OIDC_VOICE_SECRET", value = "unused-public-pkce-client" }
        ]

        secrets = [
          { name = "AUTH_JWT_SECRET", valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/jwt/secret" },
          { name = "OIDC_COOKIE_KEYS", valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/oidc/cookie_keys" },
          { name = "OIDC_JWKS", valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/oidc/jwks" },
          { name = "ALTCHA_HMAC_KEY", valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/altcha/secret" },
          { name = "AUTH_SES_ACCESS_KEY_ID", valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/ses/smtp/default/auth.klankermaker.ai/username" },
          { name = "AUTH_SES_SECRET_ACCESS_KEY", valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/ses/smtp/default/auth.klankermaker.ai/password" }
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
