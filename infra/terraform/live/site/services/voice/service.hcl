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

  # DynamoDB tables for the voice service. kmv-voice-usage (Phase 4, QUOT
  # groundwork): electro-type single table for the heartbeat-lease
  # concurrency slot, daily per-user usage, global rollup, and kill-switch
  # control items (04-04 defines the item shapes); TTL on expiresAt lets
  # crashed-task heartbeats self-clean (D-01, T-04-14).
  dynamodb = {
    tables = [
      {
        table_name          = "kmv-voice-usage"
        table_type          = "electro"
        ttl_enabled         = true
        ttl_attribute_name  = "expiresAt"
        replica_regions = [
          {
            label = "use1"
            full  = "us-east-1"
          }
        ]
      }
    ]
  }

  # Least-privilege voice task-role IAM (T-04-13): usage-table-only DynamoDB
  # CRUD, namespaced CloudWatch metric publish, cluster-scoped ECS task
  # protection, and region-conditioned EC2 ENI lookup (needed by 04-01's
  # webrtc.py public-IP lookup). No table-wide or account-wide grants.
  task_role_iam_statements = [
    {
      sid     = "UsageTableCrud"
      actions = ["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:UpdateItem", "dynamodb:Query"]
      resources = [
        "arn:aws:dynamodb:*:*:table/kmv-voice-usage",
        "arn:aws:dynamodb:*:*:table/kmv-voice-usage/index/*"
      ]
    },
    {
      # Phase 4 (04-04/04-06 reconciliation): quota.read_tier() reads tier limits
      # (session_max / daily / concurrency) from the Phase-3 tiers table at
      # session start (thin-token design D-01). Read-only, tiers table only.
      sid     = "TiersTableRead"
      actions = ["dynamodb:GetItem", "dynamodb:Query"]
      resources = [
        "arn:aws:dynamodb:*:*:table/kmv-auth-electro",
        "arn:aws:dynamodb:*:*:table/kmv-auth-electro/index/*"
      ]
    },
    {
      sid       = "SessionMetricsPublish"
      actions   = ["cloudwatch:PutMetricData"]
      resources = ["*"]
      condition = {
        test     = "StringEquals"
        variable = "cloudwatch:namespace"
        values   = ["klanker-voice/ecs"]
      }
    },
    {
      sid       = "TaskScaleInProtection"
      actions   = ["ecs:UpdateTaskProtection", "ecs:DescribeTasks"]
      resources = ["arn:aws:ecs:*:*:task/app-use1-kmv/*"]
    },
    {
      sid       = "PublicIpEniLookup"
      actions   = ["ec2:DescribeNetworkInterfaces"]
      resources = ["*"]
      condition = {
        test     = "StringEquals"
        variable = "aws:RequestedRegion"
        values   = ["us-east-1"]
      }
    },
    {
      # Phase 15 (15-04, T-15-04-02): write-only ledger access — the batched
      # LedgerWriter (ledger.py) PUTs newline-JSON records under ledger/ only.
      # No s3:GetObject/ListBucket/DeleteObject — voice never reads or deletes
      # its own transcripts (append-only posture, T-15-04-03). Wildcarded
      # bucket-name prefix because the ledger module generates a random
      # suffix at apply time (same convention as the release role's
      # "${site.label}-*" S3 grant in site.hcl).
      sid       = "LedgerPutOnly"
      actions   = ["s3:PutObject"]
      resources = ["arn:aws:s3:::kmv-ledger-*/ledger/*"]
    }
  ]

  # Placeholder task definition — unused while site.hcl ecs_tasks.enabled = false.
  # Phase 4 fills real containers; image tag stays a hardcoded string.
  task = {
    name         = "voice"
    regions      = ["us-east-1"]
    cluster_name = "app"
    task_cpu     = 1024
    task_memory  = 2048

    # T-04-13: dedicated least-privilege task role (see task_role_iam_statements
    # above) — the ecs-task module creates it instead of using the shared
    # per-cluster role when this is non-empty.
    task_role_policy_statements = local.task_role_iam_statements

    containers = [
      {
        name      = "voice-app"
        # Image tag is env-driven so the CI/OIDC deploy path (deploy.yml) can
        # deploy the immutable ${github.sha} image it just built, while local /
        # manual `terragrunt apply` falls back to the last known-good preview tag.
        # (Was a hardcoded string; TF_VAR_VOICE_IMAGE_TAG closes the build/deploy
        # tag mismatch — build-voice.yml pushes :sha but deploy.yml applied :tag.)
        image     = "voice-app:${get_env("TF_VAR_VOICE_IMAGE_TAG", "288f4bcc69438d7374ff1e7af4debf7c22f62a62")}"
        cpu       = 1024
        memory    = 2048
        essential = true

        environment = [
          {
            name  = "VOICE_PUBLIC_URL"
            value = "https://voice.{{SITE_DOMAIN}}"
          }
          # NOTE: KMV_LEDGER_BUCKET is NOT listed here — this file must stay
          # pure data (see header), but the ledger bucket name is only known
          # after terraform creates it (random-suffixed S3 bucket, Phase 15
          # 15-04). It is injected at the region/us-east-1/ecs-task unit via
          # a `dependency "ledger"` block reading the ledger unit's
          # `bucket_name` output — mirrors the existing task_role_arn
          # injection from the ecs_cluster dependency in that same file.
        ]

        # Phase 4 (04-03 deploy checkpoint): inject the pipeline provider keys and
        # the KV-05 smoke/service credential from SSM SecureStrings (D-09 no-secrets-
        # in-code; execution role has ssm:GetParameters + kms:Decrypt). Issuer / JWKS
        # URI / audience / STUN / config-path all default correctly in-app, so only
        # these four need wiring. smoke_token is provisioned out-of-band (like the
        # SOPS-seeded provider keys), not terraform-managed.
        secrets = [
          {
            name      = "DEEPGRAM_API_KEY"
            valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/deepgram/api_key"
          },
          {
            name      = "ANTHROPIC_API_KEY"
            valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/anthropic/api_key"
          },
          {
            name      = "ELEVENLABS_API_KEY"
            valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/elevenlabs/api_key"
          },
          {
            name      = "KMV_SMOKE_SERVICE_TOKEN"
            valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/voice/smoke_token"
          },
          {
            # Phase 15 (15-04, T-15-04-04): the code_hash HMAC salt — SOPS ->
            # secrets module -> SSM SecureString -> valueFrom. Never in code
            # or a TOML; the raw value lives ONLY in .secrets.sops.json
            # (encrypted).
            name      = "KMV_LEDGER_SALT"
            valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/ledger/code_hash_salt"
          }
        ]

        port_mappings = [
          {
            container_port = 7860
            host_port      = 7860
          }
        ]

        # D-12/T-04-06: pin aiortc's ephemeral UDP bind range to exactly the
        # webrtc_udp security group's narrowed 20000-20100 window.
        system_controls = [
          {
            namespace = "net.ipv4.ip_local_port_range"
            value     = "20000 20100"
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

    # D-13: session-count autoscaling (1->4) on a custom ActiveSessions
    # CloudWatch metric; each task calls ecs:UpdateTaskProtection to shield
    # itself while it holds an active session (04-04 wires the emission +
    # protection calls). No CPU/memory policy — sessions are long-lived and
    # fairly fixed per-task CPU, so session count is the right signal.
    autoscaling = {
      enabled      = true
      min_capacity = 1
      max_capacity = 4
      custom_metric_target = {
        metric_name        = "ActiveSessions"
        namespace          = "klanker-voice/ecs"
        target_value       = 2
        statistic          = "Average"
        dimensions         = { Service = "voice" }
        scale_out_cooldown = 60
        scale_in_cooldown  = 120
      }
    }
  }
}
