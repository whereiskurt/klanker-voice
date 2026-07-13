---
phase: quick-260713-dfu
plan: 01
subsystem: kv-cli
tags: [telephony, cli, dynamodb, ssm, operator-tooling]
dependency-graph:
  requires: []
  provides:
    - kv-telephony-list-command
  affects:
    - kv/internal/app/cmd/root.go
tech-stack:
  added:
    - github.com/aws/aws-sdk-go-v2/service/ssm v1.71.0
  patterns:
    - narrow-interface-injection (telephonyScanAPI/ssmGetParameterAPI) for AWS-free unit tests
    - filtered base-table Scan for a sparse-GSI "list all" access pattern
    - minimal line-scan TOML reader (no new parser dependency) scoped to four scalar keys
key-files:
  created:
    - kv/internal/app/cmd/telephony.go
    - kv/internal/app/cmd/telephony_test.go
  modified:
    - kv/internal/app/cmd/root.go
    - kv/go.mod
    - kv/go.sum
decisions:
  - "DID listing uses a base-table Scan with FilterExpression attribute_exists(phone), not a Query against the gsi3/byPhone index — the index is partitioned per-phone (phone#${phone}), so there is no single partition to query for 'all phone-mapped codes'."
  - "Gate secrets are read from SSM only when --show-secrets is passed; readTelephonySecrets never calls GetParameter otherwise, proven by a test asserting zero fake-client invocations."
  - "SecretEntry.Value carries json:\"value,omitempty\" so a redacted/absent value is fully absent from --json output, not just an empty string."
  - "Gate config uses a deliberately minimal [telephony]-block line scanner (not a TOML library) since kv has no TOML dependency; a missing config file degrades to Found=false, never an error."
metrics:
  duration: ~35min
  completed: 2026-07-13
status: complete
---

# Quick Task 260713-dfu: kv telephony list command Summary

Added `kv telephony list` — a single operator table covering DID caller-ID -> access-code mappings (DynamoDB), the §24 gate secrets (SSM SecureString, redacted by default), and the `[telephony]` pipeline gate config (best-effort TOML-block scan) — so an operator no longer has to hand-query DynamoDB, SSM, and the pipeline TOML separately to see the inbound-telephony configuration at a glance.

## What Was Built

**`kv/internal/app/cmd/telephony.go`** (new):
- `PhoneMappingRecord` + `ListPhoneMappings(ctx, telephonyScanAPI, table)` — paginated base-table `Scan` with `FilterExpression: attribute_exists(phone)`, unmarshaled via `attributevalue.UnmarshalListOfMaps`. Documented in a doc comment why this is a Scan and not a gsi3 Query (the byPhone index is partitioned per-phone, with no single "all mapped codes" partition to query).
- `SecretEntry` / `SecretsReport` + `readTelephonySecrets(ctx, ssmGetParameterAPI, show bool)` — when `show` is false, SSM is never called at all; every entry is `Status: "hidden — use --show-secrets"`. When `show` is true, each of the two gate-secret parameters (`/kmv/secrets/use1/telephony/access_pin`, `/kmv/secrets/use1/telephony/passphrase_words`) is read `WithDecryption`, classifying `ParameterNotFound` -> `"not set"` and any other error (e.g. AccessDenied) -> `"error — <code>"` — never a top-level error that would abort the command.
- `GateConfigReport` + `parseGateConfig(path)` / `scanTelephonyBlock` / `parseTOMLScalarLine` — a minimal line scanner that finds the `[telephony]` header, reads until the next `[section]`, and extracts `gate_mode`, `require_gate`, `gate_window_seconds`, `unlock_tier_id`, stripping quotes and trailing ` #`-comments. A missing file returns `Found: false`, not an error.
- `Config.SSMClient(ctx)` — mirrors `Config.DynamoClient`, defaulting region to `us-east-1` (where the `/kmv/secrets/use1/*` params live) when `c.Region` is unset.
- `NewTelephonyCmd(cfg)` — a `telephony` parent with one `list` subcommand (`--show-secrets`, `--json`, `--config` flags), assembling and rendering a `TelephonyListReport{DIDs, Secrets, GateConfig}`.
- `printTelephony` — JSON path via `json.NewEncoder(...).SetIndent("", "  ")`; text path via `text/tabwriter` for the DID table (`PHONE\tCODE\tTIER\tENABLED`), then a "Gate secrets:" section and a "Gate config:" section.

**`kv/internal/app/cmd/root.go`**: added `root.AddCommand(NewTelephonyCmd(cfg))` alongside the existing `NewVoipmsCmd` registration.

**`kv/go.mod`/`kv/go.sum`**: added `github.com/aws/aws-sdk-go-v2/service/ssm v1.71.0` via `go get`, then promoted it (plus its transitive `smithy-go` and the already-used `pion/webrtc/v4`) to direct requires via `go mod tidy` — no lipgloss, no new non-AWS dependency.

**`kv/internal/app/cmd/telephony_test.go`** (new): table-driven unit tests using an in-memory `fakeTelephonyScanClient` (implements `telephonyScanAPI` via `attributevalue.MarshalMap`) and `fakeSSMGetParameterClient` (implements `ssmGetParameterAPI`, tracks every parameter name it was actually called with). Covers:
- DID rows render correctly from a fake Scan response; empty scan yields an empty (non-nil) slice and a header-only table.
- `readTelephonySecrets(show=false)` never invokes the fake SSM client at all, and the rendered default text output contains no secret value.
- `readTelephonySecrets(show=true)` returns the decrypted values with `Status: "set"`.
- A fake `*ssmtypes.ParameterNotFound` and a fake `AccessDenied`-shaped `smithy.GenericAPIError` are classified to `"not set"` / `"error — ..."` respectively, with `readTelephonySecrets` itself never returning an error.
- `--json` output is valid JSON both ways: with `show=false` no `"value"` key appears anywhere in the encoded bytes; with `show=true` the revealed value is present.
- `parseGateConfig` on a temp file with a realistic `[telephony]` block (matching `apps/voice/configs/telephony.toml`'s shape, including inline comments) extracts all four fields; on a nonexistent path it returns `Found: false` with no error.
- Command-tree wiring: `kv telephony list` has `--show-secrets`/`--json`/`--config`, and `telephony` is registered on `NewRootCmd()`.

## Verification

```
cd kv && go build ./...                                          # clean
cd kv && go vet ./internal/app/cmd/                               # clean
cd kv && go test ./internal/app/cmd/ -run 'Telephony|GateConfig|PhoneMapping'  # 14/14 pass (incl. pre-existing phone-mapping tests)
cd kv && go test ./...                                            # ok (cmd + electro packages; no dynamodb-local running in this sandbox — round-trip tests skip cleanly)
kv telephony list --help                                          # lists --show-secrets, --json, --config
```

## Deviations from Plan

None — plan executed exactly as written. One incidental cleanup: ran `go mod tidy` after `go get`, which promoted `aws-sdk-go-v2/service/ssm`, `aws/smithy-go`, and `pion/webrtc/v4` from indirect to direct requires in `go.mod` (smithy-go is now directly imported by the test file for the fake AccessDenied error; the other two were already directly used elsewhere in the module but mis-marked indirect before this plan's `go get` touched go.mod). No dependency versions changed, no new third-party packages beyond the plan's own `service/ssm` addition.

## Known Stubs

None. `kv telephony list` is a fully functional read path against real DynamoDB/SSM/TOML sources — nothing hardcoded or mocked in the shipped command.

## Threat Flags

None beyond what the plan's own threat model already covers (T-dfu-01/02/03, all mitigated as designed and proven by the show=false/AccessDenied tests above).

## Self-Check: PASSED

- FOUND: kv/internal/app/cmd/telephony.go
- FOUND: kv/internal/app/cmd/telephony_test.go
- FOUND: .planning/quick/260713-dfu-kv-telephony-list-command/260713-dfu-SUMMARY.md
- FOUND: commit 73a384a (feat: kv telephony list data layer + command + wiring)
- FOUND: commit 50276b1 (test: unit tests with fake DynamoDB + SSM clients)
