package studio

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --------------------------------------------------------------------------
// Fixture harness

// seededDIDsFile mirrors the real apps/voice/configs/studio/dids.yaml's
// shape after seeding (header comment + `dids: []`) without depending on the
// actual repo file's exact wording, so this test stays independent of
// future header edits.
const seededDIDsFile = `# apps/voice/configs/studio/dids.yaml
#
# Studio-owned per-DID default rule + greeting registry. NOT a VoIP.ms
# provisioning list -- see WriteDIDMeta's doc comment.

dids: []
`

// seededRuleOrderFile mirrors the real
// apps/voice/configs/studio/rule-order.yaml's shape after seeding (header +
// `order: []`).
const seededRuleOrderFile = `# apps/voice/configs/studio/rule-order.yaml
#
# PRESENTATION / AUTHORING ORDER ONLY -- does NOT change call routing. See
# WriteRuleOrder's doc comment.

order: []
`

func writeFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func readFile(t *testing.T, dir, relPath string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, relPath))
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	return string(b)
}

// writeGoldenDIDsRepo copies testdata/dids.golden.yaml (two pre-existing
// DID rows) into a temp repo root at dids.yaml's real repo-relative path.
func writeGoldenDIDsRepo(t *testing.T) (dir, golden string) {
	t.Helper()
	b, err := os.ReadFile("testdata/dids.golden.yaml")
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	dir = t.TempDir()
	writeFile(t, dir, studioDIDsPath, string(b))
	return dir, string(b)
}

// writeGoldenRuleOrderRepo copies testdata/rule-order.golden.yaml (a
// non-empty order list) into a temp repo root at rule-order.yaml's real
// repo-relative path.
func writeGoldenRuleOrderRepo(t *testing.T) (dir, golden string) {
	t.Helper()
	b, err := os.ReadFile("testdata/rule-order.golden.yaml")
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	dir = t.TempDir()
	writeFile(t, dir, ruleOrderPath, string(b))
	return dir, string(b)
}

// mustReplaceOnce is defined once in repofile_writer_test.go (same package)
// and reused here.

// --------------------------------------------------------------------------
// ReadDIDMeta

func TestReadDIDMeta_AbsentFileReturnsEmptySlice(t *testing.T) {
	dir := t.TempDir()
	rf := RepoFiles{Root: dir}

	got, err := rf.ReadDIDMeta()
	if err != nil {
		t.Fatalf("ReadDIDMeta() error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ReadDIDMeta() = %+v, want empty slice", got)
	}
}

func TestReadDIDMeta_EmptyFileReturnsEmptySlice(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, studioDIDsPath, "")
	rf := RepoFiles{Root: dir}

	got, err := rf.ReadDIDMeta()
	if err != nil {
		t.Fatalf("ReadDIDMeta() error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ReadDIDMeta() = %+v, want empty slice", got)
	}
}

func TestReadDIDMeta_SeededFlowEmptyReturnsEmptySlice(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, studioDIDsPath, seededDIDsFile)
	rf := RepoFiles{Root: dir}

	got, err := rf.ReadDIDMeta()
	if err != nil {
		t.Fatalf("ReadDIDMeta() error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ReadDIDMeta() = %+v, want empty slice", got)
	}
}

func TestReadDIDMeta_ParsesGoldenFixture(t *testing.T) {
	dir, _ := writeGoldenDIDsRepo(t)
	rf := RepoFiles{Root: dir}

	got, err := rf.ReadDIDMeta()
	if err != nil {
		t.Fatalf("ReadDIDMeta() error: %v", err)
	}
	want := []DIDMeta{
		{
			Did:         "+16135550100",
			Label:       "Ottawa main line",
			Region:      "CA-ON",
			DefaultRule: "kph-tier-code",
			Greeting:    "Hey, thanks for calling the Ottawa line.",
		},
		{
			Did:         "+13475550199",
			Label:       "NYC line",
			Region:      "US-NY",
			DefaultRule: "greenhouse-code",
			Greeting:    "Hi, you've reached the NYC number.",
		},
	}
	if len(got) != len(want) {
		t.Fatalf("ReadDIDMeta() returned %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// --------------------------------------------------------------------------
// WriteDIDMeta

func TestWriteDIDMeta_NewDIDAppendsToSeededEmptyFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, studioDIDsPath, seededDIDsFile)
	rf := RepoFiles{Root: dir}

	m := DIDMeta{
		Did:         "+16135550100",
		Label:       "Ottawa main line",
		Region:      "CA-ON",
		DefaultRule: "kph-tier-code",
		Greeting:    "Hey there.",
	}
	if err := rf.WriteDIDMeta(m); err != nil {
		t.Fatalf("WriteDIDMeta() error: %v", err)
	}

	want := mustReplaceOnce(t, seededDIDsFile, "dids: []", strings.Join([]string{
		"dids:",
		`  - did: "+16135550100"`,
		`    label: "Ottawa main line"`,
		`    region: "CA-ON"`,
		`    default_rule: "kph-tier-code"`,
		`    greeting: "Hey there."`,
	}, "\n"))
	if got := readFile(t, dir, studioDIDsPath); got != want {
		t.Errorf("written file differs from expected byte-diff.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestWriteDIDMeta_NewDIDAppendsToGoldenFixture(t *testing.T) {
	dir, golden := writeGoldenDIDsRepo(t)
	rf := RepoFiles{Root: dir}

	m := DIDMeta{Did: "+16135550111", Label: "Third line", DefaultRule: "no-access"}
	if err := rf.WriteDIDMeta(m); err != nil {
		t.Fatalf("WriteDIDMeta() error: %v", err)
	}

	want := golden + `  - did: "+16135550111"` + "\n" +
		`    label: "Third line"` + "\n" +
		`    default_rule: "no-access"` + "\n"
	if got := readFile(t, dir, studioDIDsPath); got != want {
		t.Errorf("written file differs from expected byte-diff.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestWriteDIDMeta_UpsertReplacesOnlyMatchingDIDRow(t *testing.T) {
	dir, golden := writeGoldenDIDsRepo(t)
	rf := RepoFiles{Root: dir}

	m := DIDMeta{
		Did:         "+16135550100",
		Label:       "Ottawa main line (updated)",
		Region:      "CA-ON",
		DefaultRule: "no-access",
		Greeting:    "New greeting text.",
	}
	if err := rf.WriteDIDMeta(m); err != nil {
		t.Fatalf("WriteDIDMeta() error: %v", err)
	}

	want := mustReplaceOnce(t, golden, strings.Join([]string{
		`  - did: "+16135550100"`,
		`    label: "Ottawa main line"`,
		`    region: "CA-ON"`,
		`    default_rule: "kph-tier-code"`,
		`    greeting: "Hey, thanks for calling the Ottawa line."`,
	}, "\n"), strings.Join([]string{
		`  - did: "+16135550100"`,
		`    label: "Ottawa main line (updated)"`,
		`    region: "CA-ON"`,
		`    default_rule: "no-access"`,
		`    greeting: "New greeting text."`,
	}, "\n"))

	got := readFile(t, dir, studioDIDsPath)
	if got != want {
		t.Errorf("written file differs from expected byte-diff.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	// Other row + header comment must survive untouched.
	if !strings.Contains(got, `- did: "+13475550199"`) {
		t.Errorf("upsert disturbed the unrelated NYC row:\n%s", got)
	}
	if !strings.Contains(got, "# Golden fixture for studio_files_test.go") {
		t.Errorf("upsert disturbed the file header comment:\n%s", got)
	}
}

func TestDIDStore_RoundTrip(t *testing.T) {
	dir, _ := writeGoldenDIDsRepo(t)
	rf := RepoFiles{Root: dir}

	m := DIDMeta{
		Did:         "+16135550111",
		Label:       "Round-trip line",
		Region:      "CA-ON",
		DefaultRule: "kph-tier-code",
		Greeting:    "Round-trip greeting.",
	}
	if err := rf.WriteDIDMeta(m); err != nil {
		t.Fatalf("WriteDIDMeta() error: %v", err)
	}

	got, err := rf.ReadDIDMeta()
	if err != nil {
		t.Fatalf("ReadDIDMeta() error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ReadDIDMeta() returned %d rows, want 3: %+v", len(got), got)
	}
	if got[2] != m {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got[2], m)
	}

	// Upsert the same DID again with different values and confirm the
	// round trip reflects the update, not a duplicate row.
	m.Greeting = "Updated round-trip greeting."
	if err := rf.WriteDIDMeta(m); err != nil {
		t.Fatalf("WriteDIDMeta() upsert error: %v", err)
	}
	got, err = rf.ReadDIDMeta()
	if err != nil {
		t.Fatalf("ReadDIDMeta() error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("upsert produced %d rows, want 3 (no duplicate): %+v", len(got), got)
	}
	if got[2] != m {
		t.Errorf("upsert round-trip mismatch: got %+v, want %+v", got[2], m)
	}
}

func TestWriteDIDMeta_RejectsControlCharacterInDID(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, studioDIDsPath, seededDIDsFile)
	rf := RepoFiles{Root: dir}

	err := rf.WriteDIDMeta(DIDMeta{Did: "+1613555\x000100"})
	if err == nil {
		t.Fatal("WriteDIDMeta() with a control character in did: want error, got nil")
	}
	if got := readFile(t, dir, studioDIDsPath); got != seededDIDsFile {
		t.Errorf("file was modified despite a validation error:\n%s", got)
	}
}

func TestWriteDIDMeta_RejectsControlCharacterInDefaultRule(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, studioDIDsPath, seededDIDsFile)
	rf := RepoFiles{Root: dir}

	err := rf.WriteDIDMeta(DIDMeta{Did: "+16135550100", DefaultRule: "bad\x00rule"})
	if err == nil {
		t.Fatal("WriteDIDMeta() with a control character in default_rule: want error, got nil")
	}
	if got := readFile(t, dir, studioDIDsPath); got != seededDIDsFile {
		t.Errorf("file was modified despite a validation error:\n%s", got)
	}
}

// --------------------------------------------------------------------------
// ReadRuleOrder

func TestReadRuleOrder_AbsentFileReturnsEmptySlice(t *testing.T) {
	dir := t.TempDir()
	rf := RepoFiles{Root: dir}

	got, err := rf.ReadRuleOrder()
	if err != nil {
		t.Fatalf("ReadRuleOrder() error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ReadRuleOrder() = %+v, want empty slice", got)
	}
}

func TestReadRuleOrder_EmptyFileReturnsEmptySlice(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ruleOrderPath, "")
	rf := RepoFiles{Root: dir}

	got, err := rf.ReadRuleOrder()
	if err != nil {
		t.Fatalf("ReadRuleOrder() error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ReadRuleOrder() = %+v, want empty slice", got)
	}
}

func TestReadRuleOrder_SeededFlowEmptyReturnsEmptySlice(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ruleOrderPath, seededRuleOrderFile)
	rf := RepoFiles{Root: dir}

	got, err := rf.ReadRuleOrder()
	if err != nil {
		t.Fatalf("ReadRuleOrder() error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ReadRuleOrder() = %+v, want empty slice", got)
	}
}

func TestReadRuleOrder_ParsesGoldenFixture(t *testing.T) {
	dir, _ := writeGoldenRuleOrderRepo(t)
	rf := RepoFiles{Root: dir}

	got, err := rf.ReadRuleOrder()
	if err != nil {
		t.Fatalf("ReadRuleOrder() error: %v", err)
	}
	want := []string{"kph-tier-code", "greenhouse-code", "no-access"}
	if len(got) != len(want) {
		t.Fatalf("ReadRuleOrder() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ReadRuleOrder()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// --------------------------------------------------------------------------
// WriteRuleOrder

func TestWriteRuleOrder_PersistsSequenceOverSeededEmptyFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ruleOrderPath, seededRuleOrderFile)
	rf := RepoFiles{Root: dir}

	if err := rf.WriteRuleOrder([]string{"kph-tier-code", "greenhouse-code"}); err != nil {
		t.Fatalf("WriteRuleOrder() error: %v", err)
	}

	want := mustReplaceOnce(t, seededRuleOrderFile, "order: []", strings.Join([]string{
		"order:",
		"  - kph-tier-code",
		"  - greenhouse-code",
	}, "\n"))
	if got := readFile(t, dir, ruleOrderPath); got != want {
		t.Errorf("written file differs from expected byte-diff.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestWriteRuleOrder_ReplacesExistingSequenceInGoldenFixture(t *testing.T) {
	dir, golden := writeGoldenRuleOrderRepo(t)
	rf := RepoFiles{Root: dir}

	if err := rf.WriteRuleOrder([]string{"no-access", "kph-tier-code"}); err != nil {
		t.Fatalf("WriteRuleOrder() error: %v", err)
	}

	want := mustReplaceOnce(t, golden, strings.Join([]string{
		"order:",
		"  - kph-tier-code",
		"  - greenhouse-code",
		"  - no-access",
	}, "\n"), strings.Join([]string{
		"order:",
		"  - no-access",
		"  - kph-tier-code",
	}, "\n"))
	got := readFile(t, dir, ruleOrderPath)
	if got != want {
		t.Errorf("written file differs from expected byte-diff.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if strings.Contains(got, "greenhouse-code") {
		t.Errorf("dropped code id greenhouse-code survived a full-replace write:\n%s", got)
	}
	if !strings.Contains(got, "# Golden fixture for studio_files_test.go") {
		t.Errorf("write disturbed the file header comment:\n%s", got)
	}
}

func TestRuleOrder_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ruleOrderPath, seededRuleOrderFile)
	rf := RepoFiles{Root: dir}

	seq := []string{"c-one", "c-two", "c-three"}
	if err := rf.WriteRuleOrder(seq); err != nil {
		t.Fatalf("WriteRuleOrder() error: %v", err)
	}
	got, err := rf.ReadRuleOrder()
	if err != nil {
		t.Fatalf("ReadRuleOrder() error: %v", err)
	}
	if len(got) != len(seq) {
		t.Fatalf("ReadRuleOrder() = %v, want %v", got, seq)
	}
	for i := range seq {
		if got[i] != seq[i] {
			t.Errorf("ReadRuleOrder()[%d] = %q, want %q", i, got[i], seq[i])
		}
	}

	// Re-order + reduce the sequence; the write is a full replace, so the
	// round trip must reflect exactly the new sequence, not a merge.
	seq2 := []string{"c-three", "c-one"}
	if err := rf.WriteRuleOrder(seq2); err != nil {
		t.Fatalf("WriteRuleOrder() reorder error: %v", err)
	}
	got, err = rf.ReadRuleOrder()
	if err != nil {
		t.Fatalf("ReadRuleOrder() error: %v", err)
	}
	if len(got) != len(seq2) {
		t.Fatalf("ReadRuleOrder() after reorder = %v, want %v", got, seq2)
	}
	for i := range seq2 {
		if got[i] != seq2[i] {
			t.Errorf("ReadRuleOrder()[%d] = %q, want %q", i, got[i], seq2[i])
		}
	}
}

func TestWriteRuleOrder_RejectsControlCharacter(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ruleOrderPath, seededRuleOrderFile)
	rf := RepoFiles{Root: dir}

	err := rf.WriteRuleOrder([]string{"kph-tier-code", "bad\x00id"})
	if err == nil {
		t.Fatal("WriteRuleOrder() with a control character: want error, got nil")
	}
	if got := readFile(t, dir, ruleOrderPath); got != seededRuleOrderFile {
		t.Errorf("file was modified despite a validation error:\n%s", got)
	}
}

// --------------------------------------------------------------------------
// Seed files this package ships with (apps/voice/configs/studio/*.yaml)
// parse cleanly through the real production path — a regression guard that
// the seed content stays in the flow-empty shape ReadDIDMeta/ReadRuleOrder
// and WriteDIDMeta/WriteRuleOrder expect.

func TestSeedFiles_RepoRootParsesCleanly(t *testing.T) {
	root, err := repoRootForTest()
	if err != nil {
		t.Skipf("could not resolve repo root: %v", err)
	}
	rf := RepoFiles{Root: root}

	dids, err := rf.ReadDIDMeta()
	if err != nil {
		t.Fatalf("ReadDIDMeta() on real seed file: %v", err)
	}
	if len(dids) != 0 {
		t.Errorf("real dids.yaml seed should ship with zero entries, got %+v", dids)
	}

	order, err := rf.ReadRuleOrder()
	if err != nil {
		t.Fatalf("ReadRuleOrder() on real seed file: %v", err)
	}
	if len(order) != 0 {
		t.Errorf("real rule-order.yaml seed should ship with zero entries, got %+v", order)
	}
}

// repoRootForTest resolves the klanker-voice repo root the same way
// cmd/knowledge.go's repoRoot() does (`git rev-parse --show-toplevel`), so
// this test works regardless of the working directory `go test` is invoked
// from.
func repoRootForTest() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
