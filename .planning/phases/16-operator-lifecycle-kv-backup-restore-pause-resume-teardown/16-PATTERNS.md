# Phase 16: Operator Lifecycle — Pattern Map

**Mapped:** 2026-08-19
**Files analyzed:** 8 new/modified
**Analogs found:** 6 / 8

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `kv/internal/app/cmd/backup.go` | command | CRUD + file-I/O | `kv/internal/app/cmd/code.go` | role-match |
| `kv/internal/app/cmd/restore.go` | command | CRUD + file-I/O | `kv/internal/app/cmd/code.go` | role-match |
| `kv/internal/app/cmd/pause.go` | command | request-response + git orchestration | `kv/internal/app/cmd/killswitch.go` | exact |
| `kv/internal/app/cmd/destroy.go` | command | request-response + orchestration | `kv/internal/app/cmd/killswitch.go` | role-match |
| `kv/internal/app/cmd/root.go` | coordinator | request-response | `kv/internal/app/cmd/root.go` | exact |
| `infra/terraform/live/site/site.hcl` | config | request-response | `infra/terraform/live/site/site.hcl` | exact |
| `.gitignore` | config | N/A | `.gitignore` | exact |
| Internal backup/restore packages | service | CRUD + file-I/O | `kv/internal/app/cmd/voipms.go` | role-match |

## Pattern Assignments

### `kv/internal/app/cmd/root.go` (modification: register new commands)

**Analog:** `kv/internal/app/cmd/root.go` (lines 145-153)

**Command registration pattern** (lines 145-153):
```go
root.AddCommand(NewCodeCmd(cfg))
root.AddCommand(NewTierCmd(cfg))
root.AddCommand(NewSmokeCmd(cfg))
root.AddCommand(NewUsageCmd(cfg))
root.AddCommand(NewKillswitchCmd(cfg))
root.AddCommand(NewKnowledgeCmd(cfg))
root.AddCommand(NewVoipmsCmd(cfg))
root.AddCommand(NewTelephonyCmd(cfg))
root.AddCommand(NewStudioCmd(cfg))
```

**Add these three new commands in root.go's NewRootCmd():**
```go
root.AddCommand(NewBackupCmd(cfg))
root.AddCommand(NewRestoreCmd(cfg))
root.AddCommand(NewPauseCmd(cfg))   // paired with resume subcommand inside
root.AddCommand(NewDestroyCmd(cfg))
```

**Constructor naming convention** — all commands follow `New<Name>Cmd(*Config) *cobra.Command`:
```go
// killswitch.go example (line 152):
func NewKillswitchCmd(cfg *Config) *cobra.Command {
    killswitchCmd := &cobra.Command{
        Use:   "killswitch",
        Short: "View or flip the site-wide voice kill-switch (D-08/D-09)",
    }
    // ... subcommands attached via AddCommand
    return killswitchCmd
}
```

---

### `kv/internal/app/cmd/backup.go` (command, CRUD + file-I/O)

**Analog:** `kv/internal/app/cmd/code.go` (lines 1-50)

**Imports pattern** (lines 1-22 of code.go):
```go
package cmd

import (
    "context"
    "encoding/json"
    "fmt"
    "os"
    "strconv"
    "strings"
    "text/tabwriter"
    "time"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
    "github.com/aws/aws-sdk-go-v2/service/dynamodb"
    "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
    "github.com/spf13/cobra"

    "github.com/whereiskurt/klanker-voice/kv/internal/app/electro"
)
```

**For backup/restore, add:**
```go
    "archive/zip"
    "crypto/sha256"
    "io"
    "path/filepath"
    "github.com/aws/aws-sdk-go-v2/service/s3"
    // For external DID/EIP exports, also:
    "github.com/whereiskurt/klanker-voice/kv/internal/app/cmd" // reuse voipms client
```

**AWS client construction pattern** (from telephony.go, lines 414-420; matches root.go's Config.DynamoClient):
```go
// In root.go, extend the Config struct with new client methods:
func (c *Config) S3Client(ctx context.Context) (*s3.Client, error) {
    cfg, err := c.loadAWS(ctx)
    if err != nil {
        return nil, err
    }
    return s3.NewFromConfig(cfg), nil
}

func (c *Config) ECSClient(ctx context.Context) (*ecs.Client, error) {
    cfg, err := c.loadAWS(ctx)
    if err != nil {
        return nil, err
    }
    return ecs.NewFromConfig(cfg), nil
}
```

**DynamoDB Scan pattern** (from telephony.go, lines 60-82):
```go
// Scan with pagination loop and FilterExpression
func scanTable(ctx context.Context, api *dynamodb.Client, table string) ([]map[string]types.AttributeValue, error) {
    var out []map[string]types.AttributeValue
    var lastKey map[string]types.AttributeValue
    for {
        resp, err := api.Scan(ctx, &dynamodb.ScanInput{
            TableName:         aws.String(table),
            ExclusiveStartKey: lastKey,
        })
        if err != nil {
            return nil, fmt.Errorf("scan table: %w", err)
        }
        out = append(out, resp.Items...)
        if resp.LastEvaluatedKey == nil {
            break
        }
        lastKey = resp.LastEvaluatedKey
    }
    return out, nil
}
```

**Command structure** (from code.go, lines 55-71 for CreateAccessCode function shape; apply to backup operation):
```go
// One function per major operation (mirrors CreateAccessCode/ListAccessCodes/ExpireAccessCode pattern)
func BackupDynamoDBTable(ctx context.Context, client *dynamodb.Client, table string) ([]byte, error) {
    // Scan + serialize to JSONL
    // Return raw bytes for zip writing
}

func BackupS3Ledger(ctx context.Context, client *s3.Client, bucket string) ([]byte, error) {
    // Walk S3 prefix, collect objects, preserve key structure
}
```

---

### `kv/internal/app/cmd/restore.go` (command, CRUD + file-I/O)

**Analog:** `kv/internal/app/cmd/code.go` (CreateAccessCode pattern, lines 55-71)

**Core pattern — PutItem with batching and idempotency**:
```go
// From code.go:55-71
func CreateAccessCode(ctx context.Context, client *dynamodb.Client, table, code, tierID, group string, expiresAt, maxRedemptions *int64) error {
    if err := validateCodeCharset(code); err != nil {
        return err
    }
    // ... validation ...
    item := electro.NewAccessCodeItem(code, tierID, group, expiresAt, maxRedemptions)
    _, err := client.PutItem(ctx, &dynamodb.PutItemInput{
        TableName: aws.String(table),
        Item:      item.Marshal(),
    })
    if err != nil {
        return fmt.Errorf("put access code %q: %w", code, err)
    }
    return nil
}
```

**Restore must:**
1. Parse JSONL from the zip
2. Unmarshal into DynamoDB AttributeValue maps
3. Filter ephemeral rows (concurrency leases, OIDC sessions) before PutItem
4. Batch PutItem calls with retry/backoff (AWS SDK v2 pattern; check if BatchWriteItem exists)
5. Support `--dry-run` by counting without writing

**Resume target resolution** — resolve table/bucket names from terraform outputs (like studio.go resolves repo root):
```go
// From knowledge.go:19-25 (repoRoot pattern for finding terraform outputs)
func resolveTerrainformOutput(ctx context.Context, outputName string) (string, error) {
    out, err := exec.Command("terraform", "output", "-raw", outputName).Output()
    if err != nil {
        return "", fmt.Errorf("resolve terraform output %s: %w", outputName, err)
    }
    return strings.TrimSpace(string(out)), nil
}
```

---

### `kv/internal/app/cmd/pause.go` (command, request-response + git orchestration)

**Analog:** `kv/internal/app/cmd/killswitch.go` (lines 152-227)

**Command structure with subcommands** (killswitch.go, lines 152-227):
```go
// Parent command with on/off/status subcommands
func NewKillswitchCmd(cfg *Config) *cobra.Command {
    killswitchCmd := &cobra.Command{
        Use:   "killswitch",
        Short: "View or flip the site-wide voice kill-switch (D-08/D-09)",
    }

    var statusJSON bool
    status := &cobra.Command{
        Use:   "status",
        Short: "Show the kill-switch's current engaged/reason/ceiling state",
        Args:  cobra.NoArgs,
        RunE: func(c *cobra.Command, args []string) error {
            // ... implementation ...
        },
    }
    status.Flags().BoolVar(&statusJSON, "json", false, "output as JSON")
    killswitchCmd.AddCommand(status)

    var onReason string
    on := &cobra.Command{
        Use:   "on",
        Short: "Engage the kill-switch (pauses every new voice session site-wide)",
        Args:  cobra.NoArgs,
        RunE: func(c *cobra.Command, args []string) error {
            // ... implementation ...
        },
    }
    on.Flags().StringVar(&onReason, "reason", "", "reason recorded")
    killswitchCmd.AddCommand(on)

    return killswitchCmd
}
```

**Apply to pause/resume:**
```go
func NewPauseCmd(cfg *Config) *cobra.Command {
    pauseCmd := &cobra.Command{
        Use:   "pause",
        Short: "Pause ECS services (scale to zero, ~$130/mo savings)",
    }

    var yes bool
    var reason string

    // pause status subcommand
    status := &cobra.Command{
        Use:   "status",
        Short: "Show current pause state",
        RunE: func(c *cobra.Command, args []string) error {
            // Read site.hcl, report paused boolean
        },
    }
    pauseCmd.AddCommand(status)

    // pause execute subcommand (main action)
    execute := &cobra.Command{
        Use:   "execute",
        Short: "Commit and dispatch pause via GitHub Actions",
        RunE: func(c *cobra.Command, args []string) error {
            // 1. Preflight (git status, branch, origin sync)
            // 2. Rewrite site.hcl paused = true
            // 3. Commit + push to main
            // 4. Dispatch terragrunt-apply.yml --ref main -f modules=ecs-service
            // 5. Verify via ECS describe-services (poll until desired==running==0)
        },
    }
    execute.Flags().BoolVar(&yes, "yes", false, "skip confirmation")
    execute.Flags().StringVar(&reason, "reason", "", "operator annotation")
    pauseCmd.AddCommand(execute)

    return pauseCmd
}
```

**Git orchestration pattern** (from knowledge.go, lines 13-25):
```go
// repoRoot resolves the klanker-voice repo root via `git rev-parse --show-toplevel`
func repoRoot() (string, error) {
    out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
    if err != nil {
        return "", fmt.Errorf("resolve repo root (git rev-parse --show-toplevel): %w", err)
    }
    return strings.TrimSpace(string(out)), nil
}

// Shell-out pattern for pause (mirrors uv run python in knowledge.go:86-91):
runCmd := exec.CommandContext(c.Context(), "git", "status", "--porcelain")
runCmd.Dir = root
output, err := runCmd.Output()  // captures stdout
if err != nil {
    return fmt.Errorf("git status: %w", err)
}
if len(output) > 0 {
    return fmt.Errorf("working tree is dirty: commit or stash changes first")
}
```

**Testability seams for shell-outs** — isolate git/gh/terraform commands behind narrow interfaces (spec §7.1):
```go
// Define interfaces for injection (mirrors studio.go's didRouterFunc pattern):
type gitAPI interface {
    Status(ctx context.Context) (bool, error)      // true = clean
    Commit(ctx context.Context, msg string) error
    Push(ctx context.Context) error
}

type ghAPI interface {
    WorkflowRun(ctx context.Context, workflow, ref, modules string) (runID string, err error)
    PollRun(ctx context.Context, runID string, timeout time.Duration) (success bool, err error)
}

type ecsAPI interface {
    DescribeServices(ctx context.Context, cluster, services ...string) ([]ServiceStatus, error)
    UpdateService(ctx context.Context, cluster, service string, desiredCount int) error
}

// In kv pause orchestration, inject these; in tests, provide fakes
func pauseServices(ctx context.Context, git gitAPI, gh ghAPI, ecs ecsAPI, yes bool) error {
    if !yes {
        // Show diff
        diff, _ := git.Diff(ctx)
        fmt.Println(diff)
        // Confirm
    }
    _ = git.Commit(ctx, "ops(infra): pause ECS services (desired_count=0)")
    _ = git.Push(ctx)
    runID, _ := gh.WorkflowRun(ctx, "terragrunt-apply.yml", "main", "ecs-service")
    success, _ := gh.PollRun(ctx, runID, 10*time.Minute)
    // Verify via ECS polling...
}
```

---

### `kv/internal/app/cmd/destroy.go` (command, request-response + orchestration)

**Analog:** `kv/internal/app/cmd/killswitch.go` (lines 152-227)

**Orchestration pattern** — sequence backup → verify → drain → empty ledger → destroy:
```go
func NewDestroyCmd(cfg *Config) *cobra.Command {
    destroyCmd := &cobra.Command{
        Use:   "destroy",
        Short: "Tear down the AWS footprint with a backup (default) or without confirmation",
    }

    var withBackup bool  // default true
    var dryRun bool

    destroy := &cobra.Command{
        Use:   "execute",
        Short: "Execute the destroy sequence (backup → drain → destroy)",
        RunE: func(c *cobra.Command, args []string) error {
            ctx := c.Context()

            // 1. Backup (mandatory unless --no-backup passed)
            if withBackup {
                if err := BackupDynamoDBTable(ctx, ...); err != nil {
                    return fmt.Errorf("backup failed; aborting destroy: %w", err)
                }
                // Verify the backup's row counts and SHA256
            }

            // 2. Drain (reuse pause's ECS path)
            if err := drainToZero(ctx, ...); err != nil {
                return fmt.Errorf("drain to zero failed: %w", err)
            }

            // 3. Empty ledger bucket explicitly (no force_destroy)
            if !dryRun {
                if err := emptyS3Bucket(ctx, ...); err != nil {
                    return fmt.Errorf("empty ledger bucket: %w", err)
                }
            }

            // 4. Terragrunt destroy in dependency order
            if !dryRun {
                if err := terragruntDestroy(ctx, ...); err != nil {
                    return fmt.Errorf("terragrunt destroy: %w", err)
                }
            }

            // 5. Report (backup path, size, NAT EIP gone, DIDs still provisioned)
            fmt.Fprintf(c.OutOrStdout(), "Destroy complete.\nBackup: %s (size: %d bytes)\nDIDs still provisioned on VoIP.ms — release manually.\n", backupPath, size)

            return nil
        },
    }
    destroy.Flags().BoolVar(&withBackup, "with-backup", true, "create backup before destroy (default true)")
    destroy.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be destroyed without destroying")
    destroyCmd.AddCommand(destroy)

    return destroyCmd
}
```

---

### `infra/terraform/live/site/site.hcl` (modification: add paused flag)

**Current state** (lines 159-164):
```hcl
  ecs_services = {
    # Phase 4 (04-02): voice service. Phase 5 deploy: auth service added.
    # Phase 12 (12-07): telephony-edge service added.
    enabled  = true
    services = [local.service_conf.voice.locals.service, local.service_conf.auth.locals.service, local.service_conf.telephony_edge.locals.service]
  }
```

**Add paused flag above ecs_services** (from CONTEXT.md, spec §5.1):
```hcl
  # Operator pause switch (kv pause / kv resume — avoid editing by hand).
  # true => every ECS service runs zero tasks. Nothing else changes: VPC,
  # NAT+EIP, ALB, WAF, CloudFront, Route53, ACM, DynamoDB, the S3 ledger and
  # ECR all stay put — which is what keeps the VoIP.ms-allowlisted NAT EIP
  # alive and makes resume a pure scale-up.
  paused = false

  ecs_services = {
    # Phase 4 (04-02): voice service. Phase 5 deploy: auth service added.
    # Phase 12 (12-07): telephony-edge service added.
    enabled = true
    services = [
      for s in [local.service_conf.voice.locals.service, local.service_conf.auth.locals.service, local.service_conf.telephony_edge.locals.service] :
      local.paused ? merge(s, {
        desired_count = 0
        autoscaling   = merge(s.autoscaling, { min_capacity = 0 })
      }) : s
    ]
  }
```

**Pattern for pause rewrite** — the `kv pause` command must:
1. Parse site.hcl as HCL (not regex; use `hcl.Parse()` or `hclparse.Parser`)
2. Locate the `paused = false` line
3. Replace with `paused = true`
4. Preserve all comments and formatting
5. Write back to disk
6. Commit the one-line change

---

### `.gitignore` (modification: add backups directory)

**Add entry** (spec §4.8, D-14):
```
# Phase 16: kv backup/restore artifacts (contain personal data — never commit)
backups/
```

---

## Shared Patterns

### AWS Client Construction (all backup/restore/pause/destroy commands)

**Source:** `kv/internal/app/cmd/root.go`, lines 75-102 and extended patterns from `telephony.go`

**Pattern: Config methods on root.go's Config struct** — all AWS clients follow this shape:
```go
// In root.go's Config struct (lines 34-41):
type Config struct {
    Table       string
    UsageTable  string
    EndpointURL string
    Region      string
    Profile     string
    LogLevel    string
}

// DynamoClient pattern (lines 92-102):
func (c *Config) DynamoClient(ctx context.Context) (*dynamodb.Client, error) {
    cfg, err := c.loadAWS(ctx)
    if err != nil {
        return nil, err
    }
    return dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
        if c.EndpointURL != "" {
            o.BaseEndpoint = &c.EndpointURL
        }
    }), nil
}

// Extend with S3Client, ECSClient, CloudWatchLogsClient using the same pattern
func (c *Config) S3Client(ctx context.Context) (*s3.Client, error) {
    cfg, err := c.loadAWS(ctx)
    if err != nil {
        return nil, err
    }
    return s3.NewFromConfig(cfg), nil
}
```

**Applies to:** `backup.go`, `restore.go`, `pause.go`, `destroy.go` when reading/writing DynamoDB, S3, or polling ECS.

---

### Shell-out Isolation (pause/resume git and gh operations)

**Source:** `kv/internal/app/cmd/knowledge.go`, lines 13-25 and 86-91

**Pattern: repoRoot() helper + exec.CommandContext**:
```go
// Find repo root once at startup
func repoRoot() (string, error) {
    out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
    if err != nil {
        return "", fmt.Errorf("resolve repo root (git rev-parse --show-toplevel): %w", err)
    }
    return strings.TrimSpace(string(out)), nil
}

// Run subprocess with context + wired stdio
runCmd := exec.CommandContext(c.Context(), "git", "status", "--porcelain")
runCmd.Dir = root
runCmd.Stdout = c.OutOrStdout()
runCmd.Stderr = c.ErrOrStderr()
runCmd.Stdin = c.InOrStdin()
if err := runCmd.Run(); err != nil {
    return fmt.Errorf("git command failed: %w", err)
}
```

**For testability:** wrap git/gh/terraform behind interfaces (see pause.go pattern above).

**Applies to:** `pause.go` and `destroy.go` when calling `git`, `gh`, and `terraform`.

---

### Error Handling and DynamoDB Errors

**Source:** `kv/internal/app/cmd/killswitch.go`, lines 64-68

**Pattern: ConditionalCheckFailedException handling for idempotent operations**:
```go
// isConditionalCheckFailed reports whether err is (or wraps) a DynamoDB
// ConditionalCheckFailedException — the expected, non-error outcome of a
// redundant/idempotent on-or-off flip.
func isConditionalCheckFailed(err error) bool {
    var condErr *types.ConditionalCheckFailedException
    return errors.As(err, &condErr)
}

// Usage in EngageKillswitch (lines 76-110):
_, err = client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
    // ...
    ConditionExpression: aws.String("attribute_not_exists(pk) OR engaged = :false"),
})
if err != nil {
    if isConditionalCheckFailed(err) {
        return false, nil  // Idempotent: already in desired state
    }
    return false, fmt.Errorf("engage killswitch: %w", err)
}
```

**Applies to:** `restore.go` when PutItem is idempotent (re-running restore over partial state).

---

### Credential Resolution and SSM (VoIP.ms exports in backup)

**Source:** `kv/internal/app/cmd/voipms.go`, lines 183-220

**Pattern: env-first, SSM fallback with credential leak invariant**:
```go
// Never interpolate credentials into logs/errors (D-04 of voipms.go)
func resolveVoipmsCreds(ctx context.Context, ssmFactory func(context.Context) (ssmGetParameterAPI, error)) (voipmsCreds, error) {
    // 1. Try env vars first (VOIPMS_API_USERNAME/PASSWORD)
    if creds, err := voipmsCredsFromEnv(); err == nil {
        return creds, nil
    }
    // 2. Fall back to SSM
    api, err := ssmFactory(ctx)
    if err != nil {
        // NEVER log the cred paths or any secret; shortSSMErrorNote abstracts the error
        return voipmsCreds{}, fmt.Errorf(
            "could not resolve VoIP.ms API credentials: set VOIPMS_API_USERNAME/VOIPMS_API_PASSWORD, "+
                "or ensure your AWS profile can read /kmv/secrets/use1/voipms/* (%s)", shortSSMErrorNote(err))
    }
    return voipmsCredsFromSSM(ctx, api)
}

// Config method delegating to package-level function
func (c *Config) resolveVoipmsCreds(ctx context.Context) (voipmsCreds, error) {
    return resolveVoipmsCreds(ctx, func(ctx context.Context) (ssmGetParameterAPI, error) {
        return c.SSMClient(ctx)
    })
}
```

**Applies to:** `backup.go` when exporting `/external/voipms-dids.json` and other secrets.

---

### Test Conventions

**Source:** `kv/internal/app/cmd/studio_test.go`, `telephony_calls_test.go`, `voipms_test.go`

**Pattern: table tests + narrow interface fakes**:
```go
// From studio_test.go (lines 55-77): inject a fake credential resolver
func TestStudioCmd_BuildVoipmsInjections_DegradesGracefullyOnCredsError(t *testing.T) {
    router, lister := buildVoipmsInjections(context.Background(), func(ctx context.Context) (voipmsCreds, error) {
        return voipmsCreds{}, errors.New("no VoIP.ms creds available")
    })
    if router != nil {
        t.Error("router is non-nil, want nil when credential resolution fails")
    }
}

// From telephony_calls_test.go (lines 54-68): use constant log lines, table-test style
const (
    loguruPrefix = "2026-07-29 05:46:13.123 | INFO     | klanker_voice.telephony.controller:on_stasis_start:1128 - "
    identityLine = loguruPrefix + "on_stasis_start: channel=1785303830.6 caller=+15197101515 did=557010_klanker-pbx"
    dialedResolvedLine = loguruPrefix + "on_stasis_start: channel=1785303830.6 dialed_did=7254043234 ..."
)

func TestParseStasisLine_IdentityShape(t *testing.T) {
    rec, ok := parseStasisLine(identityLine)
    if !ok {
        t.Fatalf("parseStasisLine(identityLine) ok = false, want true")
    }
    if rec.Channel != "1785303830.6" {
        t.Errorf("Channel = %q, want 1785303830.6", rec.Channel)
    }
}
```

**For Phase 16 tests:**
- **Backup/restore round-trip:** mock DynamoDB via narrow interface, MinIO/fake S3 (spec §7.2)
- **Pause/resume HCL rewrite:** table tests with HCL snippets, idempotence, comment preservation
- **Preflight checks:** mock git/gh APIs to test dirty-tree, wrong-branch, stale-origin refusal
- **Dry-run:** skip write calls, report counts

---

## No Analog Found

Files with patterns established in this phase (no existing close match in the codebase):

| File | Pattern | Why |
|------|---------|-----|
| Backup zip encoding | `archive/zip` + `JSONL` + manifest format | New pattern; no prior zip encoding in kv |
| HCL site.hcl parsing and rewrite | `hclwrite` library or regex + comment preservation | HCL manipulation is new to kv; terraform modules only read HCL, never rewrite |
| GitHub Actions workflow dispatch and run polling | `gh workflow run` + `gh run view --json` parsing | New pattern; kv does not currently dispatch CI workflows |
| ECS service polling and update-service correction | `ecs describe-services` + `ecs update-service` with polled verification | New pattern; kv reads ECS metrics (telephony_stats.go) but never mutates task/service state |
| S3 object tree walk with key preservation | S3 walk pattern for ledger backup | S3 is new to kv's scope; telephony/studio read repo files, not S3 inventory |

---

## Metadata

**Analog search scope:** `/Users/khundeck/working/klanker-voice/kv/internal/app/cmd/`; related modules in `infra/terraform/`, `.github/workflows/`

**Files scanned:**
- Command files: `root.go`, `killswitch.go`, `knowledge.go`, `code.go`, `tier.go`, `voipms.go`, `studio.go`, `telephony.go`, `telephony_calls.go`
- Test files: `studio_test.go`, `telephony_calls_test.go`, `voipms_test.go`
- Infrastructure: `infra/terraform/live/site/site.hcl`, `.github/workflows/terragrunt-apply.yml`

**Pattern extraction date:** 2026-08-19
