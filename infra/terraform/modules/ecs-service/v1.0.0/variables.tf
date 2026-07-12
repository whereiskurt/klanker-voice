variable "site" {
  type = object({
    label         = string
    random_suffix = string
  })
  description = "Site configuration"
}

variable "region" {
  type = object({
    label = string
    full  = string
  })
  description = "Region configuration"
}

variable "dns" {
  type = object({
    zonename   = string
    subdomains = list(string)
    ttl        = number
  })
  description = "DNS configuration"
}

variable "ecs_services" {
  type = list(object({
    name                 = string
    regions              = list(string)
    cluster_name         = string
    task_family          = string # Must match task definition family from ecs-task module
    desired_count        = optional(number, 1)
    launch_type          = optional(string, "FARGATE")
    force_new_deployment = optional(bool, true)

    # Network configuration
    assign_public_ip = optional(bool, false)

    # Service discovery configuration
    service_discovery = optional(object({
      name           = string # Service discovery name
      ttl            = optional(number, 10)
      container_name = optional(string, "") # Container to register, empty = don't use service discovery
    }), null)

    # Load balancer configurations (can have multiple)
    load_balancers = optional(list(object({
      type                  = string # "alb" or "nlb"
      container_name        = string
      container_port        = number
      target_group_port     = optional(number, null)  # Defaults to container_port
      target_group_protocol = optional(string, "TCP") # TCP, TLS, HTTP, HTTPS
      proxy_protocol_v2     = optional(bool, null)    # Explicit PP2 toggle; null = auto-detect (NLB+TCP=true)
      health_check_path     = optional(string, "/")
      health_check_protocol = optional(string, null) # Defaults to target_group_protocol
      health_check = optional(object({
        enabled             = optional(bool, true)
        healthy_threshold   = optional(number, 2)
        unhealthy_threshold = optional(number, 2)
        timeout             = optional(number, 5)
        interval            = optional(number, 30)
        matcher             = optional(string, "200-499") # For HTTP/HTTPS only
      }), {})

      # Listener configuration (creates new listener on LB)
      listener = optional(object({
        port            = number
        protocol        = string # TCP, TLS, HTTP, HTTPS
        ssl_policy      = optional(string, "ELBSecurityPolicy-TLS13-1-0-2021-06")
        certificate_arn = optional(string, "") # For TLS/HTTPS
        # Host header routing for ALB
        host_headers = optional(list(string), [])
        # Path pattern routing for ALB (supports multiple patterns)
        path_patterns = optional(list(string), [])
        # Priority for listener rule (required when using path patterns)
        priority = optional(number, null)
      }), null)
    })), [])

    # Deployment configuration
    deployment_circuit_breaker = optional(object({
      enable   = bool
      rollback = bool
    }), { enable = true, rollback = false })

    deployment_maximum_percent         = optional(number, 200)
    deployment_minimum_healthy_percent = optional(number, 50)
    health_check_grace_period_seconds  = optional(number, 300)

    # Autoscaling configuration
    autoscaling = optional(object({
      enabled      = bool
      min_capacity = optional(number, 1)
      max_capacity = optional(number, 10)

      # CPU-based scaling
      cpu_target = optional(object({
        scale_out_threshold = optional(number, 75)
        scale_in_threshold  = optional(number, 25)
        evaluation_periods  = optional(number, 2)
        period              = optional(number, 60)
        cooldown            = optional(number, 120)
      }), null)

      # Memory-based scaling
      memory_target = optional(object({
        scale_out_threshold = optional(number, 75)
        scale_in_threshold  = optional(number, 25)
        evaluation_periods  = optional(number, 2)
        period              = optional(number, 60)
        cooldown            = optional(number, 120)
      }), null)

      # Custom-metric target-tracking scaling (D-13): session-count-style
      # autoscaling on an arbitrary CloudWatch metric/namespace instead of
      # the built-in ECS CPU/Memory metrics. Mutually independent of
      # cpu_target/memory_target — set only this to scale on a custom
      # metric alone (e.g. ActiveSessions).
      custom_metric_target = optional(object({
        metric_name        = string
        namespace          = string
        target_value       = number
        statistic          = optional(string, "Average")
        dimensions         = optional(map(string), {})
        scale_out_cooldown = optional(number, 60)
        scale_in_cooldown  = optional(number, 60)
      }), null)
    }), { enabled = false })
  }))
  description = "List of ECS service definitions"
  default     = []
}

# Outputs from dependencies
variable "task_definitions" {
  type        = map(string)
  description = "Map of task definition ARNs by task name from ecs-task module"
  default     = {}
}

variable "clusters" {
  type = map(object({
    cluster_id     = string
    cluster_name   = string
    cluster_arn    = string
    namespace_id   = string
    namespace_name = string
  }))
  description = "Map of cluster details by cluster name from ecs-cluster module"
  default     = {}
}

variable "alb_arn" {
  type        = string
  description = "ARN of the Application Load Balancer"
  default     = ""
}

variable "alb_listener_arn" {
  type        = string
  description = "ARN of the ALB HTTPS listener"
  default     = ""
}

variable "nlb_arn" {
  type        = string
  description = "ARN of the Network Load Balancer"
  default     = ""
}

variable "nlb_default_certificate_arn" {
  type        = string
  description = "Default ACM certificate ARN for NLB TLS listeners when not specified in service config"
  default     = ""
}

variable "vpc_id" {
  type        = string
  description = "VPC ID for target groups"
}

variable "private_subnet_ids" {
  type        = list(string)
  description = "Private subnet IDs for ECS services"
  default     = []
}

variable "public_subnet_ids" {
  type        = list(string)
  description = "Public subnet IDs for ECS services"
  default     = []
}

variable "security_group_ids" {
  type        = list(string)
  description = "Security group IDs for ECS services"
  default     = []
}

# Phase 12 (D-01, T-12-07-01): per-service security group override, keyed
# by ECS service name (the same key `local.services_map` uses). When a
# service's name is present here, its `network_configuration.security_groups`
# is THIS list instead of the module-wide `security_group_ids` default —
# needed because `security_group_ids` is shared by every service in the
# module and includes `webrtc_udp` (0.0.0.0/0 on UDP 20000-20100), which
# would defeat a service that needs a narrower, POP-locked ingress set
# (telephony-edge). Absent/unset services keep the pre-existing global
# behavior byte-for-byte — this is purely additive.
variable "security_group_overrides" {
  type        = map(list(string))
  description = "Per-service security group ID list override, keyed by service name. Services not present here use the shared security_group_ids list."
  default     = {}
}
