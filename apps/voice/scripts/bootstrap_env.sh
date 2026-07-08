#!/usr/bin/env bash
# bootstrap_env.sh — D-10 key bootstrap: SSM /kmv/secrets/use1/* -> apps/voice/.env
#
# Reads the three provider API keys from AWS SSM SecureString parameters using
# the klanker-application profile (us-east-1) and writes apps/voice/.env with
# mode 600. SSM is the single source of truth; nothing plaintext in the repo.
#
# Parameter layout note: the deployed secrets live under the region-qualified
# /kmv/secrets/use1/{provider}/api_key tree (the same tree the auth service
# reads), NOT the old /kmv/bootstrap/{provider}_api_key paths.
#
# Security invariants:
#   - never echoes a secret value to stdout/stderr
#   - umask 077 before writing; chmod 600 the result
#   - refuses to run with shell xtrace enabled
#   - fails fast: no partial .env if any parameter fetch fails
#   - refuses to write .env outside apps/voice

set -euo pipefail

# Refuse to run with xtrace enabled — it would echo secret values.
case "$-" in
  *x*)
    echo "ERROR: refusing to run with xtrace (set -x) enabled — it would leak secrets." >&2
    exit 1
    ;;
esac

AWS_PROFILE_NAME="klanker-application"
AWS_REGION="us-east-1"

# Resolve the target directory from the script's own location: scripts/ -> apps/voice.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd -P)"
TARGET_DIR="$(cd "${SCRIPT_DIR}/.." >/dev/null 2>&1 && pwd -P)"

# Refuse to write .env anywhere but apps/voice.
case "${TARGET_DIR}" in
  */apps/voice) ;;
  *)
    echo "ERROR: resolved target directory '${TARGET_DIR}' is not apps/voice — refusing to write .env." >&2
    exit 1
    ;;
esac

ENV_FILE="${TARGET_DIR}/.env"

# param-name:ENV_VAR_NAME pairs (exact names pipecat's services/dotenv loading expect)
PARAMS=(
  "/kmv/secrets/use1/deepgram/api_key:DEEPGRAM_API_KEY"
  "/kmv/secrets/use1/anthropic/api_key:ANTHROPIC_API_KEY"
  "/kmv/secrets/use1/elevenlabs/api_key:ELEVENLABS_API_KEY"
)

fetch_param() {
  # Prints the parameter VALUE on stdout for capture only — callers must never
  # forward it to the terminal.
  local name="$1"
  aws ssm get-parameter \
    --with-decryption \
    --profile "${AWS_PROFILE_NAME}" \
    --region "${AWS_REGION}" \
    --name "${name}" \
    --query Parameter.Value \
    --output text
}

# Fetch all three values BEFORE writing anything — no partial .env on failure.
VALUES=()
for pair in "${PARAMS[@]}"; do
  param_name="${pair%%:*}"
  if ! value="$(fetch_param "${param_name}")" || [ -z "${value}" ] || [ "${value}" = "None" ]; then
    echo "ERROR: failed to fetch SSM parameter '${param_name}' (profile ${AWS_PROFILE_NAME}, region ${AWS_REGION})." >&2
    echo "       No .env was written. Authenticate the profile (e.g. 'aws sso login --profile ${AWS_PROFILE_NAME}') and retry." >&2
    exit 1
  fi
  echo "fetched ${param_name}"
  VALUES+=("${value}")
done

# Write atomically with restrictive permissions.
umask 077
TMP_FILE="$(mktemp "${TARGET_DIR}/.env.XXXXXX")"
trap 'rm -f "${TMP_FILE}"' EXIT

{
  for i in "${!PARAMS[@]}"; do
    env_name="${PARAMS[$i]#*:}"
    printf '%s=%s\n' "${env_name}" "${VALUES[$i]}"
  done
} > "${TMP_FILE}"

chmod 600 "${TMP_FILE}"
mv "${TMP_FILE}" "${ENV_FILE}"
trap - EXIT

echo "wrote ${ENV_FILE} (mode 600, 3 keys)"
