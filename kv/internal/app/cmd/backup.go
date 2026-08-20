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
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
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
