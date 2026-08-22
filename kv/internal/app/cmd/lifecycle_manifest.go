package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// BackupManifestVersion is the current schema version written by `kv backup`
// and required by `kv restore`. Bump this and add an explicit migration path
// in ParseBackupManifest before ever changing a field's meaning.
const BackupManifestVersion = 1

// BackupManifestPath is the zip-internal path of the manifest file (D-04's
// artifact layout: manifest.json at the archive root).
const BackupManifestPath = "manifest.json"

// BackupManifest is the Phase-16 backup artifact schema (D-05): everything
// needed to audit what a backup contains and prove, on restore, that every
// row and every byte survived the round trip.
//
// The recorded TableName (on each BackupTableRef) and BucketName (on
// BackupLedgerRef) exist ONLY to audit where this backup came from — they
// are never a restore destination. A restore destination comes exclusively
// from ResolveLiveTargets, resolved live from terraform outputs at restore
// time (D-10); post-destroy/recreate infrastructure carries new resolved
// names (e.g. the ledger bucket's random_id suffix), so a name read out of
// this manifest would silently target the wrong (or a nonexistent) account
// resource.
type BackupManifest struct {
	Version      int       `json:"version"`
	CreatedAt    time.Time `json:"createdAt"`
	GitSHA       string    `json:"gitSha"`
	KvVersion    string    `json:"kvVersion"`
	AWSAccountID string    `json:"awsAccountId"`
	Region       string    `json:"region"`

	Tables []BackupTableRef `json:"tables"`
	Ledger BackupLedgerRef  `json:"ledger"`

	External []BackupFileRef `json:"external"`
	Files    []BackupFileRef `json:"files"`
}

// BackupTableRef records one DynamoDB table's backup: Logical is the stable
// key used across the phase (auth-electro, auth-authjs, voice-usage) and
// TableName is the physical name resolved live at backup time — audit-only,
// see BackupManifest's doc comment.
type BackupTableRef struct {
	Logical   string `json:"logical"`
	TableName string `json:"tableName"`
	Path      string `json:"path"`
	RowCount  int64  `json:"rowCount"`
}

// BackupLedgerRef records the ledger backup: BucketName is audit-only (see
// BackupManifest's doc comment), Prefix is the key prefix backed up (empty
// means the whole bucket), and ObjectCount/ByteTotal summarize the object
// tree under ledger/ in the archive.
type BackupLedgerRef struct {
	BucketName  string `json:"bucketName"`
	Prefix      string `json:"prefix"`
	ObjectCount int64  `json:"objectCount"`
	ByteTotal   int64  `json:"byteTotal"`
}

// BackupFileRef is a single file's identity inside the backup zip: Path is
// the zip-internal slash-separated path (e.g. "dynamodb/kmv-voice-usage.jsonl"
// or "external/nat-eip.txt"), Bytes is its size, and SHA256 is its lowercase
// hex digest — the pair verification (D-09) checks after writing.
type BackupFileRef struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// SHA256Hex streams r through crypto/sha256 (io.Copy, never fully buffering
// the input — a large ledger object must not be read entirely into memory)
// and returns the lowercase hex digest plus the number of bytes read.
func SHA256Hex(r io.Reader) (digest string, n int64, err error) {
	h := sha256.New()
	n, err = io.Copy(h, r)
	if err != nil {
		return "", 0, fmt.Errorf("sha256 hash: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// Marshal produces indented JSON with a trailing newline — the on-disk shape
// of manifest.json inside the backup zip.
func (m *BackupManifest) Marshal() ([]byte, error) {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal backup manifest: %w", err)
	}
	return append(b, '\n'), nil
}

// ParseBackupManifest decodes manifest.json bytes, rejecting an unknown
// Version with an error naming the version it found — `kv restore` must
// never silently guess at an incompatible schema.
func ParseBackupManifest(b []byte) (*BackupManifest, error) {
	var m BackupManifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse backup manifest: %w", err)
	}
	if m.Version != BackupManifestVersion {
		return nil, fmt.Errorf("unsupported backup manifest version %d (expected %d)", m.Version, BackupManifestVersion)
	}
	return &m, nil
}

// FileRef looks up a zip-internal path in m.Files, returning found=false
// when absent.
func (m *BackupManifest) FileRef(path string) (BackupFileRef, bool) {
	for _, f := range m.Files {
		if f.Path == path {
			return f, true
		}
	}
	return BackupFileRef{}, false
}
