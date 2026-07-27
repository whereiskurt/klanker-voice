package studio

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
// ParseTelephonyGames / RepoFiles.ReadTelephonyGames / AnnotateGameEnv
// (quick task 260727-pdh)

const fixtureGamesTOML = `[telephony]
enabled = true

# Game 1: numeric only, no spoken trigger.
[[telephony.announcement]]
dids = ["7254043234"]
otp_url = "https://auth.klankermaker.ai/use1/ctf/otp"
otp_env_var = "CTF_OTP_AUTH_TOKEN"
code_env_var = "CTF_ANNOUNCEMENT_CODE_3234"
line_template = "Hey! {code}"
sms_dids = []
sms_reply_dids = ["7254043234"]
sms_relay_url = "https://auth.klankermaker.ai/use1/ctf/sms"

# Game 2: either-factor, plus a commented-out example dids line (mirrors the
# shipped telephony.toml's historical "# dids = [...]" precedent) that MUST
# be ignored -- this entry has no LIVE dids line, so it stays GLOBAL.
[[telephony.announcement]]
# dids = ["9999999999"]
otp_url = "https://auth.klankermaker.ai/use1/ctf/otp"
code_env_var = "CTF_ANNOUNCEMENT_CODE_UCTF"
words_env_var = "CTF_ANNOUNCEMENT_WORDS_UCTF"
line_template = "Hey! {code}"
sms_reply_dids = ["7254048283"]
`

func writeFixtureGamesTOML(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "telephony.toml")
	if err := os.WriteFile(path, []byte(fixtureGamesTOML), 0o600); err != nil {
		t.Fatalf("write fixture telephony.toml: %v", err)
	}
	return path
}

func TestParseTelephonyGames_OneEntryPerBlock(t *testing.T) {
	got, err := ParseTelephonyGames(writeFixtureGamesTOML(t))
	if err != nil {
		t.Fatalf("ParseTelephonyGames() error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2: %+v", len(got), got)
	}

	g1 := got[0]
	if len(g1.DIDs) != 1 || g1.DIDs[0] != "7254043234" {
		t.Errorf("got[0].DIDs = %+v, want [7254043234]", g1.DIDs)
	}
	if g1.CodeEnvVar != "CTF_ANNOUNCEMENT_CODE_3234" {
		t.Errorf("got[0].CodeEnvVar = %q, want CTF_ANNOUNCEMENT_CODE_3234", g1.CodeEnvVar)
	}
	if len(g1.SmsReplyDIDs) != 1 || g1.SmsReplyDIDs[0] != "7254043234" {
		t.Errorf("got[0].SmsReplyDIDs = %+v, want [7254043234]", g1.SmsReplyDIDs)
	}
	// No words_env_var line -> empty NAME, no spoken trigger.
	if g1.WordsEnvVar != "" {
		t.Errorf("got[0].WordsEnvVar = %q, want empty (no spoken trigger)", g1.WordsEnvVar)
	}

	g2 := got[1]
	if g2.CodeEnvVar != "CTF_ANNOUNCEMENT_CODE_UCTF" {
		t.Errorf("got[1].CodeEnvVar = %q, want CTF_ANNOUNCEMENT_CODE_UCTF", g2.CodeEnvVar)
	}
	if g2.WordsEnvVar != "CTF_ANNOUNCEMENT_WORDS_UCTF" {
		t.Errorf("got[1].WordsEnvVar = %q, want CTF_ANNOUNCEMENT_WORDS_UCTF", g2.WordsEnvVar)
	}
	if len(g2.SmsReplyDIDs) != 1 || g2.SmsReplyDIDs[0] != "7254048283" {
		t.Errorf("got[1].SmsReplyDIDs = %+v, want [7254048283]", g2.SmsReplyDIDs)
	}
}

func TestParseTelephonyGames_CommentedDidsLineIgnored(t *testing.T) {
	got, err := ParseTelephonyGames(writeFixtureGamesTOML(t))
	if err != nil {
		t.Fatalf("ParseTelephonyGames() error: %v", err)
	}
	// The second block's ONLY dids line is commented out -- must NOT be
	// parsed as live config, so the entry stays GLOBAL (empty DIDs), never
	// the commented value.
	if len(got) < 2 {
		t.Fatalf("len(got) = %d, want >= 2", len(got))
	}
	if len(got[1].DIDs) != 0 {
		t.Errorf("got[1].DIDs = %+v, want empty (commented dids line must be ignored)", got[1].DIDs)
	}
}

func TestParseTelephonyGames_MissingFileReturnsTypedError(t *testing.T) {
	_, err := ParseTelephonyGames(filepath.Join(t.TempDir(), "telephony.toml"))
	if _, ok := errors.AsType[*RepoFileError](err); !ok {
		t.Fatalf("error is not a *RepoFileError: %v (%T)", err, err)
	}
}

func TestRepoFiles_ReadTelephonyGames_MissingFileReturnsTypedError(t *testing.T) {
	rf := RepoFiles{Root: t.TempDir()}
	_, err := rf.ReadTelephonyGames()
	if _, ok := errors.AsType[*RepoFileError](err); !ok {
		t.Fatalf("error is not a *RepoFileError: %v (%T)", err, err)
	}
}

func TestAnnotateGameEnv_StatusRules(t *testing.T) {
	t.Setenv("GAME_CODE_SET", "123456")
	t.Setenv("GAME_WORDS_SENTINEL", "__unset__")
	t.Setenv("GAME_WORDS_MIXED_CASE_SENTINEL", "__UNSET__")
	t.Setenv("GAME_WORDS_EMPTY", "   ")
	// GAME_CODE_ABSENT deliberately not set.

	games := []GameEntry{
		{CodeEnvVar: "GAME_CODE_SET", WordsEnvVar: "GAME_WORDS_SENTINEL"},
		{CodeEnvVar: "GAME_CODE_ABSENT", WordsEnvVar: "GAME_WORDS_EMPTY"},
		{CodeEnvVar: "GAME_CODE_SET", WordsEnvVar: "GAME_WORDS_MIXED_CASE_SENTINEL"},
		{CodeEnvVar: "GAME_CODE_ABSENT"}, // no words_env_var at all -> ""
	}

	got := AnnotateGameEnv(games)

	if got[0].CodeStatus != "set" {
		t.Errorf("got[0].CodeStatus = %q, want %q (present + non-empty)", got[0].CodeStatus, "set")
	}
	if got[0].WordsStatus != "not set" {
		t.Errorf("got[0].WordsStatus = %q, want %q (sentinel)", got[0].WordsStatus, "not set")
	}
	if got[1].CodeStatus != "not set" {
		t.Errorf("got[1].CodeStatus = %q, want %q (absent from environment)", got[1].CodeStatus, "not set")
	}
	if got[1].WordsStatus != "not set" {
		t.Errorf("got[1].WordsStatus = %q, want %q (empty/whitespace-only)", got[1].WordsStatus, "not set")
	}
	if got[2].WordsStatus != "not set" {
		t.Errorf("got[2].WordsStatus = %q, want %q (sentinel, case-insensitive)", got[2].WordsStatus, "not set")
	}
	if got[3].WordsStatus != "" {
		t.Errorf("got[3].WordsStatus = %q, want %q (no env var name configured)", got[3].WordsStatus, "")
	}
}

func TestAnnotateGameEnv_NeverCarriesAValue(t *testing.T) {
	// GameEntry has no value field to leak -- this test documents the
	// contract via reflection over the JSON tags, so a future field
	// addition can't silently smuggle a secret value onto the wire.
	rt := reflect.TypeOf(GameEntry{})
	for i := 0; i < rt.NumField(); i++ {
		name := strings.ToLower(rt.Field(i).Name)
		if strings.Contains(name, "value") || strings.Contains(name, "secret") {
			t.Errorf("GameEntry has a suspicious field %q -- this type must carry NAMES and STATUSES only, never a value", rt.Field(i).Name)
		}
	}
}

func TestParseTelephonyGames_ShippedConfigMatchesTask2Entries(t *testing.T) {
	// Resolves apps/voice/configs/telephony.toml four directories up from
	// this package (kv/internal/app/studio -> kv/internal/app -> kv/internal
	// -> kv -> repo root) -- the guard that keeps this Go parser and the
	// Python loader's shipped-config fixture (quick task 260727-pdh Task 2)
	// from drifting apart. Skips (does not fail) if the file is absent --
	// this package must not assume it is checked out inside the full
	// klanker-voice monorepo.
	path := filepath.Join("..", "..", "..", "..", "apps", "voice", "configs", "telephony.toml")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("shipped apps/voice/configs/telephony.toml not found at %s: %v", path, err)
	}

	got, err := ParseTelephonyGames(path)
	if err != nil {
		t.Fatalf("ParseTelephonyGames(%s) error: %v", path, err)
	}
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3 (3234/3283/8283): %+v", len(got), got)
	}

	if len(got[0].DIDs) != 1 || got[0].DIDs[0] != "7254043234" || got[0].CodeEnvVar != "CTF_ANNOUNCEMENT_CODE_3234" || got[0].WordsEnvVar != "" {
		t.Errorf("got[0] (3234 game) = %+v, want dids=[7254043234] code=CTF_ANNOUNCEMENT_CODE_3234 words=\"\"", got[0])
	}
	if len(got[1].DIDs) != 1 || got[1].DIDs[0] != "7254043283" || got[1].CodeEnvVar != "CTF_ANNOUNCEMENT_CODE_3283" || got[1].WordsEnvVar != "" {
		t.Errorf("got[1] (3283 game) = %+v, want dids=[7254043283] code=CTF_ANNOUNCEMENT_CODE_3283 words=\"\"", got[1])
	}
	if len(got[2].DIDs) != 1 || got[2].DIDs[0] != "7254048283" || got[2].CodeEnvVar != "CTF_ANNOUNCEMENT_CODE_UCTF" || got[2].WordsEnvVar != "CTF_ANNOUNCEMENT_WORDS_UCTF" {
		t.Errorf("got[2] (8283 game) = %+v, want dids=[7254048283] code=CTF_ANNOUNCEMENT_CODE_UCTF words=CTF_ANNOUNCEMENT_WORDS_UCTF", got[2])
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
