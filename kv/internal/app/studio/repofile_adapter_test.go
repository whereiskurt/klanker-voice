package studio

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const fixtureManifestYAML = `# fixture manifest
version: 1

tour_priority:
  - klanker-maker
  - meshtk

topics:
  - id: klanker-maker
    spoken_name: "klanker-maker"
    pack: klanker-maker.md
    sources:
      - path: /Users/khundeck/working/klankrmkr/docs
        kind: docs
        public: true
        skip_if_missing: true
        note: >-
          km's own docs/ tree, primary source.
      - path: apps/voice/knowledge/diagrams/km-sandbox-aws.md
        kind: diagram
        public: true

  - id: greenhouse
    spoken_name: "Kurt's background"
    pack: greenhouse.md
    sources:
      - path: apps/voice/knowledge/corpus/kurt-resume.md
        kind: docs
        public: false
        skip_if_missing: true
        note: >-
          SEED only, not public.
`

const fixtureTopicMapYAML = `version: 1
confidence_floor: 2

topics:
  - id: klanker-maker
    spoken_name: "klanker-maker"
    hook: >-
      Kurt's AI-agent runtime, a multi-line
      spoken hook that must be skipped cleanly.
    keywords:
      - term: "klanker maker"
        weight: 3
      - term: "klanker"
        weight: 2

  - id: greenhouse
    spoken_name: "Kurt's background"
    hidden: true
    sticky: true
    exit:
      - "interview over"
      - "interview s over"
    hook: >-
      (hidden) recruiting mode.
    keywords:
      - term: "greenhouse"
        weight: 3
`

const fixtureTelephonyTOML = `label = "KPH(telephony-harness)"

[stt]
provider = "deepgram-nova3"

[telephony]                         # some inline comment
enabled = true
provider = "voipms"
gate_mode = "either"                 # "dtmf" | "passphrase" | "either"
require_gate = true

[quota]
heartbeat_renew_interval = 15
`

// writeFixtureRepo builds a temp repo root with the three config files at
// their real repo-relative paths, so RepoFiles{Root: dir} reads them exactly
// as it would in the real repo.
func writeFixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write(manifestPath, fixtureManifestYAML)
	write(topicMapPath, fixtureTopicMapYAML)
	write(telephonyConfigPath, fixtureTelephonyTOML)
	return dir
}

// --------------------------------------------------------------------------
// ReadManifest

func TestReadManifest_ParsesTopicsSourcesAndTalkable(t *testing.T) {
	rf := RepoFiles{Root: writeFixtureRepo(t)}
	got, err := rf.ReadManifest()
	if err != nil {
		t.Fatalf("ReadManifest() error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}

	km := got[0]
	if km.ID != "klanker-maker" || km.SpokenName != "klanker-maker" || km.Pack != "klanker-maker.md" {
		t.Errorf("got[0] = %+v, want id/spoken_name/pack from klanker-maker fixture", km)
	}
	if len(km.Sources) != 2 {
		t.Fatalf("len(got[0].Sources) = %d, want 2", len(km.Sources))
	}
	if km.Sources[0].Path != "/Users/khundeck/working/klankrmkr/docs" || km.Sources[0].Kind != "docs" || !km.Sources[0].Public {
		t.Errorf("got[0].Sources[0] = %+v, want the docs source (public=true)", km.Sources[0])
	}
	if km.Sources[1].Path != "apps/voice/knowledge/diagrams/km-sandbox-aws.md" || km.Sources[1].Kind != "diagram" {
		t.Errorf("got[0].Sources[1] = %+v, want the diagram source", km.Sources[1])
	}
	if !km.Talkable {
		t.Error("got[0].Talkable = false, want true (all sources public:true)")
	}

	gh := got[1]
	if gh.ID != "greenhouse" {
		t.Errorf("got[1].ID = %q, want %q", gh.ID, "greenhouse")
	}
	if gh.Talkable {
		t.Error("got[1].Talkable = true, want false (its only source has public:false)")
	}
}

func TestReadManifest_MissingFileReturnsTypedError(t *testing.T) {
	rf := RepoFiles{Root: t.TempDir()}
	_, err := rf.ReadManifest()
	if err == nil {
		t.Fatal("ReadManifest() error = nil, want a typed error for a missing file")
	}
	if _, ok := errors.AsType[*RepoFileError](err); !ok {
		t.Fatalf("error is not a *RepoFileError: %v (%T)", err, err)
	}
}

// --------------------------------------------------------------------------
// ReadTopicMap

func TestReadTopicMap_ParsesKeywordsIntoUnlocks(t *testing.T) {
	rf := RepoFiles{Root: writeFixtureRepo(t)}
	got, err := rf.ReadTopicMap()
	if err != nil {
		t.Fatalf("ReadTopicMap() error: %v", err)
	}
	want := []Unlock{
		{Phrase: "klanker maker", Add: []string{"klanker-maker"}},
		{Phrase: "klanker", Add: []string{"klanker-maker"}},
		{Phrase: "greenhouse", Add: []string{"greenhouse"}},
	}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Phrase != w.Phrase || len(got[i].Add) != 1 || got[i].Add[0] != w.Add[0] {
			t.Errorf("got[%d] = %+v, want %+v", i, got[i], w)
		}
	}
}

func TestReadTopicMap_MissingFileReturnsTypedError(t *testing.T) {
	rf := RepoFiles{Root: t.TempDir()}
	_, err := rf.ReadTopicMap()
	if _, ok := errors.AsType[*RepoFileError](err); !ok {
		t.Fatalf("error is not a *RepoFileError: %v (%T)", err, err)
	}
}

// fixtureTopicMapWithInlineComments reproduces the exact live shape found in
// apps/voice/knowledge/router/topic-map.yaml (lines 36 and 82): a quoted
// `- term: "..."` value followed by a trailing inline `# comment`, plus a
// quoted value that legitimately contains a `#` character inside the quotes
// (must survive untouched) and a single-quoted variant.
const fixtureTopicMapWithInlineComments = `version: 1
confidence_floor: 2

topics:
  - id: klanker-maker
    spoken_name: "klanker-maker"
    keywords:
      - term: "clanker maker"        # common ASR mis-hearing of "klanker"
        weight: 3
      - term: "thirty four"          # spoken "thirty-four" edition name
        weight: 1
      - term: "rate is #1 this week"
        weight: 1
      - term: 'single quoted term'   # trailing comment on a single-quoted value
        weight: 1
`

func TestReadTopicMap_StripsInlineCommentFromQuotedScalar(t *testing.T) {
	dir := t.TempDir()
	full := filepath.Join(dir, topicMapPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(fixtureTopicMapWithInlineComments), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	rf := RepoFiles{Root: dir}
	got, err := rf.ReadTopicMap()
	if err != nil {
		t.Fatalf("ReadTopicMap() error: %v", err)
	}

	want := []string{
		"clanker maker",
		"thirty four",
		"rate is #1 this week",
		"single quoted term",
	}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Phrase != w {
			t.Errorf("got[%d].Phrase = %q, want %q", i, got[i].Phrase, w)
		}
	}
}

// --------------------------------------------------------------------------
// ReadTelephonyGate

func TestReadTelephonyGate_ReturnsGateMode(t *testing.T) {
	rf := RepoFiles{Root: writeFixtureRepo(t)}
	got, err := rf.ReadTelephonyGate()
	if err != nil {
		t.Fatalf("ReadTelephonyGate() error: %v", err)
	}
	if got != "either" {
		t.Errorf("ReadTelephonyGate() = %q, want %q", got, "either")
	}
}

func TestReadTelephonyGate_MissingFileReturnsTypedError(t *testing.T) {
	rf := RepoFiles{Root: t.TempDir()}
	_, err := rf.ReadTelephonyGate()
	if _, ok := errors.AsType[*RepoFileError](err); !ok {
		t.Fatalf("error is not a *RepoFileError: %v (%T)", err, err)
	}
}

// --------------------------------------------------------------------------
// yamlScalar

func TestYamlScalar(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "double-quoted with trailing inline comment (live topic-map.yaml shape)",
			in:   ` "clanker maker"        # common ASR mis-hearing of "klanker"`,
			want: "clanker maker",
		},
		{
			name: "double-quoted value containing a literal # (preserved)",
			in:   ` "rate is #1 this week"`,
			want: "rate is #1 this week",
		},
		{
			name: "double-quoted value with # inside quotes AND a trailing comment",
			in:   ` "rate is #1 this week"   # note`,
			want: "rate is #1 this week",
		},
		{
			name: "single-quoted with trailing inline comment",
			in:   ` 'single quoted term'   # trailing comment`,
			want: "single quoted term",
		},
		{
			name: "single-quoted value containing a literal # (preserved)",
			in:   ` 'rate is #1 this week'`,
			want: "rate is #1 this week",
		},
		{
			name: "unquoted with trailing inline comment",
			in:   ` either                 # "dtmf" | "passphrase" | "either"`,
			want: "either",
		},
		{
			name: "unquoted plain value, no comment",
			in:   ` klanker-maker`,
			want: "klanker-maker",
		},
		{
			name: "quoted value with internal padding preserved",
			in:   ` " km "`,
			want: " km ",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := yamlScalar(c.in); got != c.want {
				t.Errorf("yamlScalar(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
