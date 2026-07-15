package studio

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// --------------------------------------------------------------------------
// Golden-fixture harness

// writeGoldenTopicMapRepo copies testdata/topic-map.golden.yaml into a temp
// repo root at topic-map.yaml's real repo-relative path, returning the repo
// root and the golden fixture's own bytes (as a string) for use as the
// "before" side of a byte-diff assertion.
func writeGoldenTopicMapRepo(t *testing.T) (dir, golden string) {
	t.Helper()
	b, err := os.ReadFile("testdata/topic-map.golden.yaml")
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	dir = t.TempDir()
	full := filepath.Join(dir, topicMapPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, b, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return dir, string(b)
}

func readTopicMapFile(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, topicMapPath))
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	return string(b)
}

// writeGoldenManifestRepo is writeGoldenTopicMapRepo's manifest.yaml
// counterpart.
func writeGoldenManifestRepo(t *testing.T) (dir, golden string) {
	t.Helper()
	b, err := os.ReadFile("testdata/manifest.golden.yaml")
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	dir = t.TempDir()
	full := filepath.Join(dir, manifestPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, b, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return dir, string(b)
}

func readManifestFile(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, manifestPath))
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	return string(b)
}

// writeGoldenTelephonyRepo is writeGoldenTopicMapRepo's telephony.toml
// counterpart.
func writeGoldenTelephonyRepo(t *testing.T) (dir, golden string) {
	t.Helper()
	b, err := os.ReadFile("testdata/telephony.golden.toml")
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	dir = t.TempDir()
	full := filepath.Join(dir, telephonyConfigPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, b, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return dir, string(b)
}

func readTelephonyFile(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, telephonyConfigPath))
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	return string(b)
}

// mustReplaceOnce applies strings.Replace with n=1 and fails the test if
// old isn't found exactly once in s — guarding every hand-authored
// "expected output" fixture below against a stale/non-unique anchor.
func mustReplaceOnce(t *testing.T, s, old, new string) string {
	t.Helper()
	if strings.Count(s, old) != 1 {
		t.Fatalf("anchor %q does not appear exactly once in fixture (found %d)", old, strings.Count(s, old))
	}
	return strings.Replace(s, old, new, 1)
}

// --------------------------------------------------------------------------
// WriteTopicMapKeyword

func TestWriteTopicMapKeyword_AddNewTermAppendsAtEndOfList(t *testing.T) {
	dir, golden := writeGoldenTopicMapRepo(t)
	rf := RepoFiles{Root: dir}

	if err := rf.WriteTopicMapKeyword("klanker-maker", "km cli", 2, KeywordAdd); err != nil {
		t.Fatalf("WriteTopicMapKeyword() error: %v", err)
	}

	want := mustReplaceOnce(t, golden,
		"      - term: \"klanker\"\n        weight: 2\n\n  - id: tiogo",
		"      - term: \"klanker\"\n        weight: 2\n      - term: \"km cli\"\n        weight: 2\n\n  - id: tiogo",
	)
	if got := readTopicMapFile(t, dir); got != want {
		t.Errorf("written file differs from expected byte-diff.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestWriteTopicMapKeyword_AddNewTermWithoutWeightOmitsWeightLine(t *testing.T) {
	dir, golden := writeGoldenTopicMapRepo(t)
	rf := RepoFiles{Root: dir}

	if err := rf.WriteTopicMapKeyword("tiogo", "vuln scan", 0, KeywordAdd); err != nil {
		t.Fatalf("WriteTopicMapKeyword() error: %v", err)
	}

	want := mustReplaceOnce(t, golden,
		"      - term: \"tenable\"\n        weight: 3\n\n  # HIDDEN",
		"      - term: \"tenable\"\n        weight: 3\n      - term: \"vuln scan\"\n\n  # HIDDEN",
	)
	if got := readTopicMapFile(t, dir); got != want {
		t.Errorf("written file differs from expected byte-diff.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestWriteTopicMapKeyword_RemoveDeletesInlineCommentTermAndWeightLine(t *testing.T) {
	dir, golden := writeGoldenTopicMapRepo(t)
	rf := RepoFiles{Root: dir}

	if err := rf.WriteTopicMapKeyword("klanker-maker", "clanker maker", 0, KeywordRemove); err != nil {
		t.Fatalf("WriteTopicMapKeyword() error: %v", err)
	}

	want := mustReplaceOnce(t, golden,
		"      - term: \"klanker maker\"\n        weight: 3\n      - term: \"clanker maker\"        # common ASR mis-hearing of \"klanker\"\n        weight: 3\n      - term: \"klanker\"",
		"      - term: \"klanker maker\"\n        weight: 3\n      - term: \"klanker\"",
	)
	if got := readTopicMapFile(t, dir); got != want {
		t.Errorf("written file differs from expected byte-diff.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestWriteTopicMapKeyword_AddOnExistingTermReplacesWeightPreservingInlineComment(t *testing.T) {
	dir, golden := writeGoldenTopicMapRepo(t)
	rf := RepoFiles{Root: dir}

	// "clanker maker" has an inline `# common ASR mis-hearing...` comment on
	// its term line -- replacing its weight must leave that comment (and
	// the term line generally) untouched.
	if err := rf.WriteTopicMapKeyword("klanker-maker", "clanker maker", 5, KeywordAdd); err != nil {
		t.Fatalf("WriteTopicMapKeyword() error: %v", err)
	}

	want := mustReplaceOnce(t, golden,
		"      - term: \"clanker maker\"        # common ASR mis-hearing of \"klanker\"\n        weight: 3",
		"      - term: \"clanker maker\"        # common ASR mis-hearing of \"klanker\"\n        weight: 5",
	)
	if got := readTopicMapFile(t, dir); got != want {
		t.Errorf("written file differs from expected byte-diff.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestWriteTopicMapKeyword_HookBlockScalarAndHiddenTopicSurviveUnrelatedEdit(t *testing.T) {
	dir, golden := writeGoldenTopicMapRepo(t)
	rf := RepoFiles{Root: dir}

	if err := rf.WriteTopicMapKeyword("tiogo", "nessus scan", 1, KeywordAdd); err != nil {
		t.Fatalf("WriteTopicMapKeyword() error: %v", err)
	}
	got := readTopicMapFile(t, dir)

	for _, unchanged := range []string{
		"    hook: >-\n      Kurt's AI-agent runtime -- a Go CLI called km that turns a YAML file\n      into a locked-down AWS sandbox, with kernel-level network filtering.",
		"  - id: greenhouse\n    spoken_name: \"Kurt's background\"\n    hidden: true\n    sticky: true\n    hook: >-\n      (hidden) Kurt's professional background -- recruiting mode.\n    keywords:\n      - term: \"greenhouse\"\n        weight: 3\n",
	} {
		if !strings.Contains(got, unchanged) {
			t.Errorf("expected unrelated block to survive byte-for-byte, missing:\n%s\n\ngot file:\n%s", unchanged, got)
		}
	}
	if strings.Count(golden, "hook: >-") != strings.Count(got, "hook: >-") {
		t.Errorf("hook: >- block-scalar count changed: golden=%d got=%d", strings.Count(golden, "hook: >-"), strings.Count(got, "hook: >-"))
	}
}

func TestWriteTopicMapKeyword_RejectsControlCharBeforeAnyWrite(t *testing.T) {
	dir, golden := writeGoldenTopicMapRepo(t)
	rf := RepoFiles{Root: dir}

	err := rf.WriteTopicMapKeyword("klanker-maker", "bad\x00term", 1, KeywordAdd)
	if err == nil {
		t.Fatal("WriteTopicMapKeyword() error = nil, want error for a control-char term")
	}
	if got := readTopicMapFile(t, dir); got != golden {
		t.Error("file was modified despite a rejected control-char term")
	}
}

func TestWriteTopicMapKeyword_RejectsEmbeddedDoubleQuoteBeforeAnyWrite(t *testing.T) {
	dir, golden := writeGoldenTopicMapRepo(t)
	rf := RepoFiles{Root: dir}

	err := rf.WriteTopicMapKeyword("klanker-maker", `bad "quoted" term`, 1, KeywordAdd)
	if err == nil {
		t.Fatal("WriteTopicMapKeyword() error = nil, want error for a term containing a double-quote")
	}
	if got := readTopicMapFile(t, dir); got != golden {
		t.Error("file was modified despite a rejected embedded-quote term")
	}
}

func TestWriteTopicMapKeyword_RemoveUnknownTermReturnsErrorAndWritesNothing(t *testing.T) {
	dir, golden := writeGoldenTopicMapRepo(t)
	rf := RepoFiles{Root: dir}

	err := rf.WriteTopicMapKeyword("klanker-maker", "not a real term", 0, KeywordRemove)
	if err == nil {
		t.Fatal("WriteTopicMapKeyword() error = nil, want error for an unknown term")
	}
	if got := readTopicMapFile(t, dir); got != golden {
		t.Error("file was modified despite a remove of an unknown term")
	}
}

func TestWriteTopicMapKeyword_UnknownTopicReturnsErrorAndWritesNothing(t *testing.T) {
	dir, golden := writeGoldenTopicMapRepo(t)
	rf := RepoFiles{Root: dir}

	err := rf.WriteTopicMapKeyword("not-a-real-topic", "whatever", 1, KeywordAdd)
	if err == nil {
		t.Fatal("WriteTopicMapKeyword() error = nil, want error for an unknown topic id")
	}
	if got := readTopicMapFile(t, dir); got != golden {
		t.Error("file was modified despite an unknown topic id")
	}
}

func TestWriteTopicMapKeyword_ReaderRoundTripsWrittenFile(t *testing.T) {
	dir, _ := writeGoldenTopicMapRepo(t)
	rf := RepoFiles{Root: dir}

	if err := rf.WriteTopicMapKeyword("klanker-maker", "km cli", 2, KeywordAdd); err != nil {
		t.Fatalf("WriteTopicMapKeyword() error: %v", err)
	}
	if err := rf.WriteTopicMapKeyword("klanker-maker", "clanker maker", 0, KeywordRemove); err != nil {
		t.Fatalf("WriteTopicMapKeyword() error: %v", err)
	}

	unlocks, err := rf.ReadTopicMap()
	if err != nil {
		t.Fatalf("ReadTopicMap() error: %v", err)
	}

	var phrases []string
	for _, u := range unlocks {
		phrases = append(phrases, u.Phrase)
	}
	wantPresent := []string{"klanker maker", "klanker", "km cli", "tiogo", "tenable", "greenhouse"}
	for _, w := range wantPresent {
		if !slices.Contains(phrases, w) {
			t.Errorf("ReadTopicMap() after write missing expected phrase %q; got %v", w, phrases)
		}
	}
	for _, p := range phrases {
		if p == "clanker maker" {
			t.Errorf("ReadTopicMap() after write still contains removed phrase %q; got %v", p, phrases)
		}
	}
}

// --------------------------------------------------------------------------
// WriteTelephonyGate

func TestWriteTelephonyGate_ModeSwitchPreservesNeighborsAndComments(t *testing.T) {
	dir, golden := writeGoldenTelephonyRepo(t)
	rf := RepoFiles{Root: dir}

	if err := rf.WriteTelephonyGate("passphrase", true); err != nil {
		t.Fatalf("WriteTelephonyGate() error: %v", err)
	}

	want := mustReplaceOnce(t, golden,
		`gate_mode = "either"                 # "dtmf" | "passphrase" | "either" (D-05b: both factors, either unlocks)`,
		`gate_mode = "passphrase"                 # "dtmf" | "passphrase" | "either" (D-05b: both factors, either unlocks)`,
	)
	if got := readTelephonyFile(t, dir); got != want {
		t.Errorf("written file differs from expected byte-diff.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestWriteTelephonyGate_NoSecretSetsRequireGateFalseWithoutTouchingGateMode(t *testing.T) {
	dir, golden := writeGoldenTelephonyRepo(t)
	rf := RepoFiles{Root: dir}

	if err := rf.WriteTelephonyGate("", false); err != nil {
		t.Fatalf("WriteTelephonyGate() error: %v", err)
	}

	want := mustReplaceOnce(t, golden,
		`require_gate = true                  # master switch; false is a test/dev-only escape hatch`,
		`require_gate = false                  # master switch; false is a test/dev-only escape hatch`,
	)
	got := readTelephonyFile(t, dir)
	if got != want {
		t.Errorf("written file differs from expected byte-diff.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if strings.Contains(got, `gate_mode = "none"`) {
		t.Error(`written file contains gate_mode = "none" -- the off-mode must NEVER be written (16-RESEARCH.md Pitfall 2)`)
	}
}

func TestWriteTelephonyGate_RejectsInvalidModeAndWritesNothing(t *testing.T) {
	dir, golden := writeGoldenTelephonyRepo(t)
	rf := RepoFiles{Root: dir}

	err := rf.WriteTelephonyGate("none", true)
	if err == nil {
		t.Fatal(`WriteTelephonyGate("none", true) error = nil, want a rejection -- "none" is not a valid gate_mode`)
	}
	if got := readTelephonyFile(t, dir); got != golden {
		t.Error("file was modified despite a rejected invalid gate_mode")
	}
}

func TestWriteTelephonyGate_RejectsArbitraryInvalidMode(t *testing.T) {
	dir, golden := writeGoldenTelephonyRepo(t)
	rf := RepoFiles{Root: dir}

	err := rf.WriteTelephonyGate("smoke-signal", true)
	if err == nil {
		t.Fatal(`WriteTelephonyGate("smoke-signal", true) error = nil, want a rejection`)
	}
	if got := readTelephonyFile(t, dir); got != golden {
		t.Error("file was modified despite a rejected invalid gate_mode")
	}
}

// fixtureTelephonyMissingGateKeys is a [telephony] block that has neither
// gate_mode nor require_gate yet, exercising the "insert inside the block"
// path (never at file top).
const fixtureTelephonyMissingGateKeys = `label = "KPH(test)"

[telephony]
enabled = true
[extra]
foo = "bar"
`

func TestWriteTelephonyGate_InsertsMissingKeysInsideBlock(t *testing.T) {
	dir := t.TempDir()
	full := filepath.Join(dir, telephonyConfigPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(fixtureTelephonyMissingGateKeys), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	rf := RepoFiles{Root: dir}
	if err := rf.WriteTelephonyGate("dtmf", true); err != nil {
		t.Fatalf("WriteTelephonyGate() error: %v", err)
	}

	want := `label = "KPH(test)"

[telephony]
enabled = true
gate_mode = "dtmf"
require_gate = true
[extra]
foo = "bar"
`
	if got := readTelephonyFile(t, dir); got != want {
		t.Errorf("written file differs from expected.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestWriteTelephonyGate_ReaderRoundTripsWrittenFile(t *testing.T) {
	dir, _ := writeGoldenTelephonyRepo(t)
	rf := RepoFiles{Root: dir}

	if err := rf.WriteTelephonyGate("passphrase", true); err != nil {
		t.Fatalf("WriteTelephonyGate() error: %v", err)
	}

	got, err := rf.ReadTelephonyGate()
	if err != nil {
		t.Fatalf("ReadTelephonyGate() error: %v", err)
	}
	if got != "passphrase" {
		t.Errorf("ReadTelephonyGate() after write = %q, want %q", got, "passphrase")
	}
}

// --------------------------------------------------------------------------
// WriteManifestSource (KNOW-02)

func TestWriteManifestSource_AppendsAtEndOfTopicSourcesList(t *testing.T) {
	dir, golden := writeGoldenManifestRepo(t)
	rf := RepoFiles{Root: dir}

	if err := rf.WriteManifestSource("klanker-maker", "apps/voice/knowledge/corpus/km-extra.md", "docs"); err != nil {
		t.Fatalf("WriteManifestSource() error: %v", err)
	}

	want := mustReplaceOnce(t, golden,
		"        public: true\n        note: km sandbox AWS architecture, ingested as text (Amendment 3-D).\n\n  - id: meshtk",
		"        public: true\n        note: km sandbox AWS architecture, ingested as text (Amendment 3-D).\n      - path: apps/voice/knowledge/corpus/km-extra.md\n        kind: docs\n        public: true\n\n  - id: meshtk",
	)
	if got := readManifestFile(t, dir); got != want {
		t.Errorf("written file differs from expected byte-diff.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestWriteManifestSource_LastTopicInFileAppendsBeforeTrailingComment(t *testing.T) {
	dir, golden := writeGoldenManifestRepo(t)
	rf := RepoFiles{Root: dir}

	if err := rf.WriteManifestSource("greenhouse", "apps/voice/knowledge/corpus/greenhouse-extra.md", "transcript"); err != nil {
		t.Fatalf("WriteManifestSource() error: %v", err)
	}

	want := mustReplaceOnce(t, golden,
		"      - path: apps/voice/knowledge/corpus/kurt-resume.md\n        kind: docs\n        public: true\n\n# Any further topics",
		"      - path: apps/voice/knowledge/corpus/kurt-resume.md\n        kind: docs\n        public: true\n      - path: apps/voice/knowledge/corpus/greenhouse-extra.md\n        kind: transcript\n        public: true\n\n# Any further topics",
	)
	if got := readManifestFile(t, dir); got != want {
		t.Errorf("written file differs from expected byte-diff.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestWriteManifestSource_PreservesCommentsBlockScalarsAndOrdering asserts
// every OTHER topic, comment, note: >- block scalar, and tour_priority
// ordering survives an unrelated edit byte-for-byte.
func TestWriteManifestSource_PreservesCommentsBlockScalarsAndOrdering(t *testing.T) {
	dir, golden := writeGoldenManifestRepo(t)
	rf := RepoFiles{Root: dir}

	if err := rf.WriteManifestSource("meshtk", "apps/voice/knowledge/corpus/meshtk-extra.md", "code"); err != nil {
		t.Fatalf("WriteManifestSource() error: %v", err)
	}
	got := readManifestFile(t, dir)

	for _, unchanged := range []string{
		"tour_priority:\n  - klanker-maker\n  - meshtk\n",
		"note: >-\n          km's own docs/ tree (~1,950 md files repo-wide); primary,\n          high-signal source for the deep pack (Amendment 5).",
		"  # HIDDEN easter-egg (2026-07-10, SCAFFOLD): last topic in the file --\n  # exercises appending immediately before the trailing file comment.\n  - id: greenhouse",
		"# Any further topics: append the same shape here",
	} {
		if !strings.Contains(got, unchanged) {
			t.Errorf("expected unrelated block to survive byte-for-byte, missing:\n%s\n\ngot file:\n%s", unchanged, got)
		}
	}
	if strings.Count(golden, "note: >-") != strings.Count(got, "note: >-") {
		t.Errorf("note: >- block-scalar count changed: golden=%d got=%d", strings.Count(golden, "note: >-"), strings.Count(got, "note: >-"))
	}
	if strings.Count(golden, "  - id: ") != strings.Count(got, "  - id: ") {
		t.Errorf("topic count changed: golden=%d got=%d", strings.Count(golden, "  - id: "), strings.Count(got, "  - id: "))
	}
}

func TestWriteManifestSource_RejectsInvalidKindBeforeAnyWrite(t *testing.T) {
	dir, golden := writeGoldenManifestRepo(t)
	rf := RepoFiles{Root: dir}

	err := rf.WriteManifestSource("klanker-maker", "apps/voice/knowledge/corpus/extra.md", "audio")
	if err == nil {
		t.Fatal("WriteManifestSource() error = nil, want a rejection for kind \"audio\" (not in the enum)")
	}
	if got := readManifestFile(t, dir); got != golden {
		t.Error("file was modified despite a rejected invalid kind")
	}
}

func TestWriteManifestSource_UnknownTopicReturnsErrorAndWritesNothing(t *testing.T) {
	dir, golden := writeGoldenManifestRepo(t)
	rf := RepoFiles{Root: dir}

	err := rf.WriteManifestSource("not-a-real-topic", "apps/voice/knowledge/corpus/extra.md", "docs")
	if err == nil {
		t.Fatal("WriteManifestSource() error = nil, want an error for an unknown topic id")
	}
	if got := readManifestFile(t, dir); got != golden {
		t.Error("file was modified despite an unknown topic id")
	}
}

func TestWriteManifestSource_RejectsBlankOrControlCharPathBeforeAnyWrite(t *testing.T) {
	dir, golden := writeGoldenManifestRepo(t)
	rf := RepoFiles{Root: dir}

	for _, badPath := range []string{"", "   ", "bad\x00path"} {
		err := rf.WriteManifestSource("klanker-maker", badPath, "docs")
		if err == nil {
			t.Errorf("WriteManifestSource(path=%q) error = nil, want a rejection", badPath)
		}
	}
	if got := readManifestFile(t, dir); got != golden {
		t.Error("file was modified despite a rejected blank/control-char path")
	}
}

// TestWriteManifestSource_AllowsAbsolutePath asserts an absolute path (a
// sibling local checkout, the existing manifest.yaml convention) is NOT
// rejected — path shape is not the safety boundary, public:true is
// (17-RESEARCH.md Security Domain).
func TestWriteManifestSource_AllowsAbsolutePath(t *testing.T) {
	dir, _ := writeGoldenManifestRepo(t)
	rf := RepoFiles{Root: dir}

	if err := rf.WriteManifestSource("meshtk", "/Users/khundeck/working/meshtk/cmd", "code"); err != nil {
		t.Fatalf("WriteManifestSource() with an absolute path error: %v, want no rejection", err)
	}
	if got := readManifestFile(t, dir); !strings.Contains(got, "- path: /Users/khundeck/working/meshtk/cmd\n") {
		t.Errorf("written file missing the absolute-path source; got:\n%s", got)
	}
}

// TestWriteManifestSource_AlwaysWritesPublicTrue asserts public:true is
// hardcoded on every write — the function signature has no public
// parameter, so this is really a round-trip-shape assertion, not a "what if
// the caller passed false" test (there is no such parameter to pass).
func TestWriteManifestSource_AlwaysWritesPublicTrue(t *testing.T) {
	dir, _ := writeGoldenManifestRepo(t)
	rf := RepoFiles{Root: dir}

	if err := rf.WriteManifestSource("klanker-maker", "apps/voice/knowledge/corpus/extra.md", "docs"); err != nil {
		t.Fatalf("WriteManifestSource() error: %v", err)
	}
	got := readManifestFile(t, dir)
	if !strings.Contains(got, "- path: apps/voice/knowledge/corpus/extra.md\n        kind: docs\n        public: true") {
		t.Errorf("written source block missing the hardcoded public: true field; got:\n%s", got)
	}
}

// TestWriteManifestSource_ReaderRoundTripsWrittenFile asserts
// RepoFiles.ReadManifest sees the newly appended source after a write.
func TestWriteManifestSource_ReaderRoundTripsWrittenFile(t *testing.T) {
	dir, _ := writeGoldenManifestRepo(t)
	rf := RepoFiles{Root: dir}

	if err := rf.WriteManifestSource("meshtk", "apps/voice/knowledge/corpus/meshtk-extra.md", "code"); err != nil {
		t.Fatalf("WriteManifestSource() error: %v", err)
	}

	packs, err := rf.ReadManifest()
	if err != nil {
		t.Fatalf("ReadManifest() error: %v", err)
	}
	var meshtk *KnowledgePack
	for i := range packs {
		if packs[i].ID == "meshtk" {
			meshtk = &packs[i]
		}
	}
	if meshtk == nil {
		t.Fatal("ReadManifest() after write missing topic \"meshtk\"")
	}
	found := false
	for _, s := range meshtk.Sources {
		if s.Path == "apps/voice/knowledge/corpus/meshtk-extra.md" && s.Kind == "code" && s.Public {
			found = true
		}
	}
	if !found {
		t.Errorf("meshtk.Sources after write = %+v, want the newly appended source", meshtk.Sources)
	}
}
