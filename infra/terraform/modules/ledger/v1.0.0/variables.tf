variable "site" {
  description = "Site configuration"
  type = object({
    label         = string
    random_suffix = optional(string, "")
  })
}

variable "region" {
  description = "Region configuration"
  type = object({
    label = string
    full  = string
  })
}

variable "tags" {
  description = "Common tags to apply to all resources"
  type        = map(string)
  default     = {}
}

variable "expiration_days" {
  description = "Number of days after which ledger/ objects expire (A5 — simple time-based retention, tunable per-site)"
  type        = number
  default     = 365
}
