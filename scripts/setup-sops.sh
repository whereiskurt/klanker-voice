#!/bin/bash
set -euo pipefail

## setup-sops.sh — Create the single-region KMS key for SOPS encryption (kmv).
##
## Single-region adaptation of the defcon.run.34 env.sops.sh (D-03):
##   - keeps: alias existence check, .sops.yaml writer, TF_VAR persistence,
##     gh-variable printout
##   - drops: the replica-regions loop and the --multi-region key flag
##
## Idempotent: safe to run repeatedly. If alias/sops already resolves, the
## existing key is reused; .sops.yaml and infra/.envrc are (re)written with
## the same values.
##
## Prerequisites:
##   - AWS CLI v2, gh CLI (authenticated), SSO session active for the
##     klanker-terraform profile (aws sso login --sso-session=Developer)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

AWS_PROFILE="klanker-terraform"
REGION="us-east-1"
ACCOUNT_ID="${TF_VAR_APPLICATION_ACCOUNT_ID:-052251888500}"
ALIAS_NAME="alias/sops"
GITHUB_REPO="whereiskurt/klanker-voice"

echo "=== SOPS KMS Key Setup (kmv, single-region) ==="
echo "  AWS Profile: ${AWS_PROFILE}"
echo "  Account ID:  ${ACCOUNT_ID}"
echo "  Region:      ${REGION}"
echo ""

## Step 1: Reuse the key if alias/sops already resolves (idempotence).
KEY_ID=""
KEY_ID=$(aws kms describe-key \
  --profile "${AWS_PROFILE}" \
  --region "${REGION}" \
  --key-id "${ALIAS_NAME}" \
  --query "KeyMetadata.KeyId" \
  --output text 2>/dev/null) || true

if [[ -n "${KEY_ID}" && "${KEY_ID}" != "None" ]]; then
  echo "Key alias '${ALIAS_NAME}' already exists in ${REGION} — reusing."
  echo "  Key ID: ${KEY_ID}"
else
  ## Step 2: Create a single-region key (NO --multi-region flag — D-03).
  echo "Creating single-region KMS key in ${REGION}..."
  KEY_ID=$(aws kms create-key \
    --profile "${AWS_PROFILE}" \
    --region "${REGION}" \
    --description "SOPS secrets encryption (kmv)" \
    --query "KeyMetadata.KeyId" \
    --output text)
  echo "  Created key: ${KEY_ID}"

  echo "Creating alias '${ALIAS_NAME}' in ${REGION}..."
  aws kms create-alias \
    --profile "${AWS_PROFILE}" \
    --region "${REGION}" \
    --alias-name "${ALIAS_NAME}" \
    --target-key-id "${KEY_ID}"
fi

## Step 3: Write .sops.yaml at the repo root — ONE regional ARN (D-03).
SOPS_YAML="${REPO_ROOT}/.sops.yaml"
KMS_ARN="arn:aws:kms:${REGION}:${ACCOUNT_ID}:${ALIAS_NAME}"
echo ""
echo "Writing ${SOPS_YAML}..."
cat > "${SOPS_YAML}" <<EOF
creation_rules:
  - path_regex: \.secrets(\.sops)?\.json\$
    kms: "${KMS_ARN}"
EOF
echo "  Done."

## Step 4: Persist TF_VAR_SOPS_KMS_KEY_ID into infra/.envrc.
##
## site.hcl's github_oidc kms-sops-decrypt policies interpolate this key ID.
## Without persistence, every fresh shell / CI run resolves it to the empty
## placeholder and `terragrunt apply` on github-oidc drifts the live IAM
## policies back to a bogus ARN (research Pitfall 5).
ENVRC="${REPO_ROOT}/infra/.envrc"
echo ""
echo "Persisting TF_VAR_SOPS_KMS_KEY_ID=${KEY_ID} to ${ENVRC}..."
if grep -q "^export TF_VAR_SOPS_KMS_KEY_ID=" "${ENVRC}" 2>/dev/null; then
  tmp="${ENVRC}.tmp"
  sed "s|^export TF_VAR_SOPS_KMS_KEY_ID=.*|export TF_VAR_SOPS_KMS_KEY_ID=${KEY_ID}|" \
    "${ENVRC}" > "${tmp}"
  mv "${tmp}" "${ENVRC}"
else
  printf '\n# Written by setup-sops.sh\nexport TF_VAR_SOPS_KMS_KEY_ID=%s\n' \
    "${KEY_ID}" >> "${ENVRC}"
fi
echo "  Done."

## Step 5: Set the matching GitHub repository variable (CI does not source
## infra/.envrc — workflows read vars.TF_VAR_SOPS_KMS_KEY_ID).
echo ""
echo "=== CI setup ==="
echo "  gh variable set TF_VAR_SOPS_KMS_KEY_ID --repo ${GITHUB_REPO} --body \"${KEY_ID}\""
gh variable set TF_VAR_SOPS_KMS_KEY_ID --repo "${GITHUB_REPO}" --body "${KEY_ID}"
echo "  Repository variable set."

echo ""
echo "SOPS setup complete. Key ID: ${KEY_ID}"
