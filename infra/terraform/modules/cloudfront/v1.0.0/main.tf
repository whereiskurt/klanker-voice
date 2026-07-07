data "aws_caller_identity" "current" {}

resource "random_id" "rnd" {
  byte_length = 8
}

locals {
  domain_set = toset(var.cloudfront.domains)

  # Region labels in site-config order; the first is the primary/live region.
  # FLAT single-domain routing (klanker-voice divergence from defcon.run.34's
  # per-region-path scheme): exactly one S3 origin (the SPA shell + hashed
  # assets) and one ALB origin (the app's /api/offer signaling + /health),
  # both from the primary region. The default behavior serves S3; /api/* and
  # /health are the only paths sent to the ALB. cac1/apse1 asset buckets are
  # pre-wired as mock dependencies at the unit layer but are NOT wired as
  # origins here until a region is lit up (added to cloudfront.regions AND
  # removed from site.skip_regions), at which point per-region routing (origin
  # groups or a path prefix) would be added — deferred, out of scope today.
  region_labels = [for r in var.cloudfront.regions : r.label]
  primary       = local.region_labels[0]

  # Set of region labels that should be skipped (derived from skip_regions).
  skipped_region_labels = toset([
    for r in var.cloudfront.regions : r.label
    if contains(var.site.skip_regions, r.full)
  ])

  # AWS managed policy IDs
  cache_disabled_id    = "4135ea2d-6df8-44a3-9df3-4b5a84be39ad" # Managed-CachingDisabled
  cache_optimized_id   = "658327ea-f89d-4fab-a63d-7e88639e58f6" # Managed-CachingOptimized
  origin_all_viewer_id = "216adef6-5c7f-47e4-b989-5492eafa07d3" # Managed-AllViewer (forwards Host + Authorization)
}

# S3 bucket for CloudFront access logs (in us-east-1 with CloudFront) - one per domain
resource "aws_s3_bucket" "cloudfront_logs" {
  for_each = var.cloudfront.logging.enabled ? local.domain_set : toset([])

  bucket        = "logs-cf-${each.key}-${var.site.label}-${random_id.rnd.hex}"
  force_destroy = true

  tags = merge(
    var.tags,
    {
      Name        = "cloudfront-logs-${each.key}"
      Purpose     = "CloudFront Logs"
      Environment = var.site.label
      Domain      = "${each.key}.${var.dns.zonename}"
    }
  )

  provider = aws.global-application
}

resource "aws_s3_bucket_ownership_controls" "cloudfront_logs_ownership" {
  for_each = var.cloudfront.logging.enabled ? local.domain_set : toset([])

  bucket   = aws_s3_bucket.cloudfront_logs[each.key].id
  provider = aws.global-application

  rule {
    object_ownership = "BucketOwnerPreferred"
  }
}

resource "aws_s3_bucket_acl" "cloudfront_logs_acl" {
  for_each = var.cloudfront.logging.enabled ? local.domain_set : toset([])

  depends_on = [aws_s3_bucket_ownership_controls.cloudfront_logs_ownership]
  bucket     = aws_s3_bucket.cloudfront_logs[each.key].id
  acl        = "private"
  provider   = aws.global-application
}

# Origin Access Control for the S3 asset bucket - one per domain (primary region)
resource "aws_cloudfront_origin_access_control" "cf_oac" {
  for_each = local.domain_set

  name                              = "oac-${each.key}-${local.primary}-${var.dns.zonename}"
  description                       = "OAC for ${each.key} ${local.primary} S3 asset bucket"
  origin_access_control_origin_type = "s3"
  signing_behavior                  = "always"
  signing_protocol                  = "sigv4"

  provider = aws.global-application
}

# CloudFront Distribution - one per domain (flat: S3 default + ALB for /api,/health)
resource "aws_cloudfront_distribution" "main" {
  for_each = local.domain_set

  enabled             = true
  is_ipv6_enabled     = true
  comment             = "klanker-voice full-front distribution for ${each.key}.${var.dns.zonename}"
  default_root_object = "index.html"
  price_class         = var.cloudfront.price_class
  aliases             = ["${each.key}.${var.dns.zonename}"]

  depends_on = [aws_s3_bucket_acl.cloudfront_logs_acl]

  # S3 origin - the static SPA shell + content-hashed assets. Retained across
  # deploys (sync-without-delete) so old+new bundles coexist; a browser that
  # loaded index.html from one build can never 404 on another build's hashes.
  origin {
    domain_name              = var.regional_origins_by_domain[each.key][local.primary].s3_bucket_regional_domain_name
    origin_id                = "s3-${local.primary}"
    origin_access_control_id = aws_cloudfront_origin_access_control.cf_oac[each.key].id
  }

  # ALB origin - the dynamic app surface (/api/offer SDP signaling, /health).
  origin {
    domain_name = var.regional_origins_by_domain[each.key][local.primary].alb_dns_name
    origin_id   = "alb-${local.primary}"

    custom_origin_config {
      http_port              = 80
      https_port             = 443
      origin_protocol_policy = "https-only"
      origin_ssl_protocols   = ["TLSv1.2"]
    }
  }

  # Default: serve the SPA from S3. index.html is uploaded no-cache by the sync
  # step; the content-hashed assets are immutable and long-cached.
  default_cache_behavior {
    target_origin_id       = "s3-${local.primary}"
    viewer_protocol_policy = "redirect-to-https"
    allowed_methods        = ["GET", "HEAD", "OPTIONS"]
    cached_methods         = ["GET", "HEAD"]
    compress               = true

    cache_policy_id = local.cache_optimized_id
  }

  # /api/* -> ALB. Managed-AllViewer forwards the viewer Host header (so the
  # ALB's host-header listener rule for ${each.key}.${var.dns.zonename} still
  # matches) AND the Authorization bearer token used by /api/offer. Never
  # cached (SDP signaling).
  ordered_cache_behavior {
    path_pattern           = "/api/*"
    target_origin_id       = "alb-${local.primary}"
    viewer_protocol_policy = "redirect-to-https"
    allowed_methods        = ["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]
    cached_methods         = ["GET", "HEAD"]
    compress               = false

    cache_policy_id          = local.cache_disabled_id
    origin_request_policy_id = local.origin_all_viewer_id
  }

  # /health -> ALB (real app liveness), never cached.
  ordered_cache_behavior {
    path_pattern           = "/health"
    target_origin_id       = "alb-${local.primary}"
    viewer_protocol_policy = "redirect-to-https"
    allowed_methods        = ["GET", "HEAD", "OPTIONS"]
    cached_methods         = ["GET", "HEAD"]
    compress               = false

    cache_policy_id          = local.cache_disabled_id
    origin_request_policy_id = local.origin_all_viewer_id
  }

  # SPA deep-link routing: a private OAC bucket returns 403 for a missing key
  # (and 404 for a missing object) - serve index.html with 200 so the client
  # router can take over. Defense-in-depth on top of the app's own 404->index
  # fallback. Short error-cache TTL so a genuinely-missing asset during a fresh
  # sync self-heals quickly.
  custom_error_response {
    error_code            = 403
    response_code         = 200
    response_page_path    = "/index.html"
    error_caching_min_ttl = 10
  }

  custom_error_response {
    error_code            = 404
    response_code         = 200
    response_page_path    = "/index.html"
    error_caching_min_ttl = 10
  }

  viewer_certificate {
    acm_certificate_arn      = var.cert_map["${each.key}.${var.dns.zonename}"].arn
    ssl_support_method       = "sni-only"
    minimum_protocol_version = "TLSv1.2_2021"
  }

  restrictions {
    geo_restriction {
      restriction_type = "none"
    }
  }

  dynamic "logging_config" {
    for_each = var.cloudfront.logging.enabled ? [1] : []
    content {
      bucket          = aws_s3_bucket.cloudfront_logs[each.key].bucket_domain_name
      include_cookies = var.cloudfront.logging.include_cookies
      prefix          = "cloudfront/"
    }
  }

  web_acl_id = lookup(var.waf_web_acl_arns, each.key, "")

  tags = merge(
    var.tags,
    {
      Name        = "${each.key}.${var.dns.zonename}"
      Purpose     = "CloudFront Distribution"
      Environment = var.site.label
      Domain      = "${each.key}.${var.dns.zonename}"
    }
  )

  provider = aws.global-application
}

# S3 bucket policy allowing CloudFront OAC read access to the primary (use1)
# asset bucket. Bucket-policy API calls must hit the bucket's home region, so
# this uses the aws.use1 regional provider alias. Guarded so it is never
# applied to a skipped/mock region.
resource "aws_s3_bucket_policy" "cf_oac_access_use1" {
  for_each = {
    for domain in var.cloudfront.domains : domain => var.regional_origins_by_domain[domain]["use1"]
    if contains(keys(var.regional_origins_by_domain[domain]), "use1") &&
    !contains(local.skipped_region_labels, "use1") &&
    !startswith(try(var.regional_origins_by_domain[domain]["use1"].s3_bucket_id, ""), "mock-")
  }

  bucket = each.value.s3_bucket_id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "AllowCloudFrontOACAccess"
        Effect = "Allow"
        Principal = {
          Service = "cloudfront.amazonaws.com"
        }
        Action   = "s3:GetObject"
        Resource = "${each.value.s3_bucket_arn}/*"
        Condition = {
          StringEquals = {
            "AWS:SourceArn" = aws_cloudfront_distribution.main[each.key].arn
          }
        }
      },
      {
        Sid       = "DenyNonHTTPS"
        Effect    = "Deny"
        Principal = "*"
        Action    = "s3:*"
        Resource = [
          each.value.s3_bucket_arn,
          "${each.value.s3_bucket_arn}/*"
        ]
        Condition = {
          Bool = {
            "aws:SecureTransport" = "false"
          }
        }
      }
    ]
  })

  provider = aws.use1
}
