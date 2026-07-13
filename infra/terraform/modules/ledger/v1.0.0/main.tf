data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

resource "random_id" "rnd" {
  byte_length = 8
}

# Private, append-only S3 bucket holding the transcription ledger (LEDG-02).
# Naming mirrors cloudfront-assets: "${site.label}-<purpose>-${region.label}-${random}".
# The bucket also hosts the Athena workgroup's query-result output under a
# separate athena-results/ prefix (same bucket, different prefix — no second
# bucket needed for query scratch space).
resource "aws_s3_bucket" "ledger" {
  bucket = "${var.site.label}-ledger-${var.region.label}-${random_id.rnd.hex}"

  tags = merge(
    var.tags,
    {
      Name        = "${var.site.label}-ledger-${var.region.label}"
      Region      = var.region.full
      Purpose     = "Transcription Ledger"
      Environment = var.site.label
    }
  )
}

# Server-side encryption (T-15-04-01). SSE-S3/AES256 is sufficient per
# RESEARCH — no KMS key needed for this bucket.
resource "aws_s3_bucket_server_side_encryption_configuration" "ledger" {
  bucket = aws_s3_bucket.ledger.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

# Block all public access (T-15-04-01) — the bucket holds personal-data
# transcripts; it must never be reachable except via task-role IAM.
resource "aws_s3_bucket_public_access_block" "ledger" {
  bucket = aws_s3_bucket.ledger.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# Simple time-based retention (A5/CONTEXT: one rule is enough for v1).
# Scoped to the ledger/ prefix only so athena-results/ query-scratch objects
# are not affected by this rule (they can accumulate/expire independently
# later if desired — out of scope for v1).
resource "aws_s3_bucket_lifecycle_configuration" "ledger" {
  bucket = aws_s3_bucket.ledger.id

  rule {
    id     = "expire-ledger-records"
    status = "Enabled"

    filter {
      prefix = "ledger/"
    }

    expiration {
      days = var.expiration_days
    }
  }
}

# Athena workgroup — enforce_workgroup_configuration=true means every query
# MUST use this workgroup's own result location (no per-query override),
# and results land in a separate prefix in the same bucket (Pattern 5).
resource "aws_athena_workgroup" "ledger" {
  name = "${var.site.label}-ledger-${var.region.label}"

  configuration {
    enforce_workgroup_configuration = true

    result_configuration {
      output_location = "s3://${aws_s3_bucket.ledger.id}/athena-results/"
    }
  }

  tags = merge(
    var.tags,
    {
      Name        = "${var.site.label}-ledger-${var.region.label}"
      Environment = var.site.label
    }
  )
}

resource "aws_glue_catalog_database" "ledger" {
  name = replace("${var.site.label}_ledger_${var.region.label}", "-", "_")
}

# Partition-projection external table (Pattern 5) — no MSCK REPAIR, no
# crawler, zero ongoing partition maintenance. Column set MUST exactly equal
# apps/voice/src/klanker_voice/ledger.py's LEDGER_FIELDS tuple (Pitfall 6 —
# see apps/voice/tests/test_ledger_schema.py, which asserts both sides
# against one hardcoded canonical list).
resource "aws_glue_catalog_table" "ledger" {
  name          = "ledger"
  database_name = aws_glue_catalog_database.ledger.name
  table_type    = "EXTERNAL_TABLE"

  parameters = {
    EXTERNAL                            = "TRUE"
    "projection.enabled"                = "true"
    "projection.dt.type"                = "date"
    "projection.dt.format"              = "yyyy-MM-dd"
    "projection.dt.range"               = "2026-07-01,NOW"
    "projection.dt.interval"            = "1"
    "projection.dt.interval.unit"       = "DAYS"
    "storage.location.template"         = "s3://${aws_s3_bucket.ledger.id}/ledger/dt=$${dt}/"
  }

  partition_keys {
    name = "dt"
    type = "string"
  }

  storage_descriptor {
    location      = "s3://${aws_s3_bucket.ledger.id}/ledger/"
    input_format  = "org.apache.hadoop.mapred.TextInputFormat"
    output_format = "org.apache.hadoop.hive.ql.io.HiveIgnoreKeyTextOutputFormat"

    ser_de_info {
      name                  = "ledger-json"
      serialization_library = "org.openx.data.jsonserde.JsonSerDe"

      parameters = {
        "ignore.malformed.json" = "true"
      }
    }

    # Column order/types MUST match LEDGER_FIELDS exactly (Pitfall 6).
    columns {
      name = "role"
      type = "string"
    }
    columns {
      name = "text"
      type = "string"
    }
    columns {
      name = "email"
      type = "string"
    }
    columns {
      name = "caller_id"
      type = "string"
    }
    columns {
      name = "did"
      type = "string"
    }
    columns {
      name = "ts"
      type = "bigint"
    }
    columns {
      name = "session_id"
      type = "string"
    }
    columns {
      name = "turn_seq"
      type = "int"
    }
    columns {
      name = "code_hash"
      type = "string"
    }
    columns {
      name = "tier_id"
      type = "string"
    }
    columns {
      name = "channel"
      type = "string"
    }
    columns {
      name = "interrupted"
      type = "boolean"
    }
  }
}
