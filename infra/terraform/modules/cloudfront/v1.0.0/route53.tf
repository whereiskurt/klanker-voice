# Local to build full domain names and lookup zones
locals {
  # Build full domain names for all CloudFront domains
  full_domains = [for domain in var.cloudfront.domains : "${domain}.${var.dns.zonename}"]

  # Map full domain name -> hosted zone id (looked up from the site zone_map)
  domain_zones = {
    for domain in local.full_domains : domain => var.zone_map[domain].zone_id
  }
}

# Route53 A records (aliases) pointing to the CloudFront distribution.
# One A alias per domain, targeting that domain's distribution.
resource "aws_route53_record" "cloudfront_alias" {
  for_each = local.domain_zones

  zone_id = each.value
  name    = each.key
  type    = "A"

  # The voice A-record may pre-exist (created out-of-band at launch, pointing at
  # the ALB). allow_overwrite lets terraform take ownership and repoint it to
  # CloudFront in-place (the DNS cutover) instead of failing on "already exists",
  # and re-asserts on every apply. Matches the certs/site route53 convention.
  allow_overwrite = true

  alias {
    name                   = aws_cloudfront_distribution.main[split(".", each.key)[0]].domain_name
    zone_id                = aws_cloudfront_distribution.main[split(".", each.key)[0]].hosted_zone_id
    evaluate_target_health = false
  }

  provider = aws.global-application
}
