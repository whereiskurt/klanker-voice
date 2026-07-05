# Secrets Management

This directory uses SOPS for encrypted secrets management. The filenames below
match what `site.hcl` actually reads — do not invent other names.

## How It Works

At parse time (every plan/apply), `site.hcl` resolves `secret_values`:

1. If `.secrets.sops.json` exists → decrypt on the fly via `sops --decrypt`
2. Else if `.secrets.json` exists → read the plaintext fallback (gitignored)
3. Else → `{}` (secret-consuming units will plan empty values)

The decrypted JSON feeds the `secrets` module, which writes SSM SecureString
parameters at `/kmv/secrets/use1/<name>/<key>`.

## Files

| File | Git? | Description |
|------|------|-------------|
| `.secrets.sops.json.template` | Yes | Template with placeholder values (the six kmv secrets) |
| `.secrets.json` | **No** | Plaintext fallback (gitignored — TEMPORARY until the SOPS key exists) |
| `.secrets.sops.json` | Yes | SOPS encrypted (safe to commit once created) |
| `../../../../.sops.yaml` | Yes | SOPS config with the KMS key (repo root) |

## Quick Start (once the SOPS KMS key exists)

```bash
# 1. Start from the template
cp .secrets.sops.json.template /tmp/.secrets.json
# Edit /tmp/.secrets.json with real values

# 2. Encrypt it (uses the repo-root .sops.yaml creation rule)
sops encrypt /tmp/.secrets.json > .secrets.sops.json
rm /tmp/.secrets.json

# 3. Remove the plaintext fallback if present (site.hcl prefers the sops file)
rm -f .secrets.json

# 4. Commit the encrypted file (safe!)
git add .secrets.sops.json
git commit -m "Add encrypted secrets"
```

## Editing Encrypted Secrets

```bash
# Edit in place (decrypts in memory, re-encrypts on save)
sops edit .secrets.sops.json
```

## Secret Shape (must match site.hcl secrets.definitions)

```json
{
  "deepgram":   { "api_key": "..." },
  "anthropic":  { "api_key": "..." },
  "elevenlabs": { "api_key": "..." },
  "jwt":        { "secret": "...", "internal_secret": "..." },
  "oidc":       { "cookie_keys": "..." },
  "altcha":     { "secret": "..." }
}
```

## KMS Key Setup (single-region)

```bash
aws kms create-key --profile klanker-terraform --region us-east-1 \
  --description "SOPS secrets encryption (kmv)"
aws kms create-alias --alias-name alias/sops --target-key-id <KeyId> \
  --profile klanker-terraform --region us-east-1
```

Repo-root `.sops.yaml`:

```yaml
creation_rules:
  - path_regex: \.secrets(\.sops)?\.json$
    kms: "arn:aws:kms:us-east-1:052251888500:alias/sops"
```

Persist the key id: `TF_VAR_SOPS_KMS_KEY_ID` in `infra/.envrc` and as a GitHub
repo variable (the github-oidc kms-sops-decrypt policies interpolate it).

## CI/CD

CI roles decrypt `.secrets.sops.json` via KMS (the readonly/deploy/release
roles carry `kms:Decrypt` on the SOPS key). No plaintext secrets in CI env.
