package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// fixedManifestCreatedAt is used across manifest tests instead of time.Now()
// so assertions never depend on wall-clock timing.
var fixedManifestCreatedAt = time.Date(2026, 8, 12, 14, 30, 0, 0, time.UTC)

func sampleBackupManifest() *BackupManifest {
	return &BackupManifest{
		Version:      BackupManifestVersion,
		CreatedAt:    fixedManifestCreatedAt,
		GitSHA:       "abc1234def",
		KvVersion:    "dev",
		AWSAccountID: "123456789012",
		Region:       "us-east-1",
		Tables: []BackupTableRef{
			{Logical: "auth-electro", TableName: "kmv-auth-electro", Path: "dynamodb/kmv-auth-electro.jsonl", RowCount: 42},
			{Logical: "auth-authjs", TableName: "kmv-auth-authjs", Path: "dynamodb/kmv-auth-authjs.jsonl", RowCount: 7},
			{Logical: "voice-usage", TableName: "kmv-voice-usage", Path: "dynamodb/kmv-voice-usage.jsonl", RowCount: 1200},
		},
		Ledger: BackupLedgerRef{
			BucketName:  "kmv-ledger-abc123",
			Prefix:      "",
			ObjectCount: 10,
			ByteTotal:   2048,
		},
		External: []BackupFileRef{
			{Path: "external/voipms-dids.json", Bytes: 512, SHA256: "deadbeef00"},
			{Path: "external/nat-eip.txt", Bytes: 12, SHA256: "deadbeef01"},
		},
		Files: []BackupFileRef{
			{Path: "dynamodb/kmv-auth-electro.jsonl", Bytes: 1024, SHA256: "cafebabe00"},
		},
	}
}

// Test 1: a manifest round-trips through marshal then parse with every D-05
// field preserved.
func TestBackupManifest_RoundTrip(t *testing.T) {
	want := sampleBackupManifest()

	b, err := want.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !bytes.HasSuffix(b, []byte("\n")) {
		t.Error("Marshal() output does not end with a trailing newline")
	}

	got, err := ParseBackupManifest(b)
	if err != nil {
		t.Fatalf("ParseBackupManifest() error = %v", err)
	}

	if got.Version != want.Version {
		t.Errorf("Version = %d, want %d", got.Version, want.Version)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, want.CreatedAt)
	}
	if got.GitSHA != want.GitSHA {
		t.Errorf("GitSHA = %q, want %q", got.GitSHA, want.GitSHA)
	}
	if got.KvVersion != want.KvVersion {
		t.Errorf("KvVersion = %q, want %q", got.KvVersion, want.KvVersion)
	}
	if got.AWSAccountID != want.AWSAccountID {
		t.Errorf("AWSAccountID = %q, want %q", got.AWSAccountID, want.AWSAccountID)
	}
	if got.Region != want.Region {
		t.Errorf("Region = %q, want %q", got.Region, want.Region)
	}
	if len(got.Tables) != len(want.Tables) {
		t.Fatalf("len(Tables) = %d, want %d", len(got.Tables), len(want.Tables))
	}
	for i, wantTable := range want.Tables {
		gotTable := got.Tables[i]
		if gotTable != wantTable {
			t.Errorf("Tables[%d] = %+v, want %+v", i, gotTable, wantTable)
		}
	}
	if got.Ledger != want.Ledger {
		t.Errorf("Ledger = %+v, want %+v", got.Ledger, want.Ledger)
	}
	if len(got.External) != len(want.External) {
		t.Fatalf("len(External) = %d, want %d", len(got.External), len(want.External))
	}
	for i, wantFile := range want.External {
		if got.External[i] != wantFile {
			t.Errorf("External[%d] = %+v, want %+v", i, got.External[i], wantFile)
		}
	}
	if len(got.Files) != len(want.Files) {
		t.Fatalf("len(Files) = %d, want %d", len(got.Files), len(want.Files))
	}
	for i, wantFile := range want.Files {
		if got.Files[i] != wantFile {
			t.Errorf("Files[%d] = %+v, want %+v", i, got.Files[i], wantFile)
		}
	}

	// A second marshal of the round-tripped manifest must be byte-identical
	// to the first — proves nothing was lost or reordered.
	gotBytes, err := got.Marshal()
	if err != nil {
		t.Fatalf("re-Marshal() error = %v", err)
	}
	if !bytes.Equal(b, gotBytes) {
		t.Errorf("re-marshaled manifest differs from the original:\nwant %s\ngot  %s", b, gotBytes)
	}
}

// Test 2: SHA256Hex over a known byte slice returns the known lowercase hex
// digest and the correct byte count.
func TestSHA256Hex(t *testing.T) {
	t.Run("KnownString", func(t *testing.T) {
		digest, n, err := SHA256Hex(strings.NewReader("hello world"))
		if err != nil {
			t.Fatalf("SHA256Hex() error = %v", err)
		}
		const wantDigest = "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
		if digest != wantDigest {
			t.Errorf("digest = %q, want %q", digest, wantDigest)
		}
		if n != 11 {
			t.Errorf("n = %d, want 11", n)
		}
	})

	t.Run("EmptyReader", func(t *testing.T) {
		digest, n, err := SHA256Hex(strings.NewReader(""))
		if err != nil {
			t.Fatalf("SHA256Hex() error = %v", err)
		}
		const wantDigest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
		if digest != wantDigest {
			t.Errorf("digest = %q, want %q", digest, wantDigest)
		}
		if n != 0 {
			t.Errorf("n = %d, want 0", n)
		}
	})
}

// Test 3: ParseBackupManifest on a manifest whose Version is unknown returns
// a non-nil error naming the version it found.
func TestBackupManifest_ParseUnknownVersion(t *testing.T) {
	b := []byte(`{"version": 99, "gitSha": "abc"}`)
	m, err := ParseBackupManifest(b)
	if err == nil {
		t.Fatal("ParseBackupManifest() error = nil, want non-nil for unknown version")
	}
	if m != nil {
		t.Errorf("ParseBackupManifest() manifest = %+v, want nil", m)
	}
	if !strings.Contains(err.Error(), "99") {
		t.Errorf("error %q does not name the found version 99", err.Error())
	}
}

// Test 4: FileRef returns found=false for a path absent from the manifest
// and found=true with the recorded digest for one present.
func TestBackupManifest_FileRef(t *testing.T) {
	m := sampleBackupManifest()

	t.Run("Found", func(t *testing.T) {
		ref, found := m.FileRef("dynamodb/kmv-auth-electro.jsonl")
		if !found {
			t.Fatal("FileRef() found = false, want true")
		}
		if ref.SHA256 != "cafebabe00" {
			t.Errorf("ref.SHA256 = %q, want %q", ref.SHA256, "cafebabe00")
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		_, found := m.FileRef("dynamodb/does-not-exist.jsonl")
		if found {
			t.Error("FileRef() found = true, want false for an absent path")
		}
	})
}

// Test 5: ParseBackupManifest on truncated or non-JSON bytes returns a
// non-nil error and a nil manifest.
func TestBackupManifest_ParseMalformed(t *testing.T) {
	cases := map[string][]byte{
		"Truncated": []byte(`{"version": 1, "gitSha"`),
		"NotJSON":   []byte("this is not json at all"),
		"Empty":     []byte(""),
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			m, err := ParseBackupManifest(b)
			if err == nil {
				t.Fatal("ParseBackupManifest() error = nil, want non-nil")
			}
			if m != nil {
				t.Errorf("ParseBackupManifest() manifest = %+v, want nil", m)
			}
		})
	}
}
