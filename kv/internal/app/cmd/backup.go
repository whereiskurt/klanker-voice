// Package cmd — kv backup: a verified, self-contained artifact of everything
// that exists only in AWS (D-01/D-02). Built for the destroy case from day
// one: three DynamoDB tables scanned to JSONL, the full S3 transcript ledger
// as a key-preserving object tree, a small external inventory (VoIP.ms DIDs,
// NAT EIP, non-secret SSM parameters), a manifest with a SHA-256 per file,
// and a verification pass that re-opens the finished zip and re-checks it
// (D-09). See docs/superpowers/specs/2026-08-12-pause-backup-teardown-design.md
// §4 for the full design.
package cmd

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/spf13/cobra"
)

// --------------------------------------------------------------------------
// AWS seams (D-30): every AWS call this file makes goes through one of these
// narrow interfaces, so every test in backup_test.go runs offline against a
// hand-rolled fake — no DynamoDB, S3, or network reachable from `go test`.

// dynamoScanAPI is the narrow subset of *dynamodb.Client ScanTableToJSONL
// needs.
type dynamoScanAPI interface {
	Scan(ctx context.Context, params *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
}

// s3ListGetAPI is the narrow subset of *s3.Client WalkLedgerObjects needs.
type s3ListGetAPI interface {
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// --------------------------------------------------------------------------
// DynamoDB -> JSONL (D-06: a plain Scan, never ExportTableToPointInTime —
// native export needs PITR and writes its output to an S3 bucket INSIDE the
// account being destroyed, which is backwards for a destroy-day artifact).
//
// Encoding choice: each JSONL line is one item, encoded as a JSON object
// keyed by attribute name whose value is a "wire" struct
// (avWire, below) carrying one non-nil field per DynamoDB AttributeValue
// variant (S/N/B/BOOL/NULL/SS/NS/BS/M/L). This is deliberately NOT
// attributevalue.UnmarshalMap into a generic map[string]any: going through
// Go's native `any` representation collapses SS and NS to the same
// []string shape, so an item carrying both a string set and a number set
// cannot be told apart on the way back in — exactly the round-trip fidelity
// 16-03's restore depends on. avWire keeps every AttributeValue variant
// distinguishable through the JSON boundary, verified by round-tripping the
// full type set (binary, sets, null, nested maps and lists) in
// backup_test.go.

// avWire is the JSON wire shape for one types.AttributeValue: exactly one
// field is non-nil, identifying which DynamoDB attribute type the value is.
type avWire struct {
	S    *string           `json:"S"`
	N    *string           `json:"N"`
	B    []byte            `json:"B"`
	SS   []string          `json:"SS"`
	NS   []string          `json:"NS"`
	BS   [][]byte          `json:"BS"`
	M    map[string]avWire `json:"M"`
	L    []avWire          `json:"L"`
	NULL *bool             `json:"NULL"`
	BOOL *bool             `json:"BOOL"`
}

// toWire converts one types.AttributeValue into its avWire representation.
func toWire(av types.AttributeValue) (avWire, error) {
	switch v := av.(type) {
	case *types.AttributeValueMemberS:
		s := v.Value
		return avWire{S: &s}, nil
	case *types.AttributeValueMemberN:
		n := v.Value
		return avWire{N: &n}, nil
	case *types.AttributeValueMemberB:
		b := v.Value
		if b == nil {
			b = []byte{}
		}
		return avWire{B: b}, nil
	case *types.AttributeValueMemberBOOL:
		b := v.Value
		return avWire{BOOL: &b}, nil
	case *types.AttributeValueMemberNULL:
		b := v.Value
		return avWire{NULL: &b}, nil
	case *types.AttributeValueMemberSS:
		ss := v.Value
		if ss == nil {
			ss = []string{}
		}
		return avWire{SS: ss}, nil
	case *types.AttributeValueMemberNS:
		ns := v.Value
		if ns == nil {
			ns = []string{}
		}
		return avWire{NS: ns}, nil
	case *types.AttributeValueMemberBS:
		bs := make([][]byte, len(v.Value))
		copy(bs, v.Value)
		if bs == nil {
			bs = [][]byte{}
		}
		return avWire{BS: bs}, nil
	case *types.AttributeValueMemberM:
		m := make(map[string]avWire, len(v.Value))
		for k, val := range v.Value {
			w, err := toWire(val)
			if err != nil {
				return avWire{}, err
			}
			m[k] = w
		}
		return avWire{M: m}, nil
	case *types.AttributeValueMemberL:
		l := make([]avWire, len(v.Value))
		for i, val := range v.Value {
			w, err := toWire(val)
			if err != nil {
				return avWire{}, err
			}
			l[i] = w
		}
		return avWire{L: l}, nil
	default:
		return avWire{}, fmt.Errorf("unsupported dynamodb attribute value type %T", av)
	}
}

// fromWire converts an avWire back into a types.AttributeValue, the inverse
// of toWire.
func fromWire(w avWire) (types.AttributeValue, error) {
	switch {
	case w.S != nil:
		return &types.AttributeValueMemberS{Value: *w.S}, nil
	case w.N != nil:
		return &types.AttributeValueMemberN{Value: *w.N}, nil
	case w.B != nil:
		return &types.AttributeValueMemberB{Value: w.B}, nil
	case w.BOOL != nil:
		return &types.AttributeValueMemberBOOL{Value: *w.BOOL}, nil
	case w.NULL != nil:
		return &types.AttributeValueMemberNULL{Value: *w.NULL}, nil
	case w.SS != nil:
		return &types.AttributeValueMemberSS{Value: w.SS}, nil
	case w.NS != nil:
		return &types.AttributeValueMemberNS{Value: w.NS}, nil
	case w.BS != nil:
		return &types.AttributeValueMemberBS{Value: w.BS}, nil
	case w.M != nil:
		m := make(map[string]types.AttributeValue, len(w.M))
		for k, val := range w.M {
			av, err := fromWire(val)
			if err != nil {
				return nil, err
			}
			m[k] = av
		}
		return &types.AttributeValueMemberM{Value: m}, nil
	case w.L != nil:
		l := make([]types.AttributeValue, len(w.L))
		for i, val := range w.L {
			av, err := fromWire(val)
			if err != nil {
				return nil, err
			}
			l[i] = av
		}
		return &types.AttributeValueMemberL{Value: l}, nil
	default:
		return nil, fmt.Errorf("empty dynamodb attribute value wire object")
	}
}

// marshalItemJSONL encodes one DynamoDB item (a map of attribute name to
// AttributeValue) as a single compact JSON line, with no trailing newline.
func marshalItemJSONL(item map[string]types.AttributeValue) ([]byte, error) {
	wire := make(map[string]avWire, len(item))
	for k, v := range item {
		w, err := toWire(v)
		if err != nil {
			return nil, fmt.Errorf("attribute %q: %w", k, err)
		}
		wire[k] = w
	}
	b, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("marshal item: %w", err)
	}
	return b, nil
}

// unmarshalItemJSONL decodes one JSONL line back into a DynamoDB item — the
// inverse of marshalItemJSONL, used by 16-03's restore.
func unmarshalItemJSONL(b []byte) (map[string]types.AttributeValue, error) {
	var wire map[string]avWire
	if err := json.Unmarshal(b, &wire); err != nil {
		return nil, fmt.Errorf("unmarshal item: %w", err)
	}
	item := make(map[string]types.AttributeValue, len(wire))
	for k, w := range wire {
		av, err := fromWire(w)
		if err != nil {
			return nil, fmt.Errorf("attribute %q: %w", k, err)
		}
		item[k] = av
	}
	return item, nil
}

// ScanTableToJSONL paginates a full table Scan (ExclusiveStartKey loop,
// matching telephony.go's ListPhoneMappings pattern) and writes one JSONL
// line per item to w, returning the total row count. On any Scan error the
// returned error names table and rowCount is zero — a caller must never
// treat a partial write as a complete backup of the table.
func ScanTableToJSONL(ctx context.Context, api dynamoScanAPI, table string, w io.Writer) (rowCount int64, err error) {
	var lastKey map[string]types.AttributeValue
	for {
		resp, err := api.Scan(ctx, &dynamodb.ScanInput{
			TableName:         aws.String(table),
			ExclusiveStartKey: lastKey,
		})
		if err != nil {
			return 0, fmt.Errorf("scan table %s: %w", table, err)
		}
		for _, item := range resp.Items {
			line, err := marshalItemJSONL(item)
			if err != nil {
				return 0, fmt.Errorf("scan table %s: %w", table, err)
			}
			if _, err := w.Write(line); err != nil {
				return 0, fmt.Errorf("scan table %s: write jsonl: %w", table, err)
			}
			if _, err := w.Write([]byte("\n")); err != nil {
				return 0, fmt.Errorf("scan table %s: write jsonl: %w", table, err)
			}
			rowCount++
		}
		if resp.LastEvaluatedKey == nil {
			break
		}
		lastKey = resp.LastEvaluatedKey
	}
	return rowCount, nil
}

// --------------------------------------------------------------------------
// S3 ledger -> key-preserving object tree (D-07: always the WHOLE bucket, no
// prefix filter, no max-object cap — there is no partial-backup flag).

// LedgerObject is one object's identity in the ledger backup.
type LedgerObject struct {
	Key    string
	Size   int64
	SHA256 string
}

// WalkLedgerObjects paginates ListObjectsV2 over the whole bucket (no prefix
// filter) and, for every object, opens it via GetObject and calls visit with
// its key, body, and size — preserving every key verbatim, including
// slashes, so the object tree is reconstructable. Returns the total object
// count and byte total. Any list or get error aborts and names the bucket or
// key.
func WalkLedgerObjects(ctx context.Context, api s3ListGetAPI, bucket string, visit func(key string, body io.Reader, size int64) error) (objects int64, bytesTotal int64, err error) {
	var token *string
	for {
		resp, err := api.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			ContinuationToken: token,
		})
		if err != nil {
			return 0, 0, fmt.Errorf("list ledger bucket %s: %w", bucket, err)
		}
		for _, obj := range resp.Contents {
			key := aws.ToString(obj.Key)
			getResp, err := api.GetObject(ctx, &s3.GetObjectInput{
				Bucket: aws.String(bucket),
				Key:    aws.String(key),
			})
			if err != nil {
				return 0, 0, fmt.Errorf("get ledger object %s: %w", key, err)
			}
			size := aws.ToInt64(obj.Size)
			verr := visit(key, getResp.Body, size)
			closeErr := getResp.Body.Close()
			if verr != nil {
				return 0, 0, fmt.Errorf("visit ledger object %s: %w", key, verr)
			}
			if closeErr != nil {
				return 0, 0, fmt.Errorf("close ledger object %s: %w", key, closeErr)
			}
			objects++
			bytesTotal += size
		}
		if resp.IsTruncated == nil || !*resp.IsTruncated {
			break
		}
		token = resp.NextContinuationToken
	}
	return objects, bytesTotal, nil
}

// --------------------------------------------------------------------------
// external/ssm-params.json (D-14: secret values stay in SOPS and SSM and
// are NEVER captured by this file — GetParameter is only ever called for
// String/StringList types, always with decryption disabled; SecureString
// parameters are recorded as metadata-only with valueOmitted=true).

// ssmInventoryPathPrefix is the SSM path this file's inventory is scoped to
// — every Phase-16-relevant parameter lives under /kmv/.
const ssmInventoryPathPrefix = "/kmv/"

// ssmInventoryAPI is the narrow subset of *ssm.Client ExportSSMInventory
// needs.
type ssmInventoryAPI interface {
	DescribeParameters(ctx context.Context, params *ssm.DescribeParametersInput, optFns ...func(*ssm.Options)) (*ssm.DescribeParametersOutput, error)
	GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// ssmParamRecord is one SSM parameter's inventory record. Value is present
// only for String/StringList parameters; SecureString parameters set
// ValueOmitted=true and carry no Value, so the artifact self-documents why
// the value is absent rather than silently omitting the field.
type ssmParamRecord struct {
	Name             string    `json:"name"`
	Type             string    `json:"type"`
	LastModifiedDate time.Time `json:"lastModifiedDate"`
	Version          int64     `json:"version"`
	Value            string    `json:"value,omitempty"`
	ValueOmitted     bool      `json:"valueOmitted"`
}

type ssmInventory struct {
	Parameters []ssmParamRecord `json:"parameters"`
}

// ExportSSMInventory paginates DescribeParameters under path (recursive) and
// builds external/ssm-params.json: name/type/lastModifiedDate/version for
// every parameter, plus the decrypted value ONLY for String/StringList
// parameters (GetParameter is called with WithDecryption:false always —
// SecureString values are never read, matching apps/voice's own principle
// that secrets stay in SOPS/SSM, not backup artifacts).
func ExportSSMInventory(ctx context.Context, api ssmInventoryAPI, path string) ([]byte, error) {
	var records []ssmParamRecord
	var nextToken *string
	for {
		resp, err := api.DescribeParameters(ctx, &ssm.DescribeParametersInput{
			ParameterFilters: []ssmtypes.ParameterStringFilter{
				{Key: aws.String("Path"), Option: aws.String("Recursive"), Values: []string{path}},
			},
			NextToken: nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("describe ssm parameters under %s: %w", path, err)
		}
		for _, p := range resp.Parameters {
			rec := ssmParamRecord{
				Name:             aws.ToString(p.Name),
				Type:             string(p.Type),
				LastModifiedDate: aws.ToTime(p.LastModifiedDate),
				Version:          p.Version,
			}
			if p.Type == ssmtypes.ParameterTypeSecureString {
				rec.ValueOmitted = true
			} else {
				getResp, err := api.GetParameter(ctx, &ssm.GetParameterInput{
					Name:           p.Name,
					WithDecryption: aws.Bool(false),
				})
				if err != nil {
					return nil, fmt.Errorf("get ssm parameter %s: %w", aws.ToString(p.Name), err)
				}
				if getResp.Parameter != nil {
					rec.Value = aws.ToString(getResp.Parameter.Value)
				}
			}
			records = append(records, rec)
		}
		if resp.NextToken == nil {
			break
		}
		nextToken = resp.NextToken
	}
	inv := ssmInventory{Parameters: records}
	b, err := json.MarshalIndent(inv, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal ssm inventory: %w", err)
	}
	return append(b, '\n'), nil
}

// --------------------------------------------------------------------------
// external/voipms-dids.json (advisory — degrades to an error field rather
// than failing the whole backup, since the DID inventory is a convenience,
// not one of the two data sources (DynamoDB rows, ledger) the backup exists
// to protect).

// DIDLister mirrors ListInboundDIDs' signature so a real *voipmsClient call
// and a test fake are interchangeable.
type DIDLister func(ctx context.Context) ([]InboundDIDRecord, error)

type didInventory struct {
	DIDs  []InboundDIDRecord `json:"dids"`
	Error string             `json:"error,omitempty"`
}

// ExportDIDInventory builds external/voipms-dids.json via list. Mirrors
// studio.go's buildVoipmsInjections graceful-degradation shape: a nil lister
// (credential resolution failed before this was even called) or a lister
// error both produce a valid JSON document carrying an `error` string and an
// empty `dids` array, rather than aborting the whole backup. The error text
// is whatever ListInboundDIDs/resolveVoipmsCreds already produced — both are
// leak-free by their own documented invariant (never log params or URLs) —
// this function adds no request context of its own.
func ExportDIDInventory(ctx context.Context, list DIDLister) ([]byte, error) {
	inv := didInventory{DIDs: []InboundDIDRecord{}}
	if list == nil {
		inv.Error = "voip.ms DID inventory unavailable: no credentials resolved"
	} else if records, err := list(ctx); err != nil {
		inv.Error = fmt.Sprintf("voip.ms DID inventory unavailable: %s", err)
	} else {
		inv.DIDs = records
	}
	b, err := json.MarshalIndent(inv, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal did inventory: %w", err)
	}
	return append(b, '\n'), nil
}

// --------------------------------------------------------------------------
// external/nat-eip.txt

// ExportNATEIP renders the live-resolved NAT EIP (targets.NATEIP, from
// ResolveLiveTargets — D-05/D-10) with a trailing newline.
func ExportNATEIP(targets LiveTargets) []byte {
	return []byte(targets.NATEIP + "\n")
}

// --------------------------------------------------------------------------
// The zip assembly (D-04's layout) and manifest.

// backupSizeWarnBytes is the discretionary size threshold (D-07) above
// which WriteBackupArchive appends a warning to BackupResult.Warnings — the
// always-include-the-ledger design assumes a reasonably small ledger
// (operator directive 2026-08-19); this is the visible tripwire if that
// assumption stops holding.
const backupSizeWarnBytes int64 = 2 * 1024 * 1024 * 1024 // 2 GiB

// BackupDeps carries every AWS/data seam WriteBackupArchive needs, all
// narrow interfaces so tests inject fakes (D-30). Targets is the
// live-resolved destination set (16-01's ResolveLiveTargets) — the ONLY
// legal source of the table/bucket names recorded in the manifest.
type BackupDeps struct {
	Dynamo       dynamoScanAPI
	S3           s3ListGetAPI
	SSM          ssmInventoryAPI
	DIDs         DIDLister
	Targets      LiveTargets
	AWSAccountID string
	Region       string
	GitSHA       string
	Now          func() time.Time
}

// BackupOptions controls where WriteBackupArchive writes and whether the
// caller wants verification run afterward (the flag itself is read and
// acted on by the `kv backup` command in Task 3; WriteBackupArchive itself
// never verifies its own output).
type BackupOptions struct {
	OutDir string
	Verify bool
}

// BackupResult summarizes one WriteBackupArchive call: where the artifact
// landed, its size and how long it took to write, the manifest that
// describes it, and any warnings (e.g. backupSizeWarnBytes exceeded).
type BackupResult struct {
	Path     string
	Bytes    int64
	Elapsed  time.Duration
	Manifest BackupManifest
	Warnings []string
}

// countingWriter counts bytes written through it — paired with a sha256.Hash
// via io.MultiWriter inside writeZipEntry so an entry's digest and length
// are computed in one streaming pass, with no second read of the entry.
type countingWriter struct{ n int64 }

func (c *countingWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}

// writeZipEntry creates a zip entry named name in zw, invokes write with a
// writer that simultaneously feeds the zip entry, a running SHA-256 hash,
// and a byte counter, and returns the resulting BackupFileRef.
func writeZipEntry(zw *zip.Writer, name string, write func(io.Writer) error) (BackupFileRef, error) {
	// zip.Store (uncompressed): the backup already holds mostly-incompressible
	// content (JSON, audio-adjacent transcript payloads) and leaving entries
	// uncompressed keeps VerifyBackupArchive's re-read a plain byte-for-byte
	// stream rather than a decompression pass — one less thing that can fail
	// independently of the actual data.
	entry, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
	if err != nil {
		return BackupFileRef{}, fmt.Errorf("create zip entry %s: %w", name, err)
	}
	h := sha256.New()
	cw := &countingWriter{}
	mw := io.MultiWriter(entry, h, cw)
	if err := write(mw); err != nil {
		return BackupFileRef{}, fmt.Errorf("write zip entry %s: %w", name, err)
	}
	return BackupFileRef{
		Path:   name,
		Bytes:  cw.n,
		SHA256: hex.EncodeToString(h.Sum(nil)),
	}, nil
}

// backupTimestampLayout is the filename-safe UTC ISO-8601 stamp used in
// kmv-backup-<ISO8601>.zip (D-04) — colons are illegal in a Windows/most
// filesystem path component, so the stamp is compact ("20060102T150405Z")
// rather than RFC3339's colon-separated time-of-day.
const backupTimestampLayout = "20060102T150405Z"

// WriteBackupArchive assembles one self-contained backup zip (D-04's
// layout: dynamodb/<logical>.jsonl x3, ledger/<key> per object,
// external/{voipms-dids.json,nat-eip.txt,ssm-params.json}, manifest.json
// last) into opts.OutDir, naming it kmv-backup-<ISO8601>.zip from
// deps.Now(). Every non-manifest entry's SHA-256 and byte count are computed
// while streaming (writeZipEntry) and recorded in the manifest, which is
// written last so every earlier entry's digest is already known.
func WriteBackupArchive(ctx context.Context, deps BackupDeps, opts BackupOptions) (BackupResult, error) {
	start := time.Now()
	now := deps.Now
	if now == nil {
		now = time.Now
	}

	if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
		return BackupResult{}, fmt.Errorf("create backup output dir %s: %w", opts.OutDir, err)
	}
	createdAt := now().UTC()
	filename := fmt.Sprintf("kmv-backup-%s.zip", createdAt.Format(backupTimestampLayout))
	outPath := filepath.Join(opts.OutDir, filename)

	f, err := os.Create(outPath)
	if err != nil {
		return BackupResult{}, fmt.Errorf("create backup archive %s: %w", outPath, err)
	}
	zw := zip.NewWriter(f)

	manifest := BackupManifest{
		Version:      BackupManifestVersion,
		CreatedAt:    createdAt,
		GitSHA:       deps.GitSHA,
		KvVersion:    KvVersion(),
		AWSAccountID: deps.AWSAccountID,
		Region:       deps.Region,
	}

	// dynamodb/<logical>.jsonl, one per table, sorted for deterministic
	// ordering.
	logicals := make([]string, 0, len(deps.Targets.TableNames))
	for logical := range deps.Targets.TableNames {
		logicals = append(logicals, logical)
	}
	sort.Strings(logicals)

	for _, logical := range logicals {
		table := deps.Targets.TableNames[logical]
		entryPath := fmt.Sprintf("dynamodb/%s.jsonl", logical)
		var rowCount int64
		ref, err := writeZipEntry(zw, entryPath, func(w io.Writer) error {
			n, err := ScanTableToJSONL(ctx, deps.Dynamo, table, w)
			rowCount = n
			return err
		})
		if err != nil {
			f.Close()
			return BackupResult{}, err
		}
		manifest.Files = append(manifest.Files, ref)
		manifest.Tables = append(manifest.Tables, BackupTableRef{
			Logical:   logical,
			TableName: table,
			Path:      entryPath,
			RowCount:  rowCount,
		})
	}

	// ledger/<key>, streamed via WalkLedgerObjects.
	objects, ledgerBytes, err := WalkLedgerObjects(ctx, deps.S3, deps.Targets.LedgerBucket, func(key string, body io.Reader, _ int64) error {
		entryPath := "ledger/" + key
		ref, err := writeZipEntry(zw, entryPath, func(w io.Writer) error {
			_, err := io.Copy(w, body)
			return err
		})
		if err != nil {
			return err
		}
		manifest.Files = append(manifest.Files, ref)
		return nil
	})
	if err != nil {
		f.Close()
		return BackupResult{}, err
	}
	manifest.Ledger = BackupLedgerRef{
		BucketName:  deps.Targets.LedgerBucket,
		ObjectCount: objects,
		ByteTotal:   ledgerBytes,
	}

	// external/{voipms-dids.json,nat-eip.txt,ssm-params.json}
	didJSON, err := ExportDIDInventory(ctx, deps.DIDs)
	if err != nil {
		f.Close()
		return BackupResult{}, err
	}
	didRef, err := writeZipEntry(zw, "external/voipms-dids.json", func(w io.Writer) error {
		_, err := w.Write(didJSON)
		return err
	})
	if err != nil {
		f.Close()
		return BackupResult{}, err
	}
	manifest.External = append(manifest.External, didRef)

	natRef, err := writeZipEntry(zw, "external/nat-eip.txt", func(w io.Writer) error {
		_, err := w.Write(ExportNATEIP(deps.Targets))
		return err
	})
	if err != nil {
		f.Close()
		return BackupResult{}, err
	}
	manifest.External = append(manifest.External, natRef)

	ssmJSON, err := ExportSSMInventory(ctx, deps.SSM, ssmInventoryPathPrefix)
	if err != nil {
		f.Close()
		return BackupResult{}, err
	}
	ssmRef, err := writeZipEntry(zw, "external/ssm-params.json", func(w io.Writer) error {
		_, err := w.Write(ssmJSON)
		return err
	})
	if err != nil {
		f.Close()
		return BackupResult{}, err
	}
	manifest.External = append(manifest.External, ssmRef)

	manifest.Files = append(manifest.Files, manifest.External...)

	// manifest.json last — every earlier entry's digest is already known.
	manifestBytes, err := manifest.Marshal()
	if err != nil {
		f.Close()
		return BackupResult{}, err
	}
	mEntry, err := zw.CreateHeader(&zip.FileHeader{Name: BackupManifestPath, Method: zip.Store})
	if err != nil {
		f.Close()
		return BackupResult{}, fmt.Errorf("create zip entry %s: %w", BackupManifestPath, err)
	}
	if _, err := mEntry.Write(manifestBytes); err != nil {
		f.Close()
		return BackupResult{}, fmt.Errorf("write manifest entry: %w", err)
	}

	if err := zw.Close(); err != nil {
		f.Close()
		return BackupResult{}, fmt.Errorf("close backup archive: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return BackupResult{}, fmt.Errorf("stat backup archive %s: %w", outPath, err)
	}
	size := info.Size()
	if err := f.Close(); err != nil {
		return BackupResult{}, fmt.Errorf("close backup archive file %s: %w", outPath, err)
	}

	var warnings []string
	if size > backupSizeWarnBytes {
		warnings = append(warnings, fmt.Sprintf(
			"backup artifact is %s, above the %s warning threshold — the always-include-the-ledger design (D-07) assumes a reasonably small ledger",
			humanBytes(size), humanBytes(backupSizeWarnBytes)))
	}

	return BackupResult{
		Path:     outPath,
		Bytes:    size,
		Elapsed:  time.Since(start),
		Manifest: manifest,
		Warnings: warnings,
	}, nil
}

// humanBytes renders n bytes as a short human-readable size (e.g. "512 B",
// "3.4 KiB", "2.1 GiB").
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// --------------------------------------------------------------------------
// Verification (D-09): "Backup succeeded" must mean the artifact was
// re-opened and every digest, byte count, and row count re-checked against
// manifest.json. This is also 16-10's abort gate for `kv destroy
// --with-backup`, so it must never return a nil error on a partial read: a
// manifest entry with no matching zip entry, an unexpected extra zip entry
// not listed in the manifest, a digest mismatch, a byte-count mismatch, or a
// table row-count mismatch are all hard failures.

// countJSONLLinesReader counts newline-terminated lines in r without
// buffering the whole entry into memory — a large table's JSONL entry is
// streamed, not slurped.
func countJSONLLinesReader(r io.Reader) (int64, error) {
	var count int64
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		for i := 0; i < n; i++ {
			if buf[i] == '\n' {
				count++
			}
		}
		if err == io.EOF {
			return count, nil
		}
		if err != nil {
			return 0, err
		}
	}
}

// VerifyBackupArchive re-opens the zip at path, parses manifest.json, and
// checks every BackupFileRef's SHA-256 and byte count against a fresh read
// of the corresponding zip entry, and every BackupTableRef's RowCount
// against the entry's newline count. A manifest reference with no matching
// zip entry, or a zip entry not listed in the manifest, is also an error —
// a silently-added or silently-missing file cannot ride along undetected.
func VerifyBackupArchive(path string) (BackupManifest, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return BackupManifest{}, fmt.Errorf("open backup archive %s: %w", path, err)
	}
	defer zr.Close()

	entries := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		entries[f.Name] = f
	}

	manifestFile, ok := entries[BackupManifestPath]
	if !ok {
		return BackupManifest{}, fmt.Errorf("backup archive %s: missing %s", path, BackupManifestPath)
	}
	rc, err := manifestFile.Open()
	if err != nil {
		return BackupManifest{}, fmt.Errorf("backup archive %s: open %s: %w", path, BackupManifestPath, err)
	}
	manifestBytes, err := io.ReadAll(rc)
	closeErr := rc.Close()
	if err != nil {
		return BackupManifest{}, fmt.Errorf("backup archive %s: read %s: %w", path, BackupManifestPath, err)
	}
	if closeErr != nil {
		return BackupManifest{}, fmt.Errorf("backup archive %s: close %s: %w", path, BackupManifestPath, closeErr)
	}
	manifest, err := ParseBackupManifest(manifestBytes)
	if err != nil {
		return BackupManifest{}, fmt.Errorf("backup archive %s: %w", path, err)
	}

	seen := map[string]bool{BackupManifestPath: true}
	for _, ref := range manifest.Files {
		seen[ref.Path] = true
		f, ok := entries[ref.Path]
		if !ok {
			return BackupManifest{}, fmt.Errorf("backup archive %s: manifest references missing entry %s", path, ref.Path)
		}
		erc, err := f.Open()
		if err != nil {
			return BackupManifest{}, fmt.Errorf("backup archive %s: open entry %s: %w", path, ref.Path, err)
		}
		digest, n, hashErr := SHA256Hex(erc)
		closeErr := erc.Close()
		if hashErr != nil {
			return BackupManifest{}, fmt.Errorf("backup archive %s: hash entry %s: %w", path, ref.Path, hashErr)
		}
		if closeErr != nil {
			return BackupManifest{}, fmt.Errorf("backup archive %s: close entry %s: %w", path, ref.Path, closeErr)
		}
		if digest != ref.SHA256 {
			return BackupManifest{}, fmt.Errorf("backup archive %s: entry %s sha256 mismatch: manifest=%s actual=%s", path, ref.Path, ref.SHA256, digest)
		}
		if n != ref.Bytes {
			return BackupManifest{}, fmt.Errorf("backup archive %s: entry %s byte count mismatch: manifest=%d actual=%d", path, ref.Path, ref.Bytes, n)
		}
	}

	for _, tbl := range manifest.Tables {
		f, ok := entries[tbl.Path]
		if !ok {
			return BackupManifest{}, fmt.Errorf("backup archive %s: manifest references missing table entry %s", path, tbl.Path)
		}
		trc, err := f.Open()
		if err != nil {
			return BackupManifest{}, fmt.Errorf("backup archive %s: open table entry %s: %w", path, tbl.Path, err)
		}
		lines, err := countJSONLLinesReader(trc)
		closeErr := trc.Close()
		if err != nil {
			return BackupManifest{}, fmt.Errorf("backup archive %s: count rows in %s: %w", path, tbl.Path, err)
		}
		if closeErr != nil {
			return BackupManifest{}, fmt.Errorf("backup archive %s: close table entry %s: %w", path, tbl.Path, closeErr)
		}
		if lines != tbl.RowCount {
			return BackupManifest{}, fmt.Errorf("backup archive %s: table %s row count mismatch: manifest=%d actual=%d", path, tbl.TableName, tbl.RowCount, lines)
		}
	}

	for name := range entries {
		if !seen[name] {
			return BackupManifest{}, fmt.Errorf("backup archive %s: unexpected extra entry %s not listed in manifest", path, name)
		}
	}

	return *manifest, nil
}

// --------------------------------------------------------------------------
// The `kv backup` command.

// verifyFunc matches VerifyBackupArchive's signature. NewBackupCmd injects
// VerifyBackupArchive in production; tests inject a fake that fails the
// test if invoked, proving --no-verify truly skips the re-read (D-09's
// opt-out, not the default).
type verifyFunc func(path string) (BackupManifest, error)

// PrintBackupWarning writes the closing block every `kv backup` (and,
// verbatim, 16-10's `kv destroy --with-backup`) run prints: what personal
// data the artifact holds, that it is unencrypted by design, and that it
// may be the operator's only remaining copy (D-08).
func PrintBackupWarning(w io.Writer, res BackupResult) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "This backup contains personal data: full conversation transcripts and")
	fmt.Fprintln(w, "user email addresses. The archive is unencrypted by design — encrypting")
	fmt.Fprintln(w, "it with the secrets-encryption key held inside this AWS account would")
	fmt.Fprintln(w, "make it unopenable at exactly the moment it matters (after the account")
	fmt.Fprintln(w, "is gone). Store it somewhere safe: this file may be the only remaining copy.")
}

// printBackupResult writes the size/elapsed/per-table/ledger summary plus
// PrintBackupWarning's closing block to w.
func printBackupResult(w io.Writer, res BackupResult, verified bool) {
	fmt.Fprintf(w, "backup written: %s\n", res.Path)
	fmt.Fprintf(w, "size: %s\n", humanBytes(res.Bytes))
	fmt.Fprintf(w, "elapsed: %s\n", res.Elapsed.Round(time.Millisecond))
	for _, tbl := range res.Manifest.Tables {
		fmt.Fprintf(w, "  table %-16s %8d rows\n", tbl.Logical, tbl.RowCount)
	}
	fmt.Fprintf(w, "ledger: %d objects, %s\n", res.Manifest.Ledger.ObjectCount, humanBytes(res.Manifest.Ledger.ByteTotal))
	if verified {
		fmt.Fprintln(w, "verified: every SHA-256 and row count re-checked against manifest.json")
	} else {
		fmt.Fprintln(w, "verified: SKIPPED (--no-verify)")
	}
	for _, warn := range res.Warnings {
		fmt.Fprintf(w, "warning: %s\n", warn)
	}
	PrintBackupWarning(w, res)
}

// runBackup writes the archive and, when opts.Verify is set, re-reads it via
// verify (VerifyBackupArchive in production; a test double in tests) before
// printing the result to out. Kept separate from NewBackupCmd's RunE so it
// is testable with BackupDeps fakes and no real AWS/cobra wiring.
func runBackup(ctx context.Context, deps BackupDeps, opts BackupOptions, verify verifyFunc, out io.Writer) (BackupResult, error) {
	res, err := WriteBackupArchive(ctx, deps, opts)
	if err != nil {
		return BackupResult{}, err
	}
	if opts.Verify {
		if verify == nil {
			verify = VerifyBackupArchive
		}
		if _, err := verify(res.Path); err != nil {
			return res, fmt.Errorf("backup verification failed: %w", err)
		}
	}
	printBackupResult(out, res, opts.Verify)
	return res, nil
}

// gitRevParseHEAD resolves the current commit SHA (D-05's manifest field)
// via `git rev-parse HEAD`, run with Dir set to repoRootDir.
func gitRevParseHEAD(repoRootDir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoRootDir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("resolve git SHA (git rev-parse HEAD): %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// backupDIDLister builds a DIDLister from cfg's VoIP.ms credentials,
// mirroring studio.go's buildVoipmsInjections degradation: on any
// credential-resolution failure it returns nil rather than an error, so a
// missing VoIP.ms credential never blocks the DynamoDB/ledger backup that
// actually matters.
func backupDIDLister(ctx context.Context, cfg *Config) DIDLister {
	creds, err := cfg.resolveVoipmsCreds(ctx)
	if err != nil {
		return nil
	}
	vc := newVoipmsClient(creds)
	return func(ctx context.Context) ([]InboundDIDRecord, error) {
		return ListInboundDIDs(ctx, vc)
	}
}

// NewBackupCmd builds the "kv backup" command (D-01/D-03): writes one
// self-contained, timestamped zip of everything that exists only in AWS,
// verified by default (--no-verify opts out, D-09).
func NewBackupCmd(cfg *Config) *cobra.Command {
	var (
		outDir   string
		noVerify bool
	)

	backupCmd := &cobra.Command{
		Use:   "backup",
		Short: "Write a verified, self-contained backup of everything that exists only in AWS (D-02)",
		Long: "kv backup writes one timestamped, self-contained zip\n" +
			"(kmv-backup-<ISO8601>.zip) holding all three DynamoDB tables (JSONL),\n" +
			"the full S3 transcript ledger, and a small external inventory\n" +
			"(VoIP.ms DIDs, the NAT EIP, non-secret SSM parameters). Verification\n" +
			"is on by default: the finished zip is re-opened and every SHA-256 and\n" +
			"row count is re-checked against manifest.json before success is\n" +
			"reported (--no-verify skips this). Nothing in the artifact depends on\n" +
			"the AWS account continuing to exist (D-02) — it is designed for the\n" +
			"day this account is destroyed, not merely paused. The artifact is\n" +
			"deliberately unencrypted; see the closing warning this command prints.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := c.Context()

			root, err := repoRoot()
			if err != nil {
				return err
			}
			targets, err := ResolveLiveTargets(ctx, NewTerragruntOutputReader(root))
			if err != nil {
				return fmt.Errorf("resolve live targets: %w", err)
			}

			dynamoClient, err := cfg.DynamoClient(ctx)
			if err != nil {
				return err
			}
			s3Client, err := cfg.S3Client(ctx)
			if err != nil {
				return err
			}
			ssmClient, err := cfg.SSMClient(ctx)
			if err != nil {
				return err
			}

			awsCfg, err := cfg.loadAWS(ctx)
			if err != nil {
				return err
			}
			identity, err := sts.NewFromConfig(awsCfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
			if err != nil {
				return fmt.Errorf("resolve aws account id (sts get-caller-identity): %w", err)
			}

			gitSHA, err := gitRevParseHEAD(root)
			if err != nil {
				return err
			}

			deps := BackupDeps{
				Dynamo:       dynamoClient,
				S3:           s3Client,
				SSM:          ssmClient,
				DIDs:         backupDIDLister(ctx, cfg),
				Targets:      targets,
				AWSAccountID: aws.ToString(identity.Account),
				Region:       resolveAWSRegion(cfg.Region),
				GitSHA:       gitSHA,
				Now:          time.Now,
			}

			_, err = runBackup(ctx, deps, BackupOptions{OutDir: outDir, Verify: !noVerify}, VerifyBackupArchive, c.OutOrStdout())
			return err
		},
	}

	backupCmd.Flags().StringVar(&outDir, "out", "./backups", "output directory for the backup zip")
	backupCmd.Flags().BoolVar(&noVerify, "no-verify", false, "skip re-reading and verifying the written archive (verification is on by default, D-09)")

	return backupCmd
}
