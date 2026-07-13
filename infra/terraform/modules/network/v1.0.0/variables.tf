variable "site" {
  type = object({
    label         = string
    random_suffix = optional(string, "")
  })
  description = "Site configuration"
}

variable "region" {
  type = object({
    label = string
    full  = string
  })
  description = "AWS region configuration"
}

variable "dns" {
  type = object({
    zonename   = string
    subdomains = list(string)
    ttl        = optional(number, 300)
  })
  description = "DNS/Host configuration"
}

variable "cert_map" {
  type = map(object({
    arn                       = string
    domain_name               = string
    subject_alternative_names = list(string)
    validation_method         = string
  }))
  description = "Map of ACM certificates from certs module, keyed by domain name"
  default     = {}
}

variable "vpc" {
  type = object({
    cidr_block             = string
    enable_dns_hostnames   = optional(bool, true)
    enable_dns_support     = optional(bool, true)
    public_subnets_cidr    = list(string)
    private_subnets_cidr   = list(string)
    availability_zone_count = number
    tags                   = optional(map(string), {})
  })
  description = "VPC configuration including CIDR blocks and subnets. The availability_zone_count determines how many AZs to use (typically 2-4)."
}

variable "nat_gateway" {
  type = object({
    enabled = optional(bool, false)
  })
  description = "NAT Gateway configuration"
  default = {
    enabled = false
  }
}

variable "vpc_flow_logs" {
  type = object({
    enabled                  = optional(bool, false)
    traffic_type             = optional(string, "ALL")
    max_aggregation_interval = optional(number, 60)
    force_destroy            = optional(bool, true)
  })
  description = "VPC Flow Logs configuration"
  default = {
    enabled                  = false
    traffic_type             = "ALL"
    max_aggregation_interval = 60
    force_destroy            = true
  }
}

variable "vpc_endpoints" {
  type = object({
    enabled = optional(bool, false)
  })
  description = "VPC Endpoints configuration (ECR, S3, SSM, CloudWatch Logs)"
  default = {
    enabled = false
  }
}

variable "alb" {
  type = object({
    enabled                    = optional(bool, false)
    enable_deletion_protection = optional(bool, false)
    ssl_policy                 = optional(string, "ELBSecurityPolicy-TLS13-1-2-2021-06")
    logs_force_destroy         = optional(bool, true)
  })
  description = "Application Load Balancer configuration. Certificate is automatically looked up from cert_map using dns.zonename."
  default = {
    enabled                    = false
    enable_deletion_protection = false
    ssl_policy                 = "ELBSecurityPolicy-TLS13-1-2-2021-06"
    logs_force_destroy         = true
  }
}

variable "nlb" {
  type = object({
    enabled                    = optional(bool, false)
    enable_deletion_protection = optional(bool, false)
    logs_force_destroy         = optional(bool, true)
  })
  description = "Network Load Balancer configuration. Uses VPC public subnets for placement."
  default = {
    enabled                    = false
    enable_deletion_protection = false
    logs_force_destroy         = true
  }
}

# Phase 12 (D-01, T-12-07-01): CIDR allow-list for the dedicated
# telephony-edge security group (SIP/RTP ingress, e.g. the VoIP.ms Toronto
# POP /32s). Deliberately its own variable — NOT folded into the generic
# security groups above — so a consumer must explicitly attach it (see the
# module's standalone `telephony_edge_security_group_id` output); it is
# never part of the default `security_group_ids` list output, which
# includes `webrtc_udp` (0.0.0.0/0 on UDP 20000-20100) and would defeat the
# POP-lock if merged in. Default `[]` (every site/region that doesn't set
# this) creates the security group with zero ingress rules — closed, not
# open — so this addition is backward-compatible everywhere it isn't used.
variable "telephony_edge_pop_cidrs" {
  type        = list(string)
  description = "CIDR blocks (e.g. VoIP.ms Toronto POP /32s) allowed to reach the telephony-edge security group on SIP/RTP. Empty = zero ingress rules (closed)."
  default     = []
}
