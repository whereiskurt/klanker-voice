#!/usr/bin/env bash
# bootstrap-state.sh — idempotent terragrunt state-backend bootstrap (D-05).
#
# Creates (or verifies) the versioned + encrypted S3 state bucket and the
# DynamoDB lock table, both named tf-kmv-use1-<SGUID>, in the state account
# (052251888500) under the klanker-terraform profile. Safe to re-run: a second
# invocation with the same SGUID is a clean no-op that only re-asserts the
# bucket security posture (versioning, encryption, public-access block).
#
# Usage:
#   scripts/bootstrap-state.sh [SGUID]
#
# SGUID is the single source of truth for the state suffix (research Pitfall 3):
# the same value must appear in the bucket/table name, infra/.envrc, the
# site.hcl random_suffix default, and the GitHub repo var SGUID.

set -euo pipefail

PROFILE="klanker-terraform"
REGION="us-east-1"
SITE_LABEL="kmv"
REGION_LABEL="use1"

# --- SGUID: accept as $1 or generate (first 8 hex of uuidgen, lowercased) ----
if [[ $# -ge 1 && -n "${1:-}" ]]; then
  SGUID="$1"
else
  SGUID="$(uuidgen | tr '[:upper:]' '[:lower:]' | tr -d '-' | cut -c1-8)"
fi

if ! [[ "$SGUID" =~ ^[0-9a-f]{8}$ ]]; then
  echo "ERROR: SGUID must be exactly 8 lowercase hex chars, got: $SGUID" >&2
  exit 1
fi

NAME="tf-${SITE_LABEL}-${REGION_LABEL}-${SGUID}"
BUCKET="$NAME"
TABLE="$NAME"

echo "=============================================================="
echo " SGUID:  $SGUID   <-- single source of truth (site.hcl / CI)"
echo " Bucket: $BUCKET"
echo " Table:  $TABLE"
echo " Account (via $PROFILE): $(aws sts get-caller-identity --profile "$PROFILE" --query Account --output text)"
echo "=============================================================="

# --- S3 bucket: head-or-create (us-east-1 needs no LocationConstraint) -------
if aws s3api head-bucket --bucket "$BUCKET" --profile "$PROFILE" --region "$REGION" 2>/dev/null; then
  echo "[ok] bucket exists: $BUCKET"
else
  echo "[create] bucket: $BUCKET"
  aws s3api create-bucket --bucket "$BUCKET" --profile "$PROFILE" --region "$REGION"
fi

# --- Always (re)apply bucket posture -----------------------------------------
echo "[apply] versioning: Enabled"
aws s3api put-bucket-versioning --bucket "$BUCKET" --profile "$PROFILE" --region "$REGION" \
  --versioning-configuration Status=Enabled

echo "[apply] default encryption: SSE-S3 (AES256)"
aws s3api put-bucket-encryption --bucket "$BUCKET" --profile "$PROFILE" --region "$REGION" \
  --server-side-encryption-configuration '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"AES256"}}]}'

# Terraform state embeds secret values (threat T-2-02) — all four flags true.
echo "[apply] public access block: all four true"
aws s3api put-public-access-block --bucket "$BUCKET" --profile "$PROFILE" --region "$REGION" \
  --public-access-block-configuration \
  BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true

# --- DynamoDB lock table: describe-or-create ---------------------------------
if aws dynamodb describe-table --table-name "$TABLE" --profile "$PROFILE" --region "$REGION" >/dev/null 2>&1; then
  echo "[ok] lock table exists: $TABLE"
else
  echo "[create] lock table: $TABLE"
  aws dynamodb create-table --table-name "$TABLE" \
    --attribute-definitions AttributeName=LockID,AttributeType=S \
    --key-schema AttributeName=LockID,KeyType=HASH \
    --billing-mode PAY_PER_REQUEST \
    --profile "$PROFILE" --region "$REGION" >/dev/null
  echo "[wait] table ACTIVE..."
  aws dynamodb wait table-exists --table-name "$TABLE" --profile "$PROFILE" --region "$REGION"
fi

# --- Outputs ------------------------------------------------------------------
echo ""
echo "# ---- environment exports (mirror these in infra/.envrc) ----"
echo "export TG_BUCKET_USE1=$BUCKET"
echo "export TG_TABLE_USE1=$TABLE"
echo "export SGUID=$SGUID"
echo ""
echo "# ---- GitHub repo variables for CI (Plan 06 runs these) ----"
echo "gh variable set SITE_LABEL --body $SITE_LABEL"
echo "gh variable set SGUID --body $SGUID"
echo "gh variable set AWS_ACCOUNT_ID --body 052251888500"
echo ""
echo "[done] state backend ready: $NAME"
