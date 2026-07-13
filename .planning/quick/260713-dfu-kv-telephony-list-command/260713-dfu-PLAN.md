---
phase: quick-260713-dfu
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
  - kv/internal/app/cmd/telephony.go
  - kv/internal/app/cmd/telephony_test.go
  - kv/internal/app/cmd/root.go
  - kv/go.mod
  - kv/go.sum
autonomous: true
requirements:
  - QUICK-260713-dfu-kv-telephony-list
user_setup: []

must_haves:
  truths:
    - "`kv telephony list` prints one operator table covering DID caller-ID mappings, gate secrets status, and gate config."
    - "DID rows show PHONE (E.164), CODE, TIER, ENABLED for every phone-mapped access code."
    - "PIN + passphrase words are REDACTED by default and only read from SSM (and printed) when --show-secrets is passed."
    - "An operator without SSM permissions still gets the DID + gate-config table; SSM errors are shown as a note, never a crash."
    - "--json emits the full structured report, with secrets still redacted unless --show-secrets is also set."
  artifacts:
    - kv/internal/app/cmd/telephony.go
    - kv/internal/app/cmd/telephony_test.go
  key_links:
    - "telephony parent command registered on root.go's command tree (NewRootCmd)."
    - "SSM client built from the same awsconfig plumbing as Config.DynamoClient; parameters read WithDecryption."
    - "DID scan reuses electro key helpers + Config.DynamoClient / Config.Table plumbing."
---

<objective>
Add a new `kv telephony list` command to the kv Go CLI: one operator table covering the telephony surface — DID caller-ID → access-code mappings (DynamoDB), the DTMF PIN + gate passphrase words (SSM SecureString, redacted by default), and the `[telephony]` gate config (best-effort TOML read).

Purpose: give an operator a single at-a-glance view of the inbound-telephony configuration without hand-querying DynamoDB, SSM, and the pipeline TOML separately.
Output: `kv/internal/app/cmd/telephony.go` (command + data layer), `kv/internal/app/cmd/telephony_test.go` (unit tests), root.go wiring, and the aws-sdk-go-v2 ssm module added to go.mod.
</objective>

<execution_context>
@$HOME/.claude/gsd-core/workflows/execute-plan.md
@$HOME/.claude/gsd-core/templates/summary.md
</execution_context>

<context>
@.planning/STATE.md

# Mirror these exactly — cobra grouping, tabwriter rendering, DynamoClient plumbing, electro key helpers:
@kv/internal/app/cmd/code.go
@kv/internal/app/cmd/voipms.go
@kv/internal/app/cmd/tier.go
@kv/internal/app/cmd/root.go
@kv/internal/app/electro/keys.go

# Test conventions to mirror (table-driven, httptest/interface-injected fakes, skip-if-unreachable):
@kv/internal/app/cmd/voipms_test.go
@kv/internal/app/cmd/code_test.go

# Gate config source + secret param names live here:
@apps/voice/configs/telephony.toml
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Implement kv telephony list (data layer + command + rendering + wiring)</name>
  <files>kv/internal/app/cmd/telephony.go, kv/internal/app/cmd/root.go, kv/go.mod, kv/go.sum</files>
  <behavior>
    - ListPhoneMappings scans the base table with FilterExpression attribute_exists(phone) and returns one PhoneMappingRecord{Phone, Code, TierID, PhoneEnabled} per phone-mapped access code; empty result yields an empty (non-nil-required) slice, no error.
    - readTelephonySecrets is NOT called at all unless --show-secrets is set; when set, it reads the two SSM SecureString params WithDecryption and maps: found -> status "set" + value; ParameterNotFound -> status "not set", no value; AccessDenied/any other error -> status "error" + a short note, never a returned error that aborts the command.
    - parseGateConfig reads the [telephony] block of the config file and extracts gate_mode, require_gate, gate_window_seconds, unlock_tier_id; a missing file yields a "config not found" marker for those fields (not an error).
    - Default (no --show-secrets) text + JSON output never contains the PIN or passphrase VALUE — only a redacted status marker.
  </behavior>
  <action>
Add the aws-sdk-go-v2 SSM module: run `cd kv && go get github.com/aws/aws-sdk-go-v2/service/ssm@latest` (per CLAUDE.md tech-stack; aws-sdk-go-v2 v1.42.x line already present) so go.mod/go.sum gain the ssm service module. NO lipgloss — the repo has none, do not add it.

Create `kv/internal/app/cmd/telephony.go` in package cmd. Structure it after code.go/voipms.go.

Define two narrow interfaces for testability (concrete *dynamodb.Client and *ssm.Client satisfy them, so production code passes the real clients and tests pass fakes):
- `telephonyScanAPI` with method `Scan(ctx, *dynamodb.ScanInput, ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)`.
- `ssmGetParameterAPI` with method `GetParameter(ctx, *ssm.GetParameterInput, ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)`.

Read-path decision (document in a doc comment on ListPhoneMappings): the gsi3/byPhone index (electro.GSI3IndexName) is partitioned per-phone (`phone#${phone}`, see electro/keys.go), so there is NO single partition to Query for "all phone-mapped codes"; and the sparse index's attribute projection does not guarantee tierId. Therefore list via a base-table `Scan` with `FilterExpression: attribute_exists(phone)`, paginating over LastEvaluatedKey exactly like ListAccessCodes' loop. `PhoneMappingRecord` uses dynamodbav tags: `phone`, `code`, `tierId`, `phoneEnabled` (bool); unmarshal with attributevalue.UnmarshalListOfMaps. This is the documented acceptable filtered-scan choice — the table is a small operator table.

Add SSM client plumbing on Config mirroring DynamoClient: `func (c *Config) SSMClient(ctx) (*ssm.Client, error)` that loads awsconfig with region = c.Region if set else "us-east-1" (the /kmv/secrets/use1/* params live in region use1), honoring the ambient credential chain; no EndpointURL override needed.

Secrets: define the two parameter-name constants — the access-pin param `/kmv/secrets/use1/telephony/access_pin` and the passphrase-words param `/kmv/secrets/use1/telephony/passphrase_words`. `readTelephonySecrets(ctx, api, show bool)` returns a SecretsReport with one entry per secret {Name, Status, Value}. When show is false, DO NOT call SSM at all — Status = "hidden — use --show-secrets", Value empty. When show is true, call GetParameter with WithDecryption=true; classify errors by inspecting the error: a ParameterNotFound (types.ParameterNotFound via errors.As) -> Status "not set"; any other error (e.g. AccessDenied) -> Status "error" + a short note derived from the error type, Value empty. Never let an SSM failure abort the command — the DID + gate-config sections must still render.

Gate config (best-effort, NO new TOML dependency — kv has no TOML parser): `parseGateConfig(path string)` opens the file; if os.Open fails with not-exist, return a GateConfigReport{Found:false} (fields shown as "config not found"), NOT an error. When present, do a minimal line scan: find the `[telephony]` header line, then read subsequent lines until the next `[section]` header, and for each `key = value` line extract gate_mode, require_gate, gate_window_seconds, unlock_tier_id — strip surrounding quotes and any trailing ` #`-prefixed inline comment from the value. Document that this is a deliberately minimal scanner scoped to four scalar keys, not a full TOML parse.

Build the command tree: `NewTelephonyCmd(cfg *Config) *cobra.Command` — a `telephony` parent (Short/Long describing the operator telephony overview) with a single `list` subcommand (Args: cobra.NoArgs), mirroring NewVoipmsCmd/NewCodeCmd grouping. The list RunE: build cfg.DynamoClient, call ListPhoneMappings; if --show-secrets, build cfg.SSMClient and call readTelephonySecrets(show=true) else readTelephonySecrets with a nil api and show=false; call parseGateConfig(configPath); assemble a TelephonyListReport{DIDs, Secrets, GateConfig} and render. Flags on list: `--show-secrets` (Bool, default false), `--json` (Bool, default false), and `--config` (String, default "apps/voice/configs/telephony.toml") for the gate-config path.

Rendering: `printTelephony(c, report, asJSON)` mirroring printAccessCodes/printTiers. JSON path: json.NewEncoder with SetIndent("","  ") encoding the TelephonyListReport (SecretsReport entries carry Value with `json:"value,omitempty"` so a redacted/empty value is simply absent — secrets stay out of JSON unless --show-secrets populated them). Text path: a tabwriter DID table with header `PHONE\tCODE\tTIER\tENABLED`, then a short "Gate secrets:" section (one line per secret: name + status, with value only when present), then a "Gate config:" section (gate_mode / require_gate / gate_window_seconds / unlock_tier_id, or "config not found").

Wire it in: in root.go's NewRootCmd, add `root.AddCommand(NewTelephonyCmd(cfg))` alongside the existing NewVoipmsCmd registration.
  </action>
  <verify>
    <automated>cd kv && go build ./... && go vet ./internal/app/cmd/</automated>
  </verify>
  <done>`cd kv && go build ./...` succeeds; `kv telephony list --help` shows --show-secrets/--json/--config; telephony registered on the root command; go.mod contains the ssm service module.</done>
</task>

<task type="auto">
  <name>Task 2: Unit tests with fake DynamoDB + SSM clients (no AWS)</name>
  <files>kv/internal/app/cmd/telephony_test.go</files>
  <action>
Create `kv/internal/app/cmd/telephony_test.go` in package cmd, table-driven, mirroring voipms_test.go's fake-injection style (no live AWS, no dynamodb-local dependency — use in-memory fakes injected through the Task-1 interfaces).

Define a fake Scan client implementing telephonyScanAPI (returns a canned *dynamodb.ScanOutput built from attributevalue.MarshalListOfMaps over PhoneMappingRecord-shaped items, or a configurable error) and a fake SSM client implementing ssmGetParameterAPI (per-parameter-name canned GetParameterOutput or error — support returning a &types.ParameterNotFound{} and a generic AccessDenied-style error).

Cover these cases:
- DID rows render: ListPhoneMappings over a fake returning two phone-mapped items yields two PhoneMappingRecords with the right Phone/Code/TierID/PhoneEnabled.
- empty-DID case: fake returns no items -> empty slice, no error, table renders header only.
- secrets redacted by default: readTelephonySecrets(show=false) with a nil/never-called SSM fake returns "hidden" status and NO value, and the rendered default text output does NOT contain a secret value (assert the fake SSM GetParameter was never invoked).
- secrets revealed with --show-secrets: readTelephonySecrets(show=true) against a fake returning decrypted values yields Status "set" + the value.
- SSM error handled gracefully: fake returns ParameterNotFound for one param and an AccessDenied-style error for the other -> Status "not set" and "error" respectively, and readTelephonySecrets returns no top-level error (command still succeeds).
- --json shape: render TelephonyListReport with asJSON=true and assert (a) it is valid JSON decoding back into the report, (b) with show=false the encoded bytes contain no secret value, (c) with show=true the value is present.
- gate config best-effort: parseGateConfig on a temp file containing a `[telephony]` block returns the four parsed fields; parseGateConfig on a nonexistent path returns Found=false and no error.

Run the whole cmd suite to confirm no regression on the existing dynamodb-local-backed tests (they skip when unreachable).
  </action>
  <verify>
    <automated>cd kv && go test ./internal/app/cmd/ -run 'Telephony|GateConfig|PhoneMapping'</automated>
  </verify>
  <done>New telephony tests pass; `cd kv && go test ./...` is green (pre-existing dynamodb-local tests still skip cleanly when no container is running).</done>
</task>

</tasks>

<threat_model>
## Trust Boundaries

| Boundary | Description |
|----------|-------------|
| operator terminal → SSM SecureString | decrypted PIN/passphrase words cross into stdout only on explicit --show-secrets |
| kv CLI → DynamoDB (kmv-auth-electro) | operator-scoped read of phone-mapped access codes |

## STRIDE Threat Register

| Threat ID | Category | Component | Severity | Disposition | Mitigation Plan |
|-----------|----------|-----------|----------|-------------|-----------------|
| T-dfu-01 | Information Disclosure | PIN/passphrase in default output | high | mitigate | Redact by default; only call SSM (WithDecryption) and print values when --show-secrets is set; a test asserts GetParameter is never invoked and no value appears in the default text/JSON path. |
| T-dfu-02 | Information Disclosure | secrets leaking into --json | high | mitigate | SecretsReport.Value is `json:"value,omitempty"` and is only populated on the --show-secrets path; a test asserts the encoded JSON has no value when show=false. |
| T-dfu-03 | Denial of Service | operator without SSM perms | low | mitigate | SSM AccessDenied/NotFound classified to a status note, never a returned error; DID + gate-config sections still render. |
</threat_model>

<verification>
- `cd kv && go build ./...` and `go vet ./internal/app/cmd/` clean.
- `cd kv && go test ./...` green (new telephony tests pass; existing dynamodb-local tests skip when unreachable).
- `kv telephony list --help` lists --show-secrets, --json, --config.
- Manual scan-read decision documented in a doc comment on ListPhoneMappings.
</verification>

<success_criteria>
- `kv telephony list` renders DID caller-ID mappings, redacted gate-secret status, and gate config in one table.
- Secrets are hidden by default (SSM not even called) and shown only with --show-secrets, in both text and JSON.
- SSM/permission/config-file failures degrade to a note, never a crash.
- telephony command is registered on the root tree; ssm module added to go.mod.
</success_criteria>

<output>
Create `.planning/quick/260713-dfu-kv-telephony-list-command/260713-dfu-SUMMARY.md` when done.
</output>
