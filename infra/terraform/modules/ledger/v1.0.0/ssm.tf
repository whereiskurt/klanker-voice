# SSM parameters for the ledger bucket. Non-sensitive metadata (bucket name/
# arn) so service.hcl env vars and operator tooling (kv) can resolve the
# random-suffixed bucket name without a terragrunt cross-unit dependency.
# Mirrors modules/cloudfront-assets/v1.0.0/ssm.tf's prefix convention.

locals {
  ssm_prefix = "/${var.site.label}/ledger/${var.region.label}"
}

resource "aws_ssm_parameter" "bucket_name" {
  name        = "${local.ssm_prefix}/bucket_name"
  description = "S3 bucket name for the transcription ledger in ${var.region.label}"
  type        = "String"
  value       = aws_s3_bucket.ledger.id

  tags = {
    Site   = var.site.label
    Region = var.region.label
  }
}

resource "aws_ssm_parameter" "bucket_arn" {
  name        = "${local.ssm_prefix}/bucket_arn"
  description = "S3 bucket ARN for the transcription ledger in ${var.region.label}"
  type        = "String"
  value       = aws_s3_bucket.ledger.arn

  tags = {
    Site   = var.site.label
    Region = var.region.label
  }
}
