# Data-only service stub for the telephony-edge (inbound-only Asterisk +
# ARI + the standalone telephony controller, one Fargate container).
# site.hcl reads this at parse time for every unit, so this file must stay
# pure data: no filesystem reads, no VERSION lookups (same rule as voice/
# auth's own service.hcl header comment).
#
# Phase 12 (D-01/D-04, 12-07): the minimum secure edge to expose the public
# DID — SSM-backed secrets via valueFrom, a dedicated least-privilege task
# role (12-05's hard constraint: never the shared cluster role; SSM grants
# only under /kmv/secrets/use1/*, NEVER /kmv/operators/*), no load balancer
# (ARI is private-network-only), and a public IP for the outbound VoIP.ms
# registration trunk. The POP-locked security group itself is NOT declared
# here — it is created by the network module (telephony-sg.hcl +
# network/v1.0.0's dedicated `telephony_edge` security group resource) and
# attached to this service via the region ecs-service unit's
# `security_group_overrides` map (this service is deliberately NOT part of
# the shared `security_group_ids` list voice/auth use — that list includes
# `webrtc_udp`, which is 0.0.0.0/0 on UDP 20000-20100; attaching it here
# would defeat the entire POP-lock).
locals {
  # ECR repository for the telephony-edge image (Asterisk + the controller,
  # one Dockerfile — apps/voice/asterisk/Dockerfile).
  ecr_repositories = [
    {
      name                 = "telephony-edge"
      regions              = ["us-east-1"]
      image_tag_mutability = "IMMUTABLE"
      lifecycle_policy = {
        max_image_count = 10
        expire_days     = 30
      }
    }
  ]

  # Least-privilege telephony-edge task-role IAM (12-05 hard constraint,
  # T-12-07-05): SSM read + KMS decrypt scoped to exactly the three
  # container-secret prefixes this service consumes. No table/account-wide
  # grant, and — critically — NEVER `/kmv/operators/*` (that prefix holds
  # the operator-only admin_phone parameter no bot task role may ever read,
  # see docs/operators/phase12-seed-data.md).
  task_role_iam_statements = [
    {
      sid     = "TelephonySecretRead"
      actions = ["ssm:GetParameters", "ssm:GetParameter"]
      resources = [
        "arn:aws:ssm:*:*:parameter/kmv/secrets/use1/voipms/*",
        "arn:aws:ssm:*:*:parameter/kmv/secrets/use1/asterisk/*",
        "arn:aws:ssm:*:*:parameter/kmv/secrets/use1/telephony/*",
      ]
    },
    {
      sid       = "TelephonyKmsDecrypt"
      actions   = ["kms:Decrypt"]
      resources = ["*"]
      condition = {
        test     = "StringEquals"
        variable = "kms:ViaService"
        values   = ["ssm.us-east-1.amazonaws.com"]
      }
    },
    # The in-container call_runtime enforces the SAME quota system as the voice
    # service (spec: "every session must be quota-gated"). A telephony call's
    # gate-unlock reads tier limits + writes the usage/concurrency ledger, so
    # this role needs the identical DynamoDB/CloudWatch/ECS grants the voice
    # task role carries — WITHOUT them, gate-unlock threw AccessDenied on
    # dynamodb:GetItem(kmv-voice-usage) and telephony calls bypassed quota
    # entirely (surfaced by the first live PSTN call). Mirrors voice
    # service.hcl's UsageTableCrud/TiersTableRead/SessionMetricsPublish/
    # TaskScaleInProtection statements. (PublicIpEniLookup is intentionally
    # omitted — the edge discovers its media address via checkip, not the ENI.)
    {
      sid     = "UsageTableCrud"
      actions = ["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:UpdateItem", "dynamodb:Query"]
      resources = [
        "arn:aws:dynamodb:*:*:table/kmv-voice-usage",
        "arn:aws:dynamodb:*:*:table/kmv-voice-usage/index/*"
      ]
    },
    {
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
  ]

  task = {
    name         = "telephony-edge"
    regions      = ["us-east-1"]
    cluster_name = "app"
    # 0.5 vCPU (the original 12-RESEARCH sizing) starved the real-time audio
    # thread on the first live PSTN call — the caller heard heavily garbled
    # TTS because Asterisk + µ-law transcode + 8k↔24k resampling + Deepgram +
    # Claude + ElevenLabs cannot share half a core without underruns. Bumped
    # to 2 vCPU / 4 GB (matching the voice service's real-time headroom).
    task_cpu    = 2048
    task_memory = 4096

    # T-12-07-05 / 12-05 hard constraint: dedicated least-privilege task
    # role (the ecs-task module creates one because this list is non-empty)
    # — NEVER the shared per-cluster role.
    task_role_policy_statements = local.task_role_iam_statements

    containers = [
      {
        name = "telephony-edge"
        # Image tag is env-driven (same CI/OIDC deploy pattern as voice/
        # auth) so build-telephony-edge.yml can deploy the immutable
        # ${github.sha} image it just built.
        image     = "telephony-edge:${get_env("TF_VAR_TELEPHONY_EDGE_IMAGE_TAG", "4ce0fcc78a909dba52d72ffb639c1127d39a1be1")}"
        cpu       = 2048
        memory    = 4096
        essential = true

        # Asterisk needs a writable root filesystem: render_configs.py
        # rewrites /etc/asterisk/{ari,pjsip}.conf at container start (D-04),
        # and Asterisk itself writes /var/log/asterisk, /var/spool/asterisk,
        # /var/lib/asterisk (astdb) and /var/run/asterisk at runtime. The
        # module's `readonly_root_filesystem` default (true) is correct for
        # voice/auth's stateless app containers but wrong here.
        readonly_root_filesystem = false

        environment = [
          {
            name = "ASTERISK_ARI_URL"
            # ARI is bound loopback-only inside this single container
            # (http.conf) — never exposed on the task ENI (D-01, §18/§25.C).
            value = "http://127.0.0.1:8088"
          },
          {
            name  = "KLANKER_PIPELINE_CONFIG"
            value = "configs/telephony.toml"
          }
        ]

        # D-04: every VoIP.ms/Asterisk/telephony secret reaches the
        # container via SSM valueFrom — nothing public-facing lives in env/
        # git. VOIPMS_SIP_* is consumed by Asterisk only (rendered into
        # pjsip.conf by the entrypoint, then scrubbed from the environment
        # before the Python controller is exec'd — see
        # apps/voice/asterisk/entrypoint.sh). ASTERISK_ARI_* is consumed by
        # BOTH Asterisk (ari.conf) and the controller (ARI REST/WS auth).
        # TELEPHONY_ENDPOINT_AUTH_TOKEN/ACCESS_PIN/PASSPHRASE_WORDS are
        # consumed by the controller only (never written to any .conf).
        secrets = [
          {
            name      = "VOIPMS_SIP_USERNAME"
            valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/voipms/sip_username"
          },
          {
            name      = "VOIPMS_SIP_PASSWORD"
            valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/voipms/sip_password"
          },
          {
            name      = "ASTERISK_ARI_USERNAME"
            valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/asterisk/ari_username"
          },
          {
            name      = "ASTERISK_ARI_PASSWORD"
            valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/asterisk/ari_password"
          },
          {
            name      = "TELEPHONY_ENDPOINT_AUTH_TOKEN"
            valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/telephony/endpoint_auth_token"
          },
          {
            name      = "TELEPHONY_ACCESS_PIN"
            valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/telephony/access_pin"
          },
          {
            name      = "TELEPHONY_PASSPHRASE_WORDS"
            valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/telephony/passphrase_words"
          },
          # The telephony pipeline runs IN this container (Phase-9
          # call_runtime): after gate unlock it builds the same
          # STT/LLM/TTS cascade the voice service runs, so it needs the
          # same three metered-API keys (surfaced by the first live call:
          # gate unlocked, then pipeline build crashed on the missing
          # Deepgram key and the caller got silence).
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
          }
        ]

        # SIP signaling only. RTP media ports are NOT individually mapped —
        # awsvpc/Fargate networking gives the task its own ENI, so the whole
        # 20000-20100 UDP window is reachable directly (matching how the
        # voice service's webrtc_udp range works); the security group (not
        # port_mappings) is what actually bounds this to the Toronto POPs.
        port_mappings = [
          {
            container_port = 5060
            host_port      = 5060
            protocol       = "udp"
          }
        ]

        # D-12-style RTP port pinning is unnecessary here: Asterisk's
        # rtp.conf (apps/voice/asterisk/rtp.conf, unmodified) already
        # statically declares 20000-20100 as its RTP allocation range — no
        # OS ephemeral-port sysctl to pin, unlike aiortc's dynamic bind.
      }
    ]
  }

  service = {
    name          = "telephony-edge"
    regions       = ["us-east-1"]
    cluster_name  = "app"
    task_family   = "telephony-edge"
    desired_count = 1

    # Public IP required: the VoIP.ms trunk is registration-based (outbound
    # REGISTER from this task to a Toronto POP), so the task needs a public
    # egress IP for that registration to complete and for return RTP/SIP
    # traffic to route back (D-01).
    assign_public_ip = true

    # NO load_balancers block: ARI is private-network-only, no public HTTP
    # for this service (D-01, T-12-07-03).

    # Single-task deployment for Phase 12 — no autoscaling (concurrency=1
    # is enforced at the application layer, SessionLifecycle/quota gate,
    # not by running more tasks).
    autoscaling = {
      enabled = false
    }
  }
}
