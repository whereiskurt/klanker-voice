package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// TerraformLiveSiteRelPath is the repo-relative path every Phase-16
// terraform unit directory is resolved beneath.
const TerraformLiveSiteRelPath = "infra/terraform/live/site"

// Terraform live-site unit directories (each relative to
// TerraformLiveSiteRelPath) that ResolveLiveTargets and its narrower
// entry points read.
const (
	tfUnitDynamoDB   = "region/us-east-1/dynamodb"
	tfUnitLedger     = "region/us-east-1/ledger"
	tfUnitECSService = "region/us-east-1/ecs-service"
	tfUnitECSCluster = "region/us-east-1/ecs-cluster"
	tfUnitNetwork    = "region/us-east-1/network"
)

// tableNameLogicalPrefix is stripped from a physical DynamoDB table name to
// derive its stable logical key (kmv-auth-electro -> auth-electro),
// matching the D-04 artifact-layout names auth-electro/auth-authjs/
// voice-usage.
const tableNameLogicalPrefix = "kmv-"

// TerraformOutputReader is the narrow seam every Phase-16 command resolves
// live infrastructure names through (D-10, D-30). unitDir is a path
// relative to TerraformLiveSiteRelPath; OutputJSON returns that unit's raw
// `terragrunt output -json` stdout. Tests inject a fake backed by canned
// per-unit JSON so no test invokes terragrunt, terraform, or AWS.
type TerraformOutputReader interface {
	OutputJSON(ctx context.Context, unitDir string) ([]byte, error)
}

// terragruntOutputReader is the production TerraformOutputReader.
type terragruntOutputReader struct {
	repoRoot string
}

// NewTerragruntOutputReader builds a TerraformOutputReader rooted at
// repoRoot (see repoRoot() in knowledge.go for the git-rev-parse pattern
// callers typically use to resolve it).
func NewTerragruntOutputReader(repoRoot string) TerraformOutputReader {
	return &terragruntOutputReader{repoRoot: repoRoot}
}

// OutputJSON runs `terragrunt output -json` with Dir set to
// repoRoot/TerraformLiveSiteRelPath/unitDir, capturing stdout only. On
// failure the returned error names the unit directory and includes
// combined stderr, but never the process environment or any credential
// material (T-16-01-01) — terragrunt inherits the operator's ambient AWS
// credentials and SOPS-decrypted values, none of which this error path may
// echo.
func (r *terragruntOutputReader) OutputJSON(ctx context.Context, unitDir string) ([]byte, error) {
	dir := filepath.Join(r.repoRoot, TerraformLiveSiteRelPath, unitDir)
	cmd := exec.CommandContext(ctx, "terragrunt", "output", "-json")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("terragrunt output -json in unit %s: %w: %s", unitDir, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// LiveTargets is the set of restore/destroy destination names resolvable
// only from current terraform outputs (D-10).
//
// These are the ONLY legal restore and destroy destinations. A caller must
// never substitute a value read out of a BackupManifest — its TableName and
// BucketName fields exist purely to audit where a backup came from, and a
// post-destroy/recreate resource (most notably the ledger bucket's
// random_id suffix) will not match what an old manifest recorded.
type LiveTargets struct {
	TableNames      map[string]string
	LedgerBucket    string
	ClusterName     string
	ServiceNames    []string
	TargetGroupARNs map[string]string
	NATEIP          string
}

// tfOutputEnvelope decodes one `terragrunt output -json` top-level entry.
// Value is left raw so each resolver below decodes it into its own shape.
type tfOutputEnvelope struct {
	Value json.RawMessage `json:"value"`
}

func decodeOutputs(unitDir string, b []byte) (map[string]tfOutputEnvelope, error) {
	var out map[string]tfOutputEnvelope
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("terraform unit %s: decode output -json: %w", unitDir, err)
	}
	return out, nil
}

func outputValue(outputs map[string]tfOutputEnvelope, unitDir, key string, dst any) error {
	env, ok := outputs[key]
	if !ok {
		return fmt.Errorf("terraform unit %s: missing output %q", unitDir, key)
	}
	if err := json.Unmarshal(env.Value, dst); err != nil {
		return fmt.Errorf("terraform unit %s: decode output %q: %w", unitDir, key, err)
	}
	return nil
}

// readUnitOutputs reads and JSON-envelope-decodes one unit's
// `terragrunt output -json`, wrapping a reader error with the unit
// directory so the failure is always attributable.
func readUnitOutputs(ctx context.Context, r TerraformOutputReader, unitDir string) (map[string]tfOutputEnvelope, error) {
	raw, err := r.OutputJSON(ctx, unitDir)
	if err != nil {
		return nil, fmt.Errorf("terraform unit %s: %w", unitDir, err)
	}
	return decodeOutputs(unitDir, raw)
}

// ResolveTableNames resolves the three DynamoDB tables from the dynamodb
// unit's `tables` output, keyed by their stable logical name
// (auth-electro, auth-authjs, voice-usage) rather than the map key
// terraform itself uses — so the mapping survives a table-key rename.
func ResolveTableNames(ctx context.Context, r TerraformOutputReader) (map[string]string, error) {
	outputs, err := readUnitOutputs(ctx, r, tfUnitDynamoDB)
	if err != nil {
		return nil, err
	}
	var tables map[string]struct {
		TableName string `json:"table_name"`
	}
	if err := outputValue(outputs, tfUnitDynamoDB, "tables", &tables); err != nil {
		return nil, err
	}
	names := make(map[string]string, len(tables))
	for _, t := range tables {
		logical := strings.TrimPrefix(t.TableName, tableNameLogicalPrefix)
		names[logical] = t.TableName
	}
	return names, nil
}

// ResolveLedgerBucket resolves the ledger S3 bucket name from the ledger
// unit's `bucket_name` output alone — it reads no other unit, so a caller
// that only needs the bucket never pays for (or risks failing on) the
// other four units' outputs.
func ResolveLedgerBucket(ctx context.Context, r TerraformOutputReader) (string, error) {
	outputs, err := readUnitOutputs(ctx, r, tfUnitLedger)
	if err != nil {
		return "", err
	}
	var bucket string
	if err := outputValue(outputs, tfUnitLedger, "bucket_name", &bucket); err != nil {
		return "", err
	}
	return bucket, nil
}

// ResolveECSPosture resolves the single ECS cluster name, the three
// service names (sorted for deterministic output), and the target-group
// ARNs keyed by target-group key, from the ecs-cluster and ecs-service
// units.
func ResolveECSPosture(ctx context.Context, r TerraformOutputReader) (cluster string, services []string, targetGroups map[string]string, err error) {
	clusterOutputs, err := readUnitOutputs(ctx, r, tfUnitECSCluster)
	if err != nil {
		return "", nil, nil, err
	}
	var clusters map[string]struct {
		ClusterName string `json:"cluster_name"`
	}
	if err := outputValue(clusterOutputs, tfUnitECSCluster, "clusters", &clusters); err != nil {
		return "", nil, nil, err
	}
	if len(clusters) != 1 {
		return "", nil, nil, fmt.Errorf("terraform unit %s: expected exactly one ECS cluster, found %d", tfUnitECSCluster, len(clusters))
	}
	for _, c := range clusters {
		cluster = c.ClusterName
	}

	serviceOutputs, err := readUnitOutputs(ctx, r, tfUnitECSService)
	if err != nil {
		return "", nil, nil, err
	}
	var svcMap map[string]struct {
		ServiceName string `json:"service_name"`
	}
	if err := outputValue(serviceOutputs, tfUnitECSService, "services", &svcMap); err != nil {
		return "", nil, nil, err
	}
	services = make([]string, 0, len(svcMap))
	for _, s := range svcMap {
		services = append(services, s.ServiceName)
	}
	sort.Strings(services)

	var tgMap map[string]struct {
		ARN string `json:"arn"`
	}
	if err := outputValue(serviceOutputs, tfUnitECSService, "target_groups", &tgMap); err != nil {
		return "", nil, nil, err
	}
	targetGroups = make(map[string]string, len(tgMap))
	for key, tg := range tgMap {
		targetGroups[key] = tg.ARN
	}

	return cluster, services, targetGroups, nil
}

// ResolveLiveTargets composes ResolveTableNames, ResolveLedgerBucket,
// ResolveECSPosture, and the network unit's `nat_eip_public_ip` output into
// the full LiveTargets set every later Phase-16 command needs.
func ResolveLiveTargets(ctx context.Context, r TerraformOutputReader) (LiveTargets, error) {
	tables, err := ResolveTableNames(ctx, r)
	if err != nil {
		return LiveTargets{}, err
	}
	bucket, err := ResolveLedgerBucket(ctx, r)
	if err != nil {
		return LiveTargets{}, err
	}
	cluster, services, targetGroups, err := ResolveECSPosture(ctx, r)
	if err != nil {
		return LiveTargets{}, err
	}

	netOutputs, err := readUnitOutputs(ctx, r, tfUnitNetwork)
	if err != nil {
		return LiveTargets{}, err
	}
	var natEIP string
	if err := outputValue(netOutputs, tfUnitNetwork, "nat_eip_public_ip", &natEIP); err != nil {
		return LiveTargets{}, err
	}

	return LiveTargets{
		TableNames:      tables,
		LedgerBucket:    bucket,
		ClusterName:     cluster,
		ServiceNames:    services,
		TargetGroupARNs: targetGroups,
		NATEIP:          natEIP,
	}, nil
}
