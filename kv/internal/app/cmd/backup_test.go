package cmd

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
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

// --------------------------------------------------------------------------
// fakeSSMInventoryAPI — hand-rolled ssmInventoryAPI fake.

type fakeSSMParam struct {
	name         string
	typ          ssmtypes.ParameterType
	value        string
	lastModified time.Time
	version      int64
}

type fakeSSMInventoryAPI struct {
	params   []fakeSSMParam
	getCalls []*ssm.GetParameterInput
}

func (f *fakeSSMInventoryAPI) DescribeParameters(_ context.Context, _ *ssm.DescribeParametersInput, _ ...func(*ssm.Options)) (*ssm.DescribeParametersOutput, error) {
	metas := make([]ssmtypes.ParameterMetadata, len(f.params))
	for i, p := range f.params {
		name := p.name
		lm := p.lastModified
		metas[i] = ssmtypes.ParameterMetadata{Name: &name, Type: p.typ, LastModifiedDate: &lm, Version: p.version}
	}
	return &ssm.DescribeParametersOutput{Parameters: metas}, nil
}

func (f *fakeSSMInventoryAPI) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	f.getCalls = append(f.getCalls, in)
	for _, p := range f.params {
		if p.name == aws.ToString(in.Name) {
			val := p.value
			return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Name: in.Name, Value: &val, Type: p.typ}}, nil
		}
	}
	return &ssm.GetParameterOutput{}, nil
}

func TestExportSSMInventory_SecureStringValueOmitted(t *testing.T) {
	now := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	api := &fakeSSMInventoryAPI{params: []fakeSSMParam{
		{name: "/kmv/secrets/use1/telephony/access_pin", typ: ssmtypes.ParameterTypeSecureString, value: "should-never-appear", lastModified: now, version: 3},
		{name: "/kmv/config/use1/some-string", typ: ssmtypes.ParameterTypeString, value: "plain-value", lastModified: now, version: 1},
		{name: "/kmv/config/use1/some-list", typ: ssmtypes.ParameterTypeStringList, value: "a,b,c", lastModified: now, version: 2},
	}}
	b, err := ExportSSMInventory(context.Background(), api, "/kmv/")
	if err != nil {
		t.Fatalf("ExportSSMInventory() error = %v, want nil", err)
	}
	var inv ssmInventory
	if err := json.Unmarshal(b, &inv); err != nil {
		t.Fatalf("unmarshal inventory: %v", err)
	}
	if len(inv.Parameters) != 3 {
		t.Fatalf("len(Parameters) = %d, want 3", len(inv.Parameters))
	}
	byName := map[string]ssmParamRecord{}
	for _, p := range inv.Parameters {
		byName[p.Name] = p
	}
	sec := byName["/kmv/secrets/use1/telephony/access_pin"]
	if !sec.ValueOmitted {
		t.Error("SecureString record ValueOmitted = false, want true")
	}
	if sec.Value != "" {
		t.Errorf("SecureString record Value = %q, want empty", sec.Value)
	}
	str := byName["/kmv/config/use1/some-string"]
	if str.ValueOmitted {
		t.Error("String record ValueOmitted = true, want false")
	}
	if str.Value != "plain-value" {
		t.Errorf("String record Value = %q, want plain-value", str.Value)
	}
	list := byName["/kmv/config/use1/some-list"]
	if list.Value != "a,b,c" {
		t.Errorf("StringList record Value = %q, want a,b,c", list.Value)
	}
	if strings.Contains(string(b), "should-never-appear") {
		t.Error("SecureString value leaked into the exported inventory JSON")
	}
}

func TestExportSSMInventory_NeverDecrypts(t *testing.T) {
	now := time.Now()
	api := &fakeSSMInventoryAPI{params: []fakeSSMParam{
		{name: "/kmv/secrets/use1/x", typ: ssmtypes.ParameterTypeSecureString, value: "secret", lastModified: now, version: 1},
		{name: "/kmv/config/use1/y", typ: ssmtypes.ParameterTypeString, value: "public", lastModified: now, version: 1},
	}}
	if _, err := ExportSSMInventory(context.Background(), api, "/kmv/"); err != nil {
		t.Fatalf("ExportSSMInventory() error = %v, want nil", err)
	}
	if len(api.getCalls) == 0 {
		t.Fatal("GetParameter never called for the String parameter")
	}
	for _, call := range api.getCalls {
		if call.WithDecryption != nil && *call.WithDecryption {
			t.Fatalf("GetParameter(%s) called with WithDecryption=true — SecureString values must never be decrypted (D-14)", aws.ToString(call.Name))
		}
	}
}

func TestExportDIDInventory_ListerErrorDegradesGracefully(t *testing.T) {
	lister := DIDLister(func(ctx context.Context) ([]InboundDIDRecord, error) {
		return nil, errors.New("voip.ms unreachable")
	})
	b, err := ExportDIDInventory(context.Background(), lister)
	if err != nil {
		t.Fatalf("ExportDIDInventory() error = %v, want nil (degrade, don't fail)", err)
	}
	var inv didInventory
	if err := json.Unmarshal(b, &inv); err != nil {
		t.Fatalf("unmarshal did inventory: %v", err)
	}
	if inv.Error == "" {
		t.Error("Error field is empty, want a non-empty degradation reason")
	}
	if len(inv.DIDs) != 0 {
		t.Errorf("DIDs = %v, want empty", inv.DIDs)
	}
}

func TestExportDIDInventory_NilListerDegradesGracefully(t *testing.T) {
	b, err := ExportDIDInventory(context.Background(), nil)
	if err != nil {
		t.Fatalf("ExportDIDInventory() error = %v, want nil", err)
	}
	var inv didInventory
	if err := json.Unmarshal(b, &inv); err != nil {
		t.Fatalf("unmarshal did inventory: %v", err)
	}
	if inv.Error == "" {
		t.Error("Error field is empty, want a non-empty degradation reason for a nil lister")
	}
}

// --------------------------------------------------------------------------
// WriteBackupArchive — the assembled zip.

func testBackupDeps(t *testing.T) BackupDeps {
	t.Helper()
	dyn := &fakeDynamoScanAPI{errAtCall: -1, pages: []*dynamodb.ScanOutput{
		{Items: []map[string]types.AttributeValue{{"pk": strAV("row1")}, {"pk": strAV("row2")}}},
	}}
	s3objects := map[string][]byte{
		"transcripts/a.json": []byte("aaaa"),
		"transcripts/b.json": []byte("bb"),
	}
	s3api := &fakeS3ListGetAPI{
		objects: s3objects,
		pages: [][]fakeS3Object{
			{{key: "transcripts/a.json", body: s3objects["transcripts/a.json"]}, {key: "transcripts/b.json", body: s3objects["transcripts/b.json"]}},
		},
	}
	ssmAPI := &fakeSSMInventoryAPI{params: []fakeSSMParam{
		{name: "/kmv/config/use1/x", typ: ssmtypes.ParameterTypeString, value: "v", lastModified: time.Now(), version: 1},
	}}
	dids := DIDLister(func(ctx context.Context) ([]InboundDIDRecord, error) {
		return []InboundDIDRecord{{DID: "7254043234", Description: "test"}}, nil
	})
	return BackupDeps{
		Dynamo: dyn,
		S3:     s3api,
		SSM:    ssmAPI,
		DIDs:   dids,
		Targets: LiveTargets{
			TableNames: map[string]string{
				"auth-electro": "kmv-auth-electro",
				"auth-authjs":  "kmv-auth-authjs",
				"voice-usage":  "kmv-voice-usage",
			},
			LedgerBucket: "kmv-ledger-abc123",
			NATEIP:       "203.0.113.7",
		},
		AWSAccountID: "123456789012",
		Region:       "us-east-1",
		GitSHA:       "deadbeef",
		Now:          func() time.Time { return time.Date(2026, 8, 20, 1, 2, 3, 0, time.UTC) },
	}
}

func openZipEntries(t *testing.T, path string) map[string][]byte {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("zip.OpenReader(%s): %v", path, err)
	}
	defer zr.Close()
	out := make(map[string][]byte, len(zr.File))
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open entry %s: %v", f.Name, err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read entry %s: %v", f.Name, err)
		}
		out[f.Name] = b
	}
	return out
}

func TestWriteBackupArchive_EntrySet(t *testing.T) {
	deps := testBackupDeps(t)
	res, err := WriteBackupArchive(context.Background(), deps, BackupOptions{OutDir: t.TempDir()})
	if err != nil {
		t.Fatalf("WriteBackupArchive() error = %v, want nil", err)
	}
	entries := openZipEntries(t, res.Path)
	want := []string{
		"manifest.json",
		"dynamodb/auth-electro.jsonl",
		"dynamodb/auth-authjs.jsonl",
		"dynamodb/voice-usage.jsonl",
		"ledger/transcripts/a.json",
		"ledger/transcripts/b.json",
		"external/voipms-dids.json",
		"external/nat-eip.txt",
		"external/ssm-params.json",
	}
	var got []string
	for name := range entries {
		got = append(got, name)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("zip entry set:\n got  = %v\n want = %v", got, want)
	}
}

func TestWriteBackupArchive_ManifestDigestsMatch(t *testing.T) {
	deps := testBackupDeps(t)
	res, err := WriteBackupArchive(context.Background(), deps, BackupOptions{OutDir: t.TempDir()})
	if err != nil {
		t.Fatalf("WriteBackupArchive() error = %v, want nil", err)
	}
	entries := openZipEntries(t, res.Path)
	if len(res.Manifest.Files) == 0 {
		t.Fatal("manifest.Files is empty, want one ref per non-manifest entry")
	}
	for _, ref := range res.Manifest.Files {
		body, ok := entries[ref.Path]
		if !ok {
			t.Errorf("manifest references %s, not present in zip", ref.Path)
			continue
		}
		sum := sha256.Sum256(body)
		gotDigest := hex.EncodeToString(sum[:])
		if gotDigest != ref.SHA256 {
			t.Errorf("entry %s: manifest sha256 %s != recomputed %s", ref.Path, ref.SHA256, gotDigest)
		}
		if int64(len(body)) != ref.Bytes {
			t.Errorf("entry %s: manifest bytes %d != actual %d", ref.Path, ref.Bytes, len(body))
		}
	}
}

func TestWriteBackupArchive_DIDListerErrorStillSucceeds(t *testing.T) {
	deps := testBackupDeps(t)
	deps.DIDs = DIDLister(func(ctx context.Context) ([]InboundDIDRecord, error) {
		return nil, errors.New("voip.ms down")
	})
	res, err := WriteBackupArchive(context.Background(), deps, BackupOptions{OutDir: t.TempDir()})
	if err != nil {
		t.Fatalf("WriteBackupArchive() error = %v, want nil (DID inventory is advisory)", err)
	}
	entries := openZipEntries(t, res.Path)
	var inv didInventory
	if err := json.Unmarshal(entries["external/voipms-dids.json"], &inv); err != nil {
		t.Fatalf("unmarshal external/voipms-dids.json: %v", err)
	}
	if inv.Error == "" {
		t.Error("expected a non-empty error field in external/voipms-dids.json")
	}
}
