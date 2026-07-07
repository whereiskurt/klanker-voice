# SSM discovery parameters for the CI build job:
#   /{site.label}/cloudfront/{domain}/distribution_id
#   /{site.label}/cloudfront/{domain}/domain
# build-voice.yml reads distribution_id to invalidate the no-cache index.html
# after syncing a fresh client dist — no list-distributions / zonename lookup
# needed. Non-sensitive infrastructure metadata.

resource "aws_ssm_parameter" "distribution_id" {
  for_each = local.domain_set

  name        = "/${var.site.label}/cloudfront/${each.key}/distribution_id"
  description = "CloudFront distribution id for ${each.key}.${var.dns.zonename}"
  type        = "String"
  value       = aws_cloudfront_distribution.main[each.key].id

  tags = {
    Site   = var.site.label
    Domain = each.key
  }

  provider = aws.global-application
}

resource "aws_ssm_parameter" "distribution_domain" {
  for_each = local.domain_set

  name        = "/${var.site.label}/cloudfront/${each.key}/domain"
  description = "Public domain for the ${each.key} CloudFront distribution"
  type        = "String"
  value       = "${each.key}.${var.dns.zonename}"

  tags = {
    Site   = var.site.label
    Domain = each.key
  }

  provider = aws.global-application
}
