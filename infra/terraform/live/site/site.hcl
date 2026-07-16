locals {

  site = {
    label            = "kmv"
    github_repo_name = "klanker-voice"
    tf_state_prefix  = "tf-kmv"
    random_suffix    = get_env("SGUID", "6e913c73")
    skip_regions     = ["ap-southeast-1", "ca-central-1"]
  }

  secret_values = jsondecode(
    # Try SOPS encrypted file first (decrypt on the fly)
    fileexists("${get_terragrunt_dir()}/.secrets.sops.json")
    ? run_cmd("--terragrunt-quiet", "sops", "--decrypt", "${get_terragrunt_dir()}/.secrets.sops.json")
    # Fall back to plaintext file
    : fileexists("${get_terragrunt_dir()}/.secrets.json")
    ? file("${get_terragrunt_dir()}/.secrets.json")
    : "{}"
  )

  dns = {
    zonename   = "klankermaker.ai"
    subdomains = ["auth", "voice"]
    ttl        = 300
  }

  # URL configuration for services
  # These values are used to generate environment variables for containers
  # and can be referenced in service.hcl files
  urls = {
    # Service subdomains (combined with dns.zonename to form full domains)
    # e.g., "auth" + "klankermaker.ai" = "auth.klankermaker.ai"
    subdomains = {
      "auth"  = "auth"
      "voice" = "voice"
    }

    # Local development ports (for .env.local files and development defaults)
    local_ports = {
      auth  = 3002
      voice = 7860
    }

    # Service discovery namespace pattern (used for internal container communication)
    # {{REGION_LABEL}} is substituted at deployment time (e.g., use1)
    service_namespace = "app-{{REGION_LABEL}}-${local.site.label}.local"
  }

  # Load service definitions from live/site/services/
  service_conf = {
    auth           = read_terragrunt_config("./services/auth/service.hcl")
    voice          = read_terragrunt_config("./services/voice/service.hcl")
    telephony_edge = read_terragrunt_config("./services/telephony-edge/service.hcl")
  }

  email = {
    enabled        = true
    primary_region = "us-east-1"
    zonenames      = ["auth.${local.dns.zonename}"]
    smtp_prefix    = "s"

    # Route 2 (zone audit): the org-domain DMARC record ships via the
    # region/us-east-1/dmarc inline unit. Enabling the apex identity here
    # would hardcode an apex receive MX and take over inbound mail for the
    # whole zone — keep this false.
    make_site_domain      = false
    make_regional_domains = false
    make_domains          = true

    # Single-region: no cross-region S3 bucket replication
    replica_regions = [
      {
        label = "use1"
        full  = "us-east-1"
      }
    ]

    smtp_iam_users = ["auth.${local.dns.zonename}"]

    # Region-level forwarding rules live in region/us-east-1/email/email.hcl
    # (single source — the module concats site- and region-level lists).
    fwd_rules = []
  }

  waf = {
    enabled  = false
    log_mode = "standard" # standard | realtime
  }

  cloudfront = {
    # Full-front CloudFront for voice.<zone>: a single distribution serves the
    # Vite SPA from a retained private S3 bucket (default behavior) and routes
    # /api/*,/health to the ALB origin. Durable fix for multi-build asset skew.
    # See docs/superpowers/specs/2026-07-07-cloudfront-static-assets-design.md.
    enabled = true

    domains      = ["voice"]
    waf_rulesets = {}

    # Only us-east-1 is live. ca-central-1 / ap-southeast-1 stay in
    # site.skip_regions with pre-wired mock asset buckets; lighting one up =
    # remove it from skip_regions AND add it here.
    regions = [
      { label = "use1", full = "us-east-1" }
    ]

    logging = {
      enabled         = false
      include_cookies = false
    }

    price_class = "PriceClass_100"
  }

  ec2spots = {
    enabled   = false
    instances = []
  }

  ecs_clusters = {
    enabled = true
    clusters = [
      {
        name            = "app"
        regions         = ["us-east-1"]
        enable_insights = false
        cluster_type    = "FARGATE"
      }
    ]
  }

  dynamodb = {
    enabled = true
    tables = concat(
      local.service_conf.auth.locals.dynamodb.tables,
      local.service_conf.voice.locals.dynamodb.tables
    )
  }

  ecr = {
    enabled = true
    repositories = concat(
      local.service_conf.auth.locals.ecr_repositories,
      local.service_conf.voice.locals.ecr_repositories,
      local.service_conf.telephony_edge.locals.ecr_repositories
    )
  }

  ecs_tasks = {
    # Phase 4 (04-02): voice task. Phase 5 deploy: auth task added — the auth
    # identity service is stood up so the browser client's OIDC sign-in works.
    # Phase 12 (12-07): telephony-edge task added — the deployed Asterisk
    # PSTN edge (D-01/D-04).
    enabled        = true
    enable_logging = true
    tasks          = [local.service_conf.voice.locals.task, local.service_conf.auth.locals.task, local.service_conf.telephony_edge.locals.task]
  }

  ecs_services = {
    # Phase 4 (04-02): voice service. Phase 5 deploy: auth service added.
    # Phase 12 (12-07): telephony-edge service added.
    enabled  = true
    services = [local.service_conf.voice.locals.service, local.service_conf.auth.locals.service, local.service_conf.telephony_edge.locals.service]
  }

  # Cross-regional secrets (provider API keys, JWT secrets, etc.)
  # Values loaded from .secrets.sops.json (encrypted) or .secrets.json fallback
  secrets = {
    enabled = true

    # Set to true to use Secrets Manager with automatic replication
    # Set to false to use SSM Parameter Store (created in each region)
    use_secrets_manager = false

    # Primary region for Secrets Manager (only used when use_secrets_manager = true)
    primary_region = "us-east-1"

    # Single-region site: no replicas
    replica_regions = []

    # Path prefix templates - supports {{SITE_LABEL}}, {{REGION_LABEL}}, {{REGION}}
    # SSM: includes region since each region has its own parameters
    ssm_prefix = "/{{SITE_LABEL}}/secrets/{{REGION_LABEL}}"
    # Secrets Manager: no region since it replicates automatically
    sm_prefix = "/{{SITE_LABEL}}/secrets"

    # Secret structure definitions - values come from the secrets file.
    # jwt/oidc/altcha are created now so the Phase 3 auth service needs no
    # secrets re-apply.
    definitions = {
      deepgram = {
        description = "Deepgram streaming STT API key"
        keys        = ["api_key"]
      }
      anthropic = {
        description = "Anthropic LLM API key"
        keys        = ["api_key"]
      }
      elevenlabs = {
        description = "ElevenLabs streaming TTS API key"
        keys        = ["api_key"]
      }
      jwt = {
        description = "JWT signing secrets"
        keys        = ["secret", "internal_secret"]
      }
      oidc = {
        description = "OIDC cookie encryption keys + RS256 JWKS signing key set (persistent, shared across the Fargate fleet)"
        keys        = ["cookie_keys", "jwks"]
      }
      altcha = {
        description = "ALTCHA proof-of-work secret"
        keys        = ["secret"]
      }
      ledger = {
        description = "Transcription ledger code_hash salt (Phase 15) — HMAC salt for hashing access codes into non-reversible record identifiers; never stored in plaintext outside SOPS"
        keys        = ["code_hash_salt"]
      }
      ctf = {
        description = "CTF phone-OTP announcement DID (quick 260715-oq0 + Rev2 260716-1g0). otp_secret = base32 TOTP shared secret (HMAC-SHA1 / 6 digits / 120s) the auth /ctf/otp issuer computes from and the meshtk verifier checks against; auth_token = optional shared bearer for the internal-only /ctf/otp route (consumed by auth as CTF_OTP_AUTH_TOKEN and sent by telephony-edge); announcement_code = the DTMF access code a caller enters to trigger the OTP readout (consumed by telephony-edge as CTF_ANNOUNCEMENT_CODE; operator-rotatable)"
        keys        = ["otp_secret", "auth_token", "announcement_code"]
      }
    }
  }

  # Phase 15 (15-04): private, append-only S3 + Athena/Glue ledger for the
  # both-sides transcription record (LEDG-02). Least-privilege service IAM
  # lives in services/voice/service.hcl (write-only) and
  # services/auth/service.hcl (read-only) — see the ledger unit's own
  # terragrunt.hcl for the enabled-toggle read.
  ledger = {
    enabled = true
  }

  # Extracted to avoid self-reference within github_oidc block
  github_oidc_delegate_role_name = "${local.site.label}-github-delegate" # "kmv-github-delegate"

  github_oidc = {
    enabled            = true
    github_org         = get_env("TF_VAR_GITHUB_ORG", "your-github-org")
    github_repo        = local.site.github_repo_name
    delegate_role_name = local.github_oidc_delegate_role_name

    # Management account for cross-account Route53 access
    # Set this to your management account ID to get the trust policy output
    # After deploying, create the delegate role in the management account
    management_account_id = get_env("TF_VAR_MANAGEMENT_ACCOUNT_ID", "123456789012")

    # No self-hosted GitHub runners for this site
    ec2_runner_instance_profile = {
      enabled = false
      name    = "github-runner"
    }

    roles = [
      # Terragrunt role - for infrastructure deployments
      # Equivalent to your local "terraform" profile + management profile
      {
        name                    = "terragrunt"
        description             = "Terragrunt infrastructure deployments"
        environment_restriction = "terraform-apply" # Only terraform-apply environment can assume this role
        max_session_duration    = 3600

        policy_arns     = []
        inline_policies = []

        # Customer-managed policies (6KB each) - avoids 10KB inline policy limit
        managed_policies = [
          {
            name = "tg-core"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid      = "TerraformState"
                  Effect   = "Allow"
                  Action   = ["dynamodb:DeleteItem", "dynamodb:GetItem", "dynamodb:PutItem", "s3:GetObject", "s3:GetObjectVersion", "s3:ListBucket", "s3:ListMultipartUploadParts", "s3:PutObject"]
                  Resource = ["arn:aws:dynamodb:*:*:table/${local.site.tf_state_prefix}-*", "arn:aws:s3:::${local.site.tf_state_prefix}-*", "arn:aws:s3:::${local.site.tf_state_prefix}-*/*"]
                },
                {
                  Sid      = "Core"
                  Effect   = "Allow"
                  Action   = ["kms:*", "sts:GetCallerIdentity"]
                  Resource = "*"
                },
                {
                  Sid      = "DynamoDB"
                  Effect   = "Allow"
                  Action   = ["dynamodb:*"]
                  Resource = "*"
                },
                {
                  Sid      = "IAM"
                  Effect   = "Allow"
                  Action   = ["iam:*"]
                  Resource = "*"
                }
              ]
            })
          },
          {
            name = "tg-compute"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid      = "EC2"
                  Effect   = "Allow"
                  Action   = ["ec2:AllocateAddress", "ec2:AssociateRouteTable", "ec2:AttachInternetGateway", "ec2:AuthorizeSecurityGroup*", "ec2:Create*", "ec2:Delete*", "ec2:Describe*", "ec2:DetachInternetGateway", "ec2:Disassociate*", "ec2:GetManagedPrefixListEntries", "ec2:Modify*", "ec2:ReleaseAddress", "ec2:RevokeSecurityGroupEgress"]
                  Resource = "*"
                },
                {
                  Sid      = "ECS"
                  Effect   = "Allow"
                  Action   = ["ecs:*"]
                  Resource = "*"
                },
                {
                  Sid      = "ECR"
                  Effect   = "Allow"
                  Action   = ["ecr:*"]
                  Resource = "*"
                },
                {
                  Sid      = "ELB"
                  Effect   = "Allow"
                  Action   = ["elasticloadbalancing:*"]
                  Resource = "*"
                },
                {
                  Sid      = "Lambda"
                  Effect   = "Allow"
                  Action   = ["lambda:*"]
                  Resource = "*"
                },
                {
                  Sid      = "AutoScaling"
                  Effect   = "Allow"
                  Action   = ["application-autoscaling:*"]
                  Resource = "*"
                }
              ]
            })
          },
          {
            name = "tg-storage"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid      = "S3"
                  Effect   = "Allow"
                  Action   = ["s3:CreateBucket", "s3:DeleteBucket", "s3:DeleteBucketPolicy", "s3:DeleteObject", "s3:DeleteObjectVersion", "s3:DeleteReplicationConfiguration", "s3:Get*", "s3:HeadBucket", "s3:List*", "s3:PutBucket*", "s3:PutEncryptionConfiguration", "s3:PutLifecycleConfiguration", "s3:PutReplicationConfiguration", "s3:TagResource", "s3:UntagResource"]
                  Resource = "*"
                },
                {
                  Sid      = "CloudWatch"
                  Effect   = "Allow"
                  Action   = ["cloudwatch:*", "logs:*"]
                  Resource = "*"
                },
                {
                  Sid      = "SSM"
                  Effect   = "Allow"
                  Action   = ["ssm:*"]
                  Resource = "*"
                },
                {
                  Sid      = "SNS"
                  Effect   = "Allow"
                  Action   = ["sns:*"]
                  Resource = "*"
                }
              ]
            })
          },
          {
            name = "tg-network"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid      = "CloudFront"
                  Effect   = "Allow"
                  Action   = ["cloudfront:*"]
                  Resource = "*"
                },
                {
                  Sid      = "Route53"
                  Effect   = "Allow"
                  Action   = ["route53:*"]
                  Resource = "*"
                },
                {
                  Sid      = "ACM"
                  Effect   = "Allow"
                  Action   = ["acm:*"]
                  Resource = "*"
                },
                {
                  Sid      = "WAF"
                  Effect   = "Allow"
                  Action   = ["wafv2:*"]
                  Resource = "*"
                },
                {
                  Sid      = "ServiceDiscovery"
                  Effect   = "Allow"
                  Action   = ["servicediscovery:*"]
                  Resource = "*"
                },
                {
                  Sid      = "Analytics"
                  Effect   = "Allow"
                  Action   = ["access-analyzer:*", "athena:*", "cloudtrail:*", "events:*", "glue:*"]
                  Resource = "*"
                },
                {
                  Sid      = "SES"
                  Effect   = "Allow"
                  Action   = ["ses:*"]
                  Resource = "*"
                }
              ]
            })
          }
        ]

        # Cross-account access to management account for Route53
        cross_account_arns = [
          "arn:aws:iam::${get_env("TF_VAR_MANAGEMENT_ACCOUNT_ID", "000000000000")}:role/${local.github_oidc_delegate_role_name}"
        ]
      },

      # Read-only role for PR plan previews
      {
        name        = "readonly"
        description = "Read-only for PR plan previews"
        # No branch/environment restriction - all PRs can use this
        max_session_duration = 3600

        policy_arns = [
          "arn:aws:iam::aws:policy/ReadOnlyAccess"
        ]

        inline_policies = [
          {
            # SOPS decrypt covers the SOPS CMK plus any per-purpose SSM CMKs
            # the readonly PR-plan role must decrypt at plan time to read
            # SecureString parameters. Extend via TF_VAR_SSM_KMS_KEY_ARNS
            # (comma-separated ARN list). Any site-scoped SSM CMK NOT in this
            # list will cause the readonly role to fail with
            # AccessDeniedException on kms:Decrypt during Terragrunt Plan.
            name = "kms-sops-decrypt"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid    = "SOPSAndSSMDecrypt"
                  Effect = "Allow"
                  Action = [
                    "kms:Decrypt",
                    "kms:DescribeKey"
                  ]
                  Resource = concat(
                    [
                      "arn:aws:kms:us-east-1:${get_env("TF_VAR_APPLICATION_ACCOUNT_ID", "000000000000")}:key/${get_env("TF_VAR_SOPS_KMS_KEY_ID", "00000000-0000-0000-0000-000000000000")}"
                    ],
                    compact(split(",", get_env("TF_VAR_SSM_KMS_KEY_ARNS", "")))
                  )
                }
              ]
            })
          },
          {
            name = "terraform-state-lock"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid    = "DynamoDBStateLock"
                  Effect = "Allow"
                  Action = [
                    "dynamodb:PutItem",
                    "dynamodb:GetItem",
                    "dynamodb:DeleteItem"
                  ]
                  Resource = [
                    "arn:aws:dynamodb:us-east-1:${get_env("TF_VAR_APPLICATION_ACCOUNT_ID", "000000000000")}:table/${local.site.tf_state_prefix}-use1-*"
                  ]
                },
                {
                  Sid    = "S3StateAccess"
                  Effect = "Allow"
                  Action = [
                    "s3:GetObject",
                    "s3:PutObject",
                    "s3:DeleteObject",
                    "s3:ListBucket"
                  ]
                  Resource = [
                    "arn:aws:s3:::${local.site.tf_state_prefix}-use1-*",
                    "arn:aws:s3:::${local.site.tf_state_prefix}-use1-*/*"
                  ]
                }
              ]
            })
          }
        ]

        # Cross-account access to management account for Route53
        cross_account_arns = [
          "arn:aws:iam::${get_env("TF_VAR_MANAGEMENT_ACCOUNT_ID", "000000000000")}:role/${local.github_oidc_delegate_role_name}"
        ]
      },

      # Release role - for GitHub Actions build workflows
      # Builds and pushes Docker images, reads deploy-time metadata
      {
        name        = "release"
        description = "Release workflow (ECR push, S3 assets)"
        # No branch restriction - release workflow can run from any branch
        max_session_duration = 7200 # 2 hours for long builds

        inline_policies = [
          {
            name = "ecr-push"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid    = "ECRAuth"
                  Effect = "Allow"
                  Action = [
                    "ecr:GetAuthorizationToken"
                  ]
                  Resource = "*"
                },
                {
                  Sid    = "ECRPush"
                  Effect = "Allow"
                  Action = [
                    "ecr:GetDownloadUrlForLayer",
                    "ecr:BatchGetImage",
                    "ecr:BatchCheckLayerAvailability",
                    "ecr:PutImage",
                    "ecr:InitiateLayerUpload",
                    "ecr:UploadLayerPart",
                    "ecr:CompleteLayerUpload",
                    "ecr:DescribeRepositories",
                    "ecr:ListImages"
                  ]
                  Resource = "arn:aws:ecr:*:*:repository/${local.site.label}-*"
                }
              ]
            })
          },
          {
            name = "s3-assets"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid    = "S3Assets"
                  Effect = "Allow"
                  Action = [
                    "s3:PutObject",
                    "s3:GetObject",
                    "s3:DeleteObject",
                    "s3:ListBucket"
                  ]
                  Resource = [
                    "arn:aws:s3:::${local.site.label}-*",
                    "arn:aws:s3:::${local.site.label}-*/*"
                  ]
                }
              ]
            })
          },
          {
            # The CI build job (build-voice.yml, this release role) syncs the
            # client dist to the cf-assets bucket, then invalidates the no-cache
            # index.html on the voice CloudFront distribution. Needs list (to
            # find the distribution by alias) + create-invalidation. Scoped to
            # "*" because CreateInvalidation/ListDistributions do not support
            # resource-level permissions.
            name = "cloudfront-invalidation"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid    = "CloudFrontInvalidation"
                  Effect = "Allow"
                  Action = [
                    "cloudfront:ListDistributions",
                    "cloudfront:GetInvalidation",
                    "cloudfront:CreateInvalidation"
                  ]
                  Resource = "*"
                }
              ]
            })
          },
          {
            name = "ssm-read"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid    = "SSMRead"
                  Effect = "Allow"
                  Action = [
                    "ssm:GetParameter",
                    "ssm:GetParameters"
                  ]
                  Resource = "arn:aws:ssm:*:*:parameter/${local.site.label}/*"
                }
              ]
            })
          },
          {
            name = "sts-identity"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid    = "STSIdentity"
                  Effect = "Allow"
                  Action = [
                    "sts:GetCallerIdentity"
                  ]
                  Resource = "*"
                }
              ]
            })
          },
          {
            # See the same-named policy on the readonly role above for the
            # TF_VAR_SSM_KMS_KEY_ARNS extension pattern.
            name = "kms-sops-decrypt"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid    = "SOPSAndSSMDecrypt"
                  Effect = "Allow"
                  Action = [
                    "kms:Decrypt",
                    "kms:DescribeKey"
                  ]
                  Resource = concat(
                    [
                      "arn:aws:kms:us-east-1:${get_env("TF_VAR_APPLICATION_ACCOUNT_ID", "000000000000")}:key/${get_env("TF_VAR_SOPS_KMS_KEY_ID", "00000000-0000-0000-0000-000000000000")}"
                    ],
                    compact(split(",", get_env("TF_VAR_SSM_KMS_KEY_ARNS", "")))
                  )
                }
              ]
            })
          },
          {
            name = "terraform-state"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid    = "S3StateAccess"
                  Effect = "Allow"
                  Action = [
                    "s3:GetObject",
                    "s3:PutObject",
                    "s3:DeleteObject",
                    "s3:ListBucket"
                  ]
                  Resource = [
                    "arn:aws:s3:::${local.site.tf_state_prefix}-*",
                    "arn:aws:s3:::${local.site.tf_state_prefix}-*/*"
                  ]
                },
                {
                  Sid    = "DynamoDBStateLock"
                  Effect = "Allow"
                  Action = [
                    "dynamodb:PutItem",
                    "dynamodb:GetItem",
                    "dynamodb:DeleteItem"
                  ]
                  Resource = [
                    "arn:aws:dynamodb:*:*:table/${local.site.tf_state_prefix}-*"
                  ]
                }
              ]
            })
          },
          {
            name = "ecs-deploy"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid    = "ECSFullDeploy"
                  Effect = "Allow"
                  Action = [
                    "ecs:RegisterTaskDefinition",
                    "ecs:DeregisterTaskDefinition",
                    "ecs:DescribeTaskDefinition",
                    "ecs:ListTaskDefinitions",
                    "ecs:UpdateService",
                    "ecs:DescribeServices",
                    "ecs:DescribeClusters",
                    "ecs:ListServices",
                    "ecs:ListClusters",
                    "ecs:TagResource",
                    "ecs:UntagResource",
                    "ecs:ListTagsForResource"
                  ]
                  Resource = "*"
                }
              ]
            })
          },
          {
            # PassRole patterns match the roles the ecs-task module produces:
            # execution role name = "<task>-<region_label>-<site_label>-execution-role"
            # (task roles are passthrough inputs; pattern kept for symmetry)
            name = "iam-ecs-roles"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid    = "PassTaskRole"
                  Effect = "Allow"
                  Action = "iam:PassRole"
                  Resource = [
                    "arn:aws:iam::*:role/*-${local.site.label}-task-role",
                    "arn:aws:iam::*:role/*-${local.site.label}-execution-role"
                  ]
                },
                {
                  Sid    = "IAMReadRoles"
                  Effect = "Allow"
                  Action = [
                    "iam:GetRole",
                    "iam:ListRolePolicies",
                    "iam:GetRolePolicy",
                    "iam:ListAttachedRolePolicies",
                    "iam:ListInstanceProfilesForRole"
                  ]
                  Resource = [
                    "arn:aws:iam::*:role/*-${local.site.label}-*",
                    "arn:aws:iam::*:role/${local.site.label}-*"
                  ]
                }
              ]
            })
          },
          {
            name = "logs-read"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid    = "LogsRead"
                  Effect = "Allow"
                  Action = [
                    "logs:DescribeLogGroups",
                    "logs:DescribeLogStreams"
                  ]
                  Resource = "*"
                }
              ]
            })
          },
          {
            name = "service-discovery"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid    = "ServiceDiscoveryRead"
                  Effect = "Allow"
                  Action = [
                    "servicediscovery:GetService",
                    "servicediscovery:GetNamespace",
                    "servicediscovery:ListServices",
                    "servicediscovery:ListNamespaces",
                    "servicediscovery:ListTagsForResource"
                  ]
                  Resource = "*"
                }
              ]
            })
          },
          {
            name = "elb-read"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid    = "ELBRead"
                  Effect = "Allow"
                  Action = [
                    "elasticloadbalancing:DescribeTargetGroups",
                    "elasticloadbalancing:DescribeTargetGroupAttributes",
                    "elasticloadbalancing:DescribeLoadBalancers",
                    "elasticloadbalancing:DescribeLoadBalancerAttributes",
                    "elasticloadbalancing:DescribeListeners",
                    "elasticloadbalancing:DescribeListenerAttributes",
                    "elasticloadbalancing:DescribeRules",
                    "elasticloadbalancing:DescribeTargetHealth",
                    "elasticloadbalancing:DescribeTags"
                  ]
                  Resource = "*"
                }
              ]
            })
          },
          {
            name = "autoscaling-read"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid    = "AutoScalingRead"
                  Effect = "Allow"
                  Action = [
                    "application-autoscaling:DescribeScalableTargets",
                    "application-autoscaling:DescribeScalingPolicies",
                    "application-autoscaling:DescribeScheduledActions",
                    "application-autoscaling:ListTagsForResource"
                  ]
                  Resource = "*"
                }
              ]
            })
          },
          {
            name = "cloudwatch-read"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid    = "CloudWatchRead"
                  Effect = "Allow"
                  Action = [
                    "cloudwatch:DescribeAlarms",
                    "cloudwatch:ListTagsForResource"
                  ]
                  Resource = "*"
                }
              ]
            })
          }
        ]
      },

      # Deploy role - for GitHub Actions deploy and rollback workflows
      # Updates ECS services via terragrunt
      {
        name                 = "deploy"
        description          = "Deploy workflow (ECS updates via terragrunt)"
        branch_restriction   = "main" # Only main branch can deploy
        max_session_duration = 3600

        inline_policies = [
          {
            name = "terraform-state"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid    = "S3StateAccess"
                  Effect = "Allow"
                  Action = [
                    "s3:GetObject",
                    "s3:PutObject",
                    "s3:DeleteObject",
                    "s3:ListBucket"
                  ]
                  Resource = [
                    "arn:aws:s3:::${local.site.tf_state_prefix}-*",
                    "arn:aws:s3:::${local.site.tf_state_prefix}-*/*"
                  ]
                },
                {
                  Sid    = "DynamoDBStateLock"
                  Effect = "Allow"
                  Action = [
                    "dynamodb:PutItem",
                    "dynamodb:GetItem",
                    "dynamodb:DeleteItem"
                  ]
                  Resource = [
                    "arn:aws:dynamodb:*:*:table/${local.site.tf_state_prefix}-*"
                  ]
                }
              ]
            })
          },
          {
            name = "ecs-deploy"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid    = "ECSFullDeploy"
                  Effect = "Allow"
                  Action = [
                    "ecs:RegisterTaskDefinition",
                    "ecs:DeregisterTaskDefinition",
                    "ecs:DescribeTaskDefinition",
                    "ecs:ListTaskDefinitions",
                    "ecs:UpdateService",
                    "ecs:DescribeServices",
                    "ecs:DescribeClusters",
                    "ecs:ListServices",
                    "ecs:ListClusters",
                    "ecs:TagResource",
                    "ecs:UntagResource",
                    "ecs:ListTagsForResource"
                  ]
                  Resource = "*"
                }
              ]
            })
          },
          {
            # PassRole patterns match the roles the ecs-task module produces:
            # execution role name = "<task>-<region_label>-<site_label>-execution-role"
            name = "iam-pass-role"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid    = "PassTaskRole"
                  Effect = "Allow"
                  Action = "iam:PassRole"
                  Resource = [
                    "arn:aws:iam::*:role/*-${local.site.label}-task-role",
                    "arn:aws:iam::*:role/*-${local.site.label}-execution-role"
                  ]
                },
                {
                  Sid    = "IAMReadRoles"
                  Effect = "Allow"
                  Action = [
                    "iam:GetRole",
                    "iam:ListRolePolicies",
                    "iam:GetRolePolicy",
                    "iam:ListAttachedRolePolicies",
                    "iam:ListInstanceProfilesForRole",
                    "iam:ListRoleTags"
                  ]
                  Resource = [
                    "arn:aws:iam::*:role/*-${local.site.label}-*",
                    "arn:aws:iam::*:role/${local.site.label}-*"
                  ]
                },
                {
                  # The ecs-task module manages each task's least-privilege
                  # INLINE policy (aws_iam_role_policy on the *-task-role /
                  # *-execution-role it owns). When a service.hcl change
                  # adds or edits a task-role statement — e.g. the Phase-15
                  # telephony-edge `LedgerPutOnly` grant — a `terragrunt apply`
                  # on the ecs-task unit must call iam:PutRolePolicy. Without
                  # this the deploy role could apply task-DEFINITION/service
                  # changes but any IAM-bearing task-role change failed with
                  # AccessDenied (PutRolePolicy), forcing those through the
                  # operator/terragrunt role. Scoped to EXACTLY the two role
                  # families the ecs-task module owns (same pattern as the
                  # PassTaskRole statement above) — never arbitrary roles, and
                  # deliberately NOT iam:CreateRole/DeleteRole/AttachRolePolicy
                  # (role lifecycle stays operator-only).
                  Sid    = "IAMWriteTaskRoleInlinePolicies"
                  Effect = "Allow"
                  Action = [
                    "iam:PutRolePolicy",
                    "iam:DeleteRolePolicy"
                  ]
                  Resource = [
                    "arn:aws:iam::*:role/*-${local.site.label}-task-role",
                    "arn:aws:iam::*:role/*-${local.site.label}-execution-role"
                  ]
                }
              ]
            })
          },
          {
            name = "ecr-read"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid    = "ECRRead"
                  Effect = "Allow"
                  Action = [
                    "ecr:GetAuthorizationToken",
                    "ecr:DescribeImages",
                    "ecr:DescribeRepositories",
                    "ecr:ListImages",
                    "ecr:BatchGetImage",
                    "ecr:GetDownloadUrlForLayer"
                  ]
                  Resource = "*"
                }
              ]
            })
          },
          {
            name = "ssm-read"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid    = "SSMRead"
                  Effect = "Allow"
                  Action = [
                    "ssm:GetParameter",
                    "ssm:GetParameters"
                  ]
                  Resource = "arn:aws:ssm:*:*:parameter/${local.site.label}/*"
                }
              ]
            })
          },
          {
            name = "logs-read"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid    = "LogsRead"
                  Effect = "Allow"
                  Action = [
                    "logs:DescribeLogGroups",
                    "logs:DescribeLogStreams"
                  ]
                  Resource = "*"
                }
              ]
            })
          },
          {
            # ecs-service unit refreshes AWS Cloud Map service-discovery
            # resources; mirror the release role's read set.
            name = "service-discovery"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid    = "ServiceDiscoveryRead"
                  Effect = "Allow"
                  Action = [
                    "servicediscovery:GetService",
                    "servicediscovery:GetNamespace",
                    "servicediscovery:ListServices",
                    "servicediscovery:ListNamespaces",
                    "servicediscovery:ListTagsForResource"
                  ]
                  Resource = "*"
                }
              ]
            })
          },
          {
            # ecs-service unit refreshes ALB target groups/listeners; without
            # this the deploy's terragrunt apply 403s on DescribeTargetGroups.
            name = "elb-read"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid    = "ELBRead"
                  Effect = "Allow"
                  Action = [
                    "elasticloadbalancing:DescribeTargetGroups",
                    "elasticloadbalancing:DescribeTargetGroupAttributes",
                    "elasticloadbalancing:DescribeLoadBalancers",
                    "elasticloadbalancing:DescribeLoadBalancerAttributes",
                    "elasticloadbalancing:DescribeListeners",
                    "elasticloadbalancing:DescribeListenerAttributes",
                    "elasticloadbalancing:DescribeRules",
                    "elasticloadbalancing:DescribeTargetHealth",
                    "elasticloadbalancing:DescribeTags"
                  ]
                  Resource = "*"
                }
              ]
            })
          },
          {
            # voice service has session-count application-autoscaling; the
            # ecs-service unit refreshes the scalable target/policies.
            name = "autoscaling-read"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid    = "AutoScalingRead"
                  Effect = "Allow"
                  Action = [
                    "application-autoscaling:DescribeScalableTargets",
                    "application-autoscaling:DescribeScalingPolicies",
                    "application-autoscaling:DescribeScheduledActions",
                    "application-autoscaling:ListTagsForResource"
                  ]
                  Resource = "*"
                }
              ]
            })
          },
          {
            # autoscaling alarms refreshed alongside the scaling policies.
            name = "cloudwatch-read"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid    = "CloudWatchRead"
                  Effect = "Allow"
                  Action = [
                    "cloudwatch:DescribeAlarms",
                    "cloudwatch:ListTagsForResource"
                  ]
                  Resource = "*"
                }
              ]
            })
          },
          {
            # See the same-named policy on the readonly role above for the
            # TF_VAR_SSM_KMS_KEY_ARNS extension pattern.
            name = "kms-sops-decrypt"
            policy = jsonencode({
              Version = "2012-10-17"
              Statement = [
                {
                  Sid    = "SOPSAndSSMDecrypt"
                  Effect = "Allow"
                  Action = [
                    "kms:Decrypt",
                    "kms:DescribeKey"
                  ]
                  Resource = concat(
                    [
                      "arn:aws:kms:us-east-1:${get_env("TF_VAR_APPLICATION_ACCOUNT_ID", "000000000000")}:key/${get_env("TF_VAR_SOPS_KMS_KEY_ID", "00000000-0000-0000-0000-000000000000")}"
                    ],
                    compact(split(",", get_env("TF_VAR_SSM_KMS_KEY_ARNS", "")))
                  )
                }
              ]
            })
          }
        ]
      }
    ]
  }
}
