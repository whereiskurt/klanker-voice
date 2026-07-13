output "bucket_id" {
  description = "Ledger S3 bucket name"
  value       = aws_s3_bucket.ledger.id
}

output "bucket_arn" {
  description = "Ledger S3 bucket ARN"
  value       = aws_s3_bucket.ledger.arn
}

output "bucket_name" {
  description = "Ledger S3 bucket name (alias of bucket_id, for readability at call sites)"
  value       = aws_s3_bucket.ledger.id
}

output "athena_workgroup_name" {
  description = "Athena workgroup name for ad-hoc ledger queries"
  value       = aws_athena_workgroup.ledger.name
}

output "glue_database_name" {
  description = "Glue catalog database name"
  value       = aws_glue_catalog_database.ledger.name
}

output "glue_table_name" {
  description = "Glue catalog table name"
  value       = aws_glue_catalog_table.ledger.name
}
