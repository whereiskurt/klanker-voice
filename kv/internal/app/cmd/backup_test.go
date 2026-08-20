package cmd

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// --------------------------------------------------------------------------
// fakeDynamoScanAPI — hand-rolled, in-memory dynamoScanAPI fake (D-30: no
// DynamoDB Local, no network).

type fakeDynamoScanAPI struct {
	pages     []*dynamodb.ScanOutput
	call      int
	errAtCall int // -1 means never error
	err       error
}

func (f *fakeDynamoScanAPI) Scan(_ context.Context, _ *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	if f.errAtCall >= 0 && f.call == f.errAtCall {
		f.call++
		return nil, f.err
	}
	if f.call >= len(f.pages) {
		return &dynamodb.ScanOutput{}, nil
	}
	page := f.pages[f.call]
	f.call++
	return page, nil
}

func strAV(s string) types.AttributeValue { return &types.AttributeValueMemberS{Value: s} }

func TestScanTableToJSONL_TwoPages(t *testing.T) {
	api := &fakeDynamoScanAPI{
		errAtCall: -1,
		pages: []*dynamodb.ScanOutput{
			{
				Items: []map[string]types.AttributeValue{
					{"pk": strAV("a")},
					{"pk": strAV("b")},
				},
				LastEvaluatedKey: map[string]types.AttributeValue{"pk": strAV("b")},
			},
			{
				Items: []map[string]types.AttributeValue{
					{"pk": strAV("c")},
				},
			},
		},
	}
	var buf bytes.Buffer
	rowCount, err := ScanTableToJSONL(context.Background(), api, "kmv-test", &buf)
	if err != nil {
		t.Fatalf("ScanTableToJSONL() error = %v, want nil", err)
	}
	if rowCount != 3 {
		t.Fatalf("rowCount = %d, want 3", rowCount)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("line count = %d, want 3 (one JSON object per line)", len(lines))
	}
}

func TestScanTableToJSONL_AttributeTypeRoundTrip(t *testing.T) {
	item := map[string]types.AttributeValue{
		"binVal":  &types.AttributeValueMemberB{Value: []byte{0x01, 0x02, 0xff}},
		"strSet":  &types.AttributeValueMemberSS{Value: []string{"x", "y"}},
		"numSet":  &types.AttributeValueMemberNS{Value: []string{"1", "2"}},
		"isNull":  &types.AttributeValueMemberNULL{Value: true},
		"nested":  &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{"inner": strAV("v")}},
		"list":    &types.AttributeValueMemberL{Value: []types.AttributeValue{strAV("a"), &types.AttributeValueMemberN{Value: "42"}}},
		"boolVal": &types.AttributeValueMemberBOOL{Value: true},
		"numVal":  &types.AttributeValueMemberN{Value: "3.14"},
		"strVal":  strAV("hello"),
	}
	api := &fakeDynamoScanAPI{errAtCall: -1, pages: []*dynamodb.ScanOutput{{Items: []map[string]types.AttributeValue{item}}}}
	var buf bytes.Buffer
	if _, err := ScanTableToJSONL(context.Background(), api, "kmv-test", &buf); err != nil {
		t.Fatalf("ScanTableToJSONL() error = %v, want nil", err)
	}
	line := strings.TrimRight(buf.String(), "\n")
	roundTripped, err := unmarshalItemJSONL([]byte(line))
	if err != nil {
		t.Fatalf("unmarshalItemJSONL() error = %v, want nil", err)
	}
	if !reflect.DeepEqual(item, roundTripped) {
		t.Errorf("round trip mismatch:\n original = %#v\nroundtrip = %#v", item, roundTripped)
	}
}

func TestScanTableToJSONL_EmptyTable(t *testing.T) {
	api := &fakeDynamoScanAPI{errAtCall: -1, pages: []*dynamodb.ScanOutput{{Items: []map[string]types.AttributeValue{}}}}
	var buf bytes.Buffer
	rowCount, err := ScanTableToJSONL(context.Background(), api, "kmv-test", &buf)
	if err != nil {
		t.Fatalf("ScanTableToJSONL() error = %v, want nil", err)
	}
	if rowCount != 0 {
		t.Errorf("rowCount = %d, want 0", rowCount)
	}
	if buf.Len() != 0 {
		t.Errorf("buf.Len() = %d, want 0 (empty table -> zero-byte JSONL entry)", buf.Len())
	}
}

func TestScanTableToJSONL_ScanErrorMidPagination(t *testing.T) {
	api := &fakeDynamoScanAPI{
		errAtCall: 1,
		err:       errors.New("boom"),
		pages: []*dynamodb.ScanOutput{
			{
				Items:            []map[string]types.AttributeValue{{"pk": strAV("a")}},
				LastEvaluatedKey: map[string]types.AttributeValue{"pk": strAV("a")},
			},
		},
	}
	var buf bytes.Buffer
	rowCount, err := ScanTableToJSONL(context.Background(), api, "kmv-test", &buf)
	if err == nil {
		t.Fatal("ScanTableToJSONL() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "kmv-test") {
		t.Errorf("error = %q, want it to name the table kmv-test", err.Error())
	}
	if rowCount != 0 {
		t.Errorf("rowCount = %d, want 0 (must not report a partial count as complete)", rowCount)
	}
}

// --------------------------------------------------------------------------
// fakeS3ListGetAPI — hand-rolled, in-memory s3ListGetAPI fake.

type fakeS3Object struct {
	key  string
	body []byte
}

type fakeS3ListGetAPI struct {
	pages    [][]fakeS3Object // each inner slice is one ListObjectsV2 page
	pageCall int
	objects  map[string][]byte
}

func (f *fakeS3ListGetAPI) ListObjectsV2(_ context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	if f.pageCall >= len(f.pages) {
		falseVal := false
		return &s3.ListObjectsV2Output{IsTruncated: &falseVal}, nil
	}
	page := f.pages[f.pageCall]
	f.pageCall++
	contents := make([]s3types.Object, len(page))
	for i, obj := range page {
		key := obj.key
		size := int64(len(obj.body))
		contents[i] = s3types.Object{Key: &key, Size: &size}
	}
	truncated := f.pageCall < len(f.pages)
	out := &s3.ListObjectsV2Output{Contents: contents, IsTruncated: &truncated}
	if truncated {
		token := "next"
		out.NextContinuationToken = &token
	}
	return out, nil
}

func (f *fakeS3ListGetAPI) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	body, ok := f.objects[*in.Key]
	if !ok {
		return nil, errors.New("object not found: " + *in.Key)
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(body))}, nil
}

func TestWalkLedgerObjects_MultiPageKeyPreservation(t *testing.T) {
	objects := map[string][]byte{
		"transcripts/2026/08/19/a.json": []byte("aaa"),
		"transcripts/2026/08/19/b.json": []byte("bb"),
		"transcripts/2026/08/20/c.json": []byte("c"),
		"transcripts/2026/08/20/d.json": []byte("dddd"),
		"transcripts/2026/08/21/e.json": []byte("ee"),
		"transcripts/2026/08/21/f.json": []byte("f"),
		"transcripts/2026/08/21/g.json": []byte("ggg"),
	}
	api := &fakeS3ListGetAPI{
		objects: objects,
		pages: [][]fakeS3Object{
			{{key: "transcripts/2026/08/19/a.json"}, {key: "transcripts/2026/08/19/b.json"}, {key: "transcripts/2026/08/20/c.json"}},
			{{key: "transcripts/2026/08/20/d.json"}, {key: "transcripts/2026/08/21/e.json"}, {key: "transcripts/2026/08/21/f.json"}},
			{{key: "transcripts/2026/08/21/g.json"}},
		},
	}
	// fix up per-object body length so Size matches
	for pi, page := range api.pages {
		for oi, obj := range page {
			api.pages[pi][oi].body = objects[obj.key]
		}
	}

	seen := map[string][]byte{}
	visited := 0
	objCount, byteTotal, err := WalkLedgerObjects(context.Background(), api, "kmv-ledger", func(key string, body io.Reader, size int64) error {
		visited++
		b, err := io.ReadAll(body)
		if err != nil {
			return err
		}
		seen[key] = b
		if int64(len(b)) != size {
			t.Errorf("visit(%s): body length %d != reported size %d", key, len(b), size)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkLedgerObjects() error = %v, want nil", err)
	}
	if visited != 7 {
		t.Fatalf("visit called %d times, want 7", visited)
	}
	if objCount != 7 {
		t.Errorf("objects = %d, want 7", objCount)
	}
	var wantBytes int64
	for _, b := range objects {
		wantBytes += int64(len(b))
	}
	if byteTotal != wantBytes {
		t.Errorf("byteTotal = %d, want %d", byteTotal, wantBytes)
	}
	for key, want := range objects {
		got, ok := seen[key]
		if !ok {
			t.Errorf("key %q not visited", key)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("key %q body = %q, want %q", key, got, want)
		}
	}
}

// countJSONLLines is a small test helper shared with later tasks' tests.
func countJSONLLines(t *testing.T, b []byte) int {
	t.Helper()
	if len(b) == 0 {
		return 0
	}
	scanner := bufio.NewScanner(bytes.NewReader(b))
	n := 0
	for scanner.Scan() {
		n++
	}
	return n
}
